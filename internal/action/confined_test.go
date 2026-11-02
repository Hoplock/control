// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package action_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/action"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/standing"
	"github.com/hoplock/control/internal/store"
)

// A confined inbound action (0041, M28): Control checks what an action reaches
// against the scope its handler hands it, inside the act's own transaction and
// from its own data, and an unlock with IfPlacedBy lifts only the lock its
// action placed.

// target stores a target in the harness's tenant.
func (h *harness) target(id, hostname string, labels map[string]string) {
	h.t.Helper()
	if err := h.store.Targets().Upsert(h.t.Context(), tenant, store.Target{ID: id, Hostname: hostname, Zone: "edge", Labels: labels}); err != nil {
		h.t.Fatalf("upsert target %s: %v", id, err)
	}
}

// hop records one decision of a session, as a hop of a chain would.
func (h *harness) hop(t store.Tenant, session, subject, targetID string) {
	h.t.Helper()
	if err := h.store.Decisions().Insert(h.t.Context(), t, store.Decision{
		ID: "d-" + session + "-" + subject + "-" + targetID, SubjectID: subject, TargetID: targetID, InputsDigest: "x",
		Effect: store.DecisionEffectAllow, ProxyID: "proxy-1", SessionID: session, DecidedAt: time.Now(),
	}); err != nil {
		h.t.Fatalf("insert decision: %v", err)
	}
}

// grant stores a grant with a selector.
func (h *harness) grant(id, subject string, targets []string, labels map[string]string, zones []string) {
	h.t.Helper()
	if err := h.store.Grants().Insert(h.t.Context(), tenant, store.Grant{
		ID: id, SubjectID: subject, Scope: "break-fix", Origin: store.GrantOriginManual,
		ScopeTargets: targets, ScopeLabels: labels, ScopeZones: zones,
		NotBefore: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour),
		CreatedBy: store.GrantActor{Principal: "p-admin"}, Reason: "incident",
	}); err != nil {
		h.t.Fatalf("insert grant %s: %v", id, err)
	}
}

// wantOutOfScope executes an action that must be refused as out of scope, and
// asserts it changed nothing, that its error names the scope field and no id,
// and that Control's own record names the field and what lay outside.
func (h *harness) wantOutOfScope(name string, a ext.Action, field, outside string) store.AuditRecord {
	h.t.Helper()
	before := len(h.records(tenant, audit.EventActionRefused))
	ledger, kills, invs := h.ledger(tenant), len(h.revoker.killed()), len(h.revoker.invalidated())

	res, err := h.do(a)
	if ext.KindOf(err) != ext.KindDenied || res.ReasonCode != action.ReasonOutOfScope || res.Affected != 0 {
		h.t.Errorf("%s: err = %v, result = %+v; want KindDenied with %q", name, err, res, action.ReasonOutOfScope)
		return store.AuditRecord{}
	}
	if !contains(err, "within."+field) {
		h.t.Errorf("%s: the error %q does not name within.%s", name, err, field)
	}
	for _, id := range []string{outside, a.SubjectID, a.SessionID, a.TargetID, a.GrantID} {
		if id != "" && !strings.HasPrefix(id, "all_") && strings.Contains(err.Error(), id) {
			h.t.Errorf("%s: the error %q names %q, which the requester may not learn about", name, err, id)
		}
	}
	if h.ledger(tenant) != ledger || len(h.revoker.killed()) != kills || len(h.revoker.invalidated()) != invs {
		h.t.Errorf("%s: an action outside its scope left a ledger row or published something", name)
	}
	recs := h.records(tenant, audit.EventActionRefused)
	if len(recs) != before+1 {
		h.t.Errorf("%s: %d action.refused records, want one more than %d", name, len(recs), before)
		return store.AuditRecord{}
	}
	for key, want := range map[string]string{
		"operator.code": action.ReasonOutOfScope, "operator.scope_field": field, "operator.outside": outside,
	} {
		if got := recs[0].Attributes[key]; got != want {
			h.t.Errorf("%s: the refusal record's %s = %q, want %q", name, key, got, want)
		}
	}
	return recs[0]
}

// wantInside executes an action its scope admits.
func (h *harness) wantInside(name string, a ext.Action) ext.ActionResult {
	h.t.Helper()
	res, err := h.do(a)
	if err != nil {
		h.t.Errorf("%s: %v, want it executed inside its scope", name, err)
	}
	return res
}

