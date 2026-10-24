// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoplock/control/ext"
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
	// DefaultReplayBuffer is how many published events of each tenant are
	// kept for a reconnecting subscriber. Past it the answer is `resync`,
	// which is correct but expensive — the proxy drops its entire decision
	// cache — so the buffer is sized to cover a reconnect with backoff
	// rather than an outage.
	DefaultReplayBuffer = 1024
	// DefaultSubscriberQueue is how far one subscriber may fall behind
	// before it is dropped. See [Options.SubscriberQueue].
	DefaultSubscriberQueue = 256
	// DefaultSyncInterval is how often a node reads the log for events no
	// wake-up told it of, and writes and reads the deployment's
	// subscriptions. See [Options.SyncInterval].
	DefaultSyncInterval = time.Second
)

// Options configures a [Bus].
type Options struct {
	// HeartbeatInterval is the interval this server keeps AND advertises.
	// The two are one number here on purpose — see [Bus.HeartbeatInterval].
	HeartbeatInterval time.Duration
	// ReplayBuffer is how many published events of each tenant are
	// retained.
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
	// Store is the deployment's shared log and subscription set (0027).
	// With one, every node built over the same store serves the same stream
	// and a restart replays rather than resyncs. Without one the log is
	// kept in this process and this node's subscriptions are the only ones
	// it knows: the broker every release before 0027 ran, which tests and a
	// database-less embedding still build.
	Store *store.Store
	// Coordinator carries the wake-up that tells the other nodes an event
	// was logged (ext.ClusterCoordinator's bus). Nil means there are no
	// other nodes. It is a wake-up and never the authority: a lost one
	// costs a node at most one SyncInterval, because the log is read on
	// that clock too.
	Coordinator ext.ClusterCoordinator
	// Node is this process's id in the deployment, as the coordinator names
	// it. Empty is "local".
	Node string
	// SyncInterval is how often [Bus.Start]'s loop reads the log for events
	// it was not woken for, and writes this node's subscriptions and reads
	// the others'. It bounds how late a lost wake-up makes delivery, and
	// how late another node's subscription is seen (twice it).
	SyncInterval time.Duration
	// Logger is where the broker's own events go. Nil takes slog's default.
	Logger *slog.Logger
	// Now overrides the clock. Tests use it; nothing in production should.
	Now func() time.Time
}

// Bus is the event broker (M9): the log, the subscriptions this node holds,
// and the copy of the ones every other node holds.
type Bus struct {
	mu sync.Mutex
	// local is every subscription this node holds, by tenant and proxy.
	local map[store.Tenant]map[string]map[*subscription]struct{}
	// cursors are how far this node has fanned each tenant's log out. A
	// tenant has one from the first subscription in it, for the life of
	// the process.
	cursors map[store.Tenant]*cursor
	// lines numbers the lines that name a position rather than take one,
	// so two of them at one position are still two ids, in order.
	lines  uint64
	closed bool

	// remote is the copy of every other node's subscriptions, by tenant
	// and proxy, as of the last sync (see presence.go). tracked is the
	// tenants it covers: every tenant this node was asked about.
	remote  map[store.Tenant]map[string]time.Time
	tracked map[store.Tenant]struct{}

	events   eventLog
	presence *store.Store
	coord    ext.ClusterCoordinator
	node     string

	interval   time.Duration
	advertised int32
	queueSize  int
	sync       time.Duration
	// refresh is how far a subscription's last write may advance before
	// its row is rewritten.
	refresh time.Duration
	log     *slog.Logger
	now     func() time.Time

	// live is held open for as long as any subscription is registered, so
	// that Close can drain rather than drop.
	live sync.WaitGroup

	// The loop Start runs, and what only it touches.
	stop    context.CancelFunc
	loops   sync.WaitGroup
	written map[store.Tenant]map[string]time.Time
	ticks   int
}

// cursor is one tenant's log as this node has fanned it out.
type cursor struct {
	// mu serialises reading the tenant's log and fanning it out on this
	// node, so events are queued in log order and none twice.
	mu sync.Mutex
	// epoch is the tenant's stream's, fixed when the cursor is made.
	epoch string
	// head is the last position fanned out. It is written holding both mu
	// and Bus.mu, so either is enough to read it.
	head int64
}

