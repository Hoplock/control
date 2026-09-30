// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// Spec is what an administrator asks for: "Alice gets prod-dba on the
// database hosts for the next thirty minutes, because of INC-9".
type Spec struct {
	// Subject is who would hold the grant: the subject id the proxy sends,
	// which is what the decision path looks grants up by. A subject that has
	// never logged in is allowed, so access can be arranged before it is
	// needed.
	Subject string
	// Scope is what the grant permits and which targets it covers.
	Scope Scope
	// NotBefore is when the window opens. Zero, or an instant already past,
	// means now.
	NotBefore time.Time
	// ExpiresAt and Duration say when it closes. Exactly one is required:
	// both set is two answers to one question.
	ExpiresAt time.Time
	Duration  time.Duration
	// Reason is why, in the requester's own words. Required: a grant nobody
	// can explain is standing access with a timer on it.
	Reason string
	// ReasonCode is an optional stable code for the reason, which a report
	// can group by where it cannot group by prose.
	ReasonCode string
	// ExternalRef is the ticket or incident the grant is tied to, when there
	// is one. It travels to the proxy as the session's grant context, so an
	// auditor can ask which sessions a ticket authorised (M10).
	ExternalRef string
}

// Scope is what a grant permits and which targets it covers.
type Scope struct {
	// Name is the scope a policy rule matches with `grant.scopes`: the
	// privilege itself. What it lets the holder DO is the matching rule's
	// route — channels, commands, credentials — so a grant can never permit
	// something no rule was written to permit.
	Name string
	// Targets are hostnames, exact or with a single leading wildcard.
	Targets []string
	// Labels must all be present on the target with these values.
	Labels map[string]string
	// Zones are fleet zones.
	Zones []string
}

// Bounds on a spec. They are about keeping a grant readable and a request
// cheap to validate, not about policy: the policy bound on a window is
// Options.MaxDuration.
const (
	maxSubjectLen    = 256
	maxScopeNameLen  = 128
	maxHostnameLen   = 253
	maxSelectorItems = 64
	maxLabelLen      = 128
	maxReasonLen     = 1024
	maxReasonCodeLen = 64
	maxExternalLen   = 256
	// MaxRevokeReasonLen bounds the text a revocation shows the holder. It
	// is displayed in a terminal as the connection closes, so a paragraph is
	// already too long.
	MaxRevokeReasonLen = 512
)

// reasonCodePattern is a stable code: lower case, digits, and three
// separators, nothing a report would have to normalise.
var reasonCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// window is a validated, normalised window: UTC, and truncated to the
// microsecond the database keeps, so what was checked is exactly what is
// stored.
type window struct {
	notBefore time.Time
	expiresAt time.Time
}

// normalize validates a spec and returns it in the form it is stored in.
func normalize(spec Spec, now time.Time, maxDuration time.Duration) (Spec, window, error) {
	out := Spec{
		Subject:     strings.TrimSpace(spec.Subject),
		Reason:      strings.TrimSpace(spec.Reason),
		ReasonCode:  strings.TrimSpace(spec.ReasonCode),
		ExternalRef: strings.TrimSpace(spec.ExternalRef),
	}

	if err := text("subject", out.Subject, maxSubjectLen, true); err != nil {
		return Spec{}, window{}, err
	}
	scope, err := normalizeScope(spec.Scope)
	if err != nil {
		return Spec{}, window{}, err
	}
	out.Scope = scope

	if err := prose("reason", out.Reason, maxReasonLen, true); err != nil {
		return Spec{}, window{}, err
	}
	if out.ReasonCode != "" {
		if len(out.ReasonCode) > maxReasonCodeLen {
			return Spec{}, window{}, tooLong("reason_code", maxReasonCodeLen)
		}
		if !reasonCodePattern.MatchString(out.ReasonCode) {
			return Spec{}, window{}, &ValidationError{Field: "reason_code", Problem: ProblemInvalid}
		}
	}
	if err := text("external_ref", out.ExternalRef, maxExternalLen, false); err != nil {
		return Spec{}, window{}, err
	}

	w, err := normalizeWindow(spec, now, maxDuration)
	if err != nil {
		return Spec{}, window{}, err
	}
	out.NotBefore, out.ExpiresAt = w.notBefore, w.expiresAt
	return out, w, nil
}

