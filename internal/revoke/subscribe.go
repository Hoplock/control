// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke

import (
	"context"
	"fmt"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// The subscription side: gap recovery, ordered delivery, and the heartbeat.
//
// Every guarantee this package makes is made here, and the one that is easiest
// to lose is the FIRST line. A reconnecting subscriber is owed either every
// event after its `last_event_id` or a `resync` before anything else; resuming
// live delivery over a gap looks identical to a healthy stream from the proxy's
// side and is the one behaviour worse than both. With the log shared (0027), the
// node answering need not be the node that minted the id.

// Subscribe serves one long-lived subscription and blocks until it ends.
//
// emit is called for every event, in order, and its first error ends the
// stream. Subscribe returns nil when the stream ended for a reason that is not
// a failure — the subscriber went away, or the bus was drained for a deploy —
// and an error otherwise.
//
// The registration happens holding the tenant's cursor, after catching it up
// with the log, which is what makes "nothing is skipped" true rather than
// likely: every event after the cursor's head at that instant is queued to this
// subscriber by a later fan-out, and the replay covers exactly up to that head.
// Doing the two in either order without the cursor leaves a window that drops
// an event or delivers it twice, and the proxy cannot tell which happened.
func (b *Bus) Subscribe(ctx context.Context, tenant store.Tenant, proxyID, lastEventID string, emit func(*contract.RevocationEvent) error) error {
	if tenant == "" {
		return fmt.Errorf("revoke: a tenant is required")
	}
	if proxyID == "" {
		return fmt.Errorf("revoke: a proxy id is required")
	}
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return ErrClosed
	}

	c, err := b.cursorFor(ctx, tenant)
	if err != nil {
		return err
	}
	sub := &subscription{
		tenant:  tenant,
		proxyID: proxyID,
		ch:      make(chan *contract.RevocationEvent, b.queueSize),
		dropped: make(chan struct{}),
		drained: make(chan struct{}),
	}

	c.mu.Lock()
	// Catch up first. An id another node minted may be past what this node
	// has delivered — its wake-up still in flight — and it is not unknown
	// for that: it is in the log.
	if _, err := b.advanceLocked(ctx, tenant, c, 0); err != nil {
		c.mu.Unlock()
		return err
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		c.mu.Unlock()
		return ErrClosed
	}
	through := c.head
	from, replay, resync := b.planLocked(c, lastEventID)
	sub.touch(b.now())
	b.addLocked(sub)
	b.live.Add(1)
	b.mu.Unlock()
	c.mu.Unlock()

	defer func() {
		b.remove(sub)
		b.live.Done()
	}()

	var missed []*contract.RevocationEvent
	if replay {
		var held bool
		if missed, held, err = b.replay(ctx, tenant, proxyID, c.epoch, from, through); err != nil {
			return err
		}
		if !held {
			// Too old: an event after the id has been pruned, so the
			// replay would have a hole in it.
			missed = nil
			b.mu.Lock()
			resync = b.resyncLineLocked(c, through)
			b.mu.Unlock()
		}
	}

	send := func(ev *contract.RevocationEvent) error {
		if err := emit(ev); err != nil {
			return err
		}
		sub.touch(b.now())
		return nil
	}

	if resync != nil {
		// FIRST LINE AND NOTHING OLDER. The proxy is being told it has
		// missed events it cannot be given, so anything replayed beside
		// this would be a fragment of a history it must not trust.
		if err := send(resync); err != nil {
			return err
		}
	}
	for _, ev := range missed {
		if err := send(ev); err != nil {
			return err
		}
	}

	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// The subscriber went away, or the request was cancelled.
			// Neither is this server's failure.
			return nil
		case <-sub.drained:
			// A clean shutdown. Everything already queued was for
			// this subscriber, so hand it over before ending the
			// response rather than leaving a gap behind.
			for {
				select {
				case ev := <-sub.ch:
					if err := send(ev); err != nil {
						return err
					}
				default:
					return nil
				}
			}
		case <-sub.dropped:
			return fmt.Errorf("revoke: subscriber %s fell more than %d events behind", proxyID, b.queueSize)
		case ev := <-sub.ch:
			if err := send(ev); err != nil {
				return err
			}
		case <-ticker.C:
			// The heartbeat goes through the bus and into this
			// subscriber's own queue rather than straight to the
			// writer, so that it takes its place in the one order
			// this subscriber sees. A heartbeat written past a
			// queued event would hand the proxy an id it could
			// resume from having not seen the event before it.
			b.heartbeat(sub)
		}
	}
}

