// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package accessctx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/store"
)

// THE PUSH RECEIVER. An external system says a window has opened; this decides
// whether that becomes a grant. It is the security-critical half of M16: by
// construction the caller is software somebody else wrote, asking Hoplock to
// open access to a production host. The order below is the design:
//
//  1. Who: the provider exists and takes pushes, the tenant has a binding for
//     it and the binding is on, and THIS credential is one the binding names.
//     Another integration's token pushing here is an escalation attempt.
//  2. How often: the integration's rate.
//  3. What: the provider reads the push into a window. Malformed is refused.
//  4. Again?: the same assertion id is the same window — answered with the
//     grant it already became, or refused if it now says something else.
//     This runs before the scope check, so a replay after a binding was
//     narrowed is idempotent rather than an "escalation".
//  5. When: an assertion too old, or from the future, is refused; a window
//     already over has nothing to grant.
//  6. Whether: subject, targets, scope and privilege, against the binding and
//     the active policy. Outside it is refused AND audited as an escalation.
//  7. How long: clamped to the shorter of the binding's maximum and the
//     server's ceiling, measured from when the window opens.
//  8. The grant, through internal/access, audited with the credential.

// PushResult is what an admitted push produced.
type PushResult struct {
	// Grant is the grant the window is.
	Grant store.Grant
	// Replayed reports that the assertion was already a grant, and this push
	// changed nothing.
	Replayed bool
	// Clamped reports that the window asked for more than Ceiling and was
	// given Ceiling.
	Clamped bool
	// Ceiling is the longest window this push could have been granted.
	Ceiling time.Duration
}

