// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Grants and the workflow requests that may precede them (0012, M10).

// Everything a grant carries survives the round trip: the scope's selector,
// why and who, the workflow's provenance and the external assertion. A field
// that is written and not read back is a field explain (0019) cannot show.
func TestAGrantRoundTripsEveryField(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	now := time.Now().UTC().Truncate(time.Microsecond)
	want := store.Grant{
		ID:           "g-full",
		SubjectID:    "alice",
		Scope:        "prod-dba",
		ScopeTargets: []string{"*.db.example.com", "pg-1.example.com"},
		ScopeLabels:  map[string]string{"env": "prod"},
		ScopeZones:   []string{"eu"},
		NotBefore:    now,
		ExpiresAt:    now.Add(30 * time.Minute),
		Origin:       store.GrantOriginWorkflow,
		ReasonCode:   "incident",
		Reason:       "INC-9 needs a DBA on the primary",
		CreatedBy:    store.GrantActor{Subject: "bob", Principal: "p-bob", BreakGlass: true},
		RequestID:    "gr-1",
		ApprovalRef:  "wf-77",
		Approvers:    []string{"carol", "dave"},
		ExternalRef:  "INC-9",
		External: store.GrantExternal{
			System:         "itsm",
			WindowStart:    now,
			WindowEnd:      now.Add(20 * time.Minute),
			AdditionalKind: store.AdditionalContextObject,
			Additional:     `{"priority":"p1"}`,
		},
	}
	if err := st.Grants().Insert(ctx, tenantA, want); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := st.Grants().Get(ctx, tenantA, "g-full")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want.CreatedAt = got.CreatedAt
	if !reflect.DeepEqual(inUTC(got), inUTC(want)) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, want)
	}

	live, err := st.Grants().ListLive(ctx, tenantA, "alice", now.Add(time.Minute))
	if err != nil || len(live) != 1 {
		t.Fatalf("ListLive: %d, %v", len(live), err)
	}
	if !reflect.DeepEqual(inUTC(live[0]), inUTC(got)) {
		t.Errorf("the decision path's read differs from Get:\n live %+v\n  get %+v", live[0], got)
	}

	// A revoked grant round-trips its revocation too.
	revoked := want
	revoked.ID = "g-revoked"
	revoked.RevokedAt = now.Add(time.Minute)
	revoked.RevokedBy = store.GrantActor{Subject: "carol", Principal: "p-carol"}
	revoked.RevokeReason = "withdrawn"
	if err := st.Grants().Insert(ctx, tenantA, revoked); err != nil {
		t.Fatalf("Insert revoked: %v", err)
	}
	back, err := st.Grants().Get(ctx, tenantA, "g-revoked")
	if err != nil {
		t.Fatalf("Get revoked: %v", err)
	}
	revoked.CreatedAt = back.CreatedAt
	if !reflect.DeepEqual(inUTC(back), inUTC(revoked)) {
		t.Errorf("revoked round trip:\n got %+v\nwant %+v", back, revoked)
	}
}

// inUTC puts every instant on one location, because the driver hands back the
// process's local zone and DeepEqual compares the pointer, not the instant.
func inUTC(g store.Grant) store.Grant {
	g.NotBefore = g.NotBefore.UTC()
	g.ExpiresAt = g.ExpiresAt.UTC()
	g.CreatedAt = g.CreatedAt.UTC()
	g.RevokedAt = g.RevokedAt.UTC()
	g.External.WindowStart = g.External.WindowStart.UTC()
	g.External.WindowEnd = g.External.WindowEnd.UTC()
	return g
}

