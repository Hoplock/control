// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package instance

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// Defaults for the health model's bounds. Configuration overrides them
// (`instance:`).
const (
	// DefaultReportInterval is how often a node writes its report. A node
	// whose report is older than three of these is not reporting.
	DefaultReportInterval = 10 * time.Second
	// DefaultMinInterval is the most often health is computed, however
	// often it is asked: the maximum rate a poller can impose.
	DefaultMinInterval = time.Second
	// DefaultFreshFor bounds the deployment and fleet checks.
	DefaultFreshFor = 30 * time.Second
	// DefaultNodeForgetAfter is how long a node that stopped reporting
	// stays in the answer before it is forgotten.
	DefaultNodeForgetAfter = 24 * time.Hour
	// DefaultLagThreshold is how many positions a node may trail a
	// tenant's log before it reads as degraded.
	DefaultLagThreshold int64 = 100
	// computeTimeout bounds one computation of health.
	computeTimeout = 5 * time.Second
)

// PlaneSource is the event plane on this node (revoke.Bus).
type PlaneSource interface {
	PlaneHealth() revoke.PlaneHealth
}

// Options configure a Service.
type Options struct {
	// Store is read for health and written for node reports. It SHOULD be
	// a pool of its own (store.WithMaxConns), so that health never holds a
	// connection the authorize path needs (M5). Required.
	Store *store.Store
	// Coordinator is the cluster coordinator: who this node is, who the
	// members are. Required.
	Coordinator ext.ClusterCoordinator
	// Fleet counts each tenant's fleet. It SHOULD read through Store's
	// pool. Required.
	Fleet *fleet.Registry
	// Plane is this node's event plane; nil reports none.
	Plane PlaneSource
	// Versions is the deployment's version set.
	Versions Versions
	// DisplayName is the operator's name for the deployment.
	DisplayName string
	// ExpectedSchema is the migration version this binary expects.
	ExpectedSchema int64

	ReportInterval     time.Duration
	MinInterval        time.Duration
	FreshFor           time.Duration
	NodeForgetAfter    time.Duration
	TenantActiveWindow time.Duration
	LagThreshold       int64

	Logger *slog.Logger
	Now    func() time.Time
}

// Service is the deployment's identity and health.
type Service struct {
	o       Options
	tenants *Tenants

	// What this node reports about itself.
	selfMu    sync.Mutex
	listeners map[string]bool
	jobs      map[string]bool

	// The health cache. computeMu is the single flight: a burst of polls
	// computes once.
	computeMu sync.Mutex
	base      *Health // deployment + nodes, no fleet
	baseAt    time.Time
	fleets    map[store.Tenant]fleetEntry
}

type fleetEntry struct {
	at time.Time
	h  FleetHealth
}

