// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package contract_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
)

// The absent-value resolvers are graded from RAW JSON rather than from a
// struct literal, and that is the whole point of these tests: a decoded
// RevocationEvent cannot tell an omitted `heartbeat_interval_seconds` from one
// sent as `0`, and those are exactly the two readings that differ. A test
// written against a struct literal would assert the resolver's arithmetic and
// nothing about the discipline it exists for.

func TestAdvertisedHeartbeatIntervalReadsAbsenceFromTheWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want time.Duration
		ok   bool
	}{
		{
			name: "absent: the key is not on the wire at all",
			line: `{"event_id":"evt-1","type":"heartbeat","timestamp":"2026-01-01T00:00:00Z"}`,
		},
		{
			name: "explicit zero reads the same as absent",
			line: `{"event_id":"evt-1","type":"heartbeat","timestamp":"2026-01-01T00:00:00Z","heartbeat_interval_seconds":0}`,
		},
		{
			name: "negative is not an interval",
			line: `{"event_id":"evt-1","type":"heartbeat","timestamp":"2026-01-01T00:00:00Z","heartbeat_interval_seconds":-5}`,
		},
		{
			name: "present",
			line: `{"event_id":"evt-1","type":"heartbeat","timestamp":"2026-01-01T00:00:00Z","heartbeat_interval_seconds":5}`,
			want: 5 * time.Second,
			ok:   true,
		},
		{
			name: "present on an event that is not a heartbeat",
			line: `{"event_id":"evt-2","type":"resync","timestamp":"2026-01-01T00:00:00Z","heartbeat_interval_seconds":3}`,
			want: 3 * time.Second,
			ok:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ev contract.RevocationEvent
			if err := json.Unmarshal([]byte(tc.line), &ev); err != nil {
				t.Fatalf("decode: %v", err)
			}
			got, ok := ev.AdvertisedHeartbeatInterval()
			if ok != tc.ok || got != tc.want {
				t.Fatalf("AdvertisedHeartbeatInterval() = (%s, %t), want (%s, %t)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// A nil event is the reading a caller gets from a stream that ended, and it
// must answer "nothing advertised" rather than panic: the fallback to the
// reader's own timers is the safe answer and it is the one every server got
// before the field existed.
func TestAdvertisedHeartbeatIntervalOnNil(t *testing.T) {
	var ev *contract.RevocationEvent
	if got, ok := ev.AdvertisedHeartbeatInterval(); ok || got != 0 {
		t.Fatalf("AdvertisedHeartbeatInterval() = (%s, %t), want (0s, false)", got, ok)
	}
}

// The field is omitted when unset rather than serialised as `0`. A server that
// sent `"heartbeat_interval_seconds":0` would be advertising a value the
// contract's own minimum forbids, and a reader distinguishing absent from zero
// would be right to reject it.
func TestHeartbeatIntervalIsOmittedWhenUnset(t *testing.T) {
	b, err := json.Marshal(contract.RevocationEvent{
		EventID: "evt-1", Type: contract.EventTypeHeartbeat, Timestamp: "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := fields["heartbeat_interval_seconds"]; present {
		t.Fatalf("an unset interval was written to the wire: %s", b)
	}
}

// The ceiling is the second half of the heartbeat obligation, and it is a
// number in this package so that nothing has to remember it.
func TestHeartbeatCeilingIsTenSeconds(t *testing.T) {
	if contract.MaxHeartbeatIntervalSeconds != 10 {
		t.Fatalf("MaxHeartbeatIntervalSeconds = %d, want 10", contract.MaxHeartbeatIntervalSeconds)
	}
	if contract.MaxHeartbeatInterval != 10*time.Second {
		t.Fatalf("MaxHeartbeatInterval = %s, want 10s", contract.MaxHeartbeatInterval)
	}
}
