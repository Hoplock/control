// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// The runner's own two promises: a failing assertion does not take the rest of
// the run with it, and a failing run exits non-zero.

func TestACaseThatAbortsDoesNotStopTheRun(t *testing.T) {
	s := &Suite{}
	s.run("g", "aborts", func(c *Case) {
		c.must(false, "stop here")
		c.require(false, "this line must never run")
	})
	s.run("g", "passes", func(c *Case) {})

	if len(s.results) != 2 {
		t.Fatalf("want 2 results, got %d", len(s.results))
	}
	if s.results[0].Status != StatusFail {
		t.Errorf("aborting case reported %s", s.results[0].Status)
	}
	if len(s.results[0].Details) != 1 {
		t.Errorf("aborting case kept running: %v", s.results[0].Details)
	}
	if s.results[1].Status != StatusPass {
		t.Errorf("the case after an abort reported %s", s.results[1].Status)
	}
}

func TestRequireCollectsEveryFailureInACase(t *testing.T) {
	s := &Suite{}
	s.run("g", "three problems", func(c *Case) {
		c.require(false, "first")
		c.require(false, "second")
		c.require(true, "not reported")
		c.require(false, "third")
	})
	if got := len(s.results[0].Details); got != 3 {
		t.Errorf("want all 3 failures reported in one pass, got %d: %v", got, s.results[0].Details)
	}
}

func TestAPanicThatIsNotAnAbortStillPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("a real panic was swallowed as if it were a controlled abort")
		}
	}()
	s := &Suite{}
	s.run("g", "boom", func(c *Case) { panic(errors.New("boom")) })
}

func TestReportSaysWhetherAnythingFailed(t *testing.T) {
	s := &Suite{}
	s.run("g", "ok", func(c *Case) {})
	var buf bytes.Buffer
	if !s.Report(&buf, false) {
		t.Error("an all-passing run reported failure")
	}

	s.run("g", "bad", func(c *Case) { c.require(false, "the detail") })
	buf.Reset()
	if s.Report(&buf, false) {
		t.Error("a run with a failure reported success")
	}
	if !strings.Contains(buf.String(), "the detail") {
		t.Errorf("a failure's detail was not printed:\n%s", buf.String())
	}
}

func TestResponseHasDistinguishesAbsentFromZero(t *testing.T) {
	// Every absent-value-default assertion rests on this: a decoded struct
	// cannot tell `require_session_capture: false` from an omitted key, and
	// those are the two readings the contract insists are different.
	r := &response{fields: map[string]any{"require_session_capture": false}}
	if !r.has("require_session_capture") {
		t.Error("a key present on the wire with a zero value read as absent")
	}
	if r.has("enforcement") {
		t.Error("a key that is not on the wire read as present")
	}
	if (&response{}).has("anything") {
		t.Error("a response with no decoded body reported a key as present")
	}
}
