// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package action_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/action"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/standing"
	"github.com/hoplock/control/internal/store"
)

// same reports whether two results are one: the first answer, returned again.
func same(a, b ext.ActionResult) bool {
	return a.ActionID == b.ActionID && a.Affected == b.Affected && a.ReasonCode == b.ReasonCode && a.AppliedAt.Equal(b.AppliedAt)
}

func attr(t *testing.T, r store.AuditRecord, key string) string {
	t.Helper()
	return r.Attributes[key]
}

// wantsAction asserts a record names the action behind it: the id, the kind,
// the external reference, the reason code and the provider, with the handler
// as the actor and no person behind it.
func wantsAction(t *testing.T, r store.AuditRecord, id, kind, ref, code string) {
	t.Helper()
	for key, want := range map[string]string{
		audit.AttrActionID: id, audit.AttrActionKind: kind, audit.AttrActionExternalRef: ref,
		audit.AttrActionReasonCode: code, audit.AttrActionProvider: provider,
		audit.AttrPrincipalID: action.PrincipalPrefix + provider,
	} {
		if got := attr(t, r, key); got != want {
			t.Errorf("%s: %s = %q, want %q (attributes %v)", r.Attributes[audit.AttrEvent], key, got, want, r.Attributes)
		}
	}
	if got := attr(t, r, audit.AttrActorSubject); got != "" {
		t.Errorf("the handler has no person behind it, and %s names %q", r.Attributes[audit.AttrEvent], got)
	}
}

func TestKillSessionBySessionAndBySubjectPublishesOneKillWithTheHoldersReason(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if out := h.authorize(alice, "sess-1"); out.Deny != nil {
		t.Fatalf("setup: alice was denied: %+v", out.Deny)
	}

	// By session, with the requester's own words, verbatim.
	res := h.mustDo(ext.Action{
		Kind: ext.ActionKillSession, ActionID: "k-1", SessionID: "sess-1",
		ReasonCode: "malware", ReasonText: "Your session was ended by the SOC.", ExternalRef: "INC-4711",
	})
	if res.Affected != 1 || res.ActionID != "k-1" || res.ReasonCode != "" || res.AppliedAt.IsZero() {
		t.Errorf("result = %+v, want one kill published", res)
	}
	kills := h.revoker.killed()
	if len(kills) != 1 || len(kills[0].SessionIDs) != 1 || kills[0].SessionIDs[0] != "sess-1" || kills[0].Reason != "Your session was ended by the SOC." {
		t.Fatalf("published %+v, want one session_kill for sess-1 with the requester's words", kills)
	}
	recs := h.records(tenant, audit.EventRevocationPublished)
	if len(recs) != 1 {
		t.Fatalf("%d operator.revocation.published records, want one", len(recs))
	}
	wantsAction(t, recs[0], "k-1", "kill_session", "INC-4711", "malware")
	if attr(t, recs[0], "operator.reason") != "Your session was ended by the SOC." {
		t.Errorf("the record does not say what the holder was shown: %v", recs[0].Attributes)
	}

	// By subject, with Control's words when the requester gave none.
	res = h.mustDo(ext.Action{Kind: ext.ActionKillSession, ActionID: "k-2", SubjectID: alice})
	if res.Affected != 1 {
		t.Errorf("result = %+v", res)
	}
	kills = h.revoker.killed()
	if len(kills) != 2 || kills[1].Subject != alice || kills[1].Reason != action.KillReason {
		t.Fatalf("published %+v, want a subject kill with %q", kills, action.KillReason)
	}
	if strings.Contains(action.KillReason, "administrator") {
		t.Errorf("a kill reason that says administrator is false for an action: %q", action.KillReason)
	}
	h.verify()
}

