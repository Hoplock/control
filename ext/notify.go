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
	// ActionKillSession ends a running session, or every session of one
	// subject. EXACTLY ONE of Action.SessionID or Action.SubjectID names
	// which: both, or neither, is KindInvalid. The user is told, because
	// access ending must never look like a crash.
	ActionKillSession
	// ActionLockSubject stops a subject from obtaining new access, and ends
	// what they currently hold. While the lock stands every authorize call
	// for the subject is a deny, whatever policy, grants or windows say; it
	// stands until ActionUnlockSubject or an operator lifts it. Control never
	// expires a lock: a requester that means its lock to end at a time
	// executes ActionUnlockSubject then, with Action.IfPlacedBy set to the
	// ActionID that placed it, so the expiry cannot lift a lock somebody else
	// placed meanwhile. A break-glass subject (M7) is refused with
	// KindDenied: the break-glass path exists so that access survives the
	// failure of everything else, and an operator, acting deliberately, can
	// still lock it.
	ActionLockSubject
	// ActionUnlockSubject reverses ActionLockSubject. Without
	// Action.IfPlacedBy it lifts whatever lock stands on the subject, an
	// operator's included. With it, it lifts the lock only if that action of
	// this handler placed it, and leaves any other standing (`not_this_lock`):
	// that is the form an expiry uses.
	ActionUnlockSubject
	// ActionRevokeGrant ends one grant before its window closes, and the
	// sessions it backed. A grant already revoked or expired is nothing to
	// act on.
	ActionRevokeGrant
	// ActionQuarantineTarget stops new access to one target: while the
	// quarantine stands no authorize call for it is allowed, whatever policy
	// says. Sessions already running to it are not ended. Lifting a
	// quarantine is an operator's act only; there is no kind for it.
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
	// SubjectID, SessionID, TargetID and GrantID name what to act on. Each
	// Kind takes exactly the selector it names and no other; one it does not
	// take is KindInvalid, naming the field.
	SubjectID string
	SessionID string
	TargetID  string
	GrantID   string
	// Within confines the action to what its requester may touch. When it is
	// non-nil, Control resolves what the action reaches from its own data,
	// inside the transaction that performs it, and refuses an action that
	// reaches outside: KindDenied, ActionResult.ReasonCode `out_of_scope`,
	// nothing executed, recorded as `action.refused`. ActionScope says what
	// each kind reaches. Nil confines nothing, which is the whole tenant.
	//
	// A non-nil scope with every field empty is KindInvalid on `within`: it is
	// far more likely a credential whose scope failed to load than a request
	// for the whole tenant, which is nil.
	//
	// Within is who may ask, not what is asked, so it is not part of the
	// action's identity: a repeat of an ActionID is checked against the scope
	// it carries, before the first result is returned and before anything is
	// sent again, and a repeat under a scope that does not admit what the
	// action reaches is refused.
	Within *ActionScope
	// IfPlacedBy is for ActionUnlockSubject only, and makes the unlock
	// conditional: the lock is lifted only if the ActionID that placed it
	// equals this one AND it was placed by this handler (the principal
	// `action-handler:<provider>`). A lock placed by an operator, by another
	// action, or under a provider name since changed is left standing:
	// ActionResult.Affected 0, ReasonCode `not_this_lock`, nil error. With no
	// lock standing it is `not_locked`, as without it.
	//
	// It is held to the bounds of ActionID. With any other kind it is
	// KindInvalid on `if_placed_by`. Unlike Within it is part of the action's
	// identity: the same ActionID with a different IfPlacedBy, or with one
	// where the first had none, is KindConflict.
	IfPlacedBy string
	// ReasonCode is why, as a stable code; ReasonText is what the user is
	// told when the action ends their session, and is the one piece of prose
	// here that reaches a person directly.
	//
	// ReasonText is displayed verbatim in a terminal as the connection
	// closes, so it must be one line with no control characters and at most
	// 512 bytes. Text that is not is KindInvalid on `reason_text`: Control
	// never truncates it or replaces it with its own. When it is empty,
	// Control writes a fixed reason for the kind: one for a killed session,
	// one for a lock and one for a revoked grant. It is not shown for an
	// action that ends nothing.
	ReasonCode string
	ReasonText string
	// ExternalRef ties the action to the incident or detection that caused
	// it, and is recorded in the audit trail.
	ExternalRef string
	// RequestedAt is when the external system asked.
	RequestedAt time.Time
}

