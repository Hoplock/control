// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/decision"
)

// declaring returns a request that declares one vocabulary.
func atVersion(version int32, target string) *contract.AuthorizeRequest {
	req := request(target, "proxy-1")
	req.PolicyVersion = &version
	return req
}

// requireMismatch asserts an outage naming both numbers and the proxy, and
// never a deny or an answer.
func requireMismatch(t *testing.T, h *harness, req *contract.AuthorizeRequest) {
	t.Helper()
	out, err := h.authorize(t, req)
	if out.Deny != nil {
		t.Fatalf("a proxy declaring %d was DENIED %s; a mismatch is a rollout problem, not a statement about the user (M11)",
			*req.PolicyVersion, req.Target)
	}
	if out.Response != nil {
		t.Fatalf("a proxy declaring %d was answered %s; this server serves %d only, and an answer in any other "+
			"vocabulary is either one it cannot read or one with restrictions dropped", *req.PolicyVersion, req.Target, contract.PolicyVersion)
	}
	var mismatch *decision.VersionMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("%s at %d: error = %v, want a *decision.VersionMismatchError", req.Target, *req.PolicyVersion, err)
	}
	want := decision.VersionMismatchError{Declared: *req.PolicyVersion, Served: contract.PolicyVersion, ProxyID: req.Conn.ProxyID}
	if *mismatch != want {
		t.Errorf("mismatch = %+v, want %+v", *mismatch, want)
	}
	msg := mismatch.Error()
	for _, part := range []string{
		strconv.Itoa(int(*req.PolicyVersion)), strconv.Itoa(int(contract.PolicyVersion)), req.Conn.ProxyID,
	} {
		if !strings.Contains(msg, part) {
			t.Errorf("the refusal %q does not name %q", msg, part)
		}
	}
}

// otherVocabularies is every declared vocabulary this server does not serve,
// from the field's minimum to two past the current one.
func otherVocabularies() []int32 {
	var out []int32
	for v := contract.PolicyVersionMinimum; v <= contract.PolicyVersion+2; v++ {
		if v != contract.PolicyVersion {
			out = append(out, v)
		}
	}
	return out
}

// A PROXY IS NEVER SENT VOCABULARY ABOVE WHAT IT DECLARED (PLAN §4, 0064). This
// server serves exactly contract.PolicyVersion, so the only answers it gives
// are to a proxy declaring that, and every other declaration — lower or
// higher, on a route carrying the newest vocabulary or none of it, and on a
// route the policy denies — is the same 5xx. A proxy would refuse a whole
// response over an unknown `method` value or field, and the user would see an
// outage; a thinned answer would be a widened session. Neither can happen
// when the only answer is in the one vocabulary the proxy declared.
//
// Until 0064 the gate was tiered, and a proxy declaring 4 was answered every
// route without `brokered-certificate`, a floor or a ban. Each of those routes
// is refused below: the single-version rule is stricter than the tiers in every
// case, which is what made folding them in safe.
func TestEveryOtherVocabularyIsRefusedOnEveryRoute(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	routes := map[string]string{
		"brokered-certificate":                  "cert-host.example.com",
		"the baseline":                          "edge-host.example.com",
		"a target this server has no record of": "nowhere.example.com",
	}
	for name, target := range routes {
		for _, v := range otherVocabularies() {
			t.Run(name+" at "+strconv.Itoa(int(v)), func(t *testing.T) {
				requireMismatch(t, h, atVersion(v, target))
			})
		}
	}

	// The served version is answered exactly as the policy says, the
	// certificate rung and the brokered key both on the ladder.
	resp := h.mustAllow(t, atVersion(contract.PolicyVersion, "cert-host.example.com"))
	if len(resp.TargetAuthLadder) != 2 || resp.TargetAuthLadder[0].Method != contract.TargetAuthBrokeredCertificate {
		t.Fatalf("ladder = %+v, want the certificate rung first and both rungs", resp.TargetAuthLadder)
	}
	for name := range resp.TargetAuthLadder[0].Params {
		switch name {
		case contract.ParamUsername, contract.ParamKeyType, contract.ParamLifetimeSeconds:
		default:
			t.Errorf("the certificate rung carries %q, outside the three policy names", name)
		}
	}
	h.mustAllow(t, atVersion(contract.PolicyVersion, "edge-host.example.com"))
	// The unknown target is an outage of its own at the served version — and
	// a different one, so the refusals above were the version's.
	_, err := h.authorize(t, atVersion(contract.PolicyVersion, "nowhere.example.com"))
	var mismatch *decision.VersionMismatchError
	if err == nil || errors.As(err, &mismatch) {
		t.Fatalf("the unknown target at the served version = %v; want its own outage, not a version mismatch", err)
	}
}

