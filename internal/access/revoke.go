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
	reason, err := normalizeRevokeReason(reason)
	if err != nil {
		return Revocation{}, err
	}
	now := s.now().UTC()

	var out Revocation
	err = s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		revoked, err := tx.Grants().Revoke(ctx, tenant, grantID, store.GrantRevocation{
			At: now, By: actor.grantActor(), Reason: reason,
		})
		if err != nil {
			return err
		}
		g, err := tx.Grants().Get(ctx, tenant, grantID)
		if err != nil {
			return err
		}
		out = Revocation{Grant: g, Revoked: revoked}
		if !revoked {
			// Already withdrawn, by someone and for a reason that stand.
			// Recording a second revocation would be a second answer to
			// "when did access stop", and it would not be more true.
			return nil
		}
		return s.recorder.GrantEvent(ctx, tx, tenant, Event{
			Name: EventGrantRevoked, Actor: actor, Grant: &g, At: now,
		})
	})
	if err != nil {
		return Revocation{}, err
	}
	if out.Revoked {
		s.announce(ctx, tenant, Event{Name: EventGrantRevoked, Actor: actor, Grant: &out.Grant, At: now})
	}

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
