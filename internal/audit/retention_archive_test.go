// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Retention with an archive registered (0025): nothing the archive has not
// confirmed is deleted or emptied. The confirmations are written to the store
// directly here, as the feed would write them; the feed's own tests are in
// internal/archive.

func archivedRetainer(t *testing.T, st *store.Store) *audit.Emitter {
	t.Helper()
	emitter, err := audit.NewEmitter(newIngester(t, st), audit.WithRetentionSigner(retentionKeys(t, st)), audit.WithArchive(true))
	if err != nil {
		t.Fatal(err)
	}
	return emitter
}

// confirm moves the archive's position in a stream to a sequence, as a
// confirmed batch would.
func confirm(t *testing.T, st *store.Store, tenant store.Tenant, stream string, seq int64) {
	t.Helper()
	recs, err := st.Audit().Chain(t.Context(), tenant, stream, seq-1, 1)
	if err != nil || len(recs) != 1 {
		t.Fatalf("record %d of %s: %v", seq, stream, err)
	}
	if _, err := st.Feed().Confirm(t.Context(), tenant, store.ArchiveConsumer, stream, seq, recs[0].Hash); err != nil {
		t.Fatal(err)
	}
}

// unchanged reads what a refused pass must not have touched.
func unchanged(t *testing.T, st *store.Store, tenant store.Tenant, stream string) string {
	t.Helper()
	recs, err := st.Audit().Chain(t.Context(), tenant, stream, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	control, _ := st.Audit().Chain(t.Context(), tenant, audit.StreamControl, 0, 1000)
	marker, err := st.Audit().Retention(t.Context(), tenant, stream)
	if err != nil && !store.IsNotFound(err) {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d records, %d on the control stream, marker %d", len(recs), len(control), marker.ThroughSeq)
}

// A pass by sequence beyond the archive's confirmation is refused whole: no row
// deleted, no marker moved, no record written. Once the archive catches up, the
// same pass deletes what it asked for.
func TestAPassBySequenceBeyondTheArchiveIsRefusedWhole(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()
	mustIngest(ctx, t, in, tenantA, proxyA, records(8, "sess-archive")...)
	stream := audit.StreamFor(proxyA)
	emitter := archivedRetainer(t, st)
	confirm(t, st, tenantA, stream, 3)

	before := unchanged(t, st, tenantA, stream)
	_, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, ThroughSeq: 5}, retentionActor)
	var refused *audit.NotArchivedError
	if !errors.As(err, &refused) || refused.Stream != stream || refused.ArchivedThroughSeq != 3 || refused.ThroughSeq != 5 {
		t.Fatalf("a pass through 5 with the archive at 3 answered %v, want a refusal naming the stream and 3", err)
	}
	if after := unchanged(t, st, tenantA, stream); after != before {
		t.Errorf("a refused pass changed the store: %s, then %s", before, after)
	}
	// With no position at all — an archive that confirmed nothing — even the
	// first record is refused.
	mustIngest(ctx, t, in, tenantA, "proxy-2", records(3, "sess-unfed")...)
	_, err = emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: audit.StreamFor("proxy-2"), ThroughSeq: 1}, retentionActor)
	if !audit.IsNotArchived(err) {
		t.Errorf("a pass over a stream the archive never confirmed answered %v", err)
	}

	confirm(t, st, tenantA, stream, 7)
	out, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, ThroughSeq: 5}, retentionActor)
	if err != nil {
		t.Fatalf("the same pass once the archive caught up: %v", err)
	}
	if out.Deleted != 5 || out.HeldByArchive || !out.Archived || out.ArchivedThroughSeq != 7 {
		t.Errorf("pass = %+v, want 5 deleted, not held, archived through 7", out)
	}
	verify(t, st, tenantA)
}

