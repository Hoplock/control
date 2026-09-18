// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

func fleetNow() time.Time { return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) }

// hasRung decodes a stored capability document, which is jsonb and therefore
// re-rendered on the way out — so it is compared semantically, never as bytes.
func hasRung(t *testing.T, raw json.RawMessage, rung string) bool {
	t.Helper()
	var doc struct {
		Execution []string `json:"execution"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode capabilities %s: %v", raw, err)
	}
	return slices.Contains(doc.Execution, rung)
}

// The enrollment grant's one-time-ness is a DATABASE guarantee, not a Go one: the
// check and the write are one statement, so two enrollments racing on one token
// cannot both win.
func TestEnrollmentConsumeIsOneStatement(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	if err := st.ProxyEnrollments().Create(ctx, tenantA, store.ProxyEnrollment{
		ProxyID:      "p-edge",
		GrantedZones: []string{"edge"},
		TokenHash:    []byte("hash-1"),
		CreatedBy:    "operator",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := st.ProxyEnrollments().Consume(ctx, tenantA, "p-edge", fleetNow()); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	if err := st.ProxyEnrollments().Consume(ctx, tenantA, "p-edge", fleetNow()); !store.IsConflict(err) {
		t.Errorf("second Consume: %v, want ErrConflict", err)
	}
	// Absent and already-consumed are told apart: they are different operator
	// problems.
	if err := st.ProxyEnrollments().Consume(ctx, tenantA, "p-nobody", fleetNow()); !store.IsNotFound(err) {
		t.Errorf("Consume for an unknown proxy: %v, want ErrNotFound", err)
	}

	got, err := st.ProxyEnrollments().Get(ctx, tenantA, "p-edge")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ConsumedAt.IsZero() {
		t.Error("the consumed grant carries no time")
	}
	if !slices.Equal(got.GrantedZones, []string{"edge"}) {
		t.Errorf("granted zones = %v", got.GrantedZones)
	}
}

// Re-issuing a grant is a conflict rather than an overwrite: overwriting one
// silently is how a spent token comes back to life.
func TestReIssuingAnEnrollmentGrantIsAConflict(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	grant := store.ProxyEnrollment{ProxyID: "p-edge", GrantedZones: []string{"edge"}, TokenHash: []byte("h")}
	if err := st.ProxyEnrollments().Create(ctx, tenantA, grant); err != nil {
		t.Fatalf("Create: %v", err)
	}
	grant.TokenHash = []byte("h2")
	if err := st.ProxyEnrollments().Create(ctx, tenantA, grant); !store.IsConflict(err) {
		t.Errorf("Create again: %v, want ErrConflict", err)
	}
}

// THE ONE GLOBAL UNIQUENESS CONSTRAINT IN THIS SCHEMA (M18): a token hash may not
// exist under two tenants, because south-bound tenancy is resolved from the
// enrolment credential and a credential resolving to two authorities is the
// vulnerability class M18 exists to close.
func TestATokenHashCannotExistUnderTwoTenants(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	hash := []byte("the-same-secret")
	if err := st.ProxyEnrollments().Create(ctx, tenantA, store.ProxyEnrollment{
		ProxyID: "p-edge", GrantedZones: []string{"edge"}, TokenHash: hash,
	}); err != nil {
		t.Fatalf("Create for tenant A: %v", err)
	}
	if err := st.ProxyEnrollments().Create(ctx, tenantB, store.ProxyEnrollment{
		ProxyID: "p-edge", GrantedZones: []string{"edge"}, TokenHash: hash,
	}); !store.IsConflict(err) {
		t.Errorf("Create for tenant B with the same hash: %v, want ErrConflict", err)
	}

	// Everything ELSE is tenant-scoped, so two tenants may reuse a proxy id.
	if err := st.ProxyEnrollments().Create(ctx, tenantB, store.ProxyEnrollment{
		ProxyID: "p-edge", GrantedZones: []string{"edge"}, TokenHash: []byte("a-different-secret"),
	}); err != nil {
		t.Errorf("two tenants cannot reuse a proxy id: %v", err)
	}
}

// A declaration is the whole edge set: an edge a proxy stops declaring must be
// removed rather than left routable.
func TestReplacingEdgesRemovesWhatWasNotDeclared(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	first := []store.ProxyEdge{
		{ToZone: "region", Direction: store.HopDial, Address: "gw:22", Cost: 1},
		{ToZone: "enclave", Direction: store.HopRelay, Cost: 2},
	}
	if err := st.ProxyEdges().ReplaceForProxy(ctx, tenantA, "p-edge", first); err != nil {
		t.Fatalf("ReplaceForProxy: %v", err)
	}
	if got, err := st.ProxyEdges().List(ctx, tenantA); err != nil || len(got) != 2 {
		t.Fatalf("List = %d edge(s), %v; want 2", len(got), err)
	}

	if err := st.ProxyEdges().ReplaceForProxy(ctx, tenantA, "p-edge", first[:1]); err != nil {
		t.Fatalf("ReplaceForProxy: %v", err)
	}
	got, err := st.ProxyEdges().List(ctx, tenantA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ToZone != "region" {
		t.Errorf("edges = %+v, want only the region edge", got)
	}
}

// A proxy may declare more than one way into one zone: in a segmented estate it
// can legitimately dial one member and reach another over a relay registration.
func TestAProxyMayDeclareTwoWaysIntoOneZone(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	edges := []store.ProxyEdge{
		{ToZone: "enclave", Direction: store.HopDial, Address: "a:22", NextProxyID: "p-a", Cost: 1},
		{ToZone: "enclave", Direction: store.HopRelay, NextProxyID: "p-b", Cost: 1},
	}
	if err := st.ProxyEdges().ReplaceForProxy(ctx, tenantA, "p-edge", edges); err != nil {
		t.Fatalf("ReplaceForProxy: %v", err)
	}
	got, err := st.ProxyEdges().List(ctx, tenantA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d edge(s), want 2", len(got))
	}
}

// The edge validations are argument errors rather than constraint violations, so a
// caller reads what is wrong rather than a SQLSTATE.
func TestEdgeValidationsRefuseBeforeSQL(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	bad := map[string]store.ProxyEdge{
		"no zone":                {Direction: store.HopDial, Address: "a:22", Cost: 1},
		"unknown direction":      {ToZone: "z", Direction: "teleport", Cost: 1},
		"non-positive cost":      {ToZone: "z", Direction: store.HopDial, Address: "a:22"},
		"dial with no way there": {ToZone: "z", Direction: store.HopDial, Cost: 1},
	}
	for name, edge := range bad {
		if err := st.ProxyEdges().ReplaceForProxy(ctx, tenantA, "p-edge", []store.ProxyEdge{edge}); !store.IsInvalid(err) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

// The upstream reports what it holds, so a registration it stops reporting has
// dropped — and must leave routing rather than linger.
func TestRelayRegistrationsAreReplacedNotAccumulated(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	now := fleetNow()

	if err := st.RelayRegistrations().ReplaceForUpstream(ctx, tenantA, "p-region",
		[]string{"p-enclave-1", "p-enclave-2"}, now); err != nil {
		t.Fatalf("ReplaceForUpstream: %v", err)
	}
	if got, err := st.RelayRegistrations().List(ctx, tenantA); err != nil || len(got) != 2 {
		t.Fatalf("List = %d, %v; want 2", len(got), err)
	}

	// registered_at survives a re-report: an operator wants to know how long the
	// link has been up, not when it was last confirmed.
	later := now.Add(time.Minute)
	if err := st.RelayRegistrations().ReplaceForUpstream(ctx, tenantA, "p-region",
		[]string{"p-enclave-1"}, later); err != nil {
		t.Fatalf("ReplaceForUpstream: %v", err)
	}
	got, err := st.RelayRegistrations().List(ctx, tenantA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].DownstreamProxyID != "p-enclave-1" {
		t.Fatalf("registrations = %+v, want only p-enclave-1", got)
	}
	if !got[0].RegisteredAt.Equal(now) {
		t.Errorf("registered_at = %v, want it left at %v", got[0].RegisteredAt, now)
	}
	if !got[0].LastSeenAt.Equal(later) {
		t.Errorf("last_seen_at = %v, want %v", got[0].LastSeenAt, later)
	}

	// Reporting nothing clears the upstream's registrations entirely.
	if err := st.RelayRegistrations().ReplaceForUpstream(ctx, tenantA, "p-region", nil, later); err != nil {
		t.Fatalf("ReplaceForUpstream: %v", err)
	}
	if got, err := st.RelayRegistrations().List(ctx, tenantA); err != nil || len(got) != 0 {
		t.Errorf("List = %d, %v; want none", len(got), err)
	}
}

// Configuration versions are immutable, and the desired pointer records what it
// displaced so a rollback has a target.
func TestConfigVersionsAreImmutableAndDesiredRecordsThePrevious(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	scope := store.ConfigScope{Kind: store.ConfigScopeZone, ID: "region"}

	for v := int64(1); v <= 2; v++ {
		next, err := st.ProxyConfigs().NextVersion(ctx, tenantA, scope)
		if err != nil {
			t.Fatalf("NextVersion: %v", err)
		}
		if next != v {
			t.Fatalf("NextVersion = %d, want %d", next, v)
		}
		if err := st.ProxyConfigs().InsertVersion(ctx, tenantA, store.ProxyConfigVersion{
			Scope: scope, Version: v, Document: json.RawMessage(`{"k":"v"}`), Hash: "h", CreatedBy: "op",
		}); err != nil {
			t.Fatalf("InsertVersion %d: %v", v, err)
		}
		if err := st.ProxyConfigs().SetDesired(ctx, tenantA, scope, v, "op", fleetNow()); err != nil {
			t.Fatalf("SetDesired %d: %v", v, err)
		}
	}

	if err := st.ProxyConfigs().InsertVersion(ctx, tenantA, store.ProxyConfigVersion{
		Scope: scope, Version: 2, Document: json.RawMessage(`{"k":"edited"}`), Hash: "h2",
	}); !store.IsConflict(err) {
		t.Errorf("re-inserting version 2: %v, want ErrConflict", err)
	}

	desired, err := st.ProxyConfigs().GetDesired(ctx, tenantA, scope)
	if err != nil {
		t.Fatalf("GetDesired: %v", err)
	}
	if desired.Version != 2 || desired.PreviousVersion != 1 {
		t.Errorf("desired = %d, previous = %d; want 2 and 1", desired.Version, desired.PreviousVersion)
	}

	// Re-publishing the version already desired must not erase the rollback
	// target.
	if err := st.ProxyConfigs().SetDesired(ctx, tenantA, scope, 2, "op", fleetNow()); err != nil {
		t.Fatalf("SetDesired again: %v", err)
	}
	desired, err = st.ProxyConfigs().GetDesired(ctx, tenantA, scope)
	if err != nil {
		t.Fatalf("GetDesired: %v", err)
	}
	if desired.PreviousVersion != 1 {
		t.Errorf("a no-op publish moved previous_version to %d, want 1", desired.PreviousVersion)
	}
}

// A publish does not change what is out there: the running columns are the
// proxy's fact and PutState leaves them alone.
func TestPutStateDoesNotTouchWhatTheProxyReported(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	if err := st.ProxyConfigs().PutState(ctx, tenantA, store.ProxyConfigState{
		ProxyID: "p-a", DesiredVersion: 1, DesiredHash: "h1",
		DesiredDocument: json.RawMessage(`{"k":1}`), DesiredAt: fleetNow(),
	}); err != nil {
		t.Fatalf("PutState: %v", err)
	}
	if err := st.ProxyConfigs().ReportRunning(ctx, tenantA, "p-a", 1, "h1", fleetNow()); err != nil {
		t.Fatalf("ReportRunning: %v", err)
	}
	if err := st.ProxyConfigs().PutState(ctx, tenantA, store.ProxyConfigState{
		ProxyID: "p-a", DesiredVersion: 2, DesiredHash: "h2",
		DesiredDocument: json.RawMessage(`{"k":2}`), DesiredAt: fleetNow(),
	}); err != nil {
		t.Fatalf("second PutState: %v", err)
	}

	got, err := st.ProxyConfigs().GetState(ctx, tenantA, "p-a")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if got.RunningVersion != 1 || got.RunningHash != "h1" {
		t.Errorf("running = %d/%q, want 1/h1: a publish must not rewrite what the proxy reported",
			got.RunningVersion, got.RunningHash)
	}
	if !got.Drifted() {
		t.Error("the state does not report drift")
	}

	if err := st.ProxyConfigs().ReportRunning(ctx, tenantA, "p-nobody", 1, "h", fleetNow()); !store.IsNotFound(err) {
		t.Errorf("ReportRunning for an unknown proxy: %v, want ErrNotFound", err)
	}
}

func TestConfigScopeIsValidated(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	bad := []store.ConfigScope{
		{Kind: "cluster", ID: "x"},
		{Kind: store.ConfigScopeZone},
	}
	for _, scope := range bad {
		if _, err := st.ProxyConfigs().NextVersion(ctx, tenantA, scope); !store.IsInvalid(err) {
			t.Errorf("%+v: %v, want ErrInvalid", scope, err)
		}
	}
}

// An observation with no time is STORED with no time. A NOT NULL column would
// force this server to invent a date, which is the fail-open M17 forbids.
func TestAnUndatedCapabilityRecordStaysUndated(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	if err := st.TargetCapabilities().Put(ctx, tenantA, store.TargetCapabilityRecord{
		Hostname: "db-1.prod", Port: 22, Platform: "linux",
		Execution:  []string{"account-confined"},
		ReportedBy: "p-a",
		// ObservedAt deliberately absent.
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := st.TargetCapabilities().Get(ctx, tenantA, "db-1.prod", 22, "linux")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.ObservedAt.IsZero() {
		t.Errorf("observed_at = %v, want the zero time", got.ObservedAt)
	}
	if got.ReceivedAt.IsZero() {
		t.Error("received_at is zero: the server's own arrival time is not optional")
	}
}

// The platform is part of the key: two drivers can legitimately see the same host
// differently, and one must not overwrite the other.
func TestCapabilityRecordsAreKeyedByPlatform(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	for _, platform := range []string{"", "linux", "fortigate"} {
		if err := st.TargetCapabilities().Put(ctx, tenantA, store.TargetCapabilityRecord{
			Hostname: "fw-1", Port: 22, Platform: platform,
			Execution:  []string{"platform-attested"},
			ObservedAt: fleetNow(),
		}); err != nil {
			t.Fatalf("Put %q: %v", platform, err)
		}
	}

	got, err := st.TargetCapabilities().List(ctx, tenantA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d record(s), want 3 — one per platform", len(got))
	}
}

// Absence is an input to the fail-safe rule, not a failure to hide.
func TestAnAbsentCapabilityRecordIsNotFound(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	if _, err := st.TargetCapabilities().Get(t.Context(), tenantA, "never-probed", 22, ""); !store.IsNotFound(err) {
		t.Errorf("Get: %v, want ErrNotFound", err)
	}
}

// The fleet tables are per tenant like every other (M18): tenant A asking for
// tenant B's rows, by B's own ids, gets nothing.
func TestFleetRepositoriesDoNotReachAnotherTenant(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	now := fleetNow()

	scope := store.ConfigScope{Kind: store.ConfigScopeZone, ID: "shared-zone-name"}
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		bOnly := string(tenant) + "-only"
		if err := st.ProxyEnrollments().Create(ctx, tenant, store.ProxyEnrollment{
			ProxyID: bOnly, GrantedZones: []string{"edge"}, TokenHash: []byte("hash-" + string(tenant)),
		}); err != nil {
			t.Fatalf("seed enrollment for %s: %v", tenant, err)
		}
		if err := st.ProxyEdges().ReplaceForProxy(ctx, tenant, bOnly, []store.ProxyEdge{
			{ToZone: "shared-zone-name", Direction: store.HopDial, Address: "gw:22", Cost: 1},
		}); err != nil {
			t.Fatalf("seed edges for %s: %v", tenant, err)
		}
		if err := st.RelayRegistrations().ReplaceForUpstream(ctx, tenant, bOnly, []string{bOnly + "-down"}, now); err != nil {
			t.Fatalf("seed registrations for %s: %v", tenant, err)
		}
		if err := st.ProxyConfigs().InsertVersion(ctx, tenant, store.ProxyConfigVersion{
			Scope: scope, Version: 1, Document: json.RawMessage(`{"t":"` + string(tenant) + `"}`), Hash: "h",
		}); err != nil {
			t.Fatalf("seed config for %s: %v", tenant, err)
		}
		if err := st.ProxyConfigs().PutState(ctx, tenant, store.ProxyConfigState{
			ProxyID: bOnly, DesiredVersion: 1, DesiredHash: "h", DesiredAt: now,
		}); err != nil {
			t.Fatalf("seed config state for %s: %v", tenant, err)
		}
		if err := st.TargetCapabilities().Put(ctx, tenant, store.TargetCapabilityRecord{
			Hostname: "db-1.shared", Port: 22, Platform: "linux",
			Execution: []string{"account-confined"}, ObservedAt: now,
		}); err != nil {
			t.Fatalf("seed capabilities for %s: %v", tenant, err)
		}
	}

	bOnly := string(tenantB) + "-only"
	reads := map[string]func() error{
		"ProxyEnrollments.Get": func() error {
			_, err := st.ProxyEnrollments().Get(ctx, tenantA, bOnly)
			return err
		},
		"ProxyEnrollments.Consume": func() error {
			return st.ProxyEnrollments().Consume(ctx, tenantA, bOnly, now)
		},
		"ProxyEnrollments.Delete": func() error { return st.ProxyEnrollments().Delete(ctx, tenantA, bOnly) },
		"ProxyConfigs.GetState": func() error {
			_, err := st.ProxyConfigs().GetState(ctx, tenantA, bOnly)
			return err
		},
		"ProxyConfigs.ReportRunning": func() error {
			return st.ProxyConfigs().ReportRunning(ctx, tenantA, bOnly, 1, "h", now)
		},
	}
	for name, read := range reads {
		if err := read(); !store.IsNotFound(err) {
			t.Errorf("%s reached tenant B's row from tenant A: %v, want ErrNotFound", name, err)
		}
	}

	// The list-shaped reads answer with A's rows only, and each tenant's
	// identically named config scope holds its own document.
	if got, err := st.ProxyEdges().List(ctx, tenantA); err != nil || len(got) != 1 {
		t.Errorf("ProxyEdges.List = %d, %v; want exactly tenant A's one edge", len(got), err)
	}
	if got, err := st.RelayRegistrations().List(ctx, tenantA); err != nil || len(got) != 1 {
		t.Errorf("RelayRegistrations.List = %d, %v; want exactly tenant A's one registration", len(got), err)
	}
	if got, err := st.ProxyConfigs().ListStates(ctx, tenantA); err != nil || len(got) != 1 {
		t.Errorf("ProxyConfigs.ListStates = %d, %v; want exactly tenant A's one state", len(got), err)
	}
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		v, err := st.ProxyConfigs().GetVersion(ctx, tenant, scope, 1)
		if err != nil {
			t.Fatalf("GetVersion for %s: %v", tenant, err)
		}
		if want := `{"t":"` + string(tenant) + `"}`; string(v.Document) != want {
			t.Errorf("%s's version 1 document = %s, want %s", tenant, v.Document, want)
		}
	}
	// The same hostname, observed by both tenants, is two records.
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		if _, err := st.TargetCapabilities().Get(ctx, tenant, "db-1.shared", 22, "linux"); err != nil {
			t.Errorf("%s lost its own capability record: %v", tenant, err)
		}
	}
}

// The heartbeat report leaves alone what it does not name, so a proxy that says
// nothing about its capabilities has not withdrawn them.
func TestRecordHealthOnlyReplacesWhatItNames(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	p := storetest.Proxy("p-a", "region")
	p.ContractVersion = 4
	p.DeclaredCapabilities = json.RawMessage(`{"execution":["account-confined"]}`)
	if err := st.Proxies().Upsert(ctx, tenantA, p); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := st.Proxies().RecordHealth(ctx, tenantA, store.ProxyHealthReport{
		ProxyID: "p-a", At: fleetNow(), SessionCount: 3,
	}); err != nil {
		t.Fatalf("RecordHealth: %v", err)
	}
	got, err := st.Proxies().Get(ctx, tenantA, "p-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ContractVersion != 4 {
		t.Errorf("contract version = %d, want it left at 4", got.ContractVersion)
	}
	if !hasRung(t, got.DeclaredCapabilities, "account-confined") {
		t.Errorf("declared capabilities = %s, want them left alone", got.DeclaredCapabilities)
	}
	if got.SessionCount != 3 {
		t.Errorf("session count = %d, want 3", got.SessionCount)
	}
	if !got.LastHeartbeatAt.Equal(fleetNow()) {
		t.Errorf("heartbeat = %v, want %v", got.LastHeartbeatAt, fleetNow())
	}
	if got.LastError != "" || !got.LastErrorAt.IsZero() {
		t.Errorf("a healthy heartbeat recorded an error: %q at %v", got.LastError, got.LastErrorAt)
	}

	// A named capability set replaces the stored one, and a named version
	// replaces the stored version.
	version := 5
	if err := st.Proxies().RecordHealth(ctx, tenantA, store.ProxyHealthReport{
		ProxyID: "p-a", At: fleetNow(), ContractVersion: &version,
		DeclaredCapabilities: json.RawMessage(`{}`),
		LastError:            "boom",
	}); err != nil {
		t.Fatalf("RecordHealth: %v", err)
	}
	got, err = st.Proxies().Get(ctx, tenantA, "p-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ContractVersion != 5 {
		t.Errorf("contract version = %d, want 5", got.ContractVersion)
	}
	if string(got.DeclaredCapabilities) != "{}" {
		t.Errorf("declared capabilities = %s, want {}", got.DeclaredCapabilities)
	}
	if got.LastError != "boom" || got.LastErrorAt.IsZero() {
		t.Errorf("last error = %q at %v, want it recorded with a time", got.LastError, got.LastErrorAt)
	}
}

// Proxies.List is the graph load: it answers with one tenant's fleet, in id
// order, and the ordering is part of the pathfinding tiebreak.
func TestProxiesListIsOneTenantInIDOrder(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		for _, id := range []string{"p-c", "p-a", "p-b"} {
			if err := st.Proxies().Upsert(ctx, tenant, storetest.Proxy(id, "region")); err != nil {
				t.Fatalf("Upsert: %v", err)
			}
		}
	}

	got, err := st.Proxies().List(ctx, tenantA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, p := range got {
		ids = append(ids, p.ID)
	}
	if want := []string{"p-a", "p-b", "p-c"}; !slices.Equal(ids, want) {
		t.Errorf("List = %v, want %v", ids, want)
	}
}
