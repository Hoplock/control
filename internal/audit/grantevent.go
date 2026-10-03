// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// This server's records of acts on a grant (M10, 0012): a grant created,
// revoked, requested of a workflow, or a request closed without one.
//
// They are `kind: policy_decision` — the contract's kind enum is closed (M1),
// and a grant is a policy input, so creating or withdrawing one is a decision
// about policy's inputs taken by a person — on this server's own chain
// ([StreamControl]), and the event name says which act.
//
// THEY ARE WRITTEN INSIDE THE ACT'S TRANSACTION. The grant row and the record of
// who created it commit together, so there is no grant nobody can account for
// and no account of a grant that was never created.

// Attribute keys a grant record carries. None of them is one of the grant
// CONTEXT keys (`grant_system`, `grant_reference`, ...): those index what a
// proxy copied onto a session's records from an external assertion (M16), and
// an administrative record filed under them would answer "which sessions did
// this ticket authorise" with an act that is not a session.
const (
	AttrGrantID          = "grant_id"
	AttrGrantScope       = "grant_scope"
	AttrGrantOrigin      = "grant_origin"
	AttrGrantNotBefore   = "grant_not_before"
	AttrGrantExpiresAt   = "grant_expires_at"
	AttrGrantTargets     = "grant_targets"
	AttrGrantLabels      = "grant_labels"
	AttrGrantZones       = "grant_zones"
	AttrGrantReason      = "grant_reason"
	AttrGrantReasonCode  = "grant_reason_code"
	AttrGrantExternalRef = "grant_external_ref"
	AttrGrantRequestID   = "grant_request_id"
	AttrGrantWorkflowRef = "grant_workflow_ref"
	AttrGrantApprovers   = "grant_approvers"
	AttrGrantRevoke      = "grant_revoke_reason"
	// AttrGrantSelfGranted marks a grant whose creator is its holder. It is
	// not refused — an administrator may need access too — but an auditor
	// asks about it first, so it is a filter rather than a comparison.
	AttrGrantSelfGranted = "grant_self_granted"

	// The external path (M16, 0013). Prefixed `grant_external_` and never
	// `grant_window_`, for the reason above: the asserted window of the act
	// is not a session's grant context.
	AttrGrantExternalSystem    = "grant_external_system"
	AttrGrantExternalAssertion = "grant_external_assertion_id"
	AttrGrantExternalMode      = "grant_external_mode"
	AttrGrantExternalStart     = "grant_external_window_start"
	AttrGrantExternalEnd       = "grant_external_window_end"
	// AttrGrantExternalClamped marks a push that asked for a longer window
	// than its ceiling and was given the ceiling. Clamped, not refused — and
	// the record is where an auditor finds that it happened.
	AttrGrantExternalClamped = "grant_external_clamped"
	AttrGrantExternalCeiling = "grant_external_ceiling_seconds"

	AttrRequestState   = "grant_request_state"
	AttrRequestOutcome = "grant_request_outcome"
	AttrRequestDetail  = "grant_request_outcome_text"
	AttrRequestClamped = "grant_request_window_clamped"

	// AttrActorSubject is the person who acted; AttrPrincipalID (above) is
	// the credential they acted through, and AttrBreakGlass whether it was
	// a break-glass one.
	AttrActorSubject = "actor_subject"
	// AttrWorkflow names the registered workflow whose decision a record
	// applies.
	AttrWorkflow = "workflow"
)

var _ access.Recorder = (*Emitter)(nil)