// A pass by instant deletes only through the confirmation, says it was held
// back and how far the archive is, is sealed, and is recorded with the same
// facts; once the archive catches up, the same pass deletes what it reaches.
func TestAPassByInstantIsHeldBackByTheArchive(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()
	mustIngest(ctx, t, in, tenantA, proxyA, records(8, "sess-instant")...)
	stream := audit.StreamFor(proxyA)
	emitter := archivedRetainer(t, st)
	confirm(t, st, tenantA, stream, 3)
	now := time.Now().Add(time.Minute)

	out, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, Before: now}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	if out.Deleted != 3 || out.ThroughSeq != 3 || !out.HeldByArchive || out.ArchivedThroughSeq != 3 || out.Signature == "" {
		t.Fatalf("pass = %+v, want 1..3 deleted and sealed, held back at 3", out)
	}
	recs, err := st.Audit().Query(ctx, tenantA, store.AuditQuery{Event: audit.EventAuditRetained})
	if err != nil || len(recs) != 1 {
		t.Fatalf("records of the pass: %d, %v", len(recs), err)
	}
	if a := recs[0].Attributes; a["operator.held_by_archive"] != "true" || a["operator.archived_through_seq"] != "3" {
		t.Errorf("the pass's record says %v, want it held back by the archive at 3", a)
	}
	verify(t, st, tenantA)

	// Held at the marker: nothing more to take, and nothing recorded.
	again, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, Before: now}, retentionActor)
	if err != nil || again.Deleted != 0 || again.ThroughSeq != 3 || !again.HeldByArchive {
		t.Errorf("the pass again = %+v, %v; want nothing deleted, held at the marker", again, err)
	}

	confirm(t, st, tenantA, stream, 8)
	all, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, Before: now}, retentionActor)
	if err != nil || all.ThroughSeq != 7 || all.HeldByArchive {
		t.Errorf("the pass once caught up = %+v, %v; want through 7, the head kept, not held", all, err)
	}
	verify(t, st, tenantA)
}

// A record the archive confirmed whose capture it has not is not deleted
// either: the prefix stops before it, by sequence and by instant.
func TestAPrefixPassWaitsForTheCapturesOfWhatItDeletes(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()
	recs := records(6, "sess-cap")
	recs[3].Kind = "stream"
	recs[3].Payload = base64.StdEncoding.EncodeToString([]byte("whoami\r\n"))
	mustIngest(ctx, t, in, tenantA, proxyA, recs...)
	stream := audit.StreamFor(proxyA)
	emitter := archivedRetainer(t, st)
	confirm(t, st, tenantA, stream, 6)

	if _, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, ThroughSeq: 4}, retentionActor); !audit.IsNotArchived(err) {
		t.Fatalf("deleting a record whose capture is not archived answered %v", err)
	}
	out, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, Before: time.Now().Add(time.Minute)}, retentionActor)
	if err != nil || out.ThroughSeq != 3 || !out.HeldByArchive {
		t.Fatalf("by instant = %+v, %v; want through 3, before the record whose capture waits", out, err)
	}
	if _, err := st.Feed().ConfirmCaptures(ctx, tenantA, []string{recs[3].RecordID}); err != nil {
		t.Fatal(err)
	}
	if out, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, ThroughSeq: 4}, retentionActor); err != nil || out.Deleted != 1 {
		t.Errorf("once the capture is archived = %+v, %v", out, err)
	}
	verify(t, st, tenantA)
}

// One tenant's positions never gate another tenant's retention, and with no
// archive registered positions are not read at all.
func TestArchivePositionsAreTheirTenantsAndUnusedWithoutAnArchive(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()
	stream := audit.StreamFor(proxyA)
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		mustIngest(ctx, t, in, tenant, proxyA, records(6, "sess-"+string(tenant))...)
	}
	confirm(t, st, tenantA, stream, 6)
	emitter := archivedRetainer(t, st)
	if _, err := emitter.RetainPrefix(ctx, tenantB, audit.PrefixPass{Stream: stream, ThroughSeq: 2}, retentionActor); !audit.IsNotArchived(err) {
		t.Errorf("tenant B's pass, on tenant A's position, answered %v", err)
	}
	if out, err := emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: stream, ThroughSeq: 2}, retentionActor); err != nil || out.Deleted != 2 {
		t.Errorf("tenant A's pass = %+v, %v", out, err)
	}

	// No archive: a position behind the pass changes nothing, and the answer
	// says nothing held it.
	out, err := retainer(t, st).RetainPrefix(ctx, tenantB, audit.PrefixPass{Stream: stream, ThroughSeq: 4}, retentionActor)
	if err != nil || out.Deleted != 4 || out.Archived || out.HeldByArchive {
		t.Errorf("a pass with no archive registered = %+v, %v", out, err)
	}
	recs, _ := st.Audit().Query(ctx, tenantB, store.AuditQuery{Event: audit.EventAuditRetained})
	for _, r := range recs {
		if _, ok := r.Attributes["operator.held_by_archive"]; ok {
			t.Errorf("a pass with no archive registered recorded archive facts: %v", r.Attributes)
		}
	}
}

