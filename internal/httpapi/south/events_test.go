// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/httpapi/south"
	"github.com/hoplock/control/internal/revoke"
)

// `GET /v1/proxies/{proxy_id}/events` end to end, over a real connection.
//
// These cases need a SERVER rather than a recorder, and that is not incidental:
// what is being graded is that the response never ends, that every line is
// flushed rather than buffered, and that the chain's request timeout does not
// cut it. A `httptest.ResponseRecorder` has no connection to flush to and no
// deadline to survive, so it would grade none of those.

// eventStream is one open subscription the test reads from.
type eventStream struct {
	t      *testing.T
	cancel context.CancelFunc
	body   io.ReadCloser
	lines  *bufio.Scanner
	events chan *contract.RevocationEvent
	fail   chan error
}

func (h *harness) subscribe(t *testing.T, base, proxyID, lastEventID, token string) (*eventStream, int) {
	t.Helper()
	u := base + "/v1/proxies/" + url.PathEscape(proxyID) + "/events"
	if lastEventID != "" {
		u += "?last_event_id=" + url.QueryEscape(lastEventID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		cancel()
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// No client timeout: the stream is supposed to outlive one.
	resp, err := (&http.Client{}).Do(req) //nolint:bodyclose // closed by eventStream.close
	if err != nil {
		cancel()
		t.Fatalf("subscribe: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		cancel()
		return nil, resp.StatusCode
	}
	if ct := resp.Header.Get("Content-Type"); ct != contract.MediaTypeNDJSON {
		t.Errorf("content type = %q, want %q", ct, contract.MediaTypeNDJSON)
	}

	st := &eventStream{
		t:      t,
		cancel: cancel,
		body:   resp.Body,
		lines:  bufio.NewScanner(resp.Body),
		events: make(chan *contract.RevocationEvent, 128),
		fail:   make(chan error, 1),
	}
	go st.read()
	t.Cleanup(st.close)
	return st, resp.StatusCode
}

func (st *eventStream) read() {
	for st.lines.Scan() {
		line := bytes.TrimSpace(st.lines.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev contract.RevocationEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			st.fail <- fmt.Errorf("a stream line is not a RevocationEvent: %s", line)
			return
		}
		st.events <- &ev
	}
	st.fail <- io.EOF
}

func (st *eventStream) close() {
	if st == nil {
		return
	}
	st.cancel()
	_ = st.body.Close()
}

// nextLine returns the next event of any type.
func (st *eventStream) nextLine(d time.Duration) *contract.RevocationEvent {
	st.t.Helper()
	select {
	case ev := <-st.events:
		return ev
	case err := <-st.fail:
		st.t.Fatalf("the stream ended: %v", err)
	case <-time.After(d):
		st.t.Fatalf("no line inside %s", d)
	}
	return nil
}

// next returns the next event that is not a heartbeat.
func (st *eventStream) next(d time.Duration) *contract.RevocationEvent {
	st.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ev := st.nextLine(time.Until(deadline)); ev.Type != contract.EventTypeHeartbeat {
			return ev
		}
	}
	st.t.Fatalf("only heartbeats arrived inside %s", d)
	return nil
}

func (h *harness) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(h.server)
	t.Cleanup(srv.Close)
	return srv.URL
}

// waitForSubscription blocks until the broker has registered a proxy's stream.
func (h *harness) waitForSubscription(t *testing.T, proxyID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		live, err := h.bus.LiveSubscriptions(context.Background(), testTenant)
		if err == nil {
			if _, ok := live[proxyID]; ok {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the broker never registered a subscription for %s", proxyID)
}

// ---------------------------------------------------------------------------
// the stream itself
// ---------------------------------------------------------------------------

// The stream is NDJSON, it carries heartbeats, and every heartbeat says what
// interval this server is keeping. The bound it is graded against is that
// claim rather than a number this test chose — which is the whole of upstream
// Hoplock/proxy#56.
func TestTheStreamHeartbeatsWithinTheIntervalItAdvertises(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	base := h.serve(t)

	st, code := h.subscribe(t, base, "proxy-1", "", h.token())
	if code != http.StatusOK {
		t.Fatalf("subscribe answered %d", code)
	}

	first := st.nextLine(3 * time.Second)
	if first.Type != contract.EventTypeHeartbeat {
		t.Fatalf("the first line of an idle fresh subscription was %s, want a heartbeat", first.Type)
	}
	advertised, ok := first.AdvertisedHeartbeatInterval()
	if !ok {
		t.Fatal("a heartbeat carried no heartbeat_interval_seconds, so the proxy's bound is a number nobody stated")
	}
	if advertised > contract.MaxHeartbeatInterval {
		t.Fatalf("this server advertises %s, past the contract's ceiling of %s: two consecutive intervals "+
			"must fit inside the proxy's 20s reconnect timeout", advertised, contract.MaxHeartbeatInterval)
	}
	if first.EventID == "" || first.Timestamp == "" {
		t.Fatalf("a heartbeat with no event_id or timestamp: %+v", first)
	}

	// The claim is kept: the next heartbeat arrives inside the interval
	// this server just named.
	start := time.Now()
	second := st.nextLine(advertised + 2*time.Second)
	if second.Type != contract.EventTypeHeartbeat {
		t.Fatalf("expected a second heartbeat, got %s", second.Type)
	}
	if gap := time.Since(start); gap > advertised {
		t.Fatalf("the next heartbeat took %s, past the %s this server advertised", gap, advertised)
	}
}

// A line that only arrives when a buffer fills is a kill switch that only
// arrives when the next event does. A single event on an otherwise idle
// stream is what proves it is flushed.
func TestAPublishedEventReachesAnOpenSubscription(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	base := h.serve(t)

	st, _ := h.subscribe(t, base, "proxy-1", "", h.token())
	h.waitForSubscription(t, "proxy-1")

	if _, err := h.bus.Kill(context.Background(), testTenant, revoke.ToProxy("proxy-1"), revoke.Kill{
		Subject: "alice@example.com",
		Reason:  "Access withdrawn by the security team; contact #security.",
	}); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	got := st.next(3 * time.Second)
	if got.Type != contract.EventTypeSessionKill {
		t.Fatalf("got %s, want session_kill", got.Type)
	}
	if got.SessionKill.Subject != "alice@example.com" {
		t.Fatalf("session_kill = %+v", got.SessionKill)
	}
	if got.SessionKill.Reason == "" {
		t.Fatal("the kill carried no reason, so the user sees a session that looks like a crash")
	}
}

// Gap recovery, over the wire: an event published while nobody was listening
// is replayed to the reconnecting subscriber and nothing is skipped.
func TestReconnectingWithALastEventIDReplaysTheGap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	base := h.serve(t)

	st, _ := h.subscribe(t, base, "proxy-1", "", h.token())
	h.waitForSubscription(t, "proxy-1")

	rcpt, err := h.bus.Invalidate(context.Background(), testTenant, revoke.Everyone(), revoke.Invalidation{Keys: []string{"k1"}})
	if err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	processed := st.next(3 * time.Second)
	if processed.EventID != rcpt.EventID {
		t.Fatalf("delivered %s, want %s", processed.EventID, rcpt.EventID)
	}
	st.close()

	// Into the gap.
	missed, err := h.bus.Invalidate(context.Background(), testTenant, revoke.Everyone(), revoke.Invalidation{Keys: []string{"k2"}})
	if err != nil {
		t.Fatalf("Invalidate: %v", err)
	}

	resumed, code := h.subscribe(t, base, "proxy-1", processed.EventID, h.token())
	if code != http.StatusOK {
		t.Fatalf("resubscribe answered %d", code)
	}
	replayed := resumed.next(3 * time.Second)
	if replayed.Type == contract.EventTypeResync {
		t.Fatal("resynced although the missed event was still in the replay buffer")
	}
	if replayed.EventID != missed.EventID {
		t.Fatalf("replayed %s, want the event published into the gap (%s)", replayed.EventID, missed.EventID)
	}
}

// And an id that cannot be replayed from is answered with `resync` as the
// FIRST line: the proxy is told it has missed events it cannot be given, and
// nothing older is handed to it beside that.
func TestReconnectingWithAnUnknownIDResyncsFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	base := h.serve(t)

	st, code := h.subscribe(t, base, "proxy-1", "evt-00000000-000000000042", h.token())
	if code != http.StatusOK {
		t.Fatalf("subscribe answered %d", code)
	}
	first := st.nextLine(3 * time.Second)
	if first.Type != contract.EventTypeResync {
		t.Fatalf("the first line was %s, want resync: the id names a position this server does not have", first.Type)
	}
}

// A subscription outlives the chain's request timeout. Without the exemption
// this stream would be cut every ten seconds and the fleet would spend its
// life reconnecting.
func TestTheStreamOutlivesTheRequestTimeout(t *testing.T) {
	t.Parallel()
	h := newHarnessWith(t, func(o *south.Options) {
		// Short enough to fire inside a test, and long enough that a
		// non-streaming request still completes under it.
		o.RequestTimeout = 250 * time.Millisecond
	})
	base := h.serve(t)

	st, _ := h.subscribe(t, base, "proxy-1", "", h.token())
	deadline := time.After(time.Second)
	beats := 0
	for {
		select {
		case <-st.events:
			beats++
			if beats >= 4 {
				return
			}
		case err := <-st.fail:
			t.Fatalf("the stream was cut after %d heartbeats: %v — the request timeout is not exempted", beats, err)
		case <-deadline:
			if beats < 2 {
				t.Fatalf("only %d heartbeats arrived in a second", beats)
			}
			return
		}
	}
}

// ---------------------------------------------------------------------------
// who may subscribe
// ---------------------------------------------------------------------------

func TestSubscribingRequiresTheProxyCredential(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	base := h.serve(t)

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"no credential", ""},
		{"a credential this server did not issue", testTenant.String() + ".not-the-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, code := h.subscribe(t, base, "proxy-1", "", tc.token)
			if code != http.StatusUnauthorized {
				t.Fatalf("subscribe answered %d, want 401", code)
			}
		})
	}
}