func TestRevokeGrantRevokesThroughTheOperatorsPathAndEndsItsSessions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.grantFor(tenant, "g-live", alice, "sess-g1", "sess-g2")

	res := h.mustDo(ext.Action{
		Kind: ext.ActionRevokeGrant, ActionID: "r-1", GrantID: "g-live", ReasonCode: "contained", ExternalRef: "INC-9",
	})
	if res.Affected != 1 {
		t.Fatalf("result = %+v, want one grant revoked", res)
	}
	g, err := h.store.Grants().Get(t.Context(), tenant, "g-live")
	if err != nil || g.RevokedAt.IsZero() || g.RevokedBy.Principal != action.PrincipalPrefix+provider || g.RevokedBy.Subject != "" {
		t.Fatalf("the grant is %+v (%v), want it revoked by the handler with no person behind it", g, err)
	}
	if g.RevokeReason != action.RevokeReason || strings.Contains(g.RevokeReason, "administrator") {
		t.Errorf("the holder was shown %q, want Control's words for an action", g.RevokeReason)
	}
	for _, sess := range []string{"sess-g1", "sess-g2"} {
		if h.revoker.killsOf(sess, "") == 0 {
			t.Errorf("no session_kill ended %s", sess)
		}
	}
	recs := h.records(tenant, access.EventGrantRevoked)
	if len(recs) != 1 {
		t.Fatalf("%d grant.revoked records, want one", len(recs))
	}
	wantsAction(t, recs[0], "r-1", "revoke_grant", "INC-9", "contained")

	// A grant already revoked is nothing to act on: Affected 0, a reason code,
	// and the row exactly as the first revocation left it.
	res = h.mustDo(ext.Action{Kind: ext.ActionRevokeGrant, ActionID: "r-2", GrantID: "g-live"})
	if res.Affected != 0 || res.ReasonCode != action.ReasonAlreadyRevoked {
		t.Errorf("revoking a revoked grant = %+v, want Affected 0 and %q", res, action.ReasonAlreadyRevoked)
	}
	again, _ := h.store.Grants().Get(t.Context(), tenant, "g-live")
	if !again.RevokedAt.Equal(g.RevokedAt) || again.RevokedBy != g.RevokedBy || again.RevokeReason != g.RevokeReason {
		t.Errorf("a no-op changed the grant: %+v became %+v", g, again)
	}
	if n := len(h.records(tenant, access.EventGrantRevoked)); n != 1 {
		t.Errorf("a no-op wrote a second grant.revoked record: %d", n)
	}
	// It is still audited.
	unchanged := h.records(tenant, audit.EventActionUnchanged)
	if len(unchanged) != 1 {
		t.Fatalf("%d action.unchanged records, want one", len(unchanged))
	}
	wantsAction(t, unchanged[0], "r-2", "revoke_grant", "", "")

	// An expired grant likewise, and it is not rewritten as revoked.
	old := store.Grant{
		ID: "g-old", SubjectID: alice, Scope: "break-fix", Origin: store.GrantOriginManual,
		NotBefore: time.Now().Add(-2 * time.Hour), ExpiresAt: time.Now().Add(-time.Hour),
		CreatedBy: store.GrantActor{Principal: "p-admin"}, Reason: "incident",
	}
	if err := h.store.Grants().Insert(t.Context(), tenant, old); err != nil {
		t.Fatal(err)
	}
	res = h.mustDo(ext.Action{Kind: ext.ActionRevokeGrant, ActionID: "r-3", GrantID: "g-old"})
	if res.Affected != 0 || res.ReasonCode != action.ReasonGrantExpired {
		t.Errorf("revoking an expired grant = %+v, want Affected 0 and %q", res, action.ReasonGrantExpired)
	}
	if g, _ := h.store.Grants().Get(t.Context(), tenant, "g-old"); !g.RevokedAt.IsZero() {
		t.Errorf("an expired grant was rewritten as revoked: %+v", g)
	}
	h.verify()
}

