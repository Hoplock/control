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

	// TWO STATEMENTS IN ONE TRANSACTION, in this order. The partial unique
	// index is checked row by row, not once at the end of a statement, so a
	// single UPDATE that set the new row before clearing the old one — which
	// is what a rollback to an older version does, rows being visited in
	// their physical order — violates it. The wanted row is locked first, so
	// a version that does not exist clears nothing: a caller cannot
	// deactivate the live bundle by asking for one that is not there.
	return r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		var found int
		err := q.QueryRow(ctx, `
			SELECT 1 FROM policy_bundles WHERE tenant = $1 AND version = $2 FOR UPDATE`,
			tenant, version).Scan(&found)
		if err != nil {
			return wrap(op, err)
		}
		if _, err := q.Exec(ctx, `
			UPDATE policy_bundles SET active = false
			WHERE tenant = $1 AND active AND version <> $2`, tenant, version); err != nil {
			return wrap(op, err)
		}
		if _, err := q.Exec(ctx, `
			UPDATE policy_bundles SET active = true
			WHERE tenant = $1 AND version = $2`, tenant, version); err != nil {
			return wrap(op, err)
		}
		return nil
	})
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

func (r bundleRepo) List(ctx context.Context, tenant Tenant) ([]PolicyBundle, error) {
	const op = "store.PolicyBundles.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+bundleColumns+`
		FROM policy_bundles
		WHERE tenant = $1
		ORDER BY version DESC`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []PolicyBundle
	for rows.Next() {
		b, err := scanBundle(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, wrap(op, rows.Err())
}

func scanBundle(op string, row rowScanner) (PolicyBundle, error) {
	var b PolicyBundle
	err := row.Scan(&b.Version, &b.Source, &b.Hash, &b.UploadedBy, &b.UploadedAt, &b.Active)
	if err != nil {
		return PolicyBundle{}, wrap(op, err)
	}
	return b, nil
}
