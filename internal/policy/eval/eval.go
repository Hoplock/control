// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/hoplock/control/internal/policy/compile"
	"github.com/hoplock/control/internal/policy/model"
)

// DefaultDenyReason is what the always-present default-deny records when it
// fires. It is a constant rather than a string built at the call site, because
// "nothing matched" must read the same in every decision record ever written.
const DefaultDenyReason = "no rule matched; the bundle's default-deny applied"

// Explanation is a first-class output of evaluation, not a log line (M4).
//
// The proxy tells a user "access denied" and a session id, deliberately vague —
// a precise denial makes the proxy an oracle for probing the estate — and the
// operator resolves that id here into the whole story. Vague to the user, total
// to the auditor, and that pair only works if this side is genuinely total. A
// wrong explanation is a product bug, not a logging bug.
type Explanation struct {
	// Effect is what was decided.
	Effect model.Effect
	// Basis is whether a rule decided it or the default-deny fired.
	Basis model.Basis
	// Rule is the id of the rule that decided, empty on a default-deny.
	Rule string
	// RuleLine is where that rule was authored.
	RuleLine int
	// Terms are the input values that made the rule match, in a fixed
	// order. On a default-deny there are none, which is itself the answer:
	// nothing matched.
	Terms []model.MatchedTerm
	// Obligations are the obligations the decision emitted.
	Obligations []model.ObligationKind
	// DenyReason is why access was refused: the rule's own reason, or
	// DefaultDenyReason.
	DenyReason string
	// Grant is the id of the grant that satisfied the rule's grant
	// constraint, empty where none was involved.
	Grant string
	// BundleDigest names the exact document this decision came from.
	BundleDigest string
	// EvaluatedAt is the instant the decision was made for — the input,
	// echoed, because a decision record read back later has to say what
	// "now" was.
	EvaluatedAt time.Time
	// RulesConsidered is how many rules were examined before the decision.
	// It is the cheapest possible check that evaluation stayed linear (M5).
	RulesConsidered int
}

// String renders the explanation the way an operator reads it.
func (e Explanation) String() string {
	var b strings.Builder
	switch e.Basis {
	case model.BasisRule:
		fmt.Fprintf(&b, "%s by rule %q (line %d)", e.Effect, e.Rule, e.RuleLine)
	case model.BasisDefaultDeny:
		fmt.Fprintf(&b, "%s by default-deny", e.Effect)
	}
	if len(e.Terms) > 0 {
		parts := make([]string, 0, len(e.Terms))
		for _, t := range e.Terms {
			parts = append(parts, t.String())
		}
		fmt.Fprintf(&b, " on %s", strings.Join(parts, ", "))
	}
	if e.Effect == model.EffectDeny && e.DenyReason != "" {
		fmt.Fprintf(&b, ": %s", e.DenyReason)
	}
	if len(e.Obligations) > 0 {
		obs := make([]string, 0, len(e.Obligations))
		for _, o := range e.Obligations {
			obs = append(obs, string(o))
		}
		fmt.Fprintf(&b, " [obligations: %s]", strings.Join(obs, ", "))
	}
	return b.String()
}

