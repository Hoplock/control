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

// The role bindings and the outstanding certificates, paged (0034).
// page_test.go holds both readers to every paged reader's promises; these hold
// what only they promise.

// bindingKey is a binding as its list orders it: role, holder kind, holder.
func bindingKey(b store.RoleBinding) string {
	if b.GroupID != "" {
		return b.Role + "/group/" + b.GroupID
	}
	return b.Role + "/subject/" + b.SubjectID
}

// The order is per role, a subject's bindings before a group's, each by its
// holder's id, and a walk continues from every component of that key: a
// subject and a group of the same name hold the same role as two bindings,
// in that order.
func TestRoleBindingPagesFollowTheirWholeKey(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	for _, b := range []store.RoleBinding{
		{GroupID: "ops", Role: "auditor"},
		{SubjectID: "zed", Role: "auditor"},
		{GroupID: "sre", Role: "admin"},
		{SubjectID: "bob", Role: "admin"},
		{GroupID: "eng", Role: "auditor"},
		{SubjectID: "ops", Role: "auditor"},
		{SubjectID: "alice", Role: "admin"},
	} {
		if err := st.RoleBindings().Bind(ctx, tenantA, b); err != nil {
			t.Fatalf("bind %s: %v", bindingKey(b), err)
		}
	}
	want := []string{
		"admin/subject/alice", "admin/subject/bob", "admin/group/sre",
		"auditor/subject/ops", "auditor/subject/zed", "auditor/group/eng", "auditor/group/ops",
	}

	for _, limit := range []int{1, 2, 3, store.MaxPageLimit} {
		var seen []string
		p := store.Page{Limit: limit}
		for range len(want) + 1 {
			got, err := st.RoleBindings().Page(ctx, tenantA, p)
			if err != nil {
				t.Fatalf("limit %d after %v: %v", limit, p.After, err)
			}
			for _, b := range got.Rows {
				seen = append(seen, bindingKey(b))
			}
			if !got.More {
				break
			}
			last := got.Rows[len(got.Rows)-1]
			if last.GroupID != "" {
				p.After = []any{last.Role, true, last.GroupID}
			} else {
				p.After = []any{last.Role, false, last.SubjectID}
			}
		}
		if !slices.Equal(seen, want) {
			t.Errorf("limit %d walked %v, want %v", limit, seen, want)
		}
	}

	// A position between a subject's binding and a group's of the same name
	// continues with the group's.
	got, err := st.RoleBindings().Page(ctx, tenantA, store.Page{Limit: 1, After: []any{"auditor", false, "zzz"}})
	if err != nil || len(got.Rows) != 1 || bindingKey(got.Rows[0]) != "auditor/group/eng" || !got.More {
		t.Errorf("after the last subject's auditor binding: %+v more=%t (%v), want the first group's", got.Rows, got.More, err)
	}
}

