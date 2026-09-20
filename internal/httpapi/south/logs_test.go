// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
)

func logRecord(id string) contract.LogRecord {
	return contract.LogRecord{
		RecordID:  id,
		SessionID: "sess-http",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Kind:      "command",
		Severity:  contract.SeverityInfo,
		Message:   "a command ran",
	}
}

// The contract's two status codes, and the distinction they draw: a batch is
// ACCEPTED FOR STORAGE and a priority record is DURABLE.
func TestTheTwoLogPathsAnswerTheContractsStatusCodes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	batch := h.post(t, contract.PathLogsBatch, contract.LogBatchRequest{
		Records: []contract.LogRecord{logRecord("http-1"), logRecord("http-2")},
	})
	if batch.Code != http.StatusAccepted {
		t.Fatalf("batch = %d, want 202: %s", batch.Code, batch.Body)
	}
	var accepted contract.LogBatchResponse
	if err := json.Unmarshal(batch.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if accepted.Accepted != 2 {
		t.Errorf("accepted = %d, want 2", accepted.Accepted)
	}

	priority := h.post(t, contract.PathLogsPriority, contract.LogPriorityRequest{
		Record: logRecord("http-priority"),
	})
	if priority.Code != http.StatusOK {
		t.Fatalf("priority = %d, want 200: %s", priority.Code, priority.Body)
	}
	var ack contract.LogPriorityResponse
	if err := json.Unmarshal(priority.Body.Bytes(), &ack); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !ack.Accepted {
		t.Error("accepted is false on a 200; the ack is what the proxy acts on")
	}
	if ack.ReceiptID != "http-priority" {
		t.Errorf("receipt = %q, want the record id", ack.ReceiptID)
	}
}

// The count is how a proxy draining its disk buffer sees de-duplication, and
// the contract says fewer than sent means duplicates.
func TestAReplayedBatchAnswersZeroAccepted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	body := contract.LogBatchRequest{Records: []contract.LogRecord{logRecord("replay-1"), logRecord("replay-2")}}
	if res := h.post(t, contract.PathLogsBatch, body); res.Code != http.StatusAccepted {
		t.Fatalf("first batch = %d: %s", res.Code, res.Body)
	}

	res := h.post(t, contract.PathLogsBatch, body)
	if res.Code != http.StatusAccepted {
		t.Fatalf("replay = %d, want 202: %s", res.Code, res.Body)
	}
	var got contract.LogBatchResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Accepted != 0 {
		t.Errorf("accepted = %d on a full replay, want 0", got.Accepted)
	}
}

// M11 binds hardest here. A record this server refuses is a CALLER MISTAKE and
// answering 401 would send an operator to debug credentials over a bad
// timestamp.
func TestAMalformedRecordIsA400AndNeverA401(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	cases := map[string]contract.LogRecord{
		"an unknown kind": func() contract.LogRecord { r := logRecord("bad-kind"); r.Kind = "exfiltration"; return r }(),
		"a bad timestamp": func() contract.LogRecord { r := logRecord("bad-time"); r.Timestamp = "soon"; return r }(),
		"no record id":    func() contract.LogRecord { r := logRecord(""); return r }(),
	}
	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				path string
				body any
			}{
				{contract.PathLogsBatch, contract.LogBatchRequest{Records: []contract.LogRecord{rec}}},
				{contract.PathLogsPriority, contract.LogPriorityRequest{Record: rec}},
			} {
				res := h.post(t, tc.path, tc.body)
				if res.Code != http.StatusBadRequest {
					t.Fatalf("%s = %d, want 400: %s", tc.path, res.Code, res.Body)
				}
				h.requireEnvelope(t, res)
				if code := envelopeCode(t, res.Body.Bytes()); code != contract.ErrCodeInvalidRequest {
					t.Errorf("%s answered code %q, want %q", tc.path, code, contract.ErrCodeInvalidRequest)
				}
			}
		})
	}
}

// An empty batch violates the contract's `minItems: 1`, so it is a malformed
// request rather than a successful ingest of nothing.
func TestAnEmptyBatchIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res := h.post(t, contract.PathLogsBatch, contract.LogBatchRequest{})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("empty batch = %d, want 400: %s", res.Code, res.Body)
	}
}

// M18: a record whose body names another tenant is refused, and the refusal is
// distinguishable from an ingest failure — a 400 saying what was claimed,
// never a 5xx and never a 401.
func TestARecordNamingAnotherTenantIsRefusedWithAReason(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rec := logRecord("cross-tenant")
	rec.Attributes = map[string]string{audit.AttrTenant: "someone-else"}

	res := h.post(t, contract.PathLogsPriority, contract.LogPriorityRequest{Record: rec})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", res.Code, res.Body)
	}
	h.requireEnvelope(t, res)
	if got := res.Body.String(); !containsAll(got, "someone-else", "enrolled") {
		t.Errorf("the refusal does not say what was claimed: %s", got)
	}

	// And it is in the log, at WARN, because it means two customers'
	// configurations have been crossed.
	if !containsAll(h.logs.String(), "audit_tenant_mismatch", "someone-else") {
		t.Error("a cross-tenant record was refused without a log line naming it")
	}
}

func envelopeCode(t *testing.T, body []byte) string {
	t.Helper()
	var env contract.ErrorResponse
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env.Error.Code
}

func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			return false
		}
	}
	return true
}
