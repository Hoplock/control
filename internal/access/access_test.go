// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The grant lifecycle against the real decision path (0012's acceptance
// criteria): a real database, the real engine behind /v1/authorize, the real
// audit chain and the real revocation broker — and ONE clock, which the test
// owns. Nothing in this file runs a sweeper, because nothing in the product has
// one to run.

const (
	tenant = store.Tenant("acme")
	alice  = "alice@example.com"
	admin  = "admin@example.com"
)

// The policy the grants are an input to. `prod-dba` exists only while
// somebody holds a grant for it; the development host is ordinary access.
const bundle = `
schema_version: 1
tenant: acme
description: the grant lifecycle's fixture policy
labels:
  env: [prod, dev]
groups: [dba]
rules:
  - id: jit-dba
    effect: allow
    match:
      target:
        labels: {env: [prod]}
      grant: {required: true, scopes: [prod-dba]}
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: ephemeral-user
          username: {from: subject-local-part}
          key_type: ed25519
          lifetime_seconds: 900
      max_session_duration: 4h
`

type harness struct {
	t         *testing.T
	store     *store.Store
	decisions *decision.Service
	bus       *revoke.Bus
	grants    *access.Service
	notes     *recordingNotifier
	revoker   *recordingRevoker

	mu    sync.Mutex
	clock time.Time
}

type harnessOption func(*access.Options)

// withWorkflow registers an approval workflow, as a Hoplock Enterprise binary
// would.
func withWorkflow(w ext.GrantWorkflow) harnessOption {
	return func(o *access.Options) {
		o.Workflow = w
		o.WorkflowProvider = "example/workflow"
	}
}

// withIDs makes grant and request ids deterministic, so two harnesses can be
// compared record for record.
func withIDs() harnessOption {
	return func(o *access.Options) {
		o.NewID = func(prefix string) string { return prefix + "_fixed" }
	}
}

func newHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()
	st := storetest.New(t)
	h := &harness{
		t:     t,
		store: st,
		clock: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
		notes: &recordingNotifier{},
	}
	log := slog.New(slog.DiscardHandler)

	storetest.Seed(t, st, storetest.Fixture{
		Tenant:   tenant,
		Subjects: []store.Subject{storetest.Subject(alice, "dba")},
		Targets: []store.Target{
			{ID: "t-db", Hostname: "db01.example.com", Zone: "edge", Labels: map[string]string{"env": "prod"}},
			{ID: "t-web", Hostname: "web01.example.com", Zone: "edge", Labels: map[string]string{"env": "dev"}},
		},
		Proxies: []store.Proxy{{
			ID: "proxy-1", Zone: "edge", PublicKey: []byte("key-proxy-1"),
			State: store.EnrollmentEnrolled, LastHeartbeatAt: h.clock,
		}},
	})
	version, err := st.PolicyBundles().NextVersion(t.Context(), tenant)
	if err != nil {
		t.Fatalf("bundle version: %v", err)
	}
	if err := st.PolicyBundles().Insert(t.Context(), tenant, store.PolicyBundle{
		Version: version, Source: []byte(bundle), Hash: "sha256:fixture", UploadedBy: "access_test",
	}); err != nil {
		t.Fatalf("insert bundle: %v", err)
	}
	if err := st.PolicyBundles().Activate(t.Context(), tenant, version); err != nil {
		t.Fatalf("activate bundle: %v", err)
	}

	registry := fleet.New(st,
		fleet.WithClock(h.now),
		fleet.WithLogger(log),
		// The lifecycle moves the clock by hours. The proxy's heartbeat is
		// not what these tests are about, so it stays live throughout.
		fleet.WithLiveness(fleet.Liveness{HeartbeatTTL: 7 * 24 * time.Hour}),
	)
	h.decisions, err = decision.New(decision.Options{
		Store: st, Fleet: registry, Subjects: identity.NewStoreDirectory(st),
		Logger: log, Now: h.now, Refresh: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("decision: %v", err)
	}

	h.bus, err = revoke.New(revoke.Options{Logger: log, Now: h.now})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	t.Cleanup(func() { _ = h.bus.Close(context.Background()) })
	h.revoker = &recordingRevoker{next: h.bus}

	ingest, err := audit.New(audit.Options{Store: st, Logger: log})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	emitter, err := audit.NewEmitter(ingest)
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}

	o := access.Options{
		Store:    st,
		Recorder: emitter,
		Revoker:  h.revoker,
		Notifier: h.notes,
		Settle:   time.Millisecond,
		Logger:   log,
		Now:      h.now,
	}
	for _, opt := range opts {
		opt(&o)
	}
	h.grants, err = access.New(o)
	if err != nil {
		t.Fatalf("access: %v", err)
	}
	return h
}

func (h *harness) now() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clock
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.clock = h.clock.Add(d)
	h.mu.Unlock()
}

var operator = access.Actor{Principal: "p-admin", Subject: admin, DisplayName: "Admin", CorrelationID: "corr-1"}

// dbaFor30m is "alice gets prod-dba for the next thirty minutes".
func dbaFor30m() access.Spec {
	return access.Spec{
		Subject:     alice,
		Scope:       access.Scope{Name: "prod-dba", Labels: map[string]string{"env": "prod"}},
		Duration:    30 * time.Minute,
		Reason:      "INC-9 needs a DBA on the primary",
		ExternalRef: "INC-9",
	}
}

func (h *harness) create(spec access.Spec) store.Grant {
	h.t.Helper()
	out, err := h.grants.Create(h.t.Context(), tenant, operator, spec)
	if err != nil {
		h.t.Fatalf("create: %v", err)
	}
	if out.Grant == nil {
		h.t.Fatalf("create produced no grant: %+v", out.Request)
	}
	return *out.Grant
}

// authorize asks the real decision path, as proxy-1 would, for session sess.
func (h *harness) authorize(target, session string) decision.Outcome {
	h.t.Helper()
	version := contract.PolicyVersion
	out, err := h.decisions.Authorize(h.t.Context(), tenant, &contract.AuthorizeRequest{
		Identity:      contract.Identity{Subject: alice, Login: "alice", Source: "local"},
		Target:        target,
		AuthMethod:    contract.AuthMethodCert,
		PolicyVersion: &version,
		Conn: contract.ConnMeta{
			SessionID: session, ProxyID: "proxy-1", ClientAddr: "203.0.113.7:52344",
			Timestamp: h.now().Format(time.RFC3339),
		},
	})
	if err != nil {
		h.t.Fatalf("authorize %s: %v (an outage, where a decision was expected)", target, err)
	}
	return out
}

func (h *harness) record(decisionID string) (store.Decision, map[string]any, map[string]any) {
	h.t.Helper()
	d, err := h.store.Decisions().Get(h.t.Context(), tenant, decisionID)
	if err != nil {
		h.t.Fatalf("decision record %s: %v", decisionID, err)
	}
	var inputs, expl map[string]any
	if err := json.Unmarshal(d.Inputs, &inputs); err != nil {
		h.t.Fatalf("inputs: %v", err)
	}
	if err := json.Unmarshal(d.Explanation, &expl); err != nil {
		h.t.Fatalf("explanation: %v", err)
	}
	return d, inputs, expl
}

