// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package compile

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/hoplock/control/internal/policy/model"
)

// Compile turns a validated bundle into a decision program.
//
// Compilation is where an authoring mistake becomes an error instead of a
// production surprise (M3). Every check here is one this server can make
// knowing nothing about the fleet, and every one of them is a route that could
// otherwise only fail at connect time, in front of a user.
//
// What compilation deliberately does NOT decide is whether a given proxy or
// target can *provide* something correctly authored — a rung, a device-field
// name, a platform. That is a capability question answered from the fleet
// registry (M17), on the issue path in 0008 and at publish time in 0014. The
// split matters: an unrecognised device-field name is a skipped rung on the
// proxy, not an error, and a compiler that refused one would make an estate's
// own driver unauthorable.
//
// Every rejection names the rule, the line, and what to do instead, and carries
// a stable code with typed parameters beside the English (M21). This text is a
// product surface: it is what a policy author sees.
func Compile(b *model.Bundle) (*Program, error) {
	c := &compiler{bundle: b}
	c.rs = append(c.rs, b.Validate()...)

	prog := &Program{
		tenant:   b.Tenant,
		digest:   b.Digest(),
		location: b.Location(),
		rules:    make([]Rule, 0, len(b.Rules)),
	}
	for i := range b.Rules {
		prog.rules = append(prog.rules, c.rule(&b.Rules[i]))
	}
	c.checkReachability(b.Rules)

	if len(c.rs) > 0 {
		return nil, c.rs.Sorted()
	}
	return prog, nil
}

type compiler struct {
	bundle *model.Bundle
	rs     model.Rejections
}

func (c *compiler) add(code model.Code, rule string, line int, kv ...string) {
	c.rs = append(c.rs, model.Reject(code, rule, line, kv...))
}

func (c *compiler) rule(r *model.Rule) Rule {
	out := Rule{
		id:          r.ID,
		line:        r.Line(),
		effect:      r.Effect,
		reason:      r.Reason,
		route:       r.Route,
		obligations: r.Obligations,
		cache:       r.Cache,
		match:       c.compileMatch(r),
	}
	c.checkObligations(r)
	c.checkCache(r)
	if r.Route != nil {
		c.checkRoute(r)
	}
	return out
}

// ---------------------------------------------------------------------------
// The match
// ---------------------------------------------------------------------------

func (c *compiler) compileMatch(r *model.Rule) matcher {
	var m matcher
	if s := r.Match.Subject; s != nil {
		m.subject = c.compileSubject(r, s)
	}
	if d := r.Match.Device; d != nil {
		m.device = c.compileDevice(r, d)
	}
	if x := r.Match.Context; x != nil {
		m.context = c.compileContext(r, x)
	}
	if t := r.Match.Target; t != nil {
		m.target = c.compileTarget(r, t)
	}
	if g := r.Match.Grant; g.Constrained() {
		m.grant = &grantMatcher{required: g.Required, scopes: g.Scopes, origins: g.Origins}
		c.emptyTerm(r, model.AxisGrant, "scopes", len(g.Scopes), g.Scopes == nil)
		c.emptyTerm(r, model.AxisGrant, "origins", len(g.Origins), g.Origins == nil)
	}
	return m
}

func (c *compiler) compileSubject(r *model.Rule, s *model.SubjectMatch) *subjectMatcher {
	c.emptyTerm(r, model.AxisSubject, "ids", len(s.IDs), s.IDs == nil)
	c.emptyTerm(r, model.AxisSubject, "sources", len(s.Sources), s.Sources == nil)
	c.emptyTerm(r, model.AxisSubject, "groups", len(s.Groups), s.Groups == nil)
	c.emptyTerm(r, model.AxisSubject, "auth_methods", len(s.AuthMethods), s.AuthMethods == nil)
	for _, g := range s.Groups {
		c.checkGroup(r, g)
	}
	out := &subjectMatcher{
		ids:         s.IDs,
		sources:     s.Sources,
		groups:      s.Groups,
		claims:      s.Claims,
		authMethods: s.AuthMethods,
		mfa:         s.MFA,
	}
	if len(s.Claims) > 0 {
		out.claimKeys = sortedKeys(s.Claims)
		for _, k := range out.claimKeys {
			c.emptyTerm(r, model.AxisSubject, "claims."+k, len(s.Claims[k]), false)
		}
	}
	return out
}

