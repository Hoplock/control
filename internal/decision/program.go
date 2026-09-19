// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hoplock/control/internal/policy/compile"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// The compiled policy, held in memory (M5).
//
// A proxy holds a user's SSH handshake open while this server answers, so the
// decision path may not parse and compile a bundle per request: compilation
// walks every rule and every match axis, which is work proportional to the
// policy rather than to the question. It is done once per bundle VERSION and
// served from memory afterwards.
//
// Freshness is bounded rather than exact. A newly activated bundle is picked up
// within [refreshInterval], because the alternative — a read of `policy_bundles`
// on every authorize call to ask "is this still the active one" — puts a whole
// row of policy source on the hot path to answer a question whose answer is
// almost always "yes". A policy change that must take effect NOW is a
// revocation (M9), not an activation, and the revocation stream is the path
// with no cache in front of it.

// DefaultRefreshInterval is how long a cached program or graph is served before
// this server re-reads what the active one is.
//
// It is short enough that an operator activating a bundle sees it take effect
// while still looking at the screen, and long enough that a fleet at five
// figures of requests per second is not re-reading policy source for every one
// of them (M5).
const DefaultRefreshInterval = 5 * time.Second

// programCache serves one compiled program per tenant.
type programCache struct {
	st       *store.Store
	refresh  time.Duration
	now      func() time.Time
	mu       sync.Mutex
	programs map[store.Tenant]*programEntry
}

type programEntry struct {
	prog    *compile.Program
	version int64
	hash    string
	// checkedAt is when this server last asked which bundle is active. It
	// is not when the program was compiled: a re-read that finds the same
	// version refreshes the check and keeps the program.
	checkedAt time.Time
}

func newProgramCache(st *store.Store, refresh time.Duration, now func() time.Time) *programCache {
	return &programCache{
		st:       st,
		refresh:  refresh,
		now:      now,
		programs: make(map[store.Tenant]*programEntry),
	}
}

// ErrNoActiveBundle is a tenant with no policy at all.
//
// It is an OUTAGE and never a deny (M11). A tenant whose bundle has not been
// activated yet has no policy to have denied anybody with, and answering `401`
// would send an operator to look at permissions during what is a deployment
// problem.
var ErrNoActiveBundle = fmt.Errorf("decision: the tenant has no active policy bundle")

// Program returns the compiled program for a tenant, compiling it if the active
// bundle has changed since it was last read.
func (c *programCache) Program(ctx context.Context, tenant store.Tenant) (*compile.Program, error) {
	c.mu.Lock()
	entry, ok := c.programs[tenant]
	c.mu.Unlock()

	now := c.now()
	if ok && now.Sub(entry.checkedAt) < c.refresh {
		return entry.prog, nil
	}

	bundle, err := c.st.PolicyBundles().GetActive(ctx, tenant)
	if err != nil {
		if store.IsNotFound(err) {
			if ok {
				// A bundle that was active a moment ago and is not now is
				// a deactivation, and the last known policy is not a
				// substitute for it — serving it would be this server
				// deciding that the operator did not mean it.
				c.forget(tenant)
			}
			return nil, ErrNoActiveBundle
		}
		if ok {
			// A read failure is not a policy change. The program in hand
			// was compiled from a bundle that really was active, so it is
			// served while the database is unreachable rather than
			// failing every connection in the estate: the alternative
			// turns a replica failover into a fleet-wide outage.
			return entry.prog, nil
		}
		return nil, err
	}

	if ok && entry.version == bundle.Version && entry.hash == bundle.Hash {
		c.touch(tenant, now)
		return entry.prog, nil
	}

	prog, err := compileBundle(bundle)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.programs[tenant] = &programEntry{
		prog:      prog,
		version:   bundle.Version,
		hash:      bundle.Hash,
		checkedAt: now,
	}
	c.mu.Unlock()
	return prog, nil
}

func (c *programCache) touch(tenant store.Tenant, at time.Time) {
	c.mu.Lock()
	if entry, ok := c.programs[tenant]; ok {
		entry.checkedAt = at
	}
	c.mu.Unlock()
}

func (c *programCache) forget(tenant store.Tenant) {
	c.mu.Lock()
	delete(c.programs, tenant)
	c.mu.Unlock()
}

// compileBundle parses and compiles stored policy source.
//
// A bundle that does not compile is an OUTAGE, not a denial: it was accepted by
// an authoring path that should have refused it (0005 compiles before storing),
// so what it says about the estate is that this server is misconfigured, not
// that this user may not connect.
func compileBundle(b store.PolicyBundle) (*compile.Program, error) {
	bundle, err := model.Parse(b.Source)
	if err != nil {
		return nil, fmt.Errorf("decision: policy bundle %d does not parse: %w", b.Version, err)
	}
	prog, err := compile.Compile(bundle)
	if err != nil {
		return nil, fmt.Errorf("decision: policy bundle %d does not compile: %w", b.Version, err)
	}
	return prog, nil
}
