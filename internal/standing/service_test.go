// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package standing_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/standing"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

const tenant = store.Tenant("acme")

// stream is a revocation publisher that remembers what it was asked and can be
// told to refuse.
type stream struct {
	mu    sync.Mutex
	calls []string
	fail  error
}

func (s *stream) Kill(_ context.Context, _ store.Tenant, _ revoke.Audience, k revoke.Kill) (revoke.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return revoke.Receipt{}, s.fail
	}
	s.calls = append(s.calls, "kill:"+k.Subject+":"+k.Reason)
	return revoke.Receipt{}, nil
}

func (s *stream) Invalidate(_ context.Context, _ store.Tenant, _ revoke.Audience, inv revoke.Invalidation) (revoke.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return revoke.Receipt{}, s.fail
	}
	name := "invalidate:" + inv.Subject
	if inv.All {
		name = "invalidate:all"
	}
	s.calls = append(s.calls, name)
	return revoke.Receipt{}, nil
}

func (s *stream) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// recorder keeps the operator events it was asked to write, in the
// transaction it was handed.
type recorder struct {
	mu     sync.Mutex
	events []audit.OperatorAct
	fail   error
}

func (r *recorder) OperatorEvent(_ context.Context, tx *store.Store, _ store.Tenant, act audit.OperatorAct) error {
	if tx == nil {
		return errors.New("a standing denial's record was written outside the transaction that placed it")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.events = append(r.events, act)
	return nil
}

func (r *recorder) count(event string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.Event == event {
			n++
		}
	}
	return n
}

