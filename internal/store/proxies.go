// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"time"
)

type proxyRepo struct{ s *Store }

const proxyColumns = `proxy_id, zone, public_key, enrollment_state, last_heartbeat_at,
	contract_version, declared_capabilities, session_count, last_error, last_error_at,
	created_at, updated_at`

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

func (r proxyRepo) List(ctx context.Context, tenant Tenant) ([]Proxy, error) {
	const op = "store.Proxies.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// The graph load (0006). The fleet is small relative to the estate and
	// pathfinding needs all of it at once, so this is one query rather than
	// a walk per zone.
	rows, err := r.s.db.Query(ctx, `
		SELECT `+proxyColumns+`
		FROM proxies
		WHERE tenant = $1
		ORDER BY proxy_id`, tenant)
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
		INSERT INTO proxies (tenant, proxy_id, zone, public_key, enrollment_state, last_heartbeat_at,
			contract_version, declared_capabilities)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant, proxy_id) DO UPDATE SET
			zone                  = EXCLUDED.zone,
			public_key            = EXCLUDED.public_key,
			enrollment_state      = EXCLUDED.enrollment_state,
			last_heartbeat_at     = COALESCE(EXCLUDED.last_heartbeat_at, proxies.last_heartbeat_at),
			contract_version      = EXCLUDED.contract_version,
			declared_capabilities = EXCLUDED.declared_capabilities,
			updated_at            = now()`,
		tenant, p.ID, p.Zone, nonNilBytes(p.PublicKey), string(p.State), nullableTime(p.LastHeartbeatAt),
		p.ContractVersion, nonNilJSON(p.DeclaredCapabilities))
	return wrap(op, err)
}

// RecordHealth stamps the liveness and health a heartbeat carried.
//
// It is one statement rather than a read-modify-write because a heartbeat is
// the most frequent write in this table and two nodes may receive consecutive
// ones from the same proxy. The declared capability set rides on it because a
// proxy that has been upgraded advertises more and one that has been downgraded
// advertises less, and the freshness of that set is the freshness of the
// heartbeat that carried it — one lever, not two (M17).
func (r proxyRepo) RecordHealth(ctx context.Context, tenant Tenant, h ProxyHealthReport) error {
	const op = "store.Proxies.RecordHealth"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if h.ProxyID == "" {
		return invalid(op, "proxy id is required")
	}
	if h.At.IsZero() {
		return invalid(op, "heartbeat time is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// COALESCE on the capability set keeps the stored one when the caller
	// sends none: a heartbeat that says nothing about capabilities has not
	// withdrawn them.
	tag, err := r.s.db.Exec(ctx, `
		UPDATE proxies SET
			last_heartbeat_at     = $3,
			session_count         = $4,
			last_error            = $5,
			last_error_at         = CASE WHEN $5 = '' THEN last_error_at ELSE $3 END,
			contract_version      = COALESCE($6, contract_version),
			declared_capabilities = COALESCE($7, declared_capabilities),
			updated_at            = now()
		WHERE tenant = $1 AND proxy_id = $2`,
		tenant, h.ProxyID, h.At, h.SessionCount, h.LastError,
		nullableInt(h.ContractVersion), nullableJSON(h.DeclaredCapabilities))
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
		p           Proxy
		state       string
		heartbeat   *time.Time
		lastErrorAt *time.Time
		caps        json.RawMessage
	)
	err := row.Scan(&p.ID, &p.Zone, &p.PublicKey, &state, &heartbeat,
		&p.ContractVersion, &caps, &p.SessionCount, &p.LastError, &lastErrorAt,
		&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Proxy{}, wrap(op, err)
	}
	p.State = EnrollmentState(state)
	p.LastHeartbeatAt = timeOrZero(heartbeat)
	p.LastErrorAt = timeOrZero(lastErrorAt)
	p.DeclaredCapabilities = caps
	return p, nil
}