// A token issued to one proxy may not read another's kill switch: it would
// learn which of its neighbours' sessions are being ended.
func TestABoundTokenMaySubscribeOnlyToItsOwnStream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	base := h.serve(t)

	token, _, err := h.fleet.IssueProxyToken(t.Context(), testTenant, "proxy-2", "bound", time.Time{})
	if err != nil {
		t.Fatalf("IssueProxyToken: %v", err)
	}
	bound := token.String()

	if _, code := h.subscribe(t, base, "proxy-2", "", bound); code != http.StatusOK {
		t.Fatalf("a bound token was refused its own stream: %d", code)
	}
	if _, code := h.subscribe(t, base, "proxy-1", "", bound); code != http.StatusUnauthorized {
		t.Fatalf("a token bound to proxy-2 subscribed to proxy-1's stream: %d", code)
	}
}

// ---------------------------------------------------------------------------
// what the stream turns on: cache hints (M9, PLAN §5.4)
// ---------------------------------------------------------------------------

// The M9 rule end to end on the host-key response: no subscription, no hint —
// and the hint appears as soon as the stream that could withdraw it exists.
func TestAHostKeyHintIsIssuedOnlyWhileTheStreamIsHealthy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	base := h.serve(t)

	const target = "hinted.example.com"
	const fingerprint = "SHA256:hinted-key"

	// A first sighting is never reused however it is hinted, so this one
	// gets no hint for a second reason as well.
	h.reportHostKey(t, target, fingerprint)

	var got contract.HostKeyReportResponse
	decode(t, h.reportHostKey(t, target, fingerprint), &got)
	if got.Cache != nil {
		t.Fatalf("cache = %+v, want none: proxy-1 holds no event subscription, so nothing could withdraw it", got.Cache)
	}

	_, _ = h.subscribe(t, base, "proxy-1", "", h.token())
	h.waitForSubscription(t, "proxy-1")

	decode(t, h.reportHostKey(t, target, fingerprint), &got)
	if !got.Cache.Cacheable() {
		t.Fatalf("cache = %+v, want a usable hint now that the stream is open", got.Cache)
	}
	if got.Cache.Key == "" {
		t.Fatal("a hint with a TTL and no key is one no revocation can name")
	}
}

