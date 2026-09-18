// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"context"
	"time"
)

// Notification is something an operator should be told about. It carries codes
// and structured attributes rather than a rendered message: a Slack
// integration and an on-call pager format the same event differently, and the
// console holds the only string catalogue in the product (M21). An
// implementation renders from Kind and Attributes.
type Notification struct {
	// Tenant is required.
	Tenant Tenant
	// Kind is the event's stable code — "grant.requested",
	// "policy.activated", "fleet.proxy.unhealthy".
	Kind string
	// Severity grades how much to care.
	Severity Severity
	// SubjectID, TargetID, SessionID and DecisionID tie the notification to
	// the rest of the story; any may be empty.
	SubjectID  string
	TargetID   string
	SessionID  string
	DecisionID string
	// Attributes are the event's structured fields.
	Attributes map[string]string
	// OccurredAt is when the thing happened.
	OccurredAt time.Time
	// Link is a deep link into this deployment's console for the event, when
	// the deployment knows its own external URL. It is a convenience for the
	// person who receives the notification, and may be empty.
	Link string
	// DedupeKey identifies the underlying thing, so a channel that
	// suppresses repeats can. Two notifications about one grant request
	// share it.
	DedupeKey string
}

// Notifier delivers an operator-facing notification. What varies is where an
// organisation's people actually are — Slack, Teams, email, an on-call rotation
// with an escalation policy — and none of that is access control.
//
// When no implementation is registered, notifications are delivered to the
// webhook the deployment configured, and nowhere else. A webhook is a real
// answer rather than a stub: it is what a self-hosting deployment points at its
// own automation, and everything Control has to say is available through it.
// A notifier adds a channel; Control's webhook keeps working alongside it.
//
// Notify must not block on a human. It hands the notification to a channel and
// returns; a notifier that waits for an acknowledgement is holding up whatever
// produced the event. Delivery is best-effort and never gates access: a
// notification that could not be sent is an error to log and retry, never a
// reason to deny (M11).
type Notifier interface {
	// Notify delivers one notification. The context carries Control's
	// deadline for the attempt.
	Notify(ctx context.Context, n Notification) error
}

// ActionKind is what an external system is asking Control to do. It is a
// closed enum because it is a list of privileges: an action Control cannot
// name is an action nobody can be granted.
type ActionKind int

const (
	// ActionUnspecified is the zero value and is never a valid action. A
	// zero-valued Action must not default to killing a session: the safe
	// direction here is to reject what was not asked for explicitly.
	ActionUnspecified ActionKind = iota
	// ActionKillSession ends a running session. The user is told, because
	// access ending must never look like a crash.
	ActionKillSession
	// ActionLockSubject stops a subject from obtaining new access, and ends
	// what they currently hold.
	ActionLockSubject
	// ActionUnlockSubject reverses ActionLockSubject.
	ActionUnlockSubject
	// ActionRevokeGrant ends one grant before its window closes.
	ActionRevokeGrant
	// ActionQuarantineTarget stops new access to one target.
	ActionQuarantineTarget
)

// String renders the action as the stable code that crosses the wire.
func (k ActionKind) String() string {
	switch k {
	case ActionUnspecified:
		return "unspecified"
	case ActionKillSession:
		return "kill_session"
	case ActionLockSubject:
		return "lock_subject"
	case ActionUnlockSubject:
		return "unlock_subject"
	case ActionRevokeGrant:
		return "revoke_grant"
	case ActionQuarantineTarget:
		return "quarantine_target"
	}
	return "unknown"
}

// Action is one inbound request to act on this deployment.
type Action struct {
	// Tenant is required.
	Tenant Tenant
	// Kind is what to do.
	Kind ActionKind
	// ActionID is the requester's identifier for this action. Executing it
	// twice must have the effect of executing it once.
	ActionID string
	// SubjectID, SessionID, TargetID and GrantID name what to act on; which
	// of them is required depends on Kind.
	SubjectID string
	SessionID string
	TargetID  string
	GrantID   string
	// ReasonCode is why, as a stable code; ReasonText is what the user is
	// told when the action ends their session, and is the one piece of prose
	// here that reaches a person directly.
	ReasonCode string
	ReasonText string
	// ExternalRef ties the action to the incident or detection that caused
	// it, and is recorded in the audit trail.
	ExternalRef string
	// RequestedAt is when the external system asked.
	RequestedAt time.Time
}

// ActionResult is what executing an action did.
type ActionResult struct {
	// ActionID echoes the request.
	ActionID string
	// Affected counts what the action actually changed — sessions killed,
	// grants revoked. Zero with no error means there was nothing to act on,
	// which is a legitimate outcome and not a failure.
	Affected int
	// AppliedAt is when Control did it.
	AppliedAt time.Time
	// ReasonCode explains a refusal or a partial application.
	ReasonCode string
}

// ActionExecutor is Control's side of the inbound action channel: the closed
// set of things an external system may ask this deployment to do. It is passed
// to ActionHandler.Start, so the privilege is handed over explicitly rather
// than assumed, and every execution is audited with the action's external
// reference attached.
type ActionExecutor interface {
	// Execute performs one action. It is idempotent on Action.ActionID.
	Execute(ctx context.Context, a Action) (ActionResult, error)
}

// ActionHandler is the inbound channel an external system uses to act on this
// deployment: a SOAR platform that has decided a session is hostile and wants
// it killed, or a detection that wants a subject locked out. What varies is the
// platform and its transport — a queue, a signed webhook, a vendor SDK — and
// each carries its own authentication, which is precisely why it is an
// extension and not an endpoint Control exposes to everyone.
//
// The direction is inverted for the same reason IdentitySync's is: the handler
// owns the listener and Control owns the effect. Control passes an
// ActionExecutor to Start, and that executor is the complete list of what an
// inbound action can do.
//
// When no implementation is registered, no external system can ask this
// deployment to kill a session or lock a subject out. Nothing is lost: an
// operator can do all of it through the north-bound API and the console, and
// revocation still fans out to the fleet (M9). What a handler adds is somebody
// else's automation being able to pull the same lever — a capability Control
// never had rather than one taken out of it.
//
// Start is called once, after the registry is sealed and before the listeners
// open, and must return promptly; a handler that needs to keep running starts
// its own goroutines and stops them on Stop.
type ActionHandler interface {
	// Start begins accepting inbound actions, executing them through exec.
	Start(ctx context.Context, exec ActionExecutor) error
	// Stop ends it. It is called on shutdown and must be safe to call
	// without a preceding successful Start.
	Stop(ctx context.Context) error
}
