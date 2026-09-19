// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

const appliancePolicy = `schema_version: 1
tenant: acme
labels:
  env: [prod]
rules:
  - id: fortigate-admins
    effect: allow
    match:
      target:
        zones: [enclave]
        labels: {env: [prod]}
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      enforcement:
        execution: platform-attested
        attestation: {asserted_by: netsec, reference: baseline-7}
      credentials:
        - method: ephemeral-account
          username: hoplock-jit
          platform: fortigate
          credential_kind: publickey
          expiry_posture: proxy-enforced
          lifetime_seconds: 600
          device_fields: {vdom: root}
  - id: no-appliance-access
    effect: deny
    reason: not for you
`

func parseBundle(t *testing.T, src string) *model.Bundle {
	t.Helper()
	b, err := model.Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse policy:\n%v", err)
	}
	return b
}

// Requirements reads what a policy needs out of the policy itself, so an operator
// asks about what they wrote rather than about a second description of it.
func TestRequirementsAreExtractedFromTheBundle(t *testing.T) {
	t.Parallel()
	reqs := fleet.Requirements(parseBundle(t, appliancePolicy))

	// A deny has no route, so there is nothing a proxy could fail to satisfy.
	if len(reqs) != 1 {
		t.Fatalf("got %d requirement(s), want 1 (the allow rule only)", len(reqs))
	}
	req := reqs[0]
	if req.RuleID != "fortigate-admins" {
		t.Errorf("rule = %q", req.RuleID)
	}
	if got, want := req.Execution, contract.ExecutionPlatformAttested; got != want {
		t.Errorf("execution = %q, want %q", got, want)
	}
	if len(req.Ladder) != 1 {
		t.Fatalf("ladder has %d entr(ies), want 1", len(req.Ladder))
	}
	entry := req.Ladder[0]
	if got, want := entry.Method, contract.TargetAuthEphemeralAccount; got != want {
		t.Errorf("method = %q, want %q", got, want)
	}
	if entry.Platform != "fortigate" {
		t.Errorf("platform = %q", entry.Platform)
	}
	if got, want := entry.ExpiryPosture, contract.ExpiryPostureProxyEnforced; got != want {
		t.Errorf("expiry posture = %q, want %q", got, want)
	}
	if got, want := entry.DeviceFields, []string{"vdom"}; !slices.Equal(got, want) {
		t.Errorf("device fields = %v, want %v", got, want)
	}
	if got, want := req.TargetZones, []fleet.Zone{"enclave"}; !slices.Equal(got, want) {
		t.Errorf("zones = %v, want %v", got, want)
	}
	if got, want := req.TargetLabels["env"], []string{"prod"}; !slices.Equal(got, want) {
		t.Errorf("labels[env] = %v, want %v", got, want)
	}
}

func TestRequirementsOfNilBundleIsNothing(t *testing.T) {
	t.Parallel()
	if got := fleet.Requirements(nil); got != nil {
		t.Errorf("Requirements(nil) = %v, want nil", got)
	}
}

// A ladder needs ONE servable entry, not all of them: that is what a ladder is
// (proxy D14). A build serving only the fallback can serve the route.
func TestALadderNeedsOneServableEntry(t *testing.T) {
	t.Parallel()
	src := `schema_version: 1
tenant: acme
rules:
  - id: prefer-then-fall-back
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - {method: ephemeral-user, username: jit}
        - {method: static-key, username: svc, credential_ref: vault://k}
`
	req := fleet.Requirements(parseBundle(t, src))[0]

	// A build with only the fallback.
	fallbackOnly := fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthStaticKey},
	}
	if v := verdictFor(t, fallbackOnly, req); !v.OK {
		t.Errorf("a build serving the fallback cannot serve the route: %v", v.Missing)
	}

	// A build with neither.
	neither := fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthBrokeredKey},
	}
	v := verdictFor(t, neither, req)
	if v.OK {
		t.Fatal("a build serving no ladder entry was reported as able to")
	}
	if len(v.Missing) != 2 {
		t.Errorf("got %d reason(s), want one per entry: %v", len(v.Missing), v.Missing)
	}
	for i, m := range v.Missing {
		if !strings.HasPrefix(m, "ladder[") {
			t.Errorf("reason %d does not name the entry it is about: %q", i, m)
		}
	}
}

