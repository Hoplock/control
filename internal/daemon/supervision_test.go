// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
// (d) configured and hanging past every timeout, and (e) connected and no
// longer reading when the deployment's operator revokes the registration.
// All five are indistinguishable on the decision path, and none produces a
// deny. In (e) the registration is also held to its bound: the supervisor
// loses the connection within one heartbeat of the check that saw the
// revocation, is not recorded as told, and nothing dials afterwards
// (prompt 0047).
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

	// (e): a supervisor that answers and keeps its response alive, so the
	// liveness watchdog never ends the registration, and never reads a byte
	// of the request body. What the deployment writes lands in the socket's
	// buffer and is never read; only the bound can end the connection.
	const notReadingHeartbeat = 250 * time.Millisecond
	var (
		notReadingDials atomic.Int64
		notReadingLost  = make(chan time.Time, 1)
		revokedAt       time.Time
	)
	notReadingSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notReadingDials.Add(1)
		rc := http.NewResponseController(w)
		_ = rc.EnableFullDuplex()
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		_ = enc.Encode(instance.Line{Kind: instance.InboundResume})
		_ = rc.Flush()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
			case <-tick.C:
				if enc.Encode(instance.Line{Kind: instance.InboundHeartbeat}) == nil && rc.Flush() == nil {
					continue
				}
			}
			select {
			case notReadingLost <- time.Now():
			default:
			}
			return
		}
	}))
	t.Cleanup(notReadingSrv.Close)
	notReadingLog := &syncBuffer{}

	supervisorAt := func(endpoint, heartbeat string) string {
		return "supervisor: {endpoint: \"" + endpoint + "\", credential_env: HOPLOCK_TEST_SUPERVISOR_CREDENTIAL, " +
			"insecure_transport: true, heartbeat: " + heartbeat + ", dial_timeout: 100ms, max_backoff: 100ms}\n"
	}
	supervisor := func(endpoint string) string { return supervisorAt(endpoint, "50ms") }
	cases := []struct {
		name, supervisor string
		reached          func() bool
		log              io.Writer
		// act runs once the supervisor is reached; the decision path is then
		// driven until settled says what act started has finished, and check
		// runs before the instance stops.
		act     func(t *testing.T)
		settled func() bool
		check   func(t *testing.T)
	}{
		{name: "unconfigured", reached: func() bool { return true }},
		{name: "unreachable", supervisor: supervisor(unreachable), reached: func() bool { return true }},
		{name: "failing", supervisor: supervisor(failingSrv.URL), reached: func() bool { return failing.Load() > 1 }},
		{name: "hanging", supervisor: supervisor(hangingSrv.URL), reached: func() bool { return hanging.Load() > 1 }},
		{
			name:       "revoked while not reading",
			supervisor: supervisorAt(notReadingSrv.URL, notReadingHeartbeat.String()),
			reached:    func() bool { return notReadingDials.Load() > 0 },
			log:        notReadingLog,
			act: func(t *testing.T) {
				revokedAt = time.Now()
				if _, err := d.st.Deployment().SetSupervisionRevoked(t.Context(), true, "ops", revokedAt.UTC()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_, _ = d.st.Deployment().SetSupervisionRevoked(context.Background(), false, "", time.Time{})
				})
			},
			settled: func() bool { return len(notReadingLost) > 0 },
			check: func(t *testing.T) {
				var lost time.Time
				select {
				case lost = <-notReadingLost:
				default:
					t.Fatalf("a supervisor that stopped reading kept a revoked registration open")
				}
				// The check runs within one heartbeat of the revocation, and
				// the bound is one heartbeat after the check.
				if took := lost.Sub(revokedAt); took > 3*notReadingHeartbeat {
					t.Fatalf("the supervisor lost the connection %s after the revocation; the bound is %s after the check", took, notReadingHeartbeat)
				}
				time.Sleep(3 * notReadingHeartbeat)
				if n := notReadingDials.Load(); n != 1 {
					t.Fatalf("a revoked registration dialled %d times, want the one", n)
				}
				var told []bool
				for _, line := range strings.Split(notReadingLog.String(), "\n") {
					var ev struct {
						Event string `json:"event"`
						Told  *bool  `json:"told_supervisor"`
					}
					if json.Unmarshal([]byte(line), &ev) == nil && ev.Event == "supervision_revoked" && ev.Told != nil {
						told = append(told, *ev.Told)
					}
				}
				if !slices.Equal(told, []bool{false}) {
					t.Fatalf("supervision_revoked recorded told_supervisor %v, want one false: the supervisor never read the line", told)
				}
			},
		},
	}

	var answers []string
	for _, c := range cases {
		cfg := d.config(t, c.supervisor)
		logger := slog.New(slog.DiscardHandler)
		if c.log != nil {
			logger = slog.New(slog.NewJSONHandler(c.log, nil))
		}
		self, stopInstance, err := buildInstance(t.Context(), cfg, d.bus, d.extensions, logger)
		if err != nil {
			t.Fatalf("%s: build instance: %v", c.name, err)
		}
		// A failed check still stops the instance, and before the test
		// servers close: a server waits on a registration still open to it.
		stop := sync.OnceFunc(stopInstance)
		t.Cleanup(stop)
		deadline := time.Now().Add(5 * time.Second)
		for !c.reached() && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if !c.reached() {
			stop()
			t.Fatalf("%s: the supervisor was never dialled, so this case tested nothing", c.name)
		}
		if c.act != nil {
			c.act(t)
		}
		seen := map[string]bool{}
		settleBy := time.Now().Add(5 * time.Second)
		for i := 0; i < 20 || (c.settled != nil && !c.settled() && time.Now().Before(settleBy)); i++ {
			got, _ := d.authorize(t)
			seen[got] = true
		}
		if c.check != nil {
			c.check(t)
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
		t.Fatalf("the five cases are distinguishable on the decision path: %v", answers)
	}
}

// syncBuffer is a log destination a test reads while the daemon writes it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
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
	// The load is on before the measurement starts and still on when it
	// ends: what is asserted is concurrency, not a count a slow box misses.
	for polls.Load() < 16 {
		time.Sleep(time.Millisecond)
	}
	before := polls.Load()
	loaded := p95()
	during := polls.Load() - before
	cancel()
	wg.Wait()

	if during == 0 {
		t.Fatalf("no health poll ran while authorize was measured; the load this test claims was not applied")
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
