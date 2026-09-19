// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

type auditRepo struct{ s *Store }

const auditColumns = `record_id, stream, chain_seq, prev_hash, hash, session_id, kind, severity, payload, recorded_at, received_at`

// defaultAuditLimit bounds a chain walk when the caller asks for no limit.
const defaultAuditLimit = 500

func (r auditRepo) Append(ctx context.Context, tenant Tenant, rec AuditRecord) error {
	const op = "store.Audit.Append"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if rec.RecordID == "" {
		return invalid(op, "record id is required")
	}
	if rec.Stream == "" {
		return invalid(op, "stream is required")
	}
	if rec.ChainSeq <= 0 {
		return invalid(op, "chain sequence must be positive")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	recordedAt := rec.RecordedAt
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}

	// Deliberately no ON CONFLICT DO NOTHING.
	//
	// `record_id` is the idempotency key (M8) and a resend must be
	// *observably* a duplicate: LogBatchResponse.accepted says how many
	// records were stored, and "fewer than sent" is how the proxy sees its
	// replay being deduplicated. Swallowing the conflict here would make
	// every batch look fully accepted. 0010 counts the ErrConflict results
	// and reports the difference.
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO audit_records (tenant, record_id, stream, chain_seq, prev_hash, hash,
		                           session_id, kind, severity, payload, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		tenant, rec.RecordID, rec.Stream, rec.ChainSeq, rec.PrevHash, rec.Hash,
		rec.SessionID, rec.Kind, rec.Severity, nonNilJSON(rec.Payload), recordedAt)
	return wrap(op, err)
}

func (r auditRepo) Get(ctx context.Context, tenant Tenant, recordID string) (AuditRecord, error) {
	const op = "store.Audit.Get"
	if err := checkTenant(op, tenant); err != nil {
		return AuditRecord{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+auditColumns+`
		FROM audit_records
		WHERE tenant = $1 AND record_id = $2`, tenant, recordID)
	return scanAuditRecord(op, row)
}

func (r auditRepo) Chain(ctx context.Context, tenant Tenant, stream string, afterSeq int64, limit int) ([]AuditRecord, error) {
	const op = "store.Audit.Chain"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultAuditLimit
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// Ordered by chain_seq, never by arrival: two tenants' records
	// interleaved by arrival time still produce two chains that each verify
	// alone, because the chain is keyed per tenant per stream (M8, M18).
	rows, err := r.s.db.Query(ctx, `
		SELECT `+auditColumns+`
		FROM audit_records
		WHERE tenant = $1 AND stream = $2 AND chain_seq > $3
		ORDER BY chain_seq
		LIMIT $4`, tenant, stream, afterSeq, limit)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []AuditRecord
	for rows.Next() {
		rec, err := scanAuditRecord(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, wrap(op, rows.Err())
}

func (r auditRepo) ChainHead(ctx context.Context, tenant Tenant, stream string) (AuditRecord, error) {
	const op = "store.Audit.ChainHead"
	if err := checkTenant(op, tenant); err != nil {
		return AuditRecord{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+auditColumns+`
		FROM audit_records
		WHERE tenant = $1 AND stream = $2
		ORDER BY chain_seq DESC
		LIMIT 1`, tenant, stream)
	return scanAuditRecord(op, row)
}

func scanAuditRecord(op string, row rowScanner) (AuditRecord, error) {
	var rec AuditRecord
	err := row.Scan(&rec.RecordID, &rec.Stream, &rec.ChainSeq, &rec.PrevHash, &rec.Hash,
		&rec.SessionID, &rec.Kind, &rec.Severity, &rec.Payload,
		&rec.RecordedAt, &rec.ReceivedAt)
	if err != nil {
		return AuditRecord{}, wrap(op, err)
	}
	return rec, nil
}