func (c *compiler) compileDevice(r *model.Rule, d *model.DeviceMatch) *deviceMatcher {
	out := &deviceMatcher{required: d.Required, posture: d.Posture}
	if len(d.Posture) > 0 {
		out.postureKeys = sortedKeys(d.Posture)
		for _, k := range out.postureKeys {
			c.emptyTerm(r, model.AxisDevice, "posture."+k, len(d.Posture[k]), false)
		}
	}
	return out
}

func (c *compiler) compileContext(r *model.Rule, x *model.ContextMatch) *contextMatcher {
	c.emptyTerm(r, model.AxisContext, "days", len(x.Days), x.Days == nil)
	c.emptyTerm(r, model.AxisContext, "source_cidrs", len(x.SourceCIDRs), x.SourceCIDRs == nil)
	c.emptyTerm(r, model.AxisContext, "proxy_ids", len(x.ProxyIDs), x.ProxyIDs == nil)

	out := &contextMatcher{window: x.TimeOfDay, proxyIDs: x.ProxyIDs, loc: c.bundle.Location()}
	for _, d := range x.Days {
		wd, ok := d.Weekday()
		if !ok {
			// Unreachable through Parse, which refuses an unknown day at
			// the enum, but a hand-built bundle gets the same answer.
			c.add(model.CodeDocumentMalformed, r.ID, r.Line(), "detail", "unknown day "+strconv.Quote(string(d)))
			continue
		}
		out.days = append(out.days, wd)
	}
	for _, s := range x.SourceCIDRs {
		p, err := parseNetwork(s)
		if err != nil {
			c.add(model.CodeMatchInvalidCIDR, r.ID, r.Line(), "cidr", s)
			continue
		}
		out.prefixes = append(out.prefixes, p)
	}
	return out
}

func (c *compiler) compileTarget(r *model.Rule, t *model.TargetMatch) *targetMatcher {
	c.emptyTerm(r, model.AxisTarget, "hostnames", len(t.Hostnames), t.Hostnames == nil)
	c.emptyTerm(r, model.AxisTarget, "zones", len(t.Zones), t.Zones == nil)
	for _, h := range t.Hostnames {
		if !model.ValidHostPattern(h) {
			c.add(model.CodeMatchInvalidHostname, r.ID, r.Line(), "pattern", h)
		}
	}
	out := &targetMatcher{hostnames: t.Hostnames, labels: t.Labels, zones: t.Zones}
	if len(t.Labels) > 0 {
		out.labelKeys = sortedKeys(t.Labels)
		for _, k := range out.labelKeys {
			c.emptyTerm(r, model.AxisTarget, "labels."+k, len(t.Labels[k]), false)
			c.checkLabel(r, k, t.Labels[k])
		}
	}
	return out
}

// checkGroup refuses a reference to a group the bundle never declared. A typo
// here is a rule that silently never fires, which is the most expensive kind of
// policy bug: nothing is broken, somebody just does not have the access they
// were given.
func (c *compiler) checkGroup(r *model.Rule, g string) {
	if !slices.Contains(c.bundle.Groups, g) {
		c.add(model.CodeMatchUnknownGroup, r.ID, r.Line(), "group", g)
	}
}

func (c *compiler) checkLabel(r *model.Rule, key string, values []string) {
	declared, ok := c.bundle.Labels[key]
	if !ok {
		c.add(model.CodeMatchUnknownLabelKey, r.ID, r.Line(), "label_key", key)
		return
	}
	for _, v := range values {
		if !slices.Contains(declared, v) {
			c.add(model.CodeMatchUnknownLabel, r.ID, r.Line(), "label_key", key, "label_value", v)
		}
	}
}

// emptyTerm reports a term the author opened and then named nothing in. An
// empty list matches nothing, so it is almost never what was meant — and where
// it is, deleting the key says it more clearly.
func (c *compiler) emptyTerm(r *model.Rule, axis model.MatchAxis, term string, n int, absent bool) {
	if n == 0 && !absent {
		c.add(model.CodeMatchEmptyTerm, r.ID, r.Line(), "axis", string(axis), "term", term)
	}
}

