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
// side and is the one behaviour worse than both.

// Subscribe serves one long-lived subscription and blocks until it ends.
//
// emit is called for every event, in order, and its first error ends the
// stream. Subscribe returns nil when the stream ended for a reason that is not
// a failure — the subscriber went away, or the bus was drained for a deploy —
// and an error otherwise.
//
// The registration and the replay decision are taken under ONE lock
// acquisition, which is what makes "nothing is skipped" true rather than
// likely: an event published after registration is queued for this subscriber,
// and the replay covers exactly up to the point registration happened. Doing
// the two in either order without the lock leaves a window that drops an event
// or delivers it twice, and the proxy cannot tell which happened.
func (b *Bus) Subscribe(ctx context.Context, tenant store.Tenant, proxyID, lastEventID string, emit func(*contract.RevocationEvent) error) error {
	if tenant == "" {
		return fmt.Errorf("revoke: a tenant is required")
	}
	if proxyID == "" {
		return fmt.Errorf("revoke: a proxy id is required")
	}

	sub := &subscription{
		tenant:  tenant,
		proxyID: proxyID,
		ch:      make(chan *contract.RevocationEvent, b.queueSize),
		dropped: make(chan struct{}),
		drained: make(chan struct{}),
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	replay, resync := b.planLocked(tenant, proxyID, lastEventID)
	sub.touch(b.now())
	b.subs[sub] = struct{}{}
	b.live.Add(1)
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.subs, sub)
		b.mu.Unlock()
		b.live.Done()
	}()

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
	for _, ev := range replay {
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

// planLocked decides what a reconnecting subscriber is owed.
//
// Three answers, and the contract names all three: replay everything after the
// id, in order; answer `resync` when the id is too old, unknown, or no history
// is kept; or — for an absent id — start from now and replay nothing.
// The resync line, when there is one, is minted HERE rather than by the caller
// afterwards: its id comes from the same sequence as every other event, and an
// id minted after the lock was released could be lower than one already queued
// to this subscriber — which would hand the proxy a resume point it had not
// reached.
func (b *Bus) planLocked(tenant store.Tenant, proxyID, lastEventID string) (replay []*contract.RevocationEvent, resync *contract.RevocationEvent) {
	if lastEventID == "" {
		// A fresh subscription. Starting from now is what the contract
		// says, and it is also the only safe reading: this server does
		// not know what the proxy already holds.
		return nil, nil
	}

	runID, seq, ok := parseEventID(lastEventID)
	switch {
	case !ok, runID != b.runID:
		// Unknown: a malformed id, or one minted before a restart. The
		// ring is in memory, so there is no history to replay from and
		// no way to know what was missed.
		return nil, b.mintResyncLocked()
	case seq > b.seq:
		// An id from an order this run never reached. Believing it
		// would resume live delivery past events that do exist.
		return nil, b.mintResyncLocked()
	case seq < b.evicted:
		// Too old: a retained event after this id has already fallen
		// out of the ring, so the replay would have a hole in it.
		return nil, b.mintResyncLocked()
	}

	for _, s := range b.ring {
		if s.seq <= seq || s.tenant != tenant || !s.aud.reaches(proxyID) {
			continue
		}
		ev := s.ev
		replay = append(replay, &ev)
	}
	return replay, nil
}

// mintResyncLocked builds the resync line. It takes an id from the same
// sequence as every other event, so a proxy that processes it and then
// reconnects resumes from a point this server understands.
func (b *Bus) mintResyncLocked() *contract.RevocationEvent {
	_, id := b.mintLocked()
	return &contract.RevocationEvent{
		EventID:                  id,
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
func (b *Bus) heartbeat(sub *subscription) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	_, id := b.mintLocked()
	ev := &contract.RevocationEvent{
		EventID:                  id,
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
	b.mu.Unlock()
}