// Receipt is what an operator action produced.
type Receipt struct {
	// EventID is the id this server minted. An operator surface hands it
	// back so that an action can be found in the audit record (0010).
	EventID string
	// Delivered is how many subscriptions ON THIS NODE the event was
	// queued to now. It is not a delivery guarantee and not a count of the
	// fleet: a proxy that is reconnecting receives the event by replay, and
	// a proxy whose stream another node holds receives it from the log
	// through that node; neither is counted here.
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
		local:      make(map[store.Tenant]map[string]map[*subscription]struct{}),
		cursors:    make(map[store.Tenant]*cursor),
		remote:     make(map[store.Tenant]map[string]time.Time),
		tracked:    make(map[store.Tenant]struct{}),
		written:    make(map[store.Tenant]map[string]time.Time),
		coord:      o.Coordinator,
		node:       o.Node,
		interval:   interval,
		advertised: advertised,
		queueSize:  o.SubscriberQueue,
		sync:       o.SyncInterval,
		refresh:    2 * interval,
		log:        o.Logger,
		now:        o.Now,
	}
	retain := o.ReplayBuffer
	if retain <= 0 {
		retain = DefaultReplayBuffer
	}
	if o.Store != nil {
		b.events = storeLog{st: o.Store, retain: retain}
		b.presence = o.Store
	} else {
		b.events = newMemoryLog(newRunID(), retain)
	}
	if b.node == "" {
		b.node = "local"
	}
	if b.queueSize <= 0 {
		b.queueSize = DefaultSubscriberQueue
	}
	if b.sync <= 0 {
		b.sync = DefaultSyncInterval
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
// interval it names, AND that interval is inside the ceiling. A configuration
// that separated the timer from the claim would let them drift, so [New]
// derives the claim from the timer and refuses a timer past the ceiling.
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

// ConfigChanged tells one proxy that its desired configuration document moved
// (proxy D18). It NAMES the document and never carries it: the stream is
// replayable, and a replayed document would be a stale one applied as if
// current — the proxy answers by fetching the current one, so a replayed
// notification for an older version cannot bring that version back. That is
// also why it is logged for replay like any other event. It touches no session
// and no cached decision.
func (b *Bus) ConfigChanged(ctx context.Context, tenant store.Tenant, proxyID string, ev contract.ConfigChangedEvent) (Receipt, error) {
	aud := ToProxy(proxyID)
	if err := aud.validate(); err != nil {
		return Receipt{}, err
	}
	if ev.Version == "" || ev.Hash == "" {
		return Receipt{}, fmt.Errorf("revoke: a config_changed names a version and a hash")
	}
	return b.publish(ctx, tenant, aud, contract.RevocationEvent{
		Type:          contract.EventTypeConfigChanged,
		ConfigChanged: &ev,
	}, false)
}

// publish logs an event, fans it out to this node's subscriptions, and wakes
// the other nodes to fan it out to theirs.
//
// THE LOG IS THE ACKNOWLEDGEMENT. A receipt is returned only once the event is
// committed to it, and from then on every node holding a subscription it is
// addressed to delivers it — woken now, or at its next sync if the wake-up is
// lost — and a proxy reconnecting anywhere is replayed it. A node that dies
// after the commit loses nothing a caller was told was published; one that
// dies before it told the caller nothing.
//
// The fan-out here holds the tenant's cursor across the append, so nothing else
// on this node can deliver the event first: the receipt's count is exact for
// this node.
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
	c := b.cursors[tenant]
	b.mu.Unlock()

	at := b.now().UTC()
	ev.Timestamp = at.Format(time.RFC3339)
	if c != nil {
		c.mu.Lock()
	}
	seq, pos, err := b.events.append(ctx, tenant, logged{aud: aud, ev: ev, at: at}, b.node)
	delivered := 0
	if err == nil && c != nil {
		var fanErr error
		if delivered, fanErr = b.advanceLocked(ctx, tenant, c, seq); fanErr != nil {
			// Logged, so acknowledged: the event is not lost, only late
			// here, and this node's next sync delivers it.
			b.log.Warn("a logged event could not be read back for delivery on this node; the next sync delivers it",
				"event", "revoke_fanout_deferred", "tenant", tenant.String(), "error", fanErr.Error())
		}
	}
	if c != nil {
		c.mu.Unlock()
	}
	if err != nil {
		return Receipt{}, err
	}
	b.wake(ctx, tenant, seq)
	return Receipt{EventID: eventID(pos.epoch, seq), Delivered: delivered, CoversHostKeyDecisions: coversHostKeys}, nil
}

