// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/notify"
)

var secret = []byte("the webhook's signing key")

func notification() ext.Notification {
	return ext.Notification{
		Tenant:     "acme",
		Kind:       "grant.created",
		Severity:   ext.SeverityInfo,
		SubjectID:  "alice@example.com",
		Attributes: map[string]string{"grant_id": "g_1", "scope": "prod-dba"},
		OccurredAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
		DedupeKey:  "grant.created:g_1",
	}
}

// receiver records what a webhook endpoint was sent.
type receiver struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
	status   func(n int) int
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.bodies = append(r.bodies, body)
	n := len(r.requests)
	r.mu.Unlock()
	status := http.StatusNoContent
	if r.status != nil {
		status = r.status(n)
	}
	w.WriteHeader(status)
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *receiver) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for r.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("the receiver got %d requests, want %d", r.count(), n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func dispatcher(t *testing.T, o notify.Options) *notify.Dispatcher {
	t.Helper()
	if o.Backoff == 0 {
		o.Backoff = time.Millisecond
	}
	o.Logger = slog.New(slog.DiscardHandler)
	d := notify.New(o)
	d.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.Close(ctx)
	})
	return d
}

func webhook(t *testing.T, url string, key []byte) *notify.Webhook {
	t.Helper()
	w, err := notify.NewWebhook(notify.WebhookOptions{URL: url, Secret: key, Timeout: time.Second})
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	return w
}

// The body is codes and attributes, and it is signed: a receiver recomputes
// the HMAC over the timestamp and the exact bytes, and can tell this server
// from anybody who learned the URL.
func TestTheWebhookSignsWhatItSends(t *testing.T) {
	rcv := &receiver{}
	srv := httptest.NewServer(rcv)
	t.Cleanup(srv.Close)

	d := dispatcher(t, notify.Options{Webhook: webhook(t, srv.URL+"/hooks/hoplock", secret)})
	d.Notify(context.Background(), notification())
	rcv.waitFor(t, 1)

	req, body := rcv.requests[0], rcv.bodies[0]
	stamp := req.Header.Get(notify.HeaderTimestamp)
	if _, err := strconv.ParseInt(stamp, 10, 64); err != nil {
		t.Fatalf("timestamp header %q", stamp)
	}
	if got, want := req.Header.Get(notify.HeaderSignature), notify.Sign(secret, stamp, body); got != want {
		t.Errorf("signature %q, want %q", got, want)
	}
	if req.Header.Get(notify.HeaderEvent) != "grant.created" || req.Header.Get(notify.HeaderDelivery) == "" {
		t.Errorf("headers %v", req.Header)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("body: %v", err)
	}
	attrs, _ := payload["attributes"].(map[string]any)
	if payload["kind"] != "grant.created" || payload["tenant"] != "acme" || payload["severity"] != "info" ||
		payload["subject_id"] != "alice@example.com" || attrs["grant_id"] != "g_1" ||
		payload["delivery_id"] != req.Header.Get(notify.HeaderDelivery) {
		t.Errorf("payload %s", body)
	}
}

// A webhook with no key sends unsigned — the start-up log says so — and never
// sends a signature header computed over nothing.
func TestAnUnsignedWebhookSendsNoSignature(t *testing.T) {
	rcv := &receiver{}
	srv := httptest.NewServer(rcv)
	t.Cleanup(srv.Close)
	d := dispatcher(t, notify.Options{Webhook: webhook(t, srv.URL, nil)})
	d.Notify(context.Background(), notification())
	rcv.waitFor(t, 1)
	if got := rcv.requests[0].Header.Get(notify.HeaderSignature); got != "" {
		t.Errorf("an unsigned webhook sent signature %q", got)
	}
}

