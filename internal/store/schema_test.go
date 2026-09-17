// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"slices"
	"testing"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// bookkeepingTables are the only tables allowed to have no tenant column.
//
// There is exactly one, and it holds no tenant data: schema_migrations records
// which files have run against this database. Adding to this list is the thing
// M12 was written to prevent, so it is a list rather than a pattern.
var bookkeepingTables = []string{"schema_migrations"}

// M12, checked against the database rather than against a reviewer's
// attention: retrofitting the tenant column into a populated audit store later
// is a migration nobody wants to run, and the only way that stays true is if a
// table added without it fails here.
func TestEveryTableCarriesTheTenantColumn(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	rows, err := st.Pool().Query(ctx, `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname = current_schema()
		ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list tables: %v", err)
	}
	if len(tables) < 2 {
		t.Fatalf("found %d tables, want the initial schema", len(tables))
	}

	for _, table := range tables {
		if slices.Contains(bookkeepingTables, table) {
			continue
		}
		var hasTenant bool
		err := st.Pool().QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema()
				  AND table_name = $1
				  AND column_name = 'tenant'
			)`, table).Scan(&hasTenant)
		if err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if !hasTenant {
			t.Errorf("table %q has no tenant column (M12)", table)
		}
	}
}

// The tenant is part of every primary key, so a row cannot exist outside a
// tenant and a uniqueness constraint cannot straddle two.
func TestEveryPrimaryKeyStartsWithTheTenant(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	rows, err := st.Pool().Query(ctx, `
		SELECT c.relname,
		       (SELECT a.attname
		        FROM pg_attribute a
		        WHERE a.attrelid = c.oid AND a.attnum = i.indkey[0])
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_index i ON i.indrelid = c.oid AND i.indisprimary
		WHERE c.relkind = 'r' AND n.nspname = current_schema()
		ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("list primary keys: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var table, firstColumn string
		if err := rows.Scan(&table, &firstColumn); err != nil {
			t.Fatalf("scan primary key: %v", err)
		}
		if slices.Contains(bookkeepingTables, table) {
			continue
		}
		seen++
		if firstColumn != "tenant" {
			t.Errorf("table %q primary key starts with %q, want tenant (M12)", table, firstColumn)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list primary keys: %v", err)
	}
	if seen == 0 {
		t.Fatal("found no primary keys to check")
	}
}

// The migration command is idempotent, and its dry run changes nothing. Both
// are acceptance criteria of this phase, and both are properties of the
// database rather than of the code that prints them.
func TestMigrateIsIdempotentAndDryRunChangesNothing(t *testing.T) {
	t.Parallel()
	st := storetest.New(t) // has already migrated once
	ctx := t.Context()

	before := schemaFingerprint(t, ctx, st)

	plan, err := st.PlanMigrations(ctx)
	if err != nil {
		t.Fatalf("PlanMigrations: %v", err)
	}
	if len(plan.Pending) != 0 {
		t.Errorf("PlanMigrations after a full migrate: %d pending, want 0", len(plan.Pending))
	}
	if len(plan.Applied) == 0 {
		t.Error("PlanMigrations reports nothing applied after a full migrate")
	}
	if got := schemaFingerprint(t, ctx, st); got != before {
		t.Error("PlanMigrations changed the schema; a dry run must change nothing")
	}

	applied, err := st.Migrate(ctx)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("second Migrate applied %d migrations, want 0", len(applied))
	}
	if got := schemaFingerprint(t, ctx, st); got != before {
		t.Error("a second Migrate changed the schema; it must be idempotent")
	}
}

// A dry run against a database that has never been migrated must also leave it
// untouched — including the bookkeeping table, which is the one a careless
// implementation creates on the way to reading it.
func TestDryRunOnAFreshDatabaseCreatesNothing(t *testing.T) {
	t.Parallel()
	dsn := storetest.DSN(t)
	ctx := t.Context()

	schema := storetest.NewSchema(t, dsn)
	st, err := store.Open(ctx, storetest.DSNForSchema(dsn, schema))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)

	plan, err := st.PlanMigrations(ctx)
	if err != nil {
		t.Fatalf("PlanMigrations on a fresh database: %v", err)
	}
	if len(plan.Applied) != 0 {
		t.Errorf("PlanMigrations reports %d applied on a fresh database, want 0", len(plan.Applied))
	}
	if len(plan.Pending) == 0 {
		t.Error("PlanMigrations reports nothing pending on a fresh database")
	}

	var tables int
	if err := st.Pool().QueryRow(ctx, `
		SELECT count(*) FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname = current_schema()`).Scan(&tables); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if tables != 0 {
		t.Errorf("a dry run left %d table(s) behind, want 0", tables)
	}

	if v, err := st.SchemaVersion(ctx); err != nil || v != 0 {
		t.Errorf("SchemaVersion on a fresh database = (%d, %v), want (0, nil)", v, err)
	}
}

// schemaFingerprint is every column of every table in the current schema,
// which is enough to notice a migration that ran when it should not have.
func schemaFingerprint(t *testing.T, ctx context.Context, st *store.Store) string {
	t.Helper()

	var fingerprint string
	err := st.Pool().QueryRow(ctx, `
		SELECT COALESCE(string_agg(table_name || '.' || column_name || ':' || data_type, ',' ORDER BY
			table_name, column_name), '')
		FROM information_schema.columns
		WHERE table_schema = current_schema()`).Scan(&fingerprint)
	if err != nil {
		t.Fatalf("fingerprint schema: %v", err)
	}
	return fingerprint
}
