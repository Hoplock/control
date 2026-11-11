// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	inst "github.com/hoplock/control/internal/instance"
	"github.com/hoplock/control/internal/store"
)

// newInstance is the deployment's instance service over the test store, one
// node, as the daemon builds it.
func newInstance(t *testing.T, st *store.Store, registry *fleet.Registry) *inst.Service {
	t.Helper()
	all, err := store.LoadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := inst.New(inst.Options{
		Store: st, Fleet: registry,
		Coordinator: extdefault.NewSingleNode(ext.Node{ID: "local", Version: "test", StartedAt: time.Now()}),
		Versions:    inst.Versions{Software: "test", NorthAPI: north.APIVersion, NorthAPIMin: north.MinAPIVersion},
		DisplayName: "north-test", ExpectedSchema: all[len(all)-1].Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetListener("north", true)
	if err := svc.Report(t.Context()); err != nil {
		t.Fatal(err)
	}
	return svc
}

// supervisorToken mints a supervisor's token, scoped as any token is.
func supervisorToken(t *testing.T, f *serverFixture, scopes map[store.Tenant]identity.RoleSet) string {
	t.Helper()
	cred, _, err := f.federation.IssueToken(t.Context(), "tenant-a", identity.TokenRequest{
		DisplayName: "msp-supervisor", Scopes: scopes, Supervisor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cred.String()
}

func versioned(t *testing.T, f *serverFixture, token, path, declared string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.http.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if declared != "" {
		req.Header.Set(north.APIVersionHeader, declared)
	}
	resp, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// The tenant set a caller sees is the tenants it may read this surface in: a
// tenant-scoped supervisor learns nothing of another tenant, not even that it
// exists (M18).
func TestTheInstanceShowsOnlyTheCallersTenants(t *testing.T) {
	f := newServer(t)
	for _, tn := range []store.Tenant{"tenant-a", "tenant-b"} {
		if err := f.st.Deployment().TouchTenant(t.Context(), tn, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	token := supervisorToken(t, f, map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAuditor}})

	resp := f.send(t, "GET", api+"/instance", token, nil)
	mustStatus(t, resp, http.StatusOK)
	var id inst.Identity
	decodeInto(t, resp, &id)
	if id.InstanceID == "" || id.Versions.NorthAPI != north.APIVersion {
		t.Fatalf("identity = %+v", id)
	}
	var names []string
	for _, tn := range id.Tenants {
		names = append(names, tn.Tenant)
	}
	if !slices.Equal(names, []string{"tenant-a"}) {
		t.Fatalf("a tenant-a supervisor sees tenants %v", names)
	}
	// Another tenant's surface is refused outright.
	mustStatus(t, f.send(t, "GET", "/api/v1/tenants/tenant-b/instance", token, nil), http.StatusForbidden)
	mustStatus(t, f.send(t, "GET", "/api/v1/tenants/tenant-b/instance/health", token, nil), http.StatusForbidden)

	resp = f.send(t, "GET", api+"/instance/health", token, nil)
	mustStatus(t, resp, http.StatusOK)
	var h inst.Health
	decodeInto(t, resp, &h)
	if len(h.Fleet) != 1 || h.Fleet[0].Tenant != "tenant-a" {
		t.Fatalf("health's fleet = %+v, want tenant-a's alone", h.Fleet)
	}
	if len(h.Nodes.Members) != 1 || h.Nodes.Members[0].NodeID != "local" {
		t.Fatalf("nodes = %+v", h.Nodes.Members)
	}
}

// A supervisor's act is in this deployment's audit trail under its own id,
// and the tenant can list and revoke it.
func TestASupervisorIsAttributedListedAndRevocable(t *testing.T) {
	f := newServer(t)
	sup := supervisorToken(t, f, map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin}})
	admin := f.roleToken(t, identity.RoleAdmin)

	var list struct {
		Supervisors []struct {
			PrincipalID string   `json:"principal_id"`
			Roles       []string `json:"roles"`
			Live        bool     `json:"live"`
		} `json:"supervisors"`
	}
	resp := f.send(t, "GET", api+"/instance/supervisors", admin, nil)
	mustStatus(t, resp, http.StatusOK)
	decodeInto(t, resp, &list)
	if len(list.Supervisors) != 1 || !identity.IsSupervisor(list.Supervisors[0].PrincipalID) ||
		!slices.Equal(list.Supervisors[0].Roles, []string{"admin"}) || !list.Supervisors[0].Live {
		t.Fatalf("supervisors = %+v", list.Supervisors)
	}
	supID := list.Supervisors[0].PrincipalID

	// The supervisor acts through the API like any client.
	f.putTarget(t, sup, "t-sup", "sup.example.com")
	recs := f.operatorRecords(t, "operator.target.put")
	if len(recs) == 0 || recs[len(recs)-1].Attributes["principal_id"] != supID {
		t.Fatalf("the supervisor's act is not attributed to it")
	}

	mustStatus(t, f.send(t, "POST", api+"/instance/supervisors/"+supID+"/revoke", admin, nil), http.StatusOK)
	mustStatus(t, f.send(t, "GET", api+"/instance", sup, nil), http.StatusUnauthorized)
	if len(f.operatorRecords(t, "operator.supervisor.revoked")) != 1 {
		t.Fatalf("the revocation was not audited")
	}
	// An ordinary token is not a supervisor, and is not revoked here.
	mustStatus(t, f.send(t, "POST", api+"/instance/supervisors/tok-nope/revoke", admin, nil), http.StatusNotFound)
}

// The version is negotiated: undeclared is the current one, a declared one
// outside the range is refused naming the range, and every answer says which
// version it was served in.
func TestTheAPIVersionIsNegotiated(t *testing.T) {
	f := newServer(t)
	token := f.roleToken(t, identity.RoleAuditor)

	if north.APIVersion != 5 || north.MinAPIVersion != 1 {
		t.Fatalf("the north-bound API is at %d (min %d); docs/API-VERSIONING.md says 5 (min 1)",
			north.APIVersion, north.MinAPIVersion)
	}
	resp := versioned(t, f, token, api+"/instance", "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get(north.APIVersionHeader) != "5" {
		t.Fatalf("undeclared: %d, served %q", resp.StatusCode, resp.Header.Get(north.APIVersionHeader))
	}
	var id inst.Identity
	decodeInto(t, resp, &id)
	if id.Versions.NorthAPI != 5 || id.Versions.NorthAPIMin != 1 {
		t.Fatalf("GET …/instance reports north_api %d, north_api_min %d; want 5 and 1",
			id.Versions.NorthAPI, id.Versions.NorthAPIMin)
	}
	for _, declared := range []string{"0", "6", "one"} {
		resp := versioned(t, f, token, api+"/instance", declared)
		var e north.Error
		decodeInto(t, resp, &e)
		if resp.StatusCode != http.StatusBadRequest || e.Code != north.CodeAPIVersionUnsupported ||
			e.Parameters["max"] != float64(north.APIVersion) || e.CorrelationID == "" {
			t.Fatalf("declared %q: %d %+v", declared, resp.StatusCode, e)
		}
	}
	for _, declared := range []string{"1", "2", "3", "4"} {
		resp := versioned(t, f, token, api+"/instance", declared)
		if resp.StatusCode != http.StatusOK || resp.Header.Get(north.APIVersionHeader) != declared {
			t.Fatalf("declared %s: %d, served %q", declared, resp.StatusCode, resp.Header.Get(north.APIVersionHeader))
		}
	}
}
