// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package action

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/standing"
	"github.com/hoplock/control/internal/store"
)

// The words a holder is shown when an action ends their session, or revokes a
// grant, and its requester gave no text of its own. A lock's are
// [standing.LockedBySecurityResponse].
//
// They are Control's text, shown verbatim in a terminal as the connection
// closes, so each says what happened and nothing more. None is
// access.DefaultRevokeReason, which says "by an administrator" and would be
// false here.
const (
	// KillReason is shown when a killed session had no text from its requester.
	KillReason = "Your session was ended as part of a security response."
	// RevokeReason is shown when a revoked grant had none.
	RevokeReason = "Your temporary access grant was revoked as part of a security response."
)

// The reason codes an action's result carries when it found nothing to act on,
// or refused on purpose. They are stable.
const (
	ReasonAlreadyLocked      = "already_locked"
	ReasonNotLocked          = "not_locked"
	ReasonAlreadyQuarantined = "already_quarantined"
	ReasonAlreadyRevoked     = "already_revoked"
	ReasonGrantExpired       = "expired"
	// ReasonBreakGlassSubject is a lock refused because the subject's row is a
	// break-glass one (M7).
	ReasonBreakGlassSubject = "break_glass_subject"
)

// PrincipalPrefix starts the principal an action handler acts as. The actor on
// every record an action writes, and on every grant row it touches, is
// `action-handler:<provider>` with no subject, the way a command run from the
// box is `local:`.
const PrincipalPrefix = "action-handler:"

// maxIDLen bounds an action id, a tenant and the selectors.
const maxIDLen = 256

// Grants is the part of internal/access an action revokes a grant through.
// `*access.Service` implements it.
type Grants interface {
	RevokeWithin(ctx context.Context, tx *store.Store, tenant store.Tenant, actor access.Actor, grantID, reason string, action *access.ActionRef) (access.Revocation, error)
	Finish(ctx context.Context, tenant store.Tenant, actor access.Actor, rev access.Revocation, action *access.ActionRef) (access.Revocation, error)
	Resend(ctx context.Context, tenant store.Tenant, grantID string) (access.Revocation, error)
}

// Revoker publishes a kill. `*revoke.Operator` implements it.
type Revoker interface {
	Kill(ctx context.Context, tenant store.Tenant, aud revoke.Audience, k revoke.Kill) (revoke.Receipt, error)
}

// Recorder writes an audit record inside a transaction, or on its own with a
// nil one. `*audit.Emitter` implements it.
type Recorder interface {
	OperatorEvent(ctx context.Context, tx *store.Store, tenant store.Tenant, act audit.OperatorAct) error
}

// Options configures an Executor.
type Options struct {
	// Provider is the registered handler's provider name: the actor's
	// identity and the `provider` on every record. Required.
	Provider string
	Store    *store.Store
	Standing *standing.Service
	Grants   Grants
	Revoker  Revoker
	Recorder Recorder
	// DefaultTenant is where a refusal that names no tenant is recorded: an
	// action with an empty tenant is refused, and the refusal has to be
	// written somewhere. Empty records nothing for such a refusal but logs it.
	DefaultTenant store.Tenant
	Logger        *slog.Logger
	// Now overrides the clock for tests.
	Now func() time.Time
}

// Executor implements ext.ActionExecutor.
type Executor struct {
	provider      string
	principal     string
	store         *store.Store
	standing      *standing.Service
	grants        Grants
	revoker       Revoker
	recorder      Recorder
	defaultTenant store.Tenant
	log           *slog.Logger
	now           func() time.Time

	// closing is set when the handler is being stopped: an Execute that
	// arrives after is KindUnavailable.
	closing atomic.Bool
}

var _ ext.ActionExecutor = (*Executor)(nil)

