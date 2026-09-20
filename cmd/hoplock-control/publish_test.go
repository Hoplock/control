// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// What the publish listener publishes is the KILL SWITCH, so the cases that
// matter most here are the ones about who may reach it and what it refuses to
// claim.

const publishTenant = store.Tenant("default")

func newPublishHarness(t *testing.T) (*revoke.Bus, string) {
	t.Helper()

	bus, err := revoke.New(revoke.Options{HeartbeatInterval: time.Second})
	if err != nil {
		t.Fatalf("revoke.New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = bus.Close(ctx)
	})

	ps, err := newPublishServer(
		revoke.NewOperator(bus, nil),
		publishTenant,
		"test-publish-token",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("newPublishServer: %v", err)
	}
	srv := httptest.NewServer(ps.handler())
	t.Cleanup(srv.Close)
	return bus, srv.URL
}

func publishTo(t *testing.T, base, token, body string) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/debug/revoke", rdr)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	raw, _ := io.ReadAll(res.Body)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	return res.StatusCode, fields
}

// An unauthenticated kill switch is worse than no kill switch.
func TestThePublishListenerRefusesAnUnknownCredential(t *testing.T) {
	t.Parallel()
	_, base := newPublishHarness(t)

	for name, token := range map[string]string{
		"no credential":   "",
		"the wrong one":   "not-the-token",
		"a near miss":     "test-publish-tokeN",
		"the empty token": "Bearer",
	} {
		if code, _ := publishTo(t, base, token, ""); code != http.StatusUnauthorized {
			t.Errorf("%s: answered %d, want 401", name, code)
		}
	}
}

// A listener bound without a credential is refused at construction, not served
// and guarded later.
func TestThePublishListenerCannotBeBuiltWithoutACredential(t *testing.T) {
	t.Parallel()
	if _, err := newPublishServer(nil, publishTenant, "", slog.Default()); err == nil {
		t.Fatal("a publish listener was built with no token, so anybody who can reach the port holds the kill switch")
	}
}

// The suite posts a body-less request and asserts only that an event happens,
// so the default has to be an event — and the least surprising one.
func TestABodylessPublishInvalidatesEverything(t *testing.T) {
	t.Parallel()
	bus, base := newPublishHarness(t)

	events := subscribeForTest(t, bus, "proxy-1")

	code, body := publishTo(t, base, "test-publish-token", "")
	if code != http.StatusOK {
		t.Fatalf("publish answered %d: %v", code, body)
	}
	if body["event_id"] == "" || body["event_id"] == nil {
		t.Fatalf("the receipt names no event id: %v", body)
	}

	ev := nextNonHeartbeat(t, events)
	if ev.Type != contract.EventTypeCacheInvalidate || !ev.CacheInvalidate.All {
		t.Fatalf("a body-less publish produced %+v", ev)
	}
}

// The asymmetry an operator is most likely to be misled by, on the surface
// they read it from: a subject-scoped invalidation says plainly that it did
// not cover a host-key decision.
func TestThePublishReceiptSaysWhatItCovered(t *testing.T) {
	t.Parallel()
	_, base := newPublishHarness(t)

	code, body := publishTo(t, base, "test-publish-token", `{"type":"cache_invalidate","subject":"alice@example.com"}`)
	if code != http.StatusOK {
		t.Fatalf("publish answered %d: %v", code, body)
	}
	if covers, _ := body["covers_host_key_decisions"].(bool); covers {
		t.Fatal("a subject-scoped invalidation reported that it covered host-key decisions")
	}

	code, body = publishTo(t, base, "test-publish-token", `{"type":"cache_invalidate","keys":["hc1:abc"]}`)
	if code != http.StatusOK {
		t.Fatalf("publish answered %d: %v", code, body)
	}
	if covers, _ := body["covers_host_key_decisions"].(bool); !covers {
		t.Fatal("an invalidation naming a key reported that it did not cover host-key decisions")
	}
}

// An operator mistake is a 400 that says what was wrong, never a cheerful 200
// that published nothing.
func TestThePublishListenerRefusesAMalformedAction(t *testing.T) {
	t.Parallel()
	_, base := newPublishHarness(t)

	for name, body := range map[string]string{
		"a kill with no reason":      `{"type":"session_kill","all":true}`,
		"a kill selecting nothing":   `{"type":"session_kill","reason":"why"}`,
		"a kill selecting two ways":  `{"type":"session_kill","all":true,"subject":"a","reason":"why"}`,
		"an event type nobody has":   `{"type":"reboot_everything"}`,
		"a host-key withdraw of air": `{"type":"host_key_withdraw"}`,
		"not JSON at all":            `{`,
	} {
		if code, _ := publishTo(t, base, "test-publish-token", body); code != http.StatusBadRequest {
			t.Errorf("%s: answered %d, want 400", name, code)
		}
	}
}

// A server with no way to resolve a host-key decision refuses to withdraw one
// rather than publishing a derived key that matches nothing.
func TestWithdrawingAHostKeyWithoutADirectoryIsRefused(t *testing.T) {
	t.Parallel()
	_, base := newPublishHarness(t)

	code, _ := publishTo(t, base, "test-publish-token",
		`{"type":"host_key_withdraw","host_key":{"target":"h.example.com","fingerprint":"SHA256:x"}}`)
	if code == http.StatusOK {
		t.Fatal("a host-key withdrawal reported success although nothing could resolve the decision's key")
	}
}

// The listener publishes and does nothing else: there is no read path and no
// way to ask it what any proxy holds.
func TestThePublishListenerServesNothingElse(t *testing.T) {
	t.Parallel()
	_, base := newPublishHarness(t)

	for _, path := range []string{"/", "/debug/logs", "/v1/authorize", "/debug"} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer test-publish-token")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s answered %d, want 404", path, res.StatusCode)
		}
	}
}

// subscribeForTest opens a subscription and hands its events back.
func subscribeForTest(t *testing.T, bus *revoke.Bus, proxyID string) chan *contract.RevocationEvent {
	t.Helper()
	out := make(chan *contract.RevocationEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = bus.Subscribe(ctx, publishTenant, proxyID, "", func(ev *contract.RevocationEvent) error {
			out <- ev
			return nil
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		live, err := bus.LiveSubscriptions(context.Background(), publishTenant)
		if err == nil {
			if _, ok := live[proxyID]; ok {
				return out
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the subscription never registered")
	return nil
}

func nextNonHeartbeat(t *testing.T, events chan *contract.RevocationEvent) *contract.RevocationEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type != contract.EventTypeHeartbeat {
				return ev
			}
		case <-deadline:
			t.Fatal("no event arrived")
		}
	}
}

// publishFailure must not turn a withdrawal that dropped nothing into a
// success, which is the whole reason the key is resolved rather than derived.
func TestPublishFailureClassification(t *testing.T) {
	t.Parallel()

	if status, _ := publishFailure(errors.New("the database went away")); status != http.StatusInternalServerError {
		t.Errorf("an unclassified failure answered %d, want 500", status)
	}
	if status, _ := publishFailure(revoke.ErrNoReason); status != http.StatusBadRequest {
		t.Errorf("a kill with no reason answered %d, want 400", status)
	}
	if status, _ := publishFailure(revoke.ErrClosed); status != http.StatusServiceUnavailable {
		t.Errorf("publishing into a draining bus answered %d, want 503", status)
	}
}
