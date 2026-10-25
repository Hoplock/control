// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"testing"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
)

// The declaration a proxy's build makes is recorded from the decision path, and
// the per-node memory that keeps that to one write per build change must never
// be what makes the stored copy right (M5). Two nodes share one store, and
// nothing routes a proxy to the same one twice: during a rolling upgrade and a
// rollback a proxy's calls land wherever the balancer sends them.
func TestTwoNodesConvergeOnAProxysLatestDeclaration(t *testing.T) {
	t.Parallel()
	st, nodeA, c := newRegistry(t)
	nodeB := fleet.New(st, fleet.WithClock(c.now))
	ctx := t.Context()

	before := upstreamDeclaration(t)
	after := upstreamDeclaration(t)
	after.AlgorithmFloors = after.AlgorithmFloors[:1] // a build that dropped a level

	stored := func() []contract.AlgorithmFloorCapability {
		t.Helper()
		decls, err := nodeA.AlgorithmDeclarations(ctx, testTenant)
		if err != nil || len(decls) != 1 {
			t.Fatalf("declarations = %+v (%v), want proxy-1's", decls, err)
		}
		return decls[0].Declaration.AlgorithmFloors
	}
	record := func(r *fleet.Registry, caps *contract.ProxyCapabilities) {
		t.Helper()
		if err := r.RecordAlgorithmDeclaration(ctx, testTenant, "proxy-1", caps); err != nil {
			t.Fatalf("RecordAlgorithmDeclaration: %v", err)
		}
	}

	record(nodeA, before) // the old build, on A
	record(nodeB, after)  // upgraded, on B
	record(nodeA, before) // rolled back, on A again: A last wrote this one itself
	c.advance(fleet.DeclarationRecheck)
	record(nodeA, before) // and keeps calling A

	if got := stored(); len(got) != len(before.AlgorithmFloors) {
		t.Fatalf("the store holds %d floor levels for proxy-1, which runs a build declaring %d: "+
			"node A's memory of its own last write suppressed the write that would have corrected node B's",
			len(got), len(before.AlgorithmFloors))
	}
}

// Within the recheck the memory saves the write, which is all it is for: an
// unchanged declaration on every call is one write per node per interval, not
// one per call.
func TestAnUnchangedDeclarationIsNotRewrittenOnEveryCall(t *testing.T) {
	t.Parallel()
	st, r, c := newRegistry(t)
	ctx := t.Context()
	caps := upstreamDeclaration(t)

	if err := r.RecordAlgorithmDeclaration(ctx, testTenant, "proxy-1", caps); err != nil {
		t.Fatal(err)
	}
	// Make the stored copy differ behind this node's back: a write it has
	// not made, so only a store write from this node could restore it.
	if _, err := st.ProxyAlgorithmDeclarations().Put(ctx, testTenant, store.ProxyAlgorithmDeclaration{
		ProxyID: "proxy-1", Declaration: []byte(`{}`), Digest: "elsewhere", DeclaredAt: c.now(),
	}); err != nil {
		t.Fatal(err)
	}
	c.advance(fleet.DeclarationRecheck / 2)
	if err := r.RecordAlgorithmDeclaration(ctx, testTenant, "proxy-1", caps); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ProxyAlgorithmDeclarations().List(ctx, testTenant)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v (%v)", rows, err)
	}
	if rows[0].Digest != "elsewhere" {
		t.Error("an unchanged declaration inside the recheck interval reached the store")
	}
}

// The north-bound lists read the declarations and the key exchanges a page at
// a time (M26), decoded as the whole-tenant readers decode them. A declaration
// on the page that does not decode fails the page: an outage, never a page
// short of it (M26.6, M11).
func TestObservationPagesDecodeAsTheWholeTenantReadersDo(t *testing.T) {
	t.Parallel()
	st, r, c := newRegistry(t)
	ctx := t.Context()

	for _, id := range []string{"proxy-2", "proxy-1", "proxy-3"} {
		if err := r.RecordAlgorithmDeclaration(ctx, testTenant, id, upstreamDeclaration(t)); err != nil {
			t.Fatalf("record %s: %v", id, err)
		}
	}
	first, err := r.AlgorithmDeclarationPage(ctx, testTenant, store.Page{Limit: 2})
	if err != nil || len(first.Rows) != 2 || !first.More {
		t.Fatalf("first page = %+v (%v), want two declarations and more", first, err)
	}
	whole, err := r.AlgorithmDeclarations(ctx, testTenant)
	if err != nil || len(whole) != 3 {
		t.Fatalf("whole = %+v (%v)", whole, err)
	}
	if first.Rows[0].ProxyID != "proxy-1" || first.Rows[1].ProxyID != "proxy-2" ||
		len(first.Rows[0].Declaration.AlgorithmFloors) != len(whole[0].Declaration.AlgorithmFloors) ||
		!first.Rows[0].DeclaredAt.Equal(whole[0].DeclaredAt) {
		t.Errorf("the page %+v is not the head of the whole list %+v", first.Rows, whole)
	}

	if _, err := st.ProxyAlgorithmDeclarations().Put(ctx, testTenant, store.ProxyAlgorithmDeclaration{
		ProxyID: "proxy-3", Declaration: []byte(`{"algorithm_floors":`), Digest: "broken", DeclaredAt: c.now(),
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := r.AlgorithmDeclarationPage(ctx, testTenant, store.Page{Limit: 10}); err == nil || got.Rows != nil || got.More {
		t.Errorf("a page holding an undecodable declaration answered %+v, %v; want an error and nothing else", got, err)
	}
	if got, err := r.AlgorithmDeclarationPage(ctx, testTenant, store.Page{Limit: 2}); err != nil || len(got.Rows) != 2 {
		t.Errorf("a page before the undecodable declaration answered %+v, %v; want its two", got, err)
	}

	for _, port := range []int32{2222, 22} {
		if _, err := r.ReportTargetKex(ctx, testTenant, fleet.KexObservation{
			Hostname: "db-1", Port: port, FloorMet: contract.FloorMetModernKex,
			Negotiated: contract.KexCurve25519, ObservedAt: c.now(), ReportedBy: "proxy-1",
		}); err != nil {
			t.Fatalf("report kex %d: %v", port, err)
		}
	}
	kex, err := r.TargetKexPage(ctx, testTenant, store.Page{Limit: 1})
	if err != nil || len(kex.Rows) != 1 || !kex.More {
		t.Fatalf("kex page = %+v (%v), want one and more", kex, err)
	}
	if o := kex.Rows[0]; o.Port != 22 || o.FloorMet != contract.FloorMetModernKex ||
		o.Negotiated != contract.KexCurve25519 || o.ReportedBy != "proxy-1" || !o.Fresh(c.now(), r.TargetObservationTTL()) {
		t.Errorf("kex page row = %+v, want db-1:22 as reported", o)
	}
}