// THE MISMATCH THIS QUERY EXISTS TO CATCH: a policy naming `device_field.vdom`
// where the enforcing proxy's FortiGate driver does not declare `vdom`.
//
// In production that is a ladder that quietly loses a rung and says nothing — a
// skipped rung on the proxy (proxy D14), invisible in the response, and on a
// one-rung ladder a denial nobody authored.
func TestAnUndeclaredDeviceFieldIsReportedBeforePublish(t *testing.T) {
	t.Parallel()
	req := fleet.Requirements(parseBundle(t, appliancePolicy))[0]

	withField := fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthEphemeralAccount},
		Platforms:         []string{"fortigate"},
		ExpiryPostures:    []contract.ExpiryPosture{contract.ExpiryPostureProxyEnforced},
		Execution:         []contract.ExecutionRung{contract.ExecutionPlatformAttested},
		DeviceFields:      map[string][]string{"fortigate": {"vdom"}},
	}
	if v := verdictFor(t, withField, req); !v.OK {
		t.Fatalf("a build declaring vdom cannot serve the route: %v", v.Missing)
	}

	withoutField := withField
	withoutField.DeviceFields = map[string][]string{"fortigate": {"something-else"}}
	v := verdictFor(t, withoutField, req)
	if v.OK {
		t.Fatal("a build whose driver does not declare vdom was reported as able to serve the route")
	}
	found := false
	for _, m := range v.Missing {
		if strings.Contains(m, "device_field.vdom") {
			found = true
		}
	}
	if !found {
		t.Errorf("the answer does not name the undeclared field: %v", v.Missing)
	}
}

// A rung the build does not implement is reported, separately from the ladder.
func TestAnUnimplementedRungIsReported(t *testing.T) {
	t.Parallel()
	src := `schema_version: 1
tenant: acme
rules:
  - id: confined
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter:
        mode: whitelist
        exec_mode: restricted
        restricted_exec:
          commands: [{executable: /bin/true, form: exact, argv: []}]
      enforcement: {execution: account-confined, reach: account-network-isolated}
      credentials: [{method: ephemeral-user, username: jit}]
`
	req := fleet.Requirements(parseBundle(t, src))[0]

	caps := fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthEphemeralUser},
		// Neither rung implemented.
	}
	v := verdictFor(t, caps, req)
	if v.OK {
		t.Fatal("a build implementing neither rung was reported as able to serve the route")
	}
	joined := strings.Join(v.Missing, "; ")
	if !strings.Contains(joined, "account-confined") || !strings.Contains(joined, "account-network-isolated") {
		t.Errorf("the answer does not name both rungs: %v", v.Missing)
	}
}

// The absent-value default needs nothing of anybody, so a route that claims no
// rung is servable by a build that declares none.
func TestARouteClaimingNoRungNeedsNothing(t *testing.T) {
	t.Parallel()
	src := `schema_version: 1
tenant: acme
rules:
  - id: plain
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
`
	req := fleet.Requirements(parseBundle(t, src))[0]
	if v := verdictFor(t, fleet.Capabilities{}, req); !v.OK {
		t.Errorf("a route claiming no rung was refused: %v", v.Missing)
	}
}

// verdictFor is the per-proxy half of the query, which is a pure function of a
// build's declared capabilities.
func verdictFor(t *testing.T, caps fleet.Capabilities, req fleet.RouteRequirement) fleet.ProxyVerdict {
	t.Helper()
	return fleet.CheckProxy("p-under-test", "enclave", caps, true, req)
}

