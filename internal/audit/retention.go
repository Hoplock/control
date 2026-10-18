// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/store"
)

// Retention (PLAN §7, M8): the one job that deletes from this store.
//
// A PASS AND ITS RECORD ARE ONE TRANSACTION. M8 lets retention delete because
// it records what it deleted, and an operator's act is written in the
// transaction that makes the change (PLAN §3). A deletion that committed before
// its record would leave, on any failure between the two, a stream whose prefix
// is gone with nothing on the control chain saying so; a record that committed
// without its deletion would claim one that never happened. So the pass and
// `operator.audit.retained` commit together or not at all, and retention goes
// through the emitter for that reason alone.
//
// The deletion itself — a contiguous prefix of one stream, never its head,
// under the stream's append lock, with the marker moved forward — is the
// store's (internal/store/retention.go).

// PrefixPass names one pass over one stream: through a sequence, or through
// every record received before an instant. Exactly one of the two is set.
type PrefixPass struct {
	Stream     string
	ThroughSeq int64
	Before     time.Time
}

// PrefixRetained is what a pass did. Deleted is zero, and nothing is deleted or
// recorded, when a pass by instant found nothing to take: no record received
// before it, or none an earlier pass had not taken already. ThroughSeq and
// ThroughHash are where the stream's chain now starts, and KeyName and
// Signature the seal over them.
type PrefixRetained struct {
	Stream      string
	Deleted     int64
	ThroughSeq  int64
	ThroughHash string
	KeyName     string
	Signature   string
}

// RetainPrefix runs one pass, seals it and records it, in one transaction.
//
// A pass by instant is planned inside that transaction: the prefix is every
// record before the first one received at or after the instant, and never the
// head. The plan reads before the append lock is taken, which is safe in the
// one direction that matters — a record appended meanwhile is newer than the
// head the plan stopped short of — and a pass by sequence the store refuses
// (at or behind the marker, or at the head) is refused whole.
//
// With no key store the pass is refused before anything is read or deleted
// (ErrRetentionKeyNotConfigured). A marker or a start that does not verify
// refuses it too (*RetentionUnverifiedError), under the stream's lock.
func (e *Emitter) RetainPrefix(ctx context.Context, tenant store.Tenant, pass PrefixPass, actor OperatorActor) (PrefixRetained, error) {
	if (pass.ThroughSeq > 0) == !pass.Before.IsZero() {
		return PrefixRetained{}, fmt.Errorf("audit: a retention pass names a sequence or an instant, exactly one")
	}
	if e.keys == nil {
		return PrefixRetained{}, ErrRetentionKeyNotConfigured
	}
	out := PrefixRetained{Stream: pass.Stream}
	err := e.in.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		through := pass.ThroughSeq
		if !pass.Before.IsZero() {
			n, err := tx.Audit().ThroughSeqBefore(ctx, tenant, pass.Stream, pass.Before)
			if err != nil {
				return err
			}
			prior, err := tx.Audit().Retention(ctx, tenant, pass.Stream)
			switch {
			case err == nil && n <= prior.ThroughSeq:
				out.ThroughSeq, out.ThroughHash = prior.ThroughSeq, prior.ThroughHash
				out.KeyName, out.Signature = prior.KeyName, prior.Signature
				return nil
			case err != nil && !store.IsNotFound(err):
				return err
			}
			if n == 0 {
				return nil
			}
			through = n
		}

		marker, deleted, err := tx.Audit().RetainPrefix(ctx, tenant, pass.Stream, through, store.RetainPrefixOptions{
			By: actor.Principal,
			Check: func(ctx context.Context, prior *store.AuditRetention, first store.AuditRecord) error {
				return e.keys.checkPrefixStart(ctx, tenant, pass.Stream, prior, first)
			},
			Seal: func(ctx context.Context, seq int64, hash string) (store.RetentionSeal, error) {
				return e.keys.sign(ctx, tenant, RetentionMessage(tenant, pass.Stream, seq, hash))
			},
		})
		if err != nil {
			return err
		}
		out.Deleted, out.ThroughSeq, out.ThroughHash = deleted, marker.ThroughSeq, marker.ThroughHash
		out.KeyName, out.Signature = marker.KeyName, marker.Signature
		return e.OperatorEvent(ctx, tx, tenant, OperatorAct{
			Event: EventAuditRetained, Actor: actor, After: strconv.FormatInt(marker.ThroughSeq, 10),
			Fields: map[string]string{
				"stream": pass.Stream, "through_hash": marker.ThroughHash, "deleted": strconv.FormatInt(deleted, 10),
				"key": marker.KeyName, "signature": marker.Signature,
			},
			Message: fmt.Sprintf("stream %s retained through %d", pass.Stream, marker.ThroughSeq),
		})
	})
	if err != nil {
		return PrefixRetained{}, err
	}
	return out, nil
}

