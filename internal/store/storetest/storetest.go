// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package storetest is the Postgres-backed test harness every phase from 0003
// onwards builds its store fixtures with.
//
// It exists so that no phase has to invent its own, and it deliberately runs
// against a REAL database. The whole point of the repository layer is the SQL
// (PLAN M13), so a fake tests the fake: a mocked query returns whatever the
// mock was told, including for the uid cursor's lock and the audit table's
// uniqueness constraint, which are the two things in this schema that are
// correct only because Postgres enforces them.
//
// Isolation is a fresh schema per test rather than a rolled-back transaction.
// A transaction cannot hold the concurrency tests: N goroutines advancing one
// cursor need N connections, and connections inside one transaction are one
// connection. A schema costs a `CREATE SCHEMA` and a `DROP ... CASCADE`, and
// buys tests that are order-independent and can run in parallel.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoplock/control/internal/store"
)

// DSNEnv names the environment variable holding the test database DSN.
//
//	export HOPLOCK_TEST_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
//	go test ./internal/store/...
//
// See internal/store/README.md for a local setup that takes one command.
const DSNEnv = "HOPLOCK_TEST_DSN"

// New returns a Store connected to a private schema with every migration
// applied, and registers the cleanup that drops it.
//
// It takes a testing.TB rather than a *testing.T so that a BENCHMARK can have
// a real database too. M5 is a latency claim, and a benchmark measured against
// a fake measures the fake (0008).
//
// With DSNEnv unset the test is skipped — a contributor without Postgres can
// still run `go test ./...` — EXCEPT in CI, where a skip is indistinguishable
// from a pass in the log and this whole file would quietly stop being run. CI
// gets a failure naming the missing variable instead.
func New(t testing.TB) *store.Store {
	t.Helper()

	dsn := os.Getenv(DSNEnv)
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is unset in CI: the Postgres-backed tests must run there, "+
				"and a skipped test reads as a passing one", DSNEnv)
		}
		t.Skipf("%s is unset; see internal/store/README.md to run the "+
			"Postgres-backed tests locally", DSNEnv)
	}

	ctx := t.Context()
	schema := NewSchema(t, dsn)

	st, err := store.Open(ctx, DSNForSchema(dsn, schema))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)

	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// NewSchema creates an empty private schema and registers the cleanup that
// drops it. New builds on it; a test that needs an UNMIGRATED database — the
// dry-run test does — calls it directly.
func NewSchema(t testing.TB, dsn string) string {
	t.Helper()

	schema := "hoplock_test_" + randomSuffix(t)

	// Created through a throwaway connection: the Store under test is
	// opened with search_path already pointing at the schema, so nothing it
	// runs can touch another test's tables by forgetting to qualify a name.
	admin, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connect to %s: %v", DSNEnv, err)
	}
	defer admin.Close()

	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}
	t.Cleanup(func() { dropSchema(t, dsn, schema) })
	return schema
}

// DSN returns the configured test DSN, skipping or failing exactly as New
// does. A test that needs its own pool — the concurrency tests do — uses this.
func DSN(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv(DSNEnv)
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is unset in CI", DSNEnv)
		}
		t.Skipf("%s is unset; see internal/store/README.md", DSNEnv)
	}
	return dsn
}

// DSNForSchema rewrites a DSN so every connection from the pool starts in the
// given schema. It is how a test gets isolation without qualifying a name in
// any of the SQL under test.
func DSNForSchema(dsn, schema string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "search_path=" + schema
}

// dropSchema removes a test schema and everything in it.
func dropSchema(t testing.TB, dsn, schema string) {
	t.Helper()
	// Not t.Context(): cleanup runs after the test's context is cancelled,
	// and a schema left behind accumulates until somebody notices.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Errorf("drop schema %s: connect: %v", schema, err)
		return
	}
	defer admin.Close()

	if _, err := admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
		t.Errorf("drop schema %s: %v", schema, err)
	}
}

