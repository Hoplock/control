// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package compile

import (
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"github.com/hoplock/control/internal/policy/model"
)

// The compiled form of a rule's match: every list is a plain slice scanned
// linearly and every pattern is already parsed, so evaluation does no work that
// depends on how somebody wrote the policy (M5).
//
// Matching also *records* as it goes. The terms that made a rule match are an
// output of this engine, not a by-product of logging (M4), so they are
// collected here rather than reconstructed afterwards — a reconstruction is a
// second implementation of matching, and the second one is the one that is
// wrong.
//
// Every term is appended in a fixed order and map-derived terms are walked in
// sorted key order, because "the same inputs produce byte-identical
// explanations" is a promise a range over a Go map cannot keep.

type matcher struct {
	subject *subjectMatcher
	device  *deviceMatcher
	context *contextMatcher
	target  *targetMatcher
	grant   *grantMatcher
}

type subjectMatcher struct {
	ids         []string
	sources     []string
	groups      []string
	claimKeys   []string
	claims      map[string][]string
	authMethods []model.AuthMethod
	mfa         *bool
}

type deviceMatcher struct {
	required    bool
	postureKeys []string
	posture     map[string][]string
}

type contextMatcher struct {
	days     []time.Weekday
	window   *model.TimeWindow
	prefixes []netip.Prefix
	proxyIDs []string
	loc      *time.Location
}

type targetMatcher struct {
	hostnames []string
	labelKeys []string
	labels    map[string][]string
	zones     []string
}

type grantMatcher struct {
	required bool
	scopes   []string
	origins  []model.GrantOrigin
}

// match reports whether the rule matches, appending the terms that made it do
// so. It returns the grant that satisfied a grant constraint, when one did, so
// the snapshot can carry that grant's context (M10, M16).
func (m *matcher) match(in model.Input, terms *[]model.MatchedTerm) (*model.Grant, bool) {
	if m.subject != nil && !m.subject.match(in.Subject, terms) {
		return nil, false
	}
	if m.device != nil && !m.device.match(in.Device, terms) {
		return nil, false
	}
	if m.context != nil && !m.context.match(in, terms) {
		return nil, false
	}
	if m.target != nil && !m.target.match(in.Target, terms) {
		return nil, false
	}
	if m.grant == nil {
		return nil, true
	}
	return m.grant.match(in, terms)
}

func (s *subjectMatcher) match(in model.Subject, terms *[]model.MatchedTerm) bool {
	if len(s.ids) > 0 {
		if !slices.Contains(s.ids, in.ID) {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{Axis: model.AxisSubject, Term: "id", Value: in.ID})
	}
	if len(s.sources) > 0 {
		if !slices.Contains(s.sources, in.Source) {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{Axis: model.AxisSubject, Term: "source", Value: in.Source})
	}
	if len(s.groups) > 0 {
		hit, ok := firstIntersection(s.groups, in.Groups)
		if !ok {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{Axis: model.AxisSubject, Term: "groups", Value: hit})
	}
	for _, k := range s.claimKeys {
		v, present := in.Claims[k]
		if !present || !slices.Contains(s.claims[k], v) {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{Axis: model.AxisSubject, Term: "claims." + k, Value: v})
	}
	if len(s.authMethods) > 0 {
		if !slices.Contains(s.authMethods, in.AuthMethod) {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{
			Axis: model.AxisSubject, Term: "auth_method", Value: string(in.AuthMethod)})
	}
	if s.mfa != nil {
		if *s.mfa != in.MFA {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{
			Axis: model.AxisSubject, Term: "mfa", Value: strconv.FormatBool(in.MFA)})
	}
	return true
}

func (d *deviceMatcher) match(in *model.Device, terms *[]model.MatchedTerm) bool {
	// Absence is never read as compliance: a rule that constrains posture
	// does not match an endpoint that supplied none.
	if in == nil {
		return !d.required && len(d.postureKeys) == 0
	}
	if d.required {
		*terms = append(*terms, model.MatchedTerm{Axis: model.AxisDevice, Term: "present", Value: "true"})
	}
	for _, k := range d.postureKeys {
		v, present := in.Posture[k]
		if !present || !slices.Contains(d.posture[k], v) {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{Axis: model.AxisDevice, Term: "posture." + k, Value: v})
	}
	return true
}

