// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package eval_test

import (
	"encoding/json"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/policy/compile"
	"github.com/hoplock/control/internal/policy/eval"
	"github.com/hoplock/control/internal/policy/model"
)

// mustProgram parses and compiles a bundle, failing the test with every
// rejection rather than the first — the same courtesy the compiler extends to
// an author.
func mustProgram(t *testing.T, src string) *compile.Program {
	t.Helper()
	b, err := model.Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse:\n%v", err)
	}
	prog, err := compile.Compile(b)
	if err != nil {
		t.Fatalf("compile:\n%v", err)
	}
	return prog
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad timestamp %q: %v", s, err)
	}
	return ts
}

// prodInput is the input the showcase policy is written for.
func prodInput(t *testing.T) model.Input {
	t.Helper()
	return model.Input{
		Now: at(t, "2026-09-18T10:00:00Z"),
		Subject: model.Subject{
			ID:         "alice@example.com",
			Source:     "okta",
			Groups:     []string{"sre", "oncall"},
			AuthMethod: model.AuthMethodCert,
			MFA:        true,
		},
		Context: model.Context{
			SourceAddr: netip.MustParseAddr("10.1.2.3"),
			ProxyID:    "edge-1",
		},
		Target: model.Target{
			Hostname: "db01.prod.example.com",
			Port:     22,
			Zone:     "prod-dc1",
			Labels:   map[string]string{"env": "prod", "kind": "host"},
		},
	}
}

