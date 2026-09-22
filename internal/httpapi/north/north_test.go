// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The north-bound surface's tests.
//
// THE ONE THAT MATTERS MOST IS THE TABLE TEST OVER EVERY REGISTERED ROUTE. M18's
// failure mode is not a wrong decision in the middleware — it is one handler that
// reads the tenant from the path itself — so the test enumerates the ROUTER
// rather than a hand-written list, and a route added in a later phase without
// isolation fails it on the day it is added.

var testKEK = bytes.Repeat([]byte{0x11}, extdefault.KeyEncryptionKeySize)

type serverFixture struct {
	st         *store.Store
	federation *identity.Federation
	server     *north.Server
	http       *httptest.Server
}

func newServer(t *testing.T) *serverFixture {
	t.Helper()
	st := storetest.New(t)

	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	keys, err := extdefault.NewSoftwareKeyStore(st, testKEK)
	if err != nil {
		t.Fatalf("key store: %v", err)
	}
	ca, err := credential.New(st, keys)
	if err != nil {
		t.Fatalf("ca: %v", err)
	}

	server, err := north.New(north.Options{
		Federation:      federation,
		CA:              ca,
		DefaultTenant:   "tenant-a",
		InsecureCookies: true,
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	if err := server.Ensure(context.Background(), "tenant-a"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := ca.Ensure(context.Background(), "tenant-b"); err != nil {
		t.Fatalf("ensure b: %v", err)
	}

	f := &serverFixture{st: st, federation: federation, server: server}
	f.http = httptest.NewServer(server)
	t.Cleanup(f.http.Close)
	return f
}

// token mints a credential scoped to the given tenants and roles.
func (f *serverFixture) token(t *testing.T, home store.Tenant, scopes map[store.Tenant]identity.RoleSet) string {
	t.Helper()
	cred, _, err := f.federation.IssueToken(context.Background(), home, identity.TokenRequest{
		DisplayName: "test", Scopes: scopes,
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return cred.String()
}

func (f *serverFixture) do(t *testing.T, method, path, token string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, f.http.URL+path, body)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func decodeError(t *testing.T, resp *http.Response) north.Error {
	t.Helper()
	var e north.Error
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatalf("error body: %v", err)
	}
	return e
}

// ---------------------------------------------------------------------------
// M18: the tenant is a selector, never a widening
// ---------------------------------------------------------------------------

// samplePath fills a route's pattern with values, selecting tenant-b wherever a
// tenant can be named.
func samplePath(pattern, tenant string) string {
	var parts []string
	for _, segment := range strings.Split(pattern, "/") {
		switch {
		case segment == "{"+north.TenantPathParam+"}":
			parts = append(parts, tenant)
		case strings.HasPrefix(segment, "{"):
			parts = append(parts, "sample")
		default:
			parts = append(parts, segment)
		}
	}
	return strings.Join(parts, "/")
}

func TestEveryRegisteredRouteRefusesATenantOutsideTheCallersScope(t *testing.T) {
	f := newServer(t)
	// An ADMINISTRATOR in tenant-a: the strongest credential there is, and it
	// must still reach nothing in tenant-b.
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{
		"tenant-a": {identity.RoleAdmin},
	})

	tenantRoutes := 0
	for _, route := range f.server.Routes() {
		if route.Access != "tenant" {
			continue
		}
		tenantRoutes++
		t.Run(route.Method+" "+route.Pattern, func(t *testing.T) {
			path := samplePath(route.Pattern, "tenant-b")
			resp := f.do(t, route.Method, path, token, bytes.NewReader([]byte(`{}`)))
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("answered %d, want 403: a tenant-a credential reached tenant-b", resp.StatusCode)
			}
			body := decodeError(t, resp)
			if body.Code != north.CodeTenantOutOfScope {
				t.Fatalf("code %q, want %q", body.Code, north.CodeTenantOutOfScope)
			}
			if body.CorrelationID == "" {
				t.Error("the refusal carries no correlation id")
			}
		})
	}
	if tenantRoutes == 0 {
		t.Fatal("no tenant-scoped routes are registered, so this test asserted nothing")
	}
}

func TestEveryRegisteredRouteRefusesAnUnauthenticatedCaller(t *testing.T) {
	f := newServer(t)
	for _, route := range f.server.Routes() {
		if route.Access == "anonymous" {
			continue
		}
		t.Run(route.Method+" "+route.Pattern, func(t *testing.T) {
			resp := f.do(t, route.Method, samplePath(route.Pattern, "tenant-a"), "",
				bytes.NewReader([]byte(`{}`)))
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("answered %d, want 401", resp.StatusCode)
			}
			if got := decodeError(t, resp).Code; got != north.CodeUnauthenticated {
				t.Fatalf("code %q", got)
			}
		})
	}
}

func TestEveryRouteDeclaresAnAccessClassAndATenantRouteDeclaresAPermission(t *testing.T) {
	f := newServer(t)
	for _, route := range f.server.Routes() {
		switch route.Access {
		case "tenant":
			if route.Permission == "" {
				t.Errorf("%s names no permission", route)
			}
			if !slices.Contains(identity.AllPermissions, route.Permission) {
				t.Errorf("%s names permission %q, which this build does not define", route, route.Permission)
			}
		case "authenticated":
			// A route about the CALLER rather than about a tenant's data.
			// One carrying a tenant selector in its path would be a route
			// whose selector nothing checks.
			if north.SelectsTenantInPath(route.Pattern) {
				t.Errorf("%s carries a tenant selector but resolves no tenant", route)
			}
			if route.Permission != "" {
				t.Errorf("%s names permission %q, which nothing checks", route, route.Permission)
			}
		case "anonymous":
			if route.Permission != "" {
				t.Errorf("%s names permission %q, which nothing checks", route, route.Permission)
			}
		default:
			t.Errorf("%s has access class %q", route, route.Access)
		}
		if route.Summary == "" {
			t.Errorf("%s has no summary, so the startup log and the route listing say nothing", route)
		}
	}
}

func TestARouteThatIsNotFullyClassifiedIsRefusedAtRegistration(t *testing.T) {
	handler := func(http.ResponseWriter, *http.Request) {}
	wrap := func(north.Route) http.Handler { return http.HandlerFunc(handler) }

	for name, route := range map[string]north.Route{
		"tenant route with no permission": {
			Method: "GET", Pattern: "/x", Access: north.AccessTenant, Handler: handler,
		},
		"tenant route with an unknown permission": {
			Method: "GET", Pattern: "/x", Access: north.AccessTenant,
			Permission: "policy:invent", Handler: handler,
		},
		"anonymous route carrying a permission": {
			Method: "GET", Pattern: "/x", Access: north.AccessAnonymous,
			Permission: identity.PermPolicyRead, Handler: handler,
		},
		"no handler": {
			Method: "GET", Pattern: "/x", Access: north.AccessAnonymous,
		},
		"no method": {
			Pattern: "/x", Access: north.AccessAnonymous, Handler: handler,
		},
	} {
		router := north.NewRouter(wrap)
		if err := router.Register(route); err == nil {
			t.Errorf("%s: registered anyway", name)
		}
	}

	// A duplicate is refused too: two handlers on one method and path is a
	// route where which one answers depends on registration order.
	router := north.NewRouter(wrap)
	route := north.Route{Method: "GET", Pattern: "/x", Access: north.AccessAnonymous, Handler: handler}
	if err := router.Register(route); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := router.Register(route); err == nil {
		t.Error("a duplicate route registered")
	}
}

func TestARoleGrantedInOneTenantConfersNothingInAnotherOverTheWire(t *testing.T) {
	// The same rule as the unit test, asserted through the whole chain and for
	// an administrator as well as a restricted role.
	f := newServer(t)

	for name, scopes := range map[string]map[store.Tenant]identity.RoleSet{
		"administrator": {"tenant-a": {identity.RoleAdmin}},
		"auditor":       {"tenant-a": {identity.RoleAuditor}},
	} {
		t.Run(name, func(t *testing.T) {
			token := f.token(t, "tenant-a", scopes)

			// Its own tenant: reachable.
			resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/ca", token, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("own tenant answered %d", resp.StatusCode)
			}
			// The other: refused, whatever the role.
			resp = f.do(t, "GET", "/api/v1/tenants/tenant-b/ca", token, nil)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("other tenant answered %d", resp.StatusCode)
			}
			if got := decodeError(t, resp).Code; got != north.CodeTenantOutOfScope {
				t.Fatalf("code %q", got)
			}
		})
	}
}

