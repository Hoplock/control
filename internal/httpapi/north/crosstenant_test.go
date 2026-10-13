// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The cross-tenant read class (M24): one route class that spans tenants, whose
// set the middleware resolves and the handler is handed.
//
// The isolation test below enumerates the ROUTER, as the tenant-route one does,
// so a cross-tenant route mounted later is covered on the day it is mounted.

// lacking returns a role that does not hold perm, so a test can scope a
// principal into a tenant without the permission a route needs, whatever that
// permission is.
func lacking(t *testing.T, perm identity.Permission) identity.Role {
	t.Helper()
	for _, r := range identity.AllRoles {
		if !r.Can(perm) {
			return r
		}
	}
	t.Fatalf("every role holds %s, so no principal can be scoped without it", perm)
	return ""
}

func TestEveryCrossTenantRouteResolvesOnlyTheTenantsWhereTheCallerHoldsItsPermission(t *testing.T) {
	f := newServer(t)

	crossRoutes := 0
	for _, route := range f.server.Routes() {
		if route.Access != "cross-tenant" {
			continue
		}
		crossRoutes++
		t.Run(route.Method+" "+route.Pattern, func(t *testing.T) {
			// The route's permission in tenant-a only. Scoped to tenant-b
			// with a role lacking it, and not scoped to tenant-c at all.
			token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{
				"tenant-a": {identity.RoleAuditor},
				"tenant-b": {lacking(t, route.Permission)},
			})
			path := samplePath(route.Pattern, "")

			// No selector: exactly the tenant where it holds the
			// permission, and no one tenant.
			resp := f.do(t, route.Method, path, token, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("answered %d, want 200", resp.StatusCode)
			}
			caller := decodeCaller(t, resp)
			if !slices.Equal(caller.Tenants, []ext.Tenant{"tenant-a"}) {
				t.Errorf("the handler was handed %v, want exactly [tenant-a]", caller.Tenants)
			}
			if caller.Tenant != "" {
				t.Errorf("a cross-tenant read was handed one tenant, %q", caller.Tenant)
			}

			// Naming a tenant in scope without the permission, and one
			// out of scope: both refused, neither dropped, and the
			// handler never runs.
			for _, c := range []struct {
				tenant, code string
				params       map[string]any
			}{
				{"tenant-b", north.CodeForbidden, map[string]any{"tenant": "tenant-b", "permission": string(route.Permission)}},
				{"tenant-c", north.CodeTenantOutOfScope, map[string]any{"tenant": "tenant-c"}},
			} {
				resp := f.do(t, route.Method, path+"?"+north.TenantQueryParam+"="+c.tenant, token, nil)
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("naming %s answered %d, want 403", c.tenant, resp.StatusCode)
				}
				e := decodeError(t, resp)
				if e.Code != c.code {
					t.Errorf("naming %s: code %q, want %q", c.tenant, e.Code, c.code)
				}
				for k, v := range c.params {
					if e.Parameters[k] != v {
						t.Errorf("naming %s: parameter %s = %v, want %v", c.tenant, k, e.Parameters[k], v)
					}
				}
			}
		})
	}
	if crossRoutes == 0 {
		t.Fatal("no cross-tenant route is registered, so this test asserted nothing")
	}
}

func TestControlServesNoCrossTenantRouteOfItsOwn(t *testing.T) {
	// Aggregating across tenants is Enterprise's (its E11); Control's own
	// routes stay per tenant (M24). Registration refuses one, and this
	// asserts the table it serves has none.
	routes, err := north.Table("", nil, nil)
	if err != nil {
		t.Fatalf("Control's own table: %v", err)
	}
	for _, route := range append(routes, newServer(t).server.Routes()...) {
		if route.Access == "cross-tenant" && route.Provider == "" {
			t.Errorf("%s is a cross-tenant route of Control's own", route)
		}
	}
}

