// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadMigrationsReadsTheEmbeddedSet(t *testing.T) {
	t.Parallel()

	all, err := LoadMigrations()
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("LoadMigrations returned nothing; the embedded set is empty")
	}
	for i, m := range all {
		if i > 0 && m.Version <= all[i-1].Version {
			t.Errorf("migrations are not in ascending order: %d after %d",
				m.Version, all[i-1].Version)
		}
		if m.Checksum == "" || m.SQL == "" {
			t.Errorf("migration %04d_%s has an empty checksum or body", m.Version, m.Name)
		}
	}
}

func TestLoadMigrationsRejectsABadSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{
			name:  "a file that does not match the convention",
			files: fstest.MapFS{"initial.sql": &fstest.MapFile{Data: []byte("SELECT 1")}},
			want:  "does not match",
		},
		{
			name: "two files sharing a version",
			files: fstest.MapFS{
				"0001_a.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
				"0001_b.sql": &fstest.MapFile{Data: []byte("SELECT 2")},
			},
			want: "share version",
		},
		{
			name:  "three digits instead of four",
			files: fstest.MapFS{"001_initial.sql": &fstest.MapFile{Data: []byte("SELECT 1")}},
			want:  "does not match",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadMigrationsFS(tc.files)
			if err == nil {
				t.Fatalf("loadMigrationsFS: want an error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("loadMigrationsFS error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// Forward-only, checked (PLAN §8). Editing a migration that has already run is
// how two deployments end up with different schemas both reporting the same
// version, and the checksum is what turns that into a startup error.
func TestPendingMigrationsRefusesAnEditedMigration(t *testing.T) {
	t.Parallel()

	all := []Migration{{Version: 1, Name: "initial", Checksum: "new"}}
	applied := []AppliedMigration{{Version: 1, Name: "initial", Checksum: "old"}}

	_, err := pendingMigrations("store.Test", all, applied)
	if err == nil {
		t.Fatal("pendingMigrations: want an error for an edited migration, got nil")
	}
	if !strings.Contains(err.Error(), "forward-only") {
		t.Errorf("error = %q, want it to say migrations are forward-only", err)
	}
}

// The shape a rebase produces: two branches each adding 0004, one merged
// first. Applying the second out of order gives two databases that both say
// "up to date" with different schemas.
func TestPendingMigrationsRefusesAnOutOfOrderMigration(t *testing.T) {
	t.Parallel()

	all := []Migration{
		{Version: 1, Name: "initial", Checksum: "a"},
		{Version: 2, Name: "late", Checksum: "b"},
		{Version: 3, Name: "already_applied", Checksum: "c"},
	}
	applied := []AppliedMigration{
		{Version: 1, Name: "initial", Checksum: "a"},
		{Version: 3, Name: "already_applied", Checksum: "c"},
	}

	_, err := pendingMigrations("store.Test", all, applied)
	if err == nil {
		t.Fatal("pendingMigrations: want an error for a migration numbered below the highest applied")
	}
	if !strings.Contains(err.Error(), "renumber") {
		t.Errorf("error = %q, want it to name the remedy", err)
	}
}

func TestPendingMigrationsOnAFreshDatabaseIsEverything(t *testing.T) {
	t.Parallel()

	all := []Migration{
		{Version: 1, Name: "initial", Checksum: "a"},
		{Version: 2, Name: "next", Checksum: "b"},
	}
	pending, err := pendingMigrations("store.Test", all, nil)
	if err != nil {
		t.Fatalf("pendingMigrations: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pendingMigrations returned %d, want 2", len(pending))
	}
}
