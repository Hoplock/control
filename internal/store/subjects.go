// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
)

type subjectRepo struct{ s *Store }

const subjectColumns = `subject_id, source, display_name, principals, groups, claims, created_at, updated_at`

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
		INSERT INTO subjects (tenant, subject_id, source, display_name, principals, groups, claims)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant, subject_id) DO UPDATE SET
			source       = EXCLUDED.source,
			display_name = EXCLUDED.display_name,
			principals   = EXCLUDED.principals,
			groups       = EXCLUDED.groups,
			claims       = EXCLUDED.claims,
			updated_at   = now()`,
		tenant, s.ID, s.Source, s.DisplayName,
		nonNilStrings(s.Principals), nonNilStrings(s.Groups), nonNilMap(s.Claims))
	return wrap(op, err)
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

// scanSubject reads one subject row. It exists so the two lookups cannot drift
// in what they read or in how they report absence.
func scanSubject(op string, row rowScanner) (Subject, error) {
	var s Subject
	err := row.Scan(&s.ID, &s.Source, &s.DisplayName, &s.Principals, &s.Groups,
		&s.Claims, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return Subject{}, wrap(op, err)
	}
	return s, nil
}
