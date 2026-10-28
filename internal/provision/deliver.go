// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package provision

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// What a committed call owes the revocation stream, delivered after the commit.
//
// A deactivation revokes everything the subject holds, IN THIS ORDER (item 6):
//
//  1. the deactivation and its record commit, with the subject's north-bound
//     sessions, API tokens and outstanding certificates revoked in the same
//     transaction — from here the next authorize is a deny;
//  2. every live or scheduled grant is revoked through access.Service.Revoke,
//     which ends the sessions each one backed;
//  3. every pending grant request is withdrawn through the cancel path, so a
//     workflow's later approval creates nothing;
//  4. `cache_invalidate` and then `session_kill` for the subject are published,
//     and again one decision budget later when this call placed the
//     deactivation, for an authorize that read the subject a moment before the
//     commit.
//
// A step that fails does not stop the others. The deactivation stands, the link
// remembers that its revocations are owed, and the same call — or any later
// call on the id — finishes them.

// grantRevocationConcurrency bounds the grant revocations one deactivation runs
// at once: each waits one decision budget for its settle pass, so one after
// another would hold a directory's call for as long as the subject had grants.
const grantRevocationConcurrency = 8

// maxSubjectInvalidations is how many members of a group are invalidated one
// event each. Past it the tenant's cached decisions are dropped with one event,
// as a quarantine drops them (M28): a group of thousands would otherwise push
// the tenant's revocation log past its replay floor and resync every proxy.
const maxSubjectInvalidations = 64

// deliverSubject finishes a subject call: the deactivation's revocations when
// the call was a deactivation or an earlier one's are still owed, else the
// cache invalidation an applied change owes.
func (s *Sink) deliverSubject(ctx context.Context, op string, tenant store.Tenant, out subjectOutcome) error {
	link := out.link
	if !link.Provisioned {
		// Nothing was ever provisioned under the id, so nothing can be held
		// and nothing can be cached: a deactivation kept for an unknown id
		// has nothing to revoke.
		return nil
	}
	if out.deactivate || link.DeactivationOwed {
		return s.cascade(ctx, op, tenant, link, out.placed)
	}
	if link.InvalidateOwed {
		if err := s.publish(ctx, tenant, link.SubjectID, false, false); err != nil {
			return s.unavailable(ctx, op, "dropping the subject's cached decisions", err)
		}
		if err := s.store.DirectoryLinks().Settle(ctx, tenant, link.ExternalID, true, false); err != nil {
			return s.internal(ctx, op, "record the invalidation as delivered", err)
		}
	}
	return nil
}

// cascade revokes everything a deactivated subject holds.
func (s *Sink) cascade(ctx context.Context, op string, tenant store.Tenant, link store.DirectoryLink, settle bool) error {
	actor := access.Actor{Principal: s.principal}
	var undelivered, failed error

	ids, err := s.heldGrants(ctx, tenant, link.SubjectID)
	if err != nil {
		failed = errors.Join(failed, fmt.Errorf("reading the subject's grants: %w", err))
	}
	notTold, notRevoked := s.revokeGrants(ctx, tenant, actor, ids)
	undelivered, failed = errors.Join(undelivered, notTold), errors.Join(failed, notRevoked)
	if err := s.withdrawRequests(ctx, tenant, actor, link.SubjectID); err != nil {
		failed = errors.Join(failed, err)
	}
	if err := s.publish(ctx, tenant, link.SubjectID, true, settle); err != nil {
		var published *recordError
		if errors.As(err, &published) {
			failed = errors.Join(failed, err)
		} else {
			undelivered = errors.Join(undelivered, err)
		}
	}

	switch {
	case undelivered != nil:
		return s.unavailable(ctx, op, "ending what the deactivated subject holds", errors.Join(undelivered, failed))
	case failed != nil:
		return s.internal(ctx, op, "finish revoking what the deactivated subject holds", failed)
	}
	if err := s.store.DirectoryLinks().Settle(ctx, tenant, link.ExternalID, true, true); err != nil {
		return s.internal(ctx, op, "record the deactivation as delivered", err)
	}
	return nil
}

