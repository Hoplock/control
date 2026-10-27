// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

func lockBy(subject, principal string) store.SubjectLock {
	return store.SubjectLock{
		SubjectID: subject, LockedAt: time.Now().UTC().Truncate(time.Microsecond),
		LockedBy:   store.StandingActor{Principal: principal},
		ReasonCode: "compromised", Reason: "your account is locked", ExternalRef: "INC-9", ActionID: "act-1",
	}
}

// The decision path's read answers the row and the lock in one statement, and
// answers a lock for an id that has no row.
func TestSubjectStandingReadsTheRowAndItsLockTogether(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	if err := st.Subjects().Upsert(ctx, tenantA, storetest.Subject("alice", "sre")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Neither a row nor a lock is a Standing, not an error.
	got, err := st.Subjects().Standing(ctx, tenantA, "nobody")
	if err != nil {
		t.Fatalf("Standing of an unknown id: %v", err)
	}
	if got.Known || got.Lock != nil {
		t.Errorf("an unknown id read as %+v, want neither a row nor a lock", got)
	}

	// A row and no lock.
	got, err = st.Subjects().Standing(ctx, tenantA, "alice")
	if err != nil {
		t.Fatalf("Standing: %v", err)
	}
	if !got.Known || got.Lock != nil || got.Subject.ID != "alice" || len(got.Subject.Groups) != 1 {
		t.Errorf("an unlocked subject read as %+v", got)
	}

	// A row and its lock.
	want := lockBy("alice", "action-handler:acme")
	placed, ok, err := st.SubjectLocks().Place(ctx, tenantA, want)
	if err != nil || !ok {
		t.Fatalf("Place = %+v, %v, %v; want it placed", placed, ok, err)
	}
	got, err = st.Subjects().Standing(ctx, tenantA, "alice")
	if err != nil {
		t.Fatalf("Standing: %v", err)
	}
	if !got.Known || got.Lock == nil {
		t.Fatalf("a locked subject read as %+v, want the row and the lock", got)
	}
	if l := *got.Lock; l.SubjectID != "alice" || l.LockedBy.Principal != "action-handler:acme" ||
		l.ReasonCode != "compromised" || l.ExternalRef != "INC-9" || l.ActionID != "act-1" || !l.LockedAt.Equal(want.LockedAt) {
		t.Errorf("the lock read back as %+v, want %+v", l, want)
	}

	// The lock outlives the row, and deleting then re-creating the subject
	// does not lift it.
	if err := st.Subjects().Delete(ctx, tenantA, "alice"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err = st.Subjects().Standing(ctx, tenantA, "alice")
	if err != nil {
		t.Fatalf("Standing after delete: %v", err)
	}
	if got.Known || got.Lock == nil {
		t.Errorf("after the row was deleted the read is %+v, want no row and the lock still standing", got)
	}
	if err := st.Subjects().Upsert(ctx, tenantA, storetest.Subject("alice", "sre")); err != nil {
		t.Fatalf("re-create: %v", err)
	}
	got, _ = st.Subjects().Standing(ctx, tenantA, "alice")
	if !got.Known || got.Lock == nil {
		t.Errorf("after the row was re-created the read is %+v, want the row and the lock", got)
	}

	// Another tenant's subject of the same id is neither locked nor known.
	got, err = st.Subjects().Standing(ctx, tenantB, "alice")
	if err != nil {
		t.Fatalf("Standing in another tenant: %v", err)
	}
	if got.Known || got.Lock != nil {
		t.Errorf("tenant B read tenant A's subject as %+v", got)
	}
}

func TestSubjectLockFirstPlacementStandsAndLiftReturnsIt(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	first := lockBy("alice", "first")
	cur, placed, err := st.SubjectLocks().Place(ctx, tenantA, first)
	if err != nil || !placed {
		t.Fatalf("first Place = %v, %v", placed, err)
	}
	if cur.LockedBy.Principal != "first" {
		t.Errorf("first Place returned %+v", cur)
	}

	// A second placement changes nothing: the first lock's time, actor and
	// reason are the ones that stand.
	second := lockBy("alice", "second")
	second.ReasonCode = "other"
	cur, placed, err = st.SubjectLocks().Place(ctx, tenantA, second)
	if err != nil {
		t.Fatalf("second Place: %v", err)
	}
	if placed || cur.LockedBy.Principal != "first" || cur.ReasonCode != "compromised" {
		t.Errorf("second Place = %+v placed=%v, want the first lock standing", cur, placed)
	}

	// For reads a page's worth, and only this tenant's.
	if _, _, err := st.SubjectLocks().Place(ctx, tenantB, lockBy("bob", "b")); err != nil {
		t.Fatalf("Place in tenant B: %v", err)
	}
	found, err := st.SubjectLocks().For(ctx, tenantA, []string{"alice", "bob", "carol"})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if len(found) != 1 || found["alice"].LockedBy.Principal != "first" {
		t.Errorf("For = %+v, want only tenant A's lock on alice", found)
	}

	lifted, err := st.SubjectLocks().Lift(ctx, tenantA, "alice")
	if err != nil || lifted.LockedBy.Principal != "first" {
		t.Fatalf("Lift = %+v, %v", lifted, err)
	}
	if _, err := st.SubjectLocks().Lift(ctx, tenantA, "alice"); !store.IsNotFound(err) {
		t.Errorf("Lift with nothing standing: %v, want ErrNotFound", err)
	}
	if _, err := st.SubjectLocks().Get(ctx, tenantA, "alice"); !store.IsNotFound(err) {
		t.Errorf("Get after Lift: %v, want ErrNotFound", err)
	}
	// Tenant B's lock on bob is untouched by a lift in tenant A.
	if _, err := st.SubjectLocks().Get(ctx, tenantB, "bob"); err != nil {
		t.Errorf("tenant B's lock: %v", err)
	}

	if _, _, err := st.SubjectLocks().Place(ctx, tenantA, store.SubjectLock{SubjectID: "x"}); !store.IsInvalid(err) {
		t.Errorf("Place with no credential: %v, want ErrInvalid", err)
	}
}

func TestTargetStandingAndQuarantine(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	if err := st.Targets().Upsert(ctx, tenantA, storetest.Target("t1", "db-1.example", map[string]string{"env": "prod"})); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := st.Targets().StandingByHostname(ctx, tenantA, "db-1.example")
	if err != nil || got.Quarantine != nil || got.Target.ID != "t1" || got.Target.Labels["env"] != "prod" {
		t.Fatalf("StandingByHostname = %+v, %v", got, err)
	}
	if _, err := st.Targets().StandingByHostname(ctx, tenantA, "absent.example"); !store.IsNotFound(err) {
		t.Errorf("StandingByHostname of an unknown host: %v, want ErrNotFound", err)
	}

	// A quarantine needs a target row.
	q := store.TargetQuarantine{
		TargetID: "ghost", QuarantinedAt: time.Now().UTC(), QuarantinedBy: store.StandingActor{Principal: "op"},
	}
	if _, _, err := st.TargetQuarantines().Place(ctx, tenantA, q); !store.IsNotFound(err) {
		t.Errorf("Place on a target with no row: %v, want ErrNotFound", err)
	}

	q.TargetID = "t1"
	q.ReasonCode = "compromised"
	placed, ok, err := st.TargetQuarantines().Place(ctx, tenantA, q)
	if err != nil || !ok {
		t.Fatalf("Place = %+v, %v, %v", placed, ok, err)
	}
	if _, ok, err := st.TargetQuarantines().Place(ctx, tenantA, q); err != nil || ok {
		t.Errorf("a second Place = placed %v, err %v; want it left standing", ok, err)
	}

	got, err = st.Targets().StandingByHostname(ctx, tenantA, "db-1.example")
	if err != nil || got.Quarantine == nil || got.Quarantine.ReasonCode != "compromised" || got.Quarantine.TargetID != "t1" {
		t.Fatalf("StandingByHostname after Place = %+v, %v", got, err)
	}

	// Deleting a quarantined target would lift the quarantine silently, so it
	// is refused, and the row stays.
	err = st.Targets().Delete(ctx, tenantA, "t1")
	if !store.IsTargetQuarantined(err) || !store.IsConflict(err) {
		t.Errorf("Delete of a quarantined target: %v, want a conflict naming the quarantine", err)
	}
	if _, err := st.Targets().Get(ctx, tenantA, "t1"); err != nil {
		t.Errorf("the quarantined target was deleted anyway: %v", err)
	}
	// Another tenant's target of the same id is not quarantined.
	if err := st.Targets().Upsert(ctx, tenantB, storetest.Target("t1", "db-1.example", nil)); err != nil {
		t.Fatalf("Upsert in tenant B: %v", err)
	}
	if err := st.Targets().Delete(ctx, tenantB, "t1"); err != nil {
		t.Errorf("Delete in tenant B: %v, want it deleted: the quarantine is tenant A's", err)
	}

	found, err := st.TargetQuarantines().For(ctx, tenantA, []string{"t1", "t2"})
	if err != nil || len(found) != 1 {
		t.Errorf("For = %+v, %v", found, err)
	}

	if _, err := st.TargetQuarantines().Lift(ctx, tenantA, "t1"); err != nil {
		t.Fatalf("Lift: %v", err)
	}
	if _, err := st.TargetQuarantines().Lift(ctx, tenantA, "t1"); !store.IsNotFound(err) {
		t.Errorf("Lift with nothing standing: %v, want ErrNotFound", err)
	}
	if err := st.Targets().Delete(ctx, tenantA, "t1"); err != nil {
		t.Errorf("Delete after the release: %v", err)
	}
}

// A quarantine placed while a delete is in flight is seen by it, and a delete
// in flight cannot leave a quarantine behind.
func TestQuarantineAndDeleteDoNotRace(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	const rounds = 25
	for i := range rounds {
		id := "t" + string(rune('a'+i))
		if err := st.Targets().Upsert(ctx, tenantA, storetest.Target(id, id+".example", nil)); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		var wg sync.WaitGroup
		var placeErr, deleteErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _, placeErr = st.TargetQuarantines().Place(ctx, tenantA, store.TargetQuarantine{
				TargetID: id, QuarantinedAt: time.Now().UTC(), QuarantinedBy: store.StandingActor{Principal: "op"},
			})
		}()
		go func() {
			defer wg.Done()
			deleteErr = st.Targets().Delete(ctx, tenantA, id)
		}()
		wg.Wait()

		_, getErr := st.Targets().Get(ctx, tenantA, id)
		_, qErr := st.TargetQuarantines().Get(ctx, tenantA, id)
		switch {
		case placeErr == nil && deleteErr == nil:
			// Delete first would make Place not-found, so both succeeding
			// means the place ran first and the delete did not see it.
			t.Fatalf("round %d: both succeeded: a quarantine and a delete interleaved (target err %v, quarantine err %v)", i, getErr, qErr)
		case placeErr == nil:
			if !store.IsTargetQuarantined(deleteErr) || getErr != nil || qErr != nil {
				t.Fatalf("round %d: placed, delete=%v, target=%v, quarantine=%v", i, deleteErr, getErr, qErr)
			}
		default:
			if !store.IsNotFound(placeErr) || deleteErr != nil || !store.IsNotFound(getErr) || !store.IsNotFound(qErr) {
				t.Fatalf("round %d: not placed (%v), delete=%v, target=%v, quarantine=%v", i, placeErr, deleteErr, getErr, qErr)
			}
		}
	}
}

func TestActionLedger(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	if err := st.ActionExecutions().Lock(ctx, tenantA, "a1"); !store.IsInvalid(err) {
		t.Errorf("Lock outside a transaction: %v, want ErrInvalid: it would serialise nothing", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	row := store.ActionExecution{
		ActionID: "a1", Kind: "kill_session", SessionID: "s-1", Affected: 1, AppliedAt: now,
		Provider: "acme", HolderReason: "ended by security",
	}
	if err := st.ActionExecutions().Insert(ctx, tenantA, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := st.ActionExecutions().Insert(ctx, tenantA, row); !store.IsConflict(err) {
		t.Errorf("a second Insert: %v, want ErrConflict", err)
	}
	got, err := st.ActionExecutions().Get(ctx, tenantA, "a1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Kind != "kill_session" || got.SessionID != "s-1" || got.Affected != 1 || !got.AppliedAt.Equal(now) ||
		got.Provider != "acme" || got.HolderReason != "ended by security" || !got.RecordedAt.IsZero() {
		t.Errorf("Get = %+v", got)
	}
	// The id is the tenant's.
	if _, err := st.ActionExecutions().Get(ctx, tenantB, "a1"); !store.IsNotFound(err) {
		t.Errorf("Get in another tenant: %v, want ErrNotFound", err)
	}
	if err := st.ActionExecutions().MarkRecorded(ctx, tenantA, "a1", now.Add(time.Second)); err != nil {
		t.Fatalf("MarkRecorded: %v", err)
	}
	got, _ = st.ActionExecutions().Get(ctx, tenantA, "a1")
	if !got.RecordedAt.Equal(now.Add(time.Second)) {
		t.Errorf("RecordedAt = %v after MarkRecorded", got.RecordedAt)
	}
	if err := st.ActionExecutions().MarkRecorded(ctx, tenantA, "absent", now); !store.IsNotFound(err) {
		t.Errorf("MarkRecorded of an absent id: %v, want ErrNotFound", err)
	}
}

// The advisory lock serialises everything under one action id, and only that
// id.
func TestActionLockSerialisesOneIDOnly(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	var inside, overlap atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
				if err := tx.ActionExecutions().Lock(ctx, tenantA, "same"); err != nil {
					return err
				}
				if inside.Add(1) > 1 {
					overlap.Add(1)
				}
				time.Sleep(10 * time.Millisecond)
				inside.Add(-1)
				return nil
			})
			if err != nil {
				t.Errorf("InTx: %v", err)
			}
		}()
	}
	wg.Wait()
	if overlap.Load() != 0 {
		t.Errorf("%d transactions were inside the lock for one action id at once", overlap.Load())
	}

	// A different id is not held up by the first.
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
			if err := tx.ActionExecutions().Lock(ctx, tenantA, "one"); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	other := make(chan error, 1)
	go func() {
		other <- st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
			return tx.ActionExecutions().Lock(ctx, tenantA, "two")
		})
	}()
	select {
	case err := <-other:
		if err != nil {
			t.Errorf("Lock of a different id: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("a lock on one action id held up another")
	}
	close(release)
	if err := <-done; err != nil {
		t.Errorf("InTx: %v", err)
	}
}