// THE PRE-PUBLISH QUERY, OVER BOTH SOURCES (M17). A route needs a proxy whose
// build implements the rung AND a target that can take it. Answering from the
// proxy alone would pass a policy that fails on every appliance in the estate;
// answering from the target alone would pass one no build in the fleet
// implements.
func TestCheckPolicySpansBothCapabilitySources(t *testing.T) {
	t.Parallel()
	st, r, c := newRegistry(t)
	ctx := t.Context()

	const src = `schema_version: 1
tenant: acme
labels:
  env: [prod]
rules:
  - id: confine-prod
    effect: allow
    match:
      target:
        labels: {env: [prod]}
    route:
      intent: direct
      channels: [session]
      filter:
        mode: whitelist
        exec_mode: restricted
        restricted_exec:
          commands: [{executable: /bin/true, form: exact, argv: []}]
      enforcement: {execution: account-confined}
      credentials: [{method: ephemeral-user, username: jit}]
`
	reqs := fleet.Requirements(parseBundle(t, src))

	// A proxy whose build implements the rung, and one that does not.
	enrolled(t, r, testTenant, "p-can", "prod", nil, fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthEphemeralUser},
		Execution:         []contract.ExecutionRung{contract.ExecutionAccountConfined},
	})
	enrolled(t, r, testTenant, "p-cannot", "prod", nil, fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthEphemeralUser},
	})

	// Two targets the rule matches and one it does not.
	for _, tgt := range []store.Target{
		{ID: "host-fresh", Hostname: "host-fresh.prod", Zone: "prod", Labels: map[string]string{"env": "prod"}},
		{ID: "host-unprobed", Hostname: "host-unprobed.prod", Zone: "prod", Labels: map[string]string{"env": "prod"}},
		{ID: "host-dev", Hostname: "host-dev.dev", Zone: "dev", Labels: map[string]string{"env": "dev"}},
	} {
		if err := st.Targets().Upsert(ctx, testTenant, tgt); err != nil {
			t.Fatalf("seed target %s: %v", tgt.ID, err)
		}
	}
	// Only one of them has been probed and can take the rung.
	if _, err := r.ReportTargetCapabilities(ctx, testTenant, fleet.TargetCapabilities{
		Key:        fleet.TargetCapabilityKey{Hostname: "host-fresh.prod"},
		Execution:  []contract.ExecutionRung{contract.ExecutionAccountConfined},
		ObservedAt: c.now(),
		ReportedBy: "p-can",
	}); err != nil {
		t.Fatalf("ReportTargetCapabilities: %v", err)
	}

	answers, err := r.CheckPolicy(ctx, testTenant, reqs)
	if err != nil {
		t.Fatalf("CheckPolicy: %v", err)
	}
	if len(answers) != 1 {
		t.Fatalf("got %d answer(s), want 1", len(answers))
	}
	sat := answers[0]
	if sat.RuleID != "confine-prod" {
		t.Errorf("rule = %q", sat.RuleID)
	}

	// The proxy half.
	proxies := map[string]fleet.ProxyVerdict{}
	for _, p := range sat.Proxies {
		proxies[p.ProxyID] = p
	}
	if len(proxies) != 2 {
		t.Fatalf("got %d prox(ies), want 2", len(proxies))
	}
	if !proxies["p-can"].OK {
		t.Errorf("p-can cannot serve the route: %v", proxies["p-can"].Missing)
	}
	if proxies["p-cannot"].OK {
		t.Error("p-cannot was reported as able to serve a rung its build does not implement")
	}

	// The target half — and the rule's label match decides which targets are in
	// the answer at all.
	targets := map[string]fleet.TargetVerdict{}
	for _, tv := range sat.Targets {
		targets[tv.Hostname] = tv
	}
	if _, present := targets["host-dev.dev"]; present {
		t.Error("a target the rule does not match is in the answer")
	}
	if len(targets) != 2 {
		t.Fatalf("got %d target(s), want 2: %v", len(targets), targets)
	}
	if !targets["host-fresh.prod"].OK {
		t.Errorf("the probed target cannot take the rung: %v", targets["host-fresh.prod"].Missing)
	}
	if !targets["host-fresh.prod"].Observed {
		t.Error("the probed target does not register as observed")
	}
	if targets["host-unprobed.prod"].OK {
		t.Error("an unprobed target was reported as able to take an APPLIED rung")
	}
	if targets["host-unprobed.prod"].Observed {
		t.Error("an unprobed target registered as observed")
	}

	if !sat.Satisfiable() {
		t.Error("the policy is satisfiable by one proxy and one target but reported otherwise")
	}
}