// New builds a Service.
func New(o Options) (*Service, error) {
	if o.Store == nil || o.Coordinator == nil || o.Fleet == nil {
		return nil, errors.New("instance: a store, a cluster coordinator and a fleet registry are required")
	}
	if o.ReportInterval <= 0 {
		o.ReportInterval = DefaultReportInterval
	}
	if o.MinInterval <= 0 {
		o.MinInterval = DefaultMinInterval
	}
	if o.FreshFor <= 0 {
		o.FreshFor = DefaultFreshFor
	}
	if o.NodeForgetAfter <= 0 {
		o.NodeForgetAfter = DefaultNodeForgetAfter
	}
	if o.TenantActiveWindow <= 0 {
		o.TenantActiveWindow = DefaultTenantActiveWindow
	}
	if o.LagThreshold <= 0 {
		o.LagThreshold = DefaultLagThreshold
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{
		o:         o,
		tenants:   NewTenants(o.Store, o.Now),
		listeners: map[string]bool{},
		jobs:      map[string]bool{},
		fleets:    map[store.Tenant]fleetEntry{},
	}, nil
}

// Tenants is the tracker the paths that resolve a tenant touch.
func (s *Service) Tenants() *Tenants { return s.tenants }

// Versions is the deployment's version set.
func (s *Service) Versions() Versions { return s.o.Versions }

// SetListener records whether one of this node's listeners is serving.
func (s *Service) SetListener(name string, serving bool) {
	s.selfMu.Lock()
	s.listeners[name] = serving
	s.selfMu.Unlock()
}

// SetJob records whether this node holds a leader-elected job.
func (s *Service) SetJob(job string, held bool) {
	s.selfMu.Lock()
	if held {
		s.jobs[job] = true
	} else {
		delete(s.jobs, job)
	}
	s.selfMu.Unlock()
}

// Identity is the deployment's identity, with the tenant set filtered to what
// visible admits (nil admits every tenant).
func (s *Service) Identity(ctx context.Context, visible func(store.Tenant) bool) (Identity, error) {
	d, err := s.o.Store.Deployment().Identity(ctx)
	if err != nil {
		return Identity{}, err
	}
	tenants, err := s.o.Store.Deployment().Tenants(ctx)
	if err != nil {
		return Identity{}, err
	}
	return identityOf(d, s.o.DisplayName, s.o.Versions, tenants, s.o.Now(), s.o.TenantActiveWindow, visible), nil
}

// --- the node report -------------------------------------------------------

// nodeReport is what a node writes about itself, stored as JSON.
type nodeReport struct {
	Jobs      []string        `json:"jobs"`
	Listeners map[string]bool `json:"listeners"`
	Plane     *PlaneView      `json:"event_plane,omitempty"`
}

// Report writes this node's report once.
func (s *Service) Report(ctx context.Context) error {
	node, err := s.o.Coordinator.Node(ctx)
	if err != nil {
		return err
	}
	s.selfMu.Lock()
	rep := nodeReport{
		Jobs:      slices.Sorted(maps.Keys(s.jobs)),
		Listeners: maps.Clone(s.listeners),
	}
	s.selfMu.Unlock()
	if s.o.Plane != nil {
		rep.Plane = planeView(s.o.Plane.PlaneHealth())
	}
	body, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	started := node.StartedAt
	if started.IsZero() {
		started = s.o.Now()
	}
	return s.o.Store.Deployment().ReportNode(ctx, store.DeploymentNode{
		NodeID: string(node.ID), Version: node.Version, StartedAt: started,
		ReportedAt: s.o.Now(), Report: body,
	})
}

// Run writes this node's report every ReportInterval until ctx ends, and then
// records that the node left on purpose.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(s.o.ReportInterval)
	defer tick.Stop()
	for {
		if err := s.Report(ctx); err != nil && ctx.Err() == nil {
			s.o.Logger.Warn("this node's health report could not be written; peers will read it as not reporting",
				"event", "instance_report_failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			s.leave()
			return
		case <-tick.C:
		}
	}
}

func (s *Service) leave() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	node, err := s.o.Coordinator.Node(ctx)
	if err != nil {
		return
	}
	if err := s.o.Store.Deployment().LeaveNode(ctx, string(node.ID), s.o.Now()); err != nil {
		s.o.Logger.Warn("this node could not record that it left; peers will read it as not reporting",
			"event", "instance_leave_failed", "error", err.Error())
	}
}

func planeView(p revoke.PlaneHealth) *PlaneView {
	v := &PlaneView{
		Tenants:             []TenantLagView{},
		WakeFailures:        p.WakeFailures,
		LastWakeFailure:     stamp(p.LastWakeFailure),
		PresenceFailures:    p.PresenceFailures,
		LastPresenceFailure: stamp(p.LastPresenceFailure),
	}
	for _, t := range p.Tenants {
		v.Tenants = append(v.Tenants, TenantLagView{Tenant: string(t.Tenant), Lag: t.Lag})
	}
	return v
}

// --- health ---------------------------------------------------------------

// Health answers for the given tenants' fleets, beneath the deployment and
// its nodes. visible filters every tenant name in the answer (the event
// plane's per-tenant lag); nil admits every one. It computes at most once per
// MinInterval however often it is asked, and serves what it last observed —
// judged against the clock now — when a computation cannot finish.
func (s *Service) Health(ctx context.Context, tenants []store.Tenant, visible func(store.Tenant) bool) Health {
	// The computation is shared by every caller inside the interval, so it
	// runs on a context of its own: one caller hanging up must not cache a
	// failure to observe for the others.
	s.computeMu.Lock()
	now := s.o.Now()
	if s.base == nil || now.Sub(s.baseAt) >= s.o.MinInterval {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), computeTimeout)
		b := s.computeBase(cctx, now)
		cancel()
		s.base, s.baseAt = &b, now
	}
	h := cloneHealth(*s.base)
	h.Fleet = make([]FleetHealth, 0, len(tenants))
	for _, t := range tenants {
		e, ok := s.fleets[t]
		if !ok || now.Sub(e.at) >= s.o.MinInterval {
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), computeTimeout)
			e = fleetEntry{at: now, h: s.computeFleet(cctx, t, now)}
			cancel()
			s.fleets[t] = e
		}
		f := e.h
		f.Reasons = slices.Clone(f.Reasons)
		h.Fleet = append(h.Fleet, f)
	}
	s.computeMu.Unlock()

	if visible != nil {
		for i := range h.Nodes.Members {
			if p := h.Nodes.Members[i].EventPlane; p != nil {
				p.Tenants = slices.DeleteFunc(p.Tenants, func(l TenantLagView) bool { return !visible(store.Tenant(l.Tenant)) })
			}
		}
	}
	return applyFreshness(h, s.o.Now())
}

