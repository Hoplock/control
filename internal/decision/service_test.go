// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The composition root, against a real database and a real fleet graph.
//
// What is asserted here is the part no unit test can reach: that the inputs are
// gathered from this server's own records, that the trail decides where the
// route starts, that every no-route outcome is an OUTAGE, and that a record is
// written on every path including the one that refuses.

const (
	testTenant = store.Tenant("acme")
	alice      = "alice@example.com"
)

// The fleet the tests route over:
//
//	proxy-1 (zone edge) --dial--> zone deep (proxy-2)
//	proxy-2 (zone deep)
//
// and four targets: one in each zone, one in a zone no proxy serves, and one
// the policy denies.
const testBundle = `
schema_version: 1
tenant: acme
description: the decision layer's fixture policy
labels:
  env: [prod, dev]
groups: [sre]
rules:
  - id: deny-forbidden
    effect: deny
    reason: this target is not reachable by anybody
    match:
      target:
        hostnames: [forbidden.example.com]

  - id: direct-only
    effect: allow
    match:
      target:
        hostnames: [pinned.example.com]
    route:
      intent: direct
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-key
          username: netadmin
          credential_ref: vault://pinned

  - id: cached
    effect: allow
    match:
      target:
        hostnames: [cached.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-key
          username: netadmin
          credential_ref: vault://cached
    cache:
      key: [subject, target]
      ttl_seconds: 120

  - id: no-shell
    effect: allow
    match:
      target:
        hostnames: [rung-host.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      requests:
        types: [exec]
      filter:
        mode: blacklist
      credentials:
        - method: ephemeral-user
          username: svc
          key_type: ed25519
      enforcement:
        execution: no-interactive-shell

  - id: account-restricted
    effect: allow
    match:
      target:
        hostnames: [restricted-host.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: whitelist
        exec_mode: restricted
        restricted_exec:
          commands:
            - executable: /usr/bin/uptime
              form: exact
              argv: ["-p"]
      credentials:
        - method: ephemeral-user
          username: svc
          key_type: ed25519
      enforcement:
        execution: account-restricted

  - id: attested
    effect: allow
    match:
      target:
        hostnames: [attested-host.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-key
          username: netadmin
          credential_ref: vault://attested
      enforcement:
        execution: platform-attested
        attestation:
          asserted_by: network-engineering
          reference: CR-4711

  - id: fortigate
    effect: allow
    match:
      target:
        hostnames: [device-host.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: ephemeral-account
          username: hoplock-svc
          platform: fortigate
          credential_kind: password
          expiry_posture: target-enforced
          lifetime_seconds: 600
          device_fields:
            vdom: root

  - id: undeclared-platform
    effect: allow
    match:
      target:
        hostnames: [ios-host.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: ephemeral-account
          username: hoplock-svc
          platform: ios
          credential_kind: password
          expiry_posture: target-enforced
          lifetime_seconds: 600

  - id: sre-anywhere
    effect: allow
    match:
      subject:
        groups: [sre]
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
      max_session_duration: 4h
`

type harness struct {
	store    *store.Store
	fleet    *fleet.Registry
	service  *decision.Service
	clock    time.Time
	subs     *fakeSubscriptions
	proxyCap map[string]fleet.Capabilities
	refresh  time.Duration
	// relayRegistered is whether proxy-3 currently holds an outbound relay
	// registration with proxy-1. A relay edge without one is not viable and
	// is NEVER downgraded to a dial.
	relayRegistered bool
}

// fakeSubscriptions stands in for the revocation stream's liveness signal,
// which is 0009's to implement. It is the ONLY thing between this build and an
// issued cache hint (M9), so a test that wants one wires it deliberately.
type fakeSubscriptions struct {
	seen map[string]time.Time
	err  error
}

func (f *fakeSubscriptions) LiveSubscriptions(context.Context, store.Tenant) (map[string]time.Time, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.seen, nil
}

func newHarness(t testing.TB, opts ...func(*harness)) *harness {
	t.Helper()
	st := storetest.New(t)
	h := &harness{
		store:    st,
		clock:    time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC),
		subs:     &fakeSubscriptions{},
		proxyCap: map[string]fleet.Capabilities{},
		// No refresh window by default: every call re-reads the active
		// bundle and the graph, so a test that changes either sees the
		// change rather than the cache.
		refresh:         time.Nanosecond,
		relayRegistered: true,
	}
	for _, opt := range opts {
		opt(h)
	}

	registryOpts := []fleet.Option{
		fleet.WithClock(h.now),
		fleet.WithLogger(slog.New(slog.DiscardHandler)),
	}
	if h.subs != nil {
		registryOpts = append(registryOpts, fleet.WithSubscriptionState(h.subs))
	}
	h.fleet = fleet.New(st, registryOpts...)
	h.seed(t)

	svc, err := decision.New(decision.Options{
		Store:    st,
		Fleet:    h.fleet,
		Subjects: identity.NewStoreDirectory(st),
		Logger:   slog.New(slog.DiscardHandler),
		Now:      h.now,
		Refresh:  h.refresh,
	})
	if err != nil {
		t.Fatalf("decision.New: %v", err)
	}
	h.service = svc
	return h
}

