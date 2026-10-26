// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Every index migration 0012 built for an audit filter is one the filter can
// use (0021). An index no query can use costs every insert and buys nothing,
// and the query it was built for reads the whole tenant instead: the runbook's
// "which sessions negotiated this" was a full scan.
//
// Whether the planner PREFERS an index depends on the data; whether it CAN use
// one depends only on the predicate, and that is what this asks. Inside a
// transaction that is rolled back, it drops every other index on the table and
// switches sequential scans off, then asks for the generic plan — the one a
// prepared statement is given for any parameter. A predicate the index can
// answer is planned on it; one it cannot is a sequential scan.
func TestEveryIndexedAuditFilterCanUseItsIndex(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	day := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name  string
		q     store.AuditQuery
		index string
	}{
		{"negotiated", store.AuditQuery{Negotiated: "key_exchanges:curve25519-sha256"}, "audit_records_negotiated_idx"},
		{"banned", store.AuditQuery{Banned: "ciphers:aes128-cbc"}, "audit_records_bans_idx"},
		{"a refused record id", store.AuditQuery{Gaps: true, GapRecordID: "rec-1"}, "audit_records_gap_ids_idx"},
		{"a certificate serial", store.AuditQuery{CertificateSerial: "42"}, "audit_records_certificate_idx"},
		{"a gap span", store.AuditQuery{Gaps: true, SpanFrom: day, SpanTo: day.Add(time.Hour)}, "audit_records_gap_span_idx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := st.Pool().Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()

			rows, err := tx.Query(ctx, `
				SELECT indexname FROM pg_indexes
				WHERE schemaname = current_schema() AND tablename = 'audit_records' AND indexname <> $1`, tc.index)
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
				if name == "audit_records_pkey" {
					stmt = "ALTER TABLE audit_records DROP CONSTRAINT audit_records_pkey CASCADE"
				}
				if _, err := tx.Exec(ctx, stmt); err != nil {
					t.Fatalf("%s: %v", stmt, err)
				}
			}
			if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
				t.Fatal(err)
			}

			// The simple protocol, because a generic plan is asked for with
			// its parameters unbound and the extended one binds them.
			results, err := tx.Conn().PgConn().Exec(ctx, "EXPLAIN (GENERIC_PLAN) "+store.AuditPageSQL(tc.q, store.Page{Limit: store.DefaultPageLimit})).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			var lines []string
			for _, row := range results[0].Rows {
				lines = append(lines, string(row[0]))
			}
			if text := strings.Join(lines, "\n"); !strings.Contains(text, tc.index) {
				t.Errorf("the filter cannot use %s; with only it available the plan is:\n%s", tc.index, text)
			}
		})
	}
}
