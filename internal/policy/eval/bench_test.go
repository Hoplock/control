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
//
// # Why the measurement is testing.Benchmark and the budget knows about -race
//
// This test used to time a fixed number of iterations by hand and take the best
// of several runs. It failed intermittently, and phase 0007 measured why. Two
// separate faults, neither of them in the evaluator:
//
//  1. **The budget was being compared against race-instrumented code.**
//     `make test` is `go test -race ./...`, which is the only way CI ever runs
//     these, and the detector costs this path 6-9x. The 2,000-rule case
//     measures ~27µs without it and ~244µs with it — against a 300µs budget
//     that was set as "an order of magnitude above the measurement" from the
//     27µs figure. Under the command that actually runs, the order of magnitude
//     was about 19% of headroom, and the test was passing on luck.
//  2. **A hand-rolled timer is noisy at the scale being measured.** Best-of-N
//     over a fixed iteration count does not discard a warm-up properly and does
//     not adapt N to the speed of the machine, so on a two-core runner the
//     ratio between the two sizes wandered up to ~4x — tripping the linearity
//     check — when a calibrated measurement of the same code gives ~2.6x.
//
// So the figure now comes from [testing.Benchmark], which calibrates the
// iteration count and reports a per-op time, and the ceiling is
// [measurementBudget], which is [evaluationBudget] in a normal build and a
// scaled version of it under the detector. EVALUATIONBUDGET IS STILL THE M5
// NUMBER and does not move: what changes is that a run knows which of the two
// things it is measuring.
//
// What was NOT wrong is the evaluator. Measured across 250..8,000 rules it is
// flat at 13.6-13.9 ns/rule to 2,000 and allocates ONCE per call whatever the
// rule count, which is what the third assertion below now pins directly.
func TestEvaluationIsLinearAndWithinBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}

	measure := func(rules int) testing.BenchmarkResult {
		prog := benchProgram(t, rules)
		in := benchInput(rules)
		r := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				eval.Evaluate(prog, in)
			}
		})
		if r.N == 0 {
			t.Fatalf("the benchmark over %d rules ran no iterations", rules)
		}
		return r
	}

	half, full := measure(benchRules/2), measure(benchRules)
	halfNs, fullNs := time.Duration(half.NsPerOp()), time.Duration(full.NsPerOp())

	// Logged as ns/rule as well as ns/op, because the per-rule figure is the
	// one a reader can compare against the numbers in this file's header
	// without doing arithmetic during an incident.
	t.Logf("evaluation %s: %d rules in %v (%.1f ns/rule, %d allocs), %d rules in %v (%.1f ns/rule, %d allocs)",
		measurementMode,
		benchRules/2, halfNs, float64(half.NsPerOp())/float64(benchRules/2), half.AllocsPerOp(),
		benchRules, fullNs, float64(full.NsPerOp())/float64(benchRules), full.AllocsPerOp())

	if fullNs > measurementBudget {
		t.Errorf("worst-case evaluation took %v over %d rules, budget %s is %v (M5)",
			fullNs, benchRules, measurementMode, measurementBudget)
	}

	// Four times the cost for twice the rules is not linear by any reading,
	// and the slack absorbs a slow runner without hiding a real regression.
	// A calibrated measurement of today's evaluator sits at ~2x without the
	// detector and ~2.6x with it, so the threshold has room without being
	// vacuous.
	if halfNs > 0 && fullNs > 4*halfNs {
		t.Errorf("evaluation is not linear in rule count: %v for %d rules, %v for %d",
			halfNs, benchRules/2, fullNs, benchRules)
	}

	// THE ASSERTION WITH NO CLOCK IN IT, and the one most likely to catch a
	// real regression. Evaluation allocates once per call — the snapshot — and
	// that is independent of how many rules were examined. Something that
	// allocated per rule (a slice appended to in the match loop, an explanation
	// built for every rule rather than the one that matched) would show up here
	// exactly, on any machine, under any load, with no threshold to tune.
	if full.AllocsPerOp() > half.AllocsPerOp() {
		t.Errorf("evaluation allocated %d times over %d rules and %d times over %d: "+
			"allocations must not grow with rule count",
			full.AllocsPerOp(), benchRules, half.AllocsPerOp(), benchRules/2)
	}
}
