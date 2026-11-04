// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package instance

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// clock is a settable clock shared by every node of a test deployment.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// membership is a cluster's member list, shared by its nodes' coordinators.
type membership struct {
	mu      sync.Mutex
	members []ext.Node
	err     error
}

func (m *membership) set(nodes ...ext.Node) {
	m.mu.Lock()
	m.members = nodes
	m.mu.Unlock()
}

// nodeCoord is one node's view of a test cluster.
type nodeCoord struct {
	self ext.Node
	m    *membership
}

func (c nodeCoord) Node(context.Context) (ext.Node, error) { return c.self, nil }
func (c nodeCoord) Members(context.Context) ([]ext.Node, error) {
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	return slices.Clone(c.m.members), c.m.err
}
func (nodeCoord) Lead(context.Context, string) (ext.Leadership, error) {
	return nil, errors.New("not in this test")
}
func (nodeCoord) Publish(context.Context, ext.ClusterEvent) error { return nil }
func (nodeCoord) Subscribe(context.Context, string) (<-chan ext.ClusterEvent, func(), error) {
	return nil, nil, errors.New("not in this test")
}

type testNode struct {
	svc  *Service
	node ext.Node
}

// cluster builds n services over one store, one per node, sharing a clock.
func cluster(t *testing.T, st *store.Store, clk *clock, versions ...string) ([]testNode, *membership) {
	t.Helper()
	m := &membership{}
	var nodes []testNode
	var members []ext.Node
	for i, v := range versions {
		n := ext.Node{ID: ext.NodeID("node-" + string(rune('a'+i))), Version: v, StartedAt: clk.Now()}
		members = append(members, n)
		svc, err := New(Options{
			Store: st, Coordinator: nodeCoord{self: n, m: m},
			Fleet:          fleet.New(st, fleet.WithClock(clk.Now)),
			Versions:       Versions{Software: v, NorthAPI: 1, NorthAPIMin: 1},
			DisplayName:    "staging",
			ExpectedSchema: expectedSchema(t),
			ReportInterval: 10 * time.Second,
			MinInterval:    time.Nanosecond,
			Logger:         slog.New(slog.DiscardHandler),
			Now:            clk.Now,
		})
		if err != nil {
			t.Fatal(err)
		}
		svc.SetListener("north", true)
		svc.SetListener("south", true)
		nodes = append(nodes, testNode{svc: svc, node: n})
	}
	m.set(members...)
	return nodes, m
}

func expectedSchema(t *testing.T) int64 {
	t.Helper()
	all, err := store.LoadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	return all[len(all)-1].Version
}

func reportAll(t *testing.T, nodes ...testNode) {
	t.Helper()
	for _, n := range nodes {
		if err := n.svc.Report(t.Context()); err != nil {
			t.Fatalf("report %s: %v", n.node.ID, err)
		}
	}
}

func codes(rs []Reason) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.Code)
	}
	return out
}

func hasReason(rs []Reason, code, node string) bool {
	return slices.ContainsFunc(rs, func(r Reason) bool { return r.Code == code && (node == "" || r.Node == node) })
}

