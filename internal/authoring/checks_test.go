// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package authoring

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
)

// Control's own checks are a pure function of the bundle and the facts, so
// each finding is tested here without a database; service_test.go drives the
// same checks through a real fleet.

const header = `schema_version: 1
tenant: acme
labels:
  env: [prod, dev]
groups: [sre]
scopes:
  ops: {}
rules:
`

func route(extra string) string {
	return `    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-key
          username: netadmin
          credential_ref: vault://x
` + extra
}

func candidate(t *testing.T, rules string) *Candidate {
	t.Helper()
	rejections, cand := Compile("acme", []byte(header+rules))
	if cand == nil {
		t.Fatalf("the fixture does not compile: %+v", rejections)
	}
	return cand
}

func run(t *testing.T, rules string, facts Facts) []Finding {
	t.Helper()
	c := candidate(t, rules)
	if facts.Now.IsZero() {
		facts.Now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		facts.TTL = 24 * time.Hour
	}
	return controlFindings(c.Bundle, c.Program, facts, DefaultInterpreters)
}

func withCode(fs []Finding, code string) []Finding {
	return slices.DeleteFunc(slices.Clone(fs), func(f Finding) bool { return f.Code != code })
}

// ---------------------------------------------------------------------------
// the interpreter warning
// ---------------------------------------------------------------------------

func restrictedRule(id, host, rung string, executables ...string) string {
	var cmds strings.Builder
	for _, e := range executables {
		cmds.WriteString("            - executable: " + e + "\n              form: exact\n              argv: [\"x\"]\n")
	}
	return `  - id: ` + id + `
    effect: allow
    match:
      target:
        hostnames: [` + host + `]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: whitelist
        exec_mode: restricted
        restricted_exec:
          commands:
` + cmds.String() + `      credentials:
        - method: ephemeral-user
          username: svc
          key_type: ed25519
      enforcement:
        execution: ` + rung + `
`
}

func TestAnInterpreterInARestrictedAllowListIsAWarning(t *testing.T) {
	t.Parallel()
	fs := withCode(run(t, restrictedRule("logs", "app.example.com", "account-restricted",
		"/usr/bin/uptime", "/usr/bin/find", "/usr/bin/python3.11"), Facts{}), CodeInterpreter)

	if len(fs) != 2 {
		t.Fatalf("got %d interpreter findings, want find and python3.11: %+v", len(fs), fs)
	}
	for _, f := range fs {
		if f.Severity != SeverityWarning {
			t.Errorf("%s is %s; it must be a warning the author may override", f.ID, f.Severity)
		}
		if f.Parameters["guarantee"] != "no-interactive-shell" || f.Parameters["rung"] != "account-restricted" {
			t.Errorf("%s does not name the rung and its real guarantee: %v", f.ID, f.Parameters)
		}
		if !strings.Contains(f.Message, "no-interactive-shell") || !strings.Contains(f.Message, "logs") {
			t.Errorf("the message does not say why: %s", f.Message)
		}
	}
	if fs[0].Parameters["executable"] != "/usr/bin/find" || fs[1].Parameters["interpreter"] != "python" {
		t.Errorf("findings name %v and %v", fs[0].Parameters, fs[1].Parameters)
	}

	// account-confined claims the same boundary; no-interactive-shell does
	// not claim one, so the list is not a boundary there to weaken.
	if fs := withCode(run(t, restrictedRule("c", "app.example.com", "account-confined", "/bin/vi"), Facts{}), CodeInterpreter); len(fs) != 1 {
		t.Errorf("account-confined with vi: %d findings, want 1", len(fs))
	}
}

// ---------------------------------------------------------------------------
// the cache-hint warning, and its two negatives
// ---------------------------------------------------------------------------

func cachedRule(id, host, match, routeExtra string) string {
	return `  - id: ` + id + `
    effect: allow
    match:
      target:
        hostnames: [` + host + `]
` + match + route(routeExtra) + `    cache:
      key: [subject, target]
      ttl_seconds: 300
`
}

