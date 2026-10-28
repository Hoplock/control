// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/store"
)

// Standing denials (0039, M28): a lock on a subject and a quarantine on a
// target are applied ABOVE the engine. These tests hold that to the criteria it
// was built against: the deny holds whatever policy, a grant or a window says;
// the record names the lock, `explain` says so, replay reproduces the deny from
// the record; and none of it costs the decision path a round trip.

func (h *harness) lock(t testing.TB, subject string) store.SubjectLock {
	t.Helper()
	l, placed, err := h.store.SubjectLocks().Place(t.Context(), testTenant, store.SubjectLock{
		SubjectID: subject, LockedAt: h.clock.Add(-time.Minute),
		LockedBy:   store.StandingActor{Principal: "action-handler:acme", BreakGlass: false},
		ReasonCode: "compromised", Reason: "Your access was suspended as part of a security response.",
		ExternalRef: "INC-4711", ActionID: "act-1",
	})
	if err != nil || !placed {
		t.Fatalf("lock %s: %v (placed %v)", subject, err, placed)
	}
	return l
}

func (h *harness) quarantine(t testing.TB, target string) {
	t.Helper()
	_, placed, err := h.store.TargetQuarantines().Place(t.Context(), testTenant, store.TargetQuarantine{
		TargetID: target, QuarantinedAt: h.clock.Add(-time.Minute),
		QuarantinedBy: store.StandingActor{Principal: "ops-token"}, ReasonCode: "compromised",
	})
	if err != nil || !placed {
		t.Fatalf("quarantine %s: %v (placed %v)", target, err, placed)
	}
}

// A locked subject is a deny with `subject_locked`, whatever a rule, a grant or
// a window would have said, and an unlock restores the allow policy gives.
func TestALockedSubjectIsDeniedWhateverPolicyAndGrantsSay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Policy allows alice everywhere (`sre-anywhere`), and a live grant is
	// another way in. Both are overridden.
	if err := h.store.Grants().Insert(t.Context(), testTenant, func() store.Grant {
		g := store.Grant{
			ID: "g-1", SubjectID: alice, Scope: "break-fix", Origin: store.GrantOriginManual,
			NotBefore: h.clock.Add(-time.Hour), ExpiresAt: h.clock.Add(time.Hour),
			CreatedBy: store.GrantActor{Principal: "admin"}, Reason: "incident",
		}
		return g
	}()); err != nil {
		t.Fatalf("insert grant: %v", err)
	}
	// And so is a window an external system asserted, which is a grant like
	// the rest.
	if err := h.store.Grants().Insert(t.Context(), testTenant, store.Grant{
		ID: "g-window", SubjectID: alice, Scope: "scan-window", Origin: store.GrantOriginExternal,
		NotBefore: h.clock.Add(-time.Hour), ExpiresAt: h.clock.Add(time.Hour), ExternalRef: "SCAN-1",
		External:  store.GrantExternal{System: "scanner", Mode: store.ExternalPush, AssertionID: "scan-1"},
		CreatedBy: store.GrantActor{Principal: "scanner-token"}, Reason: "scan",
	}); err != nil {
		t.Fatalf("insert external grant: %v", err)
	}
	h.mustAllow(t, request("edge-host.example.com", "proxy-1"))

	l := h.lock(t, alice)

	out, err := h.authorize(t, request("edge-host.example.com", "proxy-1"))
	if err != nil {
		t.Fatalf("authorize while locked: %v: a lock is a deny made on purpose, never an outage (M11)", err)
	}
	if out.Deny == nil || out.Response != nil {
		t.Fatalf("authorize while locked = %+v, want a deny", out)
	}
	if out.Deny.Reason != decision.CodeSubjectLocked || out.Deny.Rule != "" {
		t.Errorf("the deny is %+v, want reason %q and no rule", out.Deny, decision.CodeSubjectLocked)
	}

	// The record names the lock, whole.
	story, err := decision.Explain(t.Context(), h.store, nil, testTenant, out.Deny.DecisionID)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if story.Effect != decision.EffectDeny || story.Rule != "" {
		t.Errorf("explain says effect %q rule %q, want a deny that no rule answered", story.Effect, story.Rule)
	}
	var standing []struct {
		Code        string `json:"code"`
		SubjectID   string `json:"subject_id"`
		PlacedAt    string `json:"placed_at"`
		ReasonCode  string `json:"reason_code"`
		ExternalRef string `json:"external_ref"`
		ActionID    string `json:"action_id"`
		PlacedBy    struct {
			Principal string `json:"principal"`
		} `json:"placed_by"`
	}
	if err := json.Unmarshal(story.Standing, &standing); err != nil || len(standing) != 1 {
		t.Fatalf("explain carries standing %s (%v), want exactly the lock", story.Standing, err)
	}
	if got := standing[0]; got.Code != "subject_locked" || got.SubjectID != alice || got.ReasonCode != "compromised" ||
		got.ExternalRef != "INC-4711" || got.ActionID != "act-1" || got.PlacedBy.Principal != "action-handler:acme" ||
		got.PlacedAt != l.LockedAt.UTC().Format(time.RFC3339) {
		t.Errorf("the recorded lock is %+v, want the lock that stood", got)
	}
	var expl struct {
		Basis    string   `json:"basis"`
		Standing []string `json:"standing"`
		Deny     string   `json:"deny_reason"`
	}
	if err := json.Unmarshal(story.Explanation, &expl); err != nil ||
		expl.Basis != "standing" || len(expl.Standing) != 1 || expl.Standing[0] != "subject_locked" || expl.Deny != "subject_locked" {
		t.Errorf("the explanation is %s (%v), want the standing basis naming subject_locked", story.Explanation, err)
	}
	// The grant was not read: nothing it says could matter, and a record that
	// listed it would suggest it had been weighed.
	var inputs struct {
		Grants []json.RawMessage `json:"grants"`
	}
	if err := json.Unmarshal(story.Inputs, &inputs); err != nil || len(inputs.Grants) != 0 {
		t.Errorf("a locked call's inputs list grants %s (%v), want none", story.Inputs, err)
	}

	// The same id in another tenant is not locked: a lock is the tenant's.
	other, err := h.store.Subjects().Standing(t.Context(), "other-tenant", alice)
	if err != nil || other.Known || other.Lock != nil {
		t.Errorf("standing in another tenant = %+v (%v), want neither the row nor the lock", other, err)
	}

	// Lifting the lock restores whatever policy allows.
	if _, err := h.store.SubjectLocks().Lift(t.Context(), testTenant, alice); err != nil {
		t.Fatalf("lift: %v", err)
	}
	h.mustAllow(t, request("edge-host.example.com", "proxy-1"))
}

