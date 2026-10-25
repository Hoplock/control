// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// clock is a settable clock, so staleness is tested by moving time rather than
// by sleeping through it.
type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func (c *clock) advance(d time.Duration) { c.at = c.at.Add(d) }

// subs is a stand-in for the subscription state 0009 implements.
type subs map[string]time.Time

func (s subs) LiveSubscriptions(context.Context, store.Tenant) (map[string]time.Time, error) {
	return s, nil
}

func (s subs) LiveSubscription(_ context.Context, _ store.Tenant, proxyID string) (time.Time, bool, error) {
	seen, ok := s[proxyID]
	return seen, ok, nil
}

// recordingPublisher stands in for the event stream, and records what it was
// asked to announce. fail makes that many announcements fail first.
type recordingPublisher struct {
	mu     sync.Mutex
	calls  []string
	events []contract.ConfigChangedEvent
	fail   int
}

func (p *recordingPublisher) PublishConfigChange(_ context.Context, _ store.Tenant, proxyID string, ev contract.ConfigChangedEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail > 0 {
		p.fail--
		return errors.New("the stream is down")
	}
	p.calls = append(p.calls, proxyID)
	p.events = append(p.events, ev)
	return nil
}

func (p *recordingPublisher) announced() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func newRegistry(t *testing.T, opts ...fleet.Option) (*store.Store, *fleet.Registry, *clock) {
	t.Helper()
	st := storetest.New(t)
	c := &clock{at: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	opts = append([]fleet.Option{fleet.WithClock(c.now)}, opts...)
	return st, fleet.New(st, opts...), c
}

// enrolled is the common setup: a grant, an enrollment, and a first heartbeat so
// the proxy is a routing option.
func enrolled(t *testing.T, r *fleet.Registry, tenant store.Tenant, proxyID string, zone fleet.Zone, edges []fleet.Edge, caps fleet.Capabilities) {
	t.Helper()
	ctx := t.Context()

	token, err := r.IssueGrant(ctx, tenant, fleet.EnrollmentGrant{
		ProxyID:      proxyID,
		GrantedZones: []fleet.Zone{zone},
		CreatedBy:    "operator",
	}, time.Time{}, nil)
	if err != nil {
		t.Fatalf("IssueGrant %s: %v", proxyID, err)
	}
	if _, err := r.Enroll(ctx, fleet.EnrollmentRequest{
		Token:           token.String(),
		ProxyID:         proxyID,
		Zone:            zone,
		PublicKey:       []byte("key-" + proxyID),
		ContractVersion: 4,
		Capabilities:    caps,
		Edges:           edges,
	}); err != nil {
		t.Fatalf("Enroll %s: %v", proxyID, err)
	}
	if err := r.Heartbeat(ctx, tenant, fleet.HeartbeatReport{ProxyID: proxyID}); err != nil {
		t.Fatalf("Heartbeat %s: %v", proxyID, err)
	}
}

// THE ENROLLMENT REFUSAL THIS PHASE EXISTS FOR. A proxy that could claim any zone
// could insert itself as a hop into other people's routes.
func TestEnrollmentRefusesAZoneItWasNotGranted(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	token, err := r.IssueGrant(ctx, testTenant, fleet.EnrollmentGrant{
		ProxyID:      "p-edge",
		GrantedZones: []fleet.Zone{"edge"},
	}, time.Time{}, nil)
	if err != nil {
		t.Fatalf("IssueGrant: %v", err)
	}

	_, err = r.Enroll(ctx, fleet.EnrollmentRequest{
		Token:     token.String(),
		ProxyID:   "p-edge",
		Zone:      "enclave", // not granted
		PublicKey: []byte("key-p-edge"),
	})
	if err == nil {
		t.Fatal("a proxy enrolled into a zone it was not granted")
	}
	if !errorsIs(err, fleet.ErrZoneNotGranted) {
		t.Fatalf("err = %v, want ErrZoneNotGranted", err)
	}
	if !fleet.EnrollmentRefused(err) {
		t.Error("the refusal is not reported as a decision")
	}

	// And the refusal did not spend the token: the whole enrollment is one
	// transaction, so a rejected zone leaves the operator's grant usable.
	if _, err := r.Enroll(ctx, fleet.EnrollmentRequest{
		Token: token.String(), ProxyID: "p-edge", Zone: "edge", PublicKey: []byte("key-p-edge"),
	}); err != nil {
		t.Errorf("the granted zone was refused after a rejected attempt: %v", err)
	}
}

// The token is one-time. A second enrollment with it is refused rather than
// re-issued, because a token that keeps working is a permanent credential nobody
// meant to create.
func TestEnrollmentTokenIsOneTime(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	token, err := r.IssueGrant(ctx, testTenant, fleet.EnrollmentGrant{
		ProxyID: "p-edge", GrantedZones: []fleet.Zone{"edge"},
	}, time.Time{}, nil)
	if err != nil {
		t.Fatalf("IssueGrant: %v", err)
	}
	req := fleet.EnrollmentRequest{
		Token: token.String(), ProxyID: "p-edge", Zone: "edge", PublicKey: []byte("key-p-edge"),
	}

	if _, err := r.Enroll(ctx, req); err != nil {
		t.Fatalf("first Enroll: %v", err)
	}
	if _, err := r.Enroll(ctx, req); !errorsIs(err, fleet.ErrEnrollmentSpent) {
		t.Errorf("second Enroll: %v, want ErrEnrollmentSpent", err)
	}
}

func TestEnrollmentRefusesAWrongOrExpiredCredential(t *testing.T) {
	t.Parallel()
	_, r, c := newRegistry(t)
	ctx := t.Context()

	if _, err := r.IssueGrant(ctx, testTenant, fleet.EnrollmentGrant{
		ProxyID: "p-edge", GrantedZones: []fleet.Zone{"edge"},
	}, c.now().Add(time.Hour), nil); err != nil {
		t.Fatalf("IssueGrant: %v", err)
	}

	t.Run("no grant at all", func(t *testing.T) {
		wrong, err := fleet.MintEnrollmentToken(testTenant)
		if err != nil {
			t.Fatalf("MintEnrollmentToken: %v", err)
		}
		_, err = r.Enroll(ctx, fleet.EnrollmentRequest{
			Token: wrong.String(), ProxyID: "p-nobody", Zone: "edge", PublicKey: []byte("k"),
		})
		if !errorsIs(err, fleet.ErrEnrollmentUnknown) {
			t.Errorf("err = %v, want ErrEnrollmentUnknown", err)
		}
	})

	t.Run("wrong secret", func(t *testing.T) {
		wrong, err := fleet.MintEnrollmentToken(testTenant)
		if err != nil {
			t.Fatalf("MintEnrollmentToken: %v", err)
		}
		_, err = r.Enroll(ctx, fleet.EnrollmentRequest{
			Token: wrong.String(), ProxyID: "p-edge", Zone: "edge", PublicKey: []byte("k"),
		})
		if !errorsIs(err, fleet.ErrEnrollmentRejected) {
			t.Errorf("err = %v, want ErrEnrollmentRejected", err)
		}
	})

	t.Run("malformed token", func(t *testing.T) {
		for _, bad := range []string{"", "no-separator", ".secret-only", "tenant-only."} {
			if _, err := r.Enroll(ctx, fleet.EnrollmentRequest{
				Token: bad, ProxyID: "p-edge", Zone: "edge", PublicKey: []byte("k"),
			}); !errorsIs(err, fleet.ErrEnrollmentRejected) {
				t.Errorf("%q: err = %v, want ErrEnrollmentRejected", bad, err)
			}
		}
	})
}

// A grant is refused at issuance when it grants no zone: a grant that can never
// succeed is an operator's typo, and the moment to say so is while they are
// looking.
func TestAGrantMustGrantAZone(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	if _, err := r.IssueGrant(t.Context(), testTenant, fleet.EnrollmentGrant{ProxyID: "p"}, time.Time{}, nil); err == nil {
		t.Fatal("a grant with no zones was issued")
	}
}

// A proxy is not a routing option the instant it enrolls: enrollment says an
// operator approved it, not that it is running.
func TestEnrollmentDoesNotMakeAProxyLive(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	token, err := r.IssueGrant(ctx, testTenant, fleet.EnrollmentGrant{
		ProxyID: "p-edge", GrantedZones: []fleet.Zone{"edge"},
	}, time.Time{}, nil)
	if err != nil {
		t.Fatalf("IssueGrant: %v", err)
	}
	if _, err := r.Enroll(ctx, fleet.EnrollmentRequest{
		Token: token.String(), ProxyID: "p-edge", Zone: "edge", PublicKey: []byte("key-p-edge"),
	}); err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	g, err := r.Graph(ctx, testTenant)
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	n, ok := g.Node("p-edge")
	if !ok {
		t.Fatal("the enrolled proxy is not in the graph")
	}
	if n.Live {
		t.Error("a proxy that has never reported is a routing option")
	}
}

// A heartbeat from a proxy with no enrolled row is refused and does NOT create
// one: a heartbeat that enrolled its sender would be the auto-enrollment this
// phase exists to refuse, arriving through the one call nobody thinks of as an
// enrollment path.
func TestHeartbeatNeverEnrolls(t *testing.T) {
	t.Parallel()
	st, r, _ := newRegistry(t)
	ctx := t.Context()

	if err := r.Heartbeat(ctx, testTenant, fleet.HeartbeatReport{ProxyID: "p-stranger"}); !errorsIs(err, fleet.ErrNotEnrolled) {
		t.Fatalf("err = %v, want ErrNotEnrolled", err)
	}
	if _, err := st.Proxies().Get(ctx, testTenant, "p-stranger"); !store.IsNotFound(err) {
		t.Errorf("the heartbeat created a proxy row: %v", err)
	}
}

// A stale proxy drops out of routing, and returns when it heartbeats again —
// against a real clock and the configured TTL.
func TestStalenessAgainstTheClock(t *testing.T) {
	t.Parallel()
	_, r, c := newRegistry(t, fleet.WithLiveness(fleet.Liveness{HeartbeatTTL: time.Minute}))
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-edge", "edge",
		[]fleet.Edge{{ToZone: "region", Connection: contract.HopConnectionDial, Address: "gw:22", Cost: 1}},
		fleet.Capabilities{})
	enrolled(t, r, testTenant, "p-region", "region", nil, fleet.Capabilities{})

	dest := fleet.Destination{Zone: "region"}
	if _, err := r.Path(ctx, testTenant, fleet.EntryPoint{ProxyID: "p-edge"}, dest); err != nil {
		t.Fatalf("freshly heartbeated: %v", err)
	}

	// Past the TTL, nothing is live.
	c.advance(2 * time.Minute)
	if _, err := r.Path(ctx, testTenant, fleet.EntryPoint{ProxyID: "p-edge"}, dest); !fleet.IsNoPath(err) {
		t.Fatalf("after the TTL: %v, want a no-path outcome", err)
	}

	// Heartbeating again brings them back.
	for _, id := range []string{"p-edge", "p-region"} {
		if err := r.Heartbeat(ctx, testTenant, fleet.HeartbeatReport{ProxyID: id}); err != nil {
			t.Fatalf("Heartbeat %s: %v", id, err)
		}
	}
	if _, err := r.Path(ctx, testTenant, fleet.EntryPoint{ProxyID: "p-edge"}, dest); err != nil {
		t.Fatalf("after heartbeating again: %v", err)
	}
}

