// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
)

// A PAIR OF ASSERTIONS THAT CANNOT FAIL IS A PAIR THAT GRADES NOTHING.
//
// The heartbeat obligation is two obligations, and the failure mode these
// tests exist for is a suite that reports both as met against a server that
// meets neither. So each case below is run against a deliberately wrong server
// and the test asserts that the suite FAILS it — which is the only way to know
// the assertions are load-bearing.
//
// The fake is a stream and nothing else: the suite's other groups are not run
// here, and the expectation file is the minimum that makes the event cases
// gradeable.

// fakeControl serves just enough of the contract to grade the event cases.
type fakeControl struct {
	// interval is how often it writes a heartbeat.
	interval time.Duration
	// advertise is what it claims on the wire, in seconds. Zero advertises
	// nothing, which is a legal answer from a server that predates the
	// field.
	advertise int32
	// stall stops the writer after the first heartbeat, which is the shape
	// of a server that has degraded the whole fleet to uncached without
	// saying so.
	stall bool

	mu  sync.Mutex
	seq int
}

func (f *fakeControl) nextID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	return fmt.Sprintf("evt-%08d", f.seq)
}

func (f *fakeControl) event(t contract.EventType) *contract.RevocationEvent {
	return &contract.RevocationEvent{
		EventID:                  f.nextID(),
		Type:                     t,
		Timestamp:                time.Now().UTC().Format(time.RFC3339),
		HeartbeatIntervalSeconds: f.advertise,
	}
}

func (f *fakeControl) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+contract.PathProxyEvents, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contract.MediaTypeNDJSON)
		w.WriteHeader(http.StatusOK)
		ctrl := http.NewResponseController(w)
		_ = ctrl.Flush()

		write := func(ev *contract.RevocationEvent) bool {
			line, err := json.Marshal(ev)
			if err != nil {
				return false
			}
			if _, err := w.Write(append(line, '\n')); err != nil {
				return false
			}
			return ctrl.Flush() == nil
		}

		if !write(f.event(contract.EventTypeHeartbeat)) {
			return
		}
		if f.stall {
			// Held open and silent, which is exactly what the proxy
			// cannot tell from a healthy idle stream.
			<-r.Context().Done()
			return
		}
		ticker := time.NewTicker(f.interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if !write(f.event(contract.EventTypeHeartbeat)) {
					return
				}
			}
		}
	})
	return mux
}

// runEventChecks points the suite at a fake and returns the graded results.
func runEventChecks(t *testing.T, f *fakeControl, fallbackSeconds int) map[string]Result {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)

	e := &Expectations{}
	e.Events.ProxyID = "proxy-1"
	e.Events.HeartbeatIntervalSeconds = fallbackSeconds
	e.Events.PublishURL = srv.URL + "/debug/revoke"

	s := NewSuite(srv.URL, "", e, 5*time.Second)
	s.checkHeartbeats()

	out := make(map[string]Result, len(s.results))
	for _, r := range s.results {
		out[r.Name] = r
	}
	return out
}

func result(t *testing.T, results map[string]Result, contains string) Result {
	t.Helper()
	for name, r := range results {
		if strings.Contains(name, contains) {
			return r
		}
	}
	t.Fatalf("no case whose name contains %q ran; the suite graded %d cases", contains, len(results))
	return Result{}
}

const (
	caseCeiling = "within the contract's ceiling"
	caseKept    = "arrive within the interval the server advertises"
)

// A conformant server passes both halves.
func TestTheHeartbeatCasesPassAConformantServer(t *testing.T) {
	t.Parallel()
	results := runEventChecks(t, &fakeControl{interval: 100 * time.Millisecond, advertise: 1}, 5)

	for _, name := range []string{caseCeiling, caseKept} {
		if r := result(t, results, name); r.Status != StatusPass {
			t.Errorf("%q failed against a conformant server: %v", r.Name, r.Details)
		}
	}
}