// CapturesRetained is what a capture pass did. Before is the instant it ran to:
// the one asked for, or now when that was later, because a watermark ahead of
// the clock would vouch for captures not yet recorded. KeyName and Signature
// are the tenant's watermark seal after the pass.
type CapturesRetained struct {
	Emptied   int64
	Before    time.Time
	KeyName   string
	Signature string
}

// RetainCaptures empties the bytes of every session capture recorded before an
// instant, moves the tenant's signed watermark to it and records the pass, in
// one transaction. The records stay, with the digest the chain covers, so what
// is verifiable afterwards is that a capture is gone rather than that it was
// changed — and the watermark is what makes "gone" a pass's doing rather than a
// writer's. A pass that emptied nothing is recorded too: it is still an
// operator's act.
func (e *Emitter) RetainCaptures(ctx context.Context, tenant store.Tenant, before time.Time, actor OperatorActor) (CapturesRetained, error) {
	if e.keys == nil {
		return CapturesRetained{}, ErrRetentionKeyNotConfigured
	}
	if now := e.now(); before.After(now) {
		before = now
	}
	out := CapturesRetained{Before: captureInstant(before)}
	err := e.in.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		var err error
		if out.Emptied, err = tx.Audit().RetainCaptures(ctx, tenant, out.Before, store.RetainCapturesOptions{
			By: actor.Principal,
			Check: func(ctx context.Context, prior *store.CaptureRetention, outside int64) error {
				return e.keys.checkCaptureStart(ctx, tenant, prior, outside)
			},
			Seal: func(ctx context.Context, before time.Time) (store.RetentionSeal, error) {
				return e.keys.sign(ctx, tenant, CaptureRetentionMessage(tenant, before))
			},
		}); err != nil {
			return err
		}
		mark, err := tx.Audit().CaptureRetention(ctx, tenant)
		if err != nil {
			return err
		}
		out.KeyName, out.Signature = mark.KeyName, mark.Signature
		return e.OperatorEvent(ctx, tx, tenant, OperatorAct{
			Event: EventAuditRetained, Actor: actor,
			Fields: map[string]string{
				"captures_before": out.Before.Format(time.RFC3339Nano), "captures": strconv.FormatInt(out.Emptied, 10),
				"watermark": mark.Before.UTC().Format(time.RFC3339Nano),
				"key":       mark.KeyName, "signature": mark.Signature,
			},
			Message: fmt.Sprintf("%d session capture(s) retained", out.Emptied),
		})
	})
	if err != nil {
		return CapturesRetained{}, err
	}
	return out, nil
}