// TestProductPolicy is the acceptance case: the one policy that makes this
// product distinct compiles and evaluates to exactly that snapshot, axis by
// axis. It is written out long-hand rather than compared against a golden blob
// because each assertion is a separate promise the product makes.
func TestProductPolicy(t *testing.T) {
	src, err := os.ReadFile("testdata/prod-shell.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	prog := mustProgram(t, string(src))
	in := prodInput(t)

	snap, why := eval.Evaluate(prog, in)
	if snap == nil {
		t.Fatalf("expected an allow, got %v", why)
	}
	if why.Effect != model.EffectAllow || why.Rule != "prod-shell" {
		t.Fatalf("explanation = %v, want an allow by prod-shell", why)
	}

	// May open a shell.
	if got, want := snap.Channels, []model.ChannelType{model.ChannelSession, model.ChannelDirectTCPIP}; !equalSlices(got, want) {
		t.Errorf("channels = %v, want %v", got, want)
	}
	if snap.Requests == nil {
		t.Fatal("requests must be policed; an absent allow-list permits everything")
	}
	for _, want := range []model.RequestType{model.RequestPTYReq, model.RequestShell, model.RequestExec} {
		if !snap.Requests.Permits(want) {
			t.Errorf("request %q should be permitted", want)
		}
	}
	// May not run sftp: with the request policy present, a subsystem is
	// permitted only by being named, and none is.
	if snap.Requests.Subsystems != nil && len(*snap.Requests.Subsystems) != 0 {
		t.Errorf("subsystems = %v, want none permitted", *snap.Requests.Subsystems)
	}

	// May tunnel only to postgres.prod:5432.
	if snap.Forwards == nil || len(snap.Forwards.DirectTCPIP) != 1 {
		t.Fatalf("forwards = %+v, want exactly one permitted destination", snap.Forwards)
	}
	if d := snap.Forwards.DirectTCPIP[0]; d.Host != "postgres.prod" || d.Port != 5432 {
		t.Errorf("destination = %s, want postgres.prod:5432", d)
	}

	// May never open a listener: no forwarded-tcpip channel, and the global
	// request allow-list is present and empty, which denies tcpip-forward.
	for _, ch := range snap.Channels {
		if ch == model.ChannelForwardedTCPIP {
			t.Error("forwarded-tcpip must not be permitted")
		}
	}
	if snap.GlobalRequests == nil {
		t.Fatal("global requests must be policed, or tcpip-forward is relayed unpoliced")
	}
	if len(snap.GlobalRequests.Types) != 0 {
		t.Errorf("global requests = %v, want none permitted", snap.GlobalRequests.Types)
	}

	// May only run the argv shapes on this list.
	if snap.Filter.ExecMode != model.ExecModeRestricted {
		t.Errorf("exec mode = %q, want restricted", snap.Filter.ExecMode)
	}
	if snap.Filter.RestrictedExec == nil || len(snap.Filter.RestrictedExec.Commands) != 2 {
		t.Fatalf("restricted exec = %+v, want two commands", snap.Filter.RestrictedExec)
	}
	if len(snap.Filter.Rules) != 0 {
		t.Errorf("filter rules = %v, want none: the two tiers are alternatives", snap.Filter.Rules)
	}

	// The rest of the snapshot.
	if len(snap.Credentials) != 1 {
		t.Fatalf("ladder = %+v, want one entry", snap.Credentials)
	}
	if got := snap.Credentials[0].Username; got.Value != "alice@example.com" {
		t.Errorf("username = %q, want the authenticated subject", got.Value)
	}
	if want := in.Now.Add(8 * time.Hour); !snap.SessionDeadline.Equal(want) {
		t.Errorf("session deadline = %v, want %v", snap.SessionDeadline, want)
	}
	if !snap.RequireSessionCapture {
		t.Error("a record-session obligation must make capture required")
	}
	if snap.Concurrency.MaxSessionsPerSubject != 2 {
		t.Errorf("per-subject cap = %d, want 2", snap.Concurrency.MaxSessionsPerSubject)
	}
	if snap.Cache == nil || snap.Cache.TTLSeconds != 300 {
		t.Fatalf("cache hint = %+v, want a 300s hint", snap.Cache)
	}
	if snap.Target != "db01.prod.example.com" || snap.Port != 22 {
		t.Errorf("target = %s:%d, want the target that was asked about", snap.Target, snap.Port)
	}
}

// TestProductPolicyDenies checks the other half of the fixture: an identity the
// rule does not admit is denied by the rule that says so, not by silence.
func TestProductPolicyDenies(t *testing.T) {
	src, err := os.ReadFile("testdata/prod-shell.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	prog := mustProgram(t, string(src))

	in := prodInput(t)
	in.Subject.Groups = []string{"dba"}

	snap, why := eval.Evaluate(prog, in)
	if snap != nil {
		t.Fatalf("expected a deny, got a snapshot: %+v", snap)
	}
	if why.Rule != "default-deny-prod" || why.Basis != model.BasisRule {
		t.Fatalf("explanation = %v, want a deny by default-deny-prod", why)
	}
	if !strings.Contains(why.DenyReason, "SRE group") {
		t.Errorf("deny reason = %q, want the rule's own reason", why.DenyReason)
	}
	if len(why.Terms) == 0 {
		t.Error("a deny by rule must still name the inputs that made it match")
	}
}

const orderedBundle = `
schema_version: 1
tenant: acme
labels:
  env: [prod, dev]
groups: [sre, dba]
rules:
  - id: sre-on-prod
    effect: allow
    match:
      subject: {groups: [sre]}
      target: {labels: {env: [prod]}}
    route: {intent: direct, channels: [session], filter: {mode: whitelist}}
  - id: nobody-on-prod
    effect: deny
    reason: production is SRE-only
    match:
      target: {labels: {env: [prod]}}
  - id: everything-else
    effect: allow
    match:
      target: {labels: {env: [dev]}}
    route: {intent: direct, channels: [session], filter: {mode: whitelist}}
`

// TestFirstMatchWins uses overlapping rules: the SRE rule and the blanket deny
// both match an SRE on production, and order is the whole of the difference.
func TestFirstMatchWins(t *testing.T) {
	prog := mustProgram(t, orderedBundle)

	sre := model.Input{
		Now:     at(t, "2026-09-18T10:00:00Z"),
		Subject: model.Subject{ID: "alice", Groups: []string{"sre"}},
		Target:  model.Target{Hostname: "db01", Labels: map[string]string{"env": "prod"}},
	}
	snap, why := eval.Evaluate(prog, sre)
	if snap == nil || why.Rule != "sre-on-prod" {
		t.Fatalf("explanation = %v, want the first matching rule to win", why)
	}

	other := sre
	other.Subject = model.Subject{ID: "bob", Groups: []string{"dba"}}
	snap, why = eval.Evaluate(prog, other)
	if snap != nil || why.Rule != "nobody-on-prod" {
		t.Fatalf("explanation = %v, want the second rule to decide", why)
	}
}

// TestDefaultDeny asserts the always-present default-deny: an input nothing
// matches is denied, the basis says so, and the reason is recorded rather than
// left for a reader to infer from silence.
func TestDefaultDeny(t *testing.T) {
	prog := mustProgram(t, orderedBundle)

	in := model.Input{
		Now:     at(t, "2026-09-18T10:00:00Z"),
		Subject: model.Subject{ID: "carol"},
		Target:  model.Target{Hostname: "lab01", Labels: map[string]string{"env": "staging"}},
	}
	snap, why := eval.Evaluate(prog, in)
	if snap != nil {
		t.Fatalf("expected a deny, got %+v", snap)
	}
	if why.Basis != model.BasisDefaultDeny {
		t.Errorf("basis = %q, want %q", why.Basis, model.BasisDefaultDeny)
	}
	if why.Rule != "" {
		t.Errorf("rule = %q, want empty: no rule decided", why.Rule)
	}
	if why.DenyReason != eval.DefaultDenyReason {
		t.Errorf("reason = %q, want %q", why.DenyReason, eval.DefaultDenyReason)
	}
	if why.RulesConsidered != 3 {
		t.Errorf("rules considered = %d, want every rule", why.RulesConsidered)
	}
}

// TestExplanationNamesDecidingInputs treats the explanation as the product
// surface it is: for an allow and for a deny alike, it must name the rule and
// the input values that made it match. A wrong explanation is a product bug.
func TestExplanationNamesDecidingInputs(t *testing.T) {
	const src = `
schema_version: 1
tenant: acme
labels:
  env: [prod]
groups: [sre]
rules:
  - id: allow-sre
    effect: allow
    match:
      subject: {groups: [sre], mfa: true, auth_methods: [cert]}
      context: {proxy_ids: [edge-1], source_cidrs: ["10.0.0.0/8"]}
      target: {hostnames: ["*.prod.example.com"], labels: {env: [prod]}, zones: [dc1]}
    route: {intent: direct, channels: [session], filter: {mode: whitelist}}
  - id: deny-rest
    effect: deny
    reason: not an SRE
    match:
      target: {zones: [dc1]}
`
	prog := mustProgram(t, src)
	in := model.Input{
		Now: at(t, "2026-09-18T10:00:00Z"),
		Subject: model.Subject{
			ID: "alice", Groups: []string{"sre"}, MFA: true, AuthMethod: model.AuthMethodCert,
		},
		Context: model.Context{SourceAddr: netip.MustParseAddr("10.1.2.3"), ProxyID: "edge-1"},
		Target: model.Target{
			Hostname: "db01.prod.example.com", Zone: "dc1",
			Labels: map[string]string{"env": "prod"},
		},
	}

	_, why := eval.Evaluate(prog, in)
	if why.Rule != "allow-sre" {
		t.Fatalf("rule = %q, want allow-sre", why.Rule)
	}
	want := []model.MatchedTerm{
		{Axis: model.AxisSubject, Term: "groups", Value: "sre"},
		{Axis: model.AxisSubject, Term: "auth_method", Value: "cert"},
		{Axis: model.AxisSubject, Term: "mfa", Value: "true"},
		{Axis: model.AxisContext, Term: "source_network", Value: "10.0.0.0/8"},
		{Axis: model.AxisContext, Term: "proxy_id", Value: "edge-1"},
		{Axis: model.AxisTarget, Term: "hostname", Value: "*.prod.example.com"},
		{Axis: model.AxisTarget, Term: "labels.env", Value: "prod"},
		{Axis: model.AxisTarget, Term: "zone", Value: "dc1"},
	}
	if !equalSlices(why.Terms, want) {
		t.Errorf("terms =\n  %v\nwant\n  %v", why.Terms, want)
	}

	deny := in
	deny.Subject.Groups = nil
	_, why = eval.Evaluate(prog, deny)
	if why.Rule != "deny-rest" || why.Effect != model.EffectDeny {
		t.Fatalf("explanation = %v, want a deny by deny-rest", why)
	}
	if !equalSlices(why.Terms, []model.MatchedTerm{{Axis: model.AxisTarget, Term: "zone", Value: "dc1"}}) {
		t.Errorf("deny terms = %v, want the zone that made it match", why.Terms)
	}
}

// TestInputAxes walks every axis a rule may match on, in both directions: the
// input that satisfies the rule and one that does not.
func TestInputAxes(t *testing.T) {
	base := func() model.Input {
		return model.Input{
			Now: at(t, "2026-09-16T10:30:00Z"), // a Wednesday
			Subject: model.Subject{
				ID: "alice@example.com", Source: "okta",
				Groups: []string{"sre"}, Claims: map[string]string{"department": "infra"},
				AuthMethod: model.AuthMethodCert, MFA: true,
			},
			Device:  &model.Device{Posture: map[string]string{"disk_encryption": "on"}},
			Context: model.Context{SourceAddr: netip.MustParseAddr("10.1.2.3"), ProxyID: "edge-1"},
			Target: model.Target{
				Hostname: "db01.prod.example.com", Zone: "dc1",
				Labels: map[string]string{"env": "prod"},
			},
			Grants: []model.Grant{{
				ID: "g-1", Subject: "alice@example.com", Origin: model.GrantOriginWorkflow,
				Scope:     model.GrantScope{Name: "prod-dba"},
				ExpiresAt: at(t, "2026-09-16T12:00:00Z"),
			}},
		}
	}

	cases := []struct {
		name     string
		match    string
		mismatch func(*model.Input)
	}{
		{"subject id", `subject: {ids: ["alice@example.com"]}`, func(i *model.Input) { i.Subject.ID = "mallory" }},
		{"idp source", `subject: {sources: [okta]}`, func(i *model.Input) { i.Subject.Source = "adfs" }},
		{"groups", `subject: {groups: [sre]}`, func(i *model.Input) { i.Subject.Groups = []string{"dba"} }},
		{"claims", `subject: {claims: {department: [infra]}}`, func(i *model.Input) { i.Subject.Claims = nil }},
		{"auth method", `subject: {auth_methods: [cert]}`, func(i *model.Input) { i.Subject.AuthMethod = model.AuthMethodPasswordMFA }},
		{"mfa", `subject: {mfa: true}`, func(i *model.Input) { i.Subject.MFA = false }},
		{"device posture", `device: {required: true, posture: {disk_encryption: [on]}}`, func(i *model.Input) { i.Device = nil }},
		{"day of week", `context: {days: [wednesday]}`, func(i *model.Input) { i.Now = at(t, "2026-09-19T10:30:00Z") }},
		{"time of day", `context: {time_of_day: "09:00-17:00"}`, func(i *model.Input) { i.Now = at(t, "2026-09-16T22:30:00Z") }},
		{"source network", `context: {source_cidrs: ["10.0.0.0/8"]}`, func(i *model.Input) { i.Context.SourceAddr = netip.MustParseAddr("192.0.2.7") }},
		{"asking proxy", `context: {proxy_ids: [edge-1]}`, func(i *model.Input) { i.Context.ProxyID = "enclave-9" }},
		{"target hostname", `target: {hostnames: ["*.prod.example.com"]}`, func(i *model.Input) { i.Target.Hostname = "db01.dev.example.com" }},
		{"target labels", `target: {labels: {env: [prod]}}`, func(i *model.Input) { i.Target.Labels = map[string]string{"env": "dev"} }},
		{"target zone", `target: {zones: [dc1]}`, func(i *model.Input) { i.Target.Zone = "dc2" }},
		{"live grant", `grant: {required: true, scopes: [prod-dba], origins: [workflow]}`, func(i *model.Input) { i.Grants = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `
schema_version: 1
tenant: acme
labels:
  env: [prod, dev]
groups: [sre, dba]
rules:
  - id: under-test
    effect: allow
    match:
      ` + tc.match + `
    route: {intent: direct, channels: [session], filter: {mode: whitelist}}
`
			prog := mustProgram(t, src)

			if snap, why := eval.Evaluate(prog, base()); snap == nil {
				t.Fatalf("the matching input was denied: %v", why)
			}
			broken := base()
			tc.mismatch(&broken)
			if snap, why := eval.Evaluate(prog, broken); snap != nil {
				t.Fatalf("the non-matching input was allowed: %v", why)
			}
		})
	}
}

// TestPostureAbsenceIsNotCompliance is the one device-axis case worth its own
// test: an endpoint that supplied no posture must not satisfy a rule that
// constrains posture. Absence is never read as compliance.
func TestPostureAbsenceIsNotCompliance(t *testing.T) {
	const src = `
schema_version: 1
tenant: acme
rules:
  - id: needs-encryption
    effect: allow
    match:
      device: {posture: {disk_encryption: [on]}}
    route: {intent: direct, channels: [session], filter: {mode: whitelist}}
`
	prog := mustProgram(t, src)
	in := model.Input{Now: at(t, "2026-09-18T10:00:00Z"), Subject: model.Subject{ID: "alice"}}
	if snap, _ := eval.Evaluate(prog, in); snap != nil {
		t.Fatal("an endpoint with no posture satisfied a posture constraint")
	}
	in.Device = &model.Device{Posture: map[string]string{"disk_encryption": "on"}}
	if snap, why := eval.Evaluate(prog, in); snap == nil {
		t.Fatalf("a compliant endpoint was denied: %v", why)
	}
}

// TestGrantBoundsTheDeadline: where a grant supplied the access, its expiry and
// the window an external system asserted bound the session deadline. The proxy
// enforces only that instant, so this server weighs the window rather than
// sending it along and hoping.
func TestGrantBoundsTheDeadline(t *testing.T) {
	const src = `
schema_version: 1
tenant: acme
rules:
  - id: jit
    effect: allow
    match:
      grant: {required: true}
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      max_session_duration: 8h
`
	prog := mustProgram(t, src)
	now := at(t, "2026-09-18T10:00:00Z")
	in := model.Input{
		Now:     now,
		Subject: model.Subject{ID: "alice"},
		Target:  model.Target{Hostname: "db01"},
		Grants: []model.Grant{{
			ID: "g-7", Subject: "alice", Origin: model.GrantOriginExternal,
			Scope:     model.GrantScope{Name: "change-window"},
			ExpiresAt: now.Add(30 * time.Minute),
			External: &model.ExternalReference{
				System: "helix", Reference: "CHG0042",
				WindowStart: now, WindowEnd: now.Add(20 * time.Minute),
				AdditionalContext: &model.AdditionalContext{Text: "approved by change board"},
			},
		}},
	}

	snap, why := eval.Evaluate(prog, in)
	if snap == nil {
		t.Fatalf("expected an allow: %v", why)
	}
	if want := now.Add(20 * time.Minute); !snap.SessionDeadline.Equal(want) {
		t.Errorf("deadline = %v, want the asserted window's end %v", snap.SessionDeadline, want)
	}
	if snap.GrantContext == nil || snap.GrantContext.Reference != "CHG0042" {
		t.Fatalf("grant context = %+v, want the ticket named", snap.GrantContext)
	}
	if snap.GrantContext.Origin != model.GrantOriginExternal {
		t.Errorf("origin = %q, want the grant's own origin recorded", snap.GrantContext.Origin)
	}
	if why.Grant != "g-7" {
		t.Errorf("explanation grant = %q, want g-7", why.Grant)
	}
}

// TestDeterminism: the same inputs produce byte-identical snapshots and
// explanations. Maps are involved on every side of this — claims, labels,
// posture, device fields — and Go randomises their iteration, so this is the
// test that catches a term order derived from one.
func TestDeterminism(t *testing.T) {
	const src = `
schema_version: 1
tenant: acme
labels:
  env: [prod]
  owner: [payments, search]
  kind: [appliance]
groups: [sre]
rules:
  - id: wide
    effect: allow
    match:
      subject:
        groups: [sre]
        claims: {department: [infra], region: [emea]}
      device:
        posture: {disk_encryption: [on], firewall: [on], patched: ["true"]}
      target:
        labels: {env: [prod], owner: [payments], kind: [appliance]}
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - method: ephemeral-account
          username: hoplock-jit
          platform: fortios
          credential_kind: publickey
          expiry_posture: proxy-enforced
          lifetime_seconds: 600
          device_fields: {vdom: root, tenant_partition: "7", zzz: last}
`
	prog := mustProgram(t, src)
	in := model.Input{
		Now: at(t, "2026-09-18T10:00:00Z"),
		Subject: model.Subject{
			ID: "alice", Groups: []string{"sre"},
			Claims: map[string]string{"department": "infra", "region": "emea", "unused": "x"},
		},
		Device: &model.Device{Posture: map[string]string{
			"disk_encryption": "on", "firewall": "on", "patched": "true",
		}},
		Target: model.Target{
			Hostname: "fw01", Port: 22,
			Labels: map[string]string{"env": "prod", "owner": "payments", "kind": "appliance"},
		},
	}

	var first string
	for i := range 200 {
		snap, why := eval.Evaluate(prog, in)
		if snap == nil {
			t.Fatalf("run %d was denied: %v", i, why)
		}
		blob, err := json.Marshal(struct {
			Snapshot    *model.Snapshot
			Explanation eval.Explanation
		}{snap, why})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if i == 0 {
			first = string(blob)
			continue
		}
		if string(blob) != first {
			t.Fatalf("run %d differs:\n got %s\nwant %s", i, blob, first)
		}
	}
}

// TestSnapshotDoesNotAliasTheProgram: a program is served from memory to every
// request, so a snapshot that shared its slices would let one caller's edit
// become another caller's policy.
func TestSnapshotDoesNotAliasTheProgram(t *testing.T) {
	const src = `
schema_version: 1
tenant: acme
rules:
  - id: only
    effect: allow
    route:
      intent: direct
      channels: [session, direct-tcpip]
      forwards:
        direct_tcpip: [{host: db.internal, port: 5432}]
      filter: {mode: whitelist}
`
	prog := mustProgram(t, src)
	in := model.Input{Now: at(t, "2026-09-18T10:00:00Z"), Subject: model.Subject{ID: "alice"}}

	first, _ := eval.Evaluate(prog, in)
	first.Channels[0] = model.ChannelX11
	first.Forwards.DirectTCPIP[0].Host = "attacker.example.com"

	second, _ := eval.Evaluate(prog, in)
	if second.Channels[0] != model.ChannelSession {
		t.Errorf("channels were aliased: %v", second.Channels)
	}
	if second.Forwards.DirectTCPIP[0].Host != "db.internal" {
		t.Errorf("destinations were aliased: %v", second.Forwards.DirectTCPIP)
	}
}

// TestTenantsAreSeparatePrograms is M18's load-bearing case, written over the
// fixture a filter would silently get right and a shared program would silently
// get wrong: both tenants use the same rule ids and the same labels.
func TestTenantsAreSeparatePrograms(t *testing.T) {
	bundle := func(tenant, zone string) string {
		return `
schema_version: 1
tenant: ` + tenant + `
labels:
  env: [prod]
groups: [sre]
rules:
  - id: prod-access
    effect: allow
    match:
      subject: {groups: [sre]}
      target: {labels: {env: [prod]}, zones: [` + zone + `]}
    route: {intent: direct, channels: [session], filter: {mode: whitelist}}
`
	}
	acme := mustProgram(t, bundle("acme", "acme-dc1"))
	globex := mustProgram(t, bundle("globex", "globex-dc1"))

	set, err := compile.NewSet(acme, globex)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := compile.NewSet(acme, acme); err == nil {
		t.Error(`two programs for one tenant must be an error: "which program is served" has one answer`)
	}

	in := model.Input{
		Now:     at(t, "2026-09-18T10:00:00Z"),
		Subject: model.Subject{ID: "alice", Groups: []string{"sre"}},
		Target: model.Target{
			Hostname: "db01", Zone: "globex-dc1",
			Labels: map[string]string{"env": "prod"},
		},
	}

	// The input is globex's. Evaluated against acme's program it must find
	// nothing — not acme's identically named rule.
	prog, ok := set.Program("acme")
	if !ok {
		t.Fatal("acme's program is missing from the set")
	}
	if snap, why := eval.Evaluate(prog, in); snap != nil {
		t.Fatalf("acme's program matched globex's input: %v", why)
	}

	prog, ok = set.Program("globex")
	if !ok {
		t.Fatal("globex's program is missing from the set")
	}
	snap, why := eval.Evaluate(prog, in)
	if snap == nil {
		t.Fatalf("globex's own program denied its own input: %v", why)
	}
	if why.BundleDigest == acme.Digest() {
		t.Error("the explanation names the wrong bundle")
	}
}

// TestUsernameIsNeverTheClientTypedLogin. The vocabulary cannot express the
// substitution the contract closed, so the check is that what it *can* express
// resolves from the authenticated subject and nowhere else.
func TestUsernameIsNeverTheClientTypedLogin(t *testing.T) {
	const src = `
schema_version: 1
tenant: acme
rules:
  - id: only
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - method: ephemeral-user
          username: {from: subject-local-part}
        - method: static-key
          username: svc-break-glass
`
	prog := mustProgram(t, src)
	in := model.Input{
		Now:     at(t, "2026-09-18T10:00:00Z"),
		Subject: model.Subject{ID: "alice@example.com"},
	}
	snap, _ := eval.Evaluate(prog, in)
	if got := snap.Credentials[0].Username.Value; got != "alice" {
		t.Errorf("derived username = %q, want the subject's local part", got)
	}
	if got := snap.Credentials[1].Username.Value; got != "svc-break-glass" {
		t.Errorf("literal username = %q, want it unchanged", got)
	}
}

func equalSlices[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// maximalRoute exercises every output axis at once, so that a snapshot losing
// one of them fails here rather than at a proxy.
const maximalRoute = `
schema_version: 1
tenant: acme
rules:
  - id: everything
    effect: allow
    route:
      intent: hops-permitted
      target: bastion.example.com
      port: 2222
      channels: [session, direct-tcpip, x11]
      requests:
        types: [pty-req, shell, exec, env, x11-req, auth-agent-req]
        subsystems: [sftp]
      forwards:
        direct_tcpip:
          - {host: "10.0.0.0/8", port_range: {from: 5000, to: 6000}}
        forwarded_tcpip:
          - {host: "*.internal"}
      global_requests:
        types: [tcpip-forward, cancel-tcpip-forward]
      algorithm_profile: legacy-device
      filter:
        mode: blacklist
        rules:
          - {match: "rm -rf /", action: kill_session, message: "no"}
      enforcement:
        execution: platform-attested
        reach: platform-attested
        attestation: {asserted_by: netops, reference: CIS-1}
      credentials:
        - method: brokered-key
          username: netadmin
          credential_ref: vault://fw01
      max_session_duration: 30m
      require_session_capture: true
      concurrency: {max_sessions_per_subject: 3, max_sessions_per_target: 9}
    obligations:
      - {kind: require-approval, message: "on-call must approve"}
      - {kind: require-step-up}
    cache: {key: [subject, target, target-port, proxy, auth-method, rule], ttl_seconds: 60}
`

// TestOutputAxes walks every axis of the snapshot vocabulary (PLAN §5.2).
func TestOutputAxes(t *testing.T) {
	prog := mustProgram(t, maximalRoute)
	in := model.Input{
		Now:     at(t, "2026-09-18T10:00:00Z"),
		Subject: model.Subject{ID: "alice"},
		Target:  model.Target{Hostname: "fw01", Port: 22},
	}
	snap, why := eval.Evaluate(prog, in)
	if snap == nil {
		t.Fatalf("expected an allow: %v", why)
	}

	checks := []struct {
		axis string
		ok   bool
	}{
		{"route intent", snap.Intent == model.RouteIntentHopsPermitted},
		{"route target override", snap.Target == "bastion.example.com" && snap.Port == 2222},
		{"channel types", len(snap.Channels) == 3},
		{"in-channel requests", snap.Requests != nil && len(snap.Requests.Types) == 6},
		{"subsystems by name", snap.Requests != nil && snap.Requests.Subsystems != nil &&
			len(*snap.Requests.Subsystems) == 1},
		{"forwarding destinations", snap.Forwards != nil &&
			len(snap.Forwards.DirectTCPIP) == 1 && len(snap.Forwards.ForwardedTCPIP) == 1},
		{"port range", snap.Forwards != nil && snap.Forwards.DirectTCPIP[0].PortRange != nil &&
			snap.Forwards.DirectTCPIP[0].PortRange.To == 6000},
		{"global requests", snap.GlobalRequests != nil && len(snap.GlobalRequests.Types) == 2},
		{"filter policy", snap.Filter.Mode == model.FilterModeBlacklist && len(snap.Filter.Rules) == 1},
		{"credential ladder", len(snap.Credentials) == 1 &&
			snap.Credentials[0].Method == model.CredentialBrokeredKey &&
			snap.Credentials[0].CredentialRef == "vault://fw01"},
		{"algorithm profile", snap.AlgorithmProfile == model.AlgorithmProfileLegacyDevice},
		{"enforcement execution", snap.Enforcement.Execution == model.ExecutionPlatformAttested},
		{"enforcement reach", snap.Enforcement.Reach == model.ReachPlatformAttested},
		{"attestation", snap.Enforcement.Attestation != nil &&
			snap.Enforcement.Attestation.AssertedBy == "netops"},
		{"session deadline", snap.SessionDeadline.Equal(in.Now.Add(30 * time.Minute))},
		{"required session capture", snap.RequireSessionCapture},
		{"concurrency caps", snap.Concurrency.MaxSessionsPerSubject == 3 &&
			snap.Concurrency.MaxSessionsPerTarget == 9},
		{"cache hint", snap.Cache != nil && len(snap.Cache.Key) == 6 && snap.Cache.TTLSeconds == 60},
		{"obligations", len(snap.Obligations) == 2},
		{"obligations in the explanation", len(why.Obligations) == 2 &&
			why.Obligations[0] == model.ObligationRequireApproval},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s did not survive into the snapshot", c.axis)
		}
	}
}

// TestEnforcementIsNeverSynthesised. The absent value on both axes is
// proxy-side enforcement only, and it is exactly what a rule that says nothing
// about enforcement must emit — the engine never fills in a weaker rung,
// because a silent downgrade is what the vocabulary exists to prevent.
func TestEnforcementIsNeverSynthesised(t *testing.T) {
	prog := mustProgram(t, `
schema_version: 1
tenant: acme
rules:
  - {id: quiet, effect: allow, route: {intent: direct, channels: [session], filter: {mode: whitelist}}}
`)
	snap, _ := eval.Evaluate(prog, model.Input{
		Now: at(t, "2026-09-18T10:00:00Z"), Subject: model.Subject{ID: "alice"},
	})
	if snap.Enforcement.Stated() {
		t.Fatalf("enforcement = %+v, want nothing stated", snap.Enforcement)
	}
	if snap.Enforcement.Execution != model.ExecutionRungUnset || snap.Enforcement.Reach != model.ReachRungUnset {
		t.Errorf("a rung was synthesised: %+v", snap.Enforcement)
	}
}
