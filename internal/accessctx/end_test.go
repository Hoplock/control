// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package accessctx_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/accessctx"
	"github.com/hoplock/control/internal/accessctx/declarative"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/store"
)

// Ending an external window (0017, M16, M10): a push that ends what it opened,
// and the sessions the window backed. The order the receiver follows is at the
// head of push.go.

// endOf is the end of the window id names, and nothing else.
func endOf(id string) pushBody { return pushBody{ID: id, Ended: true} }

// openAndUse pushes the scanner's window, and a session is authorized under
// it. It returns the grant.
func (h *harness) openAndUse(provider, id, session string) store.Grant {
	h.t.Helper()
	res, err := h.push(scannerPush, provider, h.scanWindow(id))
	if err != nil {
		h.t.Fatalf("push %s: %v", id, err)
	}
	h.advance(time.Minute)
	out, err := h.authorize(scanner, "db01.example.com", session)
	if err != nil || out.Deny != nil {
		h.t.Fatalf("the pushed window = %+v, %v; want an allow", out.Deny, err)
	}
	if _, _, ex := h.recordOf(session); ex.Grant != res.Grant.ID {
		h.t.Fatalf("the decision record names grant %q, want %s", ex.Grant, res.Grant.ID)
	}
	return res.Grant
}

func (h *harness) grant(id string) store.Grant {
	h.t.Helper()
	g, err := h.store.Grants().Get(h.t.Context(), tenant, id)
	if err != nil {
		h.t.Fatalf("grant %s: %v", id, err)
	}
	return g
}

func (h *harness) verifyChain() {
	h.t.Helper()
	res, err := audit.NewVerifier(h.store).VerifyTenant(h.t.Context(), tenant)
	if err != nil || !res.OK() {
		h.t.Errorf("audit-verify = %+v, %v; want every chain intact", res, err)
	}
}

// ---------------------------------------------------------------------------
// An end ends the sessions
// ---------------------------------------------------------------------------

func TestAnEndRevokesTheWindowAndEndsItsSessions(t *testing.T) {
	for _, mode := range []store.ExternalMode{store.ExternalPush, store.ExternalPushProbe} {
		t.Run(string(mode), func(t *testing.T) {
			fake := &fakeProvider{name: "acme-scanner", pushes: true, probes: true}
			// A push-probe window counts while its probe confirms it, so
			// the probe confirms it throughout: only the end stops it.
			fake.script(confirm("SCAN-7", time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)))
			h := newHarness(t, withProviders(fake))
			h.bind(scannerBinding("acme-scanner", mode))
			g := h.openAndUse("acme-scanner", "evt-1", "s-live")

			res, err := h.push(scannerPush, "acme-scanner", endOf("evt-1"))
			if err != nil {
				t.Fatalf("end: %v", err)
			}
			if res.Ended != accessctx.EndRevoked || res.Grant.ID != g.ID || res.AssertionID != "evt-1" {
				t.Fatalf("end = %+v, want revoked, naming grant %s", res, g.ID)
			}

			// Revoked by the integration's credential, with Control's reason.
			got := h.grant(g.ID)
			if got.RevokedAt.IsZero() || got.RevokedBy.Principal != scannerPush.Principal || got.RevokedBy.Subject != "" {
				t.Errorf("revoked at %v by %+v; want the integration's credential and no subject", got.RevokedAt, got.RevokedBy)
			}
			if got.RevokeReason != access.ExternalEndReason {
				t.Errorf("revoke reason = %q, want %q", got.RevokeReason, access.ExternalEndReason)
			}

			// The session is told with that reason, at its own proxy, and the
			// holder's cached decisions are dropped — and dropped again after
			// the settle pass, which also ends any session that raced the
			// revocation (0012's two passes; a session the first pass ended
			// is not killed twice).
			kills := h.revoker.killsOf("proxy-1", "s-live")
			if len(kills) != 1 {
				t.Fatalf("session_kills for s-live = %d, want 1", len(kills))
			}
			for _, k := range kills {
				if k.Reason != access.ExternalEndReason {
					t.Errorf("the holder was shown %q, want Control's reason", k.Reason)
				}
			}
			if n := h.revoker.invalidationsOf(scanner); n != 2 {
				t.Errorf("cache_invalidates for the holder = %d, want 2", n)
			}

			// The next authorize does not count it.
			if out, err := h.authorize(scanner, "db01.example.com", "s-after"); err != nil || out.Deny == nil {
				t.Errorf("after the end = %+v, %v; want a deny", out, err)
			}

			// One record, naming the system, the assertion and the credential.
			recs := h.controlRecords(access.EventGrantRevoked)
			if len(recs) != 1 {
				t.Fatalf("grant.revoked records = %d, want 1", len(recs))
			}
			a := recs[0].Attributes
			if a[audit.AttrGrantExternalEnded] != "true" || a[audit.AttrGrantExternalSystem] != "acme-scanner" ||
				a[audit.AttrGrantExternalAssertion] != "evt-1" || a[audit.AttrPrincipalID] != scannerPush.Principal ||
				a[audit.AttrGrantRevoke] != access.ExternalEndReason {
				t.Errorf("the grant.revoked record = %+v", a)
			}
			if want := "grant revoked for " + scanner + ": acme-scanner ended the window"; recs[0].Message != want {
				t.Errorf("message = %q, want %q", recs[0].Message, want)
			}
			h.verifyChain()
		})
	}
}

