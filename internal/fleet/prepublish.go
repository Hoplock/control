// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// The pre-publish query (M17).
//
// AN OPERATOR MUST SEE THE MISMATCH BEFORE PUBLISHING, NOT AFTER. The north-bound
// API (0014) needs to answer "which proxies can actually satisfy this policy",
// which is a query over this registry's data; the query is built here and 0014
// exposes it.
//
// Why it is worth building rather than discovering: a rung or a device field the
// enforcing proxy cannot honour is a SKIPPED RUNG on the proxy (proxy D14), not a
// dropped field and not an error. The ladder just gets shorter, invisibly — the
// response says nothing — and on a one-rung ladder the session is denied with
// nobody having authored the denial. A policy naming `device_field.vdom` where
// the enforcing proxy's FortiGate driver does not declare `vdom` is exactly that:
// a ladder that quietly loses a rung in production and says nothing at publish
// time unless this query says it.
//
// It spans BOTH capability sources (M17), and that is the point of the design: the
// proxy's build answers "can this software do it", and the target's record answers
// "can this host take it". Answering only the first would pass a policy that
// fails on every appliance in the estate.

// RouteRequirement is what one allow rule's route needs of the fleet.
//
// It is derived from a bundle by [Requirements] rather than being authored, so an
// operator asks about the policy they wrote and not about a second description of
// it that can drift from it.
type RouteRequirement struct {
	// RuleID names the rule, so an answer points at a line of policy.
	RuleID string
	// Ladder is the credential ladder in authored order. A proxy needs at least
	// ONE entry it can serve: a ladder is a preference plus a fallback, and a
	// proxy that can serve the fallback can serve the route (proxy D14).
	Ladder []LadderRequirement
	// Execution and Reach are the enforcement rungs the route claims. The empty
	// string means the absent-value default, which is proxy-side enforcement and
	// needs nothing of anybody.
	Execution contract.ExecutionRung
	Reach     contract.ReachRung
	// TargetZones, TargetHostnames and TargetLabels are what the rule matched
	// on, which is how an answer knows which targets to check. All three empty
	// means the rule matches every target.
	TargetZones     []Zone
	TargetHostnames []string
	TargetLabels    map[string][]string
}

// LadderRequirement is one credential-ladder entry's demands.
type LadderRequirement struct {
	// Method is the credential method.
	Method contract.TargetAuthMethod
	// Platform is the device platform, on an `ephemeral-account` entry.
	Platform string
	// ExpiryPosture is the posture, where the entry states one.
	ExpiryPosture contract.ExpiryPosture
	// DeviceFields are the `device_field.<name>` names this entry carries, in
	// sorted order. The set is open — the contract enumerates no names — so
	// these are checked against what the driver DECLARES and never against a
	// list of this server's own.
	DeviceFields []string
}

// Requirements extracts what each allow rule in a bundle needs.
//
// Deny rules produce nothing: a deny has no route, so there is nothing a proxy
// could fail to satisfy.
func Requirements(b *model.Bundle) []RouteRequirement {
	if b == nil {
		return nil
	}
	var out []RouteRequirement
	for _, rule := range b.Rules {
		if rule.Route == nil {
			continue
		}
		out = append(out, requirementOf(rule))
	}
	return out
}

func requirementOf(rule model.Rule) RouteRequirement {
	req := RouteRequirement{
		RuleID:    rule.ID,
		Execution: contract.ExecutionRung(rule.Route.Enforcement.Execution),
		Reach:     contract.ReachRung(rule.Route.Enforcement.Reach),
	}
	// A nil target match is unconstrained, which means the rule applies to
	// every target — not to none. Reading it as none would silently answer
	// "nothing to check" for exactly the broadest rules in a bundle.
	if tm := rule.Match.Target; tm != nil {
		for _, z := range tm.Zones {
			req.TargetZones = append(req.TargetZones, Zone(z))
		}
		req.TargetHostnames = slices.Clone(tm.Hostnames)
		if len(tm.Labels) > 0 {
			req.TargetLabels = make(map[string][]string, len(tm.Labels))
			for k, v := range tm.Labels {
				req.TargetLabels[k] = slices.Clone(v)
			}
		}
	}

	for _, entry := range rule.Route.Credentials {
		l := LadderRequirement{
			Method:        contract.TargetAuthMethod(entry.Method),
			Platform:      entry.Platform,
			ExpiryPosture: contract.ExpiryPosture(entry.ExpiryPosture),
		}
		for name := range entry.DeviceFields {
			l.DeviceFields = append(l.DeviceFields, name)
		}
		slices.Sort(l.DeviceFields)
		req.Ladder = append(req.Ladder, l)
	}
	return req
}

// ProxyVerdict is whether one proxy can serve one route.
type ProxyVerdict struct {
	ProxyID string
	Zone    Zone
	// Live is whether the proxy is a routing option at all. A proxy that COULD
	// serve the route but is not live is a different answer from one that
	// cannot, and an operator needs both.
	Live bool
	// OK is whether the proxy can serve at least one ladder entry and the
	// route's rungs.
	OK bool
	// Missing says what it lacks, in a form meant to be read.
	Missing []string
}