// The floor and the bans are refused at every other vocabulary like any other
// route, and answered as authored at the served one: never stripped to fit.
func TestAFloorOrABanIsRefusedAtEveryOtherVocabulary(t *testing.T) {
	t.Parallel()
	for name, lines := range map[string]string{
		"a floor": "      algorithm_floor: modern-kex\n",
		"a ban":   "      algorithm_bans: {ciphers: [aes128-ctr]}\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.activate(t, algorithmBundle(lines))
			for _, v := range otherVocabularies() {
				req := atVersion(v, "edge-host.example.com")
				req.Capabilities = upstreamCaps(t)
				requireMismatch(t, h, req)
			}
			req := atVersion(contract.PolicyVersion, "edge-host.example.com")
			req.Capabilities = upstreamCaps(t)
			resp := h.mustAllow(t, req)
			if resp.AlgorithmFloor == "" && resp.AlgorithmBans.Empty() {
				t.Fatal("the served version was answered without the route's floor or ban")
			}
		})
	}
}

// The gate runs before anything is read: a standing denial, which is decided
// above the engine, does not reach a proxy of the wrong build either. Its
// answer would be a 401 at one vocabulary and a 5xx at the next, which is the
// nondeterminism the early gate exists to remove.
func TestTheGateRunsBeforeEvaluation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.activate(t, `
schema_version: 1
tenant: acme
rules:
  - id: nothing
    effect: deny
    reason: nobody reaches edge-host in this test
    match:
      target:
        hostnames: [edge-host.example.com]
`)
	for _, v := range otherVocabularies() {
		requireMismatch(t, h, atVersion(v, "edge-host.example.com"))
	}
	if out, err := h.authorize(t, atVersion(contract.PolicyVersion, "edge-host.example.com")); err != nil || out.Deny == nil {
		t.Fatalf("the authored deny at the served version = %+v, %v; want the deny", out, err)
	}
}

// algorithmBundle is a one-rule policy for edge-host.example.com whose route
// carries the given algorithm lines (already indented under `route:`).
func algorithmBundle(lines string) string {
	return `
schema_version: 1
tenant: acme
rules:
  - id: algorithms
    effect: allow
    match:
      target:
        hostnames: [edge-host.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-key
          username: netadmin
          credential_ref: vault://edge
` + lines
}

// upstreamCaps is a declaration as the proxy renders it at the vendored commit.
func upstreamCaps(t *testing.T) *contract.ProxyCapabilities {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "contract", "testdata", "upstream-capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Capabilities *contract.ProxyCapabilities `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	return req.Capabilities
}

func defaultOffer(t *testing.T, caps *contract.ProxyCapabilities) contract.AlgorithmProfileCapability {
	t.Helper()
	for _, p := range caps.AlgorithmProfiles {
		if p.Profile == contract.AlgorithmProfileDefault {
			return p
		}
	}
	t.Fatal("no default profile in the fixture")
	return contract.AlgorithmProfileCapability{}
}

func yamlList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func requireCapabilityRefusal(t *testing.T, h *harness, req *contract.AuthorizeRequest, axis string) {
	t.Helper()
	out, err := h.authorize(t, req)
	if out.Deny != nil {
		t.Fatal("a capability shortfall was DENIED; nobody authored a refusal of this user (M11)")
	}
	if out.Response != nil {
		t.Fatalf("the route was answered (%+v); it must be refused whole, never stripped to fit", out.Response.AlgorithmBans)
	}
	var capErr *decision.CapabilityError
	if !errors.As(err, &capErr) || capErr.Axis != axis {
		t.Fatalf("error = %v, want a CapabilityError on %s", err, axis)
	}
}

// A floor level is sent only to a proxy that declared it.
func TestAFloorIsSentOnlyToAProxyThatDeclaredIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.activate(t, algorithmBundle("      algorithm_floor: pq-hybrid-kex\n"))

	req := atVersion(contract.PolicyVersion, "edge-host.example.com")
	req.Capabilities = upstreamCaps(t)
	resp := h.mustAllow(t, req)
	if resp.AlgorithmFloor != contract.AlgorithmFloorPQHybridKex {
		t.Fatalf("algorithm_floor = %q, want pq-hybrid-kex", resp.AlgorithmFloor)
	}

	without := atVersion(contract.PolicyVersion, "edge-host.example.com")
	without.Capabilities = upstreamCaps(t)
	without.Capabilities.AlgorithmFloors = []contract.AlgorithmFloorCapability{{
		Level: contract.AlgorithmFloorModernKex, KeyExchanges: []string{"curve25519-sha256"},
	}}
	requireCapabilityRefusal(t, h, without, "algorithm_floor")

	absent := atVersion(contract.PolicyVersion, "edge-host.example.com")
	absent.Capabilities = nil
	requireCapabilityRefusal(t, h, absent, "algorithm_floor")
}

