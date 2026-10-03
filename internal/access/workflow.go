// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/store"
)

// The Enterprise seam: routing a grant through a registered ext.GrantWorkflow.
//
// A WORKFLOW DECIDES WHETHER A GRANT IS CREATED, AND NOTHING ELSE. The grant it
// approves is the same object an administrator creates — same table, same
// translation into the engine (PolicyGrant), same decision record — with
// origin `workflow` and the request, the workflow's reference and the approvers
// recorded beside it. A request it has not approved lives in a table the
// decision path never reads, so "pending is not access" is a property of where
// the row is rather than of a predicate somebody has to remember.
//
// What Control does with each answer is stated on ext.GrantWorkflow itself,
// because that is what Hoplock Enterprise builds against; the functions below
// are that statement, executed.

// Outcome codes this server writes on a request it closed itself. A workflow's
// own ReasonCode is kept wherever it gave one.
const (
	// OutcomeWindowClosed: still pending when the window it asked for
	// closed, so approving it could no longer grant anything.
	OutcomeWindowClosed = "window_closed"
	// OutcomeApprovedTooLate: approved for a window that had already closed
	// by the time the approval arrived.
	OutcomeApprovedTooLate = "approved_after_window"
	// OutcomeCancelled: withdrawn by an operator.
	OutcomeCancelled = "cancelled"
	// OutcomeWorkflowLostRequest: the workflow no longer knows the
	// reference it gave.
	OutcomeWorkflowLostRequest = "workflow_lost_request"
	// OutcomeWorkflowDenied, OutcomeWorkflowExpired: the workflow's answer,
	// when it gave no code of its own.
	OutcomeWorkflowDenied  = "workflow_denied"
	OutcomeWorkflowExpired = "workflow_expired"
	// outcomeWorkflowPrefix prefixes the error kind a workflow failed with,
	// e.g. `workflow_invalid`.
	outcomeWorkflowPrefix = "workflow_"
)

// maxOutcomeText bounds a diagnostic a workflow handed back, which is stored
// and shown to operators.
const maxOutcomeText = 512

// request records a request, then puts it to the workflow.
func (s *Service) request(ctx context.Context, tenant store.Tenant, actor Actor, spec Spec, now time.Time) (Created, error) {
	req := store.GrantRequest{
		ID:               s.newID("gr"),
		SubjectID:        spec.Subject,
		Scope:            spec.Scope.Name,
		ScopeTargets:     spec.Scope.Targets,
		ScopeLabels:      spec.Scope.Labels,
		ScopeZones:       spec.Scope.Zones,
		NotBefore:        spec.NotBefore,
		ExpiresAt:        spec.ExpiresAt,
		ReasonCode:       spec.ReasonCode,
		Reason:           spec.Reason,
		ExternalRef:      spec.ExternalRef,
		RequestedBy:      actor.grantActor(),
		RequestedAt:      now,
		WorkflowProvider: s.provider,
	}
	// The request is recorded BEFORE the workflow hears of it, so there is
	// never a request the workflow is deciding that this server has no row
	// for — and so the request id the workflow must treat as one request is
	// durable before it is ever sent.
	err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.GrantRequests().Insert(ctx, tenant, req); err != nil {
			return err
		}
		stored, err := tx.GrantRequests().Get(ctx, tenant, req.ID)
		if err != nil {
			return err
		}
		req = stored
		return s.recorder.GrantEvent(ctx, tx, tenant, Event{
			Name: EventGrantRequested, Actor: actor, Workflow: s.provider, Request: &req, At: now,
		})
	})
	if err != nil {
		return Created{}, err
	}
	s.announce(ctx, tenant, Event{Name: EventGrantRequested, Actor: actor, Workflow: s.provider, Request: &req, At: now})
	s.Track(tenant)
	return s.submit(ctx, tenant, actor, req)
}

// submit puts a request to the workflow, or puts it again: Submit is
// idempotent on the request id, which is what makes resubmitting after an
// outage safe.
func (s *Service) submit(ctx context.Context, tenant store.Tenant, actor Actor, req store.GrantRequest) (Created, error) {
	wreq, err := s.workflowRequest(ctx, tenant, req)
	if err != nil {
		return Created{Request: &req}, err
	}
	wctx, cancel := context.WithTimeout(ctx, s.workflowTimeout)
	decision, err := s.workflow.Submit(wctx, wreq)
	cancel()
	if err != nil {
		return s.workflowFailed(ctx, tenant, actor, req, err, "submit")
	}
	return s.apply(ctx, tenant, actor, req, decision)
}