// ---------------------------------------------------------------------------
// Obligations and the cache hint
// ---------------------------------------------------------------------------

func (c *compiler) checkObligations(r *model.Rule) {
	seen := make([]model.ObligationKind, 0, len(r.Obligations))
	for i, o := range r.Obligations {
		idx := strconv.Itoa(i + 1)
		if o.Kind == "" {
			c.add(model.CodeObligationKindMissing, r.ID, r.Line(), "index", idx)
			continue
		}
		if slices.Contains(seen, o.Kind) {
			c.add(model.CodeObligationDuplicate, r.ID, r.Line(), "obligation", string(o.Kind))
		}
		seen = append(seen, o.Kind)

		if r.Effect == model.EffectDeny {
			c.add(model.CodeObligationOnDeny, r.ID, r.Line(), "obligation", string(o.Kind))
			continue
		}
		if len(o.ApproverGroups) > 0 {
			if o.Kind != model.ObligationRequireApproval {
				c.add(model.CodeObligationContradicts, r.ID, r.Line(),
					"obligation", string(o.Kind),
					"detail", "approver groups belong to `require-approval` and mean nothing on this obligation")
			}
			for _, g := range o.ApproverGroups {
				c.checkGroup(r, g)
			}
		}
		// The one obligation that can contradict the route it rides on:
		// "record this session" beside a route that explicitly says the
		// proxy need not capture it. One rule, two answers, and the proxy
		// would have to pick.
		if o.Kind == model.ObligationRecordSession && r.Route != nil &&
			r.Route.RequireSessionCapture != nil && !*r.Route.RequireSessionCapture {
			c.add(model.CodeObligationContradicts, r.ID, r.Line(),
				"obligation", string(o.Kind),
				"detail", "the route sets `require_session_capture: false`, so the rule both requires and waives the recording")
		}
	}
}

func (c *compiler) checkCache(r *model.Rule) {
	if r.Cache == nil {
		return
	}
	if r.Effect == model.EffectDeny {
		c.add(model.CodeCacheOnDeny, r.ID, r.Line())
		return
	}
	if len(r.Cache.Key) == 0 {
		c.add(model.CodeCacheKeyEmpty, r.ID, r.Line())
	} else {
		seen := make([]model.CacheKeyComponent, 0, len(r.Cache.Key))
		for _, k := range r.Cache.Key {
			if slices.Contains(seen, k) {
				c.add(model.CodeCacheKeyDuplicate, r.ID, r.Line(), "component", string(k))
			}
			seen = append(seen, k)
		}
		// A key that does not name the subject selects a sharing scope
		// wider than one identity, and a shared entry serves one user
		// another user's policy (PLAN §5.4).
		if !slices.Contains(r.Cache.Key, model.CacheKeySubject) {
			c.add(model.CodeCacheKeyNotIdentityBound, r.ID, r.Line())
		}
	}
	if r.Cache.TTLSeconds <= 0 {
		c.add(model.CodeCacheTTLInvalid, r.ID, r.Line(), "ttl", strconv.Itoa(int(r.Cache.TTLSeconds)))
	}
}

// ---------------------------------------------------------------------------
// The route
// ---------------------------------------------------------------------------

func (c *compiler) checkRoute(r *model.Rule) {
	rt := r.Route
	if rt.Intent == "" {
		c.add(model.CodeRouteIntentMissing, r.ID, r.Line())
	}
	if rt.Channels == nil {
		c.add(model.CodeRouteChannelsMissing, r.ID, r.Line())
	}
	c.checkChannelConstraints(r)
	c.checkFilter(r)
	c.checkCredentials(r)
	c.checkEnforcement(r)

	if rt.MaxSessionDuration.Duration() < 0 {
		c.add(model.CodeRouteDeadlineInvalid, r.ID, r.Line(), "duration", rt.MaxSessionDuration.String())
	}
	if rt.Concurrency.MaxSessionsPerSubject < 0 {
		c.add(model.CodeRouteConcurrencyInvalid, r.ID, r.Line(), "field", "max_sessions_per_subject")
	}
	if rt.Concurrency.MaxSessionsPerTarget < 0 {
		c.add(model.CodeRouteConcurrencyInvalid, r.ID, r.Line(), "field", "max_sessions_per_target")
	}
}