func TestLockThenUnlockRoundTripsAndEachActIsRecordedOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if out := h.authorize(alice, "sess-1"); out.Deny != nil {
		t.Fatalf("setup: alice was denied: %+v", out.Deny)
	}

	res := h.mustDo(ext.Action{
		Kind: ext.ActionLockSubject, ActionID: "l-1", SubjectID: alice, ReasonCode: "Malware Detected", ExternalRef: "INC-5",
		ReasonText: "Your account is locked while we investigate.",
	})
	if res.Affected != 1 || res.ReasonCode != "" {
		t.Fatalf("lock result = %+v", res)
	}
	// Placing the lock ends what alice holds, with the requester's words, and
	// drops her cached decisions.
	if h.revoker.killsOf("", alice) == 0 {
		t.Error("locking did not end the subject's sessions")
	}
	for _, k := range h.revoker.killed() {
		if k.Subject == alice && k.Reason != "Your account is locked while we investigate." {
			t.Errorf("the holder was shown %q", k.Reason)
		}
	}
	var dropped bool
	for _, inv := range h.revoker.invalidated() {
		dropped = dropped || inv.Subject == alice
	}
	if !dropped {
		t.Error("locking did not invalidate the subject's cached decisions")
	}

	out := h.authorize(alice, "sess-2")
	if out.Deny == nil || out.Deny.Reason != "subject_locked" {
		t.Fatalf("authorize while locked = %+v, want a subject_locked deny", out)
	}
	// Another subject is unaffected.
	if out := h.authorize(bob, "sess-3"); out.Deny != nil {
		t.Errorf("bob was denied for alice's lock: %+v", out.Deny)
	}

	recs := h.records(tenant, audit.EventSubjectLocked)
	if len(recs) != 1 {
		t.Fatalf("%d operator.subject.locked records, want one", len(recs))
	}
	wantsAction(t, recs[0], "l-1", "lock_subject", "INC-5", "Malware Detected")

	res = h.mustDo(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "u-1", SubjectID: alice, ExternalRef: "INC-5"})
	if res.Affected != 1 {
		t.Fatalf("unlock result = %+v", res)
	}
	if out := h.authorize(alice, "sess-4"); out.Deny != nil {
		t.Errorf("authorize after the unlock was denied: %+v", out.Deny)
	}
	unlocked := h.records(tenant, audit.EventSubjectUnlocked)
	if len(unlocked) != 1 {
		t.Fatalf("%d operator.subject.unlocked records, want one", len(unlocked))
	}
	wantsAction(t, unlocked[0], "u-1", "unlock_subject", "INC-5", "")
	if n := len(h.records(tenant, audit.EventSubjectLocked)); n != 1 {
		t.Errorf("unlocking wrote another lock record: %d", n)
	}

	// Locking twice is nothing to act on, and a lock is left to an operator
	// when it stands: unlocking nothing is likewise.
	h.mustDo(ext.Action{Kind: ext.ActionLockSubject, ActionID: "l-2", SubjectID: alice})
	res = h.mustDo(ext.Action{Kind: ext.ActionLockSubject, ActionID: "l-3", SubjectID: alice})
	if res.Affected != 0 || res.ReasonCode != action.ReasonAlreadyLocked {
		t.Errorf("a second lock = %+v, want Affected 0 and %q", res, action.ReasonAlreadyLocked)
	}
	h.mustDo(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "u-2", SubjectID: alice})
	res = h.mustDo(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "u-3", SubjectID: alice})
	if res.Affected != 0 || res.ReasonCode != action.ReasonNotLocked {
		t.Errorf("a second unlock = %+v, want Affected 0 and %q", res, action.ReasonNotLocked)
	}
	// The lock's default words are Control's, and not the administrator's.
	h.mustDo(ext.Action{Kind: ext.ActionLockSubject, ActionID: "l-4", SubjectID: bob})
	lastKill := h.revoker.killed()[len(h.revoker.killed())-1]
	if lastKill.Subject != bob || lastKill.Reason != standing.LockedBySecurityResponse || strings.Contains(lastKill.Reason, "administrator") {
		t.Errorf("a lock with no text showed %+v", lastKill)
	}
	h.verify()
}