// workflowFailed decides what a workflow's error means for the request.
func (s *Service) workflowFailed(ctx context.Context, tenant store.Tenant, actor Actor, req store.GrantRequest, err error, op string) (Created, error) {
	kind := ext.KindOf(err)
	switch kind {
	case ext.KindInternal, ext.KindUnavailable:
		// An outage. The request stays open and is asked about again, so
		// an approver's answer is not lost to a network blip — and it is
		// never taken as a "no", which would be an outage read as a
		// decision (M11).
		s.log.WarnContext(ctx, "the grant workflow could not be reached; the request stays open",
			"event", "grant_workflow_unreachable",
			"tenant", tenant.String(),
			"request_id", req.ID,
			"op", op,
			"provider", s.provider,
			"error", err.Error(),
		)
		now := s.now().UTC()
		if perr := s.store.GrantRequests().Polled(ctx, tenant, req.ID, "", now); perr != nil {
			return Created{Request: &req}, perr
		}
		req.PolledAt = now
		return Created{Request: &req, Unconfirmed: req.WorkflowRef == ""}, nil
	case ext.KindDenied:
		// A refusal chosen on purpose, which is the only kind of error
		// that may be one.
		return s.close(ctx, tenant, actor, req, store.GrantRequestDenied,
			OutcomeWorkflowDenied, clip(err.Error()), nil, "")
	case ext.KindNotFound:
		if op == "status" {
			return s.close(ctx, tenant, actor, req, store.GrantRequestFailed,
				OutcomeWorkflowLostRequest, clip(err.Error()), nil, "")
		}
		return s.close(ctx, tenant, actor, req, store.GrantRequestFailed,
			outcomeWorkflowPrefix+kind.String(), clip(err.Error()), nil, "")
	case ext.KindInvalid, ext.KindConflict, ext.KindDisabled, ext.KindMalformed:
		// An answer that asking again would repeat.
		return s.close(ctx, tenant, actor, req, store.GrantRequestFailed,
			outcomeWorkflowPrefix+kind.String(), clip(err.Error()), nil, "")
	}
	// ext.KindOf answers only the kinds above; an unknown one is treated as
	// the outage it most likely is.
	return Created{Request: &req}, err
}

// apply applies a workflow's answer to a request.
func (s *Service) apply(ctx context.Context, tenant store.Tenant, actor Actor, req store.GrantRequest, d ext.GrantDecision) (Created, error) {
	switch d.Outcome {
	case ext.GrantPending:
		now := s.now().UTC()
		if err := s.store.GrantRequests().Polled(ctx, tenant, req.ID, d.WorkflowRef, now); err != nil {
			return Created{Request: &req}, err
		}
		if req.WorkflowRef == "" {
			req.WorkflowRef = d.WorkflowRef
		}
		req.PolledAt = now
		return Created{Request: &req}, nil
	case ext.GrantApproved:
		return s.approve(ctx, tenant, actor, req, d)
	case ext.GrantDenied:
		return s.close(ctx, tenant, actor, req, store.GrantRequestDenied,
			or(d.ReasonCode, OutcomeWorkflowDenied), clip(d.ReasonText), approvalsOf(d), d.WorkflowRef)
	case ext.GrantExpired:
		return s.close(ctx, tenant, actor, req, store.GrantRequestExpired,
			or(d.ReasonCode, OutcomeWorkflowExpired), clip(d.ReasonText), approvalsOf(d), d.WorkflowRef)
	}
	return s.close(ctx, tenant, actor, req, store.GrantRequestFailed,
		outcomeWorkflowPrefix+"unknown_outcome", "", approvalsOf(d), d.WorkflowRef)
}

