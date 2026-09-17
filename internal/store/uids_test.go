// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

func TestUIDCursorAdvancesAndExhausts(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	storetest.Seed(t, st, storetest.Fixture{
		Tenant:     tenantA,
		UIDCursors: []storetest.UIDCursorFixture{{TargetID: "t1", First: 100000, RangeEnd: 100025}},
	})

	first, err := st.UIDCursors().Advance(ctx, tenantA, "t1", 10)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if first.From != 100000 || first.To != 100010 {
		t.Errorf("first block = [%d,%d), want [100000,100010)", first.From, first.To)
	}

	second, err := st.UIDCursors().Advance(ctx, tenantA, "t1", 10)
	if err != nil {
		t.Fatalf("second Advance: %v", err)
	}
	if second.From != first.To {
		t.Errorf("second block starts at %d, want %d: blocks must not overlap", second.From, first.To)
	}

	// Five uids are left, so a block of ten is exhausted rather than
	// partially granted. The contract answers this with 409: outage-class,
	// nothing provisioned, and the remedy is the operator's.
	_, err = st.UIDCursors().Advance(ctx, tenantA, "t1", 10)
	if !store.IsExhausted(err) {
		t.Errorf("Advance past the end of the range: %v, want ErrExhausted", err)
	}
	if store.IsNotFound(err) {
		t.Error("an exhausted range reads as a missing cursor; they are different answers")
	}

	// And a failed Advance grants nothing: the cursor did not move.
	cursor, err := st.UIDCursors().Get(ctx, tenantA, "t1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cursor.NextUID != second.To {
		t.Errorf("an exhausted Advance moved the cursor to %d, want %d", cursor.NextUID, second.To)
	}
	if cursor.Remaining() != 5 {
		t.Errorf("Remaining() = %d, want 5", cursor.Remaining())
	}

	// The remaining five can still be granted: exhausted is about the size
	// asked for, not about the cursor being unusable.
	last, err := st.UIDCursors().Advance(ctx, tenantA, "t1", 5)
	if err != nil {
		t.Fatalf("Advance of exactly the remainder: %v", err)
	}
	if last.To != 100025 {
		t.Errorf("last block ends at %d, want 100025", last.To)
	}

	// A cursor that does not exist is not an exhausted one.
	if _, err := st.UIDCursors().Advance(ctx, tenantA, "no-such-target", 1); !store.IsNotFound(err) {
		t.Errorf("Advance for an unknown target: %v, want ErrNotFound", err)
	}

	// Re-creating a cursor is the one operation that would move it
	// backwards without ever writing a lower value.
	if err := st.UIDCursors().Create(ctx, tenantA, "t1", 0, 100000); !store.IsConflict(err) {
		t.Errorf("re-creating a cursor: %v, want ErrConflict", err)
	}
}

// The cursor never goes backwards under concurrency.
//
// A test that advanced serially would pass on the lost-update bug this lock
// exists to prevent, so this one runs N real connections at once against one
// row and asserts the granted blocks are disjoint and cover the range exactly.
// A uid granted twice is a fresh session inheriting a torn-down one's files,
// and it is invisible until it is an incident.
func TestUIDCursorIsDisjointUnderConcurrency(t *testing.T) {
	t.Parallel()
	dsn := storetest.DSN(t)
	ctx := t.Context()

	schema := storetest.NewSchema(t, dsn)

	const (
		workers   = 16
		blockSize = 8
		first     = 100000
	)

	// Every worker gets its OWN Store, and therefore its own pool: sharing
	// one pool would still exercise the lock, but a separate connection per
	// worker is what a multi-node deployment actually looks like (M5: the
	// only scaling story is horizontal).
	stores := make([]*store.Store, workers)
	for i := range stores {
		st, err := store.Open(ctx, storetest.DSNForSchema(dsn, schema))
		if err != nil {
			t.Fatalf("open store %d: %v", i, err)
		}
		t.Cleanup(st.Close)
		stores[i] = st
	}

	if _, err := stores[0].Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := stores[0].UIDCursors().Create(ctx, tenantA, "t1", first, first+workers*blockSize); err != nil {
		t.Fatalf("create cursor: %v", err)
	}

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		blocks []store.UIDBlock
		errs   []error
		start  = make(chan struct{})
	)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release them together, so they contend
			block, err := stores[i].UIDCursors().Advance(ctx, tenantA, "t1", blockSize)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			blocks = append(blocks, block)
		}()
	}
	close(start)
	wg.Wait()

	for _, err := range errs {
		t.Errorf("concurrent Advance: %v", err)
	}
	if len(blocks) != workers {
		t.Fatalf("got %d blocks, want %d", len(blocks), workers)
	}

	slices.SortFunc(blocks, func(a, b store.UIDBlock) int { return int(a.From - b.From) })
	for i, b := range blocks {
		if b.Size() != blockSize {
			t.Errorf("block %d is %d wide, want %d", i, b.Size(), blockSize)
		}
		want := int64(first + i*blockSize)
		if b.From != want {
			// Disjoint AND strictly increasing: a gap would mean uids
			// lost, an overlap would mean uids reused.
			t.Errorf("block %d = [%d,%d), want it to start at %d", i, b.From, b.To, want)
		}
	}

	cursor, err := stores[0].UIDCursors().Get(ctx, tenantA, "t1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if want := int64(first + workers*blockSize); cursor.NextUID != want {
		t.Errorf("cursor is at %d after %d concurrent advances, want %d",
			cursor.NextUID, workers, want)
	}
}

