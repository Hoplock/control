// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/jackc/pgx/v5"
)

// The shared event plane (0027, PLAN M9, M5): the event stream's log and the
// set of subscriptions every node holds, so that a proxy is served the same
// stream whichever node it reaches.
//
// ORDER IS THE STREAM ROW'S LOCK. A publish locks its tenant's event_streams row
// before it takes a position from event_log_seq and holds it to its commit, so a
// tenant's positions commit in the order they were taken — and Postgres makes a
// commit visible before it releases its locks. A read that sees a tenant's
// position N therefore sees every earlier one of that tenant's that will ever
// exist: a node fanning out "after the last position I delivered" never skips
// an event that commits later. Positions of different tenants are not ordered
// against each other, and nothing needs them to be, because no subscription
// spans tenants (M18).

// EventStream is one tenant's stream as the log keeps it.
type EventStream struct {
	// Epoch is in every id the stream hands out; an id under another epoch
	// was not minted by this log.
	Epoch string
	// Head is the tenant's last logged position, zero with none.
	Head int64
	// Floor is the highest position pruned from the tenant's log, zero with
	// none: a subscriber resuming from before it has missed an event that is
	// gone.
	Floor int64
}

// EventLogEntry is one logged event.
type EventLogEntry struct {
	// Seq is its position, assigned by Append.
	Seq int64
	// AudienceAll addresses every subscriber in the tenant; otherwise
	// AudienceProxies lists the proxies it reaches.
	AudienceAll     bool
	AudienceProxies []string
	// Body is the event, encoded by the caller: the log does not need to
	// know the wire's shapes.
	Body []byte
	// PublishedAt is when it was published, and Origin the node that
	// published it.
	PublishedAt time.Time
	Origin      string
}

// EventSubscription is one proxy's live stream on one node.
type EventSubscription struct {
	ProxyID string
	NodeID  string
	// SeenAt is the subscription's last successful write, as its node last
	// recorded it.
	SeenAt time.Time
}

type eventLogRepo struct{ s *Store }

// EventLog returns the shared event plane's log (0027).
func (s *Store) EventLog() EventLogRepository { return eventLogRepo{s} }

// newEpoch draws the epoch a tenant's stream is created with.
func newEpoch() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// openStreamSQL creates a tenant's stream row the first time anything asks for
// it, and leaves one that exists alone.
const openStreamSQL = `INSERT INTO event_streams (tenant, epoch) VALUES ($1, $2) ON CONFLICT (tenant) DO NOTHING`

func (r eventLogRepo) Open(ctx context.Context, tenant Tenant) (EventStream, error) {
	const op = "store.EventLog.Open"
	if err := checkTenant(op, tenant); err != nil {
		return EventStream{}, err
	}
	epoch, err := newEpoch()
	if err != nil {
		return EventStream{}, wrap(op, err)
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	if _, err := r.s.db.Exec(ctx, openStreamSQL, tenant, epoch); err != nil {
		return EventStream{}, wrap(op, err)
	}
	var st EventStream
	err = r.s.db.QueryRow(ctx, `SELECT epoch, head_seq, floor_seq FROM event_streams WHERE tenant = $1`, tenant).
		Scan(&st.Epoch, &st.Head, &st.Floor)
	return st, wrap(op, err)
}

func (r eventLogRepo) Append(ctx context.Context, tenant Tenant, e EventLogEntry, retain int) (EventLogEntry, EventStream, error) {
	const op = "store.EventLog.Append"
	if err := checkTenant(op, tenant); err != nil {
		return EventLogEntry{}, EventStream{}, err
	}
	switch {
	case retain <= 0:
		return EventLogEntry{}, EventStream{}, invalid(op, "retain must be positive")
	case len(e.Body) == 0:
		return EventLogEntry{}, EventStream{}, invalid(op, "an event has a body")
	case !e.AudienceAll && len(e.AudienceProxies) == 0:
		return EventLogEntry{}, EventStream{}, invalid(op, "an event is addressed to at least one proxy, or to all of them")
	case e.PublishedAt.IsZero():
		return EventLogEntry{}, EventStream{}, invalid(op, "an event has a publication time")
	}
	if e.AudienceProxies == nil {
		e.AudienceProxies = []string{}
	}
	epoch, err := newEpoch()
	if err != nil {
		return EventLogEntry{}, EventStream{}, wrap(op, err)
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var st EventStream
	err = r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		if _, err := q.Exec(ctx, openStreamSQL, tenant, epoch); err != nil {
			return wrap(op, err)
		}
		// The tenant's publish lock, held to the commit: the position is
		// taken only once it is held, so the tenant's positions commit in
		// the order they were taken.
		if err := q.QueryRow(ctx, `SELECT epoch, head_seq, floor_seq FROM event_streams WHERE tenant = $1 FOR UPDATE`,
			tenant).Scan(&st.Epoch, &st.Head, &st.Floor); err != nil {
			return wrap(op, err)
		}
		if err := q.QueryRow(ctx, `SELECT nextval('event_log_seq')`).Scan(&e.Seq); err != nil {
			return wrap(op, err)
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO event_log (tenant, seq, audience_all, audience_proxies, body, published_at, origin)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			tenant, e.Seq, e.AudienceAll, e.AudienceProxies, string(e.Body), e.PublishedAt.UTC(), e.Origin); err != nil {
			return wrap(op, err)
		}
		// Keep the tenant's newest `retain` and record how far the prune
		// reached: the floor below which a reconnect is owed `resync`.
		var pruned int64
		if err := q.QueryRow(ctx, `
			WITH doomed AS (
				SELECT seq FROM event_log WHERE tenant = $1 ORDER BY seq DESC OFFSET $2
			), gone AS (
				DELETE FROM event_log l USING doomed d
				WHERE l.tenant = $1 AND l.seq = d.seq
				RETURNING l.seq
			)
			SELECT COALESCE(max(seq), 0) FROM gone`, tenant, retain).Scan(&pruned); err != nil {
			return wrap(op, err)
		}
		return wrap(op, q.QueryRow(ctx, `
			UPDATE event_streams SET head_seq = $2, floor_seq = GREATEST(floor_seq, $3)
			WHERE tenant = $1
			RETURNING epoch, head_seq, floor_seq`, tenant, e.Seq, pruned).Scan(&st.Epoch, &st.Head, &st.Floor))
	})
	if err != nil {
		return EventLogEntry{}, EventStream{}, err
	}
	return e, st, nil
}