// A window that has not opened yet, ended, is revoked and never counts.
func TestAScheduledWindowEndedBeforeItOpensNeverCounts(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))
	w := h.scanWindow("evt-later")
	w.NotBefore, w.NotAfter = h.now().Add(time.Hour), h.now().Add(2*time.Hour)
	opened, err := h.push(scannerPush, "acme-scanner", w)
	if err != nil || opened.Grant.State(h.now()) != store.GrantScheduled {
		t.Fatalf("push a scheduled window = %+v, %v", opened.Grant, err)
	}

	res, err := h.push(scannerPush, "acme-scanner", endOf("evt-later"))
	if err != nil || res.Ended != accessctx.EndRevoked {
		t.Fatalf("end a scheduled window = %+v, %v; want revoked", res, err)
	}
	h.advance(90 * time.Minute)
	if out, err := h.authorize(scanner, "db01.example.com", "s-scheduled"); err != nil || out.Deny == nil {
		t.Errorf("inside the ended window = %+v, %v; want a deny", out, err)
	}
}

// An end is read by its id alone: never refused for its age, its scope or its
// window, and what it states beyond the id is recorded, under keys apart from
// the window the open asserted.
func TestAnEndIsReadByItsIDAloneAndWhatItStatesIsRecorded(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))
	h.openAndUse("acme-scanner", "evt-1", "s-1")
	if _, err := h.push(scannerPush, "acme-scanner", h.scanWindow("evt-2")); err != nil {
		t.Fatalf("push evt-2: %v", err)
	}

	issued := h.now().Add(-2 * time.Hour) // far older than max_assertion_age
	res, err := h.push(scannerPush, "acme-scanner", pushBody{
		ID: "evt-1", Reference: "SCAN-7-DONE", IssuedAt: issued,
		Additional: json.RawMessage(`{"result":"finished","findings":3}`), Ended: true,
	})
	if err != nil || res.Ended != accessctx.EndRevoked {
		t.Fatalf("an old end with a reference and context = %+v, %v; want revoked", res, err)
	}
	recs := h.controlRecords(access.EventGrantRevoked)
	if len(recs) != 1 {
		t.Fatalf("grant.revoked records = %d", len(recs))
	}
	a := recs[0].Attributes
	if a[audit.AttrGrantExternalEndedReference] != "SCAN-7-DONE" ||
		a[audit.AttrGrantExternalEndedIssuedAt] != issued.UTC().Format(time.RFC3339Nano) ||
		a[audit.AttrGrantExternalEndedAdditional] != `{"result":"finished","findings":3}` {
		t.Errorf("what the end stated = %+v", a)
	}
	// The open's own window end is still the open's.
	if a[audit.AttrGrantExternalEnd] == "" || a[audit.AttrGrantExternalEnd] == a[audit.AttrGrantExternalEndedIssuedAt] {
		t.Errorf("grant_external_window_end = %q; want the window the open asserted", a[audit.AttrGrantExternalEnd])
	}
	// Nothing about it reached the holder.
	for _, k := range h.revoker.killsOf("proxy-1", "s-1") {
		if k.Reason != access.ExternalEndReason {
			t.Errorf("the holder was shown %q", k.Reason)
		}
	}

	// A subject, targets and a window that match nothing — another subject,
	// a host outside the binding, a window long over — and it is still read
	// by its id alone.
	res, err = h.push(scannerPush, "acme-scanner", pushBody{
		ID: "evt-2", Subject: "mallory@example.com", Targets: []string{"evil.example.org"},
		NotBefore: h.now().Add(-48 * time.Hour), NotAfter: h.now().Add(-47 * time.Hour), Ended: true,
	})
	if err != nil || res.Ended != accessctx.EndRevoked {
		t.Errorf("an end carrying nonsense beside its id = %+v, %v; want revoked", res, err)
	}
	if n := len(h.controlRecords(accessctx.EventEscalation)); n != 0 {
		t.Errorf("escalation records = %d; an end is never judged for its scope", n)
	}
}