// New builds an Executor. It refuses to build without what it needs, so a
// registered handler is never started with an executor that fails on its first
// action.
func New(o Options) (*Executor, error) {
	switch {
	case strings.TrimSpace(o.Provider) == "":
		return nil, errors.New("action: an executor needs the provider that registered its handler: it is the actor on every record")
	case o.Store == nil, o.Standing == nil, o.Grants == nil, o.Revoker == nil, o.Recorder == nil:
		return nil, errors.New("action: an executor needs a store, the standing-denial service, the grant service, " +
			"the revocation publisher and the audit recorder")
	}
	e := &Executor{
		provider: o.Provider, principal: PrincipalPrefix + o.Provider,
		store: o.Store, standing: o.Standing, grants: o.Grants, revoker: o.Revoker, recorder: o.Recorder,
		defaultTenant: o.DefaultTenant, log: o.Logger, now: o.Now,
	}
	if e.log == nil {
		e.log = slog.Default()
	}
	if e.now == nil {
		e.now = time.Now
	}
	return e, nil
}

// request is an action that passed validation, in the store's terms.
type request struct {
	tenant                                  store.Tenant
	id                                      string
	kind                                    ext.ActionKind
	subjectID, sessionID, targetID, grantID string
	reasonCode, reasonText, externalRef     string
}

// ref is the action as every record it writes names it.
func (r request) ref(provider string) *access.ActionRef {
	return &access.ActionRef{
		ID: r.id, Kind: r.kind.String(), ExternalRef: r.externalRef, ReasonCode: r.reasonCode, Provider: provider,
	}
}

// refusal is an action Control will not perform, and why.
type refusal struct {
	kind ext.Kind
	// field is the Action field at fault, for KindInvalid and KindNotFound.
	field string
	// code is the reason code, for KindDenied.
	code string
	msg  string
}

func invalidField(field, format string, args ...any) *refusal {
	return &refusal{kind: ext.KindInvalid, field: field, msg: fmt.Sprintf(format, args...)}
}

func notFound(field string, what string) *refusal {
	return &refusal{kind: ext.KindNotFound, field: field, msg: what + " is not known in this tenant"}
}

// Execute performs one action. See ext.ActionExecutor for what each error
// kind asks of the caller.
func (e *Executor) Execute(ctx context.Context, a ext.Action) (ext.ActionResult, error) {
	res := ext.ActionResult{ActionID: a.ActionID}
	if e.closing.Load() {
		return res, e.errorf(ext.KindUnavailable, "the action channel is stopping and executes nothing more")
	}

	req, bad := validate(a)
	if bad != nil {
		return e.refuse(ctx, a, res, bad)
	}

	var (
		prior    *store.ActionExecution
		done     applied
		executed store.ActionExecution
	)
	err := e.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		// Everything under one action id is serialised, so two calls have one
		// effect: the second finds the first's row.
		if err := tx.ActionExecutions().Lock(ctx, req.tenant, req.id); err != nil {
			return err
		}
		found, err := tx.ActionExecutions().Get(ctx, req.tenant, req.id)
		switch {
		case err == nil:
			if !sameAction(found, req) {
				return &refusal{kind: ext.KindConflict, field: "action_id",
					msg: fmt.Sprintf("action id %q was already used for a %s, and a different action cannot share it", req.id, found.Kind)}
			}
			prior = &found
			return nil
		case !store.IsNotFound(err):
			return err
		}

		done, err = e.apply(ctx, tx, req)
		if err != nil {
			return err
		}
		executed = store.ActionExecution{
			ActionID: req.id, Kind: req.kind.String(), SubjectID: req.subjectID, SessionID: req.sessionID,
			TargetID: req.targetID, GrantID: req.grantID, Affected: done.affected, ReasonCode: done.reasonCode,
			AppliedAt: e.now().UTC().Truncate(time.Microsecond), Provider: e.provider, HolderReason: done.holder,
		}
		if !done.recordAfter {
			executed.RecordedAt = executed.AppliedAt
		}
		return tx.ActionExecutions().Insert(ctx, req.tenant, executed)
	})
	if err != nil {
		var r *refusal
		if errors.As(err, &r) {
			return e.refuse(ctx, a, res, r)
		}
		return res, e.internal(ctx, "execute", err)
	}

	if prior != nil {
		// A repeat: the first result, and what the first execution owed the
		// stream, sent again.
		res = resultOf(*prior)
		if err := e.redeliver(ctx, req, *prior); err != nil {
			return res, err
		}
		return res, nil
	}

	res = resultOf(executed)
	if err := e.deliver(ctx, req, done); err != nil {
		return res, err
	}
	return res, nil
}

