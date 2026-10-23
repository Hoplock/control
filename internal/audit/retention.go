// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"errors"
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
//
// Archived is set when an archive is registered (0025): ArchivedThroughSeq is
// then how far the archive held the stream whole when the pass read it, and
// HeldByArchive says the pass deleted less than its instant reached because the
// archive had not confirmed the rest.
type PrefixRetained struct {
	Stream      string
	Deleted     int64
	ThroughSeq  int64
	ThroughHash string
	KeyName     string
	Signature   string

	Archived           bool
	ArchivedThroughSeq int64
	HeldByArchive      bool
}

// NotArchivedError is a pass by sequence beyond what the archive has confirmed
// (0025). It is refused whole — nothing is deleted, sealed or recorded —
// because a pass by sequence names exactly what to delete, and deleting less
// while calling it done would be a different act from the one an operator
// asked for. A pass by instant is held back instead.
type NotArchivedError struct {
	Stream             string
	ThroughSeq         int64
	ArchivedThroughSeq int64
}

func (e *NotArchivedError) Error() string {
	return fmt.Sprintf("audit: stream %s is archived through %d, so a pass through %d would delete what the archive has not confirmed",
		e.Stream, e.ArchivedThroughSeq, e.ThroughSeq)
}

// IsNotArchived reports whether err is a *NotArchivedError.
func IsNotArchived(err error) bool {
	var e *NotArchivedError
	return errors.As(err, &e)
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
//
// WITH AN ARCHIVE REGISTERED IT NEVER DELETES WHAT THE ARCHIVE HAS NOT
// CONFIRMED (0025): every record through the pass's sequence, and every capture
// of them, must be confirmed (store.FeedRepository.ArchivedThrough). A pass by
// sequence beyond that is refused (*NotArchivedError); a pass by instant stops
// at it, and says it was held back. The confirmation is read in the pass's own
// transaction and again under the stream's append lock, and it only ever moves
// forward, so a read there can under-delete and never over-delete.
func (e *Emitter) RetainPrefix(ctx context.Context, tenant store.Tenant, pass PrefixPass, actor OperatorActor) (PrefixRetained, error) {
	if (pass.ThroughSeq > 0) == !pass.Before.IsZero() {
		return PrefixRetained{}, fmt.Errorf("audit: a retention pass names a sequence or an instant, exactly one")
	}
	if e.keys == nil {
		return PrefixRetained{}, ErrRetentionKeyNotConfigured
	}
	out := PrefixRetained{Stream: pass.Stream, Archived: e.archived}
	err := e.in.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		through := pass.ThroughSeq
		if e.archived {
			a, err := tx.Feed().ArchivedThrough(ctx, tenant, pass.Stream)
			if err != nil {
				return err
			}
			out.ArchivedThroughSeq = a
		}
		if !pass.Before.IsZero() {
			n, err := tx.Audit().ThroughSeqBefore(ctx, tenant, pass.Stream, pass.Before)
			if err != nil {
				return err
			}
			if e.archived && n > out.ArchivedThroughSeq {
				n, out.HeldByArchive = out.ArchivedThroughSeq, true
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
				if err := e.keys.checkPrefixStart(ctx, tenant, pass.Stream, prior, first); err != nil {
					return err
				}
				if !e.archived {
					return nil
				}
				a, err := tx.Feed().ArchivedThrough(ctx, tenant, pass.Stream)
				if err != nil {
					return err
				}
				out.ArchivedThroughSeq = a
				if through > a {
					return &NotArchivedError{Stream: pass.Stream, ThroughSeq: through, ArchivedThroughSeq: a}
				}
				return nil
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
		fields := map[string]string{
			"stream": pass.Stream, "through_hash": marker.ThroughHash, "deleted": strconv.FormatInt(deleted, 10),
			"key": marker.KeyName, "signature": marker.Signature,
		}
		message := fmt.Sprintf("stream %s retained through %d", pass.Stream, marker.ThroughSeq)
		if e.archived {
			fields["held_by_archive"] = strconv.FormatBool(out.HeldByArchive)
			fields["archived_through_seq"] = strconv.FormatInt(out.ArchivedThroughSeq, 10)
			if out.HeldByArchive {
				message += ", held back by the archive"
			}
		}
		return e.OperatorEvent(ctx, tx, tenant, OperatorAct{
			Event: EventAuditRetained, Actor: actor, After: strconv.FormatInt(marker.ThroughSeq, 10),
			Fields: fields, Message: message,
		})
	})
	if err != nil {
		return PrefixRetained{}, err
	}
	return out, nil
}

// CapturesRetained is what a capture pass did. Before is the instant it ran to:
// the one asked for, or now when that was later, because a watermark ahead of
// the clock would vouch for captures not yet recorded — or, with an archive
// registered, the recorded_at of the earliest capture the archive has not
// confirmed, when that was earlier still, and HeldByArchive says so (0025).
// KeyName and Signature are the tenant's watermark seal after the pass.
type CapturesRetained struct {
	Emptied   int64
	Before    time.Time
	KeyName   string
	Signature string

	Archived      bool
	HeldByArchive bool
}

// RetainCaptures empties the bytes of every session capture recorded before an
// instant, moves the tenant's signed watermark to it and records the pass, in
// one transaction. The records stay, with the digest the chain covers, so what
// is verifiable afterwards is that a capture is gone rather than that it was
// changed — and the watermark is what makes "gone" a pass's doing rather than a
// writer's. A pass that emptied nothing is recorded too: it is still an
// operator's act.
//
// With an archive registered (0025) it empties no capture the archive has not
// confirmed: it runs no further than the earliest such capture's recorded_at,
// read under the tenant's capture-retention lock, and signs its watermark over
// the instant it ran to, because a watermark vouches for every capture before
// it. A capture recorded before that instant that arrives meanwhile is kept
// too, because the pass empties only confirmed captures.
func (e *Emitter) RetainCaptures(ctx context.Context, tenant store.Tenant, before time.Time, actor OperatorActor) (CapturesRetained, error) {
	if e.keys == nil {
		return CapturesRetained{}, ErrRetentionKeyNotConfigured
	}
	if now := e.now(); before.After(now) {
		before = now
	}
	out := CapturesRetained{Before: captureInstant(before), Archived: e.archived}
	err := e.in.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		var bound func(context.Context, time.Time) (time.Time, error)
		if e.archived {
			bound = func(ctx context.Context, asked time.Time) (time.Time, error) {
				earliest, ok, err := tx.Feed().EarliestUnarchivedCapture(ctx, tenant)
				if err != nil {
					return time.Time{}, err
				}
				if earliest = captureInstant(earliest); ok && earliest.Before(asked) {
					out.Before, out.HeldByArchive = earliest, true
					return earliest, nil
				}
				return asked, nil
			}
		}
		var err error
		if out.Emptied, err = tx.Audit().RetainCaptures(ctx, tenant, out.Before, store.RetainCapturesOptions{
			By:           actor.Principal,
			Bound:        bound,
			ArchivedOnly: e.archived,
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
		fields := map[string]string{
			"captures_before": out.Before.Format(time.RFC3339Nano), "captures": strconv.FormatInt(out.Emptied, 10),
			"watermark": mark.Before.UTC().Format(time.RFC3339Nano),
			"key":       mark.KeyName, "signature": mark.Signature,
		}
		message := fmt.Sprintf("%d session capture(s) retained", out.Emptied)
		if e.archived {
			fields["held_by_archive"] = strconv.FormatBool(out.HeldByArchive)
			if out.HeldByArchive {
				message += ", held back by the archive"
			}
		}
		return e.OperatorEvent(ctx, tx, tenant, OperatorAct{
			Event: EventAuditRetained, Actor: actor, Fields: fields, Message: message,
		})
	})
	if err != nil {
		return CapturesRetained{}, err
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