func (h *harness) now() time.Time { return h.clock }

func (h *harness) seed(t testing.TB) {
	t.Helper()
	ctx := t.Context()

	storetest.Seed(t, h.store, storetest.Fixture{
		Tenant:   testTenant,
		Subjects: []store.Subject{storetest.Subject(alice, "sre")},
		Targets: []store.Target{
			{ID: "t-edge", Hostname: "edge-host.example.com", Zone: "edge", Labels: map[string]string{"env": "prod"}},
			{ID: "t-deep", Hostname: "deep-host.example.com", Zone: "deep", Labels: map[string]string{"env": "prod"}},
			{ID: "t-lost", Hostname: "lost-host.example.com", Zone: "enclave"},
			{ID: "t-forbidden", Hostname: "forbidden.example.com", Zone: "edge"},
			{ID: "t-pinned", Hostname: "pinned.example.com", Zone: "deep"},
			{ID: "t-cached", Hostname: "cached.example.com", Zone: "edge"},
			{ID: "t-rung", Hostname: "rung-host.example.com", Zone: "edge"},
			{ID: "t-restricted", Hostname: "restricted-host.example.com", Zone: "edge"},
			{ID: "t-attested", Hostname: "attested-host.example.com", Zone: "edge"},
			{ID: "t-device", Hostname: "device-host.example.com", Zone: "edge"},
			{ID: "t-ios", Hostname: "ios-host.example.com", Zone: "edge"},
			{ID: "t-vault", Hostname: "vault-host.example.com", Zone: "vault"},
		},
	})

	for _, p := range []struct {
		id, zone string
	}{{"proxy-1", "edge"}, {"proxy-2", "deep"}, {"proxy-3", "vault"}} {
		proxy := store.Proxy{
			ID:              p.id,
			Zone:            p.zone,
			PublicKey:       []byte("key-" + p.id),
			State:           store.EnrollmentEnrolled,
			LastHeartbeatAt: h.clock,
		}
		if caps, ok := h.proxyCap[p.id]; ok {
			raw, err := fleet.MarshalCapabilities(caps)
			if err != nil {
				t.Fatalf("marshal capabilities: %v", err)
			}
			proxy.DeclaredCapabilities = raw
		}
		if err := h.store.Proxies().Upsert(ctx, testTenant, proxy); err != nil {
			t.Fatalf("seed proxy %s: %v", p.id, err)
		}
	}

	if err := h.store.ProxyEdges().ReplaceForProxy(ctx, testTenant, "proxy-1", []store.ProxyEdge{
		{
			ProxyID:     "proxy-1",
			ToZone:      "deep",
			Direction:   store.HopDial,
			Address:     "proxy-2.example.com:2222",
			NextProxyID: "proxy-2",
			Cost:        1,
		},
		{
			// The other connection direction (proxy D11): proxy-3 has
			// registered an outbound connection with proxy-1, which opens
			// a channel over it rather than dialling into the zone.
			ProxyID:     "proxy-1",
			ToZone:      "vault",
			Direction:   store.HopRelay,
			NextProxyID: "proxy-3",
			Cost:        1,
		},
	}); err != nil {
		t.Fatalf("seed edge: %v", err)
	}
	if h.relayRegistered {
		if err := h.store.RelayRegistrations().ReplaceForUpstream(
			ctx, testTenant, "proxy-1", []string{"proxy-3"}, h.clock); err != nil {
			t.Fatalf("seed relay registration: %v", err)
		}
	}

	h.activate(t, testBundle)
}

func (h *harness) activate(t testing.TB, source string) {
	t.Helper()
	ctx := t.Context()
	version, err := h.store.PolicyBundles().NextVersion(ctx, testTenant)
	if err != nil {
		t.Fatalf("next bundle version: %v", err)
	}
	if err := h.store.PolicyBundles().Insert(ctx, testTenant, store.PolicyBundle{
		Version:    version,
		Source:     []byte(source),
		Hash:       "sha256:fixture",
		UploadedBy: "decision_test",
	}); err != nil {
		t.Fatalf("insert bundle: %v", err)
	}
	if err := h.store.PolicyBundles().Activate(ctx, testTenant, version); err != nil {
		t.Fatalf("activate bundle: %v", err)
	}
}

// request builds an authorize request the way a proxy would.
func request(target, proxyID string, trail ...string) *contract.AuthorizeRequest {
	version := contract.PolicyVersion
	return &contract.AuthorizeRequest{
		Identity: contract.Identity{
			Subject: alice,
			Login:   "alice",
			Source:  "local",
		},
		Target:        target,
		AuthMethod:    contract.AuthMethodCert,
		PolicyVersion: &version,
		Capabilities: &contract.ProxyCapabilities{
			Execution: []string{string(contract.ExecutionNoInteractiveShell)},
		},
		Conn: contract.ConnMeta{
			SessionID:  "sess-1",
			ProxyID:    proxyID,
			ClientAddr: "203.0.113.7:52344",
			HopTrail:   trail,
			Timestamp:  "2026-09-19T12:00:00Z",
		},
	}
}

