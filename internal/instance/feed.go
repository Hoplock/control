// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package instance

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/hoplock/control/internal/store"
)

// The supervisory feed: a coarse event log of what changed in this deployment,
// kept in the store (migration 0024) with 0027's shape — positions in order, an
// epoch in every id, the newest N kept and a floor below which a resume is owed
// `resync` — so a registration that moves to another node with leadership
// resumes from its last event id rather than starting over.
//
// It carries changes of STATE, never records: not audit records, not decision
// records, not session content. A supervisor that wants those asks the
// north-bound API, as a client an operator can see and refuse (M19).

// Event kinds. Stable: a supervisor is written against them (M19).
const (
	EventNodeJoined         = "node_joined"
	EventNodeLeft           = "node_left"
	EventJobMoved           = "job_moved"
	EventUpgradeStarted     = "upgrade_started"
	EventUpgradeFinished    = "upgrade_finished"
	EventHealthChanged      = "health_changed"
	EventFleetHealthChanged = "fleet_health_changed"
	EventPolicyPublished    = "policy_published"
	EventCountsChanged      = "counts_changed"
)

// EventKinds is every kind the feed logs.
var EventKinds = []string{
	EventNodeJoined, EventNodeLeft, EventJobMoved, EventUpgradeStarted, EventUpgradeFinished,
	EventHealthChanged, EventFleetHealthChanged, EventPolicyPublished, EventCountsChanged,
}

// DefaultFeedRetain is how many events the feed keeps.
const DefaultFeedRetain = 1024

// snapshot is what the feed compares between two observations.
type snapshot struct {
	state    State
	reasons  []string
	nodes    map[string]string // present node → version
	stale    map[string]bool   // nodes present but not reporting
	jobs     map[string]string // job → node
	upgrade  bool
	versions []string
	fleet    map[string]State
	policy   map[string]int64
	enrolled map[string]int
	tenants  int
}

func snapshotOf(h Health, tenants int) snapshot {
	s := snapshot{
		state: h.State, nodes: map[string]string{}, stale: map[string]bool{}, jobs: map[string]string{},
		upgrade: h.Nodes.Upgrade.InProgress, versions: h.Nodes.Upgrade.Versions,
		fleet: map[string]State{}, policy: map[string]int64{}, enrolled: map[string]int{}, tenants: tenants,
	}
	for _, r := range h.Reasons {
		s.reasons = append(s.reasons, r.Code)
	}
	for _, n := range h.Nodes.Members {
		switch n.State {
		case StateLeft:
		case StateStale:
			s.nodes[n.NodeID] = n.Version
			s.stale[n.NodeID] = true
		case StateHealthy, StateDegraded, StateInProgress, StateUnknown:
			s.nodes[n.NodeID] = n.Version
		}
	}
	for _, j := range h.Nodes.Jobs {
		s.jobs[j.Job] = j.Node
	}
	for _, f := range h.Fleet {
		s.fleet[f.Tenant] = f.State
		s.policy[f.Tenant] = f.PolicyVersion
		if f.Summary != nil {
			s.enrolled[f.Tenant] = f.Summary.Enrolled
		}
	}
	return s
}

// pending is one event to log.
type pending struct {
	kind string
	data any
}

// diff lists what changed from prev to next, in a stable order.
func diff(prev, next snapshot) []pending {
	var out []pending
	for _, id := range slices.Sorted(maps.Keys(next.nodes)) {
		if _, ok := prev.nodes[id]; !ok {
			out = append(out, pending{EventNodeJoined, map[string]any{"node": id, "version": next.nodes[id]}})
		}
	}
	for _, id := range slices.Sorted(maps.Keys(prev.nodes)) {
		if _, ok := next.nodes[id]; !ok {
			out = append(out, pending{EventNodeLeft, map[string]any{"node": id, "reason": "left"}})
		} else if next.stale[id] && !prev.stale[id] {
			out = append(out, pending{EventNodeLeft, map[string]any{"node": id, "reason": "not_reporting"}})
		} else if !next.stale[id] && prev.stale[id] {
			out = append(out, pending{EventNodeJoined, map[string]any{"node": id, "version": next.nodes[id]}})
		}
	}
	for _, job := range slices.Sorted(maps.Keys(union(prev.jobs, next.jobs))) {
		if prev.jobs[job] != next.jobs[job] {
			out = append(out, pending{EventJobMoved, map[string]any{"job": job, "from": prev.jobs[job], "to": next.jobs[job]}})
		}
	}
	switch {
	case next.upgrade && !prev.upgrade:
		out = append(out, pending{EventUpgradeStarted, map[string]any{"versions": next.versions}})
	case !next.upgrade && prev.upgrade:
		out = append(out, pending{EventUpgradeFinished, map[string]any{"versions": next.versions}})
	}
	if next.state != prev.state || !slices.Equal(next.reasons, prev.reasons) {
		out = append(out, pending{EventHealthChanged, map[string]any{
			"from": prev.state, "to": next.state, "reasons": nonNil(next.reasons),
		}})
	}
	for _, t := range slices.Sorted(maps.Keys(next.fleet)) {
		if prev.fleet[t] != next.fleet[t] {
			out = append(out, pending{EventFleetHealthChanged, map[string]any{"tenant": t, "from": prev.fleet[t], "to": next.fleet[t]}})
		}
		if next.policy[t] != prev.policy[t] && next.policy[t] != 0 {
			out = append(out, pending{EventPolicyPublished, map[string]any{"tenant": t, "version": next.policy[t]}})
		}
	}
	if next.tenants != prev.tenants || !maps.Equal(next.enrolled, prev.enrolled) {
		out = append(out, pending{EventCountsChanged, map[string]any{"tenants": next.tenants, "enrolled_proxies": next.enrolled}})
	}
	return out
}

