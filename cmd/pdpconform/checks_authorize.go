// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/hoplock/control/internal/contract"
)

const (
	groupAuthorize   = "authorize (POST /v1/authorize)"
	groupVocabulary  = "vocabulary negotiation (POST /v1/authorize)"
	groupDeviceField = "device fields (device_field.<name>)"
	groupDefaults    = "absent-value defaults (POST /v1/authorize)"
)

// The policy fields of AuthorizeResponse: the keys that carry policy and are
// therefore what a thinned answer drops. `decision_id`, `target`, and
// `route_type` are not on this list — they are not policy and a server that
// still sends them at a lower version has thinned nothing.
var policyFields = []string{
	"permitted_channels", "permitted_requests", "permitted_forwards",
	"permitted_global_requests", "target_auth_ladder", "algorithm_profile",
	"filter_policy", "enforcement", "session_deadline",
	"require_session_capture", "grant_context", "concurrency", "hop",
}

var deviceFieldName = regexp.MustCompile(contract.DeviceFieldNamePattern)

// The rungs the suite declares on every authorize request. They are the
// contract's own vocabulary, listed rather than derived so that a rung added
// upstream is a line to add here — and a suite that silently stopped declaring
// one would make the server withhold it and the assertion pass for the wrong
// reason.
var (
	executionRungs = []string{
		string(contract.ExecutionProxyInspected),
		string(contract.ExecutionNoInteractiveShell),
		string(contract.ExecutionAccountRestricted),
		string(contract.ExecutionAccountConfined),
		string(contract.ExecutionPlatformAuthorized),
		string(contract.ExecutionPlatformAttested),
	}
	reachRungs = []string{
		string(contract.ReachProxyChannelPolicy),
		string(contract.ReachAccountEgressRestricted),
		string(contract.ReachAccountNetworkIsolated),
		string(contract.ReachPlatformAttested),
	}
)

// authorizeBody builds an authorize request as raw JSON.
//
// It is built as a map rather than as contract.AuthorizeRequest because two of
// the cases below need a body the Go type cannot express: one with
// policy_version omitted ENTIRELY (distinct from sent as zero), and one with an
// arbitrary version the type would happily hold but a struct literal makes
// awkward to vary.
func (s *Suite) authorizeBody(r AuthorizeRoute, version *int32) []byte {
	login := r.Login
	subject := r.Subject
	if subject == "" {
		subject = login
	}
	body := map[string]any{
		"identity": map[string]any{
			"subject": subject,
			"login":   login,
			"source":  "pdpconform",
		},
		"target":      r.Target,
		"auth_method": string(contract.AuthMethodCert),
		"conn":        s.conn(r.ProxyID),
		// The suite declares EVERY rung, and it is not pretending to be a
		// proxy build when it does. `capabilities` is how a proxy says what
		// it can provide, and a server is entitled to withhold a rung
		// nobody declared — so a suite that sent nothing could not grade a
		// server that honours the field, and the `enforcement` pair below
		// would pass vacuously against one that cannot express a rung at
		// all. Declaring everything says "do not constrain me on capability
		// grounds"; what is graded here is the envelope, never which rungs
		// an estate can take.
		"capabilities": map[string]any{
			"execution": executionRungs,
			"reach":     reachRungs,
		},
	}
	if r.Port != 0 {
		body["target_port"] = r.Port
	}
	if version != nil {
		body["policy_version"] = *version
	}
	b, err := json.Marshal(body)
	if err != nil {
		panic(err) // a map of strings and ints does not fail to marshal
	}
	return b
}

func (s *Suite) authorize(r AuthorizeRoute, version int32) (*response, error) {
	return s.postJSON(contract.PathAuthorize, s.authorizeBody(r, &version))
}

