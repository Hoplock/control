// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// DefaultRevokeReason is what the holder is shown when a grant was revoked with
// no reason recorded. This surface requires one, so it is reached only by a
// grant withdrawn some other way; it says what happened and nothing more,
// because the text is displayed verbatim (the contract's disclosure rule).
const DefaultRevokeReason = "Your temporary access grant was revoked by an administrator."

// ExternalEndReason is what the holder is shown when the external system
// that opened their window ended it. It is Control's text, never the
// system's: it is displayed verbatim as the session ends (the contract's
// disclosure rule), and the references it does not name are in the record.
const ExternalEndReason = "The access window this session depended on has ended."

// DeactivatedByDirectoryReason is what the holder is shown when their
// organisation's directory deactivated them (0040): the grants and the sessions
// a deactivation ends are ended with it. It is Control's text, never the
// directory's reason code, which is a code and is recorded, and never
// DefaultRevokeReason, which says "by an administrator" and would be false.
const DeactivatedByDirectoryReason = "Your account was deactivated by your organisation's directory."

// settlePassTimeout bounds the second pass, which runs whether or not the
// caller is still waiting: a revocation half-delivered because a client hung up
// is the failure the pass exists to prevent.
const settlePassTimeout = 10 * time.Second

// Revocation is what revoking a grant did.
type Revocation struct {
	// Grant is the grant as it now stands.
	Grant store.Grant
	// Revoked reports that THIS call withdrew it. False means it had already
	// been revoked: the first revocation's time, revoker and reason stand,
	// and this call sent the kill and the invalidation again.
	Revoked bool
	// Sessions are the sessions the grant backed that were ended by name.
	Sessions []store.GrantSession
	// Widened reports that the grant backed more sessions than this server
	// ends by name, so every session of the holder was ended instead.
	Widened bool
	// EventIDs are the revocation-stream events published, in order.
	EventIDs []string

	// at is the instant the revocation was stamped with, and untouched
	// reports that it was left alone because the grant had expired: what
	// Finish needs from the transaction that made it.
	at        time.Time
	untouched bool
}

// Revoke withdraws a grant, ends the sessions it backed, and tells the holder
// why.
//
// ORDER IS THE DESIGN. The withdrawal and its audit record commit together
// first, so from that instant no authorize call can use the grant. Then the
// holder's cached decisions are dropped and the sessions it backed are killed
// — the invalidation first, so a user whose session ends and who reconnects at
// once reaches this server rather than a decision cached under the grant.
// Then, once one authorize call's budget has passed, the same again: an
// authorize that read the grant as live a moment before the revocation
// committed has by then recorded its session, and the second pass ends it.
// Without that pass, a revocation racing a login would leave that one session
// running until the grant's original expiry.
func (s *Service) Revoke(ctx context.Context, tenant store.Tenant, actor Actor, grantID, reason string) (Revocation, error) {
	if err := actor.check(); err != nil {
		return Revocation{}, err
	}
	reason, err := NormalizeHolderReason(reason)
	if err != nil {
		return Revocation{}, err
	}
	return s.revoke(ctx, tenant, actor, grantID, reason, nil, nil)
}

// EndExternal revokes the grant a pushed window became because the external
// system that asserted the window says it has ended (M16, 0017). The actor is
// the integration's credential that pushed the end.
//
// It is Revoke — the same transaction, record, announcement, two passes and
// ErrUndelivered — with ExternalEndReason as the reason the holder is shown and
// what the end stated on the record. Two things differ, and both are about not
// rewriting what is already true:
//
//   - A grant that EXPIRED before the end arrived, and was never revoked, is
//     returned untouched: Revoked false, nothing written, published or
//     announced. Revoking it would make Grant.State answer `revoked` — a
//     second, false answer to when access stopped. Expiry is judged at the
//     instant the revocation would be stamped with, so no revocation is ever
//     recorded after the grant's own expiry.
//   - A grant no push produced is refused as an outage, not a refusal: the
//     push receiver only ever passes a grant it found by its assertion, so
//     anything else is a wiring fault.
//
// A grant already revoked is handled exactly as Revoke handles one: the first
// revocation's time, actor and reason stand, and the kill and the invalidation
// are sent again — which is what makes an end whose delivery failed safe to
// push again.
func (s *Service) EndExternal(ctx context.Context, tenant store.Tenant, actor Actor, grantID string, end ExternalEnd) (Revocation, error) {
	if err := actor.check(); err != nil {
		return Revocation{}, err
	}
	if !AdditionalContextValid(end.AdditionalContext) {
		return Revocation{}, &ValidationError{Field: "additional_context", Problem: ProblemInvalid}
	}
	return s.revoke(ctx, tenant, actor, grantID, ExternalEndReason, &end, nil)
}