// checkChannelConstraints refuses an axis the route opens and then leaves
// unconstrained. A `direct-tcpip` channel's whole meaning is the destination in
// its payload, and a subsystem permission that names no subsystem cannot deny
// sftp on its own — so an unconstrained axis is a mistake, not a wildcard, and
// the compiler says so rather than quietly opening the estate.
func (c *compiler) checkChannelConstraints(r *model.Rule) {
	rt := r.Route
	channels := []model.ChannelType{}
	if rt.Channels != nil {
		channels = *rt.Channels
	}
	if slices.Contains(channels, model.ChannelDirectTCPIP) &&
		(rt.Forwards == nil || len(rt.Forwards.DirectTCPIP) == 0) {
		c.add(model.CodeRouteForwardsUnconstrained, r.ID, r.Line(),
			"channel", string(model.ChannelDirectTCPIP), "list", "direct_tcpip")
	}
	if slices.Contains(channels, model.ChannelForwardedTCPIP) &&
		(rt.Forwards == nil || len(rt.Forwards.ForwardedTCPIP) == 0) {
		c.add(model.CodeRouteForwardsUnconstrained, r.ID, r.Line(),
			"channel", string(model.ChannelForwardedTCPIP), "list", "forwarded_tcpip")
	}
	if rt.Requests != nil && rt.Requests.Subsystems != nil && len(*rt.Requests.Subsystems) == 0 {
		c.add(model.CodeRouteSubsystemsMissing, r.ID, r.Line())
	}
	if rt.Forwards != nil {
		c.checkDestinations(r, rt.Forwards.DirectTCPIP)
		c.checkDestinations(r, rt.Forwards.ForwardedTCPIP)
	}
	c.checkDestinations(r, rt.Enforcement.PermittedDestinations)
}

func (c *compiler) checkDestinations(r *model.Rule, dests []model.Destination) {
	for _, d := range dests {
		if !validDestination(d) {
			c.add(model.CodeRouteInvalidDestination, r.ID, r.Line(), "destination", d.String())
		}
	}
}

func (c *compiler) checkFilter(r *model.Rule) {
	f := r.Route.Filter
	if f.Mode == model.FilterModeUnset {
		c.add(model.CodeRouteFilterModeMissing, r.ID, r.Line())
	}
	// The two exec tiers are alternatives, never layers (proxy D12).
	if len(f.Rules) > 0 && f.RestrictedExec != nil {
		c.add(model.CodeRouteFilterBothTiers, r.ID, r.Line())
	}
	if f.RestrictedExec != nil && f.ExecMode != model.ExecModeRestricted {
		c.add(model.CodeRouteExecModeMismatch, r.ID, r.Line())
	}
	if f.ExecMode == model.ExecModeRestricted &&
		(f.RestrictedExec == nil || len(f.RestrictedExec.Commands) == 0) {
		c.add(model.CodeRouteRestrictedExecEmpty, r.ID, r.Line())
	}
	for i, fr := range f.Rules {
		if detail := filterRuleProblem(fr); detail != "" {
			c.add(model.CodeRouteFilterRuleInvalid, r.ID, r.Line(),
				"index", strconv.Itoa(i+1), "detail", detail)
		}
	}
	if f.RestrictedExec != nil {
		for i, cmd := range f.RestrictedExec.Commands {
			if detail := restrictedCommandProblem(cmd); detail != "" {
				c.add(model.CodeRouteRestrictedCmdInvalid, r.ID, r.Line(),
					"index", strconv.Itoa(i+1), "executable", cmd.Executable, "detail", detail)
			}
		}
	}
}

func (c *compiler) checkCredentials(r *model.Rule) {
	rt := r.Route
	if rt.Credentials == nil {
		return
	}
	if len(rt.Credentials) == 0 {
		c.add(model.CodeRouteLadderEmpty, r.ID, r.Line())
		return
	}
	for i, e := range rt.Credentials {
		c.checkCredentialEntry(r, i+1, e)
	}
}

