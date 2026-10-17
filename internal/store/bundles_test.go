// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Activations racing each other leave exactly one bundle active (M18: "which
// program is served" has one answer). Activate locks the wanted row, clears the
// others and sets it in one transaction, and the partial unique index refuses
// a second active row, so the loser of a race is a conflict and nothing else:
// never zero active, never two (0021). Rollbacks — activating an older version
// — are half the activations here, because that is the order that used to
// trip the index mid-statement.
func TestRacingActivationsLeaveExactlyOneActive(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	bundles := []store.PolicyBundle{storetest.Bundle(1, true)}
	for v := int64(2); v <= 5; v++ {
		bundles = append(bundles, storetest.Bundle(v, false))
	}
	storetest.Seed(t, st, storetest.Fixture{Tenant: tenantA, Bundles: bundles})

	// Unraced, a rollback is never the loser of anything: it succeeds.
	for _, version := range []int64{3, 1} {
		if err := st.PolicyBundles().Activate(ctx, tenantA, version); err != nil {
			t.Fatalf("activating %d with nothing racing it: %v", version, err)
		}
	}

	for round := range 10 {
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := range 8 {
			wg.Add(1)
			go func(version int64) {
				defer wg.Done()
				errs <- st.PolicyBundles().Activate(ctx, tenantA, version)
			}(int64((round+i)%5 + 1))
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil && !store.IsConflict(err) {
				t.Fatalf("round %d: an activation failed with %v, want success or a conflict", round, err)
			}
		}
		var active int
		if err := st.Pool().QueryRow(ctx,
			`SELECT count(*) FROM policy_bundles WHERE tenant = $1 AND active`, tenantA).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active != 1 {
			t.Fatalf("round %d: %d bundles active, want exactly one", round, active)
		}
	}
}

// GetByHash finds the version holding a digest without reading every source,
// and the oldest when there are several. Absent is not-found.
func TestABundleIsFoundByItsHash(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	dup := storetest.Bundle(3, false)
	dup.Hash = storetest.Bundle(2, false).Hash
	storetest.Seed(t, st, storetest.Fixture{Tenant: tenantA, Bundles: []store.PolicyBundle{
		storetest.Bundle(1, true), storetest.Bundle(2, false), dup,
	}})
	got, err := st.PolicyBundles().GetByHash(ctx, tenantA, dup.Hash)
	if err != nil || got.Version != 2 {
		t.Errorf("by hash = %d, %v; want version 2, the oldest holding it", got.Version, err)
	}
	if _, err := st.PolicyBundles().GetByHash(ctx, tenantA, "sha256:none"); !store.IsNotFound(err) {
		t.Errorf("an unknown hash answered %v, want not found", err)
	}
	if _, err := st.PolicyBundles().GetByHash(ctx, tenantB, dup.Hash); !store.IsNotFound(err) {
		t.Errorf("another tenant's hash answered %v, want not found", err)
	}
}

// Each tenant lock — the policy lock, and the inventory's (0023) — serialises
// two transactions in one tenant, leaves another tenant's alone, and is refused
// outside a transaction, where it would serialise nothing.
func TestEachTenantLockSerialisesATenantsActs(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	for name, lock := range map[string]func(context.Context, *store.Store, store.Tenant) error{
		"policy bundles": func(ctx context.Context, s *store.Store, tenant store.Tenant) error {
			return s.PolicyBundles().Lock(ctx, tenant)
		},
		"targets": func(ctx context.Context, s *store.Store, tenant store.Tenant) error {
			return s.Targets().Lock(ctx, tenant)
		},
		"subjects": func(ctx context.Context, s *store.Store, tenant store.Tenant) error {
			return s.Subjects().Lock(ctx, tenant)
		},
		"claim mappings": func(ctx context.Context, s *store.Store, tenant store.Tenant) error {
			return s.ClaimMappings().Lock(ctx, tenant)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			if err := lock(ctx, st, tenantA); !store.IsInvalid(err) {
				t.Fatalf("the lock outside a transaction answered %v, want invalid", err)
			}
			held, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
					if err := lock(ctx, tx, tenantA); err != nil {
						return err
					}
					close(held)
					<-release
					return nil
				})
			}()
			<-held
			// Another tenant's lock is its own.
			if err := st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
				return lock(ctx, tx, tenantB)
			}); err != nil {
				t.Fatalf("tenant b's lock: %v", err)
			}
			waited := make(chan error, 1)
			go func() {
				waited <- st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
					return lock(ctx, tx, tenantA)
				})
			}()
			select {
			case err := <-waited:
				t.Fatalf("a second lock in tenant a was taken while the first was held: %v", err)
			case <-time.After(200 * time.Millisecond):
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := <-waited; err != nil {
				t.Fatalf("the second lock, once the first was released: %v", err)
			}
		})
	}
}
