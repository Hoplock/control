// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
)

type subjectRepo struct{ s *Store }

const subjectColumns = `subject_id, source, display_name, principals, groups, claims,
	break_glass, mapping_version, created_at, updated_at`

func (r subjectRepo) Get(ctx context.Context, tenant Tenant, subjectID string) (Subject, error) {
	const op = "store.Subjects.Get"
	if err := checkTenant(op, tenant); err != nil {
		return Subject{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+subjectColumns+`
		FROM subjects
		WHERE tenant = $1 AND subject_id = $2`, tenant, subjectID)
	return scanSubject(op, row)
}

func (r subjectRepo) GetByPrincipal(ctx context.Context, tenant Tenant, principal string) (Subject, error) {
	const op = "store.Subjects.GetByPrincipal"
	if err := checkTenant(op, tenant); err != nil {
		return Subject{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+subjectColumns+`
		FROM subjects
		WHERE tenant = $1 AND $2 = ANY(principals)`, tenant, principal)
	return scanSubject(op, row)
}

func (r subjectRepo) Upsert(ctx context.Context, tenant Tenant, s Subject) error {
	const op = "store.Subjects.Upsert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if s.ID == "" {
		return invalid(op, "subject id is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO subjects (tenant, subject_id, source, display_name, principals, groups, claims,
			break_glass, mapping_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (tenant, subject_id) DO UPDATE SET
			source       = EXCLUDED.source,
			display_name = EXCLUDED.display_name,
			principals   = EXCLUDED.principals,
			groups       = EXCLUDED.groups,
			claims       = EXCLUDED.claims,
			break_glass  = EXCLUDED.break_glass,
			mapping_version = EXCLUDED.mapping_version,
			updated_at   = now()`,
		tenant, s.ID, s.Source, s.DisplayName,
		nonNilStrings(s.Principals), nonNilStrings(s.Groups), nonNilMap(s.Claims),
		s.BreakGlass, s.MappingVersion)
	return wrap(op, err)
}

// subjectLockPrefix keys the tenant's subject lock (Store.lockTenant).
const subjectLockPrefix = "subjects\x1f"

func (r subjectRepo) Lock(ctx context.Context, tenant Tenant) error {
	return r.s.lockTenant(ctx, "store.Subjects.Lock", subjectLockPrefix, tenant)
}

func (r subjectRepo) Delete(ctx context.Context, tenant Tenant, subjectID string) error {
	const op = "store.Subjects.Delete"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		DELETE FROM subjects WHERE tenant = $1 AND subject_id = $2`, tenant, subjectID)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

// subjectOrder is the subject list's order: its primary key.
var subjectOrder = order{columns: []string{"subject_id"}}

// Page returns one page of the tenant's subjects, in id order: the identities
// an operator administers (0019).
func (r subjectRepo) Page(ctx context.Context, tenant Tenant, p Page) (Paged[Subject], error) {
	const op = "store.Subjects.Page"
	if err := checkTenant(op, tenant); err != nil {
		return Paged[Subject]{}, err
	}
	if err := checkPage(op, subjectOrder, p); err != nil {
		return Paged[Subject]{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tail, args := subjectOrder.clause(p, []any{tenant})
	return queryPage(ctx, r.s.db, op, p, `
		SELECT `+subjectColumns+`
		FROM subjects
		WHERE tenant = $1`+tail, args, scanSubject)
}

// scanSubject reads one subject row. It exists so the lookups and the list
// cannot drift in what they read or in how they report absence.
func scanSubject(op string, row rowScanner) (Subject, error) {
	var s Subject
	err := row.Scan(&s.ID, &s.Source, &s.DisplayName, &s.Principals, &s.Groups,
		&s.Claims, &s.BreakGlass, &s.MappingVersion, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return Subject{}, wrap(op, err)
	}
	return s, nil
}
