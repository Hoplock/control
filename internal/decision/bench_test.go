// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The latency budget (M5), measured rather than asserted.
//
// A proxy holds a user's SSH handshake open while this server answers, on every
// connection and per hop, and an estate whose machine-to-machine health
// checking runs through the proxy can put this endpoint into five figures of
// requests per second. So the measurement is taken under FAN-OUT — one subject
// against very many targets — and not only under a single hot subject, because
// those two shapes stress different things: the hot subject measures the
// engine, and the fan-out measures everything the engine does not cache.
//
// What is measured is the WHOLE call, including the synchronous decision-record
// write. That write is the deliberate trade this phase made (see record.go), so
// a measurement that excluded it would be measuring a server nobody runs.

// The fixture's size, stated because a latency number without one is a number
// about a machine rather than about a system.
const (
	benchRules   = 50
	benchTargets = 200
	benchProxies = 20
)

// LatencyTarget is the p99 this phase reports against.
//
// It is well inside [decision.DefaultBudget], which is the deadline at which a
// call is abandoned rather than the latency it is expected to take: a budget
// that doubled as a target would make "we answered in time" and "we nearly
// timed out" the same statement.
const LatencyTarget = 25 * time.Millisecond

// realisticBundle builds a bundle of n rules over the label vocabulary, with
// the matching rule LAST so that every evaluation walks the whole program. A
// benchmark whose first rule matches measures a one-rule bundle.
func realisticBundle(n int) string {
	var b strings.Builder
	b.WriteString("schema_version: 1\ntenant: acme\n")
	b.WriteString("labels:\n  env: [prod, dev]\n  kind: [host, appliance]\n")
	b.WriteString("groups: [sre, dba, oncall]\nrules:\n")
	for i := range n - 1 {
		fmt.Fprintf(&b, `  - id: filler-%d
    effect: deny
    reason: filler rule %d
    match:
      target:
        hostnames: [nothing-%d.example.com]
`, i, i, i)
	}
	b.WriteString(`  - id: sre-fleet
    effect: allow
    match:
      subject:
        groups: [sre]
      target:
        labels:
          env: [prod]
    route:
      intent: hops-permitted
      channels: [session, direct-tcpip]
      requests:
        types: [pty-req, shell, exec]
      forwards:
        direct_tcpip:
          - host: postgres.prod
            port: 5432
      filter:
        mode: whitelist
        exec_mode: restricted
        restricted_exec:
          commands:
            - executable: /usr/bin/systemctl
              form: positional
              args:
                - kind: oneof
                  values: [status, restart]
      credentials:
        - method: ephemeral-user
          username: {from: subject-local-part}
          key_type: ed25519
          lifetime_seconds: 900
      max_session_duration: 8h
      concurrency:
        max_sessions_per_subject: 4
`)
	return b.String()
}

// benchHarness seeds the fixture whose size the numbers are reported against.
func benchHarness(tb testing.TB) *harness {
	tb.Helper()
	h := newHarness(tb, func(h *harness) {
		// The refresh window is the production one here rather than the
		// tests' zero: re-compiling the bundle on every call is not what
		// the server does, and measuring it would be measuring a bug.
		h.refresh = decision.DefaultRefreshInterval
	})

	ctx := tb.Context()
	targets := make([]store.Target, 0, benchTargets)
	for i := range benchTargets {
		targets = append(targets, store.Target{
			ID:       fmt.Sprintf("bench-%d", i),
			Hostname: fmt.Sprintf("bench-%d.example.com", i),
			Zone:     "edge",
			Labels:   map[string]string{"env": "prod", "kind": "host"},
		})
	}
	storetest.Seed(tb, h.store, storetest.Fixture{Tenant: testTenant, Targets: targets})

	for i := range benchProxies {
		if err := h.store.Proxies().Upsert(ctx, testTenant, store.Proxy{
			ID:              fmt.Sprintf("bench-proxy-%d", i),
			Zone:            fmt.Sprintf("zone-%d", i),
			PublicKey:       []byte(fmt.Sprintf("key-%d", i)),
			State:           store.EnrollmentEnrolled,
			LastHeartbeatAt: h.clock,
		}); err != nil {
			tb.Fatalf("seed proxy: %v", err)
		}
	}

	h.activate(tb, realisticBundle(benchRules))
	return h
}

