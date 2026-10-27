// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package standing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// The words a holder is shown when a lock ends their sessions, for the two
// ways one is placed. They are Control's text: the holder sees them verbatim as
// the connection closes (the contract's disclosure rule), so each says what
// happened and nothing more, and neither is access.DefaultRevokeReason, which
// says "by an administrator" and would be false for a handler's lock.
const (
	// LockedByAdministrator is shown when an operator placed the lock and gave
	// no reason of their own.
	LockedByAdministrator = "Your access has been suspended by an administrator."
	// LockedBySecurityResponse is shown when an inbound action placed the lock
	// and its requester gave no text.
	LockedBySecurityResponse = "Your access was suspended as part of a security response."
)

// maxIDLen bounds a subject or target id, as a grant bounds its subject.
const maxIDLen = 256

// ErrBreakGlassSubject is a lock refused because the subject's row is a
// break-glass one and the caller asked for such a lock to be refused (M7): the
// break-glass path exists so that access survives the failure of everything
// else, and a third party able to lock it out holds a denial of service.
var ErrBreakGlassSubject = errors.New("standing: the subject is a break-glass subject")

// ErrUndelivered reports a denial that stands, committed and recorded, whose
// publication on the revocation stream failed: the sessions it should end
// were not told. Asking for the same act again publishes again.
var ErrUndelivered = errors.New("standing: the denial stands but could not be published")

// IsUndelivered reports whether err is [ErrUndelivered].
func IsUndelivered(err error) bool { return errors.Is(err, ErrUndelivered) }

// Actor is who placed or lifted a denial: the credential, from the
// authenticated request and never from a body — or an inbound action handler.
type Actor struct {
	// Principal is the credential that acted. Required.
	Principal string
	// Subject is the person behind it; empty for a machine token and for an
	// action handler.
	Subject string
	// BreakGlass reports a break-glass credential (M7).
	BreakGlass bool
	// CorrelationID ties the act to the request log line.
	CorrelationID string
	// Action is the inbound action performing the act, nil for an operator.
	Action *access.ActionRef
}

func (a Actor) stored() store.StandingActor {
	return store.StandingActor{Subject: a.Subject, Principal: a.Principal, BreakGlass: a.BreakGlass}
}

func (a Actor) audited() audit.OperatorActor {
	return audit.OperatorActor{
		Principal: a.Principal, Subject: a.Subject, BreakGlass: a.BreakGlass, CorrelationID: a.CorrelationID,
	}
}

func (a Actor) check() error {
	if strings.TrimSpace(a.Principal) == "" {
		// A wiring fault, not a validation error: the transport always has a
		// principal on an authenticated route, and a fault is an outage (M11).
		return errors.New("standing: an act on a standing denial needs the credential that performed it")
	}
	return nil
}

// Recorder writes the audit record of an act inside the act's transaction.
// `*audit.Emitter` implements it.
type Recorder interface {
	OperatorEvent(ctx context.Context, tx *store.Store, tenant store.Tenant, act audit.OperatorAct) error
}

// Revoker publishes on the revocation stream. `*revoke.Bus` and
// `*revoke.Operator` implement it.
type Revoker interface {
	Kill(ctx context.Context, tenant store.Tenant, aud revoke.Audience, k revoke.Kill) (revoke.Receipt, error)
	Invalidate(ctx context.Context, tenant store.Tenant, aud revoke.Audience, inv revoke.Invalidation) (revoke.Receipt, error)
}

// DefaultSettle is how long a freshly placed denial waits before it publishes
// its second pass. The daemon sets it from the decision budget.
const DefaultSettle = 2500 * time.Millisecond

// settlePassTimeout bounds the second pass, which runs whether or not the
// caller is still waiting.
const settlePassTimeout = 10 * time.Second

// Options configures a Service.
type Options struct {
	Store    *store.Store
	Recorder Recorder
	Revoker  Revoker
	// Settle is the wait before the second publication of a denial this call
	// placed: longer than one authorize call's budget. Zero takes DefaultSettle.
	Settle time.Duration
	Logger *slog.Logger
	// Now overrides the clock for tests.
	Now func() time.Time
}

// Service places and lifts standing denials.
type Service struct {
	store    *store.Store
	recorder Recorder
	revoker  Revoker
	settle   time.Duration
	log      *slog.Logger
	now      func() time.Time
}