// The state is a function of the clock, three times out of four. A grant is
// never "expired" because something marked it so: it is expired because the
// instant asked about is past its end.
func TestAGrantsStateIsAFunctionOfTheInstant(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	g := store.Grant{NotBefore: start, ExpiresAt: start.Add(time.Hour)}

	cases := []struct {
		at   time.Time
		want store.GrantState
	}{
		{start.Add(-time.Nanosecond), store.GrantScheduled},
		{start, store.GrantActive},
		{start.Add(time.Hour - time.Nanosecond), store.GrantActive},
		{start.Add(time.Hour), store.GrantExpired},
	}
	for _, c := range cases {
		if got := g.State(c.at); got != c.want {
			t.Errorf("State(%v) = %s, want %s", c.at, got, c.want)
		}
		if got := g.Live(c.at); got != (c.want == store.GrantActive) {
			t.Errorf("Live(%v) = %v, want %v", c.at, got, c.want == store.GrantActive)
		}
	}

	g.RevokedAt = start.Add(time.Minute)
	for _, c := range cases {
		if got := g.State(c.at); got != store.GrantRevoked {
			t.Errorf("a revoked grant's State(%v) = %s, want revoked whatever the clock says", c.at, got)
		}
	}
}

// The list filters by the same four states, judged at the instant the caller
// supplies — and it refuses to judge one without an instant.
func TestTheListFiltersByStateAtAnInstant(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	now := time.Now().UTC()
	grant := func(id string, from, to time.Duration) store.Grant {
		return store.Grant{
			ID: id, SubjectID: "alice", Scope: "s", Origin: store.GrantOriginManual,
			NotBefore: now.Add(from), ExpiresAt: now.Add(to),
		}
	}
	storetest.Seed(t, st, storetest.Fixture{
		Tenant: tenantA,
		Grants: []store.Grant{
			grant("active", -time.Hour, time.Hour),
			grant("scheduled", time.Hour, 2*time.Hour),
			grant("expired", -2*time.Hour, -time.Hour),
			grant("revoked", -time.Hour, time.Hour),
			{ID: "bobs", SubjectID: "bob", Scope: "s", Origin: store.GrantOriginManual,
				NotBefore: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)},
		},
	})
	if _, err := st.Grants().Revoke(ctx, tenantA, "revoked", store.GrantRevocation{At: now, Reason: "test"}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	ids := func(q store.GrantQuery) []string {
		t.Helper()
		got, err := st.Grants().List(ctx, tenantA, q)
		if err != nil {
			t.Fatalf("List(%+v): %v", q, err)
		}
		var out []string
		for _, g := range got {
			out = append(out, g.ID)
		}
		return out
	}

	for state, want := range map[store.GrantState]string{
		store.GrantActive:    "active",
		store.GrantScheduled: "scheduled",
		store.GrantExpired:   "expired",
		store.GrantRevoked:   "revoked",
	} {
		got := ids(store.GrantQuery{SubjectID: "alice", State: state, At: now})
		if len(got) != 1 || got[0] != want {
			t.Errorf("state %s: %v, want [%s]", state, got, want)
		}
	}
	if got := ids(store.GrantQuery{SubjectID: "alice"}); len(got) != 4 {
		t.Errorf("alice's grants: %v, want all four", got)
	}
	if got := ids(store.GrantQuery{}); len(got) != 5 {
		t.Errorf("the tenant's grants: %v, want five", got)
	}
	if got := ids(store.GrantQuery{Limit: 2}); len(got) != 2 {
		t.Errorf("a limit of 2 returned %v", got)
	}

	if _, err := st.Grants().List(ctx, tenantA, store.GrantQuery{State: store.GrantActive}); !store.IsInvalid(err) {
		t.Errorf("a state with no instant: %v, want ErrInvalid", err)
	}
	if _, err := st.Grants().List(ctx, tenantA, store.GrantQuery{State: "stale", At: now}); !store.IsInvalid(err) {
		t.Errorf("an unknown state: %v, want ErrInvalid", err)
	}
}

