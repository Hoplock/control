// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Retention deletes a contiguous prefix and records where the chain resumes,
// so the verifier passes across a pass — and a deletion from the middle, which
// looks exactly like tampering, still breaks it.
func TestARetentionPassKeepsTheChainVerifying(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	verifier := audit.NewVerifier(st)
	ctx := t.Context()
	mustIngest(ctx, t, in, tenantA, proxyA, records(8, "sess-retain")...)

	streams, err := st.Audit().Streams(ctx, tenantA)
	if err != nil || len(streams) != 1 {
		t.Fatalf("streams = %v, %v", streams, err)
	}
	stream := streams[0]

	marker, deleted, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 5, "admin@example.com")
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	if deleted != 5 || marker.ThroughSeq != 5 || marker.ThroughHash == "" {
		t.Fatalf("retained %d through %d (%q), want 5 through 5 with a hash", deleted, marker.ThroughSeq, marker.ThroughHash)
	}

	result, err := verifier.VerifyTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !result.OK() {
		t.Fatalf("a lawful retention pass broke the chain: %s", result.Streams[0].Break)
	}
	if s := result.Streams[0]; s.Records != 3 || s.RetainedThrough != 5 {
		t.Errorf("walked %d records from %d, want 3 resuming after 5", s.Records, s.RetainedThrough)
	}

	// Appending after a pass continues the same chain.
	mustIngest(ctx, t, in, tenantA, proxyA, records(2, "sess-after")...)
	if result, err := verifier.VerifyTenant(ctx, tenantA); err != nil || !result.OK() {
		t.Fatalf("appending after retention: %v %v", err, result.Streams)
	}

	// A record deleted from the middle is still a break, retention or not.
	if _, err := st.Pool().Exec(ctx,
		`DELETE FROM audit_records WHERE tenant = $1 AND stream = $2 AND chain_seq = 7`, tenantA, stream); err != nil {
		t.Fatalf("delete: %v", err)
	}
	result, err = verifier.VerifyTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.OK() || result.Streams[0].Break.Kind != audit.BreakGap {
		t.Errorf("a deletion from the middle after retention: %+v, want a gap", result.Streams[0])
	}
}

// What a pass refuses: going backwards, and the head.
func TestRetentionNeverMovesBackAndNeverTakesTheHead(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()
	mustIngest(ctx, t, in, tenantA, proxyA, records(4, "sess-bounds")...)
	streams, _ := st.Audit().Streams(ctx, tenantA)
	stream := streams[0]

	if _, _, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 4, "x"); !store.IsInvalid(err) {
		t.Errorf("retaining through the head answered %v, want invalid", err)
	}
	if _, _, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 2, "x"); err != nil {
		t.Fatalf("retain through 2: %v", err)
	}
	if _, _, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 2, "x"); !store.IsInvalid(err) {
		t.Errorf("retaining through 2 twice answered %v, want invalid", err)
	}
	if _, _, err := st.Audit().RetainPrefix(ctx, "other-tenant", stream, 1, "x"); !store.IsNotFound(err) {
		t.Errorf("another tenant's retention of this stream answered %v, want not found", err)
	}
}

