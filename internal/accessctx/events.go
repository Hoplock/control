// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package accessctx

import (
	"context"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/store"
)

// The acts this package records (M8). An accepted push is recorded by
// internal/access as the grant it became (`grant.created`, with the push's
// details); what is recorded here is everything a push or a binding change
// produced that is NOT a grant.
const (
	// EventEscalation is a push outside its integration's scope, or from a
	// credential the binding does not name. It is filed critical: the
	// interesting security event in this whole design is a scanner
	// integration asking for a host it has never scanned.
	EventEscalation = "access_context.escalation_attempt"
	// EventPushRefused is any other push refused after it was read — an
	// integration with no binding, a binding that is off, a reused id, a
	// stale assertion.
	EventPushRefused = "access_context.push_refused"
	// EventBindingPut is a scope binding created or replaced.
	EventBindingPut = "access_context.binding_put"
	// EventBindingDeleted is a scope binding removed.
	EventBindingDeleted = "access_context.binding_deleted"
)

// Event is one act, as it is recorded.
type Event struct {
	// Name is one of the Event constants.
	Name string
	// Actor is the credential that pushed, or that changed the binding.
	Actor access.Actor
	// Provider is the integration involved.
	Provider string
	// Binding is the binding written, or the one a push was judged against.
	Binding *store.AccessContextBinding
	// Assertion is what a push asserted, when it could be read.
	Assertion *ext.WindowAssertion
	// Code and Field say why a push was refused: one of the Code constants,
	// and the part of the push at fault.
	Code  string
	Field string
	// Detail is the operator-facing diagnostic.
	Detail string
	// At is when it happened.
	At time.Time
}

// Recorder writes the audit record of an act (M8). `audit.Emitter` implements
// it. A binding change is recorded inside the transaction that makes it, so a
// binding nobody can account for cannot exist; a refused push has no
// transaction, and its record is the act.
type Recorder interface {
	ContextEvent(ctx context.Context, tx *store.Store, tenant store.Tenant, ev Event) error
}