// GrantEvent records one act on a grant inside tx, the transaction that
// performs it. It implements access.Recorder. A nil tx writes on its own, for
// a caller with no transaction to share — which is never an act on a grant
// row, where the two must commit together.
func (e *Emitter) GrantEvent(ctx context.Context, tx *store.Store, tenant store.Tenant, ev access.Event) error {
	if tenant == "" {
		return fmt.Errorf("audit: a grant record needs a tenant")
	}
	if ev.Grant == nil && ev.Request == nil {
		return fmt.Errorf("audit: a grant record names a grant or a request")
	}

	recordID, err := newRecordID()
	if err != nil {
		return err
	}

	attrs := map[string]string{AttrEvent: ev.Name}
	subject, target := "", ""
	if r := ev.Request; r != nil {
		subject = r.SubjectID
		target = soleHost(r.ScopeTargets)
		attrs[AttrGrantRequestID] = r.ID
		attrs[AttrRequestState] = string(r.State)
		attrs[AttrGrantScope] = r.Scope
		attrs[AttrGrantNotBefore] = stamp(r.NotBefore)
		attrs[AttrGrantExpiresAt] = stamp(r.ExpiresAt)
		putList(attrs, AttrGrantTargets, r.ScopeTargets)
		putLabels(attrs, r.ScopeLabels)
		putList(attrs, AttrGrantZones, r.ScopeZones)
		attrs[AttrGrantReason] = r.Reason
		put(attrs, AttrGrantReasonCode, r.ReasonCode)
		put(attrs, AttrGrantExternalRef, r.ExternalRef)
		put(attrs, AttrGrantWorkflowRef, r.WorkflowRef)
		put(attrs, AttrRequestOutcome, r.OutcomeCode)
		put(attrs, AttrRequestDetail, r.OutcomeText)
		if r.WindowClamped {
			attrs[AttrRequestClamped] = "true"
		}
	}
	if g := ev.Grant; g != nil {
		// The grant outranks its request for the fields they share: it is
		// what now exists, with the window it actually has.
		subject = g.SubjectID
		target = soleHost(g.ScopeTargets)
		attrs[AttrGrantID] = g.ID
		attrs[AttrGrantScope] = g.Scope
		attrs[AttrGrantOrigin] = string(access.PolicyOrigin(g.Origin))
		attrs[AttrGrantNotBefore] = stamp(g.NotBefore)
		attrs[AttrGrantExpiresAt] = stamp(g.ExpiresAt)
		putList(attrs, AttrGrantTargets, g.ScopeTargets)
		putLabels(attrs, g.ScopeLabels)
		putList(attrs, AttrGrantZones, g.ScopeZones)
		attrs[AttrGrantReason] = g.Reason
		put(attrs, AttrGrantReasonCode, g.ReasonCode)
		put(attrs, AttrGrantExternalRef, g.ExternalRef)
		put(attrs, AttrGrantRequestID, g.RequestID)
		put(attrs, AttrGrantWorkflowRef, g.ApprovalRef)
		putList(attrs, AttrGrantApprovers, g.Approvers)
		if ev.Name == access.EventGrantRevoked {
			attrs[AttrGrantRevoke] = g.RevokeReason
		}
		if g.CreatedBy.Subject != "" && g.CreatedBy.Subject == g.SubjectID {
			attrs[AttrGrantSelfGranted] = "true"
		}
		put(attrs, AttrGrantExternalSystem, g.External.System)
		put(attrs, AttrGrantExternalAssertion, g.External.AssertionID)
		put(attrs, AttrGrantExternalMode, string(g.External.Mode))
		if !g.External.WindowStart.IsZero() {
			attrs[AttrGrantExternalStart] = stamp(g.External.WindowStart)
		}
		if !g.External.WindowEnd.IsZero() {
			attrs[AttrGrantExternalEnd] = stamp(g.External.WindowEnd)
		}
	}
	if x := ev.External; x != nil {
		attrs[AttrGrantExternalClamped] = strconv.FormatBool(x.Clamped)
		attrs[AttrGrantExternalCeiling] = strconv.FormatInt(int64(x.Ceiling/time.Second), 10)
	}

	actor := ev.Actor
	attrs[AttrBreakGlass] = strconv.FormatBool(actor.BreakGlass)
	put(attrs, AttrPrincipalID, actor.Principal)
	put(attrs, AttrActorSubject, actor.Subject)
	put(attrs, AttrCorrelationID, actor.CorrelationID)
	put(attrs, AttrWorkflow, ev.Workflow)

	at := ev.At
	if at.IsZero() {
		at = e.now()
	}

	// The session id is the credential the act came through, as it is for a
	// login: "what else did this operator do in that session" is the next
	// question. An act a workflow's decision drove has no credential, so it
	// is filed under the request or the grant it concerns.
	sessionID := firstNonEmptyOf(actor.Principal, actor.CorrelationID,
		attrs[AttrGrantRequestID], attrs[AttrGrantID], recordID)

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
			SessionID:  sessionID,
			Timestamp:  at.UTC().Format(time.RFC3339Nano),
			Kind:       "policy_decision",
			Severity:   grantSeverity(ev),
			Message:    grantMessage(ev),
			Subject:    subject,
			Target:     target,
			Attributes: attrs,
		}},
	})
	return err
}

