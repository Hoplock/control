// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
)

// upstreamDeclaration is a declaration exactly as the proxy's own encoder
// renders it at the vendored commit (internal/contract/testdata). Every case
// below starts from it, so the judgement is held to a build's real lists and
// never to a list of this repository's.
func upstreamDeclaration(t *testing.T) *contract.ProxyCapabilities {
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

func offerOf(t *testing.T, decl *contract.ProxyCapabilities, profile contract.AlgorithmProfile) contract.AlgorithmProfileCapability {
	t.Helper()
	for _, p := range decl.AlgorithmProfiles {
		if p.Profile == profile {
			return p
		}
	}
	t.Fatalf("the fixture declares no %s profile", profile)
	return contract.AlgorithmProfileCapability{}
}

func emptied(j fleet.AlgorithmJudgement, axis string) bool {
	return slices.ContainsFunc(j.Emptied, func(e fleet.AxisEmptied) bool { return e.Axis == axis })
}

func TestAnEmptiedAxisIsJudgedFromTheDeclaration(t *testing.T) {
	decl := upstreamDeclaration(t)
	def := offerOf(t, decl, contract.AlgorithmProfileDefault)

	t.Run("a ciphers ban under default", func(t *testing.T) {
		j := fleet.JudgeAlgorithms(fleet.AlgorithmPolicy{
			Profile: contract.AlgorithmProfileDefault,
			Bans:    &contract.AlgorithmBans{Ciphers: slices.Clone(def.Ciphers)},
		}, decl)
		if !emptied(j, contract.AxisCiphers) || !j.Refused() {
			t.Fatalf("banning every default cipher was not judged an emptied axis: %+v", j)
		}
	})

	t.Run("a key-exchange ban removing every exchange the floor accepts", func(t *testing.T) {
		j := fleet.JudgeAlgorithms(fleet.AlgorithmPolicy{
			Floor: contract.AlgorithmFloorPQHybridKex,
			Bans:  &contract.AlgorithmBans{KeyExchanges: []string{"mlkem768x25519-sha256"}},
		}, decl)
		if !emptied(j, contract.AxisKeyExchanges) {
			t.Fatalf("banning the only pq-hybrid exchange was not judged an emptied axis: %+v", j)
		}
	})

	// THE CURVE25519 STEP, by name. Ban one spelling plus every other
	// exchange the route offers: plain set subtraction leaves the other
	// spelling, and the proxy — which cannot offer either alone — leaves
	// nothing.
	t.Run("curve25519 under one spelling", func(t *testing.T) {
		var bans []string
		for _, kex := range def.KeyExchanges {
			if kex != contract.KexCurve25519LibSSH {
				bans = append(bans, kex)
			}
		}
		if !slices.Contains(bans, contract.KexCurve25519) {
			t.Fatal("the fixture's default offer has no curve25519-sha256")
		}
		j := fleet.JudgeAlgorithms(fleet.AlgorithmPolicy{
			Bans: &contract.AlgorithmBans{KeyExchanges: bans},
		}, decl)
		if !emptied(j, contract.AxisKeyExchanges) {
			t.Fatalf("a ban on curve25519-sha256 and everything else left the libssh spelling standing: %+v", j)
		}
	})

	t.Run("a ban leaving something is not refused", func(t *testing.T) {
		j := fleet.JudgeAlgorithms(fleet.AlgorithmPolicy{
			Floor: contract.AlgorithmFloorModernKex,
			Bans:  &contract.AlgorithmBans{Ciphers: def.Ciphers[:len(def.Ciphers)-1], HostKeys: []string{"ssh-rsa"}},
		}, decl)
		if j.Refused() || j.Unknown {
			t.Fatalf("a ban leaving every axis something was judged %+v", j)
		}
	})

	// A route that names no profile is judged from the `default` entry,
	// because absent means default.
	t.Run("no profile reads default", func(t *testing.T) {
		j := fleet.JudgeAlgorithms(fleet.AlgorithmPolicy{
			Bans: &contract.AlgorithmBans{MACs: slices.Clone(def.MACs)},
		}, decl)
		if !emptied(j, contract.AxisMACs) {
			t.Fatalf("a route with no profile was not judged from default: %+v", j)
		}
	})
}

// The judgement reads the declaration and no copied list: change the
// declaration by one cipher and the answer changes with it, both ways.
func TestTheJudgementReadsTheDeclarationAndNoCopiedList(t *testing.T) {
	decl := upstreamDeclaration(t)
	def := offerOf(t, decl, contract.AlgorithmProfileDefault)
	keep := def.Ciphers[len(def.Ciphers)-1]
	allButOne := def.Ciphers[:len(def.Ciphers)-1]

	withCiphers := func(ciphers []string) *contract.ProxyCapabilities {
		out := *decl
		out.AlgorithmProfiles = slices.Clone(decl.AlgorithmProfiles)
		for i := range out.AlgorithmProfiles {
			if out.AlgorithmProfiles[i].Profile == contract.AlgorithmProfileDefault {
				out.AlgorithmProfiles[i].Ciphers = ciphers
			}
		}
		return &out
	}

	accepted := fleet.AlgorithmPolicy{Bans: &contract.AlgorithmBans{Ciphers: allButOne}}
	if j := fleet.JudgeAlgorithms(accepted, decl); j.Refused() {
		t.Fatalf("the fixture refused a ban leaving %s: %+v", keep, j)
	}
	fewer := withCiphers(slices.DeleteFunc(slices.Clone(def.Ciphers), func(c string) bool { return c == keep }))
	if j := fleet.JudgeAlgorithms(accepted, fewer); !emptied(j, contract.AxisCiphers) {
		t.Errorf("a build offering one cipher fewer still accepted the route: %+v", j)
	}

	refused := fleet.AlgorithmPolicy{Bans: &contract.AlgorithmBans{Ciphers: slices.Clone(def.Ciphers)}}
	if j := fleet.JudgeAlgorithms(refused, decl); !j.Refused() {
		t.Fatalf("the fixture accepted a ban on every cipher: %+v", j)
	}
	more := withCiphers(append(slices.Clone(def.Ciphers), "aes256-cbc"))
	if j := fleet.JudgeAlgorithms(refused, more); j.Refused() {
		t.Errorf("a build offering one cipher more still refused the route: %+v", j)
	}
}

// What cannot be judged is unknown — never a refusal, never a silent pass.
func TestAnUndeclaredProfileIsUnknown(t *testing.T) {
	decl := upstreamDeclaration(t)
	policy := fleet.AlgorithmPolicy{
		Profile: contract.AlgorithmProfileLegacyDevice,
		Bans:    &contract.AlgorithmBans{Ciphers: []string{"aes128-cbc"}},
	}

	noProfiles := *decl
	noProfiles.AlgorithmProfiles = nil
	if j := fleet.JudgeAlgorithms(policy, &noProfiles); !j.Unknown || j.Refused() {
		t.Errorf("a declaration with no algorithm_profiles was judged %+v, want unknown", j)
	}

	omitted := *decl
	omitted.AlgorithmProfiles = slices.DeleteFunc(slices.Clone(decl.AlgorithmProfiles),
		func(p contract.AlgorithmProfileCapability) bool {
			return p.Profile == contract.AlgorithmProfileLegacyDevice
		})
	if j := fleet.JudgeAlgorithms(policy, &omitted); !j.Unknown || j.Refused() {
		t.Errorf("a declaration omitting the route's profile was judged %+v, want unknown", j)
	}
	if j := fleet.JudgeAlgorithms(policy, nil); !j.Unknown {
		t.Errorf("an absent declaration was judged %+v, want unknown", j)
	}
}

func TestAFloorTheBuildDoesNotDeclareIsRefused(t *testing.T) {
	decl := upstreamDeclaration(t)
	noFloors := *decl
	noFloors.AlgorithmFloors = nil
	j := fleet.JudgeAlgorithms(fleet.AlgorithmPolicy{Floor: contract.AlgorithmFloorPQHybridKex}, &noFloors)
	if !j.FloorUndeclared || !j.Refused() {
		t.Fatalf("an undeclared level was judged %+v", j)
	}
	if fleet.DeclaresFloor(nil, contract.AlgorithmFloorModernKex) {
		t.Error("an absent declaration declared a floor")
	}
	if !fleet.DeclaresFloor(decl, contract.AlgorithmFloorPQHybridKex) {
		t.Error("the fixture's pq-hybrid-kex level was not found")
	}
}

func TestABanNoBuildOffersIsReportedAsUnmatched(t *testing.T) {
	decl := upstreamDeclaration(t)
	got := fleet.UnmatchedBans(&contract.AlgorithmBans{
		KeyExchanges: []string{"curve25519-sha256@libssh.org", "diffie-hellman-group1-sha1x"},
		Ciphers:      []string{"aes128-cbc"},
	}, []*contract.ProxyCapabilities{decl})
	if !slices.Equal(got[contract.AxisKeyExchanges], []string{"diffie-hellman-group1-sha1x"}) {
		t.Errorf("key exchanges unmatched = %v, want only the typo", got[contract.AxisKeyExchanges])
	}
	if len(got[contract.AxisCiphers]) != 0 {
		t.Errorf("aes128-cbc is offered by legacy-device and was reported unmatched: %v", got)
	}
}