// An attested rung needs nothing of the target, so an appliance nobody can probe
// is still satisfiable. This is the answer the enforcement vocabulary exists to
// stop giving as "none available".
func TestAnAttestedRungIsSatisfiableOnAnUnprobedAppliance(t *testing.T) {
	t.Parallel()
	st, r, _ := newRegistry(t)
	ctx := t.Context()

	reqs := fleet.Requirements(parseBundle(t, appliancePolicy))

	enrolled(t, r, testTenant, "p-can", "enclave", nil, fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthEphemeralAccount},
		Platforms:         []string{"fortigate"},
		ExpiryPostures:    []contract.ExpiryPosture{contract.ExpiryPostureProxyEnforced},
		Execution:         []contract.ExecutionRung{contract.ExecutionPlatformAttested},
		DeviceFields:      map[string][]string{"fortigate": {"vdom"}},
	})
	if err := st.Targets().Upsert(ctx, testTenant, store.Target{
		ID: "fw-1", Hostname: "fw-1.enclave", Zone: "enclave",
		Labels: map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	answers, err := r.CheckPolicy(ctx, testTenant, reqs)
	if err != nil {
		t.Fatalf("CheckPolicy: %v", err)
	}
	sat := answers[0]
	if len(sat.Targets) != 1 {
		t.Fatalf("got %d target(s), want 1", len(sat.Targets))
	}
	if !sat.Targets[0].OK {
		t.Errorf("an unprobed appliance cannot take an attested rung: %v", sat.Targets[0].Missing)
	}
	if sat.Targets[0].Observed {
		t.Error("an unprobed appliance registered as observed")
	}
	if !sat.Satisfiable() {
		t.Error("the appliance policy is not satisfiable")
	}
}

// A policy no live proxy can serve is not satisfiable, and a proxy that could
// serve it but is not live is reported separately from one that cannot.
func TestSatisfiableNeedsALiveProxy(t *testing.T) {
	t.Parallel()
	_, r, c := newRegistry(t, fleet.WithLiveness(fleet.Liveness{HeartbeatTTL: time.Minute}))
	ctx := t.Context()

	const src = `schema_version: 1
tenant: acme
rules:
  - id: plain
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials: [{method: ephemeral-user, username: jit}]
`
	reqs := fleet.Requirements(parseBundle(t, src))
	enrolled(t, r, testTenant, "p-a", "prod", nil, fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthEphemeralUser},
	})

	answers, err := r.CheckPolicy(ctx, testTenant, reqs)
	if err != nil {
		t.Fatalf("CheckPolicy: %v", err)
	}
	if !answers[0].Satisfiable() {
		t.Fatal("a live, capable proxy did not satisfy the policy")
	}

	c.advance(2 * time.Minute)
	answers, err = r.CheckPolicy(ctx, testTenant, reqs)
	if err != nil {
		t.Fatalf("CheckPolicy: %v", err)
	}
	if answers[0].Satisfiable() {
		t.Error("a stale proxy satisfied the policy")
	}
	if !answers[0].Proxies[0].OK {
		t.Error("a stale proxy that CAN serve the route is reported as unable to: the two are different answers")
	}
	if answers[0].Proxies[0].Live {
		t.Error("a stale proxy is reported as live")
	}
}