func TestQuarantineDeniesNewAccessAndLeavesALiveSessionRunning(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if out := h.authorize(alice, "sess-live"); out.Deny != nil {
		t.Fatalf("setup: %+v", out.Deny)
	}
	killsBefore := len(h.revoker.killed())

	res := h.mustDo(ext.Action{
		Kind: ext.ActionQuarantineTarget, ActionID: "q-1", TargetID: "t-db", ReasonCode: "compromised", ExternalRef: "INC-6",
	})
	if res.Affected != 1 {
		t.Fatalf("result = %+v", res)
	}
	if out := h.authorize(bob, "sess-new"); out.Deny == nil || out.Deny.Reason != "target_quarantined" {
		t.Fatalf("authorize against a quarantined target = %+v, want a target_quarantined deny", out)
	}
	// Quarantine stops new access. It ends nothing.
	if n := len(h.revoker.killed()); n != killsBefore {
		t.Errorf("the quarantine published %d kills", n-killsBefore)
	}
	// And it drops every cached decision in the tenant, which is how a cached
	// allow for the target is withdrawn.
	var all bool
	for _, inv := range h.revoker.invalidated() {
		all = all || inv.All
	}
	if !all {
		t.Error("the quarantine did not invalidate cached decisions")
	}
	recs := h.records(tenant, audit.EventTargetQuarantined)
	if len(recs) != 1 {
		t.Fatalf("%d operator.target.quarantined records, want one", len(recs))
	}
	wantsAction(t, recs[0], "q-1", "quarantine_target", "INC-6", "compromised")

	// Twice is nothing to act on.
	res = h.mustDo(ext.Action{Kind: ext.ActionQuarantineTarget, ActionID: "q-2", TargetID: "t-db"})
	if res.Affected != 0 || res.ReasonCode != action.ReasonAlreadyQuarantined {
		t.Errorf("a second quarantine = %+v", res)
	}

	// Only an operator releases one: ext declares no kind for it.
	if _, err := h.standing.Release(t.Context(), tenant, standing.Actor{Principal: "p-operator"}, standing.LiftRequest{ID: "t-db"}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if out := h.authorize(bob, "sess-after"); out.Deny != nil {
		t.Errorf("authorize after the release was denied: %+v", out.Deny)
	}
	h.verify()
}

// Each of these is KindInvalid naming the field, writes an action.refused
// record, and has no effect.
func TestAMalformedActionIsRefusedNamingTheFieldAndHasNoEffect(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	cases := []struct {
		name  string
		a     ext.Action
		field string
	}{
		{"unspecified kind", ext.Action{ActionID: "m-1", SubjectID: alice}, "kind"},
		{"an unknown kind", ext.Action{Kind: ext.ActionKind(99), ActionID: "m-2", SubjectID: alice}, "kind"},
		{"a kill with neither selector", ext.Action{Kind: ext.ActionKillSession, ActionID: "m-3"}, "session_id"},
		{"a kill with both selectors", ext.Action{Kind: ext.ActionKillSession, ActionID: "m-4", SessionID: "s", SubjectID: alice}, "subject_id"},
		{"a lock with no subject", ext.Action{Kind: ext.ActionLockSubject, ActionID: "m-5"}, "subject_id"},
		{"a lock with an extra selector", ext.Action{Kind: ext.ActionLockSubject, ActionID: "m-6", SubjectID: alice, TargetID: "t-db"}, "target_id"},
		{"an unlock with a grant", ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "m-7", SubjectID: alice, GrantID: "g"}, "grant_id"},
		{"a revoke with no grant", ext.Action{Kind: ext.ActionRevokeGrant, ActionID: "m-8"}, "grant_id"},
		{"a revoke with a session", ext.Action{Kind: ext.ActionRevokeGrant, ActionID: "m-9", GrantID: "g", SessionID: "s"}, "session_id"},
		{"a quarantine with no target", ext.Action{Kind: ext.ActionQuarantineTarget, ActionID: "m-10"}, "target_id"},
		{"a quarantine with a subject", ext.Action{Kind: ext.ActionQuarantineTarget, ActionID: "m-11", TargetID: "t-db", SubjectID: alice}, "subject_id"},
		{"no action id", ext.Action{Kind: ext.ActionLockSubject, SubjectID: alice}, "action_id"},
		{"an action id with a newline", ext.Action{Kind: ext.ActionLockSubject, ActionID: "a\nb", SubjectID: alice}, "action_id"},
		{"an action id too long", ext.Action{Kind: ext.ActionLockSubject, ActionID: strings.Repeat("x", 300), SubjectID: alice}, "action_id"},
		{"reason text with a control character", ext.Action{Kind: ext.ActionKillSession, ActionID: "m-12", SubjectID: alice, ReasonText: "line one\nline two"}, "reason_text"},
		{"reason text over the limit", ext.Action{Kind: ext.ActionKillSession, ActionID: "m-13", SubjectID: alice, ReasonText: strings.Repeat("y", 600)}, "reason_text"},
		{"reason text that is blank", ext.Action{Kind: ext.ActionLockSubject, ActionID: "m-14", SubjectID: alice, ReasonText: "   "}, "reason_text"},
		{"a reason code with a control character", ext.Action{Kind: ext.ActionLockSubject, ActionID: "m-15", SubjectID: alice, ReasonCode: "bad\ncode"}, "reason_code"},
		{"a reason code too long", ext.Action{Kind: ext.ActionLockSubject, ActionID: "m-17", SubjectID: alice, ReasonCode: strings.Repeat("c", 200)}, "reason_code"},
		{"an external reference with a control character", ext.Action{Kind: ext.ActionLockSubject, ActionID: "m-16", SubjectID: alice, ExternalRef: "INC\t1\n"}, "external_ref"},
	}
	for _, c := range cases {
		before := len(h.records(tenant, audit.EventActionRefused))
		res, err := h.do(c.a)
		if ext.KindOf(err) != ext.KindInvalid || !contains(err, c.field) {
			t.Errorf("%s: err = %v, want KindInvalid naming %q", c.name, err, c.field)
			continue
		}
		if res.Affected != 0 {
			t.Errorf("%s: result = %+v", c.name, res)
		}
		recs := h.records(tenant, audit.EventActionRefused)
		if len(recs) != before+1 {
			t.Errorf("%s: %d action.refused records, want one more than %d", c.name, len(recs), before)
			continue
		}
		if got := recs[0].Attributes["operator.field"]; got != c.field {
			t.Errorf("%s: the refusal record names field %q, want %q", c.name, got, c.field)
		}
		for key, v := range recs[0].Attributes {
			if strings.ContainsAny(v, "\n\r\t") {
				t.Errorf("%s: the refusal record carries a control character in %s: %q", c.name, key, v)
			}
		}
	}

	// No effect: nothing stored, nothing published, nothing in the ledger.
	if n := h.ledger(tenant); n != 0 {
		t.Errorf("refused actions left %d ledger rows", n)
	}
	if len(h.revoker.killed()) != 0 || len(h.revoker.invalidated()) != 0 {
		t.Errorf("refused actions published %d kills and %d invalidations", len(h.revoker.killed()), len(h.revoker.invalidated()))
	}
	if st, _ := h.store.Subjects().Standing(t.Context(), tenant, alice); st.Lock != nil {
		t.Errorf("a refused action locked alice")
	}
	h.verify()
}