func union(a, b map[string]string) map[string]string {
	out := maps.Clone(a)
	if out == nil {
		out = map[string]string{}
	}
	maps.Copy(out, b)
	return out
}

// Feed observes the deployment and logs what changed. It runs on the node that
// holds the supervisory singleton, and only there.
type Feed struct {
	svc    *Service
	retain int
	origin string
	prev   *snapshot
}

// NewFeed builds a feed over svc, logging as origin.
func NewFeed(svc *Service, origin string, retain int) *Feed {
	if retain <= 0 {
		retain = DefaultFeedRetain
	}
	return &Feed{svc: svc, retain: retain, origin: origin}
}

// Snapshot is the whole report: identity and health at every level, every
// tenant. It is what a `resync` and a `report` carry.
func (f *Feed) Snapshot(ctx context.Context) (Report, error) {
	id, err := f.svc.Identity(ctx, nil)
	if err != nil {
		return Report{}, err
	}
	tenants := make([]store.Tenant, 0, len(id.Tenants))
	for _, t := range id.Tenants {
		tenants = append(tenants, store.Tenant(t.Tenant))
	}
	return Report{Identity: id, Health: f.svc.Health(ctx, tenants, nil)}, nil
}

// Report is identity and health together.
type Report struct {
	Identity Identity `json:"identity"`
	Health   Health   `json:"health"`
}

// Observe takes one observation and logs every change since the previous one.
// The first observation after gaining leadership is the baseline and logs
// nothing: what it would say is what a resume's `resync` carries.
func (f *Feed) Observe(ctx context.Context) (int, error) {
	rep, err := f.Snapshot(ctx)
	if err != nil {
		return 0, err
	}
	next := snapshotOf(rep.Health, len(rep.Identity.Tenants))
	if f.prev == nil {
		f.prev = &next
		return 0, nil
	}
	changes := diff(*f.prev, next)
	for i, c := range changes {
		body, err := json.Marshal(c.data)
		if err != nil {
			return i, err
		}
		if _, _, err := f.svc.o.Store.Deployment().AppendSupervision(ctx, store.SupervisionEntry{
			Kind: c.kind, Body: body, LoggedAt: f.svc.o.Now().UTC(), Origin: f.origin,
		}, f.retain); err != nil {
			// The baseline does not move past a change that was not
			// logged; the next observation tries again.
			return i, err
		}
	}
	f.prev = &next
	return len(changes), nil
}

// Reset forgets the baseline, as on losing leadership.
func (f *Feed) Reset() { f.prev = nil }

// EventID renders a feed position as an event id: 0027's format.
func EventID(epoch string, seq int64) string { return fmt.Sprintf("evt-%s-%012d", epoch, seq) }

// lineID names a position without taking one, as a heartbeat does.
func lineID(epoch string, seq int64, line uint64) string {
	return fmt.Sprintf("evt-%s-%012d.%012d", epoch, seq, line)
}

// ParseEventID reads an id back. A line id resumes from its position.
func ParseEventID(id string) (epoch string, seq int64, ok bool) {
	rest, found := strings.CutPrefix(id, "evt-")
	if !found {
		return "", 0, false
	}
	epoch, pos, found := strings.Cut(rest, "-")
	if !found || epoch == "" {
		return "", 0, false
	}
	pos, _, _ = strings.Cut(pos, ".")
	n, err := strconv.ParseInt(pos, 10, 64)
	if err != nil || n < 0 {
		return "", 0, false
	}
	return epoch, n, true
}