type fixture struct {
	st  *store.Store
	svc *standing.Service
	out *stream
	rec *recorder
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := storetest.New(t)
	storetest.Seed(t, st, storetest.Fixture{
		Tenant: tenant,
		Subjects: []store.Subject{
			storetest.Subject("alice", "sre"),
			{ID: "root", Source: "local", BreakGlass: true},
		},
		Targets: []store.Target{{ID: "t-db", Hostname: "db.example.com", Zone: "edge"}},
	})
	f := &fixture{st: st, out: &stream{}, rec: &recorder{}}
	var err error
	f.svc, err = standing.New(standing.Options{
		Store: st, Recorder: f.rec, Revoker: f.out, Settle: time.Millisecond, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

var operator = standing.Actor{Principal: "p-operator", Subject: "oscar"}

func TestServiceRefusesToBuildWithoutWhatItNeeds(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	for name, o := range map[string]standing.Options{
		"no store":    {Recorder: &recorder{}, Revoker: &stream{}},
		"no recorder": {Store: st, Revoker: &stream{}},
		"no revoker":  {Store: st, Recorder: &recorder{}},
	} {
		if _, err := standing.New(o); err == nil {
			t.Errorf("%s: New succeeded", name)
		}
	}
}

// A lock is recorded once, when it is placed, and publishes the invalidation
// before the kill, twice: the second pass closes the race with an authorize
// that read the world a moment before the commit.
func TestALockPublishesBothHalvesTwiceAndIsRecordedOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	res, err := f.svc.Lock(t.Context(), tenant, operator, standing.LockRequest{
		SubjectID: "alice", ReasonCode: "compromised", Reason: "locked for review",
	})
	if err != nil || !res.Placed {
		t.Fatalf("Lock = %+v, %v", res, err)
	}
	want := []string{"invalidate:alice", "kill:alice:locked for review", "invalidate:alice", "kill:alice:locked for review"}
	if got := f.out.seen(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Errorf("published %v, want %v: the invalidation first, then the kill, and again after the settle", got, want)
	}
	if f.rec.count(audit.EventSubjectLocked) != 1 {
		t.Errorf("%d lock records", f.rec.count(audit.EventSubjectLocked))
	}

	// Asking again: no record, no settle pass, but the kill is sent again.
	res, err = f.svc.Lock(t.Context(), tenant, standing.Actor{Principal: "p-other"}, standing.LockRequest{
		SubjectID: "alice", Reason: "a different reason",
	})
	if err != nil || res.Placed || res.Lock.Reason != "locked for review" || res.Lock.LockedBy.Principal != "p-operator" {
		t.Fatalf("a second Lock = %+v, %v, want the first lock standing", res, err)
	}
	if got := f.out.seen(); len(got) != 6 {
		t.Errorf("a repeated lock published %v after the first four, want one pass of two", got[4:])
	}
	if f.rec.count(audit.EventSubjectLocked) != 1 {
		t.Errorf("a repeated lock was recorded again")
	}
}

func TestALockWhosePublicationFailsStandsAndSaysSo(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.out.fail = errors.New("no stream")

	res, err := f.svc.Lock(t.Context(), tenant, operator, standing.LockRequest{SubjectID: "alice", Reason: "r"})
	if !standing.IsUndelivered(err) || !res.Placed {
		t.Fatalf("Lock = %+v, %v; want a placed lock and ErrUndelivered", res, err)
	}
	if _, err := f.st.SubjectLocks().Get(t.Context(), tenant, "alice"); err != nil {
		t.Errorf("the lock was not kept: %v", err)
	}
	// Asking again after the stream is back is what delivers it.
	f.out.fail = nil
	if _, err := f.svc.Lock(t.Context(), tenant, operator, standing.LockRequest{SubjectID: "alice", Reason: "r"}); err != nil {
		t.Fatalf("Lock again: %v", err)
	}
	if got := f.out.seen(); len(got) != 2 {
		t.Errorf("published %v, want the one pass the retry owed", got)
	}
}

// A record that cannot be written keeps the lock from being placed: the row and
// its record commit together or not at all.
func TestALockWhoseRecordCannotBeWrittenIsNotPlaced(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.rec.fail = errors.New("the chain is down")

	if _, err := f.svc.Lock(t.Context(), tenant, operator, standing.LockRequest{SubjectID: "alice", Reason: "r"}); err == nil {
		t.Fatal("Lock succeeded with its record refused")
	}
	if _, err := f.st.SubjectLocks().Get(t.Context(), tenant, "alice"); !store.IsNotFound(err) {
		t.Errorf("a lock was stored without its record: %v", err)
	}
	if len(f.out.seen()) != 0 {
		t.Errorf("a lock that was not placed published %v", f.out.seen())
	}
}

func TestALockNeedsARowAndRefusesBreakGlassOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	_, err := f.svc.Lock(t.Context(), tenant, operator, standing.LockRequest{SubjectID: "ghost", Reason: "r"})
	if !store.IsNotFound(err) {
		t.Errorf("locking a subject with no row: %v, want not found", err)
	}
	_, err = f.svc.Lock(t.Context(), tenant, operator, standing.LockRequest{SubjectID: "root", Reason: "r", RefuseBreakGlass: true})
	if !errors.Is(err, standing.ErrBreakGlassSubject) {
		t.Errorf("an action's lock of a break-glass subject: %v, want ErrBreakGlassSubject", err)
	}
	if _, err := f.st.SubjectLocks().Get(t.Context(), tenant, "root"); !store.IsNotFound(err) {
		t.Error("a refused lock was stored")
	}
	if _, err := f.svc.Lock(t.Context(), tenant, operator, standing.LockRequest{SubjectID: "root", Reason: "r"}); err != nil {
		t.Errorf("an operator's lock of a break-glass subject: %v", err)
	}
	// A lock that already stands is left alone even for a request that would
	// refuse a new one: refusing it would help nobody.
	if res, err := f.svc.Lock(t.Context(), tenant, standing.Actor{Principal: "p-action"}, standing.LockRequest{
		SubjectID: "root", Reason: "r", RefuseBreakGlass: true,
	}); err != nil || res.Placed {
		t.Errorf("a repeated lock with RefuseBreakGlass = %+v, %v", res, err)
	}
}

func TestARequestThatCannotBeShownIsRefusedWithTheFieldAtFault(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	for name, tc := range map[string]struct {
		req   standing.LockRequest
		field string
	}{
		"no subject":              {standing.LockRequest{Reason: "r"}, "subject"},
		"no reason":               {standing.LockRequest{SubjectID: "alice"}, "reason"},
		"a reason with a newline": {standing.LockRequest{SubjectID: "alice", Reason: "a\nb"}, "reason"},
		"a reason too long":       {standing.LockRequest{SubjectID: "alice", Reason: string(make([]byte, 513))}, "reason"},
		"a bad code":              {standing.LockRequest{SubjectID: "alice", Reason: "r", ReasonCode: "no\ngood"}, "reason_code"},
		"a bad reference":         {standing.LockRequest{SubjectID: "alice", Reason: "r", ExternalRef: "a\nb"}, "external_ref"},
	} {
		_, err := f.svc.Lock(t.Context(), tenant, operator, tc.req)
		var ve *access.ValidationError
		if !errors.As(err, &ve) || ve.Field != tc.field {
			t.Errorf("%s: err = %v, want a validation error on %q", name, err, tc.field)
		}
	}
	if _, err := f.svc.Lock(t.Context(), tenant, standing.Actor{}, standing.LockRequest{SubjectID: "alice", Reason: "r"}); err == nil {
		t.Error("a lock with no credential behind it was placed")
	}
}

func TestAQuarantineDropsEveryCachedDecisionAndKillsNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	res, err := f.svc.Quarantine(t.Context(), tenant, operator, standing.QuarantineRequest{TargetID: "t-db", ReasonCode: "compromised"})
	if err != nil || !res.Placed {
		t.Fatalf("Quarantine = %+v, %v", res, err)
	}
	for _, c := range f.out.seen() {
		if c != "invalidate:all" {
			t.Errorf("a quarantine published %q", c)
		}
	}
	if len(f.out.seen()) != 2 || f.rec.count(audit.EventTargetQuarantined) != 1 {
		t.Errorf("published %v and recorded %d times", f.out.seen(), f.rec.count(audit.EventTargetQuarantined))
	}
	if _, err := f.svc.Quarantine(t.Context(), tenant, operator, standing.QuarantineRequest{TargetID: "ghost"}); !store.IsNotFound(err) {
		t.Errorf("quarantining a target with no row: %v, want not found", err)
	}

	lifted, err := f.svc.Release(t.Context(), tenant, operator, standing.LiftRequest{ID: "t-db", ReasonCode: "cleared"})
	if err != nil || !lifted.Lifted {
		t.Fatalf("Release = %+v, %v", lifted, err)
	}
	if len(f.out.seen()) != 2 {
		t.Errorf("a release published %v: a deny is never cached as an allow, so lifting owes nothing", f.out.seen()[2:])
	}
	if again, err := f.svc.Release(t.Context(), tenant, operator, standing.LiftRequest{ID: "t-db"}); err != nil || again.Lifted {
		t.Errorf("a second Release = %+v, %v", again, err)
	}
	if f.rec.count(audit.EventTargetReleased) != 1 {
		t.Errorf("%d release records, want one", f.rec.count(audit.EventTargetReleased))
	}
}

