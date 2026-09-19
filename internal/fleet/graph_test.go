// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
)

const testTenant = store.Tenant("tenant-a")

// dial builds a live node with one dial edge per zone named.
func node(id string, zone fleet.Zone, edges ...fleet.Edge) fleet.Node {
	return fleet.Node{ProxyID: id, Zone: zone, Live: true, Edges: edges}
}

func dial(to fleet.Zone, addr string, cost int) fleet.Edge {
	return fleet.Edge{ToZone: to, Connection: contract.HopConnectionDial, Address: addr, Cost: cost}
}

func dialTo(to fleet.Zone, next, addr string, cost int) fleet.Edge {
	return fleet.Edge{
		ToZone: to, Connection: contract.HopConnectionDial,
		Address: addr, NextProxyID: next, Cost: cost,
	}
}

func relay(to fleet.Zone, cost int) fleet.Edge {
	return fleet.Edge{ToZone: to, Connection: contract.HopConnectionRelay, Cost: cost}
}

func graph(t *testing.T, nodes []fleet.Node, opts ...fleet.GraphOption) *fleet.Graph {
	t.Helper()
	g, err := fleet.NewGraph(testTenant, nodes, opts...)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return g
}

// hopIDs renders a path as the proxies it visits, which is what every assertion
// below is really about.
func hopIDs(hops []fleet.Hop) []string {
	out := make([]string, 0, len(hops))
	for _, h := range hops {
		out = append(out, h.NextProxyID)
	}
	return out
}

// chain is the topology most of these tests use: an edge proxy, a regional
// gateway, and an enclave proxy behind a relay registration — the exact shape
// proxy D11 describes.
func chain() []fleet.Node {
	edge := node("p-edge", "edge", dial("region", "region-gw:8022", 1))
	region := node("p-region", "region", relay("enclave", 1))
	region.Relays = []string{"p-enclave"}
	enclave := node("p-enclave", "enclave")
	return []fleet.Node{edge, region, enclave}
}

// A target in the asking proxy's own zone is a DIRECT route, which this package
// reports as an empty, non-nil path. 0008 turns that into route_type: direct.
func TestPathInTheEntryZoneIsDirect(t *testing.T) {
	t.Parallel()
	g := graph(t, chain())

	hops, err := g.Path(fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "edge"})
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if hops == nil {
		t.Fatal("a direct route returned a nil path; it must be empty and non-nil")
	}
	if len(hops) != 0 {
		t.Errorf("a direct route returned %d hop(s): %v", len(hops), hopIDs(hops))
	}
}

func TestPathOneHop(t *testing.T) {
	t.Parallel()
	g := graph(t, chain())

	hops, err := g.Path(fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "region"})
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got, want := hopIDs(hops), []string{"p-region"}; !slices.Equal(got, want) {
		t.Fatalf("path = %v, want %v", got, want)
	}
	if got := hops[0].Connection; got != contract.HopConnectionDial {
		t.Errorf("connection = %q, want dial", got)
	}
	if got, want := hops[0].Address, "region-gw:8022"; got != want {
		t.Errorf("address = %q, want %q", got, want)
	}
	if got, want := hops[0].FromProxyID, "p-edge"; got != want {
		t.Errorf("from = %q, want %q", got, want)
	}
}

// The two-hop case, and the one that has to cross a relay edge to get there.
func TestPathTwoHopsAcrossARelayEdge(t *testing.T) {
	t.Parallel()
	g := graph(t, chain())

	hops, err := g.Path(fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "enclave"})
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got, want := hopIDs(hops), []string{"p-region", "p-enclave"}; !slices.Equal(got, want) {
		t.Fatalf("path = %v, want %v", got, want)
	}
	if got := hops[1].Connection; got != contract.HopConnectionRelay {
		t.Fatalf("second hop connection = %q, want relay", got)
	}
	// A relay hop opens no connection, so an address on one would be a dial
	// waiting to be attempted.
	if got := hops[1].Address; got != "" {
		t.Errorf("relay hop carries address %q, want none", got)
	}
}

