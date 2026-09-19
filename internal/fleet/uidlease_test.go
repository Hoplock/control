// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The uid lease's assertions are about the SEQUENCE rather than the response.
// A server that hands out the same uid twice passes every single-lease check
// there is, so everything below leases repeatedly and compares.

const uidTenant = store.Tenant("acme")

// block is one granted range, kept so later grants can be compared against it.
type block struct {
	from, to int32
	lease    string
	proxy    string
	fate     string
}

func overlaps(a, b block) bool { return a.from < b.to && b.from < a.to }

// THE ASSERTION THE WHOLE ENDPOINT EXISTS FOR. Blocks granted for one target
// never overlap — across abandonment, across expiry, and across two proxies.
//
// A server that keyed its cursor by proxy would look perfect to a
// single-proxy test and hand the same uid to two proxies on the same host, so
// the second proxy is not optional here.
func TestBlocksForOneTargetNeverOverlap(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st, fleet.WithUIDAllocation(fleet.UIDAllocation{
		RangeMin: 2_000_000, RangeMax: 2_099_999, BlockSize: 4096,
	}))
	ctx := t.Context()

	var held []block
	lease := func(proxyID, fate string) block {
		t.Helper()
		got, err := reg.LeaseUIDs(ctx, uidTenant, fleet.UIDLeaseRequest{
			ProxyID: proxyID, Hostname: "busy.example.com", Port: 22, Count: 4096,
			RangeMin: 2_000_000, RangeMax: 2_099_999,
		})
		if err != nil {
			t.Fatalf("lease for %s: %v", proxyID, err)
		}
		b := block{from: got.From, to: got.To, lease: got.LeaseID, proxy: proxyID, fate: fate}
		if b.to <= b.from {
			t.Fatalf("granted an empty or inverted block [%d,%d); the answer at the top of a range is 409",
				b.from, b.to)
		}
		for _, prior := range held {
			if overlaps(prior, b) {
				t.Fatalf("a uid was granted twice: [%d,%d) via %s (%s) intersects [%d,%d) via %s (%s) — "+
					"the cursor must only ever advance, and a block is never reclaimed whether it was "+
					"used, abandoned, or allowed to expire",
					b.from, b.to, b.proxy, b.fate, prior.from, prior.to, prior.proxy, prior.fate)
			}
		}
		held = append(held, b)
		return b
	}

	lease("proxy-1", "used")
	// Blocks taken and never allocated from. A server that reclaims one to
	// save uids is the exact failure this endpoint exists to prevent.
	lease("proxy-1", "abandoned")
	lease("proxy-1", "abandoned")
	// A block left to run past any term. The term bounds how long the PROXY
	// keeps allocating; it is not what makes the uids non-reusable, so an
	// expired block is never one this server may hand to somebody else.
	lease("proxy-1", "expired")
	// Exclusivity is per TARGET, not per proxy.
	lease("proxy-2", "used")
	lease("proxy-2", "abandoned")
	lease("proxy-1", "used")

	if len(held) != 7 {
		t.Fatalf("granted %d blocks, want 7", len(held))
	}

	// Every grant is recorded, so an incident can resolve a uid back to the
	// proxy whose block it came from without polling the fleet.
	recorded, err := st.UIDLeases().ListForTarget(ctx, uidTenant, fleet.UIDTargetID("busy.example.com", 22))
	if err != nil {
		t.Fatalf("ListForTarget: %v", err)
	}
	if len(recorded) != len(held) {
		t.Fatalf("recorded %d leases for %d grants", len(recorded), len(held))
	}
	sort.Slice(held, func(i, j int) bool { return held[i].from < held[j].from })
	for i, r := range recorded {
		if int32(r.From) != held[i].from || r.LeaseID != held[i].lease {
			t.Errorf("lease %d recorded as %s [%d,%d), granted %s [%d,%d)",
				i, r.LeaseID, r.From, r.To, held[i].lease, held[i].from, held[i].to)
		}
	}
}

// The cursor lives in Postgres, not in a process. A block granted before a
// restart must still be gone afterwards — the in-process record is what the
// proxy moved AWAY from, and reproducing it here would defeat the endpoint.
func TestTheCursorSurvivesARestart(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	alloc := fleet.WithUIDAllocation(fleet.UIDAllocation{
		RangeMin: 2_000_000, RangeMax: 2_099_999, BlockSize: 4096,
	})
	req := fleet.UIDLeaseRequest{
		ProxyID: "proxy-1", Hostname: "restart.example.com", Count: 4096,
		RangeMin: 2_000_000, RangeMax: 2_099_999,
	}

	before, err := fleet.New(st, alloc).LeaseUIDs(ctx, uidTenant, req)
	if err != nil {
		t.Fatalf("lease before restart: %v", err)
	}

	// A second registry over the same database is what a restarted process
	// is: nothing in memory carries over.
	after, err := fleet.New(st, alloc).LeaseUIDs(ctx, uidTenant, req)
	if err != nil {
		t.Fatalf("lease after restart: %v", err)
	}
	if after.From < before.To {
		t.Fatalf("after a restart the next block started at %d, inside the block [%d,%d) granted before it: "+
			"the cursor did not survive, which is uid reuse on every deploy",
			after.From, before.From, before.To)
	}
}