func (h *harness) authorize(t testing.TB, req *contract.AuthorizeRequest) (decision.Outcome, error) {
	t.Helper()
	return h.service.Authorize(t.Context(), testTenant, req)
}

func (h *harness) mustAllow(t testing.TB, req *contract.AuthorizeRequest) *contract.AuthorizeResponse {
	t.Helper()
	out, err := h.authorize(t, req)
	if err != nil {
		t.Fatalf("authorize %s from %s: %v", req.Target, req.Conn.ProxyID, err)
	}
	if out.Deny != nil {
		t.Fatalf("authorize %s from %s was denied (%s); it should have been allowed",
			req.Target, req.Conn.ProxyID, out.Deny.Reason)
	}
	return out.Response
}

// ---------------------------------------------------------------------------
// the hop trail
// ---------------------------------------------------------------------------

// ONE login, ONE target, asked twice. An empty trail from the edge proxy
// answers `nexthop`; a trail naming that edge proxy, asked by the proxy behind
// it, answers `direct` — same user, same target.
//
// A test that only exercised the empty-trail call could not tell a server that
// reads the trail from one that discards it.
func TestTheSameQuestionAnsweredAtTwoPointsInAChain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	edge := h.mustAllow(t, request("deep-host.example.com", "proxy-1"))
	if edge.RouteType != contract.RouteTypeNexthop {
		t.Fatalf("at the edge: route_type = %q, want nexthop", edge.RouteType)
	}
	if edge.Hop == nil {
		t.Fatal("a nexthop route with no hop metadata: the chain cannot be built")
	}
	if edge.Hop.NextProxyID != "proxy-2" {
		t.Errorf("next_proxy_id = %q, want proxy-2", edge.Hop.NextProxyID)
	}
	if edge.Hop.FinalTarget != "deep-host.example.com" {
		t.Errorf("final_target = %q, want the end host", edge.Hop.FinalTarget)
	}
	if edge.Target != "proxy-2.example.com" || edge.TargetPort != 2222 {
		t.Errorf("target = %q:%d, want the next proxy's address", edge.Target, edge.TargetPort)
	}
	// "This proxy appended", and nothing further along the path: the next
	// proxy has not traversed itself yet, and a trail that ran ahead of the
	// session would have it refuse its own leg as a loop.
	if got := edge.Hop.HopTrail; len(got) != 1 || got[0] != "proxy-1" {
		t.Errorf("hop_trail = %v, want exactly [proxy-1]", got)
	}

	inner := h.mustAllow(t, request("deep-host.example.com", "proxy-2", "proxy-1"))
	if inner.RouteType != contract.RouteTypeDirect {
		t.Fatalf("behind the edge: route_type = %q, want direct — the path starts at the proxy ASKING, "+
			"not at the user's entry proxy", inner.RouteType)
	}
	if inner.Hop != nil {
		t.Errorf("a direct route carries hop metadata: %+v", inner.Hop)
	}
	if inner.Target != "deep-host.example.com" {
		t.Errorf("target = %q, want the end host", inner.Target)
	}
}

// A trail that already names the asking proxy is a cycle in the estate's
// routing. That is a fault in the fleet, not a decision about the user: 5xx
// (M11), never 401, and never a route that closes the loop.
func TestATrailThatLoopsIsAnOutageAndNotADeny(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	for _, tc := range []struct {
		name  string
		req   *contract.AuthorizeRequest
		phase string
	}{
		{
			name:  "the asking proxy is already in its own trail",
			req:   request("deep-host.example.com", "proxy-1", "proxy-2", "proxy-1"),
			phase: "loop",
		},
		{
			name:  "the next hop is already in the trail",
			req:   request("deep-host.example.com", "proxy-1", "proxy-2"),
			phase: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := h.authorize(t, tc.req)
			if err == nil {
				t.Fatalf("a looping chain was answered %+v; it must be refused as an outage", out)
			}
			if out.Deny != nil {
				t.Fatal("a looping chain was DENIED; the estate is broken, the user is not")
			}
			var routeErr *decision.RouteError
			if !errors.As(err, &routeErr) {
				t.Fatalf("error %v is not a *decision.RouteError", err)
			}
		})
	}
}

// The hop cap is spent by the trail, so this server keeps its own answers
// inside `max_hops` rather than relying on the proxy to notice.
func TestTheTrailSpendsTheHopBudget(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Four positions already traversed against a cap of four leaves no
	// budget for the hop this answer would add.
	req := request("deep-host.example.com", "proxy-1", "a", "b", "c", "d")
	_, err := h.authorize(t, req)
	if err == nil {
		t.Fatal("a chain past the hop cap was answered; the proxy would refuse the route this server said was fine")
	}
	var routeErr *decision.RouteError
	if !errors.As(err, &routeErr) {
		t.Fatalf("error %v is not a *decision.RouteError", err)
	}
	if routeErr.Reason != string(fleet.NoPathMaxHops) {
		t.Errorf("reason = %q, want %q", routeErr.Reason, fleet.NoPathMaxHops)
	}
}

