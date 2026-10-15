// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"sync"
	"testing"

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
