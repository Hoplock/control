// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// appendRecords writes n chained records to a stream, the last of them
// optionally with a capture.
func appendRecords(t *testing.T, st *store.Store, tenant store.Tenant, stream string, from, n int64) {
	t.Helper()
	for seq := from; seq < from+n; seq++ {
		if err := st.Audit().Append(t.Context(), tenant, storetest.AuditRecord(fmt.Sprintf("%s-%s-%d", tenant, stream, seq), stream, seq)); err != nil {
			t.Fatal(err)
		}
	}
}

// withCapture writes a record that claims a capture, and the capture's bytes.
func withCapture(t *testing.T, st *store.Store, tenant store.Tenant, stream string, seq int64, recordedAt time.Time) string {
	t.Helper()
	id := fmt.Sprintf("%s-%s-%d", tenant, stream, seq)
	if err := st.Audit().PutCapture(t.Context(), tenant, id, []byte("whoami\r\n")); err != nil {
		t.Fatal(err)
	}
	rec := storetest.AuditRecord(id, stream, seq)
	rec.CaptureBytes, rec.CaptureSHA256, rec.RecordedAt = 8, "sha256:x", recordedAt
	if err := st.Audit().Append(t.Context(), tenant, rec); err != nil {
		t.Fatal(err)
	}
	return id
}

// A lease is held by one node at a time, renewed by its holder, taken by
// another only once it lapses, and given back early by Release.
func TestAFeedLeaseIsHeldByOneNodeAtATime(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	claim := func(holder string, ttl time.Duration) bool {
		t.Helper()
		_, ok, err := st.Feed().Claim(ctx, tenantA, store.ArchiveConsumer, "proxy:p1", holder, ttl)
		if err != nil {
			t.Fatalf("claim by %s: %v", holder, err)
		}
		return ok
	}
	if !claim("a", time.Hour) {
		t.Fatal("the first claim was refused")
	}
	if claim("b", time.Hour) {
		t.Fatal("a second node took a lease the first holds")
	}
	if !claim("a", time.Hour) {
		t.Fatal("the holder could not renew its own lease")
	}
	if err := st.Feed().Release(ctx, tenantA, store.ArchiveConsumer, "proxy:p1", "b"); err != nil {
		t.Fatal(err)
	}
	if claim("b", time.Hour) {
		t.Fatal("a release by a node that does not hold the lease gave it away")
	}
	if err := st.Feed().Release(ctx, tenantA, store.ArchiveConsumer, "proxy:p1", "a"); err != nil {
		t.Fatal(err)
	}
	if !claim("b", 50*time.Millisecond) {
		t.Fatal("a released lease was not taken")
	}
	time.Sleep(100 * time.Millisecond)
	if !claim("a", time.Hour) {
		t.Fatal("a lapsed lease was not taken")
	}
	// Another tenant's stream of the same name has a lease of its own (M18).
	if _, ok, err := st.Feed().Claim(ctx, tenantB, store.ArchiveConsumer, "proxy:p1", "b", time.Hour); err != nil || !ok {
		t.Fatalf("tenant B's lease = %v, %v; another tenant's lease held it", ok, err)
	}
}

// A position only moves forward: a confirmation at or behind it changes
// nothing, which is what lets two nodes whose turns overlapped both confirm.
func TestAFeedPositionNeverMovesBackwards(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	if _, err := st.Feed().Position(ctx, tenantA, store.ArchiveConsumer, "control"); !store.IsNotFound(err) {
		t.Fatalf("a stream never fed: %v, want not found", err)
	}
	for _, c := range []struct {
		seq  int64
		want int64
	}{{5, 5}, {3, 5}, {5, 5}, {9, 9}} {
		pos, err := st.Feed().Confirm(ctx, tenantA, store.ArchiveConsumer, "control", c.seq, fmt.Sprintf("h%d", c.seq))
		if err != nil {
			t.Fatal(err)
		}
		if pos.ConfirmedSeq != c.want || pos.ConfirmedHash != fmt.Sprintf("h%d", c.want) || pos.ConfirmedAt.IsZero() {
			t.Errorf("confirming %d left %+v, want %d", c.seq, pos, c.want)
		}
	}
	if pos, err := st.Feed().Position(ctx, tenantB, store.ArchiveConsumer, "control"); !store.IsNotFound(err) {
		t.Errorf("tenant B's position = %+v, %v; tenant A's confirmation reached it", pos, err)
	}
}

