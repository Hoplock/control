// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The shared event plane (0027): several buses over one store, woken through a
// coordinator that fans out between them, serve one stream whichever bus a
// proxy reaches. Each test here is one of 0009's multi-node prerequisites.

// cluster is a deployment of nodes in one process: one store, and one in-process
// bus that every node's coordinator view publishes to and subscribes on, which
// is exactly what a clustered coordinator gives a node — a bus every other node
// is on.
type cluster struct {
	t   *testing.T
	st  *store.Store
	bus *extdefault.SingleNode
}

func newCluster(t *testing.T) *cluster {
	t.Helper()
	bus := extdefault.NewSingleNode(ext.Node{ID: "shared"})
	t.Cleanup(bus.Close)
	return &cluster{t: t, st: storetest.New(t), bus: bus}
}

// view is one node's coordinator: the shared bus, under the node's own name.
type view struct {
	ext.ClusterCoordinator
	id ext.NodeID
}

func (v view) Node(context.Context) (ext.Node, error) { return ext.Node{ID: v.id}, nil }

// node starts one bus of the deployment. A test that wants a coordinator other
// than the shared bus passes one.
func (c *cluster) node(id string, o revoke.Options, coord ...ext.ClusterCoordinator) *revoke.Bus {
	c.t.Helper()
	o.Store, o.Node = c.st, id
	o.Coordinator = view{ClusterCoordinator: c.bus, id: ext.NodeID(id)}
	if len(coord) > 0 {
		o.Coordinator = coord[0]
	}
	if o.HeartbeatInterval == 0 {
		o.HeartbeatInterval = time.Second
	}
	if o.SyncInterval == 0 {
		// Long, so a delivery inside a test's patience came from the
		// wake-up; a test of the sync path sets its own.
		o.SyncInterval = time.Hour
	}
	b, err := revoke.New(o)
	if err != nil {
		c.t.Fatalf("revoke.New: %v", err)
	}
	b.Start(c.t.Context())
	c.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.Close(ctx)
	})
	return b
}

// on subscribes a proxy on one node, in a tenant, and waits until it is
// registered there.
func on(t *testing.T, b *revoke.Bus, tenant store.Tenant, proxyID, lastEventID string) *reader {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &reader{
		t:      t,
		cancel: cancel,
		events: make(chan *contract.RevocationEvent, 256),
		done:   make(chan error, 1),
	}
	go func() {
		r.done <- b.Subscribe(ctx, tenant, proxyID, lastEventID, func(ev *contract.RevocationEvent) error {
			r.events <- ev
			return nil
		})
	}()
	waitFor(t, func() bool {
		_, ok, err := b.LiveSubscription(context.Background(), tenant, proxyID)
		return err == nil && ok
	}, "subscription for "+proxyID+" to register")
	t.Cleanup(cancel)
	return r
}

// first returns the first line a subscription is sent, heartbeat or not.
func (r *reader) first(d time.Duration) *contract.RevocationEvent {
	r.t.Helper()
	select {
	case ev := <-r.events:
		return ev
	case <-time.After(d):
		r.t.Fatalf("nothing arrived inside %s", d)
		return nil
	}
}

// gone waits for a subscription to end and be forgotten on its node.
func gone(t *testing.T, b *revoke.Bus, r *reader, tenant store.Tenant, proxyID string) {
	t.Helper()
	r.cancel()
	waitFor(t, func() bool {
		_, ok, err := b.LiveSubscription(context.Background(), tenant, proxyID)
		return err == nil && !ok
	}, "the subscription for "+proxyID+" to end")
}

const (
	tenantX = store.Tenant("tenant-x")
	tenantY = store.Tenant("tenant-y")
)

