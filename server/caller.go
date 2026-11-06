// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"slices"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/httpapi/north"
)

// Caller is who Control authenticated for the request a host route is serving,
// or a registered report provider is answering.
//
// It is a projection, never the principal itself: which tenants a caller may
// reach is Control's to know and the middleware's to check. A handler is handed
// what the request covers — one tenant, or on a cross-tenant read the set — and
// never asks, because a handler that could ask would be a handler that chose
// its own set, which is the missing filter M18 exists to rule out.
type Caller struct {
	// Tenant is the one tenant Control resolved, from the caller's scope —
	// never read from the request. Read the tenant from here and nowhere
	// else: the {tenant} in the path is a selector Control has already
	// checked, not a value a handler may trust on its own. It is zero on an
	// AccessCrossTenant route.
	Tenant ext.Tenant
	// Tenants is the set Control resolved on an AccessCrossTenant route:
	// sorted, never empty, each one in the caller's scope with the route's
	// permission. It is nil on an AccessTenant route, where Tenant is the one.
	// Read the set from here and nowhere else, and say in the response which
	// tenants it covers: that is how a report says "these" rather than "some".
	Tenants []ext.Tenant
	// Subject is the person behind a session: ID and Groups. It is zero for
	// a machine token. Username and ExternalID are always empty: Control's
	// principal carries neither, and they are never guessed.
	Subject ext.Subject
	// Principal is the id of the credential that acted. Safe to log.
	Principal string
	// BreakGlass reports a credential from the local break-glass path. It
	// is asserted when the credential is minted and never inferred (M7).
	BreakGlass bool
	// CorrelationID is the id the request is logged under (M11).
	CorrelationID string
	// ActingOperator is the supervising operator a supervisor's call named
	// in its Hoplock-Acting-Operator header (0048), decoded. It is the
	// supervisor's assertion, not Control's verification: what Control
	// verified is Principal, a `sup-` id, and nothing else. It is empty
	// unless Principal is a supervisor's and the call named one.
	//
	// A host records it as the supervisor's word, beside Principal, and
	// never authorises on it, matches it, or puts it in Principal's place.
	ActingOperator string
}

// CallerFrom returns who Control authenticated for the request ctx belongs to,
// in two places: inside a host route, and inside Control's own calls into a
// registered ext.ReportProvider — its Reports and Generate, on Control's report
// routes — so a pack can record who generated a report. It is for attribution,
// never for authorisation: Control has already resolved the tenant and checked
// the permission. ok is false everywhere else. WriteError still answers only
// inside a host route: a provider call is not one.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := north.HostCallerFrom(ctx)
	if !ok {
		return Caller{}, false
	}
	return Caller{
		Tenant:         c.Tenant,
		Tenants:        slices.Clone(c.Tenants),
		Subject:        c.Subject,
		Principal:      c.Principal,
		BreakGlass:     c.BreakGlass,
		CorrelationID:  c.CorrelationID,
		ActingOperator: c.ActingOperator,
	}, true
}