func TestATenantSelectorInAHeaderOrAQueryIsCheckedTheSameWay(t *testing.T) {
	// Three ways to select and one place that checks.
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{
		"tenant-a": {identity.RoleAdmin},
	})

	req, err := http.NewRequest("GET", f.http.URL+"/api/v1/tenants/tenant-b/ca", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(north.TenantHeader, "tenant-a")
	resp, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// The PATH wins, and it names a tenant outside scope.
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a header did not override a path selector as expected: %d", resp.StatusCode)
	}
}

func TestAPrincipalScopedToSeveralTenantsMustSelectOne(t *testing.T) {
	// Never defaulted: a default would silently pick one.
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{
		"tenant-a": {identity.RoleAdmin},
		"tenant-b": {identity.RoleAdmin},
	})

	// A route with a tenant in its path is unambiguous.
	if resp := f.do(t, "GET", "/api/v1/tenants/tenant-b/ca", token, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("an in-scope tenant answered %d", resp.StatusCode)
	}
}

func TestASingleTenantDeploymentNeverHasToNameATenant(t *testing.T) {
	// M18: with one tenant, no route gains a required parameter and nobody has
	// to know tenancy exists.
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{
		"tenant-a": {identity.RoleAuditor},
	})

	resp := f.do(t, "GET", "/api/v1/session", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("whoami answered %d", resp.StatusCode)
	}
	// And the pre-authentication routes resolve the configured tenant.
	resp = f.do(t, "GET", "/api/v1/session/methods", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in methods answered %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// RBAC
// ---------------------------------------------------------------------------

func TestAPermissionTheCallerDoesNotHoldIsADifferentRefusalFromAWrongTenant(t *testing.T) {
	// One is a missing role, the other is a credential being used against the
	// wrong estate: two different operator problems.
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{
		"tenant-a": {identity.RoleAuditor},
	})

	// ca:read: the auditor holds it.
	if resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/ca", token, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("ca:read answered %d", resp.StatusCode)
	}
	// ca:rotate: it does not.
	resp := f.do(t, "POST", "/api/v1/tenants/tenant-a/ca/rotate", token, bytes.NewReader([]byte(`{}`)))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ca:rotate answered %d, want 403", resp.StatusCode)
	}
	body := decodeError(t, resp)
	if body.Code != north.CodeForbidden {
		t.Fatalf("code %q, want %q", body.Code, north.CodeForbidden)
	}
	if body.Parameters["permission"] != string(identity.PermCARotate) {
		t.Errorf("the refusal does not name the permission: %v", body.Parameters)
	}
}

