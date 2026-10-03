// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package accessctx

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// THE SCOPE BINDING (M16). One per provider per tenant, and it is the whole of
// what an integration may ever assert there:
//
//   - WHO: subject ids, or members of groups. At least one.
//   - WHAT: a target selector in a grant scope's own shape — hostname
//     patterns, required labels, zones, ANDed. At least one constraint. The
//     labels and zones ride on every grant the integration produces, so a
//     target that stops carrying them stops being covered at decision time.
//   - WHICH SCOPE: the one grant scope its windows carry.
//   - HOW LONG: the longest window it may open; the server's ceiling applies
//     on top, and a window asking for more is clamped, not refused.
//   - PRIVILEGED: whether it may open a window in a scope the policy marks
//     privileged at all.
//   - BY WHOM: the north-bound credentials that may push for it.
//   - HOW: push, probe, or push-probe (a push opens, a probe confirms).
//
// This file is the internal API the north-bound surface (0014) exposes, and
// the admission checks the push receiver and the probe path share.

// Bounds on a binding. They keep a binding readable and an admission check
// cheap; they are not policy.
const (
	maxBindingItems    = 64
	maxIdentifierLen   = 256
	maxHostnameLen     = 253
	maxLabelLen        = 128
	maxDescriptionLen  = 1024
	minBindingWindow   = time.Minute
	maxAssertionIDLen  = 256
	maxAssertionTarget = 64
)

// PutBinding creates or replaces a provider's binding in a tenant, audited with
// the actor in the transaction that writes it.
//
// The provider must be one this server runs, in a mode it implements — a
// binding for a provider that cannot push is a push receiver nobody can reach
// — and the binding's window may not exceed the server's ceiling.
func (s *Service) PutBinding(ctx context.Context, tenant store.Tenant, actor access.Actor, b store.AccessContextBinding) (store.AccessContextBinding, error) {
	if strings.TrimSpace(actor.Principal) == "" {
		return store.AccessContextBinding{}, errNoPrincipal
	}
	b, err := s.normalizeBinding(b)
	if err != nil {
		return store.AccessContextBinding{}, err
	}
	b.UpdatedBy = store.GrantActor{Subject: actor.Subject, Principal: actor.Principal, BreakGlass: actor.BreakGlass}

	var stored store.AccessContextBinding
	err = s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.AccessContextBindings().Put(ctx, tenant, b); err != nil {
			return err
		}
		got, err := tx.AccessContextBindings().Get(ctx, tenant, b.Provider)
		if err != nil {
			return err
		}
		stored = got
		return s.recorder.ContextEvent(ctx, tx, tenant, Event{
			Name: EventBindingPut, Actor: actor, Provider: b.Provider, Binding: &stored, At: s.now().UTC(),
		})
	})
	if err != nil {
		return store.AccessContextBinding{}, err
	}
	return stored, nil
}

// Binding returns one provider's binding. Absent is store.ErrNotFound.
func (s *Service) Binding(ctx context.Context, tenant store.Tenant, provider string) (store.AccessContextBinding, error) {
	return s.store.AccessContextBindings().Get(ctx, tenant, provider)
}

// Bindings returns every binding in a tenant, by provider.
func (s *Service) Bindings(ctx context.Context, tenant store.Tenant) ([]store.AccessContextBinding, error) {
	return s.store.AccessContextBindings().List(ctx, tenant)
}

// DeleteBinding removes a provider's binding, audited. Grants its pushes
// already produced are grants: they stay until they expire or are revoked,
// which is the act that ends the sessions they back. A push-probe window,
// though, stops counting at once — there is no binding to confirm it under.
func (s *Service) DeleteBinding(ctx context.Context, tenant store.Tenant, actor access.Actor, provider string) (bool, error) {
	if strings.TrimSpace(actor.Principal) == "" {
		return false, errNoPrincipal
	}
	var deleted bool
	err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		existing, err := tx.AccessContextBindings().Get(ctx, tenant, provider)
		if store.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if deleted, err = tx.AccessContextBindings().Delete(ctx, tenant, provider); err != nil || !deleted {
			return err
		}
		return s.recorder.ContextEvent(ctx, tx, tenant, Event{
			Name: EventBindingDeleted, Actor: actor, Provider: provider, Binding: &existing, At: s.now().UTC(),
		})
	})
	return deleted && err == nil, err
}