// A lock holds for a subject id this server has no row for: a person who never
// logged in, or whose row was deleted, is locked all the same.
func TestALockHoldsForASubjectWithNoRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	const ghost = "ghost@example.com"
	h.lock(t, ghost)

	req := request("edge-host.example.com", "proxy-1")
	req.Identity.Subject = ghost
	out, err := h.authorize(t, req)
	if err != nil || out.Deny == nil || out.Deny.Reason != decision.CodeSubjectLocked {
		t.Fatalf("authorize = %+v, %v; want a subject_locked deny", out, err)
	}

	// The record says the subject was not known, and still names the lock.
	story, err := decision.Explain(t.Context(), h.store, nil, testTenant, out.Deny.DecisionID)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var in struct {
		Subject struct {
			Known bool `json:"known"`
		} `json:"subject"`
	}
	if err := json.Unmarshal(story.Inputs, &in); err != nil || in.Subject.Known {
		t.Errorf("the record says the subject was known: %s", story.Inputs)
	}
}

// Deleting a locked subject and re-creating it leaves it locked, and a login's
// upsert (which is Subjects().Upsert) does not clear the lock.
func TestALockSurvivesTheSubjectBeingRewrittenDeletedAndRecreated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()

	h.lock(t, alice)
	deny := func(step string) {
		t.Helper()
		out, err := h.authorize(t, request("edge-host.example.com", "proxy-1"))
		if err != nil || out.Deny == nil || out.Deny.Reason != decision.CodeSubjectLocked {
			t.Fatalf("%s: authorize = %+v, %v; want the lock to still deny", step, out, err)
		}
	}

	sub, err := h.store.Subjects().Get(ctx, testTenant, alice)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	sub.Groups = []string{"sre", "dba"}
	if err := h.store.Subjects().Upsert(ctx, testTenant, sub); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	deny("after an upsert")

	if err := h.store.Subjects().Delete(ctx, testTenant, alice); err != nil {
		t.Fatalf("delete: %v", err)
	}
	deny("after the row was deleted")

	sub.Groups = []string{"sre"}
	if err := h.store.Subjects().Upsert(ctx, testTenant, sub); err != nil {
		t.Fatalf("re-create: %v", err)
	}
	deny("after the row was re-created")
}

