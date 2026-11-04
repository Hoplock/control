// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/httpapi/south"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/instance"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

const isolationTenant = store.Tenant("acme")

const isolationBundle = `
schema_version: 1
tenant: acme
groups: [sre]
rules:
  - id: sre-edge
    effect: allow
    match:
      subject:
        groups: [sre]
      target:
        hostnames: [edge-host.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: ephemeral-user
          username: {from: subject-local-part}
          key_type: ed25519
          lifetime_seconds: 900
`

// decisionPath is a real south-bound listener over a real store, with a proxy
// credential, as serve builds it: what a proxy drives.
type decisionPath struct {
	st         *store.Store
	dsn        string
	url        string
	client     *http.Client
	token      string
	bus        *revoke.Bus
	extensions *ext.Extensions
}

func newDecisionPath(t *testing.T) *decisionPath {
	t.Helper()
	dsn := storetest.DSNForSchema(storetest.DSN(t), storetest.NewSchema(t, storetest.DSN(t)))
	ctx := t.Context()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	now := time.Now().UTC()
	storetest.Seed(t, st, storetest.Fixture{
		Tenant:   isolationTenant,
		Subjects: []store.Subject{storetest.Subject("alice@example.com", "sre")},
		Targets:  []store.Target{{ID: "t-edge", Hostname: "edge-host.example.com", Zone: "edge"}},
		Proxies: []store.Proxy{{
			ID: "proxy-1", Zone: "edge", PublicKey: []byte("key-proxy-1"),
			State: store.EnrollmentEnrolled, LastHeartbeatAt: now,
		}},
	})
	version, err := st.PolicyBundles().NextVersion(ctx, isolationTenant)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PolicyBundles().Insert(ctx, isolationTenant, store.PolicyBundle{
		Version: version, Source: []byte(isolationBundle), Hash: "sha256:isolation", UploadedBy: "isolation_test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.PolicyBundles().Activate(ctx, isolationTenant, version); err != nil {
		t.Fatal(err)
	}

	bus, err := revoke.New(revoke.Options{Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = bus.Close(ctx)
	})
	registry := fleet.New(st, fleet.WithSubscriptionState(bus), fleet.WithLogger(log))
	ingest, err := audit.New(audit.Options{Store: st, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := decision.New(decision.Options{
		Store: st, Fleet: registry, Subjects: identity.NewStoreDirectory(st), Logger: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := south.New(south.Options{
		Identity: identity.NewService(identity.NewStoreDirectory(st)),
		Fleet:    registry, Decision: decisions, Events: bus, Logs: ingest, Logger: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	token, _, err := registry.IssueProxyToken(ctx, isolationTenant, "proxy-1", "isolation", time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	reg := ext.NewRegistry()
	if err := extdefault.Register(reg, extdefault.Deps{Node: ext.Node{ID: "local", Version: "test", StartedAt: now}}); err != nil {
		t.Fatal(err)
	}
	extensions, err := reg.Seal()
	if err != nil {
		t.Fatal(err)
	}
	return &decisionPath{st: st, dsn: dsn, url: srv.URL, client: srv.Client(), token: token.String(), bus: bus, extensions: extensions}
}

// authorize drives one decision, as a proxy does, and returns what a proxy
// acts on: the status, whether it was allowed, and the route.
func (d *decisionPath) authorize(t *testing.T) (string, time.Duration) {
	t.Helper()
	pv := contract.PolicyVersion
	body, _ := json.Marshal(contract.AuthorizeRequest{
		Identity:      contract.Identity{Subject: "alice@example.com", Login: "alice", Source: "local"},
		Target:        "edge-host.example.com",
		AuthMethod:    contract.AuthMethodCert,
		PolicyVersion: &pv,
		Conn: contract.ConnMeta{
			SessionID: "sess-iso", ProxyID: "proxy-1", ClientAddr: "203.0.113.7:52344",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
	})
	req, err := http.NewRequest(http.MethodPost, d.url+contract.PathAuthorize, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+d.token)
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := d.client.Do(req)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out contract.AuthorizeResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	// A deny is a 401 (M11); an allow is a 200 with a route.
	return fmt.Sprintf("%d route=%s target=%s channels=%v", resp.StatusCode,
		out.RouteType, out.Target, out.PermittedChannels), took
}

func (d *decisionPath) config(t *testing.T, supervisor string) *config.Config {
	t.Helper()
	doc := "listeners: {south: \"127.0.0.1:0\", north: \"127.0.0.1:1\"}\n" +
		"database: {dsn: \"" + d.dsn + "\"}\n" +
		"instance: {report_interval: 50ms, health_interval: 1ms, health_fresh_for: 1s}\n" + supervisor
	cfg, err := config.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

// A SUPERVISOR CANNOT AFFECT A SESSION. A real proxy credential drives
// authorize through the real listener while the supervisor is (a)
// unconfigured, (b) configured and unreachable, (c) configured and failing,
// and (d) configured and hanging past every timeout. All four are
// indistinguishable on the decision path, and none produces a deny.
//
// This test may never be weakened (prompt 0046).
func TestASupervisorCannotAffectASession(t *testing.T) {
	d := newDecisionPath(t)
	t.Setenv("HOPLOCK_TEST_SUPERVISOR_CREDENTIAL", "s3cret")

	var failing, hanging atomic.Int64
	failingSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failing.Add(1)
		http.Error(w, "supervisor on fire", http.StatusInternalServerError)
	}))
	t.Cleanup(failingSrv.Close)
	release := make(chan struct{})
	hangingSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hanging.Add(1)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); hangingSrv.Close() })
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unreachable := "http://" + closed.Addr().String() + "/register"
	_ = closed.Close()

	supervisor := func(endpoint string) string {
		return "supervisor: {endpoint: \"" + endpoint + "\", credential_env: HOPLOCK_TEST_SUPERVISOR_CREDENTIAL, " +
			"insecure_transport: true, heartbeat: 50ms, dial_timeout: 100ms, max_backoff: 100ms}\n"
	}
	cases := []struct {
		name, supervisor string
		reached          func() bool
	}{
		{"unconfigured", "", func() bool { return true }},
		{"unreachable", supervisor(unreachable), func() bool { return true }},
		{"failing", supervisor(failingSrv.URL), func() bool { return failing.Load() > 1 }},
		{"hanging", supervisor(hangingSrv.URL), func() bool { return hanging.Load() > 1 }},
	}

	var answers []string
	for _, c := range cases {
		cfg := d.config(t, c.supervisor)
		self, stop, err := buildInstance(t.Context(), cfg, d.bus, d.extensions, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatalf("%s: build instance: %v", c.name, err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for !c.reached() && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if !c.reached() {
			stop()
			t.Fatalf("%s: the supervisor was never dialled, so this case tested nothing", c.name)
		}
		seen := map[string]bool{}
		for range 20 {
			got, _ := d.authorize(t)
			seen[got] = true
		}
		stop()
		_ = self
		if len(seen) != 1 {
			t.Fatalf("%s: the decision path answered differently across calls: %v", c.name, seen)
		}
		for got := range seen {
			if !strings.HasPrefix(got, "200 route=") {
				t.Fatalf("%s: authorize answered %q; a supervisor turned into a deny or an outage", c.name, got)
			}
			answers = append(answers, got)
		}
	}
	if len(slices.Compact(answers)) != 1 {
		t.Fatalf("the four cases are distinguishable on the decision path: %v", answers)
	}
}

// Computing health takes nothing the authorize path needs: polled at its
// maximum rate, from several callers at once, the decision path's latency is
// unaffected — health reads through a pool of its own, at most once per its
// interval.
func TestHealthPolledAtItsMaximumRateLeavesTheDecisionPathAlone(t *testing.T) {
	d := newDecisionPath(t)
	cfg := d.config(t, "")
	self, stop, err := buildInstance(t.Context(), cfg, d.bus, d.extensions, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	p95 := func() time.Duration {
		var took []time.Duration
		for range 60 {
			_, d := d.authorize(t)
			took = append(took, d)
		}
		slices.Sort(took)
		return took[len(took)*95/100]
	}
	for range 10 { // warm the decision path's caches
		d.authorize(t)
	}
	baseline := p95()

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	var polls atomic.Int64
	for range 8 {
		wg.Go(func() {
			for ctx.Err() == nil {
				self.Health(ctx, []store.Tenant{isolationTenant}, nil)
				polls.Add(1)
			}
		})
	}
	loaded := p95()
	cancel()
	wg.Wait()

	if polls.Load() < 100 {
		t.Fatalf("health was polled %d times; the load this test claims was not applied", polls.Load())
	}
	// Generous on purpose: the claim is "unaffected", and a shared CI box
	// jitters. What this bound refuses is the decision path queueing behind
	// health for connections, which costs far more than this.
	if bound := 3*baseline + 25*time.Millisecond; loaded > bound {
		t.Fatalf("authorize p95 went from %s to %s under %d health polls (bound %s)", baseline, loaded, polls.Load(), bound)
	}
	h := self.Health(t.Context(), []store.Tenant{isolationTenant}, nil)
	if h.State == instance.StateDegraded {
		t.Fatalf("health under its own load reads degraded: %v", h.Reasons)
	}
}