// ---------------------------------------------------------------------------
// M11: which failures are decisions
// ---------------------------------------------------------------------------

// No path available — an enclave relay down, a zone no live proxy serves — is
// an OUTAGE. This is the M11 assertion that matters most in this phase,
// because "deny" is the tempting shortcut.
func TestNoPathIsAnOutageAndNotADeny(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	out, err := h.authorize(t, request("lost-host.example.com", "proxy-1"))
	if err == nil {
		t.Fatalf("an unreachable zone was answered %+v", out)
	}
	if out.Deny != nil {
		t.Fatal("an unreachable zone was DENIED: the user would be told access denied and the operator " +
			"sent to debug permissions during an outage")
	}
	var routeErr *decision.RouteError
	if !errors.As(err, &routeErr) {
		t.Fatalf("error %v is not a *decision.RouteError", err)
	}
	if routeErr.Reason != string(fleet.NoPathNoLiveRoute) {
		t.Errorf("reason = %q, want %q", routeErr.Reason, fleet.NoPathNoLiveRoute)
	}
}

// A tenant with no active bundle has no policy to have denied anybody with.
func TestATenantWithNoPolicyIsAnOutage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	if _, err := h.store.Pool().Exec(t.Context(), "UPDATE policy_bundles SET active = false"); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	out, err := h.authorize(t, request("edge-host.example.com", "proxy-1"))
	if !errors.Is(err, decision.ErrNoActiveBundle) {
		t.Fatalf("error = %v, want ErrNoActiveBundle", err)
	}
	if out.Deny != nil {
		t.Fatal("a tenant with no policy DENIED; it has no policy to have denied with")
	}
}

// ---------------------------------------------------------------------------
// decision records (M4)
// ---------------------------------------------------------------------------

// A deny writes a record naming the deciding rule. It is the path that matters
// most and the easiest one to forget: the user is given a session id and
// nothing else, and this is what that id resolves into.
func TestADenyWritesARecordNamingTheRule(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	out, err := h.authorize(t, request("forbidden.example.com", "proxy-1"))
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if out.Deny == nil {
		t.Fatal("the fixture's deny rule allowed")
	}
	if out.Deny.Rule != "deny-forbidden" {
		t.Errorf("rule = %q, want deny-forbidden", out.Deny.Rule)
	}

	rec, err := h.store.Decisions().Get(t.Context(), testTenant, out.Deny.DecisionID)
	if err != nil {
		t.Fatalf("the deny wrote no decision record: %v", err)
	}
	if rec.Effect != decision.EffectDeny {
		t.Errorf("effect = %q, want %q", rec.Effect, decision.EffectDeny)
	}
	if rec.MatchedRule != "deny-forbidden" {
		t.Errorf("matched_rule = %q, want deny-forbidden", rec.MatchedRule)
	}
	if len(rec.Snapshot) > 2 {
		t.Errorf("a denial carries a snapshot: %s", rec.Snapshot)
	}
	var expl map[string]any
	if err := json.Unmarshal(rec.Explanation, &expl); err != nil {
		t.Fatalf("explanation does not decode: %v", err)
	}
	if reason, _ := expl["deny_reason"].(string); !strings.Contains(reason, "not reachable by anybody") {
		t.Errorf("deny_reason = %q, want the rule's own reason", reason)
	}
}

// A record for a chained call names the trail it was decided under. "Which hop
// asked, and what had it already been through" is unrecoverable afterwards.
func TestAChainedDecisionRecordNamesItsTrail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	resp := h.mustAllow(t, request("deep-host.example.com", "proxy-2", "proxy-1"))
	rec, err := h.store.Decisions().Get(t.Context(), testTenant, resp.DecisionID)
	if err != nil {
		t.Fatalf("no decision record: %v", err)
	}
	if rec.ProxyID != "proxy-2" {
		t.Errorf("proxy_id = %q, want the hop that asked", rec.ProxyID)
	}
	if rec.SessionID != "sess-1" {
		t.Errorf("session_id = %q, want the session the user was told", rec.SessionID)
	}

	var inputs struct {
		Context struct {
			HopTrail []string `json:"hop_trail"`
		} `json:"context"`
		Subject struct {
			Groups []string `json:"groups"`
		} `json:"subject"`
	}
	if err := json.Unmarshal(rec.Inputs, &inputs); err != nil {
		t.Fatalf("inputs do not decode: %v", err)
	}
	if got := inputs.Context.HopTrail; len(got) != 1 || got[0] != "proxy-1" {
		t.Errorf("recorded hop_trail = %v, want [proxy-1]", got)
	}
	if len(inputs.Subject.Groups) != 1 || inputs.Subject.Groups[0] != "sre" {
		t.Errorf("recorded groups = %v, want the groups THIS SERVER holds", inputs.Subject.Groups)
	}
}