// The subscription is the stronger liveness signal and may keep a proxy live
// whose explicit heartbeat has aged out: this server is holding that connection
// open, which is a fact it observed rather than one it was told.
func TestASubscriptionKeepsAProxyLive(t *testing.T) {
	t.Parallel()
	seen := subs{}
	_, r, c := newRegistry(t,
		fleet.WithLiveness(fleet.Liveness{HeartbeatTTL: time.Minute}),
		fleet.WithSubscriptionState(seen),
	)
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-edge", "edge",
		[]fleet.Edge{{ToZone: "region", Connection: contract.HopConnectionDial, Address: "gw:22", Cost: 1}},
		fleet.Capabilities{})
	enrolled(t, r, testTenant, "p-region", "region", nil, fleet.Capabilities{})

	c.advance(2 * time.Minute)
	seen["p-edge"] = c.now()
	seen["p-region"] = c.now()

	if _, err := r.Path(ctx, testTenant, fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "region"}); err != nil {
		t.Fatalf("a proxy with a live subscription was not routable: %v", err)
	}
}

// A relay edge is viable only while the registration is, and the upstream is the
// only party that can report it.
func TestRelayRegistrationLivenessThroughTheRegistry(t *testing.T) {
	t.Parallel()
	_, r, c := newRegistry(t, fleet.WithLiveness(fleet.Liveness{
		HeartbeatTTL:         10 * time.Minute,
		RelayRegistrationTTL: time.Minute,
	}))
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-edge", "edge",
		[]fleet.Edge{{ToZone: "region", Connection: contract.HopConnectionDial, Address: "gw:22", Cost: 1}},
		fleet.Capabilities{})
	enrolled(t, r, testTenant, "p-region", "region",
		[]fleet.Edge{{ToZone: "enclave", Connection: contract.HopConnectionRelay, Cost: 1}},
		fleet.Capabilities{})
	enrolled(t, r, testTenant, "p-enclave", "enclave", nil, fleet.Capabilities{})

	dest := fleet.Destination{Zone: "enclave", Hostname: "db-1.enclave"}
	entry := fleet.EntryPoint{ProxyID: "p-edge"}

	// Nobody has reported a registration yet, so the relay edge is not viable
	// and the only route is refused as an outage.
	if _, err := r.Path(ctx, testTenant, entry, dest); !fleet.IsNoPath(err) {
		t.Fatalf("a relay edge with no registration was routed: %v", err)
	}

	// The upstream reports the registration.
	if err := r.Heartbeat(ctx, testTenant, fleet.HeartbeatReport{
		ProxyID:            "p-region",
		RelayRegistrations: []string{"p-enclave"},
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	hops, err := r.Path(ctx, testTenant, entry, dest)
	if err != nil {
		t.Fatalf("with a registration: %v", err)
	}
	if got, want := hopIDs(hops), []string{"p-region", "p-enclave"}; !slices.Equal(got, want) {
		t.Fatalf("path = %v, want %v", got, want)
	}
	if hops[1].Connection != contract.HopConnectionRelay {
		t.Errorf("second hop = %q, want relay", hops[1].Connection)
	}

	// The registration ages out, and the route goes with it. It is NOT
	// downgraded to a dial.
	c.advance(2 * time.Minute)
	if _, err := r.Path(ctx, testTenant, entry, dest); !fleet.IsNoPath(err) {
		t.Fatalf("a stale registration was still routed: %v", err)
	}

	// And a heartbeat that stops naming it removes it outright.
	c.advance(-2 * time.Minute)
	if err := r.Heartbeat(ctx, testTenant, fleet.HeartbeatReport{ProxyID: "p-region"}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if _, err := r.Path(ctx, testTenant, entry, dest); !fleet.IsNoPath(err) {
		t.Fatalf("a dropped registration was still routed: %v", err)
	}
}

// A revoked proxy is not live however loudly it reports: enrollment state is an
// operator's decision and liveness may not overrule it.
func TestARevokedProxyIsNotRoutable(t *testing.T) {
	t.Parallel()
	st, r, _ := newRegistry(t)
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-edge", "edge",
		[]fleet.Edge{{ToZone: "region", Connection: contract.HopConnectionDial, Address: "gw:22", Cost: 1}},
		fleet.Capabilities{})
	enrolled(t, r, testTenant, "p-region", "region", nil, fleet.Capabilities{})

	p, err := st.Proxies().Get(ctx, testTenant, "p-region")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	p.State = store.EnrollmentRevoked
	if err := st.Proxies().Upsert(ctx, testTenant, p); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if _, err := r.Path(ctx, testTenant, fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "region"}); !fleet.IsNoPath(err) {
		t.Errorf("a revoked proxy was routed through: %v", err)
	}
}

