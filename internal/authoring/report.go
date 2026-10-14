// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package authoring is the policy lifecycle behind the north-bound surface
// (0019): validate, upload, diff, activate and apply a bundle, and say — before
// anything is published — what the fleet and the estate can actually serve.
//
// TWO QUESTIONS, TWO ANSWERS. The compiler decides whether a policy is VALID,
// and its refusals are [Rejection]s, returned verbatim: the rule, the line, the
// stable code and what to do instead (0005). This package decides whether a
// policy is ADVISABLE, and its answers are [Finding]s in ext's severity
// vocabulary — the line `ext.PolicyValidator`'s own doc comment draws. Control's
// checks run first and always; a validator an operator registers adds
// governance on top and never replaces them.
//
// WHAT A SEVERITY DOES AT ACTIVATION.
//
//   - advice is shown and stops nothing. The absence of proof is advice: a
//     target whose capability record is stale, undated or absent, a proxy that
//     has declared nothing, a declaration that cannot judge a ban. Refusing on
//     it would make an unprobeable appliance unauthorable (M17).
//   - warning is shown and stops activation UNTIL THE AUTHOR ACCEPTS IT, by the
//     finding's id. That is the override: never a silent pass, never a hard
//     refusal, and the acceptance is recorded with the act. A rung no proxy
//     provides, an interpreter in a restricted allow-list, a cache hint that
//     outlives the condition that gated it, an axis a ban empties on a build.
//   - blocking stops activation, and only a registered validator produces it.
//     Control's own checks never do: what is wrong whatever the fleet runs is a
//     compiler error, and everything else is a fact about a fleet that may
//     change.
package authoring

import (
	"slices"
	"strings"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/policy/model"
)

// The severities, spelled as ext.FindingSeverity renders them on the wire.
const (
	SeverityAdvice   = "advice"
	SeverityWarning  = "warning"
	SeverityBlocking = "blocking"
)

// SourceControl is the source of a finding Control's own checks produced. A
// registered validator's findings name its provider instead.
const SourceControl = "control"

// The codes Control's own checks produce (M21). A code is an identifier, never
// a summary: reword a message whenever it helps, and never reuse a code for a
// different condition.
const (
	// CodeProxiesUnreported: no proxy in the tenant has declared what it
	// can serve, so no rule can be judged against a build. Advice.
	CodeProxiesUnreported = "satisfiability.proxies_unreported"
	// CodeNoProxyProvides: no proxy that has declared its capabilities can
	// serve the route — a rung, a method, a platform or a device field. On
	// the proxy that is a SKIPPED RUNG, so the ladder gets shorter with no
	// error anywhere. Warning.
	CodeNoProxyProvides = "satisfiability.no_proxy_provides"
	// CodeTargetCannotTake: a fresh capability record says a target the rule
	// reaches cannot take the rung it claims. Warning.
	CodeTargetCannotTake = "satisfiability.target_cannot_take"
	// CodeTargetUnproven: a target's record is stale, undated or absent, so
	// an applied rung is not proven there. Advice — the absence of proof is
	// not proof of absence.
	CodeTargetUnproven = "satisfiability.target_unproven"
	// CodeInterpreter: a restricted allow-list names an executable that hands
	// back a shell, under a rung that claims the list is a boundary. Warning.
	CodeInterpreter = "authoring.interpreter_in_allow_list"
	// CodeCacheHintOutlives: a cache hint on a rule gated by a condition the
	// key does not carry, on a route no grant and no duration bounds.
	// Warning.
	CodeCacheHintOutlives = "authoring.cache_hint_outlives_condition"
	// CodeFloorUndeclared: a proxy's latest declaration lacks the route's
	// floor level, so authorize refuses the route there. Warning.
	CodeFloorUndeclared = "algorithm.floor_undeclared"
	// CodeAxisEmptied: on one build the route's bans leave an axis nothing to
	// offer, so authorize refuses the route there. Warning.
	CodeAxisEmptied = "algorithm.axis_emptied"
	// CodeAlgorithmUnknown: a declaration cannot judge the route's bans — it
	// omits the route's profile, or omits `algorithm_profiles` — or no proxy
	// has declared its algorithms at all. Advice.
	CodeAlgorithmUnknown = "algorithm.declaration_unknown"
	// CodeBanUnmatched: a banned name no recorded build offers. A typo looks
	// exactly like a working ban. Warning.
	CodeBanUnmatched = "algorithm.ban_unmatched"
	// CodeTargetBelowFloor: a target's stored key-exchange observation ranks
	// below the route's floor; raising the floor breaks it. Warning.
	CodeTargetBelowFloor = "algorithm.target_below_floor"
	// CodeTargetNotReported: a target the floor reaches has no key-exchange
	// observation, or a stale one. Advice, and never shown as met.
	CodeTargetNotReported = "algorithm.target_not_reported"
	// CodePolicyUnmet: the store holds a `target.algorithm_policy_unmet` for a
	// target the route reaches, under the policy the route names. Warning —
	// the record is a past observation, and the device may have changed.
	CodePolicyUnmet = "algorithm.policy_unmet"
)

