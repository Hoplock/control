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

// ---------------------------------------------------------------------------
// ends that named no window (0017)
// ---------------------------------------------------------------------------

type accessContextWindowEndRepo struct{ s *Store }

const accessContextWindowEndColumns = `external_system, assertion_id, external_ref, issued_at, ` +
	`ended_at, ended_by, ended_by_principal`

// assertionKeySeparator joins the three parts of an assertion's lock key. An
// assertion id carries no control character (the push receiver refuses one),
// so a separator from that range keeps two different triples from joining to
// the same text in practice — and a collision would only serialize them.
const assertionKeySeparator = "\x1f"

func (r accessContextWindowEndRepo) Lock(ctx context.Context, tenant Tenant, system, assertionID string) error {
	const op = "store.AccessContextWindowEnds.Lock"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if r.s.pool != nil {
		// A transaction-scoped lock taken outside a transaction is released
		// the instant the statement ends, which is a lock that serializes
		// nothing — and the reason this exists is that two paths must not
		// interleave.
		return invalid(op, "the assertion's lock is taken inside a transaction")
	}
	if system == "" || assertionID == "" {
		return invalid(op, "a system and an assertion id are required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// One bigint key. Postgres keeps the one-bigint key space apart from the
	// two-int4 space the audit chain locks in (AppendChain), so the two never
	// contend; a collision of the hash only serializes two assertions.
	_, err := r.s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		string(tenant)+assertionKeySeparator+system+assertionKeySeparator+assertionID)
	return wrap(op, err)
}

func (r accessContextWindowEndRepo) Get(ctx context.Context, tenant Tenant, system, assertionID string) (AccessContextWindowEnd, error) {
	const op = "store.AccessContextWindowEnds.Get"
	if err := checkTenant(op, tenant); err != nil {
		return AccessContextWindowEnd{}, err
	}
	if system == "" || assertionID == "" {
		return AccessContextWindowEnd{}, invalid(op, "a system and an assertion id are required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var (
		e      AccessContextWindowEnd
		issued *time.Time
	)
	err := r.s.db.QueryRow(ctx, `
		SELECT `+accessContextWindowEndColumns+`
		FROM access_context_window_ends
		WHERE tenant = $1 AND external_system = $2 AND assertion_id = $3`,
		tenant, system, assertionID).Scan(&e.System, &e.AssertionID, &e.Reference, &issued,
		&e.EndedAt, &e.EndedBy.Subject, &e.EndedBy.Principal)
	if err != nil {
		return AccessContextWindowEnd{}, wrap(op, err)
	}
	e.IssuedAt = timeOrZero(issued)
	if !e.IssuedAt.IsZero() {
		e.IssuedAt = e.IssuedAt.UTC()
	}
	e.EndedAt = e.EndedAt.UTC()
	return e, nil
}

func (r accessContextWindowEndRepo) Insert(ctx context.Context, tenant Tenant, e AccessContextWindowEnd) error {
	const op = "store.AccessContextWindowEnds.Insert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	switch {
	case e.System == "" || e.AssertionID == "":
		return invalid(op, "a system and an assertion id are required")
	case e.EndedAt.IsZero():
		return invalid(op, "when the end was kept is required")
	case e.EndedBy.Principal == "":
		return invalid(op, "the credential that pushed the end is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO access_context_window_ends (tenant, `+accessContextWindowEndColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		tenant, e.System, e.AssertionID, e.Reference, nullableTime(e.IssuedAt),
		e.EndedAt, e.EndedBy.Subject, e.EndedBy.Principal)
	return wrap(op, err)
}
