// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

// What each proxy build offers, and what each target negotiates
// (Hoplock/proxy#69, #72). Migration 0012 says why each is keyed as it is.

// ProxyAlgorithmDeclaration is one proxy's latest declared algorithm offer.
type ProxyAlgorithmDeclaration struct {
	ProxyID string
	// Declaration is the JSON of the three lists, under the contract's
	// names. The fleet package owns the shape.
	Declaration []byte
	// Digest identifies Declaration; a write naming the stored digest
	// changes nothing.
	Digest     string
	DeclaredAt time.Time
}

// ProxyAlgorithmDeclarationRepository stores what each proxy build offers.
type ProxyAlgorithmDeclarationRepository interface {
	// Put writes a declaration unless the stored one has the same digest, and
	// reports whether it wrote.
	Put(ctx context.Context, tenant Tenant, d ProxyAlgorithmDeclaration) (bool, error)
	// List returns every proxy's latest declaration, by proxy id.
	List(ctx context.Context, tenant Tenant) ([]ProxyAlgorithmDeclaration, error)
}

// TargetKexObservation is one target's key exchange, as a proxy saw it on a
// handshake. It grants nothing.
type TargetKexObservation struct {
	Hostname   string
	Port       int32
	FloorMet   string
	Negotiated string
	Offered    []string
	ObservedAt time.Time
	ReportedBy string
	ReceivedAt time.Time
}

// TargetKexObservationRepository stores the key-exchange observation, beside
// and never over the rung observation in TargetCapabilities.
type TargetKexObservationRepository interface {
	// Put replaces the observation for one host and port.
	Put(ctx context.Context, tenant Tenant, o TargetKexObservation) error
	// Get returns one. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, hostname string, port int32) (TargetKexObservation, error)
	// List returns every observation in the tenant.
	List(ctx context.Context, tenant Tenant) ([]TargetKexObservation, error)
}

// ProxyAlgorithmDeclarations returns the per-proxy declaration repository.
func (s *Store) ProxyAlgorithmDeclarations() ProxyAlgorithmDeclarationRepository {
	return algorithmDeclarationRepo{s}
}

// TargetKexObservations returns the key-exchange observation repository.
func (s *Store) TargetKexObservations() TargetKexObservationRepository { return kexRepo{s} }

type algorithmDeclarationRepo struct{ s *Store }

func (r algorithmDeclarationRepo) Put(ctx context.Context, tenant Tenant, d ProxyAlgorithmDeclaration) (bool, error) {
	const op = "store.ProxyAlgorithmDeclarations.Put"
	if err := checkTenant(op, tenant); err != nil {
		return false, err
	}
	if d.ProxyID == "" || d.Digest == "" {
		return false, invalid(op, "proxy id and digest are required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	tag, err := r.s.db.Exec(ctx, `
		INSERT INTO proxy_algorithm_declarations (tenant, proxy_id, declaration, digest, declared_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant, proxy_id) DO UPDATE SET
			declaration = EXCLUDED.declaration,
			digest      = EXCLUDED.digest,
			declared_at = EXCLUDED.declared_at
		WHERE proxy_algorithm_declarations.digest IS DISTINCT FROM EXCLUDED.digest`,
		tenant, d.ProxyID, string(d.Declaration), d.Digest, d.DeclaredAt)
	if err != nil {
		return false, wrap(op, err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r algorithmDeclarationRepo) List(ctx context.Context, tenant Tenant) ([]ProxyAlgorithmDeclaration, error) {
	const op = "store.ProxyAlgorithmDeclarations.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	rows, err := r.s.db.Query(ctx, `
		SELECT proxy_id, declaration, digest, declared_at
		FROM proxy_algorithm_declarations WHERE tenant = $1 ORDER BY proxy_id`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []ProxyAlgorithmDeclaration
	for rows.Next() {
		var (
			d    ProxyAlgorithmDeclaration
			body string
		)
		if err := rows.Scan(&d.ProxyID, &body, &d.Digest, &d.DeclaredAt); err != nil {
			return nil, wrap(op, err)
		}
		d.Declaration = []byte(body)
		out = append(out, d)
	}
	return out, wrap(op, rows.Err())
}

type kexRepo struct{ s *Store }

const kexColumns = `hostname, target_port, floor_met, negotiated, offered, observed_at, reported_by, received_at`

func (r kexRepo) Put(ctx context.Context, tenant Tenant, o TargetKexObservation) error {
	const op = "store.TargetKexObservations.Put"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if o.Hostname == "" || o.FloorMet == "" || o.ObservedAt.IsZero() {
		return invalid(op, "hostname, floor_met and observed_at are required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO target_kex_observations (tenant, `+kexColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, now()))
		ON CONFLICT (tenant, hostname, target_port) DO UPDATE SET
			floor_met   = EXCLUDED.floor_met,
			negotiated  = EXCLUDED.negotiated,
			offered     = EXCLUDED.offered,
			observed_at = EXCLUDED.observed_at,
			reported_by = EXCLUDED.reported_by,
			received_at = EXCLUDED.received_at`,
		tenant, o.Hostname, o.Port, o.FloorMet, o.Negotiated, nonNilStrings(o.Offered),
		o.ObservedAt, o.ReportedBy, nullableTime(o.ReceivedAt))
	return wrap(op, err)
}

func (r kexRepo) Get(ctx context.Context, tenant Tenant, hostname string, port int32) (TargetKexObservation, error) {
	const op = "store.TargetKexObservations.Get"
	if err := checkTenant(op, tenant); err != nil {
		return TargetKexObservation{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	row := r.s.db.QueryRow(ctx, `SELECT `+kexColumns+` FROM target_kex_observations
		WHERE tenant = $1 AND hostname = $2 AND target_port = $3`, tenant, hostname, port)
	return scanKex(op, row)
}

func (r kexRepo) List(ctx context.Context, tenant Tenant) ([]TargetKexObservation, error) {
	const op = "store.TargetKexObservations.List"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	rows, err := r.s.db.Query(ctx, `SELECT `+kexColumns+` FROM target_kex_observations
		WHERE tenant = $1 ORDER BY hostname, target_port`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []TargetKexObservation
	for rows.Next() {
		o, err := scanKex(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, wrap(op, rows.Err())
}

func scanKex(op string, row rowScanner) (TargetKexObservation, error) {
	var o TargetKexObservation
	if err := row.Scan(&o.Hostname, &o.Port, &o.FloorMet, &o.Negotiated, &o.Offered,
		&o.ObservedAt, &o.ReportedBy, &o.ReceivedAt); err != nil {
		return TargetKexObservation{}, wrap(op, err)
	}
	return o, nil
}