// controlRecords reads this server's own audit records for one event.
func (h *harness) controlRecords(event string) []store.AuditRecord {
	h.t.Helper()
	recs, err := h.store.Audit().Query(h.t.Context(), tenant, store.AuditQuery{Event: event, Limit: 100})
	if err != nil {
		h.t.Fatalf("audit query: %v", err)
	}
	return recs
}

// ---------------------------------------------------------------------------
// The lifecycle: create, allow, expire — with nothing having run
// ---------------------------------------------------------------------------

// THE POINT OF THE PHASE. Before the grant, prod is denied; with it, the next
// authorize allows; after its window closes, the next authorize denies again —
// and between the allow and the final deny, NOTHING RAN. The grant row is
// byte-for-byte what it was; only the clock moved.
func TestAGrantAllowsUntilItsWindowClosesWithNoSweeper(t *testing.T) {
	h := newHarness(t)

	if out := h.authorize("db01.example.com", "sess-0"); out.Deny == nil {
		t.Fatal("prod was allowed before any grant existed")
	}

	g := h.create(dbaFor30m())
	if got := g.State(h.now()); got != store.GrantActive {
		t.Fatalf("a grant created now for thirty minutes is %s", got)
	}

	h.advance(time.Minute)
	allowed := h.authorize("db01.example.com", "sess-1")
	if allowed.Deny != nil {
		t.Fatalf("the next authorize after the grant was denied: %s", allowed.Deny.Reason)
	}
	// The grant bounds the session: a snapshot cached or replayed later
	// can never outlive the grant that supplied it (0005).
	if deadline := allowed.Response.SessionDeadline; deadline == "" ||
		deadline != g.ExpiresAt.UTC().Format(time.RFC3339) {
		t.Errorf("session_deadline = %q, want the grant's expiry %s", deadline, g.ExpiresAt.UTC().Format(time.RFC3339))
	}
	// Ordinary access is untouched by a grant for something else.
	if out := h.authorize("web01.example.com", "sess-2"); out.Deny == nil {
		t.Error("a prod-dba grant opened a development host no rule allows")
	}

	before, err := h.store.Grants().Get(t.Context(), tenant, g.ID)
	if err != nil {
		t.Fatalf("read grant: %v", err)
	}

	h.advance(30 * time.Minute) // past the window's end
	if out := h.authorize("db01.example.com", "sess-3"); out.Deny == nil {
		t.Fatal("the grant still allowed after its window closed")
	}

	after, err := h.store.Grants().Get(t.Context(), tenant, g.ID)
	if err != nil {
		t.Fatalf("read grant: %v", err)
	}
	if !after.RevokedAt.IsZero() || after.RevokedAt != before.RevokedAt ||
		!after.ExpiresAt.Equal(before.ExpiresAt) || !after.NotBefore.Equal(before.NotBefore) {
		t.Errorf("the grant row changed between the allow and the deny — something ran:\nbefore %+v\n after %+v", before, after)
	}
	if got := after.State(h.now()); got != store.GrantExpired {
		t.Errorf("state after the window = %s, want expired — by the clock alone", got)
	}
}

// A grant scheduled for later confers nothing until its window opens, by the
// same predicate.
func TestAScheduledGrantConfersNothingUntilItOpens(t *testing.T) {
	h := newHarness(t)
	spec := dbaFor30m()
	spec.NotBefore = h.now().Add(time.Hour)
	g := h.create(spec)
	if got := g.State(h.now()); got != store.GrantScheduled {
		t.Fatalf("state = %s, want scheduled", got)
	}
	if out := h.authorize("db01.example.com", "sess-early"); out.Deny == nil {
		t.Fatal("a scheduled grant allowed before its window opened")
	}
	h.advance(time.Hour + time.Minute)
	if out := h.authorize("db01.example.com", "sess-on-time"); out.Deny != nil {
		t.Fatalf("the grant did not allow once its window opened: %s", out.Deny.Reason)
	}
}

// ---------------------------------------------------------------------------
// The decision record names the grant
// ---------------------------------------------------------------------------

func TestADecisionMadeUnderAGrantNamesItInTheRecord(t *testing.T) {
	h := newHarness(t)
	g := h.create(dbaFor30m())

	out := h.authorize("db01.example.com", "sess-1")
	if out.Deny != nil {
		t.Fatalf("denied: %s", out.Deny.Reason)
	}
	rec, inputs, expl := h.record(out.Response.DecisionID)

	// The explanation names the grant that satisfied the rule — what
	// `explain` (0014) renders — and so does the indexed column revocation
	// reads.
	if expl["grant"] != g.ID {
		t.Errorf("explanation grant = %v, want %s", expl["grant"], g.ID)
	}
	if rec.GrantID != g.ID {
		t.Errorf("decisions.grant_id = %q, want %s", rec.GrantID, g.ID)
	}
	// The inputs carry the grant whole: what the engine matched on, and who
	// created it and why, so the record explains itself after the grant row
	// has long expired.
	grants, _ := inputs["grants"].([]any)
	if len(grants) != 1 {
		t.Fatalf("inputs.grants = %v, want the one live grant", inputs["grants"])
	}
	got := grants[0].(map[string]any)
	scope := got["scope"].(map[string]any)
	creator := got["created_by"].(map[string]any)
	external := got["external"].(map[string]any)
	for name, pair := range map[string][2]any{
		"id":                {got["id"], g.ID},
		"origin":            {got["origin"], "administrator"},
		"scope.name":        {scope["name"], "prod-dba"},
		"reason":            {got["reason"], "INC-9 needs a DBA on the primary"},
		"created_by":        {creator["subject"], admin},
		"created_by.cred":   {creator["principal"], "p-admin"},
		"external.ref":      {external["reference"], "INC-9"},
		"expires_at":        {got["expires_at"], g.ExpiresAt.UTC().Format(time.RFC3339)},
		"scope.labels[env]": {scope["labels"].(map[string]any)["env"], "prod"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("recorded grant %s = %v, want %v", name, pair[0], pair[1])
		}
	}
	// The cited ticket rides to the proxy as the session's grant context,
	// so every record of the session can name it (M10) — with no system,
	// because no external system asserted it.
	if gc := out.Response.GrantContext; gc == nil || gc.Reference != "INC-9" || gc.System != "" {
		t.Errorf("grant_context = %+v, want the cited ticket and no invented system", gc)
	}

	// And from the grant's side: what did it let anybody do.
	_, used, err := h.grants.Inspect(t.Context(), tenant, g.ID)
	if err != nil || len(used) != 1 || used[0].ID != out.Response.DecisionID {
		t.Errorf("Inspect decisions = %+v, %v; want the one decision the grant supplied", used, err)
	}
}

