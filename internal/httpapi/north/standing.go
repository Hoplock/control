// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/standing"
	"github.com/hoplock/control/internal/store"
)

// Standing denials (0039, M28): a lock on a subject and a quarantine on a
// target.
//
// THE SAME CODE AN INBOUND ACTION RUNS. These handlers call
// [standing.Service], which internal/action calls too, so an operator and a
// registered ext.ActionHandler place and lift a denial through one
// implementation: the row, the record, the sessions it ends and the cached
// decisions it drops. That is what makes `ActionHandler`'s promise — an
// operator can do all of it through the north-bound API and the console — true
// for every kind it can execute (M15).
//
// They sit under `session:revoke`, the permission whose holders can already
// end every session in the tenant, which is a larger lever than a lock. A
// lock already standing, and no lock to lift, are `200`s that say so: the act
// is idempotent, and a client that retries after an undelivered publication
// must not be told it did something wrong.

// lockView is a lock as an operator reads it.
type lockView struct {
	Subject     string    `json:"subject"`
	LockedAt    time.Time `json:"locked_at"`
	LockedBy    actorView `json:"locked_by"`
	ReasonCode  string    `json:"reason_code,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	ExternalRef string    `json:"external_ref,omitempty"`
	// ActionID is the inbound action that placed it, empty for an operator's.
	ActionID string `json:"action_id,omitempty"`
}

func viewLock(l store.SubjectLock) *lockView {
	return &lockView{
		Subject: l.SubjectID, LockedAt: l.LockedAt.UTC(), LockedBy: viewStandingActor(l.LockedBy),
		ReasonCode: l.ReasonCode, Reason: l.Reason, ExternalRef: l.ExternalRef, ActionID: l.ActionID,
	}
}

// quarantineView is a quarantine as an operator reads it.
type quarantineView struct {
	Target        string    `json:"target"`
	QuarantinedAt time.Time `json:"quarantined_at"`
	QuarantinedBy actorView `json:"quarantined_by"`
	ReasonCode    string    `json:"reason_code,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	ExternalRef   string    `json:"external_ref,omitempty"`
	ActionID      string    `json:"action_id,omitempty"`
}

func viewQuarantine(q store.TargetQuarantine) *quarantineView {
	return &quarantineView{
		Target: q.TargetID, QuarantinedAt: q.QuarantinedAt.UTC(), QuarantinedBy: viewStandingActor(q.QuarantinedBy),
		ReasonCode: q.ReasonCode, Reason: q.Reason, ExternalRef: q.ExternalRef, ActionID: q.ActionID,
	}
}

func viewStandingActor(a store.StandingActor) actorView {
	return actorView{Subject: a.Subject, PrincipalID: a.Principal, BreakGlass: a.BreakGlass}
}

