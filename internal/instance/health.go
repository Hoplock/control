// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package instance

import (
	"slices"
	"time"

	"github.com/hoplock/control/internal/fleet"
)

// The health model (M19): three levels beneath one identity, every value
// observed at a time, and three rules —
//
//   - ABSENT IS NOT HEALTHY. A check that did not run, a node that has not
//     reported and a fleet summary that could not be computed are each
//     StateUnknown, with a reason naming which, and the roll-up never turns
//     one into healthy.
//   - EVERY VALUE IS TIMESTAMPED with when it was observed, and one older
//     than its freshness bound is served as StateStale, never as current.
//   - HEALTH IS READ-ONLY AND OFF THE DECISION PATH. See doc.go.

// State is a health state. Closed set (M13).
type State string

const (
	// StateHealthy is observed, fresh and fine.
	StateHealthy State = "healthy"
	// StateDegraded is observed and not fine; the reasons say why.
	StateDegraded State = "degraded"
	// StateInProgress is a rolling upgrade: nodes on different versions,
	// which is expected while a deploy runs and is not a fault.
	StateInProgress State = "in_progress"
	// StateUnknown is absent: not run, not reported, or not computable.
	StateUnknown State = "unknown"
	// StateStale is observed, but longer ago than its freshness bound.
	StateStale State = "stale"
	// StateLeft is a node that stopped on purpose. It is not in the
	// roll-up: a node scaled away is not a degraded deployment.
	StateLeft State = "left"
)

// Reason codes. Stable: they are part of the north-bound promise (M19, M21).
const (
	ReasonDatabaseUnreachable   = "database_unreachable"
	ReasonSchemaBehind          = "schema_behind"
	ReasonSchemaAhead           = "schema_ahead"
	ReasonListenerNotServing    = "listener_not_serving"
	ReasonNodeNotReporting      = "node_not_reporting"
	ReasonNodeNotReported       = "node_not_reported"
	ReasonNodesUnreadable       = "nodes_unreadable"
	ReasonMembershipUnavailable = "membership_unavailable"
	ReasonRollingUpgrade        = "rolling_upgrade"
	ReasonEventPlaneLag         = "event_plane_lag"
	ReasonEventPlaneFailures    = "event_plane_failures"
	ReasonFleetUncomputable     = "fleet_uncomputable"
	ReasonProxiesStale          = "proxies_stale"
	ReasonProxiesUnreachable    = "proxies_unreachable"
	ReasonConfigDrift           = "config_drift"
	ReasonStale                 = "stale"
	ReasonNotObserved           = "not_observed"
)

// AllReasons is every reason code, for the test that holds them stable.
var AllReasons = []string{
	ReasonDatabaseUnreachable, ReasonSchemaBehind, ReasonSchemaAhead, ReasonListenerNotServing,
	ReasonNodeNotReporting, ReasonNodeNotReported, ReasonNodesUnreadable, ReasonMembershipUnavailable,
	ReasonRollingUpgrade, ReasonEventPlaneLag, ReasonEventPlaneFailures, ReasonFleetUncomputable,
	ReasonProxiesStale, ReasonProxiesUnreachable, ReasonConfigDrift, ReasonStale, ReasonNotObserved,
}

// Reason says why a state is what it is, naming what it is about.
type Reason struct {
	Code   string `json:"code"`
	Node   string `json:"node,omitempty"`
	Tenant string `json:"tenant,omitempty"`
	// Check names the check, on the deployment's roll-up.
	Check string `json:"check,omitempty"`
	// Count is how many, where the reason counts something.
	Count int64 `json:"count,omitempty"`
}

// Health is the deployment's health at every level, as one reader may see it.
type Health struct {
	// State is the roll-up of everything below; Reasons every reason
	// beneath it, so a degraded deployment always says why.
	State   State    `json:"state"`
	Reasons []Reason `json:"reasons"`
	// ServedAt is when this answer was assembled. Each value below carries
	// its own observation time, which is earlier.
	ServedAt   time.Time        `json:"served_at"`
	Deployment DeploymentChecks `json:"deployment"`
	Nodes      NodesHealth      `json:"nodes"`
	Fleet      []FleetHealth    `json:"fleet"`
}

// DeploymentChecks are the checks only the deployment as a whole answers.
type DeploymentChecks struct {
	Database  DatabaseCheck `json:"database"`
	Listeners Check         `json:"listeners"`
}

// Check is one observed value.
type Check struct {
	State State `json:"state"`
	// ObservedAt is when it was observed; null when it never was.
	ObservedAt      *time.Time `json:"observed_at"`
	FreshForSeconds int64      `json:"fresh_for_seconds"`
	Reasons         []Reason   `json:"reasons"`
}

