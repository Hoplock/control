// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type grantRepo struct{ s *Store }

const grantColumns = `grant_id, subject_id, scope, scope_targets, scope_labels, scope_zones, ` +
	`not_before, expires_at, origin, reason_code, reason, ` +
	`created_by, created_by_principal, created_by_break_glass, ` +
	`request_id, approval_ref, approvers, external_ref, ` +
	`external_system, external_window_start, external_window_end, ` +
	`external_additional_kind, external_additional, ` +
	`revoked_at, revoked_by, revoked_by_principal, revoke_reason, created_at, ` +
	`external_assertion_id, external_mode`

// defaultGrantLimit and maxGrantLimit bound an operator's list. There is no
// unbounded list on this surface, for the reason there is none beside the
// decision path (M5): a tenant's history only grows.
const (
	defaultGrantLimit = 100
	maxGrantLimit     = 1000
)

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
		INSERT INTO grants (tenant, grant_id, subject_id, scope, scope_targets, scope_labels, scope_zones,
		                    not_before, expires_at, origin, reason_code, reason,
		                    created_by, created_by_principal, created_by_break_glass,
		                    request_id, approval_ref, approvers, external_ref,
		                    external_system, external_window_start, external_window_end,
		                    external_additional_kind, external_additional, revoked_at,
		                    revoked_by, revoked_by_principal, revoke_reason,
		                    external_assertion_id, external_mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
		        $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30)`,
		tenant, g.ID, g.SubjectID, g.Scope,
		nonNilStrings(g.ScopeTargets), nonNilMap(g.ScopeLabels), nonNilStrings(g.ScopeZones),
		notBefore, g.ExpiresAt, string(g.Origin), g.ReasonCode, g.Reason,
		g.CreatedBy.Subject, g.CreatedBy.Principal, g.CreatedBy.BreakGlass,
		g.RequestID, g.ApprovalRef, nonNilStrings(g.Approvers), g.ExternalRef,
		g.External.System, nullableTime(g.External.WindowStart), nullableTime(g.External.WindowEnd),
		g.External.AdditionalKind, g.External.Additional, nullableTime(g.RevokedAt),
		g.RevokedBy.Subject, g.RevokedBy.Principal, g.RevokeReason,
		g.External.AssertionID, string(g.External.Mode))
	return wrap(op, err)
}

func (r grantRepo) GetByAssertion(ctx context.Context, tenant Tenant, system, assertionID string) (Grant, error) {
	const op = "store.Grants.GetByAssertion"
	if err := checkTenant(op, tenant); err != nil {
		return Grant{}, err
	}
	if system == "" || assertionID == "" {
		return Grant{}, invalid(op, "a system and an assertion id are required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// Served by grants_external_assertion_key (0009), the same unique index
	// that makes a second insert of one assertion impossible.
	row := r.s.db.QueryRow(ctx, `
		SELECT `+grantColumns+`
		FROM grants
		WHERE tenant = $1 AND external_system = $2 AND external_assertion_id = $3`,
		tenant, system, assertionID)
	return scanGrant(op, row)
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
	//
	// EXPIRY IS THIS PREDICATE (0012). A grant stops being an input the
	// instant `at` reaches its expiry, because that is what the WHERE clause
	// says — not because anything ran. There is no sweeper to be stuck.
	rows, err := r.s.db.Query(ctx, `
		SELECT `+grantColumns+`
		FROM grants
		WHERE tenant = $1
		  AND subject_id = $2
		  AND revoked_at IS NULL
		  AND not_before <= $3
		  AND expires_at > $3
		ORDER BY expires_at, grant_id`, tenant, subjectID, at)
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

func (r grantRepo) List(ctx context.Context, tenant Tenant, q GrantQuery) ([]Grant, error) {
	const op = "store.Grants.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	limit := q.Limit
	switch {
	case limit <= 0:
		limit = defaultGrantLimit
	case limit > maxGrantLimit:
		limit = maxGrantLimit
	}

	conds := []string{"tenant = $1"}
	args := []any{tenant}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if q.SubjectID != "" {
		conds = append(conds, "subject_id = "+arg(q.SubjectID))
	}
	if q.State != "" && q.At.IsZero() {
		return nil, invalid(op, "a state filter needs the instant it is judged at")
	}
	// The same four states Grant.State computes, as predicates over the
	// instant the caller supplied. Three of them are functions of the clock
	// and none of them is a stored column.
	switch q.State {
	case "":
	case GrantActive:
		at := arg(q.At)
		conds = append(conds, "revoked_at IS NULL AND not_before <= "+at+" AND expires_at > "+at)
	case GrantScheduled:
		conds = append(conds, "revoked_at IS NULL AND not_before > "+arg(q.At))
	case GrantExpired:
		conds = append(conds, "revoked_at IS NULL AND expires_at <= "+arg(q.At))
	case GrantRevoked:
		conds = append(conds, "revoked_at IS NOT NULL")
	default:
		return nil, invalid(op, fmt.Sprintf("%q is not a grant state", q.State))
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+grantColumns+`
		FROM grants
		WHERE `+strings.Join(conds, " AND ")+`
		ORDER BY created_at DESC, grant_id DESC
		LIMIT `+arg(limit), args...)
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

func (r grantRepo) Revoke(ctx context.Context, tenant Tenant, grantID string, rev GrantRevocation) (bool, error) {
	const op = "store.Grants.Revoke"
	if err := checkTenant(op, tenant); err != nil {
		return false, err
	}
	if rev.At.IsZero() {
		return false, invalid(op, "revocation time is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// Only an unrevoked grant is touched, so the FIRST revocation's time,
	// revoker and reason are the ones that stand. An auditor asks when
	// access stopped, and the second answer is not more true than the first.
	tag, err := r.s.db.Exec(ctx, `
		UPDATE grants
		SET revoked_at = $3, revoked_by = $4, revoked_by_principal = $5, revoke_reason = $6
		WHERE tenant = $1 AND grant_id = $2 AND revoked_at IS NULL`,
		tenant, grantID, rev.At, rev.By.Subject, rev.By.Principal, rev.Reason)
	if err != nil {
		return false, wrap(op, err)
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}

	var exists bool
	err = r.s.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM grants WHERE tenant = $1 AND grant_id = $2)`,
		tenant, grantID).Scan(&exists)
	if err != nil {
		return false, wrap(op, err)
	}
	if !exists {
		return false, notFound(op)
	}
	return false, nil
}

