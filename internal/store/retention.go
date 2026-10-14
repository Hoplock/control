// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Audit retention (0019, PLAN §7).
//
// THE ONE RULE IT MUST NOT BREAK. A record deleted out of the middle of a chain
// leaves its successor pointing at a hash nothing produces, which is exactly what
// tampering looks like. So a pass deletes a contiguous PREFIX of one stream and
// keeps, per stream, the sequence it deleted through and the hash of the last
// record it removed (`audit_retention`); the verifier resumes from that row.
//
// The head of a stream is never retained: it is what the next record chains
// onto, and a stream emptied entirely would restart at sequence 1 with no
// predecessor — a second chain under the first one's name.

// AuditRetention is where a stream's chain now starts.
type AuditRetention struct {
	Stream string
	// ThroughSeq is the last sequence deleted; the chain resumes at the next.
	ThroughSeq int64
	// ThroughHash is the hash of the last record deleted: what the first
	// remaining record's prev_hash must equal.
	ThroughHash string
	RetainedAt  time.Time
	RetainedBy  string
}

// ErrCaptureRetained is a capture whose bytes a retention pass removed. It is
// not ErrNotFound: "the bytes are gone, deliberately" and "there were never any,
// or someone removed them" are different answers, and only the second is a
// break.
var ErrCaptureRetained = errors.New("store: the capture's bytes were removed by retention")

// IsCaptureRetained reports whether err is ErrCaptureRetained.
func IsCaptureRetained(err error) bool { return errors.Is(err, ErrCaptureRetained) }

// Retention returns where a stream's chain starts after retention. A stream
// nothing has retained is ErrNotFound, and starts at sequence 1.
func (r auditRepo) Retention(ctx context.Context, tenant Tenant, stream string) (AuditRetention, error) {
	const op = "store.Audit.Retention"
	if err := checkTenant(op, tenant); err != nil {
		return AuditRetention{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	return r.retention(ctx, op, r.s.db, tenant, stream)
}

func (r auditRepo) retention(ctx context.Context, op string, q querier, tenant Tenant, stream string) (AuditRetention, error) {
	out := AuditRetention{Stream: stream}
	err := q.QueryRow(ctx, `
		SELECT through_seq, through_hash, retained_at, retained_by
		FROM audit_retention WHERE tenant = $1 AND stream = $2`, tenant, stream).
		Scan(&out.ThroughSeq, &out.ThroughHash, &out.RetainedAt, &out.RetainedBy)
	if err != nil {
		return AuditRetention{}, wrap(op, err)
	}
	return out, nil
}

// RetainPrefix deletes a stream's records through a sequence, and their
// captures, and moves the stream's retention row to that record. It takes the
// stream's append lock, so no record is appended while the prefix moves. It
// answers how many records it deleted.
//
// A sequence at or before the current retention point, past the head, or AT the
// head is refused as invalid.
func (r auditRepo) RetainPrefix(ctx context.Context, tenant Tenant, stream string, throughSeq int64, by string) (AuditRetention, int64, error) {
	const op = "store.Audit.RetainPrefix"
	if err := checkTenant(op, tenant); err != nil {
		return AuditRetention{}, 0, err
	}
	if stream == "" {
		return AuditRetention{}, 0, invalid(op, "stream is required")
	}
	if throughSeq <= 0 {
		return AuditRetention{}, 0, invalid(op, "through_seq must be positive")
	}

	var (
		marker  AuditRetention
		deleted int64
	)
	err := r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		if _, err := q.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, string(tenant), stream); err != nil {
			return wrap(op, err)
		}
		prior, err := r.retention(ctx, op, q, tenant, stream)
		if err != nil && !IsNotFound(err) {
			return err
		}
		if err == nil && throughSeq <= prior.ThroughSeq {
			return invalid(op, fmt.Sprintf("stream %s is already retained through %d", stream, prior.ThroughSeq))
		}
		head, err := r.chainHead(ctx, op, q, tenant, stream)
		if err != nil {
			return err
		}
		if throughSeq >= head.ChainSeq {
			return invalid(op, fmt.Sprintf("the head of stream %s is %d, and the head is never retained", stream, head.ChainSeq))
		}
		var hash string
		if err := q.QueryRow(ctx, `
			SELECT hash FROM audit_records
			WHERE tenant = $1 AND stream = $2 AND chain_seq = $3`, tenant, stream, throughSeq).Scan(&hash); err != nil {
			return wrap(op, err)
		}
		if _, err := q.Exec(ctx, `
			DELETE FROM audit_captures c
			USING audit_records a
			WHERE c.tenant = $1 AND a.tenant = $1 AND a.record_id = c.record_id
			  AND a.stream = $2 AND a.chain_seq <= $3`, tenant, stream, throughSeq); err != nil {
			return wrap(op, err)
		}
		tag, err := q.Exec(ctx, `
			DELETE FROM audit_records
			WHERE tenant = $1 AND stream = $2 AND chain_seq <= $3`, tenant, stream, throughSeq)
		if err != nil {
			return wrap(op, err)
		}
		deleted = tag.RowsAffected()
		return q.QueryRow(ctx, `
			INSERT INTO audit_retention (tenant, stream, through_seq, through_hash, retained_by)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (tenant, stream) DO UPDATE
			SET through_seq = EXCLUDED.through_seq, through_hash = EXCLUDED.through_hash,
			    retained_at = now(), retained_by = EXCLUDED.retained_by
			RETURNING through_seq, through_hash, retained_at, retained_by`,
			tenant, stream, throughSeq, hash, by).
			Scan(&marker.ThroughSeq, &marker.ThroughHash, &marker.RetainedAt, &marker.RetainedBy)
	})
	if err != nil {
		return AuditRetention{}, 0, wrapExisting(op, err)
	}
	marker.Stream = stream
	return marker, deleted, nil
}