// Evaluate runs a compiled program against one set of inputs and returns the
// whole-connection snapshot and the explanation for it. A nil snapshot is a
// denial, and the explanation says which rule denied or that nothing matched.
//
// Evaluation is total: it has no error return, no clock of its own, and no
// unbounded construct. Rules are scanned in order, first match wins, and the
// default-deny at the end is always present and always recorded — it is not an
// authored rule and cannot be removed.
//
// The tenant is nowhere in this signature on purpose (M18). Which program is
// served is a lookup the caller already did; it is not a predicate evaluated
// per request, and an explanation that named a tenant term instead of a rule
// would be a worse explanation.
func Evaluate(prog *compile.Program, in model.Input) (*model.Snapshot, Explanation) {
	base := Explanation{
		BundleDigest: prog.Digest(),
		EvaluatedAt:  in.Now,
	}
	// Capacity for the common case: a handful of terms, no growth in the
	// hot path.
	terms := make([]model.MatchedTerm, 0, 8)

	for i := range prog.Len() {
		rule := prog.Rule(i)
		terms = terms[:0]
		grant, ok := rule.Match(in, &terms)
		if !ok {
			continue
		}
		base.Basis = model.BasisRule
		base.Rule = rule.ID()
		base.RuleLine = rule.Line()
		base.Terms = slices.Clone(terms)
		base.RulesConsidered = i + 1
		if grant != nil {
			base.Grant = grant.ID
		}

		switch rule.Effect() {
		case model.EffectDeny:
			base.Effect = model.EffectDeny
			base.DenyReason = rule.Reason()
			return nil, base
		case model.EffectAllow:
			base.Effect = model.EffectAllow
			base.Obligations = obligationKinds(rule.Obligations())
			return snapshot(rule, in, grant), base
		}
		// A rule with no effect cannot be compiled, so this is
		// unreachable; treating it as a denial rather than a panic keeps
		// evaluation total, and the basis still names the rule.
		base.Effect = model.EffectDeny
		base.DenyReason = DefaultDenyReason
		return nil, base
	}

	base.Effect = model.EffectDeny
	base.Basis = model.BasisDefaultDeny
	base.DenyReason = DefaultDenyReason
	base.RulesConsidered = prog.Len()
	return nil, base
}

// snapshot renders a matched allow rule's route as the connection's snapshot,
// resolving what only evaluation knows: the target actually asked about, the
// account name, the absolute deadline, and the grant context.
//
// Everything is copied. The program is immutable and served from memory to
// every request on the decision path (M5), so a snapshot that aliased it would
// let one caller's edit become another caller's policy.
func snapshot(rule *compile.Rule, in model.Input, grant *model.Grant) *model.Snapshot {
	rt := rule.Route()
	obligations := slices.Clone(rule.Obligations())

	s := &model.Snapshot{
		Rule:             rule.ID(),
		Intent:           rt.Intent,
		Target:           orString(rt.Target, in.Target.Hostname),
		Port:             orInt32(rt.Port, in.Target.Port),
		Channels:         channels(rt),
		Requests:         cloneRequests(rt.Requests),
		Forwards:         cloneForwards(rt.Forwards),
		GlobalRequests:   cloneGlobalRequests(rt.GlobalRequests),
		Filter:           cloneFilter(rt.Filter),
		Credentials:      resolveLadder(rt.Credentials, in.Subject.ID),
		AlgorithmProfile: rt.AlgorithmProfile,
		Enforcement:      cloneEnforcement(rt.Enforcement),
		SessionDeadline:  deadline(rt, in, grant),
		Concurrency:      rt.Concurrency,
		Cache:            cloneCache(rule.Cache()),
		Obligations:      obligations,
	}

	s.RequireSessionCapture = rt.RequireSessionCapture != nil && *rt.RequireSessionCapture
	for _, o := range obligations {
		// A record-session obligation is not advisory: it is the
		// compensating control that makes an unbounded-privilege route
		// defensible, and the compiler has already refused the rule that
		// both required it and waived it.
		if o.Kind == model.ObligationRecordSession {
			s.RequireSessionCapture = true
		}
	}
	if grant != nil {
		s.GrantContext = grant.Context()
	}
	return s
}

// deadline resolves the session's end as an absolute instant.
//
// An instant rather than a duration, because a duration re-anchors on every hop
// of a chained route and silently multiplies the window; the engine already
// takes time as an input, so the instant is computable and total.
//
// Where a grant supplied the access, its expiry and the window an external
// system asserted bound the deadline too. The proxy enforces only this value
// (proxy D16), so this server sets it having already weighed the window rather
// than sending the window along and hoping.
func deadline(rt *model.Route, in model.Input, grant *model.Grant) time.Time {
	var out time.Time
	if d := rt.MaxSessionDuration.Duration(); d > 0 {
		out = in.Now.Add(d)
	}
	if grant == nil {
		return out
	}
	out = earlier(out, grant.ExpiresAt)
	if grant.External != nil {
		out = earlier(out, grant.External.WindowEnd)
	}
	return out
}

func earlier(a, b time.Time) time.Time {
	switch {
	case b.IsZero():
		return a
	case a.IsZero() || b.Before(a):
		return b
	default:
		return a
	}
}