func TestTheSessionResponseRendersTheCallersScopeWithoutDecidingAnything(t *testing.T) {
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{
		"tenant-a": {identity.RolePolicyAuthor},
	})

	resp := f.do(t, "GET", "/api/v1/session", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("whoami answered %d", resp.StatusCode)
	}
	var body struct {
		Kind   string `json:"kind"`
		Scopes map[string]struct {
			Roles       []string `json:"roles"`
			Permissions []string `json:"permissions"`
		} `json:"scopes"`
		BreakGlass bool `json:"break_glass"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.Kind != string(store.PrincipalToken) {
		t.Errorf("kind: %q", body.Kind)
	}
	if body.BreakGlass {
		t.Error("a machine token is flagged break-glass")
	}
	scope, ok := body.Scopes["tenant-a"]
	if !ok {
		t.Fatalf("scopes: %v", body.Scopes)
	}
	if !slices.Contains(scope.Roles, "policy-author") {
		t.Errorf("roles: %v", scope.Roles)
	}
	if !slices.Contains(scope.Permissions, string(identity.PermPolicyPublish)) {
		t.Errorf("permissions: %v", scope.Permissions)
	}
	if _, leaked := body.Scopes["tenant-b"]; leaked {
		t.Error("the response names a tenant the caller has no scope in")
	}
}

// ---------------------------------------------------------------------------
// the envelope and the chain
// ---------------------------------------------------------------------------

func TestEveryErrorCarriesACodeAMessageAndTheCorrelationID(t *testing.T) {
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{
		"tenant-a": {identity.RoleAuditor},
	})

	cases := []struct {
		name   string
		method string
		path   string
		token  string
		status int
		code   string
	}{
		{"unauthenticated", "GET", "/api/v1/tenants/tenant-a/ca", "", http.StatusUnauthorized, north.CodeUnauthenticated},
		{"forbidden", "POST", "/api/v1/tenants/tenant-a/ca/rotate", token, http.StatusForbidden, north.CodeForbidden},
		{"tenant out of scope", "GET", "/api/v1/tenants/tenant-b/ca", token, http.StatusForbidden, north.CodeTenantOutOfScope},
		{"not found", "GET", "/api/v1/nothing-here", token, http.StatusNotFound, north.CodeNotFound},
		{"method not allowed", "DELETE", "/api/v1/tenants/tenant-a/ca", token, http.StatusMethodNotAllowed, north.CodeMethodNotAllowed},
		{"invalid request", "POST", "/api/v1/session/local", "", http.StatusBadRequest, north.CodeInvalidRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body io.Reader
			if c.method == "POST" {
				body = bytes.NewReader([]byte(`{}`))
			}
			resp := f.do(t, c.method, c.path, c.token, body)
			if resp.StatusCode != c.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, c.status)
			}
			e := decodeError(t, resp)
			if e.Code != c.code {
				t.Errorf("code %q, want %q", e.Code, c.code)
			}
			if e.Message == "" {
				t.Error("no English message, so curl and CI logs say nothing")
			}
			if e.CorrelationID == "" {
				t.Error("no correlation id (M11)")
			}
			if header := resp.Header.Get(north.CorrelationHeader); header != e.CorrelationID {
				t.Errorf("the header and the body disagree: %q / %q", header, e.CorrelationID)
			}
		})
	}
}

func TestEveryCodeThisSurfaceProducesIsDeclared(t *testing.T) {
	// Adding one is deliberate; reusing one for a different condition is not
	// allowed (0014's rule, decided here because the first routes are here).
	if len(north.AllCodes) != len(slices.Compact(slices.Sorted(slices.Values(north.AllCodes)))) {
		t.Fatalf("a code is declared twice: %v", north.AllCodes)
	}
	for _, code := range north.AllCodes {
		if code == "" || strings.ToLower(code) != code {
			t.Errorf("%q is not a stable lower-case identifier", code)
		}
	}
}

func TestACallerSuppliedCorrelationIDIsEchoedAndFiltered(t *testing.T) {
	f := newServer(t)

	for supplied, want := range map[string]string{
		"my-request-42": "my-request-42",
		// A newline, a control character or a kilobyte of text must not reach
		// a log line or an error body.
		"bad value":              "",
		"<script>alert(1)":       "",
		strings.Repeat("x", 200): "",
	} {
		req, err := http.NewRequest("GET", f.http.URL+"/api/v1/session/methods", nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set(north.CorrelationHeader, supplied)
		resp, err := f.http.Client().Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		_ = resp.Body.Close()
		got := resp.Header.Get(north.CorrelationHeader)
		if want != "" && got != want {
			t.Errorf("%q was not echoed: %q", supplied, got)
		}
		if want == "" && got == supplied {
			t.Errorf("%q survived the filter", supplied)
		}
		if got == "" {
			t.Errorf("%q produced no correlation id at all", supplied)
		}
	}
}

func TestNoContractRouteIsReachableOnThisListener(t *testing.T) {
	// M2: the two surfaces never share a port, a chain, or a credential type.
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{
		"tenant-a": {identity.RoleAdmin},
	})
	for _, path := range []string{
		"/v1/authorize", "/v1/auth/cert", "/v1/auth/password", "/v1/logs/batch",
		"/v1/hostkeys/report", "/v1/uids/lease",
	} {
		resp := f.do(t, "POST", path, token, bytes.NewReader([]byte(`{}`)))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s answered %d on the north-bound listener", path, resp.StatusCode)
		}
	}
	for _, route := range f.server.Routes() {
		if strings.HasPrefix(route.Pattern, "/v1/") {
			t.Errorf("%s is registered on the north-bound listener", route)
		}
	}
}

// ---------------------------------------------------------------------------
// the certificate authority's surface
// ---------------------------------------------------------------------------

func TestTheCARouteRendersTheWholeTrustBundle(t *testing.T) {
	// A client assembling the file itself is a client that will one day publish
	// only the active key, which breaks every session signed a minute before a
	// rotation.
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{
		"tenant-a": {identity.RoleAdmin},
	})

	resp := f.do(t, "POST", "/api/v1/tenants/tenant-a/ca/rotate", token,
		bytes.NewReader([]byte(`{"comment":"quarterly"}`)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate answered %d", resp.StatusCode)
	}
	var rotated struct {
		PreviousKeyID       string `json:"previous_key_id"`
		NewKeyID            string `json:"new_key_id"`
		RevokedCertificates int64  `json:"revoked_certificates"`
		CA                  struct {
			TrustedUserCAKeys string `json:"trusted_user_ca_keys"`
			TrustBundle       []struct {
				KeyID  string `json:"key_id"`
				Active bool   `json:"active"`
			} `json:"trust_bundle"`
		} `json:"ca"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rotated); err != nil {
		t.Fatalf("body: %v", err)
	}
	if rotated.PreviousKeyID == "" || rotated.PreviousKeyID == rotated.NewKeyID {
		t.Fatalf("keys: %q -> %q", rotated.PreviousKeyID, rotated.NewKeyID)
	}
	if rotated.RevokedCertificates != 0 {
		t.Errorf("a routine rotation revoked %d certificates", rotated.RevokedCertificates)
	}
	if len(rotated.CA.TrustBundle) != 2 {
		t.Fatalf("trust bundle: %d entries", len(rotated.CA.TrustBundle))
	}
	if lines := strings.Count(strings.TrimSpace(rotated.CA.TrustedUserCAKeys), "\n") + 1; lines != 2 {
		t.Errorf("TrustedUserCAKeys carries %d lines, want both keys", lines)
	}
}

