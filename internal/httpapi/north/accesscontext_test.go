// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/accessctx"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/revoke"
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

// probes counts every Probe call, so a test can say "no provider was asked".
var probes atomic.Int64

func (windowsAsJSON) Probe(context.Context, ext.AccessContextQuery) (ext.AccessEvidence, error) {
	probes.Add(1)
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
	return newPushServerWith(t, nil)
}

// newPushServerWith builds the fixture with revoker publishing revocations; nil
// is the real bus.
func newPushServerWith(t *testing.T, revoker access.Revoker) *pushFixture {
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
	grants := newGrants(t, st, access.Options{Revoker: revoker})
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
	server, err := north.New(withLifecycle(t, st, north.Options{
		Federation: federation, Grants: grants, AccessContext: svc, MaxPushBytes: 2048,
		DefaultTenant: "tenant-a", InsecureCookies: true,
	}))
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

// An end over the wire (0017): 200 whatever it did, with what became of the
// window; an open under an ended id is 400 window_closed on `id`; and an end
// whose sessions could not be told is 503, as the revoke route answers one.

type endAnswer struct {
	AssertionID string `json:"assertion_id"`
	Outcome     string `json:"outcome"`
	Grant       string `json:"grant"`
	State       string `json:"state"`
}

func decodeEnd(t *testing.T, resp *http.Response) endAnswer {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("end = %d %+v, want 200", resp.StatusCode, decodeError(t, resp))
	}
	var a endAnswer
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		t.Fatal(err)
	}
	return a
}

func ended(id string) []byte {
	b, _ := json.Marshal(map[string]any{"id": id, "ended": true})
	return b
}