// ---------------------------------------------------------------------------
// Revocation
// ---------------------------------------------------------------------------

// Revoking a live grant ends the session it backed, and the holder is TOLD:
// the session_kill that reaches the proxy carries the operator's reason
// verbatim, so access ending never looks like a crash.
func TestRevokingALiveGrantEndsItsSessionWithTheReasonShown(t *testing.T) {
	h := newHarness(t)
	g := h.create(dbaFor30m())
	out := h.authorize("db01.example.com", "sess-1")
	if out.Deny != nil {
		t.Fatalf("denied: %s", out.Deny.Reason)
	}

	// proxy-1 holds its revocation stream open, as every proxy does (M9).
	events := h.subscribe("proxy-1")

	const reason = "Your prod-dba access was withdrawn by the on-call lead; contact #dba."
	rev, err := h.grants.Revoke(t.Context(), tenant, operator, g.ID, reason)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !rev.Revoked || rev.Grant.RevokedAt.IsZero() {
		t.Fatalf("revocation = %+v, want the grant withdrawn by this call", rev)
	}
	if !slices.Contains(rev.Sessions, store.GrantSession{ProxyID: "proxy-1", SessionID: "sess-1"}) {
		t.Errorf("sessions ended = %+v, want sess-1 on proxy-1", rev.Sessions)
	}

	var kill *contract.SessionKillEvent
	var invalidatedFirst bool
	deadline := time.After(5 * time.Second)
	for kill == nil {
		select {
		case ev := <-events:
			switch ev.Type {
			case contract.EventTypeCacheInvalidate:
				if ev.CacheInvalidate.Subject == alice {
					invalidatedFirst = true
				}
			case contract.EventTypeSessionKill:
				kill = ev.SessionKill
			case contract.EventTypeHeartbeat, contract.EventTypeResync:
			}
		case <-deadline:
			t.Fatal("proxy-1 never received a session_kill")
		}
	}
	if !slices.Contains(kill.SessionIDs, "sess-1") {
		t.Errorf("session_kill ids = %v, want sess-1", kill.SessionIDs)
	}
	if kill.Reason != reason {
		t.Errorf("the reason the user is shown = %q, want the operator's own %q", kill.Reason, reason)
	}
	// The holder's cached decisions go FIRST: a user whose session ends and
	// who reconnects at once must reach this server, not a decision cached
	// under the grant.
	if !invalidatedFirst {
		t.Error("the session_kill arrived before the cache_invalidate for the holder")
	}

	// And the grant is no input to the very next decision.
	if again := h.authorize("db01.example.com", "sess-2"); again.Deny == nil {
		t.Error("a revoked grant still allowed")
	}
	// The reason is on the grant too: the auditor reads what the user read.
	if rev.Grant.RevokeReason != reason || rev.Grant.RevokedBy.Principal != "p-admin" {
		t.Errorf("stored revocation = %q by %+v", rev.Grant.RevokeReason, rev.Grant.RevokedBy)
	}
}

// A revocation that races an authorize: the authorize read the grant as live a
// moment before the revocation committed, and records its session a moment
// after the first pass looked. The second pass — one decision budget later —
// ends it. Without that pass, that one session would outlive the revocation
// until the grant's original expiry.
func TestASessionThatRacesTheRevocationIsEndedByTheSecondPass(t *testing.T) {
	h := newHarness(t)
	g := h.create(dbaFor30m())

	// The racing authorize lands between the passes: the first session_kill
	// is the moment the first pass has already read its session list.
	h.revoker.onFirstKill = func() {
		if err := h.store.Decisions().Insert(context.Background(), tenant, store.Decision{
			ID: "d-racer", SubjectID: alice, InputsDigest: "x", Effect: store.DecisionEffectAllow,
			ProxyID: "proxy-1", SessionID: "sess-racer", GrantID: g.ID, DecidedAt: h.now(),
		}); err != nil {
			t.Errorf("insert racing decision: %v", err)
		}
	}
	h.authorize("db01.example.com", "sess-1")

	rev, err := h.grants.Revoke(t.Context(), tenant, operator, g.ID, "withdrawn")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !slices.Contains(rev.Sessions, store.GrantSession{ProxyID: "proxy-1", SessionID: "sess-racer"}) {
		t.Fatalf("the racing session was not ended: %+v", rev.Sessions)
	}
	if !h.revoker.killed("proxy-1", "sess-racer") {
		t.Error("no session_kill was published for the racing session")
	}
	// Ended once each: the second pass does not re-send what the first did.
	if n := h.revoker.killCount("proxy-1", "sess-1"); n != 1 {
		t.Errorf("sess-1 was killed %d times, want once", n)
	}
}

// Revoking twice is one revocation: the first time, revoker and reason stand,
// and the second call re-sends what a revocation publishes — which is what
// makes it the answer to "the kill could not be delivered".
func TestRevokingTwiceIsOneRevocationAndResends(t *testing.T) {
	h := newHarness(t)
	g := h.create(dbaFor30m())
	h.authorize("db01.example.com", "sess-1")

	first, err := h.grants.Revoke(t.Context(), tenant, operator, g.ID, "first reason")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	other := access.Actor{Principal: "p-other", Subject: "other@example.com"}
	second, err := h.grants.Revoke(t.Context(), tenant, other, g.ID, "second reason")
	if err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	if second.Revoked {
		t.Error("the second call reported that it revoked the grant")
	}
	if !second.Grant.RevokedAt.Equal(first.Grant.RevokedAt) || second.Grant.RevokeReason != "first reason" ||
		second.Grant.RevokedBy.Principal != "p-admin" {
		t.Errorf("the second revocation overwrote the first: %+v", second.Grant)
	}
	if n := h.revoker.killCount("proxy-1", "sess-1"); n != 2 {
		t.Errorf("sess-1 kill published %d times across two revokes, want it re-sent once more", n)
	}
	if recs := h.controlRecords(access.EventGrantRevoked); len(recs) != 1 {
		t.Errorf("%d revocation records, want exactly the first", len(recs))
	}
}

func TestRevokingNeedsAReasonToShowTheHolder(t *testing.T) {
	h := newHarness(t)
	g := h.create(dbaFor30m())
	_, err := h.grants.Revoke(t.Context(), tenant, operator, g.ID, "   ")
	var ve *access.ValidationError
	if !errors.As(err, &ve) || ve.Field != "reason" || ve.Problem != access.ProblemRequired {
		t.Fatalf("revoke with no reason: %v, want a validation error on reason", err)
	}
	if got, _ := h.store.Grants().Get(t.Context(), tenant, g.ID); !got.RevokedAt.IsZero() {
		t.Error("a refused revocation revoked the grant anyway")
	}
	if _, err := h.grants.Revoke(t.Context(), tenant, operator, "g_nope", "x"); !store.IsNotFound(err) {
		t.Errorf("revoking an absent grant: %v, want not found", err)
	}
}

