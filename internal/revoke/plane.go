// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/store"
)

// The shared event plane (0027): how an event logged on one node reaches the
// subscriptions every other node holds, and how each node learns which proxies
// hold a stream anywhere.
//
// THE LOG IS THE AUTHORITY AND THE BUS IS A DOORBELL. A publish commits to the
// log and then publishes a wake-up on ext.ClusterCoordinator's bus naming the
// tenant and the position. A node woken for a tenant it holds subscriptions in
// reads that tenant's log after the last position it delivered and fans out
// what it finds, so the wake-up carries no event and none of the order: a
// duplicate is a read that finds nothing, a reordered pair is one read that
// finds both, and a lost one is found by the same read on this node's sync
// clock. That is why the seam's bus needs promise so little (ext/cluster.go):
// at least once, in any order, and a subscriber it cannot keep up with closed
// rather than silently skipped.
//
// Nothing here is a cross-node transport of Control's own. The coordinator
// carries the wake-ups — in process for one node (extdefault.SingleNode), and
// between nodes for a clustered deployment (Hoplock Enterprise, its E9) — and
// the store carries the rest, as it already carries everything else every node
// must agree on.

// wakeTopic is the coordinator topic wake-ups travel on. Every one carries the
// tenant it is about (M18); a node fans out only to subscriptions in it.
const wakeTopic = "hoplock.events.logged"

// wakeKind is a wake-up's kind within the topic.
const wakeKind = "logged"

// wakeUp is a wake-up's payload: the position logged.
type wakeUp struct {
	Seq int64 `json:"seq"`
}

// presenceExpiry is how old a row of the deployment's subscription set may be
// before any node deletes it. A node rewrites its rows while their streams
// live, so only a node that died leaves one this old, and every reader stopped
// believing it long before (its last write is far past fleet.heartbeat_ttl).
const presenceExpiry = 10 * time.Minute

// expireEvery is how many syncs pass between expiry sweeps.
const expireEvery = 60

// Start joins this bus to the deployment: it listens for other nodes'
// wake-ups, and runs the sync loop that catches up with the log and keeps the
// subscription copy current. It runs until [Bus.Close].
//
// A bus that is never started is a correct single node: its own publications
// reach its own subscriptions at once, because publish fans out before it
// returns. What only the loop provides is everything another node did.
func (b *Bus) Start(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	b.mu.Lock()
	b.stop = cancel
	b.mu.Unlock()
	if b.coord != nil {
		b.loops.Add(1)
		go b.listen(loopCtx)
	}
	b.loops.Add(1)
	go b.run(loopCtx)
}

// stopLoop ends the loop Start runs and waits for it, inside ctx. It reports
// whether the loop is stopped (true also when it never ran).
func (b *Bus) stopLoop(ctx context.Context) bool {
	b.mu.Lock()
	stop := b.stop
	b.mu.Unlock()
	if stop == nil {
		return true
	}
	stop()
	done := make(chan struct{})
	go func() {
		b.loops.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// wake tells the other nodes an event was logged. A failure costs them at most
// one sync interval, never the event, so it is logged and not returned: the
// publication already succeeded when the log took it.
func (b *Bus) wake(ctx context.Context, tenant store.Tenant, seq int64) {
	if b.coord == nil {
		return
	}
	payload, err := json.Marshal(wakeUp{Seq: seq})
	if err == nil {
		err = b.coord.Publish(ctx, ext.ClusterEvent{
			Tenant:      ext.Tenant(tenant),
			Topic:       wakeTopic,
			Kind:        wakeKind,
			Origin:      ext.NodeID(b.node),
			PublishedAt: b.now().UTC(),
			Payload:     payload,
		})
	}
	if err != nil {
		b.log.Warn("the other nodes were not woken for a logged event; each delivers it at its next sync",
			"event", "revoke_wake_failed", "tenant", tenant.String(), "seq", seq, "error", err.Error())
	}
}

// listen reads wake-ups from the coordinator until ctx ends. A subscription the
// coordinator drops — closed, because this node fell behind or lost the bus —
// is taken again, and the log is read for everything it may have missed.
func (b *Bus) listen(ctx context.Context) {
	defer b.loops.Done()
	backoff := b.sync
	for ctx.Err() == nil {
		ch, cancel, err := b.coord.Subscribe(ctx, wakeTopic)
		if err != nil {
			b.log.Warn("could not subscribe to the coordinator's bus; the log is read on the sync clock meanwhile",
				"event", "revoke_wake_subscribe_failed", "error", err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			continue
		}
		// Whatever was published while this node was not subscribed is in
		// the log.
		b.catchUp(ctx)
		b.drain(ctx, ch)
		cancel()
		if ctx.Err() == nil {
			b.log.Warn("this node was dropped from the coordinator's bus; resubscribing and reading the log",
				"event", "revoke_wake_dropped")
		}
	}
}

// drain handles wake-ups until the channel closes or ctx ends.
func (b *Bus) drain(ctx context.Context, ch <-chan ext.ClusterEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b.woken(ctx, ev)
		}
	}
}

// woken handles one wake-up: if this node holds subscriptions in its tenant
// and has not delivered that far, it reads the tenant's log and fans out.
func (b *Bus) woken(ctx context.Context, ev ext.ClusterEvent) {
	if ev.Kind != wakeKind || string(ev.Origin) == b.node || ev.Tenant == "" {
		// Its own wake-up: publish already fanned it out here.
		return
	}
	var w wakeUp
	if err := json.Unmarshal(ev.Payload, &w); err != nil {
		// Unreadable is not lost: the sync reads the log anyway.
		b.log.Warn("a wake-up did not decode", "event", "revoke_wake_malformed", "error", err.Error())
		return
	}
	tenant := store.Tenant(ev.Tenant)
	b.mu.Lock()
	c := b.cursors[tenant]
	held := len(b.local[tenant]) > 0
	b.mu.Unlock()
	if c == nil || !held {
		// Nobody here to deliver to. The first subscription in the tenant
		// catches its cursor up before it registers.
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if w.Seq <= c.head {
		return
	}
	if _, err := b.advanceLocked(ctx, tenant, c, 0); err != nil && ctx.Err() == nil {
		b.log.Warn("the log could not be read after a wake-up; the next sync delivers it",
			"event", "revoke_fanout_deferred", "tenant", tenant.String(), "error", err.Error())
	}
}

// run is the sync loop: every SyncInterval it reads the log of every tenant
// this node holds subscriptions in, and writes this node's subscriptions and
// reads the other nodes'.
func (b *Bus) run(ctx context.Context) {
	defer b.loops.Done()
	tick := time.NewTicker(b.sync)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			b.catchUp(ctx)
			b.syncPresence(ctx)
		}
	}
}

// catchUp fans out, in every tenant this node holds subscriptions in, whatever
// the log holds past this node's cursor. It is what bounds a lost wake-up — or
// a publisher that died between its commit and its wake-up — to one sync.
func (b *Bus) catchUp(ctx context.Context) {
	b.mu.Lock()
	type pending struct {
		tenant store.Tenant
		c      *cursor
	}
	var todo []pending
	for tenant := range b.local {
		if c := b.cursors[tenant]; c != nil {
			todo = append(todo, pending{tenant, c})
		}
	}
	b.mu.Unlock()
	for _, p := range todo {
		p.c.mu.Lock()
		_, err := b.advanceLocked(ctx, p.tenant, p.c, 0)
		p.c.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			b.log.Warn("the log could not be read to catch up; the next sync tries again",
				"event", "revoke_catch_up_failed", "tenant", p.tenant.String(), "error", err.Error())
		}
	}
}
