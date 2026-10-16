// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"slices"

	"github.com/hoplock/control/internal/contract"
)

// Judging a route's algorithm floor and bans against ONE proxy's declaration
// (Hoplock/proxy#69, #72).
//
// Whether a ban leaves an axis nothing to offer depends on what the route's
// profile and floor offer IN THE BUILD that enforces it, which each proxy
// declares on every authorize request: `capabilities.algorithm_profiles` for
// each profile's offer per axis, and `capabilities.algorithm_floors` for each
// level's key exchanges. Upstream's api/README.md ("Capability advertisement")
// states the rule that composes the two, once, and an upstream test proves it
// agrees with the proxy's own refusal axis for axis. It is applied here and not
// restated beyond what the code needs: the profile's list, the floor's
// intersection, then the bans with the curve25519 step.
//
// NOTHING HERE HOLDS A LIST OF ITS OWN. The judgement reads a declaration or it
// does not judge: a profile the declaration omits, or a declaration with no
// `algorithm_profiles` at all (a build from before #72), is UNKNOWN — never
// proof either way, never a refusal, never a silent pass. A copy of a profile's
// algorithms here would drift the day a build changes, and `algorithms` (the
// union) is the source of the typo warning and of nothing else.
//
// One function serves two callers: the decision path, which refuses a route to
// exactly the proxies whose own declaration shows an axis emptied (a
// computation over the request, no read, so it fits M5), and the publish-time
// report, which shows the same finding per build as a warning the author may
// override (M17).

// AlgorithmPolicy is a route's algorithm policy, as it goes on the wire.
type AlgorithmPolicy struct {
	// Profile is the route's profile; empty means `default` (PLAN §5.2).
	Profile contract.AlgorithmProfile
	// Floor is the route's floor; empty means none.
	Floor contract.AlgorithmFloor
	// Bans are the route's bans, the bundle's merged in; nil means none.
	Bans *contract.AlgorithmBans
}

// Constrains reports whether there is anything to judge: a floor or a ban.
func (p AlgorithmPolicy) Constrains() bool { return p.Floor != "" || !p.Bans.Empty() }

// ResolvedProfile is the profile the route is judged under.
func (p AlgorithmPolicy) ResolvedProfile() contract.AlgorithmProfile {
	if p.Profile == "" {
		return contract.AlgorithmProfileDefault
	}
	return p.Profile
}

// AxisEmptied is one axis a route leaves nothing to offer on one build.
type AxisEmptied struct {
	// Axis is the contract's axis name.
	Axis string `json:"axis"`
	// Offered is what the build offers on the axis under the route's profile
	// and floor, before the bans subtract from it.
	Offered []string `json:"offered"`
}

// AlgorithmJudgement is the answer for one route on one build.
type AlgorithmJudgement struct {
	// FloorUndeclared is a floor level the build does not declare: the server
	// MUST NOT send it there, so authorize refuses the route on that proxy.
	FloorUndeclared bool `json:"floor_undeclared,omitempty"`
	// Unknown is a declaration that cannot judge the bans: it omits the
	// route's profile, or omits `algorithm_profiles` altogether.
	Unknown bool `json:"unknown,omitempty"`
	// Emptied are the axes the route leaves nothing to offer. The proxy
	// refuses such a route as a contract violation, so this server must not
	// send it.
	Emptied []AxisEmptied `json:"emptied,omitempty"`
}

// Refused reports whether the build refuses the route.
func (j AlgorithmJudgement) Refused() bool { return j.FloorUndeclared || len(j.Emptied) > 0 }

// DeclaresFloor reports whether a declaration names a floor level. Absent
// declares nothing, so no floor may be sent at all.
func DeclaresFloor(decl *contract.ProxyCapabilities, floor contract.AlgorithmFloor) bool {
	_, ok := floorExchanges(decl, floor)
	return ok
}

func floorExchanges(decl *contract.ProxyCapabilities, floor contract.AlgorithmFloor) ([]string, bool) {
	if decl == nil {
		return nil, false
	}
	for _, f := range decl.AlgorithmFloors {
		if f.Level == floor {
			return f.KeyExchanges, true
		}
	}
	return nil, false
}

