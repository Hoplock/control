// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/hoplock/control/internal/store/migrations"
)

// migrationsLockID is the advisory lock every migration run takes.
//
// Migrations are applied by an explicit command rather than on boot (PLAN §8)
// precisely because two nodes starting together must not race — but "run the
// command twice by accident" is a thing operators do, and CI does it on every
// job. The lock makes the second run wait for the first and then find nothing
// to do, instead of both running `CREATE TABLE` and one failing halfway.
//
// The constant is arbitrary but must never change: its whole job is that two
// processes pick the same number.
const migrationsLockID int64 = 7565393572195319

// migrationFilePattern is the file naming convention: four digits, an
// underscore, a description. A file that does not match is an error rather
// than a file to skip — a migration silently not applied is the failure this
// whole mechanism exists to prevent.
var migrationFilePattern = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migration is one file: its version, its name, and the SQL it applies.
type Migration struct {
	// Version is the numeric prefix, and the order migrations are applied
	// in.
	Version int64
	// Name is the descriptive half of the filename.
	Name string
	// SQL is the file's contents.
	SQL string
	// Checksum is the SHA-256 of SQL, hex encoded. It is recorded when a
	// migration is applied and compared on every later run: a merged
	// migration that has been edited is a startup error, not a silent
	// divergence between two deployments (PLAN §8).
	Checksum string
}

// AppliedMigration is a row of schema_migrations.
type AppliedMigration struct {
	Version  int64
	Name     string
	Checksum string
}

// MigrationPlan is what a run would do. It is what --dry-run prints, and it is
// computed by exactly the same code that applies migrations — a dry run that
// is produced by a second implementation is a dry run that can be wrong.
type MigrationPlan struct {
	// Applied are the migrations already recorded in the database.
	Applied []AppliedMigration
	// Pending are the migrations this run would apply, in order.
	Pending []Migration
}

// LoadMigrations reads and validates the embedded migration set.
func LoadMigrations() ([]Migration, error) {
	return loadMigrationsFS(migrations.FS)
}

// loadMigrationsFS is LoadMigrations over an arbitrary filesystem, so the
// validation rules can be tested against a deliberately broken set.
func loadMigrationsFS(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}

	var out []Migration
	seen := map[int64]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := migrationFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf(
				"store: migration %q does not match NNNN_short_description.sql", e.Name())
		}
		version, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("store: migration %q: %w", e.Name(), err)
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf(
				"store: migrations %q and %q share version %04d", other, e.Name(), version)
		}
		seen[version] = e.Name()

		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: read migration %q: %w", e.Name(), err)
		}
		sum := sha256.Sum256(body)
		out = append(out, Migration{
			Version:  version,
			Name:     m[2],
			SQL:      string(body),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// PlanMigrations reports what Migrate would do, without changing anything.
//
// "Without changing anything" includes the bookkeeping table: a dry run
// against a database that has never been migrated reads no schema_migrations
// and creates none, because a --dry-run that leaves a table behind has already
// broken the promise it was added for.
func (s *Store) PlanMigrations(ctx context.Context) (MigrationPlan, error) {
	const op = "store.PlanMigrations"

	all, err := LoadMigrations()
	if err != nil {
		return MigrationPlan{}, &Error{Op: op, Kind: KindInternal, Err: err}
	}
	applied, err := s.appliedMigrations(ctx, op)
	if err != nil {
		return MigrationPlan{}, err
	}
	pending, err := pendingMigrations(op, all, applied)
	if err != nil {
		return MigrationPlan{}, err
	}
	return MigrationPlan{Applied: applied, Pending: pending}, nil
}

// Migrate applies every pending migration, in order, and returns the ones it
// applied. It is idempotent: a second run finds nothing pending and changes
// nothing.
//
// Each migration runs in its own transaction together with the row recording
// it, so a failure halfway leaves the schema at the last complete version
// rather than at "somewhere inside 0004".
func (s *Store) Migrate(ctx context.Context) ([]Migration, error) {
	const op = "store.Migrate"
	if s.pool == nil {
		return nil, &Error{Op: op, Kind: KindInternal, Err: errMigrateInTx}
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer conn.Release()

	// Session-scoped advisory lock, held for the whole run and released by
	// the explicit unlock below (and by the connection closing, whatever
	// happens to this process).
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationsLockID); err != nil {
		return nil, wrap(op, err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.rollbackTimeout())
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, migrationsLockID)
	}()

	if err := s.ensureMigrationsTable(ctx, op); err != nil {
		return nil, err
	}

	plan, err := s.PlanMigrations(ctx)
	if err != nil {
		return nil, err
	}

	var done []Migration
	for _, m := range plan.Pending {
		if err := s.applyMigration(ctx, op, m); err != nil {
			return done, err
		}
		done = append(done, m)
	}
	return done, nil
}

// errMigrateInTx reports Migrate called on a transaction-bound Store.
var errMigrateInTx = errors.New("Migrate needs the pool: each migration runs in its own transaction")

// applyMigration runs one migration and records it, in one transaction.
func (s *Store) applyMigration(ctx context.Context, op string, m Migration) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wrap(op, err)
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.rollbackTimeout())
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return &Error{
			Op:   op,
			Kind: KindInternal,
			Err:  fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, err),
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO schema_migrations (version, name, checksum)
		VALUES ($1, $2, $3)`, m.Version, m.Name, m.Checksum); err != nil {
		return wrap(op, err)
	}
	return wrap(op, tx.Commit(ctx))
}

// ensureMigrationsTable creates the bookkeeping table if it is absent.
//
// It is the one table in this schema WITHOUT a tenant column, and the reason
// is that it holds no tenant data: it records which files have run against
// this database. TestEveryTableCarriesTheTenantColumn names it as the single
// exception, so a table added without the column fails rather than joins it.
func (s *Store) ensureMigrationsTable(ctx context.Context, op string) error {
	_, err := s.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    bigint      PRIMARY KEY,
			name       text        NOT NULL,
			checksum   text        NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`)
	return wrap(op, err)
}

