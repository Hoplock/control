// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// A host binary's routes (M15): mounted in the one table, behind the one
// middleware, answering in the one envelope.

const testHostProvider = "test/host"

// testHostRoutes are the two routes every test server carries. The report
// handler's behaviour is chosen by its {report} segment, so one route can
// exercise every way a host answers.
func testHostRoutes() []north.HostRoute {
	return []north.HostRoute{
		{
			Method: "GET", Pattern: "reports/{report}",
			Permission: identity.PermAuditRead,
			Summary:    "a test host's report",
			Handler:    http.HandlerFunc(hostReport),
		},
		{
			Method: "POST", Pattern: "reports/schedules",
			Permission: identity.PermReportWrite,
			Summary:    "a test host's report schedule",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				answerCaller(w, r, http.StatusCreated)
			}),
		},
	}
}

func hostReport(w http.ResponseWriter, r *http.Request) {
	switch r.PathValue("report") {
	case "missing":
		north.WriteHostError(w, r, http.StatusNotFound, "report_not_found",
			map[string]any{"report": "missing"}, "there is no such report")
	case "undeclared":
		north.WriteHostError(w, r, http.StatusNotFound, "report_vanished", nil, "it went")
	case "unauthorised":
		north.WriteHostError(w, r, http.StatusUnauthorized, "report_not_found", nil, "who are you")
	case "unauthenticated-code":
		north.WriteHostError(w, r, http.StatusForbidden, north.CodeUnauthenticated, nil, "who are you")
	case "internal-as-refusal":
		north.WriteHostError(w, r, http.StatusBadRequest, north.CodeInternal, nil, "not an outage")
	case "success-status":
		north.WriteHostError(w, r, http.StatusOK, "report_not_found", nil, "fine really")
	case "no-message":
		north.WriteHostError(w, r, http.StatusNotFound, "report_not_found", nil, " ")
	case "panic":
		panic("a host handler fell over")
	default:
		answerCaller(w, r, http.StatusOK)
	}
}

// answerCaller writes what the handler was told about its caller.
func answerCaller(w http.ResponseWriter, r *http.Request, status int) {
	caller, ok := north.HostCallerFrom(r.Context())
	if !ok {
		north.WriteHostError(w, r, http.StatusInternalServerError, north.CodeInternal, nil, "no caller")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(caller)
}

func decodeCaller(t *testing.T, resp *http.Response) north.HostCaller {
	t.Helper()
	var c north.HostCaller
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatalf("caller body: %v", err)
	}
	return c
}

func TestAHostRouteRunsOnlyAfterControlHasAuthenticatedResolvedAndChecked(t *testing.T) {
	f := newServer(t)
	cred, principal, err := f.federation.IssueToken(context.Background(), "tenant-a", identity.TokenRequest{
		DisplayName: "auditor", Scopes: map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAuditor}},
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	token := cred.String()

	// Allowed: audit:read, which the auditor holds, in a tenant it may act in.
	resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/reports/quarterly", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("answered %d, want 200", resp.StatusCode)
	}
	caller := decodeCaller(t, resp)
	if caller.Tenant != ext.Tenant("tenant-a") {
		t.Errorf("tenant %q, want the one the middleware resolved", caller.Tenant)
	}
	if caller.Principal != principal.ID {
		t.Errorf("principal %q, want %q", caller.Principal, principal.ID)
	}
	if caller.Subject.ID != "" || caller.Subject.Groups != nil {
		t.Errorf("a machine token arrived with a subject: %+v", caller.Subject)
	}
	if caller.BreakGlass {
		t.Error("a token arrived as break-glass")
	}
	if caller.CorrelationID == "" || caller.CorrelationID != resp.Header.Get(north.CorrelationHeader) {
		t.Errorf("correlation id %q, the response says %q", caller.CorrelationID, resp.Header.Get(north.CorrelationHeader))
	}

	// Each refusal below is the middleware's: the handler would have
	// answered 201 with a caller body, and never a code.
	for _, c := range []struct {
		name, method, path, token string
		status                    int
		code                      string
	}{
		{"without the permission", "POST", "/api/v1/tenants/tenant-a/reports/schedules", token, http.StatusForbidden, north.CodeForbidden},
		{"without a credential", "POST", "/api/v1/tenants/tenant-a/reports/schedules", "", http.StatusUnauthorized, north.CodeUnauthenticated},
		{"in another tenant", "GET", "/api/v1/tenants/tenant-b/reports/quarterly", token, http.StatusForbidden, north.CodeTenantOutOfScope},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp := f.do(t, c.method, c.path, c.token, strings.NewReader(`{}`))
			if resp.StatusCode != c.status {
				t.Fatalf("answered %d, want %d", resp.StatusCode, c.status)
			}
			if got := decodeError(t, resp).Code; got != c.code {
				t.Fatalf("code %q, want %q", got, c.code)
			}
		})
	}
}