// An action with no tenant cannot be recorded in it, so its refusal is
// recorded in the deployment's own.
func TestAnActionWithNoTenantIsRefusedAndRecordedInTheDefaultTenant(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	_, err := h.exec.Execute(t.Context(), ext.Action{Kind: ext.ActionLockSubject, ActionID: "n-1", SubjectID: alice})
	if ext.KindOf(err) != ext.KindInvalid || !contains(err, "tenant") {
		t.Fatalf("err = %v, want KindInvalid naming tenant", err)
	}
	recs := h.records(tenant, audit.EventActionRefused)
	if len(recs) != 1 || recs[0].Attributes["operator.field"] != "tenant" {
		t.Errorf("the refusal was recorded as %+v, want one naming the tenant field", recs)
	}
	if h.ledger(tenant) != 0 {
		t.Error("a refused action left a ledger row")
	}
}

// An unknown subject, grant, target or session is KindNotFound, so is one that
// exists only in another tenant, and nothing in either tenant changes.
func TestAnIDThatIsNotInTheTenantIsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.grantFor(other, "g-theirs", alice)
	if err := h.store.Targets().Upsert(t.Context(), other, store.Target{ID: "t-theirs", Hostname: "theirs.example.com", Zone: "edge"}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Subjects().Upsert(t.Context(), other, store.Subject{ID: "carol@example.com", Source: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Decisions().Insert(t.Context(), other, store.Decision{
		ID: "d-theirs", SubjectID: alice, InputsDigest: "x", Effect: store.DecisionEffectAllow,
		ProxyID: "proxy-1", SessionID: "sess-theirs", DecidedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	for name, a := range map[string]ext.Action{
		"an unknown subject to lock":            {Kind: ext.ActionLockSubject, ActionID: "f-1", SubjectID: "nobody@example.com"},
		"an unknown subject to unlock":          {Kind: ext.ActionUnlockSubject, ActionID: "f-2", SubjectID: "nobody@example.com"},
		"an unknown subject to kill":            {Kind: ext.ActionKillSession, ActionID: "f-3", SubjectID: "nobody@example.com"},
		"an unknown session":                    {Kind: ext.ActionKillSession, ActionID: "f-4", SessionID: "sess-nobody"},
		"an unknown grant":                      {Kind: ext.ActionRevokeGrant, ActionID: "f-5", GrantID: "g-nobody"},
		"an unknown target":                     {Kind: ext.ActionQuarantineTarget, ActionID: "f-6", TargetID: "t-nobody"},
		"a grant that is another tenant's":      {Kind: ext.ActionRevokeGrant, ActionID: "f-7", GrantID: "g-theirs"},
		"a target that is another tenant's":     {Kind: ext.ActionQuarantineTarget, ActionID: "f-8", TargetID: "t-theirs"},
		"a subject that is another tenant's":    {Kind: ext.ActionLockSubject, ActionID: "f-9", SubjectID: "carol@example.com"},
		"a session that is another tenant's":    {Kind: ext.ActionKillSession, ActionID: "f-10", SessionID: "sess-theirs"},
		"a subject that is another tenant's, 2": {Kind: ext.ActionKillSession, ActionID: "f-11", SubjectID: "carol@example.com"},
	} {
		res, err := h.do(a)
		if ext.KindOf(err) != ext.KindNotFound {
			t.Errorf("%s: err = %v, want KindNotFound", name, err)
		}
		if res.Affected != 0 {
			t.Errorf("%s: result = %+v", name, res)
		}
	}

	if n := h.ledger(tenant) + h.ledger(other); n != 0 {
		t.Errorf("not-found actions left %d ledger rows", n)
	}
	if len(h.revoker.killed()) != 0 || len(h.revoker.invalidated()) != 0 {
		t.Error("not-found actions published events")
	}
	if g, _ := h.store.Grants().Get(t.Context(), other, "g-theirs"); !g.RevokedAt.IsZero() {
		t.Error("tenant globex's grant was revoked by a tenant acme action")
	}
	if _, err := h.store.TargetQuarantines().Get(t.Context(), other, "t-theirs"); !store.IsNotFound(err) {
		t.Errorf("tenant globex's target was quarantined by a tenant acme action: %v", err)
	}
	if st, _ := h.store.Subjects().Standing(t.Context(), other, "carol@example.com"); st.Lock != nil {
		t.Error("tenant globex's subject was locked by a tenant acme action")
	}
	// The refusals are recorded in the tenant that was asked.
	if n := len(h.records(tenant, audit.EventActionRefused)); n != 11 {
		t.Errorf("%d action.refused records in the tenant asked, want 11", n)
	}
	if n := len(h.records(other, audit.EventActionRefused)); n != 0 {
		t.Errorf("the other tenant has %d action.refused records", n)
	}
	h.verify()

	// The same ids in their own tenant act on their own rows only.
	if _, err := h.exec.Execute(t.Context(), ext.Action{
		Tenant: ext.Tenant(other), Kind: ext.ActionRevokeGrant, ActionID: "f-7", GrantID: "g-theirs",
	}); err != nil {
		t.Fatalf("revoking tenant globex's own grant: %v", err)
	}
	if g, _ := h.store.Grants().Get(t.Context(), other, "g-theirs"); g.RevokedAt.IsZero() {
		t.Error("tenant globex's grant was not revoked by its own tenant's action")
	}
}

func TestAnInboundActionCannotLockABreakGlassSubjectButAnOperatorCan(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res, err := h.do(ext.Action{Kind: ext.ActionLockSubject, ActionID: "b-1", SubjectID: root, ExternalRef: "INC-7"})
	if ext.KindOf(err) != ext.KindDenied || res.ReasonCode != action.ReasonBreakGlassSubject {
		t.Fatalf("err = %v, result = %+v; want KindDenied with %q", err, res, action.ReasonBreakGlassSubject)
	}
	recs := h.records(tenant, audit.EventActionRefused)
	if len(recs) != 1 || recs[0].Attributes["operator.code"] != action.ReasonBreakGlassSubject ||
		recs[0].Attributes["operator.refusal"] != "denied" {
		t.Fatalf("the refusal was recorded as %+v", recs)
	}
	wantsAction(t, recs[0], "b-1", "lock_subject", "INC-7", "")
	if st, _ := h.store.Subjects().Standing(t.Context(), tenant, root); st.Lock != nil {
		t.Error("the break-glass subject was locked")
	}
	if h.ledger(tenant) != 0 {
		t.Error("a refusal left a ledger row")
	}
	if len(h.revoker.killed()) != 0 {
		t.Error("a refusal published a kill")
	}

	// An operator, acting deliberately, still can.
	if _, err := h.standing.Lock(t.Context(), tenant, standing.Actor{Principal: "p-operator", BreakGlass: true}, standing.LockRequest{
		SubjectID: root, Reason: standing.LockedByAdministrator,
	}); err != nil {
		t.Fatalf("an operator's lock of a break-glass subject: %v", err)
	}
	if st, _ := h.store.Subjects().Standing(t.Context(), tenant, root); st.Lock == nil {
		t.Error("the operator's lock did not stand")
	}
	// And a handler may still lift it: unlocking is not locking out.
	if res := h.mustDo(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "b-2", SubjectID: root}); res.Affected != 1 {
		t.Errorf("unlock result = %+v", res)
	}
	h.verify()
}

func TestAnActionIsIdempotentOnItsID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.authorize(alice, "sess-1")

	a := ext.Action{Kind: ext.ActionKillSession, ActionID: "i-1", SessionID: "sess-1", ReasonText: "ended", ExternalRef: "INC-1"}
	first := h.mustDo(a)
	second := h.mustDo(a)

	// The same action twice returns the first result, writes no second record,
	// and sends the kill again.
	if !same(first, second) || first.Affected != 1 {
		t.Errorf("first = %+v, second = %+v", first, second)
	}
	if n := len(h.records(tenant, audit.EventRevocationPublished)); n != 1 {
		t.Errorf("a repeat wrote %d revocation records, want one", n)
	}
	if n := h.revoker.killsOf("sess-1", ""); n != 2 {
		t.Errorf("the kill was published %d times, want twice: once, and again for the repeat", n)
	}
	if h.ledger(tenant) != 1 {
		t.Errorf("%d ledger rows, want one", h.ledger(tenant))
	}
	// Different words under the same id are still the same action.
	retry := a
	retry.ReasonText, retry.ExternalRef = "different words", "INC-2"
	if got := h.mustDo(retry); !same(got, first) {
		t.Errorf("a retry with other words = %+v, want %+v", got, first)
	}

	// A different action under the same id is a conflict, and changes nothing.
	for name, other := range map[string]ext.Action{
		"another session": {Kind: ext.ActionKillSession, ActionID: "i-1", SessionID: "sess-2"},
		"another kind":    {Kind: ext.ActionLockSubject, ActionID: "i-1", SubjectID: alice},
		"a subject":       {Kind: ext.ActionKillSession, ActionID: "i-1", SubjectID: alice},
	} {
		_, err := h.do(other)
		if ext.KindOf(err) != ext.KindConflict {
			t.Errorf("%s under a used id: err = %v, want KindConflict", name, err)
		}
	}
	if st, _ := h.store.Subjects().Standing(t.Context(), tenant, alice); st.Lock != nil {
		t.Error("a conflicting action locked alice")
	}

	// The id is the tenant's: another tenant may use it for its own action.
	if _, err := h.exec.Execute(t.Context(), ext.Action{Tenant: ext.Tenant(other), Kind: ext.ActionLockSubject, ActionID: "i-1", SubjectID: alice}); err != nil {
		t.Errorf("the same id in another tenant: %v", err)
	}
	h.verify()
}

// A repeat of a lock re-sends the kill only while the lock stands: a late
// duplicate must not end the sessions of a subject who was unlocked since.
func TestARepeatedLockSendsNothingOnceTheLockIsLifted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	lock := ext.Action{Kind: ext.ActionLockSubject, ActionID: "d-1", SubjectID: alice}
	h.mustDo(lock)
	before := h.revoker.killsOf("", alice)
	h.mustDo(lock)
	if h.revoker.killsOf("", alice) <= before {
		t.Error("a repeat of a standing lock did not re-send the kill")
	}

	h.mustDo(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "d-2", SubjectID: alice})
	before = h.revoker.killsOf("", alice)
	res := h.mustDo(lock)
	if res.Affected != 1 {
		t.Errorf("the repeat returned %+v, want the first result", res)
	}
	if h.revoker.killsOf("", alice) != before {
		t.Error("a late duplicate of a lock ended the sessions of a subject who was unlocked since")
	}
	h.verify()
}

