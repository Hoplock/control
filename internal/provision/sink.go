// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package provision

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// PrincipalPrefix starts the principal an identity sync acts as. The actor on
// every record a sync's call writes, and on every grant, request and
// credential a deactivation revokes, is `identity-sync:<provider>` with no
// subject, the way an action handler is `action-handler:<provider>`.
const PrincipalPrefix = store.IdentitySyncPrincipalPrefix

// DeactivatedByDirectory is what the holder is shown when a deactivation ends
// their sessions and grants. It is Control's text: displayed verbatim as the
// connection closes (the contract's disclosure rule), so it says what happened
// and nothing more. It is never the directory's reason code, which is a code
// and is recorded, and never access.DefaultRevokeReason, which says "by an
// administrator" and would be false.
const DeactivatedByDirectory = access.DeactivatedByDirectoryReason

// ReasonDirectoryInactive is the reason code a deactivation carries when an
// upsert said `Active: false` rather than calling DeactivateSubject.
const ReasonDirectoryInactive = "directory_inactive"

// SubjectIDPrefix starts every subject id Control allocates for a directory
// identity. It has no `:`, so it can never be spelled like a federated subject
// (`<connector>:<subject>`); the link row, not the spelling, is the join.
const SubjectIDPrefix = "dir-"

// DefaultSettle is how long a freshly placed deactivation waits before it
// publishes its second pass. The daemon sets it from the decision budget.
const DefaultSettle = 2500 * time.Millisecond

// Grants is the part of internal/access a deactivation revokes through.
// `*access.Service` implements it.
type Grants interface {
	Revoke(ctx context.Context, tenant store.Tenant, actor access.Actor, grantID, reason string) (access.Revocation, error)
	Cancel(ctx context.Context, tenant store.Tenant, actor access.Actor, requestID, reason string) (access.Created, error)
}

// Revoker publishes on the revocation stream. `*revoke.Operator` implements
// it.
type Revoker interface {
	Kill(ctx context.Context, tenant store.Tenant, aud revoke.Audience, k revoke.Kill) (revoke.Receipt, error)
	Invalidate(ctx context.Context, tenant store.Tenant, aud revoke.Audience, inv revoke.Invalidation) (revoke.Receipt, error)
}

// Recorder writes the audit records a call makes. `*audit.Emitter`
// implements it.
type Recorder interface {
	SyncEvent(ctx context.Context, tx *store.Store, tenant store.Tenant, act audit.SyncAct) error
	OperatorEvent(ctx context.Context, tx *store.Store, tenant store.Tenant, act audit.OperatorAct) error
}

// Options configures a Sink.
type Options struct {
	// Provider is the registered sync's provider: the actor's identity and
	// the `provider` on every record. Required.
	Provider string
	Store    *store.Store
	Grants   Grants
	Revoker  Revoker
	Recorder Recorder
	// DefaultTenant is where a refusal that names no usable tenant is
	// recorded. Empty logs such a refusal and records nothing.
	DefaultTenant store.Tenant
	// Settle is the wait before the second publication of a deactivation this
	// call placed: longer than one authorize call's budget. Zero takes
	// DefaultSettle.
	Settle time.Duration
	Logger *slog.Logger
	// Now overrides the clock for tests.
	Now func() time.Time
}

// Sink implements ext.SyncSink over Control's identity store.
//
// It is safe for concurrent use. Calls on one ExternalID are serialised, so
// two calls on one id have one effect; calls on different ids run
// concurrently, and a group change waits for the subject writes in flight in
// its tenant and holds the next ones back while it re-derives its members.
type Sink struct {
	provider      string
	principal     string
	store         *store.Store
	grants        Grants
	revoker       Revoker
	recorder      Recorder
	defaultTenant store.Tenant
	settle        time.Duration
	log           *slog.Logger
	now           func() time.Time

	// closing is set when the sync is being stopped: a call that arrives
	// after is KindUnavailable.
	closing atomic.Bool
}

var _ ext.SyncSink = (*Sink)(nil)

