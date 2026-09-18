// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// DefaultTargetCapabilityTTL is how long a target capability observation stays
// fresh, and DefaultReportAfter is the interval this server asks a proxy to
// re-observe on.
//
// THE SERVER OWNS THE FRESHNESS OF ITS OWN RECORD (M17). It answers
// `report_after_seconds` and decides the interval; a proxy may re-observe
// sooner, never later. It is the same reasoning as a cache TTL: the party that
// depends on the value decides how old it may get.
//
// The report interval is deliberately well inside the TTL, so a proxy reporting
// on schedule never has a record expire between two reports. A TTL equal to the
// interval would make every record momentarily stale on a healthy fleet, and a
// rule that fires constantly is one an operator learns to ignore.
const (
	DefaultTargetCapabilityTTL = 24 * time.Hour
	DefaultReportAfter         = 6 * time.Hour
)

// Capabilities is what one proxy BUILD declares it can provide (M17).
//
// It is operational data, not the authority on what a given connection may be
// answered with. The same distinction holds here as for the enrolled contract
// version: the proxy declares its rungs per call in
// `AuthorizeRequest.capabilities`, and that field is the one that cannot be
// stale. What is stored is a fleet-readiness signal — "can this zone serve this
// policy yet" — and the pre-publish query is what it is for.
//
// Absent declares nothing, which is the fail-safe reading.
type Capabilities struct {
	// CredentialMethods are the target-auth methods this build implements.
	CredentialMethods []contract.TargetAuthMethod `json:"credential_methods,omitempty"`
	// Platforms are the `ephemeral-account` device platforms it has drivers
	// for.
	Platforms []string `json:"platforms,omitempty"`
	// ExpiryPostures are the postures it can honour.
	ExpiryPostures []contract.ExpiryPosture `json:"expiry_postures,omitempty"`
	// Execution and Reach are the enforcement rungs this build implements.
	Execution []contract.ExecutionRung `json:"execution,omitempty"`
	Reach     []contract.ReachRung     `json:"reach,omitempty"`
	// DeviceFields maps a platform to the `device_field.<name>` names that
	// platform's driver accepts.
	//
	// The declared set is TWO LEVELS DEEP and has to be stored that way: a
	// driver declares names per platform, and the set is OPEN — the contract
	// enumerates no names (proxy D13 makes customer-written drivers
	// first-class), so this registry stores whatever a proxy declares rather
	// than validating against a list of its own. `vdom` on `fortigate` is the
	// one documented today; it is an example, not the schema.
	//
	// Why this is load-bearing rather than informational: a rung naming a field
	// the enforcing proxy's driver does not declare is a SKIPPED RUNG on the
	// proxy, not a dropped field (proxy D14 — an unknown parameter may be a
	// constraint, so a proxy that cannot honour one must not connect). The
	// ladder just gets shorter, invisibly, and on a one-rung ladder the session
	// is denied with nobody having authored the denial.
	DeviceFields map[string][]string `json:"device_fields,omitempty"`
}

// Empty reports whether this declares nothing at all.
func (c Capabilities) Empty() bool {
	return len(c.CredentialMethods) == 0 && len(c.Platforms) == 0 &&
		len(c.ExpiryPostures) == 0 && len(c.Execution) == 0 &&
		len(c.Reach) == 0 && len(c.DeviceFields) == 0
}

// HasCredentialMethod reports whether the build implements a method.
func (c Capabilities) HasCredentialMethod(m contract.TargetAuthMethod) bool {
	return slices.Contains(c.CredentialMethods, m)
}

// HasPlatform reports whether the build has a driver for a platform.
func (c Capabilities) HasPlatform(p string) bool { return slices.Contains(c.Platforms, p) }

// HasExpiryPosture reports whether the build can honour a posture.
func (c Capabilities) HasExpiryPosture(p contract.ExpiryPosture) bool {
	return slices.Contains(c.ExpiryPostures, p)
}

// HasExecutionRung reports whether the build implements an execution rung.
func (c Capabilities) HasExecutionRung(r contract.ExecutionRung) bool {
	return slices.Contains(c.Execution, r)
}

// HasReachRung reports whether the build implements a reach rung.
func (c Capabilities) HasReachRung(r contract.ReachRung) bool {
	return slices.Contains(c.Reach, r)
}

// HasDeviceField reports whether the platform's driver accepts a field name.
func (c Capabilities) HasDeviceField(platform, name string) bool {
	return slices.Contains(c.DeviceFields[platform], name)
}

