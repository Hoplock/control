// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"time"
)

// Standing denials (0039, PLAN M28): a subject lock and a target quarantine.
//
// Each is a row in a table of its own, keyed by the id it denies and referencing
// nothing. The decision path reads one in the statement that already reads the
// subject or the target (Subjects().Standing, Targets().StandingByHostname), so
// a standing denial costs the path no round trip (M5).

// StandingActor is who placed a standing denial: the person behind the
// credential (empty for a machine token, and for an inbound action handler,
// which has no person), the credential, and whether it was a break-glass one
// (M7).
type StandingActor struct {
	Subject    string
	Principal  string
	BreakGlass bool
}

// SubjectLock is a standing denial on a subject id: while it stands, every
// authorize for that subject is a deny, whatever policy, grants or windows say.
type SubjectLock struct {
	// SubjectID is the denied subject. It need not have a row: a lock outlives
	// a deleted subject.
	SubjectID string
	// LockedAt is when it was placed.
	LockedAt time.Time
	// LockedBy is who placed it.
	LockedBy StandingActor
	// ReasonCode is a stable code a report can group by; Reason is the text
	// the holder was shown when their sessions were ended.
	ReasonCode string
	Reason     string
	// ExternalRef and ActionID tie it to the incident and the inbound action
	// that caused it, when an action placed it.
	ExternalRef string
	ActionID    string
}

// TargetQuarantine is a standing denial on a target: while it stands, no new
// access to that target is allowed, whatever policy, grants or windows say.
type TargetQuarantine struct {
	TargetID      string
	QuarantinedAt time.Time
	QuarantinedBy StandingActor
	ReasonCode    string
	Reason        string
	ExternalRef   string
	ActionID      string
}

// SubjectStanding is a subject as the decision path reads it: the row, if
// there is one, and every standing denial on the id, in ONE round trip.
//
// A lock holds for an id with no row, so absence of the row is not absence of
// the lock. A second standing denial on a subject (the directory's deactivation,
// 0040) is another nullable join in the same statement and another field here;
// it is not a second query.
type SubjectStanding struct {
	// Subject is the row; the zero value when Known is false.
	Subject Subject
	// Known reports that this server holds a row for the subject.
	Known bool
	// Lock is the lock on the id, nil when none stands.
	Lock *SubjectLock
}

// TargetStanding is a target as the decision path reads it: the row and the
// quarantine on it, in one round trip.
type TargetStanding struct {
	Target Target
	// Quarantine is nil when none stands.
	Quarantine *TargetQuarantine
}

// ErrTargetQuarantined reports a target that cannot be deleted because it is
// quarantined: deleting the row would lift the quarantine silently.
var ErrTargetQuarantined = errors.New("store: the target is quarantined")

// IsTargetQuarantined reports whether err is a refusal to delete a quarantined
// target.
func IsTargetQuarantined(err error) bool { return errors.Is(err, ErrTargetQuarantined) }

// ---------------------------------------------------------------------------
// subject locks
// ---------------------------------------------------------------------------

type subjectLockRepo struct{ s *Store }

const subjectLockColumns = `subject_id, locked_at, locked_by, locked_by_principal,
	locked_by_break_glass, reason_code, reason, external_ref, action_id`

func scanSubjectLock(op string, row rowScanner) (SubjectLock, error) {
	var l SubjectLock
	err := row.Scan(&l.SubjectID, &l.LockedAt, &l.LockedBy.Subject, &l.LockedBy.Principal,
		&l.LockedBy.BreakGlass, &l.ReasonCode, &l.Reason, &l.ExternalRef, &l.ActionID)
	if err != nil {
		return SubjectLock{}, wrap(op, err)
	}
	l.LockedAt = l.LockedAt.UTC()
	return l, nil
}