// CheckAuthorize grades the decision that shapes the whole session.
//
// Everything here asserts the ENVELOPE and never the policy content: which
// channels a route permits is the implementation's business, and a suite that
// graded that would be grading a fixture rather than a contract.
func (s *Suite) CheckAuthorize() {
	e := &s.expect.Authorize

	s.run(groupAuthorize, "a direct route answers 200 with the required envelope", func(c *Case) {
		r := s.mustAuthorize(c, e.Direct, contract.PolicyVersion)
		var got contract.AuthorizeResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
		c.require(got.RouteType == contract.RouteTypeDirect,
			"want route_type %q, got %q", contract.RouteTypeDirect, got.RouteType)
		s.requireAuthorizeEnvelope(c, r, &got)
	})

	s.run(groupAuthorize, "a nexthop route answers 200 and carries hop metadata", func(c *Case) {
		r := s.mustAuthorize(c, e.Nexthop, contract.PolicyVersion)
		var got contract.AuthorizeResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
		c.must(got.RouteType == contract.RouteTypeNexthop,
			"want route_type %q, got %q", contract.RouteTypeNexthop, got.RouteType)
		s.requireAuthorizeEnvelope(c, r, &got)
		if c.require(got.Hop != nil, "a nexthop route carries no hop metadata, so the chain cannot be built") {
			c.require(got.Hop.FinalTarget != "" || got.Hop.NextProxyID != "",
				"hop metadata names neither final_target nor next_proxy_id")
			if got.Hop.Direction() == contract.HopConnectionRelay {
				c.require(got.Hop.NextProxyID != "",
					"a relay hop with no next_proxy_id names no registration to open a channel over")
			}
		}
	})

	s.run(groupAuthorize, "an unauthorized target is a 401 decision with the envelope", func(c *Case) {
		r, err := s.authorize(e.Deny, contract.PolicyVersion)
		c.must(err == nil, "request failed: %v", err)
		c.require(r.status == 401,
			"want 401 (deny is a decision; everything else is an outage, M11), got %d: %s",
			r.status, snippet(r.body))
		if r.status == 401 {
			s.requireEnvelope(c, r)
		}
	})

	s.run(groupAuthorize, "the richest route the server serves is a well-formed envelope", func(c *Case) {
		r := s.mustAuthorize(c, e.Full, contract.PolicyVersion)
		var got contract.AuthorizeResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
		s.requireAuthorizeEnvelope(c, r, &got)
		c.note("policy fields present: %s", strings.Join(presentPolicyFields(r), ", "))
	})

	s.run(groupAuthorize, "every credential ladder entry names params.username", func(c *Case) {
		// The tightening (PLAN §4): username is required on every method the
		// contract defines, a route omitting it is refused at the first
		// authorize call, and there is no version at which omitting it is
		// correct. It is asserted as a required field, never as an
		// absent-value default — omission here means the route is refused.
		var sawBrokered bool
		for _, route := range []AuthorizeRoute{e.Ladder, e.Full, e.Direct} {
			if route.Target == "" {
				continue
			}
			r, err := s.authorize(route, contract.PolicyVersion)
			c.must(err == nil, "request failed: %v", err)
			if r.status != 200 {
				continue
			}
			var got contract.AuthorizeResponse
			c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
			state, ladder := got.Ladder()
			if state != contract.LadderWalk {
				continue
			}
			for i, entry := range ladder {
				if entry.Method == contract.TargetAuthBrokeredKey {
					sawBrokered = true
				}
				c.require(strings.TrimSpace(entry.Params[contract.ParamUsername]) != "",
					"%s ladder[%d] (%s) names no params.username; the proxy refuses such a route at the first authorize call",
					route.Target, i, entry.Method)
			}
		}
		c.require(sawBrokered,
			"no brokered-key ladder entry was served by any graded route, so the tightening was never exercised; "+
				"point authorize.ladder at a route whose ladder carries one")
	})

	s.checkVocabulary()
	s.checkDeviceFields()
	s.checkAbsentValueDefaults()
}

