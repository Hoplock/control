// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/extdefault"
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
	verifier := audit.NewVerifier(st, audit.WithRetentionKeys(retentionKeys(t, st)))
	ctx := t.Context()
	mustIngest(ctx, t, in, tenantA, proxyA, records(8, "sess-retain")...)

	streams, err := st.Audit().Streams(ctx, tenantA)
	if err != nil || len(streams) != 1 {
		t.Fatalf("streams = %v, %v", streams, err)
	}
	stream := streams[0]

	marker, err := retainer(t, st).RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, ThroughSeq: 5}, retentionActor)
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	if marker.Deleted != 5 || marker.ThroughSeq != 5 || marker.ThroughHash == "" || marker.Signature == "" {
		t.Fatalf("retained %+v, want 5 through 5 with a hash and a seal", marker)
	}

	// The pass's own record joined the control stream, so the proxy's stream
	// is read by name.
	result, err := verifier.VerifyStream(ctx, tenantA, stream)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Break != nil {
		t.Fatalf("a lawful retention pass broke the chain: %s", result.Break)
	}
	if result.Records != 3 || result.RetainedThrough != 5 {
		t.Errorf("walked %d records from %d, want 3 resuming after 5", result.Records, result.RetainedThrough)
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
	result, err = verifier.VerifyStream(ctx, tenantA, stream)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Break == nil || result.Break.Kind != audit.BreakGap {
		t.Errorf("a deletion from the middle after retention: %+v, want a gap", result)
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
	emitter := retainer(t, st)
	pass := func(tenant store.Tenant, through int64) error {
		_, err := emitter.RetainPrefix(ctx, tenant, audit.PrefixPass{Stream: stream, ThroughSeq: through}, retentionActor)
		return err
	}

	if err := pass(tenantA, 4); !store.IsInvalid(err) {
		t.Errorf("retaining through the head answered %v, want invalid", err)
	}
	if err := pass(tenantA, 2); err != nil {
		t.Fatalf("retain through 2: %v", err)
	}
	if err := pass(tenantA, 2); !store.IsInvalid(err) {
		t.Errorf("retaining through 2 twice answered %v, want invalid", err)
	}
	if err := pass("other-tenant", 1); !store.IsNotFound(err) {
		t.Errorf("another tenant's retention of this stream answered %v, want not found", err)
	}
}

// A capture's bytes can go while its record stays: the chain still verifies,
// and the store says the bytes are gone rather than changed.
func TestRetainedCapturesAreGoneNotChanged(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	verifier := audit.NewVerifier(st, audit.WithCaptureVerification(true), audit.WithRetentionKeys(retentionKeys(t, st)))
	ctx := t.Context()

	rec := record("rec-capture", "sess-capture")
	rec.Kind = "stream"
	rec.Timestamp = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	rec.Payload = base64.StdEncoding.EncodeToString([]byte("whoami\r\nroot\r\n"))
	mustIngest(ctx, t, in, tenantA, proxyA, []contract.LogRecord{rec}...)

	out, err := retainer(t, st).RetainCaptures(ctx, tenantA, time.Now().Add(-24*time.Hour), retentionActor)
	if err != nil || out.Emptied != 1 {
		t.Fatalf("retain captures = %+v, %v; want 1", out, err)
	}
	if _, err := st.Audit().Capture(ctx, tenantA, rec.RecordID); !store.IsCaptureRetained(err) {
		t.Errorf("reading a retained capture answered %v, want ErrCaptureRetained", err)
	}
	result, err := verifier.VerifyStream(ctx, tenantA, audit.StreamFor(proxyA))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Break != nil || result.CapturesRetained != 1 {
		t.Errorf("verification after capture retention: %+v", result)
	}
}

func retainer(t *testing.T, st *store.Store) *audit.Emitter {
	t.Helper()
	emitter, err := audit.NewEmitter(newIngester(t, st), audit.WithRetentionSigner(retentionKeys(t, st)))
	if err != nil {
		t.Fatal(err)
	}
	return emitter
}

// testKEK is the key-encryption key every test's software custodian shares, so
// a key one helper generated another can use.
var testKEK = bytes.Repeat([]byte{0x24}, extdefault.KeyEncryptionKeySize)

// retentionKeys is Control's own custody over the test database: the key store
// a deployment with a key-encryption key and no ext.KeyStore runs.
func retentionKeys(t *testing.T, st *store.Store) *audit.RetentionKeys {
	t.Helper()
	return audit.NewRetentionKeys(softwareKeyStore(t, st))
}

func softwareKeyStore(t *testing.T, st *store.Store) *extdefault.SoftwareKeyStore {
	t.Helper()
	ks, err := extdefault.NewSoftwareKeyStore(st, testKEK)
	if err != nil {
		t.Fatal(err)
	}
	return ks
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
	if result, err := audit.NewVerifier(st, audit.WithRetentionKeys(retentionKeys(t, st))).VerifyStream(ctx, tenantA, stream); err != nil || result.Break != nil || result.Records != 1 {
		t.Errorf("verification after the passes = %+v, %v; want the head alone, verifying", result, err)
	}
	// Three passes, three records: the one that took nothing is an
	// operator's act too (0070).
	recs, _ := st.Audit().Query(ctx, tenantA, store.AuditQuery{Event: audit.EventAuditRetained})
	if len(recs) != 3 {
		t.Fatalf("%d records of a pass, want 3", len(recs))
	}
	agrees(t, recs[0], all)
	if recs[0].Message != "stream "+stream+" retained through 5" {
		t.Errorf("a pass that deleted says %q", recs[0].Message)
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

// agrees asserts a prefix pass's operator.audit.retained record carries the
// facts its answer does, and no archive fact when no archive is registered.
func agrees(t *testing.T, rec store.AuditRecord, out audit.PrefixRetained) {
	t.Helper()
	a := rec.Attributes
	want := map[string]string{
		"event":                         audit.EventAuditRetained,
		"operator.stream":               out.Stream,
		"operator.deleted":              strconv.FormatInt(out.Deleted, 10),
		audit.AttrAfter:                 strconv.FormatInt(out.ThroughSeq, 10),
		"operator.through_hash":         out.ThroughHash,
		"operator.key":                  out.KeyName,
		"operator.signature":            out.Signature,
		"operator.held_by_archive":      "",
		"operator.archived_through_seq": "",
	}
	if out.Archived {
		want["operator.held_by_archive"] = strconv.FormatBool(out.HeldByArchive)
		want["operator.archived_through_seq"] = strconv.FormatInt(out.ArchivedThroughSeq, 10)
	}
	for k, v := range want {
		got, ok := a[k]
		switch {
		case v == "" && ok:
			t.Errorf("the record carries %s = %q, which the answer has no value for", k, got)
		case v != "" && got != v:
			t.Errorf("the record carries %s = %q, the answer %q", k, got, v)
		}
	}
}

// retainedRecords is every operator.audit.retained record of a tenant, newest
// first.
func retainedRecords(t *testing.T, st *store.Store, tenant store.Tenant) []store.AuditRecord {
	t.Helper()
	recs, err := st.Audit().Query(t.Context(), tenant, store.AuditQuery{Event: audit.EventAuditRetained})
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

// markerRow is a stream's audit_retention row exactly as stored, or "" with
// none.
func markerRow(t *testing.T, st *store.Store, tenant store.Tenant, stream string) string {
	t.Helper()
	var row string
	err := st.Pool().QueryRow(t.Context(),
		`SELECT row_to_json(r)::text FROM audit_retention r WHERE tenant = $1 AND stream = $2`, tenant, stream).Scan(&row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// controlHead is where the tenant's control stream ends: how many records it
// holds and the last one's hash.
func controlHead(t *testing.T, st *store.Store, tenant store.Tenant) string {
	t.Helper()
	recs, err := st.Audit().Chain(t.Context(), tenant, audit.StreamControl, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		return "empty"
	}
	return fmt.Sprintf("%d records ending %s", len(recs), recs[len(recs)-1].Hash)
}

// A pass by instant that takes nothing is recorded all the same (0070), and
// writes no marker and no seal. With no marker its record says the stream is
// retained through 0 and names no hash, key or signature; with one, it names
// the marker's, and the marker row is exactly as it was.
func TestAPassByInstantThatTakesNothingIsRecorded(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()
	mustIngest(ctx, t, in, tenantA, proxyA, records(6, "sess-nothing")...)
	stream := audit.StreamFor(proxyA)
	emitter := retainer(t, st)
	// Every record was received now, so an hour ago reaches none of them.
	early := time.Now().Add(-time.Hour)

	out, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, Before: early}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	if out.Deleted != 0 || out.ThroughSeq != 0 || out.ThroughHash != "" || out.KeyName != "" || out.Signature != "" || out.Archived {
		t.Fatalf("a pass that reaches nothing on an unretained stream = %+v", out)
	}
	recs := retainedRecords(t, st, tenantA)
	if len(recs) != 1 {
		t.Fatalf("%d records of a pass that took nothing, want 1", len(recs))
	}
	agrees(t, recs[0], out)
	if recs[0].Message != "stream "+stream+": nothing retained" {
		t.Errorf("the record says %q", recs[0].Message)
	}
	if row := markerRow(t, st, tenantA, stream); row != "" {
		t.Errorf("a pass that took nothing wrote a marker: %s", row)
	}
	if result, err := audit.NewVerifier(st, audit.WithRetentionKeys(retentionKeys(t, st))).VerifyStream(ctx, tenantA, stream); err != nil || result.Break != nil || result.Records != 6 || result.RetainedThrough != 0 {
		t.Errorf("verification after it = %+v, %v; want all 6, from 1", result, err)
	}

	sealed, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, ThroughSeq: 3}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	before := markerRow(t, st, tenantA, stream)
	again, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, Before: early}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	if again.Deleted != 0 || again.ThroughSeq != 3 || again.ThroughHash != sealed.ThroughHash ||
		again.KeyName != sealed.KeyName || again.Signature != sealed.Signature {
		t.Fatalf("a pass short of the marker = %+v, want the marker %+v", again, sealed)
	}
	recs = retainedRecords(t, st, tenantA)
	if len(recs) != 3 {
		t.Fatalf("%d records after three passes, want 3", len(recs))
	}
	agrees(t, recs[0], again)
	if recs[0].Message != "stream "+stream+": nothing retained, still retained through 3" {
		t.Errorf("the record says %q", recs[0].Message)
	}
	if after := markerRow(t, st, tenantA, stream); after != before {
		t.Errorf("a pass that took nothing changed the marker from %s to %s", before, after)
	}
	verify(t, st, tenantA)
}

// What is refused is refused whole (PLAN §7): a pass the store refuses (at or
// behind the marker, or at the head), one with no key store and one over a
// start that does not verify each leave the control stream's head where it
// was — nothing deleted, sealed or recorded.
func TestARefusedPassRecordsNothing(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()
	mustIngest(ctx, t, in, tenantA, proxyA, records(6, "sess-refused")...)
	stream := audit.StreamFor(proxyA)
	emitter := retainer(t, st)
	if _, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, ThroughSeq: 2}, retentionActor); err != nil {
		t.Fatal(err)
	}
	head := controlHead(t, st, tenantA)
	later := time.Now().Add(time.Minute)

	for name, through := range map[string]int64{"the head": 6, "the marker": 2, "behind the marker": 1} {
		if _, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, ThroughSeq: through}, retentionActor); !store.IsInvalid(err) {
			t.Errorf("a pass through %s answered %v, want invalid", name, err)
		}
	}
	keyless, err := audit.NewEmitter(newIngester(t, st))
	if err != nil {
		t.Fatal(err)
	}
	for _, pass := range []audit.PrefixPass{{Stream: stream, ThroughSeq: 4}, {Stream: stream, Before: later}, {Stream: stream, Before: time.Now().Add(-time.Hour)}} {
		if _, err := keyless.RetainPrefix(ctx, tenantA, pass, retentionActor); !errors.Is(err, audit.ErrRetentionKeyNotConfigured) {
			t.Errorf("a pass %+v with no key store answered %v", pass, err)
		}
	}
	if _, err := st.Pool().Exec(ctx,
		`UPDATE audit_retention SET signature = '' WHERE tenant = $1 AND stream = $2`, tenantA, stream); err != nil {
		t.Fatal(err)
	}
	for _, pass := range []audit.PrefixPass{{Stream: stream, ThroughSeq: 4}, {Stream: stream, Before: later}} {
		if _, err := emitter.RetainPrefix(ctx, tenantA, pass, retentionActor); !audit.IsRetentionUnverified(err) {
			t.Errorf("a pass %+v over an unsigned marker answered %v", pass, err)
		}
	}
	if after := controlHead(t, st, tenantA); after != head {
		t.Errorf("refused passes moved the control stream from %s to %s", head, after)
	}
	if chain, _ := st.Audit().Chain(ctx, tenantA, stream, 0, 100); len(chain) != 4 {
		t.Errorf("%d of 4 records left after refused passes", len(chain))
	}
}
