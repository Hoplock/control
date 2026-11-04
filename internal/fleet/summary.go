// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/hoplock/control/internal/store"
)

// The fleet summary (0046, PLAN M19): the numbers 0006 already computes per
// proxy, counted rather than listed, so a supervisor and a deployment's own
// health surface can ask "is the fleet healthy" without fetching every proxy.
// The drill-down is the fleet view (`GET …/proxies`), bounded by M26.

// SummaryPageSize is how many proxies one read of the summary pages through at
// a time; MaxSummaryProxies is where it stops counting and says so.
const (
	SummaryPageSize   = 500
	MaxSummaryProxies = 100_000
	// RecentErrorWindow is how recent a proxy's last error must be to count.
	RecentErrorWindow = time.Hour
)

// FleetSummary is one tenant's fleet, counted.
type FleetSummary struct {
	// Enrolled counts proxies in the enrolled state. Live counts those that
	// are a routing option now. Stale counts enrolled proxies that have
	// reported before and are not live; Unreachable those that have never
	// reported. The three of Live, Stale and Unreachable add up to Enrolled.
	Enrolled, Live, Stale, Unreachable int
	// Pending and Revoked count proxies in those enrollment states.
	Pending, Revoked int
	// ConfigDrifted counts enrolled proxies not caught up with their desired
	// configuration (proxy D18).
	ConfigDrifted int
	// ContractVersions is how many enrolled proxies declared each contract
	// vocabulary, by version.
	ContractVersions map[string]int
	// RelayRegistrations counts live relay registrations (proxy D11).
	RelayRegistrations int
	// RecentErrors counts proxies whose last error is within
	// RecentErrorWindow.
	RecentErrors int
	// Truncated is set when the fleet is larger than MaxSummaryProxies and
	// the counts cover only the first of them.
	Truncated bool
	// ObservedAt is when the summary was computed.
	ObservedAt time.Time
}

// Summary counts a tenant's fleet. It reads through HealthPage, so every
// count means what the fleet view says it means, and it reads only the
// tenant's rows (M18).
func (r *Registry) Summary(ctx context.Context, tenant store.Tenant) (FleetSummary, error) {
	now := r.now()
	out := FleetSummary{ContractVersions: map[string]int{}, ObservedAt: now.UTC()}
	page := store.Page{Limit: SummaryPageSize}
	seen := 0
	for {
		got, err := r.HealthPage(ctx, tenant, page)
		if err != nil {
			return FleetSummary{}, err
		}
		for _, p := range got.Rows {
			out.add(p, now)
		}
		seen += len(got.Rows)
		if !got.More || len(got.Rows) == 0 {
			return out, nil
		}
		if seen >= MaxSummaryProxies {
			out.Truncated = true
			return out, nil
		}
		page.After = []any{got.Rows[len(got.Rows)-1].ProxyID}
	}
}

func (s *FleetSummary) add(p ProxyHealth, now time.Time) {
	switch p.State {
	case store.EnrollmentEnrolled:
		s.Enrolled++
	case store.EnrollmentRevoked:
		s.Revoked++
		return
	case store.EnrollmentPending:
		s.Pending++
		return
	default:
		s.Pending++ // a state this build does not know is not enrolled
		return
	}
	switch {
	case p.Live:
		s.Live++
	case p.LastHeartbeatAt.IsZero() && p.SubscriptionSeenAt.IsZero():
		s.Unreachable++
	default:
		s.Stale++
	}
	if !p.Config.CaughtUp && p.Config.DesiredVersion != "" {
		s.ConfigDrifted++
	}
	s.ContractVersions[strconv.Itoa(p.ContractVersion)]++
	s.RelayRegistrations += len(p.RelayRegistrations)
	if !p.LastErrorAt.IsZero() && now.Sub(p.LastErrorAt) <= RecentErrorWindow {
		s.RecentErrors++
	}
}

// ContractSpread lists the contract versions the enrolled fleet declared, in
// order.
func (s FleetSummary) ContractSpread() []string {
	out := make([]string, 0, len(s.ContractVersions))
	for v := range s.ContractVersions {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}