// A denial belongs to its tenant: the same ids in another tenant are neither
// locked by it nor lifted by it.
func TestADenialIsTheTenantsOwn(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const other = store.Tenant("globex")
	storetest.Seed(t, f.st, storetest.Fixture{
		Tenant:   other,
		Subjects: []store.Subject{storetest.Subject("alice", "sre")},
		Targets:  []store.Target{{ID: "t-db", Hostname: "db.example.com", Zone: "edge"}},
	})

	if _, err := f.svc.Lock(t.Context(), tenant, operator, standing.LockRequest{SubjectID: "alice", Reason: "r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Quarantine(t.Context(), tenant, operator, standing.QuarantineRequest{TargetID: "t-db"}); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.st.Subjects().Standing(t.Context(), other, "alice"); st.Lock != nil {
		t.Error("tenant globex's alice is locked by tenant acme's lock")
	}
	if got, err := f.svc.Unlock(t.Context(), other, operator, standing.LiftRequest{ID: "alice"}); err != nil || got.Lifted {
		t.Errorf("tenant globex lifted tenant acme's lock: %+v, %v", got, err)
	}
	if got, err := f.svc.Release(t.Context(), other, operator, standing.LiftRequest{ID: "t-db"}); err != nil || got.Lifted {
		t.Errorf("tenant globex released tenant acme's quarantine: %+v, %v", got, err)
	}
	if _, err := f.st.SubjectLocks().Get(t.Context(), tenant, "alice"); err != nil {
		t.Errorf("tenant acme's lock was lifted: %v", err)
	}
}