// Outstanding is judged at each page's instant, and a certificate's place is
// its issuance, which never moves. So a certificate that expires during a walk
// is not on the pages after it, the rest keep their places, and a revoked one
// is on none. Two issued at one instant are ordered by serial, newest first.
func TestAnOutstandingPageIsJudgedAtItsInstant(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	if err := putCertificate(ctx, st, tenantA, "seed"); err != nil { // the CA key
		t.Fatalf("seed: %v", err)
	}
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	issued := func(minutesAgo int) time.Time { return now.Add(-time.Duration(minutesAgo) * time.Minute) }
	for _, c := range []store.SSHCertificate{
		{Serial: 10, KeyID: "c-10", IssuedAt: issued(1), ValidBefore: now.Add(time.Hour)},
		{Serial: 12, KeyID: "c-12", IssuedAt: issued(2), ValidBefore: now.Add(time.Hour)},
		{Serial: 11, KeyID: "c-11", IssuedAt: issued(2), ValidBefore: now.Add(time.Hour)},
		{Serial: 9, KeyID: "c-9", IssuedAt: issued(3), ValidBefore: now.Add(5 * time.Minute)}, // expires mid-walk
		{Serial: 8, KeyID: "c-8", IssuedAt: issued(4), ValidBefore: now.Add(time.Hour)},
		{Serial: 7, KeyID: "c-7", IssuedAt: issued(5), ValidBefore: now.Add(time.Hour)}, // revoked
		{Serial: 6, KeyID: "c-6", IssuedAt: issued(6), ValidBefore: now},                // expired at now
	} {
		if err := insertCertificate(ctx, st, tenantA, c); err != nil {
			t.Fatalf("insert %s: %v", c.KeyID, err)
		}
	}
	if err := st.SSHCertificates().Revoke(ctx, tenantA, 7, now.Add(-time.Hour), "test"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	read := func(at time.Time, p store.Page) ([]string, store.Page, bool) {
		t.Helper()
		got, err := st.SSHCertificates().OutstandingPage(ctx, tenantA, at, p)
		if err != nil {
			t.Fatalf("page at %s after %v: %v", at, p.After, err)
		}
		var ids []string
		for _, c := range got.Rows {
			if c.Certificate == "" {
				t.Errorf("%s was read without its certificate", c.KeyID)
			}
			ids = append(ids, c.KeyID)
		}
		next := p
		if n := len(got.Rows); n > 0 {
			next.After = []any{got.Rows[n-1].IssuedAt, got.Rows[n-1].Serial}
		}
		return ids, next, got.More
	}

	// The whole list at now: neither the revoked certificate nor the one
	// whose window closes at now.
	whole, _, more := read(now, store.Page{Limit: store.MaxPageLimit})
	if want := []string{"c-10", "c-12", "c-11", "c-9", "c-8", "seed"}; !slices.Equal(whole, want) || more {
		t.Errorf("at now: %v more=%t, want %v", whole, more, want)
	}

	// A walk of two a page, the clock passing c-9's expiry after the first.
	first, p, more := read(now, store.Page{Limit: 2})
	if want := []string{"c-10", "c-12"}; !slices.Equal(first, want) || !more {
		t.Fatalf("first page: %v more=%t, want %v", first, more, want)
	}
	rest, _, more := read(now.Add(10*time.Minute), store.Page{Limit: store.MaxPageLimit, After: p.After})
	if want := []string{"c-11", "c-8", "seed"}; !slices.Equal(rest, want) || more {
		t.Errorf("after c-12, ten minutes on: %v more=%t, want %v: c-9 expired, the rest in place", rest, more, want)
	}
}

// Both orders are read from the index migration 0018 built, asked as
// grant_page_test.go asks: the generic plan with every other index on the
// table dropped and sequential scans off, so this is what the index CAN serve.
// Each case also asks under the index the list had before 0018, and that plan
// sorts, so the assertion tells the two apart. The certificates' old index is
// kept, for the compromise rotation's revocation, and is still in the schema.
func TestTheBindingAndCertificateListsReadTheirOrderFromAnIndex(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	bindings := store.Page{Limit: 1, After: []any{"admin", false, "alice"}}
	certificates := store.Page{Limit: 1, After: []any{time.Now(), int64(1)}}
	for _, tc := range []struct {
		name, table, index, before string
		sql                        string
		positioned                 bool
	}{
		{"role bindings", "identity_role_bindings", "identity_role_bindings_order_key", "identity_role_bindings_subject_key",
			store.RoleBindingPageSQL(store.Page{Limit: 1}), false},
		{"role bindings after a position", "identity_role_bindings", "identity_role_bindings_order_key", "identity_role_bindings_subject_key",
			store.RoleBindingPageSQL(bindings), true},
		{"outstanding certificates", "ssh_certificates", "ssh_certificates_issued_order_idx", "ssh_certificates_outstanding_idx",
			store.OutstandingPageSQL(store.Page{Limit: 1}), false},
		{"outstanding certificates after a position", "ssh_certificates", "ssh_certificates_issued_order_idx", "ssh_certificates_outstanding_idx",
			store.OutstandingPageSQL(certificates), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := planWithOnly(t, st, tc.table, tc.index, "", tc.sql)
			if !strings.Contains(plan, "Index Scan using "+tc.index) || strings.Contains(plan, "Sort") {
				t.Errorf("the page is not read in order from %s; with only it available the plan is:\n%s", tc.index, plan)
			}
			// The row comparison is the scan's start, not a filter over
			// every row before it.
			if tc.positioned && !regexp.MustCompile(`Index Cond: .*ROW\(`).MatchString(plan) {
				t.Errorf("the position is not an index condition of %s:\n%s", tc.index, plan)
			}
			old := planWithOnly(t, st, tc.table, tc.before, "", tc.sql)
			if !strings.Contains(old, "Sort") {
				t.Errorf("under %s alone the page is planned without a sort, so this test cannot tell them apart:\n%s",
					tc.before, old)
			}
		})
	}

	var kept int
	if err := st.Pool().QueryRow(ctx, `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = current_schema() AND indexname = 'ssh_certificates_outstanding_idx'`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Errorf("ssh_certificates_outstanding_idx, which RevokeByCAKey reads, is gone (%d)", kept)
	}
}
