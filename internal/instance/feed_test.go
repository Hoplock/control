// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package instance

import (
	"slices"
	"testing"
)

func kindsOf(ps []pending) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.kind)
	}
	return out
}

// The feed says what changed, coarsely, and nothing else.
func TestTheFeedLogsChangesOfState(t *testing.T) {
	base := snapshot{
		state: StateHealthy, nodes: map[string]string{"a": "v1", "b": "v1"}, stale: map[string]bool{},
		jobs: map[string]string{SupervisionJob: "a"}, fleet: map[string]State{"acme": StateHealthy},
		policy: map[string]int64{"acme": 3}, enrolled: map[string]int{"acme": 4}, tenants: 1,
	}
	if got := diff(base, base); len(got) != 0 {
		t.Fatalf("no change logged %v", kindsOf(got))
	}

	next := base
	next.nodes = map[string]string{"a": "v1", "b": "v2", "c": "v2"}
	next.stale = map[string]bool{"a": true}
	next.jobs = map[string]string{SupervisionJob: "b"}
	next.upgrade, next.versions = true, []string{"v1", "v2"}
	next.state, next.reasons = StateDegraded, []string{ReasonNodeNotReporting}
	next.fleet = map[string]State{"acme": StateDegraded}
	next.policy = map[string]int64{"acme": 4}
	next.enrolled = map[string]int{"acme": 5}
	got := kindsOf(diff(base, next))
	for _, want := range []string{
		EventNodeJoined, EventNodeLeft, EventJobMoved, EventUpgradeStarted, EventHealthChanged,
		EventFleetHealthChanged, EventPolicyPublished, EventCountsChanged,
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	for _, k := range got {
		if !slices.Contains(EventKinds, k) {
			t.Errorf("logged %q, which is not a feed kind", k)
		}
	}

	done := next
	done.upgrade = false
	if !slices.Contains(kindsOf(diff(next, done)), EventUpgradeFinished) {
		t.Errorf("an upgrade finishing was not logged")
	}
}

func TestEventIDsRoundTrip(t *testing.T) {
	for _, id := range []string{EventID("0123abcd", 42), lineID("0123abcd", 42, 7)} {
		epoch, seq, ok := ParseEventID(id)
		if !ok || epoch != "0123abcd" || seq != 42 {
			t.Errorf("%s parsed as %q %d %v", id, epoch, seq, ok)
		}
	}
	for _, bad := range []string{"", "evt-", "evt-x", "evt-x-y", "abc-1-2", "evt--1"} {
		if _, _, ok := ParseEventID(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}