func (c *compiler) checkCredentialEntry(r *model.Rule, index int, e model.CredentialEntry) {
	idx := strconv.Itoa(index)
	if e.Method == "" {
		c.add(model.CodeCredentialMethodMissing, r.ID, r.Line(), "index", idx)
		return
	}
	method := string(e.Method)

	// Every method the contract defines requires `params.username`, and the
	// proxy refuses a route that omits it at the first authorize call. This
	// is where the author can still fix it. It is never filled in from the
	// identity's `login`: that is a client-typed string and precisely the
	// substitution the contract closed.
	//
	// There is no policy_version to gate this on and there cannot be — a
	// tightening adds no field and changes no field's meaning, so it is not
	// expressible through the version at all.
	switch e.Username.Source {
	case model.UsernameUnset:
		c.add(model.CodeCredentialUsernameMissing, r.ID, r.Line(), "index", idx, "method", method)
	case model.UsernameLiteral:
		if strings.TrimSpace(e.Username.Value) == "" {
			c.add(model.CodeCredentialUsernameEmpty, r.ID, r.Line(), "index", idx)
		}
	case model.UsernameSubject, model.UsernameSubjectLocalPart:
	}

	c.checkParamScope(r, idx, method, "key_type", e.KeyType != "",
		e.Method == model.CredentialEphemeralUser)
	c.checkParamScope(r, idx, method, "credential_ref", e.CredentialRef != "",
		e.Method == model.CredentialBrokeredKey)
	c.checkParamScope(r, idx, method, "platform", e.Platform != "",
		e.Method == model.CredentialEphemeralAccount)
	c.checkParamScope(r, idx, method, "credential_kind", e.CredentialKind != model.CredentialKindUnset,
		e.Method == model.CredentialEphemeralAccount)
	c.checkParamScope(r, idx, method, "expiry_posture", e.ExpiryPosture != model.ExpiryPostureUnset,
		e.Method == model.CredentialEphemeralAccount)
	c.checkParamScope(r, idx, method, "lifetime_seconds", e.LifetimeSeconds != 0,
		e.Method == model.CredentialEphemeralUser || e.Method == model.CredentialEphemeralAccount)

	if e.LifetimeSeconds < 0 {
		c.add(model.CodeCredentialLifetimeInvalid, r.ID, r.Line(),
			"index", idx, "lifetime", strconv.Itoa(int(e.LifetimeSeconds)))
	}

	if e.Method == model.CredentialEphemeralAccount {
		if e.Platform == "" {
			c.add(model.CodeCredentialPlatformMissing, r.ID, r.Line(), "index", idx)
		}
		if e.CredentialKind == model.CredentialKindUnset {
			c.add(model.CodeCredentialKindMissing, r.ID, r.Line(), "index", idx)
		}
		switch e.ExpiryPosture {
		case model.ExpiryPostureUnset:
			c.add(model.CodeCredentialPostureMissing, r.ID, r.Line(), "index", idx)
		case model.ExpiryPostureTargetEnforced, model.ExpiryPostureProxyEnforced:
			if e.LifetimeSeconds <= 0 {
				c.add(model.CodeCredentialLifetimeMissing, r.ID, r.Line(),
					"index", idx, "posture", string(e.ExpiryPosture))
			}
		case model.ExpiryPostureAcceptedRisk:
		}
	}

	c.checkDeviceFields(r, idx, e)
}

func (c *compiler) checkParamScope(r *model.Rule, idx, method, param string, set, permitted bool) {
	if set && !permitted {
		c.add(model.CodeCredentialParamWrongMethod, r.ID, r.Line(),
			"index", idx, "param", param, "method", method)
	}
}

