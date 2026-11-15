// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Advisory locks are the database's, not a schema's, and the store keys them by
// tenant and id. A test's store keys its locks under its schema
// (store.WithLockNamespace, set by storetest), so two tests naming one tenant no
// longer stall or deadlock each other — while two stores of one test still
// exclude each other, as two nodes must, and a store with no namespace takes
// exactly the key it always took, which is what keeps nodes of two versions
// excluding each other through a rolling upgrade.
func TestLockNamespacesKeepTestsApartAndProductionKeysUnchanged(t *testing.T) {
	t.Parallel()
	dsn := storetest.DSN(t)
	// One tenant name for every store below, as every test once shared one.
	const tenant = store.Tenant("lock-namespace-probe")

	// hold takes the tenant's directory-groups lock in a transaction and keeps
	// it until the returned func is called.
	hold := func(st *store.Store) (release func()) {
		t.Helper()
		held, rel, errc := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			errc <- st.InTx(t.Context(), func(ctx context.Context, tx *store.Store) error {
				if err := tx.DirectoryGroups().Lock(ctx, tenant); err != nil {
					return err
				}
				close(held)
				<-rel
				return nil
			})
		}()
		select {
		case <-held:
		case err := <-errc:
			t.Fatalf("hold: %v", err)
		}
		return func() {
			close(rel)
			if err := <-errc; err != nil {
				t.Errorf("release: %v", err)
			}
		}
	}
	// take tries for the same lock, giving up after a moment.
	take := func(st *store.Store) error {
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		return st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
			return tx.DirectoryGroups().Lock(ctx, tenant)
		})
	}

	schemaA, schemaB := storetest.NewSchema(t, dsn), storetest.NewSchema(t, dsn)
	a, b := storetest.Open(t, dsn, schemaA), storetest.Open(t, dsn, schemaB)

	t.Run("two tests do not share a lock", func(t *testing.T) {
		release := hold(a)
		defer release()
		if err := take(b); err != nil {
			t.Fatalf("a store in another test's namespace waited on this one's lock: %v", err)
		}
	})

	t.Run("two stores of one test still exclude each other", func(t *testing.T) {
		again := storetest.Open(t, dsn, schemaA)
		release := hold(a)
		defer release()
		if err := take(again); err == nil {
			t.Fatal("a second store in the same namespace took a lock the first holds")
		}
	})

	t.Run("no namespace is the key a node has always taken", func(t *testing.T) {
		plain, err := store.Open(t.Context(), storetest.DSNForSchema(dsn, storetest.NewSchema(t, dsn)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(plain.Close)
		release := hold(plain)
		defer release()

		// The directory-groups key exactly as the store built it before
		// namespaces existed. A change to it is a lock that two nodes of
		// different versions stop sharing mid-upgrade.
		const unchanged = "directory\x1f" + string(tenant) + "\x1fgroups"
		raw, err := pgxpool.New(t.Context(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		tx, err := raw.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		var got bool
		if err := tx.QueryRow(t.Context(), `SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))`, unchanged).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got {
			t.Fatalf("a store with no namespace does not hold %q: its key changed", unchanged)
		}
	})
}