// An outage is retried with the same delivery id; a refusal is not retried at
// all, because asking again gets the same answer.
func TestAFailureIsRetriedAndARefusalIsNot(t *testing.T) {
	flaky := &receiver{status: func(n int) int {
		if n < 3 {
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	}}
	refusing := &receiver{status: func(int) int { return http.StatusUnauthorized }}
	flakySrv, refusingSrv := httptest.NewServer(flaky), httptest.NewServer(refusing)
	t.Cleanup(flakySrv.Close)
	t.Cleanup(refusingSrv.Close)

	dispatcher(t, notify.Options{Webhook: webhook(t, flakySrv.URL, secret)}).Notify(context.Background(), notification())
	flaky.waitFor(t, 3)
	ids := map[string]bool{}
	for _, r := range flaky.requests {
		ids[r.Header.Get(notify.HeaderDelivery)] = true
	}
	if len(ids) != 1 {
		t.Errorf("the retries carried %d delivery ids, want one a receiver can de-duplicate on", len(ids))
	}

	dispatcher(t, notify.Options{Webhook: webhook(t, refusingSrv.URL, secret)}).Notify(context.Background(), notification())
	refusing.waitFor(t, 1)
	time.Sleep(50 * time.Millisecond)
	if n := refusing.count(); n != 1 {
		t.Errorf("a 401 was retried: %d attempts", n)
	}
}

// Nothing waits on a notification: a destination that hangs fills its own
// queue and the rest are dropped, while Notify keeps returning at once.
func TestNotifyNeverBlocksTheCaller(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(srv.Close)

	d := dispatcher(t, notify.Options{Webhook: webhook(t, srv.URL, secret), Queue: 1, Attempts: 1})
	// Registered after the dispatcher, so it runs first: the hung handler is
	// released before Close drains.
	t.Cleanup(func() { close(release) })
	start := time.Now()
	for range 100 {
		d.Notify(context.Background(), notification())
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("100 notifications to a hung destination took %v to hand over", elapsed)
	}
}

// A redirect is refused rather than followed: a 307 would re-send the signed
// body to wherever the receiver said, which is not what this deployment
// configured.
func TestARedirectIsNotFollowed(t *testing.T) {
	elsewhere := &receiver{}
	target := httptest.NewServer(elsewhere)
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)

	w := webhook(t, redirector.URL, secret)
	err := w.Deliver(context.Background(), notify.Delivery{ID: "ntf_1", Notification: notification()})
	if !errors.Is(err, notify.ErrPermanent) {
		t.Errorf("a redirect answered %v, want a permanent refusal", err)
	}
	if elsewhere.count() != 0 {
		t.Error("the signed body was re-sent to the redirect's target")
	}
}

// A notifier a host binary registered receives the same notification beside
// Control's own webhook, which keeps working (ext.Notifier).
func TestARegisteredNotifierIsAChannelBesideTheWebhook(t *testing.T) {
	rcv := &receiver{}
	srv := httptest.NewServer(rcv)
	t.Cleanup(srv.Close)
	slack := &fakeNotifier{}

	d := dispatcher(t, notify.Options{
		Webhook: webhook(t, srv.URL, secret),
		Notifiers: []ext.Bound[ext.Notifier]{{
			Registration: ext.Registration{Provider: "example/slack"}, Impl: slack,
		}},
	})
	if got := d.Destinations(); len(got) != 2 || got[0] != "webhook" || got[1] != "example/slack" {
		t.Errorf("destinations = %v", got)
	}
	d.Notify(context.Background(), notification())
	rcv.waitFor(t, 1)
	deadline := time.Now().Add(5 * time.Second)
	for slack.count.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if slack.count.Load() != 1 {
		t.Errorf("the registered notifier received %d notifications", slack.count.Load())
	}
}

// Close hands over what is queued before the process exits.
func TestCloseDeliversWhatIsQueued(t *testing.T) {
	rcv := &receiver{}
	srv := httptest.NewServer(rcv)
	t.Cleanup(srv.Close)
	d := notify.New(notify.Options{Webhook: webhook(t, srv.URL, secret), Logger: slog.New(slog.DiscardHandler)})
	for range 5 {
		d.Notify(context.Background(), notification())
	}
	d.Start()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	if n := rcv.count(); n != 5 {
		t.Errorf("%d of 5 queued notifications were delivered by Close", n)
	}
	d.Notify(context.Background(), notification()) // after Close: dropped, never a panic
}

func TestOnlyASafeWebhookURLIsAccepted(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://hooks.example.com/hoplock":   true,
		"http://127.0.0.1:9000/hook":          true,
		"http://localhost/hook":               true,
		"http://[::1]:8080/hook":              true,
		"http://hooks.example.com/hook":       false, // clear text across a network
		"https://user:pass@hooks.example.com": false, // a credential in the configuration
		"ftp://hooks.example.com/hook":        false,
		"/relative/hook":                      false,
		"":                                    false,
	} {
		_, err := notify.ParseWebhookURL(raw)
		if (err == nil) != ok {
			t.Errorf("ParseWebhookURL(%q) = %v, want accepted=%v", raw, err, ok)
		}
	}
}

type fakeNotifier struct{ count atomic.Int32 }

func (f *fakeNotifier) Notify(context.Context, ext.Notification) error {
	f.count.Add(1)
	return nil
}