// A first sighting and a key this server has not ruled on are never hinted:
// reusing a first sighting would replay trust-on-first-use into the audit log
// for every later connection.
func TestAFirstSightingIsNeverHinted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	base := h.serve(t)
	_, _ = h.subscribe(t, base, "proxy-1", "", h.token())
	h.waitForSubscription(t, "proxy-1")

	var got contract.HostKeyReportResponse
	decode(t, h.reportHostKey(t, "first.example.com", "SHA256:never-seen"), &got)
	if got.Known {
		t.Fatal("a key nobody has reported answered known: true")
	}
	if got.Cache != nil {
		t.Fatalf("cache = %+v, want none on a first sighting", got.Cache)
	}
}

// The asymmetry PLAN §5.4 names, end to end: the decision is withdrawn by
// publishing its OWN key, and the same withdrawal addressed by `subject` is
// not reported as having covered it.
func TestAHostKeyDecisionIsWithdrawnByItsOwnKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	base := h.serve(t)

	const target = "withdraw.example.com"
	const fingerprint = "SHA256:withdraw-key"

	st, _ := h.subscribe(t, base, "proxy-1", "", h.token())
	h.waitForSubscription(t, "proxy-1")

	h.reportHostKey(t, target, fingerprint)
	var got contract.HostKeyReportResponse
	decode(t, h.reportHostKey(t, target, fingerprint), &got)
	if !got.Cache.Cacheable() {
		t.Fatalf("cache = %+v, want a hint to withdraw", got.Cache)
	}
	issued := got.Cache.Key

	op := revoke.NewOperator(h.bus, h.fleet)
	ctx := context.Background()

	// Addressed by subject: it reaches the stream, and it says plainly
	// that it did not cover a host-key decision. An operator who read it
	// as having done so would have been misled by this server.
	bySubject, err := op.Invalidate(ctx, testTenant, revoke.Everyone(), revoke.Invalidation{Subject: "alice@example.com"})
	if err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if bySubject.CoversHostKeyDecisions {
		t.Fatal("a subject-scoped invalidation claimed to cover a host-key decision, which is not made for a person")
	}
	delivered := st.next(3 * time.Second)
	if delivered.CacheInvalidate.Subject != "alice@example.com" {
		t.Fatalf("delivered %+v", delivered.CacheInvalidate)
	}

	// Addressed as the decision itself: the key the proxy holds.
	byKey, err := op.WithdrawHostKey(ctx, testTenant, revoke.Everyone(), revoke.HostKeyRef{
		Target: target, Fingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("WithdrawHostKey: %v", err)
	}
	if !byKey.CoversHostKeyDecisions {
		t.Fatal("withdrawing by key did not report that it covered a host-key decision")
	}
	withdrawn := st.next(3 * time.Second)
	if withdrawn.Type != contract.EventTypeCacheInvalidate {
		t.Fatalf("got %s, want cache_invalidate", withdrawn.Type)
	}
	if len(withdrawn.CacheInvalidate.Keys) != 1 || withdrawn.CacheInvalidate.Keys[0] != issued {
		t.Fatalf("withdrew %v, want exactly the key the response issued (%s)",
			withdrawn.CacheInvalidate.Keys, issued)
	}
}

// A withdrawal for a decision nobody recorded REFUSES rather than publishing a
// derived key that matches nothing: a revocation that silently misses is worse
// than one that refuses.
func TestWithdrawingAnUnknownHostKeyDecisionIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	op := revoke.NewOperator(h.bus, h.fleet)
	_, err := op.WithdrawHostKey(context.Background(), testTenant, revoke.Everyone(), revoke.HostKeyRef{
		Target: "nobody-reported-this.example.com", Fingerprint: "SHA256:nothing",
	})
	if err == nil {
		t.Fatal("withdrawing a host-key decision this server has never recorded reported success")
	}
	if !strings.Contains(err.Error(), "host-key") {
		t.Fatalf("the refusal does not say what was missing: %v", err)
	}
}
