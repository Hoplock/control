// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"time"
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

// GrantPageSQL and GrantRequestPageSQL are the statements Grants().Page and
// GrantRequests().PageForSubject run, for the test that asks Postgres how it
// would plan them. They are compiled into the test binary alone.
func GrantPageSQL(q GrantQuery, p Page) string {
	sql, _, err := grantPageSQL("test.GrantPageSQL", "tenant", q, p)
	if err != nil {
		panic(err)
	}
	return sql
}

func GrantRequestPageSQL(q GrantRequestQuery, p Page) string {
	sql, _, err := grantRequestPageSQL("test.GrantRequestPageSQL", "tenant", "subject", q, p)
	if err != nil {
		panic(err)
	}
	return sql
}

// SetGrantCreatedAt moves when a grant was created, which nothing in the
// product does: Insert takes the database's clock, and the paged-reader tests
// place each grant in the list's order. DeleteGrant and DeleteGrantRequest
// remove a row, which nothing in the product does either: those tests delete
// an item of every list they walk. All three are compiled into the test binary
// alone.
func SetGrantCreatedAt(ctx context.Context, s *Store, tenant Tenant, grantID string, at time.Time) error {
	tag, err := s.db.Exec(ctx, `UPDATE grants SET created_at = $3 WHERE tenant = $1 AND grant_id = $2`, tenant, grantID, at)
	if err == nil && tag.RowsAffected() == 0 {
		return notFound("test.SetGrantCreatedAt")
	}
	return err
}

func DeleteGrant(ctx context.Context, s *Store, tenant Tenant, grantID string) error {
	return deleteOne(ctx, s, "test.DeleteGrant",
		`DELETE FROM grants WHERE tenant = $1 AND grant_id = $2`, tenant, grantID)
}

func DeleteGrantRequest(ctx context.Context, s *Store, tenant Tenant, requestID string) error {
	return deleteOne(ctx, s, "test.DeleteGrantRequest",
		`DELETE FROM grant_requests WHERE tenant = $1 AND request_id = $2`, tenant, requestID)
}

// AuditPageSQL is the statement Audit().Page and Query run for a selection
// and a page, for the tests that ask Postgres how it would plan it. It is
// compiled into the test binary alone.
func AuditPageSQL(q AuditQuery, p Page) string {
	sql, _, err := auditPageSQL("test.AuditPageSQL", "tenant", q, p)
	if err != nil {
		panic(err)
	}
	return sql
}

// BlockedPageSQL is the statement Audit().BlockedCommands runs for a page,
// for the test that asks Postgres how it would plan it. It is compiled into
// the test binary alone.
func BlockedPageSQL(ascending bool, p Page) string {
	sql, _, err := blockedPageSQL("test.BlockedPageSQL", "tenant", BlockedCommandQuery{LabelName: "env", Ascending: ascending}, p)
	if err != nil {
		panic(err)
	}
	return sql
}

// DeleteAuditRecord removes one record, for the paging tests' writes between
// and during a walk. Nothing in Control deletes a record but retention, which
// deletes a stream's prefix (M8).
func DeleteAuditRecord(ctx context.Context, s *Store, tenant Tenant, recordID string) error {
	return deleteOne(ctx, s, "test.DeleteAuditRecord",
		`DELETE FROM audit_records WHERE tenant = $1 AND record_id = $2`, tenant, recordID)
}

// RoleBindingPageSQL and OutstandingPageSQL are the statements
// RoleBindings().Page and SSHCertificates().OutstandingPage run, for the test
// that asks Postgres how it would plan them. They are compiled into the test
// binary alone.
func RoleBindingPageSQL(p Page) string {
	sql, _, err := roleBindingPageSQL("test.RoleBindingPageSQL", "tenant", p)
	if err != nil {
		panic(err)
	}
	return sql
}

func OutstandingPageSQL(p Page) string {
	sql, _, err := outstandingPageSQL("test.OutstandingPageSQL", "tenant", time.Now(), p)
	if err != nil {
		panic(err)
	}
	return sql
}

// DeleteSSHCertificate removes one issued certificate's row, which nothing in
// the product does (the record is append-only, and a certificate leaves the
// outstanding list by expiring or being revoked): the paged-reader tests delete
// an item of every list they walk. It is compiled into the test binary alone.
func DeleteSSHCertificate(ctx context.Context, s *Store, tenant Tenant, serial int64) error {
	return deleteOne(ctx, s, "test.DeleteSSHCertificate",
		`DELETE FROM ssh_certificates WHERE tenant = $1 AND serial = $2`, tenant, serial)
}

// BundlePageSQL is the statement PolicyBundles().Page runs, for the test that
// asks Postgres how it would plan it. It is compiled into the test binary
// alone.
func BundlePageSQL(p Page) string {
	sql, _, err := bundlePageSQL("test.BundlePageSQL", "tenant", p)
	if err != nil {
		panic(err)
	}
	return sql
}

// DeletePolicyBundle removes one stored version, which nothing in the product
// does (a version is immutable and kept, M4): the paged-reader tests delete an
// item of every list they walk. It is compiled into the test binary alone.
func DeletePolicyBundle(ctx context.Context, s *Store, tenant Tenant, version int64) error {
	return deleteOne(ctx, s, "test.DeletePolicyBundle",
		`DELETE FROM policy_bundles WHERE tenant = $1 AND version = $2`, tenant, version)
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