// planLocked decides what a reconnecting subscriber is owed, as far as it can
// be decided without reading the log.
//
// Three answers, and the contract names all three: replay everything after the
// id, in order; answer `resync` when the id is unknown or names a position this
// log has not reached; or — for an absent id — start from now and replay
// nothing. Whether the id is too old is the replay's to say, because it is a
// fact about what the log still holds. The resync line, when there is one, is
// minted here, at the head the subscription registers at, so every event queued
// to it afterwards has an id past the line's.
func (b *Bus) planLocked(c *cursor, lastEventID string) (from int64, replay bool, resync *contract.RevocationEvent) {
	if lastEventID == "" {
		// A fresh subscription. Starting from now is what the contract
		// says, and it is also the only safe reading: this server does
		// not know what the proxy already holds.
		return 0, false, nil
	}
	epoch, pos, ok := parseEventID(lastEventID)
	switch {
	case !ok, epoch != c.epoch:
		// Unknown: a malformed id, or one another log minted — the
		// in-process broker before a restart, or before 0027. There is
		// no history to replay from and no way to know what was missed.
		return 0, false, b.resyncLineLocked(c, c.head)
	case pos > c.head:
		// A position this log has never reached, after catching up
		// with it. Believing it would resume live delivery past events
		// that do exist.
		return 0, false, b.resyncLineLocked(c, c.head)
	}
	return pos, true, nil
}

// replayPage is how many logged events one read of a replay takes.
const replayPage = 256

// replay reads what a subscriber resuming after from missed, up to through, and
// reports whether the log still held all of it.
//
// The floor is read AFTER the events. A prune deletes its events and raises the
// floor in one transaction, so a floor still at or below from when it is read
// means no prune reached past from before the events were read either: what was
// read is everything.
func (b *Bus) replay(ctx context.Context, tenant store.Tenant, proxyID, epoch string, from, through int64) ([]*contract.RevocationEvent, bool, error) {
	var out []*contract.RevocationEvent
	after := from
	for after < through {
		page, err := b.events.after(ctx, tenant, after, through, replayPage)
		if err != nil {
			return nil, false, err
		}
		for _, l := range page {
			after = l.seq
			if l.aud.reaches(proxyID) {
				out = append(out, l.render(epoch, b.advertised))
			}
		}
		if len(page) < replayPage {
			break
		}
	}
	pos, err := b.events.open(ctx, tenant)
	if err != nil {
		return nil, false, err
	}
	if from < pos.floor {
		return nil, false, nil
	}
	return out, true, nil
}

// resyncLineLocked builds the resync line answering one reconnect. It names the
// position the subscription registered at, so a proxy that processes it and
// reconnects resumes from a point every node understands.
func (b *Bus) resyncLineLocked(c *cursor, at int64) *contract.RevocationEvent {
	b.lines++
	return &contract.RevocationEvent{
		EventID:                  positionID(c.epoch, at, b.lines),
		Type:                     contract.EventTypeResync,
		Timestamp:                b.now().UTC().Format(time.RFC3339),
		HeartbeatIntervalSeconds: b.advertised,
	}
}

// heartbeat queues one liveness line to a single subscriber.
//
// It carries `heartbeat_interval_seconds` — the interval this server is
// keeping NOW — because a stream that goes silent is indistinguishable from a
// healthy idle one, and since upstream Hoplock/proxy#56 the bound a proxy holds
// this server to is a claim read off the wire rather than a number in its
// configuration. The claim and the timer are the same value: see
// [Bus.HeartbeatInterval].
//
// Its id names the cursor's head: everything at or before it that was
// addressed to this subscriber is already ahead of it in the queue.
func (b *Bus) heartbeat(sub *subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	c := b.cursors[sub.tenant]
	b.lines++
	ev := &contract.RevocationEvent{
		EventID:                  positionID(c.epoch, c.head, b.lines),
		Type:                     contract.EventTypeHeartbeat,
		Timestamp:                b.now().UTC().Format(time.RFC3339),
		HeartbeatIntervalSeconds: b.advertised,
	}
	select {
	case sub.ch <- ev:
	default:
		// Not reading its queue AND owed a heartbeat: this subscriber is
		// behind by every measure this server has.
		sub.evict()
	}
}