func TestPathThreeHops(t *testing.T) {
	t.Parallel()
	nodes := []fleet.Node{
		node("p-a", "za", dial("zb", "b:22", 1)),
		node("p-b", "zb", dial("zc", "c:22", 1)),
		node("p-c", "zc", dial("zd", "d:22", 1)),
		node("p-d", "zd"),
	}
	// Four proxies is exactly DefaultMaxHops, so this also proves the cap is a
	// bound and not an off-by-one.
	g := graph(t, nodes)

	hops, err := g.Path(fleet.EntryPoint{ProxyID: "p-a"}, fleet.Destination{Zone: "zd"})
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got, want := hopIDs(hops), []string{"p-b", "p-c", "p-d"}; !slices.Equal(got, want) {
		t.Fatalf("path = %v, want %v", got, want)
	}
}

// The assertion proxy D11 turns on: a relay edge whose downstream proxy holds no
// live registration is NOT selected, and when it is the only route the answer is
// an explicit no-path — never a dial, and never a deny.
func TestRelayEdgeWithoutRegistrationIsNotSelected(t *testing.T) {
	t.Parallel()

	nodes := chain()
	for i := range nodes {
		if nodes[i].ProxyID == "p-region" {
			nodes[i].Relays = nil // the registration dropped
		}
	}
	g := graph(t, nodes)

	hops, err := g.Path(fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "enclave"})
	if err == nil {
		t.Fatalf("Path returned %v, want a no-path outcome", hopIDs(hops))
	}
	if !fleet.IsNoPath(err) {
		t.Fatalf("Path returned %v, want a *NoPathError", err)
	}
	np, _ := fleet.NoPath(err)
	if np.Reason != fleet.NoPathNoLiveRoute {
		t.Errorf("reason = %q, want %q", np.Reason, fleet.NoPathNoLiveRoute)
	}
}

// The other half of the same rule, stated as its own test because a downgrade is
// the failure a future refactor is most likely to introduce as a convenience: it
// would "fix" an outage by opening an inbound path into a protected zone.
func TestRelayEdgeIsNeverDowngradedToADial(t *testing.T) {
	t.Parallel()

	// The relay edge carries an address, as a careless operator or a migration
	// might leave behind. It must still not be dialled.
	region := node("p-region", "region", fleet.Edge{
		ToZone:     "enclave",
		Connection: contract.HopConnectionRelay,
		Address:    "enclave-gw:8022",
		Cost:       1,
	})
	nodes := []fleet.Node{
		node("p-edge", "edge", dial("region", "region-gw:8022", 1)),
		region,
		node("p-enclave", "enclave"),
	}
	g := graph(t, nodes)

	if _, err := g.Path(fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "enclave"}); !fleet.IsNoPath(err) {
		t.Fatalf("a relay edge with an address and no registration produced %v, want no path", err)
	}

	// With the registration, the hop is a relay and still carries no address.
	region.Relays = []string{"p-enclave"}
	nodes[1] = region
	hops, err := graph(t, nodes).Path(fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "enclave"})
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got := hops[1].Connection; got != contract.HopConnectionRelay {
		t.Errorf("connection = %q, want relay", got)
	}
	if got := hops[1].Address; got != "" {
		t.Errorf("relay hop carries address %q, want none", got)
	}
}

// A no-path outcome is an OUTAGE and never a deny (M11). The assertion is about
// the type, because that is what 0008 branches on.
func TestNoPathIsAnOutageShapedError(t *testing.T) {
	t.Parallel()
	g := graph(t, []fleet.Node{node("p-a", "za")})

	_, err := g.Path(fleet.EntryPoint{ProxyID: "p-a"}, fleet.Destination{Zone: "elsewhere"})
	if !fleet.IsNoPath(err) {
		t.Fatalf("err = %v, want a no-path outcome", err)
	}
	np, ok := fleet.NoPath(err)
	if !ok {
		t.Fatal("NoPath did not extract the outcome")
	}
	if np.Destination.Zone != "elsewhere" || np.Entry.ProxyID != "p-a" {
		t.Errorf("outcome does not name what was asked: %+v", np)
	}
	if !strings.Contains(err.Error(), "no viable path") {
		t.Errorf("message %q does not say what happened", err.Error())
	}
}

