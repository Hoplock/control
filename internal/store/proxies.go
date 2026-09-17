// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

type proxyRepo struct{ s *Store }

const proxyColumns = `proxy_id, zone, public_key, enrollment_state, last_heartbeat_at, created_at, updated_at`

func (r proxyRepo) Get(ctx context.Context, tenant Tenant, proxyID string) (Proxy, error) {
	const op = "store.Proxies.Get"
	if err := checkTenant(op, tenant); err != nil {
		return Proxy{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// DECISION PATH (M5): "proxy by id", served by the primary key.
	row := r.s.db.QueryRow(ctx, `
		SELECT `+proxyColumns+`
		FROM proxies
		WHERE tenant = $1 AND proxy_id = $2`, tenant, proxyID)
	return scanProxy(op, row)
}

func (r proxyRepo) ListByZone(ctx context.Context, tenant Tenant, zone string) ([]Proxy, error) {
	const op = "store.Proxies.ListByZone"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+proxyColumns+`
		FROM proxies
		WHERE tenant = $1 AND zone = $2
		ORDER BY proxy_id`, tenant, zone)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []Proxy
	for rows.Next() {
		p, err := scanProxy(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, wrap(op, rows.Err())
}

func (r proxyRepo) Upsert(ctx context.Context, tenant Tenant, p Proxy) error {
	const op = "store.Proxies.Upsert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if p.ID == "" {
		return invalid(op, "proxy id is required")
	}
	if p.State == "" {
		return invalid(op, "proxy enrollment state is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// COALESCE keeps the stored heartbeat when the caller supplies none: a
	// re-enrollment describes the proxy, not its liveness, and a proxy that
	// has gone quiet must not look live because somebody edited its zone.
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO proxies (tenant, proxy_id, zone, public_key, enrollment_state, last_heartbeat_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant, proxy_id) DO UPDATE SET
			zone              = EXCLUDED.zone,
			public_key        = EXCLUDED.public_key,
			enrollment_state  = EXCLUDED.enrollment_state,
			last_heartbeat_at = COALESCE(EXCLUDED.last_heartbeat_at, proxies.last_heartbeat_at),
			updated_at        = now()`,
		tenant, p.ID, p.Zone, p.PublicKey, string(p.State), nullableTime(p.LastHeartbeatAt))
	return wrap(op, err)
}

func (r proxyRepo) RecordHeartbeat(ctx context.Context, tenant Tenant, proxyID string, t time.Time) error {
	const op = "store.Proxies.RecordHeartbeat"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if t.IsZero() {
		return invalid(op, "heartbeat time is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		UPDATE proxies
		SET last_heartbeat_at = $3, updated_at = now()
		WHERE tenant = $1 AND proxy_id = $2`, tenant, proxyID, t)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func (r proxyRepo) Delete(ctx context.Context, tenant Tenant, proxyID string) error {
	const op = "store.Proxies.Delete"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		DELETE FROM proxies WHERE tenant = $1 AND proxy_id = $2`, tenant, proxyID)
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

func scanProxy(op string, row rowScanner) (Proxy, error) {
	var (
		p         Proxy
		state     string
		heartbeat *time.Time
	)
	err := row.Scan(&p.ID, &p.Zone, &p.PublicKey, &state, &heartbeat, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Proxy{}, wrap(op, err)
	}
	p.State = EnrollmentState(state)
	p.LastHeartbeatAt = timeOrZero(heartbeat)
	return p, nil
}
