// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// The log: every event a proxy may be replayed, in one order per tenant, and the
// floor below which a reconnect is owed `resync` (M9).
//
// It is the authority for what was published. A node fans out by reading it
// "after the last position I delivered", whichever node published, so what one
// node delivers and what another replays are the same events in the same order.
// Two implementations sit behind one interface and the code above it does not
// branch on which: the store's (0027), which every node of a deployment shares
// and which survives a restart, and one in this process, which is what a bus
// built without a store keeps and which is exactly the broker every release
// before 0027 ran — its ids carry a per-process epoch, so a restart is a resync.

// logged is one event as the log holds it: its position, its audience — which
// decides replay exactly as it decided delivery — and the event without its id
// or its heartbeat interval, which the node writing the line supplies.
type logged struct {
	seq int64
	aud Audience
	ev  contract.RevocationEvent
	// at is when it was published; the store keeps it beside the event.
	at time.Time
}

// render is the line a subscriber is sent.
func (l logged) render(epoch string, advertised int32) *contract.RevocationEvent {
	ev := l.ev
	ev.EventID = eventID(epoch, l.seq)
	// The interval may be set on ANY event, not only a heartbeat, and a
	// reader takes it wherever it appears. Putting it on everything means a
	// proxy that reconnects into a burst of replay learns the interval on
	// its first line rather than on its first heartbeat.
	ev.HeartbeatIntervalSeconds = advertised
	return &ev
}

// position is one tenant's stream as the log keeps it.
type position struct {
	// epoch is in every id the stream hands out.
	epoch string
	// head is the last logged position, and floor the highest one pruned:
	// a subscriber resuming from before floor has missed an event that is
	// gone.
	head, floor int64
}

// eventLog is what the bus needs from a log.
type eventLog interface {
	// open returns a tenant's stream, creating it the first time.
	open(ctx context.Context, tenant store.Tenant) (position, error)
	// append logs one event, assigning its position, and prunes the
	// tenant's log to the retained count.
	append(ctx context.Context, tenant store.Tenant, e logged, origin string) (int64, position, error)
	// after returns the tenant's events after a position, in order, at most
	// limit, and none past through unless through is zero.
	after(ctx context.Context, tenant store.Tenant, after, through int64, limit int) ([]logged, error)
}

// ---------------------------------------------------------------------------
// the store's log
// ---------------------------------------------------------------------------

// storeLog is the log every node of a deployment shares (store.EventLogRepository).
type storeLog struct {
	st     *store.Store
	retain int
}

func (l storeLog) open(ctx context.Context, tenant store.Tenant) (position, error) {
	st, err := l.st.EventLog().Open(ctx, tenant)
	if err != nil {
		return position{}, err
	}
	return position{epoch: st.Epoch, head: st.Head, floor: st.Floor}, nil
}

func (l storeLog) append(ctx context.Context, tenant store.Tenant, e logged, origin string) (int64, position, error) {
	ev := e.ev
	ev.EventID, ev.HeartbeatIntervalSeconds = "", 0
	body, err := json.Marshal(ev)
	if err != nil {
		return 0, position{}, fmt.Errorf("revoke: encode event: %w", err)
	}
	entry, st, err := l.st.EventLog().Append(ctx, tenant, store.EventLogEntry{
		AudienceAll:     e.aud.All,
		AudienceProxies: e.aud.ProxyIDs,
		Body:            body,
		PublishedAt:     e.at,
		Origin:          origin,
	}, l.retain)
	if err != nil {
		return 0, position{}, err
	}
	return entry.Seq, position{epoch: st.Epoch, head: st.Head, floor: st.Floor}, nil
}