func (c *contextMatcher) match(in model.Input, terms *[]model.MatchedTerm) bool {
	local := in.Now.In(c.loc)
	if len(c.days) > 0 {
		if !slices.Contains(c.days, local.Weekday()) {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{
			Axis: model.AxisContext, Term: "day", Value: local.Weekday().String()})
	}
	if c.window != nil {
		minute := local.Hour()*60 + local.Minute()
		if !c.window.Contains(minute) {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{
			Axis: model.AxisContext, Term: "time_of_day", Value: local.Format("15:04")})
	}
	if len(c.prefixes) > 0 {
		addr := in.Context.SourceAddr
		if !addr.IsValid() {
			return false
		}
		hit, ok := firstPrefix(c.prefixes, addr)
		if !ok {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{
			Axis: model.AxisContext, Term: "source_network", Value: hit.String()})
	}
	if len(c.proxyIDs) > 0 {
		if !slices.Contains(c.proxyIDs, in.Context.ProxyID) {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{
			Axis: model.AxisContext, Term: "proxy_id", Value: in.Context.ProxyID})
	}
	return true
}

func (t *targetMatcher) match(in model.Target, terms *[]model.MatchedTerm) bool {
	if len(t.hostnames) > 0 {
		hit, ok := firstHostPattern(t.hostnames, in.Hostname)
		if !ok {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{Axis: model.AxisTarget, Term: "hostname", Value: hit})
	}
	for _, k := range t.labelKeys {
		v, present := in.Labels[k]
		if !present || !slices.Contains(t.labels[k], v) {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{Axis: model.AxisTarget, Term: "labels." + k, Value: v})
	}
	if len(t.zones) > 0 {
		if !slices.Contains(t.zones, in.Zone) {
			return false
		}
		*terms = append(*terms, model.MatchedTerm{Axis: model.AxisTarget, Term: "zone", Value: in.Zone})
	}
	return true
}

// match finds the first live grant that covers this target and satisfies the
// rule's scope and origin constraints. First in the caller's order, so the same
// inputs always name the same grant.
func (g *grantMatcher) match(in model.Input, terms *[]model.MatchedTerm) (*model.Grant, bool) {
	for i := range in.Grants {
		grant := &in.Grants[i]
		if grant.Subject != in.Subject.ID || !grant.Live(in.Now) || !grant.Covers(in.Target) {
			continue
		}
		if len(g.scopes) > 0 && !slices.Contains(g.scopes, grant.Scope.Name) {
			continue
		}
		if len(g.origins) > 0 && !slices.Contains(g.origins, grant.Origin) {
			continue
		}
		*terms = append(*terms,
			model.MatchedTerm{Axis: model.AxisGrant, Term: "grant_id", Value: grant.ID},
			model.MatchedTerm{Axis: model.AxisGrant, Term: "scope", Value: grant.Scope.Name},
			model.MatchedTerm{Axis: model.AxisGrant, Term: "origin", Value: string(grant.Origin)},
		)
		return grant, true
	}
	return nil, false
}

// firstIntersection returns the first member of want that is also in have,
// scanning want in its authored order so the recorded term is stable.
func firstIntersection(want, have []string) (string, bool) {
	for _, w := range want {
		if slices.Contains(have, w) {
			return w, true
		}
	}
	return "", false
}

func firstPrefix(prefixes []netip.Prefix, addr netip.Addr) (netip.Prefix, bool) {
	// Compare unmapped: a v4-mapped v6 client address must still match a v4
	// prefix, or a policy written in IPv4 stops working the day a proxy
	// starts accepting on a dual-stack listener.
	addr = addr.Unmap()
	for _, p := range prefixes {
		if p.Contains(addr) {
			return p, true
		}
	}
	return netip.Prefix{}, false
}

func firstHostPattern(patterns []string, host string) (string, bool) {
	for _, p := range patterns {
		if model.MatchesHostPattern(host, p) {
			return p, true
		}
	}
	return "", false
}

// sortedKeys returns a map's keys in order, which is how every map-derived term
// gets a deterministic position in an explanation.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
