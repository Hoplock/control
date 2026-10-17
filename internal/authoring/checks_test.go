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

// The unmet-policy warning is about the route's PROFILE first (#66): a route
// naming no floor and no ban still reaches a target that reported it cannot
// meet the profile, and `default` is exactly the profile that stopped reaching
// SHA-1-only devices. It warns whatever else the route names.
func TestAnUnmetRecordWarnsARouteThatNamesOnlyAProfile(t *testing.T) {
	t.Parallel()
	unmet := []audit.UnmetAlgorithmPolicy{{
		RecordID: "rec-1", Target: "switch.example.com", Axis: "key_exchange",
		Offered: []string{"diffie-hellman-group1-sha1"}, OfferedKnown: true, Profile: "default",
		RecordedAt: time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC),
	}}
	facts := Facts{Satisfaction: reaching("switch", "switch.example.com"), Unmet: unmet}
	for _, extra := range []string{"", "      algorithm_profile: default\n"} {
		rule := `  - id: switch
    effect: allow
    match:
      target:
        hostnames: [switch.example.com]
` + route(extra)
		if fs := withCode(run(t, rule, facts), CodePolicyUnmet); len(fs) != 1 || fs[0].Severity != SeverityWarning {
			t.Errorf("a route under %q with no floor or ban: %+v, want one warning", extra, fs)
		}
	}
}

// A floor record warns a route naming that floor or a higher one, and a ban
// record warns a route naming one of the bans it failed; neither warns a route
// that names less.
func TestAnUnmetRecordWarnsByItsCause(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	unmet := []audit.UnmetAlgorithmPolicy{
		{RecordID: "rec-floor", Target: "a.example.com", Axis: "key_exchange", Cause: "floor",
			Profile: "default", Floor: "modern-kex", RecordedAt: at},
		{RecordID: "rec-ban", Target: "b.example.com", Axis: "cipher", Cause: "ban",
			Profile: "default", Bans: []string{"ciphers:aes128-ctr"}, RecordedAt: at},
	}
	ruleWith := func(host, extra string) string {
		return `  - id: r
    effect: allow
    match:
      target:
        hostnames: [` + host + `]
` + route(extra)
	}
	cases := []struct {
		name, host, extra string
		want              int
	}{
		{"the floor it failed", "a.example.com", "      algorithm_floor: modern-kex\n", 1},
		{"a higher floor", "a.example.com", "      algorithm_floor: pq-hybrid-kex\n", 1},
		{"no floor", "a.example.com", "", 0},
		{"the ban it failed", "b.example.com", "      algorithm_bans:\n        ciphers: [aes128-ctr]\n", 1},
		{"another ban", "b.example.com", "      algorithm_bans:\n        ciphers: [aes256-ctr]\n", 0},
	}
	for _, tc := range cases {
		facts := Facts{Satisfaction: reaching("r", tc.host), Unmet: unmet}
		if fs := withCode(run(t, ruleWith(tc.host, tc.extra), facts), CodePolicyUnmet); len(fs) != tc.want {
			t.Errorf("%s: %d warnings, want %d: %+v", tc.name, len(fs), tc.want, fs)
		}
	}
}

// Whether a ban is a typo is judged against what builds DECLARED they offer.
// When no build has said, nothing can be judged, and that is the absence of
// proof (M17): advice, never a warning that holds activation — the emergency
// runbook's ban is the publish least able to wait on one.
func TestABanIsNeverAWarningWhenNoBuildDeclaredItsOffer(t *testing.T) {
	t.Parallel()
	floorsOnly := fleet.DeclaredAlgorithms{ProxyID: "proxy-old", Declaration: fleet.AlgorithmDeclaration{
		AlgorithmFloors: declaration("x").Declaration.AlgorithmFloors,
	}}
	for name, decls := range map[string][]fleet.DeclaredAlgorithms{
		"no declaration at all":           nil,
		"declarations without algorithms": {floorsOnly},
	} {
		fs := run(t, bannedRule("ban", "a.example.com", "        ciphers: [aes128-cbc]\n"), Facts{Declarations: decls})
		if got := withCode(fs, CodeBanUnmatched); len(got) != 0 {
			t.Errorf("%s: %+v; a ban nobody could judge is not a typo warning", name, got)
		}
		unknown := withCode(fs, CodeAlgorithmUnknown)
		if !slices.ContainsFunc(unknown, func(f Finding) bool {
			return f.ID == findingID(CodeAlgorithmUnknown, "ban") && f.Severity == SeverityAdvice
		}) {
			t.Errorf("%s: %+v, want advice that the ban could not be judged", name, unknown)
		}
	}
	// A build that declared its offer judges every name, and a name it offers
	// is no finding at all.
	offered := withCode(run(t, bannedRule("ban", "a.example.com", "        ciphers: [aes128-ctr]\n"),
		Facts{Declarations: []fleet.DeclaredAlgorithms{declaration("proxy-1", "aes128-ctr", "aes256-ctr"), floorsOnly}}), CodeBanUnmatched)
	if len(offered) != 0 {
		t.Errorf("a banned name a build offers: %+v, want nothing", offered)
	}
}