// `observed_floor` is corroboration from an untrusted party: it may raise the
// cursor and may never lower it.
func TestObservedFloorRaisesAndNeverLowers(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st, fleet.WithUIDAllocation(fleet.UIDAllocation{BlockSize: 4096}))
	ctx := t.Context()

	req := fleet.UIDLeaseRequest{
		ProxyID: "proxy-1", Hostname: "floor.example.com", Count: 4096,
		RangeMin: 2_500_000, RangeMax: 2_599_999,
	}

	first, err := reg.LeaseUIDs(ctx, uidTenant, req)
	if err != nil {
		t.Fatalf("first lease: %v", err)
	}

	// A floor ABOVE the last grant: the cursor catches up rather than
	// granting blocks the proxy must immediately discard.
	raised := req
	raised.ObservedFloor = first.To + 10_000
	second, err := reg.LeaseUIDs(ctx, uidTenant, raised)
	if err != nil {
		t.Fatalf("raised lease: %v", err)
	}
	if second.From <= raised.ObservedFloor {
		t.Errorf("observed_floor %d was relayed and the next block starts at %d; the floor is the highest "+
			"uid SEEN GIVEN OUT, so the lowest one still free is one past it",
			raised.ObservedFloor, second.From)
	}

	// A floor BELOW the last grant must not pull the cursor back down.
	lowered := req
	lowered.ObservedFloor = first.From
	third, err := reg.LeaseUIDs(ctx, uidTenant, lowered)
	if err != nil {
		t.Fatalf("lowered lease: %v", err)
	}
	if third.From < second.To {
		t.Fatalf("an observed_floor of %d (below the last grant) pulled the cursor back to %d, "+
			"and the previous grant ended at %d — a lowered floor is uid reuse",
			lowered.ObservedFloor, third.From, second.To)
	}
}

// A floor past the top of the range burns that range rather than writing a
// cursor outside it. That is the stated exposure of trusting a relayed
// observation: root on a target can report a large floor and burn that
// target's range — loud, bounded, and reaching no other target — and it is the
// right side of an invariant that prefers refusing to reusing.
func TestAFloorPastTheRangeExhaustsItRatherThanEscapingIt(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st, fleet.WithUIDAllocation(fleet.UIDAllocation{BlockSize: 1024}))
	ctx := t.Context()

	const rangeMin, rangeMax = 3_000_000, 3_004_095
	req := fleet.UIDLeaseRequest{
		ProxyID: "proxy-1", Hostname: "burned.example.com", Count: 1024,
		RangeMin: rangeMin, RangeMax: rangeMax,
		// One below the top, so the grant that carries this floor still
		// succeeds and the raised cursor is committed with it.
		ObservedFloor: rangeMax - 1,
	}
	got, err := reg.LeaseUIDs(ctx, uidTenant, req)
	if err != nil {
		t.Fatalf("lease with a floor near the top: %v", err)
	}
	if got.From != rangeMax || got.To != rangeMax+1 {
		t.Fatalf("block = [%d,%d), want the single uid above the reported floor", got.From, got.To)
	}

	cursor, err := st.UIDCursors().Get(ctx, uidTenant, fleet.UIDTargetID("burned.example.com", 0))
	if err != nil {
		t.Fatalf("read the cursor back: %v", err)
	}
	if cursor.NextUID > cursor.RangeEnd {
		t.Fatalf("the cursor escaped its range: %d > %d", cursor.NextUID, cursor.RangeEnd)
	}

	// The range is now spent, which is a refusal rather than a smaller block.
	if _, err := reg.LeaseUIDs(ctx, uidTenant, req); !errors.Is(err, fleet.ErrUIDRangeUnsatisfiable) {
		t.Fatalf("lease against a burned range = %v, want the range exhausted", err)
	}
}

// A cursor at the top of its range answers the exhausted error, which the
// transport turns into 409 — never a 200 carrying an empty block.
func TestAnExhaustedRangeIsRefusedRatherThanShrunk(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st, fleet.WithUIDAllocation(fleet.UIDAllocation{BlockSize: 4096}))
	ctx := t.Context()

	req := fleet.UIDLeaseRequest{
		ProxyID: "proxy-1", Hostname: "narrow.example.com", Count: 4096,
		RangeMin: 3_000_000, RangeMax: 3_012_287, // three blocks wide
	}

	var granted int
	for range 8 {
		got, err := reg.LeaseUIDs(ctx, uidTenant, req)
		if errors.Is(err, fleet.ErrUIDRangeUnsatisfiable) {
			if granted == 0 {
				t.Fatal("the very first lease answered exhausted: the cursor did not start at range_min, " +
					"so this case graded nothing")
			}
			if got.To > 0 {
				t.Fatalf("the exhausted answer also carried a block [%d,%d)", got.From, got.To)
			}
			return
		}
		if err != nil {
			t.Fatalf("lease %d: %v", granted+1, err)
		}
		if got.To-1 > req.RangeMax || got.From < req.RangeMin {
			t.Fatalf("block [%d,%d) falls outside the requested [%d,%d]; the proxy refuses such a block "+
				"as an outage rather than allocating from it",
				got.From, got.To, req.RangeMin, req.RangeMax)
		}
		granted++
	}
	t.Fatalf("leased %d blocks from a range only %d wide without ever running out; a cursor that never "+
		"reaches the top is one that is reusing uids", granted, req.RangeMax-req.RangeMin+1)
}