// Clone returns a deep copy, so a Node cannot be edited through the value a
// caller kept.
func (c Capabilities) Clone() Capabilities {
	out := Capabilities{
		CredentialMethods: slices.Clone(c.CredentialMethods),
		Platforms:         slices.Clone(c.Platforms),
		ExpiryPostures:    slices.Clone(c.ExpiryPostures),
		Execution:         slices.Clone(c.Execution),
		Reach:             slices.Clone(c.Reach),
	}
	if c.DeviceFields != nil {
		out.DeviceFields = make(map[string][]string, len(c.DeviceFields))
		for k, v := range c.DeviceFields {
			out.DeviceFields[k] = slices.Clone(v)
		}
	}
	return out
}

// MarshalCapabilities renders a declared set for storage.
func MarshalCapabilities(c Capabilities) (json.RawMessage, error) { return json.Marshal(c) }

// UnmarshalCapabilities reads a stored set back.
//
// A document this server cannot parse yields the EMPTY set and no error, because
// the fail-safe reading of "I cannot tell what this proxy declared" is "it
// declared nothing" — which withholds rungs rather than granting them. Failing
// the call instead would turn one malformed row into a fleet-wide outage.
func UnmarshalCapabilities(raw json.RawMessage) Capabilities {
	if len(raw) == 0 {
		return Capabilities{}
	}
	var c Capabilities
	if err := json.Unmarshal(raw, &c); err != nil {
		return Capabilities{}
	}
	return c
}

// TargetCapabilityKey identifies one target capability record.
//
// Platform is part of the key because an `ephemeral-account` device is observed
// through a driver, and two drivers can legitimately see the same host
// differently.
type TargetCapabilityKey struct {
	Hostname string
	Port     int32
	Platform string
}

// TargetCapabilities is what one TARGET can take, as a proxy found it by
// probing after it had logged in.
//
// It is an OBSERVATION AND GRANTS NOTHING. The authority for a rung is the
// authorize response, and the proxy re-checks the rung against the live target
// when it provisions. So the worst a stale record can cause is a REFUSED
// SESSION — never a session running below the rung its own audit record claims.
// There is deliberately no path by which a report widens anything.
type TargetCapabilities struct {
	Key TargetCapabilityKey
	// Execution and Reach are the rungs observed.
	Execution []contract.ExecutionRung
	Reach     []contract.ReachRung
	// ObservedAt is when the proxy saw this. THE ZERO VALUE IS A REAL STATE and
	// means undated, which is treated as stale: a capability with no date has
	// no shelf life.
	ObservedAt time.Time
	// Detail is whatever the driver added, carried opaquely.
	Detail map[string]string
	// ReportedBy names the proxy that reported it. It is provenance for an
	// operator, never authority.
	ReportedBy string
}

// Fresh reports whether the record may be relied on at now.
//
// An undated record is never fresh. That is not an edge case to tidy up later:
// it is the assertion an implementation is most likely to get wrong, because a
// zero time compares as "long ago" in some formulations and as "no constraint"
// in others, and only one of those fails safe.
func (t TargetCapabilities) Fresh(now time.Time, ttl time.Duration) bool {
	if t.ObservedAt.IsZero() {
		return false
	}
	if ttl <= 0 {
		return false
	}
	return !now.After(t.ObservedAt.Add(ttl))
}

// TargetRungs is what a target may be asked for, resolved from a record that
// may be fresh, stale, undated, or missing entirely.
type TargetRungs struct {
	// Execution and Reach are the rungs available, in the contract's order.
	Execution []contract.ExecutionRung
	Reach     []contract.ReachRung
	// Observed reports whether a FRESH record contributed. False covers all
	// three fail-safe cases, and 0008 uses it to explain a refusal rather than
	// to change one.
	Observed bool
}

// AllowsExecution reports whether an execution rung is available.
func (r TargetRungs) AllowsExecution(rung contract.ExecutionRung) bool {
	return slices.Contains(r.Execution, rung)
}

// AllowsReach reports whether a reach rung is available.
func (r TargetRungs) AllowsReach(rung contract.ReachRung) bool {
	return slices.Contains(r.Reach, rung)
}

