// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// Defaults for the broker. Each one bounds something that would otherwise be
// unbounded, and two of them are bounded by the contract rather than by taste.
const (
	// DefaultHeartbeatInterval is how often a subscription is told the
	// stream is alive. It is well inside the contract's ceiling, which is
	// what leaves room for one lost heartbeat.
	DefaultHeartbeatInterval = 5 * time.Second
	// DefaultReplayBuffer is how many published events are kept for a
	// reconnecting subscriber. Past it the answer is `resync`, which is
	// correct but expensive — the proxy drops its entire decision cache —
	// so the buffer is sized to cover a reconnect with backoff rather than
	// an outage.
	DefaultReplayBuffer = 1024
	// DefaultSubscriberQueue is how far one subscriber may fall behind
	// before it is dropped. See [Options.SubscriberQueue].
	DefaultSubscriberQueue = 256
)

// Options configures a [Bus].
type Options struct {
	// HeartbeatInterval is the interval this server keeps AND advertises.
	// The two are one number here on purpose — see [Bus.HeartbeatInterval].
	HeartbeatInterval time.Duration
	// ReplayBuffer is how many published events are retained.
	ReplayBuffer int
	// SubscriberQueue is how many events may be outstanding to one
	// subscriber before it is dropped.
	//
	// DROPPING IS THE POLICY, and it is deliberate. The alternative to
	// dropping a subscriber that is not reading is to block the publisher,
	// which makes one stalled proxy an outage for the fleet, or to grow its
	// queue, which makes one stalled proxy an out-of-memory. A dropped
	// subscriber reconnects and is answered with replay or `resync`, so
	// nothing is silently skipped — it costs that proxy its cache, which is
	// the cheapest of the three.
	SubscriberQueue int
	// Logger is where the broker's own events go. Nil takes slog's default.
	Logger *slog.Logger
	// Now overrides the clock. Tests use it; nothing in production should.
	Now func() time.Time
}

// Bus is the in-process event broker (M9).
type Bus struct {
	mu sync.Mutex
	// seq is the monotonic source of every event id this run mints. It
	// counts heartbeats and resyncs too, so that an id a proxy sends back
	// always names a point in one order.
	seq uint64
	// evicted is the highest retained seq that has been dropped from the
	// ring. A subscriber resuming from before it cannot be replayed, and
	// that is the whole of the resync decision — see [Bus.planLocked].
	evicted uint64
	ring    []stored
	subs    map[*subscription]struct{}
	closed  bool

	// runID distinguishes this process from every earlier one. It is in
	// every event id, so an id minted before a restart is UNKNOWN rather
	// than believed: the ring is in memory and a restart empties it, and
	// answering `resync` to an id this run never minted is the contract's
	// own answer for a server that keeps no history.
	runID string

	interval   time.Duration
	advertised int32
	ringSize   int
	queueSize  int
	log        *slog.Logger
	now        func() time.Time

	// live is held open for as long as any subscription is registered, so
	// that Close can drain rather than drop.
	live sync.WaitGroup
}

// stored is one retained event, with what it takes to replay it to the right
// subscriber: the audience decided delivery once and must decide it the same
// way on a replay.
type stored struct {
	seq    uint64
	tenant store.Tenant
	aud    Audience
	ev     contract.RevocationEvent
}

// Receipt is what an operator action produced.
type Receipt struct {
	// EventID is the id this server minted. An operator surface hands it
	// back so that an action can be found in the audit record (0010).
	EventID string
	// Delivered is how many subscriptions the event was queued to NOW. It
	// is not a delivery guarantee and not a count of the fleet: a proxy
	// that is reconnecting receives the event by replay and is not counted
	// here.
	Delivered int
	// CoversHostKeyDecisions reports whether this event can withdraw a
	// host-key decision. It is on the receipt rather than left implicit
	// because a revocation that silently misses is worse than one that
	// refuses — see [Invalidation.ReachesHostKeyDecisions].
	CoversHostKeyDecisions bool
}

