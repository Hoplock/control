// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// What this listener serves is the AUDIT STORE, so the cases that matter most
// are the ones about who may reach it and what it will not do.

const auditTenant = store.Tenant("default")

func newAuditReadHarness(t *testing.T) (http.Handler, *audit.Ingester, *store.Store) {
	t.Helper()

	st := storetest.New(t)
	in, err := audit.New(audit.Options{Store: st})
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	srv, err := newAuditReadServer(audit.NewReader(st), auditTenant, "read-token")
	if err != nil {
		t.Fatalf("newAuditReadServer: %v", err)
	}
	return srv.handler(), in, st
}

func ingestOne(t *testing.T, in *audit.Ingester, id string) {
	t.Helper()
	if _, err := in.Ingest(t.Context(), audit.Submission{
		Tenant:  auditTenant,
		ProxyID: "proxy-1",
		Records: []contract.LogRecord{{
			RecordID:  id,
			SessionID: "sess-read",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Kind:      "command",
			Severity:  contract.SeverityInfo,
			Message:   "a command ran",
		}},
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
}

// An unauthenticated read of the audit store is a published audit store. The
// constructor refuses to build without a credential even though config already
// refuses the pairing, because this constructor is what a test reaches for and
// the property must not be reachable by forgetting an argument.
func TestTheAuditReadListenerRefusesToBindWithoutACredential(t *testing.T) {
	t.Parallel()

	if _, err := newAuditReadServer(nil, auditTenant, ""); err == nil {
		t.Fatal("a read listener was built with no token")
	}
}

func TestTheAuditReadListenerRequiresItsOwnCredential(t *testing.T) {
	t.Parallel()
	h, in, _ := newAuditReadHarness(t)
	ingestOne(t, in, "rec-auth")

	for name, header := range map[string]string{
		"no header":     "",
		"the wrong one": "Bearer nearly-the-read-token",
		"not a bearer":  "read-token",
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/debug/logs/rec-auth", nil)
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)

			if res.Code != http.StatusUnauthorized {
				t.Fatalf("= %d, want 401", res.Code)
			}
			if strings.Contains(res.Body.String(), "a command ran") {
				t.Error("a refused read returned the record anyway")
			}
		})
	}
}

// The path the conformance suite's durability assertion goes through: a record
// named by its client-assigned id, readable the instant it was acked.
func TestARecordIsReadableByItsID(t *testing.T) {
	t.Parallel()
	h, in, _ := newAuditReadHarness(t)
	ingestOne(t, in, "rec-by-id")

	res := auditGet(t, h, "/debug/logs/rec-by-id")
	if res.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", res.Code, res.Body)
	}

	var got auditRecordView
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.RecordID != "rec-by-id" {
		t.Errorf("record_id = %q", got.RecordID)
	}
	if got.Hash == "" || got.ChainSeq == 0 || got.Body == "" {
		t.Errorf("the view omits the chain fields: %+v", got)
	}
	// A reader who holds the chain can check the record without asking this
	// server to vouch for it, which is the point of carrying them.
	if want := audit.Link(string(auditTenant), got.Stream, got.ChainSeq, got.PrevHash, got.Body); want != got.Hash {
		t.Errorf("the served record does not re-link: want %s, got %s", want, got.Hash)
	}
}

func TestAnUnknownRecordIsA404(t *testing.T) {
	t.Parallel()
	h, _, _ := newAuditReadHarness(t)

	if res := auditGet(t, h, "/debug/logs/nope"); res.Code != http.StatusNotFound {
		t.Fatalf("= %d, want 404: %s", res.Code, res.Body)
	}
}

// It ONLY READS. The store is append-only and a path that appeared to offer
// anything else would be lying about it.
func TestTheAuditReadListenerServesNothingElse(t *testing.T) {
	t.Parallel()
	h, in, _ := newAuditReadHarness(t)
	ingestOne(t, in, "rec-readonly")

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/debug/logs/rec-readonly", nil)
		req.Header.Set("Authorization", "Bearer read-token")
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404: this listener reads and nothing else", method, res.Code)
		}
	}

	for _, path := range []string{"/debug/revoke", "/v1/authorize", "/metrics", "/"} {
		if res := auditGet(t, h, path); res.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, res.Code)
		}
	}
}

func TestASessionReadsBackInOrder(t *testing.T) {
	t.Parallel()
	h, in, _ := newAuditReadHarness(t)
	for _, id := range []string{"rec-s1", "rec-s2", "rec-s3"} {
		ingestOne(t, in, id)
	}

	res := auditGet(t, h, "/debug/logs?session_id=sess-read")
	if res.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", res.Code, res.Body)
	}
	var got struct {
		Records []auditRecordView `json:"records"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Records) != 3 {
		t.Fatalf("got %d records, want 3", len(got.Records))
	}
	for i := 1; i < len(got.Records); i++ {
		if got.Records[i].ChainSeq < got.Records[i-1].ChainSeq {
			t.Fatalf("records came back out of order: %d then %d",
				got.Records[i-1].ChainSeq, got.Records[i].ChainSeq)
		}
	}
}

func auditGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer read-token")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}