// ---------------------------------------------------------------------------
// Idempotent, and never rewriting
// ---------------------------------------------------------------------------

func TestAnEndPushedAgainChangesNothingAndSendsTheKillAgain(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))
	g := h.openAndUse("acme-scanner", "evt-1", "s-1")
	if _, err := h.push(scannerPush, "acme-scanner", endOf("evt-1")); err != nil {
		t.Fatalf("end: %v", err)
	}
	first := h.grant(g.ID)
	kills := len(h.revoker.killsOf("proxy-1", "s-1"))

	h.advance(time.Minute)
	res, err := h.push(scannerPush, "acme-scanner", endOf("evt-1"))
	if err != nil || res.Ended != accessctx.EndAlreadyEnded || res.Grant.ID != g.ID {
		t.Fatalf("the end again = %+v, %v; want already_ended naming the grant", res, err)
	}
	again := h.grant(g.ID)
	if !again.RevokedAt.Equal(first.RevokedAt) || again.RevokedBy != first.RevokedBy || again.RevokeReason != first.RevokeReason {
		t.Errorf("the second end rewrote the first: %+v → %+v", first, again)
	}
	if n := len(h.controlRecords(access.EventGrantRevoked)); n != 1 {
		t.Errorf("grant.revoked records = %d, want 1", n)
	}
	if n := len(h.revoker.killsOf("proxy-1", "s-1")); n <= kills {
		t.Errorf("session_kills = %d after the second end, %d before; want the kill sent again", n, kills)
	}
}

// An operator's revocation stands when the window's system ends it later.
func TestAnEndAfterAnOperatorsRevocationLeavesTheOperatorsStanding(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))
	g := h.openAndUse("acme-scanner", "evt-1", "s-1")
	if _, err := h.grants.Revoke(t.Context(), tenant, admin, g.ID, "Scanning paused for the release freeze."); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	res, err := h.push(scannerPush, "acme-scanner", endOf("evt-1"))
	if err != nil || res.Ended != accessctx.EndAlreadyEnded {
		t.Fatalf("an end after an operator's revocation = %+v, %v; want already_ended", res, err)
	}
	got := h.grant(g.ID)
	if got.RevokedBy.Principal != admin.Principal || got.RevokeReason != "Scanning paused for the release freeze." {
		t.Errorf("the operator's revocation = %+v %q; want it standing", got.RevokedBy, got.RevokeReason)
	}
	recs := h.controlRecords(access.EventGrantRevoked)
	if len(recs) != 1 || recs[0].Attributes[audit.AttrGrantExternalEnded] != "" {
		t.Errorf("grant.revoked records = %+v; want the operator's alone", recs)
	}
}