func TestADeploymentWithNoKeyEncryptionKeyStillServesEverythingElse(t *testing.T) {
	// A listener that will not bind is worse than a CA route that explains
	// itself: the console is exactly what an operator needs in order to fix it.
	st := storetest.New(t)
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	server, err := north.New(north.Options{
		Federation: federation, DefaultTenant: "tenant-a", InsecureCookies: true,
	})
	if err != nil {
		t.Fatalf("a listener without a certificate authority did not build: %v", err)
	}
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	cred, _, err := federation.IssueToken(context.Background(), "tenant-a", identity.TokenRequest{
		DisplayName: "test",
		Scopes:      map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin}},
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	req, err := http.NewRequest("GET", ts.URL+"/api/v1/tenants/tenant-a/ca", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+cred.String())
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", resp.StatusCode)
	}
	var e north.Error
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatalf("body: %v", err)
	}
	if e.Code != north.CodeCANotConfigured {
		t.Fatalf("code %q", e.Code)
	}
	if e.Parameters["config_key"] != "credential.key_encryption_key_env" {
		t.Errorf("the answer does not name the key to set: %v", e.Parameters)
	}

	// And a route that does not need the CA still works.
	req, err = http.NewRequest("GET", ts.URL+"/api/v1/session", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+cred.String())
	resp2, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("session answered %d", resp2.StatusCode)
	}
}