// TargetVerdict is whether one target can take a route's enforcement rung.
type TargetVerdict struct {
	Hostname string
	Zone     Zone
	// OK is whether the rungs the route claims are available on this target.
	OK bool
	// Observed reports whether a FRESH capability record contributed. False
	// means the answer came from the fail-safe set alone, which is why an
	// appliance nobody can probe can still be OK on an attested rung.
	Observed bool
	// Missing says which rungs are not available.
	Missing []string
}

// Satisfaction is the answer for one route requirement.
type Satisfaction struct {
	// RuleID names the rule this is about.
	RuleID string
	// Proxies is every proxy in the tenant, with its verdict, in id order.
	Proxies []ProxyVerdict
	// Targets is every target the rule could match, with its verdict, in
	// hostname order.
	Targets []TargetVerdict
}

// Satisfiable reports whether at least one live proxy and, where the route
// claims a target-dependent rung, at least one matching target can serve it.
//
// It is the one-line answer 0014 puts next to a publish button. The detail
// underneath it is what an operator reads when the answer is no.
func (s Satisfaction) Satisfiable() bool {
	proxyOK := false
	for _, p := range s.Proxies {
		if p.OK && p.Live {
			proxyOK = true
			break
		}
	}
	if !proxyOK {
		return false
	}
	if len(s.Targets) == 0 {
		return true
	}
	for _, t := range s.Targets {
		if t.OK {
			return true
		}
	}
	return false
}

// CheckPolicy answers, for each of a policy's routes, which proxies can satisfy
// it and which targets can take its enforcement rung.
//
// Both capability sources are consulted, and they are ANDed: a route needs a
// proxy whose build implements it AND a target that can take it. Answering from
// the proxy alone would pass a policy that fails on every appliance; answering
// from the target alone would pass one no build in the fleet implements.
func (r *Registry) CheckPolicy(ctx context.Context, tenant store.Tenant, reqs []RouteRequirement) ([]Satisfaction, error) {
	now := r.now()

	proxies, err := r.st.Proxies().List(ctx, tenant)
	if err != nil {
		return nil, err
	}
	var subs map[string]time.Time
	if r.subs != nil {
		subs, err = r.subs.LiveSubscriptions(ctx, tenant)
		if err != nil {
			return nil, err
		}
	}
	// Targets are read whole because the question is "which of my estate can
	// take this", which no index narrows: a rule matching on labels selects an
	// arbitrary subset. It is an authoring-time query and not on the decision
	// path (M5), so the scan is the right shape here and would not be there.
	targets, err := r.st.Targets().ListByLabels(ctx, tenant, nil)
	if err != nil {
		return nil, err
	}
	caps, err := r.st.TargetCapabilities().List(ctx, tenant)
	if err != nil {
		return nil, err
	}

	byTarget := make(map[TargetCapabilityKey]TargetCapabilities, len(caps))
	for _, rec := range caps {
		got := targetCapabilitiesFromStore(rec)
		byTarget[got.Key] = got
	}

	out := make([]Satisfaction, 0, len(reqs))
	for _, req := range reqs {
		sat := Satisfaction{RuleID: req.RuleID}
		for _, p := range proxies {
			sat.Proxies = append(sat.Proxies, proxyVerdict(p, req, r.isLive(p, subs, now)))
		}
		for _, t := range targets {
			if !matchesTarget(t, req) {
				continue
			}
			sat.Targets = append(sat.Targets, targetVerdict(t, req, byTarget, now, r.liveness.TargetCapabilityTTL))
		}
		out = append(out, sat)
	}
	return out, nil
}

// proxyVerdict adapts a stored row onto [CheckProxy].
func proxyVerdict(p store.Proxy, req RouteRequirement, live bool) ProxyVerdict {
	return CheckProxy(p.ID, Zone(p.Zone), UnmarshalCapabilities(p.DeclaredCapabilities), live, req)
}

// CheckProxy reports whether one proxy's declared capabilities can serve a route.
//
// It is a PURE function of the declared set, so the answer can be tested without
// a fleet and 0014 can render it for a proxy it already holds. Nothing here reads
// the target's side: that is the second source, and the two are ANDed by
// [Registry.CheckPolicy].
func CheckProxy(proxyID string, zone Zone, caps Capabilities, live bool, req RouteRequirement) ProxyVerdict {
	v := ProxyVerdict{ProxyID: proxyID, Zone: zone, Live: live}

	// A ladder needs one servable entry, not all of them: that is what a ladder
	// IS (proxy D14). What the operator needs to see when none is servable is
	// why each one failed, so every entry's reason is reported.
	servable := len(req.Ladder) == 0
	for i, entry := range req.Ladder {
		missing := ladderGaps(caps, entry)
		if len(missing) == 0 {
			servable = true
			continue
		}
		for _, m := range missing {
			v.Missing = append(v.Missing, "ladder["+strconv.Itoa(i)+"]: "+m)
		}
	}
	if servable {
		// The per-entry reasons are only interesting when the ladder as a whole
		// cannot be served. Keeping them otherwise would report a working
		// policy as a problem, which is how a check stops being read.
		v.Missing = nil
	}

	rungOK := true
	if req.Execution != "" && !caps.HasExecutionRung(req.Execution) {
		v.Missing = append(v.Missing, "execution rung "+string(req.Execution)+" not implemented by this build")
		rungOK = false
	}
	if req.Reach != "" && !caps.HasReachRung(req.Reach) {
		v.Missing = append(v.Missing, "reach rung "+string(req.Reach)+" not implemented by this build")
		rungOK = false
	}

	v.OK = servable && rungOK
	return v
}

