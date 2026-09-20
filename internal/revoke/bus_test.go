// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

const testTenant = store.Tenant("default")

// newBus builds a broker with a heartbeat fast enough that a test does not
// spend a second waiting for one.
func newBus(t *testing.T, o revoke.Options) *revoke.Bus {
	t.Helper()
	if o.HeartbeatInterval == 0 {
		o.HeartbeatInterval = 50 * time.Millisecond
	}
	b, err := revoke.New(o)
	if err != nil {
		t.Fatalf("revoke.New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.Close(ctx)
	})
	return b
}

// reader is one subscriber, read from a goroutine so a test can assert what a
// proxy would have seen.
type reader struct {
	t      *testing.T
	cancel context.CancelFunc
	events chan *contract.RevocationEvent
	done   chan error
}

func subscribe(t *testing.T, b *revoke.Bus, proxyID, lastEventID string) *reader {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &reader{
		t:      t,
		cancel: cancel,
		events: make(chan *contract.RevocationEvent, 256),
		done:   make(chan error, 1),
	}
	ready := make(chan struct{})
	go func() {
		var once sync.Once
		r.done <- b.Subscribe(ctx, testTenant, proxyID, lastEventID, func(ev *contract.RevocationEvent) error {
			once.Do(func() { close(ready) })
			r.events <- ev
			return nil
		})
	}()
	// Wait until the subscription is registered, so a publish that follows
	// is one this subscriber was present for. A fresh subscription emits
	// nothing until its first heartbeat, so the registration is observed
	// through the bus rather than through the stream.
	waitForSubscription(t, b, proxyID)
	_ = ready
	t.Cleanup(cancel)
	return r
}

// next returns the next event that is not a heartbeat.
func (r *reader) next(d time.Duration) *contract.RevocationEvent {
	r.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case ev := <-r.events:
			if ev.Type == contract.EventTypeHeartbeat {
				continue
			}
			return ev
		case <-deadline:
			r.t.Fatalf("no event inside %s", d)
			return nil
		}
	}
}

// nothing asserts that no event arrives inside d.
func (r *reader) nothing(d time.Duration) {
	r.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case ev := <-r.events:
			if ev.Type == contract.EventTypeHeartbeat {
				continue
			}
			r.t.Fatalf("an event reached a subscriber it was not addressed to: %s (%s)", ev.EventID, ev.Type)
		case <-deadline:
			return
		}
	}
}

