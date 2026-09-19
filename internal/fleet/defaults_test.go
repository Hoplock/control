// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"testing"

	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/fleet"
)

// The two copies of the fleet's defaults must agree.
//
// internal/config declares them because it is the lowest layer in this module and
// a dependency on internal/fleet would make loading a file depend on the package
// that consumes it. That duplication is affordable only while it cannot drift, so
// this test is the thing that makes it affordable.
func TestConfigDefaultsMatchTheFleetsOwn(t *testing.T) {
	t.Parallel()
	want := fleet.DefaultLiveness()

	cases := []struct {
		name         string
		fromConfig   any
		fromTheFleet any
	}{
		{"heartbeat_ttl", config.DefaultHeartbeatTTL, want.HeartbeatTTL},
		{"relay_registration_ttl", config.DefaultRelayRegistrationTTL, want.RelayRegistrationTTL},
		{"target_capability_ttl", config.DefaultTargetCapabilityTTL, want.TargetCapabilityTTL},
		{"capability_report_after", config.DefaultCapabilityReportAfter, want.ReportAfter},
		{"max_hops", config.DefaultMaxHops, fleet.DefaultMaxHops},
		{"max_cache_ttl", config.DefaultMaxCacheTTL, fleet.DefaultMaxCacheTTL},
	}
	for _, tc := range cases {
		if tc.fromConfig != tc.fromTheFleet {
			t.Errorf("%s: config says %v, internal/fleet says %v", tc.name, tc.fromConfig, tc.fromTheFleet)
		}
	}
}

// A partially filled Liveness takes the defaults for what it left out. A zero TTL
// would make everything stale at once and take the whole fleet out of routing, so
// the zero value must mean "unset" rather than "immediately stale".
func TestAPartialLivenessTakesTheDefaults(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t, fleet.WithLiveness(fleet.Liveness{HeartbeatTTL: 1}))
	got := r.Liveness()
	want := fleet.DefaultLiveness()

	if got.HeartbeatTTL != 1 {
		t.Errorf("the configured field was overwritten: %v", got.HeartbeatTTL)
	}
	if got.RelayRegistrationTTL != want.RelayRegistrationTTL {
		t.Errorf("relay TTL = %v, want the default %v", got.RelayRegistrationTTL, want.RelayRegistrationTTL)
	}
	if got.TargetCapabilityTTL != want.TargetCapabilityTTL {
		t.Errorf("capability TTL = %v, want the default %v", got.TargetCapabilityTTL, want.TargetCapabilityTTL)
	}
	if got.ReportAfter != want.ReportAfter {
		t.Errorf("report interval = %v, want the default %v", got.ReportAfter, want.ReportAfter)
	}
}
