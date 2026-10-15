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

// requireMismatch asserts an outage naming both numbers, and never a deny.
func requireMismatch(t *testing.T, h *harness, req *contract.AuthorizeRequest, needed int32) {
	t.Helper()
	out, err := h.authorize(t, req)
	if out.Deny != nil {
		t.Fatal("a vocabulary mismatch was DENIED; it is a rollout problem, not a statement about the user (M11)")
	}
	if out.Response != nil {
		t.Fatalf("a proxy declaring %d was answered; the route needs %d and must be refused whole, never thinned",
			*req.PolicyVersion, needed)
	}
	var mismatch *decision.VersionMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %v, want a *decision.VersionMismatchError", err)
	}
	if mismatch.Needed != needed || mismatch.Declared != *req.PolicyVersion {
		t.Errorf("mismatch = %+v, want needed=%d declared=%d", mismatch, needed, *req.PolicyVersion)
	}
}

// brokered-certificate is vocabulary 5 (Hoplock/proxy#68). A proxy declaring 4
// is refused that route WHOLE — never answered with the brokered-key rung alone,
// which would drop a rung the policy wrote — and still gets every route it can
// read.
func TestTheBrokeredCertificateTier(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	requireMismatch(t, h, atVersion(contract.VocabularyBaseline, "cert-host.example.com"),
		contract.VocabularyBrokeredCertificate)

	resp := h.mustAllow(t, atVersion(contract.VocabularyBrokeredCertificate, "cert-host.example.com"))
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

	// A route without the method is answered to the older proxy unchanged.
	h.mustAllow(t, atVersion(contract.VocabularyBaseline, "edge-host.example.com"))

	// At ANY rung: a ladder that names the method second needs 5 as surely
	// as one that names it first, and is never answered with its first rung
	// alone.
	h.republish(t, `
schema_version: 1
tenant: acme
rules:
  - id: certificate-second
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
        - method: brokered-certificate
          username: netadmin
`)
	requireMismatch(t, h, atVersion(contract.VocabularyBaseline, "edge-host.example.com"),
		contract.VocabularyBrokeredCertificate)
	if resp := h.mustAllow(t, atVersion(contract.VocabularyBrokeredCertificate, "edge-host.example.com")); len(resp.TargetAuthLadder) != 2 {
		t.Fatalf("ladder = %+v, want both rungs", resp.TargetAuthLadder)
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

// A floor or a ban is vocabulary 6 (Hoplock/proxy#69): refused whole to a proxy
// declaring 5 with a 5xx naming both numbers, while a brokered-certificate route
// is still answered at 5 and a route with neither is answered unchanged.
func TestTheAlgorithmFloorTier(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Neither: answered to a proxy declaring 5.
	h.mustAllow(t, atVersion(contract.VocabularyBrokeredCertificate, "edge-host.example.com"))
	// Only brokered-certificate: still answered at 5.
	h.mustAllow(t, atVersion(contract.VocabularyBrokeredCertificate, "cert-host.example.com"))

	h.republish(t, algorithmBundle("      algorithm_floor: modern-kex\n"))
	requireMismatch(t, h, atVersion(contract.VocabularyBrokeredCertificate, "edge-host.example.com"),
		contract.VocabularyAlgorithmFloor)

	h.republish(t, algorithmBundle("      algorithm_bans: {ciphers: [aes128-ctr]}\n"))
	requireMismatch(t, h, atVersion(contract.VocabularyBrokeredCertificate, "edge-host.example.com"),
		contract.VocabularyAlgorithmFloor)
}

// A floor level is sent only to a proxy that declared it.
func TestAFloorIsSentOnlyToAProxyThatDeclaredIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.activate(t, algorithmBundle("      algorithm_floor: pq-hybrid-kex\n"))

	req := atVersion(contract.VocabularyAlgorithmFloor, "edge-host.example.com")
	req.Capabilities = upstreamCaps(t)
	resp := h.mustAllow(t, req)
	if resp.AlgorithmFloor != contract.AlgorithmFloorPQHybridKex {
		t.Fatalf("algorithm_floor = %q, want pq-hybrid-kex", resp.AlgorithmFloor)
	}

	without := atVersion(contract.VocabularyAlgorithmFloor, "edge-host.example.com")
	without.Capabilities = upstreamCaps(t)
	without.Capabilities.AlgorithmFloors = []contract.AlgorithmFloorCapability{{
		Level: contract.AlgorithmFloorModernKex, KeyExchanges: []string{"curve25519-sha256"},
	}}
	requireCapabilityRefusal(t, h, without, "algorithm_floor")

	absent := atVersion(contract.VocabularyAlgorithmFloor, "edge-host.example.com")
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

			refused := atVersion(contract.VocabularyAlgorithmFloor, "edge-host.example.com")
			refused.Capabilities = upstreamCaps(t)
			requireCapabilityRefusal(t, h, refused, "algorithm_bans")

			other := atVersion(contract.VocabularyAlgorithmFloor, "edge-host.example.com")
			other.Capabilities = upstreamCaps(t)
			tc.widen(other.Capabilities)
			resp := h.mustAllow(t, other)
			if resp.AlgorithmBans.Empty() {
				t.Fatal("the ban was stripped from the answer")
			}

			unknown := atVersion(contract.VocabularyAlgorithmFloor, "edge-host.example.com")
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

	req := atVersion(contract.VocabularyAlgorithmFloor, "edge-host.example.com")
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

	changed := atVersion(contract.VocabularyAlgorithmFloor, "edge-host.example.com")
	changed.Capabilities = upstreamCaps(t)
	changed.Capabilities.AlgorithmFloors = changed.Capabilities.AlgorithmFloors[:1]
	h.mustAllow(t, changed)
	after, _ := h.store.ProxyAlgorithmDeclarations().List(ctx, testTenant)
	if after[0].Digest == first[0].Digest {
		t.Error("a changed build's declaration was not recorded")
	}
}

// republish activates a new policy after the harness has already answered, and
// moves the frozen clock past the program cache's refresh so the next call
// reads it.
func (h *harness) republish(t *testing.T, source string) {
	t.Helper()
	h.activate(t, source)
	h.clock = h.clock.Add(time.Second)
}