// A capture pass with the archive behind runs to the earliest unconfirmed
// capture's instant, says so, signs its watermark over that instant, and
// verifies afterwards; a capture recorded before it that arrives later is kept.
func TestACapturePassIsHeldBackByTheArchive(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	at := func(hoursAgo int) time.Time { return now.Add(-time.Duration(hoursAgo) * time.Hour) }
	capture := func(id string, hoursAgo int) contract.LogRecord {
		r := record(id, "sess-held")
		r.Kind = "stream"
		r.Timestamp = at(hoursAgo).Format(time.RFC3339)
		r.Payload = base64.StdEncoding.EncodeToString([]byte(id + " bytes"))
		return r
	}
	mustIngest(ctx, t, in, tenantA, proxyA, capture("cap-48", 48), capture("cap-36", 36), capture("cap-24", 24))
	if _, err := st.Feed().ConfirmCaptures(ctx, tenantA, []string{"cap-48"}); err != nil {
		t.Fatal(err)
	}
	emitter := archivedRetainer(t, st)
	out, err := emitter.RetainCaptures(ctx, tenantA, at(12), retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	if out.Emptied != 1 || !out.HeldByArchive || !out.Before.Equal(at(36)) || !out.Archived {
		t.Fatalf("pass = %+v, want cap-48 emptied and the pass run to cap-36's instant %s", out, at(36))
	}
	mark, err := st.Audit().CaptureRetention(ctx, tenantA)
	if err != nil || !mark.Before.Equal(at(36)) || mark.Signature == "" {
		t.Errorf("the watermark is %+v, %v; want it signed over %s, the instant the pass ran to", mark, err, at(36))
	}
	for id, gone := range map[string]bool{"cap-48": true, "cap-36": false, "cap-24": false} {
		if _, err := st.Audit().Capture(ctx, tenantA, id); store.IsCaptureRetained(err) != gone {
			t.Errorf("%s: retained %v, want %v", id, store.IsCaptureRetained(err), gone)
		}
	}
	recs, _ := st.Audit().Query(ctx, tenantA, store.AuditQuery{Event: audit.EventAuditRetained})
	if len(recs) != 1 || recs[0].Attributes["operator.held_by_archive"] != "true" {
		t.Errorf("the pass's record: %+v", recs)
	}
	verify(t, st, tenantA)

	// A capture recorded long ago, arriving now unarchived, is past the
	// watermark already and is kept by the next pass, which also runs no
	// further than it.
	mustIngest(ctx, t, in, tenantA, proxyA, capture("cap-72", 72))
	if _, err := st.Feed().ConfirmCaptures(ctx, tenantA, []string{"cap-36", "cap-24"}); err != nil {
		t.Fatal(err)
	}
	out, err = emitter.RetainCaptures(ctx, tenantA, at(12), retentionActor)
	if err != nil || out.Emptied != 0 || !out.HeldByArchive || !out.Before.Equal(at(72)) {
		t.Errorf("the next pass = %+v, %v; want nothing emptied, held at cap-72's instant", out, err)
	}
	if _, err := st.Audit().Capture(ctx, tenantA, "cap-72"); err != nil {
		t.Errorf("an unarchived capture was emptied: %v", err)
	}
	if _, err := st.Feed().ConfirmCaptures(ctx, tenantA, []string{"cap-72"}); err != nil {
		t.Fatal(err)
	}
	out, err = emitter.RetainCaptures(ctx, tenantA, at(12), retentionActor)
	if err != nil || out.Emptied != 3 || out.HeldByArchive || !out.Before.Equal(at(12)) {
		t.Errorf("once caught up = %+v, %v; want the rest emptied, through the instant asked", out, err)
	}
	verify(t, st, tenantA)
}

func verify(t *testing.T, st *store.Store, tenant store.Tenant) {
	t.Helper()
	result, err := audit.NewVerifier(st, audit.WithCaptureVerification(true), audit.WithRetentionKeys(retentionKeys(t, st))).
		VerifyTenant(t.Context(), tenant)
	if err != nil || !result.OK() {
		t.Errorf("audit-verify --captures on %s: %v %+v", tenant, err, result.Streams)
	}
}