// A revocation whose kill cannot be published is still a revocation: the
// grant is withdrawn and recorded, and the caller is told plainly that its
// sessions were not ended, so it can retry.
func TestAnUndeliveredRevocationIsRecordedAndSaysSo(t *testing.T) {
	h := newHarness(t)
	g := h.create(dbaFor30m())
	h.authorize("db01.example.com", "sess-1")
	h.revoker.fail = errors.New("the broker is closed")

	rev, err := h.grants.Revoke(t.Context(), tenant, operator, g.ID, "withdrawn")
	if !access.IsUndelivered(err) {
		t.Fatalf("revoke with a failing broker: %v, want ErrUndelivered", err)
	}
	if !rev.Revoked {
		t.Error("the revocation was not recorded as done")
	}
	if out := h.authorize("db01.example.com", "sess-2"); out.Deny == nil {
		t.Error("an undelivered revocation left the grant usable by new sessions")
	}
}

// ---------------------------------------------------------------------------
// Audit: every act, with the actor, in the act's own transaction
// ---------------------------------------------------------------------------

func TestCreatingAndRevokingAreAuditedWithTheActor(t *testing.T) {
	h := newHarness(t)
	g := h.create(dbaFor30m())
	if _, err := h.grants.Revoke(t.Context(), tenant, operator, g.ID, "withdrawn"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	for _, event := range []string{access.EventGrantCreated, access.EventGrantRevoked} {
		recs := h.controlRecords(event)
		if len(recs) != 1 {
			t.Fatalf("%d %s records, want 1", len(recs), event)
		}
		r := recs[0]
		for key, want := range map[string]string{
			audit.AttrGrantID:       g.ID,
			audit.AttrPrincipalID:   "p-admin",
			audit.AttrActorSubject:  admin,
			audit.AttrBreakGlass:    "false",
			audit.AttrCorrelationID: "corr-1",
			audit.AttrGrantScope:    "prod-dba",
			audit.AttrGrantOrigin:   "administrator",
		} {
			if got := r.Attributes[key]; got != want {
				t.Errorf("%s record: %s = %q, want %q", event, key, got, want)
			}
		}
		if r.Kind != "policy_decision" || r.Stream != audit.StreamControl || r.Subject != alice {
			t.Errorf("%s record filed as kind %q on stream %q about %q", event, r.Kind, r.Stream, r.Subject)
		}
	}
	if got := h.controlRecords(access.EventGrantRevoked)[0].Attributes[audit.AttrGrantRevoke]; got != "withdrawn" {
		t.Errorf("revocation record's reason = %q", got)
	}

	// The chain these records joined still verifies.
	result, err := audit.NewVerifier(h.store).VerifyStream(t.Context(), tenant, audit.StreamControl)
	if err != nil || result.Break != nil || result.Records != 2 {
		t.Errorf("control stream verification: %+v, %v", result, err)
	}
}

// A grant this server cannot write down is a grant it does not create: the
// record and the row are one transaction.
func TestAGrantThatCannotBeAuditedIsNotCreated(t *testing.T) {
	h := newHarness(t)
	failing, err := access.New(access.Options{
		Store:    h.store,
		Recorder: failingRecorder{},
		Revoker:  h.revoker,
		Now:      h.now,
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("access: %v", err)
	}
	if _, err := failing.Create(t.Context(), tenant, operator, dbaFor30m()); err == nil {
		t.Fatal("a create whose audit record failed succeeded")
	}
	rows, err := h.store.Grants().List(t.Context(), tenant, store.GrantQuery{})
	if err != nil || len(rows) != 0 {
		t.Fatalf("grants after a failed audit: %d, %v — want none", len(rows), err)
	}
	if out := h.authorize("db01.example.com", "sess-1"); out.Deny == nil {
		t.Error("an unaudited grant allowed access")
	}
}

// A break-glass credential creating a grant is flagged on the record, asserted
// from the credential and never inferred (M7).
func TestABreakGlassGrantIsFlaggedCritical(t *testing.T) {
	h := newHarness(t)
	glass := access.Actor{Principal: "p-glass", Subject: admin, BreakGlass: true}
	if _, err := h.grants.Create(t.Context(), tenant, glass, dbaFor30m()); err != nil {
		t.Fatalf("create: %v", err)
	}
	recs := h.controlRecords(access.EventGrantCreated)
	if len(recs) != 1 || recs[0].Attributes[audit.AttrBreakGlass] != "true" || recs[0].Severity != "critical" {
		t.Fatalf("break-glass grant record = %+v", recs)
	}
	g, _ := h.store.Grants().List(t.Context(), tenant, store.GrantQuery{})
	if len(g) != 1 || !g[0].CreatedBy.BreakGlass {
		t.Errorf("the grant does not carry its creator's break-glass flag: %+v", g)
	}
}

// ---------------------------------------------------------------------------
// Origin: the engine has no branch on it
// ---------------------------------------------------------------------------

// The same scenario with an administrator's grant and an externally confirmed
// one (M16) produces identical decisions. What differs is only what explain
// names — the origin, and the ticket the external system asserted.
func TestAnAdministratorsGrantAndAnExternalOneDecideIdentically(t *testing.T) {
	admin := newHarness(t, withIDs())
	external := newHarness(t, withIDs())

	adminGrant := admin.create(dbaFor30m())
	// The external grant is created the way 0013's push path creates one —
	// through access.CreateExternal, audited, with origin external, the
	// system that asserted it, the reference, and the window it asserted:
	// here the grant's own, so the deadline is the same too.
	spec := access.ExternalSpec{
		Subject: alice,
		Scope: access.Scope{Name: adminGrant.Scope, Labels: adminGrant.ScopeLabels,
			Targets: []string{"db01.example.com"}},
		NotBefore: adminGrant.NotBefore, ExpiresAt: adminGrant.ExpiresAt,
		System: "itsm", AssertionID: "INC-9/1", Mode: store.ExternalPush, Reference: "INC-9",
		WindowStart: adminGrant.NotBefore, WindowEnd: adminGrant.ExpiresAt,
		Reason: "window asserted by itsm", Ceiling: time.Hour,
	}
	if _, _, err := external.grants.CreateExternal(t.Context(), tenant, scannerToken, spec); err != nil {
		t.Fatalf("create the external grant: %v", err)
	}

	admin.advance(time.Minute)
	external.advance(time.Minute)
	a := admin.authorize("db01.example.com", "sess-1")
	b := external.authorize("db01.example.com", "sess-1")
	if a.Deny != nil || b.Deny != nil {
		t.Fatalf("denied: administrator %+v, external %+v", a.Deny, b.Deny)
	}

	// Identical decisions, modulo the decision id and the grant context —
	// which is where the external system's name rides, and must.
	ra, rb := *a.Response, *b.Response
	if rb.GrantContext == nil || rb.GrantContext.System != "itsm" || rb.GrantContext.Reference != "INC-9" {
		t.Errorf("the external grant's context = %+v, want the system and ticket named", rb.GrantContext)
	}
	ra.DecisionID, rb.DecisionID = "", ""
	ra.GrantContext, rb.GrantContext = nil, nil
	ja, _ := json.Marshal(ra)
	jb, _ := json.Marshal(rb)
	if string(ja) != string(jb) {
		t.Errorf("the engine answered differently by origin:\nadministrator %s\n     external %s", ja, jb)
	}

	_, _, ea := admin.record(a.Response.DecisionID)
	_, _, eb := external.record(b.Response.DecisionID)
	if ea["rule"] != eb["rule"] || ea["effect"] != eb["effect"] || ea["grant"] != eb["grant"] {
		t.Errorf("explanations differ: %v / %v", ea, eb)
	}
}

// ---------------------------------------------------------------------------
// The Enterprise seam: ext.GrantWorkflow
// ---------------------------------------------------------------------------

// With a workflow registered, creation routes through it — and the grant it
// approves is indistinguishable to the engine from an administrator's: the
// same authorize outcome and the same decision-record shape. Hoplock
// Enterprise depends on that equivalence.
func TestAWorkflowsGrantIsIndistinguishableToTheEngine(t *testing.T) {
	direct := newHarness(t, withIDs())
	wf := &fakeWorkflow{}
	routed := newHarness(t, withIDs(), withWorkflow(wf))

	d := direct.create(dbaFor30m())
	w := routed.create(dbaFor30m())

	if len(wf.submitted()) != 1 {
		t.Fatalf("the workflow saw %d submissions, want the create routed through it", len(wf.submitted()))
	}
	if w.Origin != store.GrantOriginWorkflow || w.RequestID == "" || w.ApprovalRef == "" ||
		!slices.Equal(w.Approvers, []string{"carol@example.com"}) {
		t.Errorf("the routed grant does not record the path that produced it: %+v", w)
	}
	if d.Origin != store.GrantOriginManual || d.RequestID != "" {
		t.Errorf("the direct grant claims a workflow: %+v", d)
	}

	direct.advance(time.Minute)
	routed.advance(time.Minute)
	a := direct.authorize("db01.example.com", "sess-1")
	b := routed.authorize("db01.example.com", "sess-1")
	if a.Deny != nil || b.Deny != nil {
		t.Fatalf("denied: direct %+v, workflow %+v", a.Deny, b.Deny)
	}
	ra, rb := *a.Response, *b.Response
	ra.DecisionID, rb.DecisionID = "", ""
	ja, _ := json.Marshal(ra)
	jb, _ := json.Marshal(rb)
	if string(ja) != string(jb) {
		t.Errorf("the same authorize answered differently:\n  direct %s\nworkflow %s", ja, jb)
	}

	_, ia, ea := direct.record(a.Response.DecisionID)
	_, ib, eb := routed.record(b.Response.DecisionID)
	if !sameShape(ia, ib) {
		t.Errorf("the decision record's inputs differ in shape:\n  direct %v\nworkflow %v", ia, ib)
	}
	if !sameShape(ea, eb) || ea["rule"] != eb["rule"] || ea["effect"] != eb["effect"] {
		t.Errorf("the explanations differ:\n  direct %v\nworkflow %v", ea, eb)
	}
	// What differs is provenance, and only provenance.
	ga := ia["grants"].([]any)[0].(map[string]any)
	gb := ib["grants"].([]any)[0].(map[string]any)
	if ga["origin"] != "administrator" || gb["origin"] != "workflow" || gb["request_id"] == "" {
		t.Errorf("the recorded origins are %v and %v", ga["origin"], gb["origin"])
	}
}

// The workflow is shown what it is deciding: the scope exactly as it would be
// stored, and the inventory records for the hosts it names.
func TestTheWorkflowSeesTheWholeScope(t *testing.T) {
	wf := &fakeWorkflow{}
	h := newHarness(t, withWorkflow(wf))
	spec := dbaFor30m()
	spec.Scope.Targets = []string{"DB01.example.com", "*.replica.example.com"}
	spec.ReasonCode = "incident"
	h.create(spec)

	got := wf.submitted()[0]
	if got.Scope.Name != "prod-dba" || !slices.Equal(got.Scope.Hostnames, []string{"*.replica.example.com", "db01.example.com"}) ||
		got.Scope.Labels["env"] != "prod" {
		t.Errorf("scope sent = %+v", got.Scope)
	}
	if len(got.Targets) != 1 || got.Targets[0].ID != "t-db" || got.Targets[0].Labels["env"] != "prod" {
		t.Errorf("resolved targets = %+v, want the one exact host this server holds", got.Targets)
	}
	if !slices.Equal(got.Privileges, []string{"prod-dba"}) || got.RequestedBy.ID != admin ||
		got.Subject.ID != alice || !slices.Contains(got.Subject.Groups, "dba") ||
		got.ReasonCode != "incident" || got.ExternalRef != "INC-9" || got.Tenant != ext.Tenant(tenant) {
		t.Errorf("request sent = %+v", got)
	}
}

// What Control does with a grant the workflow REJECTS: creates nothing, closes
// the request as denied, audits the closure, and tells the requester. The
// engine never hears of it.
func TestAGrantTheWorkflowDeniesIsNeverCreated(t *testing.T) {
	wf := &fakeWorkflow{decide: func(ext.GrantRequest) ext.GrantDecision {
		return ext.GrantDecision{
			WorkflowRef: "wf-1", Outcome: ext.GrantDenied, ReasonCode: "outside_change_window",
			Approvals: []ext.GrantApproval{{Approver: ext.Subject{ID: "carol@example.com"}, Approved: false}},
		}
	}}
	h := newHarness(t, withWorkflow(wf))

	out, err := h.grants.Create(t.Context(), tenant, operator, dbaFor30m())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.Grant != nil {
		t.Fatalf("a denied request produced a grant: %+v", out.Grant)
	}
	if out.Request == nil || out.Request.State != store.GrantRequestDenied ||
		out.Request.OutcomeCode != "outside_change_window" || len(out.Request.Approvals) != 1 {
		t.Fatalf("request = %+v, want it closed as denied with the workflow's reason", out.Request)
	}
	if rows, _ := h.store.Grants().List(t.Context(), tenant, store.GrantQuery{}); len(rows) != 0 {
		t.Errorf("grants after a denial: %+v", rows)
	}
	if out := h.authorize("db01.example.com", "sess-1"); out.Deny == nil {
		t.Error("a denied request allowed access")
	}
	closed := h.controlRecords(access.EventGrantRequestClosed)
	if len(closed) != 1 || closed[0].Attributes[audit.AttrRequestState] != "denied" ||
		closed[0].Attributes[audit.AttrWorkflow] != "example/workflow" {
		t.Errorf("request closure records = %+v", closed)
	}
	if !h.notes.has(access.EventGrantRequestClosed) {
		t.Error("the closure was not announced")
	}
}

// Pending is not access. The request waits in a table the decision path never
// reads, the poller asks the workflow again, and the grant exists from the
// moment the workflow approves — not before.
func TestAPendingRequestBecomesAGrantOnlyWhenApproved(t *testing.T) {
	wf := &fakeWorkflow{decide: pending}
	h := newHarness(t, withWorkflow(wf))

	out, err := h.grants.Create(t.Context(), tenant, operator, dbaFor30m())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.Grant != nil || out.Request.State != store.GrantRequestPending || out.Request.WorkflowRef == "" {
		t.Fatalf("create = grant %+v, request %+v; want a pending request with the workflow's reference",
			out.Grant, out.Request)
	}
	if deny := h.authorize("db01.example.com", "sess-1"); deny.Deny == nil {
		t.Fatal("a pending request allowed access")
	}

	// Still pending on the next poll: nothing changes.
	if err := h.grants.Poll(t.Context(), tenant); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if rows, _ := h.store.Grants().List(t.Context(), tenant, store.GrantQuery{}); len(rows) != 0 {
		t.Fatalf("a grant exists while its request is pending: %+v", rows)
	}

	// An approver answers; the next poll applies it, clamping a window wider
	// than the one asked for and recording that it did.
	req := out.Request
	wf.setStatus(req.WorkflowRef, ext.GrantDecision{
		WorkflowRef: req.WorkflowRef, Outcome: ext.GrantApproved,
		Window:    ext.Window{NotAfter: req.ExpiresAt.Add(time.Hour)},
		Approvals: []ext.GrantApproval{{Approver: ext.Subject{ID: "carol@example.com"}, Approved: true}},
	})
	h.advance(time.Minute)
	if err := h.grants.Poll(t.Context(), tenant); err != nil {
		t.Fatalf("poll: %v", err)
	}
	got, err := h.grants.Request(t.Context(), tenant, req.ID)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if got.Grant == nil || got.Request.State != store.GrantRequestApproved || !got.Request.WindowClamped {
		t.Fatalf("after approval: grant %+v, request %+v", got.Grant, got.Request)
	}
	if !got.Grant.ExpiresAt.Equal(req.ExpiresAt) {
		t.Errorf("approved grant expires %v, want the requested %v — never later", got.Grant.ExpiresAt, req.ExpiresAt)
	}
	if allow := h.authorize("db01.example.com", "sess-2"); allow.Deny != nil {
		t.Errorf("the approved grant did not allow: %s", allow.Deny.Reason)
	}
}

// A workflow that cannot be reached is an outage, never a "no": the request
// stays open with no reference, and the poller submits it again under the
// same id until it is answered.
func TestAnUnreachableWorkflowIsResubmittedUnderTheSameID(t *testing.T) {
	wf := &fakeWorkflow{}
	wf.failSubmits(1, ext.Errorf(ext.PointGrantWorkflow, "example/workflow", "GrantWorkflow.Submit",
		ext.KindUnavailable, "connection refused"))
	h := newHarness(t, withWorkflow(wf))

	out, err := h.grants.Create(t.Context(), tenant, operator, dbaFor30m())
	if err != nil {
		t.Fatalf("an unreachable workflow failed the create: %v", err)
	}
	if !out.Unconfirmed || out.Request.State != store.GrantRequestPending || out.Grant != nil {
		t.Fatalf("create = %+v, want an unconfirmed pending request", out)
	}

	if err := h.grants.Poll(t.Context(), tenant); err != nil {
		t.Fatalf("poll: %v", err)
	}
	subs := wf.submitted()
	if len(subs) != 2 || subs[0].RequestID != subs[1].RequestID {
		t.Fatalf("submissions = %d, want the same request id twice", len(subs))
	}
	got, _ := h.grants.Request(t.Context(), tenant, out.Request.ID)
	if got.Grant == nil || got.Request.State != store.GrantRequestApproved {
		t.Errorf("after resubmission: %+v", got.Request)
	}
}

// A request still pending when the window it asked for closes is expired, and
// the workflow is told to stop asking its approvers.
func TestAPendingRequestExpiresWithItsWindow(t *testing.T) {
	wf := &fakeWorkflow{decide: pending}
	h := newHarness(t, withWorkflow(wf))
	out, err := h.grants.Create(t.Context(), tenant, operator, dbaFor30m())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	h.advance(31 * time.Minute)
	if err := h.grants.Poll(t.Context(), tenant); err != nil {
		t.Fatalf("poll: %v", err)
	}
	got, _ := h.grants.Request(t.Context(), tenant, out.Request.ID)
	if got.Request.State != store.GrantRequestExpired || got.Request.OutcomeCode != access.OutcomeWindowClosed {
		t.Errorf("request = %+v, want expired with its window", got.Request)
	}
	if !slices.Contains(wf.cancelled(), out.Request.WorkflowRef) {
		t.Error("the workflow was not told the request expired")
	}
}

func TestCancellingAPendingRequest(t *testing.T) {
	wf := &fakeWorkflow{decide: pending}
	h := newHarness(t, withWorkflow(wf))
	out, err := h.grants.Create(t.Context(), tenant, operator, dbaFor30m())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := h.grants.Cancel(t.Context(), tenant, operator, out.Request.ID, "no longer needed")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got.Request.State != store.GrantRequestCancelled || !slices.Contains(wf.cancelled(), out.Request.WorkflowRef) {
		t.Errorf("after cancel: %+v, workflow cancels %v", got.Request, wf.cancelled())
	}
	// An approval arriving afterwards is ignored: nothing moves a request
	// back to pending.
	wf.setStatus(out.Request.WorkflowRef, ext.GrantDecision{WorkflowRef: out.Request.WorkflowRef, Outcome: ext.GrantApproved})
	if err := h.grants.Poll(t.Context(), tenant); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if rows, _ := h.store.Grants().List(t.Context(), tenant, store.GrantQuery{}); len(rows) != 0 {
		t.Errorf("a cancelled request produced a grant: %+v", rows)
	}
	if _, err := h.grants.Cancel(t.Context(), tenant, operator, out.Request.ID, ""); !errors.Is(err, access.ErrNotPending) {
		t.Errorf("cancelling a decided request: %v, want ErrNotPending", err)
	}
}

// A workflow that answers with something retrying would repeat closes the
// request as failed — and a workflow that forgot its own reference too.
func TestAWorkflowFailureThatRetryingCannotFixClosesTheRequest(t *testing.T) {
	wf := &fakeWorkflow{decide: pending}
	h := newHarness(t, withWorkflow(wf))
	out, err := h.grants.Create(t.Context(), tenant, operator, dbaFor30m())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wf.statusErr = ext.Errorf(ext.PointGrantWorkflow, "example/workflow", "GrantWorkflow.Status",
		ext.KindNotFound, "no such request")
	if err := h.grants.Poll(t.Context(), tenant); err != nil {
		t.Fatalf("poll: %v", err)
	}
	got, _ := h.grants.Request(t.Context(), tenant, out.Request.ID)
	if got.Request.State != store.GrantRequestFailed || got.Request.OutcomeCode != access.OutcomeWorkflowLostRequest {
		t.Errorf("request = %+v, want failed: the workflow lost it", got.Request)
	}
}

// ---------------------------------------------------------------------------
// Notifications
// ---------------------------------------------------------------------------

func TestEveryActIsAnnounced(t *testing.T) {
	h := newHarness(t)
	g := h.create(dbaFor30m())
	if _, err := h.grants.Revoke(t.Context(), tenant, operator, g.ID, "withdrawn"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	created := h.notes.find(access.EventGrantCreated)
	if created == nil || created.SubjectID != alice || created.Attributes["grant_id"] != g.ID ||
		created.Attributes["actor_principal"] != "p-admin" || created.Attributes["origin"] != "administrator" ||
		created.Tenant != ext.Tenant(tenant) {
		t.Errorf("grant.created notification = %+v", created)
	}
	revoked := h.notes.find(access.EventGrantRevoked)
	if revoked == nil || revoked.Attributes["revoke_reason"] != "withdrawn" {
		t.Errorf("grant.revoked notification = %+v", revoked)
	}
	// Notifications carry codes and attributes, never a sentence (M21).
	for _, n := range h.notes.all() {
		if n.DedupeKey == "" || n.Kind == "" {
			t.Errorf("notification without a kind or a dedupe key: %+v", n)
		}
	}
}

// ---------------------------------------------------------------------------
// fakes
// ---------------------------------------------------------------------------

// subscribe holds proxy's revocation stream open, as the proxy would, and
// returns what arrives on it.
func (h *harness) subscribe(proxyID string) <-chan *contract.RevocationEvent {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.t.Cleanup(cancel)
	events := make(chan *contract.RevocationEvent, 64)
	go func() {
		_ = h.bus.Subscribe(ctx, tenant, proxyID, "", func(ev *contract.RevocationEvent) error {
			events <- ev
			return nil
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		live, err := h.bus.LiveSubscriptions(ctx, tenant)
		if err == nil {
			if _, ok := live[proxyID]; ok {
				return events
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("%s never subscribed", proxyID)
	return nil
}

// recordingRevoker passes publications to the real broker and remembers them.
type recordingRevoker struct {
	next        access.Revoker
	onFirstKill func()
	fail        error

	mu    sync.Mutex
	kills []revoke.Kill
	auds  []revoke.Audience
}

func (r *recordingRevoker) Kill(ctx context.Context, tenant store.Tenant, aud revoke.Audience, k revoke.Kill) (revoke.Receipt, error) {
	if r.fail != nil {
		return revoke.Receipt{}, r.fail
	}
	r.mu.Lock()
	first := len(r.kills) == 0
	r.kills = append(r.kills, k)
	r.auds = append(r.auds, aud)
	hook := r.onFirstKill
	r.mu.Unlock()
	rcpt, err := r.next.Kill(ctx, tenant, aud, k)
	if first && hook != nil {
		hook()
	}
	return rcpt, err
}

func (r *recordingRevoker) Invalidate(ctx context.Context, tenant store.Tenant, aud revoke.Audience, inv revoke.Invalidation) (revoke.Receipt, error) {
	if r.fail != nil {
		return revoke.Receipt{}, r.fail
	}
	return r.next.Invalidate(ctx, tenant, aud, inv)
}

func (r *recordingRevoker) killCount(proxyID, sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for i, k := range r.kills {
		if slices.Contains(r.auds[i].ProxyIDs, proxyID) && slices.Contains(k.SessionIDs, sessionID) {
			n++
		}
	}
	return n
}

func (r *recordingRevoker) killed(proxyID, sessionID string) bool {
	return r.killCount(proxyID, sessionID) > 0
}

type recordingNotifier struct {
	mu  sync.Mutex
	got []ext.Notification
}

func (r *recordingNotifier) Notify(_ context.Context, n ext.Notification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, n)
}

func (r *recordingNotifier) all() []ext.Notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

func (r *recordingNotifier) find(kind string) *ext.Notification {
	for _, n := range r.all() {
		if n.Kind == kind {
			return &n
		}
	}
	return nil
}

func (r *recordingNotifier) has(kind string) bool { return r.find(kind) != nil }

type failingRecorder struct{}

func (failingRecorder) GrantEvent(context.Context, *store.Store, store.Tenant, access.Event) error {
	return errors.New("the audit store refused the record")
}

// fakeWorkflow is an approval workflow as Hoplock Enterprise would register
// one. By default it approves at once, with one approver.
type fakeWorkflow struct {
	decide    func(ext.GrantRequest) ext.GrantDecision
	statusErr error

	mu          sync.Mutex
	submits     []ext.GrantRequest
	submitFails int
	submitErr   error
	status      map[string]ext.GrantDecision
	cancels     []string
}

func pending(req ext.GrantRequest) ext.GrantDecision {
	return ext.GrantDecision{WorkflowRef: "wf-" + req.RequestID, Outcome: ext.GrantPending}
}

func (w *fakeWorkflow) failSubmits(n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.submitFails, w.submitErr = n, err
}

func (w *fakeWorkflow) Submit(_ context.Context, req ext.GrantRequest) (ext.GrantDecision, error) {
	w.mu.Lock()
	w.submits = append(w.submits, req)
	if w.submitFails > 0 {
		w.submitFails--
		err := w.submitErr
		w.mu.Unlock()
		return ext.GrantDecision{}, err
	}
	decide := w.decide
	w.mu.Unlock()
	if decide != nil {
		return decide(req), nil
	}
	return ext.GrantDecision{
		WorkflowRef: "wf-" + req.RequestID,
		Outcome:     ext.GrantApproved,
		Approvals:   []ext.GrantApproval{{Approver: ext.Subject{ID: "carol@example.com"}, Approved: true}},
	}, nil
}

func (w *fakeWorkflow) Status(_ context.Context, _ ext.Tenant, ref string) (ext.GrantDecision, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.statusErr != nil {
		return ext.GrantDecision{}, w.statusErr
	}
	if d, ok := w.status[ref]; ok {
		return d, nil
	}
	return ext.GrantDecision{WorkflowRef: ref, Outcome: ext.GrantPending}, nil
}

func (w *fakeWorkflow) Cancel(_ context.Context, _ ext.Tenant, ref, _ string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cancels = append(w.cancels, ref)
	return nil
}

func (w *fakeWorkflow) setStatus(ref string, d ext.GrantDecision) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == nil {
		w.status = map[string]ext.GrantDecision{}
	}
	w.status[ref] = d
}

func (w *fakeWorkflow) submitted() []ext.GrantRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.submits)
}

func (w *fakeWorkflow) cancelled() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.cancels)
}

// sameShape reports whether two decoded JSON documents have the same keys at
// every level, whatever their values and however many elements a list holds.
func sameShape(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || !slices.Equal(slices.Sorted(maps.Keys(av)), slices.Sorted(maps.Keys(bv))) {
			return false
		}
		for k := range av {
			if !sameShape(av[k], bv[k]) {
				return false
			}
		}
		return true
	case []any:
		// A list's shape is its elements' shape, not its length: no
		// approvers and one approver are the same field.
		bv, ok := b.([]any)
		if !ok {
			return false
		}
		for i := range min(len(av), len(bv)) {
			if !sameShape(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		_, isMap := b.(map[string]any)
		_, isList := b.([]any)
		return !isMap && !isList
	}
}

// ---------------------------------------------------------------------------
// The external path (M16, 0013)
// ---------------------------------------------------------------------------

var scannerToken = access.Actor{Principal: "p-scanner", DisplayName: "acme-scanner token", CorrelationID: "corr-push"}

// externalFor30m is what a push the binding admitted arrives as: the window it
// asserted, the window it is granted, and how that was bounded.
func (h *harness) externalFor30m(assertion string) access.ExternalSpec {
	now := h.now()
	return access.ExternalSpec{
		Subject:           alice,
		Scope:             access.Scope{Name: "prod-dba", Targets: []string{"db01.example.com"}, Labels: map[string]string{"env": "prod"}},
		NotBefore:         now,
		ExpiresAt:         now.Add(30 * time.Minute),
		System:            "itsm",
		AssertionID:       assertion,
		Mode:              store.ExternalPush,
		Reference:         "CHG-42",
		WindowStart:       now,
		WindowEnd:         now.Add(2 * time.Hour),
		AdditionalContext: json.RawMessage(`{"change_class":"standard"}`),
		Reason:            "window asserted by itsm",
		Ceiling:           30 * time.Minute,
		Clamped:           true,
	}
}

// A pushed window becomes a grant like any other: stored, audited with the
// credential that pushed it in the same transaction — including that its
// window was clamped — and announced naming the system.
func TestAnExternalWindowIsAGrantAuditedWithItsCeiling(t *testing.T) {
	h := newHarness(t)
	g, replayed, err := h.grants.CreateExternal(t.Context(), tenant, scannerToken, h.externalFor30m("a-1"))
	if err != nil || replayed {
		t.Fatalf("CreateExternal = %v, replayed %v", err, replayed)
	}
	if g.Origin != store.GrantOriginExternal || g.External.System != "itsm" || g.External.AssertionID != "a-1" ||
		g.External.Mode != store.ExternalPush || g.ExternalRef != "CHG-42" ||
		g.External.AdditionalKind != store.AdditionalContextObject || g.CreatedBy.Principal != "p-scanner" {
		t.Errorf("the stored grant = %+v", g)
	}
	if !g.ExpiresAt.Equal(h.now().Add(30*time.Minute)) || !g.External.WindowEnd.Equal(h.now().Add(2*time.Hour)) {
		t.Errorf("windows: granted until %v, asserted until %v", g.ExpiresAt, g.External.WindowEnd)
	}

	recs := h.controlRecords(access.EventGrantCreated)
	if len(recs) != 1 {
		t.Fatalf("%d grant.created records, want 1", len(recs))
	}
	attrs := recs[0].Attributes
	for k, want := range map[string]string{
		audit.AttrGrantOrigin:            "external",
		audit.AttrGrantExternalSystem:    "itsm",
		audit.AttrGrantExternalAssertion: "a-1",
		audit.AttrGrantExternalMode:      "push",
		audit.AttrGrantExternalClamped:   "true",
		audit.AttrGrantExternalCeiling:   "1800",
		audit.AttrPrincipalID:            "p-scanner",
	} {
		if attrs[k] != want {
			t.Errorf("audit attribute %s = %q, want %q", k, attrs[k], want)
		}
	}
	n := h.notes.find(access.EventGrantCreated)
	if n == nil || n.Attributes["external_system"] != "itsm" || n.Attributes["window_clamped"] != "true" {
		t.Errorf("the announcement = %+v, want the system named and the clamp said", n)
	}
}

// The same assertion twice is one grant — and a revoked one stays revoked,
// because the external system saying it again is not a new decision.
func TestAnAssertionPushedAgainIsTheSameGrantEvenRevoked(t *testing.T) {
	h := newHarness(t)
	first, _, err := h.grants.CreateExternal(t.Context(), tenant, scannerToken, h.externalFor30m("a-1"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, replayed, err := h.grants.CreateExternal(t.Context(), tenant, scannerToken, h.externalFor30m("a-1"))
	if err != nil || !replayed || again.ID != first.ID {
		t.Fatalf("second = %s, replayed %v, %v; want %s replayed", again.ID, replayed, err, first.ID)
	}

	if _, err := h.grants.Revoke(t.Context(), tenant, operator, first.ID, "scan cancelled"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	third, replayed, err := h.grants.CreateExternal(t.Context(), tenant, scannerToken, h.externalFor30m("a-1"))
	if err != nil || !replayed || third.ID != first.ID || third.RevokedAt.IsZero() {
		t.Fatalf("after revocation = %+v, replayed %v, %v; want the revoked grant", third, replayed, err)
	}
	if n := len(h.controlRecords(access.EventGrantCreated)); n != 1 {
		t.Errorf("%d grant.created records for one assertion, want 1", n)
	}
}

// `additional_context` is a JSON string or a JSON object and nothing else; a
// pushed window that names no host is one nobody can audit.
func TestAnExternalWindowIsRefusedWhenMalformed(t *testing.T) {
	h := newHarness(t)
	for name, mutate := range map[string]func(*access.ExternalSpec){
		"a number":     func(s *access.ExternalSpec) { s.AdditionalContext = json.RawMessage(`42`) },
		"a list":       func(s *access.ExternalSpec) { s.AdditionalContext = json.RawMessage(`["a"]`) },
		"no host":      func(s *access.ExternalSpec) { s.Scope.Targets = nil },
		"no assertion": func(s *access.ExternalSpec) { s.AssertionID = "" },
		"probe mode":   func(s *access.ExternalSpec) { s.Mode = store.ExternalProbe },
		"empty window": func(s *access.ExternalSpec) { s.ExpiresAt = s.NotBefore },
	} {
		spec := h.externalFor30m("a-" + name)
		mutate(&spec)
		var verr *access.ValidationError
		if _, _, err := h.grants.CreateExternal(t.Context(), tenant, scannerToken, spec); !errors.As(err, &verr) {
			t.Errorf("%s: err = %v, want a validation error", name, err)
		}
	}
	if !access.AdditionalContextValid(json.RawMessage(`"a sentence"`)) || access.AdditionalContextValid(json.RawMessage(`true`)) {
		t.Error("AdditionalContextValid disagrees with the contract's string-or-object rule")
	}
}