// The fleet view is what an operator looks at first during an incident.
func TestHealthReportsWhatAnOperatorNeeds(t *testing.T) {
	t.Parallel()
	_, r, c := newRegistry(t, fleet.WithSubscriptionState(subs{"p-region": time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}))
	ctx := t.Context()

	caps := fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthEphemeralUser},
		Execution:         []contract.ExecutionRung{contract.ExecutionAccountConfined},
	}
	enrolled(t, r, testTenant, "p-region", "region", nil, caps)
	if err := r.Heartbeat(ctx, testTenant, fleet.HeartbeatReport{
		ProxyID:            "p-region",
		SessionCount:       7,
		LastError:          "target leg refused",
		RelayRegistrations: []string{"p-enclave"},
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	health, err := healthAll(ctx, r, testTenant)
	if err != nil {
		t.Fatalf("HealthPage: %v", err)
	}
	if len(health) != 1 {
		t.Fatalf("got %d row(s), want 1", len(health))
	}
	h := health[0]
	if !h.Live {
		t.Error("a freshly heartbeated proxy is not live")
	}
	if h.SessionCount != 7 {
		t.Errorf("session count = %d, want 7", h.SessionCount)
	}
	if h.LastError != "target leg refused" {
		t.Errorf("last error = %q", h.LastError)
	}
	if h.LastErrorAt.IsZero() {
		t.Error("the last error carries no time")
	}
	if h.ContractVersion != 4 {
		t.Errorf("contract version = %d, want 4", h.ContractVersion)
	}
	if !h.Capabilities.HasExecutionRung(contract.ExecutionAccountConfined) {
		t.Error("the declared capabilities are not in the fleet view")
	}
	if got, want := h.RelayRegistrations, []string{"p-enclave"}; !slices.Equal(got, want) {
		t.Errorf("relay registrations = %v, want %v", got, want)
	}
	if h.SubscriptionSeenAt.IsZero() {
		t.Error("the subscription signal is not in the fleet view")
	}
	if !h.LastHeartbeatAt.Equal(c.now()) {
		t.Errorf("last heartbeat = %v, want %v", h.LastHeartbeatAt, c.now())
	}
}

// The fleet view is read a page at a time (M26), and every read behind a page
// is for that page's proxies alone. Two rows the page must not read are
// poisoned — a relay registration and a configuration state that cannot be
// scanned — and held by proxies past the page: the page reads cleanly, and
// each page that holds one fails whole (M26.6), which is what proves the
// poison would have been seen.
//
// The page holds a proxy with a live relay, one with no state row, and one
// whose heartbeat has aged out but whose event stream another node holds: it
// is live through that node's subscription, read from this node's copy (M25).
func TestAPageOfTheFleetViewReadsItsOwnProxiesAlone(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	c := &clock{at: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	seenAt := c.at.Add(2 * time.Minute)

	// Two nodes over one store. This node's registry reads node A's copy;
	// node B holds p-3's stream.
	bus := func(node string) *revoke.Bus {
		b, err := revoke.New(revoke.Options{
			Store: st, Node: node, HeartbeatInterval: time.Second, SyncInterval: 20 * time.Millisecond,
			Now: func() time.Time { return seenAt }, Logger: slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatalf("revoke.New %s: %v", node, err)
		}
		b.Start(ctx)
		t.Cleanup(func() {
			closing, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = b.Close(closing)
		})
		return b
	}
	nodeA, nodeB := bus("node-a"), bus("node-b")
	r := fleet.New(st, fleet.WithClock(c.now), fleet.WithSubscriptionState(nodeA))

	for _, id := range []string{"p-1", "p-3", "p-4", "p-5"} {
		enrolled(t, r, testTenant, id, "region", nil, fleet.Capabilities{})
	}
	// p-2 has a row and no state: it never enrolled through this server.
	if err := st.Proxies().Upsert(ctx, testTenant, storetest.Proxy("p-2", "region")); err != nil {
		t.Fatalf("Upsert p-2: %v", err)
	}
	if err := st.RelayRegistrations().ReplaceForUpstream(ctx, testTenant, "p-4", []string{"p-4-down"}, c.at); err != nil {
		t.Fatalf("seed p-4's registration: %v", err)
	}
	for _, poison := range []string{
		`UPDATE relay_registrations SET last_seen_at = 'infinity' WHERE tenant = $1 AND upstream_proxy_id = 'p-4'`,
		`UPDATE proxy_config_state SET restart_required = '{NULL}' WHERE tenant = $1 AND proxy_id = 'p-5'`,
	} {
		if tag, err := st.Pool().Exec(ctx, poison, testTenant); err != nil || tag.RowsAffected() != 1 {
			t.Fatalf("poison %q: %v (%d rows)", poison, err, tag.RowsAffected())
		}
	}

	// Two minutes on: p-1 and p-2 heartbeat, p-3 does not.
	c.advance(2 * time.Minute)
	for _, h := range []fleet.HeartbeatReport{
		{ProxyID: "p-1", RelayRegistrations: []string{"p-1-down"}},
		{ProxyID: "p-2"},
	} {
		if err := r.Heartbeat(ctx, testTenant, h); err != nil {
			t.Fatalf("Heartbeat %s: %v", h.ProxyID, err)
		}
	}
	streamCtx, endStream := context.WithCancel(context.Background())
	defer endStream()
	go func() {
		_ = nodeB.Subscribe(streamCtx, testTenant, "p-3", "", func(*contract.RevocationEvent) error { return nil })
	}()
	eventually(t, "node A to see node B's stream for p-3", func() bool {
		_, ok, err := nodeA.LiveSubscription(ctx, testTenant, "p-3")
		return err == nil && ok
	})

	page, err := r.HealthPage(ctx, testTenant, store.Page{Limit: 3})
	if err != nil {
		t.Fatalf("HealthPage: %v: it read a row past its page", err)
	}
	var ids []string
	for _, h := range page.Rows {
		ids = append(ids, h.ProxyID)
	}
	if want := []string{"p-1", "p-2", "p-3"}; !slices.Equal(ids, want) || !page.More {
		t.Fatalf("the first page = %v (more %t), want %v and more", ids, page.More, want)
	}
	p1, p2, p3 := page.Rows[0], page.Rows[1], page.Rows[2]
	if !p1.Live || !slices.Equal(p1.RelayRegistrations, []string{"p-1-down"}) || p1.Config.ProxyID != "p-1" {
		t.Errorf("p-1 = live %t, relays %v, config %+v; want live with its one relay", p1.Live, p1.RelayRegistrations, p1.Config)
	}
	if !p2.Live || !reflect.DeepEqual(p2.Config, fleet.ConfigRollout{ProxyID: "p-2"}) || p2.RelayRegistrations != nil {
		t.Errorf("p-2 = live %t, config %+v, relays %v; want live, an empty rollout row and no relay", p2.Live, p2.Config, p2.RelayRegistrations)
	}
	if !p3.Live || !p3.SubscriptionSeenAt.Equal(seenAt) {
		t.Errorf("p-3 = live %t, subscription seen %v; want live through node B's stream, seen %v", p3.Live, p3.SubscriptionSeenAt, seenAt)
	}
	if !c.now().After(p3.LastHeartbeatAt.Add(fleet.DefaultLiveness().HeartbeatTTL)) {
		t.Fatalf("p-3's heartbeat (%v) has not aged out, so the copy proved nothing", p3.LastHeartbeatAt)
	}

	// Each poisoned row fails the one page that holds it, and nothing of
	// the page is answered beside the error.
	for _, after := range []string{"p-3", "p-4"} {
		got, err := r.HealthPage(ctx, testTenant, store.Page{Limit: 1, After: []any{after}})
		if err == nil || got.Rows != nil || got.More {
			t.Errorf("the page after %s = %+v, %v; want its poisoned read to fail it whole", after, got, err)
		}
	}

	// The copy is read on every page: once node B's stream ends, p-3 is
	// not live.
	endStream()
	eventually(t, "node A to stop seeing p-3's stream", func() bool {
		_, ok, err := nodeA.LiveSubscription(ctx, testTenant, "p-3")
		return err == nil && !ok
	})
	page, err = r.HealthPage(ctx, testTenant, store.Page{Limit: 1, After: []any{"p-2"}})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].Live || !page.Rows[0].SubscriptionSeenAt.IsZero() {
		t.Errorf("p-3 after its stream ended = %+v, %v; want not live and no subscription", page.Rows, err)
	}
}

// eventually waits for cond, for at most five seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// An empty LastError leaves the recorded error's timestamp alone: a healthy
// heartbeat must not claim that yesterday's error just happened.
func TestAHealthyHeartbeatDoesNotRestampAnOldError(t *testing.T) {
	t.Parallel()
	_, r, c := newRegistry(t)
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-edge", "edge", nil, fleet.Capabilities{})
	if err := r.Heartbeat(ctx, testTenant, fleet.HeartbeatReport{ProxyID: "p-edge", LastError: "boom"}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	errAt := healthOf(t, r, "p-edge").LastErrorAt

	c.advance(time.Minute)
	if err := r.Heartbeat(ctx, testTenant, fleet.HeartbeatReport{ProxyID: "p-edge"}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if got := healthOf(t, r, "p-edge").LastErrorAt; !got.Equal(errAt) {
		t.Errorf("the error time moved to %v, want it left at %v", got, errAt)
	}
}

func healthOf(t *testing.T, r *fleet.Registry, proxyID string) fleet.ProxyHealth {
	t.Helper()
	health, err := healthAll(t.Context(), r, testTenant)
	if err != nil {
		t.Fatalf("HealthPage: %v", err)
	}
	for _, h := range health {
		if h.ProxyID == proxyID {
			return h
		}
	}
	t.Fatalf("no health row for %s", proxyID)
	return fleet.ProxyHealth{}
}

// healthAll is the fleet view whole, read as one page: every tenant these
// tests build holds fewer proxies than a page.
func healthAll(ctx context.Context, r *fleet.Registry, tenant store.Tenant) ([]fleet.ProxyHealth, error) {
	page, err := r.HealthPage(ctx, tenant, store.Page{Limit: store.MaxPageLimit})
	if err == nil && page.More {
		err = errors.New("the fleet view is longer than one page")
	}
	return page.Rows, err
}

// rolloutsOf is the rollout view whole, or its drifted view, read as one page.
func rolloutsOf(ctx context.Context, r *fleet.Registry, tenant store.Tenant, drifted bool) ([]fleet.ConfigRollout, error) {
	page, err := r.ConfigRolloutPage(ctx, tenant, drifted, store.Page{Limit: store.MaxPageLimit})
	if err == nil && page.More {
		err = errors.New("the rollout view is longer than one page")
	}
	return page.Rows, err
}

// A heartbeat that says nothing about capabilities has not withdrawn them; one
// that declares an empty set has.
func TestHeartbeatCapabilitiesAreReplacedOnlyWhenNamed(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	caps := fleet.Capabilities{Execution: []contract.ExecutionRung{contract.ExecutionAccountConfined}}
	enrolled(t, r, testTenant, "p-edge", "edge", nil, caps)

	if err := r.Heartbeat(ctx, testTenant, fleet.HeartbeatReport{ProxyID: "p-edge"}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if !healthOf(t, r, "p-edge").Capabilities.HasExecutionRung(contract.ExecutionAccountConfined) {
		t.Error("a silent heartbeat withdrew the declared capabilities")
	}

	if err := r.Heartbeat(ctx, testTenant, fleet.HeartbeatReport{
		ProxyID: "p-edge", Capabilities: &fleet.Capabilities{},
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if !healthOf(t, r, "p-edge").Capabilities.Empty() {
		t.Error("an explicitly empty declaration did not withdraw the capabilities")
	}
}

// errorsIs is errors.Is, named so the assertions above read as sentences.
func errorsIs(err, target error) bool { return errors.Is(err, target) }
