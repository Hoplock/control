// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/httpapi/north"
)

// Caller is who Control authenticated for the request a host route is serving.
//
// It is a projection, never the principal itself: which tenants a caller may
// reach is Control's to know and the middleware's to check, and a handler that
// could ask would be a handler that could write a cross-tenant route (M18).
type Caller struct {
	// Tenant is the one tenant Control resolved, from the caller's scope —
	// never read from the request. Read the tenant from here and nowhere
	// else: the {tenant} in the path is a selector Control has already
	// checked, not a value a handler may trust on its own.
	Tenant ext.Tenant
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
}

// CallerFrom returns who Control authenticated for the request a host route is
// serving. ok is false outside a host route.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := north.HostCallerFrom(ctx)
	if !ok {
		return Caller{}, false
	}
	return Caller{
		Tenant:        c.Tenant,
		Subject:       c.Subject,
		Principal:     c.Principal,
		BreakGlass:    c.BreakGlass,
		CorrelationID: c.CorrelationID,
	}, true
}