// waitForSubscription blocks until a proxy's subscription is registered, so a
// publication that follows is one it was present for.
func waitForSubscription(t *testing.T, b *revoke.Bus, proxyID string) {
	t.Helper()
	waitFor(t, func() bool {
		live, err := b.LiveSubscriptions(context.Background(), testTenant)
		if err != nil {
			return false
		}
		_, ok := live[proxyID]
		return ok
	}, "subscription for "+proxyID+" to register")
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func kill(subject string) revoke.Kill {
	return revoke.Kill{Subject: subject, Reason: "Access withdrawn by the security team."}
}

// ---------------------------------------------------------------------------
// addressing
// ---------------------------------------------------------------------------

// Three concurrent subscribers, because addressing that is only ever asserted
// against one subscriber is addressing that is not asserted at all: a bus that
// sent everything to everybody would pass.
func TestKillReachesExactlyTheIntendedSubscribers(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{})
	one, two, three := subscribe(t, b, "proxy-1", ""), subscribe(t, b, "proxy-2", ""), subscribe(t, b, "proxy-3", "")

	// By session id, to one proxy.
	rcpt, err := b.Kill(context.Background(), testTenant, revoke.ToProxy("proxy-1"), revoke.Kill{
		SessionIDs: []string{"session-a"},
		Reason:     "Access withdrawn by the security team.",
	})
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if rcpt.Delivered != 1 {
		t.Fatalf("delivered to %d subscribers, want 1", rcpt.Delivered)
	}
	got := one.next(time.Second)
	if got.Type != contract.EventTypeSessionKill || got.SessionKill == nil || got.SessionKill.SessionIDs[0] != "session-a" {
		t.Fatalf("proxy-1 got %+v", got)
	}
	two.nothing(100 * time.Millisecond)
	three.nothing(100 * time.Millisecond)

	// By subject, to a named set: the operator does not have to know where
	// Alice is, but they may still narrow it.
	if _, err := b.Kill(context.Background(), testTenant, revoke.ToProxies("proxy-2", "proxy-3"), kill("alice@example.com")); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if got := two.next(time.Second); got.SessionKill.Subject != "alice@example.com" {
		t.Fatalf("proxy-2 got %+v", got.SessionKill)
	}
	if got := three.next(time.Second); got.SessionKill.Subject != "alice@example.com" {
		t.Fatalf("proxy-3 got %+v", got.SessionKill)
	}
	one.nothing(100 * time.Millisecond)

	// To all.
	rcpt, err = b.Kill(context.Background(), testTenant, revoke.Everyone(), revoke.Kill{All: true, Reason: "Estate-wide lockdown."})
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if rcpt.Delivered != 3 {
		t.Fatalf("delivered to %d subscribers, want 3", rcpt.Delivered)
	}
	for _, r := range []*reader{one, two, three} {
		if got := r.next(time.Second); !got.SessionKill.All {
			t.Fatalf("a subscriber did not get the estate-wide kill: %+v", got)
		}
	}
}

func TestInvalidateReachesExactlyTheIntendedSubscribers(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{})
	one, two, three := subscribe(t, b, "proxy-1", ""), subscribe(t, b, "proxy-2", ""), subscribe(t, b, "proxy-3", "")

	if _, err := b.Invalidate(context.Background(), testTenant, revoke.ToProxy("proxy-2"), revoke.Invalidation{Keys: []string{"hc1:abc"}}); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if got := two.next(time.Second); got.CacheInvalidate == nil || got.CacheInvalidate.Keys[0] != "hc1:abc" {
		t.Fatalf("proxy-2 got %+v", got)
	}
	one.nothing(100 * time.Millisecond)
	three.nothing(100 * time.Millisecond)

	if _, err := b.Invalidate(context.Background(), testTenant, revoke.Everyone(), revoke.Invalidation{Subject: "alice@example.com"}); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	for _, r := range []*reader{one, two, three} {
		if got := r.next(time.Second); got.CacheInvalidate.Subject != "alice@example.com" {
			t.Fatalf("a subscriber did not get the subject invalidation: %+v", got)
		}
	}

	if _, err := b.Invalidate(context.Background(), testTenant, revoke.ToProxies("proxy-1", "proxy-3"), revoke.Invalidation{All: true}); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if got := one.next(time.Second); !got.CacheInvalidate.All {
		t.Fatalf("proxy-1 got %+v", got)
	}
	if got := three.next(time.Second); !got.CacheInvalidate.All {
		t.Fatalf("proxy-3 got %+v", got)
	}
	two.nothing(100 * time.Millisecond)
}

// Tenancy is a delivery dimension too (M18): a subscriber in another tenant is
// not a subscriber this event reaches, whatever it is addressed to.
func TestAnEventNeverCrossesATenant(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{})
	mine := subscribe(t, b, "proxy-1", "")

	rcpt, err := b.Kill(context.Background(), store.Tenant("other"), revoke.Everyone(), kill("alice@example.com"))
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if rcpt.Delivered != 0 {
		t.Fatalf("delivered to %d subscribers in another tenant", rcpt.Delivered)
	}
	mine.nothing(150 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// validation
// ---------------------------------------------------------------------------

// A revoked session that looks like a crash is the failure the reason field
// exists to prevent, so an empty one is refused rather than defaulted.
func TestAnEmptySessionKillReasonIsRejected(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{})
	_, err := b.Kill(context.Background(), testTenant, revoke.Everyone(), revoke.Kill{All: true})
	if !errors.Is(err, revoke.ErrNoReason) {
		t.Fatalf("Kill with no reason: %v, want ErrNoReason", err)
	}
}

