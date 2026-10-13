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