// BenchmarkAuthorize measures the whole call, in the two shapes that stress
// different halves of it.
func BenchmarkAuthorize(b *testing.B) {
	h := benchHarness(b)

	b.Run("hot-subject", func(b *testing.B) {
		req := request("bench-0.example.com", "proxy-1")
		b.ReportAllocs()
		for b.Loop() {
			if _, err := h.service.Authorize(b.Context(), testTenant, req); err != nil {
				b.Fatalf("authorize: %v", err)
			}
		}
	})

	b.Run("fan-out", func(b *testing.B) {
		// One subject against very many targets: the machine-identity
		// pattern, where a hint keyed per (subject, target) has a hit rate
		// near zero and every call is decided from scratch.
		reqs := make([]*contract.AuthorizeRequest, benchTargets)
		for i := range reqs {
			reqs[i] = request(fmt.Sprintf("bench-%d.example.com", i), "proxy-1")
		}
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			if _, err := h.service.Authorize(b.Context(), testTenant, reqs[i%len(reqs)]); err != nil {
				b.Fatalf("authorize: %v", err)
			}
			i++
		}
	})
}

// TestAuthorizeLatencyUnderFanOut reports p50/p99 and holds them to the target.
//
// It is a test rather than only a benchmark because the budget is a PROPERTY of
// this server, not a number somebody reads off a chart when they remember to
// run `go test -bench`. The sample is deliberately modest: this runs in CI on a
// shared runner, so the assertion is loose enough not to be a flake and tight
// enough to catch an unbounded read appearing on the decision path.
func TestAuthorizeLatencyUnderFanOut(t *testing.T) {
	t.Parallel()
	h := benchHarness(t)

	const samples = 300
	reqs := make([]*contract.AuthorizeRequest, benchTargets)
	for i := range reqs {
		reqs[i] = request(fmt.Sprintf("bench-%d.example.com", i), "proxy-1")
	}

	// One call outside the measurement: the first compiles the bundle and
	// loads the graph, which is work the server does once per refresh
	// interval rather than per request.
	if _, err := h.service.Authorize(t.Context(), testTenant, reqs[0]); err != nil {
		t.Fatalf("warm-up: %v", err)
	}

	took := make([]time.Duration, 0, samples)
	for i := range samples {
		start := time.Now()
		out, err := h.service.Authorize(t.Context(), testTenant, reqs[i%len(reqs)])
		took = append(took, time.Since(start))
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		if out.Response == nil {
			t.Fatalf("the fixture policy denied %s", reqs[i%len(reqs)].Target)
		}
	}
	sort.Slice(took, func(a, b int) bool { return took[a] < took[b] })

	p50 := took[len(took)*50/100]
	p99 := took[len(took)*99/100]
	t.Logf("authorize over %d calls, %d rules, %d targets, %d proxies: p50=%v p99=%v (target p99 %v, budget %v)",
		samples, benchRules, benchTargets, benchProxies, p50, p99, LatencyTarget, decision.DefaultBudget)

	if p99 > decision.DefaultBudget {
		t.Fatalf("p99 = %v, past the hard budget of %v: a call that cannot answer inside its own deadline "+
			"holds a user's handshake open until it is abandoned", p99, decision.DefaultBudget)
	}
	if p99 > LatencyTarget {
		t.Errorf("p99 = %v, past the %v this phase reports against; the numbers above are what to compare "+
			"against the learnings file before assuming the runner is at fault", p99, LatencyTarget)
	}
}