// requireAuthorizeEnvelope asserts the parts of the response the contract makes
// required, plus the internal consistency rules a server can get wrong without
// any single field being malformed.
func (s *Suite) requireAuthorizeEnvelope(c *Case, r *response, got *contract.AuthorizeResponse) {
	c.require(r.has("route_type"), "response omits route_type, which is required")
	c.require(r.has("target"), "response omits target, which is required")
	c.require(r.has("permitted_channels"),
		"response omits permitted_channels, which is required — an empty list denies every channel and absent means nothing at all")
	c.require(r.has("filter_policy"),
		"response omits filter_policy, which is required so a policy always has a defined default and cannot fail open by omission")
	c.require(got.Target != "", "response carries an empty target")
	c.require(got.FilterPolicy.Mode == contract.FilterModeWhitelist || got.FilterPolicy.Mode == contract.FilterModeBlacklist,
		"filter_policy.mode is %q, which is not a mode the contract defines", got.FilterPolicy.Mode)

	// The two exec tiers are alternatives, not layers.
	if got.FilterPolicy.Tier() == contract.ExecModeRestricted {
		c.require(got.FilterPolicy.RestrictedExec != nil,
			"exec_mode is restricted and restricted_exec is absent")
		c.require(len(got.FilterPolicy.Rules) == 0,
			"exec_mode is restricted and rules is non-empty; a guardrail and a boundary would disagree about the same command")
	} else {
		c.require(got.FilterPolicy.RestrictedExec == nil,
			"exec_mode is filtered and restricted_exec is present")
	}

	// An attestation beside a rung the proxy rendered itself would leave a
	// reader unable to tell which half the record meant.
	if got.Enforcement != nil {
		attested := got.EnforcedExecution().Attested() || got.EnforcedReach().Attested()
		if attested {
			if c.require(got.Enforcement.Attestation != nil,
				"an attested rung with no attestation: the claim is unverified and now also unattributable") {
				c.require(got.Enforcement.Attestation.AssertedBy != "", "attestation names no asserted_by")
				c.require(got.Enforcement.Attestation.Reference != "",
					`attestation carries no reference; "trust us" and an empty string are the same answer`)
			}
		} else {
			c.require(got.Enforcement.Attestation == nil,
				"an attestation beside applied rungs (%s/%s), which the contract forbids",
				got.EnforcedExecution(), got.EnforcedReach())
		}
		if got.EnforcedExecution() == contract.ExecutionPlatformAuthorized {
			c.require(got.Enforcement.PlatformRole != "",
				"execution platform-authorized requires platform_role, and there is no default to fall back on")
		} else {
			c.require(got.Enforcement.PlatformRole == "",
				"platform_role is set beside execution %q, which forbids it", got.EnforcedExecution())
		}
		if got.EnforcedReach() == contract.ReachAccountEgressRestricted {
			c.require(len(got.Enforcement.PermittedDestinations) > 0,
				"reach account-egress-restricted requires a non-empty permitted_destinations; they are the whole content of the rung")
		} else {
			c.require(len(got.Enforcement.PermittedDestinations) == 0,
				"permitted_destinations is set beside reach %q, which forbids it", got.EnforcedReach())
		}
	}

	// A cache hint the proxy cannot key the invalidation on is one it must not
	// hold: the key is what a cache_invalidate event names.
	if got.Cache.Cacheable() {
		c.require(got.Cache.Key != "",
			"cache.ttl_seconds is %d and cache.key is empty; a key is required whenever the TTL is above zero",
			got.Cache.TTLSeconds)
	}

	// A forwarding entry may carry at most one port constraint.
	if got.PermittedForwards != nil {
		for _, dir := range [][]contract.ForwardDestination{
			got.PermittedForwards.DirectTCPIP, got.PermittedForwards.ForwardedTCPIP,
		} {
			for _, d := range dir {
				c.require(d.Port == 0 || d.PortRange == nil,
					"forward destination %q carries both port and port_range, which the proxy refuses", d.Host)
				c.require(d.Host != "", "a forward destination carries no host")
			}
		}
	}
}