// New builds a broker.
//
// It REFUSES an interval outside the contract's ceiling rather than clamping
// one, and that refusal is the answer to "what stops it being configured above
// the ceiling". A server that advertised 600s and honestly kept to it would
// pass its own claim and take every proxy in the fleet off cached decisions
// (PLAN §4), so the number is not an operator's to get wrong quietly: the
// process does not start.
func New(o Options) (*Bus, error) {
	interval := o.HeartbeatInterval
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}
	advertised := advertise(interval)
	if advertised > contract.MaxHeartbeatIntervalSeconds {
		return nil, fmt.Errorf(
			"revoke: a heartbeat interval of %s advertises %ds, past the contract's ceiling of %ds",
			interval, advertised, contract.MaxHeartbeatIntervalSeconds)
	}

	b := &Bus{
		subs:       make(map[*subscription]struct{}),
		runID:      newRunID(),
		interval:   interval,
		advertised: advertised,
		ringSize:   o.ReplayBuffer,
		queueSize:  o.SubscriberQueue,
		log:        o.Logger,
		now:        o.Now,
	}
	if b.ringSize <= 0 {
		b.ringSize = DefaultReplayBuffer
	}
	if b.queueSize <= 0 {
		b.queueSize = DefaultSubscriberQueue
	}
	if b.log == nil {
		b.log = slog.Default()
	}
	if b.now == nil {
		b.now = time.Now
	}
	return b, nil
}

// advertise renders an interval as the whole number of seconds this server
// claims to keep.
//
// It rounds UP, so this server never advertises an interval it does not keep:
// a claim of 1s against a timer of 1.4s is a server failing its own
// advertisement every tick. The advertised number and the timer are derived
// from ONE value for the same reason — two numbers that can drift apart will,
// and the drift is invisible until a fleet is already reconnecting.
func advertise(interval time.Duration) int32 {
	secs := interval / time.Second
	if interval%time.Second != 0 {
		secs++
	}
	if secs < 1 {
		secs = 1
	}
	return int32(secs)
}

// HeartbeatInterval is the interval this server keeps and advertises.
//
// ONE VALUE, BOTH OBLIGATIONS. Since upstream Hoplock/proxy#56 the stream
// carries `heartbeat_interval_seconds`, so "within the interval the server
// advertises" is a claim read off the wire rather than a number typed into a
// harness — and the two halves are separately graded: this server keeps the
// interval it names, AND that interval is inside the contract's ceiling. A
// configuration that separated the timer from the claim would let them drift,
// so [New] derives the claim from the timer and refuses a timer past the
// ceiling.
func (b *Bus) HeartbeatInterval() time.Duration { return b.interval }

// AdvertisedHeartbeatSeconds is what this server puts on the wire.
func (b *Bus) AdvertisedHeartbeatSeconds() int32 { return b.advertised }

// Kill publishes a session_kill.
func (b *Bus) Kill(ctx context.Context, tenant store.Tenant, aud Audience, k Kill) (Receipt, error) {
	if err := aud.validate(); err != nil {
		return Receipt{}, err
	}
	if err := k.validate(); err != nil {
		return Receipt{}, err
	}
	return b.publish(ctx, tenant, aud, contract.RevocationEvent{
		Type:        contract.EventTypeSessionKill,
		SessionKill: k.payload(),
	}, false)
}

// Invalidate publishes a cache_invalidate.
func (b *Bus) Invalidate(ctx context.Context, tenant store.Tenant, aud Audience, inv Invalidation) (Receipt, error) {
	if err := aud.validate(); err != nil {
		return Receipt{}, err
	}
	if err := inv.validate(); err != nil {
		return Receipt{}, err
	}
	return b.publish(ctx, tenant, aud, contract.RevocationEvent{
		Type:            contract.EventTypeCacheInvalidate,
		CacheInvalidate: inv.payload(),
	}, inv.ReachesHostKeyDecisions())
}

