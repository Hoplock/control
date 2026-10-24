// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke

import (
	"context"
	"time"

	"github.com/hoplock/control/internal/store"
)

// The deployment's subscription set (0027, M5, PLAN §5.4).
//
// The cache-hint gate may hint a proxy only while a withdrawal can reach it,
// which is whether it holds a live subscription ANYWHERE — the node it asked
// need not be the node holding its stream. So each node writes the streams it
// holds to the store and reads every other node's into a copy in memory, and
// [Bus.LiveSubscriptions] answers from this node's own subscriptions and that
// copy, never from the store: the gate is on the decision path (M5).
//
// THE COPY'S BOUND. A stream another node takes is in this node's copy within
// two sync intervals — one for its holder to write it, one for this node to
// read it — and one that ends is out of it within the same two. Its last-seen
// trails the holder's own by at most one refresh (twice the heartbeat interval)
// and those two syncs, and a holder that dies stops rewriting it, so it ages out
// exactly where a stale subscription on this node does: every reader judges
// last-seen against fleet.heartbeat_ttl. That is M5's rule for a node's memory:
// a copy whose authority is the store, late by a stated bound, never wrong for
// longer.
//
// A read that fails drops that tenant's copy rather than keeping it, so the
// failure withholds hints — the safe direction (M9) — instead of extending them
// past what anyone has seen.

// syncPresence writes this node's subscriptions and reads every other node's.
func (b *Bus) syncPresence(ctx context.Context) {
	if b.presence == nil {
		return
	}
	now := b.now()

	b.mu.Lock()
	desired := make(map[store.Tenant]map[string]time.Time, len(b.local))
	for tenant, byProxy := range b.local {
		rows := make(map[string]time.Time, len(byProxy))
		for proxyID, subs := range byProxy {
			for sub := range subs {
				if seen := sub.seen(); seen.After(rows[proxyID]) {
					rows[proxyID] = seen
				}
			}
		}
		desired[tenant] = rows
	}
	tracked := make([]store.Tenant, 0, len(b.tracked)+len(b.local))
	for tenant := range b.tracked {
		tracked = append(tracked, tenant)
	}
	for tenant := range b.local {
		if _, ok := b.tracked[tenant]; !ok {
			tracked = append(tracked, tenant)
		}
	}
	b.mu.Unlock()

	b.writePresence(ctx, desired)

	b.ticks++
	expire := b.ticks%expireEvery == 0
	remote := make(map[store.Tenant]map[string]time.Time, len(tracked))
	for _, tenant := range tracked {
		if expire {
			if _, err := b.presence.EventSubscriptions().Expire(ctx, tenant, now.Add(-presenceExpiry)); err != nil && ctx.Err() == nil {
				b.log.Warn("expired event subscriptions could not be removed",
					"event", "revoke_presence_expire_failed", "tenant", tenant.String(), "error", err.Error())
			}
		}
		rows, err := b.presence.EventSubscriptions().List(ctx, tenant)
		if err != nil {
			if ctx.Err() == nil {
				b.log.Warn("other nodes' event subscriptions could not be read; hints to proxies they hold are withheld until they are",
					"event", "revoke_presence_read_failed", "tenant", tenant.String(), "error", err.Error())
			}
			continue
		}
		copied := make(map[string]time.Time, len(rows))
		for _, row := range rows {
			if row.NodeID == b.node {
				// This node's own subscriptions are read from memory,
				// and a row this node wrote before a restart is one it
				// no longer holds.
				continue
			}
			if row.SeenAt.After(copied[row.ProxyID]) {
				copied[row.ProxyID] = row.SeenAt
			}
		}
		remote[tenant] = copied
	}

	b.mu.Lock()
	b.remote = remote
	b.mu.Unlock()
}

// writePresence brings this node's rows in line with the subscriptions it
// holds: a new stream is written, one whose last-seen moved a refresh past its
// row is rewritten, and one that ended is removed. Only the loop calls it, and
// Close once the loop has stopped, so written needs no lock.
func (b *Bus) writePresence(ctx context.Context, desired map[store.Tenant]map[string]time.Time) {
	tenants := make(map[store.Tenant]struct{}, len(desired)+len(b.written))
	for tenant := range desired {
		tenants[tenant] = struct{}{}
	}
	for tenant := range b.written {
		tenants[tenant] = struct{}{}
	}
	for tenant := range tenants {
		want, have := desired[tenant], b.written[tenant]
		var (
			put  []store.EventSubscription
			gone []string
		)
		for proxyID, seen := range want {
			if stored, ok := have[proxyID]; !ok || seen.Sub(stored) >= b.refresh {
				put = append(put, store.EventSubscription{ProxyID: proxyID, NodeID: b.node, SeenAt: seen})
			}
		}
		for proxyID := range have {
			if _, ok := want[proxyID]; !ok {
				gone = append(gone, proxyID)
			}
		}
		if len(put) > 0 {
			if err := b.presence.EventSubscriptions().Put(ctx, tenant, b.node, put); err != nil {
				if ctx.Err() == nil {
					b.log.Warn("this node's event subscriptions could not be written; other nodes withhold hints to them until they are",
						"event", "revoke_presence_write_failed", "tenant", tenant.String(), "error", err.Error())
				}
			} else {
				if have == nil {
					have = make(map[string]time.Time, len(put))
					b.written[tenant] = have
				}
				for _, row := range put {
					have[row.ProxyID] = row.SeenAt
				}
			}
		}
		if len(gone) > 0 {
			if err := b.presence.EventSubscriptions().Remove(ctx, tenant, b.node, gone); err != nil {
				if ctx.Err() == nil {
					b.log.Warn("ended event subscriptions could not be removed; other nodes stop believing them once their last write ages out",
						"event", "revoke_presence_remove_failed", "tenant", tenant.String(), "error", err.Error())
				}
			} else {
				for _, proxyID := range gone {
					delete(have, proxyID)
				}
			}
		}
		if len(have) == 0 {
			delete(b.written, tenant)
		}
	}
}

// withdrawPresence removes every row this node wrote, at shutdown, so no other
// node keeps counting a stream that ended with the process.
func (b *Bus) withdrawPresence(ctx context.Context) {
	if b.presence == nil {
		return
	}
	b.writePresence(ctx, nil)
}
