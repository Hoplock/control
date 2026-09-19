// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The expectation file is the suite's only description of the server in front
// of it, so a file that loads but says nothing is a run that grades nothing.
// These tests are about that failure mode rather than about YAML.

func TestLoadExpectationsAcceptsTheMockFile(t *testing.T) {
	e, err := LoadExpectations(filepath.Join("testdata", "mock-expectations.yaml"))
	if err != nil {
		t.Fatalf("the checked-in mock expectations do not load: %v", err)
	}
	if e.ProxyID == "" || e.SecondProxyID == "" {
		t.Error("mock expectations name no proxy ids")
	}
	if e.ProxyID == e.SecondProxyID {
		t.Error("second_proxy_id equals proxy_id, so exclusivity is never asserted across two proxies")
	}
}

func TestLoadExpectationsRejectsAnUnknownKey(t *testing.T) {
	// Strict decoding, because a typo would otherwise silently disable a case
	// and a suite whose cases can be disabled by a typo grades nothing.
	path := writeTemp(t, "proxy_id: p\nsecond_proxyid: q\n")
	_, err := LoadExpectations(path)
	if err == nil {
		t.Fatal("a misspelled key was accepted")
	}
	if !strings.Contains(err.Error(), "second_proxyid") {
		t.Errorf("error does not name the offending key: %v", err)
	}
}

func TestLoadExpectationsNamesEveryMissingInput(t *testing.T) {
	// All of them at once: a loader that reports the first problem makes
	// writing an expectation file for a new server a guessing game.
	path := writeTemp(t, "proxy_id: p\n")
	_, err := LoadExpectations(path)
	if err == nil {
		t.Fatal("an empty expectation file was accepted")
	}
	for _, want := range []string{
		"second_proxy_id",
		"auth.cert_accept.login",
		"authorize.direct.target",
		"authorize.defaults.enforcement.default.target",
		"authorize.device_fields.with_fields.target",
		"uids.exhaustion.target",
		"logs.read_url",
		"events.publish_url",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name the missing %s: %v", want, err)
		}
	}
}

func TestDefaultsFillOnlyWhatTheSuiteCanAnswerFor(t *testing.T) {
	e, err := LoadExpectations(filepath.Join("testdata", "mock-expectations.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	e.Logs.BatchSize = 0
	e.UIDs.AbandonedLeases = 0
	e.defaults()

	if e.Logs.BatchSize <= 0 {
		t.Error("batch_size was left at zero, so the replay case would send nothing")
	}
	if e.UIDs.AbandonedLeases <= 0 {
		t.Error("abandoned_leases was left at zero, so no block is ever abandoned and the invariant is under-graded")
	}
}

// The suite must never lease twice against the same target name in two runs:
// the cursor only ever advances, so the second run would grade nothing.
func TestUIDTargetsAreUniquePerRun(t *testing.T) {
	e, err := LoadExpectations(filepath.Join("testdata", "mock-expectations.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a := NewSuite("http://example.invalid", "", e, 0)
	b := NewSuite("http://example.invalid", "", e, 0)

	if a.runID == b.runID {
		t.Skip("two suites built inside the same clock tick; the run id is time-derived")
	}
	if a.uidTarget("uid-exhaust.company.com") == b.uidTarget("uid-exhaust.company.com") {
		t.Error("two runs lease against the same target name, so the second grades an already-exhausted cursor")
	}
	if got := a.uidTarget("uid-exhaust.company.com"); !strings.HasSuffix(got, ".company.com") {
		t.Errorf("uidTarget(%q) = %q, which is no longer a name in the server's own domain", "uid-exhaust.company.com", got)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "expectations.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp expectations: %v", err)
	}
	return path
}
