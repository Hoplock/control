// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// This server's records of what an operator did through the north-bound surface
// (0019): a bundle activated, a target relabelled, a proxy enrolled, a session
// killed, a binding written, a configuration published.
//
// AN AUDIT SYSTEM WHOSE OWN ADMINISTRATION IS UNAUDITED HAS A HOLE EXACTLY WHERE
// IT MATTERS. So every mutating act records who did it, what it was, when, and
// the before and after versions where the act moves one. They are
// `kind: policy_decision` on this server's own chain ([StreamControl]), like a
// grant's (grantevent.go), and the event name says which act. They belong to
// no session — `session_id: ""` is a record of no session on every kind
// (Hoplock/proxy#71) — and are filed by the credential that acted instead.

// The acts, by event name. They are `operator.<noun>.<verb>` so that no act can
// collide with a name a proxy emits (`target.*`, `device.*`) and so that "every
// administrative act in this tenant" is one prefix.
const (
	EventPolicyUploaded        = "operator.policy.uploaded"
	EventPolicyActivated       = "operator.policy.activated"
	EventTargetPut             = "operator.target.put"
	EventTargetDeleted         = "operator.target.deleted"
	EventTargetsRelabelled     = "operator.targets.relabelled"
	EventSubjectPut            = "operator.subject.put"
	EventSubjectDeleted        = "operator.subject.deleted"
	EventRoleBound             = "operator.role.bound"
	EventRoleUnbound           = "operator.role.unbound"
	EventClaimMappingActivated = "operator.claim_mapping.activated"
	EventProxyApproved         = "operator.proxy.approved"
	EventProxyWithdrawn        = "operator.proxy.withdrawn"
	EventFleetConfigPublished  = "operator.fleet_config.published"
	EventFleetConfigRolledBack = "operator.fleet_config.rolled_back"
	EventRevocationPublished   = "operator.revocation.published"
	// The standing denials (0039, M28): a subject locked or unlocked, a target
	// quarantined or released.
	EventSubjectLocked     = "operator.subject.locked"
	EventSubjectUnlocked   = "operator.subject.unlocked"
	EventTargetQuarantined = "operator.target.quarantined"
	EventTargetReleased    = "operator.target.released"
	EventAuditRetained     = "operator.audit.retained"
	// EventAuditRetentionKeyRotated is a new generation of a tenant's
	// retention key: it signs from now on, and older ones still check what
	// they signed.
	EventAuditRetentionKeyRotated = "operator.audit.retention_key_rotated"
)

// EventActionRefused is an inbound action Control refused (0039): malformed,
// aimed at something it has no row for, or refused on purpose. It is not under
// the operator prefix because no operator did it: the registered handler asked,
// and Control said no.
const EventActionRefused = "action.refused"

// EventActionUnchanged is an inbound action Control executed and found nothing
// to act on: a lock already standing, a lock to lift that is not there, a
// grant already revoked. The execution is audited all the same, because every
// execution is; it is not recorded as the lever's own event, because that
// would claim an act that did not happen.
const EventActionUnchanged = "action.unchanged"

// OperatorEventPrefix is what every act's event name starts with.
const OperatorEventPrefix = "operator."

// Attribute keys an inbound action adds to the record of an act it performed,
// or was refused. They are the same on every such record, whichever service
// wrote it, so that "everything this action did" is one filter.
const (
	AttrActionID          = "action_id"
	AttrActionKind        = "action_kind"
	AttrActionExternalRef = "external_ref"
	AttrActionReasonCode  = "reason_code"
	AttrActionProvider    = "provider"
)

// putAction writes the facts of the inbound action behind a record, if any.
func putAction(attrs map[string]string, a *access.ActionRef) {
	if a == nil {
		return
	}
	attrs[AttrActionID] = a.ID
	attrs[AttrActionKind] = a.Kind
	attrs[AttrActionProvider] = a.Provider
	put(attrs, AttrActionExternalRef, a.ExternalRef)
	put(attrs, AttrActionReasonCode, a.ReasonCode)
}

