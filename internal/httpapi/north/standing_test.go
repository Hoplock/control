// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/standing"
	"github.com/hoplock/control/internal/store"
)

// The subject lock and the target quarantine over the wire (0039, M28): the
// four routes, what they publish, what they record, and how the inventory shows
// them.

// watcher is one proxy's event stream on tenant-a, collecting what a proxy
// holding it would have been told. This fixture's bus is the in-process
// broker, which keeps no log, so a test subscribes before it acts.
type watcher struct {
	mu     sync.Mutex
	events []contract.RevocationEvent
}

func (f *serverFixture) watch(t *testing.T) *watcher {
	t.Helper()
	w := &watcher{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = f.lifecycle.bus.Subscribe(ctx, "tenant-a", "proxy-watch", "", func(ev *contract.RevocationEvent) error {
			if ev.Type == contract.EventTypeHeartbeat {
				return nil
			}
			w.mu.Lock()
			w.events = append(w.events, *ev)
			w.mu.Unlock()
			return nil
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		live, err := f.lifecycle.bus.LiveSubscriptions(ctx, "tenant-a")
		if err == nil {
			if _, ok := live["proxy-watch"]; ok {
				return w
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the watcher's subscription never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// got is what the watcher has seen so far.
func (w *watcher) got(t *testing.T) []contract.RevocationEvent {
	t.Helper()
	// Delivery to a subscriber is asynchronous; a publication has been
	// answered before its subscriber has been written to.
	time.Sleep(50 * time.Millisecond)
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.events)
}

func (f *serverFixture) putSubject(t *testing.T, token, id string) {
	t.Helper()
	mustStatus(t, f.send(t, "PUT", api+"/subjects/"+id, token, map[string]any{"groups": []string{"sre"}}), http.StatusOK)
}

func (f *serverFixture) putTarget(t *testing.T, token, id, host string) {
	t.Helper()
	mustStatus(t, f.send(t, "PUT", api+"/targets/"+id, token, map[string]any{"hostname": host, "zone": "edge"}), http.StatusOK)
}

type lockResponse struct {
	Subject  string `json:"subject"`
	Locked   bool   `json:"locked"`
	Placed   bool   `json:"placed"`
	Unlocked bool   `json:"unlocked"`
	Lock     *struct {
		Subject    string `json:"subject"`
		ReasonCode string `json:"reason_code"`
		Reason     string `json:"reason"`
		LockedBy   struct {
			PrincipalID string `json:"principal_id"`
		} `json:"locked_by"`
	} `json:"lock"`
}

func TestAnOperatorLocksAndUnlocksASubject(t *testing.T) {
	f := newServer(t)
	desk := f.roleToken(t, identity.RoleGrantAdmin)
	admin := f.roleToken(t, identity.RoleAdmin)
	f.putSubject(t, admin, "mallory@example.com")
	stream := f.watch(t)

	// Placing a lock answers what stands, records it once and publishes both
	// halves of what it owes: the subject's cached decisions dropped, their
	// sessions ended with the words the holder is shown.
	resp := f.send(t, "POST", api+"/subjects/mallory@example.com/lock", desk,
		map[string]any{"reason_code": "compromised", "reason": "Your account is suspended while it is investigated."})
	mustStatus(t, resp, http.StatusOK)
	var placed lockResponse
	decodeInto(t, resp, &placed)
	if !placed.Locked || !placed.Placed || placed.Lock == nil || placed.Lock.ReasonCode != "compromised" ||
		placed.Lock.Reason != "Your account is suspended while it is investigated." || placed.Lock.LockedBy.PrincipalID == "" {
		t.Fatalf("lock answered %+v, want the lock that now stands", placed)
	}
	var kills, invalidations int
	for _, ev := range stream.got(t) {
		switch {
		case ev.SessionKill != nil && ev.SessionKill.Subject == "mallory@example.com":
			kills++
			if ev.SessionKill.Reason != "Your account is suspended while it is investigated." {
				t.Errorf("the kill showed the holder %q", ev.SessionKill.Reason)
			}
		case ev.CacheInvalidate != nil && ev.CacheInvalidate.Subject == "mallory@example.com":
			invalidations++
		}
	}
	// Placed once: the first pass and the settle pass each send both.
	if kills != 2 || invalidations != 2 {
		t.Errorf("placing the lock published %d kills and %d invalidations for the subject, want 2 and 2 (two passes)", kills, invalidations)
	}
	if recs := f.operatorRecords(t, audit.EventSubjectLocked); len(recs) != 1 ||
		recs[0].Attributes[audit.AttrPrincipalID] == "" || recs[0].Subject != "mallory@example.com" ||
		recs[0].Attributes["operator.reason_code"] != "compromised" {
		t.Errorf("the lock was recorded as %+v, want one record naming the operator and the subject", recs)
	}

	// Asking again changes nothing and says so: a 200, not placed, the first
	// lock's facts, no second record. It sends the kill again, because an
	// undelivered one is only ever repaired by asking.
	resp = f.send(t, "POST", api+"/subjects/mallory@example.com/lock", desk, map[string]any{"reason_code": "other"})
	mustStatus(t, resp, http.StatusOK)
	var again lockResponse
	decodeInto(t, resp, &again)
	if !again.Locked || again.Placed || again.Lock == nil || again.Lock.ReasonCode != "compromised" {
		t.Errorf("a second lock answered %+v, want the first lock standing and Placed false", again)
	}
	if recs := f.operatorRecords(t, audit.EventSubjectLocked); len(recs) != 1 {
		t.Errorf("a lock already standing wrote a second record: %d", len(recs))
	}

	// The inventory shows it, under the permission it already has.
	auditor := f.roleToken(t, identity.RoleAuditor)
	resp = f.send(t, "GET", api+"/subjects/mallory@example.com", auditor, nil)
	mustStatus(t, resp, http.StatusOK)
	var one struct {
		ID     string `json:"id"`
		Locked bool   `json:"locked"`
		Lock   *struct {
			ReasonCode string `json:"reason_code"`
		} `json:"lock"`
	}
	decodeInto(t, resp, &one)
	if !one.Locked || one.Lock == nil || one.Lock.ReasonCode != "compromised" {
		t.Errorf("GET subject = %+v, want it locked with the lock's facts", one)
	}
	resp = f.send(t, "GET", api+"/subjects", auditor, nil)
	mustStatus(t, resp, http.StatusOK)
	var list struct {
		Subjects []struct {
			ID     string `json:"id"`
			Locked bool   `json:"locked"`
		} `json:"subjects"`
	}
	decodeInto(t, resp, &list)
	listed := false
	for _, s := range list.Subjects {
		if s.ID == "mallory@example.com" {
			listed = true
			if !s.Locked {
				t.Errorf("the subject list shows mallory unlocked")
			}
		}
	}
	if !listed {
		t.Fatalf("the subject list %+v does not include mallory", list.Subjects)
	}

	// Writing, deleting and re-creating the subject leaves the lock standing.
	f.putSubject(t, admin, "mallory@example.com")
	mustStatus(t, f.send(t, "DELETE", api+"/subjects/mallory@example.com", admin, nil), http.StatusNoContent)
	resp = f.send(t, "GET", api+"/subjects/mallory@example.com", auditor, nil)
	mustStatus(t, resp, http.StatusOK)
	var orphan struct {
		Locked        bool `json:"locked"`
		RecordMissing bool `json:"record_missing"`
	}
	decodeInto(t, resp, &orphan)
	if !orphan.Locked || !orphan.RecordMissing {
		t.Errorf("a deleted subject's lock reads %+v, want it still standing and marked as having no row", orphan)
	}
	f.putSubject(t, admin, "mallory@example.com")

	// Unlocking lifts it, records it once, and says what it lifted.
	resp = f.send(t, "POST", api+"/subjects/mallory@example.com/unlock", desk, map[string]any{"reason_code": "cleared"})
	mustStatus(t, resp, http.StatusOK)
	var lifted lockResponse
	decodeInto(t, resp, &lifted)
	if !lifted.Unlocked || lifted.Lock == nil || lifted.Lock.ReasonCode != "compromised" {
		t.Errorf("unlock answered %+v, want the lock it lifted", lifted)
	}
	before := len(stream.got(t))
	if recs := f.operatorRecords(t, audit.EventSubjectUnlocked); len(recs) != 1 {
		t.Errorf("the unlock was recorded %d times, want once", len(recs))
	}
	// No lock to lift is a 200 that says so, writes nothing and publishes
	// nothing: a deny is never cached as an allow, so lifting owes the stream
	// nothing.
	resp = f.send(t, "POST", api+"/subjects/mallory@example.com/unlock", desk, nil)
	mustStatus(t, resp, http.StatusOK)
	var nothing lockResponse
	decodeInto(t, resp, &nothing)
	if nothing.Unlocked {
		t.Errorf("unlocking with nothing standing answered %+v", nothing)
	}
	if recs := f.operatorRecords(t, audit.EventSubjectUnlocked); len(recs) != 1 {
		t.Errorf("an unlock with nothing to lift wrote a record: %d", len(recs))
	}
	if after := len(stream.got(t)); after != before {
		t.Errorf("lifting published %d events, want none", after-before)
	}
}

func TestALockNeedsASubjectThisServerHasARowFor(t *testing.T) {
	f := newServer(t)
	desk := f.roleToken(t, identity.RoleGrantAdmin)

	resp := f.send(t, "POST", api+"/subjects/nobody@example.com/lock", desk, nil)
	mustStatus(t, resp, http.StatusNotFound)
	if e := decodeError(t, resp); e.Code != north.CodeNotFound {
		t.Errorf("locking an unknown subject answered %s, want not_found", e.Code)
	}
	if recs := f.operatorRecords(t, audit.EventSubjectLocked); len(recs) != 0 {
		t.Errorf("a lock that could not be placed wrote %d records", len(recs))
	}
}

func TestALockWithoutAReasonShowsTheHolderControlsWords(t *testing.T) {
	f := newServer(t)
	desk := f.roleToken(t, identity.RoleGrantAdmin)
	f.putSubject(t, f.roleToken(t, identity.RoleAdmin), "erin@example.com")
	stream := f.watch(t)

	mustStatus(t, f.send(t, "POST", api+"/subjects/erin@example.com/lock", desk, nil), http.StatusOK)
	var shown string
	for _, ev := range stream.got(t) {
		if ev.SessionKill != nil {
			shown = ev.SessionKill.Reason
		}
	}
	if shown != standing.LockedByAdministrator {
		t.Errorf("the holder was shown %q, want %q", shown, standing.LockedByAdministrator)
	}
}

func TestALockRefusesAReasonItCannotShow(t *testing.T) {
	f := newServer(t)
	desk := f.roleToken(t, identity.RoleGrantAdmin)
	f.putSubject(t, f.roleToken(t, identity.RoleAdmin), "frank@example.com")

	for name, body := range map[string]map[string]any{
		"a control character": {"reason": "line one\nline two"},
		"too long":            {"reason": string(make([]byte, 600))},
		"a bad reason code":   {"reason_code": "not\na code"},
	} {
		resp := f.send(t, "POST", api+"/subjects/frank@example.com/lock", desk, body)
		mustStatus(t, resp, http.StatusBadRequest)
		if e := decodeError(t, resp); e.Code != north.CodeInvalidRequest {
			t.Errorf("%s answered %s, want invalid_request", name, e.Code)
		}
	}
	if _, err := f.st.SubjectLocks().Get(t.Context(), "tenant-a", "frank@example.com"); !store.IsNotFound(err) {
		t.Errorf("a refused lock was stored: %v", err)
	}
}

// A lock whose publication fails STANDS, and the answer says so: the sessions
// were not told, and asking again is how they are.
func TestALockWhoseSessionsCouldNotBeToldStillStands(t *testing.T) {
	f := newServer(t)
	desk := f.roleToken(t, identity.RoleGrantAdmin)
	f.putSubject(t, f.roleToken(t, identity.RoleAdmin), "gina@example.com")

	if err := f.lifecycle.bus.Close(t.Context()); err != nil {
		t.Fatalf("close the bus: %v", err)
	}
	resp := f.send(t, "POST", api+"/subjects/gina@example.com/lock", desk, nil)
	mustStatus(t, resp, http.StatusServiceUnavailable)
	e := decodeError(t, resp)
	if e.Code != north.CodeDenialUndelivered || e.Parameters["stands"] != true || e.Parameters["subject"] != "gina@example.com" {
		t.Errorf("the refusal is %s %v, want standing_denial_undelivered naming the subject and that it stands", e.Code, e.Parameters)
	}
	if _, err := f.st.SubjectLocks().Get(t.Context(), "tenant-a", "gina@example.com"); err != nil {
		t.Errorf("the lock was not stored: %v", err)
	}
	if recs := f.operatorRecords(t, audit.EventSubjectLocked); len(recs) != 1 {
		t.Errorf("the lock was recorded %d times, want once: it stands", len(recs))
	}
}

func TestAnOperatorQuarantinesAndReleasesATarget(t *testing.T) {
	f := newServer(t)
	desk := f.roleToken(t, identity.RoleGrantAdmin)
	admin := f.roleToken(t, identity.RoleAdmin)
	f.putTarget(t, admin, "t-db", "db.example.com")
	stream := f.watch(t)

	resp := f.send(t, "POST", api+"/targets/t-db/quarantine", desk, map[string]any{"reason_code": "compromised"})
	mustStatus(t, resp, http.StatusOK)
	var placed struct {
		Quarantined bool `json:"quarantined"`
		Placed      bool `json:"placed"`
		Quarantine  *struct {
			Target     string `json:"target"`
			ReasonCode string `json:"reason_code"`
		} `json:"quarantine"`
	}
	decodeInto(t, resp, &placed)
	if !placed.Quarantined || !placed.Placed || placed.Quarantine == nil || placed.Quarantine.Target != "t-db" {
		t.Fatalf("quarantine answered %+v", placed)
	}

	// It drops every cached decision in the tenant and ends NO session:
	// quarantine stops new access, and an operator who wants sessions ended
	// has the revocations route.
	var invalidations int
	for _, ev := range stream.got(t) {
		if ev.SessionKill != nil {
			t.Errorf("a quarantine ended sessions: %+v", ev.SessionKill)
		}
		if ev.CacheInvalidate != nil && ev.CacheInvalidate.All {
			invalidations++
		}
	}
	if invalidations != 2 {
		t.Errorf("placing the quarantine published %d tenant-wide invalidations, want 2 (two passes)", invalidations)
	}
	if recs := f.operatorRecords(t, audit.EventTargetQuarantined); len(recs) != 1 || recs[0].Target != "db.example.com" {
		t.Errorf("the quarantine was recorded as %+v", recs)
	}

	// Again: a 200 that says nothing changed.
	resp = f.send(t, "POST", api+"/targets/t-db/quarantine", desk, nil)
	mustStatus(t, resp, http.StatusOK)
	decodeInto(t, resp, &placed)
	if placed.Placed {
		t.Errorf("a quarantine already standing answered Placed")
	}
	if recs := f.operatorRecords(t, audit.EventTargetQuarantined); len(recs) != 1 {
		t.Errorf("a quarantine already standing wrote a second record")
	}

	// The inventory shows it, and the target cannot be deleted from under it.
	auditor := f.roleToken(t, identity.RoleAuditor)
	resp = f.send(t, "GET", api+"/targets/t-db", auditor, nil)
	mustStatus(t, resp, http.StatusOK)
	var one struct {
		Quarantined bool `json:"quarantined"`
	}
	decodeInto(t, resp, &one)
	if !one.Quarantined {
		t.Errorf("GET target does not show the quarantine")
	}
	resp = f.send(t, "GET", api+"/targets", auditor, nil)
	mustStatus(t, resp, http.StatusOK)
	var list struct {
		Targets []struct {
			ID          string `json:"id"`
			Quarantined bool   `json:"quarantined"`
		} `json:"targets"`
	}
	decodeInto(t, resp, &list)
	if len(list.Targets) != 1 || !list.Targets[0].Quarantined {
		t.Errorf("the target list = %+v, want the one target quarantined", list.Targets)
	}

	resp = f.send(t, "DELETE", api+"/targets/t-db", admin, nil)
	mustStatus(t, resp, http.StatusConflict)
	if e := decodeError(t, resp); e.Code != north.CodeTargetQuarantined {
		t.Errorf("deleting a quarantined target answered %s, want target_quarantined", e.Code)
	}
	if _, err := f.st.Targets().Get(t.Context(), "tenant-a", "t-db"); err != nil {
		t.Errorf("the quarantined target was deleted anyway: %v", err)
	}
	// Writing it (a relabel, a zone change) does not lift the quarantine.
	f.putTarget(t, admin, "t-db", "db.example.com")
	if _, err := f.st.TargetQuarantines().Get(t.Context(), "tenant-a", "t-db"); err != nil {
		t.Errorf("a PUT lifted the quarantine: %v", err)
	}

	resp = f.send(t, "POST", api+"/targets/t-db/release", desk, map[string]any{"reason_code": "cleared"})
	mustStatus(t, resp, http.StatusOK)
	var released struct {
		Released bool `json:"released"`
	}
	decodeInto(t, resp, &released)
	if !released.Released {
		t.Errorf("release answered %+v", released)
	}
	if recs := f.operatorRecords(t, audit.EventTargetReleased); len(recs) != 1 {
		t.Errorf("the release was recorded %d times, want once", len(recs))
	}
	resp = f.send(t, "POST", api+"/targets/t-db/release", desk, nil)
	mustStatus(t, resp, http.StatusOK)
	decodeInto(t, resp, &released)
	if released.Released {
		t.Errorf("releasing with nothing standing answered Released")
	}
	if recs := f.operatorRecords(t, audit.EventTargetReleased); len(recs) != 1 {
		t.Errorf("a release with nothing to lift wrote a record")
	}
	mustStatus(t, f.send(t, "DELETE", api+"/targets/t-db", admin, nil), http.StatusNoContent)
}

func TestAQuarantineNeedsATargetWithARow(t *testing.T) {
	f := newServer(t)
	resp := f.send(t, "POST", api+"/targets/ghost/quarantine", f.roleToken(t, identity.RoleGrantAdmin), nil)
	mustStatus(t, resp, http.StatusNotFound)
}

// Only the roles that hold session:revoke place or lift either: the fleet
// admin and the policy author, who own the estate, do not.
func TestOnlyTheSessionRevokersPlaceOrLiftADenial(t *testing.T) {
	f := newServer(t)
	for _, route := range []struct{ path string }{
		{"/subjects/x@example.com/lock"}, {"/subjects/x@example.com/unlock"},
		{"/targets/t/quarantine"}, {"/targets/t/release"},
	} {
		for _, role := range identity.AllRoles {
			resp := f.send(t, "POST", api+route.path, f.roleToken(t, role), nil)
			if role.Can(identity.PermSessionRevoke) {
				if resp.StatusCode == http.StatusForbidden {
					t.Errorf("%s was refused POST %s", role, route.path)
				}
				continue
			}
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s answered POST %s with %d, want 403", role, route.path, resp.StatusCode)
			}
		}
	}
	// And a credential for another tenant reaches neither: a denial is a
	// tenant's own.
	other := f.token(t, "tenant-b", map[store.Tenant]identity.RoleSet{"tenant-b": {identity.RoleAdmin}})
	resp := f.send(t, "POST", api+"/subjects/x@example.com/lock", other, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("tenant-b's admin answered %d against tenant-a's lock route, want 403", resp.StatusCode)
	}
}