// An allowed evaluation that could not be answered is recorded as unserved,
// not as an allow. An allow record for a session the proxy was handed a 5xx
// for is a record that lies about exactly the case somebody is reading it for.
func TestAnUnservedDecisionIsRecordedAsSuch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	if _, err := h.authorize(t, request("lost-host.example.com", "proxy-1")); err == nil {
		t.Fatal("the unreachable target was answered")
	}
	recs, err := h.store.Decisions().ListBySession(t.Context(), testTenant, "sess-1", 10)
	if err != nil {
		t.Fatalf("list by session: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records for the session, want 1", len(recs))
	}
	if recs[0].Effect != decision.EffectUnserved {
		t.Errorf("effect = %q, want %q", recs[0].Effect, decision.EffectUnserved)
	}
	var expl struct {
		Unserved string `json:"unserved"`
	}
	if err := json.Unmarshal(recs[0].Explanation, &expl); err != nil {
		t.Fatalf("explanation does not decode: %v", err)
	}
	if !strings.Contains(expl.Unserved, "no-live-route") {
		t.Errorf("unserved = %q, want it to name the fleet's reason", expl.Unserved)
	}
}

// ---------------------------------------------------------------------------
// the trail carries no authority
// ---------------------------------------------------------------------------

// The identity on the wire may narrow a decision and may never widen one. A
// group a proxy awards itself in the request must not reach the engine.
func TestGroupsOnTheWireDoNotWidenADecision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	req := request("edge-host.example.com", "proxy-1")
	req.Identity.Subject = "mallory@example.com" // no record here
	req.Identity.Groups = []string{"sre"}        // and a group it awarded itself

	out, err := h.authorize(t, req)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if out.Deny == nil {
		t.Fatal("a subject this server has no record of was allowed on the strength of a group it sent itself")
	}
}

// ---------------------------------------------------------------------------
// vocabulary negotiation
// ---------------------------------------------------------------------------

func TestVocabularyNegotiation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	t.Run("the current version gets the whole snapshot", func(t *testing.T) {
		resp := h.mustAllow(t, request("edge-host.example.com", "proxy-1"))
		if resp.DecisionID == "" {
			t.Error("the response carries no decision_id")
		}
	})

	t.Run("an absent version is a malformed request, never a deny", func(t *testing.T) {
		req := request("edge-host.example.com", "proxy-1")
		req.PolicyVersion = nil
		out, err := h.authorize(t, req)
		if out.Deny != nil {
			t.Fatal("an absent policy_version was DENIED; a malformed caller is not a refused user (M11)")
		}
		var status *contract.StatusError
		if !errors.As(err, &status) || !errors.Is(status.Class, contract.ErrInvalidRequest) {
			t.Fatalf("error = %v, want an invalid-request classification", err)
		}
	})

	t.Run("a version below what the route needs is an outage naming the mismatch", func(t *testing.T) {
		req := request("edge-host.example.com", "proxy-1")
		one := int32(1)
		req.PolicyVersion = &one
		out, err := h.authorize(t, req)
		if out.Deny != nil {
			t.Fatal("a version mismatch was DENIED; it is a rollout problem, not a statement about the user")
		}
		var mismatch *decision.VersionMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("error = %v, want a *decision.VersionMismatchError", err)
		}
		if mismatch.Needed != contract.PolicyVersion || mismatch.Declared != 1 {
			t.Errorf("mismatch = %+v, want needed=%d declared=1", mismatch, contract.PolicyVersion)
		}
	})
}

// ---------------------------------------------------------------------------
// cache hints (PLAN §5.4, M9)
// ---------------------------------------------------------------------------