// revoke is the one revocation path, for an operator's revocation and for an
// external system's end alike: two paths would be two places for the settle
// pass to drift apart. end is nil for an operator's.
func (s *Service) revoke(ctx context.Context, tenant store.Tenant, actor Actor, grantID, reason string, end *ExternalEnd, action *ActionRef) (Revocation, error) {
	var out Revocation
	err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		var err error
		out, err = s.revokeWithin(ctx, tx, tenant, actor, grantID, reason, end, action)
		return err
	})
	if err != nil {
		return Revocation{}, err
	}
	return s.finish(ctx, tenant, actor, out, end, action)
}

// revokeWithin is the transaction half of a revocation: the withdrawal and its
// audit record, which commit together or not at all.
func (s *Service) revokeWithin(ctx context.Context, tx *store.Store, tenant store.Tenant, actor Actor, grantID, reason string, end *ExternalEnd, action *ActionRef) (Revocation, error) {
	now := s.now().UTC()
	if end != nil {
		g, err := tx.Grants().Get(ctx, tenant, grantID)
		if err != nil {
			return Revocation{}, err
		}
		if g.External.AssertionID == "" {
			return Revocation{}, fmt.Errorf("access: grant %s was not produced by a push, and only a push's own system ends one", grantID)
		}
		if g.RevokedAt.IsZero() && !now.Before(g.ExpiresAt) {
			// Over already, by its own window. Nothing to end,
			// and nothing true to add.
			return Revocation{Grant: g, at: now, untouched: true}, nil
		}
	}
	revoked, err := tx.Grants().Revoke(ctx, tenant, grantID, store.GrantRevocation{
		At: now, By: actor.grantActor(), Reason: reason,
	})
	if err != nil {
		return Revocation{}, err
	}
	g, err := tx.Grants().Get(ctx, tenant, grantID)
	if err != nil {
		return Revocation{}, err
	}
	out := Revocation{Grant: g, Revoked: revoked, at: now}
	if !revoked {
		// Already withdrawn, by someone and for a reason that stand.
		// Recording a second revocation would be a second answer to
		// "when did access stop", and it would not be more true.
		return out, nil
	}
	if err := s.recorder.GrantEvent(ctx, tx, tenant, Event{
		Name: EventGrantRevoked, Actor: actor, Grant: &g, Ended: end, Action: action, At: now,
	}); err != nil {
		return Revocation{}, err
	}
	return out, nil
}

// finish is the half after the commit: the announcement, and the publication
// the revocation owes the fleet.
func (s *Service) finish(ctx context.Context, tenant store.Tenant, actor Actor, out Revocation, end *ExternalEnd, action *ActionRef) (Revocation, error) {
	if out.untouched {
		return out, nil
	}
	if out.Revoked {
		s.announce(ctx, tenant, Event{Name: EventGrantRevoked, Actor: actor, Grant: &out.Grant, Ended: end, Action: action, At: out.at})
	}

	grantID := out.Grant.ID
	if err := s.withdraw(ctx, tenant, &out); err != nil {
		s.log.ErrorContext(ctx, "a grant was revoked and ending its sessions could not be published",
			"event", "grant_revocation_undelivered",
			"tenant", tenant.String(),
			"grant_id", grantID,
			"error", err.Error(),
		)
		return out, fmt.Errorf("%w: %v", ErrUndelivered, err)
	}
	return out, nil
}

// RevokeWithin revokes a grant inside tx, the transaction an inbound action's
// ledger row commits in (0039), and records it there as `grant.revoked`, with
// the action named. Nothing is published: the caller commits, then calls
// [Service.Finish] with the result. The holder's reason is the action's,
// validated like an operator's.
func (s *Service) RevokeWithin(ctx context.Context, tx *store.Store, tenant store.Tenant, actor Actor, grantID, reason string, action *ActionRef) (Revocation, error) {
	if err := actor.check(); err != nil {
		return Revocation{}, err
	}
	reason, err := NormalizeHolderReason(reason)
	if err != nil {
		return Revocation{}, err
	}
	return s.revokeWithin(ctx, tx, tenant, actor, grantID, reason, nil, action)
}

// Finish announces and publishes what a revocation made by [Service.RevokeWithin]
// owes, once its transaction has committed: the same two passes a revocation
// makes. A failure to publish is [ErrUndelivered], and the grant stays revoked.
func (s *Service) Finish(ctx context.Context, tenant store.Tenant, actor Actor, rev Revocation, action *ActionRef) (Revocation, error) {
	return s.finish(ctx, tenant, actor, rev, nil, action)
}