// approve creates the grant a workflow approved, and closes the request that
// produced it, in one transaction.
func (s *Service) approve(ctx context.Context, tenant store.Tenant, actor Actor, req store.GrantRequest, d ext.GrantDecision) (Created, error) {
	now := s.now().UTC()
	from, to, clamped := clamp(req.NotBefore, req.ExpiresAt, d.Window)
	approvals := approvalsOf(d)
	if !to.After(from) || !to.After(now) {
		// Approved for a window that grants nothing any more. Creating the
		// grant would record access that never existed.
		return s.close(ctx, tenant, actor, req, store.GrantRequestExpired,
			OutcomeApprovedTooLate, clip(d.ReasonText), approvals, d.WorkflowRef)
	}

	ref := or(req.WorkflowRef, d.WorkflowRef)
	g := store.Grant{
		ID:           s.newID("g"),
		SubjectID:    req.SubjectID,
		Scope:        req.Scope,
		ScopeTargets: req.ScopeTargets,
		ScopeLabels:  req.ScopeLabels,
		ScopeZones:   req.ScopeZones,
		NotBefore:    from,
		ExpiresAt:    to,
		Origin:       store.GrantOriginWorkflow,
		ReasonCode:   req.ReasonCode,
		Reason:       req.Reason,
		// The grant's creator is who ASKED. The workflow decided and the
		// approvers agreed, and both are recorded beside it.
		CreatedBy:   req.RequestedBy,
		RequestID:   req.ID,
		ApprovalRef: ref,
		Approvers:   approverIDs(approvals),
		ExternalRef: req.ExternalRef,
	}

	var resolved store.GrantRequest
	err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		// The request moves first: it is the guard. Two nodes polling the
		// same workflow both see "approved", and only one of them moves
		// the request out of pending; the other's transaction stops here.
		if err := tx.GrantRequests().Resolve(ctx, tenant, req.ID, store.GrantRequestResolution{
			State:         store.GrantRequestApproved,
			OutcomeCode:   d.ReasonCode,
			OutcomeText:   clip(d.ReasonText),
			Approvals:     approvals,
			WindowClamped: clamped,
			GrantID:       g.ID,
			WorkflowRef:   ref,
			At:            now,
		}); err != nil {
			return err
		}
		if err := tx.Grants().Insert(ctx, tenant, g); err != nil {
			return err
		}
		stored, err := tx.Grants().Get(ctx, tenant, g.ID)
		if err != nil {
			return err
		}
		g = stored
		resolved, err = tx.GrantRequests().Get(ctx, tenant, req.ID)
		if err != nil {
			return err
		}
		return s.recorder.GrantEvent(ctx, tx, tenant, Event{
			Name: EventGrantCreated, Actor: actor, Workflow: s.provider,
			Grant: &g, Request: &resolved, At: now,
		})
	})
	if store.IsConflict(err) {
		return s.current(ctx, tenant, req.ID)
	}
	if err != nil {
		return Created{Request: &req}, err
	}
	s.announce(ctx, tenant, Event{
		Name: EventGrantCreated, Actor: actor, Workflow: s.provider, Grant: &g, Request: &resolved, At: now,
	})
	return Created{Grant: &g, Request: &resolved}, nil
}

// close moves a request to a terminal state that produced no grant.
func (s *Service) close(
	ctx context.Context,
	tenant store.Tenant,
	actor Actor,
	req store.GrantRequest,
	state store.GrantRequestState,
	code, text string,
	approvals []store.GrantApproval,
	ref string,
) (Created, error) {
	now := s.now().UTC()
	workflow := s.provider
	if state == store.GrantRequestCancelled || code == OutcomeWindowClosed {
		// Control closed these itself; no workflow decided them.
		workflow = ""
	}
	var resolved store.GrantRequest
	err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.GrantRequests().Resolve(ctx, tenant, req.ID, store.GrantRequestResolution{
			State:       state,
			OutcomeCode: code,
			OutcomeText: text,
			Approvals:   approvals,
			WorkflowRef: ref,
			At:          now,
		}); err != nil {
			return err
		}
		var err error
		resolved, err = tx.GrantRequests().Get(ctx, tenant, req.ID)
		if err != nil {
			return err
		}
		return s.recorder.GrantEvent(ctx, tx, tenant, Event{
			Name: EventGrantRequestClosed, Actor: actor, Workflow: workflow, Request: &resolved, At: now,
		})
	})
	if store.IsConflict(err) {
		return s.current(ctx, tenant, req.ID)
	}
	if err != nil {
		return Created{Request: &req}, err
	}
	s.announce(ctx, tenant, Event{
		Name: EventGrantRequestClosed, Actor: actor, Workflow: workflow, Request: &resolved, At: now,
	})
	return Created{Request: &resolved}, nil
}

// current reads a request as it stands — after another node applied a
// decision first — together with the grant it produced, if it produced one.
func (s *Service) current(ctx context.Context, tenant store.Tenant, requestID string) (Created, error) {
	req, err := s.store.GrantRequests().Get(ctx, tenant, requestID)
	if err != nil {
		return Created{}, err
	}
	out := Created{Request: &req}
	if req.GrantID != "" {
		g, err := s.store.Grants().Get(ctx, tenant, req.GrantID)
		if err != nil {
			return Created{}, err
		}
		out.Grant = &g
	}
	return out, nil
}

