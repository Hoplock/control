// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// What a session replay reads (0051, north-bound version 3): a session's
// records in order through the paged query, and each `stream` record's
// capture through `…/audit/records/{record}/capture`.

type capture struct {
	RecordID  string  `json:"record_id"`
	SessionID string  `json:"session_id"`
	Bytes     int     `json:"capture_bytes"`
	SHA256    string  `json:"capture_sha256"`
	Retained  bool    `json:"retained"`
	Payload   *string `json:"payload"`
}

// A capture is served as the bytes the proxy sent, beside the size and digest
// the record's hashed body carries; a record that carries no capture is a 404,
// and so is a record that does not exist.
func TestACaptureIsServedWithTheDigestItsRecordCarries(t *testing.T) {
	f := newServer(t)
	auditor := f.roleToken(t, identity.RoleAuditor)
	in, err := audit.New(audit.Options{Store: f.st})
	if err != nil {
		t.Fatal(err)
	}
	chunk := []byte("$ whoami\r\nops\r\n")
	if _, err := in.Ingest(t.Context(), audit.Submission{Tenant: "tenant-a", ProxyID: "proxy-a", Records: []contract.LogRecord{
		{
			RecordID: "rec-stream", SessionID: "sess-replay", Kind: "stream", Severity: contract.SeverityInfo,
			Timestamp: time.Now().UTC().Format(time.RFC3339), Payload: base64.StdEncoding.EncodeToString(chunk),
		},
		{
			RecordID: "rec-plain", SessionID: "sess-replay", Kind: "session_start", Severity: contract.SeverityInfo,
			Timestamp: time.Now().UTC().Format(time.RFC3339), Message: "session opened",
		},
	}}); err != nil {
		t.Fatal(err)
	}

	resp := f.send(t, "GET", api+"/audit/records/rec-stream/capture", auditor, nil)
	mustStatus(t, resp, http.StatusOK)
	var got capture
	decodeInto(t, resp, &got)
	stored, err := f.st.Audit().Get(t.Context(), "tenant-a", "rec-stream")
	if err != nil {
		t.Fatal(err)
	}
	if got.RecordID != "rec-stream" || got.SessionID != "sess-replay" || got.Retained || got.Payload == nil {
		t.Fatalf("capture = %+v", got)
	}
	raw, err := base64.StdEncoding.DecodeString(*got.Payload)
	if err != nil || string(raw) != string(chunk) {
		t.Errorf("payload decodes to %q (%v); want the bytes the proxy sent", raw, err)
	}
	if got.Bytes != len(chunk) || got.Bytes != int(stored.CaptureBytes) || got.SHA256 == "" || got.SHA256 != stored.CaptureSHA256 {
		t.Errorf("capture says %d bytes, %s; the record says %d, %s", got.Bytes, got.SHA256, stored.CaptureBytes, stored.CaptureSHA256)
	}

	for _, id := range []string{"rec-plain", "no-such-record"} {
		resp := f.send(t, "GET", api+"/audit/records/"+id+"/capture", auditor, nil)
		mustStatus(t, resp, http.StatusNotFound)
		if e := decodeError(t, resp); e.Code != north.CodeNotFound {
			t.Errorf("%s: %+v", id, e)
		}
	}
}

// A capture a retention pass emptied is a different answer from one never
// taken: the record still describes it, and the read says it is gone.
func TestARetainedCaptureSaysItIsGone(t *testing.T) {
	f := newServer(t)
	admin, auditor := f.roleToken(t, identity.RoleAdmin), f.roleToken(t, identity.RoleAuditor)
	in, err := audit.New(audit.Options{Store: f.st})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Ingest(t.Context(), audit.Submission{Tenant: "tenant-a", ProxyID: "proxy-a", Records: []contract.LogRecord{{
		RecordID: "rec-old", SessionID: "sess-old", Kind: "stream", Severity: contract.SeverityInfo,
		Timestamp: time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
		Payload:   base64.StdEncoding.EncodeToString([]byte("ls\r\n")),
	}}}); err != nil {
		t.Fatal(err)
	}
	resp := f.send(t, "POST", api+"/audit/captures/retention", admin, map[string]any{"before": time.Now().Add(-24 * time.Hour)})
	mustStatus(t, resp, http.StatusOK)

	resp = f.send(t, "GET", api+"/audit/records/rec-old/capture", auditor, nil)
	mustStatus(t, resp, http.StatusOK)
	var got capture
	decodeInto(t, resp, &got)
	if !got.Retained || got.Payload != nil || got.Bytes != 4 || got.SHA256 == "" {
		t.Errorf("a retained capture answered %+v; want retained, no payload, and the record's size and digest", got)
	}
}

// `?principal=` is what one actor did here (M19): the records naming it as
// the actor, every kind of act, and nobody else's. Its cursor binds it, so a
// page of one actor's records never continues another's.
func TestThePrincipalFilterAnswersWhatOneActorDid(t *testing.T) {
	f := newServer(t)
	auditor := f.roleToken(t, identity.RoleAuditor)
	sup, supID := supervisorWith(t, func() (identity.Credential, *identity.Principal, error) {
		return f.federation.IssueToken(t.Context(), "tenant-a", identity.TokenRequest{
			DisplayName: "msp", Supervisor: true,
			Scopes: map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin}},
		})
	})
	other, _ := f.tokenFor(t, identity.RoleAdmin)
	for range 3 {
		mustStatus(t, f.send(t, "POST", api+"/revocations", sup, map[string]any{"type": "resync"}), http.StatusOK)
		mustStatus(t, f.send(t, "POST", api+"/revocations", other, map[string]any{"type": "resync"}), http.StatusOK)
	}

	var seen []string
	after := ""
	for page := 0; ; page++ {
		q := url.Values{"principal": {supID}, "limit": {"2"}}
		if after != "" {
			q.Set("after", after)
		}
		resp := f.send(t, "GET", api+"/audit/records?"+q.Encode(), auditor, nil)
		mustStatus(t, resp, http.StatusOK)
		var body struct {
			Records []struct {
				RecordID   string            `json:"record_id"`
				Attributes map[string]string `json:"attributes"`
			} `json:"records"`
			Truncated bool   `json:"truncated"`
			Next      string `json:"next"`
		}
		decodeInto(t, resp, &body)
		for _, r := range body.Records {
			if r.Attributes[audit.AttrPrincipalID] != supID {
				t.Errorf("record %s names %q; the filter asked for %s", r.RecordID, r.Attributes[audit.AttrPrincipalID], supID)
			}
			seen = append(seen, r.RecordID)
		}
		if !body.Truncated {
			break
		}
		if page > 5 {
			t.Fatal("the pages do not end")
		}
		after = body.Next
		// The cursor is bound to the principal: presented without it, it
		// is refused rather than read as a position in the whole tenant.
		resp = f.send(t, "GET", api+"/audit/records?"+url.Values{"limit": {"2"}, "after": {after}}.Encode(), auditor, nil)
		mustStatus(t, resp, http.StatusBadRequest)
	}
	if len(seen) != 3 {
		t.Errorf("the supervisor's records: %d, want its 3 acts", len(seen))
	}
}