// Push receives one push for provider in tenant from actor.
//
// A *Refusal is a push refused on purpose; any other error is an outage the
// caller may retry (M11).
func (s *Service) Push(ctx context.Context, tenant store.Tenant, actor access.Actor, provider, contentType string, body []byte) (PushResult, error) {
	if strings.TrimSpace(actor.Principal) == "" {
		return PushResult{}, fmt.Errorf("accessctx: a push needs the credential that made it")
	}
	now := s.now().UTC()

	// 1. Who.
	p, ok := s.providers.Lookup(provider)
	if !ok {
		return PushResult{}, &Refusal{Code: CodeProviderNotFound}
	}
	if !p.Info.Pushes {
		return PushResult{}, &Refusal{Code: CodePushNotSupported}
	}
	b, err := s.store.AccessContextBindings().Get(ctx, tenant, provider)
	switch {
	case store.IsNotFound(err):
		return PushResult{}, s.refuse(ctx, tenant, actor, provider, nil, nil, &Refusal{
			Code: CodeBindingNotFound, Detail: "no scope binding names this integration in this tenant",
		})
	case err != nil:
		return PushResult{}, err
	}
	if !b.Enabled {
		return PushResult{}, s.refuse(ctx, tenant, actor, provider, &b, nil, &Refusal{Code: CodeBindingDisabled})
	}
	if !slices.Contains(b.PushPrincipals, actor.Principal) {
		return PushResult{}, s.refuse(ctx, tenant, actor, provider, &b, nil, &Refusal{
			Code: CodePushNotPermitted, Escalation: true,
			Detail: "this credential is not one the integration's binding names",
		})
	}
	if !b.Mode.Pushes() {
		return PushResult{}, s.refuse(ctx, tenant, actor, provider, &b, nil, &Refusal{
			Code: CodePushNotAccepted, Detail: "the binding only probes",
		})
	}

	// 2. How often. Not audited: a flood is exactly what must not become a
	// flood of audit records. It is logged, once per refusal.
	if ok, wait := s.limiter.allow(string(tenant) + "\x00" + provider); !ok {
		s.log.WarnContext(ctx, "access-context push rate-limited",
			"event", "access_context_rate_limited", "tenant", tenant.String(),
			"provider", provider, "principal", actor.Principal)
		return PushResult{}, &Refusal{Code: CodeRateLimited, RetryAfter: wait}
	}

	// 3. What. A push that cannot be read is the integration's bug, told to
	// the integration; it is not audited, because it asserted nothing.
	ictx, cancel := context.WithTimeout(ctx, s.interpretTimeout)
	a, err := p.impl.Interpret(ictx, ext.AccessContextPush{
		Tenant: ext.Tenant(tenant), ContentType: contentType, Body: body, ReceivedAt: now,
	})
	cancel()
	switch {
	case err == nil:
	case ext.IsMalformed(err), ext.IsInvalid(err):
		return PushResult{}, &Refusal{Code: CodeAssertionMalformed, Detail: err.Error()}
	case ext.IsDisabled(err):
		return PushResult{}, &Refusal{Code: CodePushNotSupported}
	default:
		return PushResult{}, fmt.Errorf("accessctx: provider %s could not read the push: %w", provider, err)
	}
	a, err = normalizeAssertion(a)
	if err != nil {
		return PushResult{}, err
	}

	// 4. Again?
	existing, err := s.store.Grants().GetByAssertion(ctx, tenant, provider, a.ID)
	switch {
	case err == nil:
		return s.replay(ctx, tenant, actor, provider, &b, a, existing)
	case !store.IsNotFound(err):
		return PushResult{}, err
	}

	// 5. When.
	if !a.IssuedAt.IsZero() {
		switch {
		case a.IssuedAt.Before(now.Add(-s.maxAge)):
			return PushResult{}, s.refuse(ctx, tenant, actor, provider, &b, &a, &Refusal{
				Code: CodeAssertionStale, Field: "issued_at",
				Detail: "issued " + a.IssuedAt.UTC().Format(time.RFC3339) + ", older than the server accepts",
			})
		case a.IssuedAt.After(now.Add(s.skew)):
			return PushResult{}, s.refuse(ctx, tenant, actor, provider, &b, &a, &Refusal{
				Code: CodeAssertionFromFuture, Field: "issued_at",
				Detail: "issued " + a.IssuedAt.UTC().Format(time.RFC3339) + ", further ahead than the clock skew tolerated",
			})
		}
	}
	if !a.Window.NotAfter.After(now) {
		return PushResult{}, s.refuse(ctx, tenant, actor, provider, &b, &a, &Refusal{
			Code: CodeWindowClosed, Field: "window.not_after",
			Detail: "the window ended at " + a.Window.NotAfter.UTC().Format(time.RFC3339),
		})
	}

	// 6. Whether.
	if field, detail, err := s.outsideScope(ctx, tenant, b, a); err != nil {
		return PushResult{}, err
	} else if field != "" {
		return PushResult{}, s.refuse(ctx, tenant, actor, provider, &b, &a, &Refusal{
			Code: CodeOutsideScope, Field: field, Detail: detail, Escalation: true,
		})
	}

	// 7. How long. A start this server's clock has already reached — or
	// will within the skew it tolerates — is now: a window is not granted
	// retroactively, and a scanner whose clock runs a few seconds fast is
	// not refused for it. A start further ahead is a scheduled grant.
	notBefore := now
	if a.Window.NotBefore.After(now.Add(s.skew)) {
		notBefore = a.Window.NotBefore.UTC()
	}
	ceiling := min(b.MaxWindow, s.ceiling)
	expiresAt, clamped := a.Window.NotAfter.UTC(), false
	if limit := notBefore.Add(ceiling); expiresAt.After(limit) {
		expiresAt, clamped = limit, true
	}

	// 8. The grant.
	g, replayed, err := s.grants.CreateExternal(ctx, tenant, actor, access.ExternalSpec{
		Subject: a.Subject,
		Scope: access.Scope{
			Name: b.Scope, Targets: a.Targets, Labels: b.TargetLabels, Zones: b.TargetZones,
		},
		NotBefore:         notBefore,
		ExpiresAt:         expiresAt,
		System:            provider,
		AssertionID:       a.ID,
		Mode:              b.Mode,
		Reference:         a.Reference,
		WindowStart:       a.Window.NotBefore,
		WindowEnd:         a.Window.NotAfter,
		AdditionalContext: a.AdditionalContext,
		Reason:            "window asserted by " + provider,
		Ceiling:           ceiling,
		Clamped:           clamped,
	})
	var verr *access.ValidationError
	switch {
	case errors.As(err, &verr):
		return PushResult{}, &Refusal{Code: CodeAssertionMalformed, Field: verr.Field, Detail: verr.Problem}
	case err != nil:
		return PushResult{}, err
	case replayed:
		// Two pushes of one assertion raced, and the other one won.
		return s.replay(ctx, tenant, actor, provider, &b, a, g)
	}
	return PushResult{Grant: g, Clamped: clamped, Ceiling: ceiling}, nil
}