// Place stores a lock, or leaves the one that stands. It returns the lock now
// standing and whether THIS call placed it: the first lock's time, actor and
// reason are the ones that stand.
func (r subjectLockRepo) Place(ctx context.Context, tenant Tenant, l SubjectLock) (SubjectLock, bool, error) {
	const op = "store.SubjectLocks.Place"
	if err := checkTenant(op, tenant); err != nil {
		return SubjectLock{}, false, err
	}
	if l.SubjectID == "" {
		return SubjectLock{}, false, invalid(op, "subject id is required")
	}
	if l.LockedBy.Principal == "" {
		return SubjectLock{}, false, invalid(op, "the credential that placed the lock is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var (
		cur    SubjectLock
		placed bool
	)
	err := r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		tag, err := q.Exec(ctx, `
			INSERT INTO subject_locks (tenant, subject_id, locked_at, locked_by, locked_by_principal,
				locked_by_break_glass, reason_code, reason, external_ref, action_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (tenant, subject_id) DO NOTHING`,
			tenant, l.SubjectID, l.LockedAt.UTC(), l.LockedBy.Subject, l.LockedBy.Principal,
			l.LockedBy.BreakGlass, l.ReasonCode, l.Reason, l.ExternalRef, l.ActionID)
		if err != nil {
			return wrap(op, err)
		}
		placed = tag.RowsAffected() == 1
		cur, err = scanSubjectLock(op, q.QueryRow(ctx, `
			SELECT `+subjectLockColumns+` FROM subject_locks WHERE tenant = $1 AND subject_id = $2`,
			tenant, l.SubjectID))
		return err
	})
	if err != nil {
		return SubjectLock{}, false, err
	}
	return cur, placed, nil
}