var errNoPrincipal = &access.ValidationError{Field: "actor", Problem: access.ProblemRequired}

// normalizeBinding validates a binding and puts its lists in one canonical
// order, so two bindings for the same thing read the same.
func (s *Service) normalizeBinding(in store.AccessContextBinding) (store.AccessContextBinding, error) {
	out := store.AccessContextBinding{
		Provider:    strings.TrimSpace(in.Provider),
		Mode:        in.Mode,
		Scope:       strings.TrimSpace(in.Scope),
		MaxWindow:   in.MaxWindow,
		Privileged:  in.Privileged,
		Enabled:     in.Enabled,
		Description: strings.TrimSpace(in.Description),
	}
	p, ok := s.providers.Lookup(out.Provider)
	switch {
	case !ValidName(out.Provider):
		return store.AccessContextBinding{}, invalidField("provider")
	case !ok:
		// A binding for a provider this server does not run is a scope
		// nobody can exercise, and a typo here would otherwise surface as
		// pushes refused for want of a binding.
		return store.AccessContextBinding{}, &access.ValidationError{Field: "provider", Problem: ProblemNotRegistered}
	case !out.Mode.Valid():
		return store.AccessContextBinding{}, invalidField("mode")
	case out.Mode.Pushes() && !p.Info.Pushes, out.Mode.Probes() && !p.Info.Probes:
		return store.AccessContextBinding{}, &access.ValidationError{Field: "mode", Problem: ProblemUnsupported}
	case !model.ValidScopeName(out.Scope):
		return store.AccessContextBinding{}, invalidField("scope")
	case out.MaxWindow < minBindingWindow:
		return store.AccessContextBinding{}, invalidField("max_window")
	case out.MaxWindow > s.ceiling:
		return store.AccessContextBinding{}, &access.ValidationError{
			Field: "max_window", Problem: access.ProblemExceedsMaximum, Limit: s.ceiling.String(),
		}
	case len(out.Description) > maxDescriptionLen:
		return store.AccessContextBinding{}, tooLongField("description", maxDescriptionLen)
	}
	out.MaxWindow = out.MaxWindow.Truncate(time.Second)

	var err error
	if out.Subjects, err = identifiers("subjects", in.Subjects); err != nil {
		return store.AccessContextBinding{}, err
	}
	if out.SubjectGroups, err = identifiers("subject_groups", in.SubjectGroups); err != nil {
		return store.AccessContextBinding{}, err
	}
	if len(out.Subjects)+len(out.SubjectGroups) == 0 {
		// A binding that may grant to nobody is a mistake; one that may
		// grant to anybody is not expressible.
		return store.AccessContextBinding{}, &access.ValidationError{Field: "subjects", Problem: access.ProblemRequired}
	}

	if len(in.Targets) > maxBindingItems {
		return store.AccessContextBinding{}, tooManyField("targets", maxBindingItems)
	}
	for _, t := range in.Targets {
		t = strings.ToLower(strings.TrimSpace(t))
		if len(t) > maxHostnameLen || !validHostPattern(t) {
			return store.AccessContextBinding{}, invalidField("targets")
		}
		out.Targets = append(out.Targets, t)
	}
	slices.Sort(out.Targets)
	out.Targets = slices.Compact(out.Targets)

	if len(in.TargetLabels) > maxBindingItems {
		return store.AccessContextBinding{}, tooManyField("target_labels", maxBindingItems)
	}
	for k, v := range in.TargetLabels {
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" || v == "" || len(k) > maxLabelLen || len(v) > maxLabelLen || hasControl(k) || hasControl(v) {
			return store.AccessContextBinding{}, invalidField("target_labels")
		}
		if out.TargetLabels == nil {
			out.TargetLabels = map[string]string{}
		}
		out.TargetLabels[k] = v
	}
	if out.TargetZones, err = identifiers("target_zones", in.TargetZones); err != nil {
		return store.AccessContextBinding{}, err
	}
	if len(out.Targets) == 0 && len(out.TargetLabels) == 0 && len(out.TargetZones) == 0 {
		// Likewise a binding that may name every target.
		return store.AccessContextBinding{}, &access.ValidationError{Field: "targets", Problem: access.ProblemRequired}
	}

	if out.PushPrincipals, err = identifiers("push_principals", in.PushPrincipals); err != nil {
		return store.AccessContextBinding{}, err
	}
	switch {
	case out.Mode.Pushes() && len(out.PushPrincipals) == 0:
		// A push binding nobody may push for is a receiver that refuses
		// everything; saying so now beats every push saying it later.
		return store.AccessContextBinding{}, &access.ValidationError{Field: "push_principals", Problem: access.ProblemRequired}
	case !out.Mode.Pushes() && len(out.PushPrincipals) > 0:
		// Credentials named for a binding that takes no pushes would read
		// as a permission that exists.
		return store.AccessContextBinding{}, &access.ValidationError{Field: "push_principals", Problem: ProblemUnsupported}
	}
	return out, nil
}