// A stale proxy drops out of routing, and comes back when it heartbeats again.
// At this layer "stale" is Node.Live, which Registry.Graph computes from the
// heartbeat; the registry test asserts the other half against a real clock.
func TestStaleProxyDropsOutOfRoutingAndReturns(t *testing.T) {
	t.Parallel()
	nodes := chain()

	for i := range nodes {
		if nodes[i].ProxyID == "p-region" {
			nodes[i].Live = false
		}
	}
	if _, err := graph(t, nodes).Path(fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "region"}); !fleet.IsNoPath(err) {
		t.Fatalf("a stale proxy was still routed through: %v", err)
	}

	for i := range nodes {
		if nodes[i].ProxyID == "p-region" {
			nodes[i].Live = true
		}
	}
	hops, err := graph(t, nodes).Path(fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "region"})
	if err != nil {
		t.Fatalf("after heartbeating again: %v", err)
	}
	if got, want := hopIDs(hops), []string{"p-region"}; !slices.Equal(got, want) {
		t.Errorf("path = %v, want %v", got, want)
	}
}

// A stale ENTRY proxy is its own outcome: it is enrolled, so this is not
// "unknown", and answering it a route would be routing through a proxy this
// server has not heard from.
func TestStaleEntryProxyIsItsOwnOutcome(t *testing.T) {
	t.Parallel()
	nodes := chain()
	nodes[0].Live = false

	_, err := graph(t, nodes).Path(fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "region"})
	np, ok := fleet.NoPath(err)
	if !ok {
		t.Fatalf("err = %v, want a no-path outcome", err)
	}
	if np.Reason != fleet.NoPathEntryNotLive {
		t.Errorf("reason = %q, want %q", np.Reason, fleet.NoPathEntryNotLive)
	}
}

// A cyclic graph never yields a path containing a repeat.
func TestCyclicGraphYieldsNoRepeatedProxy(t *testing.T) {
	t.Parallel()
	nodes := []fleet.Node{
		node("p-a", "za", dial("zb", "b:22", 1)),
		node("p-b", "zb", dial("za", "a:22", 1), dial("zc", "c:22", 1)),
		node("p-c", "zc", dial("za", "a:22", 1), dial("zb", "b:22", 1)),
	}
	g := graph(t, nodes)

	hops, err := g.Path(fleet.EntryPoint{ProxyID: "p-a"}, fleet.Destination{Zone: "zc"})
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	seen := map[string]bool{"p-a": true}
	for _, h := range hops {
		if seen[h.NextProxyID] {
			t.Fatalf("path %v repeats %q", hopIDs(hops), h.NextProxyID)
		}
		seen[h.NextProxyID] = true
	}

	// And a cycle with no exit answers no-path rather than spinning.
	closed := []fleet.Node{
		node("p-a", "za", dial("zb", "b:22", 1)),
		node("p-b", "zb", dial("za", "a:22", 1)),
	}
	if _, err := graph(t, closed).Path(fleet.EntryPoint{ProxyID: "p-a"}, fleet.Destination{Zone: "zc"}); !fleet.IsNoPath(err) {
		t.Fatalf("a closed cycle produced %v, want no path", err)
	}
}

// The trail the session already carries is not advice: it removes proxies from
// the search, and a trail naming the asking proxy is a loop that has already
// happened.
func TestIncomingTrailIsHonoured(t *testing.T) {
	t.Parallel()
	g := graph(t, chain())

	t.Run("the asking proxy in its own trail is a loop", func(t *testing.T) {
		_, err := g.Path(
			fleet.EntryPoint{ProxyID: "p-region", HopTrail: []string{"p-edge", "p-region"}},
			fleet.Destination{Zone: "enclave"},
		)
		np, ok := fleet.NoPath(err)
		if !ok {
			t.Fatalf("err = %v, want a no-path outcome", err)
		}
		if np.Reason != fleet.NoPathLoop {
			t.Errorf("reason = %q, want %q", np.Reason, fleet.NoPathLoop)
		}
	})

	t.Run("a proxy in the trail is not revisited", func(t *testing.T) {
		// p-region asks for itself on the second leg, having come from p-edge.
		// Its route to the enclave must not go back out through p-edge.
		hops, err := g.Path(
			fleet.EntryPoint{ProxyID: "p-region", HopTrail: []string{"p-edge"}},
			fleet.Destination{Zone: "enclave"},
		)
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		if slices.Contains(hopIDs(hops), "p-edge") {
			t.Errorf("path %v revisits a proxy already in the trail", hopIDs(hops))
		}
	})
}