// appliedMigrations reads schema_migrations in version order.
func (s *Store) appliedMigrations(ctx context.Context, op string) ([]AppliedMigration, error) {
	rows, err := s.db.Query(ctx, `
		SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		if isUndefinedTable(err) {
			// A database that has never been migrated. Nothing is
			// applied, which is a state rather than a failure.
			return nil, nil
		}
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []AppliedMigration
	for rows.Next() {
		var a AppliedMigration
		if err := rows.Scan(&a.Version, &a.Name, &a.Checksum); err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, a)
	}
	return out, wrap(op, rows.Err())
}

// pendingMigrations diffs the files against what is recorded, and refuses two
// things on the way.
//
// An applied migration whose file has changed is an error: forward-only means
// the file that ran is the file on disk, and a checksum that has moved means
// two deployments built the same "version" from different SQL.
//
// So is a pending migration numbered BELOW one already applied. That is the
// shape a rebase produces — two branches each adding 0004 — and applying it
// out of order gives two databases that both say "up to date" with different
// schemas.
func pendingMigrations(op string, all []Migration, applied []AppliedMigration) ([]Migration, error) {
	byVersion := make(map[int64]AppliedMigration, len(applied))
	var highest int64
	for _, a := range applied {
		byVersion[a.Version] = a
		if a.Version > highest {
			highest = a.Version
		}
	}

	var pending []Migration
	for _, m := range all {
		a, ok := byVersion[m.Version]
		if ok {
			if a.Checksum != m.Checksum {
				return nil, &Error{Op: op, Kind: KindInternal, Err: fmt.Errorf(
					"migration %04d_%s has changed since it was applied "+
						"(recorded %s, on disk %s): migrations are forward-only, add a new one",
					m.Version, m.Name, short(a.Checksum), short(m.Checksum))}
			}
			continue
		}
		if m.Version < highest {
			return nil, &Error{Op: op, Kind: KindInternal, Err: fmt.Errorf(
				"migration %04d_%s is unapplied but %04d already ran: "+
					"renumber it above the highest applied version",
				m.Version, m.Name, highest)}
		}
		pending = append(pending, m)
	}
	return pending, nil
}

// short trims a checksum for an error message.
func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}

// SchemaVersion reports the highest applied migration version, or 0 when the
// database has never been migrated. 0015 reads it to report whether a
// deployment's schema is at the version its binary expects.
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	const op = "store.SchemaVersion"

	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	var version int64
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		if isUndefinedTable(err) || errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, wrap(op, err)
	}
	return version, nil
}