// Attribute keys an operator record carries beside the actor's.
const (
	// AttrBefore and AttrAfter are the versions the act moved between.
	AttrBefore = "before"
	AttrAfter  = "after"
	// AttrAccepted names the findings the operator accepted on a publish:
	// the interpreter warning, the cache-hint warning and the rest of the
	// publish-time report are overridable, and the override is a fact.
	AttrAccepted = "accepted_findings"
	// AttrOperatorPrefix namespaces an act's own fields.
	AttrOperatorPrefix = "operator."
)

// OperatorActor is who performed an act, from the credential and never from
// the request body.
type OperatorActor struct {
	Principal     string
	Subject       string
	BreakGlass    bool
	CorrelationID string
}

// OperatorAct is one act on the north-bound surface.
type OperatorAct struct {
	// Event is `operator.<noun>.<verb>`, e.g. `operator.policy.activated`.
	Event string
	Actor OperatorActor
	// Before and After are the versions the act moved between, where it
	// moves one: a bundle version, a configuration version, a label set.
	Before, After string
	// Subject and Target are what the act concerned, where it concerned
	// one, so the existing subject and target filters find it.
	Subject, Target string
	// Accepted are the findings the operator overrode.
	Accepted []string
	// Fields are the act's own facts, under AttrOperatorPrefix.
	Fields map[string]string
	// Action is the inbound action that performed the act, or was refused,
	// nil for every act a person performed (0039).
	Action *access.ActionRef
	// Message is the sentence a record list shows.
	Message string
	// Severity defaults to warn: an operator changing the estate is worth a
	// look. A break-glass credential is always critical.
	Severity contract.LogSeverity
	At       time.Time
}

// OperatorEvent records one act. With tx it writes inside the act's own
// transaction, so the act and its record commit together; nil writes on its
// own, for an act with no transaction to share — a stream publication.
func (e *Emitter) OperatorEvent(ctx context.Context, tx *store.Store, tenant store.Tenant, act OperatorAct) error {
	if tenant == "" {
		return fmt.Errorf("audit: an operator record needs a tenant")
	}
	if act.Event == "" {
		return fmt.Errorf("audit: an operator record names its act")
	}
	recordID, err := newRecordID()
	if err != nil {
		return err
	}
	attrs := map[string]string{
		AttrEvent:      act.Event,
		AttrBreakGlass: strconv.FormatBool(act.Actor.BreakGlass),
	}
	put(attrs, AttrPrincipalID, act.Actor.Principal)
	put(attrs, AttrActorSubject, act.Actor.Subject)
	put(attrs, AttrCorrelationID, act.Actor.CorrelationID)
	put(attrs, AttrBefore, act.Before)
	put(attrs, AttrAfter, act.After)
	putList(attrs, AttrAccepted, act.Accepted)
	putAction(attrs, act.Action)
	for k, v := range act.Fields {
		put(attrs, AttrOperatorPrefix+k, v)
	}

	severity := act.Severity
	if severity == "" {
		severity = contract.SeverityWarn
	}
	if act.Actor.BreakGlass {
		severity = contract.SeverityCritical
	}
	at := act.At
	if at.IsZero() {
		at = e.now()
	}
	message := act.Message
	if message == "" {
		message = act.Event
	}

	in := e.in
	if tx != nil {
		in = in.within(tx)
	}
	_, err = in.Ingest(ctx, Submission{
		Tenant:   tenant,
		Stream:   StreamControl,
		Priority: true,
		Records: []contract.LogRecord{{
			RecordID:   recordID,
			SessionID:  "",
			Timestamp:  at.UTC().Format(time.RFC3339Nano),
			Kind:       "policy_decision",
			Severity:   severity,
			Message:    message,
			Subject:    act.Subject,
			Target:     act.Target,
			Attributes: attrs,
		}},
	})
	return err
}
