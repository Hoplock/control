// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke

import (
	"testing"
	"time"
)

// The failures the plane logs are counted where they are logged, and lag is
// what this node has been told minus what it fanned out.
func TestPlaneHealthCountsWhatThePlaneLogs(t *testing.T) {
	b, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	b.health.wake(at)
	b.health.wake(at.Add(time.Second))
	b.health.presence(at)
	b.mu.Lock()
	b.cursors["acme"] = &cursor{head: 10, seen: 25}
	b.cursors["globex"] = &cursor{head: 5, seen: 5}
	b.mu.Unlock()

	h := b.PlaneHealth()
	if h.WakeFailures != 2 || !h.LastWakeFailure.Equal(at.Add(time.Second)) || h.PresenceFailures != 1 {
		t.Fatalf("counters = %+v", h)
	}
	if len(h.Tenants) != 2 || h.Tenants[0].Tenant != "acme" || h.Tenants[0].Lag != 15 || h.Tenants[1].Lag != 0 {
		t.Fatalf("lag = %+v", h.Tenants)
	}
}