// AdoptRetention seals a stream's marker that no pass signed — one written by
// v0.6.0 to v0.10.0, which nothing can tell from a forged one — and records the
// adoption, in one transaction.
//
// IT SIGNS ONLY WHAT AN OPERATOR HAS LOOKED AT, AND ONLY OVER A STREAM THAT
// VERIFIES. throughSeq and throughHash must be the marker's own, as audit-verify
// printed them, so a marker that moved since is refused; every record after the
// marker must chain onto it, so a marker over a broken stream is refused; and
// the store seals only an unsigned marker naming exactly those values. What no
// check can supply is whether the prefix was deleted lawfully: that is the
// operator's attestation, and the record says who made it.
func (e *Emitter) AdoptRetention(ctx context.Context, tenant store.Tenant, stream string, throughSeq int64, throughHash string, actor OperatorActor) (store.AuditRetention, error) {
	if e.keys == nil {
		return store.AuditRetention{}, ErrRetentionKeyNotConfigured
	}
	marker, err := e.in.store.Audit().Retention(ctx, tenant, stream)
	switch {
	case store.IsNotFound(err):
		return store.AuditRetention{}, fmt.Errorf("audit: stream %s has no retention marker to adopt", stream)
	case err != nil:
		return store.AuditRetention{}, err
	case marker.Signature != "":
		return store.AuditRetention{}, fmt.Errorf("audit: stream %s's marker is already signed, by key %s: there is nothing to adopt", stream, marker.KeyName)
	case marker.ThroughSeq != throughSeq || marker.ThroughHash != throughHash:
		return store.AuditRetention{}, fmt.Errorf(
			"audit: stream %s's marker is through %d with hash %s, not the %d and %s named: adopt names the marker you verified",
			stream, marker.ThroughSeq, marker.ThroughHash, throughSeq, throughHash)
	}
	walked, err := NewVerifier(e.in.store).walk(ctx, tenant, stream, &marker)
	if err != nil {
		return store.AuditRetention{}, err
	}
	if walked.Break != nil {
		return store.AuditRetention{}, fmt.Errorf("audit: stream %s does not verify from its marker, so the marker is not adopted: %w", stream, walked.Break)
	}

	var out store.AuditRetention
	err = e.in.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		seal, err := e.keys.sign(ctx, tenant, RetentionMessage(tenant, stream, throughSeq, throughHash))
		if err != nil {
			return err
		}
		if out, err = tx.Audit().SealRetention(ctx, tenant, stream, throughSeq, throughHash, seal); err != nil {
			return err
		}
		return e.OperatorEvent(ctx, tx, tenant, OperatorAct{
			Event: EventAuditRetentionAdopted, Actor: actor, After: strconv.FormatInt(throughSeq, 10),
			Fields: map[string]string{
				"stream": stream, "through_hash": throughHash, "records_verified": strconv.FormatInt(walked.Records, 10),
				"key": seal.KeyName, "signature": seal.Signature,
			},
			Message: fmt.Sprintf("stream %s's unsigned retention marker through %d adopted", stream, throughSeq),
		})
	})
	if err != nil {
		return store.AuditRetention{}, err
	}
	return out, nil
}

// AdoptCaptureRetention writes a tenant's first signed capture watermark over
// captures emptied before 0024, at the instant an operator names, and records
// the adoption, in one transaction. The store refuses it when the tenant has a
// watermark, or when any capture recorded at or after the instant is marked
// retained, so it covers what is already gone and nothing more; an instant
// ahead of the clock is refused here, because it would vouch for captures not
// yet recorded. The verifier then holds every retained capture to the
// timestamp inside its hashed record.
func (e *Emitter) AdoptCaptureRetention(ctx context.Context, tenant store.Tenant, before time.Time, actor OperatorActor) (store.CaptureRetention, error) {
	if e.keys == nil {
		return store.CaptureRetention{}, ErrRetentionKeyNotConfigured
	}
	if before.IsZero() || before.After(e.now()) {
		return store.CaptureRetention{}, fmt.Errorf("audit: a capture watermark is an instant that has already passed")
	}
	before = captureInstant(before)
	var out store.CaptureRetention
	err := e.in.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		seal, err := e.keys.sign(ctx, tenant, CaptureRetentionMessage(tenant, before))
		if err != nil {
			return err
		}
		if out, err = tx.Audit().SealCaptureRetention(ctx, tenant, before, seal, actor.Principal); err != nil {
			return err
		}
		return e.OperatorEvent(ctx, tx, tenant, OperatorAct{
			Event: EventAuditRetentionAdopted, Actor: actor,
			Fields: map[string]string{
				"captures_before": before.Format(time.RFC3339Nano), "key": seal.KeyName, "signature": seal.Signature,
			},
			Message: "captures retained before " + before.Format(time.RFC3339) + " adopted",
		})
	})
	if err != nil {
		return store.CaptureRetention{}, err
	}
	return out, nil
}

// RotateRetentionKey makes a new generation of a tenant's retention key and
// records it. The key store is outside the database, so the two cannot share a
// transaction: the record follows the key, as a revocation's follows its
// publication.
func (e *Emitter) RotateRetentionKey(ctx context.Context, tenant store.Tenant, actor OperatorActor) (ext.KeyInfo, error) {
	if e.keys == nil {
		return ext.KeyInfo{}, ErrRetentionKeyNotConfigured
	}
	before, _, err := e.keys.Newest(ctx, tenant)
	if err != nil {
		return ext.KeyInfo{}, err
	}
	info, err := e.keys.Rotate(ctx, tenant)
	if err != nil {
		return ext.KeyInfo{}, err
	}
	err = e.OperatorEvent(ctx, nil, tenant, OperatorAct{
		Event: EventAuditRetentionKeyRotated, Actor: actor, Before: before.Name, After: info.Ref.Name,
		Fields:  map[string]string{"custodian": info.Custodian},
		Message: "retention key rotated to " + info.Ref.Name,
	})
	return info, err
}