// sameAction reports whether a ledger row is the action a request asks for: the
// same kind and the same selector. The reason, the text and the external
// reference are not part of it: a requester that retries with different words
// is retrying, not asking for something else.
func sameAction(x store.ActionExecution, r request) bool {
	return x.Kind == r.kind.String() && x.SubjectID == r.subjectID && x.SessionID == r.sessionID &&
		x.TargetID == r.targetID && x.GrantID == r.grantID
}

func resultOf(x store.ActionExecution) ext.ActionResult {
	return ext.ActionResult{ActionID: x.ActionID, Affected: x.Affected, AppliedAt: x.AppliedAt, ReasonCode: x.ReasonCode}
}

// applied is what one kind's act did inside the transaction, and what is left
// to do once it has committed.
type applied struct {
	affected   int
	reasonCode string
	// holder is the reason the holder was shown, kept so a repeat sends the
	// same words.
	holder string
	// owed is what a lock or a quarantine owes the stream.
	owed standing.Owed
	// kill is the kill a kill_session publishes, recordAfter that its record
	// is written once it is published.
	kill        *revoke.Kill
	recordAfter bool
	// revocation is a grant revocation awaiting its publication.
	revocation *access.Revocation
}

// apply performs one kind's act inside tx, and records it there. A refusal is
// returned as a *refusal and rolls the transaction back, so a refused action
// leaves nothing behind.
func (e *Executor) apply(ctx context.Context, tx *store.Store, req request) (applied, error) {
	ref := req.ref(e.provider)
	switch req.kind {
	case ext.ActionKillSession:
		return e.kill(ctx, tx, req)
	case ext.ActionLockSubject:
		return e.lock(ctx, tx, req, ref)
	case ext.ActionUnlockSubject:
		return e.unlock(ctx, tx, req, ref)
	case ext.ActionRevokeGrant:
		return e.revokeGrant(ctx, tx, req, ref)
	case ext.ActionQuarantineTarget:
		return e.quarantine(ctx, tx, req, ref)
	case ext.ActionUnspecified:
	}
	return applied{}, invalidField("kind", "%s is not an action kind Control executes", req.kind)
}

func (e *Executor) standingActor(ref *access.ActionRef) standing.Actor {
	return standing.Actor{Principal: e.principal, Action: ref}
}

func (e *Executor) accessActor() access.Actor { return access.Actor{Principal: e.principal} }

func (e *Executor) kill(ctx context.Context, tx *store.Store, req request) (applied, error) {
	k := revoke.Kill{Reason: firstNonEmpty(req.reasonText, KillReason)}
	if req.sessionID != "" {
		found, err := tx.Decisions().SessionExists(ctx, req.tenant, req.sessionID)
		if err != nil {
			return applied{}, err
		}
		if !found {
			return applied{}, notFound("session_id", "the session")
		}
		k.SessionIDs = []string{req.sessionID}
	} else {
		s, err := tx.Subjects().Standing(ctx, req.tenant, req.subjectID)
		if err != nil {
			return applied{}, err
		}
		if !s.Known && s.Lock == nil {
			return applied{}, notFound("subject_id", "the subject")
		}
		k.Subject = req.subjectID
	}
	// The record is written once the kill is published: the act is the
	// publication, and a record that said so first would say it of an event
	// the stream refused.
	return applied{affected: 1, holder: k.Reason, kill: &k, recordAfter: true}, nil
}

