// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"slices"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
)

var capNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// THE ASSERTION THIS PHASE EXISTS TO GET RIGHT (M17).
//
// A target with a fresh record, one with an expired record, one whose record has
// no observation time, and one with no record at all: the last three yield the
// SAME answer, and all four still allow the proxy-side defaults and the attested
// rung.
//
// The undated case is asserted explicitly because it is the one an implementation
// is most likely to treat as fresh: a zero time compares as "no constraint" in
// some formulations, and only "stale" fails safe.
func TestStaleUndatedAndAbsentAreOneCase(t *testing.T) {
	t.Parallel()
	const ttl = 24 * time.Hour

	// An applied rung, which needs the target to support it.
	applied := contract.ExecutionAccountConfined
	appliedReach := contract.ReachAccountNetworkIsolated

	fresh := &fleet.TargetCapabilities{
		Key:        fleet.TargetCapabilityKey{Hostname: "db-1", Platform: "linux"},
		Execution:  []contract.ExecutionRung{applied},
		Reach:      []contract.ReachRung{appliedReach},
		ObservedAt: capNow.Add(-time.Hour),
	}
	expired := &fleet.TargetCapabilities{
		Key:        fleet.TargetCapabilityKey{Hostname: "db-2", Platform: "linux"},
		Execution:  []contract.ExecutionRung{applied},
		Reach:      []contract.ReachRung{appliedReach},
		ObservedAt: capNow.Add(-48 * time.Hour),
	}
	undated := &fleet.TargetCapabilities{
		Key:       fleet.TargetCapabilityKey{Hostname: "db-3", Platform: "linux"},
		Execution: []contract.ExecutionRung{applied},
		Reach:     []contract.ReachRung{appliedReach},
		// ObservedAt deliberately left zero.
	}

	freshRungs := fleet.ResolveTargetRungs(fresh, capNow, ttl)
	if !freshRungs.Observed {
		t.Error("a fresh record did not register as observed")
	}
	if !freshRungs.AllowsExecution(applied) {
		t.Errorf("a fresh record did not make %q available", applied)
	}
	if !freshRungs.AllowsReach(appliedReach) {
		t.Errorf("a fresh record did not make %q available", appliedReach)
	}

	// The three fail-safe states, compared against each other rather than
	// against a written-out expectation: the claim is that they are ONE case.
	cases := map[string]fleet.TargetRungs{
		"expired": fleet.ResolveTargetRungs(expired, capNow, ttl),
		"undated": fleet.ResolveTargetRungs(undated, capNow, ttl),
		"absent":  fleet.ResolveTargetRungs(nil, capNow, ttl),
	}
	want := cases["absent"]
	for name, got := range cases {
		if got.Observed {
			t.Errorf("%s: registered as observed, want not", name)
		}
		if !slices.Equal(got.Execution, want.Execution) {
			t.Errorf("%s: execution = %v, absent = %v: the three states must be one",
				name, got.Execution, want.Execution)
		}
		if !slices.Equal(got.Reach, want.Reach) {
			t.Errorf("%s: reach = %v, absent = %v: the three states must be one",
				name, got.Reach, want.Reach)
		}
		if got.AllowsExecution(applied) {
			t.Errorf("%s: allowed the applied rung %q, which needs the target", name, applied)
		}
		if got.AllowsReach(appliedReach) {
			t.Errorf("%s: allowed the applied rung %q, which needs the target", name, appliedReach)
		}
	}

	// And all four still allow what needs nothing of the target: the two
	// proxy-side defaults and an attested rung. That last one is the point —
	// it is how an appliance nobody can probe still carries a real enforcement
	// claim rather than dropping to "none available".
	all := map[string]fleet.TargetRungs{"fresh": freshRungs}
	for name, got := range cases {
		all[name] = got
	}
	for name, got := range all {
		if !got.AllowsExecution(contract.ExecutionProxyInspected) {
			t.Errorf("%s: the execution default is not available", name)
		}
		if !got.AllowsReach(contract.ReachProxyChannelPolicy) {
			t.Errorf("%s: the reach default is not available", name)
		}
		if !got.AllowsExecution(contract.ExecutionPlatformAttested) {
			t.Errorf("%s: the attested execution rung is not available", name)
		}
		if !got.AllowsReach(contract.ReachPlatformAttested) {
			t.Errorf("%s: the attested reach rung is not available", name)
		}
	}
}

