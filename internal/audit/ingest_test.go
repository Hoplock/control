// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

const (
	tenantA = store.Tenant("acme")
	tenantB = store.Tenant("globex")
	proxyA  = "proxy-1"
)

func newIngester(t *testing.T, st *store.Store) *audit.Ingester {
	t.Helper()
	in, err := audit.New(audit.Options{Store: st})
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	return in
}

func record(id, session string) contract.LogRecord {
	return contract.LogRecord{
		RecordID:  id,
		SessionID: session,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Kind:      "command",
		Severity:  contract.SeverityInfo,
		Message:   "a command ran",
		Subject:   "alice@example.invalid",
		Target:    "db-1.example.invalid",
	}
}

func records(n int, session string) []contract.LogRecord {
	out := make([]contract.LogRecord, n)
	for i := range out {
		out[i] = record(fmt.Sprintf("%s-rec-%d", session, i), session)
	}
	return out
}

// M8: ingest is idempotent on the client-assigned record id, and the count is
// how a proxy draining its disk buffer sees the de-duplication.
func TestAReplayedBatchIsNotDoubleCounted(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()

	batch := records(5, "sess-replay")
	sub := audit.Submission{Tenant: tenantA, ProxyID: proxyA, Records: batch}

	first, err := in.Ingest(ctx, sub)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if first.Stored != 5 {
		t.Fatalf("first ingest stored %d, want 5", first.Stored)
	}

	second, err := in.Ingest(ctx, sub)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.Stored != 0 || second.Duplicates != 5 {
		t.Fatalf("replay stored %d (%d duplicates), want 0 stored and 5 duplicates",
			second.Stored, second.Duplicates)
	}

	// And the chain did not move: a replay that re-chained would leave
	// five records nobody sent between the ones somebody did.
	head, err := st.Audit().ChainHead(ctx, tenantA, audit.StreamFor(proxyA))
	if err != nil {
		t.Fatalf("ChainHead: %v", err)
	}
	if head.ChainSeq != 5 {
		t.Errorf("chain head at %d, want 5", head.ChainSeq)
	}
}

// The acceptance criterion says PARALLEL writers, not a sequential resend: a
// read-then-write in Go passes the sequential version and loses the race under
// concurrency, which is exactly why the constraint is the database's.
func TestConcurrentDuplicateSubmissionsStoreExactlyOneRow(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()

	const writers = 8
	batch := records(3, "sess-race")

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		total  int
		failed []error
	)
	start := make(chan struct{})
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rcpt, err := in.Ingest(ctx, audit.Submission{
				Tenant: tenantA, ProxyID: proxyA, Records: batch,
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, err)
				return
			}
			total += rcpt.Stored
		}()
	}
	close(start)
	wg.Wait()

	if len(failed) > 0 {
		t.Fatalf("%d of %d concurrent writers failed: %v", len(failed), writers, failed[0])
	}
	if total != len(batch) {
		t.Errorf("the writers between them stored %d rows, want %d: exactly one writer may win each record",
			total, len(batch))
	}

	chain, err := st.Audit().Chain(ctx, tenantA, audit.StreamFor(proxyA), 0, 0)
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if len(chain) != len(batch) {
		t.Fatalf("the store holds %d rows, want %d", len(chain), len(batch))
	}
	for i, rec := range chain {
		if want := int64(i + 1); rec.ChainSeq != want {
			t.Errorf("row %d is at sequence %d, want %d: concurrent writers left a gap", i, rec.ChainSeq, want)
		}
	}
}