func (e *Executor) lock(ctx context.Context, tx *store.Store, req request, ref *access.ActionRef) (applied, error) {
	res, err := e.standing.PlaceLock(ctx, tx, req.tenant, e.standingActor(ref), standing.LockRequest{
		SubjectID: req.subjectID, ReasonCode: req.reasonCode,
		Reason:      firstNonEmpty(req.reasonText, standing.LockedBySecurityResponse),
		ExternalRef: req.externalRef, RefuseBreakGlass: true,
	})
	switch {
	case errors.Is(err, standing.ErrBreakGlassSubject):
		return applied{}, &refusal{kind: ext.KindDenied, code: ReasonBreakGlassSubject,
			msg: "the subject is a break-glass subject, which an inbound action may not lock; an operator may"}
	case store.IsNotFound(err):
		return applied{}, notFound("subject_id", "the subject")
	case err != nil:
		return applied{}, translate(err)
	}
	out := applied{affected: 1, holder: res.Lock.Reason, owed: res.Owed}
	if !res.Placed {
		out.affected, out.reasonCode = 0, ReasonAlreadyLocked
		if err := e.recordUnchanged(ctx, tx, req, ref, out.reasonCode); err != nil {
			return applied{}, err
		}
	}
	return out, nil
}

func (e *Executor) unlock(ctx context.Context, tx *store.Store, req request, ref *access.ActionRef) (applied, error) {
	res, err := e.standing.LiftLock(ctx, tx, req.tenant, e.standingActor(ref), standing.LiftRequest{
		ID: req.subjectID, ReasonCode: req.reasonCode, Reason: req.reasonText, ExternalRef: req.externalRef,
	})
	if err != nil {
		return applied{}, translate(err)
	}
	if res.Lifted {
		return applied{affected: 1}, nil
	}
	// Nothing to lift. A subject this server has never heard of is not
	// "not locked", it is not known.
	s, err := tx.Subjects().Standing(ctx, req.tenant, req.subjectID)
	if err != nil {
		return applied{}, err
	}
	if !s.Known {
		return applied{}, notFound("subject_id", "the subject")
	}
	if err := e.recordUnchanged(ctx, tx, req, ref, ReasonNotLocked); err != nil {
		return applied{}, err
	}
	return applied{reasonCode: ReasonNotLocked}, nil
}

func (e *Executor) quarantine(ctx context.Context, tx *store.Store, req request, ref *access.ActionRef) (applied, error) {
	res, err := e.standing.PlaceQuarantine(ctx, tx, req.tenant, e.standingActor(ref), standing.QuarantineRequest{
		TargetID: req.targetID, ReasonCode: req.reasonCode, Reason: req.reasonText, ExternalRef: req.externalRef,
	})
	if err != nil {
		if store.IsNotFound(err) {
			return applied{}, notFound("target_id", "the target")
		}
		return applied{}, translate(err)
	}
	out := applied{affected: 1, owed: res.Owed}
	if !res.Placed {
		out.affected, out.reasonCode = 0, ReasonAlreadyQuarantined
		if err := e.recordUnchanged(ctx, tx, req, ref, out.reasonCode); err != nil {
			return applied{}, err
		}
	}
	return out, nil
}

func (e *Executor) revokeGrant(ctx context.Context, tx *store.Store, req request, ref *access.ActionRef) (applied, error) {
	g, err := tx.Grants().Get(ctx, req.tenant, req.grantID)
	if err != nil {
		if store.IsNotFound(err) {
			return applied{}, notFound("grant_id", "the grant")
		}
		return applied{}, err
	}
	// A grant already revoked, or already over by its own window, is nothing
	// to act on, and its row is left as it is: revoking an expired grant would
	// make its state say `revoked`, a second and false answer to when access
	// stopped.
	switch {
	case !g.RevokedAt.IsZero():
		return e.revokeUnchanged(ctx, tx, req, ref, ReasonAlreadyRevoked)
	case !e.now().Before(g.ExpiresAt):
		return e.revokeUnchanged(ctx, tx, req, ref, ReasonGrantExpired)
	}

	reason := firstNonEmpty(req.reasonText, RevokeReason)
	rev, err := e.grants.RevokeWithin(ctx, tx, req.tenant, e.accessActor(), req.grantID, reason, ref)
	if err != nil {
		return applied{}, translate(err)
	}
	if !rev.Revoked {
		// Revoked by someone else between the read and the write.
		return e.revokeUnchanged(ctx, tx, req, ref, ReasonAlreadyRevoked)
	}
	return applied{affected: 1, holder: reason, revocation: &rev}, nil
}