// RetainCaptures removes the bytes of every capture whose record was recorded
// before an instant, keeping the record — whose digest the chain covers — and a
// mark that the bytes are gone by retention. It answers how many it emptied.
func (r auditRepo) RetainCaptures(ctx context.Context, tenant Tenant, recordedBefore time.Time) (int64, error) {
	const op = "store.Audit.RetainCaptures"
	if err := checkTenant(op, tenant); err != nil {
		return 0, err
	}
	if recordedBefore.IsZero() {
		return 0, invalid(op, "an instant is required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	tag, err := r.s.db.Exec(ctx, `
		UPDATE audit_captures c
		SET bytes = ''::bytea, retained_at = now()
		FROM audit_records a
		WHERE c.tenant = $1 AND a.tenant = $1 AND a.record_id = c.record_id
		  AND a.recorded_at < $2 AND c.retained_at IS NULL`, tenant, recordedBefore)
	if err != nil {
		return 0, wrap(op, err)
	}
	return tag.RowsAffected(), nil
}

// ThroughSeqBefore answers the last sequence of a stream that could be retained
// to drop everything received before an instant: the record just before the
// first one received at or after it, and never the head. Zero means nothing.
func (r auditRepo) ThroughSeqBefore(ctx context.Context, tenant Tenant, stream string, before time.Time) (int64, error) {
	const op = "store.Audit.ThroughSeqBefore"
	if err := checkTenant(op, tenant); err != nil {
		return 0, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	var through int64
	err := r.s.db.QueryRow(ctx, `
		SELECT COALESCE(
			(SELECT MIN(chain_seq) - 1 FROM audit_records
			 WHERE tenant = $1 AND stream = $2 AND received_at >= $3),
			(SELECT MAX(chain_seq) - 1 FROM audit_records WHERE tenant = $1 AND stream = $2),
			0)`, tenant, stream, before).Scan(&through)
	if err != nil {
		return 0, wrap(op, err)
	}
	return max(through, 0), nil
}

// wrapExisting keeps a store error a transaction body already classified.
func wrapExisting(op string, err error) error {
	var e *Error
	if errors.As(err, &e) {
		return err
	}
	return wrap(op, err)
}