// Problems a binding can have beyond access's own vocabulary.
const (
	// ProblemNotRegistered is a provider this server does not run.
	ProblemNotRegistered = "not_registered"
	// ProblemUnsupported is a mode the provider does not implement, or push
	// credentials on a binding that takes no pushes.
	ProblemUnsupported = "unsupported"
)

func identifiers(field string, in []string) ([]string, error) {
	if len(in) > maxBindingItems {
		return nil, tooManyField(field, maxBindingItems)
	}
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || len(v) > maxIdentifierLen || hasControl(v) {
			return nil, invalidField(field)
		}
		out = append(out, v)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func hasControl(v string) bool { return strings.ContainsFunc(v, unicode.IsControl) }

// validHostPattern is the policy engine's own pattern language — an exact
// hostname or a single leading wildcard — so a binding and a push can name
// exactly what a rule and a grant can, and nothing they cannot.
func validHostPattern(t string) bool {
	return model.ValidHostPattern(t) && !strings.ContainsFunc(t, unicode.IsSpace) && !hasControl(t)
}

func invalidField(field string) error {
	return &access.ValidationError{Field: field, Problem: access.ProblemInvalid}
}

func tooLongField(field string, limit int) error {
	return &access.ValidationError{Field: field, Problem: access.ProblemTooLong, Limit: strconv.Itoa(limit)}
}

func tooManyField(field string, limit int) error {
	return &access.ValidationError{Field: field, Problem: access.ProblemTooMany, Limit: strconv.Itoa(limit)}
}

// admitsSubject reports whether the binding may grant to this subject: one of
// its subject ids, or a member of one of its groups. Groups come from this
// server's own record of the subject, never from what the integration sent.
func admitsSubject(b store.AccessContextBinding, subjectID string, groups []string) bool {
	if slices.Contains(b.Subjects, subjectID) {
		return true
	}
	for _, g := range groups {
		if slices.Contains(b.SubjectGroups, g) {
			return true
		}
	}
	return false
}

// admitsTarget reports whether the binding covers a concrete target: the one a
// decision is about. A label or zone constraint needs the target's inventory
// record; a target this server holds no record of carries none, and is
// admitted only by a binding that constrains hostnames alone.
func admitsTarget(b store.AccessContextBinding, host string, labels map[string]string, zone string) bool {
	if len(b.Targets) > 0 && !matchesAny(host, b.Targets) {
		return false
	}
	for k, v := range b.TargetLabels {
		if labels[k] != v {
			return false
		}
	}
	return len(b.TargetZones) == 0 || slices.Contains(b.TargetZones, zone)
}

// admitsPattern reports whether the binding covers a target a push NAMED,
// which may be a wildcard. A pattern must lie inside one of the binding's
// hostname patterns; the binding's labels and zones need not be checked here,
// because they ride on the grant and are checked against each target at
// decision time — but an exact hostname is checked now, against its record,
// so that a push naming a host outside the binding is refused and audited
// rather than producing a grant that merely never matches.
func admitsPattern(b store.AccessContextBinding, pattern string, rec *store.Target) bool {
	if len(b.Targets) > 0 {
		inside := false
		for _, bp := range b.Targets {
			if model.HostPatternCovers(bp, pattern) {
				inside = true
				break
			}
		}
		if !inside {
			return false
		}
	}
	if strings.HasPrefix(pattern, "*.") || (len(b.TargetLabels) == 0 && len(b.TargetZones) == 0) {
		return true
	}
	if rec == nil {
		// An exact host the binding constrains by label or zone, and this
		// server has no record of: nothing proves it is inside.
		return false
	}
	return admitsTarget(b, pattern, rec.Labels, rec.Zone)
}

func matchesAny(host string, patterns []string) bool {
	for _, p := range patterns {
		if model.MatchesHostPattern(host, p) {
			return true
		}
	}
	return false
}