// lockOf reads the lock on a subject, nil when none stands.
func (s *Server) lockOf(ctx context.Context, tenant store.Tenant, subject string) (*store.SubjectLock, error) {
	l, err := s.st.SubjectLocks().Get(ctx, tenant, subject)
	switch {
	case store.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return &l, nil
}

// quarantineOf reads the quarantine on a target, nil when none stands.
func (s *Server) quarantineOf(ctx context.Context, tenant store.Tenant, target string) (*store.TargetQuarantine, error) {
	q, err := s.st.TargetQuarantines().Get(ctx, tenant, target)
	switch {
	case store.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return &q, nil
}

// standingBody is what a lock or a quarantine is placed with, and what a lift
// is recorded with. Every field is optional.
type standingBody struct {
	// ReasonCode is a stable code a report can group by.
	ReasonCode string `json:"reason_code"`
	// Reason is what a lock shows the holder as their sessions end, verbatim,
	// and is recorded; a quarantine and a lift only record it. An omitted
	// reason on a lock is Control's words for an administrator's act.
	Reason string `json:"reason"`
}

// decodeOptionalBody reads a body that may be absent: an empty one is the zero
// value, and anything else is held to decodeJSON's rules.
func decodeOptionalBody(r *http.Request, into any) error {
	err := decodeJSON(r, into)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// standingActor is the credential that acted, from the request and never from
// its body.
func (s *Server) standingActor(w http.ResponseWriter, r *http.Request) (standing.Actor, bool) {
	a, ok := s.actorFrom(w, r)
	if !ok {
		return standing.Actor{}, false
	}
	return standing.Actor{
		Principal: a.Principal, Subject: a.Subject, BreakGlass: a.BreakGlass, CorrelationID: a.CorrelationID,
	}, true
}

// requireStanding answers a route on a server built without the service. New
// refuses that, so it is a wiring fault and an outage (M11).
func (s *Server) requireStanding(w http.ResponseWriter, r *http.Request) bool {
	if s.standing != nil {
		return true
	}
	s.fail(w, r, "standing denials", errors.New("north: no standing-denial service is wired"))
	return false
}

// standingFailure answers a standing denial's failure. A publication that
// failed after the commit is its own answer: the denial STANDS, and asking
// again publishes again.
func (s *Server) standingFailure(w http.ResponseWriter, r *http.Request, op string, params map[string]any, err error) {
	if standing.IsUndelivered(err) {
		params["stands"] = true
		writeError(w, http.StatusServiceUnavailable, Error{
			Code: CodeDenialUndelivered, CorrelationID: CorrelationIDFrom(r.Context()), Parameters: params,
			Message: "the denial is in force and no new access can be obtained under it, but ending the sessions " +
				"and dropping the cached decisions it owes could not be published; ask again to re-send",
		})
		return
	}
	s.inventoryFailure(w, r, op, err)
}

type lockAnswer struct {
	Subject string `json:"subject"`
	// Locked is always true on a lock answer; Placed is false when a lock
	// already stood, whose first time, actor and reason are the ones shown.
	Locked bool      `json:"locked"`
	Placed bool      `json:"placed"`
	Lock   *lockView `json:"lock"`
}

func (s *Server) handleSubjectLock(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok || !s.requireStanding(w, r) {
		return
	}
	actor, ok := s.standingActor(w, r)
	if !ok {
		return
	}
	var body standingBody
	if err := decodeOptionalBody(r, &body); err != nil {
		s.refuseBody(w, r, err)
		return
	}
	if body.Reason == "" {
		body.Reason = standing.LockedByAdministrator
	}
	id := r.PathValue("subject")
	res, err := s.standing.Lock(r.Context(), tenant, actor, standing.LockRequest{
		SubjectID: id, ReasonCode: body.ReasonCode, Reason: body.Reason,
	})
	var ve *access.ValidationError
	if errors.As(err, &ve) {
		s.invalid(w, r, ve.Field, ve.Problem, ve.Limit)
		return
	}
	if err != nil {
		s.standingFailure(w, r, "lock subject", map[string]any{"subject": id, "locked": true}, err)
		return
	}
	answer(w, http.StatusOK, lockAnswer{Subject: res.Lock.SubjectID, Locked: true, Placed: res.Placed, Lock: viewLock(res.Lock)})
}

type unlockAnswer struct {
	Subject string `json:"subject"`
	// Unlocked is true when THIS call lifted a lock; false means none stood,
	// and nothing changed.
	Unlocked bool `json:"unlocked"`
	// Lock is the lock that was lifted.
	Lock *lockView `json:"lock,omitempty"`
}

func (s *Server) handleSubjectUnlock(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok || !s.requireStanding(w, r) {
		return
	}
	actor, ok := s.standingActor(w, r)
	if !ok {
		return
	}
	var body standingBody
	if err := decodeOptionalBody(r, &body); err != nil {
		s.refuseBody(w, r, err)
		return
	}
	id := r.PathValue("subject")
	res, err := s.standing.Unlock(r.Context(), tenant, actor, standing.LiftRequest{
		ID: id, ReasonCode: body.ReasonCode, Reason: body.Reason,
	})
	var ve *access.ValidationError
	if errors.As(err, &ve) {
		s.invalid(w, r, ve.Field, ve.Problem, ve.Limit)
		return
	}
	if err != nil {
		s.inventoryFailure(w, r, "unlock subject", err)
		return
	}
	out := unlockAnswer{Subject: id, Unlocked: res.Lifted}
	if res.Lifted {
		out.Lock = viewLock(res.Lock)
	}
	answer(w, http.StatusOK, out)
}

type quarantineAnswer struct {
	Target      string          `json:"target"`
	Quarantined bool            `json:"quarantined"`
	Placed      bool            `json:"placed"`
	Quarantine  *quarantineView `json:"quarantine"`
}

func (s *Server) handleTargetQuarantine(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok || !s.requireStanding(w, r) {
		return
	}
	actor, ok := s.standingActor(w, r)
	if !ok {
		return
	}
	var body standingBody
	if err := decodeOptionalBody(r, &body); err != nil {
		s.refuseBody(w, r, err)
		return
	}
	id := r.PathValue("target")
	res, err := s.standing.Quarantine(r.Context(), tenant, actor, standing.QuarantineRequest{
		TargetID: id, ReasonCode: body.ReasonCode, Reason: body.Reason,
	})
	var ve *access.ValidationError
	if errors.As(err, &ve) {
		s.invalid(w, r, ve.Field, ve.Problem, ve.Limit)
		return
	}
	if err != nil {
		s.standingFailure(w, r, "quarantine target", map[string]any{"target": id, "quarantined": true}, err)
		return
	}
	answer(w, http.StatusOK, quarantineAnswer{
		Target: res.Quarantine.TargetID, Quarantined: true, Placed: res.Placed, Quarantine: viewQuarantine(res.Quarantine),
	})
}

type releaseAnswer struct {
	Target string `json:"target"`
	// Released is true when THIS call lifted a quarantine; false means none
	// stood, and nothing changed.
	Released   bool            `json:"released"`
	Quarantine *quarantineView `json:"quarantine,omitempty"`
}

func (s *Server) handleTargetRelease(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok || !s.requireStanding(w, r) {
		return
	}
	actor, ok := s.standingActor(w, r)
	if !ok {
		return
	}
	var body standingBody
	if err := decodeOptionalBody(r, &body); err != nil {
		s.refuseBody(w, r, err)
		return
	}
	id := r.PathValue("target")
	res, err := s.standing.Release(r.Context(), tenant, actor, standing.LiftRequest{
		ID: id, ReasonCode: body.ReasonCode, Reason: body.Reason,
	})
	var ve *access.ValidationError
	if errors.As(err, &ve) {
		s.invalid(w, r, ve.Field, ve.Problem, ve.Limit)
		return
	}
	if err != nil {
		s.inventoryFailure(w, r, "release target", err)
		return
	}
	out := releaseAnswer{Target: id, Released: res.Lifted}
	if res.Lifted {
		out.Quarantine = viewQuarantine(res.Quarantine)
	}
	answer(w, http.StatusOK, out)
}