// The server owns the freshness of its own record and says so in the interval it
// answers. A proxy may re-observe sooner, never later.
func TestTheServerDecidesTheReportInterval(t *testing.T) {
	t.Parallel()
	_, r, c := newRegistry(t, fleet.WithLiveness(fleet.Liveness{
		TargetCapabilityTTL: 4 * time.Hour,
		ReportAfter:         time.Hour,
	}))
	ctx := t.Context()

	after, err := r.ReportTargetCapabilities(ctx, testTenant, fleet.TargetCapabilities{
		Key:        fleet.TargetCapabilityKey{Hostname: "db-1.prod", Platform: "linux"},
		Execution:  []contract.ExecutionRung{contract.ExecutionAccountConfined},
		ObservedAt: c.now(),
	})
	if err != nil {
		t.Fatalf("ReportTargetCapabilities: %v", err)
	}
	if after != time.Hour {
		t.Errorf("report-after = %v, want 1h (the configured interval)", after)
	}
}

// An undated report is stored undated, not stamped with its arrival time: the
// proxy's clock is the only one that saw the target, and inventing a date is the
// fail-open the rule exists to prevent.
func TestAnUndatedReportIsStoredUndated(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	key := fleet.TargetCapabilityKey{Hostname: "db-1.prod", Platform: "linux"}
	if _, err := r.ReportTargetCapabilities(ctx, testTenant, fleet.TargetCapabilities{
		Key:       key,
		Execution: []contract.ExecutionRung{contract.ExecutionAccountConfined},
		// ObservedAt deliberately absent.
	}); err != nil {
		t.Fatalf("ReportTargetCapabilities: %v", err)
	}

	got, err := r.TargetRungs(ctx, testTenant, key)
	if err != nil {
		t.Fatalf("TargetRungs: %v", err)
	}
	if got.Observed {
		t.Error("an undated record was treated as fresh")
	}
	if got.AllowsExecution(contract.ExecutionAccountConfined) {
		t.Error("an undated record granted an applied rung")
	}
	if !got.AllowsExecution(contract.ExecutionPlatformAttested) {
		t.Error("an undated record withheld the attested rung, which needs nothing of the target")
	}
}

// A record past its TTL behaves exactly like an absent one, against a real clock.
func TestARecordAgesOut(t *testing.T) {
	t.Parallel()
	_, r, c := newRegistry(t, fleet.WithLiveness(fleet.Liveness{
		TargetCapabilityTTL: time.Hour,
		ReportAfter:         time.Minute,
	}))
	ctx := t.Context()

	key := fleet.TargetCapabilityKey{Hostname: "db-1.prod", Platform: "linux"}
	if _, err := r.ReportTargetCapabilities(ctx, testTenant, fleet.TargetCapabilities{
		Key:        key,
		Execution:  []contract.ExecutionRung{contract.ExecutionAccountConfined},
		ObservedAt: c.now(),
	}); err != nil {
		t.Fatalf("ReportTargetCapabilities: %v", err)
	}
	if got, _ := r.TargetRungs(ctx, testTenant, key); !got.AllowsExecution(contract.ExecutionAccountConfined) {
		t.Fatal("a fresh record did not apply")
	}

	c.advance(2 * time.Hour)
	got, err := r.TargetRungs(ctx, testTenant, key)
	if err != nil {
		t.Fatalf("TargetRungs: %v", err)
	}
	if got.Observed || got.AllowsExecution(contract.ExecutionAccountConfined) {
		t.Error("an expired record still applied")
	}
	absent, err := r.TargetRungs(ctx, testTenant, fleet.TargetCapabilityKey{Hostname: "never-probed"})
	if err != nil {
		t.Fatalf("TargetRungs: %v", err)
	}
	if !slices.Equal(got.Execution, absent.Execution) || !slices.Equal(got.Reach, absent.Reach) {
		t.Error("an expired record and an absent one answered differently")
	}
}