// advanceBatch is how many logged events one read of the log takes.
const advanceBatch = 256

// advanceLocked fans out every event of the tenant's log after the cursor, in
// order, and reports how many subscriptions the event at want was queued to.
// The caller holds c.mu.
func (b *Bus) advanceLocked(ctx context.Context, tenant store.Tenant, c *cursor, want int64) (int, error) {
	delivered := 0
	for {
		batch, err := b.events.after(ctx, tenant, c.head, 0, advanceBatch)
		if err != nil {
			return delivered, err
		}
		if len(batch) == 0 {
			return delivered, nil
		}
		var dropped []*subscription
		b.mu.Lock()
		for _, l := range batch {
			n, d := b.fanOutLocked(tenant, l.aud, l.render(c.epoch, b.advertised))
			dropped = append(dropped, d...)
			if l.seq == want {
				delivered = n
			}
			c.head = l.seq
		}
		b.mu.Unlock()
		b.logDropped(tenant, dropped)
		if len(batch) < advanceBatch {
			return delivered, nil
		}
	}
}

// cursorFor returns the tenant's cursor, making it — at the head of the
// tenant's log — the first time.
func (b *Bus) cursorFor(ctx context.Context, tenant store.Tenant) (*cursor, error) {
	b.mu.Lock()
	c := b.cursors[tenant]
	b.mu.Unlock()
	if c != nil {
		return c, nil
	}
	pos, err := b.events.open(ctx, tenant)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing := b.cursors[tenant]; existing != nil {
		return existing, nil
	}
	// Anything logged after the head was read is fanned out by the first
	// advance, which every subscription takes before it registers.
	c = &cursor{epoch: pos.epoch, head: pos.head}
	b.cursors[tenant] = c
	return c, nil
}

