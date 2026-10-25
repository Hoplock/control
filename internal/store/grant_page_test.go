// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The grant lists, paged (0033). The promises every paged reader keeps are in
// page_test.go, which holds Grants.Page and GrantRequests.PageForSubject to
// them; these are the ones only these lists make.

// A state is judged at the instant each page is read, so a grant can change
// state between pages. A walk still visits each grant at most once, because
// the position is creation, which never moves: a grant that expires mid-walk
// is simply not on the pages read after it expired.
func TestAGrantWalkUnderAStateFilterVisitsEachGrantAtMostOnce(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	// g-00 is the newest. Grant i is live throughout when i%3 == 0, expires i
	// minutes into the walk when i%3 == 1, and becomes live i minutes into it
	// when i%3 == 2.
	var always []string
	for i := range 12 {
		id := fmt.Sprintf("g-%02d", i)
		g := storetest.Grant(id, "alice", time.Hour)
		g.NotBefore, g.ExpiresAt = start.Add(-time.Hour), start.Add(24*time.Hour)
		switch i % 3 {
		case 0:
			always = append(always, id)
		case 1:
			g.ExpiresAt = start.Add(time.Duration(i) * time.Minute)
		case 2:
			g.NotBefore = start.Add(time.Duration(i) * time.Minute)
		}
		if err := st.Grants().Insert(ctx, tenantA, g); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
		if err := store.SetGrantCreatedAt(ctx, st, tenantA, id, start.Add(-time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("place %s: %v", id, err)
		}
	}

	// One grant a page, the clock two minutes on for each: the instant
	// every page is judged at, as access.Service.GrantPage judges it.
	seen := map[string]int{}
	var order []string
	p := store.Page{Limit: 1}
	for page := 0; ; page++ {
		if page > 12 {
			t.Fatal("the walk did not end")
		}
		at := start.Add(time.Duration(2*page) * time.Minute)
		got, err := st.Grants().Page(ctx, tenantA, store.GrantQuery{State: store.GrantActive, At: at}, p)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, g := range got.Rows {
			seen[g.ID]++
			order = append(order, g.ID)
			if state := g.State(at); state != store.GrantActive {
				t.Errorf("page %d at %s holds %s, which is %s then", page, at.Format(time.TimeOnly), g.ID, state)
			}
		}
		if !got.More {
			break
		}
		last := got.Rows[len(got.Rows)-1]
		p.After = []any{last.CreatedAt, last.ID}
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%s was visited %d times", id, n)
		}
	}
	if !slices.IsSorted(order) {
		t.Errorf("the walk went backwards: %v", order)
	}
	for _, id := range always {
		if seen[id] != 1 {
			t.Errorf("%s was live for the whole walk and was seen %d times", id, seen[id])
		}
	}
	// What each page's instant says: g-10 expired before its page was
	// read, and g-11 had become live by then.
	if want := []string{"g-00", "g-02", "g-03", "g-05", "g-06", "g-08", "g-09", "g-11"}; !slices.Equal(order, want) {
		t.Errorf("the walk saw %v, want %v", order, want)
	}
}

// The tiebreak is the id, in the time's direction (M26.4): grants created in
// the same instant, and requests raised in it, are walked by id descending,
// each once, and the walk continues across the instant.
func TestGrantPagesFollowTheirWholeKey(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	// Three in one instant, one either side.
	placed := map[string]time.Time{"x-b": at, "x-c": at, "x-a": at, "y-1": at.Add(time.Second), "w-1": at.Add(-time.Second)}
	want := []string{"y-1", "x-c", "x-b", "x-a", "w-1"}
	for id, created := range placed {
		if err := st.Grants().Insert(ctx, tenantA, storetest.Grant(id, "alice", time.Hour)); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
		if err := store.SetGrantCreatedAt(ctx, st, tenantA, id, created); err != nil {
			t.Fatalf("place %s: %v", id, err)
		}
		req := storetest.GrantRequest(id, "alice", time.Hour)
		req.RequestedAt = created
		if err := st.GrantRequests().Insert(ctx, tenantA, req); err != nil {
			t.Fatalf("insert request %s: %v", id, err)
		}
	}

	for name, page := range map[string]func(store.Page) ([]string, []any, bool){
		"grants": func(p store.Page) ([]string, []any, bool) {
			got, err := st.Grants().Page(ctx, tenantA, store.GrantQuery{}, p)
			if err != nil {
				t.Fatalf("grants after %v: %v", p.After, err)
			}
			last := got.Rows[len(got.Rows)-1]
			return []string{last.ID}, []any{last.CreatedAt, last.ID}, got.More
		},
		"a holder's grants": func(p store.Page) ([]string, []any, bool) {
			got, err := st.Grants().Page(ctx, tenantA, store.GrantQuery{SubjectID: "alice"}, p)
			if err != nil {
				t.Fatalf("alice's grants after %v: %v", p.After, err)
			}
			last := got.Rows[len(got.Rows)-1]
			return []string{last.ID}, []any{last.CreatedAt, last.ID}, got.More
		},
		"own requests": func(p store.Page) ([]string, []any, bool) {
			got, err := st.GrantRequests().PageForSubject(ctx, tenantA, "alice", store.GrantRequestQuery{}, p)
			if err != nil {
				t.Fatalf("requests after %v: %v", p.After, err)
			}
			last := got.Rows[len(got.Rows)-1]
			return []string{last.ID}, []any{last.RequestedAt, last.ID}, got.More
		},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			p := store.Page{Limit: 1}
			for range len(want) + 1 {
				ids, after, more := page(p)
				seen = append(seen, ids...)
				if !more {
					break
				}
				p.After = after
			}
			if !slices.Equal(seen, want) {
				t.Errorf("the walk saw %v, want %v", seen, want)
			}
		})
	}
}