// A window that expired is not rewritten as revoked: no record, no event, and
// the row unchanged.
func TestAnEndOfAnExpiredWindowRewritesNothing(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))
	g := h.openAndUse("acme-scanner", "evt-1", "s-1")
	h.advance(3 * time.Hour) // the window ran two
	before := h.grant(g.ID)
	published := h.revoker.published()

	res, err := h.push(scannerPush, "acme-scanner", endOf("evt-1"))
	if err != nil || res.Ended != accessctx.EndAlreadyEnded || res.Grant.ID != g.ID {
		t.Fatalf("an end of an expired window = %+v, %v; want already_ended", res, err)
	}
	if s := res.Grant.State(h.now()); s != store.GrantExpired {
		t.Errorf("state = %s, want expired", s)
	}
	after := h.grant(g.ID)
	if !after.RevokedAt.IsZero() || after.RevokeReason != "" || !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Errorf("the expired grant was rewritten: %+v", after)
	}
	if n := len(h.controlRecords(access.EventGrantRevoked)); n != 0 {
		t.Errorf("grant.revoked records = %d, want none", n)
	}
	if n := h.revoker.published() - published; n != 0 {
		t.Errorf("%d events published for an expired window, want none", n)
	}
}

// With the stream refusing to publish, the end revokes and says the sessions
// were not told; pushed again once it recovers, it tells them.
func TestAnUndeliveredEndIsRevokedAndPushedAgainToResend(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))
	g := h.openAndUse("acme-scanner", "evt-1", "s-1")

	h.revoker.failWith(errors.New("the broker is closed"))
	res, err := h.push(scannerPush, "acme-scanner", endOf("evt-1"))
	if !access.IsUndelivered(err) {
		t.Fatalf("an end with the stream down = %v, want ErrUndelivered", err)
	}
	if res.Grant.ID != g.ID || res.Ended != accessctx.EndRevoked {
		t.Errorf("the undelivered end = %+v; want it to name the grant it revoked", res)
	}
	if h.grant(g.ID).RevokedAt.IsZero() {
		t.Error("an undelivered end left the grant unrevoked")
	}

	h.revoker.failWith(nil)
	res, err = h.push(scannerPush, "acme-scanner", endOf("evt-1"))
	if err != nil || res.Ended != accessctx.EndAlreadyEnded {
		t.Fatalf("the end again, the stream back = %+v, %v; want already_ended", res, err)
	}
	if len(h.revoker.killsOf("proxy-1", "s-1")) == 0 {
		t.Error("pushing the end again did not send the kill")
	}
}

// ---------------------------------------------------------------------------
// Ended stays ended
// ---------------------------------------------------------------------------

func TestAnEndBeforeItsOpenIsKeptAndTheOpenRefused(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))

	res, err := h.push(scannerPush, "acme-scanner", pushBody{ID: "evt-early", Reference: "SCAN-8", Ended: true})
	if err != nil || res.Ended != accessctx.EndRecorded || res.Grant.ID != "" || res.AssertionID != "evt-early" {
		t.Fatalf("an end for an id never opened = %+v, %v; want recorded", res, err)
	}
	recs := h.controlRecords(accessctx.EventWindowEnded)
	if len(recs) != 1 || recs[0].Attributes[audit.AttrContextAssertion] != "evt-early" ||
		recs[0].Attributes[audit.AttrPrincipalID] != scannerPush.Principal || recs[0].Severity != "warn" {
		t.Fatalf("access_context.window_ended records = %+v", recs)
	}

	_, err = h.push(scannerPush, "acme-scanner", h.scanWindow("evt-early"))
	r, ok := accessctx.AsRefusal(err)
	if !ok || r.Code != accessctx.CodeWindowClosed || r.Field != "id" {
		t.Fatalf("the open after its end = %v; want window_closed on id", err)
	}
	if n := len(h.controlRecords(accessctx.EventPushRefused)); n != 1 {
		t.Errorf("push_refused records = %d, want 1", n)
	}
	if n := h.grantCount(); n != 0 {
		t.Errorf("grants = %d; an ended window was opened", n)
	}

	res, err = h.push(scannerPush, "acme-scanner", endOf("evt-early"))
	if err != nil || res.Ended != accessctx.EndAlreadyEnded {
		t.Errorf("a second end = %+v, %v; want already_ended", res, err)
	}
	if n := len(h.controlRecords(accessctx.EventWindowEnded)); n != 1 {
		t.Errorf("window_ended records = %d after a second end, want 1", n)
	}
	h.verifyChain()
}

