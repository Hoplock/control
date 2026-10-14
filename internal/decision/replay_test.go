// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/policy/compile"
	"github.com/hoplock/control/internal/policy/model"
)

// candidateRules are inserted ahead of the fixture's own: one closes a target
// that was open and one opens a target that was closed, so a replay has a flip
// in each direction and a decision it must leave alone. Each is narrowed to the
// fixture's subject so that the rule it overtakes stays reachable, which the
// compiler insists on.
const candidateRules = `  - id: lock-cached
    effect: deny
    reason: cached is closed for maintenance
    match:
      subject:
        groups: [sre]
      target:
        hostnames: [cached.example.com]

  - id: open-forbidden
    effect: allow
    match:
      subject:
        ids: [alice@example.com]
      target:
        hostnames: [forbidden.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-key
          username: netadmin
          credential_ref: vault://forbidden

`

func compileSource(t *testing.T, source string) *compile.Program {
	t.Helper()
	b, err := model.Parse([]byte(source))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	prog, err := compile.Compile(b)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return prog
}

// recordSession authorizes a target under its own session id and returns the
// decision id, whatever the answer was.
func (h *harness) recordSession(t *testing.T, target, session string) string {
	t.Helper()
	req := request(target, "proxy-1")
	req.Conn.SessionID = session
	out, err := h.authorize(t, req)
	if err != nil {
		t.Fatalf("authorize %s: %v", target, err)
	}
	if out.Deny != nil {
		return out.Deny.DecisionID
	}
	return out.Response.DecisionID
}