// DatabaseCheck is the store: reachable, and at the schema this binary
// expects.
type DatabaseCheck struct {
	State                 State      `json:"state"`
	ObservedAt            *time.Time `json:"observed_at"`
	FreshForSeconds       int64      `json:"fresh_for_seconds"`
	Reasons               []Reason   `json:"reasons"`
	SchemaVersion         int64      `json:"schema_version"`
	ExpectedSchemaVersion int64      `json:"expected_schema_version"`
}

// NodesHealth is the deployment's membership.
type NodesHealth struct {
	State           State        `json:"state"`
	ObservedAt      *time.Time   `json:"observed_at"`
	FreshForSeconds int64        `json:"fresh_for_seconds"`
	Reasons         []Reason     `json:"reasons"`
	Members         []NodeHealth `json:"members"`
	Upgrade         Upgrade      `json:"upgrade"`
	// Jobs is which node holds each leader-elected job, the supervisory
	// registration among them.
	Jobs []JobHolder `json:"jobs"`
}

// Upgrade is whether nodes run different versions.
type Upgrade struct {
	InProgress bool     `json:"in_progress"`
	Versions   []string `json:"versions"`
}

// JobHolder is one leader-elected job and the node that holds it.
type JobHolder struct {
	Job  string `json:"job"`
	Node string `json:"node"`
}

// NodeHealth is one node.
type NodeHealth struct {
	NodeID  string `json:"node_id"`
	Version string `json:"version"`
	// Member is whether the cluster coordinator lists it now.
	Member     bool       `json:"member"`
	State      State      `json:"state"`
	Reasons    []Reason   `json:"reasons"`
	StartedAt  *time.Time `json:"started_at"`
	ReportedAt *time.Time `json:"reported_at"`
	// UptimeSeconds is as of ReportedAt.
	UptimeSeconds int64           `json:"uptime_seconds"`
	Jobs          []string        `json:"jobs"`
	Listeners     map[string]bool `json:"listeners"`
	EventPlane    *PlaneView      `json:"event_plane"`
}

// PlaneView is a node's event plane (M9, M25).
type PlaneView struct {
	Tenants             []TenantLagView `json:"tenants"`
	WakeFailures        uint64          `json:"wake_failures"`
	LastWakeFailure     *time.Time      `json:"last_wake_failure"`
	PresenceFailures    uint64          `json:"presence_failures"`
	LastPresenceFailure *time.Time      `json:"last_presence_failure"`
}

// TenantLagView is how far a node trails one tenant's log.
type TenantLagView struct {
	Tenant string `json:"tenant"`
	Lag    int64  `json:"lag"`
}

// FleetHealth is one tenant's proxy fleet, summarised; the drill-down is the
// fleet view (`GET …/proxies`).
type FleetHealth struct {
	Tenant          string     `json:"tenant"`
	State           State      `json:"state"`
	ObservedAt      *time.Time `json:"observed_at"`
	FreshForSeconds int64      `json:"fresh_for_seconds"`
	Reasons         []Reason   `json:"reasons"`
	// PolicyVersion is the tenant's active policy version; absent when none
	// is active.
	PolicyVersion int64         `json:"policy_version,omitempty"`
	Summary       *FleetSummary `json:"summary"`
}

// FleetSummary is fleet.FleetSummary on the wire.
type FleetSummary struct {
	Enrolled           int            `json:"enrolled"`
	Live               int            `json:"live"`
	Stale              int            `json:"stale"`
	Unreachable        int            `json:"unreachable"`
	Pending            int            `json:"pending"`
	Revoked            int            `json:"revoked"`
	ConfigDrifted      int            `json:"config_drifted"`
	ContractVersions   map[string]int `json:"contract_versions"`
	RelayRegistrations int            `json:"relay_registrations"`
	RecentErrors       int            `json:"recent_errors"`
	Truncated          bool           `json:"truncated"`
}

func summaryView(s fleet.FleetSummary) *FleetSummary {
	return &FleetSummary{
		Enrolled: s.Enrolled, Live: s.Live, Stale: s.Stale, Unreachable: s.Unreachable,
		Pending: s.Pending, Revoked: s.Revoked, ConfigDrifted: s.ConfigDrifted,
		ContractVersions: s.ContractVersions, RelayRegistrations: s.RelayRegistrations,
		RecentErrors: s.RecentErrors, Truncated: s.Truncated,
	}
}

// fleetState judges a summary.
func fleetState(tenant string, s fleet.FleetSummary) (State, []Reason) {
	var reasons []Reason
	if s.Stale > 0 {
		reasons = append(reasons, Reason{Code: ReasonProxiesStale, Tenant: tenant, Count: int64(s.Stale)})
	}
	if s.Unreachable > 0 {
		reasons = append(reasons, Reason{Code: ReasonProxiesUnreachable, Tenant: tenant, Count: int64(s.Unreachable)})
	}
	if s.ConfigDrifted > 0 {
		reasons = append(reasons, Reason{Code: ReasonConfigDrift, Tenant: tenant, Count: int64(s.ConfigDrifted)})
	}
	if len(reasons) > 0 {
		return StateDegraded, reasons
	}
	return StateHealthy, []Reason{}
}