func TestPublicationSelectorsAreExclusive(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{})
	ctx := context.Background()

	if _, err := b.Kill(ctx, testTenant, revoke.Everyone(), revoke.Kill{Reason: "why"}); !errors.Is(err, revoke.ErrNoSelector) {
		t.Fatalf("a kill selecting nothing: %v, want ErrNoSelector", err)
	}
	if _, err := b.Kill(ctx, testTenant, revoke.Everyone(), revoke.Kill{All: true, Subject: "alice", Reason: "why"}); !errors.Is(err, revoke.ErrNoSelector) {
		t.Fatalf("a kill selecting two ways: %v, want ErrNoSelector", err)
	}
	if _, err := b.Invalidate(ctx, testTenant, revoke.Everyone(), revoke.Invalidation{}); !errors.Is(err, revoke.ErrNoSelector) {
		t.Fatalf("an invalidation selecting nothing: %v, want ErrNoSelector", err)
	}
	if _, err := b.Kill(ctx, testTenant, revoke.Audience{}, kill("alice")); !errors.Is(err, revoke.ErrNoAudience) {
		t.Fatalf("an event addressed to nobody: %v, want ErrNoAudience", err)
	}
}

// The asymmetry an operator is most likely to be misled by: a subject-scoped
// invalidation cannot reach a host-key decision, because that decision was not
// made for a person. The receipt says so rather than leaving the operator to
// assume it was covered.
func TestASubjectInvalidationDoesNotClaimToCoverHostKeyDecisions(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{})
	ctx := context.Background()

	bySubject, err := b.Invalidate(ctx, testTenant, revoke.Everyone(), revoke.Invalidation{Subject: "alice@example.com"})
	if err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if bySubject.CoversHostKeyDecisions {
		t.Fatal("a subject-scoped invalidation reported that it covered host-key decisions")
	}

	byKey, err := b.Invalidate(ctx, testTenant, revoke.Everyone(), revoke.Invalidation{Keys: []string{"hc1:abc"}})
	if err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if !byKey.CoversHostKeyDecisions {
		t.Fatal("an invalidation naming a key reported that it did not cover host-key decisions")
	}

	all, err := b.Invalidate(ctx, testTenant, revoke.Everyone(), revoke.Invalidation{All: true})
	if err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if !all.CoversHostKeyDecisions {
		t.Fatal("an estate-wide invalidation reported that it did not cover host-key decisions")
	}

	resync, err := b.Resync(ctx, testTenant, revoke.Everyone())
	if err != nil {
		t.Fatalf("Resync: %v", err)
	}
	if !resync.CoversHostKeyDecisions {
		t.Fatal("a resync reported that it did not cover host-key decisions")
	}
}

// ---------------------------------------------------------------------------
// ordering, replay and resync
// ---------------------------------------------------------------------------

func TestEventIDsAreMonotonicPerSubscriber(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{HeartbeatInterval: 20 * time.Millisecond})
	r := subscribe(t, b, "proxy-1", "")

	for i := 0; i < 20; i++ {
		if _, err := b.Invalidate(context.Background(), testTenant, revoke.ToProxy("proxy-1"), revoke.Invalidation{Keys: []string{fmt.Sprintf("k%d", i)}}); err != nil {
			t.Fatalf("Invalidate: %v", err)
		}
	}

	var last string
	for i := 0; i < 20; i++ {
		// Heartbeats are in this stream too and they carry ids from the
		// same sequence, so the assertion covers them as well.
		select {
		case ev := <-r.events:
			if last != "" && ev.EventID <= last {
				t.Fatalf("event id %q did not advance past %q", ev.EventID, last)
			}
			last = ev.EventID
			if ev.Type == contract.EventTypeHeartbeat {
				i--
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d events arrived", i)
		}
	}
}

