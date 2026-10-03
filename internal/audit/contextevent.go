// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/hoplock/control/internal/accessctx"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// This server's records of external access context (M16, 0013): a push refused
// — filed as an escalation attempt when it reached outside its integration's
// scope — and a scope binding written or removed. An ACCEPTED push is not
// recorded here: it is recorded as the grant it became (GrantEvent), with the
// credential that pushed it, in the grant's own transaction.
//
// Like a grant record, these are `kind: policy_decision` on this server's own
// chain (StreamControl): a binding decides what may ever become a policy input,
// and a refused push is a decision that something did not.

// Attribute keys an external-context record carries. They are prefixed
// `access_context_` and never `grant_`: a refused push produced no grant, and
// the `grant_system`/`grant_reference` keys index a session's grant context.
const (
	AttrContextProvider    = "access_context_provider"
	AttrContextCode        = "access_context_code"
	AttrContextField       = "access_context_field"
	AttrContextDetail      = "access_context_detail"
	AttrContextAssertion   = "access_context_assertion_id"
	AttrContextReference   = "access_context_reference"
	AttrContextScope       = "access_context_scope"
	AttrContextTargets     = "access_context_targets"
	AttrContextWindowStart = "access_context_window_start"
	AttrContextWindowEnd   = "access_context_window_end"
	AttrContextIssuedAt    = "access_context_issued_at"

	AttrBindingMode           = "access_context_binding_mode"
	AttrBindingScope          = "access_context_binding_scope"
	AttrBindingSubjects       = "access_context_binding_subjects"
	AttrBindingSubjectGroups  = "access_context_binding_subject_groups"
	AttrBindingTargets        = "access_context_binding_targets"
	AttrBindingTargetLabels   = "access_context_binding_target_labels"
	AttrBindingTargetZones    = "access_context_binding_target_zones"
	AttrBindingMaxWindow      = "access_context_binding_max_window_seconds"
	AttrBindingPrivileged     = "access_context_binding_privileged"
	AttrBindingEnabled        = "access_context_binding_enabled"
	AttrBindingPushPrincipals = "access_context_binding_push_principals"
)

var _ accessctx.Recorder = (*Emitter)(nil)

// ContextEvent records one external-context act. With a transaction it writes
// inside it, so a binding change and its record commit together; a refused push
// has no transaction, and is recorded on its own.
func (e *Emitter) ContextEvent(ctx context.Context, tx *store.Store, tenant store.Tenant, ev accessctx.Event) error {
	if tenant == "" {
		return fmt.Errorf("audit: an access-context record needs a tenant")
	}
	recordID, err := newRecordID()
	if err != nil {
		return err
	}

	attrs := map[string]string{AttrEvent: ev.Name}
	put(attrs, AttrContextProvider, ev.Provider)
	put(attrs, AttrContextCode, ev.Code)
	put(attrs, AttrContextField, ev.Field)
	put(attrs, AttrContextDetail, ev.Detail)

	subject, target := "", ""
	if a := ev.Assertion; a != nil {
		subject = a.Subject
		target = soleHost(a.Targets)
		put(attrs, AttrContextAssertion, a.ID)
		put(attrs, AttrContextReference, a.Reference)
		put(attrs, AttrContextScope, a.Scope)
		putList(attrs, AttrContextTargets, a.Targets)
		if !a.Window.NotBefore.IsZero() {
			attrs[AttrContextWindowStart] = stamp(a.Window.NotBefore)
		}
		if !a.Window.NotAfter.IsZero() {
			attrs[AttrContextWindowEnd] = stamp(a.Window.NotAfter)
		}
		if !a.IssuedAt.IsZero() {
			attrs[AttrContextIssuedAt] = stamp(a.IssuedAt)
		}
	}
	if b := ev.Binding; b != nil {
		attrs[AttrBindingMode] = string(b.Mode)
		attrs[AttrBindingScope] = b.Scope
		putList(attrs, AttrBindingSubjects, b.Subjects)
		putList(attrs, AttrBindingSubjectGroups, b.SubjectGroups)
		putList(attrs, AttrBindingTargets, b.Targets)
		putLabelsAs(attrs, AttrBindingTargetLabels, b.TargetLabels)
		putList(attrs, AttrBindingTargetZones, b.TargetZones)
		attrs[AttrBindingMaxWindow] = strconv.FormatInt(int64(b.MaxWindow/time.Second), 10)
		attrs[AttrBindingPrivileged] = strconv.FormatBool(b.Privileged)
		attrs[AttrBindingEnabled] = strconv.FormatBool(b.Enabled)
		putList(attrs, AttrBindingPushPrincipals, b.PushPrincipals)
	}

	actor := ev.Actor
	attrs[AttrBreakGlass] = strconv.FormatBool(actor.BreakGlass)
	put(attrs, AttrPrincipalID, actor.Principal)
	put(attrs, AttrActorSubject, actor.Subject)
	put(attrs, AttrCorrelationID, actor.CorrelationID)

	at := ev.At
	if at.IsZero() {
		at = e.now()
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
			SessionID:  firstNonEmptyOf(actor.Principal, actor.CorrelationID, recordID),
			Timestamp:  at.UTC().Format(time.RFC3339Nano),
			Kind:       "policy_decision",
			Severity:   contextSeverity(ev),
			Message:    contextMessage(ev),
			Subject:    subject,
			Target:     target,
			Attributes: attrs,
		}},
	})
	return err
}

// contextSeverity grades a record for the reader who filters by severity. An
// escalation attempt is critical — it is the one event this whole design
// exists to surface — and so is anything a break-glass credential did.
func contextSeverity(ev accessctx.Event) contract.LogSeverity {
	switch {
	case ev.Name == accessctx.EventEscalation, ev.Actor.BreakGlass:
		return contract.SeverityCritical
	default:
		return contract.SeverityWarn
	}
}

func contextMessage(ev accessctx.Event) string {
	switch ev.Name {
	case accessctx.EventEscalation:
		return "escalation attempt through " + ev.Provider + ": " + ev.Code
	case accessctx.EventPushRefused:
		return "push from " + ev.Provider + " refused: " + ev.Code
	case accessctx.EventBindingPut:
		return "scope binding for " + ev.Provider + " written"
	case accessctx.EventBindingDeleted:
		return "scope binding for " + ev.Provider + " removed"
	}
	return ev.Name
}