// At least a hundred concurrent pairs, each under a fresh id, each have one
// effect.
func TestConcurrentPairsUnderOneIDHaveOneEffect(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const pairs = 100
	for i := range pairs {
		id := fmt.Sprintf("user-%03d@example.com", i)
		if err := h.store.Subjects().Upsert(t.Context(), tenant, store.Subject{ID: id, Source: "local"}); err != nil {
			t.Fatal(err)
		}
	}

	type outcome struct {
		res ext.ActionResult
		err error
	}
	results := make([][2]outcome, pairs)
	var wg sync.WaitGroup
	gate := make(chan struct{}, 16)
	for i := range pairs {
		for j := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				gate <- struct{}{}
				defer func() { <-gate }()
				res, err := h.exec.Execute(t.Context(), ext.Action{
					Tenant: ext.Tenant(tenant), Kind: ext.ActionLockSubject, ActionID: fmt.Sprintf("pair-%03d", i),
					SubjectID: fmt.Sprintf("user-%03d@example.com", i), ExternalRef: "INC-pairs",
				})
				results[i][j] = outcome{res, err}
			}()
		}
	}
	wg.Wait()

	for i, pair := range results {
		for j, o := range pair {
			if o.err != nil {
				t.Fatalf("pair %d call %d: %v", i, j, o.err)
			}
		}
		if !same(pair[0].res, pair[1].res) || pair[0].res.Affected != 1 {
			t.Fatalf("pair %d answered %+v and %+v, want the same first result", i, pair[0].res, pair[1].res)
		}
	}
	if n := h.ledger(tenant); n != pairs {
		t.Errorf("%d ledger rows for %d pairs", n, pairs)
	}
	if n := len(h.records(tenant, audit.EventSubjectLocked)); n != pairs {
		t.Errorf("%d lock records for %d pairs: a pair wrote its record twice", n, pairs)
	}
	for i := range pairs {
		if st, _ := h.store.Subjects().Standing(t.Context(), tenant, fmt.Sprintf("user-%03d@example.com", i)); st.Lock == nil {
			t.Errorf("subject %d is not locked", i)
		}
	}
	h.verify()
}