// The forward-only guarantee is POSTGRES'S, not Go's.
//
// This test writes a lower cursor through raw SQL, with no repository method
// and therefore no Go guard anywhere in the path — which is the acceptance
// criterion: delete the Go-side check and the write must still fail. A caller
// that skips the check, a future phase that writes the column directly, and a
// repair script run at 3am all bypass a Go guard, and none of them bypasses
// the trigger.
func TestUIDCursorCannotBeLoweredEvenByRawSQL(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	storetest.Seed(t, st, storetest.Fixture{
		Tenant:     tenantA,
		UIDCursors: []storetest.UIDCursorFixture{{TargetID: "t1", First: 100000, RangeEnd: 200000}},
	})
	if _, err := st.UIDCursors().Advance(ctx, tenantA, "t1", 1000); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	_, err := st.Pool().Exec(ctx, `
		UPDATE uid_allocation_cursors
		SET next_uid = next_uid - 500
		WHERE tenant = $1 AND target_id = 't1'`, tenantA)
	if err == nil {
		t.Fatal("a raw UPDATE lowered the cursor: the forward-only guarantee is not the database's")
	}
	if !strings.Contains(err.Error(), "may only advance") {
		t.Errorf("error = %q, want the trigger's message", err)
	}

	cursor, err := st.UIDCursors().Get(ctx, tenantA, "t1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cursor.NextUID != 101000 {
		t.Errorf("cursor = %d after a refused lowering, want 101000", cursor.NextUID)
	}

	// Setting it to exactly where it is stays legal: the rule is that it
	// may not go DOWN, not that every write must move it.
	if _, err := st.Pool().Exec(ctx, `
		UPDATE uid_allocation_cursors
		SET next_uid = next_uid
		WHERE tenant = $1 AND target_id = 't1'`, tenantA); err != nil {
		t.Errorf("a no-op write was refused: %v", err)
	}

	// The range bound is the database's too: a cursor past range_end is
	// refused by the CHECK constraint rather than by whoever remembered.
	if _, err := st.Pool().Exec(ctx, `
		UPDATE uid_allocation_cursors
		SET next_uid = range_end + 1
		WHERE tenant = $1 AND target_id = 't1'`, tenantA); err == nil {
		t.Error("a raw UPDATE pushed the cursor past its range")
	}
}