func within(subjects, targets []string, labels map[string]string) *ext.ActionScope {
	return &ext.ActionScope{SubjectIDs: subjects, TargetIDs: targets, TargetLabels: labels}
}

func ids(v ...string) []string { return v }

func TestAConfinedActionOutsideItsScopeChangesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.target("t-web", "web01.example.com", map[string]string{"env": "dev"})
	h.hop(tenant, "sess-1", alice, "t-db")
	h.grant("g-1", alice, ids("db01.example.com"), nil, nil)
	prod, dev := map[string]string{"env": "prod"}, map[string]string{"env": "dev"}

	type row struct {
		name           string
		a              ext.Action
		in, out        *ext.ActionScope
		field, outside string
	}
	kill := ext.Action{Kind: ext.ActionKillSession, SessionID: "sess-1"}
	killBy := ext.Action{Kind: ext.ActionKillSession, SubjectID: alice}
	lock := ext.Action{Kind: ext.ActionLockSubject, SubjectID: alice}
	unlock := ext.Action{Kind: ext.ActionUnlockSubject, SubjectID: alice}
	revoke := ext.Action{Kind: ext.ActionRevokeGrant, GrantID: "g-1"}
	quarantine := ext.Action{Kind: ext.ActionQuarantineTarget, TargetID: "t-db"}
	rows := []row{
		{"a kill by session, by subject", kill, within(ids(alice), nil, nil), within(ids(bob), nil, nil), "subject_ids", alice},
		{"a kill by session, by target", kill, within(nil, ids("t-db"), nil), within(nil, ids("t-web"), nil), "target_ids", "t-db"},
		{"a kill by session, by label", kill, within(nil, nil, prod), within(nil, nil, dev), "target_labels", "t-db"},
		{"a kill by subject", killBy, within(ids(alice, bob), nil, nil), within(ids(bob), nil, nil), "subject_ids", alice},
		{"a lock", lock, within(ids(alice), nil, nil), within(ids(bob), nil, nil), "subject_ids", alice},
		{"an unlock", unlock, within(ids(alice), nil, nil), within(ids(bob), nil, nil), "subject_ids", alice},
		{"a revocation, by subject", revoke, within(ids(alice), nil, nil), within(ids(bob), nil, nil), "subject_ids", alice},
		{"a revocation, by target", revoke, within(nil, ids("t-db"), nil), within(nil, ids("t-web"), nil), "target_ids", "t-db"},
		{"a revocation, by label", revoke, within(nil, nil, prod), within(nil, nil, dev), "target_labels", "t-db"},
		{"a quarantine, by target", quarantine, within(nil, ids("t-db"), nil), within(nil, ids("t-web"), nil), "target_ids", "t-db"},
		{"a quarantine, by label", quarantine, within(nil, nil, prod), within(nil, nil, dev), "target_labels", "t-db"},
	}

	// Every action outside first, so nothing an inside one did can explain a
	// change.
	for i, r := range rows {
		a := r.a
		a.ActionID, a.Within = fmt.Sprintf("out-%d", i), r.out
		h.wantOutOfScope(r.name, a, r.field, r.outside)
	}
	if st, _ := h.store.Subjects().Standing(t.Context(), tenant, alice); st.Lock != nil {
		t.Error("an action outside its scope locked alice")
	}
	if g, _ := h.store.Grants().Get(t.Context(), tenant, "g-1"); !g.RevokedAt.IsZero() {
		t.Error("an action outside its scope revoked the grant")
	}
	if _, err := h.store.TargetQuarantines().Get(t.Context(), tenant, "t-db"); !store.IsNotFound(err) {
		t.Errorf("an action outside its scope quarantined the target: %v", err)
	}
	if n := len(h.records(tenant, audit.EventActionUnchanged)); n != 0 {
		t.Errorf("refused actions wrote %d action.unchanged records", n)
	}

	for i, r := range rows {
		a := r.a
		a.ActionID, a.Within = fmt.Sprintf("in-%d", i), r.in
		h.wantInside(r.name, a)
	}
	if h.revoker.killsOf("sess-1", "") == 0 || h.revoker.killsOf("", alice) == 0 {
		t.Error("a kill inside its scope published nothing")
	}
	if g, _ := h.store.Grants().Get(t.Context(), tenant, "g-1"); g.RevokedAt.IsZero() {
		t.Error("a revocation inside its scope did not revoke the grant")
	}
	if _, err := h.store.TargetQuarantines().Get(t.Context(), tenant, "t-db"); err != nil {
		t.Errorf("a quarantine inside its scope did not quarantine: %v", err)
	}

	// A chained session reaches every hop's target, and a hop to a hostname
	// with no target row reaches a target no scope of ids or labels admits.
	h.hop(tenant, "sess-chain", alice, "t-db")
	h.hop(tenant, "sess-chain", alice, "t-web")
	h.hop(tenant, "sess-unrecorded", alice, "t-db")
	h.hop(tenant, "sess-unrecorded", alice, "")
	chain := ext.Action{Kind: ext.ActionKillSession, SessionID: "sess-chain"}
	unrecorded := ext.Action{Kind: ext.ActionKillSession, SessionID: "sess-unrecorded"}
	for i, c := range []struct {
		name           string
		a              ext.Action
		w              *ext.ActionScope
		field, outside string
	}{
		{"the second hop of a chain", chain, within(nil, ids("t-db"), nil), "target_ids", "t-web"},
		{"the second hop of a chain, by label", chain, within(nil, nil, prod), "target_labels", "t-web"},
		{"a hop to an unrecorded target", unrecorded, within(nil, ids("t-db", "t-web"), nil), "target_ids", "unrecorded_target"},
		{"a hop to an unrecorded target, by label", unrecorded, within(nil, nil, prod), "target_labels", "unrecorded_target"},
	} {
		a := c.a
		a.ActionID, a.Within = fmt.Sprintf("hop-%d", i), c.w
		h.wantOutOfScope(c.name, a, c.field, c.outside)
	}
	h.wantInside("every hop inside", ext.Action{Kind: ext.ActionKillSession, ActionID: "hop-in", SessionID: "sess-chain", Within: within(ids(alice), ids("t-db", "t-web"), nil)})
	h.wantInside("an unrecorded hop under a subject scope", ext.Action{Kind: ext.ActionKillSession, ActionID: "hop-in-2", SessionID: "sess-unrecorded", Within: within(ids(alice), nil, nil)})
	h.verify()
}