func (e *Executor) revokeUnchanged(ctx context.Context, tx *store.Store, req request, ref *access.ActionRef, code string) (applied, error) {
	if err := e.recordUnchanged(ctx, tx, req, ref, code); err != nil {
		return applied{}, err
	}
	return applied{reasonCode: code}, nil
}

// recordUnchanged records an execution that found nothing to act on. It is
// not the lever's own event, because that would claim an act that did not
// happen.
func (e *Executor) recordUnchanged(ctx context.Context, tx *store.Store, req request, ref *access.ActionRef, code string) error {
	return e.recorder.OperatorEvent(ctx, tx, req.tenant, audit.OperatorAct{
		Event: audit.EventActionUnchanged, Actor: audit.OperatorActor{Principal: e.principal},
		Subject: req.subjectID, Target: req.targetID, Action: ref,
		Fields:   map[string]string{"outcome_reason": code},
		Severity: contract.SeverityInfo,
		Message:  req.kind.String() + " found nothing to act on: " + code,
	})
}

// deliver publishes what a first execution owes the stream, after its
// transaction committed. A publication the stream refused leaves the act made
// and is KindUnavailable: asking again under the same id delivers.
func (e *Executor) deliver(ctx context.Context, req request, done applied) error {
	switch {
	case done.kill != nil:
		rcpt, err := e.revoker.Kill(ctx, req.tenant, revoke.Everyone(), *done.kill)
		if err != nil {
			return e.unavailable(ctx, "session_kill", err)
		}
		return e.recordKill(ctx, req, rcpt)
	case !done.owed.None():
		if err := e.standing.Deliver(ctx, done.owed); err != nil {
			return e.unavailable(ctx, "publish", err)
		}
	case done.revocation != nil:
		if _, err := e.grants.Finish(ctx, req.tenant, e.accessActor(), *done.revocation, req.ref(e.provider)); err != nil {
			if access.IsUndelivered(err) {
				return e.unavailable(ctx, "grant revocation", err)
			}
			return e.internal(ctx, "finish a grant revocation", err)
		}
	}
	return nil
}

// redeliver sends again what the first execution of an action owed, for a
// repeat of it. A lock or a quarantine that has since been lifted owes nothing:
// a late duplicate must not end the sessions of a subject who was unlocked
// since.
func (e *Executor) redeliver(ctx context.Context, req request, prior store.ActionExecution) error {
	switch req.kind {
	case ext.ActionKillSession:
		k := revoke.Kill{Reason: prior.HolderReason, SessionIDs: nil, Subject: prior.SubjectID}
		if prior.SessionID != "" {
			k.SessionIDs = []string{prior.SessionID}
		}
		rcpt, err := e.revoker.Kill(ctx, req.tenant, revoke.Everyone(), k)
		if err != nil {
			return e.unavailable(ctx, "session_kill", err)
		}
		// The first execution's record is owed until a publication succeeded.
		return e.recordKill(ctx, req, rcpt)
	case ext.ActionLockSubject:
		if _, err := e.store.SubjectLocks().Get(ctx, req.tenant, prior.SubjectID); err != nil {
			if store.IsNotFound(err) {
				return nil
			}
			return e.internal(ctx, "read the lock", err)
		}
		owed := standing.OwedByLock(req.tenant, store.SubjectLock{SubjectID: prior.SubjectID, Reason: prior.HolderReason}, false)
		if err := e.standing.Deliver(ctx, owed); err != nil {
			return e.unavailable(ctx, "publish", err)
		}
	case ext.ActionQuarantineTarget:
		if _, err := e.store.TargetQuarantines().Get(ctx, req.tenant, prior.TargetID); err != nil {
			if store.IsNotFound(err) {
				return nil
			}
			return e.internal(ctx, "read the quarantine", err)
		}
		if err := e.standing.Deliver(ctx, standing.OwedByQuarantine(req.tenant, false)); err != nil {
			return e.unavailable(ctx, "publish", err)
		}
	case ext.ActionRevokeGrant:
		if prior.Affected == 0 {
			return nil
		}
		if _, err := e.grants.Resend(ctx, req.tenant, prior.GrantID); err != nil {
			if access.IsUndelivered(err) {
				return e.unavailable(ctx, "grant revocation", err)
			}
			if store.IsNotFound(err) {
				return nil
			}
			return e.internal(ctx, "resend a grant revocation", err)
		}
	case ext.ActionUnlockSubject, ext.ActionUnspecified:
	}
	return nil
}