func (r eventLogRepo) After(ctx context.Context, tenant Tenant, after, through int64, limit int) ([]EventLogEntry, error) {
	const op = "store.EventLog.After"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, invalid(op, "limit must be positive")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	rows, err := r.s.db.Query(ctx, `
		SELECT seq, audience_all, audience_proxies, body, published_at, origin
		FROM event_log
		WHERE tenant = $1 AND seq > $2 AND ($3 = 0 OR seq <= $3)
		ORDER BY seq
		LIMIT $4`, tenant, after, through, limit)
	if err != nil {
		return nil, wrap(op, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (EventLogEntry, error) {
		var (
			e    EventLogEntry
			body string
		)
		err := row.Scan(&e.Seq, &e.AudienceAll, &e.AudienceProxies, &body, &e.PublishedAt, &e.Origin)
		e.Body = []byte(body)
		e.PublishedAt = e.PublishedAt.UTC()
		return e, err
	})
	return out, wrap(op, err)
}

type eventSubscriptionRepo struct{ s *Store }

// EventSubscriptions returns which proxies hold a live stream on which node
// (0027).
func (s *Store) EventSubscriptions() EventSubscriptionRepository { return eventSubscriptionRepo{s} }

func (r eventSubscriptionRepo) Put(ctx context.Context, tenant Tenant, node string, subs []EventSubscription) error {
	const op = "store.EventSubscriptions.Put"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if node == "" {
		return invalid(op, "node is required")
	}
	if len(subs) == 0 {
		return nil
	}
	proxies := make([]string, len(subs))
	seen := make([]time.Time, len(subs))
	for i, s := range subs {
		if s.ProxyID == "" {
			return invalid(op, "proxy id is required")
		}
		proxies[i], seen[i] = s.ProxyID, s.SeenAt.UTC()
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	// A subscription's last write only moves forward, so a node never
	// writes a row older than the one it already stored.
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO event_subscriptions (tenant, proxy_id, node_id, seen_at)
		SELECT $1, u.proxy_id, $2, u.seen_at FROM unnest($3::text[], $4::timestamptz[]) AS u(proxy_id, seen_at)
		ON CONFLICT (tenant, proxy_id, node_id)
		DO UPDATE SET seen_at = GREATEST(event_subscriptions.seen_at, EXCLUDED.seen_at)`,
		tenant, node, proxies, seen)
	return wrap(op, err)
}

func (r eventSubscriptionRepo) Remove(ctx context.Context, tenant Tenant, node string, proxyIDs []string) error {
	const op = "store.EventSubscriptions.Remove"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if node == "" {
		return invalid(op, "node is required")
	}
	if len(proxyIDs) == 0 {
		return nil
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	_, err := r.s.db.Exec(ctx, `
		DELETE FROM event_subscriptions WHERE tenant = $1 AND node_id = $2 AND proxy_id = ANY($3)`,
		tenant, node, proxyIDs)
	return wrap(op, err)
}

func (r eventSubscriptionRepo) List(ctx context.Context, tenant Tenant) ([]EventSubscription, error) {
	const op = "store.EventSubscriptions.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	rows, err := r.s.db.Query(ctx, `
		SELECT proxy_id, node_id, seen_at FROM event_subscriptions
		WHERE tenant = $1 ORDER BY proxy_id, node_id`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (EventSubscription, error) {
		var s EventSubscription
		err := row.Scan(&s.ProxyID, &s.NodeID, &s.SeenAt)
		s.SeenAt = s.SeenAt.UTC()
		return s, err
	})
	return out, wrap(op, err)
}

func (r eventSubscriptionRepo) Expire(ctx context.Context, tenant Tenant, before time.Time) (int64, error) {
	const op = "store.EventSubscriptions.Expire"
	if err := checkTenant(op, tenant); err != nil {
		return 0, err
	}
	if before.IsZero() {
		return 0, invalid(op, "an instant is required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	tag, err := r.s.db.Exec(ctx, `DELETE FROM event_subscriptions WHERE tenant = $1 AND seen_at < $2`, tenant, before.UTC())
	if err != nil {
		return 0, wrap(op, err)
	}
	return tag.RowsAffected(), nil
}
