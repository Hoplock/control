// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package eval_test

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/policy/compile"
	"github.com/hoplock/control/internal/policy/eval"
	"github.com/hoplock/control/internal/policy/model"
)

// The evaluation budget (M5).
//
// A proxy holds a user's SSH handshake open while this server answers, on every
// connection, per hop. PLAN M5 sizes the first target deployment at ~5,800
// authorize calls per second sustained, so the decision path's own share of an
// authorize has to be small enough that the answer is dominated by I/O rather
// than by policy.
//
// Bundle size: 2,000 rules. That is the number this benchmark is written
// against and the reason is the estate rather than a round figure — ~350,000
// targets are reached through labels and zones rather than through a rule each,
// so rule count tracks the number of distinct *access patterns* an organisation
// writes down (a team times an environment times a posture), not the number of
// hosts. Two thousand is a generous reading of that: a hundred teams with
// twenty patterns apiece, with nobody having tidied up in three years.
//
// Measured on the machine this was written on (a 2.8GHz Xeon, 4 cores):
// ~27µs for the default-deny, where all 2,000 rules are examined and none
// matches, and ~34µs where the last rule matches and a snapshot is built. At
// 30µs a single core answers ~33,000 evaluations a second, so M5's ~5,800 per
// second sustained is not close to being policy-bound — which is the property
// worth keeping, not the microseconds.
//
// The ceiling below is an order of magnitude above the measurement, because a
// CI runner is not a benchmark machine and a timing test that flakes is a
// timing test that gets deleted. What it really asserts is the shape: that
// evaluation is linear in rule count with no unbounded construct, so a bundle
// twice the size costs twice as much and never more.
const (
	benchRules       = 2000
	evaluationBudget = 300 * time.Microsecond
)

// largeBundle builds a realistically shaped bundle: one rule per team per
// environment, each constraining several axes, with a catch-all deny nowhere in
// it so that the worst case really is the default-deny.
func largeBundle(rules int) string {
	var b strings.Builder
	b.WriteString("schema_version: 1\ntenant: acme\nlabels:\n  env: [prod, staging, dev]\n  owner: [")
	for i := range rules {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "team-%d", i)
	}
	b.WriteString("]\ngroups: [")
	for i := range rules {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "team-%d-oncall", i)
	}
	b.WriteString("]\nrules:\n")
	for i := range rules {
		fmt.Fprintf(&b, `  - id: rule-%d
    effect: allow
    match:
      subject: {groups: [team-%d-oncall], mfa: true}
      context: {days: [monday, tuesday, wednesday, thursday, friday], source_cidrs: ["10.%d.0.0/16"]}
      target: {hostnames: ["*.team%d.example.com"], labels: {env: [prod], owner: [team-%d]}}
    route:
      intent: direct
      channels: [session]
      requests: {types: [pty-req, shell, exec]}
      filter: {mode: whitelist}
      credentials:
        - method: ephemeral-user
          username: {from: subject-local-part}
      max_session_duration: 4h
    obligations: [{kind: record-session}]
    cache: {key: [subject, target, rule], ttl_seconds: 300}
`, i, i, i%256, i, i)
	}
	return b.String()
}

func benchProgram(tb testing.TB, rules int) *compile.Program {
	tb.Helper()
	bundle, err := model.Parse([]byte(largeBundle(rules)))
	if err != nil {
		tb.Fatalf("parse:\n%v", err)
	}
	prog, err := compile.Compile(bundle)
	if err != nil {
		tb.Fatalf("compile:\n%v", err)
	}
	return prog
}

// benchInput matches nothing, so every rule is examined: the worst case, and
// the one the budget is stated against.
func benchInput(rules int) model.Input {
	return model.Input{
		Now:     time.Date(2026, 9, 16, 10, 30, 0, 0, time.UTC),
		Subject: model.Subject{ID: "nobody@example.com", Groups: []string{"contractors"}, MFA: false},
		Context: model.Context{SourceAddr: netip.MustParseAddr("192.0.2.9"), ProxyID: "edge-1"},
		Target: model.Target{
			Hostname: fmt.Sprintf("host.team%d.example.com", rules+1), Port: 22,
			Labels: map[string]string{"env": "prod", "owner": "team-unknown"},
		},
	}
}

func BenchmarkEvaluateDefaultDeny(b *testing.B) {
	prog := benchProgram(b, benchRules)
	in := benchInput(benchRules)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if snap, _ := eval.Evaluate(prog, in); snap != nil {
			b.Fatal("the benchmark input must not match")
		}
	}
}

func BenchmarkEvaluateLastRuleMatches(b *testing.B) {
	prog := benchProgram(b, benchRules)
	last := benchRules - 1
	in := model.Input{
		Now: time.Date(2026, 9, 16, 10, 30, 0, 0, time.UTC),
		Subject: model.Subject{
			ID: "alice@example.com", Groups: []string{fmt.Sprintf("team-%d-oncall", last)}, MFA: true,
		},
		Context: model.Context{SourceAddr: netip.MustParseAddr(fmt.Sprintf("10.%d.1.1", last%256))},
		Target: model.Target{
			Hostname: fmt.Sprintf("host.team%d.example.com", last), Port: 22,
			Labels: map[string]string{"env": "prod", "owner": fmt.Sprintf("team-%d", last)},
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if snap, _ := eval.Evaluate(prog, in); snap == nil {
			b.Fatal("the benchmark input must match the last rule")
		}
	}
}

// TestEvaluationIsLinearAndWithinBudget asserts both halves of M5's promise:
// the worst case fits the stated budget, and doubling the rule count does not
// more than roughly double the cost. The linearity check is what actually
// catches a regression — an accidental quadratic passes a generous ceiling on a
// small bundle and falls over on a real one.
func TestEvaluationIsLinearAndWithinBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	measure := func(rules int) time.Duration {
		prog := benchProgram(t, rules)
		in := benchInput(rules)
		// Warm up, then take the best of several runs: the minimum is the
		// figure least polluted by a noisy neighbour on a CI runner.
		for range 50 {
			eval.Evaluate(prog, in)
		}
		best := time.Duration(1<<63 - 1)
		for range 9 {
			start := time.Now()
			const iterations = 50
			for range iterations {
				eval.Evaluate(prog, in)
			}
			if d := time.Since(start) / iterations; d < best {
				best = d
			}
		}
		return best
	}

	half, full := measure(benchRules/2), measure(benchRules)
	t.Logf("evaluation: %d rules in %v, %d rules in %v", benchRules/2, half, benchRules, full)

	if full > evaluationBudget {
		t.Errorf("worst-case evaluation took %v over %d rules, budget is %v (M5)",
			full, benchRules, evaluationBudget)
	}
	// Four times the cost for twice the rules is not linear by any reading,
	// and the slack absorbs a slow runner without hiding a real regression.
	if half > 0 && full > 4*half {
		t.Errorf("evaluation is not linear in rule count: %v for %d rules, %v for %d",
			half, benchRules/2, full, benchRules)
	}
}