// recordKill writes the record of a published kill, once. It is written after
// the publication, under the action's lock, and a repeat that finds it written
// does nothing: a retry after the stream refused the first publication is how
// the record the first execution owed gets written.
func (e *Executor) recordKill(ctx context.Context, req request, rcpt revoke.Receipt) error {
	ref := req.ref(e.provider)
	err := e.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.ActionExecutions().Lock(ctx, req.tenant, req.id); err != nil {
			return err
		}
		row, err := tx.ActionExecutions().Get(ctx, req.tenant, req.id)
		if err != nil {
			return err
		}
		if !row.RecordedAt.IsZero() {
			return nil
		}
		fields := map[string]string{
			"type": "session_kill", "event_id": rcpt.EventID, "delivered": strconv.Itoa(rcpt.Delivered),
			"audience": "all", "reason": row.HolderReason,
		}
		if row.SessionID != "" {
			fields["session_ids"] = row.SessionID
		} else {
			fields["subject"] = row.SubjectID
		}
		if err := e.recorder.OperatorEvent(ctx, tx, req.tenant, audit.OperatorAct{
			Event: audit.EventRevocationPublished, Actor: audit.OperatorActor{Principal: e.principal},
			Subject: row.SubjectID, Fields: fields, Action: ref,
			Message: "session_kill published",
		}); err != nil {
			return err
		}
		return tx.ActionExecutions().MarkRecorded(ctx, req.tenant, req.id, e.now())
	})
	if err != nil {
		return e.internal(ctx, "record a published kill", err)
	}
	return nil
}

// refuse answers an action Control will not perform, and writes the record of
// the refusal: `action.refused`, with the field or the reason code. A refusal
// has no effect, so the record is the whole of what it leaves.
func (e *Executor) refuse(ctx context.Context, a ext.Action, res ext.ActionResult, r *refusal) (ext.ActionResult, error) {
	res.ReasonCode = r.reasonCode()
	e.recordRefusal(ctx, a, r)
	return res, e.errorf(r.kind, "%s", r.message())
}

func (r *refusal) reasonCode() string {
	switch {
	case r.code != "":
		return r.code
	case r.kind == ext.KindConflict:
		return "action_conflict"
	case r.kind == ext.KindNotFound:
		return "not_found"
	default:
		return "invalid"
	}
}

func (r *refusal) message() string {
	if r.field != "" && r.kind == ext.KindInvalid {
		return "field " + r.field + ": " + r.msg
	}
	return r.msg
}

// Error lets a refusal travel out of the transaction that found it.
func (r *refusal) Error() string { return r.message() }

