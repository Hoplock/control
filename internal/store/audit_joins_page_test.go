// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The negotiated sessions and the showcase join, paged (0037, M26). The paging
// harness (page_test.go) holds both to every promise a list makes; the tests
// here hold what is particular to each: an aggregate ordered by each session's
// latest MATCHING record, continued through HAVING, and a join continued on its
// record's place.

// putSessionRecord stores one record of session at at, as sequence seq of
// stream, answering negotiatedSelection when it matches.
func putSessionRecord(t *testing.T, st *store.Store, tenant store.Tenant, id, stream string, seq int64,
	session string, at time.Time, matches bool,
) {
	t.Helper()
	rec := storetest.AuditRecord(id, stream, seq)
	rec.SessionID, rec.RecordedAt = session, at
	if matches {
		rec.Negotiated = []string{negotiatedSelection.Negotiated}
	}
	if err := st.Audit().Append(t.Context(), tenant, rec); err != nil {
		t.Fatalf("put %s: %v", id, err)
	}
}

// walkSessions reads the negotiated sessions to their end, limit a page,
// calling between after each page with the position the next one starts
// from, and answers every session read in order with its latest instant.
func walkSessions(t *testing.T, st *store.Store, tenant store.Tenant, limit int, between func([]any)) []store.AuditSession {
	t.Helper()
	var seen []store.AuditSession
	p := store.Page{Limit: limit}
	for range store.MaxPageLimit {
		got, err := st.Audit().Sessions(t.Context(), tenant, negotiatedSelection, p)
		if err != nil {
			t.Fatalf("page after %v: %v", p.After, err)
		}
		if got.More && len(got.Rows) != limit {
			t.Fatalf("a page that continues holds %d sessions, want exactly %d", len(got.Rows), limit)
		}
		seen = append(seen, got.Rows...)
		if !got.More {
			return seen
		}
		last := got.Rows[len(got.Rows)-1]
		p.After = []any{last.LatestAt, last.SessionID}
		if between != nil {
			between(p.After)
		}
	}
	t.Fatal("the walk never ended")
	return nil
}

func reversed(ids []string) []string {
	out := slices.Clone(ids)
	slices.Reverse(out)
	return out
}

func sessionNames(ss []store.AuditSession) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.SessionID)
	}
	return out
}