// New builds a Sink. It refuses to build without what it needs, so a registered
// sync is never started with a sink that fails on its first call.
func New(o Options) (*Sink, error) {
	switch {
	case strings.TrimSpace(o.Provider) == "":
		return nil, errors.New("provision: a sink needs the provider that registered its sync: it is the actor on every record")
	case o.Store == nil, o.Grants == nil, o.Revoker == nil, o.Recorder == nil:
		return nil, errors.New("provision: a sink needs a store, the grant service, the revocation publisher and the audit recorder")
	}
	s := &Sink{
		provider: o.Provider, principal: PrincipalPrefix + o.Provider,
		store: o.Store, grants: o.Grants, revoker: o.Revoker, recorder: o.Recorder,
		defaultTenant: o.DefaultTenant, settle: o.Settle, log: o.Logger, now: o.Now,
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

// Provider is the registered sync's provider.
func (s *Sink) Provider() string { return s.provider }

// clock is Control's own time, as it is stored and compared.
func (s *Sink) clock() time.Time { return instant(s.now()) }

// UpsertSubject creates or updates the subject joined to in.ExternalID. See
// ext.SyncSink for what each outcome asks of the caller.
func (s *Sink) UpsertSubject(ctx context.Context, in ext.SyncedSubject) error {
	const op = "SyncSink.UpsertSubject"
	if s.closing.Load() {
		return s.stopped(op)
	}
	req, bad := validateSubject(in)
	if bad != nil {
		return s.refuse(ctx, op, in.Tenant, in.ExternalID, bad)
	}
	out, err := s.upsertSubject(ctx, req)
	if err != nil {
		var r *refusal
		if errors.As(err, &r) {
			return s.refuse(ctx, op, in.Tenant, in.ExternalID, r)
		}
		return s.internal(ctx, op, "apply the subject", err)
	}
	return s.deliverSubject(ctx, op, req.tenant, out)
}

// DeactivateSubject places the directory's standing deactivation on the
// subject joined to externalID and revokes what it holds. See ext.SyncSink.
func (s *Sink) DeactivateSubject(ctx context.Context, tenant ext.Tenant, externalID, reasonCode string) error {
	const op = "SyncSink.DeactivateSubject"
	if s.closing.Load() {
		return s.stopped(op)
	}
	if bad := validateRef(tenant, externalID, reasonCode); bad != nil {
		return s.refuse(ctx, op, tenant, externalID, bad)
	}
	out, err := s.deactivate(ctx, store.Tenant(tenant), externalID, reasonCode)
	if err != nil {
		return s.internal(ctx, op, "deactivate the subject", err)
	}
	return s.deliverSubject(ctx, op, store.Tenant(tenant), out)
}

// UpsertGroup creates or updates a directory group. See ext.SyncSink.
func (s *Sink) UpsertGroup(ctx context.Context, in ext.SyncedGroup) error {
	const op = "SyncSink.UpsertGroup"
	if s.closing.Load() {
		return s.stopped(op)
	}
	req, bad := validateGroup(in)
	if bad != nil {
		return s.refuse(ctx, op, in.Tenant, in.ExternalID, bad)
	}
	if err := s.upsertGroup(ctx, req); err != nil {
		return s.internal(ctx, op, "apply the group", err)
	}
	return s.deliverGroup(ctx, op, req.tenant, req.externalID)
}

// DeleteGroup deletes a directory group, keeping a tombstone. See
// ext.SyncSink.
func (s *Sink) DeleteGroup(ctx context.Context, tenant ext.Tenant, externalID string) error {
	const op = "SyncSink.DeleteGroup"
	if s.closing.Load() {
		return s.stopped(op)
	}
	if bad := validateRef(tenant, externalID, ""); bad != nil {
		return s.refuse(ctx, op, tenant, externalID, bad)
	}
	if err := s.deleteGroup(ctx, store.Tenant(tenant), externalID); err != nil {
		return s.internal(ctx, op, "delete the group", err)
	}
	return s.deliverGroup(ctx, op, store.Tenant(tenant), externalID)
}

// allocate chooses the subject id for a newly linked external id. It is random,
// so it cannot be guessed from the directory's data, and it is checked against
// every subject this tenant already has, so it never lands on one.
func (s *Sink) allocate(ctx context.Context, tx *store.Store, tenant store.Tenant) (string, error) {
	for range 3 {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		id := SubjectIDPrefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
		_, err := tx.Subjects().Get(ctx, tenant, id)
		if store.IsNotFound(err) {
			if _, err := tx.DirectoryLinks().BySubject(ctx, tenant, id); store.IsNotFound(err) {
				return id, nil
			} else if err != nil {
				return "", err
			}
			continue
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("provision: no free subject id after three random draws")
}

// ---------------------------------------------------------------------------
// errors and refusals
// ---------------------------------------------------------------------------

func (s *Sink) errorf(op string, kind ext.Kind, format string, args ...any) *ext.Error {
	return ext.Errorf(ext.PointIdentitySync, s.provider, op, kind, format, args...)
}

// stopped is a call that arrived once the sync's Stop began.
func (s *Sink) stopped(op string) error {
	return s.errorf(op, ext.KindUnavailable, "Control is stopping the identity sync and applies nothing more")
}

// internal is a failure Control cannot classify further. The cause goes in the
// log, not the error: it can name tables and constraints, and the error goes
// back to somebody else's software.
func (s *Sink) internal(ctx context.Context, op, what string, err error) error {
	s.log.ErrorContext(ctx, "an identity sync call failed",
		"event", "identity_sync_failed", "provider", s.provider, "op", op, "what", what, "error", err.Error())
	return s.errorf(op, ext.KindInternal, "Control could not %s; the same call is safe to retry", what)
}

// unavailable is a publication the stream refused, or a revocation that could
// not be finished, after the change committed: the change stands, and the same
// call delivers the rest.
func (s *Sink) unavailable(ctx context.Context, op, what string, err error) error {
	s.log.ErrorContext(ctx, "an identity sync call was applied and what it owes the revocation stream was not delivered",
		"event", "identity_sync_undelivered", "provider", s.provider, "op", op, "what", what, "error", err.Error())
	return s.errorf(op, ext.KindUnavailable,
		"the change was applied and stands, but %s could not be completed; the same call delivers it", what)
}

// refuse answers a call Control will not apply, and records the refusal. A
// refusal has no effect, so the record is the whole of what it leaves.
func (s *Sink) refuse(ctx context.Context, op string, tenant ext.Tenant, externalID string, r *refusal) error {
	recordIn := store.Tenant(tenant)
	if checkID("tenant", string(tenant)) != nil {
		recordIn = s.defaultTenant
	}
	if recordIn == "" {
		s.log.WarnContext(ctx, "an identity sync call was refused and there is no tenant to record it in",
			"event", "identity_sync_refused_unrecorded", "provider", s.provider, "op", op,
			"refusal", r.kind.String(), "field", r.field)
	} else {
		fields := map[string]string{"call": op, "refusal": r.kind.String()}
		if r.field != "" {
			fields["field"] = r.field
		}
		if r.conflict != "" {
			fields["conflicting_subject"] = r.conflict
		}
		// What was sent is recorded only where it is fit to be: an id with a
		// control character in it is exactly what must not reach a log line.
		if err := s.recorder.SyncEvent(ctx, nil, recordIn, audit.SyncAct{
			Event: audit.EventSyncRefused, Principal: s.principal, Provider: s.provider,
			ExternalID: clean(externalID), Fields: fields, Severity: contract.SeverityWarn,
			Message: op + " refused: " + r.kind.String(),
		}); err != nil {
			s.log.ErrorContext(ctx, "a refused identity sync call could not be recorded",
				"event", "identity_sync_refused_unrecorded", "provider", s.provider, "tenant", recordIn.String(), "error", err.Error())
		}
	}
	return s.errorf(op, r.kind, "%s", r.message())
}

// stopping makes every call from now on KindUnavailable. The lifecycle calls it
// before it stops the sync, so nothing new starts as the listeners drain.
func (s *Sink) stopping() { s.closing.Store(true) }
