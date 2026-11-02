// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The reads and the one statement a confined inbound action needs (0041): a
// session's reach, targets and a grant held until the act, the unlock that
// lifts only its own lock, and the ledger column that makes the condition part
// of the action.

// A conditional lift lifts only the lock that action of that principal placed,
// in one statement, and leaves every other standing.
func TestLiftIfPlacedByLiftsOnlyTheLockItsActionPlaced(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	const handler = "action-handler:soar"

	place := func(tn store.Tenant, subject, principal, actionID string) {
		t.Helper()
		l := lockBy(subject, principal)
		l.ActionID = actionID
		if _, placed, err := st.SubjectLocks().Place(ctx, tn, l); err != nil || !placed {
			t.Fatalf("Place %s on %s: %v, %v", actionID, subject, placed, err)
		}
	}
	place(tenantA, "op-locked", "p-operator", "")
	place(tenantA, "other-action", handler, "B")
	place(tenantA, "other-handler", "action-handler:renamed", "A")
	place(tenantA, "mine", handler, "A")
	place(tenantB, "mine", handler, "A")

	for _, subject := range []string{"op-locked", "other-action", "other-handler", "nobody"} {
		if _, err := st.SubjectLocks().LiftIfPlacedBy(ctx, tenantA, subject, "A", handler); !store.IsNotFound(err) {
			t.Errorf("LiftIfPlacedBy(%s): %v, want ErrNotFound", subject, err)
		}
	}
	for _, subject := range []string{"op-locked", "other-action", "other-handler"} {
		if _, err := st.SubjectLocks().Get(ctx, tenantA, subject); err != nil {
			t.Errorf("the lock on %s was lifted by a condition it does not meet: %v", subject, err)
		}
	}

	lifted, err := st.SubjectLocks().LiftIfPlacedBy(ctx, tenantA, "mine", "A", handler)
	if err != nil || lifted.ActionID != "A" || lifted.LockedBy.Principal != handler {
		t.Fatalf("LiftIfPlacedBy(mine) = %+v, %v", lifted, err)
	}
	if _, err := st.SubjectLocks().Get(ctx, tenantA, "mine"); !store.IsNotFound(err) {
		t.Errorf("the lock the action placed still stands: %v", err)
	}
	if _, err := st.SubjectLocks().Get(ctx, tenantB, "mine"); err != nil {
		t.Errorf("tenant B's lock under the same action id was lifted by tenant A: %v", err)
	}

	// An empty condition would match an operator's lock: it is refused.
	for name, args := range map[string][2]string{"no action id": {"", handler}, "no principal": {"A", ""}} {
		if _, err := st.SubjectLocks().LiftIfPlacedBy(ctx, tenantA, "op-locked", args[0], args[1]); !store.IsInvalid(err) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if _, err := st.SubjectLocks().Get(ctx, tenantA, "op-locked"); err != nil {
		t.Errorf("the operator's lock was lifted: %v", err)
	}
}

// A session reaches every subject and target its decision records name, each
// once, every hop included; another tenant's session reaches nothing.
func TestSessionReachIsEveryHopOfTheSession(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	insert := func(tn store.Tenant, id, session, subject, target string) {
		t.Helper()
		if err := st.Decisions().Insert(ctx, tn, store.Decision{
			ID: id, SubjectID: subject, TargetID: target, InputsDigest: "x", Snapshot: json.RawMessage(`{}`),
			Effect: store.DecisionEffectAllow, ProxyID: "p", SessionID: session, DecidedAt: time.Now(),
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert(tenantA, "d1", "sess-1", "alice", "t-bastion")
	insert(tenantA, "d2", "sess-1", "alice", "t-db")
	insert(tenantA, "d3", "sess-1", "alice", "")
	insert(tenantA, "d4", "sess-1", "alice", "t-db")
	insert(tenantA, "d5", "sess-2", "bob", "t-web")
	insert(tenantB, "d6", "sess-b", "carol", "t-b")

	reach, err := st.Decisions().SessionReach(ctx, tenantA, "sess-1")
	if err != nil {
		t.Fatalf("SessionReach: %v", err)
	}
	if !slices.Equal(reach.SubjectIDs, []string{"alice"}) || !slices.Equal(reach.TargetIDs, []string{"", "t-bastion", "t-db"}) {
		t.Errorf("SessionReach(sess-1) = %+v, want alice over both hops and the unrecorded one", reach)
	}
	for name, sess := range map[string]string{"an unknown session": "sess-none", "another tenant's session": "sess-b"} {
		reach, err := st.Decisions().SessionReach(ctx, tenantA, sess)
		if err != nil || len(reach.SubjectIDs) != 0 || len(reach.TargetIDs) != 0 {
			t.Errorf("%s reaches %+v (%v), want nothing", name, reach, err)
		}
	}
	if _, err := st.Decisions().SessionReach(ctx, tenantA, ""); !store.IsInvalid(err) {
		t.Errorf("SessionReach of no session: %v, want ErrInvalid", err)
	}
}

// The reads a confined action's check makes hold their rows until the act:
// another writer cannot take the row meanwhile, and outside a transaction the
// reads are refused, because a lock there would hold nothing.
func TestTheChecksReadsHoldTheirRowsUntilTheAct(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	for _, tn := range []store.Tenant{tenantA, tenantB} {
		storetest.Seed(t, st, storetest.Fixture{
			Tenant: tn,
			Targets: []store.Target{
				storetest.Target("t-db", "db01.example.com", map[string]string{"env": "prod"}),
				storetest.Target("t-ssh", "SSH01.example.com", map[string]string{"env": "prod"}),
				storetest.Target("t-web", "web01.example.com", nil),
			},
			Grants: []store.Grant{storetest.Grant("g-1", "alice", time.Hour)},
		})
	}

	if _, err := st.Targets().ForShare(ctx, tenantA, []string{"t-db"}); !store.IsInvalid(err) {
		t.Errorf("ForShare outside a transaction: %v, want ErrInvalid", err)
	}
	if _, err := st.Targets().ForShareByHostnames(ctx, tenantA, []string{"ssh01.example.com"}); !store.IsInvalid(err) {
		t.Errorf("ForShareByHostnames outside a transaction: %v, want ErrInvalid", err)
	}
	if _, err := st.Grants().GetForUpdate(ctx, tenantA, "g-1"); !store.IsInvalid(err) {
		t.Errorf("GetForUpdate outside a transaction: %v, want ErrInvalid", err)
	}

	err := st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		byID, err := tx.Targets().ForShare(ctx, tenantA, []string{"t-db", "t-none"})
		if err != nil {
			return err
		}
		if len(byID) != 1 || byID["t-db"].Labels["env"] != "prod" {
			t.Errorf("ForShare = %+v, want t-db alone", byID)
		}
		// Case-insensitive, as a grant's selector matches.
		byHost, err := tx.Targets().ForShareByHostnames(ctx, tenantA, []string{"ssh01.example.com", "none.example.com"})
		if err != nil {
			return err
		}
		if len(byHost) != 1 || byHost[0].ID != "t-ssh" {
			t.Errorf("ForShareByHostnames = %+v, want t-ssh alone", byHost)
		}
		g, err := tx.Grants().GetForUpdate(ctx, tenantA, "g-1")
		if err != nil || g.SubjectID != "alice" {
			t.Errorf("GetForUpdate = %+v, %v", g, err)
		}
		if _, err := tx.Grants().GetForUpdate(ctx, tenantA, "g-none"); !store.IsNotFound(err) {
			t.Errorf("GetForUpdate of an unknown grant: %v, want ErrNotFound", err)
		}

		// While this transaction stands, a writer cannot take the rows it
		// read, and can take one it did not.
		for name, stmt := range map[string]string{
			"the target read by id": `SELECT 1 FROM targets WHERE tenant = 'tenant-a' AND target_id = 't-db' FOR UPDATE NOWAIT`,
			"the grant":             `SELECT 1 FROM grants WHERE tenant = 'tenant-a' AND grant_id = 'g-1' FOR SHARE NOWAIT`,
			"a relabel of the target read by hostname": `UPDATE targets SET labels = '{}' WHERE tenant = 'tenant-a' AND target_id = 't-ssh'`,
		} {
			if !heldBy(t, st, stmt) {
				t.Errorf("%s is not held: another writer took it meanwhile", name)
			}
		}
		if heldBy(t, st, `SELECT 1 FROM targets WHERE tenant = 'tenant-a' AND target_id = 't-web' FOR UPDATE NOWAIT`) {
			t.Error("a target the check did not read is held")
		}
		if heldBy(t, st, `SELECT 1 FROM targets WHERE tenant = 'tenant-b' AND target_id = 't-db' FOR UPDATE NOWAIT`) {
			t.Error("another tenant's target is held")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
}

// heldBy runs stmt on a connection of its own under a short lock timeout, and
// reports whether it found the row locked.
func heldBy(t *testing.T, st *store.Store, stmt string) bool {
	t.Helper()
	conn, err := st.Pool().Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `SET LOCAL lock_timeout = '200ms'`); err != nil {
		t.Fatalf("lock_timeout: %v", err)
	}
	_, err = tx.Exec(t.Context(), stmt)
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		return false
	case errors.As(err, &pgErr) && (pgErr.Code == "55P03"):
		return true
	}
	t.Fatalf("%s: %v", stmt, err)
	return false
}

// The ledger keeps an unlock's condition, and 0022 applies forward over the
// rows 0020's ledger already holds: they read no condition, which is what they
// asked for.
func TestTheLedgerConditionAppliesForwardOverExistingRows(t *testing.T) {
	t.Parallel()
	dsn := storetest.DSN(t)
	ctx := t.Context()
	st, err := store.Open(ctx, storetest.DSNForSchema(dsn, storetest.NewSchema(t, dsn)))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)

	if err := store.MigrateThrough(ctx, st, 21); err != nil {
		t.Fatalf("migrate through 0021: %v", err)
	}
	// A row as 0039 wrote it, before the column existed.
	if _, err := st.Pool().Exec(ctx, `
		INSERT INTO action_executions (tenant, action_id, kind, subject_id, affected, reason_code, applied_at, provider, recorded_at)
		VALUES ('tenant-a', 'u-old', 'unlock_subject', 'alice', 1, '', now(), 'soar', now())`); err != nil {
		t.Fatalf("insert a 0020 ledger row: %v", err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	old, err := st.ActionExecutions().Get(ctx, tenantA, "u-old")
	if err != nil || old.IfPlacedBy != "" || old.Kind != "unlock_subject" || old.SubjectID != "alice" || old.Affected != 1 {
		t.Errorf("the row written before 0022 reads %+v (%v), want it whole with no condition", old, err)
	}

	row := store.ActionExecution{
		ActionID: "u-new", Kind: "unlock_subject", SubjectID: "alice", AppliedAt: time.Now().UTC(),
		Provider: "soar", ReasonCode: "not_this_lock", IfPlacedBy: "lock-1",
	}
	if err := st.ActionExecutions().Insert(ctx, tenantA, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := st.ActionExecutions().Get(ctx, tenantA, "u-new")
	if err != nil || got.IfPlacedBy != "lock-1" {
		t.Errorf("Get = %+v (%v), want the condition kept", got, err)
	}
}
