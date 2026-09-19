// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

const (
	tenantA store.Tenant = "tenant-a"
	tenantB store.Tenant = "tenant-b"
)

func TestSubjectRoundTrip(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	want := storetest.Subject("alice", "engineering", "oncall")
	if err := st.Subjects().Upsert(ctx, tenantA, want); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := st.Subjects().Get(ctx, tenantA, "alice")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != want.ID || got.Source != want.Source {
		t.Errorf("Get returned %+v, want id %q source %q", got, want.ID, want.Source)
	}
	if len(got.Groups) != 2 || got.Groups[0] != "engineering" {
		t.Errorf("Get returned groups %v, want %v", got.Groups, want.Groups)
	}
	if got.Claims["sub"] != "alice" {
		t.Errorf("Get returned claims %v, want sub=alice", got.Claims)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("Get returned zero timestamps; the database sets them")
	}

	byPrincipal, err := st.Subjects().GetByPrincipal(ctx, tenantA, "alice")
	if err != nil {
		t.Fatalf("GetByPrincipal: %v", err)
	}
	if byPrincipal.ID != want.ID {
		t.Errorf("GetByPrincipal returned %q, want %q", byPrincipal.ID, want.ID)
	}

	// A subject with no groups and no claims must round-trip too: a nil
	// slice encodes as SQL NULL, and these columns are NOT NULL.
	bare := store.Subject{ID: "bare", Source: "local"}
	if err := st.Subjects().Upsert(ctx, tenantA, bare); err != nil {
		t.Fatalf("Upsert a subject with no groups or claims: %v", err)
	}

	if err := st.Subjects().Delete(ctx, tenantA, "alice"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Subjects().Get(ctx, tenantA, "alice"); !store.IsNotFound(err) {
		t.Errorf("Get after Delete: %v, want ErrNotFound", err)
	}
	if err := st.Subjects().Delete(ctx, tenantA, "alice"); !store.IsNotFound(err) {
		t.Errorf("Delete of an absent subject: %v, want ErrNotFound", err)
	}
}

func TestTargetLookupAndLabelMatching(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	storetest.Seed(t, st, storetest.Fixture{
		Tenant: tenantA,
		Targets: []store.Target{
			storetest.Target("t1", "db-1.example", map[string]string{"env": "prod", "role": "db"}),
			storetest.Target("t2", "db-2.example", map[string]string{"env": "staging", "role": "db"}),
			storetest.Target("t3", "web-1.example", map[string]string{"env": "prod", "role": "web"}),
		},
	})

	// The decision path's lookup: hostname in, labels out, one query.
	got, err := st.Targets().GetByHostname(ctx, tenantA, "db-1.example")
	if err != nil {
		t.Fatalf("GetByHostname: %v", err)
	}
	if got.ID != "t1" || got.Labels["env"] != "prod" {
		t.Errorf("GetByHostname returned %+v, want t1 with env=prod", got)
	}

	prod, err := st.Targets().ListByLabels(ctx, tenantA, map[string]string{"env": "prod"})
	if err != nil {
		t.Fatalf("ListByLabels: %v", err)
	}
	if len(prod) != 2 {
		t.Errorf("ListByLabels(env=prod) returned %d targets, want 2", len(prod))
	}

	prodDB, err := st.Targets().ListByLabels(ctx, tenantA, map[string]string{"env": "prod", "role": "db"})
	if err != nil {
		t.Fatalf("ListByLabels: %v", err)
	}
	if len(prodDB) != 1 || prodDB[0].ID != "t1" {
		t.Errorf("ListByLabels(env=prod,role=db) returned %+v, want just t1", prodDB)
	}

	if _, err := st.Targets().GetByHostname(ctx, tenantA, "absent.example"); !store.IsNotFound(err) {
		t.Errorf("GetByHostname of an absent host: %v, want ErrNotFound", err)
	}
}

func TestProxyHeartbeatIsNotClobberedByAnUpsert(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	p := storetest.Proxy("proxy-1", "zone-a")
	if err := st.Proxies().Upsert(ctx, tenantA, p); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	beat := time.Now().UTC().Truncate(time.Millisecond)
	if err := st.Proxies().RecordHealth(ctx, tenantA, store.ProxyHealthReport{ProxyID: "proxy-1", At: beat}); err != nil {
		t.Fatalf("RecordHealth: %v", err)
	}

	// Re-enrolling describes the proxy, not its liveness. A proxy that has
	// gone quiet must not look live because somebody edited its zone.
	p.Zone = "zone-b"
	if err := st.Proxies().Upsert(ctx, tenantA, p); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	got, err := st.Proxies().Get(ctx, tenantA, "proxy-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Zone != "zone-b" {
		t.Errorf("Get returned zone %q, want zone-b", got.Zone)
	}
	if !got.LastHeartbeatAt.Equal(beat) {
		t.Errorf("Get returned heartbeat %v, want %v: an upsert cleared it", got.LastHeartbeatAt, beat)
	}

	// A heartbeat from a proxy with no row is not a row to create.
	if err := st.Proxies().RecordHealth(ctx, tenantA, store.ProxyHealthReport{ProxyID: "unknown", At: beat}); !store.IsNotFound(err) {
		t.Errorf("RecordHealth for an unknown proxy: %v, want ErrNotFound", err)
	}

	inZone, err := st.Proxies().ListByZone(ctx, tenantA, "zone-b")
	if err != nil {
		t.Fatalf("ListByZone: %v", err)
	}
	if len(inZone) != 1 || inZone[0].ID != "proxy-1" {
		t.Errorf("ListByZone returned %+v, want just proxy-1", inZone)
	}

	// A proxy that has never reported reads as never, not as 1970.
	if err := st.Proxies().Upsert(ctx, tenantA, storetest.Proxy("proxy-2", "zone-b")); err != nil {
		t.Fatalf("Upsert proxy-2: %v", err)
	}
	quiet, err := st.Proxies().Get(ctx, tenantA, "proxy-2")
	if err != nil {
		t.Fatalf("Get proxy-2: %v", err)
	}
	if !quiet.LastHeartbeatAt.IsZero() {
		t.Errorf("a proxy that never reported has heartbeat %v, want the zero time", quiet.LastHeartbeatAt)
	}
}

func TestPolicyBundlesAreImmutableAndOneIsActive(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	next, err := st.PolicyBundles().NextVersion(ctx, tenantA)
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	if next != 1 {
		t.Errorf("NextVersion on an empty tenant = %d, want 1", next)
	}

	storetest.Seed(t, st, storetest.Fixture{
		Tenant:  tenantA,
		Bundles: []store.PolicyBundle{storetest.Bundle(1, true), storetest.Bundle(2, false)},
	})

	// Immutable: re-inserting a version is a conflict, never an overwrite.
	if err := st.PolicyBundles().Insert(ctx, tenantA, storetest.Bundle(1, false)); !store.IsConflict(err) {
		t.Errorf("re-inserting version 1: %v, want ErrConflict", err)
	}

	active, err := st.PolicyBundles().GetActive(ctx, tenantA)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if active.Version != 1 {
		t.Errorf("GetActive returned version %d, want 1", active.Version)
	}

	if err := st.PolicyBundles().Activate(ctx, tenantA, 2); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	active, err = st.PolicyBundles().GetActive(ctx, tenantA)
	if err != nil {
		t.Fatalf("GetActive after Activate: %v", err)
	}
	if active.Version != 2 {
		t.Errorf("GetActive returned version %d, want 2", active.Version)
	}

	// Activating a version that does not exist must not deactivate the one
	// that does: "which program is served" keeps exactly one answer.
	if err := st.PolicyBundles().Activate(ctx, tenantA, 99); !store.IsNotFound(err) {
		t.Errorf("Activate of an absent version: %v, want ErrNotFound", err)
	}
	active, err = st.PolicyBundles().GetActive(ctx, tenantA)
	if err != nil {
		t.Fatalf("GetActive after a failed Activate: %v", err)
	}
	if active.Version != 2 {
		t.Errorf("a failed Activate left version %d active, want 2 untouched", active.Version)
	}

	if next, err = st.PolicyBundles().NextVersion(ctx, tenantA); err != nil || next != 3 {
		t.Errorf("NextVersion = (%d, %v), want (3, nil)", next, err)
	}
}

// A tenant with no active bundle has no policy, and the decision path must be
// able to say so rather than deny (M11).
func TestNoActiveBundleIsNotFoundRatherThanAFailure(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)

	_, err := st.PolicyBundles().GetActive(t.Context(), tenantA)
	if !store.IsNotFound(err) {
		t.Errorf("GetActive with no bundles: %v, want ErrNotFound", err)
	}
	if store.IsUnavailable(err) {
		t.Error("GetActive with no bundles reads as an outage")
	}
}