func TestTheCacheHintWarningFiresOnlyWhereItShould(t *testing.T) {
	t.Parallel()
	const officeHours = "      context:\n        time_of_day: \"09:00-17:00\"\n"
	cases := []struct {
		name  string
		rule  string
		terms []string
	}{
		{"time of day, no duration", cachedRule("office", "a.example.com", officeHours, ""), []string{"context.time_of_day"}},
		{"days, no duration", cachedRule("weekdays", "a.example.com", "      context:\n        days: [monday, tuesday]\n", ""), []string{"context.days"}},
		{"device, no duration", cachedRule("posture", "a.example.com", "      device:\n        required: true\n", ""), []string{"device"}},
		// THE TWO NEGATIVES. A warning that fires on every cached rule is
		// one authors learn to click through.
		{"time of day with a duration", cachedRule("office", "a.example.com", officeHours, "      max_session_duration: 1h\n"), nil},
		{"grant-gated, no duration", cachedRule("granted", "a.example.com", "      grant:\n        required: true\n        scopes: [ops]\n", ""), nil},
		{"subject only, no duration", cachedRule("plain", "a.example.com", "      subject:\n        groups: [sre]\n", ""), nil},
	}
	for _, tc := range cases {
		fs := withCode(run(t, tc.rule, Facts{}), CodeCacheHintOutlives)
		if tc.terms == nil {
			if len(fs) != 0 {
				t.Errorf("%s: warned %+v, want nothing", tc.name, fs)
			}
			continue
		}
		if len(fs) != 1 {
			t.Errorf("%s: %d findings, want 1", tc.name, len(fs))
			continue
		}
		f := fs[0]
		if f.Severity != SeverityWarning || !slices.Equal(f.Parameters["terms"].([]string), tc.terms) {
			t.Errorf("%s: %s naming %v, want a warning naming %v", tc.name, f.Severity, f.Parameters["terms"], tc.terms)
		}
		if !strings.Contains(f.Message, "max_session_duration") || !strings.Contains(f.Message, "drop the cache hint") {
			t.Errorf("%s: the message does not name both fixes: %s", tc.name, f.Message)
		}
	}
}

// ---------------------------------------------------------------------------
// the algorithm floor and bans, per build
// ---------------------------------------------------------------------------

func declaration(proxyID string, defaultCiphers ...string) fleet.DeclaredAlgorithms {
	return fleet.DeclaredAlgorithms{ProxyID: proxyID, Declaration: fleet.AlgorithmDeclaration{
		AlgorithmFloors: []contract.AlgorithmFloorCapability{
			{Level: contract.AlgorithmFloorModernKex, KeyExchanges: []string{contract.KexCurve25519, contract.KexCurve25519LibSSH}},
		},
		AlgorithmProfiles: []contract.AlgorithmProfileCapability{{
			Profile:       contract.AlgorithmProfileDefault,
			KeyExchanges:  []string{contract.KexCurve25519, contract.KexCurve25519LibSSH, "ecdh-sha2-nistp256"},
			Ciphers:       defaultCiphers,
			MACs:          []string{"hmac-sha2-256"},
			HostKeys:      []string{"ssh-ed25519"},
			PublicKeyAuth: []string{"ssh-ed25519"},
		}},
		Algorithms: &contract.OfferableAlgorithms{
			KeyExchanges: []string{contract.KexCurve25519, contract.KexCurve25519LibSSH, "ecdh-sha2-nistp256"},
			Ciphers:      defaultCiphers,
		},
	}}
}

func bannedRule(id, host, bans string) string {
	return `  - id: ` + id + `
    effect: allow
    match:
      target:
        hostnames: [` + host + `]
` + route("      algorithm_bans:\n"+bans)
}

// An emptied axis is judged per build, from each build's own declaration, and
// reported as a warning naming the proxy and the axis — never a refusal.
func TestAnEmptiedAxisIsAPerBuildFinding(t *testing.T) {
	t.Parallel()
	facts := Facts{Declarations: []fleet.DeclaredAlgorithms{
		declaration("proxy-old", "aes128-ctr"),
		declaration("proxy-new", "aes128-ctr", "chacha20-poly1305@openssh.com"),
	}}
	fs := withCode(run(t, bannedRule("no-ctr", "a.example.com", "        ciphers: [aes128-ctr]\n"), facts), CodeAxisEmptied)
	if len(fs) != 1 {
		t.Fatalf("got %+v, want exactly one finding, for proxy-old", fs)
	}
	f := fs[0]
	if f.Severity != SeverityWarning || f.Parameters["proxy_id"] != "proxy-old" || f.Parameters["axis"] != contract.AxisCiphers {
		t.Errorf("finding = %s %v, want a warning for proxy-old on ciphers", f.Severity, f.Parameters)
	}
	if f.Parameters["profile"] != "default" {
		t.Errorf("a route naming no profile is judged under %v, want default", f.Parameters["profile"])
	}
}

// A ban naming one curve25519 spelling plus every other exchange the route
// offers empties the axis: the two spellings are one exchange.
func TestTheCurve25519BanIsJudgedAsOneExchange(t *testing.T) {
	t.Parallel()
	facts := Facts{Declarations: []fleet.DeclaredAlgorithms{declaration("proxy-1", "aes128-ctr")}}
	fs := withCode(run(t, bannedRule("kex", "a.example.com",
		"        key_exchanges: [curve25519-sha256, ecdh-sha2-nistp256]\n"), facts), CodeAxisEmptied)
	if len(fs) != 1 || fs[0].Parameters["axis"] != contract.AxisKeyExchanges {
		t.Errorf("banning curve25519-sha256 and ecdh-sha2-nistp256: %+v, want key_exchanges emptied", fs)
	}
}