// A server that advertises 600s and honestly keeps to it passes its own claim
// and breaks every proxy in the fleet. The ceiling case is the one that has to
// catch it, and nothing else will.
func TestTheCeilingCaseFailsAServerAdvertisingPastIt(t *testing.T) {
	t.Parallel()
	results := runEventChecks(t, &fakeControl{interval: 100 * time.Millisecond, advertise: 600}, 5)

	if r := result(t, results, caseCeiling); r.Status != StatusFail {
		t.Fatalf("the ceiling case passed a server advertising 600s: %v", r.Details)
	}
	// And the other half passes, because the server IS keeping the interval
	// it named. That is the point of grading them separately.
	if r := result(t, results, caseKept); r.Status != StatusPass {
		t.Errorf("the kept-interval case failed a server that keeps its claim: %v", r.Details)
	}
}

// A writer that stalls leaves a stream that is alive and silent, which is what
// a proxy cannot distinguish from a healthy idle one.
func TestTheKeptIntervalCaseFailsAStalledWriter(t *testing.T) {
	t.Parallel()
	results := runEventChecks(t, &fakeControl{interval: 100 * time.Millisecond, advertise: 1, stall: true}, 5)

	if r := result(t, results, caseKept); r.Status != StatusFail {
		t.Fatalf("the kept-interval case passed a server whose heartbeat writer stalled: %v", r.Details)
	}
}

// Absent is a legal answer, and the file is what makes such a server
// gradeable.
func TestAServerThatAdvertisesNothingIsGradedFromTheFile(t *testing.T) {
	t.Parallel()
	results := runEventChecks(t, &fakeControl{interval: 100 * time.Millisecond}, 5)

	for _, name := range []string{caseCeiling, caseKept} {
		r := result(t, results, name)
		if r.Status != StatusPass {
			t.Errorf("%q failed a server that advertises nothing: %v", r.Name, r.Details)
		}
		if !strings.Contains(strings.Join(r.Details, " "), "advertises none") {
			t.Errorf("%q does not say it fell back to the file: %v", r.Name, r.Details)
		}
	}
}

// And a server that advertises nothing, against a file with no fallback, is
// UNGRADEABLE — so it fails. A vacuous pass is worse than a failure.
func TestAServerThatAdvertisesNothingWithNoFallbackIsUngradeable(t *testing.T) {
	t.Parallel()
	results := runEventChecks(t, &fakeControl{interval: 100 * time.Millisecond}, 0)

	for _, name := range []string{caseCeiling, caseKept} {
		r := result(t, results, name)
		if r.Status != StatusFail {
			t.Errorf("%q passed although nothing named an interval to grade against: %v", r.Name, r.Details)
		}
	}
}

// The bound resolver on its own: the claim wins over the file, and the file is
// only reached when there is no claim.
func TestHeartbeatBoundPrefersTheClaimOverTheFile(t *testing.T) {
	t.Parallel()

	bound, from, err := heartbeatBound(5*time.Second, true, 60)
	if err != nil || bound != 5*time.Second {
		t.Fatalf("heartbeatBound(5s advertised, 60s in the file) = (%s, %v)", bound, err)
	}
	if !strings.Contains(from, "advertises") {
		t.Errorf("the bound does not say it came off the stream: %q", from)
	}

	bound, from, err = heartbeatBound(0, false, 7)
	if err != nil || bound != 7*time.Second {
		t.Fatalf("heartbeatBound(nothing advertised, 7s in the file) = (%s, %v)", bound, err)
	}
	if !strings.Contains(from, "advertises none") {
		t.Errorf("the bound does not say it fell back to the file: %q", from)
	}

	if _, _, err := heartbeatBound(0, false, 0); err == nil {
		t.Fatal("a server advertising nothing with no fallback was graded rather than refused")
	}
}

func TestHeartbeatLatenessAllowsSchedulingButNotAStall(t *testing.T) {
	t.Parallel()

	if heartbeatLate(time.Second+250*time.Millisecond, time.Second) {
		t.Error("a heartbeat a quarter of a second late was called a stall")
	}
	if !heartbeatLate(30*time.Second, time.Second) {
		t.Error("a heartbeat thirty times its interval late was not called a stall")
	}
	if !withinCeiling(contract.MaxHeartbeatInterval) {
		t.Error("the ceiling itself is not within the ceiling")
	}
	if withinCeiling(contract.MaxHeartbeatInterval + time.Second) {
		t.Error("an interval past the ceiling was accepted")
	}
}