// Revocation facts only exist on a revoked grant. The schema refuses a reason
// with no revocation time, which would be a grant that reads as withdrawn and
// is live.
func TestARevocationReasonWithoutARevocationIsRefused(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	now := time.Now().UTC()
	storetest.Seed(t, st, storetest.Fixture{
		Tenant: tenantA,
		Grants: []store.Grant{storetest.Grant("g", "alice", time.Hour)},
	})
	_, err := st.Pool().Exec(ctx,
		`UPDATE grants SET revoke_reason = 'looks withdrawn' WHERE tenant = $1 AND grant_id = 'g'`, tenantA)
	if err == nil {
		t.Fatal("the database accepted a revocation reason on an unrevoked grant")
	}
	if live, err := st.Grants().ListLive(ctx, tenantA, "alice", now); err != nil || len(live) != 1 {
		t.Fatalf("ListLive: %d, %v", len(live), err)
	}
}

// Revocation asks FROM the grant which sessions it backed. Only allowed
// decisions that named a proxy and a session count, each pair once, and a cut
// answer says it was cut.
func TestSessionsByGrantAreTheAllowedDistinctPairs(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	now := time.Now().UTC()
	decision := func(id, grant, effect, proxy, session string) {
		t.Helper()
		if err := st.Decisions().Insert(ctx, tenantA, store.Decision{
			ID: id, SubjectID: "alice", InputsDigest: "x", Effect: effect,
			ProxyID: proxy, SessionID: session, GrantID: grant, DecidedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	decision("d1", "g-1", store.DecisionEffectAllow, "proxy-1", "sess-a")
	decision("d2", "g-1", store.DecisionEffectAllow, "proxy-1", "sess-a") // a re-authorization
	decision("d3", "g-1", store.DecisionEffectAllow, "proxy-2", "sess-a") // the next hop
	decision("d4", "g-1", "deny", "proxy-1", "sess-b")
	decision("d5", "g-1", store.DecisionEffectAllow, "proxy-1", "")
	decision("d6", "g-2", store.DecisionEffectAllow, "proxy-1", "sess-c")
	decision("d7", "", store.DecisionEffectAllow, "proxy-1", "sess-d")

	got, cut, err := st.Decisions().SessionsByGrant(ctx, tenantA, "g-1", 10)
	if err != nil {
		t.Fatalf("SessionsByGrant: %v", err)
	}
	want := []store.GrantSession{{ProxyID: "proxy-1", SessionID: "sess-a"}, {ProxyID: "proxy-2", SessionID: "sess-a"}}
	if cut || !reflect.DeepEqual(got, want) {
		t.Errorf("SessionsByGrant = %+v (cut %v), want %+v", got, cut, want)
	}

	got, cut, err = st.Decisions().SessionsByGrant(ctx, tenantA, "g-1", 1)
	if err != nil || len(got) != 1 || !cut {
		t.Errorf("a limit of 1 returned %+v, cut %v, %v: want one pair and a cut answer", got, cut, err)
	}

	byGrant, err := st.Decisions().ListByGrant(ctx, tenantA, "g-1", 0)
	if err != nil || len(byGrant) != 5 {
		t.Errorf("ListByGrant: %d, %v, want the five decisions g-1 supplied", len(byGrant), err)
	}
	if _, err := st.Decisions().ListByGrant(ctx, tenantA, "", 0); !store.IsInvalid(err) {
		t.Errorf("ListByGrant of the empty id: %v, want ErrInvalid", err)
	}
	for _, d := range byGrant {
		if d.GrantID != "g-1" {
			t.Errorf("decision %s read back with grant %q", d.ID, d.GrantID)
		}
	}
}

// A request is born pending, learns its workflow reference once, and leaves
// pending exactly once.
func TestAGrantRequestIsDecidedOnce(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	now := time.Now().UTC()
	req := storetest.GrantRequest("gr-1", "alice", time.Hour)
	req.State = store.GrantRequestApproved // ignored: a request is born pending
	storetest.Seed(t, st, storetest.Fixture{
		Tenant:        tenantA,
		GrantRequests: []store.GrantRequest{req, storetest.GrantRequest("gr-2", "bob", time.Hour)},
	})

	got, err := st.GrantRequests().Get(ctx, tenantA, "gr-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != store.GrantRequestPending || !got.DecidedAt.IsZero() {
		t.Fatalf("a new request is %s decided at %v, want pending and undecided", got.State, got.DecidedAt)
	}

	// The reference is written once.
	if err := st.GrantRequests().Polled(ctx, tenantA, "gr-1", "wf-1", now); err != nil {
		t.Fatalf("Polled: %v", err)
	}
	if err := st.GrantRequests().Polled(ctx, tenantA, "gr-1", "wf-other", now.Add(time.Second)); err != nil {
		t.Fatalf("second Polled: %v", err)
	}
	got, _ = st.GrantRequests().Get(ctx, tenantA, "gr-1")
	if got.WorkflowRef != "wf-1" {
		t.Errorf("workflow ref %q, want the first one the workflow gave", got.WorkflowRef)
	}

	// Least recently polled first: gr-2 has never been asked about.
	pending, err := st.GrantRequests().ListPending(ctx, tenantA, 0)
	if err != nil || len(pending) != 2 || pending[0].ID != "gr-2" {
		t.Fatalf("ListPending: %+v, %v, want gr-2 before gr-1", pending, err)
	}

	// Approved must name its grant; anything else must not.
	if err := st.GrantRequests().Resolve(ctx, tenantA, "gr-1", store.GrantRequestResolution{
		State: store.GrantRequestApproved, At: now,
	}); !store.IsInvalid(err) {
		t.Errorf("approved with no grant: %v, want ErrInvalid", err)
	}
	if err := st.GrantRequests().Resolve(ctx, tenantA, "gr-1", store.GrantRequestResolution{
		State: store.GrantRequestDenied, GrantID: "g", At: now,
	}); !store.IsInvalid(err) {
		t.Errorf("denied naming a grant: %v, want ErrInvalid", err)
	}

	approvals := []store.GrantApproval{{Approver: "carol", Approved: true, At: now.Truncate(time.Microsecond)}}
	if err := st.GrantRequests().Resolve(ctx, tenantA, "gr-1", store.GrantRequestResolution{
		State: store.GrantRequestApproved, GrantID: "g-1", Approvals: approvals,
		WindowClamped: true, At: now,
	}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got, _ = st.GrantRequests().Get(ctx, tenantA, "gr-1")
	if got.State != store.GrantRequestApproved || got.GrantID != "g-1" || !got.WindowClamped ||
		got.DecidedAt.IsZero() || len(got.Approvals) != 1 || got.Approvals[0].Approver != "carol" {
		t.Errorf("after Resolve: %+v", got)
	}

	// Twice is a conflict: a decision is applied once, however many nodes
	// polled the workflow.
	err = st.GrantRequests().Resolve(ctx, tenantA, "gr-1", store.GrantRequestResolution{
		State: store.GrantRequestDenied, At: now,
	})
	if !store.IsConflict(err) {
		t.Errorf("a second Resolve: %v, want ErrConflict", err)
	}
	if err := st.GrantRequests().Resolve(ctx, tenantA, "absent", store.GrantRequestResolution{
		State: store.GrantRequestDenied, At: now,
	}); !store.IsNotFound(err) {
		t.Errorf("Resolve of an absent request: %v, want ErrNotFound", err)
	}

	// A decided request is left alone by a late poll, and drops out of the
	// pending list.
	if err := st.GrantRequests().Polled(ctx, tenantA, "gr-1", "", now.Add(time.Hour)); err != nil {
		t.Fatalf("late Polled: %v", err)
	}
	pending, _ = st.GrantRequests().ListPending(ctx, tenantA, 0)
	if len(pending) != 1 || pending[0].ID != "gr-2" {
		t.Errorf("ListPending after a decision: %+v", pending)
	}
}

// The approvals column is a document, and a request read back carries it as
// it was written.
func TestGrantApprovalsAreStoredAsWritten(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal([]store.GrantApproval{{Approver: "a", Approved: false, Reason: "no"}})
	if err != nil {
		t.Fatal(err)
	}
	var back []store.GrantApproval
	if err := json.Unmarshal(raw, &back); err != nil || len(back) != 1 || back[0].Reason != "no" {
		t.Errorf("approval encoding does not round trip: %s", raw)
	}
}