// unprovisionedExecutionRungs are the execution rungs that need NOTHING of the
// target, so they survive every fail-safe case.
//
// The membership test is `contract.ExecutionRung.RequiresProvisioning() ==
// false`, not a list somebody maintains: a rung the proxy applies to an account
// it creates on the target needs the target to support it, and a rung the proxy
// enforces in its own channel layer does not. Deriving the set from the
// contract's own predicate is what keeps it right when the vocabulary grows.
//
// It contains the proxy-side default (`proxy-inspected`), the channel-layer rung
// (`no-interactive-shell`), and the attested rung (`platform-attested`, which
// nobody applies — the target enforces it already and the proxy records who says
// so). That last one is the point of the whole rule: it is how an appliance
// nobody can probe still carries a real enforcement claim rather than dropping
// to "none available".
var unprovisionedExecutionRungs = filterExecution([]contract.ExecutionRung{
	contract.ExecutionProxyInspected,
	contract.ExecutionNoInteractiveShell,
	contract.ExecutionAccountRestricted,
	contract.ExecutionAccountConfined,
	contract.ExecutionPlatformAuthorized,
	contract.ExecutionPlatformAttested,
})

// unprovisionedReachRungs is the same set on the reach axis: the proxy-side
// default (`proxy-channel-policy`) and the attested rung.
var unprovisionedReachRungs = filterReach([]contract.ReachRung{
	contract.ReachProxyChannelPolicy,
	contract.ReachAccountEgressRestricted,
	contract.ReachAccountNetworkIsolated,
	contract.ReachPlatformAttested,
})

func filterExecution(all []contract.ExecutionRung) []contract.ExecutionRung {
	var out []contract.ExecutionRung
	for _, r := range all {
		if !r.RequiresProvisioning() {
			out = append(out, r)
		}
	}
	return out
}

func filterReach(all []contract.ReachRung) []contract.ReachRung {
	var out []contract.ReachRung
	for _, r := range all {
		if !r.RequiresProvisioning() {
			out = append(out, r)
		}
	}
	return out
}

// ResolveTargetRungs is the fail-safe rule, in one function.
//
// STALE, UNDATED AND ABSENT ARE ONE CASE (M17). A record older than its TTL, a
// record whose observation time is missing, and no record at all are treated
// identically: they provide NOTHING THAT HAS TO BE APPLIED, while leaving
// untouched every rung that needs nothing of the target.
//
// Pass rec == nil for "no record at all". The three cases converge here rather
// than at three call sites, which is what stops a later caller from getting one
// of them right and another wrong.
//
// What this must never become is "no capabilities known means deny everything".
// That reading drops an appliance estate to "none available" and is the mistake
// this function exists to make impossible.
func ResolveTargetRungs(rec *TargetCapabilities, now time.Time, ttl time.Duration) TargetRungs {
	out := TargetRungs{
		Execution: slices.Clone(unprovisionedExecutionRungs),
		Reach:     slices.Clone(unprovisionedReachRungs),
	}
	if rec == nil || !rec.Fresh(now, ttl) {
		return out
	}
	out.Observed = true
	for _, r := range rec.Execution {
		if !slices.Contains(out.Execution, r) {
			out.Execution = append(out.Execution, r)
		}
	}
	for _, r := range rec.Reach {
		if !slices.Contains(out.Reach, r) {
			out.Reach = append(out.Reach, r)
		}
	}
	return out
}

// targetCapabilitiesFromStore converts a stored row.
func targetCapabilitiesFromStore(rec store.TargetCapabilityRecord) TargetCapabilities {
	out := TargetCapabilities{
		Key: TargetCapabilityKey{
			Hostname: rec.Hostname,
			Port:     rec.Port,
			Platform: rec.Platform,
		},
		ObservedAt: rec.ObservedAt,
		ReportedBy: rec.ReportedBy,
	}
	for _, r := range rec.Execution {
		out.Execution = append(out.Execution, contract.ExecutionRung(r))
	}
	for _, r := range rec.Reach {
		out.Reach = append(out.Reach, contract.ReachRung(r))
	}
	if len(rec.Detail) > 0 {
		out.Detail = maps.Clone(rec.Detail)
	}
	return out
}

// targetCapabilitiesToStore converts back.
func targetCapabilitiesToStore(rec TargetCapabilities, receivedAt time.Time) store.TargetCapabilityRecord {
	out := store.TargetCapabilityRecord{
		Hostname:   rec.Key.Hostname,
		Port:       rec.Key.Port,
		Platform:   rec.Key.Platform,
		ObservedAt: rec.ObservedAt,
		Detail:     rec.Detail,
		ReportedBy: rec.ReportedBy,
		ReceivedAt: receivedAt,
	}
	for _, r := range rec.Execution {
		out.Execution = append(out.Execution, string(r))
	}
	for _, r := range rec.Reach {
		out.Reach = append(out.Reach, string(r))
	}
	return out
}