// ControlCodes is every finding code Control's own checks produce, so a test
// can hold the set and a client can enumerate it.
var ControlCodes = []string{
	CodeProxiesUnreported, CodeNoProxyProvides, CodeTargetCannotTake, CodeTargetUnproven,
	CodeInterpreter, CodeCacheHintOutlives,
	CodeFloorUndeclared, CodeAxisEmptied, CodeAlgorithmUnknown, CodeBanUnmatched,
	CodeTargetBelowFloor, CodeTargetNotReported, CodePolicyUnmet,
}

// Finding is one thing a check has to say about a bundle.
type Finding struct {
	// ID is stable for the same finding on the same bundle against the same
	// fleet, and it is what an author accepts. It is built from the code and
	// what the finding is about, never from the message.
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	// Source is SourceControl, or the provider of the validator that said it.
	Source string `json:"source"`
	// Rule and Line locate it; empty and zero at document scope.
	Rule string `json:"rule,omitempty"`
	Line int    `json:"line,omitempty"`
	// Parameters are typed and named (M21).
	Parameters map[string]any `json:"parameters,omitempty"`
	// Message is English, for policyctl, CI logs and curl.
	Message string `json:"message"`
}

// Rejection is one compiler refusal, verbatim (0005).
type Rejection struct {
	Code       string            `json:"code"`
	Rule       string            `json:"rule,omitempty"`
	Line       int               `json:"line,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
	// Message is the compiler's own text, which names the rule, the line and
	// what to do instead.
	Message string `json:"message"`
}

// CodeTenantMismatch is the one rejection this package adds to the compiler's:
// a bundle naming a tenant other than the one it is uploaded to. The bundle
// says which tenant it serves (M18), and a document that disagrees with the
// route it arrived on is refused rather than reassigned.
const CodeTenantMismatch = "bundle.tenant_mismatch"

// Report is the answer to "may this be published, and what should the author
// know first".
type Report struct {
	// Valid is whether the bundle compiles.
	Valid bool `json:"valid"`
	// Digest is `sha256:<hex>` of the source: the hash a stored version
	// carries and the uploader is told.
	Digest     string      `json:"digest"`
	Rejections []Rejection `json:"rejections"`
	// Findings are every check's, Control's first, sorted by severity
	// (blocking first) and then by id.
	Findings []Finding `json:"findings"`
}

// Blocking returns the findings that stop activation outright.
func (r Report) Blocking() []Finding {
	return slices.DeleteFunc(slices.Clone(r.Findings), func(f Finding) bool { return f.Severity != SeverityBlocking })
}

// Unaccepted returns the warnings an author has not accepted.
func (r Report) Unaccepted(accepted []string) []Finding {
	return slices.DeleteFunc(slices.Clone(r.Findings), func(f Finding) bool {
		return f.Severity != SeverityWarning || slices.Contains(accepted, f.ID)
	})
}

// Accepted returns the ids of the warnings that were accepted, in order — what
// the activation's audit record names.
func (r Report) Accepted(accepted []string) []string {
	var out []string
	for _, f := range r.Findings {
		if f.Severity == SeverityWarning && slices.Contains(accepted, f.ID) {
			out = append(out, f.ID)
		}
	}
	return out
}

func rejectionsOf(rs model.Rejections) []Rejection {
	out := make([]Rejection, 0, len(rs))
	for _, r := range rs {
		out = append(out, Rejection{
			Code: string(r.Code), Rule: r.Rule, Line: r.Line, Parameters: r.Params, Message: r.Error(),
		})
	}
	return out
}

func severityOf(s ext.FindingSeverity) string { return s.String() }

func severityRank(s string) int {
	switch s {
	case SeverityBlocking:
		return 0
	case SeverityWarning:
		return 1
	}
	return 2
}

func sortFindings(fs []Finding) {
	slices.SortStableFunc(fs, func(a, b Finding) int {
		if d := severityRank(a.Severity) - severityRank(b.Severity); d != 0 {
			return d
		}
		return strings.Compare(a.ID, b.ID)
	})
}

// findingID joins a code and what it is about into an id. An author copies it
// back whole, so it only has to be stable and to tell this report's findings
// apart.
func findingID(code string, parts ...string) string {
	return code + "/" + strings.Join(parts, "/")
}