// Request returns a workflow request as it stands, asking the workflow first if
// it is still pending: reading a request is also the moment this server learns
// the tenant has one to poll.
func (s *Service) Request(ctx context.Context, tenant store.Tenant, requestID string) (Created, error) {
	req, err := s.store.GrantRequests().Get(ctx, tenant, requestID)
	if err != nil {
		return Created{}, err
	}
	if req.State != store.GrantRequestPending {
		return s.current(ctx, tenant, requestID)
	}
	s.Track(tenant)
	out, err := s.advance(ctx, tenant, req)
	if err != nil {
		// The stored request is still the truth; failing to advance it is
		// the poller's problem to retry, not a reason this read failed.
		s.log.WarnContext(ctx, "a pending grant request could not be advanced on read",
			"event", "grant_request_advance_failed",
			"tenant", tenant.String(),
			"request_id", requestID,
			"error", err.Error(),
		)
		return Created{Request: &req, Unconfirmed: req.WorkflowRef == ""}, nil
	}
	return out, nil
}

// Cancel withdraws a pending request. The workflow is told, best effort; the
// request is closed here whether or not it heard, because an approval that
// arrives for a cancelled request is ignored — the request is no longer pending
// and nothing can move it back.
func (s *Service) Cancel(ctx context.Context, tenant store.Tenant, actor Actor, requestID, reason string) (Created, error) {
	if err := actor.check(); err != nil {
		return Created{}, err
	}
	reason = strings.TrimSpace(reason)
	if err := prose("reason", reason, maxReasonLen, false); err != nil {
		return Created{}, err
	}
	req, err := s.store.GrantRequests().Get(ctx, tenant, requestID)
	if err != nil {
		return Created{}, err
	}
	if req.State != store.GrantRequestPending {
		out, err := s.current(ctx, tenant, requestID)
		if err != nil {
			return Created{}, err
		}
		return out, ErrNotPending
	}
	if s.workflow != nil && req.WorkflowRef != "" {
		wctx, cancel := context.WithTimeout(ctx, s.workflowTimeout)
		err := s.workflow.Cancel(wctx, ext.Tenant(tenant), req.WorkflowRef, reason)
		cancel()
		if err != nil {
			s.log.WarnContext(ctx, "the grant workflow was not told a request was cancelled",
				"event", "grant_workflow_cancel_failed",
				"tenant", tenant.String(),
				"request_id", requestID,
				"error", err.Error(),
			)
		}
	}
	return s.close(ctx, tenant, actor, req, store.GrantRequestCancelled, OutcomeCancelled, reason, nil, "")
}

// advance moves one pending request forward as far as it can go now.
func (s *Service) advance(ctx context.Context, tenant store.Tenant, req store.GrantRequest) (Created, error) {
	now := s.now().UTC()
	if !now.Before(req.ExpiresAt) {
		// The window asked for has closed: an approval now could grant
		// nothing, so the request is expired rather than left open for a
		// workflow to approve into the past.
		if s.workflow != nil && req.WorkflowRef != "" {
			wctx, cancel := context.WithTimeout(ctx, s.workflowTimeout)
			err := s.workflow.Cancel(wctx, ext.Tenant(tenant), req.WorkflowRef,
				"the requested window closed before a decision")
			cancel()
			if err != nil {
				s.log.WarnContext(ctx, "the grant workflow was not told a request expired",
					"event", "grant_workflow_cancel_failed",
					"tenant", tenant.String(),
					"request_id", req.ID,
					"error", err.Error(),
				)
			}
		}
		return s.close(ctx, tenant, Actor{}, req, store.GrantRequestExpired, OutcomeWindowClosed, "", nil, "")
	}
	if s.workflow == nil {
		// Nothing registered can decide it. It stays as it is, and the
		// window closing is what ends it.
		return Created{Request: &req}, nil
	}
	if req.WorkflowRef == "" {
		return s.submit(ctx, tenant, Actor{}, req)
	}
	wctx, cancel := context.WithTimeout(ctx, s.workflowTimeout)
	decision, err := s.workflow.Status(wctx, ext.Tenant(tenant), req.WorkflowRef)
	cancel()
	if err != nil {
		return s.workflowFailed(ctx, tenant, Actor{}, req, err, "status")
	}
	return s.apply(ctx, tenant, Actor{}, req, decision)
}