// Item 3: a kind that reaches a dimension whole fails a scope that confines
// that dimension, whatever the other dimension admits.
func TestADimensionReachedWholeFailsAScopeThatConfinesIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	prod := map[string]string{"env": "prod"}

	for i, c := range []struct {
		name           string
		a              ext.Action
		w              *ext.ActionScope
		field, outside string
	}{
		{"a lock under a target scope", ext.Action{Kind: ext.ActionLockSubject, SubjectID: alice}, within(ids(alice), ids("t-db"), nil), "target_ids", "all_targets"},
		{"an unlock under a target scope", ext.Action{Kind: ext.ActionUnlockSubject, SubjectID: alice}, within(nil, ids("t-db"), nil), "target_ids", "all_targets"},
		{"a kill by subject under a target scope", ext.Action{Kind: ext.ActionKillSession, SubjectID: alice}, within(nil, ids("t-db"), nil), "target_ids", "all_targets"},
		{"a lock under a label scope", ext.Action{Kind: ext.ActionLockSubject, SubjectID: alice}, within(nil, nil, prod), "target_labels", "all_targets"},
		{"an unlock under a label scope", ext.Action{Kind: ext.ActionUnlockSubject, SubjectID: alice}, within(ids(alice), nil, prod), "target_labels", "all_targets"},
		{"a kill by subject under a label scope", ext.Action{Kind: ext.ActionKillSession, SubjectID: alice}, within(nil, nil, prod), "target_labels", "all_targets"},
		{"a quarantine under a subject scope", ext.Action{Kind: ext.ActionQuarantineTarget, TargetID: "t-db"}, within(ids(alice), ids("t-db"), prod), "subject_ids", "all_subjects"},
	} {
		a := c.a
		a.ActionID, a.Within = fmt.Sprintf("w-%d", i), c.w
		h.wantOutOfScope(c.name, a, c.field, c.outside)
	}
	h.verify()
}