// Streams lists each stream with its head, its retention marker and the
// consumer's position, and counts the captures of confirmed records still
// waiting; ArchivedThrough stops before the first of them; the lag is scalars.
func TestTheFeedSeesEachStreamAndWhatIsWaiting(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	old := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	appendRecords(t, st, tenantA, "proxy:p1", 1, 2)
	capID := withCapture(t, st, tenantA, "proxy:p1", 3, old)
	appendRecords(t, st, tenantA, "proxy:p1", 4, 3)
	appendRecords(t, st, tenantA, "control", 1, 2)
	appendRecords(t, st, tenantB, "proxy:p1", 1, 9)
	if _, _, err := st.Audit().RetainPrefix(ctx, tenantA, "control", 1, testSeal); err != nil {
		t.Fatal(err)
	}

	streams, err := st.Feed().Streams(ctx, tenantA, store.ArchiveConsumer)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.FeedStream{
		{Stream: "control", Head: 2, Start: 1},
		{Stream: "proxy:p1", Head: 6},
	}
	if fmt.Sprint(streams) != fmt.Sprint(want) {
		t.Fatalf("streams = %+v, want %+v", streams, want)
	}
	if !streams[0].Behind() || streams[0].From() != 1 {
		t.Errorf("control from %d, behind %v; want after its marker", streams[0].From(), streams[0].Behind())
	}

	if _, err := st.Feed().Confirm(ctx, tenantA, store.ArchiveConsumer, "proxy:p1", 5, "h5"); err != nil {
		t.Fatal(err)
	}
	streams, _ = st.Feed().Streams(ctx, tenantA, store.ArchiveConsumer)
	if streams[1].CapturesReady != 1 || streams[1].Confirmed != 5 {
		t.Errorf("proxy:p1 = %+v, want one capture ready behind a position of 5", streams[1])
	}
	if a, err := st.Feed().ArchivedThrough(ctx, tenantA, "proxy:p1"); err != nil || a != 2 {
		t.Errorf("archived through %d, %v; want 2, before the record whose capture is not archived", a, err)
	}
	if at, ok, err := st.Feed().EarliestUnarchivedCapture(ctx, tenantA); err != nil || !ok || !at.Equal(old) {
		t.Errorf("earliest unarchived capture = %v %v %v, want %v", at, ok, err, old)
	}
	lag, err := st.Feed().Lag(ctx, tenantA, store.ArchiveConsumer)
	if err != nil || lag.StreamsBehind != 2 || lag.CapturesWaiting != 1 || lag.OldestUnconfirmed.IsZero() {
		t.Errorf("lag = %+v, %v; want both streams behind and one capture waiting", lag, err)
	}

	caps, err := st.Feed().PendingCaptures(ctx, tenantA, "proxy:p1", 5, 10, 1)
	if err != nil || len(caps) != 1 || caps[0].RecordID != capID || string(caps[0].Bytes) != "whoami\r\n" {
		t.Fatalf("pending captures = %+v, %v; want the one, whatever the byte bound", caps, err)
	}
	if caps, _ := st.Feed().PendingCaptures(ctx, tenantA, "proxy:p1", 2, 10, 1<<20); len(caps) != 0 {
		t.Errorf("a capture of a record not confirmed was offered: %+v", caps)
	}
	if n, err := st.Feed().ConfirmCaptures(ctx, tenantA, []string{capID}); err != nil || n != 1 {
		t.Fatalf("confirm captures = %d, %v", n, err)
	}
	if a, _ := st.Feed().ArchivedThrough(ctx, tenantA, "proxy:p1"); a != 5 {
		t.Errorf("archived through %d once the capture is confirmed, want the position, 5", a)
	}
	if _, ok, _ := st.Feed().EarliestUnarchivedCapture(ctx, tenantA); ok {
		t.Error("a confirmed capture still reads as waiting")
	}

	// Tenant B's records and positions are its own (M18).
	if a, _ := st.Feed().ArchivedThrough(ctx, tenantB, "proxy:p1"); a != 0 {
		t.Errorf("tenant B is archived through %d on tenant A's position", a)
	}
	if lag, _ := st.Feed().Lag(ctx, tenantB, store.ArchiveConsumer); lag.StreamsBehind != 1 || lag.CapturesWaiting != 0 {
		t.Errorf("tenant B's lag = %+v", lag)
	}
}

