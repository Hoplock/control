// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

type decisionRepo struct{ s *Store }

const decisionColumns = `decision_id, subject_id, target_id, inputs_digest, inputs, explanation, ` +
	`effect, proxy_id, session_id, matched_rule, grant_id, obligations, snapshot, decided_at`

// DecisionEffectAllow is the effect of a decision that was served as a
// snapshot. It is named here because one query of this package depends on it —
// the sessions a grant backed are the ALLOWED decisions under it — and
// `internal/decision` spells its own constant from this one, so the two cannot
// drift apart.
const DecisionEffectAllow = "allow"

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
		                       inputs_digest, inputs, explanation, effect, proxy_id, session_id,
		                       matched_rule, grant_id, obligations, snapshot, decided_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		tenant, d.ID, d.SubjectID, d.TargetID, d.InputsDigest,
		nonNilJSON(d.Inputs), nonNilJSON(d.Explanation), d.Effect, d.ProxyID, d.SessionID,
		d.MatchedRule, d.GrantID, nonNilStrings(d.Obligations), nonNilJSON(d.Snapshot), decidedAt)
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

// ListBySession returns the decisions taken for one SSH session, newest first.
//
// It is the lookup an operator actually arrives with: the proxy tells a user
// "access denied" and a session id (M4), and a chained session produces one
// record per hop under that id. Bounded like ListBySubject — nothing on or
// beside the decision path does unbounded work (M5).
func (r decisionRepo) ListBySession(ctx context.Context, tenant Tenant, sessionID string, limit int) ([]Decision, error) {
	const op = "store.Decisions.ListBySession"
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
		WHERE tenant = $1 AND session_id = $2
		ORDER BY decided_at DESC, decision_id DESC
		LIMIT $3`, tenant, sessionID, limit)
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

// ListByGrant returns the decisions a grant supplied, newest first.
//
// It is the grant's side of M4: "what did this grant let anybody do" is the
// first question an auditor asks of one, and decisions_by_grant_idx answers it
// without a scan of every explanation the tenant has produced.
func (r decisionRepo) ListByGrant(ctx context.Context, tenant Tenant, grantID string, limit int) ([]Decision, error) {
	const op = "store.Decisions.ListByGrant"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if grantID == "" {
		// The empty id is every decision no grant supplied, which is not
		// a question anybody means to ask of a grant.
		return nil, invalid(op, "grant id is required")
	}
	if limit <= 0 {
		limit = defaultDecisionLimit
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+decisionColumns+`
		FROM decisions
		WHERE tenant = $1 AND grant_id = $2
		ORDER BY decided_at DESC, decision_id DESC
		LIMIT $3`, tenant, grantID, limit)
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

// SessionsByGrant returns the distinct sessions a grant backed.
//
// A session is a (proxy, session) pair from an ALLOWED decision: that pair is
// exactly what a session_kill addresses, and a chained session contributes one
// pair per hop that asked — each hop holds its own end of it. A denied or
// unserved decision under the grant produced no session, so it contributes
// nothing to end.
//
// The limit is honoured by asking for one more row than it: a caller told the
// answer was cut short can act on that (revocation widens to the subject),
// where a caller handed a silently partial list would leave sessions running.
func (r decisionRepo) SessionsByGrant(ctx context.Context, tenant Tenant, grantID string, limit int) ([]GrantSession, bool, error) {
	const op = "store.Decisions.SessionsByGrant"
	if err := checkTenant(op, tenant); err != nil {
		return nil, false, err
	}
	if grantID == "" {
		return nil, false, invalid(op, "grant id is required")
	}
	if limit <= 0 {
		return nil, false, invalid(op, "a positive limit is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT DISTINCT proxy_id, session_id
		FROM decisions
		WHERE tenant = $1 AND grant_id = $2 AND effect = $3
		  AND proxy_id <> '' AND session_id <> ''
		ORDER BY proxy_id, session_id
		LIMIT $4`, tenant, grantID, DecisionEffectAllow, limit+1)
	if err != nil {
		return nil, false, wrap(op, err)
	}
	defer rows.Close()

	var out []GrantSession
	for rows.Next() {
		var gs GrantSession
		if err := rows.Scan(&gs.ProxyID, &gs.SessionID); err != nil {
			return nil, false, wrap(op, err)
		}
		out = append(out, gs)
	}
	if err := rows.Err(); err != nil {
		return nil, false, wrap(op, err)
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

func scanDecision(op string, row rowScanner) (Decision, error) {
	var d Decision
	err := row.Scan(&d.ID, &d.SubjectID, &d.TargetID, &d.InputsDigest, &d.Inputs, &d.Explanation,
		&d.Effect, &d.ProxyID, &d.SessionID, &d.MatchedRule, &d.GrantID,
		&d.Obligations, &d.Snapshot, &d.DecidedAt)
	if err != nil {
		return Decision{}, wrap(op, err)
	}
	return d, nil
}
