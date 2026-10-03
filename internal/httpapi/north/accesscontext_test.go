// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/accessctx"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The push receiver over the wire (0013, M16): the route's permission, the role
// that holds it, the binding naming the very credential, and the statuses a
// refusal is answered with.

const pushPath = "/api/v1/tenants/tenant-a/access-context/itsm/push"

// windowsAsJSON is an integration whose pushes are already WindowAssertions.
type windowsAsJSON struct{}

func (windowsAsJSON) Describe() ext.AccessContextInfo {
	return ext.AccessContextInfo{Name: "itsm", Pushes: true}
}

func (windowsAsJSON) Probe(context.Context, ext.AccessContextQuery) (ext.AccessEvidence, error) {
	return ext.AccessEvidence{}, ext.ErrNoEvidence
}

func (windowsAsJSON) Interpret(_ context.Context, p ext.AccessContextPush) (ext.WindowAssertion, error) {
	var a ext.WindowAssertion
	if err := json.Unmarshal(p.Body, &a); err != nil {
		return ext.WindowAssertion{}, ext.Errorf(ext.PointAccessContextProvider, "itsm", "Interpret", ext.KindMalformed, "%v", err)
	}
	return a, nil
}

// noPrivilegedScopes is a policy that marks nothing privileged.
type noPrivilegedScopes struct{}

func (noPrivilegedScopes) ScopeDeclaration(context.Context, store.Tenant, string) (model.ScopeDecl, error) {
	return model.ScopeDecl{}, nil
}

type pushFixture struct {
	st         *store.Store
	federation *identity.Federation
	http       *httptest.Server
	context    *accessctx.Service
}