func TestReconnectReplaysEverythingAfterTheLastEventID(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{HeartbeatInterval: time.Second})
	first := subscribe(t, b, "proxy-1", "")

	for _, key := range []string{"k1", "k2"} {
		if _, err := b.Invalidate(context.Background(), testTenant, revoke.ToProxy("proxy-1"), revoke.Invalidation{Keys: []string{key}}); err != nil {
			t.Fatalf("Invalidate: %v", err)
		}
	}
	_ = first.next(time.Second)
	last := first.next(time.Second).EventID
	first.cancel()

	// Published into the gap, while nobody is listening.
	if _, err := b.Invalidate(context.Background(), testTenant, revoke.ToProxy("proxy-1"), revoke.Invalidation{Keys: []string{"k3"}}); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}

	resumed := subscribe(t, b, "proxy-1", last)
	got := resumed.next(time.Second)
	if got.Type == contract.EventTypeResync {
		t.Fatal("resynced although the event was still in the replay buffer")
	}
	if got.CacheInvalidate.Keys[0] != "k3" {
		t.Fatalf("replayed %+v, want the event published into the gap", got.CacheInvalidate)
	}
	if got.EventID <= last {
		t.Fatalf("replayed %s, which is not after %s", got.EventID, last)
	}
	resumed.nothing(100 * time.Millisecond)
}

// Replay is addressed the same way delivery was: an event another proxy was
// sent is not one this proxy missed.
func TestReplayRespectsTheAudience(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{HeartbeatInterval: time.Second})
	r := subscribe(t, b, "proxy-1", "")
	if _, err := b.Invalidate(context.Background(), testTenant, revoke.Everyone(), revoke.Invalidation{Keys: []string{"mine"}}); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	last := r.next(time.Second).EventID
	r.cancel()

	for _, key := range []string{"theirs-1", "theirs-2"} {
		if _, err := b.Invalidate(context.Background(), testTenant, revoke.ToProxy("proxy-2"), revoke.Invalidation{Keys: []string{key}}); err != nil {
			t.Fatalf("Invalidate: %v", err)
		}
	}
	if _, err := b.Invalidate(context.Background(), testTenant, revoke.ToProxy("proxy-1"), revoke.Invalidation{Keys: []string{"mine-again"}}); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}

	resumed := subscribe(t, b, "proxy-1", last)
	got := resumed.next(time.Second)
	if got.Type == contract.EventTypeResync {
		t.Fatalf("resynced although nothing addressed to this proxy was missed")
	}
	if got.CacheInvalidate.Keys[0] != "mine-again" {
		t.Fatalf("replayed %+v, want only the events addressed to proxy-1", got.CacheInvalidate)
	}
	resumed.nothing(100 * time.Millisecond)
}

// The three ids that cannot be replayed from, and all three get `resync` as the
// FIRST line: too old for the buffer, never minted by this run, and minted past
// where this run has reached.
func TestAnUnreplayableIDGetsResyncAsTheFirstLine(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{HeartbeatInterval: time.Second, ReplayBuffer: 2})
	r := subscribe(t, b, "proxy-1", "")
	if _, err := b.Invalidate(context.Background(), testTenant, revoke.ToProxy("proxy-1"), revoke.Invalidation{Keys: []string{"k1"}}); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	stale := r.next(time.Second).EventID
	r.cancel()

	// Three more events past a two-event buffer, so the id above has fallen
	// out of it.
	for _, key := range []string{"k2", "k3", "k4"} {
		if _, err := b.Invalidate(context.Background(), testTenant, revoke.ToProxy("proxy-1"), revoke.Invalidation{Keys: []string{key}}); err != nil {
			t.Fatalf("Invalidate: %v", err)
		}
	}

	for _, tc := range []struct {
		name string
		id   string
	}{
		{"too old for the buffer", stale},
		{"minted by another run", "evt-ffffffff-000000000001"},
		{"not an id this server mints", "whatever-the-proxy-had"},
		{"past where this run has reached", "evt-" + runIDOf(stale) + "-999999999999"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resumed := subscribe(t, b, "proxy-1", tc.id)
			defer resumed.cancel()
			select {
			case ev := <-resumed.events:
				if ev.Type != contract.EventTypeResync {
					t.Fatalf("the first line was %s (%s), want resync and nothing older", ev.Type, ev.EventID)
				}
			case <-time.After(time.Second):
				t.Fatal("nothing arrived on the resumed stream")
			}
			// Nothing older: a fragment of a history the proxy has
			// been told not to trust is worse than none of it.
			resumed.nothing(100 * time.Millisecond)
		})
	}
}