// Poll advances a tenant's pending requests, least recently asked about first.
func (s *Service) Poll(ctx context.Context, tenant store.Tenant) error {
	pending, err := s.store.GrantRequests().ListPending(ctx, tenant, pollBatch)
	if err != nil {
		return err
	}
	var first error
	for _, req := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := s.advance(ctx, tenant, req); err != nil {
			s.log.WarnContext(ctx, "a pending grant request could not be advanced",
				"event", "grant_request_advance_failed",
				"tenant", tenant.String(),
				"request_id", req.ID,
				"error", err.Error(),
			)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// Watch polls every tracked tenant's pending requests until ctx is done. It is
// run only when a workflow is registered: without one, nothing is ever pending.
func (s *Service) Watch(ctx context.Context, interval time.Duration) {
	if s.workflow == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, tenant := range s.tracked() {
			if err := s.Poll(ctx, tenant); err != nil && ctx.Err() == nil {
				s.log.WarnContext(ctx, "grant workflow poll failed",
					"event", "grant_workflow_poll_failed",
					"tenant", tenant.String(),
					"error", err.Error(),
				)
			}
		}
	}
}

// workflowRequest renders a request for the workflow: the scope exactly as it
// would be stored, and the inventory records for the hostnames it names, so an
// approver sees what they are approving rather than an approximation of it.
func (s *Service) workflowRequest(ctx context.Context, tenant store.Tenant, req store.GrantRequest) (ext.GrantRequest, error) {
	subject := ext.Subject{ID: req.SubjectID}
	row, err := s.store.Subjects().Get(ctx, tenant, req.SubjectID)
	switch {
	case err == nil:
		subject.Username = row.DisplayName
		subject.Groups = row.Groups
	case store.IsNotFound(err):
		// A subject who has never logged in: the id is all there is.
	default:
		return ext.GrantRequest{}, err
	}

	var targets []ext.Target
	for _, host := range req.ScopeTargets {
		if strings.HasPrefix(host, "*.") {
			continue
		}
		t, err := s.store.Targets().GetByHostname(ctx, tenant, host)
		switch {
		case err == nil:
			targets = append(targets, ext.Target{ID: t.ID, Hostname: t.Hostname, Zone: t.Zone, Labels: t.Labels})
		case store.IsNotFound(err):
		default:
			return ext.GrantRequest{}, err
		}
	}

	requester := ext.Subject{ID: req.RequestedBy.Subject}
	if requester.ID == "" {
		// A machine token has no person behind it; its credential is who
		// asked.
		requester.ID = req.RequestedBy.Principal
	}

	return ext.GrantRequest{
		Tenant:    ext.Tenant(tenant),
		RequestID: req.ID,
		Subject:   subject,
		Scope: ext.GrantScope{
			Name:      req.Scope,
			Hostnames: req.ScopeTargets,
			Labels:    req.ScopeLabels,
			Zones:     req.ScopeZones,
		},
		Targets:     targets,
		Privileges:  []string{req.Scope},
		Window:      ext.Window{NotBefore: req.NotBefore, NotAfter: req.ExpiresAt},
		ReasonCode:  req.ReasonCode,
		ReasonText:  req.Reason,
		RequestedBy: requester,
		RequestedAt: req.RequestedAt,
		ExternalRef: req.ExternalRef,
	}, nil
}

// clamp narrows an approved window to the one asked for. A zero end means "as
// asked"; an end reaching outside the request is clamped, and the caller
// records that it was (ext.GrantDecision.Window).
func clamp(askedFrom, askedTo time.Time, approved ext.Window) (from, to time.Time, clamped bool) {
	from, to = askedFrom, askedTo
	if !approved.NotBefore.IsZero() {
		if approved.NotBefore.Before(askedFrom) {
			clamped = true
		} else {
			from = approved.NotBefore
		}
	}
	if !approved.NotAfter.IsZero() {
		if approved.NotAfter.After(askedTo) {
			clamped = true
		} else {
			to = approved.NotAfter
		}
	}
	return from.UTC().Truncate(time.Microsecond), to.UTC().Truncate(time.Microsecond), clamped
}

func approvalsOf(d ext.GrantDecision) []store.GrantApproval {
	if len(d.Approvals) == 0 {
		return nil
	}
	out := make([]store.GrantApproval, 0, len(d.Approvals))
	for _, a := range d.Approvals {
		out = append(out, store.GrantApproval{
			Approver: a.Approver.ID,
			Approved: a.Approved,
			At:       a.At.UTC(),
			Reason:   clip(a.ReasonText),
		})
	}
	return out
}

// approverIDs are the approvers whose answer was yes. A "no" in an approved
// decision is evidence the workflow weighed, and it stays on the request; it
// is not an approver of the grant.
func approverIDs(approvals []store.GrantApproval) []string {
	var out []string
	for _, a := range approvals {
		if a.Approved && a.Approver != "" {
			out = append(out, a.Approver)
		}
	}
	return out
}

// clip bounds a diagnostic a workflow handed back, on a rune boundary.
func clip(s string) string {
	if len(s) <= maxOutcomeText {
		return s
	}
	cut := maxOutcomeText
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