// stubBrokers hands out a broker that asserts one fixed identity, so a test can
// drive a whole federated login — Begin, Complete, provisioning and the
// session Control mints — without an IdP.
type stubBrokers struct{ subject string }

func (s stubBrokers) Broker(_ context.Context, _ store.Tenant, c store.Connector) (identity.Broker, error) {
	return stubBroker{name: c.Name, subject: s.subject}, nil
}

type stubBroker struct{ name, subject string }

func (b stubBroker) Name() string              { return b.name }
func (b stubBroker) Kind() store.ConnectorKind { return store.ConnectorOIDC }
func (b stubBroker) Begin(_ context.Context, req identity.BeginRequest) (identity.Begun, error) {
	return identity.Begun{RedirectURL: "https://idp.example/authorize?state=" + url.QueryEscape(req.State)}, nil
}
func (b stubBroker) Complete(context.Context, identity.CompleteRequest) (identity.Assertion, error) {
	return identity.Assertion{Connector: b.name, Subject: b.subject, Login: b.subject}, nil
}
func (b stubBroker) Metadata(context.Context) (string, []byte, error) { return "", nil, nil }

func TestAFederatedSessionCoversTheOneTenantItSignedInThrough(t *testing.T) {
	// Who the instance operator is today (M24): a session is minted for the
	// tenant whose connector it signed in through, so on a cross-tenant read
	// it covers that tenant — even for a person who holds the read in
	// another tenant too. Only a token minted on the box spans tenants.
	ctx := context.Background()
	st := storetest.New(t)
	federation, err := identity.NewFederation(st, identity.WithBrokerFactory(stubBrokers{subject: "alice"}))
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	if err := st.Connectors().Upsert(ctx, "tenant-a", store.Connector{
		Name: "corp", Kind: store.ConnectorOIDC, DisplayName: "Corp", Enabled: true, Config: []byte(`{}`),
	}); err != nil {
		t.Fatalf("connector: %v", err)
	}
	for _, tenant := range []store.Tenant{"tenant-a", "tenant-b"} {
		if err := st.RoleBindings().Bind(ctx, tenant, store.RoleBinding{
			SubjectID: "corp:alice", Role: string(identity.RoleAuditor), GrantedBy: "test",
		}); err != nil {
			t.Fatalf("bind in %s: %v", tenant, err)
		}
	}

	started, err := federation.Begin(ctx, "tenant-a", "corp", "")
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	session, err := federation.Complete(ctx, "tenant-a", "corp", url.Values{"state": {started.State}}, "corr-1")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	server, err := north.New(north.Options{
		Federation: federation, Grants: newGrants(t, st, access.Options{}),
		HostProvider: testHostProvider, HostRoutes: testHostRoutes(),
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	get := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", north.CrossTenantPrefix+"reports/privileged-access"+query, nil)
		req.Header.Set("Authorization", "Bearer "+session.Credential.String())
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		return rec
	}

	rec := get("")
	if rec.Code != http.StatusOK {
		t.Fatalf("a federated session answered %d, want 200", rec.Code)
	}
	caller := decodeCaller(t, rec.Result())
	if !slices.Equal(caller.Tenants, []ext.Tenant{"tenant-a"}) {
		t.Errorf("a session signed in through tenant-a covered %v, want [tenant-a]", caller.Tenants)
	}
	if caller.Subject.ID != "corp:alice" {
		t.Errorf("subject %q, want the person who signed in", caller.Subject.ID)
	}

	// Its binding in tenant-b is not in a session minted in tenant-a.
	if rec := get("?" + north.TenantQueryParam + "=tenant-b"); rec.Code != http.StatusForbidden {
		t.Errorf("naming tenant-b answered %d, want 403", rec.Code)
	} else if e := decodeError(t, rec.Result()); e.Code != north.CodeTenantOutOfScope {
		t.Errorf("naming tenant-b: code %q, want %q", e.Code, north.CodeTenantOutOfScope)
	}
}
