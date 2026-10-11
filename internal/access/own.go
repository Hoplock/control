// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"context"
	"fmt"
	"strings"

	"github.com/hoplock/control/internal/store"
)

// A requester's own door into a registered approval workflow (M10, 0016).
//
// "A developer asks for access for themselves" — M10's own example — without
// being given the power the workflow exists to govern. Everything here is 0012's
// request path, entered with the holder fixed to the caller:
//
//   - THE HOLDER IS THE ACTOR, AND NOTHING ELSE. A spec that names a subject,
//     or an actor with no person behind it, is a wiring fault and so an
//     outage, the way Actor.check treats a missing principal: the transport
//     resolves the subject from the credential and refuses a credential that
//     has none before it gets here.
//   - WITH NO WORKFLOW REGISTERED THE DOOR IS SHUT, before anything is
//     stored (ErrNoWorkflow). That is the one place this differs from an
//     administrator's create, and it is enforced here rather than only in the
//     handler so that no later caller can turn a self-request into a
//     self-grant: this path never reaches createDirect.
//   - "OWN" MEANS HOLDER. A request a grant administrator raised on the
//     caller's behalf is the caller's to read and withdraw too; withdrawing a
//     request for your own access cannot widen anything. Somebody else's
//     request is not found, by a query that names the subject (M18's rule for
//     the tenant, applied to the subject), never found and then refused.

// RequestOwn asks the registered workflow for a grant whose holder is the actor.
func (s *Service) RequestOwn(ctx context.Context, tenant store.Tenant, actor Actor, spec Spec) (Created, error) {
	if err := actor.self(); err != nil {
		return Created{}, err
	}
	if strings.TrimSpace(spec.Subject) != "" {
		return Created{}, fmt.Errorf("access: a request for oneself names no holder; the holder is the caller")
	}
	if s.workflow == nil {
		return Created{}, ErrNoWorkflow
	}
	spec.Subject = actor.Subject
	now := s.now().UTC()
	spec, _, err := normalize(spec, now, s.maxDuration)
	if err != nil {
		return Created{}, err
	}
	return s.request(ctx, tenant, actor, spec, now)
}

// OwnRequests returns the requests whose holder is subject, newest first.
func (s *Service) OwnRequests(ctx context.Context, tenant store.Tenant, subject string, q store.GrantRequestQuery) ([]store.GrantRequest, error) {
	if strings.TrimSpace(subject) == "" {
		return nil, errNoSubject
	}
	rows, err := s.store.GrantRequests().ListForSubject(ctx, tenant, subject, q)
	if err != nil {
		return nil, err
	}
	for _, req := range rows {
		if req.State == store.GrantRequestPending {
			// Reading a pending request is the moment this server learns
			// the tenant has one to poll, as Request says.
			s.Track(tenant)
			break
		}
	}
	return rows, nil
}

// OwnRequest returns one request whose holder is subject, asking the workflow
// first if it is still pending — exactly as Request does.
func (s *Service) OwnRequest(ctx context.Context, tenant store.Tenant, subject, requestID string) (Created, error) {
	if strings.TrimSpace(subject) == "" {
		return Created{}, errNoSubject
	}
	req, err := s.store.GrantRequests().GetForSubject(ctx, tenant, subject, requestID)
	if err != nil {
		return Created{}, err
	}
	return s.read(ctx, tenant, req)
}

// CancelOwn withdraws a pending request whose holder is the actor — exactly as
// Cancel does, audited with the requester as the actor.
func (s *Service) CancelOwn(ctx context.Context, tenant store.Tenant, actor Actor, requestID, reason string) (Created, error) {
	if err := actor.self(); err != nil {
		return Created{}, err
	}
	reason = strings.TrimSpace(reason)
	if err := prose("reason", reason, maxReasonLen, false); err != nil {
		return Created{}, err
	}
	req, err := s.store.GrantRequests().GetForSubject(ctx, tenant, actor.Subject, requestID)
	if err != nil {
		return Created{}, err
	}
	return s.cancel(ctx, tenant, actor, req, reason)
}

// errNoSubject is a self-scoped call with nobody behind it. Not a validation
// error: the transport refuses such a caller before it gets here, so this is a
// wiring fault, and a fault is an outage (M11).
var errNoSubject = fmt.Errorf("access: a request of one's own needs the person behind the credential")

// self checks an actor acting on its own requests: a credential, and a person
// behind it.
func (a Actor) self() error {
	if err := a.check(); err != nil {
		return err
	}
	if strings.TrimSpace(a.Subject) == "" {
		return errNoSubject
	}
	return nil
}