// PLAN §4: the priority ack means DURABLE. Acking before the write lands turns
// the guarantee the proxy acts on into a lie that only shows up after an
// incident, so the record must be readable the instant the call returns.
func TestAPriorityRecordIsReadableTheInstantItIsAcked(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	rec := record("rec-priority", "sess-priority")
	rec.Severity = contract.SeverityCritical

	resp, err := in.IngestPriority(ctx, tenantA, proxyA, &contract.LogPriorityRequest{Record: rec})
	if err != nil {
		t.Fatalf("IngestPriority: %v", err)
	}
	if !resp.Accepted {
		t.Fatal("accepted is false on a successful priority ingest; the ack is what the proxy acts on")
	}

	got, err := reader.Get(ctx, tenantA, rec.RecordID)
	if err != nil {
		t.Fatalf("reading back straight after the ack: %v", err)
	}
	if got.RecordID != rec.RecordID {
		t.Errorf("read back %q, want %q", got.RecordID, rec.RecordID)
	}

	// A resend of a record already stored is still `accepted: true`. The
	// proxy asked whether the event is recorded, and it is; answering
	// false would make a retry after a lost response look like a security
	// event that went unrecorded.
	again, err := in.IngestPriority(ctx, tenantA, proxyA, &contract.LogPriorityRequest{Record: rec})
	if err != nil {
		t.Fatalf("resending a priority record: %v", err)
	}
	if !again.Accepted {
		t.Error("a resent priority record was not acked; the record is durable either way")
	}
}

// A batch carrying one bad record fails whole. The contract documents
// `accepted` as "fewer than sent means the rest were duplicates; the proxy may
// drop them", so a shortfall caused by anything else would tell the proxy to
// discard records this server never stored.
func TestAPartiallyValidBatchStoresNothing(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()

	batch := records(3, "sess-partial")
	batch[1].Kind = "exfiltration"

	_, err := in.Ingest(ctx, audit.Submission{Tenant: tenantA, ProxyID: proxyA, Records: batch})
	var malformed *audit.MalformedError
	if !errors.As(err, &malformed) {
		t.Fatalf("Ingest: %v, want a MalformedError", err)
	}
	if malformed.Index != 1 {
		t.Errorf("index = %d, want 1", malformed.Index)
	}

	for _, rec := range batch {
		if _, err := st.Audit().Get(ctx, tenantA, rec.RecordID); !store.IsNotFound(err) {
			t.Errorf("record %s was stored from a batch that failed: %v", rec.RecordID, err)
		}
	}
}