func TestCacheHints(t *testing.T) {
	t.Parallel()

	healthy := func(h *harness) {
		h.subs = &fakeSubscriptions{seen: map[string]time.Time{
			"proxy-1": time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC),
			"proxy-2": time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC),
		}}
	}

	t.Run("a route whose policy omits a hint returns none", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, healthy)
		resp := h.mustAllow(t, request("edge-host.example.com", "proxy-1"))
		if resp.Cache != nil {
			t.Errorf("cache = %+v, want none: the rule authored no hint", resp.Cache)
		}
	})

	t.Run("a route that sets one returns the server's TTL", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, healthy)
		resp := h.mustAllow(t, request("cached.example.com", "proxy-1"))
		if !resp.Cache.Cacheable() {
			t.Fatalf("cache = %+v, want a usable hint", resp.Cache)
		}
		if resp.Cache.TTLSeconds != 120 {
			t.Errorf("ttl_seconds = %d, want the authored 120", resp.Cache.TTLSeconds)
		}
		if resp.Cache.Key == "" {
			t.Error("a hint with a TTL and no key is one the proxy cannot have a revocation name")
		}
	})

	t.Run("no key is ever shared across identities", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, healthy)
		storetest.Seed(t, h.store, storetest.Fixture{
			Tenant:   testTenant,
			Subjects: []store.Subject{storetest.Subject("bob@example.com", "sre")},
		})

		first := h.mustAllow(t, request("cached.example.com", "proxy-1"))
		second := request("cached.example.com", "proxy-1")
		second.Identity.Subject = "bob@example.com"
		other := h.mustAllow(t, second)

		if first.Cache == nil || other.Cache == nil {
			t.Fatal("one of the two identities got no hint, so the comparison graded nothing")
		}
		if first.Cache.Key == other.Cache.Key {
			t.Fatalf("two identities share cache key %q against one target: one user would be served "+
				"the other's policy", first.Cache.Key)
		}
	})

	t.Run("a proxy with an unhealthy event stream gets no hint", func(t *testing.T) {
		t.Parallel()
		// The same route, the same policy, and nothing subscribed. A
		// cached allow that cannot be withdrawn is a grant with no
		// revocation (M9).
		h := newHarness(t)
		resp := h.mustAllow(t, request("cached.example.com", "proxy-1"))
		if resp.Cache != nil {
			t.Fatalf("cache = %+v, want none: the proxy holds no event subscription", resp.Cache)
		}
	})

	t.Run("a stale subscription is an unhealthy stream", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(h *harness) {
			h.subs = &fakeSubscriptions{seen: map[string]time.Time{
				"proxy-1": time.Date(2026, 9, 19, 11, 0, 0, 0, time.UTC),
			}}
		})
		resp := h.mustAllow(t, request("cached.example.com", "proxy-1"))
		if resp.Cache != nil {
			t.Fatalf("cache = %+v, want none: the subscription aged out an hour ago", resp.Cache)
		}
	})

	t.Run("a liveness read that fails withholds the hint and not the decision", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, healthy, func(h *harness) { h.refresh = time.Hour })

		// One healthy call first, so the graph this server holds was built
		// from rows that really existed. Then the subscription source
		// fails: the decision is still answered — from the held graph —
		// and the only thing lost is the hint, which is the trade this
		// path is supposed to make. Refusing a correct decision over an
		// optimisation would be the worse answer.
		if resp := h.mustAllow(t, request("cached.example.com", "proxy-1")); resp.Cache == nil {
			t.Fatal("the healthy call got no hint, so the comparison below grades nothing")
		}
		h.subs.err = errors.New("the subscription table is unreachable")

		resp := h.mustAllow(t, request("cached.example.com", "proxy-1"))
		if resp.Cache != nil {
			t.Errorf("cache = %+v, want none", resp.Cache)
		}
	})
}

// ---------------------------------------------------------------------------
// the route intent
// ---------------------------------------------------------------------------

// A rule that permits no hops, for a target that needs one, is a DENIAL: the
// estate is healthy, the path exists, and the answer is "no" because somebody
// wrote `intent: direct`.
func TestADirectOnlyRuleDeniesAChainedTarget(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	out, err := h.authorize(t, request("pinned.example.com", "proxy-1"))
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if out.Deny == nil {
		t.Fatal("a direct-only rule was answered with a chained route")
	}
	if !strings.Contains(out.Deny.Reason, "permits no hops") {
		t.Errorf("reason = %q, want it to name the intent", out.Deny.Reason)
	}
	rec, err := h.store.Decisions().Get(t.Context(), testTenant, out.Deny.DecisionID)
	if err != nil {
		t.Fatalf("no decision record: %v", err)
	}
	if rec.Effect != decision.EffectDeny {
		t.Errorf("effect = %q, want %q", rec.Effect, decision.EffectDeny)
	}
}

// ---------------------------------------------------------------------------
// the session bounds
// ---------------------------------------------------------------------------

// A chained call does not re-anchor the deadline. A duration would; an
// absolute instant does not, which is why the contract carries one.
func TestAChainedCallDoesNotReAnchorTheSessionDeadline(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	first := h.mustAllow(t, request("deep-host.example.com", "proxy-1"))
	h.clock = h.clock.Add(30 * time.Second)
	second := h.mustAllow(t, request("deep-host.example.com", "proxy-2", "proxy-1"))

	firstAt, ok := first.Deadline()
	if !ok {
		t.Fatal("the first leg carries no session_deadline")
	}
	secondAt, ok := second.Deadline()
	if !ok {
		t.Fatal("the second leg carries no session_deadline")
	}
	// The instants differ by exactly the clock's movement, never by a fresh
	// four hours: each hop resolves the same authored duration against its
	// own `now`, and the proxy enforces the SHORTER of what it holds and
	// what it is told, so a window cannot multiply.
	a, err := time.Parse(time.RFC3339, firstAt)
	if err != nil {
		t.Fatalf("first deadline %q: %v", firstAt, err)
	}
	b, err := time.Parse(time.RFC3339, secondAt)
	if err != nil {
		t.Fatalf("second deadline %q: %v", secondAt, err)
	}
	if diff := b.Sub(a); diff != 30*time.Second {
		t.Errorf("the second leg's deadline moved by %v, want 30s — the clock's movement and nothing more", diff)
	}
}

