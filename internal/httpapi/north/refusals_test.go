// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The handler fixes of 0023, each under a test that failed before it.

// requireRefusal reads a refusal and checks its status, code and field.
func requireRefusal(t *testing.T, resp *http.Response, status int, code, field string) north.Error {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("answered %d, want %d: %+v", resp.StatusCode, status, decodeError(t, resp))
	}
	e := decodeError(t, resp)
	if e.Code != code {
		t.Errorf("code %q, want %q", e.Code, code)
	}
	if field != "" && e.Parameters["field"] != field {
		t.Errorf("field %v, want %q", e.Parameters["field"], field)
	}
	return e
}

// A relabel simulates before it writes, and the simulation can take a while.
// What is written in the meantime is kept: the write re-reads each target
// under the tenant's target lock and changes only its labels, and its record's
// before is what it replaced, not what the preview read.
func TestARelabelKeepsWhatWasWrittenWhileItSimulated(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	ctx := t.Context()
	mustStatus(t, f.send(t, "PUT", api+"/targets/t-race", admin, map[string]any{
		"hostname": "race.example.com", "zone": "a", "labels": map[string]string{"env": "prod"},
	}), http.StatusOK)

	// An operator's edit holds the lock and the row while the relabel runs.
	held, release := make(chan struct{}), make(chan struct{})
	edited := make(chan error, 1)
	go func() {
		edited <- f.st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
			if err := tx.Targets().Lock(ctx, "tenant-a"); err != nil {
				return err
			}
			if err := tx.Targets().Upsert(ctx, "tenant-a", store.Target{
				ID: "t-race", Hostname: "race.example.com", Zone: "b", Labels: map[string]string{"env": "stage"},
			}); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	relabelled := make(chan *http.Response, 1)
	go func() {
		relabelled <- f.send(t, "POST", api+"/targets/relabel", admin, map[string]any{
			"apply": true, "changes": []any{map[string]any{"target": "t-race", "labels": map[string]string{"env": "dev"}}},
		})
	}()
	time.Sleep(200 * time.Millisecond)
	close(release)
	if err := <-edited; err != nil {
		t.Fatal(err)
	}
	mustStatus(t, <-relabelled, http.StatusOK)

	got, err := f.st.Targets().Get(ctx, "tenant-a", "t-race")
	if err != nil {
		t.Fatal(err)
	}
	if got.Zone != "b" || got.Labels["env"] != "dev" {
		t.Errorf("after the relabel the target is zone %q labels %v: the edit's zone was lost, or the relabel's labels", got.Zone, got.Labels)
	}
	var before string
	for _, rec := range f.operatorRecords(t, audit.EventTargetPut) {
		if rec.Attributes[audit.AttrOperatorPrefix+"relabel"] == "true" {
			before = rec.Attributes[audit.AttrBefore]
			break
		}
	}
	if before != "env=stage" {
		t.Errorf("the relabel's record says it replaced %q, want the edit's env=stage", before)
	}
}

// A relabel names the change it refuses: a change with no target, a target
// changed twice, and a target that is not there, among many.
func TestARelabelNamesTheChangeItRefuses(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	mustStatus(t, f.send(t, "PUT", api+"/targets/t-1", admin, map[string]any{"hostname": "one.example.com"}), http.StatusOK)
	relabel := func(changes ...map[string]any) *http.Response {
		return f.send(t, "POST", api+"/targets/relabel", admin, map[string]any{"changes": changes, "apply": true})
	}
	requireRefusal(t, relabel(map[string]any{"target": "t-1"}, map[string]any{"target": " "}),
		http.StatusBadRequest, north.CodeInvalidRequest, "changes[1].target")
	requireRefusal(t, relabel(map[string]any{"target": "t-1"}, map[string]any{"target": "t-1"}),
		http.StatusBadRequest, north.CodeInvalidRequest, "changes[1].target")
	e := requireRefusal(t, relabel(map[string]any{"target": "t-1"}, map[string]any{"target": "t-missing"}),
		http.StatusNotFound, north.CodeNotFound, "")
	if e.Parameters["target"] != "t-missing" {
		t.Errorf("the 404 names %v, want t-missing", e.Parameters["target"])
	}
}

// Each revocation refusal names the field to fix, in this body's terms, and
// quotes no package's own sentence. A host-key withdrawal with a blank key is
// a request to fix, not "no such record".
func TestARevocationRefusalNamesItsField(t *testing.T) {
	f := newServer(t)
	desk := f.roleToken(t, identity.RoleGrantAdmin)
	for name, c := range map[string]struct {
		body  map[string]any
		field string
	}{
		"a kill selecting nothing":        {map[string]any{"type": "session_kill", "reason": "r"}, "session_ids"},
		"a kill selecting two":            {map[string]any{"type": "session_kill", "all": true, "subject": "a", "reason": "r"}, "session_ids"},
		"a kill with no reason":           {map[string]any{"type": "session_kill", "all": true}, "reason"},
		"an invalidation selecting two":   {map[string]any{"type": "cache_invalidate", "all": true, "keys": []string{"k"}}, "keys"},
		"a withdrawal with no target":     {map[string]any{"type": "host_key_withdraw", "host_key": map[string]any{"fingerprint": "SHA256:x"}}, "host_key.target"},
		"a withdrawal with no key":        {map[string]any{"type": "host_key_withdraw", "host_key": map[string]any{"target": "db"}}, "host_key.fingerprint"},
		"an audience naming no one":       {map[string]any{"type": "resync", "proxy_ids": []string{""}}, "proxy_ids"},
		"a withdrawal naming no host key": {map[string]any{"type": "host_key_withdraw"}, "host_key"},
	} {
		t.Run(name, func(t *testing.T) {
			e := requireRefusal(t, f.send(t, "POST", api+"/revocations", desk, c.body),
				http.StatusBadRequest, north.CodeInvalidRequest, c.field)
			if strings.Contains(e.Message, "revoke:") {
				t.Errorf("the message quotes the package: %q", e.Message)
			}
		})
	}
}

// A refusal the store makes is answered in the store's own sentence, never
// with the Go operation that refused it (carried from 0021), and a pass
// through a negative sequence is refused naming it.
func TestARetentionRefusalNamesNoGoFunction(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	in, err := audit.New(audit.Options{Store: f.st})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Ingest(t.Context(), audit.Submission{Tenant: "tenant-a", ProxyID: "proxy-h", Records: []contract.LogRecord{{
		RecordID: "head-1", SessionID: "sess-h", Kind: "session_start", Severity: contract.SeverityInfo,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}}}); err != nil {
		t.Fatal(err)
	}
	e := requireRefusal(t, f.send(t, "POST", api+"/audit/retention", admin, map[string]any{
		"stream": audit.StreamFor("proxy-h"), "through_seq": 1,
	}), http.StatusBadRequest, north.CodeInvalidRequest, "query")
	if strings.Contains(e.Message, "store.") || !strings.Contains(e.Message, "the head is never retained") {
		t.Errorf("the refusal reads %q: it names a Go operation, or not why", e.Message)
	}
	requireRefusal(t, f.send(t, "POST", api+"/audit/retention", admin, map[string]any{
		"stream": audit.StreamFor("proxy-h"), "through_seq": -1, "before": time.Now(),
	}), http.StatusBadRequest, north.CodeInvalidRequest, "through_seq")
}

// A fleet configuration refusal names the key in a sentence of this surface's,
// never the fleet package's.
func TestAConfigurationRefusalQuotesNoPackage(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	for _, settings := range []map[string]any{{"listeners.south": ":1"}, {"logging.batch_size": "many"}} {
		resp := f.send(t, "PUT", api+"/fleet/config/zone/edge", admin, map[string]any{"settings": settings})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("answered %d", resp.StatusCode)
		}
		if e := decodeError(t, resp); strings.HasPrefix(e.Message, "fleet:") || e.Parameters["key"] == nil {
			t.Errorf("refusal = %+v", e)
		}
	}
}