// New builds a Service. It refuses to build without what it needs rather than
// failing on the first lock in an emergency.
func New(o Options) (*Service, error) {
	switch {
	case o.Store == nil:
		return nil, errors.New("standing: a store is required")
	case o.Recorder == nil:
		return nil, errors.New("standing: a recorder is required")
	case o.Revoker == nil:
		return nil, errors.New("standing: a revocation publisher is required")
	}
	s := &Service{
		store: o.Store, recorder: o.Recorder, revoker: o.Revoker,
		settle: o.Settle, log: o.Logger, now: o.Now,
	}
	if s.settle <= 0 {
		s.settle = DefaultSettle
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Owed is what a committed act owes the revocation stream.
type Owed struct {
	Tenant store.Tenant
	// Invalidate drops cached decisions.
	Invalidate *revoke.Invalidation
	// Kill ends sessions.
	Kill *revoke.Kill
	// Settle asks for a second publication after one authorize call's budget,
	// for an act this call placed: an authorize that read the world a moment
	// before the commit may have recorded a session or cached an allow after
	// the first pass.
	Settle bool
}

// None reports that nothing is owed.
func (o Owed) None() bool { return o.Invalidate == nil && o.Kill == nil }

// Deliver publishes what a committed act owes, invalidation first so a user
// whose session ends and who reconnects at once reaches this server rather
// than a decision cached under the denial. A failure is [ErrUndelivered].
func (s *Service) Deliver(ctx context.Context, o Owed) error {
	if o.None() {
		return nil
	}
	if err := s.publish(ctx, o); err != nil {
		s.log.ErrorContext(ctx, "a standing denial was placed and publishing it could not be done",
			"event", "standing_denial_undelivered", "tenant", o.Tenant.String(), "error", err.Error())
		return fmt.Errorf("%w: %v", ErrUndelivered, err)
	}
	if !o.Settle {
		return nil
	}
	timer := time.NewTimer(s.settle)
	defer timer.Stop()
	<-timer.C
	// Not bound to the caller's context: a client that stops waiting does not
	// get to leave a denial half-delivered.
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settlePassTimeout)
	defer cancel()
	if err := s.publish(settleCtx, o); err != nil {
		s.log.ErrorContext(ctx, "the second publication of a standing denial failed",
			"event", "standing_denial_undelivered", "tenant", o.Tenant.String(), "error", err.Error())
		return fmt.Errorf("%w: second pass: %v", ErrUndelivered, err)
	}
	return nil
}

func (s *Service) publish(ctx context.Context, o Owed) error {
	if o.Invalidate != nil {
		if _, err := s.revoker.Invalidate(ctx, o.Tenant, revoke.Everyone(), *o.Invalidate); err != nil {
			return fmt.Errorf("cache_invalidate: %w", err)
		}
	}
	if o.Kill != nil {
		if _, err := s.revoker.Kill(ctx, o.Tenant, revoke.Everyone(), *o.Kill); err != nil {
			return fmt.Errorf("session_kill: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// the subject lock
// ---------------------------------------------------------------------------

// LockRequest is what a lock is placed with.
type LockRequest struct {
	// SubjectID is the subject to lock. It must have a row, unless a lock
	// already stands on the id.
	SubjectID string
	// ReasonCode is an optional stable code; Reason is the text the holder is
	// shown, required here (the caller supplies its default); ExternalRef ties
	// the lock to an incident.
	ReasonCode  string
	Reason      string
	ExternalRef string
	// RefuseBreakGlass refuses a subject whose row is a break-glass one with
	// [ErrBreakGlassSubject]. An inbound action sets it; an operator, acting
	// deliberately, does not.
	RefuseBreakGlass bool
}

// LockResult is what placing a lock did.
type LockResult struct {
	// Lock is the lock now standing: the first one's time, actor and reason.
	Lock store.SubjectLock
	// Placed reports that THIS call placed it. False means one already stood.
	Placed bool
	// Owed is what the caller publishes after the transaction commits.
	Owed Owed
}

// OwedByLock is what a lock owes the stream: the subject's cached decisions
// dropped and the sessions they hold ended with the holder's reason. A lock
// that already stands owes it too, because a publication that failed is only
// ever repaired by asking again.
func OwedByLock(tenant store.Tenant, l store.SubjectLock, settle bool) Owed {
	reason := l.Reason
	if reason == "" {
		reason = LockedByAdministrator
	}
	return Owed{
		Tenant:     tenant,
		Invalidate: &revoke.Invalidation{Subject: l.SubjectID},
		Kill:       &revoke.Kill{Subject: l.SubjectID, Reason: reason},
		Settle:     settle,
	}
}

func (r LockRequest) normalize() (LockRequest, error) {
	id, err := checkID("subject", r.SubjectID)
	if err != nil {
		return LockRequest{}, err
	}
	r.SubjectID = id
	if r.Reason, err = access.NormalizeHolderReason(r.Reason); err != nil {
		return LockRequest{}, err
	}
	if r.ReasonCode, err = access.NormalizeReasonCodeLine(r.ReasonCode); err != nil {
		return LockRequest{}, err
	}
	if r.ExternalRef, err = access.NormalizeExternalRef(r.ExternalRef); err != nil {
		return LockRequest{}, err
	}
	return r, nil
}

// PlaceLock places a lock inside tx, the transaction the act belongs to, and
// records it there. The caller publishes res.Owed after the commit.
//
// A lock already standing is left as it is, with no second record and its
// first time, actor and reason; the result still owes the publication.
func (s *Service) PlaceLock(ctx context.Context, tx *store.Store, tenant store.Tenant, actor Actor, req LockRequest) (LockResult, error) {
	if err := actor.check(); err != nil {
		return LockResult{}, err
	}
	req, err := req.normalize()
	if err != nil {
		return LockResult{}, err
	}

	// Under the tenant's subject lock, so a subject deleted a moment ago is
	// not locked as if it were there, and the row read is the row locked.
	if err := tx.Subjects().Lock(ctx, tenant); err != nil {
		return LockResult{}, err
	}
	standing, err := tx.Subjects().Standing(ctx, tenant, req.SubjectID)
	if err != nil {
		return LockResult{}, err
	}
	if standing.Lock != nil {
		return LockResult{Lock: *standing.Lock, Owed: OwedByLock(tenant, *standing.Lock, false)}, nil
	}
	if !standing.Known {
		return LockResult{}, &store.Error{Op: "standing.PlaceLock", Kind: store.KindNotFound}
	}
	if req.RefuseBreakGlass && standing.Subject.BreakGlass {
		return LockResult{}, ErrBreakGlassSubject
	}

	lock, placed, err := tx.SubjectLocks().Place(ctx, tenant, store.SubjectLock{
		SubjectID: req.SubjectID, LockedAt: s.now().UTC().Truncate(time.Microsecond), LockedBy: actor.stored(),
		ReasonCode: req.ReasonCode, Reason: req.Reason, ExternalRef: req.ExternalRef, ActionID: actionID(actor),
	})
	if err != nil {
		return LockResult{}, err
	}
	if placed {
		if err := s.recorder.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
			Event: audit.EventSubjectLocked, Actor: actor.audited(), Subject: req.SubjectID,
			Fields:  fieldsOf(req.ReasonCode, req.Reason),
			Action:  actor.Action,
			Message: "subject " + req.SubjectID + " locked",
		}); err != nil {
			return LockResult{}, err
		}
	}
	return LockResult{Lock: lock, Placed: placed, Owed: OwedByLock(tenant, lock, placed)}, nil
}

// Lock places a lock in a transaction of its own and publishes what it owes.
// A publication that fails after the commit is returned with the result and
// [ErrUndelivered]: the lock stands.
func (s *Service) Lock(ctx context.Context, tenant store.Tenant, actor Actor, req LockRequest) (LockResult, error) {
	var res LockResult
	err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		var err error
		res, err = s.PlaceLock(ctx, tx, tenant, actor, req)
		return err
	})
	if err != nil {
		return LockResult{}, err
	}
	return res, s.Deliver(ctx, res.Owed)
}

// LiftRequest is what lifting a denial is recorded with. Nothing here is shown
// to anyone: the lock and the quarantine end quietly, and a deny is never
// cached as an allow, so lifting publishes nothing.
type LiftRequest struct {
	// ID is the subject or the target.
	ID          string
	ReasonCode  string
	Reason      string
	ExternalRef string
}

func (r LiftRequest) normalize(field string) (LiftRequest, error) {
	id, err := checkID(field, r.ID)
	if err != nil {
		return LiftRequest{}, err
	}
	r.ID = id
	if r.Reason = strings.TrimSpace(r.Reason); r.Reason != "" {
		if r.Reason, err = access.NormalizeHolderReason(r.Reason); err != nil {
			return LiftRequest{}, err
		}
	}
	if r.ReasonCode, err = access.NormalizeReasonCodeLine(r.ReasonCode); err != nil {
		return LiftRequest{}, err
	}
	if r.ExternalRef, err = access.NormalizeExternalRef(r.ExternalRef); err != nil {
		return LiftRequest{}, err
	}
	return r, nil
}

// UnlockResult is what lifting a lock did.
type UnlockResult struct {
	// Lock is the lock that was lifted, zero when none stood.
	Lock store.SubjectLock
	// Lifted reports that THIS call lifted a lock. False means none stood.
	Lifted bool
}

// LiftLock lifts the lock on a subject inside tx and records it there. No lock
// standing is not an error: it is Lifted false.
func (s *Service) LiftLock(ctx context.Context, tx *store.Store, tenant store.Tenant, actor Actor, req LiftRequest) (UnlockResult, error) {
	if err := actor.check(); err != nil {
		return UnlockResult{}, err
	}
	req, err := req.normalize("subject")
	if err != nil {
		return UnlockResult{}, err
	}
	lock, err := tx.SubjectLocks().Lift(ctx, tenant, req.ID)
	switch {
	case store.IsNotFound(err):
		return UnlockResult{}, nil
	case err != nil:
		return UnlockResult{}, err
	}
	if err := s.recorder.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
		Event: audit.EventSubjectUnlocked, Actor: actor.audited(), Subject: req.ID,
		Fields:  liftFields(req, lock.LockedAt),
		Action:  actor.Action,
		Message: "subject " + req.ID + " unlocked",
	}); err != nil {
		return UnlockResult{}, err
	}
	return UnlockResult{Lock: lock, Lifted: true}, nil
}

