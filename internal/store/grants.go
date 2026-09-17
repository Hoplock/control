// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

type grantRepo struct{ s *Store }

const grantColumns = `grant_id, subject_id, scope, not_before, expires_at, origin, approval_ref, external_ref, revoked_at, created_at`

func (r grantRepo) Insert(ctx context.Context, tenant Tenant, g Grant) error {
	const op = "store.Grants.Insert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if g.ID == "" {
		return invalid(op, "grant id is required")
	}
	if g.Origin == "" {
		return invalid(op, "grant origin is required")
	}
	if g.ExpiresAt.IsZero() {
		return invalid(op, "grant expiry is required")
	}

	notBefore := g.NotBefore
	if notBefore.IsZero() {
		notBefore = time.Now().UTC()
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO grants (tenant, grant_id, subject_id, scope, not_before, expires_at,
		                    origin, approval_ref, external_ref, revoked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		tenant, g.ID, g.SubjectID, g.Scope, notBefore, g.ExpiresAt,
		string(g.Origin), g.ApprovalRef, g.ExternalRef, nullableTime(g.RevokedAt))
	return wrap(op, err)
}

func (r grantRepo) Get(ctx context.Context, tenant Tenant, grantID string) (Grant, error) {
	const op = "store.Grants.Get"
	if err := checkTenant(op, tenant); err != nil {
		return Grant{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+grantColumns+`
		FROM grants
		WHERE tenant = $1 AND grant_id = $2`, tenant, grantID)
	return scanGrant(op, row)
}

func (r grantRepo) ListLive(ctx context.Context, tenant Tenant, subjectID string, at time.Time) ([]Grant, error) {
	const op = "store.Grants.ListLive"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if at.IsZero() {
		return nil, invalid(op, "instant is required: time is an input, never a clock this layer reads")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// DECISION PATH (M5): served by grants_live_by_subject_idx, which is
	// partial on revoked_at IS NULL. Revoked grants are never a decision
	// input again, so they are excluded by the index rather than filtered
	// out after being read — the scan is bounded by live access rather than
	// by how many grants the tenant has ever issued.
	rows, err := r.s.db.Query(ctx, `
		SELECT `+grantColumns+`
		FROM grants
		WHERE tenant = $1
		  AND subject_id = $2
		  AND revoked_at IS NULL
		  AND not_before <= $3
		  AND expires_at > $3
		ORDER BY expires_at`, tenant, subjectID, at)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []Grant
	for rows.Next() {
		g, err := scanGrant(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, wrap(op, rows.Err())
}

func (r grantRepo) Revoke(ctx context.Context, tenant Tenant, grantID string, t time.Time) error {
	const op = "store.Grants.Revoke"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if t.IsZero() {
		return invalid(op, "revocation time is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// COALESCE keeps the first revocation time. An auditor asks when access
	// stopped, and the second answer is not more true than the first.
	tag, err := r.s.db.Exec(ctx, `
		UPDATE grants
		SET revoked_at = COALESCE(revoked_at, $3)
		WHERE tenant = $1 AND grant_id = $2`, tenant, grantID, t)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func scanGrant(op string, row rowScanner) (Grant, error) {
	var (
		g         Grant
		origin    string
		revokedAt *time.Time
	)
	err := row.Scan(&g.ID, &g.SubjectID, &g.Scope, &g.NotBefore, &g.ExpiresAt,
		&origin, &g.ApprovalRef, &g.ExternalRef, &revokedAt, &g.CreatedAt)
	if err != nil {
		return Grant{}, wrap(op, err)
	}
	g.Origin = GrantOrigin(origin)
	g.RevokedAt = timeOrZero(revokedAt)
	return g, nil
}