// Lift deletes the lock on a subject and returns it. Nothing standing is
// ErrNotFound.
func (r subjectLockRepo) Lift(ctx context.Context, tenant Tenant, subjectID string) (SubjectLock, error) {
	const op = "store.SubjectLocks.Lift"
	if err := checkTenant(op, tenant); err != nil {
		return SubjectLock{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	return scanSubjectLock(op, r.s.db.QueryRow(ctx, `
		DELETE FROM subject_locks WHERE tenant = $1 AND subject_id = $2
		RETURNING `+subjectLockColumns, tenant, subjectID))
}

// Get returns the lock on a subject. Nothing standing is ErrNotFound.
func (r subjectLockRepo) Get(ctx context.Context, tenant Tenant, subjectID string) (SubjectLock, error) {
	const op = "store.SubjectLocks.Get"
	if err := checkTenant(op, tenant); err != nil {
		return SubjectLock{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	return scanSubjectLock(op, r.s.db.QueryRow(ctx, `
		SELECT `+subjectLockColumns+` FROM subject_locks WHERE tenant = $1 AND subject_id = $2`,
		tenant, subjectID))
}

// For returns the locks standing on these subjects, by subject id: the read a
// page of the subject list makes for its own rows. It names at most a page's
// worth of ids (M26).
func (r subjectLockRepo) For(ctx context.Context, tenant Tenant, subjectIDs []string) (map[string]SubjectLock, error) {
	const op = "store.SubjectLocks.For"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if err := checkPageIDs(op, subjectIDs); err != nil {
		return nil, err
	}
	out := map[string]SubjectLock{}
	if len(subjectIDs) == 0 {
		return out, nil
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+subjectLockColumns+` FROM subject_locks
		WHERE tenant = $1 AND subject_id = ANY($2)`, tenant, subjectIDs)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	for rows.Next() {
		l, err := scanSubjectLock(op, rows)
		if err != nil {
			return nil, err
		}
		out[l.SubjectID] = l
	}
	return out, wrap(op, rows.Err())
}

// ---------------------------------------------------------------------------
// target quarantines
// ---------------------------------------------------------------------------

type targetQuarantineRepo struct{ s *Store }

const targetQuarantineColumns = `target_id, quarantined_at, quarantined_by, quarantined_by_principal,
	quarantined_by_break_glass, reason_code, reason, external_ref, action_id`

func scanTargetQuarantine(op string, row rowScanner) (TargetQuarantine, error) {
	var q TargetQuarantine
	err := row.Scan(&q.TargetID, &q.QuarantinedAt, &q.QuarantinedBy.Subject, &q.QuarantinedBy.Principal,
		&q.QuarantinedBy.BreakGlass, &q.ReasonCode, &q.Reason, &q.ExternalRef, &q.ActionID)
	if err != nil {
		return TargetQuarantine{}, wrap(op, err)
	}
	q.QuarantinedAt = q.QuarantinedAt.UTC()
	return q, nil
}

// Place stores a quarantine, or leaves the one that stands, and returns the one
// now standing and whether THIS call placed it. The target must have a row: the
// row is read FOR SHARE, so a concurrent delete waits for the quarantine and
// then refuses, and a quarantine is never placed on a target that is gone.
func (r targetQuarantineRepo) Place(ctx context.Context, tenant Tenant, q TargetQuarantine) (TargetQuarantine, bool, error) {
	const op = "store.TargetQuarantines.Place"
	if err := checkTenant(op, tenant); err != nil {
		return TargetQuarantine{}, false, err
	}
	if q.TargetID == "" {
		return TargetQuarantine{}, false, invalid(op, "target id is required")
	}
	if q.QuarantinedBy.Principal == "" {
		return TargetQuarantine{}, false, invalid(op, "the credential that placed the quarantine is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var (
		cur    TargetQuarantine
		placed bool
	)
	err := r.s.inTx(ctx, op, func(ctx context.Context, tx querier) error {
		var one int
		if err := tx.QueryRow(ctx, `
			SELECT 1 FROM targets WHERE tenant = $1 AND target_id = $2 FOR SHARE`,
			tenant, q.TargetID).Scan(&one); err != nil {
			return wrap(op, err)
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO target_quarantines (tenant, target_id, quarantined_at, quarantined_by,
				quarantined_by_principal, quarantined_by_break_glass, reason_code, reason,
				external_ref, action_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (tenant, target_id) DO NOTHING`,
			tenant, q.TargetID, q.QuarantinedAt.UTC(), q.QuarantinedBy.Subject, q.QuarantinedBy.Principal,
			q.QuarantinedBy.BreakGlass, q.ReasonCode, q.Reason, q.ExternalRef, q.ActionID)
		if err != nil {
			return wrap(op, err)
		}
		placed = tag.RowsAffected() == 1
		cur, err = scanTargetQuarantine(op, tx.QueryRow(ctx, `
			SELECT `+targetQuarantineColumns+` FROM target_quarantines WHERE tenant = $1 AND target_id = $2`,
			tenant, q.TargetID))
		return err
	})
	if err != nil {
		return TargetQuarantine{}, false, err
	}
	return cur, placed, nil
}

// Lift deletes the quarantine on a target and returns it. Nothing standing is
// ErrNotFound.
func (r targetQuarantineRepo) Lift(ctx context.Context, tenant Tenant, targetID string) (TargetQuarantine, error) {
	const op = "store.TargetQuarantines.Lift"
	if err := checkTenant(op, tenant); err != nil {
		return TargetQuarantine{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	return scanTargetQuarantine(op, r.s.db.QueryRow(ctx, `
		DELETE FROM target_quarantines WHERE tenant = $1 AND target_id = $2
		RETURNING `+targetQuarantineColumns, tenant, targetID))
}

// Get returns the quarantine on a target. Nothing standing is ErrNotFound.
func (r targetQuarantineRepo) Get(ctx context.Context, tenant Tenant, targetID string) (TargetQuarantine, error) {
	const op = "store.TargetQuarantines.Get"
	if err := checkTenant(op, tenant); err != nil {
		return TargetQuarantine{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	return scanTargetQuarantine(op, r.s.db.QueryRow(ctx, `
		SELECT `+targetQuarantineColumns+` FROM target_quarantines WHERE tenant = $1 AND target_id = $2`,
		tenant, targetID))
}

// For returns the quarantines standing on these targets, by target id: the read
// a page of the target list makes for its own rows (M26).
func (r targetQuarantineRepo) For(ctx context.Context, tenant Tenant, targetIDs []string) (map[string]TargetQuarantine, error) {
	const op = "store.TargetQuarantines.For"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if err := checkPageIDs(op, targetIDs); err != nil {
		return nil, err
	}
	out := map[string]TargetQuarantine{}
	if len(targetIDs) == 0 {
		return out, nil
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT `+targetQuarantineColumns+` FROM target_quarantines
		WHERE tenant = $1 AND target_id = ANY($2)`, tenant, targetIDs)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	for rows.Next() {
		q, err := scanTargetQuarantine(op, rows)
		if err != nil {
			return nil, err
		}
		out[q.TargetID] = q
	}
	return out, wrap(op, rows.Err())
}

// ---------------------------------------------------------------------------
// the decision path's reads
// ---------------------------------------------------------------------------

// Standing returns a subject the way the decision path reads it: the row, if
// there is one, and the lock on the id, if one stands — in ONE statement
// (M5), by the primary key of each. An id with neither is not an error: it is a
// SubjectStanding with Known false and no lock.
func (r subjectRepo) Standing(ctx context.Context, tenant Tenant, subjectID string) (SubjectStanding, error) {
	const op = "store.Subjects.Standing"
	if err := checkTenant(op, tenant); err != nil {
		return SubjectStanding{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// DECISION PATH (M5): the subject and its lock in one round trip. The id
	// comes from a one-row derived table so that a lock for an id with no
	// subject row still comes back, and each join is a primary-key hit.
	var (
		out    SubjectStanding
		locked bool
		l      SubjectLock
	)
	err := r.s.db.QueryRow(ctx, `
		SELECT s.subject_id IS NOT NULL,
			COALESCE(s.source, ''), COALESCE(s.display_name, ''),
			COALESCE(s.principals, '{}'), COALESCE(s.groups, '{}'), COALESCE(s.claims, '{}'::jsonb),
			COALESCE(s.break_glass, false), COALESCE(s.mapping_version, 0),
			COALESCE(s.created_at, 'epoch'), COALESCE(s.updated_at, 'epoch'),
			k.subject_id IS NOT NULL,
			COALESCE(k.locked_at, 'epoch'), COALESCE(k.locked_by, ''), COALESCE(k.locked_by_principal, ''),
			COALESCE(k.locked_by_break_glass, false), COALESCE(k.reason_code, ''), COALESCE(k.reason, ''),
			COALESCE(k.external_ref, ''), COALESCE(k.action_id, '')
		FROM (SELECT $1::text AS tenant, $2::text AS subject_id) q
		LEFT JOIN subjects s ON s.tenant = q.tenant AND s.subject_id = q.subject_id
		LEFT JOIN subject_locks k ON k.tenant = q.tenant AND k.subject_id = q.subject_id`,
		tenant, subjectID).Scan(
		&out.Known,
		&out.Subject.Source, &out.Subject.DisplayName,
		&out.Subject.Principals, &out.Subject.Groups, &out.Subject.Claims,
		&out.Subject.BreakGlass, &out.Subject.MappingVersion,
		&out.Subject.CreatedAt, &out.Subject.UpdatedAt,
		&locked,
		&l.LockedAt, &l.LockedBy.Subject, &l.LockedBy.Principal,
		&l.LockedBy.BreakGlass, &l.ReasonCode, &l.Reason,
		&l.ExternalRef, &l.ActionID)
	if err != nil {
		return SubjectStanding{}, wrap(op, err)
	}
	if out.Known {
		out.Subject.ID = subjectID
	}
	if locked {
		l.SubjectID = subjectID
		l.LockedAt = l.LockedAt.UTC()
		out.Lock = &l
	}
	return out, nil
}

// StandingByHostname is the decision path's target lookup: the row by hostname
// and the quarantine on it, in one statement and one round trip (M5). A
// hostname with no row is ErrNotFound, as GetByHostname answers it.
func (r targetRepo) StandingByHostname(ctx context.Context, tenant Tenant, hostname string) (TargetStanding, error) {
	const op = "store.Targets.StandingByHostname"
	if err := checkTenant(op, tenant); err != nil {
		return TargetStanding{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var (
		out         TargetStanding
		quarantined bool
		q           TargetQuarantine
	)
	t := &out.Target
	err := r.s.db.QueryRow(ctx, `
		SELECT t.target_id, t.hostname, t.zone, t.labels, t.credential_method, t.created_at, t.updated_at,
			k.target_id IS NOT NULL,
			COALESCE(k.quarantined_at, 'epoch'), COALESCE(k.quarantined_by, ''),
			COALESCE(k.quarantined_by_principal, ''), COALESCE(k.quarantined_by_break_glass, false),
			COALESCE(k.reason_code, ''), COALESCE(k.reason, ''),
			COALESCE(k.external_ref, ''), COALESCE(k.action_id, '')
		FROM targets t
		LEFT JOIN target_quarantines k ON k.tenant = t.tenant AND k.target_id = t.target_id
		WHERE t.tenant = $1 AND t.hostname = $2`, tenant, hostname).Scan(
		&t.ID, &t.Hostname, &t.Zone, &t.Labels, &t.CredentialMethod, &t.CreatedAt, &t.UpdatedAt,
		&quarantined,
		&q.QuarantinedAt, &q.QuarantinedBy.Subject, &q.QuarantinedBy.Principal,
		&q.QuarantinedBy.BreakGlass, &q.ReasonCode, &q.Reason, &q.ExternalRef, &q.ActionID)
	if err != nil {
		return TargetStanding{}, wrap(op, err)
	}
	if quarantined {
		q.TargetID = t.ID
		q.QuarantinedAt = q.QuarantinedAt.UTC()
		out.Quarantine = &q
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// the action ledger
// ---------------------------------------------------------------------------

// ActionExecution is one inbound action Control has executed: what was asked,
// the first answer, and who asked.
type ActionExecution struct {
	ActionID string
	// Kind is the action kind's code, and the four selectors are the one the
	// kind carries; the others are empty.
	Kind      string
	SubjectID string
	SessionID string
	TargetID  string
	GrantID   string
	// Affected, ReasonCode and AppliedAt are the first answer, returned again
	// on every repeat.
	Affected   int
	ReasonCode string
	AppliedAt  time.Time
	// Provider is the registered provider that asked.
	Provider string
	// HolderReason is the reason a kill showed the holder, kept so a repeat
	// sends the same words.
	HolderReason string
	// RecordedAt is when the record of the act was written; zero while a kill
	// that owes one has not been published.
	RecordedAt time.Time
}

type actionExecutionRepo struct{ s *Store }

const actionExecutionColumns = `action_id, kind, subject_id, session_id, target_id, grant_id,
	affected, reason_code, applied_at, provider, holder_reason, COALESCE(recorded_at, 'epoch')`

func scanActionExecution(op string, row rowScanner) (ActionExecution, error) {
	var a ActionExecution
	err := row.Scan(&a.ActionID, &a.Kind, &a.SubjectID, &a.SessionID, &a.TargetID, &a.GrantID,
		&a.Affected, &a.ReasonCode, &a.AppliedAt, &a.Provider, &a.HolderReason, &a.RecordedAt)
	if err != nil {
		return ActionExecution{}, wrap(op, err)
	}
	a.AppliedAt = a.AppliedAt.UTC()
	if a.RecordedAt.Unix() <= 0 {
		a.RecordedAt = time.Time{}
	} else {
		a.RecordedAt = a.RecordedAt.UTC()
	}
	return a, nil
}

// actionKeySeparator keeps a tenant from running into an action id in the
// advisory lock's key.
const actionKeySeparator = "\x1f"

// Lock serialises everything done under one action id for the rest of the
// transaction. A transaction-scoped lock taken outside a transaction is
// released the instant the statement ends, so it is refused there.
func (r actionExecutionRepo) Lock(ctx context.Context, tenant Tenant, actionID string) error {
	const op = "store.ActionExecutions.Lock"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if r.s.pool != nil {
		return invalid(op, "the action's lock is taken inside a transaction")
	}
	if actionID == "" {
		return invalid(op, "an action id is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"action"+actionKeySeparator+string(tenant)+actionKeySeparator+actionID)
	return wrap(op, err)
}

// Get returns the execution under an action id. Absent is ErrNotFound.
func (r actionExecutionRepo) Get(ctx context.Context, tenant Tenant, actionID string) (ActionExecution, error) {
	const op = "store.ActionExecutions.Get"
	if err := checkTenant(op, tenant); err != nil {
		return ActionExecution{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	return scanActionExecution(op, r.s.db.QueryRow(ctx, `
		SELECT `+actionExecutionColumns+` FROM action_executions
		WHERE tenant = $1 AND action_id = $2`, tenant, actionID))
}

// Insert records an execution. A second under the same id is ErrConflict.
func (r actionExecutionRepo) Insert(ctx context.Context, tenant Tenant, a ActionExecution) error {
	const op = "store.ActionExecutions.Insert"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if a.ActionID == "" || a.Kind == "" || a.Provider == "" {
		return invalid(op, "an action id, kind and provider are required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var recorded *time.Time
	if !a.RecordedAt.IsZero() {
		t := a.RecordedAt.UTC()
		recorded = &t
	}
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO action_executions (tenant, action_id, kind, subject_id, session_id, target_id,
			grant_id, affected, reason_code, applied_at, provider, holder_reason, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		tenant, a.ActionID, a.Kind, a.SubjectID, a.SessionID, a.TargetID, a.GrantID,
		a.Affected, a.ReasonCode, a.AppliedAt.UTC(), a.Provider, a.HolderReason, recorded)
	return wrap(op, err)
}

// MarkRecorded stamps the time the record of an execution was written. Absent
// is ErrNotFound.
func (r actionExecutionRepo) MarkRecorded(ctx context.Context, tenant Tenant, actionID string, at time.Time) error {
	const op = "store.ActionExecutions.MarkRecorded"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	tag, err := r.s.db.Exec(ctx, `
		UPDATE action_executions SET recorded_at = $3
		WHERE tenant = $1 AND action_id = $2`, tenant, actionID, at.UTC())
	if err != nil {
		return wrap(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}