// A quarantined target is a deny with `target_quarantined`, whoever asks and
// whatever policy says; every other target is as open as it was.
func TestAQuarantinedTargetIsDeniedToEveryone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.mustAllow(t, request("cached.example.com", "proxy-1"))
	h.quarantine(t, "t-cached")

	out, err := h.authorize(t, request("cached.example.com", "proxy-1"))
	if err != nil || out.Deny == nil || out.Deny.Reason != decision.CodeTargetQuarantined {
		t.Fatalf("authorize = %+v, %v; want a target_quarantined deny", out, err)
	}
	story, err := decision.Explain(t.Context(), h.store, nil, testTenant, out.Deny.DecisionID)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var standing []struct {
		Code     string `json:"code"`
		TargetID string `json:"target_id"`
	}
	if err := json.Unmarshal(story.Standing, &standing); err != nil || len(standing) != 1 ||
		standing[0].Code != "target_quarantined" || standing[0].TargetID != "t-cached" {
		t.Errorf("explain carries standing %s (%v), want the quarantine on t-cached", story.Standing, err)
	}

	// The record is filed against the target, so "everything denied on this
	// host" finds it.
	d, err := h.store.Decisions().Get(t.Context(), testTenant, out.Deny.DecisionID)
	if err != nil || d.TargetID != "t-cached" || d.Effect != decision.EffectDeny {
		t.Errorf("the stored decision is %+v (%v), want a deny filed against t-cached", d, err)
	}

	h.mustAllow(t, request("edge-host.example.com", "proxy-1"))

	if _, err := h.store.TargetQuarantines().Lift(t.Context(), testTenant, "t-cached"); err != nil {
		t.Fatalf("release: %v", err)
	}
	h.mustAllow(t, request("cached.example.com", "proxy-1"))
}

// When both stand, both are named, the lock first.
func TestALockAndAQuarantineAreBothNamed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.lock(t, alice)
	h.quarantine(t, "t-cached")

	out, err := h.authorize(t, request("cached.example.com", "proxy-1"))
	if err != nil || out.Deny == nil || out.Deny.Reason != decision.CodeSubjectLocked {
		t.Fatalf("authorize = %+v, %v; want the lock to be the reason", out, err)
	}
	story, err := decision.Explain(t.Context(), h.store, nil, testTenant, out.Deny.DecisionID)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var codes []struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(story.Standing, &codes); err != nil || len(codes) != 2 ||
		codes[0].Code != "subject_locked" || codes[1].Code != "target_quarantined" {
		t.Errorf("explain carries %s (%v), want subject_locked then target_quarantined", story.Standing, err)
	}
}

// A lock is above the policy, so it holds when the policy cannot be loaded —
// and a policy outage for anyone else is still an outage, not a deny.
func TestALockHoldsWhenThePolicyCannotBeLoaded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.lock(t, alice)
	h.activate(t, "schema_version: 1\ntenant: acme\nrules: [this is not a policy")

	out, err := h.authorize(t, request("edge-host.example.com", "proxy-1"))
	if err != nil || out.Deny == nil || out.Deny.Reason != decision.CodeSubjectLocked {
		t.Fatalf("authorize = %+v, %v; want the lock to deny without the policy", out, err)
	}

	// Another subject has no lock to stand on: that call needs the policy, and
	// it is an outage, not a denial (M11).
	req := request("edge-host.example.com", "proxy-1")
	req.Identity.Subject = "bob@example.com"
	if out, err := h.authorize(t, req); err == nil {
		t.Errorf("authorize with an unloadable policy = %+v, want an outage", out)
	}
}