// checkDeviceFields validates the shape of the open `device_field.<name>`
// namespace and nothing else. The contract validates exactly this and so does
// the compiler: shape is checkable here, meaning is the driver's. An
// unrecognised *name* is not rejected — that is a capability question (M17) and,
// on the proxy, a skipped rung.
//
// Device fields are policy metadata, never credential material: nothing here
// treats one as a secret to broker, and nothing reads one back out as an
// authorisation input.
func (c *compiler) checkDeviceFields(r *model.Rule, idx string, e model.CredentialEntry) {
	if len(e.DeviceFields) == 0 {
		return
	}
	if e.Method != model.CredentialEphemeralAccount {
		for _, name := range sortedKeys(e.DeviceFields) {
			c.add(model.CodeDeviceFieldWrongMethod, r.ID, r.Line(),
				"index", idx, "field", name, "method", string(e.Method))
		}
		return
	}
	if len(e.DeviceFields) > model.DeviceFieldMaxPerEntry {
		c.add(model.CodeDeviceFieldCount, r.ID, r.Line(),
			"index", idx, "count", strconv.Itoa(len(e.DeviceFields)),
			"max", strconv.Itoa(model.DeviceFieldMaxPerEntry))
	}
	for _, name := range sortedKeys(e.DeviceFields) {
		if !model.ValidDeviceFieldName(name) {
			c.add(model.CodeDeviceFieldName, r.ID, r.Line(),
				"index", idx, "field", name, "max", strconv.Itoa(model.DeviceFieldMaxNameLen))
		}
		if !model.ValidDeviceFieldValue(e.DeviceFields[name]) {
			c.add(model.CodeDeviceFieldValue, r.ID, r.Line(),
				"index", idx, "field", name, "max", strconv.Itoa(model.DeviceFieldMaxValueLen))
		}
	}
}

// checkEnforcement refuses a rung that contradicts the rest of the rule.
//
// These are internal-consistency checks the compiler can make with no knowledge
// of the fleet, and the proxy refuses a response that disagrees with itself —
// so a policy that can only fail at connect time fails in front of a user.
func (c *compiler) checkEnforcement(r *model.Rule) {
	rt := r.Route
	e := rt.Enforcement

	switch e.Execution {
	case model.ExecutionNoInteractiveShell:
		if rt.Requests == nil {
			c.add(model.CodeEnforcementRequestsMissing, r.ID, r.Line())
		} else {
			for _, t := range []model.RequestType{model.RequestShell, model.RequestPTYReq} {
				if rt.Requests.Permits(t) {
					c.add(model.CodeEnforcementShellPermitted, r.ID, r.Line(), "request", string(t))
				}
			}
		}
	case model.ExecutionAccountRestricted, model.ExecutionAccountConfined:
		if rt.Filter.ExecMode != model.ExecModeRestricted {
			c.add(model.CodeEnforcementExecModeRequired, r.ID, r.Line(), "rung", string(e.Execution))
		}
	case model.ExecutionPlatformAuthorized:
		if strings.TrimSpace(e.PlatformRole) == "" {
			c.add(model.CodeEnforcementRoleMissing, r.ID, r.Line())
		}
	case model.ExecutionRungUnset, model.ExecutionProxyInspected, model.ExecutionPlatformAttested:
	}

	if e.PlatformRole != "" && e.Execution != model.ExecutionPlatformAuthorized {
		c.add(model.CodeEnforcementRoleUnexpected, r.ID, r.Line(), "rung", rungOrAbsent(string(e.Execution)))
	}

	switch e.Reach {
	case model.ReachAccountEgressRestricted:
		if len(e.PermittedDestinations) == 0 {
			c.add(model.CodeEnforcementDestsRequired, r.ID, r.Line())
		}
	case model.ReachRungUnset, model.ReachProxyChannelPolicy,
		model.ReachAccountNetworkIsolated, model.ReachPlatformAttested:
	}

	// An attested rung is a claim this system does not verify, so it must be
	// attributable; an applied rung is configured by the proxy and has
	// nobody to attribute.
	attested := e.Execution.Attested() || e.Reach.Attested()
	switch {
	case attested && (e.Attestation == nil || e.Attestation.AssertedBy == "" || e.Attestation.Reference == ""):
		axis := "execution"
		if !e.Execution.Attested() {
			axis = "reach"
		}
		c.add(model.CodeEnforcementAttestationMissing, r.ID, r.Line(), "axis", axis)
	case !attested && e.Attestation != nil:
		c.add(model.CodeEnforcementAttestationUnexpected, r.ID, r.Line(),
			"execution", rungOrAbsent(string(e.Execution)),
			"reach", rungOrAbsent(string(e.Reach)))
	}

	c.checkAppliedRung(r)
}

