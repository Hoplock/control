// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package authoring

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/policy/compile"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// Facts is what Control's checks read about the fleet and the estate. It is
// gathered once per report and the checks are a pure function of it, so every
// finding below can be tested without a database.
type Facts struct {
	// Satisfaction is fleet.CheckPolicy's answer for the bundle's routes:
	// both capability sources, the proxies' builds and the targets' records.
	Satisfaction []fleet.Satisfaction
	// Proxies are the enrolled rows, for what each build DOES provide.
	Proxies []store.Proxy
	// Declarations are each proxy's latest recorded algorithm declaration.
	Declarations []fleet.DeclaredAlgorithms
	// Kex are the stored key-exchange observations.
	Kex []fleet.KexObservation
	// Unmet are the stored `target.algorithm_policy_unmet` records.
	Unmet []audit.UnmetAlgorithmPolicy
	// Now and TTL judge an observation's freshness, in the decision path's
	// own window.
	Now time.Time
	TTL time.Duration
}

// DefaultInterpreters is the starting list of executables that hand back a
// shell (GTFOBins' "shell" function): shells, interpreters, editors and pagers
// with a command escape, and tools that run a command as part of their job. It
// is a list a policy author is WARNED from, never one the data plane refuses
// from — a shipped deny-list of names in the proxy would be a blacklist
// masquerading as a boundary. `policy.interpreters` replaces it.
var DefaultInterpreters = []string{
	"sh", "bash", "dash", "zsh", "ksh", "csh", "tcsh", "fish", "ash", "busybox",
	"python", "perl", "ruby", "php", "lua", "node", "tclsh", "wish", "expect", "irb", "jjs",
	"awk", "gawk", "mawk", "nawk", "sed", "find", "xargs", "env", "nice", "timeout", "stdbuf",
	"less", "more", "most", "man", "pg",
	"vi", "vim", "view", "rvim", "nvim", "ex", "ed", "emacs", "nano", "pico", "joe", "mg",
	"tar", "zip", "cpio", "rsync", "git", "ssh", "scp", "sftp", "ftp", "gdb", "strace",
	"script", "socat", "nc", "ncat", "tmux", "screen", "watch", "flock", "make", "sudo", "su",
	"docker", "kubectl", "crontab", "at",
}

// isInterpreter reports whether an executable is on the list, by basename and
// with a trailing version stripped: `/usr/bin/python3.11` is `python`.
func isInterpreter(executable string, list []string) (string, bool) {
	name := path.Base(executable)
	if slices.Contains(list, name) {
		return name, true
	}
	if stripped := strings.TrimRight(name, "0123456789.-"); stripped != name && slices.Contains(list, stripped) {
		return stripped, true
	}
	return "", false
}

// controlFindings runs Control's own checks. It is pure.
func controlFindings(b *model.Bundle, prog *compile.Program, facts Facts, interpreters []string) []Finding {
	rules := make(map[string]model.Rule, len(b.Rules))
	for _, r := range b.Rules {
		rules[r.ID] = r
	}
	var out []Finding
	out = append(out, satisfiability(facts)...)
	for i := 0; i < prog.Len(); i++ {
		rule := prog.Rule(i)
		route := rule.Route()
		if route == nil {
			continue
		}
		authored := rules[rule.ID()]
		out = append(out, interpreterFindings(rule, route, interpreters)...)
		out = append(out, cacheHintFindings(rule, authored, route)...)
	}
	out = append(out, algorithmFindings(prog, facts)...)
	return out
}

// ---------------------------------------------------------------------------
// can this policy actually be served (M17)
// ---------------------------------------------------------------------------

type proxyShortfall struct {
	ProxyID  string         `json:"proxy_id"`
	Zone     string         `json:"zone"`
	Live     bool           `json:"live"`
	Missing  []string       `json:"missing"`
	Provides map[string]any `json:"provides"`
}

type targetShortfall struct {
	Hostname string   `json:"hostname"`
	Zone     string   `json:"zone,omitempty"`
	Missing  []string `json:"missing"`
}