// randomSuffix returns a schema-name-safe random string.
func randomSuffix(t testing.TB) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("random suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// Fixture describes rows to seed for one tenant.
//
// It exists so a later phase writes what it needs rather than a page of
// INSERTs, and so that a two-tenant isolation test is two calls rather than
// two copies of the same setup.
type Fixture struct {
	Tenant   store.Tenant
	Subjects []store.Subject
	Targets  []store.Target
	Proxies  []store.Proxy
	Bundles  []store.PolicyBundle
	Grants   []store.Grant
	// UIDCursors seeds allocation cursors. Each entry is created, never
	// upserted: a cursor that already exists is a conflict, because
	// re-creating one is how it would go backwards.
	UIDCursors []UIDCursorFixture
	// Audit records are appended in the order given. The caller supplies
	// ChainSeq; 0010 owns what a correct chain looks like, and a harness
	// that computed it would be testing itself.
	Audit []store.AuditRecord
}

// UIDCursorFixture seeds one target's allocation cursor.
type UIDCursorFixture struct {
	TargetID string
	First    int64
	RangeEnd int64
}

// Seed writes a fixture, failing the test on the first error.
func Seed(t testing.TB, st *store.Store, f Fixture) {
	t.Helper()
	ctx := t.Context()

	if f.Tenant == "" {
		t.Fatal("storetest.Seed: fixture has no tenant")
	}

	for _, s := range f.Subjects {
		if err := st.Subjects().Upsert(ctx, f.Tenant, s); err != nil {
			t.Fatalf("seed subject %s: %v", s.ID, err)
		}
	}
	for _, tgt := range f.Targets {
		if err := st.Targets().Upsert(ctx, f.Tenant, tgt); err != nil {
			t.Fatalf("seed target %s: %v", tgt.ID, err)
		}
	}
	for _, p := range f.Proxies {
		if err := st.Proxies().Upsert(ctx, f.Tenant, p); err != nil {
			t.Fatalf("seed proxy %s: %v", p.ID, err)
		}
	}
	for _, b := range f.Bundles {
		if err := st.PolicyBundles().Insert(ctx, f.Tenant, b); err != nil {
			t.Fatalf("seed bundle %d: %v", b.Version, err)
		}
	}
	for _, g := range f.Grants {
		if err := st.Grants().Insert(ctx, f.Tenant, g); err != nil {
			t.Fatalf("seed grant %s: %v", g.ID, err)
		}
	}
	for _, c := range f.UIDCursors {
		if err := st.UIDCursors().Create(ctx, f.Tenant, c.TargetID, c.First, c.RangeEnd); err != nil {
			t.Fatalf("seed uid cursor %s: %v", c.TargetID, err)
		}
	}
	for _, r := range f.Audit {
		if err := st.Audit().Append(ctx, f.Tenant, r); err != nil {
			t.Fatalf("seed audit record %s: %v", r.RecordID, err)
		}
	}
}

// The constructors below build a plausible row with one distinguishing field,
// so a test names what it cares about and nothing else.

// Subject returns a subject with the given id.
func Subject(id string, groups ...string) store.Subject {
	return store.Subject{
		ID:          id,
		Source:      "local",
		DisplayName: id,
		Principals:  []string{id},
		Groups:      groups,
		Claims:      map[string]string{"sub": id},
	}
}

// Target returns a target with the given id and hostname.
func Target(id, hostname string, labels map[string]string) store.Target {
	return store.Target{
		ID:               id,
		Hostname:         hostname,
		Zone:             "zone-a",
		Labels:           labels,
		CredentialMethod: "ephemeral-user",
	}
}

// Proxy returns an enrolled proxy with the given id.
func Proxy(id, zone string) store.Proxy {
	return store.Proxy{
		ID:        id,
		Zone:      zone,
		PublicKey: []byte("public-key-" + id),
		State:     store.EnrollmentEnrolled,
	}
}

// Bundle returns a policy bundle at the given version.
func Bundle(version int64, active bool) store.PolicyBundle {
	source := fmt.Sprintf("# policy bundle v%d\n", version)
	return store.PolicyBundle{
		Version:    version,
		Source:     []byte(source),
		Hash:       fmt.Sprintf("sha256:%064d", version),
		UploadedBy: "storetest",
		Active:     active,
	}
}

// Grant returns a live grant for a subject, valid for d from now.
func Grant(id, subjectID string, d time.Duration) store.Grant {
	now := time.Now().UTC()
	return store.Grant{
		ID:        id,
		SubjectID: subjectID,
		Scope:     "target:*",
		NotBefore: now.Add(-time.Minute),
		ExpiresAt: now.Add(d),
		Origin:    store.GrantOriginManual,
	}
}

// AuditRecord returns an audit record at a position in a stream.
//
// It is NOT chained: the hash fields are empty, because a fixture that made up
// a chain would let a test pass verification against hashes nothing computed.
// A test that needs a real chain builds one through internal/audit, which is
// the only thing that may.
func AuditRecord(recordID, stream string, seq int64) store.AuditRecord {
	return store.AuditRecord{
		RecordID:   recordID,
		Stream:     stream,
		ChainSeq:   seq,
		SessionID:  "session-" + stream,
		Kind:       "session_start",
		Severity:   "info",
		Body:       `{"seeded":true}`,
		RecordedAt: time.Now().UTC(),
	}
}
