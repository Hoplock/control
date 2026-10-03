// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package accessctx

import (
	"context"
	"sync"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/store"
)

// The probe path's node-local state. None of it is authoritative — every entry
// is a copy of something a provider or the database already said, reused for a
// bounded time — so a decision on another node, with a cold cache, reaches the
// same answer by asking again (M5).

// evidenceCache keeps definite answers for their TTL.
type evidenceCache struct {
	max int
	now func() time.Time

	mu      sync.Mutex
	entries map[string]cachedEvidence
}

type cachedEvidence struct {
	evidence ext.AccessEvidence
	expires  time.Time
}

func newEvidenceCache(max int, now func() time.Time) *evidenceCache {
	if max <= 0 {
		max = DefaultCacheEntries
	}
	return &evidenceCache{max: max, now: now, entries: map[string]cachedEvidence{}}
}

func (c *evidenceCache) get(key string) (ext.AccessEvidence, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return ext.AccessEvidence{}, false
	}
	if !c.now().Before(e.expires) {
		delete(c.entries, key)
		return ext.AccessEvidence{}, false
	}
	return e.evidence, true
}

func (c *evidenceCache) put(key string, ev ext.AccessEvidence, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.entries) >= c.max {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= c.max {
			// Still full of live answers: start again rather than grow. A
			// cold cache costs probes; an unbounded one costs the node.
			c.entries = map[string]cachedEvidence{}
		}
	}
	c.entries[key] = cachedEvidence{evidence: ev, expires: now.Add(ttl)}
}

// flights shares one provider call among every decision asking the same
// question at the same time: a scanner opening fifty connections at once is
// one probe, not fifty.
type flights struct {
	mu    sync.Mutex
	calls map[string]*flight
}

type flight struct {
	done     chan struct{}
	evidence ext.AccessEvidence
	err      error
}

// join returns the call in flight for key, and whether the caller is the one
// who must make it.
func (f *flights) join(key string) (*flight, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if call, ok := f.calls[key]; ok {
		return call, false
	}
	call := &flight{done: make(chan struct{})}
	f.calls[key] = call
	return call, true
}

// land ends a call: later questions start a new one.
func (f *flights) land(key string, call *flight) {
	f.mu.Lock()
	delete(f.calls, key)
	f.mu.Unlock()
	close(call.done)
}

// bindingCache serves a tenant's bindings from memory, re-reading them after a
// refresh interval: the decision path reads what is indexed and small (M5),
// and a binding changed through the API takes effect within one interval —
// the same promise the compiled policy makes.
type bindingCache struct {
	store   *store.Store
	refresh time.Duration
	now     func() time.Time

	mu      sync.Mutex
	tenants map[store.Tenant]bindingEntry
}

type bindingEntry struct {
	bindings []store.AccessContextBinding
	read     time.Time
}

func newBindingCache(st *store.Store, refresh time.Duration, now func() time.Time) *bindingCache {
	return &bindingCache{store: st, refresh: refresh, now: now, tenants: map[store.Tenant]bindingEntry{}}
}

func (c *bindingCache) get(ctx context.Context, tenant store.Tenant) ([]store.AccessContextBinding, error) {
	now := c.now()
	c.mu.Lock()
	e, ok := c.tenants[tenant]
	c.mu.Unlock()
	if ok && now.Sub(e.read) < c.refresh {
		return e.bindings, nil
	}
	bindings, err := c.store.AccessContextBindings().List(ctx, tenant)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.tenants[tenant] = bindingEntry{bindings: bindings, read: now}
	c.mu.Unlock()
	return bindings, nil
}