// Item 4: a grant's selector is inside a target scope only when it cannot
// reach outside it.
func TestAGrantSelectorIsInsideATargetScopeOnlyWhenItCannotReachOutside(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.target("t-db2", "db02.example.com", map[string]string{"env": "prod"})
	h.target("t-up", "DB03.Example.com", map[string]string{"env": "prod"})
	h.target("t-web", "web01.example.com", map[string]string{"env": "dev"})
	prod := map[string]string{"env": "prod"}

	h.grant("g-labels", alice, nil, prod, nil)
	h.grant("g-hosts", alice, ids("db01.example.com", "db02.example.com"), nil, nil)
	h.grant("g-mixed", alice, ids("db01.example.com", "web01.example.com"), nil, nil)
	h.grant("g-unrowed", alice, ids("db01.example.com", "ghost.example.com"), nil, nil)
	h.grant("g-wild", alice, ids("*.example.com"), nil, nil)
	h.grant("g-wild-prod", alice, ids("*.example.com"), prod, nil)
	h.grant("g-zones", alice, nil, nil, ids("edge"))
	h.grant("g-empty", alice, nil, nil, nil)
	h.grant("g-case", alice, ids("db03.example.com"), nil, nil)
	h.grant("g-hosts-2", alice, ids("db01.example.com", "db02.example.com"), nil, nil)

	labelled := within(nil, nil, prod)
	superset := within(nil, ids("t-db", "t-db2", "t-web"), nil)
	subset := within(nil, ids("t-db"), nil)
	cases := []struct {
		name           string
		grant          string
		w              *ext.ActionScope
		field, outside string // empty field: inside
	}{
		// Labels.
		{"labels the selector requires itself", "g-labels", labelled, "", ""},
		{"targets that all carry the label", "g-hosts", labelled, "", ""},
		{"a listed target that does not carry it", "g-mixed", labelled, "target_labels", "t-web"},
		{"a listed target with no row", "g-unrowed", labelled, "target_labels", "ghost.example.com"},
		{"a wildcard", "g-wild", labelled, "target_labels", "*.example.com"},
		{"a wildcard the selector's own labels bound", "g-wild-prod", labelled, "", ""},
		{"zones alone, under labels", "g-zones", labelled, "target_labels", "all_targets"},
		{"an empty selector, under labels", "g-empty", labelled, "target_labels", "all_targets"},
		{"a hostname matched as DNS matches it", "g-case", labelled, "", ""},
		// Target ids.
		{"TargetIDs a superset", "g-hosts", superset, "", ""},
		{"TargetIDs a subset", "g-hosts-2", subset, "target_ids", "t-db2"},
		{"ScopeTargets empty, labels alone", "g-labels", superset, "target_ids", "all_targets"},
		{"zones alone, under ids", "g-zones", superset, "target_ids", "all_targets"},
		{"an empty selector, under ids", "g-empty", superset, "target_ids", "all_targets"},
		{"a wildcard, under ids", "g-wild-prod", superset, "target_ids", "*.example.com"},
		{"a listed target with no row, under ids", "g-unrowed", superset, "target_ids", "ghost.example.com"},
		{"a hostname matched as DNS matches it, under ids", "g-case", within(nil, ids("t-up"), nil), "", ""},
	}
	// Outside first: an inside revocation revokes the grant.
	for i, c := range cases {
		if c.field == "" {
			continue
		}
		h.wantOutOfScope(c.name, ext.Action{Kind: ext.ActionRevokeGrant, ActionID: fmt.Sprintf("g-out-%d", i), GrantID: c.grant, Within: c.w}, c.field, c.outside)
	}
	for i, c := range cases {
		if c.field != "" {
			continue
		}
		h.wantInside(c.name, ext.Action{Kind: ext.ActionRevokeGrant, ActionID: fmt.Sprintf("g-in-%d", i), GrantID: c.grant, Within: c.w})
		if g, _ := h.store.Grants().Get(t.Context(), tenant, c.grant); g.RevokedAt.IsZero() {
			t.Errorf("%s: the grant inside its scope was not revoked", c.name)
		}
	}
	for _, id := range []string{"g-mixed", "g-unrowed", "g-wild", "g-zones", "g-empty", "g-hosts-2"} {
		if g, _ := h.store.Grants().Get(t.Context(), tenant, id); !g.RevokedAt.IsZero() {
			t.Errorf("%s was revoked by an action outside its scope", id)
		}
	}
	h.verify()
}