// ladderGaps reports why a build cannot serve one ladder entry.
func ladderGaps(caps Capabilities, entry LadderRequirement) []string {
	var missing []string
	if entry.Method != "" && !caps.HasCredentialMethod(entry.Method) {
		missing = append(missing, "credential method "+string(entry.Method)+" not implemented")
	}
	if entry.Platform != "" && !caps.HasPlatform(entry.Platform) {
		missing = append(missing, "no driver for platform "+entry.Platform)
	}
	if entry.ExpiryPosture != "" && !caps.HasExpiryPosture(entry.ExpiryPosture) {
		missing = append(missing, "expiry posture "+string(entry.ExpiryPosture)+" not supported")
	}
	for _, name := range entry.DeviceFields {
		// THE DEVICE-FIELD CHECK THIS QUERY EXISTS FOR. A field the driver does
		// not declare costs the whole rung on the proxy, silently, and nothing
		// downstream can reconstruct why.
		if !caps.HasDeviceField(entry.Platform, name) {
			missing = append(missing, "driver for "+entry.Platform+" does not declare device_field."+name)
		}
	}
	return missing
}

// targetVerdict is the per-target half: can this host take the rung the route
// claims?
func targetVerdict(t store.Target, req RouteRequirement, byTarget map[TargetCapabilityKey]TargetCapabilities, now time.Time, ttl time.Duration) TargetVerdict {
	v := TargetVerdict{Hostname: t.Hostname, Zone: Zone(t.Zone)}

	// A target is observed through a driver, so the record is looked up by the
	// platform the route names as well as by the host. A route with no platform
	// looks for the platform-less record, which is what a plain Linux host
	// reports under.
	rungs, observed := bestRungs(t, req, byTarget, now, ttl)
	v.Observed = observed

	ok := true
	if req.Execution != "" && !rungs.AllowsExecution(req.Execution) {
		v.Missing = append(v.Missing, "execution rung "+string(req.Execution)+" not available on this target")
		ok = false
	}
	if req.Reach != "" && !rungs.AllowsReach(req.Reach) {
		v.Missing = append(v.Missing, "reach rung "+string(req.Reach)+" not available on this target")
		ok = false
	}
	v.OK = ok
	return v
}

// bestRungs resolves the rungs available on a target for a requirement.
//
// It tries each platform the ladder names and then the platform-less record, and
// takes the union: a route whose ladder offers two platforms may be served
// through either, so a rung available through one is available.
func bestRungs(t store.Target, req RouteRequirement, byTarget map[TargetCapabilityKey]TargetCapabilities, now time.Time, ttl time.Duration) (TargetRungs, bool) {
	platforms := []string{""}
	for _, entry := range req.Ladder {
		if entry.Platform != "" && !slices.Contains(platforms, entry.Platform) {
			platforms = append(platforms, entry.Platform)
		}
	}

	union := ResolveTargetRungs(nil, now, ttl)
	observed := false
	for _, platform := range platforms {
		rec, ok := byTarget[TargetCapabilityKey{Hostname: t.Hostname, Platform: platform}]
		var resolved TargetRungs
		if ok {
			resolved = ResolveTargetRungs(&rec, now, ttl)
		} else {
			resolved = ResolveTargetRungs(nil, now, ttl)
		}
		if resolved.Observed {
			observed = true
		}
		for _, r := range resolved.Execution {
			if !union.AllowsExecution(r) {
				union.Execution = append(union.Execution, r)
			}
		}
		for _, r := range resolved.Reach {
			if !union.AllowsReach(r) {
				union.Reach = append(union.Reach, r)
			}
		}
	}
	union.Observed = observed
	return union, observed
}

// matchesTarget reports whether a rule's target match could select this target.
//
// It is deliberately the SAME shape as the compiler's match, not a second
// opinion: an answer that checked different targets from the ones the rule will
// actually apply to would be worse than no answer, because an operator would
// trust it.
func matchesTarget(t store.Target, req RouteRequirement) bool {
	if len(req.TargetHostnames) > 0 && !slices.Contains(req.TargetHostnames, t.Hostname) {
		return false
	}
	if len(req.TargetZones) > 0 && !slices.Contains(req.TargetZones, Zone(t.Zone)) {
		return false
	}
	for key, permitted := range req.TargetLabels {
		got, ok := t.Labels[key]
		if !ok || !slices.Contains(permitted, got) {
			return false
		}
	}
	return true
}