func (l storeLog) after(ctx context.Context, tenant store.Tenant, after, through int64, limit int) ([]logged, error) {
	entries, err := l.st.EventLog().After(ctx, tenant, after, through, limit)
	if err != nil {
		return nil, err
	}
	out := make([]logged, 0, len(entries))
	for _, e := range entries {
		var ev contract.RevocationEvent
		if err := json.Unmarshal(e.Body, &ev); err != nil {
			// A row this server wrote and cannot read is an outage, never
			// a skip: skipping it would be the silent gap M9 forbids.
			return nil, fmt.Errorf("revoke: logged event %d does not decode: %w", e.Seq, err)
		}
		out = append(out, logged{
			seq: e.Seq,
			aud: Audience{All: e.AudienceAll, ProxyIDs: e.AudienceProxies},
			ev:  ev,
			at:  e.PublishedAt,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// the log in this process
// ---------------------------------------------------------------------------

// memoryLog is what a bus with no store keeps: one order for the process, and a
// ring per tenant. Its epoch is drawn per process, so an id minted before a
// restart is unknown rather than believed — the ring is gone, and answering
// `resync` is the contract's own answer for a server that keeps no history.
type memoryLog struct {
	epoch  string
	retain int

	mu      sync.Mutex
	seq     int64
	streams map[store.Tenant]*memoryStream
}

type memoryStream struct {
	head, floor int64
	ring        []logged
}

func newMemoryLog(epoch string, retain int) *memoryLog {
	return &memoryLog{epoch: epoch, retain: retain, streams: make(map[store.Tenant]*memoryStream)}
}

func (l *memoryLog) streamLocked(tenant store.Tenant) *memoryStream {
	s := l.streams[tenant]
	if s == nil {
		s = &memoryStream{}
		l.streams[tenant] = s
	}
	return s
}

func (l *memoryLog) open(_ context.Context, tenant store.Tenant) (position, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.streamLocked(tenant)
	return position{epoch: l.epoch, head: s.head, floor: s.floor}, nil
}

func (l *memoryLog) append(_ context.Context, tenant store.Tenant, e logged, _ string) (int64, position, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.streamLocked(tenant)
	l.seq++
	e.seq = l.seq
	s.ring = append(s.ring, e)
	for len(s.ring) > l.retain {
		s.floor = s.ring[0].seq
		s.ring = s.ring[1:]
	}
	// The slice is re-cut rather than copied down, so reclaim the backing
	// array when it has grown well past what is retained.
	if cap(s.ring) > 4*l.retain && len(s.ring) == l.retain {
		fresh := make([]logged, l.retain, 2*l.retain)
		copy(fresh, s.ring)
		s.ring = fresh
	}
	s.head = e.seq
	return e.seq, position{epoch: l.epoch, head: s.head, floor: s.floor}, nil
}

func (l *memoryLog) after(_ context.Context, tenant store.Tenant, after, through int64, limit int) ([]logged, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []logged
	for _, e := range l.streamLocked(tenant).ring {
		if e.seq <= after || (through != 0 && e.seq > through) {
			continue
		}
		out = append(out, e)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// event ids
// ---------------------------------------------------------------------------

// eventID renders a logged event's position as the opaque id the proxy echoes
// back.
//
// The epoch is in it because a position means something only in the log that
// assigned it: an id minted by another log — the in-process broker of a release
// before 0027, a restarted process's memory, another deployment's database —
// names a position in an order this one does not have, and reading it as a
// position in THIS order would replay from the wrong place or resume live
// delivery over a gap nobody could see.
func eventID(epoch string, seq int64) string {
	return fmt.Sprintf("evt-%s-%012d", epoch, seq)
}

// positionID renders a line that names a position without taking one: a
// heartbeat, or the `resync` answering one reconnect. A proxy resuming from it
// is replayed everything after pos, which is everything it was not yet sent,
// because the line was queued behind every event at or before pos.
//
// line numbers such lines on this node, so two at one position are two ids,
// and an id a subscriber is sent is always greater than the one before it: the
// suffix sorts after the bare position and before the next one.
func positionID(epoch string, pos int64, line uint64) string {
	return fmt.Sprintf("evt-%s-%012d.%012d", epoch, pos, line)
}

// parseEventID reads an id this server minted, as its epoch and the position it
// names. Anything else is unknown, which is a resync rather than an error: the
// proxy echoes an opaque string back and is not at fault for having one this
// log does not recognise.
func parseEventID(id string) (epoch string, pos int64, ok bool) {
	const prefix = "evt-"
	if !strings.HasPrefix(id, prefix) {
		return "", 0, false
	}
	rest := id[len(prefix):]
	dash := strings.IndexByte(rest, '-')
	if dash <= 0 {
		return "", 0, false
	}
	epoch, digits := rest[:dash], rest[dash+1:]
	line := false
	if dot := strings.IndexByte(digits, '.'); dot >= 0 {
		if _, err := strconv.ParseUint(digits[dot+1:], 10, 64); err != nil {
			return "", 0, false
		}
		digits, line = digits[:dot], true
	}
	pos, err := strconv.ParseInt(digits, 10, 64)
	// A logged event's position is never zero; a line's may be, when
	// nothing has been logged in the tenant yet.
	if err != nil || pos < 0 || (pos == 0 && !line) {
		return "", 0, false
	}
	return epoch, pos, true
}