// Replay reproduces a locked call's deny from its record: a candidate bundle
// that would allow the same question cannot flip it, because a lock is not a
// rule it can change.
func TestReplayReproducesALockedDenyFromTheRecord(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.lock(t, alice)
	locked := h.recordSession(t, "edge-host.example.com", "sess-locked")
	if _, err := h.store.SubjectLocks().Lift(t.Context(), testTenant, alice); err != nil {
		t.Fatalf("lift: %v", err)
	}
	open := h.recordSession(t, "edge-host.example.com", "sess-open")

	// The fixture bundle allows alice everywhere, so it would allow the locked
	// call's question. It still replays as the deny it was.
	rep, err := decision.Replay(t.Context(), h.store.Decisions(), testTenant, compileSource(t, testBundle), decision.ReplayQuery{
		From: h.clock.Add(-time.Minute), To: h.clock.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rep.Replayed != 2 || rep.Unchanged != 2 || len(rep.AllowToDeny)+len(rep.DenyToAllow) != 0 || len(rep.Unreplayable) != 0 {
		t.Fatalf("replay = %+v, want both decisions unchanged (%s was locked, %s was not)", rep, locked, open)
	}

	// And a candidate that denies the unlocked call flips only that one.
	closed := strings.Replace(testBundle, "rules:\n", `rules:
  - id: close-everything
    effect: deny
    reason: closed
    match:
      subject:
        ids: [alice@example.com]
`, 1)
	rep, err = decision.Replay(t.Context(), h.store.Decisions(), testTenant, compileSource(t, closed), decision.ReplayQuery{
		From: h.clock.Add(-time.Minute), To: h.clock.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rep.Unchanged != 1 || len(rep.AllowToDeny) != 1 || rep.AllowToDeny[0].Flips[0].DecisionID != open {
		t.Errorf("replay against a closing candidate = %+v, want only %s to flip", rep, open)
	}
}

// statementCounter counts the statements a store's pool sends.
type statementCounter struct{ n atomic.Int64 }

func (c *statementCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}
func (c *statementCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// The decision path pays nothing for a standing denial it does not meet, and
// nothing extra for one it does: the lock is read in the statement that reads
// the subject, the quarantine in the one that reads the target (M5).
//
// The count is of STATEMENTS sent for one authorize call, which is the round
// trips. An unlocked call to a target nothing allows spends four: the subject
// and its lock, the target and its quarantine, the live grants, and the
// decision record. A locked call spends one fewer, because the grants are not
// read when nothing they say can matter.
func TestStandingDenialsCostTheDecisionPathNoRoundTrip(t *testing.T) {
	t.Parallel()
	counter := &statementCounter{}
	h := newHarness(t, func(h *harness) {
		h.storeOpts = append(h.storeOpts, store.WithQueryTracer(counter))
		// The production refresh window: a compiled program is served from
		// memory, which is what the real path does.
		h.refresh = decision.DefaultRefreshInterval
	})

	countOf := func(req func() error) int64 {
		t.Helper()
		before := counter.n.Load()
		if err := req(); err != nil {
			t.Fatal(err)
		}
		return counter.n.Load() - before
	}
	// bob is nobody the fixture policy allows, so every call of his is a
	// default deny: no routing, no graph, the cheapest honest baseline.
	const bob = "bob@example.com"
	ask := func(target, wantDeny string) func() error {
		return func() error {
			req := request(target, "proxy-1")
			req.Identity.Subject = bob
			out, err := h.authorize(t, req)
			if err != nil {
				return err
			}
			if out.Deny == nil {
				t.Fatalf("authorize %s as %s was not denied", target, bob)
			}
			if wantDeny != "" && out.Deny.Reason != wantDeny {
				t.Fatalf("authorize %s as %s was denied for %q, want %q", target, bob, out.Deny.Reason, wantDeny)
			}
			return nil
		}
	}
	plain := ask("edge-host.example.com", "")

	_ = countOf(plain) // warm the program and graph
	baseline := countOf(plain)
	if baseline != 4 {
		t.Fatalf("an unlocked default-deny sends %d statements, want 4 (subject+lock, target+quarantine, grants, record): "+
			"the baseline this test measures against has moved", baseline)
	}

	// With locks and quarantines standing on OTHER rows, the same call costs
	// the same.
	h.lock(t, "someone-else@example.com")
	h.quarantine(t, "t-deep")
	if got := countOf(plain); got != baseline {
		t.Errorf("a call that meets no standing denial sends %d statements with some standing elsewhere, want %d", got, baseline)
	}

	// A call that meets a lock sends fewer, never more.
	h.lock(t, bob)
	if got := countOf(ask("edge-host.example.com", decision.CodeSubjectLocked)); got != baseline-1 {
		t.Errorf("a locked call sends %d statements, want %d (the baseline less the grants it need not read)", got, baseline-1)
	}
	if _, err := h.store.SubjectLocks().Lift(t.Context(), testTenant, bob); err != nil {
		t.Fatalf("lift: %v", err)
	}

	// The directory's deactivation is a third nullable join in the subject's
	// statement (0040): one standing elsewhere costs nothing, and one on the
	// subject sends fewer statements, never more.
	h.deactivate(t, "someone-else@example.com")
	if got := countOf(plain); got != baseline {
		t.Errorf("a call that meets no deactivation sends %d statements with one standing elsewhere, want %d", got, baseline)
	}
	h.deactivate(t, bob)
	if got := countOf(ask("edge-host.example.com", decision.CodeSubjectDeactivated)); got != baseline-1 {
		t.Errorf("a deactivated call sends %d statements, want %d", got, baseline-1)
	}
	if _, err := h.store.Pool().Exec(t.Context(), `DELETE FROM directory_links`); err != nil {
		t.Fatal(err)
	}

	// A call that meets a quarantine skips the grants as well.
	h.quarantine(t, "t-edge")
	if got := countOf(ask("edge-host.example.com", decision.CodeTargetQuarantined)); got != baseline-1 {
		t.Errorf("a quarantined call sends %d statements, want %d", got, baseline-1)
	}
}

// deactivate places the directory's deactivation on a subject id, as the
// identity sync leaves it.
func (h *harness) deactivate(t testing.TB, subject string) store.DirectoryLink {
	t.Helper()
	l := store.DirectoryLink{
		ExternalID: "ext-" + subject, SubjectID: subject, Provider: "hoplock/scim", LinkedAt: h.clock.Add(-time.Hour),
		Provisioned: true, DeactivatedAt: h.clock.Add(-time.Minute).Truncate(time.Second), DeactivationReason: "terminated",
	}
	if err := h.store.DirectoryLinks().Put(t.Context(), testTenant, l); err != nil {
		t.Fatalf("deactivate %s: %v", subject, err)
	}
	return l
}

// A deactivated subject is a deny with `subject_deactivated`, whatever policy
// and grants say, recorded whole as the sync's, explained, and reproduced by
// replay; a subject both locked and deactivated is recorded with both (0040).
func TestADeactivatedSubjectIsDeniedRecordedExplainedAndReplayed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.mustAllow(t, request("edge-host.example.com", "proxy-1"))

	l := h.deactivate(t, alice)
	out, err := h.authorize(t, request("edge-host.example.com", "proxy-1"))
	if err != nil {
		t.Fatalf("authorize while deactivated: %v: a deactivation is a deny made on purpose, never an outage (M11)", err)
	}
	if out.Deny == nil || out.Deny.Reason != decision.CodeSubjectDeactivated || out.Deny.Rule != "" {
		t.Fatalf("authorize while deactivated = %+v", out)
	}
	story, err := decision.Explain(t.Context(), h.store, nil, testTenant, out.Deny.DecisionID)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var standing []struct {
		Code        string `json:"code"`
		SubjectID   string `json:"subject_id"`
		PlacedAt    string `json:"placed_at"`
		ReasonCode  string `json:"reason_code"`
		Reason      string `json:"reason"`
		ExternalRef string `json:"external_ref"`
		PlacedBy    struct {
			Principal string `json:"principal"`
			Subject   string `json:"subject"`
		} `json:"placed_by"`
	}
	if err := json.Unmarshal(story.Standing, &standing); err != nil || len(standing) != 1 {
		t.Fatalf("explain carries standing %s (%v), want exactly the deactivation", story.Standing, err)
	}
	if got := standing[0]; got.Code != "subject_deactivated" || got.SubjectID != alice || got.ReasonCode != "terminated" ||
		got.ExternalRef != "ext-"+alice || got.PlacedBy.Principal != "identity-sync:hoplock/scim" || got.PlacedBy.Subject != "" ||
		got.PlacedAt != l.DeactivatedAt.UTC().Format(time.RFC3339) || got.Reason != access.DeactivatedByDirectoryReason {
		t.Errorf("the recorded deactivation is %+v", got)
	}
	deactivatedSession := h.recordSession(t, "edge-host.example.com", "sess-deactivated")

	// Locked as well: both are named, the lock first.
	h.lock(t, alice)
	out, err = h.authorize(t, request("edge-host.example.com", "proxy-1"))
	if err != nil || out.Deny == nil {
		t.Fatalf("authorize while locked and deactivated = %+v, %v", out, err)
	}
	story, err = decision.Explain(t.Context(), h.store, nil, testTenant, out.Deny.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(story.Standing), `"subject_locked"`) || !strings.Contains(string(story.Standing), `"subject_deactivated"`) {
		t.Errorf("a locked and deactivated call records %s", story.Standing)
	}

	// The fixture bundle allows alice; the deactivated call still replays as
	// the deny it was.
	rep, err := decision.Replay(t.Context(), h.store.Decisions(), testTenant, compileSource(t, testBundle), decision.ReplayQuery{
		From: h.clock.Add(-time.Minute), To: h.clock.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	for _, flips := range rep.DenyToAllow {
		for _, f := range flips.Flips {
			if f.DecisionID == deactivatedSession {
				t.Errorf("replay reports the deactivated call %s flipping to an allow", deactivatedSession)
			}
		}
	}
}