// heldGrants returns the ids of every grant the subject holds that is live or
// scheduled at this instant: each is a way back in.
func (s *Sink) heldGrants(ctx context.Context, tenant store.Tenant, subjectID string) ([]string, error) {
	now := s.now()
	var ids []string
	for _, state := range []store.GrantState{store.GrantActive, store.GrantScheduled} {
		p := store.Page{Limit: store.DefaultPageLimit}
		for {
			page, err := s.store.Grants().Page(ctx, tenant, store.GrantQuery{SubjectID: subjectID, State: state, At: now}, p)
			if err != nil {
				return ids, err
			}
			for _, g := range page.Rows {
				ids = append(ids, g.ID)
			}
			if !page.More || len(page.Rows) == 0 {
				break
			}
			last := page.Rows[len(page.Rows)-1]
			p.After = []any{last.CreatedAt, last.ID}
		}
	}
	return ids, nil
}

// revokeGrants revokes grants through the grant service, a few at a time, and
// returns the revocations recorded and not published apart from those that
// failed. A grant revoked meanwhile by somebody else is revoked already: the
// first revocation's actor and reason stand.
func (s *Sink) revokeGrants(ctx context.Context, tenant store.Tenant, actor access.Actor, ids []string) (undelivered, failed error) {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, grantRevocationConcurrency)
	)
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			_, err := s.grants.Revoke(ctx, tenant, actor, id, DeactivatedByDirectory)
			if err == nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if access.IsUndelivered(err) {
				undelivered = errors.Join(undelivered, err)
			} else {
				failed = errors.Join(failed, fmt.Errorf("revoking grant %s: %w", id, err))
			}
		}()
	}
	wg.Wait()
	return undelivered, failed
}

// withdrawRequests withdraws every pending grant request the subject holds,
// through the grant service's cancel path, so a workflow's later approval
// creates nothing. A request decided meanwhile is not pending any more, and
// that is not a failure.
func (s *Sink) withdrawRequests(ctx context.Context, tenant store.Tenant, actor access.Actor, subjectID string) error {
	var ids []string
	p := store.Page{Limit: store.DefaultPageLimit}
	for {
		page, err := s.store.GrantRequests().PageForSubject(ctx, tenant, subjectID,
			store.GrantRequestQuery{State: store.GrantRequestPending}, p)
		if err != nil {
			return fmt.Errorf("reading the subject's pending requests: %w", err)
		}
		for _, r := range page.Rows {
			ids = append(ids, r.ID)
		}
		if !page.More || len(page.Rows) == 0 {
			break
		}
		last := page.Rows[len(page.Rows)-1]
		p.After = []any{last.RequestedAt, last.ID}
	}
	var errs error
	for _, id := range ids {
		if _, err := s.grants.Cancel(ctx, tenant, actor, id, "The subject was deactivated by the directory."); err != nil &&
			!errors.Is(err, access.ErrNotPending) {
			errs = errors.Join(errs, fmt.Errorf("withdrawing request %s: %w", id, err))
		}
	}
	return errs
}

// recordError is a publication that reached the stream and whose record could
// not be written: the act happened, and that is an outage of the audit store,
// not of the stream.
type recordError struct{ err error }

func (e *recordError) Error() string { return "recording a publication: " + e.err.Error() }
func (e *recordError) Unwrap() error { return e.err }

