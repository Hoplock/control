// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/hoplock/control/internal/store"
)

// The external path (M16, 0013): a window an external system pushed, which its
// integration's scope binding admitted, becomes a grant HERE — the same object,
// the same table, the same audit record in the same transaction, the same
// announcement — and the engine reads it through the same translation
// (PolicyGrant) as an administrator's. Everything that decides whether a push
// may become a grant at all is internal/accessctx's; nothing in this file
// judges a push, it only refuses to store one that is malformed.

// ExternalSpec is a pushed window that its integration's scope binding has
// already admitted and the ceiling has already bounded.
type ExternalSpec struct {
	// Subject is who the window is for.
	Subject string
	// Scope is the grant scope and the targets the window covers.
	Scope Scope
	// NotBefore and ExpiresAt are the window GRANTED: the asserted one,
	// started no earlier than the push arrived and clamped to the ceiling.
	NotBefore time.Time
	ExpiresAt time.Time
	// System names the provider that asserted it (ext.AccessContextInfo.Name).
	System string
	// AssertionID is the assertion's own id: the idempotency key.
	AssertionID string
	// Mode is store.ExternalPush or store.ExternalPushProbe.
	Mode store.ExternalMode
	// Reference is the ticket, scan or incident.
	Reference string
	// WindowStart and WindowEnd are the window ASSERTED, recorded verbatim.
	WindowStart time.Time
	WindowEnd   time.Time
	// AdditionalContext is a JSON string or a JSON object, verbatim, or empty.
	AdditionalContext json.RawMessage
	// Reason is why, in words an operator reads in the grant list.
	Reason string
	// Ceiling and Clamped say how the granted window was bounded, so the
	// audit record of the act can show that it was.
	Ceiling time.Duration
	Clamped bool
}

// ExternalAct is what the audit record of an external grant carries beyond the
// grant itself: the bound that was applied, and whether it bit.
type ExternalAct struct {
	// Ceiling is the longest window the push could have been granted.
	Ceiling time.Duration
	// Clamped reports that the push asked for more than Ceiling and was
	// given Ceiling — clamped, not refused (M16).
	Clamped bool
}

// CreateExternal stores the grant a pushed window becomes, or returns the one
// an earlier push of the same assertion already became.
//
// It reports replayed when the assertion was already a grant — including one
// since revoked, which stays revoked: the same id twice is the same window, not
// two, and a revocation is not undone by the external system saying it again.
// The caller compares what was pushed with what is stored; this method does
// not, because "the same id with different content" is the caller's to refuse.
func (s *Service) CreateExternal(ctx context.Context, tenant store.Tenant, actor Actor, spec ExternalSpec) (store.Grant, bool, error) {
	if err := actor.check(); err != nil {
		return store.Grant{}, false, err
	}
	now := s.now().UTC()
	g, err := s.externalGrant(spec)
	if err != nil {
		return store.Grant{}, false, err
	}

	if existing, err := s.store.Grants().GetByAssertion(ctx, tenant, g.External.System, g.External.AssertionID); err == nil {
		return existing, true, nil
	} else if !store.IsNotFound(err) {
		return store.Grant{}, false, err
	}

	g.ID = s.newID("g")
	g.Origin = store.GrantOriginExternal
	g.CreatedBy = actor.grantActor()
	act := &ExternalAct{Ceiling: spec.Ceiling, Clamped: spec.Clamped}

	err = s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.Grants().Insert(ctx, tenant, g); err != nil {
			return err
		}
		stored, err := tx.Grants().Get(ctx, tenant, g.ID)
		if err != nil {
			return err
		}
		g = stored
		return s.recorder.GrantEvent(ctx, tx, tenant, Event{
			Name: EventGrantCreated, Actor: actor, Grant: &g, External: act, At: now,
		})
	})
	if store.IsConflict(err) {
		// Two pushes of one assertion raced, and the unique key let exactly
		// one of them through. The winner's grant is the answer to both.
		existing, gerr := s.store.Grants().GetByAssertion(ctx, tenant, spec.System, spec.AssertionID)
		if gerr == nil {
			return existing, true, nil
		}
		return store.Grant{}, false, err
	}
	if err != nil {
		return store.Grant{}, false, err
	}
	s.announce(ctx, tenant, Event{Name: EventGrantCreated, Actor: actor, Grant: &g, External: act, At: now})
	return g, false, nil
}

