// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"errors"
	"fmt"
)

// NoPathReason says why no viable path exists. It is a closed set, because the
// reason is what an operator acts on: a dead proxy, a missing relay
// registration, and a topology that cannot fit inside the hop cap are three
// different afternoons.
type NoPathReason string

const (
	// NoPathNoLiveRoute means the graph holds no viable sequence of edges. The
	// commonest cause is a relay edge whose downstream proxy is not registered
	// — which is NOT downgraded to a dial (proxy D11).
	NoPathNoLiveRoute NoPathReason = "no-live-route"
	// NoPathEntryUnknown means the asking proxy is not in this tenant's graph.
	// It may exist in another tenant's, which is why nothing widens the lookup
	// to find it (M18).
	NoPathEntryUnknown NoPathReason = "entry-unknown"
	// NoPathEntryNotLive means the asking proxy is enrolled but stale or
	// revoked. Answering it a route would be routing through a proxy this
	// server has not heard from.
	NoPathEntryNotLive NoPathReason = "entry-not-live"
	// NoPathLoop means the trail the session carries already names the asking
	// proxy. The loop has happened; it is refused, not routed around.
	NoPathLoop NoPathReason = "loop"
	// NoPathMaxHops means every route is longer than the hop cap allows.
	NoPathMaxHops NoPathReason = "max-hops"
	// NoPathSearchBound means the search hit its work limit. Real fleets are
	// nowhere near it, so this names a topology worth looking at rather than a
	// proxy worth restarting.
	NoPathSearchBound NoPathReason = "search-bound"
)

// ErrNoPath is the sentinel every no-path outcome answers to.
//
// 0008 MUST translate it into an OUTAGE (`5xx`, M11) and never into a `401`.
// This is not a stylistic preference: the proxy faithfully relays a `401` to the
// user as "access denied" and sends the operator to debug permissions, so an
// unreachable enclave relay reported as a denial costs an outage spent in the
// wrong place. A user denied by policy and a user unreachable because a relay is
// down must not receive the same answer — that distinction is the whole point of
// the proxy's disclosure rule (M4).
var ErrNoPath = errors.New("fleet: no viable path")

// NoPathError is the no-path outcome, carrying enough to explain itself.
type NoPathError struct {
	// Entry is where the path was asked from.
	Entry EntryPoint
	// Destination is where it had to reach.
	Destination Destination
	// Reason says which kind of no-path this is.
	Reason NoPathReason
}

func (e *NoPathError) Error() string {
	return fmt.Sprintf("fleet: no viable path from proxy %q to zone %q: %s",
		e.Entry.ProxyID, e.Destination.Zone, e.Reason)
}

// Is makes errors.Is(err, ErrNoPath) true for every reason, so a caller that
// only needs "this is an outage, not a deny" does not have to enumerate them.
func (e *NoPathError) Is(target error) bool { return target == ErrNoPath }

// IsNoPath reports whether err is a no-path outcome.
func IsNoPath(err error) bool { return errors.Is(err, ErrNoPath) }

// NoPath extracts the outcome, for a caller that wants the reason.
func NoPath(err error) (*NoPathError, bool) {
	var e *NoPathError
	ok := errors.As(err, &e)
	return e, ok
}

// ErrZoneNotGranted is enrollment refusing a zone the proxy was not granted.
//
// It is the whole reason enrollment is an administrative act: a fleet that
// enrolled anyone who asked would let whoever can reach this server insert a hop
// into other people's routes, which is not an information leak but one party's
// session traversing another's kit.
var ErrZoneNotGranted = errors.New("fleet: proxy is not granted the zone it claims")

// ErrEnrollmentUnknown is an enrollment attempt with no pre-registered grant.
var ErrEnrollmentUnknown = errors.New("fleet: no enrollment grant for this proxy")

// ErrEnrollmentSpent is a one-time enrollment token presented twice.
var ErrEnrollmentSpent = errors.New("fleet: enrollment token has already been used")

// ErrEnrollmentExpired is an enrollment token presented after its expiry.
var ErrEnrollmentExpired = errors.New("fleet: enrollment token has expired")

// ErrEnrollmentRejected is a token whose secret does not match the grant.
//
// It is deliberately one error for "wrong secret" and "wrong tenant in the
// token": both are a credential that does not verify, and telling them apart
// for the caller would make this endpoint an oracle for which proxy ids exist
// in which tenant.
var ErrEnrollmentRejected = errors.New("fleet: enrollment credential was not accepted")

// ErrNotEnrolled is a heartbeat or a report from a proxy with no enrolled row.
//
// It is never answered by creating the row. A heartbeat that enrolled its sender
// would be the auto-enrollment this phase exists to refuse.
var ErrNotEnrolled = errors.New("fleet: proxy is not enrolled")

// ErrNoRollbackTarget is a rollback on a scope that has never been republished,
// so there is nothing to go back to.
var ErrNoRollbackTarget = errors.New("fleet: configuration scope has no previous version")
