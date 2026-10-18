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
	// KeyName and Signature are the seal the pass wrote (0024): the key
	// store's name for the tenant's key that signed, and the signature over
	// the tenant, stream, ThroughSeq and ThroughHash. Both are empty on a
	// marker no pass signed — one written before 0024, or by something other
	// than a pass — which the verifier reports and never trusts.
	KeyName   string
	Signature string
}

// RetentionSeal is what a retention pass writes beside what it retained: the
// key that signed and the signature. The store only carries it. What is signed,
// and with which key, is internal/audit's, because what the seal is worth is
// that the database cannot make one (PLAN §7).
type RetentionSeal struct {
	KeyName   string
	Signature string
}

// RetainPrefixOptions carries what a pass needs beyond the sequence.
type RetainPrefixOptions struct {
	// By is the principal the marker records.
	By string
	// Check runs under the stream's append lock, before anything is deleted,
	// with the marker the pass would move (nil when the stream has none) and
	// the record the stream's chain starts at now. Its error refuses the
	// pass whole. It is how a pass refuses to sign over a marker or a start
	// that does not verify: a new signature over an unchecked one would
	// launder it.
	Check func(ctx context.Context, prior *AuditRetention, first AuditRecord) error
	// Seal signs the marker the pass writes, given where it will stand. It
	// is required: the server writes no marker it has not signed.
	Seal func(ctx context.Context, throughSeq int64, throughHash string) (RetentionSeal, error)
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
		SELECT through_seq, through_hash, retained_at, retained_by, key_name, signature
		FROM audit_retention WHERE tenant = $1 AND stream = $2`, tenant, stream).
		Scan(&out.ThroughSeq, &out.ThroughHash, &out.RetainedAt, &out.RetainedBy, &out.KeyName, &out.Signature)
	if err != nil {
		return AuditRetention{}, wrap(op, err)
	}
	return out, nil
}

// RetainPrefix deletes a stream's records through a sequence, and their
// captures, and moves the stream's retention row to that record with the seal
// o.Seal made over it. It takes the stream's append lock, so no record is
// appended while the prefix moves. It answers how many records it deleted.
//
// A sequence at or before the current retention point, past the head, or AT the
// head is refused as invalid, and so is a pass with no Seal.
func (r auditRepo) RetainPrefix(ctx context.Context, tenant Tenant, stream string, throughSeq int64, o RetainPrefixOptions) (AuditRetention, int64, error) {
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
	if o.Seal == nil {
		return AuditRetention{}, 0, invalid(op, "a pass writes no marker it has not signed")
	}

	var (
		marker  AuditRetention
		deleted int64
	)
	err := r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		if err := lockStream(ctx, op, q, tenant, stream); err != nil {
			return err
		}
		var prior *AuditRetention
		switch got, err := r.retention(ctx, op, q, tenant, stream); {
		case err == nil:
			prior = &got
		case !IsNotFound(err):
			return err
		}
		if prior != nil && throughSeq <= prior.ThroughSeq {
			return invalid(op, fmt.Sprintf("stream %s is already retained through %d", stream, prior.ThroughSeq))
		}
		head, err := r.chainHead(ctx, op, q, tenant, stream)
		if err != nil {
			return err
		}
		if throughSeq >= head.ChainSeq {
			return invalid(op, fmt.Sprintf("the head of stream %s is %d, and the head is never retained", stream, head.ChainSeq))
		}
		if o.Check != nil {
			first, err := r.chainStart(ctx, op, q, tenant, stream)
			if err != nil {
				return err
			}
			if err := o.Check(ctx, prior, first); err != nil {
				return err
			}
		}
		var hash string
		if err := q.QueryRow(ctx, `
			SELECT hash FROM audit_records
			WHERE tenant = $1 AND stream = $2 AND chain_seq = $3`, tenant, stream, throughSeq).Scan(&hash); err != nil {
			return wrap(op, err)
		}
		seal, err := o.Seal(ctx, throughSeq, hash)
		if err != nil {
			return err
		}
		if seal.KeyName == "" || seal.Signature == "" {
			return invalid(op, "a pass writes no marker it has not signed")
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
			INSERT INTO audit_retention (tenant, stream, through_seq, through_hash, retained_by, key_name, signature)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (tenant, stream) DO UPDATE
			SET through_seq = EXCLUDED.through_seq, through_hash = EXCLUDED.through_hash,
			    retained_at = now(), retained_by = EXCLUDED.retained_by,
			    key_name = EXCLUDED.key_name, signature = EXCLUDED.signature
			RETURNING through_seq, through_hash, retained_at, retained_by, key_name, signature`,
			tenant, stream, throughSeq, hash, o.By, seal.KeyName, seal.Signature).
			Scan(&marker.ThroughSeq, &marker.ThroughHash, &marker.RetainedAt, &marker.RetainedBy,
				&marker.KeyName, &marker.Signature)
	})
	if err != nil {
		return AuditRetention{}, 0, wrapExisting(op, err)
	}
	marker.Stream = stream
	return marker, deleted, nil
}

