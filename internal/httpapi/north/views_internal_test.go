// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
)

// A bound configured below a second never reads as 0, which a client would
// read as no bound: each is whole seconds, rounded up (0045).
func TestLivenessIsWholeSecondsRoundedUp(t *testing.T) {
	got, err := json.Marshal(viewLiveness(fleet.Liveness{
		HeartbeatTTL: 500 * time.Millisecond, RelayRegistrationTTL: time.Nanosecond,
		TargetCapabilityTTL: 90*time.Second + time.Millisecond, ReportAfter: 2 * time.Second,
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"heartbeat_ttl_seconds":1,"relay_registration_ttl_seconds":1,"target_capability_ttl_seconds":91,"report_after_seconds":2}`
	if string(got) != want {
		t.Errorf("liveness = %s, want %s", got, want)
	}
	if got, err := json.Marshal(viewLiveness(fleet.DefaultLiveness())); err != nil ||
		string(got) != `{"heartbeat_ttl_seconds":90,"relay_registration_ttl_seconds":60,"target_capability_ttl_seconds":86400,"report_after_seconds":21600}` {
		t.Errorf("default liveness = %s (%v)", got, err)
	}
}

// A target with no labels is `{}`, never null, and a decision not found is
// that and nothing more.
func TestABlockedItemsLabelsAreAnObjectAndAMissingDecisionHasNoInstant(t *testing.T) {
	got, err := json.Marshal(viewBlocked([]store.BlockedCommand{{Command: "halt", Action: "block_command", TargetZone: "core"}}))
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(got, &items); err != nil || len(items) != 1 {
		t.Fatalf("items = %s (%v)", got, err)
	}
	if string(items[0]["target_labels"]) != "{}" {
		t.Errorf("target_labels = %s, want {}", items[0]["target_labels"])
	}
	for _, absent := range []string{"matched_rule", "route_type", "decided_at"} {
		if v, ok := items[0][absent]; ok {
			t.Errorf("%s = %s on a decision not found, want absent", absent, v)
		}
	}
}
