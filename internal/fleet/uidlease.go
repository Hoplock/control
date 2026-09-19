// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hoplock/control/internal/store"
)

// The ephemeral-uid lease (PLAN §4, contract `POST /v1/uids/lease`).
//
// THE ONE THING THIS SERVER MUST GUARANTEE IS THAT THE CURSOR ONLY EVER
// ADVANCES. A uid inside a granted block is never inside any other grant — for
// this proxy or any other, ever again — whether the block was used, abandoned,
// or allowed to expire. Everything else on this endpoint is a detail.
//
// So there is no release call, no expiry sweeper, no free list and no "that
// proxy is gone, recycle its range" tidy-up, and their absence is the design
// rather than an omission: each is a plausible-looking optimisation that
// reintroduces exactly the uid reuse this mechanism exists to prevent, and
// none of them fails visibly. The cost of never reclaiming is uids, which are
// 31 bits wide and cheap; the cost of reclaiming once is a fresh session
// inheriting a torn-down one's files.

// UID allocation defaults.
//
// The range is what a server allocates from when the proxy names no bounds.
// It sits above every distribution's own `UID_MAX`, above systemd's
// dynamic-user range and at the top of SSSD's default id-mapping range, and
// below 2^31 so a uid stays a positive int32 — the same facts `range_min` and
// `range_max` encode on the request, which is why a proxy that states them
// overrides these.
const (
	// DefaultUIDRangeMin and DefaultUIDRangeMax bound allocation when the
	// request names no range.
	DefaultUIDRangeMin int32 = 2_000_000
	DefaultUIDRangeMax int32 = 2_147_483_646
	// DefaultUIDBlockSize is granted when the proxy asks for no particular
	// size. It is what a busy target burns through in a day, which is the
	// outage `term_seconds` is measured against below.
	DefaultUIDBlockSize int32 = 4096
	// MaxUIDBlockSize caps what one lease may take, so a proxy asking for a
	// wildly large block cannot spend a target's range in one call. A block
	// is never reclaimed, so a single bad request is permanent.
	MaxUIDBlockSize int32 = 1 << 20
)

// ErrUIDRangeUnsatisfiable reports that no block can be granted inside the
// requested range: the cursor has reached the top of it.
//
// The contract answers this with 409, which the proxy treats exactly as an
// exhausted block — outage-class, nothing provisioned, and the remedy is the
// operator's.
var ErrUIDRangeUnsatisfiable = errors.New("fleet: the uid range for this target is exhausted")

// ErrUIDRangeInvalid reports a request whose range cannot describe any block.
// It is a malformed caller rather than a spent range, so it is a 400 and not a
// 409: an operator reading a 409 goes looking for a cursor at the top of its
// range, and there is not one.
var ErrUIDRangeInvalid = errors.New("fleet: the requested uid range is empty or inverted")

// UIDLeaseRequest is one proxy asking for a block for one target.
type UIDLeaseRequest struct {
	// ProxyID is who the block is granted to. A lease is per proxy per
	// target and is not made on behalf of a session.
	ProxyID string
	// Hostname and Port name the target, keyed exactly as
	// `/v1/hostkeys/report` and `/v1/capabilities/report` key theirs, and
	// with the same known imprecision — several names resolving to one
	// host are several cursors. The cost of getting that wrong is spare
	// uids, not a collision.
	Hostname string
	Port     int32
	// Count is how many uids the proxy would like. A REQUEST, NOT A
	// REQUIREMENT: this server grants what it chooses and the proxy uses
	// what it is granted.
	Count int32
	// RangeMin and RangeMax bound what the proxy will accept, inclusive.
	// Both zero leaves the bounds to this server's configuration.
	RangeMin int32
	RangeMax int32
	// ObservedFloor is the highest uid the proxy has seen given out on the
	// target, from the target's own high-water mark. It is corroboration
	// from an untrusted party and may only ever RAISE the cursor.
	ObservedFloor int32
}

// UIDLease is an exclusive block for one target.
type UIDLease struct {
	// LeaseID is recorded, so an incident can say which proxy's block a
	// uid came from without polling the fleet.
	LeaseID string
	// From and To are the half-open block [From, To).
	From int32
	To   int32
	// Term is how long the proxy may keep allocating from the block. ZERO
	// IS DELIBERATE and means this server states none, leaving the proxy
	// its own default — see [Registry.LeaseUIDs].
	Term time.Duration
}