// ---------------------------------------------------------------------------
// the capability constraint (M17)
// ---------------------------------------------------------------------------

// declaring builds a request whose proxy declares the given rungs and nothing
// else. `capabilities` absent declares NOTHING, which is the fail-safe reading.
func declaring(target, proxyID string, execution ...contract.ExecutionRung) *contract.AuthorizeRequest {
	req := request(target, proxyID)
	req.Capabilities = nil
	if len(execution) > 0 {
		req.Capabilities = &contract.ProxyCapabilities{}
		for _, r := range execution {
			req.Capabilities.Execution = append(req.Capabilities.Execution, string(r))
		}
	}
	return req
}

// A rung the proxy build does not declare is never emitted. Emitting it would
// cost the rung silently — the proxy skips what it cannot honour — and on a
// one-rung ladder that is a denial nobody authored.
func TestARungTheProxyDoesNotDeclareIsNeverEmitted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	out, err := h.authorize(t, declaring("rung-host.example.com", "proxy-1"))
	if err == nil {
		t.Fatalf("a rung the proxy declared nothing about was answered: %+v", out.Response)
	}
	if out.Deny != nil {
		t.Fatal("an undeclared rung was DENIED; the policy is fine and the estate cannot serve it")
	}
	var capErr *decision.CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("error %v is not a *decision.CapabilityError", err)
	}
	if capErr.Source != "proxy" {
		t.Errorf("source = %q, want proxy", capErr.Source)
	}

	// The other half: declared, and served.
	resp := h.mustAllow(t, declaring("rung-host.example.com", "proxy-1", contract.ExecutionNoInteractiveShell))
	if resp.EnforcedExecution() != contract.ExecutionNoInteractiveShell {
		t.Errorf("execution rung = %q, want no-interactive-shell", resp.EnforcedExecution())
	}
}

// Stale, undated and absent are ONE case (M17): each provides nothing that has
// to be APPLIED, while leaving the two proxy-side defaults and an attested rung
// available. Testing the three together is the point — a suite that only
// covered "absent" would not notice an implementation that treats undated as
// fresh.
func TestAStaleUndatedOrAbsentTargetRecordIsOneCase(t *testing.T) {
	t.Parallel()

	states := []struct {
		name   string
		record func(h *harness) *fleet.TargetCapabilities
	}{
		{name: "absent", record: func(*harness) *fleet.TargetCapabilities { return nil }},
		{
			name: "undated",
			record: func(*harness) *fleet.TargetCapabilities {
				return &fleet.TargetCapabilities{
					Execution: []contract.ExecutionRung{contract.ExecutionAccountRestricted},
				}
			},
		},
		{
			name: "stale",
			record: func(h *harness) *fleet.TargetCapabilities {
				return &fleet.TargetCapabilities{
					Execution:  []contract.ExecutionRung{contract.ExecutionAccountRestricted},
					ObservedAt: h.clock.Add(-48 * time.Hour),
				}
			},
		},
	}

	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			if rec := state.record(h); rec != nil {
				rec.Key = fleet.TargetCapabilityKey{Hostname: "restricted-host.example.com"}
				rec.ReportedBy = "proxy-1"
				if _, err := h.fleet.ReportTargetCapabilities(t.Context(), testTenant, *rec); err != nil {
					t.Fatalf("report capabilities: %v", err)
				}
			}

			// The two proxy-side defaults still answer.
			if resp := h.mustAllow(t, request("edge-host.example.com", "proxy-1")); resp.Enforcement != nil {
				t.Errorf("enforcement = %+v, want absent on a route that names neither axis", resp.Enforcement)
			}

			// And an attested rung still answers, which is the whole point:
			// an appliance nobody can probe carries a real enforcement
			// claim rather than "none available".
			attested := h.mustAllow(t, declaring("attested-host.example.com", "proxy-1", contract.ExecutionPlatformAttested))
			if attested.EnforcedExecution() != contract.ExecutionPlatformAttested {
				t.Errorf("execution rung = %q, want platform-attested", attested.EnforcedExecution())
			}
			if attested.Enforcement.Attestation == nil || attested.Enforcement.Attestation.Reference == "" {
				t.Error("an attested rung was served with no attributable attestation")
			}

			// What the record cannot provide is a rung that has to be
			// APPLIED to an account on the target.
			_, err := h.authorize(t, declaring("restricted-host.example.com", "proxy-1", contract.ExecutionAccountRestricted))
			var capErr *decision.CapabilityError
			if !errors.As(err, &capErr) {
				t.Fatalf("an applied rung was served against a %s capability record: %v", state.name, err)
			}
			if capErr.Source != "target" {
				t.Errorf("source = %q, want target", capErr.Source)
			}
		})
	}
}

