// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// A requester's own door into a registered approval workflow (0016): M10's own
// example — "a developer asks for access for themselves" — entered without the
// power the workflow exists to govern, and shut while nothing would decide it.

const bob = "bob@example.com"

// requesterAlice is alice asking for herself: a session with a person behind it.
var requesterAlice = access.Actor{Principal: "p-alice", Subject: alice, DisplayName: "Alice", CorrelationID: "corr-a"}

// ownDBAFor30m is dbaFor30m without a holder: the holder is whoever asks.
func ownDBAFor30m() access.Spec {
	spec := dbaFor30m()
	spec.Subject = ""
	return spec
}

// THE POINT OF THE PHASE, through the new door. Alice asks; the workflow
// approves; the grant is hers, made by her, decided by the workflow — and it is
// 0012's lifecycle from there: allowed until the window closes, denied after,
// with nothing having run.
func TestARequesterAsksForThemselvesAndTheWorkflowDecides(t *testing.T) {
	wf := &fakeWorkflow{decide: pending}
	h := newHarness(t, withWorkflow(wf))

	if out := h.authorize("db01.example.com", "sess-0"); out.Deny == nil {
		t.Fatal("prod allowed before any grant")
	}
	out, err := h.grants.RequestOwn(t.Context(), tenant, requesterAlice, ownDBAFor30m())
	if err != nil {
		t.Fatalf("request own: %v", err)
	}
	if out.Grant != nil || out.Request == nil || out.Request.State != store.GrantRequestPending ||
		out.Request.SubjectID != alice || out.Request.RequestedBy.Subject != alice {
		t.Fatalf("request own = grant %+v, request %+v; want alice's pending request", out.Grant, out.Request)
	}

	got := wf.submitted()[0]
	if got.Subject.ID != alice || got.RequestedBy.ID != alice {
		t.Errorf("the workflow saw subject %q requested by %q; from the self door they are one person",
			got.Subject.ID, got.RequestedBy.ID)
	}

	wf.setStatus(out.Request.WorkflowRef, ext.GrantDecision{
		WorkflowRef: out.Request.WorkflowRef, Outcome: ext.GrantApproved,
		Approvals: []ext.GrantApproval{{Approver: ext.Subject{ID: "carol@example.com"}, Approved: true}},
	})
	if err := h.grants.Poll(t.Context(), tenant); err != nil {
		t.Fatalf("poll: %v", err)
	}
	rows, err := h.store.Grants().List(t.Context(), tenant, store.GrantQuery{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("grants after approval: %+v, %v", rows, err)
	}
	g := rows[0]
	if g.SubjectID != alice || g.CreatedBy.Subject != alice || g.CreatedBy.Principal != "p-alice" ||
		g.Origin != store.GrantOriginWorkflow || g.RequestID != out.Request.ID {
		t.Errorf("grant = %+v, want alice's, created by alice, origin workflow", g)
	}
	if allow := h.authorize("db01.example.com", "sess-1"); allow.Deny != nil {
		t.Errorf("the approved grant did not allow: %s", allow.Deny.Reason)
	}
	h.advance(31 * time.Minute)
	if deny := h.authorize("db01.example.com", "sess-2"); deny.Deny == nil {
		t.Error("the grant still allowed after its window closed")
	}
}

// From the administrator's door the workflow sees two people; from the self
// door, one. That comparison is how a workflow tells the doors apart.
func TestTheWorkflowTellsTheDoorsApartByComparingSubjectAndRequester(t *testing.T) {
	wf := &fakeWorkflow{decide: pending}
	h := newHarness(t, withWorkflow(wf))
	if _, err := h.grants.Create(t.Context(), tenant, operator, dbaFor30m()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := h.grants.RequestOwn(t.Context(), tenant, requesterAlice, ownDBAFor30m()); err != nil {
		t.Fatalf("request own: %v", err)
	}
	subs := wf.submitted()
	if len(subs) != 2 {
		t.Fatalf("%d submissions, want 2", len(subs))
	}
	if subs[0].Subject.ID != alice || subs[0].RequestedBy.ID != admin {
		t.Errorf("administrator's door: subject %q, requested by %q", subs[0].Subject.ID, subs[0].RequestedBy.ID)
	}
	if subs[1].Subject.ID != alice || subs[1].RequestedBy.ID != alice {
		t.Errorf("self door: subject %q, requested by %q", subs[1].Subject.ID, subs[1].RequestedBy.ID)
	}
}

// With nothing registered to decide it, a request of one's own is a self-grant,
// so it is refused — before anything is stored, audited or announced — and the
// administrator's direct create is exactly what it was.
func TestWithNoWorkflowARequestOfOnesOwnIsRefusedAndNothingIsStored(t *testing.T) {
	h := newHarness(t)

	_, err := h.grants.RequestOwn(t.Context(), tenant, requesterAlice, ownDBAFor30m())
	if !errors.Is(err, access.ErrNoWorkflow) {
		t.Fatalf("request own with no workflow: %v, want ErrNoWorkflow", err)
	}
	if rows, err := h.store.GrantRequests().ListForSubject(t.Context(), tenant, alice, store.GrantRequestQuery{}); err != nil || len(rows) != 0 {
		t.Errorf("request rows after a refusal: %+v, %v", rows, err)
	}
	if rows, _ := h.store.Grants().List(t.Context(), tenant, store.GrantQuery{}); len(rows) != 0 {
		t.Errorf("grants after a refusal: %+v", rows)
	}
	if recs, err := h.store.Audit().Query(t.Context(), tenant, store.AuditQuery{Limit: 100}); err != nil || len(recs) != 0 {
		t.Errorf("audit records after a refusal: %d, %v", len(recs), err)
	}
	if len(h.notes.all()) != 0 {
		t.Errorf("a refusal was announced: %+v", h.notes.all())
	}

	g := h.create(dbaFor30m())
	if g.Origin != store.GrantOriginManual || g.CreatedBy.Subject != admin {
		t.Errorf("the administrator's direct create changed: %+v", g)
	}
}

// The holder is the caller and nothing else. A spec naming a holder, or an
// actor with nobody behind it, is a wiring fault — an outage, not a refusal the
// caller could fix — because the transport never sends either.
func TestARequestOfOnesOwnNamesNoOtherHolder(t *testing.T) {
	h := newHarness(t, withWorkflow(&fakeWorkflow{}))

	named := ownDBAFor30m()
	named.Subject = bob
	_, err := h.grants.RequestOwn(t.Context(), tenant, requesterAlice, named)
	var ve *access.ValidationError
	if err == nil || errors.As(err, &ve) || errors.Is(err, access.ErrNoWorkflow) {
		t.Errorf("a self request naming a holder: %v, want a wiring fault", err)
	}

	token := access.Actor{Principal: "tok-1", CorrelationID: "corr-t"}
	if _, err := h.grants.RequestOwn(t.Context(), tenant, token, ownDBAFor30m()); err == nil || errors.As(err, &ve) {
		t.Errorf("a self request from a machine token: %v, want a wiring fault", err)
	}
	if _, err := h.grants.OwnRequests(t.Context(), tenant, "", store.GrantRequestQuery{}); err == nil {
		t.Error("a list of nobody's own requests was answered")
	}
	if rows, _ := h.store.Grants().List(t.Context(), tenant, store.GrantQuery{}); len(rows) != 0 {
		t.Errorf("a refused self request created %d grants", len(rows))
	}
}

// "Own" means holder. Alice reads and withdraws the requests that would grant
// to her — including one a grant administrator raised for her — and bob's is
// not found, never found and refused.
func TestARequesterReadsAndWithdrawsOnlyTheirOwnRequests(t *testing.T) {
	wf := &fakeWorkflow{decide: pending}
	h := newHarness(t, withWorkflow(wf))
	storetest.Seed(t, h.store, storetest.Fixture{
		Tenant: tenant, Subjects: []store.Subject{storetest.Subject(bob)},
	})

	mine, err := h.grants.RequestOwn(t.Context(), tenant, requesterAlice, ownDBAFor30m())
	if err != nil {
		t.Fatalf("request own: %v", err)
	}
	onMyBehalf, err := h.grants.Create(t.Context(), tenant, operator, dbaFor30m())
	if err != nil {
		t.Fatalf("create for alice: %v", err)
	}
	forBob := dbaFor30m()
	forBob.Subject = bob
	bobs, err := h.grants.Create(t.Context(), tenant, operator, forBob)
	if err != nil {
		t.Fatalf("create for bob: %v", err)
	}

	list, err := h.grants.OwnRequests(t.Context(), tenant, alice, store.GrantRequestQuery{})
	if err != nil {
		t.Fatalf("own requests: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("alice's list has %d requests, want her own two", len(list))
	}
	for _, r := range list {
		if r.ID == bobs.Request.ID || r.SubjectID != alice {
			t.Errorf("alice's list holds %s for %s", r.ID, r.SubjectID)
		}
	}

	if _, err := h.grants.OwnRequest(t.Context(), tenant, alice, bobs.Request.ID); !store.IsNotFound(err) {
		t.Errorf("alice read bob's request: %v, want not found", err)
	}
	if _, err := h.grants.CancelOwn(t.Context(), tenant, requesterAlice, bobs.Request.ID, "mine now"); !store.IsNotFound(err) {
		t.Errorf("alice cancelled bob's request: %v, want not found", err)
	}
	if got, _ := h.grants.Request(t.Context(), tenant, bobs.Request.ID); got.Request.State != store.GrantRequestPending {
		t.Errorf("bob's request is %s after alice tried to cancel it", got.Request.State)
	}

	read, err := h.grants.OwnRequest(t.Context(), tenant, alice, onMyBehalf.Request.ID)
	if err != nil || read.Request.ID != onMyBehalf.Request.ID {
		t.Errorf("alice could not read the request raised on her behalf: %+v, %v", read.Request, err)
	}
	cancelled, err := h.grants.CancelOwn(t.Context(), tenant, requesterAlice, mine.Request.ID, "found another way in")
	if err != nil || cancelled.Request.State != store.GrantRequestCancelled {
		t.Fatalf("cancel own: %+v, %v", cancelled.Request, err)
	}
	if !slices.Contains(wf.cancelled(), mine.Request.WorkflowRef) {
		t.Error("the workflow was not told the requester withdrew")
	}
	closed := h.controlRecords(access.EventGrantRequestClosed)
	if len(closed) != 1 || closed[0].Attributes[audit.AttrActorSubject] != alice ||
		closed[0].Attributes[audit.AttrPrincipalID] != "p-alice" {
		t.Errorf("the withdrawal is not audited with the requester as the actor: %+v", closed)
	}
	if _, err := h.grants.CancelOwn(t.Context(), tenant, requesterAlice, mine.Request.ID, ""); !errors.Is(err, access.ErrNotPending) {
		t.Errorf("cancelling a decided request: %v, want ErrNotPending", err)
	}
}

// ---------------------------------------------------------------------------
// Break-glass reaches the workflow (M7)
// ---------------------------------------------------------------------------

// The flag on the request describes who RAISED it, it is copied from the
// request Control stored, and so it survives a resubmission whose actor is
// nobody.
func TestABreakGlassRequestReachesTheWorkflowFlagged(t *testing.T) {
	breakGlassAdmin := operator
	breakGlassAdmin.BreakGlass = true
	breakGlassAlice := requesterAlice
	breakGlassAlice.BreakGlass = true

	for name, raise := range map[string]func(*harness) error{
		"administrator's create": func(h *harness) error {
			_, err := h.grants.Create(h.t.Context(), tenant, breakGlassAdmin, dbaFor30m())
			return err
		},
		"self door": func(h *harness) error {
			_, err := h.grants.RequestOwn(h.t.Context(), tenant, breakGlassAlice, ownDBAFor30m())
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			wf := &fakeWorkflow{decide: pending}
			h := newHarness(t, withWorkflow(wf))
			if err := raise(h); err != nil {
				t.Fatalf("raise: %v", err)
			}
			if subs := wf.submitted(); len(subs) != 1 || !subs[0].BreakGlass {
				t.Errorf("the workflow saw %+v, want one request flagged break-glass", subs)
			}
		})
	}

	t.Run("resubmission after an outage", func(t *testing.T) {
		wf := &fakeWorkflow{decide: pending}
		wf.failSubmits(1, ext.Errorf(ext.PointGrantWorkflow, "example/workflow", "GrantWorkflow.Submit",
			ext.KindUnavailable, "connection refused"))
		h := newHarness(t, withWorkflow(wf))
		if _, err := h.grants.RequestOwn(t.Context(), tenant, breakGlassAlice, ownDBAFor30m()); err != nil {
			t.Fatalf("request own: %v", err)
		}
		if err := h.grants.Poll(t.Context(), tenant); err != nil {
			t.Fatalf("poll: %v", err)
		}
		subs := wf.submitted()
		if len(subs) != 2 || subs[0].RequestID != subs[1].RequestID {
			t.Fatalf("submissions = %+v, want one request submitted twice", subs)
		}
		if !subs[1].BreakGlass {
			t.Error("the poller's resubmission lost the break-glass flag")
		}
	})
}

// Never inferred: a credential not minted on the break-glass path arrives
// unflagged, however its request was raised.
func TestARequestNotRaisedOnTheBreakGlassPathArrivesUnflagged(t *testing.T) {
	wf := &fakeWorkflow{decide: pending}
	h := newHarness(t, withWorkflow(wf))
	if _, err := h.grants.Create(t.Context(), tenant, operator, dbaFor30m()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := h.grants.RequestOwn(t.Context(), tenant, requesterAlice, ownDBAFor30m()); err != nil {
		t.Fatalf("request own: %v", err)
	}
	for _, sub := range wf.submitted() {
		if sub.BreakGlass {
			t.Errorf("request %s arrived flagged break-glass", sub.RequestID)
		}
	}
}