func (s *Suite) mustAuthorize(c *Case, route AuthorizeRoute, version int32) *response {
	r, err := s.authorize(route, version)
	c.must(err == nil, "request failed: %v", err)
	c.must(r.status == 200, "want 200 for %s, got %d: %s", route.Target, r.status, snippet(r.body))
	return r
}

func presentPolicyFields(r *response) []string {
	var out []string
	for _, f := range policyFields {
		if r.has(f) {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"<none>"}
	}
	return out
}

// checkVocabulary grades the negotiation rule, which no other assertion here
// catches: a response tested only at the current version looks perfect.
func (s *Suite) checkVocabulary() {
	route := s.expect.Authorize.Negotiation

	s.run(groupVocabulary, "a request with no policy_version is refused with 400", func(c *Case) {
		// Deliberately its own case, distinct from the version-1 case below.
		// They look similar and are not: 1 is a proxy that told the truth about
		// being old, and absent is a proxy that said nothing. Only the first
		// can be answered safely, because guessing a version for the second is
		// guessing which restrictions it would silently drop.
		r, err := s.postJSON(contract.PathAuthorize, s.authorizeBody(route, nil))
		c.must(err == nil, "request failed: %v", err)
		c.require(r.status == 400,
			"want 400 invalid_request for an absent policy_version, got %d — not a 200 with a guessed version, and not a 401 (a deny is a decision about a user; this is a malformed request): %s",
			r.status, snippet(r.body))
		if r.status == 400 {
			s.requireEnvelope(c, r)
		}
		c.require(r.status != 401, "an absent policy_version answered 401; a malformed caller is not a denied user (M11)")
	})

	s.run(groupVocabulary, "a low declared version is answered safely, never thinned", func(c *Case) {
		current, err := s.authorize(route, contract.PolicyVersion)
		c.must(err == nil, "current-version request failed: %v", err)
		c.must(current.status == 200, "current-version request: want 200, got %d: %s",
			current.status, snippet(current.body))

		low, err := s.authorize(route, 1)
		c.must(err == nil, "version-1 request failed: %v", err)

		switch {
		case low.status >= 500:
			// The correct answer when the policy needs vocabulary the caller
			// cannot read: say so rather than send fields that will be refused.
			s.requireEnvelope(c, low)
			c.note("answered %d — the server holds policy it cannot express at version 1 and says so", low.status)
		case low.status == 401:
			c.require(false,
				"version 1 was answered 401; a version mismatch is a rollout problem, not a decision about the user (M11)")
		case low.status == 200:
			// A 200 is a pass only if nothing was dropped. A thinned snapshot
			// that drops a restriction and returns 200 is the failure this
			// assertion exists for, and it is expressed as a comparison
			// against the current-version answer rather than against a table
			// of which field arrived in which revision — the contract states
			// one live vocabulary in the present tense and carries no such
			// table to read.
			var dropped []string
			for _, f := range policyFields {
				if current.has(f) && !low.has(f) {
					dropped = append(dropped, f)
				}
			}
			c.require(len(dropped) == 0,
				"version 1 was answered 200 with %s dropped from the current-version answer; "+
					"an unknown field may be a restriction, and a dropped restriction is a silently widened session — "+
					"the correct answer is a 5xx naming the mismatch",
				strings.Join(dropped, ", "))
			if len(dropped) == 0 {
				c.note("answered 200 and identical in policy content: this route needs nothing above version 1")
			}
		default:
			c.require(false, "version 1 was answered %d: %s", low.status, snippet(low.body))
		}
	})
}