// Whose requests a page reads is the subject the reader is handed, never the
// position (M18): alice's position, presented for bob, continues bob's list
// from that place and reads only his requests.
func TestARequestersPositionReadsOnlyAnothersOwnRequests(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, holder := range []string{"alice", "bob", "alice", "bob", "alice", "bob"} {
		req := storetest.GrantRequest(fmt.Sprintf("r-%d-%s", i, holder), holder, time.Hour)
		req.RequestedAt = at.Add(-time.Duration(i) * time.Minute)
		if err := st.GrantRequests().Insert(ctx, tenantA, req); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	first, err := st.GrantRequests().PageForSubject(ctx, tenantA, "alice", store.GrantRequestQuery{}, store.Page{Limit: 1})
	if err != nil || len(first.Rows) != 1 || first.Rows[0].ID != "r-0-alice" || !first.More {
		t.Fatalf("alice's first page: %+v, %v", first, err)
	}
	pos := []any{first.Rows[0].RequestedAt, first.Rows[0].ID}
	got, err := st.GrantRequests().PageForSubject(ctx, tenantA, "bob", store.GrantRequestQuery{},
		store.Page{Limit: store.DefaultPageLimit, After: pos})
	if err != nil {
		t.Fatalf("bob after alice's position: %v", err)
	}
	var ids []string
	for _, req := range got.Rows {
		ids = append(ids, req.ID)
		if req.SubjectID != "bob" {
			t.Errorf("bob's page holds %s, which is %s's", req.ID, req.SubjectID)
		}
	}
	if want := []string{"r-1-bob", "r-3-bob", "r-5-bob"}; !slices.Equal(ids, want) || got.More {
		t.Errorf("bob after alice's position read %v (more %t), want %v", ids, got.More, want)
	}
}

// The grant view's sample is bounded by the server and says when the bound
// cut it (M26.3): one past the limit is read to know, and never returned.
func TestListByGrantSaysWhenItsBoundCutsIt(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		if err := st.Decisions().Insert(ctx, tenantA, store.Decision{
			ID: fmt.Sprintf("d-%d", i), SubjectID: "alice", InputsDigest: "x", Effect: store.DecisionEffectAllow,
			GrantID: "g-1", DecidedAt: at.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	for _, c := range []struct {
		limit int
		ids   []string
		cut   bool
	}{
		{limit: 2, ids: []string{"d-2", "d-1"}, cut: true},
		{limit: 3, ids: []string{"d-2", "d-1", "d-0"}, cut: false},
		{limit: 4, ids: []string{"d-2", "d-1", "d-0"}, cut: false},
	} {
		got, cut, err := st.Decisions().ListByGrant(ctx, tenantA, "g-1", c.limit)
		if err != nil {
			t.Fatalf("limit %d: %v", c.limit, err)
		}
		var ids []string
		for _, d := range got {
			ids = append(ids, d.ID)
		}
		if !slices.Equal(ids, c.ids) || cut != c.cut {
			t.Errorf("limit %d: %v cut=%t, want %v cut=%t", c.limit, ids, cut, c.ids, c.cut)
		}
	}
}

// Both orders are read from an index that holds them whole (migration 0017):
// the plan is an index scan with no sort above it, on the first page and
// after a position, under every filter. Asked as audit_plan_test.go asks it —
// the generic plan, inside a rolled-back transaction, with every other index
// on the table dropped and sequential scans off — so this is what the index
// CAN serve. Each case also builds the index 0017 replaced in its place and
// asks again: that plan sorts, so the assertion tells the two apart, and the
// replaced indexes are gone from the schema.
func TestTheGrantListsReadTheirOrderFromAnIndex(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	after := []any{time.Now(), "id"}
	at := time.Now()

	for _, tc := range []struct {
		name, table, index, replaced string
		sql                          string
	}{
		{"grants", "grants", "grants_created_order_idx", "(tenant, created_at DESC, grant_id)",
			store.GrantPageSQL(store.GrantQuery{}, store.Page{Limit: 1})},
		{"grants after a position", "grants", "grants_created_order_idx", "(tenant, created_at DESC, grant_id)",
			store.GrantPageSQL(store.GrantQuery{}, store.Page{Limit: 1, After: after})},
		{"active grants after a position", "grants", "grants_created_order_idx", "(tenant, created_at DESC, grant_id)",
			store.GrantPageSQL(store.GrantQuery{State: store.GrantActive, At: at}, store.Page{Limit: 1, After: after})},
		{"a holder's grants", "grants", "grants_subject_order_idx", "(tenant, subject_id, created_at DESC)",
			store.GrantPageSQL(store.GrantQuery{SubjectID: "alice"}, store.Page{Limit: 1})},
		{"a holder's expired grants after a position", "grants", "grants_subject_order_idx", "(tenant, subject_id, created_at DESC)",
			store.GrantPageSQL(store.GrantQuery{SubjectID: "alice", State: store.GrantExpired, At: at}, store.Page{Limit: 1, After: after})},
		{"own requests", "grant_requests", "grant_requests_subject_order_idx", "(tenant, subject_id, requested_at DESC)",
			store.GrantRequestPageSQL(store.GrantRequestQuery{}, store.Page{Limit: 1})},
		{"own pending requests after a position", "grant_requests", "grant_requests_subject_order_idx", "(tenant, subject_id, requested_at DESC)",
			store.GrantRequestPageSQL(store.GrantRequestQuery{State: store.GrantRequestPending}, store.Page{Limit: 1, After: after})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := planWithOnly(t, st, tc.table, tc.index, "", tc.sql)
			if !strings.Contains(plan, "Index Scan using "+tc.index) || strings.Contains(plan, "Sort") {
				t.Errorf("the page is not read in order from %s; with only it available the plan is:\n%s", tc.index, plan)
			}
			old := planWithOnly(t, st, tc.table, "replaced_idx", "CREATE INDEX replaced_idx ON "+tc.table+" "+tc.replaced, tc.sql)
			if !strings.Contains(old, "Sort") {
				t.Errorf("under the index 0017 replaced, %s, the page is planned without a sort, so this test cannot tell them apart:\n%s",
					tc.replaced, old)
			}
		})
	}

	rows, err := st.Pool().Query(ctx, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND indexname IN ('grants_by_created_idx', 'grants_by_subject_idx', 'grant_requests_by_subject_idx')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		t.Errorf("%s, which 0017 replaced, is still in the schema", name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// planWithOnly is the generic plan of sql with index the only index left on
// table, after running build (when it is not empty) to make it, inside a
// transaction that is rolled back.
func planWithOnly(t *testing.T, st *store.Store, table, index, build, sql string) string {
	t.Helper()
	ctx := t.Context()
	tx, err := st.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if build != "" {
		if _, err := tx.Exec(ctx, build); err != nil {
			t.Fatalf("%s: %v", build, err)
		}
	}
	rows, err := tx.Query(ctx, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = current_schema() AND tablename = $1 AND indexname <> $2`, table, index)
	if err != nil {
		t.Fatal(err)
	}
	var others []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		others = append(others, name)
	}
	rows.Close()
	for _, name := range others {
		stmt := "DROP INDEX " + name
		if name == table+"_pkey" {
			stmt = "ALTER TABLE " + table + " DROP CONSTRAINT " + name + " CASCADE"
		}
		if _, err := tx.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatal(err)
	}

	// The simple protocol, because a generic plan is asked for with its
	// parameters unbound and the extended one binds them.
	results, err := tx.Conn().PgConn().Exec(ctx, "EXPLAIN (GENERIC_PLAN) "+sql).ReadAll()
	if err != nil {
		t.Fatalf("explain: %v\n%s", err, sql)
	}
	var lines []string
	for _, row := range results[0].Rows {
		lines = append(lines, string(row[0]))
	}
	return strings.Join(lines, "\n")
}