// checkAppliedRung refuses an applied rung on a route the proxy could never
// apply it to. An applied rung needs the proxy to administer the account, which
// only `ephemeral-user` and `ephemeral-account` do, so the proxy refuses such a
// response outright.
//
// An *attested* rung on that same route is valid and must not be rejected: it is
// how an appliance carries a real enforcement claim, and a compiler that refused
// it would make the appliance estate unauthorable. A route with no ladder at all
// is not judged either — the proxy uses its locally configured method and this
// server does not know which.
func (c *compiler) checkAppliedRung(r *model.Rule) {
	ladder := r.Route.Credentials
	if len(ladder) == 0 {
		return
	}
	for _, e := range ladder {
		if e.Method.Provisions() {
			return
		}
	}
	e := r.Route.Enforcement
	if e.Execution.Applied() {
		c.add(model.CodeEnforcementAppliedUnprovisioned, r.ID, r.Line(),
			"axis", "execution", "rung", string(e.Execution))
	}
	if e.Reach.Applied() {
		c.add(model.CodeEnforcementAppliedUnprovisioned, r.ID, r.Line(),
			"axis", "reach", "rung", string(e.Reach))
	}
}

// ---------------------------------------------------------------------------
// Small shape helpers
// ---------------------------------------------------------------------------

func rungOrAbsent(s string) string {
	if s == "" {
		return "absent"
	}
	return s
}

// parseNetwork accepts a CIDR prefix or a bare address, which is the same thing
// with a full-length mask. An author writing one host should not have to write
// `/32`.
func parseNetwork(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func validDestination(d model.Destination) bool {
	if d.Host == "" {
		return false
	}
	if d.Port != 0 && d.PortRange != nil {
		return false
	}
	if d.Port != 0 && !validPort(d.Port) {
		return false
	}
	if d.PortRange != nil {
		if !validPort(d.PortRange.From) || !validPort(d.PortRange.To) || d.PortRange.From > d.PortRange.To {
			return false
		}
	}
	if _, err := netip.ParsePrefix(d.Host); err == nil {
		return true
	}
	if _, err := netip.ParseAddr(d.Host); err == nil {
		return true
	}
	return model.ValidHostPattern(d.Host)
}

func validPort(p int32) bool { return p >= 1 && p <= 65535 }

func filterRuleProblem(r model.FilterRule) string {
	switch {
	case strings.TrimSpace(r.Match) == "":
		return "it matches nothing; write the command pattern"
	case r.Action == "":
		return "it names no action"
	default:
		return ""
	}
}

func restrictedCommandProblem(cmd model.RestrictedCommand) string {
	if strings.TrimSpace(cmd.Executable) == "" {
		return "it names no executable"
	}
	switch cmd.Form {
	case model.CommandFormUnset:
		return "it names no form; use `exact` or `positional`"
	case model.CommandFormExact:
		if len(cmd.Argv) == 0 {
			return "form `exact` names the whole argv, and none is given"
		}
		if len(cmd.Args) > 0 {
			return "form `exact` takes `argv`, not `args`"
		}
	case model.CommandFormPositional:
		if len(cmd.Argv) > 0 {
			return "form `positional` takes `args`, not `argv`"
		}
		for i, a := range cmd.Args {
			if problem := argumentProblem(a); problem != "" {
				return "argument " + strconv.Itoa(i+1) + ": " + problem
			}
		}
	}
	return ""
}

func argumentProblem(a model.ArgumentSpec) string {
	switch a.Kind {
	case model.ArgumentKindUnset:
		return "it names no kind"
	case model.ArgumentKindLiteral, model.ArgumentKindPrefix:
		if a.Value == "" {
			return "kind `" + string(a.Kind) + "` needs a value"
		}
		if len(a.Values) > 0 {
			return "kind `" + string(a.Kind) + "` takes `value`, not `values`"
		}
	case model.ArgumentKindOneOf:
		if len(a.Values) == 0 {
			return "kind `oneof` needs values"
		}
		if a.Value != "" {
			return "kind `oneof` takes `values`, not `value`"
		}
	case model.ArgumentKindAny:
		if a.Value != "" || len(a.Values) > 0 {
			return "kind `any` is unconstrained and takes neither `value` nor `values`"
		}
	}
	return ""
}