// Each hop asks for itself, so the same login and target answer differently at
// the edge and behind it (M6, proxy D2). Answering the edge's route to the inner
// proxy would build a loop.
func TestEachHopIsComputedFromTheAskingProxy(t *testing.T) {
	t.Parallel()
	g := graph(t, chain())
	dest := fleet.Destination{Zone: "enclave", Hostname: "db-1.enclave"}

	fromEdge, err := g.Path(fleet.EntryPoint{ProxyID: "p-edge"}, dest)
	if err != nil {
		t.Fatalf("from the edge: %v", err)
	}
	fromRegion, err := g.Path(
		fleet.EntryPoint{ProxyID: "p-region", HopTrail: []string{"p-edge"}}, dest)
	if err != nil {
		t.Fatalf("from the region gateway: %v", err)
	}

	if len(fromEdge) != 2 {
		t.Errorf("the edge sees %d hop(s), want 2", len(fromEdge))
	}
	if len(fromRegion) != 1 {
		t.Errorf("the inner proxy sees %d hop(s), want 1", len(fromRegion))
	}
	if got, want := fromRegion[0].FromProxyID, "p-region"; got != want {
		t.Errorf("the inner proxy's hop starts at %q, want %q", got, want)
	}
}

// An over-long path is refused rather than answered, because the proxy enforces
// the same cap and a route it refuses is an outage in front of a user.
func TestOverLongPathIsRefused(t *testing.T) {
	t.Parallel()
	nodes := []fleet.Node{
		node("p-a", "za", dial("zb", "b:22", 1)),
		node("p-b", "zb", dial("zc", "c:22", 1)),
		node("p-c", "zc", dial("zd", "d:22", 1)),
		node("p-d", "zd", dial("ze", "e:22", 1)),
		node("p-e", "ze"),
	}

	// Five proxies against a cap of four.
	_, err := graph(t, nodes).Path(fleet.EntryPoint{ProxyID: "p-a"}, fleet.Destination{Zone: "ze"})
	np, ok := fleet.NoPath(err)
	if !ok {
		t.Fatalf("err = %v, want a no-path outcome", err)
	}
	if np.Reason != fleet.NoPathMaxHops {
		t.Errorf("reason = %q, want %q", np.Reason, fleet.NoPathMaxHops)
	}

	// Raising the cap makes the same topology routable, which is what proves the
	// refusal was the cap and not the graph.
	if _, err := graph(t, nodes, fleet.WithMaxHops(5)).Path(
		fleet.EntryPoint{ProxyID: "p-a"}, fleet.Destination{Zone: "ze"}); err != nil {
		t.Errorf("with a cap of 5: %v", err)
	}
}

// The trail already traversed spends the hop budget, so a chain that started
// elsewhere has less of it left.
func TestTheIncomingTrailSpendsTheHopBudget(t *testing.T) {
	t.Parallel()
	nodes := []fleet.Node{
		node("p-a", "za", dial("zb", "b:22", 1)),
		node("p-b", "zb", dial("zc", "c:22", 1)),
		node("p-c", "zc", dial("zd", "d:22", 1)),
		node("p-d", "zd"),
	}
	g := graph(t, nodes)

	// From p-b with two proxies behind it, the chain would be five long.
	_, err := g.Path(
		fleet.EntryPoint{ProxyID: "p-b", HopTrail: []string{"p-x", "p-y"}},
		fleet.Destination{Zone: "zd"},
	)
	np, ok := fleet.NoPath(err)
	if !ok {
		t.Fatalf("err = %v, want a no-path outcome", err)
	}
	if np.Reason != fleet.NoPathMaxHops {
		t.Errorf("reason = %q, want %q", np.Reason, fleet.NoPathMaxHops)
	}
}