// Replay reports THE SET of decisions a candidate flips, in each direction, and
// leaves an unchanged decision out of both. Asserting the count alone would
// pass a replay that flipped the wrong two.
func TestReplayReportsTheExactSetOfFlips(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	open := h.recordSession(t, "edge-host.example.com", "sess-open")
	cached := h.recordSession(t, "cached.example.com", "sess-cached")
	forbidden := h.recordSession(t, "forbidden.example.com", "sess-forbidden")

	candidate := compileSource(t, strings.Replace(testBundle, "rules:\n", "rules:\n"+candidateRules, 1))
	rep, err := decision.Replay(t.Context(), h.store, testTenant, candidate, decision.ReplayQuery{
		From: h.clock.Add(-time.Minute), To: h.clock.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rep.Replayed != 3 || rep.Unchanged != 1 || rep.Truncated || len(rep.Unreplayable) != 0 {
		t.Fatalf("replayed %d, unchanged %d, truncated %v, unreplayable %v; want 3, 1, false, none",
			rep.Replayed, rep.Unchanged, rep.Truncated, rep.Unreplayable)
	}

	flipped := func(groups []decision.FlipGroup) []string {
		var ids []string
		for _, g := range groups {
			if g.Count != len(g.Flips) {
				t.Errorf("group %s→%s counts %d and holds %d", g.From, g.To, g.Count, len(g.Flips))
			}
			for _, f := range g.Flips {
				ids = append(ids, f.DecisionID)
			}
		}
		slices.Sort(ids)
		return ids
	}
	if got := flipped(rep.AllowToDeny); !slices.Equal(got, []string{cached}) {
		t.Errorf("allow→deny = %v, want exactly [%s]", got, cached)
	}
	if got := flipped(rep.DenyToAllow); !slices.Equal(got, []string{forbidden}) {
		t.Errorf("deny→allow = %v, want exactly [%s]", got, forbidden)
	}
	for _, g := range append(slices.Clone(rep.AllowToDeny), rep.DenyToAllow...) {
		for _, f := range g.Flips {
			if f.DecisionID == open {
				t.Errorf("the unchanged decision %s was reported as a flip", open)
			}
		}
	}
	if g := rep.AllowToDeny[0]; g.WasRule != "cached" || g.NowRule != "lock-cached" {
		t.Errorf("allow→deny group rules = %q → %q, want cached → lock-cached", g.WasRule, g.NowRule)
	}
	if g := rep.DenyToAllow[0]; g.WasRule != "deny-forbidden" || g.NowRule != "open-forbidden" {
		t.Errorf("deny→allow group rules = %q → %q, want deny-forbidden → open-forbidden", g.WasRule, g.NowRule)
	}
}

// Replay asks the recorded question, at the recorded instant. The active policy
// replayed over its own records changes nothing — the property that makes a
// replay's flips mean "the candidate", never "time has passed".
func TestReplayingTheActivePolicyFlipsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.recordSession(t, "edge-host.example.com", "sess-a")
	h.recordSession(t, "forbidden.example.com", "sess-b")

	// A week later, nothing about the recorded questions has moved.
	rep, err := decision.Replay(t.Context(), h.store, testTenant, compileSource(t, testBundle), decision.ReplayQuery{
		From: h.clock.Add(-time.Hour), To: h.clock.Add(7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rep.Replayed != 2 || rep.Unchanged != 2 || len(rep.AllowToDeny)+len(rep.DenyToAllow) != 0 {
		t.Errorf("replaying the active policy: replayed %d, unchanged %d, flips %v/%v",
			rep.Replayed, rep.Unchanged, rep.AllowToDeny, rep.DenyToAllow)
	}
}

// The window is [from, to), and a limit that cuts it short says so.
func TestReplayBoundsItsWindowAndSaysWhenItWasCut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.recordSession(t, "edge-host.example.com", "sess-a")
	h.recordSession(t, "cached.example.com", "sess-b")

	prog := compileSource(t, testBundle)
	rep, err := decision.Replay(t.Context(), h.store, testTenant, prog, decision.ReplayQuery{
		From: h.clock.Add(-time.Minute), To: h.clock.Add(time.Minute), Limit: 1,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rep.Replayed != 1 || !rep.Truncated {
		t.Errorf("limit 1 over two records: replayed %d, truncated %v; want 1, true", rep.Replayed, rep.Truncated)
	}

	rep, err = decision.Replay(t.Context(), h.store, testTenant, prog, decision.ReplayQuery{
		From: h.clock.Add(time.Second), To: h.clock.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rep.Replayed != 0 {
		t.Errorf("a window after every record replayed %d", rep.Replayed)
	}
}

// A relabel is simulated like any other change: the candidate is the active
// policy, and the question is asked with the target's new labels.
func TestARelabelIsSimulatedByNamingTheNewLabels(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.recordSession(t, "edge-host.example.com", "sess-a")

	const prodOnly = `  - id: prod-is-closed
    effect: deny
    reason: production is change-frozen
    match:
      target:
        labels:
          env: [dev]

`
	prog := compileSource(t, strings.Replace(testBundle, "rules:\n", "rules:\n"+prodOnly, 1))
	rep, err := decision.Replay(t.Context(), h.store, testTenant, prog, decision.ReplayQuery{
		From: h.clock.Add(-time.Minute), To: h.clock.Add(time.Minute),
		Labels: decision.LabelOverride{"edge-host.example.com": {"env": "dev"}},
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(rep.AllowToDeny) != 1 || rep.AllowToDeny[0].Flips[0].DecisionID != id {
		t.Fatalf("relabelling edge-host to env=dev: allow→deny = %+v, want %s", rep.AllowToDeny, id)
	}
}

// A dry run is pure: synthetic questions, answered by the candidate alone.
func TestADryRunAnswersSyntheticQuestions(t *testing.T) {
	t.Parallel()
	prog := compileSource(t, testBundle)
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	got := decision.DryRun(prog, []decision.DryRunCase{
		{Name: "sre", Input: model.Input{Now: at, Subject: model.Subject{ID: alice, Groups: []string{"sre"}},
			Target: model.Target{Hostname: "edge-host.example.com"}}},
		{Name: "forbidden", Input: model.Input{Now: at, Subject: model.Subject{ID: alice, Groups: []string{"sre"}},
			Target: model.Target{Hostname: "forbidden.example.com"}}},
		{Name: "stranger", Input: model.Input{Now: at, Subject: model.Subject{ID: "eve@example.com"},
			Target: model.Target{Hostname: "edge-host.example.com"}}},
	})
	want := []struct{ effect, rule string }{{"allow", "sre-anywhere"}, {"deny", "deny-forbidden"}, {"deny", ""}}
	for i, w := range want {
		if got[i].Effect != w.effect || got[i].Rule != w.rule {
			t.Errorf("%s: %s by %q, want %s by %q", got[i].Name, got[i].Effect, got[i].Rule, w.effect, w.rule)
		}
	}
}