// A hinted rule gated on a source network is reused from ANY address: the
// proxy keys a cached decision on identity, target, port, method and hop trail
// (contract CacheHint), never on the client address. No duration and no grant
// bounds that, because the reused answer admits a connection the rule would
// not have matched at all.
func TestACacheHintOnASourceNetworkIsAWarningWhateverBoundsTheSession(t *testing.T) {
	t.Parallel()
	const office = "      context:\n        source_cidrs: [10.0.0.0/8]\n"
	for _, tc := range []struct{ name, match, routeExtra string }{
		{"unbounded", office, ""},
		{"with a duration", office, "      max_session_duration: 1h\n"},
		{"grant-gated", office + "      grant:\n        required: true\n        scopes: [ops]\n", ""},
	} {
		fs := withCode(run(t, cachedRule("office", "a.example.com", tc.match, tc.routeExtra), Facts{}), CodeCacheHintSpansSources)
		if len(fs) != 1 || fs[0].Severity != SeverityWarning || fs[0].Rule != "office" {
			t.Errorf("%s: %+v, want one warning on rule office", tc.name, fs)
			continue
		}
		if !strings.Contains(fs[0].Message, "drop the cache hint") {
			t.Errorf("%s: the message does not name the fix: %s", tc.name, fs[0].Message)
		}
	}
	// Without the hint, or without the source term, there is nothing to say.
	if fs := withCode(run(t, cachedRule("plain", "a.example.com", "      subject:\n        groups: [sre]\n", ""), Facts{}), CodeCacheHintSpansSources); len(fs) != 0 {
		t.Errorf("a hinted rule with no source term: %+v", fs)
	}
}

// ---------------------------------------------------------------------------
// finding ids: stable for one bundle against one fleet
// ---------------------------------------------------------------------------

// everyKind is a bundle and a fleet that produce most of Control's codes at
// once, so the id tests below cover them together.
func everyKind(now time.Time) (string, Facts) {
	rules := restrictedRule("logs", "app.example.com", "account-restricted", "/usr/bin/find", "/usr/bin/python3") +
		cachedRule("office", "office.example.com", "      context:\n        time_of_day: \"09:00-17:00\"\n        source_cidrs: [10.0.0.0/8]\n", "") +
		bannedRule("ban", "ban.example.com", "        ciphers: [aes128-ctr, aes128-crt]\n        macs: [hmac-md5-typo]\n") +
		`  - id: floored
    effect: allow
    match:
      target:
        hostnames: [old.example.com]
` + route("      algorithm_floor: pq-hybrid-kex\n")
	declared, _ := fleet.MarshalCapabilities(fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthBrokeredKey},
		Execution:         []contract.ExecutionRung{contract.ExecutionNoInteractiveShell},
	})
	sat := func(rule string, hosts ...string) fleet.Satisfaction {
		s := fleet.Satisfaction{RuleID: rule, Proxies: []fleet.ProxyVerdict{
			{ProxyID: "proxy-1", Live: true, Missing: []string{"execution rung account-restricted not implemented by this build"}},
		}}
		for _, h := range hosts {
			s.Targets = append(s.Targets, fleet.TargetVerdict{Hostname: h, Observed: true, Missing: []string{"x"}})
		}
		return s
	}
	return rules, Facts{
		Now: now, TTL: 24 * time.Hour,
		Proxies:      []store.Proxy{{ID: "proxy-1", DeclaredCapabilities: declared}},
		Satisfaction: []fleet.Satisfaction{sat("logs", "app.example.com"), sat("floored", "old.example.com")},
		Declarations: []fleet.DeclaredAlgorithms{declaration("proxy-1", "aes128-ctr"), declaration("proxy-2", "aes128-ctr")},
		Kex:          []fleet.KexObservation{{Hostname: "old.example.com", FloorMet: contract.FloorMetModernKex, ObservedAt: now.Add(-time.Hour)}},
		Unmet: []audit.UnmetAlgorithmPolicy{{
			RecordID: "rec-1", Target: "app.example.com", Axis: "key_exchange", Profile: "default", RecordedAt: now.Add(-time.Hour),
		}},
	}
}

