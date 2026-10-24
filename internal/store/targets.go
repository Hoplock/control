// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
)

type targetRepo struct{ s *Store }

const targetColumns = `target_id, hostname, zone, labels, credential_method, created_at, updated_at`

func (r targetRepo) Get(ctx context.Context, tenant Tenant, targetID string) (Target, error) {
	const op = "store.Targets.Get"
	if err := checkTenant(op, tenant); err != nil {
		return Target{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+targetColumns+`
		FROM targets
		WHERE tenant = $1 AND target_id = $2`, tenant, targetID)
	return scanTarget(op, row)
}

func (r targetRepo) GetByHostname(ctx context.Context, tenant Tenant, hostname string) (Target, error) {
	const op = "store.Targets.GetByHostname"
	if err := checkTenant(op, tenant); err != nil {
		return Target{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// DECISION PATH (M5): one index hit on targets_hostname_key, returning
	// the labels policy matches on in the same row. No join, no second
	// round trip.
	row := r.s.db.QueryRow(ctx, `
		SELECT `+targetColumns+`
		FROM targets
		WHERE tenant = $1 AND hostname = $2`, tenant, hostname)
	return scanTarget(op, row)
}

func (r targetRepo) ListByLabels(ctx context.Context, tenant Tenant, labels map[string]string) ([]Target, error) {
	const op = "store.Targets.ListByLabels"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// `@>` is the containment operator the GIN index serves: every pair in
	// the argument must be present in the row's labels.
	rows, err := r.s.db.Query(ctx, `
		SELECT `+targetColumns+`
		FROM targets
		WHERE tenant = $1 AND labels @> $2
		ORDER BY target_id`, tenant, nonNilMap(labels))
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []Target
	for rows.Next() {
		t, err := scanTarget(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, wrap(op, rows.Err())
}

// targetOrder is the target list's order: its primary key.
var targetOrder = order{columns: []string{"target_id"}}

func (r targetRepo) Page(ctx context.Context, tenant Tenant, labels map[string]string, p Page) (Paged[Target], error) {
	const op = "store.Targets.Page"
	if err := checkTenant(op, tenant); err != nil {
		return Paged[Target]{}, err
	}
	if err := checkPage(op, targetOrder, p); err != nil {
		return Paged[Target]{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// `@>` is the containment the GIN index serves, as in ListByLabels; the
	// order and the position are the primary key's.
	tail, args := targetOrder.clause(p, []any{tenant, nonNilMap(labels)})
	return queryPage(ctx, r.s.db, op, p, `
		SELECT `+targetColumns+`
		FROM targets
		WHERE tenant = $1 AND labels @> $2`+tail, args, scanTarget)
}

func (r targetRepo) Upsert(ctx context.Context, tenant Tenant, t Target) error {
	const op = "store.Targets.Upsert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if t.ID == "" {
		return invalid(op, "target id is required")
	}
	if t.Hostname == "" {
		return invalid(op, "target hostname is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO targets (tenant, target_id, hostname, zone, labels, credential_method)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant, target_id) DO UPDATE SET
			hostname          = EXCLUDED.hostname,
			zone              = EXCLUDED.zone,
			labels            = EXCLUDED.labels,
			credential_method = EXCLUDED.credential_method,
			updated_at        = now()`,
		tenant, t.ID, t.Hostname, t.Zone, nonNilMap(t.Labels), t.CredentialMethod)
	return wrap(op, err)
}

// targetLockPrefix keys the tenant's target lock (Store.lockTenant).
const targetLockPrefix = "targets\x1f"

func (r targetRepo) Lock(ctx context.Context, tenant Tenant) error {
	return r.s.lockTenant(ctx, "store.Targets.Lock", targetLockPrefix, tenant)
}

func (r targetRepo) Delete(ctx context.Context, tenant Tenant, targetID string) error {
	const op = "store.Targets.Delete"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		DELETE FROM targets WHERE tenant = $1 AND target_id = $2`, tenant, targetID)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func scanTarget(op string, row rowScanner) (Target, error) {
	var t Target
	err := row.Scan(&t.ID, &t.Hostname, &t.Zone, &t.Labels, &t.CredentialMethod,
		&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return Target{}, wrap(op, err)
	}
	return t, nil
}
