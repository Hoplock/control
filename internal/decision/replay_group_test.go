// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"testing"
)

// Flips are grouped by direction and by the rule pair, so a reader sees "these
// 40 moved from rule A to rule B" rather than 40 lines: one transition with two
// new rules is two groups, the largest first, and each direction is its own
// list.
func TestFlipsAreGroupedByDirectionAndByBothRules(t *testing.T) {
	t.Parallel()
	flip := func(id, from, to, was, now string) Flip {
		return Flip{DecisionID: id, From: from, To: to, WasRule: was, NowRule: now}
	}
	allowToDeny, denyToAllow := groupFlips([]Flip{
		flip("d1", "allow", "deny", "sre", "freeze"),
		flip("d2", "allow", "deny", "sre", ""),
		flip("d3", "allow", "deny", "sre", ""),
		flip("d4", "deny", "allow", "", "open"),
		flip("d5", "allow", "deny", "ops", ""),
	})
	type group struct {
		was, now string
		count    int
	}
	var got []group
	for _, g := range allowToDeny {
		if g.From != "allow" || g.To != "deny" || g.Count != len(g.Flips) {
			t.Errorf("group %+v is not an allow→deny group of its own count", g)
		}
		got = append(got, group{g.WasRule, g.NowRule, g.Count})
	}
	want := []group{{"sre", "", 2}, {"sre", "freeze", 1}, {"ops", "", 1}}
	if len(got) != len(want) {
		t.Fatalf("allow→deny groups = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("allow→deny group %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if len(denyToAllow) != 1 || denyToAllow[0].NowRule != "open" || denyToAllow[0].Flips[0].DecisionID != "d4" {
		t.Errorf("deny→allow = %+v, want d4 alone under open", denyToAllow)
	}
}