// `observed_floor` is a target's word relayed by the proxy, so it may only
// ever RAISE the cursor (PLAN §4). A stale observation is ignored rather than
// obeyed, and it is not an error: it is simply out of date.
func TestRaiseFloorOnlyEverRaises(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	storetest.Seed(t, st, storetest.Fixture{
		Tenant:     tenantA,
		UIDCursors: []storetest.UIDCursorFixture{{TargetID: "t1", First: 100000, RangeEnd: 200000}},
	})

	raised, err := st.UIDCursors().RaiseFloor(ctx, tenantA, "t1", 150000)
	if err != nil {
		t.Fatalf("RaiseFloor: %v", err)
	}
	if raised.NextUID != 150000 {
		t.Errorf("RaiseFloor(150000) left the cursor at %d", raised.NextUID)
	}

	stale, err := st.UIDCursors().RaiseFloor(ctx, tenantA, "t1", 120000)
	if err != nil {
		t.Fatalf("RaiseFloor with a stale floor: %v", err)
	}
	if stale.NextUID != 150000 {
		t.Errorf("a stale floor lowered the cursor to %d, want 150000", stale.NextUID)
	}

	// A block granted after a raise starts above the floor, never inside
	// the range the target says is in use.
	block, err := st.UIDCursors().Advance(ctx, tenantA, "t1", 10)
	if err != nil {
		t.Fatalf("Advance after RaiseFloor: %v", err)
	}
	if block.From < 150000 {
		t.Errorf("block starts at %d, below the reported floor of 150000", block.From)
	}

	// A floor past the top of the range exhausts that target rather than
	// failing the CHECK constraint: root on a target can burn its own
	// range, which is the stated exposure, and it must not also be an
	// outage.
	clamped, err := st.UIDCursors().RaiseFloor(ctx, tenantA, "t1", 999999)
	if err != nil {
		t.Fatalf("RaiseFloor past range_end: %v", err)
	}
	if clamped.NextUID != clamped.RangeEnd {
		t.Errorf("cursor = %d, want it clamped to range_end %d", clamped.NextUID, clamped.RangeEnd)
	}
	if _, err := st.UIDCursors().Advance(ctx, tenantA, "t1", 1); !store.IsExhausted(err) {
		t.Errorf("Advance on a burnt range: %v, want ErrExhausted", err)
	}

	if _, err := st.UIDCursors().RaiseFloor(ctx, tenantA, "no-such-target", 1); !store.IsNotFound(err) {
		t.Errorf("RaiseFloor for an unknown target: %v, want ErrNotFound", err)
	}
}

// Advance takes a row lock, which needs a transaction. Called from inside
// InTx — which 0007 will do, recording the lease alongside the block — it must
// use the caller's transaction rather than opening a second one on a second
// connection and deadlocking against the first.
func TestAdvanceWorksInsideACallersTransaction(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	storetest.Seed(t, st, storetest.Fixture{
		Tenant:     tenantA,
		UIDCursors: []storetest.UIDCursorFixture{{TargetID: "t1", First: 100000, RangeEnd: 200000}},
	})

	var block store.UIDBlock
	err := st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		var err error
		block, err = tx.UIDCursors().Advance(ctx, tenantA, "t1", 10)
		return err
	})
	if err != nil {
		t.Fatalf("Advance inside InTx: %v", err)
	}
	if block.From != 100000 {
		t.Errorf("block = [%d,%d), want it to start at 100000", block.From, block.To)
	}

	// The rollback case matters as much: a lease that was not recorded must
	// not have consumed uids either.
	rollbackErr := st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if _, err := tx.UIDCursors().Advance(ctx, tenantA, "t1", 10); err != nil {
			return err
		}
		return store.ErrConflict
	})
	if rollbackErr == nil {
		t.Fatal("InTx: want the callback's error")
	}
	cursor, err := st.UIDCursors().Get(ctx, tenantA, "t1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cursor.NextUID != 100010 {
		t.Errorf("cursor = %d after a rolled-back Advance, want 100010", cursor.NextUID)
	}
}

// There is no release path, and its absence is the design (PLAN §4). If one is
// ever added, this test is where the argument for it has to be made.
func TestUIDCursorRepositoryHasNoReleasePath(t *testing.T) {
	t.Parallel()

	iface := reflect.TypeOf((*store.UIDCursorRepository)(nil)).Elem()
	var declared []string
	for i := range iface.NumMethod() {
		declared = append(declared, iface.Method(i).Name)
	}

	for _, forbidden := range []string{"Release", "Reclaim", "Free", "Return", "Recycle"} {
		if slices.Contains(declared, forbidden) {
			t.Errorf("UIDCursorRepository declares %s: a granted block is gone — used, "+
				"abandoned or expired alike — so the only durable state is the integer (PLAN §4)",
				forbidden)
		}
	}
}