func runIDOf(eventID string) string {
	parts := strings.Split(eventID, "-")
	if len(parts) != 3 {
		return "00000000"
	}
	return parts[1]
}

// A fresh subscription starts from now: this server does not know what the
// proxy already holds, and replaying into one would be inventing a history.
func TestAFreshSubscriptionReplaysNothing(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{HeartbeatInterval: time.Second})
	if _, err := b.Invalidate(context.Background(), testTenant, revoke.Everyone(), revoke.Invalidation{All: true}); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	r := subscribe(t, b, "proxy-1", "")
	r.nothing(150 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// heartbeats
// ---------------------------------------------------------------------------

func TestHeartbeatsArriveWithinTheIntervalTheyAdvertise(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{HeartbeatInterval: 100 * time.Millisecond})
	r := subscribe(t, b, "proxy-1", "")

	var beats []time.Time
	deadline := time.After(3 * time.Second)
	for len(beats) < 3 {
		select {
		case ev := <-r.events:
			if ev.Type != contract.EventTypeHeartbeat {
				continue
			}
			advertised, ok := ev.AdvertisedHeartbeatInterval()
			if !ok {
				t.Fatal("a heartbeat carried no heartbeat_interval_seconds")
			}
			// Rounded UP from 100ms, so this server never claims an
			// interval it does not keep.
			if advertised != time.Second {
				t.Fatalf("advertised %s, want the interval rounded up to 1s", advertised)
			}
			if advertised > contract.MaxHeartbeatInterval {
				t.Fatalf("advertised %s, past the contract's ceiling of %s", advertised, contract.MaxHeartbeatInterval)
			}
			beats = append(beats, time.Now())
		case <-deadline:
			t.Fatalf("only %d heartbeats arrived", len(beats))
		}
	}
	for i := 1; i < len(beats); i++ {
		if gap := beats[i].Sub(beats[i-1]); gap > time.Second {
			t.Fatalf("heartbeat %d arrived %s after the last one, past the advertised 1s", i, gap)
		}
	}
}

// The second half of the obligation, and it is the half a server can fail
// while honestly keeping its own claim: an interval past the ceiling breaks
// every proxy in the fleet, so the process does not start with one.
func TestAnIntervalPastTheCeilingIsRefused(t *testing.T) {
	t.Parallel()
	for _, interval := range []time.Duration{
		contract.MaxHeartbeatInterval + time.Second,
		10*time.Second + time.Millisecond, // rounds UP to 11s
		10 * time.Minute,
	} {
		if _, err := revoke.New(revoke.Options{HeartbeatInterval: interval}); err == nil {
			t.Fatalf("revoke.New accepted a heartbeat interval of %s", interval)
		}
	}
	for _, interval := range []time.Duration{time.Second, contract.MaxHeartbeatInterval} {
		if _, err := revoke.New(revoke.Options{HeartbeatInterval: interval}); err != nil {
			t.Fatalf("revoke.New refused a conformant interval of %s: %v", interval, err)
		}
	}
}