// channels always yields a non-nil slice: the contract requires the field, and
// an empty list denies every channel, which is a policy an author may well mean.
func channels(rt *model.Route) []model.ChannelType {
	if rt.Channels == nil {
		return []model.ChannelType{}
	}
	out := slices.Clone(*rt.Channels)
	if out == nil {
		out = []model.ChannelType{}
	}
	return out
}

// resolveLadder renders each entry's account name for this subject. The
// snapshot carries a concrete name, because that is what the proxy is told and
// what the target's own audit trail will be made of — and it is never derived
// from the identity's `login`, which the vocabulary cannot even express.
func resolveLadder(ladder []model.CredentialEntry, subject string) []model.CredentialEntry {
	if ladder == nil {
		return nil
	}
	out := make([]model.CredentialEntry, len(ladder))
	for i, e := range ladder {
		out[i] = e
		out[i].Username = model.Username{
			Source: model.UsernameLiteral,
			Value:  e.Username.Resolve(subject),
		}
		out[i].DeviceFields = maps.Clone(e.DeviceFields)
	}
	return out
}

func cloneRequests(p *model.RequestPermissions) *model.RequestPermissions {
	if p == nil {
		return nil
	}
	out := &model.RequestPermissions{Types: slices.Clone(p.Types)}
	if p.Subsystems != nil {
		subs := slices.Clone(*p.Subsystems)
		out.Subsystems = &subs
	}
	return out
}

func cloneForwards(p *model.ForwardPermissions) *model.ForwardPermissions {
	if p == nil {
		return nil
	}
	return &model.ForwardPermissions{
		DirectTCPIP:    cloneDestinations(p.DirectTCPIP),
		ForwardedTCPIP: cloneDestinations(p.ForwardedTCPIP),
	}
}

func cloneDestinations(in []model.Destination) []model.Destination {
	if in == nil {
		return nil
	}
	out := make([]model.Destination, len(in))
	for i, d := range in {
		out[i] = d
		if d.PortRange != nil {
			pr := *d.PortRange
			out[i].PortRange = &pr
		}
	}
	return out
}

func cloneGlobalRequests(p *model.GlobalRequestPermissions) *model.GlobalRequestPermissions {
	if p == nil {
		return nil
	}
	return &model.GlobalRequestPermissions{Types: slices.Clone(p.Types)}
}

func cloneFilter(f model.FilterPolicy) model.FilterPolicy {
	out := model.FilterPolicy{Mode: f.Mode, ExecMode: f.ExecMode, Rules: slices.Clone(f.Rules)}
	if f.RestrictedExec != nil {
		cmds := make([]model.RestrictedCommand, len(f.RestrictedExec.Commands))
		for i, cmd := range f.RestrictedExec.Commands {
			cmds[i] = cmd
			cmds[i].Argv = slices.Clone(cmd.Argv)
			cmds[i].Args = cloneArgs(cmd.Args)
		}
		out.RestrictedExec = &model.RestrictedExec{Commands: cmds}
	}
	return out
}

func cloneArgs(in []model.ArgumentSpec) []model.ArgumentSpec {
	if in == nil {
		return nil
	}
	out := make([]model.ArgumentSpec, len(in))
	for i, a := range in {
		out[i] = a
		out[i].Values = slices.Clone(a.Values)
	}
	return out
}

func cloneEnforcement(e model.Enforcement) model.Enforcement {
	out := e
	out.PermittedDestinations = cloneDestinations(e.PermittedDestinations)
	if e.Attestation != nil {
		a := *e.Attestation
		out.Attestation = &a
	}
	return out
}

func cloneCache(c *model.CacheHint) *model.CacheHint {
	if c == nil {
		return nil
	}
	return &model.CacheHint{Key: slices.Clone(c.Key), TTLSeconds: c.TTLSeconds}
}

func obligationKinds(obs []model.Obligation) []model.ObligationKind {
	if len(obs) == 0 {
		return nil
	}
	out := make([]model.ObligationKind, len(obs))
	for i, o := range obs {
		out[i] = o.Kind
	}
	return out
}

func orString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func orInt32(v, fallback int32) int32 {
	if v == 0 {
		return fallback
	}
	return v
}