// 1. Cross-node fan-out. A session_kill and a config_changed published on node
// A reach the proxy whose stream node B holds, and the subscriber on A too; the
// receipt counts only A's, which is what it says it counts.
func TestAnEventPublishedOnOneNodeReachesASubscriberOnAnother(t *testing.T) {
	t.Parallel()
	c := newCluster(t)
	a, b := c.node("node-a", revoke.Options{}), c.node("node-b", revoke.Options{})
	onB := on(t, b, tenantX, "proxy-b", "")
	onA := on(t, a, tenantX, "proxy-a", "")

	rcpt, err := a.Kill(t.Context(), tenantX, revoke.Everyone(), kill("alice@example.com"))
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if rcpt.Delivered != 1 {
		t.Errorf("the receipt counted %d, want node A's one subscription", rcpt.Delivered)
	}
	for name, r := range map[string]*reader{"proxy-b on node B": onB, "proxy-a on node A": onA} {
		got := r.next(2 * time.Second)
		if got.Type != contract.EventTypeSessionKill || got.SessionKill.Subject != "alice@example.com" || got.EventID != rcpt.EventID {
			t.Fatalf("%s got %+v, want the kill published on node A as %s", name, got, rcpt.EventID)
		}
	}

	ev := contract.ConfigChangedEvent{Version: "v7", Hash: "sha256:" + strings.Repeat("b", 64)}
	if _, err := a.ConfigChanged(t.Context(), tenantX, "proxy-b", ev); err != nil {
		t.Fatalf("ConfigChanged: %v", err)
	}
	if got := onB.next(2 * time.Second); got.Type != contract.EventTypeConfigChanged || *got.ConfigChanged != ev {
		t.Fatalf("proxy-b got %+v, want the config_changed naming %+v", got, ev)
	}
	onA.nothing(150 * time.Millisecond)
}

