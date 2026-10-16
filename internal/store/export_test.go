// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

// AuditQuerySQL is the statement Query runs for a selection, for the test that
// asks Postgres how it would plan it. It is compiled into the test binary
// alone.
func AuditQuerySQL(q AuditQuery) string {
	sql, _ := auditQuerySQL("tenant", q)
	return sql
}
