// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The declarations Hoplock/proxy#69 and #72 added to the authorize request are
// read by nothing that fails when their keys are wrong. The south-bound decoder
// accepts unknown fields, so a key this package spelled differently would be
// dropped without an error, every floor check would read "no level declared"
// and every ban judgement "unknown".
//
// So this decodes a declaration as UPSTREAM renders it — testdata/
// upstream-capabilities.json is the proxy's own encoder's output at the
// vendored commit (AlgorithmFloorCapabilities, AlgorithmProfileCapabilities and
// OfferableAlgorithms in its internal/control), not a value built with this
// package's types — and requires every list to arrive non-empty.
func TestProxyCapabilitiesDecodeUpstreamShape(t *testing.T) {
	var req struct {
		Capabilities *ProxyCapabilities `json:"capabilities"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "upstream-capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	c := req.Capabilities
	if c == nil {
		t.Fatal("capabilities decoded to nil")
	}
	if len(c.AlgorithmFloors) == 0 {
		t.Fatal("algorithm_floors decoded empty")
	}
	for _, f := range c.AlgorithmFloors {
		if f.Level.Rank() < 1 || len(f.KeyExchanges) == 0 {
			t.Errorf("floor %+v decoded without a known level or its exchanges", f)
		}
	}
	if len(c.AlgorithmProfiles) == 0 {
		t.Fatal("algorithm_profiles decoded empty")
	}
	for _, p := range c.AlgorithmProfiles {
		for _, axis := range Axes {
			if len(p.Axis(axis)) == 0 {
				t.Errorf("profile %q decoded with no %s", p.Profile, axis)
			}
		}
	}
	for _, axis := range Axes {
		if len(c.Algorithms.Axis(axis)) == 0 {
			t.Errorf("algorithms decoded with no %s", axis)
		}
	}
}

func TestEmptyBansAreEmpty(t *testing.T) {
	var nilBans *AlgorithmBans
	if !nilBans.Empty() || !(&AlgorithmBans{}).Empty() || !(&AlgorithmBans{Ciphers: []string{}}).Empty() {
		t.Error("a ban with no identifier on any axis must read as empty")
	}
	if (&AlgorithmBans{MACs: []string{"hmac-sha1"}}).Empty() {
		t.Error("a ban naming an identifier is not empty")
	}
}

func TestFloorRanks(t *testing.T) {
	for floor, want := range map[AlgorithmFloor]int{"": 0, AlgorithmFloorModernKex: 1, AlgorithmFloorPQHybridKex: 2, "fips": -1} {
		if got := floor.Rank(); got != want {
			t.Errorf("%q.Rank() = %d, want %d", floor, got, want)
		}
	}
	for met, want := range map[FloorMet]int{FloorMetNone: 0, FloorMetModernKex: 1, FloorMetPQHybridKex: 2, "": -1} {
		if got := met.Rank(); got != want {
			t.Errorf("%q.Rank() = %d, want %d", met, got, want)
		}
	}
}

func TestSerialsRoundTripAboveTheFloatLimit(t *testing.T) {
	// 2^53 + 1 is the first integer a float64 cannot hold.
	for _, v := range []uint64{0, 10427, 1<<53 + 1, ^uint64(0)} {
		got, err := ParseSerial(FormatSerial(v))
		if err != nil || got != v {
			t.Errorf("serial %d round-tripped to %d (%v)", v, got, err)
		}
	}
	for _, bad := range []string{"", "-1", "1e3", "12.0", "18446744073709551616", " 1"} {
		if _, err := ParseSerial(bad); err == nil {
			t.Errorf("ParseSerial(%q) accepted a value that is not a decimal serial", bad)
		}
	}
}

// A floor-only or a profiles-only declaration names no rung, so it declares no
// rung.
func TestAnAlgorithmOnlyDeclarationDeclaresNoRung(t *testing.T) {
	floors := &ProxyCapabilities{AlgorithmFloors: []AlgorithmFloorCapability{{Level: AlgorithmFloorModernKex, KeyExchanges: []string{"curve25519-sha256"}}}}
	profiles := &ProxyCapabilities{AlgorithmProfiles: []AlgorithmProfileCapability{{Profile: AlgorithmProfileDefault}}}
	if floors.Declares() || profiles.Declares() {
		t.Error("an algorithm-only declaration read as a rung declaration")
	}
	if !(&ProxyCapabilities{Execution: []string{"no-interactive-shell"}}).Declares() {
		t.Error("a rung declaration did not declare")
	}
}