// Equal-cost paths resolve deterministically, so two nodes answering the same
// request agree. The tiebreak is (cost, hop count, proxy-id sequence).
func TestEqualCostPathsResolveDeterministically(t *testing.T) {
	t.Parallel()

	// Zone `mid` holds two equally good members, each with its own onward edge.
	build := func(order []string) []fleet.Node {
		byID := map[string]fleet.Node{
			"p-entry":    node("p-entry", "za", dial("mid", "mid:22", 1)),
			"p-mid-a":    node("p-mid-a", "mid", dialTo("zb", "p-target-a", "ta:22", 1)),
			"p-mid-b":    node("p-mid-b", "mid", dialTo("zb", "p-target-b", "tb:22", 1)),
			"p-target-a": node("p-target-a", "zb"),
			"p-target-b": node("p-target-b", "zb"),
		}
		out := make([]fleet.Node, 0, len(order))
		for _, id := range order {
			out = append(out, byID[id])
		}
		return out
	}

	orders := [][]string{
		{"p-entry", "p-mid-a", "p-mid-b", "p-target-a", "p-target-b"},
		{"p-target-b", "p-mid-b", "p-target-a", "p-mid-a", "p-entry"},
		{"p-mid-b", "p-entry", "p-target-b", "p-mid-a", "p-target-a"},
	}

	var first []string
	for i, order := range orders {
		hops, err := graph(t, build(order)).Path(
			fleet.EntryPoint{ProxyID: "p-entry"}, fleet.Destination{Zone: "zb"})
		if err != nil {
			t.Fatalf("order %d: %v", i, err)
		}
		got := hopIDs(hops)
		if i == 0 {
			first = got
			continue
		}
		if !slices.Equal(got, first) {
			t.Fatalf("order %d answered %v, order 0 answered %v: the tiebreak is not stable", i, got, first)
		}
	}
	// And the tiebreak is the documented one: the lexicographically smallest
	// proxy-id sequence.
	if want := []string{"p-mid-a", "p-target-a"}; !slices.Equal(first, want) {
		t.Errorf("path = %v, want %v (the stable tiebreak)", first, want)
	}
}

// Cost wins before the tiebreak: a dearer two-hop route loses to a cheaper one
// even when its ids sort first.
func TestCheaperPathWinsOverTheTiebreak(t *testing.T) {
	t.Parallel()
	nodes := []fleet.Node{
		node("p-entry", "za",
			dialTo("mid", "p-mid-a", "a:22", 10),
			dialTo("mid", "p-mid-b", "b:22", 1),
		),
		node("p-mid-a", "mid", dial("zb", "t:22", 1)),
		node("p-mid-b", "mid", dial("zb", "t:22", 1)),
		node("p-target", "zb"),
	}

	hops, err := graph(t, nodes).Path(fleet.EntryPoint{ProxyID: "p-entry"}, fleet.Destination{Zone: "zb"})
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got, want := hopIDs(hops), []string{"p-mid-b", "p-target"}; !slices.Equal(got, want) {
		t.Errorf("path = %v, want %v: the cheaper route must win", got, want)
	}
}

// An edge names a zone, and a zone can hold a member with no onward reachability
// beside one that has it. Resolving the edge to a single proxy would dead-end.
func TestAnEdgeBranchesOverEveryViableMemberOfTheZone(t *testing.T) {
	t.Parallel()
	nodes := []fleet.Node{
		node("p-entry", "za", dial("mid", "mid:22", 1)),
		// Sorts first and reaches nothing.
		node("p-mid-a", "mid"),
		node("p-mid-b", "mid", dial("zb", "t:22", 1)),
		node("p-target", "zb"),
	}

	hops, err := graph(t, nodes).Path(fleet.EntryPoint{ProxyID: "p-entry"}, fleet.Destination{Zone: "zb"})
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got, want := hopIDs(hops), []string{"p-mid-b", "p-target"}; !slices.Equal(got, want) {
		t.Errorf("path = %v, want %v", got, want)
	}
}

// An unknown asking proxy is its own outcome, and it is NOT widened into a
// lookup that could find another tenant's proxy (M18).
func TestUnknownEntryProxyIsItsOwnOutcome(t *testing.T) {
	t.Parallel()
	g := graph(t, chain())

	for _, id := range []string{"", "p-nobody"} {
		_, err := g.Path(fleet.EntryPoint{ProxyID: id}, fleet.Destination{Zone: "region"})
		np, ok := fleet.NoPath(err)
		if !ok {
			t.Fatalf("proxy %q: err = %v, want a no-path outcome", id, err)
		}
		if np.Reason != fleet.NoPathEntryUnknown {
			t.Errorf("proxy %q: reason = %q, want %q", id, np.Reason, fleet.NoPathEntryUnknown)
		}
	}
}

// The hop trail the metadata carries names every proxy in the chain, because a
// hop that saw a shorter trail than the truth cannot detect the loop it is about
// to close.
func TestTrailNamesTheWholeChain(t *testing.T) {
	t.Parallel()
	g := graph(t, chain())

	entry := fleet.EntryPoint{ProxyID: "p-region", HopTrail: []string{"p-edge"}}
	hops, err := g.Path(entry, fleet.Destination{Zone: "enclave"})
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := []string{"p-edge", "p-region", "p-enclave"}
	if got := fleet.Trail(entry, hops); !slices.Equal(got, want) {
		t.Errorf("Trail = %v, want %v", got, want)
	}
}