// THE RACE. An open and an end of one fresh id, at once, a hundred times:
// whichever wins, an end answered without error leaves no live grant behind,
// and no id ever has both a grant and a kept end.
func TestAnOpenAndAnEndThatRaceLeaveNothingLive(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))

	opened, recorded := 0, 0
	for i := range 100 {
		id := fmt.Sprintf("evt-race-%d", i)
		var (
			wg      sync.WaitGroup
			openErr error
			endRes  accessctx.PushResult
			endErr  error
		)
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, openErr = h.push(scannerPush, "acme-scanner", h.scanWindow(id))
		}()
		go func() {
			defer wg.Done()
			<-start
			// Staggered, so the end lands before the open, after it, and
			// between the open's first look and its insert.
			time.Sleep(time.Duration(i%20) * 250 * time.Microsecond)
			endRes, endErr = h.push(scannerPush, "acme-scanner", endOf(id))
		}()
		close(start)
		wg.Wait()

		if openErr != nil {
			if r, ok := accessctx.AsRefusal(openErr); !ok || r.Code != accessctx.CodeWindowClosed {
				t.Fatalf("%s: the open = %v; want admitted or window_closed", id, openErr)
			}
		}
		if endErr != nil {
			t.Fatalf("%s: the end = %v", id, endErr)
		}
		g, gerr := h.store.Grants().GetByAssertion(t.Context(), tenant, "acme-scanner", id)
		_, eerr := h.store.AccessContextWindowEnds().Get(t.Context(), tenant, "acme-scanner", id)
		switch {
		case gerr == nil && eerr == nil:
			t.Fatalf("%s has both a grant and a kept end", id)
		case gerr == nil && g.RevokedAt.IsZero():
			t.Fatalf("%s: the end answered %s and left grant %s live", id, endRes.Ended, g.ID)
		case gerr == nil:
			opened++
		case store.IsNotFound(gerr) && eerr == nil:
			recorded++
			if openErr == nil {
				t.Fatalf("%s: the open was admitted and no grant exists", id)
			}
		default:
			t.Fatalf("%s: grant %v, end %v", id, gerr, eerr)
		}
	}
	t.Logf("of 100 races, the open won %d and the end %d", opened, recorded)
}

