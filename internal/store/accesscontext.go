// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

// The external-context scope bindings (M16, 0013): what each integration may
// ever assert in a tenant. The decision path reads them through a cache that
// internal/accessctx holds, never per call, so nothing here is on M5's hot path.

type accessContextBindingRepo struct{ s *Store }

const accessContextBindingColumns = `provider, mode, scope, subjects, subject_groups, ` +
	`targets, target_labels, target_zones, max_window_seconds, privileged, push_principals, ` +
	`enabled, description, updated_by, updated_by_principal, updated_by_break_glass, ` +
	`created_at, updated_at`

func (r accessContextBindingRepo) Put(ctx context.Context, tenant Tenant, b AccessContextBinding) error {
	const op = "store.AccessContextBindings.Put"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	switch {
	case b.Provider == "":
		return invalid(op, "a binding names its provider")
	case !b.Mode.Valid():
		return invalid(op, "a binding's mode is push, probe or push-probe")
	case b.Scope == "":
		return invalid(op, "a binding names the grant scope it produces")
	case b.MaxWindow < time.Second:
		return invalid(op, "a binding's maximum window is at least a second")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// A replace keeps the row's creation time: "when was this integration
	// first trusted" is a question the history must still answer after its
	// scope has been edited.
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO access_context_bindings (tenant, provider, mode, scope, subjects, subject_groups,
		    targets, target_labels, target_zones, max_window_seconds, privileged, push_principals,
		    enabled, description, updated_by, updated_by_principal, updated_by_break_glass, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, now())
		ON CONFLICT (tenant, provider) DO UPDATE SET
		    mode = EXCLUDED.mode, scope = EXCLUDED.scope,
		    subjects = EXCLUDED.subjects, subject_groups = EXCLUDED.subject_groups,
		    targets = EXCLUDED.targets, target_labels = EXCLUDED.target_labels,
		    target_zones = EXCLUDED.target_zones, max_window_seconds = EXCLUDED.max_window_seconds,
		    privileged = EXCLUDED.privileged, push_principals = EXCLUDED.push_principals,
		    enabled = EXCLUDED.enabled, description = EXCLUDED.description,
		    updated_by = EXCLUDED.updated_by, updated_by_principal = EXCLUDED.updated_by_principal,
		    updated_by_break_glass = EXCLUDED.updated_by_break_glass, updated_at = now()`,
		tenant, b.Provider, string(b.Mode), b.Scope,
		nonNilStrings(b.Subjects), nonNilStrings(b.SubjectGroups),
		nonNilStrings(b.Targets), nonNilMap(b.TargetLabels), nonNilStrings(b.TargetZones),
		int64(b.MaxWindow/time.Second), b.Privileged, nonNilStrings(b.PushPrincipals),
		b.Enabled, b.Description,
		b.UpdatedBy.Subject, b.UpdatedBy.Principal, b.UpdatedBy.BreakGlass)
	return wrap(op, err)
}

func (r accessContextBindingRepo) Get(ctx context.Context, tenant Tenant, provider string) (AccessContextBinding, error) {
	const op = "store.AccessContextBindings.Get"
	if err := checkTenant(op, tenant); err != nil {
		return AccessContextBinding{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+accessContextBindingColumns+`
		FROM access_context_bindings
		WHERE tenant = $1 AND provider = $2`, tenant, provider)
	return scanAccessContextBinding(op, row)
}

func (r accessContextBindingRepo) List(ctx context.Context, tenant Tenant) ([]AccessContextBinding, error) {
	const op = "store.AccessContextBindings.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+accessContextBindingColumns+`
		FROM access_context_bindings
		WHERE tenant = $1
		ORDER BY provider`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []AccessContextBinding
	for rows.Next() {
		b, err := scanAccessContextBinding(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, wrap(op, rows.Err())
}

func (r accessContextBindingRepo) Delete(ctx context.Context, tenant Tenant, provider string) (bool, error) {
	const op = "store.AccessContextBindings.Delete"
	if err := checkTenant(op, tenant); err != nil {
		return false, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		DELETE FROM access_context_bindings WHERE tenant = $1 AND provider = $2`, tenant, provider)
	if err != nil {
		return false, wrap(op, err)
	}
	return tag.RowsAffected() > 0, nil
}

func scanAccessContextBinding(op string, row rowScanner) (AccessContextBinding, error) {
	var (
		b       AccessContextBinding
		mode    string
		seconds int64
	)
	err := row.Scan(&b.Provider, &mode, &b.Scope, &b.Subjects, &b.SubjectGroups,
		&b.Targets, &b.TargetLabels, &b.TargetZones, &seconds, &b.Privileged, &b.PushPrincipals,
		&b.Enabled, &b.Description, &b.UpdatedBy.Subject, &b.UpdatedBy.Principal, &b.UpdatedBy.BreakGlass,
		&b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return AccessContextBinding{}, wrap(op, err)
	}
	b.Mode = ExternalMode(mode)
	b.MaxWindow = time.Duration(seconds) * time.Second
	return b, nil
}
