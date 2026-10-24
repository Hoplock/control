// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
)

// TargetIDPageFailingAt reads one page of a tenant's target ids through the
// paging every reader uses, failing at the row whose id is failAt: in the
// statement when inStatement, else in the scan, once the rows before it have
// been read. It is for the test that a failure is never a short page (M26.6),
// and it is compiled into the test binary alone.
func TargetIDPageFailingAt(ctx context.Context, s *Store, tenant Tenant, p Page, failAt string, inStatement bool) (Paged[string], error) {
	divisor := "1"
	if inStatement {
		divisor = "CASE WHEN target_id = $2 THEN 0 ELSE 1 END"
	}
	tail, args := targetOrder.clause(p, []any{tenant, failAt})
	return queryPage(ctx, s.db, "test.TargetIDPageFailingAt", p, `
		SELECT target_id, 1 / (`+divisor+`)
		FROM targets
		WHERE tenant = $1 AND $2::text IS NOT NULL`+tail, args,
		func(op string, row rowScanner) (string, error) {
			var id string
			var one int
			if err := row.Scan(&id, &one); err != nil {
				return "", wrap(op, err)
			}
			if !inStatement && id == failAt {
				return "", &Error{Op: op, Kind: KindInternal, Err: errors.New("scan failed")}
			}
			return id, nil
		})
}

// AuditQuerySQL is the statement Query runs for a selection, for the test that
// asks Postgres how it would plan it. It is compiled into the test binary
// alone.
func AuditQuerySQL(q AuditQuery) string {
	sql, _ := auditQuerySQL("tenant", q)
	return sql
}