// externalGrant validates an ExternalSpec and builds the grant it describes.
// The checks are the shape a stored grant must have; whether the push was
// entitled to it was decided before this was called.
func (s *Service) externalGrant(spec ExternalSpec) (store.Grant, error) {
	subject := strings.TrimSpace(spec.Subject)
	if err := text("subject", subject, maxSubjectLen, true); err != nil {
		return store.Grant{}, err
	}
	scope, err := normalizeScope(spec.Scope)
	if err != nil {
		return store.Grant{}, err
	}
	if len(scope.Targets) == 0 {
		// A pushed window names its hosts: one that names none is one
		// nobody can audit, and it would reach every target the rule does.
		return store.Grant{}, &ValidationError{Field: "scope.targets", Problem: ProblemRequired}
	}
	for field, v := range map[string]string{
		"system": spec.System, "assertion_id": spec.AssertionID, "external_ref": spec.Reference,
	} {
		if err := text(field, strings.TrimSpace(v), maxExternalLen, true); err != nil {
			return store.Grant{}, err
		}
	}
	if spec.Mode != store.ExternalPush && spec.Mode != store.ExternalPushProbe {
		return store.Grant{}, &ValidationError{Field: "mode", Problem: ProblemInvalid}
	}
	if err := prose("reason", spec.Reason, maxReasonLen, true); err != nil {
		return store.Grant{}, err
	}

	notBefore := spec.NotBefore.UTC().Truncate(time.Microsecond)
	expiresAt := spec.ExpiresAt.UTC().Truncate(time.Microsecond)
	if notBefore.IsZero() || expiresAt.IsZero() {
		return store.Grant{}, &ValidationError{Field: "expires_at", Problem: ProblemRequired}
	}
	if !expiresAt.After(notBefore) {
		return store.Grant{}, &ValidationError{Field: "expires_at", Problem: ProblemInvalid}
	}
	kind, additional, err := additionalText(spec.AdditionalContext)
	if err != nil {
		return store.Grant{}, err
	}

	g := grantFor(Spec{Subject: subject, Scope: scope, Reason: spec.Reason, ExternalRef: strings.TrimSpace(spec.Reference)})
	g.NotBefore, g.ExpiresAt = notBefore, expiresAt
	g.External = store.GrantExternal{
		System:         strings.TrimSpace(spec.System),
		WindowStart:    spec.WindowStart.UTC().Truncate(time.Microsecond),
		WindowEnd:      spec.WindowEnd.UTC().Truncate(time.Microsecond),
		AdditionalKind: kind,
		Additional:     additional,
		AssertionID:    strings.TrimSpace(spec.AssertionID),
		Mode:           spec.Mode,
	}
	if spec.WindowStart.IsZero() {
		g.External.WindowStart = time.Time{}
	}
	if spec.WindowEnd.IsZero() {
		g.External.WindowEnd = time.Time{}
	}
	return g, nil
}

// maxAdditionalLen bounds `additional_context`. It rides on every record of
// every session the grant backs, so it is a note, not a document.
const maxAdditionalLen = 4096

// additionalText checks `additional_context` against the one shape the
// contract allows — a JSON string or a JSON object — and returns its kind and
// the text that arrived. Anything else is refused rather than coerced: it is
// stored verbatim for an auditor, and a coerced value is one nobody asserted.
func additionalText(raw json.RawMessage) (string, string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", "", nil
	}
	if len(trimmed) > maxAdditionalLen {
		return "", "", tooLong("additional_context", maxAdditionalLen)
	}
	if !json.Valid([]byte(trimmed)) {
		return "", "", &ValidationError{Field: "additional_context", Problem: ProblemInvalid}
	}
	switch trimmed[0] {
	case '"':
		return store.AdditionalContextString, trimmed, nil
	case '{':
		return store.AdditionalContextObject, trimmed, nil
	}
	return "", "", &ValidationError{Field: "additional_context", Problem: ProblemInvalid}
}

// AdditionalContextValid reports whether raw is an `additional_context` this
// server would store: empty, a JSON string, or a JSON object.
func AdditionalContextValid(raw json.RawMessage) bool {
	_, _, err := additionalText(raw)
	return err == nil
}
