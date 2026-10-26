// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// auditTie is one record of the tie test: its stream, its sequence in it, and
// the instant it records.
type auditTie struct {
	id     string
	stream string
	seq    int64
	at     time.Time
}

// The audit query's order (0036, M26.4) is recorded-at, stream and chain
// sequence. Records share an instant all the time — a proxy stamps a batch to
// the second — so a page boundary falls between two records of one instant,
// in one stream or in two, and the walk must continue exactly there, in both
// directions.
func TestAuditPagesBreakTiesAtOneInstantInBothDirections(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Second)

	// Newest first: t1 before t0; within an instant, stream descending; within
	// a stream, sequence descending.
	newestFirst := []auditTie{
		{"r-b2", "proxy:b", 2, t1},
		{"r-a3", "proxy:a", 3, t1},
		{"r-b1", "proxy:b", 1, t0},
		{"r-a2", "proxy:a", 2, t0},
		{"r-a1", "proxy:a", 1, t0},
		{"r-c1", "control", 1, t0},
	}
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		for _, r := range newestFirst {
			rec := storetest.AuditRecord(string(tenant)+"/"+r.id, r.stream, r.seq)
			rec.RecordedAt = r.at
			if err := st.Audit().Append(ctx, tenant, rec); err != nil {
				t.Fatalf("append %s in %s: %v", r.id, tenant, err)
			}
		}
	}
	ids := func(rs []auditTie) []string {
		out := make([]string, 0, len(rs))
		for _, r := range rs {
			out = append(out, string(tenantA)+"/"+r.id)
		}
		return out
	}
	oldestFirst := slices.Clone(newestFirst)
	slices.Reverse(oldestFirst)

	walkAudit := func(q store.AuditQuery, limit int) []string {
		t.Helper()
		var seen []string
		p := store.Page{Limit: limit}
		for range 20 {
			got, err := st.Audit().Page(ctx, tenantA, q, p)
			if err != nil {
				t.Fatalf("page after %v: %v", p.After, err)
			}
			if got.More && len(got.Rows) != limit {
				t.Fatalf("a page that continues holds %d records, want %d", len(got.Rows), limit)
			}
			for _, rec := range got.Rows {
				seen = append(seen, rec.RecordID)
			}
			if !got.More {
				return seen
			}
			last := got.Rows[len(got.Rows)-1]
			p.After = []any{last.RecordedAt, last.Stream, last.ChainSeq}
		}
		t.Fatal("the walk never ended")
		return nil
	}

	for _, limit := range []int{1, 2, 4} {
		if got, want := walkAudit(store.AuditQuery{}, limit), ids(newestFirst); !slices.Equal(got, want) {
			t.Errorf("newest first, %d a page: %v, want %v", limit, got, want)
		}
		if got, want := walkAudit(store.AuditQuery{Ascending: true}, limit), ids(oldestFirst); !slices.Equal(got, want) {
			t.Errorf("oldest first, %d a page: %v, want %v", limit, got, want)
		}
	}

	// A window narrows the walk and is never its position: the walk of t0's
	// instant alone still breaks its ties.
	window := store.AuditQuery{From: t0, To: t1}
	if got, want := walkAudit(window, 1), ids(newestFirst[2:]); !slices.Equal(got, want) {
		t.Errorf("the window [t0, t1), newest first: %v, want %v", got, want)
	}
	window.Ascending = true
	if got, want := walkAudit(window, 1), ids(oldestFirst[:4]); !slices.Equal(got, want) {
		t.Errorf("the window [t0, t1), oldest first: %v, want %v", got, want)
	}

	// Query, which the per-session readers keep, reads the same order.
	rows, err := st.Audit().Query(ctx, tenantA, store.AuditQuery{Ascending: true})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rows))
	for _, rec := range rows {
		got = append(got, rec.RecordID)
	}
	if want := ids(oldestFirst); !slices.Equal(got, want) {
		t.Errorf("Query, oldest first: %v, want %v", got, want)
	}
}

// The order is read from the index migration 0019 built, asked as
// grant_page_test.go asks: the generic plan with every other index on the
// table dropped and sequential scans off, so this is what the index CAN serve.
// Newest first reads it forwards, `?order=asc` backwards, and neither sorts.
// Each case also asks under the index 0019 replaced, and that plan sorts, so
// the assertion tells the two apart.
func TestTheAuditQueryReadsItsOrderFromAnIndex(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	after := []any{at, "proxy:a", int64(7)}
	for _, tc := range []struct {
		name       string
		q          store.AuditQuery
		p          store.Page
		scan       string
		positioned bool
	}{
		{"newest first", store.AuditQuery{}, store.Page{Limit: 1}, "Index Scan using", false},
		{"newest first after a position", store.AuditQuery{}, store.Page{Limit: 1, After: after}, "Index Scan using", true},
		{"oldest first", store.AuditQuery{Ascending: true}, store.Page{Limit: 1}, "Index Scan Backward using", false},
		{"oldest first after a position", store.AuditQuery{Ascending: true}, store.Page{Limit: 1, After: after}, "Index Scan Backward using", true},
		{"a window, newest first, after a position", store.AuditQuery{From: at.Add(-time.Hour), To: at},
			store.Page{Limit: 1, After: after}, "Index Scan using", true},
		{"a window, oldest first, after a position", store.AuditQuery{From: at.Add(-time.Hour), To: at, Ascending: true},
			store.Page{Limit: 1, After: after}, "Index Scan Backward using", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql := store.AuditPageSQL(tc.q, tc.p)
			plan := planWithOnly(t, st, "audit_records", "audit_records_order_idx", "", sql)
			if !strings.Contains(plan, tc.scan+" audit_records_order_idx") || strings.Contains(plan, "Sort") {
				t.Errorf("the page is not read in order from audit_records_order_idx (%s); with only it available the plan is:\n%s",
					tc.scan, plan)
			}
			// The row comparison is the scan's start, not a filter over
			// every row before it.
			if tc.positioned && !regexp.MustCompile(`Index Cond: .*ROW\(`).MatchString(plan) {
				t.Errorf("the position is not an index condition of audit_records_order_idx:\n%s", plan)
			}
			old := planWithOnly(t, st, "audit_records", "replaced_idx",
				"CREATE INDEX replaced_idx ON audit_records (tenant, recorded_at DESC)", sql)
			if !strings.Contains(old, "Sort") {
				t.Errorf("under the index 0019 replaced the page is planned without a sort, so this test cannot tell them apart:\n%s", old)
			}
		})
	}

	var n int
	if err := st.Pool().QueryRow(ctx, `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = current_schema() AND indexname = 'audit_records_recorded_idx'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("audit_records_recorded_idx, which 0019 replaced, is still in the schema")
	}
}
