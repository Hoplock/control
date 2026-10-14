// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/hoplock/control/internal/fleet"
)

// fleetOwnedKeyPattern matches a dotted key, and nothing else, in backticks.
// The same description backticks VALUES such as "30s" and "5m" (the duration
// examples), so a loose "anything in backticks" match would pick up non-keys.
var fleetOwnedKeyPattern = regexp.MustCompile("`([a-z_]+(?:\\.[a-z_]+)+)`")

// extractFleetOwned reads the backticked dotted keys out of a description.
func extractFleetOwned(description string) []string {
	var out []string
	for _, m := range fleetOwnedKeyPattern.FindAllStringSubmatch(description, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	slices.Sort(out)
	return out
}

func settingsDescription(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "contract", "control.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Description string `yaml:"description"`
				} `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	d := doc.Components.Schemas["ProxyConfigDocument"].Properties["settings"].Description
	if d == "" {
		t.Fatal("the vendored contract has no ProxyConfigDocument.settings description")
	}
	return d
}

// The fleet-owned list is held in ONE place, and this ties it to the vendored
// document: a `make contract-sync` that adds or removes a key fails here rather
// than slipping past. The extraction is strict and its count is asserted, so a
// description reworded into a shape the pattern misses fails rather than
// producing an empty list that trivially agrees.
func TestTheFleetOwnedKeysAreTheContracts(t *testing.T) {
	got := extractFleetOwned(settingsDescription(t))
	if len(got) != 17 {
		t.Fatalf("extracted %d keys from the contract, want D18's 17: %v", len(got), got)
	}
	if want := fleet.FleetOwnedKeys(); !slices.Equal(got, want) {
		t.Fatalf("the contract lists %v\nthis server publishes %v", got, want)
	}
}

// Proved, not assumed: the extraction ignores the backticked duration values,
// fails a count on a description that lost a key, and disagrees with the list
// when the contract gains one.
func TestTheFleetOwnedKeyTestCatchesDrift(t *testing.T) {
	d := settingsDescription(t)
	for _, v := range []string{"30s", "5m"} {
		if strings.Contains(d, "`\""+v+"\"`") || strings.Contains(d, "\""+v+"\"") {
			if slices.Contains(extractFleetOwned(d), v) {
				t.Errorf("the extraction picked up the value %q", v)
			}
		}
	}
	lost := strings.Replace(d, "`chain.max_hops`", "chain.max_hops", 1)
	if n := len(extractFleetOwned(lost)); n == 17 {
		t.Error("a description that lost a backticked key still extracted 17")
	}
	gained := strings.Replace(d, "`chain.max_hops`", "`chain.max_hops`, `chain.extra_setting`", 1)
	if slices.Equal(extractFleetOwned(gained), fleet.FleetOwnedKeys()) {
		t.Error("a contract with one more key agreed with this server's list")
	}
}

func TestAPublishNamingAnythingButAFleetOwnedKeyIsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		doc  fleet.ConfigDocument
		code string
		key  string
	}{
		"bootstrap-only":     {fleet.ConfigDocument{"control.cache.stale_after": "5m"}, fleet.CodeConfigKeyNotFleetOwned, "control.cache.stale_after"},
		"unknown":            {fleet.ConfigDocument{"proxy.id": "p-2"}, fleet.CodeConfigKeyNotFleetOwned, "proxy.id"},
		"a nested object":    {fleet.ConfigDocument{"logging": map[string]any{"batch_size": 128}}, fleet.CodeConfigKeyNotFleetOwned, "logging"},
		"a duration as int":  {fleet.ConfigDocument{"control.cache.max_ttl": 300}, fleet.CodeConfigValueInvalid, "control.cache.max_ttl"},
		"a count as string":  {fleet.ConfigDocument{"logging.batch_size": "128"}, fleet.CodeConfigValueInvalid, "logging.batch_size"},
		"a fractional count": {fleet.ConfigDocument{"chain.max_hops": 2.5}, fleet.CodeConfigValueInvalid, "chain.max_hops"},
	} {
		err := fleet.ValidateConfigDocument(tc.doc)
		var ke *fleet.ConfigKeyError
		if !asKeyError(err, &ke) || ke.Code != tc.code || ke.Key != tc.key {
			t.Errorf("%s: %v, want %s naming %s", name, err, tc.code, tc.key)
		}
	}
	if err := fleet.ValidateConfigDocument(fleet.ConfigDocument{
		"control.cache.max_ttl": "5m", "logging.batch_size": float64(128), "chain.upstream.address": "edge:2222",
	}); err != nil {
		t.Errorf("a valid document was refused: %v", err)
	}
}

func asKeyError(err error, target **fleet.ConfigKeyError) bool {
	ke, ok := err.(*fleet.ConfigKeyError)
	if ok {
		*target = ke
	}
	return ok
}

func TestTheWireRenderingRoundTrips(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	if v, ok := fleet.ParseWireConfigVersion(fleet.WireConfigVersion(42)); !ok || v != 42 {
		t.Errorf("version round trip = %d %v", v, ok)
	}
	for _, bad := range []string{"", "42", "v0", "v-1", "v04", "zone-eu/v42"} {
		if _, ok := fleet.ParseWireConfigVersion(bad); ok {
			t.Errorf("%q parsed as one of this server's versions", bad)
		}
	}
	if h, ok := fleet.ParseWireConfigHash(fleet.WireConfigHash(hash)); !ok || h != hash {
		t.Errorf("hash round trip = %q %v", h, ok)
	}
	for _, header := range []string{fleet.ConfigETag(hash), "sha256:" + hash, `W/"sha256:` + hash + `"`, `"x", "sha256:` + hash + `"`, "*"} {
		if !fleet.IfNoneMatchHolds(header, hash) {
			t.Errorf("If-None-Match %q did not hold", header)
		}
	}
	if fleet.IfNoneMatchHolds(`"sha256:`+strings.Repeat("cd", 32)+`"`, hash) {
		t.Error("another hash held")
	}
}
