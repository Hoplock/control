// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
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

// recordingPublisher stands in for the event-stream delivery 0009 implements,
// and records what it was asked to deliver.
type recordingPublisher struct {
	calls []string
}

func (p *recordingPublisher) PublishConfigChange(_ context.Context, _ store.Tenant, proxyID string, version int64, _ string) error {
	p.calls = append(p.calls, proxyID)
	_ = version
	return nil
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
	}, time.Time{})
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
	}, time.Time{})
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
	}, time.Time{})
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
	}, c.now().Add(time.Hour)); err != nil {
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
	if _, err := r.IssueGrant(t.Context(), testTenant, fleet.EnrollmentGrant{ProxyID: "p"}, time.Time{}); err == nil {
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
	}, time.Time{})
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

	health, err := r.Health(ctx, testTenant)
	if err != nil {
		t.Fatalf("Health: %v", err)
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
	health, err := r.Health(t.Context(), testTenant)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	for _, h := range health {
		if h.ProxyID == proxyID {
			return h
		}
	}
	t.Fatalf("no health row for %s", proxyID)
	return fleet.ProxyHealth{}
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