// One identity for the deployment, whatever the number of nodes and whatever
// its display name says; health beneath it per node.
func TestThreeNodesAreOneIdentityWithHealthPerNode(t *testing.T) {
	st := storetest.New(t)
	clk := newClock()
	nodes, _ := cluster(t, st, clk, "v0.33.0", "v0.33.0", "v0.33.0")
	reportAll(t, nodes...)

	ids := map[string]bool{}
	for _, n := range nodes {
		id, err := n.svc.Identity(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		ids[id.InstanceID] = true
	}
	if len(ids) != 1 {
		t.Fatalf("three nodes reported %d identities: %v", len(ids), ids)
	}

	// A restart with another display name: a new service over the same
	// store keeps the id.
	renamed, err := New(Options{
		Store: st, Coordinator: nodeCoord{self: nodes[0].node, m: &membership{}},
		Fleet: fleet.New(st), DisplayName: "production", Now: clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := renamed.Identity(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ids[id.InstanceID] || id.DisplayName != "production" {
		t.Fatalf("after a rename: id %q (want one of %v), name %q", id.InstanceID, ids, id.DisplayName)
	}

	h := nodes[0].svc.Health(t.Context(), nil, nil)
	if h.State != StateHealthy {
		t.Fatalf("three reporting nodes: state %s, reasons %v", h.State, h.Reasons)
	}
	if len(h.Nodes.Members) != 3 {
		t.Fatalf("members = %d, want 3 beneath one identity", len(h.Nodes.Members))
	}
}

// Degradation is legible, and two causes are not one state with one reason.
func TestDegradationNamesTheNodeAndTheCause(t *testing.T) {
	st := storetest.New(t)
	clk := newClock()
	nodes, _ := cluster(t, st, clk, "v1", "v1", "v1")
	reportAll(t, nodes...)

	// node-c stops: it writes nothing more, and the others carry on.
	clk.Advance(31 * time.Second)
	reportAll(t, nodes[0], nodes[1])
	stopped := nodes[0].svc.Health(t.Context(), nil, nil)
	if stopped.State != StateDegraded {
		t.Fatalf("one node stopped: state %s, want degraded", stopped.State)
	}
	if !hasReason(stopped.Reasons, ReasonNodeNotReporting, "node-c") {
		t.Fatalf("one node stopped: reasons %+v do not name node-c", stopped.Reasons)
	}
	if hasReason(stopped.Reasons, ReasonDatabaseUnreachable, "") {
		t.Fatalf("one node stopped reads as a database outage: %v", codes(stopped.Reasons))
	}

	// The database goes away: the same service, its store closed.
	st2 := storetest.New(t)
	down, _ := cluster(t, st2, clk, "v1")
	reportAll(t, down...)
	st2.Close()
	dbDown := down[0].svc.Health(t.Context(), nil, nil)
	if dbDown.State != StateDegraded {
		t.Fatalf("database unreachable: state %s, want degraded", dbDown.State)
	}
	if !hasReason(dbDown.Reasons, ReasonDatabaseUnreachable, "") {
		t.Fatalf("database unreachable: reasons %v", codes(dbDown.Reasons))
	}
	if hasReason(dbDown.Reasons, ReasonNodeNotReporting, "") {
		t.Fatalf("database unreachable reads as a node down: %v", codes(dbDown.Reasons))
	}
	if slices.Equal(codes(stopped.Reasons), codes(dbDown.Reasons)) {
		t.Fatalf("the two causes are collapsed into one: %v", codes(stopped.Reasons))
	}
	// And the nodes it could not read are unknown, never healthy.
	if dbDown.Nodes.State != StateUnknown {
		t.Fatalf("unreadable nodes: %s, want unknown", dbDown.Nodes.State)
	}
}

// A rolling upgrade is in progress, not a fault, and finishing it is healthy.
func TestARollingUpgradeIsInProgressNotAFault(t *testing.T) {
	st := storetest.New(t)
	clk := newClock()
	nodes, m := cluster(t, st, clk, "v1", "v1", "v2")
	reportAll(t, nodes...)

	h := nodes[0].svc.Health(t.Context(), nil, nil)
	if h.State != StateInProgress || !h.Nodes.Upgrade.InProgress {
		t.Fatalf("mixed versions: state %s, upgrade %+v", h.State, h.Nodes.Upgrade)
	}
	if !slices.Equal(h.Nodes.Upgrade.Versions, []string{"v1", "v2"}) {
		t.Fatalf("versions = %v", h.Nodes.Upgrade.Versions)
	}

	// The other two come back on v2.
	upgraded, _ := cluster(t, st, clk, "v2", "v2", "v2")
	m.set(upgraded[0].node, upgraded[1].node, upgraded[2].node)
	reportAll(t, upgraded...)
	clk.Advance(time.Second)
	h = nodes[0].svc.Health(t.Context(), nil, nil)
	if h.State != StateHealthy || h.Nodes.Upgrade.InProgress {
		t.Fatalf("after the upgrade: state %s, upgrade %+v, reasons %v", h.State, h.Nodes.Upgrade, codes(h.Reasons))
	}
}

// Absent is not healthy: a member that has not reported, no report at all,
// and a fleet that cannot be counted each have their own state.
func TestAbsentIsNeverHealthy(t *testing.T) {
	st := storetest.New(t)
	clk := newClock()
	nodes, m := cluster(t, st, clk, "v1", "v1")

	// Nobody has reported.
	h := nodes[0].svc.Health(t.Context(), nil, nil)
	if h.State == StateHealthy || h.Nodes.State != StateUnknown || h.Deployment.Listeners.State != StateUnknown {
		t.Fatalf("no reports: state %s, nodes %s, listeners %s", h.State, h.Nodes.State, h.Deployment.Listeners.State)
	}
	if h.Deployment.Listeners.ObservedAt != nil {
		t.Fatalf("listeners never observed, but carry an observation time")
	}

	// One reports; the other is a member that has not.
	reportAll(t, nodes[0])
	h = nodes[0].svc.Health(t.Context(), nil, nil)
	if h.State != StateUnknown || !hasReason(h.Reasons, ReasonNodeNotReported, "node-b") {
		t.Fatalf("a member not reported: state %s, reasons %+v", h.State, h.Reasons)
	}
	reportAll(t, nodes[1])

	// A fleet summary that cannot be computed.
	broken := storetest.New(t)
	broken.Close()
	svc, err := New(Options{
		Store: st, Coordinator: nodeCoord{self: nodes[0].node, m: m}, Fleet: fleet.New(broken),
		ExpectedSchema: expectedSchema(t), ReportInterval: 10 * time.Second, MinInterval: time.Nanosecond,
		Now: clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	h = svc.Health(t.Context(), []store.Tenant{"acme"}, nil)
	if len(h.Fleet) != 1 || h.Fleet[0].State != StateUnknown || h.Fleet[0].Summary != nil {
		t.Fatalf("uncomputable fleet: %+v", h.Fleet)
	}
	if h.State == StateHealthy {
		t.Fatalf("an uncomputable fleet rolled up to healthy")
	}
}

// Values carry their observation time, and one past its bound is stale.
func TestAValuePastItsBoundIsStale(t *testing.T) {
	st := storetest.New(t)
	clk := newClock()
	nodes, _ := cluster(t, st, clk, "v1")
	reportAll(t, nodes...)
	reportedAt := clk.Now()

	h := nodes[0].svc.Health(t.Context(), nil, nil)
	if h.Nodes.Members[0].ReportedAt == nil || !h.Nodes.Members[0].ReportedAt.Equal(reportedAt) {
		t.Fatalf("reported_at = %v, want %v", h.Nodes.Members[0].ReportedAt, reportedAt)
	}
	if h.Deployment.Database.ObservedAt == nil {
		t.Fatalf("the database check carries no observation time")
	}

	// The node's report ages past three intervals.
	clk.Advance(31 * time.Second)
	h = nodes[0].svc.Health(t.Context(), nil, nil)
	if h.Nodes.Members[0].State != StateStale {
		t.Fatalf("an old report: node state %s, want stale", h.Nodes.Members[0].State)
	}

	// And a cached answer served past its bound is stale too, not current.
	old := Health{Deployment: DeploymentChecks{
		Database:  DatabaseCheck{State: StateHealthy, ObservedAt: stamp(reportedAt), FreshForSeconds: 30},
		Listeners: Check{State: StateHealthy, ObservedAt: stamp(reportedAt), FreshForSeconds: 30},
	}, Nodes: NodesHealth{State: StateHealthy, ObservedAt: stamp(reportedAt), FreshForSeconds: 30}}
	served := applyFreshness(old, reportedAt.Add(time.Minute))
	if served.Deployment.Database.State != StateStale || served.State != StateUnknown {
		t.Fatalf("a cached healthy answer a minute old: database %s, roll-up %s", served.Deployment.Database.State, served.State)
	}
}

// Health computes at most once per its minimum interval however often it is
// asked: the maximum rate a poller can impose is the service's, not theirs.
func TestHealthComputesAtMostOncePerInterval(t *testing.T) {
	st := storetest.New(t)
	clk := newClock()
	nodes, _ := cluster(t, st, clk, "v1")
	svc := nodes[0].svc
	svc.o.MinInterval = time.Second
	reportAll(t, nodes...)

	first := svc.Health(t.Context(), nil, nil)
	at := svc.baseAt
	for range 50 {
		svc.Health(t.Context(), nil, nil)
	}
	if !svc.baseAt.Equal(at) {
		t.Fatalf("50 polls inside the interval recomputed")
	}
	clk.Advance(time.Second)
	svc.Health(t.Context(), nil, nil)
	if svc.baseAt.Equal(at) {
		t.Fatalf("a poll past the interval did not recompute")
	}
	_ = first
}

func TestEveryReasonCodeIsDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range AllReasons {
		if seen[c] {
			t.Errorf("reason %q listed twice", c)
		}
		seen[c] = true
	}
}

func TestRollupNeverTurnsAbsentIntoHealthy(t *testing.T) {
	cases := []struct {
		in   []State
		want State
	}{
		{[]State{StateHealthy, StateHealthy}, StateHealthy},
		{[]State{StateHealthy, StateUnknown}, StateUnknown},
		{[]State{StateHealthy, StateStale}, StateUnknown},
		{[]State{StateInProgress, StateHealthy}, StateInProgress},
		{[]State{StateInProgress, StateUnknown}, StateUnknown},
		{[]State{StateUnknown, StateDegraded}, StateDegraded},
		{[]State{StateLeft, StateHealthy}, StateHealthy},
		{[]State{State("something-new")}, StateUnknown},
	}
	for _, c := range cases {
		if got := rollup(c.in...); got != c.want {
			t.Errorf("rollup(%v) = %s, want %s", c.in, got, c.want)
		}
	}
}