// Unlock lifts a lock in a transaction of its own.
func (s *Service) Unlock(ctx context.Context, tenant store.Tenant, actor Actor, req LiftRequest) (UnlockResult, error) {
	var res UnlockResult
	err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		var err error
		res, err = s.LiftLock(ctx, tx, tenant, actor, req)
		return err
	})
	return res, err
}

// ---------------------------------------------------------------------------
// the target quarantine
// ---------------------------------------------------------------------------

// QuarantineRequest is what a quarantine is placed with.
type QuarantineRequest struct {
	// TargetID is the target to quarantine. It must have a row.
	TargetID    string
	ReasonCode  string
	Reason      string
	ExternalRef string
}

// QuarantineResult is what placing a quarantine did.
type QuarantineResult struct {
	Quarantine store.TargetQuarantine
	Placed     bool
	Owed       Owed
}

// OwedByQuarantine is what a quarantine owes the stream: every cached decision
// in the tenant dropped, so a cached allow for the target cannot outlive it.
//
// It is `all` for the tenant and not the keys of the decisions made for that
// target, because this server cannot name them cheaply. A cache key is issued
// per rule over the components the rule names, and the decision record that
// carries it is not indexed by target; indexing the table the decision path
// writes on every call, for the sake of a rare emergency act, would cost M5
// more than the act saves. A proxy that drops its cache asks again, and every
// target but this one is allowed again.
func OwedByQuarantine(tenant store.Tenant, settle bool) Owed {
	return Owed{Tenant: tenant, Invalidate: &revoke.Invalidation{All: true}, Settle: settle}
}