// rank orders states for the roll-up, worst first: something known to be
// wrong, then something not known, then a deploy in progress, then fine.
func rank(s State) int {
	switch s {
	case StateDegraded:
		return 4
	case StateUnknown, StateStale:
		return 3
	case StateInProgress:
		return 2
	case StateHealthy:
		return 1
	case StateLeft:
		return 0
	}
	return 3 // a state this switch does not know is not healthy
}

// rollup is the worst of states, with StateUnknown's family reported as
// StateUnknown. No state is ever rolled up into healthy except healthy.
func rollup(states ...State) State {
	worst := StateHealthy
	for _, s := range states {
		if s == StateLeft {
			continue
		}
		if rank(s) > rank(worst) {
			worst = s
		}
	}
	switch worst {
	case StateHealthy, StateDegraded, StateInProgress:
		return worst
	case StateUnknown, StateStale, StateLeft:
		return StateUnknown
	}
	return StateUnknown // a state this switch does not know is not healthy
}

// stamp is a time as a nullable wire value: the zero time is "never".
func stamp(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// staled marks a value stale when it is older than its bound at `now`.
func staled(state State, observed *time.Time, freshFor time.Duration, now time.Time, reasons []Reason) (State, []Reason) {
	if observed == nil {
		if state == StateHealthy {
			return StateUnknown, append(reasons, Reason{Code: ReasonNotObserved})
		}
		return state, reasons
	}
	if freshFor > 0 && now.Sub(*observed) > freshFor && state != StateLeft {
		return StateStale, append(reasons, Reason{Code: ReasonStale})
	}
	return state, reasons
}

// applyFreshness re-judges every timestamped value in h against `now`. It is
// how a cached answer is served: the values keep the time they were observed
// at, and one past its bound reads as stale rather than as current.
func applyFreshness(h Health, now time.Time) Health {
	h.ServedAt = now.UTC()
	d := &h.Deployment.Database
	d.State, d.Reasons = staled(d.State, d.ObservedAt, secs(d.FreshForSeconds), now, d.Reasons)
	l := &h.Deployment.Listeners
	l.State, l.Reasons = staled(l.State, l.ObservedAt, secs(l.FreshForSeconds), now, l.Reasons)
	n := &h.Nodes
	n.State, n.Reasons = staled(n.State, n.ObservedAt, secs(n.FreshForSeconds), now, n.Reasons)
	for i := range h.Fleet {
		f := &h.Fleet[i]
		f.State, f.Reasons = staled(f.State, f.ObservedAt, secs(f.FreshForSeconds), now, f.Reasons)
	}
	states := []State{d.State, l.State, n.State}
	reasons := []Reason{}
	reasons = appendChecked(reasons, "database", d.Reasons)
	reasons = appendChecked(reasons, "listeners", l.Reasons)
	reasons = appendChecked(reasons, "nodes", n.Reasons)
	for _, f := range h.Fleet {
		states = append(states, f.State)
		reasons = appendChecked(reasons, "fleet", f.Reasons)
	}
	h.State = rollup(states...)
	h.Reasons = reasons
	return h
}

func appendChecked(into []Reason, check string, rs []Reason) []Reason {
	for _, r := range rs {
		r.Check = check
		into = append(into, r)
	}
	return into
}

func secs(n int64) time.Duration { return time.Duration(n) * time.Second }

func seconds(d time.Duration) int64 { return int64((d + time.Second - 1) / time.Second) }

// cloneHealth deep-copies the slices a caller may filter, so a cached answer
// is never edited through a served one.
func cloneHealth(h Health) Health {
	h.Reasons = slices.Clone(h.Reasons)
	h.Deployment.Database.Reasons = slices.Clone(h.Deployment.Database.Reasons)
	h.Deployment.Listeners.Reasons = slices.Clone(h.Deployment.Listeners.Reasons)
	h.Nodes.Reasons = slices.Clone(h.Nodes.Reasons)
	h.Nodes.Members = slices.Clone(h.Nodes.Members)
	for i := range h.Nodes.Members {
		m := &h.Nodes.Members[i]
		m.Reasons = slices.Clone(m.Reasons)
		if m.EventPlane != nil {
			p := *m.EventPlane
			p.Tenants = slices.Clone(p.Tenants)
			m.EventPlane = &p
		}
	}
	h.Fleet = slices.Clone(h.Fleet)
	for i := range h.Fleet {
		h.Fleet[i].Reasons = slices.Clone(h.Fleet[i].Reasons)
	}
	return h
}