// A block is never granted outside the range the proxy said it would accept,
// even when this server's own configured range is wider.
func TestABlockIsNeverGrantedOutsideTheRequestedRange(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st, fleet.WithUIDAllocation(fleet.UIDAllocation{
		RangeMin: 1_000, RangeMax: 2_147_483_646, BlockSize: 4096,
	}))
	ctx := t.Context()

	got, err := reg.LeaseUIDs(ctx, uidTenant, fleet.UIDLeaseRequest{
		ProxyID: "proxy-1", Hostname: "fussy.example.com", Count: 4096,
		RangeMin: 5_000_000, RangeMax: 5_008_191,
	})
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if got.From < 5_000_000 || got.To-1 > 5_008_191 {
		t.Errorf("block [%d,%d) escaped the requested [5000000,5008191]", got.From, got.To)
	}
}

// An inverted range is a malformed caller, not a spent one: an operator
// reading a 409 would go looking for a cursor at the top of its range, and
// there is not one.
func TestAnInvertedRangeIsRefusedAsInvalid(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st)

	_, err := reg.LeaseUIDs(t.Context(), uidTenant, fleet.UIDLeaseRequest{
		ProxyID: "proxy-1", Hostname: "backwards.example.com",
		RangeMin: 3_000, RangeMax: 2_000,
	})
	if !errors.Is(err, fleet.ErrUIDRangeInvalid) {
		t.Fatalf("lease with range_max < range_min = %v, want it refused as invalid", err)
	}
}

// Concurrent leases for one target are ordered by the row lock, not by luck.
// This is the assertion the lock exists for, and a fake database cannot hold
// it: N goroutines advancing one cursor need N connections.
func TestConcurrentLeasesNeverOverlap(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st, fleet.WithUIDAllocation(fleet.UIDAllocation{BlockSize: 64}))

	const leases = 16
	type result struct {
		lease fleet.UIDLease
		err   error
	}
	results := make(chan result, leases)
	ctx := context.Background()
	for i := range leases {
		go func(i int) {
			got, err := reg.LeaseUIDs(ctx, uidTenant, fleet.UIDLeaseRequest{
				ProxyID:  "proxy-" + string(rune('a'+i%3)),
				Hostname: "hot.example.com", Count: 64,
				RangeMin: 2_000_000, RangeMax: 2_099_999,
			})
			results <- result{lease: got, err: err}
		}(i)
	}

	var got []block
	for range leases {
		r := <-results
		if r.err != nil {
			t.Fatalf("concurrent lease: %v", r.err)
		}
		got = append(got, block{from: r.lease.From, to: r.lease.To, lease: r.lease.LeaseID})
	}
	sort.Slice(got, func(i, j int) bool { return got[i].from < got[j].from })
	for i := 1; i < len(got); i++ {
		if overlaps(got[i-1], got[i]) {
			t.Fatalf("two concurrent leases overlap: [%d,%d) and [%d,%d)",
				got[i-1].from, got[i-1].to, got[i].from, got[i].to)
		}
	}
}

// This server states no `term_seconds` by default, and that is a decision
// rather than an omission: the term bounds only how long the PROXY keeps
// allocating, so shortening it costs availability during exactly the outage a
// held block exists to survive, and buys nothing.
func TestNoTermIsStatedUnlessOneIsConfigured(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	req := fleet.UIDLeaseRequest{
		ProxyID: "proxy-1", Hostname: "term.example.com", Count: 16,
		RangeMin: 2_000_000, RangeMax: 2_000_255,
	}

	silent, err := fleet.New(st).LeaseUIDs(ctx, uidTenant, req)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if silent.Term != 0 {
		t.Errorf("term = %s with nothing configured, want none stated so the proxy keeps its own default",
			silent.Term)
	}

	stated, err := fleet.New(st, fleet.WithUIDAllocation(fleet.UIDAllocation{Term: 6 * time.Hour})).
		LeaseUIDs(ctx, uidTenant, req)
	if err != nil {
		t.Fatalf("lease with a configured term: %v", err)
	}
	if stated.Term != 6*time.Hour {
		t.Errorf("term = %s, want the configured 6h", stated.Term)
	}
}
