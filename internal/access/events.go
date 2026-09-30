// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// The acts this package records and announces. They are stable codes: an audit
// query, a SIEM rule and a notification channel all filter on them.
const (
	// EventGrantCreated is a grant that now exists — an administrator's, or
	// one a workflow approved.
	EventGrantCreated = "grant.created"
	// EventGrantRevoked is a grant an operator withdrew.
	EventGrantRevoked = "grant.revoked"
	// EventGrantRequested is a request put to a registered workflow.
	EventGrantRequested = "grant.requested"
	// EventGrantRequestClosed is a request that left pending without
	// producing a grant: denied, expired, cancelled or failed. An approved
	// request is announced as the grant it produced.
	EventGrantRequestClosed = "grant.request_closed"
)

// Actor is who acts on a grant, and through which request.
type Actor struct {
	// Principal is the north-bound credential that acted (0011). Required:
	// every act on a grant is attributable to a credential.
	Principal string
	// Subject is the person behind it; empty for a machine token.
	Subject string
	// DisplayName is for notifications and the console, never for matching.
	DisplayName string
	// BreakGlass reports a break-glass credential (M7). It is copied onto
	// every record the act writes, asserted rather than inferred.
	BreakGlass bool
	// CorrelationID ties the act to the request log line.
	CorrelationID string
}

func (a Actor) grantActor() store.GrantActor {
	return store.GrantActor{Subject: a.Subject, Principal: a.Principal, BreakGlass: a.BreakGlass}
}

func (a Actor) check() error {
	if strings.TrimSpace(a.Principal) == "" {
		// Not a validation error: the transport always has a principal on
		// an authenticated route, so this is a wiring fault, and a fault is
		// an outage (M11).
		return fmt.Errorf("access: an act on a grant needs the credential that performed it")
	}
	return nil
}

// Event is one act, as it is recorded and announced.
type Event struct {
	// Name is one of the Event constants.
	Name string
	// Actor is who acted. It is the zero Actor when a workflow's decision was
	// applied by a poll: nobody acted then, and the request names who asked.
	Actor Actor
	// Workflow names the registered workflow whose decision this applies,
	// when one did.
	Workflow string
	// Grant is the grant acted on, when there is one.
	Grant *store.Grant
	// Request is the workflow request involved, when there is one.
	Request *store.GrantRequest
	// At is when it happened.
	At time.Time
}

// Recorder writes the audit record of an act on a grant (M8).
//
// It is handed the TRANSACTION the act is performed in, and that is the whole
// point of the interface: the grant row and the record of who created it commit
// together or not at all. A grant this server cannot write down is a grant it
// does not create, and a record of a grant that was never created is a record
// that lies. `audit.Emitter` implements it.
type Recorder interface {
	GrantEvent(ctx context.Context, tx *store.Store, tenant store.Tenant, ev Event) error
}

// Revoker publishes on the revocation stream the fleet holds open (M9, 0009).
// `*revoke.Bus` implements it.
type Revoker interface {
	Kill(ctx context.Context, tenant store.Tenant, aud revoke.Audience, k revoke.Kill) (revoke.Receipt, error)
	Invalidate(ctx context.Context, tenant store.Tenant, aud revoke.Audience, inv revoke.Invalidation) (revoke.Receipt, error)
}

// Notifier hands an operator notification on (ext.Notifier's core answer, the
// deployment's webhook, plus every registered notifier). It returns nothing,
// because nothing about access may wait on it or fail because of it: delivery
// is best-effort and never gates an act (M11).
type Notifier interface {
	Notify(ctx context.Context, n ext.Notification)
}

// notification renders an event for a notifier: codes and structured
// attributes, never a sentence, because the console holds the only string
// catalogue (M21) and every channel renders the same event its own way.
func notification(tenant store.Tenant, ev Event) ext.Notification {
	n := ext.Notification{
		Tenant:     ext.Tenant(tenant),
		Kind:       ev.Name,
		Severity:   ext.SeverityInfo,
		OccurredAt: ev.At,
		Attributes: map[string]string{},
	}
	attrs := n.Attributes
	if ev.Actor.Principal != "" {
		attrs["actor_principal"] = ev.Actor.Principal
		attrs["actor_subject"] = ev.Actor.Subject
		attrs["actor_break_glass"] = fmt.Sprint(ev.Actor.BreakGlass)
		if ev.Actor.BreakGlass {
			// A break-glass credential widening access is not routine,
			// and a notification filed at info is one nobody's paging
			// rule sees.
			n.Severity = ext.SeverityWarning
		}
	}
	if ev.Workflow != "" {
		attrs["workflow"] = ev.Workflow
	}
	if r := ev.Request; r != nil {
		n.SubjectID = r.SubjectID
		n.DedupeKey = ev.Name + ":" + r.ID
		attrs["request_id"] = r.ID
		attrs["request_state"] = string(r.State)
		attrs["scope"] = r.Scope
		attrs["not_before"] = r.NotBefore.UTC().Format(time.RFC3339)
		attrs["expires_at"] = r.ExpiresAt.UTC().Format(time.RFC3339)
		attrs["requested_by"] = r.RequestedBy.Subject
		if r.OutcomeCode != "" {
			attrs["outcome_code"] = r.OutcomeCode
		}
		if r.ExternalRef != "" {
			attrs["external_ref"] = r.ExternalRef
		}
	}
	if g := ev.Grant; g != nil {
		// A grant outranks its request for the fields they share: it is the
		// thing that now exists.
		n.SubjectID = g.SubjectID
		n.DedupeKey = ev.Name + ":" + g.ID
		attrs["grant_id"] = g.ID
		attrs["scope"] = g.Scope
		attrs["origin"] = string(PolicyOrigin(g.Origin))
		attrs["not_before"] = g.NotBefore.UTC().Format(time.RFC3339)
		attrs["expires_at"] = g.ExpiresAt.UTC().Format(time.RFC3339)
		if g.ExternalRef != "" {
			attrs["external_ref"] = g.ExternalRef
		}
		if ev.Name == EventGrantRevoked {
			attrs["revoke_reason"] = g.RevokeReason
		}
	}
	return n
}
