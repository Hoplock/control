// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package compile

import (
	"net/netip"
	"slices"
	"strconv"

	"github.com/hoplock/control/internal/policy/model"
)

// Unreachable-rule detection.
//
// Rules are ordered and first match wins, so a rule is unreachable when some
// earlier rule already matches everything it does. That is a containment
// question over the match axes, and it is decided **conservatively**: where
// containment cannot be proven, the rule is reported as reachable. A missed
// shadow is a rule nobody needed; a false one is a compiler refusing a policy
// that was correct, which is far worse and would teach authors to distrust the
// check.
//
// The scan is quadratic in rule count. That is deliberate and it is not on the
// decision path: compilation happens when a bundle is published, evaluation
// happens on every connection, and M5 governs only the second.

func (c *compiler) checkReachability(rules []model.Rule) {
	for i := range rules {
		for j := range rules[:i] {
			if !covers(rules[j].Match, rules[i].Match) {
				continue
			}
			c.add(model.CodeRuleUnreachable, rules[i].ID, rules[i].Line(),
				"shadowed_by", rules[j].ID,
				"other_line", strconv.Itoa(rules[j].Line()))
			break
		}
	}
}

// covers reports whether every input matched by b is also matched by a.
func covers(a, b model.Match) bool {
	return coversSubject(a.Subject, b.Subject) &&
		coversDevice(a.Device, b.Device) &&
		coversContext(a.Context, b.Context) &&
		coversTarget(a.Target, b.Target) &&
		coversGrant(a.Grant, b.Grant)
}

func coversSubject(a, b *model.SubjectMatch) bool {
	if a == nil {
		return true
	}
	if b == nil {
		b = &model.SubjectMatch{}
	}
	if !coversSet(a.IDs, b.IDs) ||
		!coversSet(a.Sources, b.Sources) ||
		!coversSet(a.Groups, b.Groups) ||
		!coversSet(a.AuthMethods, b.AuthMethods) ||
		!coversValueMap(a.Claims, b.Claims) {
		return false
	}
	if a.MFA != nil && (b.MFA == nil || *a.MFA != *b.MFA) {
		return false
	}
	return true
}

func coversDevice(a, b *model.DeviceMatch) bool {
	if a == nil {
		return true
	}
	if b == nil {
		return false
	}
	// A posture constraint already implies the endpoint supplied posture,
	// so a rule that constrains one is at least as narrow as one that only
	// demands presence.
	if a.Required && !b.Required && len(b.Posture) == 0 {
		return false
	}
	return coversValueMap(a.Posture, b.Posture)
}

func coversContext(a, b *model.ContextMatch) bool {
	if a == nil {
		return true
	}
	if b == nil {
		b = &model.ContextMatch{}
	}
	if !coversSet(a.Days, b.Days) || !coversSet(a.ProxyIDs, b.ProxyIDs) {
		return false
	}
	if a.TimeOfDay != nil && (b.TimeOfDay == nil || !coversWindow(*a.TimeOfDay, *b.TimeOfDay)) {
		return false
	}
	return coversNetworks(a.SourceCIDRs, b.SourceCIDRs)
}

func coversTarget(a, b *model.TargetMatch) bool {
	if a == nil {
		return true
	}
	if b == nil {
		b = &model.TargetMatch{}
	}
	if !coversSet(a.Zones, b.Zones) || !coversValueMap(a.Labels, b.Labels) {
		return false
	}
	if len(a.Hostnames) == 0 {
		return true
	}
	if len(b.Hostnames) == 0 {
		return false
	}
	for _, bh := range b.Hostnames {
		if !slices.ContainsFunc(a.Hostnames, func(ah string) bool {
			return model.HostPatternCovers(ah, bh)
		}) {
			return false
		}
	}
	return true
}

func coversGrant(a, b *model.GrantMatch) bool {
	if !a.Constrained() {
		return true
	}
	if !b.Constrained() {
		return false
	}
	return coversSet(a.Scopes, b.Scopes) && coversSet(a.Origins, b.Origins)
}

// coversSet reports whether an OR-list a admits everything OR-list b admits: a
// is unconstrained, or b names a subset of it. An unconstrained b under a
// constrained a is never covered.
func coversSet[T comparable](a, b []T) bool {
	if len(a) == 0 {
		return true
	}
	if len(b) == 0 {
		return false
	}
	for _, v := range b {
		if !slices.Contains(a, v) {
			return false
		}
	}
	return true
}

// coversValueMap compares key-to-permitted-values maps, which are ANDed across
// keys: every key a constrains must be constrained at least as narrowly by b.
// Extra keys in b only narrow it further.
func coversValueMap(a, b map[string][]string) bool {
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !coversSet(av, bv) {
			return false
		}
	}
	return true
}

// coversWindow reports whether time window a contains window b, expanding a
// window that wraps midnight into the one or two plain intervals it is made of.
func coversWindow(a, b model.TimeWindow) bool {
	for _, bi := range intervals(b) {
		if !slices.ContainsFunc(intervals(a), func(ai [2]int) bool {
			return ai[0] <= bi[0] && bi[1] <= ai[1]
		}) {
			return false
		}
	}
	return true
}

func intervals(w model.TimeWindow) [][2]int {
	if w.FromMinutes <= w.ToMinutes {
		return [][2]int{{w.FromMinutes, w.ToMinutes}}
	}
	return [][2]int{{w.FromMinutes, 1439}, {0, w.ToMinutes}}
}

// coversNetworks reports whether every prefix in b sits inside some prefix in a.
// A CIDR that does not parse has already been rejected, so it simply fails to
// cover rather than producing a second message about the same mistake.
func coversNetworks(a, b []string) bool {
	if len(a) == 0 {
		return true
	}
	if len(b) == 0 {
		return false
	}
	ap := make([]netip.Prefix, 0, len(a))
	for _, s := range a {
		p, err := parseNetwork(s)
		if err != nil {
			return false
		}
		ap = append(ap, p)
	}
	for _, s := range b {
		bp, err := parseNetwork(s)
		if err != nil {
			return false
		}
		if !slices.ContainsFunc(ap, func(p netip.Prefix) bool { return prefixContains(p, bp) }) {
			return false
		}
	}
	return true
}

func prefixContains(outer, inner netip.Prefix) bool {
	if outer.Addr().Is4() != inner.Addr().Is4() {
		return false
	}
	return outer.Bits() <= inner.Bits() && outer.Contains(inner.Addr())
}
