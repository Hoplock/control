// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
)

type uidRepo struct{ s *Store }

func (r uidRepo) Get(ctx context.Context, tenant Tenant, targetID string) (UIDCursor, error) {
	const op = "store.UIDCursors.Get"
	if err := checkTenant(op, tenant); err != nil {
		return UIDCursor{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var c UIDCursor
	err := r.s.db.QueryRow(ctx, `
		SELECT target_id, next_uid, range_end, updated_at
		FROM uid_allocation_cursors
		WHERE tenant = $1 AND target_id = $2`, tenant, targetID).
		Scan(&c.TargetID, &c.NextUID, &c.RangeEnd, &c.UpdatedAt)
	if err != nil {
		return UIDCursor{}, wrap(op, err)
	}
	return c, nil
}

func (r uidRepo) Create(ctx context.Context, tenant Tenant, targetID string, first, rangeEnd int64) error {
	const op = "store.UIDCursors.Create"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if targetID == "" {
		return invalid(op, "target id is required")
	}
	if first < 0 || rangeEnd < first {
		return invalid(op, "uid range must be non-negative and non-decreasing")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// No ON CONFLICT DO NOTHING and no upsert. Re-creating a cursor is the
	// one operation that would move it backwards without ever writing a
	// lower value through the trigger, so an existing cursor is a conflict
	// the caller must see.
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO uid_allocation_cursors (tenant, target_id, next_uid, range_end)
		VALUES ($1, $2, $3, $4)`, tenant, targetID, first, rangeEnd)
	return wrap(op, err)
}

func (r uidRepo) Advance(ctx context.Context, tenant Tenant, targetID string, size int64) (UIDBlock, error) {
	const op = "store.UIDCursors.Advance"
	if err := checkTenant(op, tenant); err != nil {
		return UIDBlock{}, err
	}
	if size <= 0 {
		return UIDBlock{}, invalid(op, "block size must be positive")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// The lock is the whole mechanism.
	//
	// Advancing is a read-modify-write, and two concurrent leases for one
	// target must never read the same value: a uid granted twice is a fresh
	// session inheriting a torn-down one's files, and it is invisible until
	// it is an incident. SELECT ... FOR UPDATE makes the second caller wait
	// for the first to commit and then read what the first wrote, so the
	// blocks are disjoint by construction rather than by luck.
	//
	// This costs a transaction and a round trip, and it is affordable: the
	// call is made once per BLOCK, not once per session, and it is not on
	// the decision path (PLAN §4). M5's budget is untouched.
	block := UIDBlock{}
	err := r.s.inTx(ctx, op, func(ctx context.Context, tx querier) error {
		var nextUID, rangeEnd int64
		err := tx.QueryRow(ctx, `
			SELECT next_uid, range_end
			FROM uid_allocation_cursors
			WHERE tenant = $1 AND target_id = $2
			FOR UPDATE`, tenant, targetID).Scan(&nextUID, &rangeEnd)
		if err != nil {
			return wrap(op, err)
		}

		if rangeEnd-nextUID < size {
			// The range is used up, not missing. The contract answers
			// this with 409, which the proxy treats exactly as an
			// exhausted block: outage-class, nothing provisioned, and
			// the remedy is the operator's.
			return exhausted(op, "uid range exhausted for this target")
		}

		if _, err := tx.Exec(ctx, `
			UPDATE uid_allocation_cursors
			SET next_uid = $3, updated_at = now()
			WHERE tenant = $1 AND target_id = $2`, tenant, targetID, nextUID+size); err != nil {
			return wrap(op, err)
		}

		block = UIDBlock{From: nextUID, To: nextUID + size}
		return nil
	})
	if err != nil {
		return UIDBlock{}, err
	}
	return block, nil
}

func (r uidRepo) RaiseFloor(ctx context.Context, tenant Tenant, targetID string, floor int64) (UIDCursor, error) {
	const op = "store.UIDCursors.RaiseFloor"
	if err := checkTenant(op, tenant); err != nil {
		return UIDCursor{}, err
	}
	if floor < 0 {
		return UIDCursor{}, invalid(op, "floor must be non-negative")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// GREATEST, so the statement cannot lower the cursor even if it is
	// handed a stale observation — and LEAST, so a target reporting a floor
	// past the top of its range exhausts that range rather than writing a
	// cursor the CHECK constraint would refuse. Burning a target's range is
	// the stated exposure of trusting a relayed floor (PLAN §4); a write
	// that fails the constraint would be an outage on top of it.
	var c UIDCursor
	err := r.s.db.QueryRow(ctx, `
		UPDATE uid_allocation_cursors
		SET next_uid   = LEAST(GREATEST(next_uid, $3), range_end),
		    updated_at = now()
		WHERE tenant = $1 AND target_id = $2
		RETURNING target_id, next_uid, range_end, updated_at`,
		tenant, targetID, floor).
		Scan(&c.TargetID, &c.NextUID, &c.RangeEnd, &c.UpdatedAt)
	if err != nil {
		return UIDCursor{}, wrap(op, err)
	}
	return c, nil
}