// fanOutLocked queues one event to every subscription on this node the audience
// reaches, and names the ones that were dropped for not keeping up.
func (b *Bus) fanOutLocked(tenant store.Tenant, aud Audience, ev *contract.RevocationEvent) (int, []*subscription) {
	if b.closed {
		// Draining: each subscription hands over what it already holds,
		// and a deploy's reconnect replays the rest.
		return 0, nil
	}
	byProxy := b.local[tenant]
	if len(byProxy) == 0 {
		return 0, nil
	}
	var (
		delivered int
		dropped   []*subscription
	)
	deliver := func(subs map[*subscription]struct{}) {
		for sub := range subs {
			line := *ev
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
	}
	if aud.All {
		for _, subs := range byProxy {
			deliver(subs)
		}
		return delivered, dropped
	}
	reached := make(map[string]struct{}, len(aud.ProxyIDs))
	for _, id := range aud.ProxyIDs {
		if _, twice := reached[id]; twice {
			continue
		}
		reached[id] = struct{}{}
		deliver(byProxy[id])
	}
	return delivered, dropped
}

func (b *Bus) logDropped(tenant store.Tenant, dropped []*subscription) {
	for _, sub := range dropped {
		b.log.Warn("revocation subscriber dropped for falling behind",
			"event", "revoke_subscriber_dropped",
			"tenant", tenant.String(),
			"proxy_id", sub.proxyID,
			"queue", b.queueSize,
		)
	}
}

// addLocked registers a subscription on this node.
func (b *Bus) addLocked(sub *subscription) {
	byProxy := b.local[sub.tenant]
	if byProxy == nil {
		byProxy = make(map[string]map[*subscription]struct{})
		b.local[sub.tenant] = byProxy
	}
	subs := byProxy[sub.proxyID]
	if subs == nil {
		subs = make(map[*subscription]struct{})
		byProxy[sub.proxyID] = subs
	}
	subs[sub] = struct{}{}
}

// remove forgets a subscription that ended.
func (b *Bus) remove(sub *subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	byProxy := b.local[sub.tenant]
	delete(byProxy[sub.proxyID], sub)
	if len(byProxy[sub.proxyID]) == 0 {
		delete(byProxy, sub.proxyID)
	}
	if len(byProxy) == 0 {
		delete(b.local, sub.tenant)
	}
}

// LiveSubscriptions implements fleet.SubscriptionState: which proxies hold a
// live subscription on ANY node, and when each was last written to.
//
// It is the signal the cache-hint gate and the fleet's liveness read (PLAN
// §5.4, M9): a hint is only as withdrawable as the stream that reaches its
// holder, so a proxy that is not here gets none. "Last seen" is the last
// SUCCESSFUL write rather than the moment it connected, because a subscription
// whose writes are failing is not a path a withdrawal could travel.
//
// It is read from memory and never from the store (M5): this node's own
// subscriptions as they stand, and the copy of every other node's, which is at
// most two sync intervals late (see presence.go). A tenant this node was never
// asked about is in the copy from the next sync on.
func (b *Bus) LiveSubscriptions(_ context.Context, tenant store.Tenant) (map[string]time.Time, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.trackLocked(tenant)

	out := make(map[string]time.Time, len(b.local[tenant])+len(b.remote[tenant]))
	for proxyID, subs := range b.local[tenant] {
		for sub := range subs {
			if seen := sub.seen(); seen.After(out[proxyID]) {
				out[proxyID] = seen
			}
		}
	}
	for proxyID, seen := range b.remote[tenant] {
		if seen.After(out[proxyID]) {
			out[proxyID] = seen
		}
	}
	return out, nil
}

// LiveSubscription is [Bus.LiveSubscriptions] for one proxy: whether it holds a
// live subscription on any node, and when it was last written to. It is the
// decision path's form (the cache-hint gate asks it on every hinted answer), so
// it is two map lookups however large the deployment's subscription set is.
func (b *Bus) LiveSubscription(_ context.Context, tenant store.Tenant, proxyID string) (time.Time, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.trackLocked(tenant)

	var (
		latest time.Time
		ok     bool
	)
	for sub := range b.local[tenant][proxyID] {
		ok = true
		if seen := sub.seen(); seen.After(latest) {
			latest = seen
		}
	}
	if seen, held := b.remote[tenant][proxyID]; held {
		ok = true
		if seen.After(latest) {
			latest = seen
		}
	}
	return latest, ok, nil
}

// trackLocked adds a tenant to the set the copy of other nodes' subscriptions
// covers. There is no store read that lists tenants (M18), so the set is
// learned from what this node is asked.
func (b *Bus) trackLocked(tenant store.Tenant) {
	if _, ok := b.tracked[tenant]; !ok && tenant != "" {
		b.tracked[tenant] = struct{}{}
	}
}

// Close drains every subscription and stops accepting publications.
//
// DRAINING RATHER THAN DROPPING IS THE POINT: a subscription that is cut
// mid-line hands the proxy a truncated event, and one that is cut with queued
// events behind it hands it a gap it will only discover on reconnect. So the
// bus stops publishing, each subscription finishes what it already holds, and
// the response ends normally — a deploy is a reconnect rather than a
// fleet-wide cache flush, and with a store the reconnect replays.
//
// Then it stops the loop [Bus.Start] runs and removes this node's rows from the
// deployment's subscription set, so no other node keeps counting a stream that
// ended with the process.
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
	for _, byProxy := range b.local {
		for _, subs := range byProxy {
			for sub := range subs {
				sub.finish()
			}
		}
	}
	b.mu.Unlock()

	drained := make(chan struct{})
	go func() {
		b.live.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-ctx.Done():
		b.stopLoop(ctx)
		return ctx.Err()
	}
	if b.stopLoop(ctx) {
		b.withdrawPresence(ctx)
	}
	return nil
}

// newRunID draws the epoch of a log kept in this process.
func newRunID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		// An epoch that is not unique costs a resync on reconnect, which
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