// LeaseUIDs grants an exclusive block out of the target's cursor.
//
// # On `term_seconds`, and why this server states none by default
//
// The term bounds how long THIS proxy keeps allocating from the block it
// holds. It is not what makes the uids non-reusable — the monotonic cursor is
// — so an expired block is one the proxy stops using, never one this server
// may hand to somebody else.
//
// A block ends when it is exhausted or when its term runs out, and BOTH FAIL
// CLOSED WHILE THIS SERVER IS UNREACHABLE: a Control outage plus a busy target
// is a provisioning outage for that target. `Count` is how many sessions a
// proxy rides out; the term is how long. Shortening the term therefore costs
// availability during exactly the outage a held block exists to survive, and
// buys nothing — the invariant is the cursor's, not the term's. So this server
// states no term unless a deployment configures one, which leaves the proxy its
// own default of a day; `WithUIDLeaseTerm` is for a deployment that has a
// reason to be stricter.
//
// # On `observed_floor`
//
// It is relayed from the target, so it is this server's to clamp or ignore. It
// may only raise, it is clamped to the request's range, and the exposure is
// worth stating plainly: root on a target can report a large floor and burn
// that target's range. That is loud, it is bounded, it reaches no other
// target, and it is the right side of an invariant that prefers refusing to
// reusing.
func (r *Registry) LeaseUIDs(ctx context.Context, tenant store.Tenant, req UIDLeaseRequest) (UIDLease, error) {
	if req.Hostname == "" {
		return UIDLease{}, fmt.Errorf("fleet.LeaseUIDs: target is required")
	}
	if req.ProxyID == "" {
		return UIDLease{}, fmt.Errorf("fleet.LeaseUIDs: proxy id is required")
	}

	rangeMin, rangeMax := req.RangeMin, req.RangeMax
	if rangeMin == 0 && rangeMax == 0 {
		rangeMin, rangeMax = r.uids.RangeMin, r.uids.RangeMax
	}
	if rangeMin < 0 || rangeMax <= rangeMin {
		return UIDLease{}, ErrUIDRangeInvalid
	}
	// Half-open internally, inclusive on the wire. The cursor may sit
	// exactly at rangeEnd: that is an exhausted range, which answers 409.
	rangeEnd := int64(rangeMax) + 1

	targetID := UIDTargetID(req.Hostname, req.Port)
	leaseID, err := newLeaseID()
	if err != nil {
		return UIDLease{}, err
	}

	// The cursor is created OUTSIDE the transaction below, and it has to be.
	// A duplicate-key conflict aborts the transaction it happens in, so a
	// losing racer that swallowed the conflict would then find every
	// subsequent statement refused — two proxies reporting a target this
	// server has never seen at the same instant would take each other down.
	// Out here the loser simply reads what the winner wrote.
	//
	// A cursor created by a lease that then fails is harmless: it sits at the
	// bottom of the range having granted nothing, which is where it would
	// have started anyway. It must never be RE-created, because re-creating a
	// cursor is the one operation that would move it backwards without ever
	// writing a lower value through the forward-only trigger.
	if _, err := r.st.UIDCursors().Get(ctx, tenant, targetID); err != nil {
		if !store.IsNotFound(err) {
			return UIDLease{}, err
		}
		if err := r.st.UIDCursors().Create(ctx, tenant, targetID, int64(rangeMin), rangeEnd); err != nil &&
			!store.IsConflict(err) {
			return UIDLease{}, err
		}
	}

	var out UIDLease
	err = r.st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		cursors := tx.UIDCursors()

		// The floor is raised to at least the request's range_min before
		// anything is granted, which does three things at once: it catches
		// a cursor up to a range that has moved, it clamps a relayed
		// observation to the range the proxy will accept, and — because
		// RaiseFloor is an UPDATE ... RETURNING — it takes the row lock for
		// the rest of this transaction, so the remaining-space arithmetic
		// below cannot be raced by a concurrent lease.
		floor := int64(rangeMin)
		if req.ObservedFloor > 0 {
			// The mark is the highest uid SEEN GIVEN OUT, so the lowest
			// one still free is one past it.
			observed := int64(req.ObservedFloor) + 1
			if observed > floor {
				floor = min(observed, rangeEnd)
			}
		}

		before, err := cursors.Get(ctx, tenant, targetID)
		if err != nil {
			return err
		}
		cursor, err := cursors.RaiseFloor(ctx, tenant, targetID, floor)
		if err != nil {
			return err
		}
		if cursor.NextUID > before.NextUID && req.ObservedFloor > 0 {
			r.log.InfoContext(ctx, "uid cursor raised by a floor observed on the target",
				"event", "uid_cursor_raised",
				"tenant", tenant.String(),
				"target", targetID,
				"proxy_id", req.ProxyID,
				"observed_floor", req.ObservedFloor,
				"from", before.NextUID,
				"to", cursor.NextUID,
			)
		}

		// Never grant outside the requested range: a block outside it is
		// refused by the proxy anyway, so granting one only turns a clear
		// 409 into a confusing outage.
		top := min(cursor.RangeEnd, rangeEnd)
		remaining := top - cursor.NextUID
		if remaining <= 0 {
			return ErrUIDRangeUnsatisfiable
		}

		size := int64(r.uids.BlockSize)
		if req.Count > 0 {
			size = int64(min(req.Count, r.uids.MaxBlockSize))
		}
		size = min(size, remaining)

		block, err := cursors.Advance(ctx, tenant, targetID, size)
		if err != nil {
			if store.IsExhausted(err) {
				return ErrUIDRangeUnsatisfiable
			}
			return err
		}

		out = UIDLease{
			LeaseID: leaseID,
			From:    int32(block.From),
			To:      int32(block.To),
			Term:    r.uids.Term,
		}
		return tx.UIDLeases().Record(ctx, tenant, store.UIDLease{
			LeaseID:     leaseID,
			TargetID:    targetID,
			ProxyID:     req.ProxyID,
			From:        block.From,
			To:          block.To,
			TermSeconds: int32(r.uids.Term.Seconds()),
			GrantedAt:   r.now(),
		})
	})
	if err != nil {
		return UIDLease{}, err
	}
	return out, nil
}