// The answer that holds under the race is CreateExternal's own, made under the
// assertion's lock: an end kept after the receiver's first look is still found
// there, and the open inserts and records nothing.
func TestCreateExternalRefusesAnIDAlreadyEnded(t *testing.T) {
	h := newHarness(t)
	endedAt := h.now().Add(-time.Minute)
	if err := h.store.AccessContextWindowEnds().Insert(t.Context(), tenant, store.AccessContextWindowEnd{
		System: "acme-scanner", AssertionID: "evt-1", EndedAt: endedAt,
		EndedBy: store.GrantActor{Principal: scannerPush.Principal},
	}); err != nil {
		t.Fatalf("keep the end: %v", err)
	}
	_, _, err := h.grants.CreateExternal(t.Context(), tenant, scannerPush, access.ExternalSpec{
		Subject: scanner, Scope: access.Scope{Name: "vuln-scan", Targets: []string{"db01.example.com"}},
		NotBefore: h.now(), ExpiresAt: h.now().Add(time.Hour), System: "acme-scanner", AssertionID: "evt-1",
		Mode: store.ExternalPush, Reference: "SCAN-7", Reason: "window asserted by acme-scanner",
	})
	var ended *access.EndedError
	if !errors.Is(err, access.ErrAssertionEnded) || !errors.As(err, &ended) || !ended.End.EndedAt.Equal(endedAt) {
		t.Fatalf("CreateExternal under an ended id = %v; want ErrAssertionEnded saying when", err)
	}
	if n := h.grantCount(); n != 0 {
		t.Errorf("grants = %d, want none", n)
	}
	if n := len(h.controlRecords(access.EventGrantCreated)); n != 0 {
		t.Errorf("grant.created records = %d, want none", n)
	}
}

// Ends are scoped to their system and their tenant. An end naming an id only
// another system opened keeps an end for its own system and leaves the other's
// grant live; the tenant is the credential's, so a push made in another tenant
// reaches no binding there and nothing here.
func TestAnEndReachesOnlyItsOwnSystemAndTenant(t *testing.T) {
	scannerProv := &fakeProvider{name: "acme-scanner", pushes: true}
	otherProv := &fakeProvider{name: "other-scanner", pushes: true}
	h := newHarness(t, withProviders(scannerProv, otherProv))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))
	other := scannerBinding("other-scanner", store.ExternalPush)
	other.PushPrincipals = []string{otherPush.Principal}
	h.bind(other)
	g := h.openAndUse("acme-scanner", "evt-1", "s-1")

	res, err := h.push(otherPush, "other-scanner", endOf("evt-1"))
	if err != nil || res.Ended != accessctx.EndRecorded {
		t.Fatalf("another system's end of the id = %+v, %v; want recorded for its own system", res, err)
	}
	if !h.grant(g.ID).RevokedAt.IsZero() {
		t.Error("another system's end revoked this system's window")
	}

	_, err = h.context.Push(t.Context(), "globex", scannerPush, "acme-scanner", "application/json", endOf("evt-1").bytes())
	if !isRefusal(err, accessctx.CodeBindingNotFound) {
		t.Errorf("the end pushed in another tenant = %v; want binding_not_found there", err)
	}
	if !h.grant(g.ID).RevokedAt.IsZero() {
		t.Error("an end in another tenant revoked this tenant's window")
	}
	if out, err := h.authorize(scanner, "db01.example.com", "s-2"); err != nil || out.Deny != nil {
		t.Errorf("the window after both = %+v, %v; want it still counting", out.Deny, err)
	}
}

// ---------------------------------------------------------------------------
// The door is the open's door
// ---------------------------------------------------------------------------

func TestAnEndPassesTheOpensDoor(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))
	g := h.openAndUse("acme-scanner", "evt-1", "s-1")

	if _, err := h.push(otherPush, "acme-scanner", endOf("evt-1")); !isRefusal(err, accessctx.CodePushNotPermitted) {
		t.Errorf("an end from a credential the binding does not name = %v", err)
	}
	if n := len(h.controlRecords(accessctx.EventEscalation)); n != 1 {
		t.Errorf("escalation records = %d, want 1", n)
	}

	for name, id := range map[string]string{"empty": " ", "control character": "evt\x071"} {
		_, err := h.push(scannerPush, "acme-scanner", endOf(id))
		if r, ok := accessctx.AsRefusal(err); !ok || r.Code != accessctx.CodeAssertionMalformed || r.Field != "id" {
			t.Errorf("an end with an %s id = %v; want assertion_malformed on id", name, err)
		}
	}

	b := scannerBinding("acme-scanner", store.ExternalPush)
	b.Enabled = false
	h.bind(b)
	if _, err := h.push(scannerPush, "acme-scanner", endOf("evt-1")); !isRefusal(err, accessctx.CodeBindingDisabled) {
		t.Errorf("an end through a disabled binding = %v", err)
	}
	if !h.grant(g.ID).RevokedAt.IsZero() {
		t.Error("a refused end ended the window")
	}
}