func TestTheAdvertisedIntervalIsDerivedFromTheTimer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		interval time.Duration
		want     int32
	}{
		{time.Second, 1},
		{1500 * time.Millisecond, 2}, // rounded up: never claim what is not kept
		{5 * time.Second, 5},
		{time.Millisecond, 1},
	} {
		b, err := revoke.New(revoke.Options{HeartbeatInterval: tc.interval})
		if err != nil {
			t.Fatalf("revoke.New(%s): %v", tc.interval, err)
		}
		if got := b.AdvertisedHeartbeatSeconds(); got != tc.want {
			t.Fatalf("an interval of %s advertises %ds, want %ds", tc.interval, got, tc.want)
		}
		if b.HeartbeatInterval() != tc.interval {
			t.Fatalf("HeartbeatInterval() = %s, want %s", b.HeartbeatInterval(), tc.interval)
		}
	}
}

// ---------------------------------------------------------------------------
// backpressure and shutdown
// ---------------------------------------------------------------------------

// A subscriber that is not reading must cost itself its cache and nothing
// else. The three ways a broker can get this wrong are all failures here:
// blocking the publisher makes one stalled proxy an outage for the fleet,
// growing its queue makes one stalled proxy an out-of-memory, and keeping it
// while silently skipping its events is the gap nobody can see.
func TestASlowSubscriberIsDroppedRatherThanBlockingTheBus(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{HeartbeatInterval: time.Second, SubscriberQueue: 4})

	blocked := make(chan struct{})
	slowDone := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		slowDone <- b.Subscribe(ctx, testTenant, "proxy-slow", "", func(*contract.RevocationEvent) error {
			<-blocked
			return nil
		})
	}()
	waitForSubscription(t, b, "proxy-slow")

	// Well past the queue, and every publication answers promptly.
	for i := 0; i < 20; i++ {
		done := make(chan struct{})
		go func() {
			defer close(done)
			if _, err := b.Invalidate(context.Background(), testTenant, revoke.Everyone(), revoke.Invalidation{Keys: []string{fmt.Sprintf("k%d", i)}}); err != nil {
				t.Errorf("Invalidate: %v", err)
			}
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("a publication blocked on a subscriber that was not reading")
		}
	}

	// Release the stuck write. The stream then ends with an ERROR rather
	// than cleanly: an error is what the proxy reconnects from, and a clean
	// end would read to it as this server having nothing more to say.
	close(blocked)
	select {
	case err := <-slowDone:
		if err == nil {
			t.Fatal("the slow subscriber's stream ended without an error, so nothing tells it to reconnect")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the slow subscriber was never dropped")
	}
}

// And the subscriber beside it is untouched: it sees every event, in order,
// while the stalled one is still stuck inside its write.
func TestAStalledSubscriberDoesNotAffectTheOthers(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{HeartbeatInterval: time.Second})

	blocked := make(chan struct{})
	defer close(blocked)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = b.Subscribe(ctx, testTenant, "proxy-slow", "", func(*contract.RevocationEvent) error {
			<-blocked
			return nil
		})
	}()
	waitForSubscription(t, b, "proxy-slow")

	healthy := subscribe(t, b, "proxy-fast", "")
	const events = 20
	for i := 0; i < events; i++ {
		if _, err := b.Invalidate(context.Background(), testTenant, revoke.Everyone(), revoke.Invalidation{Keys: []string{fmt.Sprintf("k%d", i)}}); err != nil {
			t.Fatalf("Invalidate: %v", err)
		}
	}
	for i := 0; i < events; i++ {
		if got := healthy.next(2 * time.Second); got.CacheInvalidate.Keys[0] != fmt.Sprintf("k%d", i) {
			t.Fatalf("the healthy subscriber got %+v, want k%d — the order is what a reconnect resumes from", got.CacheInvalidate, i)
		}
	}
}

