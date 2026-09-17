// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

type decisionRepo struct{ s *Store }

const decisionColumns = `decision_id, subject_id, target_id, inputs_digest, matched_rule, obligations, snapshot, decided_at`

// defaultDecisionLimit bounds ListBySubject when the caller asks for no limit.
// Unbounded work is never acceptable on a path the decision layer shares (M5).
const defaultDecisionLimit = 100

func (r decisionRepo) Insert(ctx context.Context, tenant Tenant, d Decision) error {
	const op = "store.Decisions.Insert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if d.ID == "" {
		return invalid(op, "decision id is required")
	}
	if d.InputsDigest == "" {
		return invalid(op, "inputs digest is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	decidedAt := d.DecidedAt
	if decidedAt.IsZero() {
		decidedAt = time.Now().UTC()
	}

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO decisions (tenant, decision_id, subject_id, target_id,
		                       inputs_digest, matched_rule, obligations, snapshot, decided_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		tenant, d.ID, d.SubjectID, d.TargetID, d.InputsDigest, d.MatchedRule,
		nonNilStrings(d.Obligations), nonNilJSON(d.Snapshot), decidedAt)
	return wrap(op, err)
}

func (r decisionRepo) Get(ctx context.Context, tenant Tenant, decisionID string) (Decision, error) {
	const op = "store.Decisions.Get"
	if err := checkTenant(op, tenant); err != nil {
		return Decision{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+decisionColumns+`
		FROM decisions
		WHERE tenant = $1 AND decision_id = $2`, tenant, decisionID)
	return scanDecision(op, row)
}

func (r decisionRepo) ListBySubject(ctx context.Context, tenant Tenant, subjectID string, limit int) ([]Decision, error) {
	const op = "store.Decisions.ListBySubject"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultDecisionLimit
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+decisionColumns+`
		FROM decisions
		WHERE tenant = $1 AND subject_id = $2
		ORDER BY decided_at DESC, decision_id DESC
		LIMIT $3`, tenant, subjectID, limit)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []Decision
	for rows.Next() {
		d, err := scanDecision(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, wrap(op, rows.Err())
}

func scanDecision(op string, row rowScanner) (Decision, error) {
	var d Decision
	err := row.Scan(&d.ID, &d.SubjectID, &d.TargetID, &d.InputsDigest, &d.MatchedRule,
		&d.Obligations, &d.Snapshot, &d.DecidedAt)
	if err != nil {
		return Decision{}, wrap(op, err)
	}
	return d, nil
}