// chainStart is the record a stream's chain starts at now: the lowest sequence
// still stored.
func (r auditRepo) chainStart(ctx context.Context, op string, q querier, tenant Tenant, stream string) (AuditRecord, error) {
	row := q.QueryRow(ctx, `
		SELECT `+auditColumns+`
		FROM audit_records
		WHERE tenant = $1 AND stream = $2
		ORDER BY chain_seq
		LIMIT 1`, tenant, stream)
	return scanAuditRecord(op, row)
}

// SealRetention writes a seal on a marker that has none: the adoption of a
// marker written before 0024, after an operator has verified the stream from
// it. It takes the stream's append lock, and it is refused as a conflict unless
// the marker exists, carries no seal, and still names throughSeq and
// throughHash — so it can never seal a marker other than the one that was
// checked, nor replace a seal a pass wrote.
func (r auditRepo) SealRetention(ctx context.Context, tenant Tenant, stream string, throughSeq int64, throughHash string, seal RetentionSeal) (AuditRetention, error) {
	const op = "store.Audit.SealRetention"
	if err := checkTenant(op, tenant); err != nil {
		return AuditRetention{}, err
	}
	if stream == "" || throughSeq <= 0 || throughHash == "" {
		return AuditRetention{}, invalid(op, "stream, through_seq and through_hash are required")
	}
	if seal.KeyName == "" || seal.Signature == "" {
		return AuditRetention{}, invalid(op, "a seal names its key and carries its signature")
	}
	var out AuditRetention
	err := r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		if err := lockStream(ctx, op, q, tenant, stream); err != nil {
			return err
		}
		tag, err := q.Exec(ctx, `
			UPDATE audit_retention SET key_name = $5, signature = $6
			WHERE tenant = $1 AND stream = $2 AND through_seq = $3 AND through_hash = $4
			  AND signature = ''`, tenant, stream, throughSeq, throughHash, seal.KeyName, seal.Signature)
		if err != nil {
			return wrap(op, err)
		}
		if tag.RowsAffected() == 0 {
			return &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf(
				"stream %s has no unsigned marker through %d with hash %s", stream, throughSeq, throughHash)}
		}
		out, err = r.retention(ctx, op, q, tenant, stream)
		return err
	})
	if err != nil {
		return AuditRetention{}, wrapExisting(op, err)
	}
	return out, nil
}

// CaptureRetention is how far capture retention has lawfully reached in a
// tenant (0024): every capture whose record was recorded before Before may have
// had its bytes emptied by a pass, and the seal says a pass signed that.
type CaptureRetention struct {
	Before     time.Time
	KeyName    string
	Signature  string
	RetainedAt time.Time
	RetainedBy string
}

// RetainCapturesOptions carries what a capture pass needs beyond the instant.
type RetainCapturesOptions struct {
	// By is the principal the watermark records.
	By string
	// Check runs under the tenant's capture-retention lock, before anything
	// is emptied, with the tenant's watermark (nil when none) and how many
	// captures are already marked retained outside it. Its error refuses
	// the pass whole, for the reason RetainPrefixOptions.Check does.
	Check func(ctx context.Context, prior *CaptureRetention, outside int64) error
	// Seal signs the watermark when the pass moves it forward. It is
	// required.
	Seal func(ctx context.Context, before time.Time) (RetentionSeal, error)
}

// captureRetentionLockPrefix keys the advisory lock capture passes take, so a
// pass's check, its emptying and its watermark are one step.
const captureRetentionLockPrefix = "audit.capture_retention:"