// checkDeviceFields grades the open `device_field.<name>` namespace: the shape
// the contract states, and nothing about which names are meaningful.
func (s *Suite) checkDeviceFields() {
	e := &s.expect.Authorize.DeviceFields

	s.run(groupDeviceField, "device fields round-trip intact and within the shape the contract states", func(c *Case) {
		r := s.mustAuthorize(c, e.WithFields, contract.PolicyVersion)
		var got contract.AuthorizeResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))

		state, ladder := got.Ladder()
		c.must(state == contract.LadderWalk, "%s serves no credential ladder to carry device fields", e.WithFields.Target)

		var seen int
		for i, entry := range ladder {
			if entry.Method != contract.TargetAuthEphemeralAccount {
				continue
			}
			count := 0
			for k, v := range entry.Params {
				if !strings.HasPrefix(k, contract.DeviceFieldPrefix) {
					continue
				}
				count++
				seen++
				name := strings.TrimPrefix(k, contract.DeviceFieldPrefix)
				c.require(deviceFieldName.MatchString(name),
					"ladder[%d] device field %q: the name must be lowercase letters, digits, hyphens and underscores, %d characters or fewer",
					i, name, contract.DeviceFieldMaxNameLen)
				c.require(v != "", "ladder[%d] device field %q has an empty value", i, name)
				c.require(len(v) <= contract.DeviceFieldMaxValueLen,
					"ladder[%d] device field %q has a value of %d characters, over the %d the contract allows",
					i, name, len(v), contract.DeviceFieldMaxValueLen)
			}
			c.require(count <= contract.DeviceFieldMaxPerEntry,
				"ladder[%d] carries %d device fields, over the %d the contract allows",
				i, count, contract.DeviceFieldMaxPerEntry)
		}
		c.require(seen > 0,
			"%s served no device_field.* parameter, so this case graded nothing; point authorize.device_fields.with_fields at a route that carries one",
			e.WithFields.Target)
		c.note("%d device field(s) round-tripped, names unenumerated by this suite on purpose", seen)
	})

	s.run(groupDeviceField, "a device field demands no higher policy_version than the route otherwise would", func(c *Case) {
		// Written against the rule rather than against a literal: the suite
		// finds the lowest version at which the comparable route WITHOUT device
		// fields is served, and asserts the route WITH them is served at the
		// same one. Pinning it to a number would make this case stale at the
		// next revision, and a suite that expected a bump here would be
		// asserting a rule the contract does not have.
		base, ok := s.lowestServedVersion(e.WithoutFields)
		c.must(ok, "%s is not served at any version in 1..%d, so there is no baseline to compare against",
			e.WithoutFields.Target, contract.PolicyVersion)

		r, err := s.authorize(e.WithFields, base)
		c.must(err == nil, "request failed: %v", err)
		c.require(r.status == 200,
			"%s (no device fields) is served at policy_version %d, but %s (device fields) answers %d there; "+
				"the namespace is open, so a name inside it is not a new policy field and demands no bump: %s",
			e.WithoutFields.Target, base, e.WithFields.Target, r.status, snippet(r.body))
		c.note("baseline version for the comparable route is %d, and the device-field route is served there too", base)
	})
}

// lowestServedVersion finds the lowest declared vocabulary at which a route is
// answered 200.
func (s *Suite) lowestServedVersion(route AuthorizeRoute) (int32, bool) {
	for v := int32(1); v <= contract.PolicyVersion; v++ {
		r, err := s.authorize(route, v)
		if err != nil {
			return 0, false
		}
		if r.status == 200 {
			return v, true
		}
	}
	return 0, false
}

