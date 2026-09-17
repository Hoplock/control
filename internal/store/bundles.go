// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
)

type bundleRepo struct{ s *Store }

const bundleColumns = `version, source, hash, uploaded_by, uploaded_at, active`

func (r bundleRepo) Insert(ctx context.Context, tenant Tenant, b PolicyBundle) error {
	const op = "store.PolicyBundles.Insert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if b.Version <= 0 {
		return invalid(op, "bundle version must be positive")
	}
	if b.Hash == "" {
		return invalid(op, "bundle hash is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// No ON CONFLICT: a bundle is immutable, so "it is already there" is
	// never answered by overwriting it (M4).
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO policy_bundles (tenant, version, source, hash, uploaded_by, active)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		tenant, b.Version, b.Source, b.Hash, b.UploadedBy, b.Active)
	return wrap(op, err)
}

func (r bundleRepo) Get(ctx context.Context, tenant Tenant, version int64) (PolicyBundle, error) {
	const op = "store.PolicyBundles.Get"
	if err := checkTenant(op, tenant); err != nil {
		return PolicyBundle{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+bundleColumns+`
		FROM policy_bundles
		WHERE tenant = $1 AND version = $2`, tenant, version)
	return scanBundle(op, row)
}

func (r bundleRepo) GetActive(ctx context.Context, tenant Tenant) (PolicyBundle, error) {
	const op = "store.PolicyBundles.GetActive"
	if err := checkTenant(op, tenant); err != nil {
		return PolicyBundle{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// The partial unique index makes "the active one" a single row rather
	// than the newest of several: the compiled program is per tenant (M18),
	// and which program is served must have exactly one answer.
	row := r.s.db.QueryRow(ctx, `
		SELECT `+bundleColumns+`
		FROM policy_bundles
		WHERE tenant = $1 AND active`, tenant)
	return scanBundle(op, row)
}

func (r bundleRepo) Activate(ctx context.Context, tenant Tenant, version int64) error {
	const op = "store.PolicyBundles.Activate"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// One statement, for two reasons. The partial unique index is never
	// transiently violated — the update clears the old active row and sets
	// the new one together, and Postgres checks the index once at the end.
	// And the `wanted` CTE makes a missing version affect no rows at all,
	// so a caller cannot deactivate the live bundle by asking for a version
	// that does not exist: without it, "cleared the old one and set
	// nothing" and "there was nothing to clear" are the same row count.
	tag, err := r.s.db.Exec(ctx, `
		WITH wanted AS (
			SELECT 1 FROM policy_bundles WHERE tenant = $1 AND version = $2
		)
		UPDATE policy_bundles
		SET active = (version = $2)
		WHERE tenant = $1
		  AND (version = $2 OR active)
		  AND EXISTS (SELECT 1 FROM wanted)`, tenant, version)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func (r bundleRepo) NextVersion(ctx context.Context, tenant Tenant) (int64, error) {
	const op = "store.PolicyBundles.NextVersion"
	if err := checkTenant(op, tenant); err != nil {
		return 0, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var next int64
	err := r.s.db.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1 FROM policy_bundles WHERE tenant = $1`,
		tenant).Scan(&next)
	if err != nil {
		return 0, wrap(op, err)
	}
	return next, nil
}

func scanBundle(op string, row rowScanner) (PolicyBundle, error) {
	var b PolicyBundle
	err := row.Scan(&b.Version, &b.Source, &b.Hash, &b.UploadedBy, &b.UploadedAt, &b.Active)
	if err != nil {
		return PolicyBundle{}, wrap(op, err)
	}
	return b, nil
}