// Item 5: a confined requester learns nothing outside its scope. An unknown
// selector is out of scope unless the scope names it by id alone, and no
// error names an id the scope does not.
func TestAConfinedRequesterLearnsNothingOutsideItsScope(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.grantFor(other, "g-theirs", alice)
	h.hop(other, "sess-theirs", alice, "t-db")
	prod := map[string]string{"env": "prod"}

	for i, c := range []struct {
		name                     string
		a                        ext.Action
		field, outside, selector string
	}{
		{"an unknown subject outside SubjectIDs", ext.Action{Kind: ext.ActionLockSubject, SubjectID: "nobody", Within: within(ids(alice), nil, nil)}, "subject_ids", "nobody", ""},
		{"an unknown subject to kill outside SubjectIDs", ext.Action{Kind: ext.ActionKillSession, SubjectID: "nobody", Within: within(ids(alice), nil, nil)}, "subject_ids", "nobody", ""},
		{"an unknown session", ext.Action{Kind: ext.ActionKillSession, SessionID: "sess-none", Within: within(ids(alice), nil, nil)}, "subject_ids", "sess-none", "session_id"},
		{"another tenant's session", ext.Action{Kind: ext.ActionKillSession, SessionID: "sess-theirs", Within: within(nil, ids("t-db"), nil)}, "target_ids", "sess-theirs", "session_id"},
		{"an unknown grant", ext.Action{Kind: ext.ActionRevokeGrant, GrantID: "g-none", Within: within(nil, nil, prod)}, "target_labels", "g-none", "grant_id"},
		{"another tenant's grant", ext.Action{Kind: ext.ActionRevokeGrant, GrantID: "g-theirs", Within: within(ids(alice), nil, nil)}, "subject_ids", "g-theirs", "grant_id"},
		{"an unknown target outside TargetIDs", ext.Action{Kind: ext.ActionQuarantineTarget, TargetID: "t-none", Within: within(nil, ids("t-db"), nil)}, "target_ids", "t-none", ""},
		{"an unknown target under labels", ext.Action{Kind: ext.ActionQuarantineTarget, TargetID: "t-none", Within: within(nil, ids("t-none"), prod)}, "target_labels", "t-none", "target_id"},
	} {
		a := c.a
		a.ActionID = fmt.Sprintf("l-%d", i)
		rec := h.wantOutOfScope(c.name, a, c.field, c.outside)
		if got := rec.Attributes["operator.unknown_selector"]; got != c.selector {
			t.Errorf("%s: the record's unknown_selector = %q, want %q", c.name, got, c.selector)
		}
	}

	// An id the scope names is answered as the unconfined requester is
	// answered: it is not known.
	for name, a := range map[string]ext.Action{
		"an unknown subject inside SubjectIDs":       {Kind: ext.ActionLockSubject, ActionID: "n-1", SubjectID: "nobody", Within: within(ids("nobody"), nil, nil)},
		"an unknown subject to unlock inside it":     {Kind: ext.ActionUnlockSubject, ActionID: "n-2", SubjectID: "nobody", Within: within(ids("nobody"), nil, nil)},
		"an unknown target inside TargetIDs":         {Kind: ext.ActionQuarantineTarget, ActionID: "n-3", TargetID: "t-none", Within: within(nil, ids("t-none"), nil)},
		"another tenant's subject inside SubjectIDs": {Kind: ext.ActionKillSession, ActionID: "n-4", SubjectID: "carol@example.com", Within: within(ids("carol@example.com"), nil, nil)},
	} {
		if _, err := h.do(a); ext.KindOf(err) != ext.KindNotFound {
			t.Errorf("%s: err = %v, want KindNotFound", name, err)
		}
	}
	h.verify()
}

// Item 6: every Execute is checked against the scope it carries, a repeat
// included, before the ledger answers and before anything is sent again.
func TestARepeatIsCheckedAgainstItsOwnScope(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.hop(tenant, "sess-1", alice, "t-db")

	kill := ext.Action{Kind: ext.ActionKillSession, ActionID: "k-1", SessionID: "sess-1", Within: within(ids(alice), nil, nil)}
	first := h.mustDo(kill)
	if n := h.revoker.killsOf("sess-1", ""); n != 1 {
		t.Fatalf("the first kill published %d times", n)
	}

	// Another credential repeats the id under a scope that does not admit the
	// session: refused, and the kill is not sent again.
	repeat := kill
	repeat.Within = within(ids(bob), nil, nil)
	h.wantOutOfScope("a repeat outside its scope", repeat, "subject_ids", alice)
	if n := h.revoker.killsOf("sess-1", ""); n != 1 {
		t.Errorf("a repeat outside its scope sent the kill again: %d publications", n)
	}

	// A repeat inside its own scope, or with none, is the first result.
	for name, w := range map[string]*ext.ActionScope{"inside": within(ids(alice, bob), nil, nil), "unconfined": nil} {
		again := kill
		again.Within = w
		if got := h.mustDo(again); !same(got, first) {
			t.Errorf("a repeat %s its scope = %+v, want %+v", name, got, first)
		}
	}

	// A lock repeated under a target scope is refused whole, and its kill is
	// not sent again.
	lock := ext.Action{Kind: ext.ActionLockSubject, ActionID: "l-1", SubjectID: alice, Within: within(ids(alice), nil, nil)}
	h.mustDo(lock)
	before := h.revoker.killsOf("", alice)
	lock.Within = within(ids(alice), ids("t-db"), nil)
	h.wantOutOfScope("a lock repeated under a target scope", lock, "target_ids", "all_targets")
	if h.revoker.killsOf("", alice) != before {
		t.Error("a refused repeat of a lock sent its kill again")
	}
	h.verify()
}