// Resync tells subscribers to drop everything they hold and re-authorize.
//
// It is the blunt instrument, and it is also the honest one: it withdraws
// every cached decision a proxy holds, host-key decisions included, which is
// why it is the answer offered beside a decision's own key rather than beside
// `subject`.
func (b *Bus) Resync(ctx context.Context, tenant store.Tenant, aud Audience) (Receipt, error) {
	if err := aud.validate(); err != nil {
		return Receipt{}, err
	}
	return b.publish(ctx, tenant, aud, contract.RevocationEvent{Type: contract.EventTypeResync}, true)
}

// publish mints an id, retains the event when it is replayable, and queues it
// to every subscription the audience reaches.
//
// A `heartbeat` is not retained: replaying liveness tells a reconnecting proxy
// only that the stream was alive at a moment that has passed. A `resync` is
// retained, because a subscriber that missed one has missed the instruction to
// drop its cache.
func (b *Bus) publish(ctx context.Context, tenant store.Tenant, aud Audience, ev contract.RevocationEvent, coversHostKeys bool) (Receipt, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if tenant == "" {
		return Receipt{}, fmt.Errorf("revoke: a tenant is required")
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return Receipt{}, ErrClosed
	}
	seq, id := b.mintLocked()
	ev.EventID = id
	ev.Timestamp = b.now().UTC().Format(time.RFC3339)
	// The interval may be set on ANY event, not only a heartbeat, and a
	// reader takes it wherever it appears. Putting it on everything means a
	// proxy that reconnects into a burst of replay learns the interval on
	// its first line rather than on its first heartbeat.
	ev.HeartbeatIntervalSeconds = b.advertised

	if ev.Type != contract.EventTypeHeartbeat {
		b.retainLocked(stored{seq: seq, tenant: tenant, aud: aud, ev: ev})
	}
	delivered, dropped := b.fanOutLocked(tenant, aud, ev)
	b.mu.Unlock()

	for _, sub := range dropped {
		b.log.Warn("revocation subscriber dropped for falling behind",
			"event", "revoke_subscriber_dropped",
			"tenant", tenant.String(),
			"proxy_id", sub.proxyID,
			"queue", b.queueSize,
		)
	}
	return Receipt{EventID: id, Delivered: delivered, CoversHostKeyDecisions: coversHostKeys}, nil
}

// mintLocked assigns the next sequence number and renders its id.
func (b *Bus) mintLocked() (uint64, string) {
	b.seq++
	return b.seq, eventID(b.runID, b.seq)
}

// retainLocked appends to the replay ring, remembering what fell out of it.
//
// What fell out is the whole of the resync decision: a subscriber resuming
// from before [Bus.evicted] cannot be given everything it missed, and the
// contract's answer to that is `resync` as the first line and nothing older —
// never live delivery over a gap.
func (b *Bus) retainLocked(s stored) {
	b.ring = append(b.ring, s)
	for len(b.ring) > b.ringSize {
		b.evicted = b.ring[0].seq
		b.ring = b.ring[1:]
	}
	// The slice is re-cut rather than copied down, so reclaim the backing
	// array when it has grown well past what is retained.
	if cap(b.ring) > 4*b.ringSize && len(b.ring) == b.ringSize {
		fresh := make([]stored, b.ringSize, 2*b.ringSize)
		copy(fresh, b.ring)
		b.ring = fresh
	}
}

// fanOutLocked queues one event to every subscription the audience reaches,
// and names the ones that were dropped for not keeping up.
func (b *Bus) fanOutLocked(tenant store.Tenant, aud Audience, ev contract.RevocationEvent) (int, []*subscription) {
	var (
		delivered int
		dropped   []*subscription
	)
	for sub := range b.subs {
		if sub.tenant != tenant || !aud.reaches(sub.proxyID) {
			continue
		}
		line := ev
		select {
		case sub.ch <- &line:
			delivered++
		default:
			// The queue is full: this subscriber is not reading. Drop
			// it rather than block the bus or grow without bound; it
			// reconnects into replay or resync.
			if sub.evict() {
				dropped = append(dropped, sub)
			}
		}
	}
	return delivered, dropped
}