// A capture's bytes can go while its record stays: the chain still verifies,
// and the store says the bytes are gone rather than changed.
func TestRetainedCapturesAreGoneNotChanged(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	verifier := audit.NewVerifier(st, audit.WithCaptureVerification(true))
	ctx := t.Context()

	rec := record("rec-capture", "sess-capture")
	rec.Kind = "stream"
	rec.Timestamp = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	rec.Payload = base64.StdEncoding.EncodeToString([]byte("whoami\r\nroot\r\n"))
	mustIngest(ctx, t, in, tenantA, proxyA, []contract.LogRecord{rec}...)

	n, err := st.Audit().RetainCaptures(ctx, tenantA, time.Now().Add(-24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("retain captures = %d, %v; want 1", n, err)
	}
	if _, err := st.Audit().Capture(ctx, tenantA, rec.RecordID); !store.IsCaptureRetained(err) {
		t.Errorf("reading a retained capture answered %v, want ErrCaptureRetained", err)
	}
	result, err := verifier.VerifyTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !result.OK() || result.Streams[0].CapturesRetained != 1 {
		t.Errorf("verification after capture retention: %+v", result.Streams[0])
	}
}

func retainer(t *testing.T, st *store.Store) *audit.Emitter {
	t.Helper()
	emitter, err := audit.NewEmitter(newIngester(t, st))
	if err != nil {
		t.Fatal(err)
	}
	return emitter
}

var retentionActor = audit.OperatorActor{Principal: "tok-admin"}

// A pass and its record are one transaction (0021): when the record cannot be
// written, nothing is deleted and the marker does not move, so the store never
// holds a deletion the control chain does not state. The failure is injected
// where it would happen, in the database, on the record's insert.
func TestARetentionPassThatCannotBeRecordedDeletesNothing(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()
	mustIngest(ctx, t, in, tenantA, proxyA, records(6, "sess-atomic")...)
	streams, _ := st.Audit().Streams(ctx, tenantA)
	stream := streams[0]

	if _, err := st.Pool().Exec(ctx, `
		CREATE FUNCTION refuse_retained() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.event = 'operator.audit.retained' THEN
				RAISE EXCEPTION 'the record of the pass cannot be written';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER refuse_retained BEFORE INSERT ON audit_records
			FOR EACH ROW EXECUTE FUNCTION refuse_retained();`); err != nil {
		t.Fatal(err)
	}

	emitter := retainer(t, st)
	if _, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, ThroughSeq: 4}, retentionActor); err == nil {
		t.Fatal("a pass whose record failed answered success")
	}
	if _, err := st.Audit().Retention(ctx, tenantA, stream); !store.IsNotFound(err) {
		t.Errorf("the marker moved for a pass that was not recorded: %v", err)
	}
	if chain, _ := st.Audit().Chain(ctx, tenantA, stream, 0, 100); len(chain) != 6 {
		t.Errorf("%d of 6 records left after a pass that was not recorded", len(chain))
	}
	if _, err := emitter.RetainCaptures(ctx, tenantA, time.Now(), retentionActor); err == nil {
		t.Error("a capture pass whose record failed answered success")
	}
}

// A pass by instant takes the records received before it and stops short of
// the first received at or after it — never the head, even when every record
// is older — and a second pass at the same instant takes nothing more.
func TestARetentionPassByInstantTakesWhatWasReceivedBeforeIt(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()
	mustIngest(ctx, t, in, tenantA, proxyA, records(6, "sess-instant")...)
	streams, _ := st.Audit().Streams(ctx, tenantA)
	stream := streams[0]

	// received_at is the arrival time and is not hashed; spread it so the
	// instant falls between sequences 3 and 4.
	cut := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	if _, err := st.Pool().Exec(ctx, `
		UPDATE audit_records SET received_at = $3::timestamptz + (chain_seq - 4) * interval '1 minute'
		WHERE tenant = $1 AND stream = $2`, tenantA, stream, cut); err != nil {
		t.Fatal(err)
	}
	emitter := retainer(t, st)
	out, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, Before: cut}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	if out.Deleted != 3 || out.ThroughSeq != 3 {
		t.Fatalf("pass = %+v, want sequences 1 to 3 and nothing received at the instant", out)
	}
	if again, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, Before: cut}, retentionActor); err != nil || again.Deleted != 0 || again.ThroughSeq != 3 {
		t.Errorf("the same pass again = %+v, %v; want nothing taken and the marker where it was", again, err)
	}
	all, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, Before: cut.Add(time.Hour)}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	if all.ThroughSeq != 5 {
		t.Errorf("a pass past every record retained through %d, want 5: the head (6) stays", all.ThroughSeq)
	}
	if result, err := audit.NewVerifier(st).VerifyStream(ctx, tenantA, stream); err != nil || result.Break != nil || result.Records != 1 {
		t.Errorf("verification after the passes = %+v, %v; want the head alone, verifying", result, err)
	}
	// Two passes took something, so two records; the no-op is not an act.
	recs, _ := st.Audit().Query(ctx, tenantA, store.AuditQuery{Event: audit.EventAuditRetained})
	if len(recs) != 2 {
		t.Fatalf("%d records of a pass, want 2", len(recs))
	}
	// The latest record and the marker were written by one transaction.
	var marker, record string
	if err := st.Pool().QueryRow(ctx, `SELECT xmin::text FROM audit_retention WHERE tenant = $1 AND stream = $2`, tenantA, stream).Scan(&marker); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool().QueryRow(ctx, `SELECT xmin::text FROM audit_records WHERE tenant = $1 AND record_id = $2`, tenantA, recs[0].RecordID).Scan(&record); err != nil {
		t.Fatal(err)
	}
	if marker != record {
		t.Errorf("the marker was written by transaction %s and the record of its pass by %s", marker, record)
	}
}