// With the stream refusing to publish, the answer is KindUnavailable and the
// state change stands. Once the stream recovers, the same id delivers the kill
// and returns the first result — for every kind that publishes.
func TestAStreamThatRefusesLeavesTheActDoneAndTheRetryDeliversIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.authorize(alice, "sess-1")
	h.grantFor(tenant, "g-1", bob, "sess-g")

	h.revoker.fail.Store(true)
	actions := []ext.Action{
		{Kind: ext.ActionKillSession, ActionID: "s-1", SessionID: "sess-1", ExternalRef: "INC-1"},
		{Kind: ext.ActionLockSubject, ActionID: "s-2", SubjectID: alice},
		{Kind: ext.ActionQuarantineTarget, ActionID: "s-3", TargetID: "t-db"},
		{Kind: ext.ActionRevokeGrant, ActionID: "s-4", GrantID: "g-1"},
	}
	firsts := make([]ext.ActionResult, len(actions))
	for i, a := range actions {
		res, err := h.do(a)
		if ext.KindOf(err) != ext.KindUnavailable {
			t.Fatalf("%s with the stream down: err = %v, want KindUnavailable", a.Kind, err)
		}
		if res.Affected != 1 {
			t.Errorf("%s: the act was made and the result says %+v", a.Kind, res)
		}
		firsts[i] = res
	}

	// The state changes stand.
	if st, _ := h.store.Subjects().Standing(t.Context(), tenant, alice); st.Lock == nil {
		t.Error("the lock was not kept")
	}
	if _, err := h.store.TargetQuarantines().Get(t.Context(), tenant, "t-db"); err != nil {
		t.Errorf("the quarantine was not kept: %v", err)
	}
	if g, _ := h.store.Grants().Get(t.Context(), tenant, "g-1"); g.RevokedAt.IsZero() {
		t.Error("the revocation was not kept")
	}
	if h.ledger(tenant) != len(actions) {
		t.Errorf("%d ledger rows, want %d: the retry has to find its action", h.ledger(tenant), len(actions))
	}
	// Nothing reached the stream, and the kill's record is still owed: it says
	// "published".
	if len(h.revoker.killed()) != 0 {
		t.Error("a kill was published through a stream that refused")
	}
	if n := len(h.records(tenant, audit.EventRevocationPublished)); n != 0 {
		t.Errorf("%d revocation records were written for a kill that was never published", n)
	}

	h.revoker.fail.Store(false)
	for i, a := range actions {
		res, err := h.do(a)
		if err != nil {
			t.Fatalf("the retry of %s: %v", a.Kind, err)
		}
		if !same(res, firsts[i]) {
			t.Errorf("the retry of %s = %+v, want the first result %+v", a.Kind, res, firsts[i])
		}
	}
	if h.revoker.killsOf("sess-1", "") != 1 {
		t.Error("the retry did not deliver the session kill")
	}
	if h.revoker.killsOf("", alice) == 0 {
		t.Error("the retry did not deliver the lock's kill")
	}
	if h.revoker.killsOf("sess-g", "") == 0 {
		t.Error("the retry did not deliver the grant's session kill")
	}
	recs := h.records(tenant, audit.EventRevocationPublished)
	if len(recs) != 1 {
		t.Fatalf("%d revocation records after the retry, want the one the first execution owed", len(recs))
	}
	wantsAction(t, recs[0], "s-1", "kill_session", "INC-1", "")

	// And a third time writes nothing more.
	h.mustDo(actions[0])
	if n := len(h.records(tenant, audit.EventRevocationPublished)); n != 1 {
		t.Errorf("a third call wrote another record: %d", n)
	}
	h.verify()
}

func TestNothingIsExecutedOnceStopHasBegun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	runner := action.NewRunner(&fakeHandler{}, h.exec, nil)
	if err := runner.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	res, err := h.do(ext.Action{Kind: ext.ActionLockSubject, ActionID: "z-1", SubjectID: alice})
	if ext.KindOf(err) != ext.KindUnavailable || res.Affected != 0 {
		t.Errorf("Execute after Stop = %+v, %v; want KindUnavailable", res, err)
	}
	if st, _ := h.store.Subjects().Standing(t.Context(), tenant, alice); st.Lock != nil {
		t.Error("an action executed after Stop")
	}
}