// Item 7: a scope must confine something, within its bounds.
func TestAScopeMustConfineSomethingWithinItsBounds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	many := make([]string, 1001)
	for i := range many {
		many[i] = fmt.Sprintf("id-%d", i)
	}
	labels := map[string]string{}
	for i := range 65 {
		labels[fmt.Sprintf("k%d", i)] = "v"
	}
	lock := func(w *ext.ActionScope) ext.Action {
		return ext.Action{Kind: ext.ActionLockSubject, SubjectID: alice, Within: w}
	}
	cases := []struct {
		name  string
		a     ext.Action
		field string
	}{
		{"an empty scope", lock(&ext.ActionScope{}), "within"},
		{"a scope of empty lists", lock(&ext.ActionScope{SubjectIDs: []string{}, TargetIDs: []string{}, TargetLabels: map[string]string{}}), "within"},
		{"too many subjects", lock(within(many, nil, nil)), "within.subject_ids"},
		{"a blank subject", lock(within(ids(alice, " "), nil, nil)), "within.subject_ids"},
		{"a subject with a newline", lock(within(ids("a\nb"), nil, nil)), "within.subject_ids"},
		{"a subject too long", lock(within(ids(strings.Repeat("s", 300)), nil, nil)), "within.subject_ids"},
		{"too many targets", lock(within(nil, many, nil)), "within.target_ids"},
		{"a target with a control character", lock(within(nil, ids("t\x00"), nil)), "within.target_ids"},
		{"too many labels", lock(within(nil, nil, labels)), "within.target_labels"},
		{"a blank label key", lock(within(nil, nil, map[string]string{" ": "v"})), "within.target_labels"},
		{"a blank label value", lock(within(nil, nil, map[string]string{"env": ""})), "within.target_labels"},
		{"a label on two lines", lock(within(nil, nil, map[string]string{"env": "prod\nx"})), "within.target_labels"},
		{"a label too long", lock(within(nil, nil, map[string]string{"env": strings.Repeat("v", 257)})), "within.target_labels"},
		{"if_placed_by on a lock", ext.Action{Kind: ext.ActionLockSubject, SubjectID: alice, IfPlacedBy: "A"}, "if_placed_by"},
		{"if_placed_by on a kill", ext.Action{Kind: ext.ActionKillSession, SubjectID: alice, IfPlacedBy: "A"}, "if_placed_by"},
		{"if_placed_by on a revocation", ext.Action{Kind: ext.ActionRevokeGrant, GrantID: "g", IfPlacedBy: "A"}, "if_placed_by"},
		{"if_placed_by on a quarantine", ext.Action{Kind: ext.ActionQuarantineTarget, TargetID: "t-db", IfPlacedBy: "A"}, "if_placed_by"},
		{"if_placed_by with a newline", ext.Action{Kind: ext.ActionUnlockSubject, SubjectID: alice, IfPlacedBy: "A\nB"}, "if_placed_by"},
		{"if_placed_by too long", ext.Action{Kind: ext.ActionUnlockSubject, SubjectID: alice, IfPlacedBy: strings.Repeat("a", 300)}, "if_placed_by"},
	}
	for i, c := range cases {
		a := c.a
		a.ActionID = fmt.Sprintf("v-%d", i)
		before := len(h.records(tenant, audit.EventActionRefused))
		_, err := h.do(a)
		if ext.KindOf(err) != ext.KindInvalid || !contains(err, c.field) {
			t.Errorf("%s: err = %v, want KindInvalid naming %q", c.name, err, c.field)
			continue
		}
		recs := h.records(tenant, audit.EventActionRefused)
		if len(recs) != before+1 || recs[0].Attributes["operator.field"] != c.field {
			t.Errorf("%s: the refusal was not recorded naming %q", c.name, c.field)
		}
	}
	// At the bounds, a scope is accepted.
	atBound := within(append(many[:999:999], alice), many[:1000], nil)
	for k := range labels {
		if len(labels) == 64 {
			break
		}
		delete(labels, k)
	}
	if _, err := h.do(ext.Action{Kind: ext.ActionKillSession, ActionID: "v-ok", SubjectID: alice, Within: atBound}); ext.KindOf(err) != ext.KindDenied {
		t.Errorf("a scope at its bounds: %v, want it validated and checked (KindDenied: a target scope refuses a kill by subject)", err)
	}
	if _, err := h.do(ext.Action{Kind: ext.ActionQuarantineTarget, ActionID: "v-ok-2", TargetID: "t-db", Within: within(nil, nil, labels)}); ext.KindOf(err) != ext.KindDenied {
		t.Errorf("64 labels: %v, want them validated and checked", err)
	}
	if n := h.ledger(tenant); n != 0 {
		t.Errorf("refused actions left %d ledger rows", n)
	}
	h.verify()
}