// fixedAuthenticator answers with the principal a credential names, so a test
// can present a break-glass or a federated principal without a login flow.
type fixedAuthenticator map[string]*identity.Principal

func (a fixedAuthenticator) Authenticate(_ context.Context, presented string) (*identity.Principal, bool, error) {
	p, ok := a[presented]
	return p, ok, nil
}

func TestBreakGlassArrivesAsAssertedAndAFederatedPrincipalArrivesWithout(t *testing.T) {
	st := storetest.New(t)
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	scope := map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAuditor}}

	breakGlass := identity.NewPrincipal("sess-root", store.PrincipalSession, "tenant-a", scope)
	breakGlass.Subject, breakGlass.Source, breakGlass.BreakGlass = "root", "local", true
	federated := identity.NewPrincipal("sess-alice", store.PrincipalSession, "tenant-a", scope)
	federated.Subject, federated.Source = "alice", "corp-oidc"
	federated.Groups = []string{"auditors", "sre"}

	server, err := north.New(north.Options{
		Federation:    federation,
		Grants:        newGrants(t, st, access.Options{}),
		Authenticator: fixedAuthenticator{"bg": breakGlass, "fed": federated},
		HostProvider:  testHostProvider,
		HostRoutes:    testHostRoutes(),
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}

	for credential, want := range map[string]north.HostCaller{
		"bg":  {Tenant: "tenant-a", Principal: "sess-root", BreakGlass: true, Subject: ext.Subject{ID: "root"}},
		"fed": {Tenant: "tenant-a", Principal: "sess-alice", Subject: ext.Subject{ID: "alice", Groups: []string{"auditors", "sre"}}},
	} {
		req := httptest.NewRequest("GET", "/api/v1/tenants/tenant-a/reports/quarterly", nil)
		req.Header.Set("Authorization", "Bearer "+credential)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: answered %d", credential, rec.Code)
		}
		var got north.HostCaller
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("%s: %v", credential, err)
		}
		got.CorrelationID = ""
		if got.Tenant != want.Tenant || got.Principal != want.Principal || got.BreakGlass != want.BreakGlass ||
			got.Subject.ID != want.Subject.ID || !slices.Equal(got.Subject.Groups, want.Subject.Groups) ||
			got.Subject.Username != "" || got.Subject.ExternalID != "" {
			t.Errorf("%s: caller %+v, want %+v", credential, got, want)
		}
	}
}

func TestAHostAnswersErrorsInControlsEnvelopeOrNotAtAll(t *testing.T) {
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAuditor}})

	resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/reports/missing", token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a declared code answered %d, want 404", resp.StatusCode)
	}
	e := decodeError(t, resp)
	if e.Code != "report_not_found" || e.Message == "" || e.Parameters["report"] != "missing" {
		t.Errorf("the envelope is %+v", e)
	}
	if e.CorrelationID == "" || e.CorrelationID != resp.Header.Get(north.CorrelationHeader) {
		t.Errorf("correlation id %q, header %q", e.CorrelationID, resp.Header.Get(north.CorrelationHeader))
	}

	// Every refusal is an outage, and none of them leaks what the host tried
	// to say.
	for _, report := range []string{
		"undeclared", "unauthorised", "unauthenticated-code", "internal-as-refusal", "success-status", "no-message",
	} {
		t.Run(report, func(t *testing.T) {
			resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/reports/"+report, token, nil)
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("answered %d, want 500", resp.StatusCode)
			}
			e := decodeError(t, resp)
			if e.Code != north.CodeInternal || e.CorrelationID == "" {
				t.Fatalf("envelope %+v, want internal with a correlation id", e)
			}
		})
	}

	// Outside a host route there is no declared set to answer from.
	rec := httptest.NewRecorder()
	north.WriteHostError(rec, httptest.NewRequest("GET", "/", nil), http.StatusNotFound, "report_not_found", nil, "x")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("WriteHostError outside a host route answered %d, want 500", rec.Code)
	}
}