func TestTheClaimMappingRouteNamesItsVersionAndWhatItCanProduce(t *testing.T) {
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{
		"tenant-a": {identity.RoleAuditor},
	})
	if _, err := f.federation.PutMapping(context.Background(), "tenant-a", []byte(
		"schema_version: 1\nclaims:\n  - claim: email\n    attribute: email\n"), "test"); err != nil {
		t.Fatalf("mapping: %v", err)
	}

	resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/identity/claim-mapping", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body struct {
		Version    int      `json:"version"`
		Attributes []string `json:"attributes"`
		Document   string   `json:"document"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.Version != 1 {
		t.Errorf("version: %d", body.Version)
	}
	if !slices.Equal(body.Attributes, []string{"email"}) {
		t.Errorf("attributes: %v", body.Attributes)
	}
	if !strings.Contains(body.Document, "schema_version") {
		t.Errorf("document: %q", body.Document)
	}
}

// ---------------------------------------------------------------------------
// break-glass over the wire
// ---------------------------------------------------------------------------

func TestABreakGlassLoginOverTheWireSaysSoInItsAnswer(t *testing.T) {
	// A break-glass session that looks like a normal one in the UI is the same
	// failure as one that looks normal in the audit record.
	f := newServer(t)
	ctx := context.Background()
	if err := f.st.Subjects().Upsert(ctx, "tenant-a", store.Subject{
		ID: "break-glass", Source: "local", Principals: []string{"root"}, BreakGlass: true,
	}); err != nil {
		t.Fatalf("subject: %v", err)
	}
	digest, err := identity.HashPassword("break-glass", "correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := f.st.SubjectPasswords().Put(ctx, "tenant-a", digest); err != nil {
		t.Fatalf("password: %v", err)
	}

	resp := f.do(t, "POST", "/api/v1/session/local", "", bytes.NewReader([]byte(
		`{"login":"root","password":"correct horse battery staple"}`)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body struct {
		Credential string `json:"credential"`
		Session    struct {
			BreakGlass bool   `json:"break_glass"`
			Source     string `json:"source"`
		} `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if !body.Session.BreakGlass {
		t.Error("the response does not say the session is break-glass")
	}
	if body.Credential == "" {
		t.Error("no credential was returned")
	}

	// A browser gets the same credential as an HttpOnly cookie it cannot leak
	// through script.
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == north.SessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie was set")
	}
	if !cookie.HttpOnly {
		t.Error("the session cookie is readable from script")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Error("the session cookie has no SameSite protection")
	}

	// And it authenticates.
	resp = f.do(t, "GET", "/api/v1/session", body.Credential, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the minted credential does not authenticate: %d", resp.StatusCode)
	}
}