// "No capabilities known" must never mean "deny everything". Stated as its own
// test because it is the reading the rule exists to make impossible.
func TestNoRecordIsNotDenyEverything(t *testing.T) {
	t.Parallel()
	got := fleet.ResolveTargetRungs(nil, capNow, 24*time.Hour)
	if len(got.Execution) == 0 || len(got.Reach) == 0 {
		t.Fatalf("no record produced no rungs at all: %+v", got)
	}
}

// The fail-safe set is exactly the rungs that require no provisioning, derived
// from the contract's own predicate rather than from a list somebody maintains.
func TestTheFailSafeSetIsTheUnprovisionedRungs(t *testing.T) {
	t.Parallel()
	got := fleet.ResolveTargetRungs(nil, capNow, 24*time.Hour)

	for _, r := range got.Execution {
		if r.RequiresProvisioning() {
			t.Errorf("%q needs the proxy to administer an account on the target, so it must not survive an absent record", r)
		}
	}
	for _, r := range got.Reach {
		if r.RequiresProvisioning() {
			t.Errorf("%q needs the proxy to administer an account on the target, so it must not survive an absent record", r)
		}
	}

	// And nothing unprovisioned is missing, checked against the vocabulary
	// rather than against a copy of it.
	everyExecution := []contract.ExecutionRung{
		contract.ExecutionProxyInspected, contract.ExecutionNoInteractiveShell,
		contract.ExecutionAccountRestricted, contract.ExecutionAccountConfined,
		contract.ExecutionPlatformAuthorized, contract.ExecutionPlatformAttested,
	}
	for _, r := range everyExecution {
		if !r.RequiresProvisioning() && !got.AllowsExecution(r) {
			t.Errorf("%q needs nothing of the target but is not available", r)
		}
	}
	everyReach := []contract.ReachRung{
		contract.ReachProxyChannelPolicy, contract.ReachAccountEgressRestricted,
		contract.ReachAccountNetworkIsolated, contract.ReachPlatformAttested,
	}
	for _, r := range everyReach {
		if !r.RequiresProvisioning() && !got.AllowsReach(r) {
			t.Errorf("%q needs nothing of the target but is not available", r)
		}
	}
}

// A zero or negative TTL means stale rather than "no expiry". A configuration
// that switched the rule off by omission would be the fail-open direction.
func TestAZeroTTLIsStaleRatherThanForever(t *testing.T) {
	t.Parallel()
	rec := &fleet.TargetCapabilities{
		Execution:  []contract.ExecutionRung{contract.ExecutionAccountConfined},
		ObservedAt: capNow,
	}
	if rec.Fresh(capNow, 0) {
		t.Error("a zero TTL made a record fresh")
	}
	if fleet.ResolveTargetRungs(rec, capNow, 0).Observed {
		t.Error("a zero TTL let an observation through")
	}
}

// A record observed exactly at the TTL boundary is still fresh; one instant past
// it is not. Stated because "stale" is a number and the boundary is where a
// reader guesses.
func TestFreshnessBoundaryIsInclusive(t *testing.T) {
	t.Parallel()
	const ttl = time.Hour
	rec := fleet.TargetCapabilities{ObservedAt: capNow.Add(-ttl)}

	if !rec.Fresh(capNow, ttl) {
		t.Error("a record observed exactly one TTL ago is not fresh")
	}
	if rec.Fresh(capNow.Add(time.Nanosecond), ttl) {
		t.Error("a record one instant past its TTL is still fresh")
	}
}