// UIDTargetID is how a target is keyed for uid allocation.
//
// It matches the proxy's own holder ("host" or "host:port"), so a cursor and
// the blocks a proxy believes it holds name the same thing.
func UIDTargetID(hostname string, port int32) string {
	if port <= 0 {
		return hostname
	}
	return hostname + ":" + strconv.Itoa(int(port))
}

// UIDAllocation is the allocation policy, in one value so it is configured
// rather than scattered.
type UIDAllocation struct {
	// RangeMin and RangeMax are used when a request names no range.
	RangeMin int32
	RangeMax int32
	// BlockSize is granted when the proxy asks for no particular size.
	BlockSize int32
	// MaxBlockSize caps what one lease may take.
	MaxBlockSize int32
	// Term is `term_seconds`. ZERO means this server states none, which
	// leaves the interval to the proxy's own default — see
	// [Registry.LeaseUIDs] for why that is the right default rather than a
	// gap.
	Term time.Duration
}

// DefaultUIDAllocation is the policy with nothing configured.
func DefaultUIDAllocation() UIDAllocation {
	return UIDAllocation{
		RangeMin:     DefaultUIDRangeMin,
		RangeMax:     DefaultUIDRangeMax,
		BlockSize:    DefaultUIDBlockSize,
		MaxBlockSize: MaxUIDBlockSize,
	}
}

// withDefaults fills in anything a caller left at zero — except Term, whose
// zero is a real answer.
func (a UIDAllocation) withDefaults() UIDAllocation {
	d := DefaultUIDAllocation()
	if a.RangeMin <= 0 {
		a.RangeMin = d.RangeMin
	}
	if a.RangeMax <= a.RangeMin {
		a.RangeMax = d.RangeMax
	}
	if a.BlockSize <= 0 {
		a.BlockSize = d.BlockSize
	}
	if a.MaxBlockSize <= 0 {
		a.MaxBlockSize = d.MaxBlockSize
	}
	if a.BlockSize > a.MaxBlockSize {
		a.BlockSize = a.MaxBlockSize
	}
	return a
}

// WithUIDAllocation sets the allocation policy. Zero-valued fields keep their
// defaults, except Term, whose zero means "state none".
func WithUIDAllocation(a UIDAllocation) Option {
	return func(r *Registry) { r.uids = a.withDefaults() }
}

// newLeaseID mints the id an incident resolves a uid back to.
func newLeaseID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("fleet: mint lease id: %w", err)
	}
	return "lease-" + base64.RawURLEncoding.EncodeToString(buf), nil
}