// publish drops the subject's cached decisions and, for a deactivation, ends
// its sessions with Control's words, each publication recorded as
// `operator.revocation.published` with the sync as the actor. With settle, the
// same again one decision budget later, unrecorded: the second pass closes a
// race, and is not a second act.
func (s *Sink) publish(ctx context.Context, tenant store.Tenant, subjectID string, kill, settle bool) error {
	pass := func(ctx context.Context, record bool) error {
		inv, err := s.revoker.Invalidate(ctx, tenant, revoke.Everyone(), revoke.Invalidation{Subject: subjectID})
		if err != nil {
			return fmt.Errorf("cache_invalidate: %w", err)
		}
		if record {
			if err := s.recordPublication(ctx, tenant, "cache_invalidate", subjectID, inv, ""); err != nil {
				return err
			}
		}
		if !kill {
			return nil
		}
		rcpt, err := s.revoker.Kill(ctx, tenant, revoke.Everyone(), revoke.Kill{Subject: subjectID, Reason: DeactivatedByDirectory})
		if err != nil {
			return fmt.Errorf("session_kill: %w", err)
		}
		if record {
			return s.recordPublication(ctx, tenant, "session_kill", subjectID, rcpt, DeactivatedByDirectory)
		}
		return nil
	}
	if err := pass(ctx, true); err != nil {
		return err
	}
	if !settle {
		return nil
	}
	timer := time.NewTimer(s.settle)
	defer timer.Stop()
	<-timer.C
	// Not bound to the caller's context: a directory that stops waiting does
	// not get to leave a deactivation half-delivered.
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := pass(settleCtx, false); err != nil {
		return fmt.Errorf("second pass: %w", err)
	}
	return nil
}

func (s *Sink) recordPublication(ctx context.Context, tenant store.Tenant, kind, subjectID string, rcpt revoke.Receipt, reason string) error {
	fields := map[string]string{
		"type": kind, "event_id": rcpt.EventID, "delivered": strconv.Itoa(rcpt.Delivered), "audience": "all",
	}
	if subjectID != "" {
		fields["subject"] = subjectID
	} else {
		fields["selector"] = "all"
	}
	if reason != "" {
		fields["reason"] = reason
	}
	if err := s.recorder.OperatorEvent(ctx, nil, tenant, audit.OperatorAct{
		Event: audit.EventRevocationPublished, Actor: audit.OperatorActor{Principal: s.principal},
		Subject: subjectID, Fields: fields,
		Message: kind + " published for a subject the directory changed",
	}); err != nil {
		return &recordError{err: err}
	}
	return nil
}

// deliverGroup drops the cached decisions of the group's members whose groups
// a group call changed: one event each, or one for the tenant past
// maxSubjectInvalidations. It kills no session: a group change narrows or
// widens what the next decision allows, and ending sessions is a deactivation's.
func (s *Sink) deliverGroup(ctx context.Context, op string, tenant store.Tenant, groupExternalID string) error {
	links, err := s.store.DirectoryLinks().Members(ctx, tenant, groupExternalID)
	if err != nil {
		return s.internal(ctx, op, "read the group's members", err)
	}
	owed := slices.DeleteFunc(links, func(l store.DirectoryLink) bool { return !l.InvalidateOwed })
	if len(owed) == 0 {
		return nil
	}
	if len(owed) > maxSubjectInvalidations {
		rcpt, err := s.revoker.Invalidate(ctx, tenant, revoke.Everyone(), revoke.Invalidation{All: true})
		if err != nil {
			return s.unavailable(ctx, op, "dropping the members' cached decisions", err)
		}
		if err := s.recordPublication(ctx, tenant, "cache_invalidate", "", rcpt, ""); err != nil {
			return s.internal(ctx, op, "record the invalidation", err)
		}
	} else {
		for _, l := range owed {
			if err := s.publish(ctx, tenant, l.SubjectID, false, false); err != nil {
				var published *recordError
				if errors.As(err, &published) {
					return s.internal(ctx, op, "record the invalidation", err)
				}
				return s.unavailable(ctx, op, "dropping the members' cached decisions", err)
			}
		}
	}
	for _, l := range owed {
		if err := s.store.DirectoryLinks().Settle(ctx, tenant, l.ExternalID, true, false); err != nil {
			return s.internal(ctx, op, "record the invalidation as delivered", err)
		}
	}
	return nil
}