// 2. One order, deployment-wide. Two nodes publishing at once: every subscriber,
// on either node, sees every event once, in one order, with ids that only move
// forward — so an id minted by either node is a resume point on both.
func TestConcurrentPublishesOnTwoNodesArriveInOneOrder(t *testing.T) {
	t.Parallel()
	c := newCluster(t)
	a, b := c.node("node-a", revoke.Options{}), c.node("node-b", revoke.Options{})
	onA := on(t, a, tenantX, "proxy-a", "")
	onB := on(t, b, tenantX, "proxy-b", "")

	const each = 25
	var wg sync.WaitGroup
	for name, n := range map[string]*revoke.Bus{"a": a, "b": b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				if _, err := n.Invalidate(context.Background(), tenantX, revoke.Everyone(),
					revoke.Invalidation{Keys: []string{fmt.Sprintf("%s-%d", name, i)}}); err != nil {
					t.Errorf("Invalidate on %s: %v", name, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	read := func(r *reader) (keys, ids []string) {
		for range 2 * each {
			ev := r.next(3 * time.Second)
			keys, ids = append(keys, ev.CacheInvalidate.Keys[0]), append(ids, ev.EventID)
		}
		r.nothing(100 * time.Millisecond)
		return keys, ids
	}
	keysA, idsA := read(onA)
	keysB, idsB := read(onB)
	if !slices.Equal(keysA, keysB) || !slices.Equal(idsA, idsB) {
		t.Fatalf("the two nodes delivered different orders:\nA %v\nB %v", keysA, keysB)
	}
	if !slices.IsSorted(idsA) || len(slices.Compact(slices.Clone(idsA))) != len(idsA) {
		t.Fatalf("ids did not strictly advance: %v", idsA)
	}
}

// 3. Durable replay with a known floor. A proxy that disconnects from node A and
// reconnects to node B with A's id is replayed exactly what it missed, published
// on either node; one resuming from a heartbeat A sent it is replayed from that
// point too; and one whose id is below the floor gets `resync` first and
// nothing older.
func TestAReconnectToAnotherNodeReplaysWhatItMissedOrResyncs(t *testing.T) {
	t.Parallel()
	c := newCluster(t)
	a := c.node("node-a", revoke.Options{HeartbeatInterval: 50 * time.Millisecond, ReplayBuffer: 3})
	b := c.node("node-b", revoke.Options{HeartbeatInterval: 50 * time.Millisecond, ReplayBuffer: 3})

	first := on(t, a, tenantX, "proxy-1", "")
	if _, err := a.Invalidate(t.Context(), tenantX, revoke.ToProxy("proxy-1"), revoke.Invalidation{Keys: []string{"k1"}}); err != nil {
		t.Fatal(err)
	}
	seen := first.next(2 * time.Second).EventID
	// A heartbeat after it: the id a proxy actually holds most of the time.
	var beat string
	for beat == "" {
		if ev := first.first(2 * time.Second); ev.Type == contract.EventTypeHeartbeat {
			beat = ev.EventID
		}
	}
	gone(t, a, first, tenantX, "proxy-1")

	// Published into the gap, on both nodes, and one for another proxy.
	if _, err := b.Invalidate(t.Context(), tenantX, revoke.ToProxy("proxy-1"), revoke.Invalidation{Keys: []string{"k2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invalidate(t.Context(), tenantX, revoke.ToProxy("proxy-2"), revoke.Invalidation{Keys: []string{"theirs"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invalidate(t.Context(), tenantX, revoke.ToProxy("proxy-1"), revoke.Invalidation{Keys: []string{"k3"}}); err != nil {
		t.Fatal(err)
	}

	for name, id := range map[string]string{"the last event": seen, "a heartbeat": beat} {
		t.Run("resuming on node B from "+name, func(t *testing.T) {
			resumed := on(t, b, tenantX, "proxy-1", id)
			for _, want := range []string{"k2", "k3"} {
				got := resumed.next(2 * time.Second)
				if got.Type == contract.EventTypeResync {
					t.Fatalf("resynced although %s was still held", want)
				}
				if got.CacheInvalidate.Keys[0] != want {
					t.Fatalf("replayed %+v, want %s", got.CacheInvalidate, want)
				}
			}
			resumed.nothing(100 * time.Millisecond)
			gone(t, b, resumed, tenantX, "proxy-1")
		})
	}

	// Past a replay buffer of three, k1's id is below the floor.
	for i := range 3 {
		if _, err := b.Invalidate(t.Context(), tenantX, revoke.ToProxy("proxy-1"), revoke.Invalidation{Keys: []string{fmt.Sprintf("later-%d", i)}}); err != nil {
			t.Fatal(err)
		}
	}
	stale := on(t, b, tenantX, "proxy-1", seen)
	if ev := stale.first(2 * time.Second); ev.Type != contract.EventTypeResync {
		t.Fatalf("the first line was %s (%s), want resync and nothing older", ev.Type, ev.EventID)
	}
	stale.nothing(150 * time.Millisecond)
}

// 3, across a restart. The log outlives the process, so a deploy is a reconnect
// that replays what was missed — not a fleet-wide cache flush — and a proxy that
// missed nothing is replayed nothing and told nothing.
func TestARestartReplaysRatherThanResyncs(t *testing.T) {
	t.Parallel()
	c := newCluster(t)
	before := c.node("node-a", revoke.Options{})
	r := on(t, before, tenantX, "proxy-1", "")
	rcpt, err := before.Invalidate(t.Context(), tenantX, revoke.Everyone(), revoke.Invalidation{Keys: []string{"k1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.next(2 * time.Second); got.EventID != rcpt.EventID {
		t.Fatalf("got %s, want %s", got.EventID, rcpt.EventID)
	}
	r.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := before.Close(ctx); err != nil {
		t.Fatal(err)
	}

	after := c.node("node-a", revoke.Options{})
	caughtUp := on(t, after, tenantX, "proxy-2", rcpt.EventID)
	caughtUp.nothing(150 * time.Millisecond)

	if _, err := after.Invalidate(t.Context(), tenantX, revoke.ToProxy("proxy-1"), revoke.Invalidation{Keys: []string{"k2"}}); err != nil {
		t.Fatal(err)
	}
	resumed := on(t, after, tenantX, "proxy-1", rcpt.EventID)
	got := resumed.next(2 * time.Second)
	if got.Type == contract.EventTypeResync || got.CacheInvalidate.Keys[0] != "k2" {
		t.Fatalf("after a restart the proxy got %+v, want k2 replayed", got)
	}
}

// failingCoordinator is a node whose wake-ups never leave it: the node died
// between logging an event and telling the others.
type failingCoordinator struct{ view }

func (failingCoordinator) Publish(context.Context, ext.ClusterEvent) error {
	return errors.New("the node died before it woke anyone")
}

// 5. Failover. A node that logged an event and died before waking anyone has
// still published it: the receipt it returned was for a logged event, the node
// holding the proxy's stream delivers it at its next sync, and the proxy whose
// stream the dead node held reconnects elsewhere and is replayed it.
func TestANodeThatDiesAfterLoggingLosesNoAcknowledgedEvent(t *testing.T) {
	t.Parallel()
	c := newCluster(t)
	dying := c.node("node-a", revoke.Options{}, failingCoordinator{view{c.bus, "node-a"}})
	survivor := c.node("node-b", revoke.Options{SyncInterval: 50 * time.Millisecond})

	onDying := on(t, dying, tenantX, "proxy-a", "")
	onSurvivor := on(t, survivor, tenantX, "proxy-b", "")
	if _, err := dying.Invalidate(t.Context(), tenantX, revoke.ToProxy("proxy-a"), revoke.Invalidation{Keys: []string{"before"}}); err != nil {
		t.Fatal(err)
	}
	last := onDying.next(2 * time.Second).EventID

	rcpt, err := dying.Kill(t.Context(), tenantX, revoke.Everyone(), kill("alice@example.com"))
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	// The node dies: its streams are cut without a drain.
	onDying.cancel()

	if got := onSurvivor.next(2 * time.Second); got.EventID != rcpt.EventID {
		t.Fatalf("the survivor delivered %s, want the acknowledged kill %s", got.EventID, rcpt.EventID)
	}
	resumed := on(t, survivor, tenantX, "proxy-a", last)
	got := resumed.next(2 * time.Second)
	if got.Type != contract.EventTypeSessionKill || got.EventID != rcpt.EventID {
		t.Fatalf("proxy-a reconnecting to the survivor got %+v, want the kill replayed", got)
	}
}

// droppingCoordinator lets a test drop a node's subscription to the bus, as a
// coordinator does to a node that fell behind or lost the other nodes.
type droppingCoordinator struct {
	view
	mu      sync.Mutex
	cancels []func()
}

func (d *droppingCoordinator) Subscribe(ctx context.Context, topic string) (<-chan ext.ClusterEvent, func(), error) {
	ch, cancel, err := d.view.Subscribe(ctx, topic)
	if err == nil {
		d.mu.Lock()
		d.cancels = append(d.cancels, cancel)
		d.mu.Unlock()
	}
	return ch, cancel, err
}

func (d *droppingCoordinator) drop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, cancel := range d.cancels {
		cancel()
	}
	d.cancels = nil
}

func (d *droppingCoordinator) subscriptions() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.cancels)
}

// A node the coordinator drops subscribes again and reads the log for what it
// missed: dropped means closed, and closed means "read the record".
func TestANodeDroppedFromTheBusResubscribesAndCatchesUp(t *testing.T) {
	t.Parallel()
	c := newCluster(t)
	a := c.node("node-a", revoke.Options{})
	coord := &droppingCoordinator{view: view{c.bus, "node-b"}}
	b := c.node("node-b", revoke.Options{}, coord)
	r := on(t, b, tenantX, "proxy-b", "")
	waitFor(t, func() bool { return coord.subscriptions() == 1 }, "node B to subscribe to the bus")

	coord.drop()
	if _, err := a.Invalidate(t.Context(), tenantX, revoke.Everyone(), revoke.Invalidation{Keys: []string{"while-dropped"}}); err != nil {
		t.Fatal(err)
	}
	if got := r.next(3 * time.Second); got.CacheInvalidate.Keys[0] != "while-dropped" {
		t.Fatalf("got %+v, want the event published while node B was off the bus", got.CacheInvalidate)
	}
	waitFor(t, func() bool { return coord.subscriptions() == 1 }, "node B to subscribe again")
}

// 4. Cluster-wide subscription state. The cache-hint gate on node A sees a
// stream node B holds within two sync intervals, and stops seeing it within two
// more once it ends; this test reports how long each took.
func TestTheHintGateSeesASubscriptionHeldOnAnotherNode(t *testing.T) {
	t.Parallel()
	const sync = 100 * time.Millisecond
	c := newCluster(t)
	a := c.node("node-a", revoke.Options{SyncInterval: sync})
	b := c.node("node-b", revoke.Options{SyncInterval: sync})

	// Asking is what puts the tenant in A's copy.
	if _, ok, _ := a.LiveSubscription(t.Context(), tenantX, "proxy-b"); ok {
		t.Fatal("node A sees a subscription nobody holds")
	}
	start := time.Now()
	r := on(t, b, tenantX, "proxy-b", "")
	waitFor(t, func() bool {
		seen, ok, err := a.LiveSubscription(context.Background(), tenantX, "proxy-b")
		return err == nil && ok && !seen.IsZero()
	}, "node A to see node B's subscription")
	appeared := time.Since(start)
	live, err := a.LiveSubscriptions(t.Context(), tenantX)
	if _, ok := live["proxy-b"]; err != nil || !ok {
		t.Fatalf("node A's whole-tenant read is %v (%v), want proxy-b in it", live, err)
	}

	start = time.Now()
	r.cancel()
	waitFor(t, func() bool {
		_, ok, err := a.LiveSubscription(context.Background(), tenantX, "proxy-b")
		return err == nil && !ok
	}, "node A to stop seeing it")
	disappeared := time.Since(start)
	t.Logf("node A saw node B's subscription after %s and lost it %s after it ended (sync %s, stated bound %s)",
		appeared, disappeared, sync, 2*sync)
	if slack := 2*sync + 500*time.Millisecond; appeared > slack || disappeared > slack {
		t.Errorf("seen after %s, lost after %s: past two sync intervals and slack (%s)", appeared, disappeared, slack)
	}
}

// Tenancy (M18), on every node: an event published in one tenant reaches no
// subscription of another, on the node it was published on or any other; one
// tenant's subscriptions are not in another's copy; and an id from one tenant's
// stream is unknown in another's.
func TestOneTenantsEventsNeverReachAnothersSubscriptionOnAnyNode(t *testing.T) {
	t.Parallel()
	c := newCluster(t)
	a := c.node("node-a", revoke.Options{SyncInterval: 50 * time.Millisecond})
	b := c.node("node-b", revoke.Options{SyncInterval: 50 * time.Millisecond})

	xOnB := on(t, b, tenantX, "proxy-1", "")
	yOnB := on(t, b, tenantY, "proxy-1", "")
	yOnA := on(t, a, tenantY, "proxy-2", "")

	rcpt, err := a.Kill(t.Context(), tenantX, revoke.Everyone(), revoke.Kill{All: true, Reason: "Tenant X lockdown."})
	if err != nil {
		t.Fatal(err)
	}
	if got := xOnB.next(2 * time.Second); got.EventID != rcpt.EventID {
		t.Fatalf("tenant X's subscriber got %+v", got)
	}
	yOnB.nothing(200 * time.Millisecond)
	yOnA.nothing(50 * time.Millisecond)

	// Node A holds no proxy-1 of its own: what it sees of proxy-1 is its copy
	// of node B's, kept per tenant, so tenant Y's ending is seen while tenant
	// X's, under the same proxy id, is not touched.
	for _, tenant := range []store.Tenant{tenantX, tenantY} {
		waitFor(t, func() bool {
			_, ok, err := a.LiveSubscription(context.Background(), tenant, "proxy-1")
			return err == nil && ok
		}, "node A to copy "+string(tenant)+"'s proxy-1 from node B")
	}
	yOnB.cancel()
	waitFor(t, func() bool {
		_, ok, err := a.LiveSubscription(context.Background(), tenantY, "proxy-1")
		return err == nil && !ok
	}, "tenant Y's proxy-1 to leave node A's copy")
	if _, ok, err := a.LiveSubscription(t.Context(), tenantX, "proxy-1"); err != nil || !ok {
		t.Fatalf("tenant X's proxy-1 left node A's copy with tenant Y's (%v)", err)
	}

	resumed := on(t, a, tenantY, "proxy-1", rcpt.EventID)
	if ev := resumed.first(2 * time.Second); ev.Type != contract.EventTypeResync {
		t.Fatalf("an id from tenant X's stream resumed tenant Y's as %s, want resync", ev.Type)
	}
}