// CaptureRetention returns the tenant's capture retention watermark. A tenant
// no signed capture pass has reached is ErrNotFound.
func (r auditRepo) CaptureRetention(ctx context.Context, tenant Tenant) (CaptureRetention, error) {
	const op = "store.Audit.CaptureRetention"
	if err := checkTenant(op, tenant); err != nil {
		return CaptureRetention{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	return r.captureRetention(ctx, op, r.s.db, tenant)
}

func (r auditRepo) captureRetention(ctx context.Context, op string, q querier, tenant Tenant) (CaptureRetention, error) {
	var out CaptureRetention
	err := q.QueryRow(ctx, `
		SELECT retained_before, key_name, signature, retained_at, retained_by
		FROM audit_capture_retention WHERE tenant = $1`, tenant).
		Scan(&out.Before, &out.KeyName, &out.Signature, &out.RetainedAt, &out.RetainedBy)
	if err != nil {
		return CaptureRetention{}, wrap(op, err)
	}
	return out, nil
}

// retainedOutside counts the captures marked retained whose record was
// recorded at or after an instant, or every one when there is no instant.
func (r auditRepo) retainedOutside(ctx context.Context, op string, q querier, tenant Tenant, prior *CaptureRetention) (int64, error) {
	var from *time.Time
	if prior != nil {
		from = &prior.Before
	}
	var n int64
	err := q.QueryRow(ctx, `
		SELECT count(*) FROM audit_captures c
		JOIN audit_records a ON a.tenant = c.tenant AND a.record_id = c.record_id
		WHERE c.tenant = $1 AND c.retained_at IS NOT NULL
		  AND ($2::timestamptz IS NULL OR a.recorded_at >= $2)`, tenant, from).Scan(&n)
	return n, wrap(op, err)
}

func lockCaptureRetention(ctx context.Context, op string, q querier, tenant Tenant) error {
	_, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		captureRetentionLockPrefix+string(tenant))
	return wrap(op, err)
}

// RetainCaptures removes the bytes of every capture whose record was recorded
// before an instant, keeping the record — whose digest the chain covers — and a
// mark that the bytes are gone by retention, and moves the tenant's watermark
// forward to the instant with the seal o.Seal made over it. A pass at or behind
// the watermark empties what it covers and signs nothing new. It answers how
// many captures it emptied.
func (r auditRepo) RetainCaptures(ctx context.Context, tenant Tenant, recordedBefore time.Time, o RetainCapturesOptions) (int64, error) {
	const op = "store.Audit.RetainCaptures"
	if err := checkTenant(op, tenant); err != nil {
		return 0, err
	}
	if recordedBefore.IsZero() {
		return 0, invalid(op, "an instant is required")
	}
	if o.Seal == nil {
		return 0, invalid(op, "a pass writes no watermark it has not signed")
	}
	var emptied int64
	err := r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		if err := lockCaptureRetention(ctx, op, q, tenant); err != nil {
			return err
		}
		var prior *CaptureRetention
		switch got, err := r.captureRetention(ctx, op, q, tenant); {
		case err == nil:
			prior = &got
		case !IsNotFound(err):
			return err
		}
		if o.Check != nil {
			outside, err := r.retainedOutside(ctx, op, q, tenant, prior)
			if err != nil {
				return err
			}
			if err := o.Check(ctx, prior, outside); err != nil {
				return err
			}
		}
		if prior == nil || recordedBefore.After(prior.Before) {
			seal, err := o.Seal(ctx, recordedBefore)
			if err != nil {
				return err
			}
			if seal.KeyName == "" || seal.Signature == "" {
				return invalid(op, "a pass writes no watermark it has not signed")
			}
			if _, err := q.Exec(ctx, `
				INSERT INTO audit_capture_retention (tenant, retained_before, key_name, signature, retained_by)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (tenant) DO UPDATE
				SET retained_before = EXCLUDED.retained_before, key_name = EXCLUDED.key_name,
				    signature = EXCLUDED.signature, retained_at = now(), retained_by = EXCLUDED.retained_by`,
				tenant, recordedBefore, seal.KeyName, seal.Signature, o.By); err != nil {
				return wrap(op, err)
			}
		}
		tag, err := q.Exec(ctx, `
			UPDATE audit_captures c
			SET bytes = ''::bytea, retained_at = now()
			FROM audit_records a
			WHERE c.tenant = $1 AND a.tenant = $1 AND a.record_id = c.record_id
			  AND a.recorded_at < $2 AND c.retained_at IS NULL`, tenant, recordedBefore)
		if err != nil {
			return wrap(op, err)
		}
		emptied = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, wrapExisting(op, err)
	}
	return emptied, nil
}

// SealCaptureRetention writes a tenant's first capture watermark without a
// pass: the adoption of captures emptied before 0024, after an operator has
// named the instant that covers them. It is refused as a conflict when the
// tenant already has a watermark, and as invalid when any capture is marked
// retained at or after the instant — so it covers what is already gone and
// nothing an operator did not name.
func (r auditRepo) SealCaptureRetention(ctx context.Context, tenant Tenant, before time.Time, seal RetentionSeal, by string) (CaptureRetention, error) {
	const op = "store.Audit.SealCaptureRetention"
	if err := checkTenant(op, tenant); err != nil {
		return CaptureRetention{}, err
	}
	if before.IsZero() {
		return CaptureRetention{}, invalid(op, "an instant is required")
	}
	if seal.KeyName == "" || seal.Signature == "" {
		return CaptureRetention{}, invalid(op, "a seal names its key and carries its signature")
	}
	var out CaptureRetention
	err := r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		if err := lockCaptureRetention(ctx, op, q, tenant); err != nil {
			return err
		}
		switch _, err := r.captureRetention(ctx, op, q, tenant); {
		case err == nil:
			return &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("tenant %s already has a capture retention watermark", tenant)}
		case !IsNotFound(err):
			return err
		}
		outside, err := r.retainedOutside(ctx, op, q, tenant, &CaptureRetention{Before: before})
		if err != nil {
			return err
		}
		if outside > 0 {
			return invalid(op, fmt.Sprintf("%d capture(s) recorded at or after %s are marked retained", outside, before.UTC().Format(time.RFC3339Nano)))
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO audit_capture_retention (tenant, retained_before, key_name, signature, retained_by)
			VALUES ($1, $2, $3, $4, $5)`, tenant, before, seal.KeyName, seal.Signature, by); err != nil {
			return wrap(op, err)
		}
		out, err = r.captureRetention(ctx, op, q, tenant)
		return err
	})
	if err != nil {
		return CaptureRetention{}, wrapExisting(op, err)
	}
	return out, nil
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