// grantSeverity grades an act for the reader who filters by severity.
//
// Widening access by hand is worth a look, and so is ending it mid-session; a
// request or its closure is routine. A break-glass credential doing any of it
// is not routine, and a record filed below critical is one nobody's alerting
// sees.
func grantSeverity(ev access.Event) contract.LogSeverity {
	switch {
	case ev.Actor.BreakGlass:
		return contract.SeverityCritical
	case ev.Name == access.EventGrantCreated, ev.Name == access.EventGrantRevoked:
		return contract.SeverityWarn
	default:
		return contract.SeverityInfo
	}
}

// grantMessage is the line an operator reads in a record list. It names the
// act and the holder; everything else is an attribute.
func grantMessage(ev access.Event) string {
	switch ev.Name {
	case access.EventGrantCreated:
		if ev.Workflow != "" {
			return "grant created for " + holder(ev) + " on approval"
		}
		if ev.Grant != nil && ev.Grant.External.Mode != "" {
			return "grant created for " + holder(ev) + " on a window " + ev.Grant.External.System + " asserted"
		}
		return "grant created for " + holder(ev)
	case access.EventGrantRevoked:
		return "grant revoked for " + holder(ev)
	case access.EventGrantRequested:
		return "grant requested for " + holder(ev)
	case access.EventGrantRequestClosed:
		state := "closed"
		if ev.Request != nil {
			state = string(ev.Request.State)
		}
		return "grant request " + state + " for " + holder(ev)
	}
	return ev.Name
}

func holder(ev access.Event) string {
	if ev.Grant != nil {
		return ev.Grant.SubjectID
	}
	if ev.Request != nil {
		return ev.Request.SubjectID
	}
	return "an unnamed subject"
}

// soleHost names the record's target when the scope names exactly one host
// exactly. A wildcard, or several, is not a target, and a record claiming one
// would appear in every per-target query as if it were about that host alone.
func soleHost(targets []string) string {
	if len(targets) != 1 || strings.HasPrefix(targets[0], "*.") {
		return ""
	}
	return targets[0]
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func put(attrs map[string]string, key, value string) {
	if value != "" {
		attrs[key] = value
	}
}

func putList(attrs map[string]string, key string, values []string) {
	if len(values) > 0 {
		attrs[key] = strings.Join(values, ",")
	}
}

// putLabels writes a label selector as sorted `k=v` pairs, one attribute, so
// "every grant on env=prod" is a substring of one field.
func putLabels(attrs map[string]string, labels map[string]string) {
	putLabelsAs(attrs, AttrGrantLabels, labels)
}

// putLabelsAs writes labels as sorted `k=v` pairs under key.
func putLabelsAs(attrs map[string]string, key string, labels map[string]string) {
	if len(labels) == 0 {
		return
	}
	pairs := make([]string, 0, len(labels))
	for k, v := range labels {
		pairs = append(pairs, k+"="+v)
	}
	slices.Sort(pairs)
	attrs[key] = strings.Join(pairs, ",")
}