// A body past the listener's limit is 413 payload_too_large naming the limit,
// not "the body is invalid", which sends an author looking for a mistake.
func TestABodyPastTheLimitIsTooLargeNotInvalid(t *testing.T) {
	f := newServer(t)
	author := f.roleToken(t, identity.RolePolicyAuthor)
	big := strings.Repeat("#", int(north.DefaultMaxBodyBytes)+1)
	e := requireRefusal(t, f.send(t, "POST", api+"/policy/validate", author, map[string]any{"source": big}),
		http.StatusRequestEntityTooLarge, north.CodePayloadTooLarge, "")
	if limit, _ := e.Parameters["limit"].(float64); int64(limit) != north.DefaultMaxBodyBytes {
		t.Errorf("the refusal names limit %v, want %d", e.Parameters["limit"], north.DefaultMaxBodyBytes)
	}
}

// A claim stored on a subject is a policy attribute on every decision for it,
// so it is held to the claim mapping's rule: a name this server sets for
// itself — chain_hop_proxy_id, which says which proxy's key authenticated a
// chain leg — is refused here as it is there, and nothing is stored.
func TestASubjectCannotBeGivenAClaimThisServerSets(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	for name, c := range map[string]struct{ claim, problem string }{
		"the chain hop":         {identity.ClaimChainHop, identity.AttributeReserved},
		"the reserved prefix":   {"hoplock.anything", identity.AttributeReserved},
		"not an attribute name": {"Not An Attribute", identity.AttributeInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			e := requireRefusal(t, f.send(t, "PUT", api+"/subjects/mallory@example.com", admin, map[string]any{
				"claims": map[string]string{c.claim: "proxy-x"},
			}), http.StatusBadRequest, north.CodeInvalidRequest, "claims."+c.claim)
			if e.Parameters["problem"] != c.problem {
				t.Errorf("problem %v, want %q", e.Parameters["problem"], c.problem)
			}
			if _, err := f.st.Subjects().Get(t.Context(), "tenant-a", "mallory@example.com"); !store.IsNotFound(err) {
				t.Errorf("the refused subject was stored: %v", err)
			}
		})
	}
	mustStatus(t, f.send(t, "PUT", api+"/subjects/mallory@example.com", admin, map[string]any{
		"claims": map[string]string{"department": "finance"},
	}), http.StatusOK)
}

