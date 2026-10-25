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

// DeleteProxyConfigState removes one proxy's rollout state, which nothing in
// the product does: the paged-reader tests delete an item of every list they
// walk. It is compiled into the test binary alone.
func DeleteProxyConfigState(ctx context.Context, s *Store, tenant Tenant, proxyID string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM proxy_config_state WHERE tenant = $1 AND proxy_id = $2`, tenant, proxyID)
	if err == nil && tag.RowsAffected() == 0 {
		return notFound("test.DeleteProxyConfigState")
	}
	return err
}

// AuditQuerySQL is the statement Query runs for a selection, for the test that
// asks Postgres how it would plan it. It is compiled into the test binary
// alone.
func AuditQuerySQL(q AuditQuery) string {
	sql, _ := auditQuerySQL("tenant", q)
	return sql
}

// DeleteAlgorithmDeclaration, DeleteTargetCapability and DeleteTargetKex
// remove one row of the lists 0032 pages, which nothing in the product does:
// the paged-reader tests delete an item of every list they walk. They are
// compiled into the test binary alone.
func DeleteAlgorithmDeclaration(ctx context.Context, s *Store, tenant Tenant, proxyID string) error {
	return deleteOne(ctx, s, "test.DeleteAlgorithmDeclaration",
		`DELETE FROM proxy_algorithm_declarations WHERE tenant = $1 AND proxy_id = $2`, tenant, proxyID)
}

func DeleteTargetCapability(ctx context.Context, s *Store, tenant Tenant, hostname string, port int32, platform string) error {
	return deleteOne(ctx, s, "test.DeleteTargetCapability",
		`DELETE FROM target_capabilities WHERE tenant = $1 AND hostname = $2 AND target_port = $3 AND platform = $4`,
		tenant, hostname, port, platform)
}

func DeleteTargetKex(ctx context.Context, s *Store, tenant Tenant, hostname string, port int32) error {
	return deleteOne(ctx, s, "test.DeleteTargetKex",
		`DELETE FROM target_kex_observations WHERE tenant = $1 AND hostname = $2 AND target_port = $3`,
		tenant, hostname, port)
}

func deleteOne(ctx context.Context, s *Store, op, sql string, args ...any) error {
	tag, err := s.db.Exec(ctx, sql, args...)
	if err == nil && tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return err
}