func TestAnEndOverTheWire(t *testing.T) {
	f := newPushServer(t)
	token, principal := f.tokenFor(t, identity.RoleIntegration)
	f.bind(t, principal)

	resp := f.push(t, pushPath, token, window("CHG-42/1", "db01.example.com"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("open = %d", resp.StatusCode)
	}
	var opened struct {
		Grant string `json:"grant"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&opened); err != nil {
		t.Fatal(err)
	}

	if a := decodeEnd(t, f.push(t, pushPath, token, ended("CHG-42/1"))); a.Outcome != "revoked" ||
		a.AssertionID != "CHG-42/1" || a.Grant != opened.Grant || a.State != "revoked" {
		t.Errorf("an end = %+v, want revoked naming grant %s", a, opened.Grant)
	}
	g, err := f.st.Grants().Get(t.Context(), "tenant-a", opened.Grant)
	if err != nil || g.RevokedBy.Principal != principal || g.RevokeReason != access.ExternalEndReason {
		t.Errorf("the grant = %+v, %v; want it revoked by the integration with Control's reason", g, err)
	}
	if a := decodeEnd(t, f.push(t, pushPath, token, ended("CHG-42/1"))); a.Outcome != "already_ended" || a.Grant != opened.Grant {
		t.Errorf("the end again = %+v, want already_ended", a)
	}

	// An end for an id never opened is an answer, not an error, and the open
	// that follows is refused.
	if a := decodeEnd(t, f.push(t, pushPath, token, ended("CHG-43/1"))); a.Outcome != "recorded" || a.Grant != "" || a.State != "" {
		t.Errorf("an end before its open = %+v, want recorded and no grant", a)
	}
	late := f.push(t, pushPath, token, window("CHG-43/1", "db01.example.com"))
	if e := decodeError(t, late); late.StatusCode != http.StatusBadRequest || e.Code != north.CodeWindowClosed ||
		e.Parameters["field"] != "id" {
		t.Errorf("an open after its end = %d %+v, want 400 window_closed on id", late.StatusCode, e)
	}

	malformed := f.push(t, pushPath, token, ended(""))
	if e := decodeError(t, malformed); malformed.StatusCode != http.StatusBadRequest ||
		e.Code != north.CodeAssertionMalformed || e.Parameters["field"] != "id" {
		t.Errorf("an end with no id = %d %+v, want 400 assertion_malformed on id", malformed.StatusCode, e)
	}
}

// switchableRevoker refuses every publication while it is down.
type switchableRevoker struct {
	next access.Revoker
	down atomic.Bool
}

func (r *switchableRevoker) Kill(ctx context.Context, tenant store.Tenant, aud revoke.Audience, k revoke.Kill) (revoke.Receipt, error) {
	if r.down.Load() {
		return revoke.Receipt{}, errors.New("the broker is closed")
	}
	return r.next.Kill(ctx, tenant, aud, k)
}

func (r *switchableRevoker) Invalidate(ctx context.Context, tenant store.Tenant, aud revoke.Audience, inv revoke.Invalidation) (revoke.Receipt, error) {
	if r.down.Load() {
		return revoke.Receipt{}, errors.New("the broker is closed")
	}
	return r.next.Invalidate(ctx, tenant, aud, inv)
}

func TestAnUndeliveredEndIsAnsweredLikeAnUndeliveredRevocation(t *testing.T) {
	bus, err := revoke.New(revoke.Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	rv := &switchableRevoker{next: bus}
	f := newPushServerWith(t, rv)
	token, principal := f.tokenFor(t, identity.RoleIntegration)
	f.bind(t, principal)
	if resp := f.push(t, pushPath, token, window("CHG-42/1", "db01.example.com")); resp.StatusCode != http.StatusCreated {
		t.Fatalf("open = %d", resp.StatusCode)
	}

	rv.down.Store(true)
	resp := f.push(t, pushPath, token, ended("CHG-42/1"))
	e := decodeError(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable || e.Code != north.CodeGrantRevocationUndelivered ||
		e.Parameters["revoked"] != true || e.Parameters["grant_id"] == "" {
		t.Fatalf("an end with the stream down = %d %+v, want 503 grant_revocation_undelivered", resp.StatusCode, e)
	}

	rv.down.Store(false)
	if a := decodeEnd(t, f.push(t, pushPath, token, ended("CHG-42/1"))); a.Outcome != "already_ended" || a.Grant != e.Parameters["grant_id"] {
		t.Errorf("the end pushed again = %+v, want already_ended on grant %v", a, e.Parameters["grant_id"])
	}
}

// ---------------------------------------------------------------------------
// bindings and providers over the wire (0019)
// ---------------------------------------------------------------------------

func (f *pushFixture) send(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, f.http.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

const bindingPath = "/api/v1/tenants/tenant-a/access-context/providers/itsm/binding"

// A binding written over this surface is validated and audited exactly as
// accessctx.Service.PutBinding is, and the integration role can neither read
// nor write one.
func TestABindingIsManagedOverTheWire(t *testing.T) {
	f := newPushServer(t)
	admin, _ := f.tokenFor(t, identity.RoleAdmin)
	auditor, _ := f.tokenFor(t, identity.RoleAuditor)
	integration, principal := f.tokenFor(t, identity.RoleIntegration)

	body := map[string]any{
		"mode": "push", "scope": "change-window", "subjects": []string{"alice@example.com"},
		"targets": []string{"*.example.com"}, "max_window": "1h", "push_principals": []string{principal},
	}
	resp := f.send(t, "PUT", bindingPath, admin, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put binding = %d %+v", resp.StatusCode, decodeError(t, resp))
	}
	var got struct {
		Provider       string   `json:"provider"`
		PushPrincipals []string `json:"push_principals"`
		MaxWindow      string   `json:"max_window"`
		Enabled        bool     `json:"enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Provider != "itsm" || len(got.PushPrincipals) != 1 || got.PushPrincipals[0] != principal || !got.Enabled {
		t.Errorf("binding = %+v", got)
	}
	rows, err := f.st.Audit().Query(t.Context(), "tenant-a", store.AuditQuery{Event: accessctx.EventBindingPut})
	if err != nil || len(rows) != 1 {
		t.Fatalf("binding_put records: %d, %v", len(rows), err)
	}

	// Validated field by field: a window with no target selector is refused.
	bad := map[string]any{"mode": "push", "scope": "change-window", "subjects": []string{"alice@example.com"}, "max_window": "1h"}
	resp = f.send(t, "PUT", bindingPath, admin, bad)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an unbounded binding = %d", resp.StatusCode)
	}
	if e := decodeError(t, resp); e.Code != north.CodeInvalidRequest || e.Parameters["field"] == nil {
		t.Errorf("refusal = %+v", e)
	}

	// Every role that reads the tenant reads it; the integration reads nothing.
	if resp := f.send(t, "GET", "/api/v1/tenants/tenant-a/access-context/bindings", auditor, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("an auditor listing bindings = %d", resp.StatusCode)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/tenants/tenant-a/access-context/bindings"},
		{"GET", bindingPath}, {"PUT", bindingPath}, {"DELETE", bindingPath},
	} {
		if resp := f.send(t, c.method, c.path, integration, body); resp.StatusCode != http.StatusForbidden {
			t.Errorf("the integration role %s %s = %d, want 403", c.method, c.path, resp.StatusCode)
		}
	}
	if resp := f.send(t, "PUT", bindingPath, auditor, body); resp.StatusCode != http.StatusForbidden {
		t.Errorf("an auditor wrote a binding: %d", resp.StatusCode)
	}

	resp = f.send(t, "DELETE", bindingPath, admin, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if resp := f.send(t, "GET", bindingPath, admin, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a deleted binding reads %d", resp.StatusCode)
	} else if e := decodeError(t, resp); e.Code != north.CodeBindingNotFound {
		t.Errorf("code %q", e.Code)
	}
}

// The provider listing names every provider by its external system's name.
func TestTheProviderListingNamesEveryProvider(t *testing.T) {
	f := newPushServer(t)
	auditor, _ := f.tokenFor(t, identity.RoleAuditor)
	resp := f.send(t, "GET", "/api/v1/tenants/tenant-a/access-context/providers", auditor, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("providers = %d", resp.StatusCode)
	}
	var out struct {
		Providers []struct {
			Name         string `json:"name"`
			Pushes       bool   `json:"pushes"`
			Registration struct {
				Provider string `json:"provider"`
			} `json:"registration"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Providers) != 1 || out.Providers[0].Name != "itsm" || !out.Providers[0].Pushes ||
		out.Providers[0].Registration.Provider != "example.com/itsm" {
		t.Errorf("providers = %+v", out.Providers)
	}
}

// Explain renders the external story whole — the provider, the reference, the
// window and how it arrived — and an unanswered probe with which way it fell;
// simulation replays the windows that counted and never asks a provider.
func TestExplainAndSimulationOfExternalContext(t *testing.T) {
	f := newPushServer(t)
	author, _ := f.tokenFor(t, identity.RolePolicyAuthor)
	inputs := json.RawMessage(`{"now":"2026-10-01T12:00:00Z","subject":{"id":"alice@example.com","mfa":false,"known":true,"break_glass":false},` +
		`"target":{"hostname":"db01.example.com","known":true,"labels":{"env":"prod"}},"context":{"proxy_id":"proxy-1"},` +
		`"grants":[{"id":"g-ext","origin":"external","scope":{"name":"change-window","targets":["db01.example.com"],"labels":null,"zones":null},` +
		`"not_before":"2026-10-01T11:00:00Z","expires_at":"2026-10-01T13:00:00Z","reason":"CHG-42","reason_code":"",` +
		`"created_by":{"subject":"","principal":"tok-itsm","break_glass":false},"request_id":"","workflow_ref":"","approvers":null,` +
		`"external":{"system":"itsm","reference":"CHG-42","window_start":"2026-10-01T11:00:00Z","window_end":"2026-10-01T13:00:00Z","mode":"push","assertion_id":"CHG-42/1"}}],` +
		`"connection":{},"external_context":[{"provider":"itsm","scope":"change-window","outcome":"confirmed"}]}`)
	for _, d := range []store.Decision{
		{ID: "dec-window", SubjectID: "alice@example.com", Effect: "allow", SessionID: "sess-w", ProxyID: "proxy-1",
			InputsDigest: "x", Inputs: inputs, DecidedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
			// A served allow is always recorded with what it served.
			Snapshot: json.RawMessage(`{"route_type":"direct","target":"db01.example.com"}`),
			Explanation: json.RawMessage(`{"effect":"allow","rule":"change","grant":"g-ext","external":{"provider":"itsm",` +
				`"reference":"CHG-42","window_start":"2026-10-01T11:00:00Z","window_end":"2026-10-01T13:00:00Z","arrived":"pushed","confirmed":false}}`)},
		{ID: "dec-unanswered", SubjectID: "alice@example.com", Effect: "deny", SessionID: "sess-u", ProxyID: "proxy-1",
			InputsDigest: "x", Inputs: json.RawMessage(`{"now":"2026-10-01T12:00:00Z","subject":{"id":"alice@example.com"},"target":{"hostname":"db01.example.com"},"context":{"proxy_id":"proxy-1"},"connection":{},"external_context":[]}`),
			DecidedAt:   time.Date(2026, 10, 1, 12, 1, 0, 0, time.UTC),
			Explanation: json.RawMessage(`{"effect":"deny","unanswered":[{"provider":"scanner","scope":"scan","fell":"closed"}]}`)},
	} {
		if err := f.st.Decisions().Insert(t.Context(), "tenant-a", d); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	resp := f.send(t, "GET", "/api/v1/tenants/tenant-a/decisions/dec-window", author, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("explain = %d %+v", resp.StatusCode, decodeError(t, resp))
	}
	var story struct {
		Grant       map[string]any `json:"grant"`
		Explanation struct {
			External map[string]any `json:"external"`
		} `json:"explanation"`
		Inputs struct {
			ExternalContext []map[string]any `json:"external_context"`
		} `json:"inputs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&story); err != nil {
		t.Fatal(err)
	}
	ext := story.Explanation.External
	if ext["provider"] != "itsm" || ext["reference"] != "CHG-42" || ext["window_end"] == nil || ext["arrived"] != "pushed" {
		t.Errorf("explanation.external = %v", ext)
	}
	if story.Grant["origin"] != "external" || len(story.Inputs.ExternalContext) != 1 {
		t.Errorf("grant %v, external_context %v", story.Grant, story.Inputs.ExternalContext)
	}

	resp = f.send(t, "GET", "/api/v1/tenants/tenant-a/decisions/dec-unanswered", author, nil)
	var denied struct {
		Explanation struct {
			Unanswered []map[string]any `json:"unanswered"`
		} `json:"explanation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&denied); err != nil || len(denied.Explanation.Unanswered) != 1 ||
		denied.Explanation.Unanswered[0]["fell"] != "closed" {
		t.Errorf("an unanswered probe's denial = %+v, %v", denied, err)
	}

	before := probes.Load()
	source := "schema_version: 1\ntenant: tenant-a\nscopes:\n  change-window: {}\nrules:\n" +
		"  - id: change\n    effect: allow\n    match:\n      grant:\n        scopes: [change-window]\n" +
		"    route:\n      intent: hops-permitted\n      channels: [session]\n      filter:\n        mode: blacklist\n" +
		"      credentials:\n        - method: brokered-key\n          username: dba\n          credential_ref: vault://db\n"
	resp = f.send(t, "POST", "/api/v1/tenants/tenant-a/policy/simulate", author, map[string]any{
		"source": source,
		"replay": map[string]any{"from": "2026-10-01T00:00:00Z", "to": "2026-10-02T00:00:00Z"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("simulate = %d %+v", resp.StatusCode, decodeError(t, resp))
	}
	var sim struct {
		Replay struct {
			Replayed    int   `json:"replayed"`
			Unchanged   int   `json:"unchanged"`
			DenyToAllow []any `json:"deny_to_allow"`
		} `json:"replay"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sim); err != nil {
		t.Fatal(err)
	}
	if sim.Replay.Replayed != 2 || sim.Replay.Unchanged != 2 {
		t.Errorf("replay = %+v; the window that counted is replayed as recorded", sim.Replay)
	}
	if got := probes.Load(); got != before {
		t.Errorf("simulation called a provider %d time(s); a probe's answer is a recorded input", got-before)
	}
}