// A capture pass that empties only confirmed captures keeps an unconfirmed one
// recorded before its instant: the guard that holds when a capture arrives
// after the pass read the earliest one waiting.
func TestACapturePassForAnArchiveEmptiesOnlyConfirmedCaptures(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	old := time.Now().Add(-time.Hour).UTC()
	confirmed := withCapture(t, st, tenantA, "proxy:p1", 1, old)
	waiting := withCapture(t, st, tenantA, "proxy:p1", 2, old)
	if _, err := st.Feed().ConfirmCaptures(ctx, tenantA, []string{confirmed}); err != nil {
		t.Fatal(err)
	}
	emptied, err := st.Audit().RetainCaptures(ctx, tenantA, time.Now(), store.RetainCapturesOptions{
		By: "x", ArchivedOnly: true,
		Seal: func(context.Context, time.Time) (store.RetentionSeal, error) {
			return store.RetentionSeal{KeyName: "audit-retention:test", Signature: "c2ln"}, nil
		},
	})
	if err != nil || emptied != 1 {
		t.Fatalf("emptied %d, %v; want the confirmed capture alone", emptied, err)
	}
	if _, err := st.Audit().Capture(ctx, tenantA, confirmed); !store.IsCaptureRetained(err) {
		t.Errorf("the confirmed capture: %v, want retained", err)
	}
	if _, err := st.Audit().Capture(ctx, tenantA, waiting); err != nil {
		t.Errorf("the capture the archive has not confirmed was emptied: %v", err)
	}
}

// A sink's position begins at a stream's chain start, after its marker. When a
// later retention pass removes records the sink has not confirmed, Skip moves
// it to the new marker once, counts the run in the row and on the lag, and a
// confirmation through the head clears the count the lag shows; the archive is
// never begun or skipped this way (0026).
func TestASinkBeginsAtTheChainStartAndSkipsWhatRetentionRemovedBehindIt(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	sinkA, sinkB := store.SinkConsumer("example.com/a"), store.SinkConsumer("example.com/b")
	appendRecords(t, st, tenantA, "proxy:p1", 1, 6)
	first, _, err := st.Audit().RetainPrefix(ctx, tenantA, "proxy:p1", 2, testSeal)
	if err != nil {
		t.Fatal(err)
	}

	for _, sink := range []string{sinkA, sinkB} {
		if err := st.Feed().Begin(ctx, tenantA, sink, "proxy:p1"); err != nil {
			t.Fatal(err)
		}
		pos, err := st.Feed().Position(ctx, tenantA, sink, "proxy:p1")
		if err != nil || pos.ConfirmedSeq != 2 || pos.ConfirmedHash != first.ThroughHash || pos.SkippedRecords != 0 {
			t.Fatalf("%s began at %+v, %v; want the marker, uncounted", sink, pos, err)
		}
		if _, moved, err := st.Feed().Skip(ctx, tenantA, sink, "proxy:p1"); err != nil || moved {
			t.Errorf("%s skipped from the chain start: %v %v", sink, moved, err)
		}
	}
	if _, err := st.Feed().Confirm(ctx, tenantA, sinkA, "proxy:p1", 4, "h4"); err != nil {
		t.Fatal(err)
	}
	if err := st.Feed().Begin(ctx, tenantA, sinkA, "proxy:p1"); err != nil {
		t.Fatal(err)
	}
	if pos, _ := st.Feed().Position(ctx, tenantA, sinkA, "proxy:p1"); pos.ConfirmedSeq != 4 {
		t.Errorf("Begin moved a position it already had, to %d", pos.ConfirmedSeq)
	}

	second, _, err := st.Audit().RetainPrefix(ctx, tenantA, "proxy:p1", 4, testSeal)
	if err != nil {
		t.Fatal(err)
	}
	if _, moved, err := st.Feed().Skip(ctx, tenantA, sinkA, "proxy:p1"); err != nil || moved {
		t.Errorf("a sink that had confirmed through the marker skipped: %v %v", moved, err)
	}
	skip, moved, err := st.Feed().Skip(ctx, tenantA, sinkB, "proxy:p1")
	if err != nil || !moved || skip != (store.FeedSkip{From: 2, Through: 4, Hash: second.ThroughHash}) || skip.Records() != 2 {
		t.Fatalf("skip = %+v %v %v; want 2 through 4", skip, moved, err)
	}
	if _, moved, _ := st.Feed().Skip(ctx, tenantA, sinkB, "proxy:p1"); moved {
		t.Error("one run was skipped twice")
	}
	pos, err := st.Feed().Position(ctx, tenantA, sinkB, "proxy:p1")
	if err != nil || pos.ConfirmedSeq != 4 || pos.ConfirmedHash != second.ThroughHash || pos.SkippedRecords != 2 || pos.SkippedBehind != 2 {
		t.Errorf("after the skip %+v, %v", pos, err)
	}
	if lag, err := st.Feed().Lag(ctx, tenantA, sinkB); err != nil || lag.SkippedByRetention != 2 || lag.StreamsBehind != 1 {
		t.Errorf("lag = %+v, %v; want 2 skipped and the stream behind", lag, err)
	}
	if lag, _ := st.Feed().Lag(ctx, tenantA, sinkA); lag.SkippedByRetention != 0 {
		t.Errorf("sink A's lag counts sink B's skip: %+v", lag)
	}

	if _, err := st.Feed().Confirm(ctx, tenantA, sinkB, "proxy:p1", 5, "h5"); err != nil {
		t.Fatal(err)
	}
	if pos, _ := st.Feed().Position(ctx, tenantA, sinkB, "proxy:p1"); pos.SkippedBehind != 2 {
		t.Errorf("a confirmation short of the head cleared the count: %+v", pos)
	}
	if _, err := st.Feed().Confirm(ctx, tenantA, sinkB, "proxy:p1", 6, "h6"); err != nil {
		t.Fatal(err)
	}
	if pos, _ := st.Feed().Position(ctx, tenantA, sinkB, "proxy:p1"); pos.SkippedBehind != 0 || pos.SkippedRecords != 2 {
		t.Errorf("caught up: %+v; want the lag's count cleared and the row's kept", pos)
	}
	if lag, _ := st.Feed().Lag(ctx, tenantA, sinkB); lag.SkippedByRetention != 0 || lag.StreamsBehind != 0 {
		t.Errorf("caught up, the lag is %+v", lag)
	}

	if _, moved, err := st.Feed().Skip(ctx, tenantA, sinkB, "control"); err != nil || moved {
		t.Errorf("a stream with no position skipped: %v %v", moved, err)
	}
	if err := st.Feed().Begin(ctx, tenantA, store.ArchiveConsumer, "proxy:p1"); !store.IsInvalid(err) {
		t.Errorf("the archive began at a marker: %v", err)
	}
	if _, _, err := st.Feed().Skip(ctx, tenantA, store.ArchiveConsumer, "proxy:p1"); !store.IsInvalid(err) {
		t.Errorf("the archive skipped: %v", err)
	}
	if _, err := st.Feed().Position(ctx, tenantB, sinkB, "proxy:p1"); !store.IsNotFound(err) {
		t.Errorf("tenant B holds tenant A's sink position: %v", err)
	}
}