func (e *Executor) recordRefusal(ctx context.Context, a ext.Action, r *refusal) {
	tenant := store.Tenant(a.Tenant)
	if tenant == "" || len(tenant) > maxIDLen || hasControl(string(tenant)) {
		tenant = e.defaultTenant
	}
	if tenant == "" {
		e.log.WarnContext(ctx, "an inbound action was refused and there is no tenant to record it in",
			"event", "action_refused_unrecorded", "provider", e.provider, "refusal", r.kind.String(), "field", r.field)
		return
	}
	fields := map[string]string{"refusal": r.kind.String()}
	if r.field != "" {
		fields["field"] = r.field
	}
	if r.code != "" {
		fields["code"] = r.code
	}
	// What was sent is recorded only where it is fit to be: an id with a
	// control character in it is exactly what must not reach a log line.
	ref := &access.ActionRef{
		ID: clean(a.ActionID), Kind: a.Kind.String(), ExternalRef: clean(a.ExternalRef),
		ReasonCode: clean(a.ReasonCode), Provider: e.provider,
	}
	if ref.ID == "" {
		ref.ID = "(none)"
	}
	if err := e.recorder.OperatorEvent(ctx, nil, tenant, audit.OperatorAct{
		Event: audit.EventActionRefused, Actor: audit.OperatorActor{Principal: e.principal},
		Subject: clean(a.SubjectID), Target: clean(a.TargetID), Fields: fields, Action: ref,
		Message: a.Kind.String() + " refused: " + r.reasonCode(),
	}); err != nil {
		e.log.ErrorContext(ctx, "a refused inbound action could not be recorded",
			"event", "action_refused_unrecorded", "provider", e.provider, "tenant", tenant.String(), "error", err.Error())
	}
}

// ---------------------------------------------------------------------------
// validation
// ---------------------------------------------------------------------------

// validate holds an action to what it must be before anything is read or
// written: the tenant and id present and bounded, the kind one of the five,
// exactly the selector its kind takes, and the free fields fit to be recorded
// and shown.
func validate(a ext.Action) (request, *refusal) {
	req := request{
		tenant: store.Tenant(a.Tenant), id: a.ActionID, kind: a.Kind,
		subjectID: a.SubjectID, sessionID: a.SessionID, targetID: a.TargetID, grantID: a.GrantID,
	}
	if bad := checkToken("tenant", string(a.Tenant)); bad != nil {
		return req, bad
	}
	if bad := checkToken("action_id", a.ActionID); bad != nil {
		return req, bad
	}

	// Which selectors each kind takes. A selector a kind does not take is not
	// ignored: an action that names a target when it means to lock a subject
	// is an action whose requester is confused about what it asked for.
	var takes, forbids []selector
	switch a.Kind {
	case ext.ActionKillSession:
		switch {
		case a.SessionID != "" && a.SubjectID != "":
			return req, invalidField("subject_id", "a kill takes exactly one of session_id or subject_id, and this names both")
		case a.SessionID == "" && a.SubjectID == "":
			return req, invalidField("session_id", "a kill takes exactly one of session_id or subject_id, and this names neither")
		}
		takes = []selector{{"session_id", a.SessionID}, {"subject_id", a.SubjectID}}
		forbids = []selector{{"target_id", a.TargetID}, {"grant_id", a.GrantID}}
	case ext.ActionLockSubject, ext.ActionUnlockSubject:
		takes = []selector{{"subject_id", a.SubjectID}}
		forbids = []selector{{"session_id", a.SessionID}, {"target_id", a.TargetID}, {"grant_id", a.GrantID}}
	case ext.ActionRevokeGrant:
		takes = []selector{{"grant_id", a.GrantID}}
		forbids = []selector{{"subject_id", a.SubjectID}, {"session_id", a.SessionID}, {"target_id", a.TargetID}}
	case ext.ActionQuarantineTarget:
		takes = []selector{{"target_id", a.TargetID}}
		forbids = []selector{{"subject_id", a.SubjectID}, {"session_id", a.SessionID}, {"grant_id", a.GrantID}}
	case ext.ActionUnspecified:
		return req, invalidField("kind", "an action has to name what it asks Control to do")
	default:
		return req, invalidField("kind", "%d is not an action kind Control executes", int(a.Kind))
	}
	for _, s := range forbids {
		if s.value != "" {
			return req, invalidField(s.field, "a %s does not take a %s", a.Kind, s.field)
		}
	}
	for _, s := range takes {
		if s.value == "" {
			continue
		}
		if bad := checkToken(s.field, s.value); bad != nil {
			return req, bad
		}
	}
	if a.Kind != ext.ActionKillSession && takes[0].value == "" {
		return req, invalidField(takes[0].field, "a %s needs a %s", a.Kind, takes[0].field)
	}

	var err error
	if req.reasonCode, err = access.NormalizeReasonCodeLine(a.ReasonCode); err != nil {
		return req, fieldOf(err)
	}
	if req.externalRef, err = access.NormalizeExternalRef(a.ExternalRef); err != nil {
		return req, fieldOf(err)
	}
	if strings.TrimSpace(a.ReasonText) != "" {
		if req.reasonText, err = access.NormalizeHolderReason(a.ReasonText); err != nil {
			return req, fieldOf(err)
		}
	} else if a.ReasonText != "" {
		// Only whitespace: text the holder would be shown as a blank line.
		return req, invalidField("reason_text", "it is blank; leave it empty for Control's own words")
	}
	return req, nil
}

