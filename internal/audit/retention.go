// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"fmt"
	"strconv"
	"time"

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
// ThroughHash are where the stream's chain now starts.
type PrefixRetained struct {
	Stream      string
	Deleted     int64
	ThroughSeq  int64
	ThroughHash string
}

// RetainPrefix runs one pass and records it, in one transaction.
//
// A pass by instant is planned inside that transaction: the prefix is every
// record before the first one received at or after the instant, and never the
// head. The plan reads before the append lock is taken, which is safe in the
// one direction that matters — a record appended meanwhile is newer than the
// head the plan stopped short of — and a pass by sequence the store refuses
// (at or behind the marker, or at the head) is refused whole.
func (e *Emitter) RetainPrefix(ctx context.Context, tenant store.Tenant, pass PrefixPass, actor OperatorActor) (PrefixRetained, error) {
	if (pass.ThroughSeq > 0) == !pass.Before.IsZero() {
		return PrefixRetained{}, fmt.Errorf("audit: a retention pass names a sequence or an instant, exactly one")
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
				return nil
			case err != nil && !store.IsNotFound(err):
				return err
			}
			if n == 0 {
				return nil
			}
			through = n
		}

		marker, deleted, err := tx.Audit().RetainPrefix(ctx, tenant, pass.Stream, through, actor.Principal)
		if err != nil {
			return err
		}
		out.Deleted, out.ThroughSeq, out.ThroughHash = deleted, marker.ThroughSeq, marker.ThroughHash
		return e.OperatorEvent(ctx, tx, tenant, OperatorAct{
			Event: EventAuditRetained, Actor: actor, After: strconv.FormatInt(marker.ThroughSeq, 10),
			Fields: map[string]string{
				"stream": pass.Stream, "through_hash": marker.ThroughHash, "deleted": strconv.FormatInt(deleted, 10),
			},
			Message: fmt.Sprintf("stream %s retained through %d", pass.Stream, marker.ThroughSeq),
		})
	})
	if err != nil {
		return PrefixRetained{}, err
	}
	return out, nil
}

// RetainCaptures empties the bytes of every session capture recorded before an
// instant and records the pass, in one transaction. The records stay, with the
// digest the chain covers, so what is verifiable afterwards is that a capture
// is gone rather than that it was changed. A pass that emptied nothing is
// recorded too: it is still an operator's act.
func (e *Emitter) RetainCaptures(ctx context.Context, tenant store.Tenant, before time.Time, actor OperatorActor) (int64, error) {
	var n int64
	err := e.in.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		var err error
		if n, err = tx.Audit().RetainCaptures(ctx, tenant, before); err != nil {
			return err
		}
		return e.OperatorEvent(ctx, tx, tenant, OperatorAct{
			Event: EventAuditRetained, Actor: actor,
			Fields: map[string]string{
				"captures_before": before.UTC().Format(time.RFC3339), "captures": strconv.FormatInt(n, 10),
			},
			Message: fmt.Sprintf("%d session capture(s) retained", n),
		})
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}
