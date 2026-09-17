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

// GrantRequest asks for time-boxed access.
type GrantRequest struct {
	// Tenant is required.
	Tenant Tenant
	// RequestID is Control's identifier for this request. It is stable, and
	// submitting it twice must produce one workflow rather than two.
	RequestID string
	// Subject is who would hold the grant.
	Subject Subject
	// Targets are what it would reach. Empty means every target the
	// subject's policy already allows, narrowed by Privileges.
	Targets []Target
	// Privileges are the stable codes for what it would permit.
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

// AccessContextQuery asks an external system whether an access is legitimate
// right now. It names the access rather than asking a general question,
// because a provider that has to be told the whole request to answer is a
// provider that has been handed the policy decision.
type AccessContextQuery struct {
	// Tenant is required.
	Tenant Tenant
	// Subject is who is asking.
	Subject Subject
	// Target is what they are reaching.
	Target Target
	// Privileges are the stable codes for what they are asking to do.
	Privileges []string
	// ExternalRef is the reference the request already carries — a change
	// ticket id, an incident id — when there is one. Empty means the
	// provider must decide from the subject and target alone.
	ExternalRef string
	// At is the instant the question is being asked about, supplied rather
	// than read from a clock so that simulation over historical inputs is
	// total (M4).
	At time.Time
}

// AccessEvidence is what an external system says about an access — and it is
// deliberately not a verdict.
//
// The first instinct of anyone implementing AccessContextProvider is to return
// a boolean, and that quietly relocates the policy engine into a vendor
// integration: the rule "a privileged session needs an approved change window"
// stops being visible in the policy bundle, stops being simulated, and stops
// being explainable, because the only thing that survives to the decision
// record is somebody else's yes. So a provider reports what the external
// system asserted — a ticket is approved and runs until 18:00, a scan is in
// progress, an incident is open — and policy decides what that is worth.
type AccessEvidence struct {
	// Reference is the external system's identifier for what it asserted:
	// the ticket, the scan, the incident. It is recorded on the grant and
	// shown by explain (0014), so it must be the identifier a human can look
	// up on the other side.
	Reference string
	// Assertions are the fields the policy engine may read, as stable codes
	// and values. A provider adds to this rather than collapsing it: the
	// richer it is, the less a rule has to assume.
	Assertions map[string]string
	// Window is the period the external system asserted. Control applies its
	// own ceiling to it regardless of what is claimed (M16), and an empty
	// NotAfter is not "forever".
	Window Window
	// ObservedAt is when the provider learned this. A cached answer carries
	// the instant it was fetched, not the instant it was replayed, so that a
	// stale probe is visible as stale rather than as fresh.
	ObservedAt time.Time
	// Cached reports that this answer came from the provider's cache rather
	// than from a live call.
	Cached bool
}

// AccessContextProvider supplies evidence from a system outside Hoplock about
// whether an access is legitimate right now (M16): a vulnerability scan is
// running against this host, a change ticket is approved and inside its window,
// an incident is open. What varies is which system an organisation keeps that
// truth in, and there is no end to that list — which is why the seam exists and
// why Control's own default is configured rather than coded.
//
// It returns evidence, not a verdict. See AccessEvidence.
//
// When no implementation is registered, no external access context is consulted
// and no window is opened by one. Everything else about the decision is
// unchanged: policy still evaluates, grants still apply, and a deployment that
// integrates nothing is not missing a feature it was promised.
//
// Control itself ships the declarative HTTP provider (phase 0013) — a probe
// URL, its authentication, a request template, assertions over the response, a
// cache TTL, and a webhook field mapping. That is not a placeholder standing in
// for the real thing: it is how a self-hosting customer integrates a scanner or
// an ITSM system nobody has heard of, without writing Go. Packaged
// vendor-specific integrations are Hoplock Enterprise's, and what they add is
// packaging and support rather than capability (M15).
//
// Probe runs on the authorize path, inside the latency budget M5 governs. The
// context carries the deadline for the provider's share of it; a provider that
// exceeds it is cancelled, and the result classifies as an outage rather than
// as a denial (M11). Report an unreachable dependency as KindUnavailable and an
// answer that could not be understood as KindMalformed — the decision record
// has to tell those apart, because one is a network and the other is a bug.
// A provider with nothing to say about this access returns ErrNoEvidence, which
// is neither.
type AccessContextProvider interface {
	// Probe asks about one access.
	Probe(ctx context.Context, q AccessContextQuery) (AccessEvidence, error)
}