func newPushServer(t *testing.T) *pushFixture {
	t.Helper()
	st := storetest.New(t)
	storetest.Seed(t, st, storetest.Fixture{
		Tenant:   "tenant-a",
		Subjects: []store.Subject{storetest.Subject("alice@example.com")},
		Targets: []store.Target{{ID: "t-db", Hostname: "db01.example.com", Zone: "edge",
			Labels: map[string]string{"env": "prod"}}},
	})
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	grants := newGrants(t, st, access.Options{})
	ingest, err := audit.New(audit.Options{Store: st})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	emitter, err := audit.NewEmitter(ingest)
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}
	providers, err := accessctx.NewProviders([]ext.Bound[ext.AccessContextProvider]{{
		Registration: ext.Registration{Provider: "example.com/itsm"}, Impl: windowsAsJSON{},
	}}, 0)
	if err != nil {
		t.Fatalf("providers: %v", err)
	}
	svc, err := accessctx.New(accessctx.Options{
		Store: st, Grants: grants, Recorder: emitter, Providers: providers, Policy: noPrivilegedScopes{},
	})
	if err != nil {
		t.Fatalf("accessctx: %v", err)
	}
	server, err := north.New(north.Options{
		Federation: federation, Grants: grants, AccessContext: svc, MaxPushBytes: 2048,
		DefaultTenant: "tenant-a", InsecureCookies: true,
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	f := &pushFixture{st: st, federation: federation, context: svc, http: httptest.NewServer(server)}
	t.Cleanup(f.http.Close)
	return f
}

func (f *pushFixture) tokenFor(t *testing.T, roles ...identity.Role) (string, string) {
	t.Helper()
	cred, principal, err := f.federation.IssueToken(context.Background(), "tenant-a", identity.TokenRequest{
		DisplayName: "itsm integration", Scopes: map[store.Tenant]identity.RoleSet{"tenant-a": roles},
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return cred.String(), principal.ID
}

func (f *pushFixture) bind(t *testing.T, principals ...string) {
	t.Helper()
	if _, err := f.context.PutBinding(t.Context(), "tenant-a",
		access.Actor{Principal: "p-admin", Subject: "admin@example.com"},
		store.AccessContextBinding{
			Provider: "itsm", Mode: store.ExternalPush, Scope: "change-window",
			Subjects: []string{"alice@example.com"}, Targets: []string{"*.example.com"},
			MaxWindow: time.Hour, PushPrincipals: principals, Enabled: true,
		}); err != nil {
		t.Fatalf("bind: %v", err)
	}
}

func (f *pushFixture) push(t *testing.T, path, token string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.http.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func window(id string, targets ...string) []byte {
	now := time.Now().UTC()
	b, _ := json.Marshal(map[string]any{
		"id": id, "reference": "CHG-42", "subject": "alice@example.com", "targets": targets,
		"window": map[string]any{"NotBefore": now, "NotAfter": now.Add(30 * time.Minute)},
	})
	return b
}

func TestThePushReceiverOverTheWire(t *testing.T) {
	f := newPushServer(t)
	token, principal := f.tokenFor(t, identity.RoleIntegration)
	f.bind(t, principal)

	// Admitted: 201, the grant the window became, and how it was bounded.
	first := window("CHG-42/1", "db01.example.com")
	resp := f.push(t, pushPath, token, first)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("push = %d %+v", resp.StatusCode, decodeError(t, resp))
	}
	var created struct {
		Grant    string `json:"grant"`
		State    string `json:"state"`
		Replayed bool   `json:"replayed"`
		Ceiling  int64  `json:"ceiling_seconds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Grant == "" || created.State != "active" || created.Replayed || created.Ceiling != 3600 {
		t.Errorf("created = %+v", created)
	}

	// The same assertion again: 200, the same grant. The same id with a
	// different window — a later stamp — is a conflict, never a second grant.
	again := f.push(t, pushPath, token, first)
	if again.StatusCode != http.StatusOK {
		t.Errorf("a replay = %d, want 200", again.StatusCode)
	}
	time.Sleep(time.Millisecond)
	if r := f.push(t, pushPath, token, window("CHG-42/1", "db01.example.com")); r.StatusCode != http.StatusConflict ||
		decodeError(t, r).Code != north.CodeAssertionConflict {
		t.Errorf("a different window under a used id = %d, want 409", r.StatusCode)
	}

	// Outside the binding: 403 with the field, and an escalation record.
	out := f.push(t, pushPath, token, window("CHG-42/2", "db01.corp.internal"))
	if e := decodeError(t, out); out.StatusCode != http.StatusForbidden || e.Code != north.CodeOutsideScope ||
		e.Parameters["field"] != "targets[0]" {
		t.Errorf("outside the scope = %d %+v", out.StatusCode, e)
	}
	recs, err := f.st.Audit().Query(t.Context(), "tenant-a", store.AuditQuery{Event: accessctx.EventEscalation, Limit: 10})
	if err != nil || len(recs) != 1 {
		t.Errorf("escalation records = %v, %v", recs, err)
	}

	// Unknown provider: 404. Oversized: 413.
	if r := f.push(t, "/api/v1/tenants/tenant-a/access-context/nobody/push", token, window("x", "db01.example.com")); r.StatusCode != http.StatusNotFound || decodeError(t, r).Code != north.CodeProviderNotFound {
		t.Errorf("an unknown provider = %d", r.StatusCode)
	}
	big := []byte(`{"id":"big","padding":"` + strings.Repeat("x", 4096) + `"}`)
	if r := f.push(t, pushPath, token, big); r.StatusCode != http.StatusRequestEntityTooLarge || decodeError(t, r).Code != north.CodePayloadTooLarge {
		t.Errorf("an oversized push = %d", r.StatusCode)
	}
	// A malformed one: 400.
	if r := f.push(t, pushPath, token, []byte(`not json`)); r.StatusCode != http.StatusBadRequest || decodeError(t, r).Code != north.CodeAssertionMalformed {
		t.Errorf("a malformed push = %d", r.StatusCode)
	}
}

// Only a credential holding access-context:push reaches the receiver, only the
// one the binding names gets past it, and the integration's credential reads
// nothing else on this surface — not even the grants its pushes became.
func TestOnlyTheNamedIntegrationCredentialMayPush(t *testing.T) {
	f := newPushServer(t)
	named, principal := f.tokenFor(t, identity.RoleIntegration)
	other, _ := f.tokenFor(t, identity.RoleIntegration)
	desk, _ := f.tokenFor(t, identity.RoleGrantAdmin)
	f.bind(t, principal)

	if r := f.push(t, pushPath, desk, window("a", "db01.example.com")); r.StatusCode != http.StatusForbidden || decodeError(t, r).Code != north.CodeForbidden {
		t.Errorf("a grant desk credential = %d; the route needs access-context:push", r.StatusCode)
	}
	if r := f.push(t, pushPath, other, window("b", "db01.example.com")); r.StatusCode != http.StatusForbidden || decodeError(t, r).Code != north.CodePushNotPermitted {
		t.Errorf("another integration's credential = %d; the binding names one credential", r.StatusCode)
	}
	if r := f.push(t, pushPath, named, window("c", "db01.example.com")); r.StatusCode != http.StatusCreated {
		t.Errorf("the named credential = %d", r.StatusCode)
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, f.http.URL+"/api/v1/tenants/tenant-a/grants", nil)
	req.Header.Set("Authorization", "Bearer "+named)
	resp, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("the integration credential listing grants = %d, want 403: it reads nothing", resp.StatusCode)
	}
}
