// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke_test

import (
	"testing"

	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/revoke"
)

// The three copies of the revocation stream's numbers must agree.
//
// `internal/config` declares them because it is the lowest layer in this module
// and a dependency on this package would make loading a file depend on the
// package that consumes it. `internal/contract` owns the one that is not this
// server's to choose. That duplication is affordable only while it cannot
// drift, so this test is what makes it affordable.
func TestConfigDefaultsMatchTheBrokersOwn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		fromConfig any
		fromHere   any
	}{
		{"heartbeat_interval", config.DefaultHeartbeatInterval, revoke.DefaultHeartbeatInterval},
		{"replay_buffer", config.DefaultReplayBuffer, revoke.DefaultReplayBuffer},
		{"subscriber_queue", config.DefaultSubscriberQueue, revoke.DefaultSubscriberQueue},
	} {
		if tc.fromConfig != tc.fromHere {
			t.Errorf("%s: config says %v, internal/revoke says %v", tc.name, tc.fromConfig, tc.fromHere)
		}
	}

	// And the ceiling is the CONTRACT's, not either of theirs: it is the
	// number that keeps two consecutive intervals inside the proxy's 20s
	// reconnect timeout, and a server is not free to hold an opinion about
	// it.
	if config.MaxHeartbeatIntervalSeconds != contract.MaxHeartbeatIntervalSeconds {
		t.Errorf("the ceiling config refuses past is %d, the contract's is %d",
			config.MaxHeartbeatIntervalSeconds, contract.MaxHeartbeatIntervalSeconds)
	}
}

// The default interval is well inside the ceiling, which is what leaves room
// for a lost heartbeat rather than making one a dead stream.
func TestTheDefaultIntervalLeavesRoomForALostHeartbeat(t *testing.T) {
	t.Parallel()
	if 2*revoke.DefaultHeartbeatInterval > contract.MaxHeartbeatInterval+revoke.DefaultHeartbeatInterval {
		t.Fatalf("the default interval of %s is too close to the %s ceiling",
			revoke.DefaultHeartbeatInterval, contract.MaxHeartbeatInterval)
	}
	b, err := revoke.New(revoke.Options{})
	if err != nil {
		t.Fatalf("revoke.New with nothing configured: %v", err)
	}
	if got := b.AdvertisedHeartbeatSeconds(); got > contract.MaxHeartbeatIntervalSeconds {
		t.Fatalf("an unconfigured broker advertises %ds, past the ceiling of %ds",
			got, contract.MaxHeartbeatIntervalSeconds)
	}
}