func ids(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.ID)
	}
	return out
}

// The same bundle against the same fleet gives the same ids, in the same
// order, run after run. Map iteration order differs between runs of one
// process, so a part derived from it fails here.
func TestFindingIDsAreTheSameOnEveryRun(t *testing.T) {
	t.Parallel()
	source, facts := everyKind(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	first := run(t, source, facts)
	sortFindings(first)
	codes := map[string]bool{}
	for _, f := range first {
		codes[f.Code] = true
		// Control's checks never block (M17): what is wrong whatever the
		// fleet runs is the compiler's, and the rest is a fact that may
		// change.
		if f.Severity == SeverityBlocking {
			t.Errorf("Control's own check blocked: %+v", f)
		}
	}
	if len(codes) < 9 {
		t.Fatalf("the fixture produces %d codes (%v), want most of them", len(codes), codes)
	}
	for i := 0; i < 50; i++ {
		again := run(t, source, facts)
		sortFindings(again)
		if !slices.Equal(ids(first), ids(again)) {
			t.Fatalf("run %d: ids %v, first run %v", i, ids(again), ids(first))
		}
	}
	if seen := slices.Compact(slices.Sorted(slices.Values(ids(first)))); len(seen) != len(first) {
		t.Errorf("two findings share an id: %v", ids(first))
	}
}

// An id is what a finding is ABOUT, never when it was computed or how it was
// worded: the same fleet seen an hour later, with the evidence re-dated, gives
// the same ids — which is what lets CI carry its -accept lines forward.
func TestFindingIDsDoNotCarryTimeOrWording(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	source, facts := everyKind(now)
	before := run(t, source, facts)

	later := facts
	later.Now = now.Add(time.Hour)
	later.Unmet = []audit.UnmetAlgorithmPolicy{facts.Unmet[0]}
	later.Unmet[0].RecordedAt = now.Add(-2 * time.Hour)
	later.Unmet[0].RecordID = "rec-2"
	later.Kex = []fleet.KexObservation{facts.Kex[0]}
	later.Kex[0].ObservedAt = now.Add(-30 * time.Minute)
	after := run(t, strings.Replace(source, "ttl_seconds: 300", "ttl_seconds: 120", 1), later)

	sortFindings(before)
	sortFindings(after)
	if !slices.Equal(ids(before), ids(after)) {
		t.Errorf("ids moved with time or wording:\nbefore %v\nafter  %v", ids(before), ids(after))
	}
}

// ---------------------------------------------------------------------------
// each check's negative case
// ---------------------------------------------------------------------------

// Nothing declared is advice, once, and silences every per-build judgement;
// one declaring proxy replaces it with the judgement.
func TestNoProxyHavingDeclaredIsAdviceAndNothingElse(t *testing.T) {
	t.Parallel()
	silent := Facts{
		Proxies: []store.Proxy{{ID: "proxy-silent"}},
		Satisfaction: []fleet.Satisfaction{{RuleID: "logs", Proxies: []fleet.ProxyVerdict{
			{ProxyID: "proxy-silent", Live: true, Missing: []string{"everything"}},
		}}},
	}
	fs := run(t, restrictedRule("logs", "app.example.com", "account-restricted", "/usr/bin/uptime"), silent)
	if got := withCode(fs, CodeProxiesUnreported); len(got) != 1 || got[0].Severity != SeverityAdvice {
		t.Errorf("no proxy declared: %+v, want one advice", got)
	}
	if got := withCode(fs, CodeNoProxyProvides); len(got) != 0 {
		t.Errorf("a proxy that declared nothing was judged: %+v", got)
	}

	declared, _ := fleet.MarshalCapabilities(fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthEphemeralUser},
		Execution:         []contract.ExecutionRung{contract.ExecutionAccountRestricted},
	})
	serving := Facts{
		Proxies: []store.Proxy{{ID: "proxy-1", DeclaredCapabilities: declared}, {ID: "proxy-silent"}},
		Satisfaction: []fleet.Satisfaction{{RuleID: "logs", Proxies: []fleet.ProxyVerdict{
			{ProxyID: "proxy-1", Live: true, OK: true},
			{ProxyID: "proxy-silent", Live: true, Missing: []string{"everything"}},
		}}},
	}
	fs = run(t, restrictedRule("logs", "app.example.com", "account-restricted", "/usr/bin/uptime"), serving)
	if got := append(withCode(fs, CodeProxiesUnreported), withCode(fs, CodeNoProxyProvides)...); len(got) != 0 {
		t.Errorf("one declaring proxy serves the route: %+v, want nothing", got)
	}
}