// A deploy is a reconnect, not a fleet-wide cache flush: what is already
// queued for a subscriber is handed over before the response ends.
func TestCloseDrainsQueuedEventsBeforeEndingTheStream(t *testing.T) {
	t.Parallel()
	b, err := revoke.New(revoke.Options{HeartbeatInterval: time.Second})
	if err != nil {
		t.Fatalf("revoke.New: %v", err)
	}

	release := make(chan struct{})
	seen := make(chan string, 16)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		first := true
		done <- b.Subscribe(ctx, testTenant, "proxy-1", "", func(ev *contract.RevocationEvent) error {
			if first {
				// Hold the writer long enough for the rest to
				// queue behind it, then let Close find them.
				<-release
				first = false
			}
			seen <- ev.EventID
			return nil
		})
	}()
	waitForSubscription(t, b, "proxy-1")

	var want []string
	for i := 0; i < 3; i++ {
		rcpt, err := b.Invalidate(context.Background(), testTenant, revoke.Everyone(), revoke.Invalidation{Keys: []string{fmt.Sprintf("k%d", i)}})
		if err != nil {
			t.Fatalf("Invalidate: %v", err)
		}
		want = append(want, rcpt.EventID)
	}
	close(release)

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelClose()
	if err := b.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a drained subscription ended with an error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the subscription did not end after Close")
	}

	close(seen)
	var got []string
	for id := range seen {
		got = append(got, id)
	}
	for _, id := range want {
		if !containsID(got, id) {
			t.Fatalf("Close dropped %s instead of draining it (delivered %v)", id, got)
		}
	}
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestAClosedBusRefusesPublicationsAndSubscriptions(t *testing.T) {
	t.Parallel()
	b, err := revoke.New(revoke.Options{})
	if err != nil {
		t.Fatalf("revoke.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := b.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := b.Kill(context.Background(), testTenant, revoke.Everyone(), kill("alice")); !errors.Is(err, revoke.ErrClosed) {
		t.Fatalf("Kill on a closed bus: %v, want ErrClosed", err)
	}
	err = b.Subscribe(context.Background(), testTenant, "proxy-1", "", func(*contract.RevocationEvent) error { return nil })
	if !errors.Is(err, revoke.ErrClosed) {
		t.Fatalf("Subscribe on a closed bus: %v, want ErrClosed", err)
	}
}

// The liveness signal the cache-hint gate reads (PLAN §5.4): a proxy holding a
// subscription is here, and one that has gone is not.
func TestLiveSubscriptionsIsTheSignalTheCacheHintGateReads(t *testing.T) {
	t.Parallel()
	b := newBus(t, revoke.Options{HeartbeatInterval: 50 * time.Millisecond})

	live, err := b.LiveSubscriptions(context.Background(), testTenant)
	if err != nil {
		t.Fatalf("LiveSubscriptions: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("a bus with no subscribers reported %v", live)
	}

	r := subscribe(t, b, "proxy-1", "")
	live, err = b.LiveSubscriptions(context.Background(), testTenant)
	if err != nil {
		t.Fatalf("LiveSubscriptions: %v", err)
	}
	seen, ok := live["proxy-1"]
	if !ok {
		t.Fatalf("a subscribed proxy is not live: %v", live)
	}
	if seen.IsZero() {
		t.Fatal("a live subscription reported a zero last-seen, which reads as stale")
	}
	if other, err := b.LiveSubscriptions(context.Background(), store.Tenant("other")); err != nil || len(other) != 0 {
		t.Fatalf("another tenant sees %v (err %v)", other, err)
	}

	r.cancel()
	waitFor(t, func() bool {
		live, err := b.LiveSubscriptions(context.Background(), testTenant)
		if err != nil {
			return false
		}
		_, ok := live["proxy-1"]
		return !ok
	}, "the subscription to be forgotten once it ends")
}