func scanGrant(op string, row rowScanner) (Grant, error) {
	var (
		g          Grant
		origin     string
		mode       string
		windowFrom *time.Time
		windowTo   *time.Time
		revokedAt  *time.Time
	)
	err := row.Scan(&g.ID, &g.SubjectID, &g.Scope, &g.ScopeTargets, &g.ScopeLabels, &g.ScopeZones,
		&g.NotBefore, &g.ExpiresAt, &origin, &g.ReasonCode, &g.Reason,
		&g.CreatedBy.Subject, &g.CreatedBy.Principal, &g.CreatedBy.BreakGlass,
		&g.RequestID, &g.ApprovalRef, &g.Approvers, &g.ExternalRef,
		&g.External.System, &windowFrom, &windowTo,
		&g.External.AdditionalKind, &g.External.Additional,
		&revokedAt, &g.RevokedBy.Subject, &g.RevokedBy.Principal, &g.RevokeReason, &g.CreatedAt,
		&g.External.AssertionID, &mode)
	if err != nil {
		return Grant{}, wrap(op, err)
	}
	g.Origin = GrantOrigin(origin)
	g.External.Mode = ExternalMode(mode)
	g.External.WindowStart = timeOrZero(windowFrom)
	g.External.WindowEnd = timeOrZero(windowTo)
	g.RevokedAt = timeOrZero(revokedAt)
	return g, nil
}

// ---------------------------------------------------------------------------
// grant requests (ext.GrantWorkflow)
// ---------------------------------------------------------------------------

type grantRequestRepo struct{ s *Store }

const grantRequestColumns = `request_id, subject_id, scope, scope_targets, scope_labels, scope_zones, ` +
	`not_before, expires_at, reason_code, reason, external_ref, ` +
	`requested_by, requested_by_principal, requested_by_break_glass, requested_at, ` +
	`workflow_provider, workflow_ref, state, outcome_code, outcome_text, approvals, ` +
	`window_clamped, grant_id, decided_at, polled_at`

// defaultPendingLimit bounds one pass over a tenant's pending requests.
const defaultPendingLimit = 50

