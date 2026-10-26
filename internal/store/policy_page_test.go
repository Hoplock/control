// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The policy version history, paged, and a session's decisions, signalled
// (0035). page_test.go holds the version reader to every paged reader's
// promises; these hold what only it promises, and ListBySession's bound.

// bundleVersion is the version the item put as id is stored under: positive,
// and smaller for a later id, so a walk of the history, newest first, reads
// the ids in ascending order as every other list here does.
func bundleVersion(id string) int64 {
	return newestFirst(id).UnixMicro()
}

// A version's source is bounded only by the upload body, so the history never
// reads it (0035): a page carries every other column of a version, no source,
// and the statement asks Postgres for none. The source here is large enough
// to be stored out of line, which is the read a list would pay for.
func TestAVersionPageReadsNoSource(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	large := bytes.Repeat([]byte("# a large policy\n"), 1<<16) // over 1 MiB
	for v := int64(1); v <= 3; v++ {
		b := storetest.Bundle(v, v == 2)
		if v == 3 {
			b.Source = large
		}
		if err := st.PolicyBundles().Insert(ctx, tenantA, b); err != nil {
			t.Fatalf("insert v%d: %v", v, err)
		}
	}
	if err := st.PolicyBundles().Activate(ctx, tenantA, 2); err != nil {
		t.Fatalf("activate: %v", err)
	}

	got, err := st.PolicyBundles().Page(ctx, tenantA, store.Page{Limit: store.DefaultPageLimit})
	if err != nil || got.More || len(got.Rows) != 3 {
		t.Fatalf("page: %d rows more=%t, %v; want the three versions", len(got.Rows), got.More, err)
	}
	for i, b := range got.Rows {
		want := storetest.Bundle(int64(3-i), i == 1)
		if b.Version != want.Version || b.Hash != want.Hash || b.UploadedBy != want.UploadedBy ||
			b.Active != want.Active || b.UploadedAt.IsZero() {
			t.Errorf("row %d is %+v, want version %d as stored, newest first", i, b, want.Version)
		}
		if b.Source != nil {
			t.Errorf("version %d carries %d bytes of source; a list of versions reads none", b.Version, len(b.Source))
		}
	}
	// Get still reads one version whole.
	if one, err := st.PolicyBundles().Get(ctx, tenantA, 3); err != nil || !bytes.Equal(one.Source, large) {
		t.Fatalf("get v3: %d bytes, %v; want its whole source", len(one.Source), err)
	}

	// What the statement asks for, as Postgres plans it: the version
	// columns, read backwards from the primary key, and no source.
	for name, p := range map[string]store.Page{
		"the first page":       {Limit: 1},
		"after a position":     {Limit: 1, After: []any{int64(3)}},
		"at the maximum limit": {Limit: store.MaxPageLimit},
	} {
		t.Run(name, func(t *testing.T) {
			plan := verbosePlan(t, st, store.BundlePageSQL(p))
			if !strings.Contains(plan, "Index Scan Backward using policy_bundles_pkey") || strings.Contains(plan, "Sort") {
				t.Errorf("the page is not read in order from the primary key:\n%s", plan)
			}
			if !strings.Contains(plan, "Output: version, hash, uploaded_by, uploaded_at, active") || strings.Contains(plan, "source") {
				t.Errorf("the page reads more than a version's columns:\n%s", plan)
			}
		})
	}
}

// verbosePlan is the generic plan of sql with the columns each step outputs.
func verbosePlan(t *testing.T, st *store.Store, sql string) string {
	t.Helper()
	ctx := t.Context()
	conn, err := st.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	// The simple protocol, because a generic plan is asked for with its
	// parameters unbound and the extended one binds them.
	results, err := conn.Conn().PgConn().Exec(ctx, "EXPLAIN (GENERIC_PLAN, VERBOSE) "+sql).ReadAll()
	if err != nil {
		t.Fatalf("explain: %v\n%s", err, sql)
	}
	var lines []string
	for _, row := range results[0].Rows {
		lines = append(lines, string(row[0]))
	}
	return strings.Join(lines, "\n")
}

// A session's decisions are bounded by its hops, not by the estate, so the
// list keeps its server-side bound and says when the bound cut it (M26.3):
// ListBySession reads one past its limit to know.
func TestListBySessionSaysWhenItsBoundCutsIt(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	at := time.Now().UTC()
	for session, n := range map[string]int{"sess-at": 3, "sess-past": 4} {
		for i := range n {
			if err := st.Decisions().Insert(ctx, tenantA, store.Decision{
				ID: fmt.Sprintf("%s-d-%d", session, i), SubjectID: "alice", InputsDigest: "x", Effect: "deny",
				SessionID: session, DecidedAt: at.Add(time.Duration(i) * time.Second),
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
	}
	// Another tenant's decisions under the same session id are not counted.
	if err := st.Decisions().Insert(ctx, tenantB, store.Decision{
		ID: "b-1", SubjectID: "alice", InputsDigest: "x", Effect: "deny", SessionID: "sess-at", DecidedAt: at,
	}); err != nil {
		t.Fatalf("seed b: %v", err)
	}

	for _, c := range []struct {
		session   string
		limit     int
		n         int
		truncated bool
		newest    string
	}{
		{"sess-at", 3, 3, false, "sess-at-d-2"},
		{"sess-past", 3, 3, true, "sess-past-d-3"},
		{"sess-past", 4, 4, false, "sess-past-d-3"},
		{"sess-none", 3, 0, false, ""},
	} {
		got, truncated, err := st.Decisions().ListBySession(ctx, tenantA, c.session, c.limit)
		if err != nil {
			t.Fatalf("%s limit %d: %v", c.session, c.limit, err)
		}
		if len(got) != c.n || truncated != c.truncated {
			t.Errorf("%s limit %d: %d decisions truncated=%t, want %d truncated=%t",
				c.session, c.limit, len(got), truncated, c.n, c.truncated)
		}
		if c.n > 0 && got[0].ID != c.newest {
			t.Errorf("%s limit %d starts at %s, want the newest, %s", c.session, c.limit, got[0].ID, c.newest)
		}
	}

	// The server's own bound, when the caller names none, is 100.
	for i := range 101 {
		if err := st.Decisions().Insert(ctx, tenantA, store.Decision{
			ID: fmt.Sprintf("long-%03d", i), SubjectID: "alice", InputsDigest: "x", Effect: "deny",
			SessionID: "sess-long", DecidedAt: at.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("seed long: %v", err)
		}
	}
	got, truncated, err := st.Decisions().ListBySession(ctx, tenantA, "sess-long", 0)
	if err != nil || len(got) != 100 || !truncated {
		t.Errorf("101 decisions under the default bound: %d, truncated=%t, %v; want 100 and truncated", len(got), truncated, err)
	}
}