// A capability document this server cannot parse declares NOTHING, which
// withholds rungs rather than granting them — and does not fail the call, because
// one malformed row must not be a fleet-wide outage.
func TestUnparseableCapabilitiesDeclareNothing(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "null", "[]", "not json", `{"execution":5}`} {
		got := fleet.UnmarshalCapabilities([]byte(raw))
		if !got.Empty() {
			t.Errorf("%q declared %+v, want nothing", raw, got)
		}
	}
}

// The declared set round-trips, including the two-level device-field map, because
// storing it flat is exactly the mistake that loses a per-platform field name.
func TestCapabilitiesRoundTrip(t *testing.T) {
	t.Parallel()
	want := fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{
			contract.TargetAuthEphemeralUser, contract.TargetAuthEphemeralAccount,
		},
		Platforms:      []string{"linux", "fortigate"},
		ExpiryPostures: []contract.ExpiryPosture{contract.ExpiryPostureTargetEnforced},
		Execution:      []contract.ExecutionRung{contract.ExecutionAccountConfined},
		Reach:          []contract.ReachRung{contract.ReachAccountEgressRestricted},
		DeviceFields: map[string][]string{
			// The one documented field today. It is an example, not the schema:
			// the set is open because the contract enumerates no names.
			"fortigate": {"vdom"},
		},
	}

	raw, err := fleet.MarshalCapabilities(want)
	if err != nil {
		t.Fatalf("MarshalCapabilities: %v", err)
	}
	got := fleet.UnmarshalCapabilities(raw)

	if !got.HasDeviceField("fortigate", "vdom") {
		t.Error("the per-platform device field did not survive the round trip")
	}
	if got.HasDeviceField("linux", "vdom") {
		t.Error("a device field leaked across platforms: the set is two levels deep")
	}
	if !got.HasCredentialMethod(contract.TargetAuthEphemeralAccount) {
		t.Error("a credential method did not survive")
	}
	if got.HasCredentialMethod(contract.TargetAuthStaticKey) {
		t.Error("an undeclared credential method appeared")
	}
	if !got.HasExecutionRung(contract.ExecutionAccountConfined) {
		t.Error("an execution rung did not survive")
	}
	if !got.HasExpiryPosture(contract.ExpiryPostureTargetEnforced) {
		t.Error("an expiry posture did not survive")
	}
}

// A device-field name this registry has never heard of is stored and matched.
// Validating against a list of this server's own would reject exactly the
// customer-written driver proxy D13 makes first-class.
func TestAnUnknownDeviceFieldNameIsStoredRatherThanRefused(t *testing.T) {
	t.Parallel()
	caps := fleet.Capabilities{
		Platforms:    []string{"some-vendor-nobody-shipped"},
		DeviceFields: map[string][]string{"some-vendor-nobody-shipped": {"partition", "context_name"}},
	}
	raw, err := fleet.MarshalCapabilities(caps)
	if err != nil {
		t.Fatalf("MarshalCapabilities: %v", err)
	}
	got := fleet.UnmarshalCapabilities(raw)
	for _, name := range []string{"partition", "context_name"} {
		if !got.HasDeviceField("some-vendor-nobody-shipped", name) {
			t.Errorf("device_field.%s was not stored", name)
		}
	}
}

// Clone is a deep copy, so a Node's capabilities cannot be edited through a value
// a caller kept.
func TestCapabilitiesCloneIsDeep(t *testing.T) {
	t.Parallel()
	orig := fleet.Capabilities{
		Platforms:    []string{"linux"},
		DeviceFields: map[string][]string{"fortigate": {"vdom"}},
	}
	copied := orig.Clone()
	orig.Platforms[0] = "changed"
	orig.DeviceFields["fortigate"][0] = "changed"

	if copied.Platforms[0] != "linux" {
		t.Error("Clone aliased the platform slice")
	}
	if copied.DeviceFields["fortigate"][0] != "vdom" {
		t.Error("Clone aliased the device-field slice")
	}
}