func TestAFloorABuildDoesNotDeclareAndAnUnknownDeclaration(t *testing.T) {
	t.Parallel()
	pq := declaration("proxy-1", "aes128-ctr")
	noProfiles := fleet.DeclaredAlgorithms{ProxyID: "proxy-2", Declaration: fleet.AlgorithmDeclaration{
		AlgorithmFloors: pq.Declaration.AlgorithmFloors,
	}}
	facts := Facts{Declarations: []fleet.DeclaredAlgorithms{pq, noProfiles}}
	rule := `  - id: pq
    effect: allow
    match:
      target:
        hostnames: [a.example.com]
` + route("      algorithm_floor: pq-hybrid-kex\n")
	fs := run(t, rule, facts)
	undeclared := withCode(fs, CodeFloorUndeclared)
	if len(undeclared) != 2 || undeclared[0].Severity != SeverityWarning {
		t.Errorf("pq-hybrid-kex, which neither build declares: %+v, want a warning per proxy", undeclared)
	}
	unknown := withCode(fs, CodeAlgorithmUnknown)
	if len(unknown) != 1 || unknown[0].Parameters["proxy_id"] != "proxy-2" || unknown[0].Severity != SeverityAdvice {
		t.Errorf("a declaration with no profiles: %+v, want advice for proxy-2 alone", unknown)
	}
}

// A banned name no recorded build offers is a typo-shaped warning, one per
// name, never a refusal.
func TestABanNoBuildOffersIsAnOverridableWarning(t *testing.T) {
	t.Parallel()
	facts := Facts{Declarations: []fleet.DeclaredAlgorithms{declaration("proxy-1", "aes128-ctr", "aes256-ctr")}}
	fs := withCode(run(t, bannedRule("typo", "a.example.com", "        ciphers: [aes128-crt]\n"), facts), CodeBanUnmatched)
	if len(fs) != 1 || fs[0].Severity != SeverityWarning || fs[0].Parameters["name"] != "aes128-crt" {
		t.Errorf("banning a name nobody offers: %+v, want one warning naming it", fs)
	}
}

// ---------------------------------------------------------------------------
// what raising a floor breaks, and the unmet-policy evidence
// ---------------------------------------------------------------------------

func reaching(rule string, hosts ...string) []fleet.Satisfaction {
	sat := fleet.Satisfaction{RuleID: rule}
	for _, h := range hosts {
		sat.Targets = append(sat.Targets, fleet.TargetVerdict{Hostname: h, OK: true, Observed: true})
	}
	return []fleet.Satisfaction{sat}
}

func TestTheImpactPreviewWarnsAndNeverRefuses(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	facts := Facts{
		Now: now, TTL: 24 * time.Hour,
		Satisfaction: reaching("floored", "old.example.com", "new.example.com", "stale.example.com", "silent.example.com"),
		Declarations: []fleet.DeclaredAlgorithms{declaration("proxy-1", "aes128-ctr")},
		Kex: []fleet.KexObservation{
			{Hostname: "old.example.com", FloorMet: contract.FloorMetNone, Negotiated: "diffie-hellman-group14-sha1", ObservedAt: now.Add(-time.Hour)},
			{Hostname: "new.example.com", FloorMet: contract.FloorMetModernKex, ObservedAt: now.Add(-time.Hour)},
			{Hostname: "stale.example.com", FloorMet: contract.FloorMetModernKex, ObservedAt: now.Add(-48 * time.Hour)},
		},
	}
	rule := `  - id: floored
    effect: allow
    match:
      target:
        hostnames: [old.example.com, new.example.com, stale.example.com, silent.example.com]
` + route("      algorithm_floor: modern-kex\n")
	fs := run(t, rule, facts)

	below := withCode(fs, CodeTargetBelowFloor)
	if len(below) != 1 || below[0].Severity != SeverityWarning {
		t.Fatalf("below-floor findings = %+v, want one warning", below)
	}
	if targets := below[0].Parameters["targets"].([]kexView); len(targets) != 1 || targets[0].Hostname != "old.example.com" {
		t.Errorf("below the floor: %+v, want old.example.com alone", targets)
	}
	notReported := withCode(fs, CodeTargetNotReported)
	if len(notReported) != 1 || notReported[0].Severity != SeverityAdvice {
		t.Fatalf("not-reported findings = %+v, want one advice", notReported)
	}
	if got := notReported[0].Parameters["targets"].([]string); !slices.Equal(got, []string{"stale.example.com", "silent.example.com"}) {
		t.Errorf("not reported = %v; a stale observation is never shown as met", got)
	}
}