// A pinned next proxy that no longer serves the zone the edge names is not
// routed over: an operator who moved a proxy between zones has an edge that no
// longer means what it says.
func TestPinnedNextProxyMustStillServeTheZone(t *testing.T) {
	t.Parallel()
	nodes := []fleet.Node{
		node("p-entry", "za", dialTo("zb", "p-moved", "m:22", 1)),
		node("p-moved", "somewhere-else"),
	}
	if _, err := graph(t, nodes).Path(
		fleet.EntryPoint{ProxyID: "p-entry"}, fleet.Destination{Zone: "zb"}); !fleet.IsNoPath(err) {
		t.Fatalf("err = %v, want no path", err)
	}
}

// A duplicate proxy id is refused rather than resolved: two rows claiming one id
// is a registry that cannot say where a session went.
func TestNewGraphRefusesADuplicateProxy(t *testing.T) {
	t.Parallel()
	_, err := fleet.NewGraph(testTenant, []fleet.Node{node("p-a", "za"), node("p-a", "zb")})
	if err == nil {
		t.Fatal("NewGraph accepted two nodes with one id")
	}
	if !strings.Contains(err.Error(), "twice") {
		t.Errorf("err = %v, want it to name the duplicate", err)
	}
}

func TestNewGraphRequiresATenant(t *testing.T) {
	t.Parallel()
	if _, err := fleet.NewGraph("", []fleet.Node{node("p-a", "za")}); err == nil {
		t.Fatal("NewGraph accepted an empty tenant")
	}
}

// The search bound answers rather than running (M5), and it says so.
func TestSearchBoundAnswersRatherThanRunning(t *testing.T) {
	t.Parallel()
	// A dense mesh with no route to the destination: every proxy reaches every
	// other zone, so the loop-free search has plenty to explore.
	var nodes []fleet.Node
	ids := []string{"p-1", "p-2", "p-3", "p-4", "p-5", "p-6"}
	for i, id := range ids {
		var edges []fleet.Edge
		for j := range ids {
			if i == j {
				continue
			}
			edges = append(edges, dial(fleet.Zone("z"+ids[j]), "x:22", 1))
		}
		nodes = append(nodes, node(id, fleet.Zone("z"+id), edges...))
	}

	_, err := graph(t, nodes, fleet.WithMaxHops(6), fleet.WithSearchBound(5)).Path(
		fleet.EntryPoint{ProxyID: "p-1"}, fleet.Destination{Zone: "unreachable"})
	np, ok := fleet.NoPath(err)
	if !ok {
		t.Fatalf("err = %v, want a no-path outcome", err)
	}
	if np.Reason != fleet.NoPathSearchBound {
		t.Errorf("reason = %q, want %q", np.Reason, fleet.NoPathSearchBound)
	}
}

// A Graph is a value: editing the slices a caller kept must not change a route.
func TestGraphDoesNotAliasItsInput(t *testing.T) {
	t.Parallel()
	nodes := chain()
	g := graph(t, nodes)

	for i := range nodes {
		nodes[i].Edges = nil
		nodes[i].Relays = nil
	}

	hops, err := g.Path(fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "enclave"})
	if err != nil {
		t.Fatalf("Path after the caller cleared its own slices: %v", err)
	}
	if len(hops) != 2 {
		t.Errorf("path = %v, want two hops", hopIDs(hops))
	}
}

// A repeat anywhere in the incoming trail is a loop that has already happened.
// Collapsing a duplicate into one entry would let a malformed trail buy hop
// budget, and the trail may only ever narrow.
func TestARepeatedTrailEntryIsALoop(t *testing.T) {
	t.Parallel()
	g := graph(t, chain())

	_, err := g.Path(
		fleet.EntryPoint{ProxyID: "p-region", HopTrail: []string{"p-x", "p-x"}},
		fleet.Destination{Zone: "enclave"},
	)
	np, ok := fleet.NoPath(err)
	if !ok {
		t.Fatalf("err = %v, want a no-path outcome", err)
	}
	if np.Reason != fleet.NoPathLoop {
		t.Errorf("reason = %q, want %q", np.Reason, fleet.NoPathLoop)
	}
}