// A session's place is its latest MATCHING record: a newer record that does
// not answer the question does not move it, a session-less record names no
// session, and two sessions whose latest records share an instant are ordered
// by id, neither skipped at a page boundary between them. The boundary is
// HAVING's tie: `max(recorded_at) = $t AND session_id > $s`.
func TestNegotiatedSessionsAreOrderedByTheirLatestMatchingRecord(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		prefix := string(tenant) + "/"
		put := func(id string, seq int64, session string, when time.Time, matches bool) {
			putSessionRecord(t, st, tenant, prefix+id, prefix+"proxy:p", seq, prefix+session, when, matches)
		}
		put("a-old", 1, "sess-a", at.Add(-2*time.Hour), true)
		put("c-1", 2, "sess-c", at.Add(-time.Hour), true)
		put("a-new", 3, "sess-a", at, true)
		put("b-1", 4, "sess-b", at, true)
		put("a-later", 5, "sess-a", at.Add(time.Hour), false) // newer, and not an answer
		putSessionRecord(t, st, tenant, prefix+"orphan", prefix+"proxy:p", 6, "", at.Add(2*time.Hour), true)
	}

	want := []store.AuditSession{
		{SessionID: "tenant-a/sess-a", LatestAt: at},
		{SessionID: "tenant-a/sess-b", LatestAt: at},
		{SessionID: "tenant-a/sess-c", LatestAt: at.Add(-time.Hour)},
	}
	for _, limit := range []int{1, 2, 3, 10} {
		got := walkSessions(t, st, tenantA, limit, nil)
		if len(got) != len(want) {
			t.Fatalf("limit %d: the walk read %v, want %v", limit, got, want)
		}
		for i := range want {
			if got[i].SessionID != want[i].SessionID || !got[i].LatestAt.Equal(want[i].LatestAt) {
				t.Errorf("limit %d: session %d is %+v, want %+v", limit, i, got[i], want[i])
			}
		}
	}

	// The boundary between the two sessions at one instant, from either
	// side of it, and from a position between them that names no session.
	cases := []struct {
		after []any
		want  []string
	}{
		{[]any{at, "tenant-a/sess-a"}, []string{"tenant-a/sess-b", "tenant-a/sess-c"}},
		{[]any{at, "tenant-a/sess-a0"}, []string{"tenant-a/sess-b", "tenant-a/sess-c"}},
		{[]any{at, "tenant-a/sess-b"}, []string{"tenant-a/sess-c"}},
		{[]any{at, ""}, []string{"tenant-a/sess-a", "tenant-a/sess-b", "tenant-a/sess-c"}},
		{[]any{at.Add(time.Microsecond), "tenant-a/zzz"}, []string{"tenant-a/sess-a", "tenant-a/sess-b", "tenant-a/sess-c"}},
		{[]any{at.Add(-time.Hour), "tenant-a/sess-c"}, []string{}},
	}
	for _, c := range cases {
		got, err := st.Audit().Sessions(t.Context(), tenantA, negotiatedSelection, store.Page{Limit: 10, After: c.after})
		if err != nil {
			t.Fatalf("after %v: %v", c.after, err)
		}
		if names := sessionNames(got.Rows); !slices.Equal(names, c.want) || got.More {
			t.Errorf("after %v: %v (more=%t), want %v", c.after, names, got.More, c.want)
		}
	}

	// No cursor crosses (M18): tenant-a's position read in tenant-b
	// continues tenant-b's list, and reads none of tenant-a's sessions.
	got, err := st.Audit().Sessions(t.Context(), tenantB, negotiatedSelection,
		store.Page{Limit: 10, After: []any{at, "tenant-a/sess-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if names := sessionNames(got.Rows); !slices.Equal(names, []string{"tenant-b/sess-a", "tenant-b/sess-b", "tenant-b/sess-c"}) {
		t.Errorf("tenant-b after tenant-a's position read %v", names)
	}
}

// A session that gains a newer matching record mid-walk moves toward the head
// of the order, behind the walk's position, so a walk never reads it twice. A
// later walk reads it at its new place. One the walk had not reached when it
// moved is behind the position too, so this walk misses it and the next finds
// it: the learnings say why that is the price of a keyset over an aggregate.
func TestASessionThatMovesMidWalkIsNeverReadTwiceInOneWalk(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i := range 5 {
		id := fmt.Sprintf("s-%d", i+1) // s-1 the newest
		putSessionRecord(t, st, tenantA, id+"-r1", "proxy:"+id, 1, id, at.Add(-time.Duration(i)*time.Minute), true)
	}

	pages := 0
	seen := walkSessions(t, st, tenantA, 1, func([]any) {
		pages++
		if pages != 2 {
			return
		}
		// The walk has read s-1 and s-2. s-1 and s-4 gain a newer
		// matching record: one listed, one not yet reached.
		putSessionRecord(t, st, tenantA, "s-1-r2", "proxy:s-1", 2, "s-1", at.Add(time.Minute), true)
		putSessionRecord(t, st, tenantA, "s-4-r2", "proxy:s-4", 2, "s-4", at.Add(2*time.Minute), true)
	})
	if names := sessionNames(seen); !slices.Equal(names, []string{"s-1", "s-2", "s-3", "s-5"}) {
		t.Fatalf("the walk read %v, want s-1 once and s-4, which moved behind it, not at all", names)
	}

	again := walkSessions(t, st, tenantA, 2, nil)
	if names := sessionNames(again); !slices.Equal(names, []string{"s-4", "s-1", "s-2", "s-3", "s-5"}) {
		t.Errorf("a later walk read %v, want each session once at its new place", names)
	}
}

// The showcase join pages on its record's place in the audit order, newest
// first, so a walk is exact under ties: records of one instant in two streams,
// a window, and the rows the join leaves out (a command that was not blocked,
// a blocked one on a target outside the label) never take a place.
func TestBlockedCommandPagesAreExactUnderTies(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := st.Targets().Upsert(ctx, tenantA, blockedTarget); err != nil {
		t.Fatal(err)
	}
	dev := storetest.Target("t-dev", "dev.example", map[string]string{"env": "dev"})
	if err := st.Targets().Upsert(ctx, tenantA, dev); err != nil {
		t.Fatal(err)
	}
	put := func(id, stream string, seq int64, when time.Time, target, action string) {
		t.Helper()
		rec := storetest.AuditRecord(id, stream, seq)
		rec.Kind, rec.Target, rec.RecordedAt = store.KindCommand, target, when
		rec.Attributes = map[string]string{"action": action, "command": "rm -rf /"}
		if err := st.Audit().Append(ctx, tenantA, rec); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	for _, stream := range []string{"proxy:x", "proxy:y"} {
		put(stream+"/old", stream, 1, at.Add(-time.Hour), blockedTarget.Hostname, store.ActionBlockCommand)
		for seq := int64(2); seq <= 4; seq++ {
			put(fmt.Sprintf("%s/%d", stream, seq), stream, seq, at, blockedTarget.Hostname, store.ActionBlockCommand)
		}
		put(stream+"/allowed", stream, 5, at, blockedTarget.Hostname, "allow")
		put(stream+"/dev", stream, 6, at, dev.Hostname, store.ActionBlockCommand)
	}

	all := []string{
		"proxy:y/4", "proxy:y/3", "proxy:y/2", "proxy:x/4", "proxy:x/3", "proxy:x/2",
		"proxy:y/old", "proxy:x/old",
	}
	read := blockedIDs(store.BlockedCommandQuery{LabelName: "env", LabelValue: "prod"})
	windowed := blockedIDs(store.BlockedCommandQuery{LabelName: "env", LabelValue: "prod", From: at})
	oldest := blockedIDs(store.BlockedCommandQuery{LabelName: "env", LabelValue: "prod", Ascending: true})
	for _, c := range []struct {
		name string
		read func(context.Context, *store.Store, store.Tenant, store.Page) ([]string, bool, error)
		want []string
	}{
		{"the whole join", read, all},
		{"from the tied instant", windowed, all[:6]},
		{"oldest first", oldest, reversed(all)},
	} {
		for _, limit := range []int{1, 2, 4, 10} {
			var seen []string
			p := store.Page{Limit: limit}
			for {
				ids, more, err := c.read(ctx, st, tenantA, p)
				if err != nil {
					t.Fatalf("%s, limit %d, after %v: %v", c.name, limit, p.After, err)
				}
				seen = append(seen, ids...)
				if !more {
					break
				}
				last := ids[len(ids)-1]
				rec, err := st.Audit().Get(ctx, tenantA, last)
				if err != nil {
					t.Fatal(err)
				}
				p.After = []any{rec.RecordedAt, rec.Stream, rec.ChainSeq}
			}
			if !slices.Equal(seen, c.want) {
				t.Errorf("%s, limit %d: the walk read %v, want %v", c.name, limit, seen, c.want)
			}
		}
	}
}

// The join is read in the audit order from `audit_records_order_idx`, as
// the audit query is (0036): with only it left on the records, a page is an
// index scan that never sorts, in either direction, and its position is the
// scan's start rather than a filter over every record before it.
func TestTheShowcaseJoinReadsItsOrderFromAnIndex(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	after := []any{at, "proxy:a", int64(7)}
	for _, tc := range []struct {
		name      string
		ascending bool
		p         store.Page
		scan      string
	}{
		{"newest first", false, store.Page{Limit: 1}, "Index Scan using"},
		{"newest first after a position", false, store.Page{Limit: 1, After: after}, "Index Scan using"},
		{"oldest first", true, store.Page{Limit: 1}, "Index Scan Backward using"},
		{"oldest first after a position", true, store.Page{Limit: 1, After: after}, "Index Scan Backward using"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			plan := planWithOnly(t, st, "audit_records", "audit_records_order_idx", "", store.BlockedPageSQL(tc.ascending, p))
			if !strings.Contains(plan, tc.scan+" audit_records_order_idx") || strings.Contains(plan, "Sort") {
				t.Errorf("the join is not read in order from audit_records_order_idx (%s); with only it available the plan is:\n%s", tc.scan, plan)
			}
			if p.After != nil && !regexp.MustCompile(`Index Cond: .*ROW\(`).MatchString(plan) {
				t.Errorf("the position is not an index condition of audit_records_order_idx:\n%s", plan)
			}
		})
	}
}

// No index serves the negotiated sessions' order (M26.4), so every page
// re-reads and groups the whole selection. This measures what one page costs
// over a selection of 100 000 records: 25 000 sessions of four matching
// records each (a floor is stamped on four), beside as many records the
// selection does not match. The learnings carry the number.
//
//	go test ./internal/store/ -run '^$' -bench NegotiatedSessionPage -benchtime 20x
func BenchmarkANegotiatedSessionPageOver100000Records(b *testing.B) {
	st := storetest.New(b)
	ctx := b.Context()
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	const streams, perStream = 200, 1000 // 200 000 records; half answer the selection
	for i := range streams {
		stream := fmt.Sprintf("proxy:bench-%03d", i)
		ids := make([]string, perStream)
		for j := range ids {
			ids[j] = fmt.Sprintf("%s/%d", stream, j+1)
		}
		_, err := st.Audit().AppendChain(ctx, tenantA, stream, ids, func(_ store.AuditRecord, fresh []string) ([]store.AuditRecord, error) {
			recs := make([]store.AuditRecord, 0, len(fresh))
			for j, id := range fresh {
				n := i*perStream + j // the record's place in the estate
				rec := storetest.AuditRecord(id, stream, int64(j+1))
				rec.RecordedAt = at.Add(time.Duration(n) * time.Second)
				rec.SessionID = fmt.Sprintf("sess-%06d", n/8)
				if n%2 == 0 {
					rec.Negotiated = []string{negotiatedSelection.Negotiated}
				}
				recs = append(recs, rec)
			}
			return recs, nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	if _, err := st.Pool().Exec(ctx, `ANALYZE audit_records`); err != nil {
		b.Fatal(err)
	}
	whole, err := st.Audit().Sessions(ctx, tenantA, negotiatedSelection, store.Page{Limit: store.MaxPageLimit})
	if err != nil || len(whole.Rows) != store.MaxPageLimit || !whole.More {
		b.Fatalf("the selection's first %d sessions: %d, more=%t, %v", store.MaxPageLimit, len(whole.Rows), whole.More, err)
	}
	middle := whole.Rows[len(whole.Rows)/2]

	for name, p := range map[string]store.Page{
		"first page of 500":    {Limit: store.DefaultPageLimit},
		"page of 500 mid-walk": {Limit: store.DefaultPageLimit, After: []any{middle.LatestAt, middle.SessionID}},
		"first page of 10 000": {Limit: store.MaxPageLimit},
	} {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				got, err := st.Audit().Sessions(ctx, tenantA, negotiatedSelection, p)
				if err != nil || len(got.Rows) != p.Limit {
					b.Fatalf("%d sessions, %v", len(got.Rows), err)
				}
			}
		})
	}
}