func TestDecisionRecordRoundTrip(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	d := store.Decision{
		ID:           "dec-1",
		SubjectID:    "alice",
		TargetID:     "t1",
		InputsDigest: "sha256:abc",
		MatchedRule:  "allow-prod-db",
		Obligations:  []string{"record", "approve"},
		Snapshot:     json.RawMessage(`{"decision":"allow"}`),
	}
	if err := st.Decisions().Insert(ctx, tenantA, d); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := st.Decisions().Get(ctx, tenantA, "dec-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.MatchedRule != d.MatchedRule || len(got.Obligations) != 2 {
		t.Errorf("Get returned %+v, want the rule and both obligations", got)
	}
	if got.DecidedAt.IsZero() {
		t.Error("Get returned a zero decided_at")
	}

	if err := st.Decisions().Insert(ctx, tenantA, d); !store.IsConflict(err) {
		t.Errorf("re-inserting a decision id: %v, want ErrConflict", err)
	}

	list, err := st.Decisions().ListBySubject(ctx, tenantA, "alice", 0)
	if err != nil {
		t.Fatalf("ListBySubject: %v", err)
	}
	if len(list) != 1 || list[0].ID != "dec-1" {
		t.Errorf("ListBySubject returned %+v, want just dec-1", list)
	}
}

