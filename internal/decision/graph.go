// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"context"
	"sync"
	"time"

	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
)

// The fleet graph, held in memory for the same reason the program is.
//
// [fleet.Registry.Graph] is three whole-tenant reads — proxies, edges, relay
// registrations — and 0006 said so in its own doc comment: "0008 will hold a
// graph rather than reload it per request (M5 has no room for three reads on
// the decision path)". This is that holding.
//
// What the staleness costs is bounded and worth stating, because it is the
// interesting half: a proxy that dies is routable for up to [refreshInterval]
// after this server could have known. That is the same exposure the liveness
// rule already carries — a heartbeat TTL of 90 seconds means a dead proxy is
// routable for up to 90 seconds anyway — so the refresh adds seconds to a
// window already measured in tens of them, and the failure it causes is a
// connection that fails at the next hop rather than a session that runs
// unpoliced.

type graphCache struct {
	registry *fleet.Registry
	refresh  time.Duration
	now      func() time.Time
	mu       sync.Mutex
	graphs   map[store.Tenant]*graphEntry
}

type graphEntry struct {
	graph    *fleet.Graph
	loadedAt time.Time
}

func newGraphCache(registry *fleet.Registry, refresh time.Duration, now func() time.Time) *graphCache {
	return &graphCache{
		registry: registry,
		refresh:  refresh,
		now:      now,
		graphs:   make(map[store.Tenant]*graphEntry),
	}
}

// Graph returns the tenant's fleet graph, reloading it when the held copy has
// aged past the refresh interval.
func (c *graphCache) Graph(ctx context.Context, tenant store.Tenant) (*fleet.Graph, error) {
	c.mu.Lock()
	entry, ok := c.graphs[tenant]
	c.mu.Unlock()

	now := c.now()
	if ok && now.Sub(entry.loadedAt) < c.refresh {
		return entry.graph, nil
	}

	g, err := c.registry.Graph(ctx, tenant)
	if err != nil {
		if ok {
			// As with the program: a read failure is not a fleet that
			// changed. The graph in hand was built from rows that really
			// existed, and serving it while the database is unreachable
			// keeps sessions being decided rather than turning a failover
			// into an estate-wide outage.
			return entry.graph, nil
		}
		return nil, err
	}

	c.mu.Lock()
	c.graphs[tenant] = &graphEntry{graph: g, loadedAt: now}
	c.mu.Unlock()
	return g, nil
}