// normalizeScope validates a scope and puts its lists in one canonical order,
// so two grants for the same thing read the same.
func normalizeScope(in Scope) (Scope, error) {
	out := Scope{Name: strings.TrimSpace(in.Name)}
	if err := text("scope.name", out.Name, maxScopeNameLen, true); err != nil {
		return Scope{}, err
	}

	if len(in.Targets) > maxSelectorItems {
		return Scope{}, tooMany("scope.targets", maxSelectorItems)
	}
	for _, t := range in.Targets {
		// Lower case, because matching is case-insensitive (DNS is) and a
		// stored scope should read the way it matches.
		t = strings.ToLower(strings.TrimSpace(t))
		if len(t) > maxHostnameLen {
			return Scope{}, tooLong("scope.targets", maxHostnameLen)
		}
		if !model.ValidHostPattern(t) || hasControl(t) || strings.ContainsAny(t, " \t") {
			// The pattern language is the policy engine's own, so a grant
			// can name exactly what a rule can and nothing it cannot.
			return Scope{}, &ValidationError{Field: "scope.targets", Problem: ProblemInvalid}
		}
		out.Targets = append(out.Targets, t)
	}
	slices.Sort(out.Targets)
	out.Targets = slices.Compact(out.Targets)

	if len(in.Labels) > maxSelectorItems {
		return Scope{}, tooMany("scope.labels", maxSelectorItems)
	}
	for k, v := range in.Labels {
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if err := text("scope.labels", k, maxLabelLen, true); err != nil {
			return Scope{}, err
		}
		if err := text("scope.labels", v, maxLabelLen, true); err != nil {
			return Scope{}, err
		}
		if out.Labels == nil {
			out.Labels = make(map[string]string, len(in.Labels))
		}
		out.Labels[k] = v
	}

	if len(in.Zones) > maxSelectorItems {
		return Scope{}, tooMany("scope.zones", maxSelectorItems)
	}
	for _, z := range in.Zones {
		z = strings.TrimSpace(z)
		if err := text("scope.zones", z, maxLabelLen, true); err != nil {
			return Scope{}, err
		}
		out.Zones = append(out.Zones, z)
	}
	slices.Sort(out.Zones)
	out.Zones = slices.Compact(out.Zones)
	return out, nil
}

// normalizeWindow resolves the window against the server's clock and its
// ceiling.
func normalizeWindow(spec Spec, now time.Time, maxDuration time.Duration) (window, error) {
	switch {
	case !spec.ExpiresAt.IsZero() && spec.Duration != 0:
		return window{}, &ValidationError{Field: "expires_at", Problem: ProblemAmbiguous}
	case spec.ExpiresAt.IsZero() && spec.Duration == 0:
		return window{}, &ValidationError{Field: "expires_at", Problem: ProblemRequired}
	case spec.Duration < 0:
		return window{}, &ValidationError{Field: "duration", Problem: ProblemInvalid}
	}

	now = now.UTC().Truncate(time.Microsecond)
	from := spec.NotBefore.UTC().Truncate(time.Microsecond)
	if from.IsZero() || from.Before(now) {
		// A window that "started an hour ago" has nothing to do with the
		// hour before it was created: a grant confers nothing on decisions
		// already taken, so the past part of it would only mislead a
		// reader of the row.
		from = now
	}
	to := spec.ExpiresAt.UTC().Truncate(time.Microsecond)
	if spec.Duration != 0 {
		to = from.Add(spec.Duration).Truncate(time.Microsecond)
	}
	if !to.After(from) {
		return window{}, &ValidationError{Field: "expires_at", Problem: ProblemNotAfterStart}
	}
	if maxDuration > 0 && to.Sub(from) > maxDuration {
		return window{}, &ValidationError{
			Field: "expires_at", Problem: ProblemExceedsMaximum, Limit: maxDuration.String(),
		}
	}
	return window{notBefore: from, expiresAt: to}, nil
}

// normalizeRevokeReason validates the text a revocation shows the holder.
func normalizeRevokeReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if err := text("reason", reason, MaxRevokeReasonLen, true); err != nil {
		return "", err
	}
	return reason, nil
}

// text validates a one-line value: no control characters at all.
func text(field, v string, limit int, required bool) error {
	if v == "" {
		if required {
			return &ValidationError{Field: field, Problem: ProblemRequired}
		}
		return nil
	}
	if len(v) > limit {
		return tooLong(field, limit)
	}
	if hasControl(v) {
		return &ValidationError{Field: field, Problem: ProblemInvalid}
	}
	return nil
}

// prose validates free text: line breaks and tabs are a person writing, every
// other control character is somebody trying to rewrite a terminal or a log.
func prose(field, v string, limit int, required bool) error {
	if v == "" {
		if required {
			return &ValidationError{Field: field, Problem: ProblemRequired}
		}
		return nil
	}
	if len(v) > limit {
		return tooLong(field, limit)
	}
	for _, r := range v {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return &ValidationError{Field: field, Problem: ProblemInvalid}
		}
	}
	return nil
}

func hasControl(v string) bool {
	return strings.ContainsFunc(v, unicode.IsControl)
}

func tooLong(field string, limit int) error {
	return &ValidationError{Field: field, Problem: ProblemTooLong, Limit: strconv.Itoa(limit)}
}

func tooMany(field string, limit int) error {
	return &ValidationError{Field: field, Problem: ProblemTooMany, Limit: strconv.Itoa(limit)}
}

// grantFor builds the grant a validated spec describes. The caller sets the
// id, the origin and the provenance.
func grantFor(spec Spec) store.Grant {
	return store.Grant{
		SubjectID:    spec.Subject,
		Scope:        spec.Scope.Name,
		ScopeTargets: spec.Scope.Targets,
		ScopeLabels:  spec.Scope.Labels,
		ScopeZones:   spec.Scope.Zones,
		NotBefore:    spec.NotBefore,
		ExpiresAt:    spec.ExpiresAt,
		ReasonCode:   spec.ReasonCode,
		Reason:       spec.Reason,
		ExternalRef:  spec.ExternalRef,
	}
}