// A ban is answered exactly as authored where the asking proxy's declaration
// leaves every axis something, refused to exactly the proxy whose declaration
// shows an axis emptied, and answered as authored where the declaration cannot
// judge it.
func TestABanIsJudgedPerProxyFromItsOwnDeclaration(t *testing.T) {
	t.Parallel()
	caps := upstreamCaps(t)
	def := defaultOffer(t, caps)
	var allButLibSSH []string
	for _, kex := range def.KeyExchanges {
		if kex != contract.KexCurve25519LibSSH {
			allButLibSSH = append(allButLibSSH, kex)
		}
	}

	for _, tc := range []struct {
		name, lines string
		// widen is a build whose declaration leaves the axis something.
		widen func(*contract.ProxyCapabilities)
	}{
		{
			name:  "a ciphers ban under default",
			lines: "      algorithm_bans: {ciphers: " + yamlList(def.Ciphers) + "}\n",
			widen: func(c *contract.ProxyCapabilities) {
				setDefault(c, func(p *contract.AlgorithmProfileCapability) { p.Ciphers = append(p.Ciphers, "aes256-cbc") })
			},
		},
		{
			name:  "a key-exchange ban removing every exchange the floor accepts",
			lines: "      algorithm_floor: pq-hybrid-kex\n      algorithm_bans: {key_exchanges: [mlkem768x25519-sha256]}\n",
			widen: func(c *contract.ProxyCapabilities) {
				for i := range c.AlgorithmFloors {
					if c.AlgorithmFloors[i].Level == contract.AlgorithmFloorPQHybridKex {
						c.AlgorithmFloors[i].KeyExchanges = append(c.AlgorithmFloors[i].KeyExchanges, "sntrup761x25519-sha512")
					}
				}
				setDefault(c, func(p *contract.AlgorithmProfileCapability) {
					p.KeyExchanges = append(p.KeyExchanges, "sntrup761x25519-sha512")
				})
			},
		},
		{
			// By name: plain set subtraction leaves the libssh spelling.
			name:  "curve25519 under one spelling",
			lines: "      algorithm_bans: {key_exchanges: " + yamlList(allButLibSSH) + "}\n",
			widen: func(c *contract.ProxyCapabilities) {
				setDefault(c, func(p *contract.AlgorithmProfileCapability) {
					p.KeyExchanges = append(p.KeyExchanges, "sntrup761x25519-sha512")
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.activate(t, algorithmBundle(tc.lines))

			refused := atVersion(contract.PolicyVersion, "edge-host.example.com")
			refused.Capabilities = upstreamCaps(t)
			requireCapabilityRefusal(t, h, refused, "algorithm_bans")

			other := atVersion(contract.PolicyVersion, "edge-host.example.com")
			other.Capabilities = upstreamCaps(t)
			tc.widen(other.Capabilities)
			resp := h.mustAllow(t, other)
			if resp.AlgorithmBans.Empty() {
				t.Fatal("the ban was stripped from the answer")
			}

			unknown := atVersion(contract.PolicyVersion, "edge-host.example.com")
			unknown.Capabilities = upstreamCaps(t)
			unknown.Capabilities.AlgorithmProfiles = nil
			if resp := h.mustAllow(t, unknown); resp.AlgorithmBans.Empty() {
				t.Fatal("a declaration that cannot judge the ban was answered without it")
			}
		})
	}
}

func setDefault(c *contract.ProxyCapabilities, edit func(*contract.AlgorithmProfileCapability)) {
	c.AlgorithmProfiles = slices.Clone(c.AlgorithmProfiles)
	for i := range c.AlgorithmProfiles {
		if c.AlgorithmProfiles[i].Profile == contract.AlgorithmProfileDefault {
			edit(&c.AlgorithmProfiles[i])
		}
	}
}

// The declaration is recorded per proxy, and only when a build changes (M5).
func TestTheAlgorithmDeclarationIsRecordedOncePerBuild(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()

	req := atVersion(contract.PolicyVersion, "edge-host.example.com")
	req.Capabilities = upstreamCaps(t)
	h.mustAllow(t, req)
	first, err := h.store.ProxyAlgorithmDeclarations().List(ctx, testTenant)
	if err != nil || len(first) != 1 || first[0].ProxyID != "proxy-1" {
		t.Fatalf("declarations = %+v (%v), want proxy-1's", first, err)
	}

	h.clock = h.clock.Add(30 * time.Second)
	h.mustAllow(t, req)
	again, _ := h.store.ProxyAlgorithmDeclarations().List(ctx, testTenant)
	if !again[0].DeclaredAt.Equal(first[0].DeclaredAt) {
		t.Error("an unchanged declaration was rewritten")
	}

	changed := atVersion(contract.PolicyVersion, "edge-host.example.com")
	changed.Capabilities = upstreamCaps(t)
	changed.Capabilities.AlgorithmFloors = changed.Capabilities.AlgorithmFloors[:1]
	h.mustAllow(t, changed)
	after, _ := h.store.ProxyAlgorithmDeclarations().List(ctx, testTenant)
	if after[0].Digest == first[0].Digest {
		t.Error("a changed build's declaration was not recorded")
	}
}