// ActionScope is what an action's requester may touch: the confinement a
// handler hands Control in Action.Within, typically the scope of the
// credential the inbound request arrived with. Each field that is set must
// hold for everything the action reaches; an empty field confines nothing,
// and a scope with every field empty is KindInvalid.
//
// Control resolves what an action reaches from its own data, inside the
// transaction that performs it, so a relabel or a re-scope committed
// meanwhile either waits for the act or is seen by the check:
//
//	Kind                        Subjects it reaches          Targets it reaches
//	kill_session by SessionID   every subject on the         every target on them, every
//	                            session's decision records   hop of a chain included
//	kill_session by SubjectID   that subject                 all of them
//	lock_subject,               that subject                 all of them (a lock denies
//	unlock_subject                                           every target)
//	revoke_grant                the grant's subject          the grant's selector (below)
//	quarantine_target           all of them (a quarantine    that target
//	                            denies it to every subject)
//
// A dimension a kind reaches whole fails a scope that confines it: a scope
// that sets TargetIDs or TargetLabels refuses a lock, an unlock and a kill by
// subject, and a scope that sets SubjectIDs refuses a quarantine. A
// credential confined to some targets must not be able to lock a person out
// of every other, nor one confined to some people deny a target to everyone
// else.
//
// A grant's selector is inside a target scope only when it cannot reach
// outside it. Its parts are ANDed, and an empty selector reaches whatever
// target the matching rule does, so:
//
//   - TargetIDs: the selector must list hostnames, and every one must name a
//     target this tenant holds whose ID is in TargetIDs. A hostname with no
//     target row, or a wildcard (`*.example.com`), can reach a target that
//     is not, and is outside.
//   - TargetLabels: for each required label k=v, either the selector's own
//     labels require k=v, or it lists hostnames and every one names a target
//     this tenant holds that carries k=v.
//   - A selector of zones alone, or an empty one, is outside every target
//     scope: a zone is not a label.
//
// A confined requester learns nothing outside its scope. The scope is checked
// before Control says whether a selector is known, and a selector Control
// holds nothing for is outside the scope, unless the scope admits it by its
// id alone: a SubjectID in SubjectIDs, or a TargetID in TargetIDs, on a kind
// whose other dimension the scope leaves unconfined. So KindNotFound reaches
// a confined requester only for an id its own scope names, and the error of
// an `out_of_scope` refusal names the scope field that failed, never the
// subject or target that lay outside it. Control's own audit record names
// those.
type ActionScope struct {
	// SubjectIDs: every subject the action reaches is one of these. At most
	// 1 000, each held to the bounds of an ActionID.
	SubjectIDs []string
	// TargetIDs: every target the action reaches is one of these (Target.ID).
	// At most 1 000, each held to the bounds of an ActionID.
	TargetIDs []string
	// TargetLabels: every target the action reaches carries each of these
	// labels with this value (Target.Labels). At most 64, each key and value
	// non-blank, one line and at most 256 bytes.
	TargetLabels map[string]string
}