func TestAPanickingHostHandlerIsAnOutageAndTheServerKeepsServing(t *testing.T) {
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAuditor}})

	resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/reports/panic", token, nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("a panic answered %d, want 500", resp.StatusCode)
	}
	if e := decodeError(t, resp); e.Code != north.CodeInternal || e.CorrelationID == "" {
		t.Fatalf("a panic answered %+v", e)
	}
	if resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/reports/quarterly", token, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("after a panic the server answered %d", resp.StatusCode)
	}
}

func TestTheRouteListingNamesWhoseEveryRouteIs(t *testing.T) {
	f := newServer(t)
	var host []string
	for _, route := range f.server.Routes() {
		switch {
		case route.Provider == testHostProvider:
			host = append(host, route.String())
			if !strings.HasPrefix(route.Pattern, north.HostPrefix) || route.Access != "tenant" {
				t.Errorf("host route %s is not a tenant route under %s", route, north.HostPrefix)
			}
		case route.Provider != "":
			t.Errorf("%s names provider %q", route, route.Provider)
		}
	}
	want := []string{
		"GET " + north.HostPrefix + "reports/{report}",
		"POST " + north.HostPrefix + "reports/schedules",
	}
	if !slices.Equal(host, want) {
		t.Errorf("host routes in the listing = %v, want %v", host, want)
	}
}

// Every refusal is a start-up error naming the route — and a mux conflict is
// one of them, never a panic.
func TestAHostRouteControlWillNotMountIsRefusedAtStartUp(t *testing.T) {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	route := func(method, pattern string, perm identity.Permission) north.HostRoute {
		return north.HostRoute{Method: method, Pattern: pattern, Permission: perm, Summary: "x", Handler: ok}
	}
	read := identity.PermAuditRead

	for name, routes := range map[string][]north.HostRoute{
		"an empty method":             {route("", "reports", read)},
		"an unknown method":           {route("BREW", "reports", read)},
		"a leading slash":             {route("GET", "/reports", read)},
		"a tenant wildcard":           {route("GET", "{tenant}/reports", read)},
		"a tenant remainder":          {route("GET", "reports/{tenant...}", read)},
		"a climb out":                 {route("GET", "reports/../ca", read)},
		"a method prefix":             {route("GET", "GET reports", read)},
		"an empty pattern":            {route("GET", "", read)},
		"an undefined permission":     {route("GET", "reports", "report:invent")},
		"no permission":               {route("GET", "reports", "")},
		"no handler":                  {{Method: "GET", Pattern: "reports", Permission: read}},
		"a duplicate host route":      {route("GET", "reports", read), route("GET", "reports", read)},
		"a duplicate Control route":   {route("GET", "grants", read)},
		"a method on Control's path":  {route("PUT", "grants", read)},
		"a mux conflict with Control": {route("GET", "grants/{id}", read)},
		"a mux conflict between two":  {route("GET", "zz/{a}/end", read), route("GET", "zz/start/{b}", read)},
	} {
		t.Run(name, func(t *testing.T) {
			err := north.CheckHost(testHostProvider, routes, nil)
			if err == nil {
				t.Fatal("registered anyway")
			}
			if !strings.Contains(err.Error(), testHostProvider) {
				t.Errorf("the refusal does not name the host: %v", err)
			}
		})
	}

	if err := north.CheckHost(testHostProvider, testHostRoutes(), []string{"report_not_found"}); err != nil {
		t.Fatalf("the test host's own routes were refused: %v", err)
	}
	if err := north.CheckHost("", testHostRoutes(), nil); err == nil {
		t.Error("host routes with no provider registered anyway")
	}
}

func TestAHostErrorCodeMustBeNewAndSnakeCase(t *testing.T) {
	for name, codes := range map[string][]string{
		"Control's own code": {north.CodeForbidden},
		"not snake_case":     {"Report-Missing"},
		"declared twice":     {"report_not_found", "report_not_found"},
		"empty":              {""},
	} {
		if err := north.CheckHost(testHostProvider, nil, codes); err == nil {
			t.Errorf("%s: %q was accepted", name, codes)
		}
	}

	// And at New, which is where a server is actually built.
	st := storetest.New(t)
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	if _, err := north.New(north.Options{
		Federation:     federation,
		Grants:         newGrants(t, st, access.Options{}),
		HostProvider:   testHostProvider,
		HostErrorCodes: []string{north.CodeNotFound},
	}); err == nil {
		t.Error("a host code colliding with Control's built a server")
	}
}
