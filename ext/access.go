// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"context"
	"time"
)

// GrantOutcome is what a workflow decided about a request. It is a closed enum
// so that a caller switching on it cannot quietly forget the pending case,
// which is the one that makes a workflow a workflow.
type GrantOutcome int

const (
	// GrantPending means nobody has decided yet. Control creates no grant and
	// polls Status until the outcome changes.
	GrantPending GrantOutcome = iota
	// GrantApproved means the grant may be created, for at most the window
	// the decision names.
	GrantApproved
	// GrantDenied means it may not. This is a decision, not a failure: it is
	// the one place in this package where "no" is an answer rather than an
	// outage (M11).
	GrantDenied
	// GrantExpired means the request was never decided and is no longer
	// open.
	GrantExpired
)

// String renders the outcome as the stable code that crosses the wire.
func (o GrantOutcome) String() string {
	switch o {
	case GrantPending:
		return "pending"
	case GrantApproved:
		return "approved"
	case GrantDenied:
		return "denied"
	case GrantExpired:
		return "expired"
	}
	return "pending"
}

// GrantScope is what a grant would cover: what it permits, and which targets
// it reaches. It is the grant's own scope, carried as Control stores it, so a
// workflow governs exactly what would be created — an approver who is shown
// three hostnames when the grant covers every `env=prod` target has approved
// something other than what they were asked.
//
// The three selectors are ANDed, and an empty one is unconstrained. A grant
// never reaches a target no policy rule reaches: it is an input the policy
// engine reads, never a way around it (M10).
type GrantScope struct {
	// Name is the scope a policy rule matches on (`grant.scopes`), and so
	// what the grant permits. It is always set.
	Name string
	// Hostnames are exact names, or a single leading wildcard
	// (`*.db.example.com`).
	Hostnames []string
	// Labels must all be present on a target with these values.
	Labels map[string]string
	// Zones are fleet zones (M6).
	Zones []string
}

// GrantRequest asks for time-boxed access.
type GrantRequest struct {
	// Tenant is required.
	Tenant Tenant
	// RequestID is Control's identifier for this request. It is stable, and
	// submitting it twice must produce one workflow rather than two.
	RequestID string
	// Subject is who would hold the grant.
	Subject Subject
	// Scope is what the grant would cover, and it is the authoritative
	// statement of the request's reach.
	Scope GrantScope
	// Targets are the inventory records for the hostnames Scope names
	// exactly, resolved so an approver can see a target's zone and labels.
	// They never widen or narrow Scope: a wildcard, a label or a zone
	// selects targets that are not listed here, and an empty list means
	// only that Scope named no host Control holds a record of.
	Targets []Target
	// Privileges are the stable codes for what it would permit. Control
	// sends Scope.Name, the one privilege a grant carries.
	Privileges []string
	// Window is the access period asked for. A workflow may approve a
	// shorter one and may never approve a longer one.
	Window Window
	// ReasonCode is why, as a stable code; ReasonText is the requester's own
	// words, which are shown to an approver and stored, and are not a code.
	ReasonCode string
	ReasonText string
	// RequestedBy is the subject that submitted the request, which is not
	// always Subject: an operator may request on someone's behalf.
	RequestedBy Subject
	// RequestedAt is when Control received it.
	RequestedAt time.Time
	// ExternalRef ties the request to something outside Hoplock — a change
	// ticket, an incident — when the requester supplied one.
	ExternalRef string
}

// GrantApproval records one approver's answer, so that "N of M approved" is
// evidence rather than an assertion.
type GrantApproval struct {
	// Approver is who answered.
	Approver Subject
	// Approved is their answer.
	Approved bool
	// At is when they gave it.
	At time.Time
	// ReasonText is their own words, if any.
	ReasonText string
}

// GrantDecision is a workflow's answer about a request.
type GrantDecision struct {
	// WorkflowRef is the workflow's own identifier for the request, which
	// Control stores and passes back to Status and Cancel.
	WorkflowRef string
	// Outcome is the decision.
	Outcome GrantOutcome
	// Window is the approved period. It must be inside the requested window;
	// Control clamps it if it is not, and records that it did.
	Window Window
	// Approvals is the evidence behind the outcome.
	Approvals []GrantApproval
	// ReasonCode is why, as a stable code.
	ReasonCode string
	// ReasonText is an operator-facing diagnostic, untranslated.
	ReasonText string
}

// GrantWorkflow decides who may hold a time-boxed grant and who must approve
// it. What varies is an organisation's governance: who is allowed to ask, how
// many people must agree, whether a change window has to be open, what happens
// out of hours. None of that is access control — the grant is the same object
// either way — which is precisely why it is a seam and not a fork of the grant
// model.
//
// When no implementation is registered, an authorised administrator creates a
// time-boxed grant directly. That is a working just-in-time access story on its
// own: the grant expires on a clock rather than on a sweeper, it is revocable,
// and the decision record names it. A workflow inserts approval in front of
// creation; it does not become the only way a grant can exist.
//
// A grant produced through a workflow and a grant produced by an administrator
// are the same kind of object, and the policy engine has no branch on which
// path made it (M10). Anything a workflow wants remembered about how the grant
// came to be travels in the grant's origin and external reference, not in a
// parallel path into the decision.
//
// What Control does with each answer (phase 0012):
//
//   - GrantApproved: the grant is created with origin `workflow`, naming the
//     request, WorkflowRef and every approver whose answer was yes. A zero
//     end of Window means "as requested"; a Window reaching outside the
//     request is clamped to it, and the request records that it was.
//   - GrantDenied or GrantExpired, or a KindDenied error: no grant is
//     created. The request is closed with that outcome and ReasonCode, the
//     closure is audited, and the requester is told. Control never falls back
//     to creating the grant directly: while a workflow is registered, an
//     administrator's grant goes through it.
//   - GrantPending: the request stays open and Control polls Status. A
//     request still pending when the window it asked for closes is cancelled
//     and closed as expired, because approving it could no longer grant
//     anything.
//   - KindUnavailable or an unclassified error from Submit is an outage: the
//     request stays open with no WorkflowRef and Control submits it again,
//     under the same RequestID, until it is answered or its window closes.
//     That is what the idempotency below is for. Any other error kind closes
//     the request as failed, because resubmitting it would get the same
//     answer.
type GrantWorkflow interface {
	// Submit puts a request to the workflow. It may answer immediately or
	// return GrantPending. It must be idempotent on GrantRequest.RequestID.
	Submit(ctx context.Context, req GrantRequest) (GrantDecision, error)
	// Status re-reads a decision by its WorkflowRef. Control polls this
	// while an outcome is pending; an unknown reference is an ErrNotFound
	// *Error.
	Status(ctx context.Context, tenant Tenant, workflowRef string) (GrantDecision, error)
	// Cancel withdraws a pending request. Cancelling one that is already
	// decided is not an error — the outcome simply stands.
	Cancel(ctx context.Context, tenant Tenant, workflowRef, reasonText string) error
}