func TestGrantsLiveWindow(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	now := time.Now().UTC()
	storetest.Seed(t, st, storetest.Fixture{
		Tenant: tenantA,
		Grants: []store.Grant{
			storetest.Grant("g-live", "alice", 30*time.Minute),
			{
				ID:        "g-expired",
				SubjectID: "alice",
				Scope:     "target:*",
				NotBefore: now.Add(-2 * time.Hour),
				ExpiresAt: now.Add(-time.Hour),
				Origin:    store.GrantOriginWorkflow,
			},
			{
				ID:        "g-future",
				SubjectID: "alice",
				Scope:     "target:*",
				NotBefore: now.Add(time.Hour),
				ExpiresAt: now.Add(2 * time.Hour),
				Origin:    store.GrantOriginExternal,
				// "Explain why" that cannot name the ticket is not an
				// explanation (M10).
				ExternalRef: "INC-4711",
			},
		},
	})

	live, err := st.Grants().ListLive(ctx, tenantA, "alice", now)
	if err != nil {
		t.Fatalf("ListLive: %v", err)
	}
	if len(live) != 1 || live[0].ID != "g-live" {
		t.Errorf("ListLive returned %+v, want just g-live", live)
	}

	future, err := st.Grants().Get(ctx, tenantA, "g-future")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if future.ExternalRef != "INC-4711" || future.Origin != store.GrantOriginExternal {
		t.Errorf("Get returned %+v, want the external origin and its reference", future)
	}

	revokedAt := now.Add(time.Minute)
	if err := st.Grants().Revoke(ctx, tenantA, "g-live", revokedAt); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	live, err = st.Grants().ListLive(ctx, tenantA, "alice", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ListLive after Revoke: %v", err)
	}
	if len(live) != 0 {
		t.Errorf("ListLive after Revoke returned %+v, want nothing", live)
	}

	// An auditor asks when access stopped; the second answer is not more
	// true than the first.
	if err := st.Grants().Revoke(ctx, tenantA, "g-live", now.Add(time.Hour)); err != nil {
		t.Fatalf("second Revoke: %v", err)
	}
	got, err := st.Grants().Get(ctx, tenantA, "g-live")
	if err != nil {
		t.Fatalf("Get after second Revoke: %v", err)
	}
	if !got.RevokedAt.Equal(revokedAt.Truncate(time.Microsecond)) {
		t.Errorf("RevokedAt = %v, want the first revocation at %v", got.RevokedAt, revokedAt)
	}

	if err := st.Grants().Revoke(ctx, tenantA, "absent", now); !store.IsNotFound(err) {
		t.Errorf("Revoke of an absent grant: %v, want ErrNotFound", err)
	}
}