// A failed attempt holds the lease over its backoff and marks the stream
// failing, from the first failure of a run; another node's failure while the
// lease is held changes nothing; a confirmation, or Recovered, ends the run.
func TestAFailingStreamIsMarkedUntilDeliverySucceeds(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	sink := store.SinkConsumer("example.com/a")
	appendRecords(t, st, tenantA, "control", 1, 2)
	if err := st.Feed().Begin(ctx, tenantA, sink, "control"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Feed().Claim(ctx, tenantA, sink, "control", "n1", time.Hour); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := st.Feed().Fail(ctx, tenantA, sink, "control", "n1", time.Hour); err != nil {
		t.Fatal(err)
	}
	pos, _ := st.Feed().Position(ctx, tenantA, sink, "control")
	if pos.FailingSince.IsZero() || pos.LeaseHolder != "n1" {
		t.Fatalf("after a failure %+v", pos)
	}
	since := pos.FailingSince
	time.Sleep(5 * time.Millisecond)
	if err := st.Feed().Fail(ctx, tenantA, sink, "control", "n1", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := st.Feed().Fail(ctx, tenantA, sink, "control", "n2", time.Hour); err != nil {
		t.Fatal(err)
	}
	pos, _ = st.Feed().Position(ctx, tenantA, sink, "control")
	if !pos.FailingSince.Equal(since) || pos.LeaseHolder != "n1" {
		t.Errorf("a later failure moved the run's start, or another node took the lease: %+v", pos)
	}
	if lag, err := st.Feed().Lag(ctx, tenantA, sink); err != nil || !lag.Failing {
		t.Errorf("lag = %+v, %v; want failing", lag, err)
	}
	if lag, _ := st.Feed().Lag(ctx, tenantA, store.SinkConsumer("example.com/b")); lag.Failing {
		t.Error("another sink reads as failing")
	}

	if _, err := st.Feed().Confirm(ctx, tenantA, sink, "control", 1, "h1"); err != nil {
		t.Fatal(err)
	}
	if lag, _ := st.Feed().Lag(ctx, tenantA, sink); lag.Failing {
		t.Error("a confirmation did not end the run")
	}
	if err := st.Feed().Fail(ctx, tenantA, sink, "control", "n1", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := st.Feed().Recovered(ctx, tenantA, sink, "control"); err != nil {
		t.Fatal(err)
	}
	if pos, _ := st.Feed().Position(ctx, tenantA, sink, "control"); !pos.FailingSince.IsZero() {
		t.Errorf("Recovered left %+v", pos)
	}
}