// replay answers an assertion that is already a grant: with that grant when it
// says the same thing, and with a conflict when the same id now says something
// else. A revoked grant is answered as it is — revoked. The external system
// saying it again is not a new decision.
func (s *Service) replay(ctx context.Context, tenant store.Tenant, actor access.Actor, provider string,
	b *store.AccessContextBinding, a ext.WindowAssertion, g store.Grant,
) (PushResult, error) {
	if field := differs(g, a); field != "" {
		return PushResult{}, s.refuse(ctx, tenant, actor, provider, b, &a, &Refusal{
			Code: CodeAssertionConflict, Field: field,
			Detail: "this assertion id is already grant " + g.ID + ", which says something else",
		})
	}
	return PushResult{Grant: g, Replayed: true}, nil
}

// differs names the first part of an assertion that does not match the grant
// it already became, or "" when it is the same window. Additional context is
// not compared: it is opaque commentary, and a system that restamps it on a
// retry has not asserted a different window.
func differs(g store.Grant, a ext.WindowAssertion) string {
	switch {
	case g.SubjectID != a.Subject:
		return "subject"
	case !slices.Equal(g.ScopeTargets, a.Targets):
		return "targets"
	case a.Scope != "" && a.Scope != g.Scope:
		return "scope"
	case g.ExternalRef != a.Reference:
		return "reference"
	case !sameInstant(g.External.WindowStart, a.Window.NotBefore):
		return "window.not_before"
	case !sameInstant(g.External.WindowEnd, a.Window.NotAfter):
		return "window.not_after"
	}
	return ""
}

func sameInstant(a, b time.Time) bool {
	return a.UTC().Truncate(time.Microsecond).Equal(b.UTC().Truncate(time.Microsecond))
}

// outsideScope checks a well-formed window against the binding and the active
// policy. It returns the field outside the binding and why, or "" when the
// window is inside it; an error is an outage — the policy could not be read —
// and is never a refusal, because "could not tell" is not "no" (M11).
func (s *Service) outsideScope(ctx context.Context, tenant store.Tenant, b store.AccessContextBinding, a ext.WindowAssertion) (string, string, error) {
	if a.Scope != "" && a.Scope != b.Scope {
		return "scope", "the window asks for a scope its binding does not produce", nil
	}

	var groups []string
	subject, err := s.store.Subjects().Get(ctx, tenant, a.Subject)
	switch {
	case err == nil:
		groups = subject.Groups
	case !store.IsNotFound(err):
		return "", "", err
	}
	if !admitsSubject(b, a.Subject, groups) {
		return "subject", "the binding does not grant to this subject", nil
	}

	for i, t := range a.Targets {
		var rec *store.Target
		if !strings.HasPrefix(t, "*.") && (len(b.TargetLabels) > 0 || len(b.TargetZones) > 0) {
			got, err := s.store.Targets().GetByHostname(ctx, tenant, t)
			switch {
			case err == nil:
				rec = &got
			case !store.IsNotFound(err):
				return "", "", err
			}
		}
		if !admitsPattern(b, t, rec) {
			return fmt.Sprintf("targets[%d]", i), "the binding does not cover " + t, nil
		}
	}

	decl, err := s.policy.ScopeDeclaration(ctx, tenant, b.Scope)
	if err != nil {
		return "", "", fmt.Errorf("accessctx: whether scope %s is privileged could not be read: %w", b.Scope, err)
	}
	if decl.Privileged && !b.Privileged {
		return "scope", "the policy marks this scope privileged, and the binding may not open privileged access", nil
	}
	return "", "", nil
}