// ActionResult is what executing an action did.
type ActionResult struct {
	// ActionID echoes the request.
	ActionID string
	// Affected counts what the action actually changed, and what it counts
	// depends on the kind:
	//
	//   - ActionKillSession: kills Control published, which is one for the
	//     session or the subject named. It is never a count of sessions seen
	//     to close: Control publishes the kill and cannot see the proxies
	//     close anything.
	//   - ActionLockSubject, ActionQuarantineTarget: one when this call
	//     placed the denial.
	//   - ActionUnlockSubject: one when this call lifted the lock.
	//   - ActionRevokeGrant: one when this call revoked the grant.
	//
	// Zero with no error means there was nothing to act on (already locked,
	// not locked, a lock this action did not place, already quarantined, a
	// grant already revoked or expired), which is a legitimate outcome and not
	// a failure; ReasonCode then says which. A repeat of an action returns the
	// first execution's result.
	Affected int
	// AppliedAt is when Control did it.
	AppliedAt time.Time
	// ReasonCode explains a refusal, a partial application or a nothing to
	// act on: `already_locked`, `not_locked`, `not_this_lock` (an unlock with
	// Action.IfPlacedBy found a lock its action did not place, and left it
	// standing), `already_quarantined`, `already_revoked`, `expired`, or,
	// with KindDenied, `break_glass_subject` or `out_of_scope`.
	ReasonCode string
}

// ActionExecutor is Control's side of the inbound action channel: the closed
// set of things an external system may ask this deployment to do. It is passed
// to ActionHandler.Start, so the privilege is handed over explicitly rather
// than assumed, and every execution is audited with the action's external
// reference and the handler's provider attached.
//
// Each kind goes through the service an operator's north-bound route calls, so
// the handler can pull no lever an operator cannot, and the handler is the
// actor on every record: the principal `action-handler:<provider>`, with no
// subject.
type ActionExecutor interface {
	// Execute performs one action. It is idempotent per tenant on
	// Action.ActionID: the same id with the same action returns the first
	// execution's result and changes nothing, and the same id with a
	// different action is KindConflict. A silent no-op is never the answer.
	//
	// The error says what to do next:
	//
	//   - KindInvalid: the action is malformed; the error names the field
	//     (`within`, `within.subject_ids`, `within.target_ids`,
	//     `within.target_labels` and `if_placed_by` among them). Do not
	//     retry it unchanged.
	//   - KindNotFound: the subject, session, grant or target is not known in
	//     Action.Tenant. An id that exists only in another tenant is not
	//     found. A confined action (Action.Within) gets it only for an id its
	//     scope names; any other unknown selector is `out_of_scope`.
	//   - KindDenied: Control refused on purpose; ActionResult.ReasonCode says
	//     why: `break_glass_subject` or `out_of_scope`. This is the answer
	//     rather than the symptom, and a retry gets the same one.
	//   - KindConflict: Action.ActionID was used for a different action,
	//     which includes an unlock with a different Action.IfPlacedBy.
	//   - KindUnavailable: the state change was made and stands, and the
	//     event stream refused the publication (or Control is stopping). It is
	//     safe to retry under the same ActionID, and the retry delivers.
	//   - KindInternal: a store failure. Nothing is known to have happened.
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
// operator can do all of it through the north-bound API and the console —
// kill a session or every session of a subject, lock and unlock a subject,
// revoke a grant, quarantine and release a target — and revocation still fans
// out to the fleet (M9). What a handler adds is somebody else's automation
// being able to pull the same lever — a capability Control never had rather
// than one taken out of it.
//
// Start is called once, after the registry is sealed and before the listeners
// open, and must return promptly; a handler that needs to keep running starts
// its own goroutines and stops them on Stop.
//
// A Start that returns an error is a broken registration, and Control does
// not stop for it: access must not depend on somebody else's automation. The
// error is logged at ERROR with the provider and its kind, nothing is
// executed, and `GET …/extensions` shows the handler as registered and not
// started. A handler whose platform is merely unreachable is not a broken
// registration: it returns nil from Start and reconnects on its own
// goroutines, because Control will not retry a Start.
type ActionHandler interface {
	// Start begins accepting inbound actions, executing them through exec.
	Start(ctx context.Context, exec ActionExecutor) error
	// Stop ends it. It is called on shutdown, before the listeners drain and
	// bounded by the shutdown deadline, and must be safe to call without a
	// preceding successful Start. An Execute that arrives once Stop has begun
	// is KindUnavailable.
	Stop(ctx context.Context) error
}
