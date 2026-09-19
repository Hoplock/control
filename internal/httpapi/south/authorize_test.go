// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// `/v1/authorize`, end to end: the real listener, the real middleware chain,
// the real engine, and a real database.
//
// The decision layer's own tests cover what it decides. What is asserted HERE
// is the thing only the whole stack can answer — which STATUS CODE each outcome
// becomes — because that is the mapping M11 is about and it is the one a unit
// test on either side cannot see.

// authorizeBundle is the policy the cases below are decided under.
const authorizeBundle = `
schema_version: 1
tenant: acme
groups: [sre]
rules:
  - id: sre-fleet
    effect: allow
    match:
      subject:
        groups: [sre]
      target:
        hostnames: [edge-host.example.com, deep-host.example.com, vault-host.example.com, lost-host.example.com]
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

// seedAuthorize adds the fleet, the targets and the policy the authorize cases
// need. The base harness seeds the identities and the proxy credential; this is
// everything a DECISION needs on top of an authenticated caller.
func seedAuthorize(t *testing.T, h *harness) {
	t.Helper()
	ctx := t.Context()

	for _, p := range []struct{ id, zone string }{
		{"proxy-1", "edge"}, {"proxy-2", "deep"}, {"proxy-3", "vault"},
	} {
		if err := h.store.Proxies().Upsert(ctx, testTenant, store.Proxy{
			ID:              p.id,
			Zone:            p.zone,
			PublicKey:       []byte("authorize-key-" + p.id),
			State:           store.EnrollmentEnrolled,
			LastHeartbeatAt: h.clock,
		}); err != nil {
			t.Fatalf("seed proxy %s: %v", p.id, err)
		}
	}
	if err := h.store.ProxyEdges().ReplaceForProxy(ctx, testTenant, "proxy-1", []store.ProxyEdge{
		{ProxyID: "proxy-1", ToZone: "deep", Direction: store.HopDial,
			Address: "proxy-2.example.com:2222", NextProxyID: "proxy-2", Cost: 1},
		{ProxyID: "proxy-1", ToZone: "vault", Direction: store.HopRelay,
			NextProxyID: "proxy-3", Cost: 1},
	}); err != nil {
		t.Fatalf("seed edges: %v", err)
	}
	if err := h.store.RelayRegistrations().ReplaceForUpstream(
		ctx, testTenant, "proxy-1", []string{"proxy-3"}, h.clock); err != nil {
		t.Fatalf("seed relay registration: %v", err)
	}

	for _, tgt := range []store.Target{
		{ID: "t-edge", Hostname: "edge-host.example.com", Zone: "edge"},
		{ID: "t-deep", Hostname: "deep-host.example.com", Zone: "deep"},
		{ID: "t-vault", Hostname: "vault-host.example.com", Zone: "vault"},
		{ID: "t-lost", Hostname: "lost-host.example.com", Zone: "enclave"},
		{ID: "t-denied", Hostname: "denied-host.example.com", Zone: "edge"},
	} {
		if err := h.store.Targets().Upsert(ctx, testTenant, tgt); err != nil {
			t.Fatalf("seed target %s: %v", tgt.Hostname, err)
		}
	}

	version, err := h.store.PolicyBundles().NextVersion(ctx, testTenant)
	if err != nil {
		t.Fatalf("next bundle version: %v", err)
	}
	if err := h.store.PolicyBundles().Insert(ctx, testTenant, store.PolicyBundle{
		Version: version, Source: []byte(authorizeBundle),
		Hash: "sha256:authorize-fixture", UploadedBy: "south_test",
	}); err != nil {
		t.Fatalf("insert bundle: %v", err)
	}
	if err := h.store.PolicyBundles().Activate(ctx, testTenant, version); err != nil {
		t.Fatalf("activate bundle: %v", err)
	}
}

func authorizeRequest(target, proxyID string, trail ...string) contract.AuthorizeRequest {
	version := contract.PolicyVersion
	return contract.AuthorizeRequest{
		Identity: contract.Identity{
			Subject: "alice@example.com",
			Login:   "alice",
			Source:  "local",
		},
		Target:        target,
		AuthMethod:    contract.AuthMethodCert,
		PolicyVersion: &version,
		Conn: contract.ConnMeta{
			SessionID:  "sess-e2e",
			ProxyID:    proxyID,
			ClientAddr: "203.0.113.7:52344",
			HopTrail:   trail,
			Timestamp:  nowRFC3339(),
		},
	}
}

func decodeAuthorize(t *testing.T, res *httptest.ResponseRecorder) contract.AuthorizeResponse {
	t.Helper()
	if res.Code != http.StatusOK {
		t.Fatalf("authorize = %d, want 200: %s", res.Code, res.Body)
	}
	var out contract.AuthorizeResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("response does not decode: %v (%s)", err, res.Body)
	}
	return out
}

// The three answers, through the whole stack.
func TestAuthorizeEndToEnd(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedAuthorize(t, h)

	t.Run("a direct route is a 200 with the required envelope", func(t *testing.T) {
		res := h.post(t, contract.PathAuthorize, authorizeRequest("edge-host.example.com", "proxy-1"))
		got := decodeAuthorize(t, res)
		if got.RouteType != contract.RouteTypeDirect {
			t.Errorf("route_type = %q, want direct", got.RouteType)
		}
		if got.DecisionID == "" {
			t.Error("no decision_id: the user is given a session id and this is what resolves it")
		}
		// The two fields the contract requires must be on the wire even
		// when they say "nothing".
		body := res.Body.String()
		for _, field := range []string{`"permitted_channels"`, `"filter_policy"`, `"target"`, `"route_type"`} {
			if !strings.Contains(body, field) {
				t.Errorf("the response omits %s, which the contract requires: %s", field, body)
			}
		}
	})

	t.Run("a dial nexthop carries its hop metadata", func(t *testing.T) {
		res := h.post(t, contract.PathAuthorize, authorizeRequest("deep-host.example.com", "proxy-1"))
		got := decodeAuthorize(t, res)
		if got.RouteType != contract.RouteTypeNexthop {
			t.Fatalf("route_type = %q, want nexthop", got.RouteType)
		}
		if got.Hop == nil {
			t.Fatal("a nexthop route with no hop metadata: the chain cannot be built")
		}
		if got.Hop.Direction() != contract.HopConnectionDial {
			t.Errorf("hop.connection = %q, want dial", got.Hop.Direction())
		}
		if got.Target != "proxy-2.example.com" || got.TargetPort != 2222 {
			t.Errorf("target = %q:%d, want the next proxy's address", got.Target, got.TargetPort)
		}
	})

	t.Run("a relay nexthop names the registration it opens over", func(t *testing.T) {
		res := h.post(t, contract.PathAuthorize, authorizeRequest("vault-host.example.com", "proxy-1"))
		got := decodeAuthorize(t, res)
		if got.Hop == nil || got.Hop.Direction() != contract.HopConnectionRelay {
			t.Fatalf("hop = %+v, want a relay hop", got.Hop)
		}
		if got.Hop.NextProxyID != "proxy-3" {
			t.Errorf("next_proxy_id = %q, want proxy-3", got.Hop.NextProxyID)
		}
	})

	t.Run("the same login and target answer differently along the chain", func(t *testing.T) {
		// The assertion the trail exists for: an empty trail from the edge
		// answers nexthop, and a trail naming that edge, asked by the proxy
		// behind it, answers direct.
		edge := decodeAuthorize(t, h.post(t, contract.PathAuthorize,
			authorizeRequest("deep-host.example.com", "proxy-1")))
		inner := decodeAuthorize(t, h.post(t, contract.PathAuthorize,
			authorizeRequest("deep-host.example.com", "proxy-2", "proxy-1")))

		if edge.RouteType != contract.RouteTypeNexthop || inner.RouteType != contract.RouteTypeDirect {
			t.Fatalf("edge answered %q and the inner hop %q; want nexthop then direct",
				edge.RouteType, inner.RouteType)
		}
	})

	t.Run("a target policy refuses is a 401 decision with the envelope", func(t *testing.T) {
		res := h.post(t, contract.PathAuthorize, authorizeRequest("denied-host.example.com", "proxy-1"))
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("authorize = %d, want 401: %s", res.Code, res.Body)
		}
		h.requireEnvelope(t, res)
		// Deliberately vague: a precise denial makes the proxy an oracle
		// for probing the estate (M4).
		if strings.Contains(res.Body.String(), "sre-fleet") {
			t.Errorf("the denial names a policy rule to the caller: %s", res.Body)
		}
	})

	t.Run("no path available is a 5xx outage and never a 401", func(t *testing.T) {
		res := h.post(t, contract.PathAuthorize, authorizeRequest("lost-host.example.com", "proxy-1"))
		if res.Code != http.StatusInternalServerError {
			t.Fatalf("authorize = %d, want 500: a user denied by policy and a user unreachable because a "+
				"relay is down must not get the same answer (M11): %s", res.Code, res.Body)
		}
		h.requireEnvelope(t, res)
	})

	t.Run("a chain that loops is a 5xx outage and never a 401", func(t *testing.T) {
		res := h.post(t, contract.PathAuthorize,
			authorizeRequest("deep-host.example.com", "proxy-1", "proxy-2", "proxy-1"))
		if res.Code != http.StatusInternalServerError {
			t.Fatalf("authorize = %d, want 500: a cycle is a fault in the estate's routing, not a "+
				"decision about the user: %s", res.Code, res.Body)
		}
		h.requireEnvelope(t, res)
	})

	t.Run("an absent policy_version is a 400 and never a 401", func(t *testing.T) {
		req := authorizeRequest("edge-host.example.com", "proxy-1")
		req.PolicyVersion = nil
		res := h.post(t, contract.PathAuthorize, req)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("authorize = %d, want 400: a proxy that cannot say what it reads is a malformed "+
				"caller, not a refused user: %s", res.Code, res.Body)
		}
		h.requireEnvelope(t, res)
	})

	t.Run("a version below what the route needs is a 5xx naming the mismatch", func(t *testing.T) {
		req := authorizeRequest("edge-host.example.com", "proxy-1")
		one := int32(1)
		req.PolicyVersion = &one
		res := h.post(t, contract.PathAuthorize, req)
		if res.Code != http.StatusInternalServerError {
			t.Fatalf("authorize = %d, want 500: %s", res.Code, res.Body)
		}
		var env contract.ErrorResponse
		if err := json.Unmarshal(res.Body.Bytes(), &env); err != nil {
			t.Fatalf("envelope does not decode: %v", err)
		}
		if env.Error.Code != contract.ErrCodeVersionUnsupported {
			t.Errorf("error.code = %q, want %q", env.Error.Code, contract.ErrCodeVersionUnsupported)
		}
		if !strings.Contains(env.Error.Message, "policy vocabulary") {
			t.Errorf("message = %q, want it to name the mismatch so an operator knows which proxy to upgrade",
				env.Error.Message)
		}
	})
}

// A bound token may not ask in another proxy's name. The route is computed FROM
// the asking proxy, so a credential that could name anybody could ask for a
// route it does not sit on.
func TestABoundTokenCannotAuthorizeForAnotherProxy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedAuthorize(t, h)

	token, _, err := h.fleet.IssueProxyToken(t.Context(), testTenant, "proxy-2", "bound", time.Time{})
	if err != nil {
		t.Fatalf("IssueProxyToken: %v", err)
	}

	res := h.do(http.MethodPost, contract.PathAuthorize,
		mustJSON(authorizeRequest("edge-host.example.com", "proxy-1")), token.String())
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("a bound token asking in another proxy's name = %d, want 401: %s", res.Code, res.Body)
	}
	h.requireEnvelope(t, res)

	// And for itself, it is served.
	res = h.do(http.MethodPost, contract.PathAuthorize,
		mustJSON(authorizeRequest("deep-host.example.com", "proxy-2", "proxy-1")), token.String())
	if res.Code != http.StatusOK {
		t.Fatalf("a bound token asking for itself = %d, want 200: %s", res.Code, res.Body)
	}
}