// computeBase observes the deployment's own checks and its nodes.
func (s *Service) computeBase(ctx context.Context, now time.Time) Health {
	var h Health
	fresh := seconds(s.o.FreshFor)

	db := DatabaseCheck{State: StateHealthy, FreshForSeconds: fresh, Reasons: []Reason{}, ExpectedSchemaVersion: s.o.ExpectedSchema}
	version, err := s.o.Store.SchemaVersion(ctx)
	switch {
	case err != nil:
		db.State = StateDegraded
		db.Reasons = append(db.Reasons, Reason{Code: ReasonDatabaseUnreachable})
	case version < s.o.ExpectedSchema:
		db.State = StateDegraded
		db.Reasons = append(db.Reasons, Reason{Code: ReasonSchemaBehind})
	case version > s.o.ExpectedSchema:
		// A newer binary migrated ahead of this one: a rolling upgrade
		// mid-flight, not a fault (M19).
		db.State = StateInProgress
		db.Reasons = append(db.Reasons, Reason{Code: ReasonSchemaAhead})
	}
	db.SchemaVersion = version
	db.ObservedAt = stamp(now)
	h.Deployment.Database = db

	h.Nodes, h.Deployment.Listeners = s.computeNodes(ctx, now)
	return h
}

func (s *Service) computeNodes(ctx context.Context, now time.Time) (NodesHealth, Check) {
	nodeFresh := 3 * s.o.ReportInterval
	nodes := NodesHealth{
		FreshForSeconds: seconds(nodeFresh), Reasons: []Reason{},
		Members: []NodeHealth{}, Jobs: []JobHolder{}, Upgrade: Upgrade{Versions: []string{}},
	}
	listeners := Check{State: StateUnknown, FreshForSeconds: seconds(nodeFresh), Reasons: []Reason{}}

	rows, err := s.o.Store.Deployment().Nodes(ctx, now.Add(-s.o.NodeForgetAfter))
	if err != nil {
		nodes.State = StateUnknown
		nodes.Reasons = append(nodes.Reasons, Reason{Code: ReasonNodesUnreadable})
		listeners.Reasons = append(listeners.Reasons, Reason{Code: ReasonNodesUnreadable})
		return nodes, listeners
	}
	nodes.ObservedAt = stamp(now)

	members, merr := s.o.Coordinator.Members(ctx)
	isMember := map[string]bool{}
	for _, m := range members {
		isMember[string(m.ID)] = true
	}

	var (
		states       []State
		versions     = map[string]bool{}
		oldestReport time.Time
		anyFresh     bool
		listenerBad  []Reason
	)
	reported := map[string]bool{}
	for _, row := range rows {
		reported[row.NodeID] = true
		var rep nodeReport
		_ = json.Unmarshal(row.Report, &rep) // an unreadable report reads as an empty one
		n := NodeHealth{
			NodeID: row.NodeID, Version: row.Version, Member: isMember[row.NodeID],
			Reasons: []Reason{}, StartedAt: stamp(row.StartedAt), ReportedAt: stamp(row.ReportedAt),
			UptimeSeconds: int64(row.ReportedAt.Sub(row.StartedAt) / time.Second),
			Jobs:          nonNil(rep.Jobs), Listeners: rep.Listeners, EventPlane: rep.Plane,
		}
		if n.Listeners == nil {
			n.Listeners = map[string]bool{}
		}
		switch {
		case !row.LeftAt.IsZero() && !row.LeftAt.Before(row.ReportedAt):
			n.State = StateLeft
		case now.Sub(row.ReportedAt) > nodeFresh:
			n.State = StateStale
			n.Reasons = append(n.Reasons, Reason{Code: ReasonNodeNotReporting, Node: row.NodeID})
		default:
			anyFresh = true
			versions[row.Version] = true
			if oldestReport.IsZero() || row.ReportedAt.Before(oldestReport) {
				oldestReport = row.ReportedAt
			}
			for _, name := range slices.Sorted(maps.Keys(n.Listeners)) {
				if !n.Listeners[name] {
					r := Reason{Code: ReasonListenerNotServing, Node: row.NodeID}
					n.Reasons = append(n.Reasons, r)
					listenerBad = append(listenerBad, r)
				}
			}
			if p := rep.Plane; p != nil {
				for _, t := range p.Tenants {
					if t.Lag > s.o.LagThreshold {
						n.Reasons = append(n.Reasons, Reason{Code: ReasonEventPlaneLag, Node: row.NodeID, Tenant: t.Tenant, Count: t.Lag})
					}
				}
				if recent(p.LastWakeFailure, now, nodeFresh) || recent(p.LastPresenceFailure, now, nodeFresh) {
					n.Reasons = append(n.Reasons, Reason{Code: ReasonEventPlaneFailures, Node: row.NodeID,
						Count: int64(p.WakeFailures + p.PresenceFailures)})
				}
			}
			n.State = StateHealthy
			if len(n.Reasons) > 0 {
				n.State = StateDegraded
			}
			for _, job := range n.Jobs {
				nodes.Jobs = append(nodes.Jobs, JobHolder{Job: job, Node: row.NodeID})
			}
		}
		nodes.Members = append(nodes.Members, n)
	}
	for _, m := range members {
		if reported[string(m.ID)] {
			continue
		}
		nodes.Members = append(nodes.Members, NodeHealth{
			NodeID: string(m.ID), Version: m.Version, Member: true, State: StateUnknown,
			Reasons:   []Reason{{Code: ReasonNodeNotReported, Node: string(m.ID)}},
			StartedAt: stamp(m.StartedAt), Jobs: []string{}, Listeners: map[string]bool{},
		})
	}
	slices.SortFunc(nodes.Members, func(a, b NodeHealth) int { return compare(a.NodeID, b.NodeID) })
	slices.SortFunc(nodes.Jobs, func(a, b JobHolder) int { return compare(a.Job+"\x00"+a.Node, b.Job+"\x00"+b.Node) })

	for _, n := range nodes.Members {
		switch n.State {
		case StateStale:
			// A node that stopped reporting is a node down: the
			// deployment is degraded, and the reason names it.
			states = append(states, StateDegraded)
		case StateHealthy, StateDegraded, StateInProgress, StateUnknown, StateLeft:
			states = append(states, n.State)
		}
		nodes.Reasons = append(nodes.Reasons, n.Reasons...)
	}
	nodes.Upgrade.Versions = slices.Sorted(maps.Keys(versions))
	if len(versions) > 1 {
		nodes.Upgrade.InProgress = true
		nodes.Reasons = append(nodes.Reasons, Reason{Code: ReasonRollingUpgrade})
		states = append(states, StateInProgress)
	}
	if merr != nil {
		nodes.Reasons = append(nodes.Reasons, Reason{Code: ReasonMembershipUnavailable})
		states = append(states, StateUnknown)
	}
	if len(nodes.Members) == 0 {
		states = append(states, StateUnknown)
		nodes.Reasons = append(nodes.Reasons, Reason{Code: ReasonNodeNotReported})
	}
	nodes.State = rollup(states...)

	if anyFresh {
		listeners.ObservedAt = stamp(oldestReport)
		listeners.State = StateHealthy
		if len(listenerBad) > 0 {
			listeners.State = StateDegraded
			listeners.Reasons = listenerBad
		}
	} else {
		listeners.Reasons = append(listeners.Reasons, Reason{Code: ReasonNodeNotReported})
	}
	return nodes, listeners
}

func (s *Service) computeFleet(ctx context.Context, tenant store.Tenant, now time.Time) FleetHealth {
	f := FleetHealth{Tenant: string(tenant), FreshForSeconds: seconds(s.o.FreshFor), Reasons: []Reason{}}
	sum, err := s.o.Fleet.Summary(ctx, tenant)
	if err != nil {
		f.State = StateUnknown
		f.Reasons = append(f.Reasons, Reason{Code: ReasonFleetUncomputable, Tenant: string(tenant)})
		return f
	}
	f.ObservedAt = stamp(sum.ObservedAt)
	f.Summary = summaryView(sum)
	f.State, f.Reasons = fleetState(string(tenant), sum)
	if b, err := s.o.Store.PolicyBundles().GetActive(ctx, tenant); err == nil {
		f.PolicyVersion = b.Version
	}
	return f
}

func recent(t *time.Time, now time.Time, within time.Duration) bool {
	return t != nil && now.Sub(*t) <= within
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