// A stored `target.algorithm_policy_unmet` under `default` warns a candidate
// route naming `default` (or naming none) for that target, and does not warn
// one naming `legacy-device`.
func TestAnUnmetRecordWarnsUnderTheProfileItWasRecordedUnder(t *testing.T) {
	t.Parallel()
	unmet := []audit.UnmetAlgorithmPolicy{{
		RecordID: "rec-1", Target: "switch.example.com", Axis: "key_exchange",
		Offered: []string{"diffie-hellman-group1-sha1"}, OfferedKnown: true, Profile: "default",
		RecordedAt: time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC),
	}}
	ruleUnder := func(profile string) string {
		extra := ""
		if profile != "" {
			extra = "      algorithm_profile: " + profile + "\n"
		}
		// A floor makes the route one the algorithm checks look at; the
		// unmet evidence is read for every route that names an algorithm
		// policy.
		return `  - id: switch
    effect: allow
    match:
      target:
        hostnames: [switch.example.com]
` + route(extra+"      algorithm_bans:\n        macs: [hmac-md5]\n")
	}
	facts := Facts{Satisfaction: reaching("switch", "switch.example.com"), Unmet: unmet}

	for _, profile := range []string{"default", ""} {
		fs := withCode(run(t, ruleUnder(profile), facts), CodePolicyUnmet)
		if len(fs) != 1 || fs[0].Severity != SeverityWarning {
			t.Fatalf("profile %q: %+v, want one warning", profile, fs)
		}
		v := fs[0].Parameters["unmet"].(unmetView)
		if v.Target != "switch.example.com" || v.Axis != "key_exchange" || len(v.Offered) != 1 {
			t.Errorf("the warning does not name the target, axis and offer: %+v", v)
		}
	}
	if fs := withCode(run(t, ruleUnder("legacy-device"), facts), CodePolicyUnmet); len(fs) != 0 {
		t.Errorf("legacy-device was warned from a record under default: %+v", fs)
	}
}

// ---------------------------------------------------------------------------
// satisfiability, both directions
// ---------------------------------------------------------------------------

func TestSatisfiabilityFindings(t *testing.T) {
	t.Parallel()
	declared, _ := fleet.MarshalCapabilities(fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthBrokeredKey},
		Execution:         []contract.ExecutionRung{contract.ExecutionNoInteractiveShell},
	})
	facts := Facts{
		Proxies: []store.Proxy{{ID: "proxy-1", Zone: "edge", DeclaredCapabilities: declared}, {ID: "proxy-silent"}},
		Satisfaction: []fleet.Satisfaction{{
			RuleID: "logs",
			Proxies: []fleet.ProxyVerdict{
				{ProxyID: "proxy-1", Zone: "edge", Live: true, Missing: []string{"execution rung account-restricted not implemented by this build"}},
				{ProxyID: "proxy-silent", Live: true, Missing: []string{"everything"}},
			},
			Targets: []fleet.TargetVerdict{
				{Hostname: "fresh.example.com", Observed: true, Missing: []string{"execution rung account-restricted not available on this target"}},
				{Hostname: "stale.example.com", Observed: false, Missing: []string{"execution rung account-restricted not available on this target"}},
				{Hostname: "fine.example.com", OK: true, Observed: true},
			},
		}},
	}
	fs := run(t, restrictedRule("logs", "fresh.example.com", "account-restricted", "/usr/bin/uptime"), facts)

	proxy := withCode(fs, CodeNoProxyProvides)
	if len(proxy) != 1 || proxy[0].Severity != SeverityWarning || proxy[0].Rule != "logs" {
		t.Fatalf("no-proxy findings = %+v, want one warning naming the rule", proxy)
	}
	short := proxy[0].Parameters["proxies"].([]proxyShortfall)
	if len(short) != 1 || short[0].ProxyID != "proxy-1" || short[0].Provides["execution"] == nil {
		t.Errorf("the finding names %+v; want proxy-1 with what it lacks and what it provides, and not the proxy that declared nothing", short)
	}
	cannot := withCode(fs, CodeTargetCannotTake)
	if len(cannot) != 1 || cannot[0].Severity != SeverityWarning {
		t.Errorf("cannot-take findings = %+v, want one warning", cannot)
	}
	unproven := withCode(fs, CodeTargetUnproven)
	if len(unproven) != 1 || unproven[0].Severity != SeverityAdvice {
		t.Errorf("a stale record is the absence of proof: %+v, want advice", unproven)
	}
}