// PLAN §7, and against what is actually on disk rather than against the Go
// value: "the proxy promises not to send it" is not a control on this side.
func TestAPasswordIsNeverOnDisk(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()

	rec := record("rec-password", "sess-password")
	rec.Kind = "auth"
	rec.Attributes = map[string]string{
		"password":    "hunter2-correct-horse",
		"auth_method": "password",
	}

	if _, err := in.Ingest(ctx, audit.Submission{
		Tenant: tenantA, ProxyID: proxyA, Records: []contract.LogRecord{rec},
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// Read the raw columns, not the repository: a redaction that happened
	// on the way out rather than on the way in would pass every other
	// assertion in this file.
	var body, attrs string
	err := st.Pool().QueryRow(ctx,
		`SELECT body, attributes::text FROM audit_records WHERE tenant = $1 AND record_id = $2`,
		tenantA, rec.RecordID).Scan(&body, &attrs)
	if err != nil {
		t.Fatalf("raw read: %v", err)
	}
	for name, column := range map[string]string{"body": body, "attributes": attrs} {
		if strings.Contains(column, "hunter2") {
			t.Errorf("the password is on disk in %s: %s", name, column)
		}
		if !strings.Contains(column, audit.RedactedValue) {
			t.Errorf("%s does not record the redaction: %s", name, column)
		}
	}
}

// M8: a `stream` record's capture is stored so a session can be replayed, and
// it lives in its own table so no query over the records drags it along.
func TestASessionCaptureIsStoredAndReplayable(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	captured := []byte("\x1b[2Jwhoami\r\nroot\r\n")
	rec := record("rec-capture", "sess-capture")
	rec.Kind = "stream"
	rec.Payload = base64.StdEncoding.EncodeToString(captured)

	if _, err := in.Ingest(ctx, audit.Submission{
		Tenant: tenantA, ProxyID: proxyA, Records: []contract.LogRecord{rec},
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	got, err := reader.Capture(ctx, tenantA, rec.RecordID)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if string(got) != string(captured) {
		t.Errorf("capture = %q, want %q", got, captured)
	}

	row, err := reader.Get(ctx, tenantA, rec.RecordID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.CaptureBytes != int32(len(captured)) {
		t.Errorf("capture_bytes = %d, want %d", row.CaptureBytes, len(captured))
	}
	if row.CaptureSHA256 == "" {
		t.Error("the record carries no capture digest, so the chain does not cover the captured bytes")
	}

	// The digest is inside the hashed body, which is what makes an altered
	// capture detectable without the chain carrying a megabyte of terminal
	// output.
	if !strings.Contains(row.Body, row.CaptureSHA256) {
		t.Error("the capture digest is not inside the hashed body")
	}
}

// M18: the chain is per tenant, so two customers ingesting at the same time
// produce two chains that each verify alone — and one customer's deletion must
// not be the other's problem.
func TestTwoTenantsProduceTwoIndependentlyVerifiableChains(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	verifier := audit.NewVerifier(st)
	ctx := t.Context()

	var wg sync.WaitGroup
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 10 {
				batch := records(3, fmt.Sprintf("%s-sess-%d", tenant, i))
				if _, err := in.Ingest(ctx, audit.Submission{
					Tenant: tenant, ProxyID: proxyA, Records: batch,
				}); err != nil {
					t.Errorf("%s ingest: %v", tenant, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		result, err := verifier.VerifyTenant(ctx, tenant)
		if err != nil {
			t.Fatalf("%s VerifyTenant: %v", tenant, err)
		}
		if !result.OK() {
			t.Fatalf("%s chain does not verify: %s", tenant, result.Streams[0].Break)
		}
		if result.Tenant != string(tenant) {
			t.Errorf("the result names tenant %q, want %q: a verification that cannot say what it checked is not one a customer can use",
				result.Tenant, tenant)
		}
		if got := result.Records(); got != 30 {
			t.Errorf("%s walked %d records, want 30", tenant, got)
		}
	}

	// Delete one of A's records directly, the way somebody with database
	// access would.
	if _, err := st.Pool().Exec(ctx,
		`DELETE FROM audit_records WHERE tenant = $1 AND chain_seq = 5`, tenantA); err != nil {
		t.Fatalf("delete: %v", err)
	}

	broken, err := verifier.VerifyTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("VerifyTenant after the deletion: %v", err)
	}
	if broken.OK() {
		t.Fatal("a deleted record left the chain verifying")
	}
	b := broken.Streams[0].Break
	if b.Kind != audit.BreakGap || b.ExpectedSeq != 5 {
		t.Errorf("break = %+v, want a gap at sequence 5", b)
	}

	stillFine, err := verifier.VerifyTenant(ctx, tenantB)
	if err != nil {
		t.Fatalf("VerifyTenant(B): %v", err)
	}
	if !stillFine.OK() {
		t.Errorf("one tenant's deletion broke another tenant's chain: %s", stillFine.Streams[0].Break)
	}
}

// The other half of tamper evidence: an ALTERED row, rather than a removed
// one, and the break must name the row that was edited.
func TestAnAlteredRowBreaksTheChainAtThatRow(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	verifier := audit.NewVerifier(st)
	ctx := t.Context()

	if _, err := in.Ingest(ctx, audit.Submission{
		Tenant: tenantA, ProxyID: proxyA, Records: records(6, "sess-alter"),
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// Rewrite the message inside the hashed body of the fourth record —
	// the edit somebody covering their tracks would make.
	if _, err := st.Pool().Exec(ctx,
		`UPDATE audit_records SET body = replace(body, 'a command ran', 'nothing happened')
		 WHERE tenant = $1 AND chain_seq = 4`, tenantA); err != nil {
		t.Fatalf("update: %v", err)
	}

	result, err := verifier.VerifyTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("VerifyTenant: %v", err)
	}
	if result.OK() {
		t.Fatal("an altered record left the chain verifying")
	}
	b := result.Streams[0].Break
	if b.Kind != audit.BreakHashMismatch {
		t.Errorf("break kind = %q, want %q", b.Kind, audit.BreakHashMismatch)
	}
	if b.ChainSeq != 4 {
		t.Errorf("break at sequence %d, want 4: a verifier that cannot say WHICH record is not investigable", b.ChainSeq)
	}
	if b.RecordID == "" || b.Want == "" || b.Got == "" {
		t.Errorf("break = %+v, want the record id and both hashes so somebody can go to the row", b)
	}
	if result.Streams[0].Records != 3 {
		t.Errorf("walked %d records before stopping, want 3: the verifier stops at the FIRST break", result.Streams[0].Records)
	}
}

// And the third shape: a row whose hash was recomputed but whose predecessor
// link was not, which is what a partial rewrite leaves behind.
func TestABrokenPredecessorLinkIsReportedAsOne(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	verifier := audit.NewVerifier(st)
	ctx := t.Context()

	if _, err := in.Ingest(ctx, audit.Submission{
		Tenant: tenantA, ProxyID: proxyA, Records: records(4, "sess-link"),
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if _, err := st.Pool().Exec(ctx,
		`UPDATE audit_records SET prev_hash = 'sha256:0000' WHERE tenant = $1 AND chain_seq = 3`,
		tenantA); err != nil {
		t.Fatalf("update: %v", err)
	}

	result, err := verifier.VerifyTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("VerifyTenant: %v", err)
	}
	if result.OK() {
		t.Fatal("a rewritten predecessor link left the chain verifying")
	}
	if got := result.Streams[0].Break.Kind; got != audit.BreakPrevMismatch {
		t.Errorf("break kind = %q, want %q", got, audit.BreakPrevMismatch)
	}
}

// A capture altered in place is detectable too, because its digest is inside
// the hashed record even though its bytes are not in the chain's input.
func TestAnAlteredCaptureIsDetected(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	verifier := audit.NewVerifier(st, audit.WithCaptureVerification(true))
	ctx := t.Context()

	rec := record("rec-tampered-capture", "sess-capture")
	rec.Kind = "stream"
	rec.Payload = base64.StdEncoding.EncodeToString([]byte("whoami\r\nroot\r\n"))

	if _, err := in.Ingest(ctx, audit.Submission{
		Tenant: tenantA, ProxyID: proxyA, Records: []contract.LogRecord{rec},
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if ok, err := verifier.VerifyTenant(ctx, tenantA); err != nil || !ok.OK() {
		t.Fatalf("a healthy chain with a capture did not verify: %v %v", err, ok.Streams)
	}

	if _, err := st.Pool().Exec(ctx,
		`UPDATE audit_captures SET bytes = $2 WHERE tenant = $1 AND record_id = $3`,
		tenantA, []byte("whoami\r\nnobody\r\n"), rec.RecordID); err != nil {
		t.Fatalf("update: %v", err)
	}

	result, err := verifier.VerifyTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("VerifyTenant: %v", err)
	}
	if result.OK() {
		t.Fatal("an altered capture left the chain verifying")
	}
	if got := result.Streams[0].Break.Kind; got != audit.BreakCaptureMismatch {
		t.Errorf("break kind = %q, want %q", got, audit.BreakCaptureMismatch)
	}
}

func mustIngest(ctx context.Context, t *testing.T, in *audit.Ingester, tenant store.Tenant, proxy string, recs ...contract.LogRecord) {
	t.Helper()
	if _, err := in.Ingest(ctx, audit.Submission{Tenant: tenant, ProxyID: proxy, Records: recs}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
}