func TestAWrongLocalPasswordIsRefusedWithoutSayingWhich(t *testing.T) {
	f := newServer(t)
	resp := f.do(t, "POST", "/api/v1/session/local", "", bytes.NewReader([]byte(
		`{"login":"nobody","password":"x"}`)))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}
	e := decodeError(t, resp)
	if e.Code != north.CodeLoginRefused {
		t.Fatalf("code %q", e.Code)
	}
	if strings.Contains(strings.ToLower(e.Message), "unknown") {
		t.Errorf("the message distinguishes an unknown login from a wrong password: %q", e.Message)
	}
}

func TestAnUnknownSignInMethodIsFourOhFourRatherThanAnUnauthorised(t *testing.T) {
	f := newServer(t)
	resp := f.do(t, "POST", "/api/v1/session/federated/nope/start", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
}

func TestSignInMethodsAreListedPerTenant(t *testing.T) {
	f := newServer(t)
	ctx := context.Background()
	config, err := json.Marshal(map[string]any{
		"issuer": "https://idp.example.com", "client_id": "c",
		"redirect_uri": "https://control.example.com/cb",
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if err := f.st.Connectors().Upsert(ctx, "tenant-b", store.Connector{
		Name: "adfs", Kind: store.ConnectorOIDC, DisplayName: "Contoso",
		Enabled: true, Config: config,
	}); err != nil {
		t.Fatalf("connector: %v", err)
	}

	// The configured tenant has none.
	resp := f.do(t, "GET", "/api/v1/session/methods", "", nil)
	var body struct {
		Tenant     string           `json:"tenant"`
		Methods    []map[string]any `json:"methods"`
		LocalLogin bool             `json:"local_login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.Tenant != "tenant-a" || len(body.Methods) != 0 || !body.LocalLogin {
		t.Fatalf("tenant-a methods: %+v", body)
	}

	// Another tenant, named by the query parameter a browser can set.
	resp = f.do(t, "GET", "/api/v1/session/methods?tenant=tenant-b", "", nil)
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.Tenant != "tenant-b" || len(body.Methods) != 1 {
		t.Fatalf("tenant-b methods: %+v", body)
	}
	if fmt.Sprint(body.Methods[0]["display_name"]) != "Contoso" {
		t.Errorf("method: %+v", body.Methods[0])
	}
}

func TestEndingASessionRevokesItAndClearsTheCookie(t *testing.T) {
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{
		"tenant-a": {identity.RoleAuditor},
	})

	resp := f.do(t, "DELETE", "/api/v1/session", token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp := f.do(t, "GET", "/api/v1/session", token, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a revoked credential still authenticates: %d", resp.StatusCode)
	}
}

func TestTheAccessLogNamesThePrincipalAndTheTenantItResolved(t *testing.T) {
	// The principal and the tenant are resolved BELOW the logging middleware, on
	// a child context the log line cannot see, so the fields only appear if the
	// holder is threaded through. A log line that silently stops naming who
	// called is the kind of regression nobody notices until an incident.
	var buf bytes.Buffer
	st := storetest.New(t)
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	keys, err := extdefault.NewSoftwareKeyStore(st, testKEK)
	if err != nil {
		t.Fatalf("key store: %v", err)
	}
	ca, err := credential.New(st, keys)
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	if _, err := ca.Ensure(context.Background(), "tenant-a"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	server, err := north.New(north.Options{
		Federation: federation, CA: ca, DefaultTenant: "tenant-a", InsecureCookies: true,
		Logger: slog.New(slog.NewJSONHandler(&buf, nil)),
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	cred, principal, err := federation.IssueToken(context.Background(), "tenant-a", identity.TokenRequest{
		DisplayName: "test",
		Scopes:      map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin}},
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	req, err := http.NewRequest("GET", ts.URL+"/api/v1/tenants/tenant-a/ca", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+cred.String())
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	line := buf.String()
	for _, want := range []string{principal.ID, `"tenant":"tenant-a"`, `"break_glass":false`} {
		if !strings.Contains(line, want) {
			t.Errorf("the access log does not carry %q:\n%s", want, line)
		}
	}
	// And what it must NOT carry.
	if strings.Contains(line, cred.Secret) {
		t.Error("the access log carries the credential")
	}
}