// PlaceQuarantine places a quarantine inside tx and records it there. Live
// sessions are not killed: a quarantine stops new access, and an operator who
// wants them ended has `POST …/revocations`.
func (s *Service) PlaceQuarantine(ctx context.Context, tx *store.Store, tenant store.Tenant, actor Actor, req QuarantineRequest) (QuarantineResult, error) {
	if err := actor.check(); err != nil {
		return QuarantineResult{}, err
	}
	id, err := checkID("target", req.TargetID)
	if err != nil {
		return QuarantineResult{}, err
	}
	req.TargetID = id
	// A quarantine's reason is not shown to anyone, so it need not be
	// supplied, but it is held to the same shape when it is.
	if req.Reason = strings.TrimSpace(req.Reason); req.Reason != "" {
		if req.Reason, err = access.NormalizeHolderReason(req.Reason); err != nil {
			return QuarantineResult{}, err
		}
	}
	if req.ReasonCode, err = access.NormalizeReasonCodeLine(req.ReasonCode); err != nil {
		return QuarantineResult{}, err
	}
	if req.ExternalRef, err = access.NormalizeExternalRef(req.ExternalRef); err != nil {
		return QuarantineResult{}, err
	}

	// The hostname is the record's target, and a target with no row is the
	// answer before anything is written.
	target, err := tx.Targets().Get(ctx, tenant, req.TargetID)
	if err != nil {
		return QuarantineResult{}, err
	}
	q, placed, err := tx.TargetQuarantines().Place(ctx, tenant, store.TargetQuarantine{
		TargetID: req.TargetID, QuarantinedAt: s.now().UTC().Truncate(time.Microsecond), QuarantinedBy: actor.stored(),
		ReasonCode: req.ReasonCode, Reason: req.Reason, ExternalRef: req.ExternalRef, ActionID: actionID(actor),
	})
	if err != nil {
		return QuarantineResult{}, err
	}
	if placed {
		if err := s.recorder.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
			Event: audit.EventTargetQuarantined, Actor: actor.audited(), Target: target.Hostname,
			Fields:  withTargetID(fieldsOf(req.ReasonCode, req.Reason), req.TargetID),
			Action:  actor.Action,
			Message: "target " + req.TargetID + " quarantined",
		}); err != nil {
			return QuarantineResult{}, err
		}
	}
	return QuarantineResult{Quarantine: q, Placed: placed, Owed: OwedByQuarantine(tenant, placed)}, nil
}