func satisfiability(facts Facts) []Finding {
	declared := map[string]fleet.Capabilities{}
	for _, p := range facts.Proxies {
		caps := fleet.UnmarshalCapabilities(p.DeclaredCapabilities)
		if !caps.Empty() {
			declared[p.ID] = caps
		}
	}

	var out []Finding
	if len(facts.Satisfaction) > 0 && len(declared) == 0 {
		// Nothing has said what it can serve. That is not a shortfall in
		// any route; it is the absence of evidence about all of them.
		out = append(out, Finding{
			ID: findingID(CodeProxiesUnreported), Severity: SeverityAdvice, Code: CodeProxiesUnreported,
			Source:     SourceControl,
			Parameters: map[string]any{"proxies": len(facts.Proxies)},
			Message: "no proxy in this tenant has declared what it can serve, so no route can be judged " +
				"against a build; publish, and the report will say more once a proxy has reported",
		})
	}

	for _, sat := range facts.Satisfaction {
		// THE PROXY HALF. A proxy that has declared nothing is not evidence
		// either way, so a route is short only when every proxy that HAS
		// declared falls short — and then it is named with what it lacks
		// and what it does provide.
		if len(declared) > 0 {
			var short []proxyShortfall
			served := false
			for _, v := range sat.Proxies {
				caps, ok := declared[v.ProxyID]
				if !ok {
					continue
				}
				if v.OK {
					served = true
					break
				}
				short = append(short, proxyShortfall{
					ProxyID: v.ProxyID, Zone: string(v.Zone), Live: v.Live, Missing: v.Missing,
					Provides: provides(caps),
				})
			}
			if !served && len(short) > 0 {
				ids := make([]string, 0, len(short))
				for _, s := range short {
					ids = append(ids, s.ProxyID)
				}
				out = append(out, Finding{
					ID: findingID(CodeNoProxyProvides, sat.RuleID), Severity: SeverityWarning,
					Code: CodeNoProxyProvides, Source: SourceControl, Rule: sat.RuleID,
					Parameters: map[string]any{"rule": sat.RuleID, "proxies": short},
					Message: fmt.Sprintf("no proxy that has declared its capabilities can serve rule %q (%s): "+
						"a rung a proxy cannot honour is skipped silently, and on a one-rung ladder the "+
						"session is denied with nobody having authored the denial",
						sat.RuleID, strings.Join(ids, ", ")),
				})
			}
		}

		// THE TARGET HALF. A fresh record that lacks the rung is a target
		// that cannot take it; no fresh record is the absence of proof,
		// which fails safe at connect time and is advice here.
		var cannot []targetShortfall
		var unproven []string
		for _, t := range sat.Targets {
			if t.OK {
				continue
			}
			if t.Observed {
				cannot = append(cannot, targetShortfall{Hostname: t.Hostname, Zone: string(t.Zone), Missing: t.Missing})
			} else {
				unproven = append(unproven, t.Hostname)
			}
		}
		if len(cannot) > 0 {
			out = append(out, Finding{
				ID: findingID(CodeTargetCannotTake, sat.RuleID), Severity: SeverityWarning,
				Code: CodeTargetCannotTake, Source: SourceControl, Rule: sat.RuleID,
				Parameters: map[string]any{"rule": sat.RuleID, "targets": cannot},
				Message: fmt.Sprintf("rule %q claims a rung that %d target(s) it reaches cannot take, by their "+
					"fresh capability records; sessions there are refused at connect time as an outage",
					sat.RuleID, len(cannot)),
			})
		}
		if len(unproven) > 0 {
			out = append(out, Finding{
				ID: findingID(CodeTargetUnproven, sat.RuleID), Severity: SeverityAdvice,
				Code: CodeTargetUnproven, Source: SourceControl, Rule: sat.RuleID,
				Parameters: map[string]any{"rule": sat.RuleID, "targets": unproven},
				Message: fmt.Sprintf("rule %q claims an applied rung on %d target(s) whose capability record is "+
					"stale, undated or absent; that is the absence of proof, so an applied rung is not "+
					"served there until a probe reports one", sat.RuleID, len(unproven)),
			})
		}
	}
	return out
}

// provides renders what a build does serve, beside what it lacks.
func provides(c fleet.Capabilities) map[string]any {
	out := map[string]any{}
	if len(c.CredentialMethods) > 0 {
		out["credential_methods"] = c.CredentialMethods
	}
	if len(c.Execution) > 0 {
		out["execution"] = c.Execution
	}
	if len(c.Reach) > 0 {
		out["reach"] = c.Reach
	}
	if len(c.Platforms) > 0 {
		out["platforms"] = c.Platforms
	}
	return out
}