func profileOffer(decl *contract.ProxyCapabilities, profile contract.AlgorithmProfile) (contract.AlgorithmProfileCapability, bool) {
	if decl == nil {
		return contract.AlgorithmProfileCapability{}, false
	}
	for _, p := range decl.AlgorithmProfiles {
		if p.Profile == profile {
			return p, true
		}
	}
	return contract.AlgorithmProfileCapability{}, false
}

// JudgeAlgorithms applies upstream's rule to one build's declaration.
func JudgeAlgorithms(p AlgorithmPolicy, decl *contract.ProxyCapabilities) AlgorithmJudgement {
	var j AlgorithmJudgement
	if !p.Constrains() {
		return j
	}
	levelKex, declared := floorExchanges(decl, p.Floor)
	if p.Floor != "" && !declared {
		j.FloorUndeclared = true
	}
	offer, ok := profileOffer(decl, p.ResolvedProfile())
	if !ok {
		j.Unknown = true
		return j
	}
	for _, axis := range contract.Axes {
		// 1. the profile's list on the axis.
		list := offer.Axis(axis)
		// 2. under a floor, key exchanges intersected with the level's.
		//    A level the build does not declare is already a refusal
		//    above; it narrows nothing here, so the bans are still judged.
		if axis == contract.AxisKeyExchanges && p.Floor != "" && declared {
			list = slices.DeleteFunc(slices.Clone(list), func(kex string) bool {
				return !slices.Contains(levelKex, kex)
			})
		}
		// 3. the bans, with the curve25519 step on key exchanges.
		left := subtractBans(axis, list, p.Bans.Axis(axis))
		if len(left) == 0 {
			j.Emptied = append(j.Emptied, AxisEmptied{Axis: axis, Offered: list})
		}
	}
	return j
}

// subtractBans removes every banned name, treating the two spellings of
// curve25519 as ONE exchange on the key-exchange axis: a ban on either removes
// both. Plain set subtraction under-refuses — a ban naming one spelling and
// every other exchange the route offers leaves the other spelling, and the
// proxy leaves nothing.
func subtractBans(axis string, list, banned []string) []string {
	if len(banned) == 0 {
		return list
	}
	canon := func(s string) string { return s }
	if axis == contract.AxisKeyExchanges {
		canon = canonicalKeyExchange
	}
	ban := make(map[string]bool, len(banned))
	for _, name := range banned {
		ban[canon(name)] = true
	}
	return slices.DeleteFunc(slices.Clone(list), func(name string) bool { return ban[canon(name)] })
}

func canonicalKeyExchange(name string) string {
	if name == contract.KexCurve25519LibSSH {
		return contract.KexCurve25519
	}
	return name
}

// UnmatchedBans are the banned names no declaration in a set offers on their
// axis, judged from each build's `algorithms`. A typo looks exactly like a
// working ban, so the publish-time report warns about these and never refuses
// them: a name no build implements is a ban already satisfied. A declaration
// with no `algorithms` says nothing about a name and is skipped, so a set in
// which no build declared its offer returns every name. That is not evidence of
// a typo — nobody said what they offer — and the publish-time report judges
// names only when at least one build has declared (M17: the absence of proof
// is advice).
func UnmatchedBans(bans *contract.AlgorithmBans, decls []*contract.ProxyCapabilities) map[string][]string {
	out := map[string][]string{}
	for _, axis := range contract.Axes {
		for _, name := range bans.Axis(axis) {
			matched := false
			for _, d := range decls {
				if d == nil || d.Algorithms == nil {
					continue
				}
				offered := d.Algorithms.Axis(axis)
				if slices.Contains(offered, name) ||
					(axis == contract.AxisKeyExchanges && slices.ContainsFunc(offered, func(o string) bool {
						return canonicalKeyExchange(o) == canonicalKeyExchange(name)
					})) {
					matched = true
					break
				}
			}
			if !matched {
				out[axis] = append(out[axis], name)
			}
		}
	}
	return out
}