type selector struct{ field, value string }

// checkToken holds an identifier to present, bounded and one line.
func checkToken(field, v string) *refusal {
	switch {
	case v == "" || strings.TrimSpace(v) == "":
		return invalidField(field, "it is required")
	case len(v) > maxIDLen:
		return invalidField(field, "it is longer than %d bytes", maxIDLen)
	case hasControl(v):
		return invalidField(field, "it contains a control character")
	case v != strings.TrimSpace(v):
		return invalidField(field, "it has leading or trailing whitespace")
	}
	return nil
}

// fieldOf names a validation error's field in the action's own spelling.
func fieldOf(err error) *refusal {
	var ve *access.ValidationError
	if !errors.As(err, &ve) {
		return &refusal{kind: ext.KindInvalid, msg: err.Error()}
	}
	field := ve.Field
	switch field {
	case "reason":
		field = "reason_text"
	case "subject":
		field = "subject_id"
	case "target":
		field = "target_id"
	}
	detail := strings.ReplaceAll(ve.Problem, "_", " ")
	if ve.Limit != "" {
		detail += " (limit " + ve.Limit + ")"
	}
	return invalidField(field, "it is %s", detail)
}

// translate turns an error from a service into a *refusal where it is one, and
// leaves an outage as it is.
func translate(err error) error {
	var ve *access.ValidationError
	if errors.As(err, &ve) {
		return fieldOf(err)
	}
	return err
}

func hasControl(v string) bool { return strings.ContainsFunc(v, unicode.IsControl) }

// clean returns v if it is fit to be written to a record, and "" if it is not.
func clean(v string) string {
	if len(v) > maxIDLen || hasControl(v) {
		return ""
	}
	return v
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// errors
// ---------------------------------------------------------------------------

func (e *Executor) errorf(kind ext.Kind, format string, args ...any) *ext.Error {
	return ext.Errorf(ext.PointActionHandler, e.provider, "ActionExecutor.Execute", kind, format, args...)
}

// unavailable is a publication the stream refused: the act was made and
// stands, and the same action id delivers it.
func (e *Executor) unavailable(ctx context.Context, what string, err error) *ext.Error {
	e.log.ErrorContext(ctx, "an inbound action was executed and publishing it failed",
		"event", "action_publish_failed", "provider", e.provider, "what", what, "error", err.Error())
	return e.errorf(ext.KindUnavailable,
		"the action was executed and stands, but the %s could not be published; retry it under the same action id to deliver it", what)
}

// internal is a failure Control cannot classify further. The cause goes in the
// log, not the error: it can name tables and constraints, and the error goes
// back to somebody else's software.
func (e *Executor) internal(ctx context.Context, what string, err error) *ext.Error {
	e.log.ErrorContext(ctx, "an inbound action failed",
		"event", "action_failed", "provider", e.provider, "what", what, "error", err.Error())
	return e.errorf(ext.KindInternal, "Control could not %s", what)
}

// stopping makes every Execute from now on KindUnavailable. The lifecycle calls
// it before it stops the handler, so nothing new starts as the listeners drain.
func (e *Executor) stopping() { e.closing.Store(true) }
