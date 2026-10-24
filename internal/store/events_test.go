// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

func logEntry(body string, proxies ...string) store.EventLogEntry {
	return store.EventLogEntry{
		AudienceAll:     len(proxies) == 0,
		AudienceProxies: proxies,
		Body:            []byte(body),
		PublishedAt:     time.Now(),
		Origin:          "node-a",
	}
}

// A stream is created once, with an epoch that stays: an id the stream minted
// must mean the same thing however many times it is opened.
func TestAnEventStreamIsOpenedOnceWithAStableEpoch(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	first, err := st.EventLog().Open(ctx, tenantA)
	if err != nil {
		t.Fatal(err)
	}
	if first.Epoch == "" || first.Head != 0 || first.Floor != 0 {
		t.Fatalf("a new stream is %+v, want an epoch and nothing logged", first)
	}
	again, err := st.EventLog().Open(ctx, tenantA)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Fatalf("reopened as %+v, want %+v", again, first)
	}
	other, err := st.EventLog().Open(ctx, tenantB)
	if err != nil {
		t.Fatal(err)
	}
	if other.Epoch == first.Epoch {
		t.Fatal("two tenants' streams share an epoch")
	}
}

// Append assigns positions in order, After reads them back in order and within
// its bounds, and each entry comes back as it was written.
func TestTheEventLogReadsBackInOrder(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	var seqs []int64
	for i := range 5 {
		e, stream, err := st.EventLog().Append(ctx, tenantA, logEntry(fmt.Sprintf(`{"n":%d}`, i), "p1", "p2"), 100)
		if err != nil {
			t.Fatal(err)
		}
		if stream.Head != e.Seq {
			t.Fatalf("head %d after appending %d", stream.Head, e.Seq)
		}
		seqs = append(seqs, e.Seq)
	}
	if !slices.IsSorted(seqs) {
		t.Fatalf("positions %v are not ascending", seqs)
	}

	all, err := st.EventLog().After(ctx, tenantA, 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("read %d entries, want 5", len(all))
	}
	for i, e := range all {
		if e.Seq != seqs[i] || string(e.Body) != fmt.Sprintf(`{"n":%d}`, i) ||
			!slices.Equal(e.AudienceProxies, []string{"p1", "p2"}) || e.AudienceAll || e.Origin != "node-a" {
			t.Fatalf("entry %d read back as %+v", i, e)
		}
	}

	window, err := st.EventLog().After(ctx, tenantA, seqs[1], seqs[3], 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 2 || window[0].Seq != seqs[2] || window[1].Seq != seqs[3] {
		t.Fatalf("after %d through %d read %+v", seqs[1], seqs[3], window)
	}
	limited, err := st.EventLog().After(ctx, tenantA, 0, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[1].Seq != seqs[1] {
		t.Fatalf("a limit of 2 read %+v", limited)
	}
}

// The log keeps each tenant's newest events and records how far it pruned. The
// floor is what turns "this id is too old" into a fact every node reads alike.
func TestTheEventLogPrunesPerTenantAndRecordsTheFloor(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	var seqs []int64
	for i := range 5 {
		e, _, err := st.EventLog().Append(ctx, tenantA, logEntry(fmt.Sprintf(`{"n":%d}`, i)), 3)
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, e.Seq)
	}
	// Another tenant's traffic prunes nothing of tenant A's.
	for range 4 {
		if _, _, err := st.EventLog().Append(ctx, tenantB, logEntry(`{}`), 1); err != nil {
			t.Fatal(err)
		}
	}

	stream, err := st.EventLog().Open(ctx, tenantA)
	if err != nil {
		t.Fatal(err)
	}
	if stream.Floor != seqs[1] || stream.Head != seqs[4] {
		t.Fatalf("stream is %+v, want floor %d and head %d", stream, seqs[1], seqs[4])
	}
	kept, err := st.EventLog().After(ctx, tenantA, 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 3 || kept[0].Seq != seqs[2] {
		t.Fatalf("kept %+v, want the newest three", kept)
	}
	other, err := st.EventLog().After(ctx, tenantB, 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 {
		t.Fatalf("tenant B kept %d, want 1", len(other))
	}
}

// Concurrent publishes in one tenant commit in the order of their positions. A
// reader polling "after the last I saw" must never see a position and later
// find an earlier one appear behind it, because a node fanning out that way
// would have skipped it.
func TestConcurrentAppendsCommitInPositionOrder(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	const writers, each = 4, 15
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				if _, _, err := st.EventLog().Append(ctx, tenantA, logEntry(fmt.Sprintf(`{"w":%d,"i":%d}`, w, i)), 1000); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	var (
		seen  int
		after int64
	)
	for finished := false; !finished; {
		select {
		case <-done:
			finished = true
		default:
		}
		got, err := st.EventLog().After(ctx, tenantA, after, 0, 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range got {
			after = e.Seq
			seen++
		}
	}
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	all, err := st.EventLog().After(ctx, tenantA, 0, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if seen != writers*each || len(all) != writers*each {
		t.Fatalf("a reader following the log saw %d of %d events: one committed behind a position it had already passed", seen, len(all))
	}
}

// A refusal before anything is written.
func TestAnUnaddressedOrEmptyEventIsRefused(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	for name, e := range map[string]store.EventLogEntry{
		"no audience": {Body: []byte(`{}`), PublishedAt: time.Now()},
		"no body":     {AudienceAll: true, PublishedAt: time.Now()},
		"no time":     {AudienceAll: true, Body: []byte(`{}`)},
	} {
		if _, _, err := st.EventLog().Append(ctx, tenantA, e, 10); !store.IsInvalid(err) {
			t.Errorf("%s: got %v, want invalid", name, err)
		}
	}
	if _, _, err := st.EventLog().Append(ctx, tenantA, logEntry(`{}`), 0); !store.IsInvalid(err) {
		t.Errorf("retain 0: got %v, want invalid", err)
	}
}

// Each node writes its own rows, never moves a last write backwards, removes
// only its own, and every node reads them all; expiry drops whatever is old,
// whoever wrote it.
func TestEventSubscriptionsAreWrittenPerNodeAndReadByAll(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	subs := st.EventSubscriptions()
	t0 := time.Now().UTC().Truncate(time.Millisecond)

	if err := subs.Put(ctx, tenantA, "node-a", []store.EventSubscription{
		{ProxyID: "p1", SeenAt: t0}, {ProxyID: "p2", SeenAt: t0},
	}); err != nil {
		t.Fatal(err)
	}
	if err := subs.Put(ctx, tenantA, "node-b", []store.EventSubscription{{ProxyID: "p1", SeenAt: t0.Add(time.Second)}}); err != nil {
		t.Fatal(err)
	}
	// An older write does not move the row back.
	if err := subs.Put(ctx, tenantA, "node-a", []store.EventSubscription{{ProxyID: "p1", SeenAt: t0.Add(-time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	if err := subs.Put(ctx, tenantB, "node-a", []store.EventSubscription{{ProxyID: "p9", SeenAt: t0}}); err != nil {
		t.Fatal(err)
	}

	got, err := subs.List(ctx, tenantA)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.EventSubscription{
		{ProxyID: "p1", NodeID: "node-a", SeenAt: t0},
		{ProxyID: "p1", NodeID: "node-b", SeenAt: t0.Add(time.Second)},
		{ProxyID: "p2", NodeID: "node-a", SeenAt: t0},
	}
	if !slices.EqualFunc(got, want, func(a, b store.EventSubscription) bool {
		return a.ProxyID == b.ProxyID && a.NodeID == b.NodeID && a.SeenAt.Equal(b.SeenAt)
	}) {
		t.Fatalf("listed %+v, want %+v", got, want)
	}

	if err := subs.Remove(ctx, tenantA, "node-a", []string{"p1"}); err != nil {
		t.Fatal(err)
	}
	got, err = subs.List(ctx, tenantA)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].NodeID != "node-b" || got[1].ProxyID != "p2" {
		t.Fatalf("after node-a removed p1, listed %+v", got)
	}

	n, err := subs.Expire(ctx, tenantA, t0.Add(500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expired %d rows, want node-a's p2", n)
	}
	if other, err := subs.List(ctx, tenantB); err != nil || len(other) != 1 {
		t.Fatalf("tenant B lists %+v (%v), want its one row untouched", other, err)
	}
}