// The schema refuses a grant whose window is empty or inverted, rather than
// leaving a row that can never be live.
func TestGrantWindowIsCheckedByTheDatabase(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)

	now := time.Now().UTC()
	err := st.Grants().Insert(t.Context(), tenantA, store.Grant{
		ID:        "backwards",
		SubjectID: "alice",
		Scope:     "target:*",
		NotBefore: now,
		ExpiresAt: now.Add(-time.Hour),
		Origin:    store.GrantOriginManual,
	})
	if !store.IsInvalid(err) {
		t.Errorf("Insert of an inverted window: %v, want ErrInvalid", err)
	}
}

// The transaction helper is here because authorize (0008) reads policy inputs
// and writes a decision record on the same path. A record that survives while
// the read it describes is rolled back — or the reverse — is a record that
// lies.
func TestInTxRollsBackEverythingOrNothing(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	wantErr := store.ErrConflict
	err := st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.Subjects().Upsert(ctx, tenantA, storetest.Subject("bob")); err != nil {
			return err
		}
		if err := tx.Decisions().Insert(ctx, tenantA, store.Decision{
			ID: "dec-tx", SubjectID: "bob", TargetID: "t1", InputsDigest: "x",
			Snapshot: json.RawMessage(`{}`),
		}); err != nil {
			return err
		}
		return wantErr
	})
	if err == nil {
		t.Fatal("InTx: want the callback's error, got nil")
	}

	if _, err := st.Subjects().Get(ctx, tenantA, "bob"); !store.IsNotFound(err) {
		t.Errorf("subject survived a rolled-back transaction: %v", err)
	}
	if _, err := st.Decisions().Get(ctx, tenantA, "dec-tx"); !store.IsNotFound(err) {
		t.Errorf("decision survived a rolled-back transaction: %v", err)
	}

	// And the happy path commits both.
	if err := st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.Subjects().Upsert(ctx, tenantA, storetest.Subject("carol")); err != nil {
			return err
		}
		return tx.Decisions().Insert(ctx, tenantA, store.Decision{
			ID: "dec-ok", SubjectID: "carol", TargetID: "t1", InputsDigest: "x",
			Snapshot: json.RawMessage(`{}`),
		})
	}); err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if _, err := st.Subjects().Get(ctx, tenantA, "carol"); err != nil {
		t.Errorf("subject did not survive a committed transaction: %v", err)
	}
	if _, err := st.Decisions().Get(ctx, tenantA, "dec-ok"); err != nil {
		t.Errorf("decision did not survive a committed transaction: %v", err)
	}
}

// A not-found from inside a transaction must still read as a not-found
// outside it: collapsing it into "the transaction failed" is how M11 is
// violated at the boundary rather than in a query.
func TestInTxPreservesTheErrorKind(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)

	err := st.InTx(t.Context(), func(ctx context.Context, tx *store.Store) error {
		_, err := tx.Subjects().Get(ctx, tenantA, "nobody")
		return err
	})
	if !store.IsNotFound(err) {
		t.Errorf("InTx returned %v, want ErrNotFound to survive the boundary", err)
	}
}