// refuse records a refused push and returns the refusal. A refusal the record
// could not be written for is an outage instead: an escalation attempt nobody
// wrote down is one nobody will ever look at, and the integration may retry.
func (s *Service) refuse(ctx context.Context, tenant store.Tenant, actor access.Actor, provider string,
	b *store.AccessContextBinding, a *ext.WindowAssertion, r *Refusal,
) error {
	name := EventPushRefused
	if r.Escalation {
		name = EventEscalation
	}
	err := s.recorder.ContextEvent(ctx, nil, tenant, Event{
		Name: name, Actor: actor, Provider: provider, Binding: b, Assertion: a,
		Code: r.Code, Field: r.Field, Detail: r.Detail, At: s.now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("accessctx: a refused push could not be recorded: %w", err)
	}
	s.log.WarnContext(ctx, "access-context push refused",
		"event", name, "tenant", tenant.String(), "provider", provider,
		"principal", actor.Principal, "code", r.Code, "field", r.Field)
	return r
}

// normalizeAssertion checks the shape of what a provider read and puts it in
// the form it is compared and stored in. A provider is somebody else's code:
// nothing it returns is trusted to be well formed.
func normalizeAssertion(a ext.WindowAssertion) (ext.WindowAssertion, error) {
	malformed := func(field, detail string) (ext.WindowAssertion, error) {
		return ext.WindowAssertion{}, &Refusal{Code: CodeAssertionMalformed, Field: field, Detail: detail}
	}
	a.ID = strings.TrimSpace(a.ID)
	a.Reference = strings.TrimSpace(a.Reference)
	a.Subject = strings.TrimSpace(a.Subject)
	a.Scope = strings.TrimSpace(a.Scope)
	switch {
	case a.ID == "" || len(a.ID) > maxAssertionIDLen || hasControl(a.ID):
		return malformed("id", "an assertion carries its own id: the idempotency key")
	case len(a.Reference) > maxAssertionIDLen || hasControl(a.Reference):
		return malformed("reference", "")
	case a.Subject == "" || len(a.Subject) > maxIdentifierLen || hasControl(a.Subject):
		return malformed("subject", "a window is for somebody")
	case len(a.Targets) == 0:
		return malformed("targets", "a window names its hosts")
	case len(a.Targets) > maxAssertionTarget:
		return malformed("targets", fmt.Sprintf("at most %d per assertion", maxAssertionTarget))
	case a.Window.NotAfter.IsZero():
		return malformed("window.not_after", "a pushed window states when it ends; nothing will ask again")
	case !a.Window.NotBefore.IsZero() && !a.Window.NotAfter.After(a.Window.NotBefore):
		return malformed("window.not_after", "the window ends before it begins")
	case !access.AdditionalContextValid(a.AdditionalContext):
		return malformed("additional_context", "a JSON string or a JSON object, and nothing else")
	}
	if a.Reference == "" {
		a.Reference = a.ID
	}
	targets := make([]string, 0, len(a.Targets))
	for i, t := range a.Targets {
		t = strings.ToLower(strings.TrimSpace(t))
		if len(t) > maxHostnameLen || !validHostPattern(t) {
			return malformed(fmt.Sprintf("targets[%d]", i), "an exact hostname or a single leading wildcard")
		}
		targets = append(targets, t)
	}
	slices.Sort(targets)
	a.Targets = slices.Compact(targets)
	if len(a.AdditionalContext) > 0 {
		a.AdditionalContext = json.RawMessage(strings.TrimSpace(string(a.AdditionalContext)))
	}
	return a, nil
}