// And the fresh record is the other half: a rung the target HAS been reported
// able to take is served.
func TestAFreshTargetRecordUnlocksAnAppliedRung(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	if _, err := h.fleet.ReportTargetCapabilities(t.Context(), testTenant, fleet.TargetCapabilities{
		Key:        fleet.TargetCapabilityKey{Hostname: "restricted-host.example.com"},
		Execution:  []contract.ExecutionRung{contract.ExecutionAccountRestricted},
		ObservedAt: h.clock.Add(-time.Hour),
		ReportedBy: "proxy-1",
	}); err != nil {
		t.Fatalf("report capabilities: %v", err)
	}

	resp := h.mustAllow(t, declaring("restricted-host.example.com", "proxy-1", contract.ExecutionAccountRestricted))
	if resp.EnforcedExecution() != contract.ExecutionAccountRestricted {
		t.Errorf("execution rung = %q, want account-restricted", resp.EnforcedExecution())
	}
}

// A platform the enforcing proxy has no driver for is a decision that cannot be
// served, and so is a device field its driver does not accept. Both cost the
// rung silently on the proxy, which on a one-rung ladder is a denial nobody
// authored — so they are caught on the issue path instead.
func TestAPlatformOrDeviceFieldTheProxyCannotHonourIsRefused(t *testing.T) {
	t.Parallel()
	declared := func(h *harness) {
		h.proxyCap["proxy-1"] = fleet.Capabilities{
			Platforms:    []string{"fortigate"},
			DeviceFields: map[string][]string{"fortigate": {"vdom"}},
		}
	}

	t.Run("a platform it declared is served with its device fields intact", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, declared)
		resp := h.mustAllow(t, request("device-host.example.com", "proxy-1"))
		_, ladder := resp.Ladder()
		if len(ladder) != 1 {
			t.Fatalf("got %d ladder entries, want 1", len(ladder))
		}
		if got := ladder[0].Params[contract.DeviceFieldPrefix+"vdom"]; got != "root" {
			t.Errorf("device_field.vdom = %q, want root", got)
		}
	})

	t.Run("a platform it has no driver for is refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, declared)
		out, err := h.authorize(t, request("ios-host.example.com", "proxy-1"))
		if err == nil {
			t.Fatalf("a platform the proxy has no driver for was served: %+v", out.Response)
		}
		if out.Deny != nil {
			t.Fatal("an undriveable platform was DENIED rather than reported as unservable")
		}
		var capErr *decision.CapabilityError
		if !errors.As(err, &capErr) {
			t.Fatalf("error %v is not a *decision.CapabilityError", err)
		}
		if capErr.Axis != "platform" || capErr.Value != "ios" {
			t.Errorf("error = %+v, want the platform named", capErr)
		}
	})

	t.Run("a proxy that declares nothing constrains nothing", func(t *testing.T) {
		t.Parallel()
		// The stored declaration is a fleet-READINESS signal rather than
		// authority (M17). A fleet that has never reported would otherwise
		// have every ephemeral-account route refused, which is a bootstrap
		// that cannot start.
		h := newHarness(t)
		if _, err := h.authorize(t, request("ios-host.example.com", "proxy-1")); err != nil {
			t.Fatalf("a proxy that declared nothing refused a route: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// the two connection directions (proxy D11)
// ---------------------------------------------------------------------------

// A relay hop opens a channel over a registration the far proxy already holds,
// so it names that registration and dials nothing.
func TestARelayHopNamesTheRegistrationAndDialsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	resp := h.mustAllow(t, request("vault-host.example.com", "proxy-1"))
	if resp.RouteType != contract.RouteTypeNexthop {
		t.Fatalf("route_type = %q, want nexthop", resp.RouteType)
	}
	if resp.Hop.Direction() != contract.HopConnectionRelay {
		t.Fatalf("hop.connection = %q, want relay", resp.Hop.Direction())
	}
	if resp.Hop.NextProxyID != "proxy-3" {
		t.Errorf("next_proxy_id = %q, want proxy-3: a relay hop with none names no registration to open over",
			resp.Hop.NextProxyID)
	}
	if resp.Target != "proxy-3" || resp.TargetPort != 0 {
		t.Errorf("target = %q:%d, want the peer and no port: a relay hop opens no connection to dial",
			resp.Target, resp.TargetPort)
	}
}

// A relay edge whose registration has dropped is NOT viable and is never
// downgraded to a dial: dialling would punch through exactly the boundary the
// mode exists to preserve, at the moment the operator is least able to see it.
func TestARelayEdgeWithNoRegistrationIsAnOutageAndNeverADial(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) { h.relayRegistered = false })

	out, err := h.authorize(t, request("vault-host.example.com", "proxy-1"))
	if err == nil {
		t.Fatalf("a relay edge with no live registration was answered: %+v", out.Response)
	}
	if out.Deny != nil {
		t.Fatal("a dropped relay registration was DENIED; the estate is broken, the user is not")
	}
	var routeErr *decision.RouteError
	if !errors.As(err, &routeErr) {
		t.Fatalf("error %v is not a *decision.RouteError", err)
	}
}
