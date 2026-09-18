// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"net/netip"
	"time"
)

// The input vocabulary: everything a rule may match on (PLAN §5.1), and
// nothing else.
//
// Two absences are deliberate.
//
// `conn.hop_trail` is not here and must not be added. It is a routing input,
// not a policy-matching axis: it selects where a path starts and it refuses
// loops and over-long chains, and it may only ever *narrow* an answer. A rule
// that granted on the strength of a hop id would turn a field any caller can
// write into authority, which is the one thing the trail must never become
// (PLAN §5.3). The decision record stores it all the same, because "which hop
// asked, and what had it already been through" is unrecoverable afterwards —
// but that is 0008's record, not this engine's input.
//
// The tenant is not here either (M18). Tenancy selects *which compiled program
// is served*, at compile time; it is not a predicate the evaluator checks per
// request. M5's budget has no room for a tenant filter on the hot path, and a
// filter is the wrong shape anyway — a tenant is not a rule, and an explanation
// that named a tenant term instead of a rule would be a worse explanation.

// Tenant owns a bundle, and therefore a compiled program. It is part of the
// lookup key a caller uses to obtain a program, never something this package
// reads from ambient state.
type Tenant string

// Input is one whole evaluation's inputs. Time is a field rather than a call to
// time.Now(): an engine with its own clock cannot be simulated over historical
// traffic (0014) and cannot be tested without flakiness.
type Input struct {
	// Now is the instant the decision is being made for. Day-of-week and
	// time-of-day matching read it in the bundle's declared location.
	Now time.Time

	Subject Subject
	// Device is endpoint posture, and it is a pointer because it is
	// genuinely optional: plenty of endpoints supply none, and a rule that
	// requires posture must not match one that has none.
	Device  *Device
	Context Context
	Target  Target

	// Grants are the live just-in-time grants for this subject (M10),
	// including windows confirmed from external context (M16). They are an
	// input like any other — not a special case bypassing the engine, which
	// would be invisible to simulation and to "explain why".
	Grants []Grant
}

// Subject is the authenticated principal.
type Subject struct {
	// ID is the subject id this server established. It is not the client
	// typed `login`.
	ID string
	// Source is the identity provider the subject came from.
	Source string
	// Groups are the groups the mapping resolved (M7).
	Groups []string
	// Claims are the mapped IdP claims, never raw token claim names.
	Claims map[string]string
	// AuthMethod is how the identity authenticated to the proxy.
	AuthMethod AuthMethod
	// MFA reports whether a second factor was used.
	MFA bool
}

// Device is endpoint posture, when an endpoint supplies any.
type Device struct {
	// Posture is the attributes the endpoint asserted.
	Posture map[string]string
}

// Context is where and when the connection is being made from.
type Context struct {
	// SourceAddr is the client address. An invalid Addr means none was
	// supplied, and a rule constraining the source network cannot match it.
	SourceAddr netip.Addr
	// ProxyID is the proxy asking — the entry proxy on a user's first hop,
	// an inner hop on a chained one (M6). Each hop asks for itself, so the
	// same login and target legitimately answer differently at the edge and
	// behind it.
	ProxyID string
}

// Target is the host being reached.
type Target struct {
	Hostname string
	Port     int32
	Zone     string
	// Labels are the target's labels, e.g. env=prod, kind=appliance.
	Labels map[string]string
}

// Grant is one live just-in-time grant (M10).
//
// Its origin varies and is recorded: an administrator created it by hand, an
// approval workflow produced it (Enterprise E8), or an external system asserted
// a window and a provider confirmed it (M16). All three are the same object to
// the engine — that is the point — but the record carries which one it was and,
// where it applies, the external reference and the window that was asserted.
type Grant struct {
	// ID is the grant, as an explanation and an audit record name it.
	ID string
	// Subject is who the grant is for.
	Subject string
	// Origin is how the grant came to exist.
	Origin GrantOrigin
	// Scope is what the grant covers.
	Scope GrantScope
	// NotBefore and ExpiresAt bound the grant. A zero NotBefore means "from
	// creation"; ExpiresAt is required of a real grant, and a zero one is
	// treated as expired rather than as forever.
	NotBefore time.Time
	ExpiresAt time.Time
	// Approvers are who approved it.
	Approvers []string
	// RequestRef is the request that produced it.
	RequestRef string
	// External is the outside system that asserted the window, when one
	// did (M16).
	External *ExternalReference
}

// GrantScope is what a grant covers.
type GrantScope struct {
	// Name is the scope a rule matches on, e.g. "prod-dba".
	Name string
	// Targets are the hostnames the grant covers, exact or with a single
	// leading wildcard. Empty means every target the rule already matched.
	Targets []string
	// Labels are label constraints the target must satisfy.
	Labels map[string]string
	// Zones are the zones the grant covers. Empty means any.
	Zones []string
}

// ExternalReference is the outside system behind a confirmed window (M16).
type ExternalReference struct {
	System    string
	Reference string
	// WindowStart and WindowEnd are what the external system asserted. They
	// are recorded, not enforced.
	WindowStart time.Time
	WindowEnd   time.Time
	// AdditionalContext is a string or an object, carried verbatim.
	AdditionalContext *AdditionalContext
}

// Live reports whether the grant is in force at the given instant.
func (g Grant) Live(now time.Time) bool {
	if g.ExpiresAt.IsZero() || !now.Before(g.ExpiresAt) {
		return false
	}
	return g.NotBefore.IsZero() || !now.Before(g.NotBefore)
}

// Covers reports whether the grant's scope covers this target.
func (g Grant) Covers(t Target) bool {
	if len(g.Scope.Targets) > 0 && !matchesAnyHostPattern(t.Hostname, g.Scope.Targets) {
		return false
	}
	for k, v := range g.Scope.Labels {
		if t.Labels[k] != v {
			return false
		}
	}
	if len(g.Scope.Zones) > 0 {
		found := false
		for _, z := range g.Scope.Zones {
			if z == t.Zone {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Context renders the grant as the GrantContext a snapshot carries.
func (g Grant) Context() *GrantContext {
	gc := &GrantContext{GrantID: g.ID, Origin: g.Origin}
	if g.External != nil {
		gc.System = g.External.System
		gc.Reference = g.External.Reference
		gc.WindowStart = g.External.WindowStart
		gc.WindowEnd = g.External.WindowEnd
		gc.AdditionalContext = g.External.AdditionalContext
	}
	return gc
}
