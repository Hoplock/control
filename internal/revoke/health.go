// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke

import (
	"slices"
	"sync/atomic"
	"time"

	"github.com/hoplock/control/internal/store"
)

// The event plane's health, as this node sees it (0046, PLAN M19, M25).
//
// 0027 made the signals and nothing reported them: how far each tenant's
// cursor on this node trails its log, and the `revoke_wake_*` and
// `revoke_presence_*` failures it logs. This file counts them where they are
// logged and hands a copy to whoever asks — the deployment's node report —
// without taking a lock the fan-out holds for longer than a map read.

// planeCounters are the failures, by family.
type planeCounters struct {
	wakeFailures     atomic.Uint64
	lastWakeFailure  atomic.Int64 // unix nanoseconds; zero is never
	presenceFailures atomic.Uint64
	lastPresence     atomic.Int64
}

func (c *planeCounters) wake(at time.Time) {
	c.wakeFailures.Add(1)
	c.lastWakeFailure.Store(at.UnixNano())
}

func (c *planeCounters) presence(at time.Time) {
	c.presenceFailures.Add(1)
	c.lastPresence.Store(at.UnixNano())
}

// TenantLag is how far this node trails one tenant's log.
type TenantLag struct {
	Tenant store.Tenant
	// Cursor is the last position this node fanned out; Seen the furthest
	// it has been told the log reached. Lag is Seen − Cursor, never below
	// zero.
	Cursor, Seen, Lag int64
}

// PlaneHealth is the event plane on this node.
type PlaneHealth struct {
	// Tenants is every tenant this node holds a cursor for, by name.
	Tenants []TenantLag
	// WakeFailures counts `revoke_wake_*`: a wake-up not published, a
	// subscription to the coordinator's bus that failed or was dropped.
	WakeFailures    uint64
	LastWakeFailure time.Time
	// PresenceFailures counts `revoke_presence_*`: subscriptions that could
	// not be written, read or removed in the store.
	PresenceFailures    uint64
	LastPresenceFailure time.Time
}

// PlaneHealth reports the event plane on this node. It is read-only, takes
// Bus.mu for the length of a map walk and nothing else, and reads no store.
func (b *Bus) PlaneHealth() PlaneHealth {
	h := PlaneHealth{
		WakeFailures:        b.health.wakeFailures.Load(),
		LastWakeFailure:     unixOrZero(b.health.lastWakeFailure.Load()),
		PresenceFailures:    b.health.presenceFailures.Load(),
		LastPresenceFailure: unixOrZero(b.health.lastPresence.Load()),
	}
	b.mu.Lock()
	for tenant, c := range b.cursors {
		lag := max(c.seen-c.head, 0)
		h.Tenants = append(h.Tenants, TenantLag{Tenant: tenant, Cursor: c.head, Seen: max(c.seen, c.head), Lag: lag})
	}
	b.mu.Unlock()
	slices.SortFunc(h.Tenants, func(a, b TenantLag) int {
		switch {
		case a.Tenant < b.Tenant:
			return -1
		case a.Tenant > b.Tenant:
			return 1
		}
		return 0
	})
	return h
}

func unixOrZero(ns int64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}