// Quarantine places a quarantine in a transaction of its own and publishes what
// it owes.
func (s *Service) Quarantine(ctx context.Context, tenant store.Tenant, actor Actor, req QuarantineRequest) (QuarantineResult, error) {
	var res QuarantineResult
	err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		var err error
		res, err = s.PlaceQuarantine(ctx, tx, tenant, actor, req)
		return err
	})
	if err != nil {
		return QuarantineResult{}, err
	}
	return res, s.Deliver(ctx, res.Owed)
}

// ReleaseResult is what lifting a quarantine did.
type ReleaseResult struct {
	Quarantine store.TargetQuarantine
	Lifted     bool
}

// LiftQuarantine lifts the quarantine on a target inside tx and records it
// there. No quarantine standing is not an error: it is Lifted false.
//
// Lifting a quarantine is an operator's act only: `ext` declares no action
// kind for it, so nothing in internal/action calls this.
func (s *Service) LiftQuarantine(ctx context.Context, tx *store.Store, tenant store.Tenant, actor Actor, req LiftRequest) (ReleaseResult, error) {
	if err := actor.check(); err != nil {
		return ReleaseResult{}, err
	}
	req, err := req.normalize("target")
	if err != nil {
		return ReleaseResult{}, err
	}
	q, err := tx.TargetQuarantines().Lift(ctx, tenant, req.ID)
	switch {
	case store.IsNotFound(err):
		return ReleaseResult{}, nil
	case err != nil:
		return ReleaseResult{}, err
	}
	hostname := ""
	if t, err := tx.Targets().Get(ctx, tenant, req.ID); err == nil {
		hostname = t.Hostname
	} else if !store.IsNotFound(err) {
		return ReleaseResult{}, err
	}
	if err := s.recorder.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
		Event: audit.EventTargetReleased, Actor: actor.audited(), Target: hostname,
		Fields:  withTargetID(liftFields(req, q.QuarantinedAt), req.ID),
		Action:  actor.Action,
		Message: "target " + req.ID + " released",
	}); err != nil {
		return ReleaseResult{}, err
	}
	return ReleaseResult{Quarantine: q, Lifted: true}, nil
}

// Release lifts a quarantine in a transaction of its own.
func (s *Service) Release(ctx context.Context, tenant store.Tenant, actor Actor, req LiftRequest) (ReleaseResult, error) {
	var res ReleaseResult
	err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		var err error
		res, err = s.LiftQuarantine(ctx, tx, tenant, actor, req)
		return err
	})
	return res, err
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func actionID(a Actor) string {
	if a.Action == nil {
		return ""
	}
	return a.Action.ID
}

func fieldsOf(reasonCode, reason string) map[string]string {
	f := map[string]string{}
	if reasonCode != "" {
		f["reason_code"] = reasonCode
	}
	if reason != "" {
		f["reason"] = reason
	}
	return f
}

func liftFields(req LiftRequest, since time.Time) map[string]string {
	f := fieldsOf(req.ReasonCode, req.Reason)
	f["stood_since"] = since.UTC().Format(time.RFC3339)
	return f
}

func withTargetID(f map[string]string, id string) map[string]string {
	f["target_id"] = id
	return f
}

// checkID validates a subject or target id the way a grant validates its
// subject: present, one line, bounded.
func checkID(field, v string) (string, error) {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return "", &access.ValidationError{Field: field, Problem: access.ProblemRequired}
	case len(v) > maxIDLen:
		return "", &access.ValidationError{Field: field, Problem: access.ProblemTooLong, Limit: fmt.Sprint(maxIDLen)}
	case strings.ContainsFunc(v, unicode.IsControl):
		return "", &access.ValidationError{Field: field, Problem: access.ProblemInvalid}
	}
	return v, nil
}