// LiveSubscriptions implements fleet.SubscriptionState: which proxies hold a
// live subscription, and when each was last written to.
//
// It is the signal the cache-hint gate reads before issuing a hint (PLAN §5.4,
// M9): a hint is only as withdrawable as the stream that reaches its holder, so
// a proxy that is not here gets none. "Last seen" is the last SUCCESSFUL write
// rather than the moment it connected, because a subscription whose writes are
// failing is not a path a withdrawal could travel.
func (b *Bus) LiveSubscriptions(_ context.Context, tenant store.Tenant) (map[string]time.Time, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make(map[string]time.Time, len(b.subs))
	for sub := range b.subs {
		if sub.tenant != tenant {
			continue
		}
		seen := sub.seen()
		if prev, ok := out[sub.proxyID]; ok && prev.After(seen) {
			continue
		}
		out[sub.proxyID] = seen
	}
	return out, nil
}

// Close drains every subscription and stops accepting publications.
//
// DRAINING RATHER THAN DROPPING IS THE POINT: a subscription that is cut
// mid-line hands the proxy a truncated event, and one that is cut with queued
// events behind it hands it a gap it will only discover on reconnect. So the
// bus stops minting, each subscription finishes what it already holds, and the
// response ends normally — a deploy is a reconnect rather than a fleet-wide
// cache flush.
//
// It returns ctx's error if the drain outlives the deadline, having asked every
// subscription to finish either way.
func (b *Bus) Close(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	for sub := range b.subs {
		sub.finish()
	}
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		b.live.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// eventID renders a sequence number as the opaque id the proxy echoes back.
//
// The run id is in it because the ring is in memory: an id minted by an earlier
// process names a position in an order this one does not have, and reading it
// as a position in THIS order would replay from the wrong place — or, worse,
// resume live delivery over a gap nobody could see.
func eventID(runID string, seq uint64) string {
	return fmt.Sprintf("evt-%s-%012d", runID, seq)
}

// parseEventID reads an id this server minted. Anything else is unknown, which
// is a resync rather than an error: the proxy echoes an opaque string back and
// is not at fault for having one this run does not recognise.
func parseEventID(id string) (runID string, seq uint64, ok bool) {
	const prefix = "evt-"
	if !strings.HasPrefix(id, prefix) {
		return "", 0, false
	}
	rest := id[len(prefix):]
	dash := strings.IndexByte(rest, '-')
	if dash <= 0 {
		return "", 0, false
	}
	seq, err := strconv.ParseUint(rest[dash+1:], 10, 64)
	if err != nil || seq == 0 {
		return "", 0, false
	}
	return rest[:dash], seq, true
}

func newRunID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		// A run id that is not unique costs a resync on reconnect, which
		// is the safe direction, so this never fails a start-up.
		return "00000000"
	}
	return hex.EncodeToString(buf)
}

// subscription is one open stream.
type subscription struct {
	tenant  store.Tenant
	proxyID string
	ch      chan *contract.RevocationEvent
	// dropped is closed when this subscriber fell behind; drained when the
	// bus is shutting down. They are two channels because the stream ends
	// differently: a drop is an error the proxy reconnects from, a drain is
	// a clean end of response.
	dropped   chan struct{}
	drained   chan struct{}
	dropOnce  sync.Once
	drainOnce sync.Once
	lastSeen  atomic.Int64
}

// evict ends this subscription with an error, and reports whether this call is
// the one that did it: a subscriber that is not reading misses every
// subsequent event too, and one log line per missed event would bury the
// incident an operator is trying to read.
func (s *subscription) evict() bool {
	first := false
	s.dropOnce.Do(func() {
		close(s.dropped)
		first = true
	})
	return first
}

func (s *subscription) finish() { s.drainOnce.Do(func() { close(s.drained) }) }

func (s *subscription) touch(t time.Time) { s.lastSeen.Store(t.UnixNano()) }
func (s *subscription) seen() time.Time   { return time.Unix(0, s.lastSeen.Load()) }