// Item 8: an unlock with IfPlacedBy lifts only a lock that action of this
// handler placed, and leaves every other standing.
func TestAConfinedUnlockLiftsOnlyTheLockItsActionPlaced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()
	operator := standing.Actor{Principal: "p-operator", Subject: "soc@example.com"}
	carol := "carol@example.com"
	if err := h.store.Subjects().Upsert(ctx, tenant, store.Subject{ID: carol, Source: "local"}); err != nil {
		t.Fatal(err)
	}

	// The lock this action placed is lifted.
	h.mustDo(ext.Action{Kind: ext.ActionLockSubject, ActionID: "A", SubjectID: alice})
	res := h.mustDo(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "exp-A", SubjectID: alice, IfPlacedBy: "A"})
	if res.Affected != 1 || res.ReasonCode != "" {
		t.Errorf("the expiry of its own lock = %+v, want it lifted", res)
	}
	if _, err := h.store.SubjectLocks().Get(ctx, tenant, alice); !store.IsNotFound(err) {
		t.Errorf("the lock its action placed still stands: %v", err)
	}
	if recs := h.records(tenant, audit.EventSubjectUnlocked); len(recs) != 1 {
		t.Errorf("%d operator.subject.unlocked records, want one", len(recs))
	}

	// Each lock somebody else placed is left standing.
	if _, err := h.standing.Lock(ctx, tenant, operator, standing.LockRequest{SubjectID: bob, Reason: "Suspended by the SOC."}); err != nil {
		t.Fatal(err)
	}
	h.mustDo(ext.Action{Kind: ext.ActionLockSubject, ActionID: "B", SubjectID: alice})
	other := store.SubjectLock{
		SubjectID: carol, LockedAt: time.Now().UTC(), ActionID: "C",
		LockedBy: store.StandingActor{Principal: action.PrincipalPrefix + "another/soar"},
	}
	if _, _, err := h.store.SubjectLocks().Place(ctx, tenant, other); err != nil {
		t.Fatal(err)
	}
	for i, c := range []struct{ name, subject, placedBy string }{
		{"a lock an operator placed", bob, "A"},
		{"a lock another action id placed", alice, "A"},
		{"a lock under the same action id by another provider", carol, "C"},
	} {
		before := len(h.records(tenant, audit.EventActionUnchanged))
		res, err := h.do(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: fmt.Sprintf("x-%d", i), SubjectID: c.subject, IfPlacedBy: c.placedBy})
		if err != nil || res.Affected != 0 || res.ReasonCode != action.ReasonNotThisLock {
			t.Errorf("%s: result = %+v, err = %v; want Affected 0, %q, nil", c.name, res, err, action.ReasonNotThisLock)
		}
		if _, err := h.store.SubjectLocks().Get(ctx, tenant, c.subject); err != nil {
			t.Errorf("%s: the lock was lifted: %v", c.name, err)
		}
		recs := h.records(tenant, audit.EventActionUnchanged)
		if len(recs) != before+1 || recs[0].Attributes["operator.outcome_reason"] != action.ReasonNotThisLock {
			t.Errorf("%s: want one action.unchanged naming %q", c.name, action.ReasonNotThisLock)
		} else {
			wantsAction(t, recs[0], fmt.Sprintf("x-%d", i), "unlock_subject", "", "")
		}
	}
	if recs := h.records(tenant, audit.EventSubjectUnlocked); len(recs) != 1 {
		t.Errorf("%d operator.subject.unlocked records, want only the first", len(recs))
	}

	// No lock at all is not_locked, and an unknown subject is not found, as
	// without the condition.
	if _, err := h.standing.Unlock(ctx, tenant, operator, standing.LiftRequest{ID: bob}); err != nil {
		t.Fatal(err)
	}
	if res, err := h.do(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "y-1", SubjectID: bob, IfPlacedBy: "A"}); err != nil || res.ReasonCode != action.ReasonNotLocked {
		t.Errorf("an unlock with nothing standing = %+v, %v; want %q", res, err, action.ReasonNotLocked)
	}
	if _, err := h.do(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "y-2", SubjectID: "nobody", IfPlacedBy: "A"}); ext.KindOf(err) != ext.KindNotFound {
		t.Errorf("an unlock of an unknown subject: %v, want KindNotFound", err)
	}
	h.verify()
}