// A target that cannot take the rung is named, and only that one: a stale
// record is advice and an able target is no finding.
func TestTheTargetsATargetFindingNamesAreTheOnesShort(t *testing.T) {
	t.Parallel()
	facts := Facts{Satisfaction: []fleet.Satisfaction{{RuleID: "logs", Targets: []fleet.TargetVerdict{
		{Hostname: "fresh.example.com", Observed: true, Missing: []string{"x"}},
		{Hostname: "stale.example.com", Missing: []string{"x"}},
		{Hostname: "fine.example.com", OK: true, Observed: true},
	}}}}
	fs := run(t, restrictedRule("logs", "fresh.example.com", "account-restricted", "/usr/bin/uptime"), facts)
	cannot := withCode(fs, CodeTargetCannotTake)
	if len(cannot) != 1 {
		t.Fatalf("cannot-take: %+v", cannot)
	}
	if got := cannot[0].Parameters["targets"].([]targetShortfall); len(got) != 1 || got[0].Hostname != "fresh.example.com" {
		t.Errorf("cannot-take names %+v, want fresh.example.com alone", got)
	}
	unproven := withCode(fs, CodeTargetUnproven)
	if len(unproven) != 1 || !slices.Equal(unproven[0].Parameters["targets"].([]string), []string{"stale.example.com"}) {
		t.Errorf("unproven: %+v, want stale.example.com alone", unproven)
	}
}

// A rung that claims no boundary has no allow-list to weaken, and a list
// replaced by configuration is the whole list.
func TestTheInterpreterWarningsNegatives(t *testing.T) {
	t.Parallel()
	noShell := strings.Replace(restrictedRule("n", "app.example.com", "no-interactive-shell", "/usr/bin/python3"),
		"      channels: [session]\n", "      channels: [session]\n      requests:\n        types: [exec]\n", 1)
	if fs := withCode(run(t, noShell, Facts{}), CodeInterpreter); len(fs) != 0 {
		t.Errorf("no-interactive-shell with python: %+v, want nothing", fs)
	}
	c := candidate(t, restrictedRule("logs", "app.example.com", "account-restricted", "/usr/bin/find", "/usr/bin/uptime"))
	fs := withCode(controlFindings(c.Bundle, c.Program, Facts{}, []string{"uptime"}), CodeInterpreter)
	if len(fs) != 1 || fs[0].Parameters["executable"] != "/usr/bin/uptime" {
		t.Errorf("a configured list of [uptime]: %+v, want uptime alone", fs)
	}
}

// A build that declares the floor's level is not named.
func TestAFloorABuildDeclaresIsNoFinding(t *testing.T) {
	t.Parallel()
	facts := Facts{Declarations: []fleet.DeclaredAlgorithms{declaration("proxy-1", "aes128-ctr")}}
	rule := `  - id: modern
    effect: allow
    match:
      target:
        hostnames: [a.example.com]
` + route("      algorithm_floor: modern-kex\n")
	if fs := withCode(run(t, rule, facts), CodeFloorUndeclared); len(fs) != 0 {
		t.Errorf("modern-kex, which proxy-1 declares: %+v, want nothing", fs)
	}
}