// ---------------------------------------------------------------------------
// an allow-list containing an interpreter is not an allow-list
// ---------------------------------------------------------------------------

func interpreterFindings(rule *compile.Rule, route *model.Route, list []string) []Finding {
	rung := route.Enforcement.Execution
	if rung != model.ExecutionAccountRestricted && rung != model.ExecutionAccountConfined {
		return nil
	}
	if route.Filter.RestrictedExec == nil {
		return nil
	}
	var out []Finding
	seen := map[string]bool{}
	for _, cmd := range route.Filter.RestrictedExec.Commands {
		name, ok := isInterpreter(cmd.Executable, list)
		if !ok || seen[cmd.Executable] {
			continue
		}
		seen[cmd.Executable] = true
		out = append(out, Finding{
			ID: findingID(CodeInterpreter, rule.ID(), cmd.Executable), Severity: SeverityWarning,
			Code: CodeInterpreter, Source: SourceControl, Rule: rule.ID(), Line: rule.Line(),
			Parameters: map[string]any{
				"rule": rule.ID(), "executable": cmd.Executable, "interpreter": name,
				"rung": string(rung), "guarantee": string(model.ExecutionNoInteractiveShell),
			},
			Message: fmt.Sprintf("rule %q allows %s under %s; %s can hand back a shell, so the allow-list does "+
				"not deliver the boundary the rung claims and the real guarantee is %s at best — remove it, "+
				"or accept this finding to publish the trade-off knowingly",
				rule.ID(), cmd.Executable, rung, name, model.ExecutionNoInteractiveShell),
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// a cache hint that outlives what gated it (0005)
// ---------------------------------------------------------------------------

// cacheHintFindings warns exactly where a hint can outlive its condition: a
// rule matching a term the key cannot carry, on a route nothing else bounds.
// Every other hint is self-limiting — a snapshot's time bounds are absolute
// instants, and a grant-gated rule always carries the grant's deadline — and a
// warning on every cached rule is one authors learn to click through.
//
// A source network is the one term nothing bounds. A proxy keys a cached
// decision on the identity, target, port, method and hop trail (the contract's
// CacheHint) and never on the client address, so a hinted rule gated on
// `context.source_cidrs` admits the same identity from ANY address for up to
// the hint's lifetime. A duration or a grant bounds a session, not an
// admission the rule would never have made, so that one warns whatever else
// the route says.
func cacheHintFindings(rule *compile.Rule, authored model.Rule, route *model.Route) []Finding {
	hint := rule.Cache()
	if hint == nil {
		return nil
	}
	var out []Finding
	if c := authored.Match.Context; c != nil && len(c.SourceCIDRs) > 0 {
		out = append(out, Finding{
			ID: findingID(CodeCacheHintSpansSources, rule.ID()), Severity: SeverityWarning,
			Code: CodeCacheHintSpansSources, Source: SourceControl, Rule: rule.ID(), Line: rule.Line(),
			Parameters: map[string]any{"rule": rule.ID(), "term": "context.source_cidrs", "ttl_seconds": hint.TTLSeconds},
			Message: fmt.Sprintf("rule %q matches on context.source_cidrs, and a proxy reuses a cached decision "+
				"by identity, target, port, method and hop trail, never by the client address: for up to %ds "+
				"the same identity is admitted from any address — drop the cache hint, or accept this finding "+
				"to publish the trade-off knowingly", rule.ID(), hint.TTLSeconds),
		})
	}
	if route.MaxSessionDuration != 0 || authored.Match.Grant.Constrained() {
		return out
	}
	var terms []string
	if c := authored.Match.Context; c != nil {
		if len(c.Days) > 0 {
			terms = append(terms, "context.days")
		}
		if c.TimeOfDay != nil {
			terms = append(terms, "context.time_of_day")
		}
	}
	if authored.Match.Device != nil {
		terms = append(terms, "device")
	}
	if len(terms) == 0 {
		return out
	}
	return append(out, Finding{
		ID: findingID(CodeCacheHintOutlives, rule.ID()), Severity: SeverityWarning,
		Code: CodeCacheHintOutlives, Source: SourceControl, Rule: rule.ID(), Line: rule.Line(),
		Parameters: map[string]any{"rule": rule.ID(), "terms": terms, "ttl_seconds": hint.TTLSeconds},
		Message: fmt.Sprintf("rule %q matches on %s, which its cache key does not carry, and its route has no "+
			"max_session_duration: a decision is reused for up to %ds after the condition stopped holding, "+
			"and a session admitted then runs until somebody closes it — add a max_session_duration, or drop "+
			"the cache hint", rule.ID(), strings.Join(terms, " and "), hint.TTLSeconds),
	})
}

// ---------------------------------------------------------------------------
// the algorithm floor and bans (Hoplock/proxy#69, #72)
// ---------------------------------------------------------------------------

func algorithmPolicyOf(route *model.Route) fleet.AlgorithmPolicy {
	p := fleet.AlgorithmPolicy{
		Profile: contract.AlgorithmProfile(route.AlgorithmProfile),
		Floor:   contract.AlgorithmFloor(route.AlgorithmFloor),
	}
	if b := route.AlgorithmBans; b != nil && !b.Empty() {
		p.Bans = &contract.AlgorithmBans{
			KeyExchanges: slices.Clone(b.KeyExchanges), Ciphers: slices.Clone(b.Ciphers),
			MACs: slices.Clone(b.MACs), HostKeys: slices.Clone(b.HostKeys),
			PublicKeyAuth: slices.Clone(b.PublicKeyAuth),
		}
	}
	return p
}

type kexView struct {
	Hostname   string            `json:"hostname"`
	Port       int32             `json:"port,omitempty"`
	FloorMet   contract.FloorMet `json:"floor_met"`
	Negotiated string            `json:"negotiated,omitempty"`
	Offered    []string          `json:"offered,omitempty"`
	ObservedAt time.Time         `json:"observed_at"`
}

func algorithmFindings(prog *compile.Program, facts Facts) []Finding {
	var out []Finding
	reached := map[string][]string{}
	for _, sat := range facts.Satisfaction {
		for _, t := range sat.Targets {
			reached[sat.RuleID] = append(reached[sat.RuleID], t.Hostname)
		}
	}

	decls := make([]*contract.ProxyCapabilities, 0, len(facts.Declarations))
	for _, d := range facts.Declarations {
		decls = append(decls, d.Declaration.Capabilities())
	}
	// Unmatched bans are a property of the NAME, judged against every
	// recorded build, so one finding per name names every rule carrying it.
	unmatched := map[string][]string{}
	var unmatchedOrder []string

	for i := 0; i < prog.Len(); i++ {
		rule := prog.Rule(i)
		route := rule.Route()
		if route == nil {
			continue
		}
		policy := algorithmPolicyOf(route)
		id := rule.ID()
		// THE STORED EVIDENCE is about the route's profile first (#66), so it
		// is read for every allow route: `default` is a profile, and it is the
		// one that stopped reaching SHA-1-only devices. A floor or a ban only
		// adds the records that failed one.
		out = append(out, unmetFindings(rule, policy, reached[id], facts.Unmet)...)
		if !policy.Constrains() {
			continue
		}

		// PER BUILD, from each proxy's own latest declaration (#72). A route
		// that names no profile is judged from `default`'s entry. Whether a
		// ban is a typo is judged only against builds that declared what they
		// offer; when none has, nothing can be judged, which is the absence
		// of proof and advice (M17), never a warning holding the runbook's ban.
		judgeable := policy.Bans == nil || slices.ContainsFunc(decls, offers)
		if len(facts.Declarations) == 0 || !judgeable {
			out = append(out, Finding{
				ID: findingID(CodeAlgorithmUnknown, id), Severity: SeverityAdvice, Code: CodeAlgorithmUnknown,
				Source: SourceControl, Rule: id, Line: rule.Line(),
				Parameters: map[string]any{"rule": id, "profile": string(policy.ResolvedProfile())},
				Message: fmt.Sprintf("rule %q names a floor or bans, and no proxy has declared the algorithms it "+
					"offers yet; it is answered as authored, and each proxy's own check is the backstop", id),
			})
		}
		for _, d := range facts.Declarations {
			j := fleet.JudgeAlgorithms(policy, d.Declaration.Capabilities())
			if j.FloorUndeclared {
				out = append(out, Finding{
					ID: findingID(CodeFloorUndeclared, id, d.ProxyID), Severity: SeverityWarning,
					Code: CodeFloorUndeclared, Source: SourceControl, Rule: id, Line: rule.Line(),
					Parameters: map[string]any{"rule": id, "proxy_id": d.ProxyID, "floor": string(policy.Floor)},
					Message: fmt.Sprintf("proxy %s does not declare the floor level %s that rule %q names; "+
						"authorize refuses the route on that proxy until its build does", d.ProxyID, policy.Floor, id),
				})
			}
			if j.Unknown {
				out = append(out, Finding{
					ID: findingID(CodeAlgorithmUnknown, id, d.ProxyID), Severity: SeverityAdvice,
					Code: CodeAlgorithmUnknown, Source: SourceControl, Rule: id, Line: rule.Line(),
					Parameters: map[string]any{"rule": id, "proxy_id": d.ProxyID, "profile": string(policy.ResolvedProfile())},
					Message: fmt.Sprintf("proxy %s's declaration does not say what profile %s offers, so whether "+
						"rule %q's bans empty an axis there is unknown; it is answered as authored",
						d.ProxyID, policy.ResolvedProfile(), id),
				})
			}
			for _, e := range j.Emptied {
				out = append(out, Finding{
					ID: findingID(CodeAxisEmptied, id, d.ProxyID, e.Axis), Severity: SeverityWarning,
					Code: CodeAxisEmptied, Source: SourceControl, Rule: id, Line: rule.Line(),
					Parameters: map[string]any{
						"rule": id, "proxy_id": d.ProxyID, "axis": e.Axis, "offered": e.Offered,
						"profile": string(policy.ResolvedProfile()), "floor": string(policy.Floor),
					},
					Message: fmt.Sprintf("on proxy %s, rule %q's bans leave %s nothing to offer under profile %s; "+
						"authorize refuses the route on that build, which is an outage and never a widening",
						d.ProxyID, id, e.Axis, policy.ResolvedProfile()),
				})
			}
		}
		if policy.Bans != nil && judgeable {
			for axis, names := range fleet.UnmatchedBans(policy.Bans, decls) {
				for _, name := range names {
					key := axis + ":" + name
					if _, ok := unmatched[key]; !ok {
						unmatchedOrder = append(unmatchedOrder, key)
					}
					unmatched[key] = append(unmatched[key], id)
				}
			}
		}

		// WHAT RAISING THE FLOOR BREAKS: the stored observations of the
		// targets the route reaches. Never a refusal — the observation is
		// past evidence, and the handshake re-checks every connection.
		if policy.Floor != "" {
			var below []kexView
			var notReported []string
			for _, host := range reached[id] {
				obs, ok := freshestKex(facts.Kex, host, route.Port)
				switch {
				case !ok || !obs.Fresh(facts.Now, facts.TTL):
					notReported = append(notReported, host)
				case obs.FloorMet.Rank() < policy.Floor.Rank():
					below = append(below, kexView{
						Hostname: obs.Hostname, Port: obs.Port, FloorMet: obs.FloorMet,
						Negotiated: obs.Negotiated, Offered: obs.Offered, ObservedAt: obs.ObservedAt,
					})
				}
			}
			if len(below) > 0 {
				out = append(out, Finding{
					ID: findingID(CodeTargetBelowFloor, id), Severity: SeverityWarning,
					Code: CodeTargetBelowFloor, Source: SourceControl, Rule: id, Line: rule.Line(),
					Parameters: map[string]any{"rule": id, "floor": string(policy.Floor), "targets": below},
					Message: fmt.Sprintf("%d target(s) rule %q reaches last negotiated below the floor %s; "+
						"raising it fails those targets closed until they are upgraded", len(below), id, policy.Floor),
				})
			}
			if len(notReported) > 0 {
				out = append(out, Finding{
					ID: findingID(CodeTargetNotReported, id), Severity: SeverityAdvice,
					Code: CodeTargetNotReported, Source: SourceControl, Rule: id, Line: rule.Line(),
					Parameters: map[string]any{"rule": id, "floor": string(policy.Floor), "targets": notReported},
					Message: fmt.Sprintf("%d target(s) rule %q reaches have no fresh key-exchange observation; "+
						"whether they meet the floor %s is not reported", len(notReported), id, policy.Floor),
				})
			}
		}
	}

	for _, key := range unmatchedOrder {
		axis, name, _ := strings.Cut(key, ":")
		rules := unmatched[key]
		out = append(out, Finding{
			ID: findingID(CodeBanUnmatched, axis, name), Severity: SeverityWarning, Code: CodeBanUnmatched,
			Source:     SourceControl,
			Parameters: map[string]any{"axis": axis, "name": name, "rules": rules},
			Message: fmt.Sprintf("no recorded proxy build offers %q on %s; a typo looks exactly like a working "+
				"ban, so check the spelling — a name no build implements is a ban already satisfied", name, axis),
		})
	}
	return out
}

// offers reports whether a build declared what it can offer, which is what a
// ban's spelling is judged against.
func offers(d *contract.ProxyCapabilities) bool { return d != nil && d.Algorithms != nil }

// freshestKex finds a target's key-exchange observation. Observations are
// keyed by host and port — the proxy's report carries no platform — so a route
// naming a port reads that port's, and one naming none reads the host's
// freshest.
func freshestKex(all []fleet.KexObservation, host string, port int32) (fleet.KexObservation, bool) {
	var best fleet.KexObservation
	found := false
	for _, o := range all {
		if o.Hostname != host || (port != 0 && o.Port != 0 && o.Port != port) {
			continue
		}
		if !found || o.ObservedAt.After(best.ObservedAt) {
			best, found = o, true
		}
	}
	return best, found
}

type unmetView struct {
	Target     string    `json:"target"`
	Axis       string    `json:"axis"`
	Cause      string    `json:"cause,omitempty"`
	Offered    []string  `json:"offered,omitempty"`
	Profile    string    `json:"profile,omitempty"`
	Floor      string    `json:"floor,omitempty"`
	Bans       []string  `json:"bans,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
	RecordID   string    `json:"record_id"`
}

// unmetFindings warns from stored `target.algorithm_policy_unmet` records for
// the targets a route reaches, matched by target hostname as the showcase join
// matches a record to a target (0010):
//
//   - a profile record (no cause, or cause `profile`) counts when it was
//     recorded under the profile the route names — an absent profile on a
//     record is never read as `default`;
//   - a floor record counts when the route names that floor or a higher one;
//   - a ban record counts when the route names one of the bans it failed.
func unmetFindings(rule *compile.Rule, policy fleet.AlgorithmPolicy, targets []string, unmet []audit.UnmetAlgorithmPolicy) []Finding {
	if len(unmet) == 0 || len(targets) == 0 {
		return nil
	}
	var routeBans []string
	for _, axis := range contract.Axes {
		for _, name := range policy.Bans.Axis(axis) {
			routeBans = append(routeBans, axis+":"+name)
		}
	}
	var out []Finding
	seen := map[string]bool{}
	for _, u := range unmet {
		if !slices.Contains(targets, u.Target) {
			continue
		}
		counts := false
		switch u.Cause {
		case "", "profile":
			counts = u.Profile != "" && u.Profile == string(policy.ResolvedProfile())
		case "floor":
			counts = policy.Floor != "" && u.Floor != "" &&
				policy.Floor.Rank() >= contract.AlgorithmFloor(u.Floor).Rank()
		case "ban":
			counts = slices.ContainsFunc(u.Bans, func(b string) bool { return slices.Contains(routeBans, b) })
		}
		if !counts {
			continue
		}
		key := u.Target + "/" + u.Axis + "/" + u.Cause
		if seen[key] {
			// Newest first: the first record for a target, axis and cause
			// is the one an author needs.
			continue
		}
		seen[key] = true
		view := unmetView{
			Target: u.Target, Axis: u.Axis, Cause: u.Cause, Offered: u.Offered, Profile: u.Profile,
			Floor: u.Floor, Bans: u.Bans, RecordedAt: u.RecordedAt, RecordID: u.RecordID,
		}
		offered := "nothing it could say"
		if len(u.Offered) > 0 {
			offered = strings.Join(u.Offered, ", ")
		}
		out = append(out, Finding{
			ID: findingID(CodePolicyUnmet, rule.ID(), u.Target, u.Axis, u.Cause), Severity: SeverityWarning,
			Code: CodePolicyUnmet, Source: SourceControl, Rule: rule.ID(), Line: rule.Line(),
			Parameters: map[string]any{"rule": rule.ID(), "unmet": view},
			Message: fmt.Sprintf("target %s, which rule %q reaches, reported on %s that it could not meet this "+
				"algorithm policy on %s: it offered %s; it may have been upgraded since, so this warns and "+
				"never refuses", u.Target, rule.ID(), u.RecordedAt.UTC().Format(time.RFC3339), u.Axis, offered),
		})
	}
	return out
}