// Opens and ends share one rate.
func TestOpensAndEndsShareOneRate(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake), withPushRate(0.001, 3))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))

	if _, err := h.push(scannerPush, "acme-scanner", h.scanWindow("evt-1")); err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := range 2 {
		if _, err := h.push(scannerPush, "acme-scanner", endOf(fmt.Sprintf("evt-%d", i+2))); err != nil {
			t.Fatalf("end %d: %v", i, err)
		}
	}
	if _, err := h.push(scannerPush, "acme-scanner", endOf("evt-1")); !isRefusal(err, accessctx.CodeRateLimited) {
		t.Errorf("an end past push_burst, after an open = %v; want rate_limited", err)
	}
}

// ---------------------------------------------------------------------------
// The declarative provider
// ---------------------------------------------------------------------------

// End to end with Control's own provider as the integration: a change ticket
// opens a window, a session is authorized under it, the ticket is cancelled,
// and the session is killed.
func TestTheDeclarativeProviderEndsAWindowEndToEnd(t *testing.T) {
	prov, err := declarative.New(config.DeclarativeProviderConfig{
		Name: "itsm",
		Push: &config.DeclarativePushConfig{
			ID: "$.change.revision_id", Reference: "$.change.number", Subject: "$.change.implementer",
			Targets: "$.change.ci[*]", WindowEnd: "$.change.planned_end",
			Ended: []config.DeclarativeAssertion{{Path: "$.change.status", OneOf: []string{"Cancelled", "Closed"}}},
		},
	}, config.EgressConfig{}, declarative.Options{})
	if err != nil {
		t.Fatalf("declarative: %v", err)
	}
	h := newHarness(t, withProviders(prov))
	h.bind(store.AccessContextBinding{
		Provider: "itsm", Mode: store.ExternalPush, Scope: "change-window",
		SubjectGroups: []string{"dba"}, Targets: []string{"*.example.com"},
		MaxWindow: 4 * time.Hour, PushPrincipals: []string{scannerPush.Principal}, Enabled: true,
	})
	ticket := func(status string) []byte {
		b, _ := json.Marshal(map[string]any{"change": map[string]any{
			"revision_id": "CHG-42/1", "number": "CHG-42", "status": status,
			"implementer": alice, "ci": []string{"db01.example.com"},
			"planned_end": h.now().Add(2 * time.Hour).Format(time.RFC3339),
		}})
		return b
	}

	opened, err := h.context.Push(t.Context(), tenant, scannerPush, "itsm", "application/json", ticket("Approved"))
	if err != nil || opened.Ended != "" {
		t.Fatalf("an approved change = %+v, %v; want an open", opened, err)
	}
	if out, err := h.authorize(alice, "db01.example.com", "s-change"); err != nil || out.Deny != nil {
		t.Fatalf("inside the change window = %+v, %v; want an allow", out.Deny, err)
	}

	ended, err := h.context.Push(t.Context(), tenant, scannerPush, "itsm", "application/json", ticket("Cancelled"))
	if err != nil || ended.Ended != accessctx.EndRevoked || ended.Grant.ID != opened.Grant.ID {
		t.Fatalf("a cancelled change = %+v, %v; want the window revoked", ended, err)
	}
	if kills := h.revoker.killsOf("proxy-1", "s-change"); len(kills) == 0 || kills[0].Reason != access.ExternalEndReason {
		t.Errorf("the live session's kills = %+v; want it ended with Control's reason", kills)
	}
	if out, err := h.authorize(alice, "db01.example.com", "s-after"); err != nil || out.Deny == nil {
		t.Errorf("after the cancellation = %+v, %v; want a deny", out, err)
	}
}