// The runbook's third step says when its limit cut the answer short, because
// the fourth ends exactly the sessions it lists; and each question is asked
// whole, an axis with its value.
func TestTheNegotiatedQuerySaysWhenItStoppedShort(t *testing.T) {
	f := newServer(t)
	auditor := f.roleToken(t, identity.RoleAuditor)
	in, err := audit.New(audit.Options{Store: f.st})
	if err != nil {
		t.Fatal(err)
	}
	var recs []contract.LogRecord
	for _, session := range []string{"sess-1", "sess-2", "sess-3"} {
		recs = append(recs, contract.LogRecord{
			RecordID: "neg-" + session, SessionID: session, Kind: "provisioning", Severity: contract.SeverityInfo,
			Timestamp: time.Now().UTC().Format(time.RFC3339), Message: "negotiated",
			Attributes: map[string]string{
				audit.AttrEvent: audit.EventAlgorithmsNegotiated, audit.AttrTargetKexAlgorithm: "diffie-hellman-group14-sha1",
			},
		})
	}
	if _, err := in.Ingest(t.Context(), audit.Submission{Tenant: "tenant-a", ProxyID: "proxy-n", Records: recs}); err != nil {
		t.Fatal(err)
	}
	ask := func(limit string) (sessions []string, truncated bool) {
		t.Helper()
		resp := f.send(t, "GET", api+"/audit/negotiated?axis=key_exchanges&value=diffie-hellman-group14-sha1"+limit, auditor, nil)
		mustStatus(t, resp, http.StatusOK)
		var got struct {
			Sessions  []string `json:"sessions"`
			Truncated *bool    `json:"truncated"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Truncated == nil {
			t.Fatal("the answer carries no truncated")
		}
		return got.Sessions, *got.Truncated
	}
	if sessions, truncated := ask("&limit=2"); len(sessions) != 2 || !truncated {
		t.Errorf("limit 2 of 3: %v, truncated %v", sessions, truncated)
	}
	if sessions, truncated := ask("&limit=3"); len(sessions) != 3 || truncated {
		t.Errorf("limit 3 of 3: %v, truncated %v", sessions, truncated)
	}
	if sessions, truncated := ask(""); !slices.Equal(slices.Sorted(slices.Values(sessions)), []string{"sess-1", "sess-2", "sess-3"}) || truncated {
		t.Errorf("no limit: %v, truncated %v", sessions, truncated)
	}
	requireRefusal(t, f.send(t, "GET", api+"/audit/negotiated?axis=key_exchanges", auditor, nil),
		http.StatusBadRequest, north.CodeInvalidRequest, "value")
	requireRefusal(t, f.send(t, "GET", api+"/audit/negotiated?ban_name=aes128-cbc", auditor, nil),
		http.StatusBadRequest, north.CodeInvalidRequest, "ban_axis")
}

// An id from another tenant is never found here, whichever route reads it: a
// decision, a session, a record, a certificate serial, a target, a subject or
// a policy version stored in tenant-b reads as absent (or as nothing) to an
// administrator of tenant-a, because every read names the tenant the
// middleware resolved and nothing the request carries (M18).
func TestAnIDFromAnotherTenantIsNotFound(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	ctx := t.Context()
	const other = store.Tenant("tenant-b")
	if err := f.st.Decisions().Insert(ctx, other, store.Decision{
		ID: "dec-b", SessionID: "sess-b", Effect: "deny", InputsDigest: "x",
		Inputs: json.RawMessage(`{}`), Explanation: json.RawMessage(`{}`), DecidedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	in, err := audit.New(audit.Options{Store: f.st})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Ingest(ctx, audit.Submission{Tenant: other, ProxyID: "proxy-b", Records: []contract.LogRecord{{
		RecordID: "rec-b", SessionID: "sess-b", Kind: "provisioning", Severity: contract.SeverityInfo,
		Timestamp: time.Now().UTC().Format(time.RFC3339), Message: "provisioned",
		Attributes: map[string]string{audit.AttrCertificateSerial: "4242"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Targets().Upsert(ctx, other, store.Target{ID: "t-b", Hostname: "b.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Subjects().Upsert(ctx, other, store.Subject{ID: "sub-b", Source: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.PolicyBundles().Insert(ctx, other, store.PolicyBundle{Version: 1, Source: []byte("x"), Hash: "sha256:b"}); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/decisions/dec-b", "/sessions/sess-b/decisions", "/audit/records/rec-b",
		"/targets/t-b", "/subjects/sub-b", "/policy/bundles/1",
	} {
		if resp := f.send(t, "GET", api+path, admin, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s answered %d for tenant-b's id, want 404", path, resp.StatusCode)
		}
	}
	for _, c := range []struct{ path, list string }{
		{"/audit/certificates/4242/records", "records"},
		{"/audit/sessions/sess-b/certificates", "certificates"},
		{"/audit/records?session=sess-b", "records"},
	} {
		resp := f.send(t, "GET", api+c.path, admin, nil)
		mustStatus(t, resp, http.StatusOK)
		var got map[string]json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		var list []json.RawMessage
		if err := json.Unmarshal(got[c.list], &list); err != nil {
			t.Fatalf("GET %s: %s is not a list: %v", c.path, c.list, err)
		}
		if len(list) != 0 {
			t.Errorf("GET %s returned tenant-b's %s: %d", c.path, c.list, len(list))
		}
	}
}