func (r grantRequestRepo) Insert(ctx context.Context, tenant Tenant, req GrantRequest) error {
	const op = "store.GrantRequests.Insert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	switch {
	case req.ID == "":
		return invalid(op, "request id is required")
	case req.SubjectID == "":
		return invalid(op, "request subject is required")
	case req.NotBefore.IsZero() || req.ExpiresAt.IsZero():
		return invalid(op, "request window is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// A request is born pending, whatever the caller put in State: the only
	// way out of pending is Resolve, which is the one place a decision is
	// applied.
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO grant_requests (tenant, request_id, subject_id, scope, scope_targets, scope_labels,
		                            scope_zones, not_before, expires_at, reason_code, reason, external_ref,
		                            requested_by, requested_by_principal, requested_by_break_glass,
		                            requested_at, workflow_provider, workflow_ref, state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
		        COALESCE($16, now()), $17, $18, 'pending')`,
		tenant, req.ID, req.SubjectID, req.Scope,
		nonNilStrings(req.ScopeTargets), nonNilMap(req.ScopeLabels), nonNilStrings(req.ScopeZones),
		req.NotBefore, req.ExpiresAt, req.ReasonCode, req.Reason, req.ExternalRef,
		req.RequestedBy.Subject, req.RequestedBy.Principal, req.RequestedBy.BreakGlass,
		nullableTime(req.RequestedAt), req.WorkflowProvider, req.WorkflowRef)
	return wrap(op, err)
}

func (r grantRequestRepo) Get(ctx context.Context, tenant Tenant, requestID string) (GrantRequest, error) {
	const op = "store.GrantRequests.Get"
	if err := checkTenant(op, tenant); err != nil {
		return GrantRequest{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+grantRequestColumns+`
		FROM grant_requests
		WHERE tenant = $1 AND request_id = $2`, tenant, requestID)
	return scanGrantRequest(op, row)
}

func (r grantRequestRepo) ListPending(ctx context.Context, tenant Tenant, limit int) ([]GrantRequest, error) {
	const op = "store.GrantRequests.ListPending"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultPendingLimit
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// Least recently asked about first (grant_requests_pending_idx), so a
	// request the workflow keeps failing on cannot starve the others.
	rows, err := r.s.db.Query(ctx, `
		SELECT `+grantRequestColumns+`
		FROM grant_requests
		WHERE tenant = $1 AND state = 'pending'
		ORDER BY polled_at NULLS FIRST, requested_at, request_id
		LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []GrantRequest
	for rows.Next() {
		req, err := scanGrantRequest(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, wrap(op, rows.Err())
}

func (r grantRequestRepo) Polled(ctx context.Context, tenant Tenant, requestID, workflowRef string, at time.Time) error {
	const op = "store.GrantRequests.Polled"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if at.IsZero() {
		return invalid(op, "the poll time is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// The workflow's reference is written once: a resubmission that the
	// workflow answered with a different reference would mean it did not
	// treat the request id as one request, and the first reference is the
	// one every later poll must keep asking about.
	_, err := r.s.db.Exec(ctx, `
		UPDATE grant_requests
		SET polled_at = $4,
		    workflow_ref = CASE WHEN workflow_ref = '' THEN $3 ELSE workflow_ref END
		WHERE tenant = $1 AND request_id = $2 AND state = 'pending'`,
		tenant, requestID, workflowRef, at)
	return wrap(op, err)
}

func (r grantRequestRepo) Resolve(ctx context.Context, tenant Tenant, requestID string, res GrantRequestResolution) error {
	const op = "store.GrantRequests.Resolve"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	switch res.State {
	case GrantRequestPending:
		return invalid(op, "a request is resolved into a decision, never into pending")
	case GrantRequestApproved:
		if res.GrantID == "" {
			return invalid(op, "an approved request must name the grant it produced")
		}
	case GrantRequestDenied, GrantRequestExpired, GrantRequestCancelled, GrantRequestFailed:
		if res.GrantID != "" {
			return invalid(op, "only an approved request names a grant")
		}
	default:
		return invalid(op, fmt.Sprintf("%q is not a request state", res.State))
	}
	if res.At.IsZero() {
		return invalid(op, "the decision time is required")
	}
	approvals, err := json.Marshal(nonNilApprovals(res.Approvals))
	if err != nil {
		return &Error{Op: op, Kind: KindInternal, Err: err}
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		UPDATE grant_requests
		SET state = $3, outcome_code = $4, outcome_text = $5, approvals = $6,
		    window_clamped = $7, grant_id = $8,
		    workflow_ref = CASE WHEN workflow_ref = '' THEN $9 ELSE workflow_ref END,
		    decided_at = $10, polled_at = $10
		WHERE tenant = $1 AND request_id = $2 AND state = 'pending'`,
		tenant, requestID, string(res.State), res.OutcomeCode, res.OutcomeText, json.RawMessage(approvals),
		res.WindowClamped, res.GrantID, res.WorkflowRef, res.At)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	var exists bool
	err = r.s.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM grant_requests WHERE tenant = $1 AND request_id = $2)`,
		tenant, requestID).Scan(&exists)
	if err != nil {
		return wrap(op, err)
	}
	if !exists {
		return notFound(op)
	}
	return conflict(op, "the request has already been decided")
}

func nonNilApprovals(v []GrantApproval) []GrantApproval {
	if v == nil {
		return []GrantApproval{}
	}
	return v
}

func scanGrantRequest(op string, row rowScanner) (GrantRequest, error) {
	var (
		req       GrantRequest
		state     string
		approvals []byte
		decidedAt *time.Time
		polledAt  *time.Time
	)
	err := row.Scan(&req.ID, &req.SubjectID, &req.Scope, &req.ScopeTargets, &req.ScopeLabels, &req.ScopeZones,
		&req.NotBefore, &req.ExpiresAt, &req.ReasonCode, &req.Reason, &req.ExternalRef,
		&req.RequestedBy.Subject, &req.RequestedBy.Principal, &req.RequestedBy.BreakGlass, &req.RequestedAt,
		&req.WorkflowProvider, &req.WorkflowRef, &state, &req.OutcomeCode, &req.OutcomeText, &approvals,
		&req.WindowClamped, &req.GrantID, &decidedAt, &polledAt)
	if err != nil {
		return GrantRequest{}, wrap(op, err)
	}
	req.State = GrantRequestState(state)
	req.DecidedAt = timeOrZero(decidedAt)
	req.PolledAt = timeOrZero(polledAt)
	if len(approvals) > 0 {
		if err := json.Unmarshal(approvals, &req.Approvals); err != nil {
			return GrantRequest{}, &Error{Op: op, Kind: KindInternal, Err: err}
		}
	}
	return req, nil
}