// Resend publishes again what the revocation of a grant owes the fleet, for a
// grant that is already revoked: how an inbound action asked for a second time
// gets its kill delivered after the first publication failed. Nothing is
// written; the first revocation's time, actor and reason stand. A grant that
// is not revoked is ErrNotFound-shaped: there is nothing to resend.
func (s *Service) Resend(ctx context.Context, tenant store.Tenant, grantID string) (Revocation, error) {
	g, err := s.store.Grants().Get(ctx, tenant, grantID)
	if err != nil {
		return Revocation{}, err
	}
	if g.RevokedAt.IsZero() {
		return Revocation{}, fmt.Errorf("access: grant %s is not revoked, so there is nothing to resend: %w", grantID, store.ErrNotFound)
	}
	out := Revocation{Grant: g}
	if err := s.withdraw(ctx, tenant, &out); err != nil {
		s.log.ErrorContext(ctx, "a revoked grant's sessions could not be told again",
			"event", "grant_revocation_undelivered",
			"tenant", tenant.String(),
			"grant_id", grantID,
			"error", err.Error(),
		)
		return out, fmt.Errorf("%w: %v", ErrUndelivered, err)
	}
	return out, nil
}

// withdraw publishes what a revocation owes the fleet, twice.
func (s *Service) withdraw(ctx context.Context, tenant store.Tenant, rev *Revocation) error {
	g := rev.Grant
	reason := g.RevokeReason
	if reason == "" {
		reason = DefaultRevokeReason
	}
	ended := map[store.GrantSession]bool{}

	pass := func(ctx context.Context) error {
		inv, err := s.revoker.Invalidate(ctx, tenant, revoke.Everyone(), revoke.Invalidation{Subject: g.SubjectID})
		if err != nil {
			return fmt.Errorf("cache_invalidate for the holder: %w", err)
		}
		rev.EventIDs = append(rev.EventIDs, inv.EventID)

		sessions, cut, err := s.store.Decisions().SessionsByGrant(ctx, tenant, g.ID, s.killLimit)
		if err != nil {
			return fmt.Errorf("finding the sessions the grant backed: %w", err)
		}
		byProxy := map[string][]string{}
		for _, gs := range sessions {
			if ended[gs] {
				continue
			}
			ended[gs] = true
			rev.Sessions = append(rev.Sessions, gs)
			byProxy[gs.ProxyID] = append(byProxy[gs.ProxyID], gs.SessionID)
		}
		proxies := make([]string, 0, len(byProxy))
		for p := range byProxy {
			proxies = append(proxies, p)
		}
		slices.Sort(proxies)
		for _, p := range proxies {
			// One event per proxy, addressed to that proxy: a session id
			// is the proxy's own, and each hop of a chained session holds
			// its own end of it.
			rcpt, err := s.revoker.Kill(ctx, tenant, revoke.ToProxy(p), revoke.Kill{
				SessionIDs: byProxy[p], Reason: reason,
			})
			if err != nil {
				return fmt.Errorf("session_kill to %s: %w", p, err)
			}
			rev.EventIDs = append(rev.EventIDs, rcpt.EventID)
		}

		if cut && !rev.Widened {
			// More sessions than this server ends by name. Ending every
			// session of the holder ends some that no grant backed, which
			// costs the holder a reconnect; ending too few would leave
			// revoked access running, which costs the estate.
			rcpt, err := s.revoker.Kill(ctx, tenant, revoke.Everyone(), revoke.Kill{
				Subject: g.SubjectID, Reason: reason,
			})
			if err != nil {
				return fmt.Errorf("session_kill for the holder: %w", err)
			}
			rev.Widened = true
			rev.EventIDs = append(rev.EventIDs, rcpt.EventID)
		}
		return nil
	}

	if err := pass(ctx); err != nil {
		return err
	}
	if g.RevokedAt.Before(g.NotBefore) || !g.RevokedAt.Before(g.ExpiresAt) {
		// It was not live when it was revoked — not yet open, or already
		// closed — so no authorize call can have been using it, and there
		// is no race for a second pass to close.
		return nil
	}

	// The settle pass. It is not bound to the caller's context: a client
	// that stops waiting does not get to leave a revocation half-delivered.
	timer := time.NewTimer(s.settle)
	defer timer.Stop()
	<-timer.C
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settlePassTimeout)
	defer cancel()
	if err := pass(settleCtx); err != nil {
		return fmt.Errorf("second pass: %w", err)
	}
	return nil
}

// IsUndelivered reports a revocation that is recorded and not yet published.
func IsUndelivered(err error) bool { return errors.Is(err, ErrUndelivered) }