// checkAbsentValueDefaults grades the v4 absent-value defaults.
//
// Each is graded as a PAIR — a route the server answers without the field and a
// route it answers with it. An assertion that only ever sees the absent half
// passes vacuously against a server that cannot express the field at all, and a
// vacuous pass here is worse than a failure because it reads as coverage.
func (s *Suite) checkAbsentValueDefaults() {
	d := &s.expect.Authorize.Defaults

	type spec struct {
		name  string
		field string
		pair  DefaultPair
		// def describes what the absent value MEANS, and resolved reads it back
		// through the same resolver the rest of this repository uses.
		def      string
		resolved func(*contract.AuthorizeResponse) any
		// differs reports whether the "set" route actually says something
		// other than the default. This is what stops the pair being two
		// identical routes.
		differs func(*contract.AuthorizeResponse) bool
	}

	specs := []spec{
		{
			name: "enforcement", field: "enforcement", pair: d.Enforcement,
			def: "proxy-side enforcement on both axes (proxy-inspected / proxy-channel-policy)",
			resolved: func(r *contract.AuthorizeResponse) any {
				return string(r.EnforcedExecution()) + " / " + string(r.EnforcedReach())
			},
			differs: func(r *contract.AuthorizeResponse) bool {
				return r.EnforcedExecution() != contract.ExecutionProxyInspected ||
					r.EnforcedReach() != contract.ReachProxyChannelPolicy
			},
		},
		{
			name: "session_deadline", field: "session_deadline", pair: d.SessionDeadline,
			def: "no deadline",
			resolved: func(r *contract.AuthorizeResponse) any {
				v, ok := r.Deadline()
				if !ok {
					return "no deadline"
				}
				return v
			},
			differs: func(r *contract.AuthorizeResponse) bool { _, ok := r.Deadline(); return ok },
		},
		{
			name: "require_session_capture", field: "require_session_capture", pair: d.RequireCapture,
			def:      "false",
			resolved: func(r *contract.AuthorizeResponse) any { return r.CaptureRequired() },
			differs:  func(r *contract.AuthorizeResponse) bool { return r.CaptureRequired() },
		},
		{
			name: "concurrency", field: "concurrency", pair: d.Concurrency,
			def: "uncapped",
			resolved: func(r *contract.AuthorizeResponse) any {
				sub, tgt := r.Caps()
				if sub == 0 && tgt == 0 {
					return "uncapped"
				}
				return [2]int32{sub, tgt}
			},
			differs: func(r *contract.AuthorizeResponse) bool {
				sub, tgt := r.Caps()
				return sub != 0 || tgt != 0
			},
		},
	}

	for _, sp := range specs {
		s.run(groupDefaults, "absent "+sp.field+" means "+sp.def, func(c *Case) {
			absent := s.mustAuthorize(c, sp.pair.Default, contract.PolicyVersion)
			var noField contract.AuthorizeResponse
			c.must(absent.into(&noField) == nil, "undecodable body: %s", snippet(absent.body))
			c.require(!absent.has(sp.field),
				"%s was expected to answer without %s and carries it; point authorize.defaults.%s.default at a route that does not set it",
				sp.pair.Default.Target, sp.field, sp.name)

			gotDefault := sp.resolved(&noField)
			c.require(reflect.DeepEqual(gotDefault, sp.resolved(&contract.AuthorizeResponse{})),
				"%s resolves to %v with %s absent, and the contract's default is %v",
				sp.pair.Default.Target, gotDefault, sp.field, sp.resolved(&contract.AuthorizeResponse{}))

			// The other half. Without it this assertion passes against a server
			// that cannot express the field at all.
			set := s.mustAuthorize(c, sp.pair.Set, contract.PolicyVersion)
			var withField contract.AuthorizeResponse
			c.must(set.into(&withField) == nil, "undecodable body: %s", snippet(set.body))
			c.require(set.has(sp.field),
				"%s was expected to SET %s and does not, so the absent case above graded nothing",
				sp.pair.Set.Target, sp.field)
			c.require(sp.differs(&withField),
				"%s carries %s but it resolves to the default anyway (%v), so absence is still indistinguishable",
				sp.pair.Set.Target, sp.field, sp.resolved(&withField))
			c.note("absent on %s => %v; set on %s => %v",
				sp.pair.Default.Target, gotDefault, sp.pair.Set.Target, sp.resolved(&withField))
		})
	}
}