// The reported scenario: a handler locks under A, an operator lifts that lock
// and locks the subject again, and the handler's expiry leaves the operator's
// lock standing.
func TestAnExpiryNeverLiftsALockAnOperatorPlacedSince(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()
	operator := standing.Actor{Principal: "p-operator", Subject: "soc@example.com"}

	h.mustDo(ext.Action{Kind: ext.ActionLockSubject, ActionID: "A", SubjectID: alice, Within: within(ids(alice), nil, nil)})
	if res, err := h.standing.Unlock(ctx, tenant, operator, standing.LiftRequest{ID: alice}); err != nil || !res.Lifted {
		t.Fatalf("the operator's unlock = %+v, %v", res, err)
	}
	if _, err := h.standing.Lock(ctx, tenant, operator, standing.LockRequest{SubjectID: alice, ReasonCode: "investigating", Reason: "Suspended by the SOC."}); err != nil {
		t.Fatal(err)
	}

	res, err := h.do(ext.Action{
		Kind: ext.ActionUnlockSubject, ActionID: "expire-A", SubjectID: alice, IfPlacedBy: "A",
		Within: within(ids(alice), nil, nil),
	})
	if err != nil || res.Affected != 0 || res.ReasonCode != action.ReasonNotThisLock {
		t.Fatalf("the expiry = %+v, %v; want it to leave the operator's lock", res, err)
	}
	l, err := h.store.SubjectLocks().Get(ctx, tenant, alice)
	if err != nil || l.LockedBy.Principal != "p-operator" || l.ReasonCode != "investigating" {
		t.Errorf("the lock standing is %+v (%v), want the operator's", l, err)
	}
	if out := h.authorize(alice, "sess-after"); out.Deny == nil {
		t.Error("alice was allowed while the operator's lock stands")
	}
	h.verify()
}

// IfPlacedBy changes what an unlock does, so it is part of the action; the
// scope is who asked, and is not.
func TestIfPlacedByIsPartOfTheAction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	first := h.mustDo(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "u-1", SubjectID: alice, IfPlacedBy: "A"})
	for name, placedBy := range map[string]string{"another condition": "B", "no condition": ""} {
		if _, err := h.do(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "u-1", SubjectID: alice, IfPlacedBy: placedBy}); ext.KindOf(err) != ext.KindConflict {
			t.Errorf("%s under the same id: %v, want KindConflict", name, err)
		}
	}
	h.mustDo(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "u-2", SubjectID: alice})
	if _, err := h.do(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "u-2", SubjectID: alice, IfPlacedBy: "A"}); ext.KindOf(err) != ext.KindConflict {
		t.Errorf("a condition where the first had none: %v, want KindConflict", err)
	}

	// The same condition, confined or not, is the same action.
	for name, w := range map[string]*ext.ActionScope{"unconfined": nil, "confined": within(ids(alice), nil, nil)} {
		got := h.mustDo(ext.Action{Kind: ext.ActionUnlockSubject, ActionID: "u-1", SubjectID: alice, IfPlacedBy: "A", Within: w})
		if !same(got, first) {
			t.Errorf("a repeat %s = %+v, want %+v", name, got, first)
		}
	}
	row, err := h.store.ActionExecutions().Get(t.Context(), tenant, "u-1")
	if err != nil || row.IfPlacedBy != "A" {
		t.Errorf("the ledger row = %+v (%v), want the condition kept", row, err)
	}
	h.verify()
}
