// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// Just-in-time grants over the wire (0012): the route table's permissions, the
// statuses that say which path a create took, and the actor that is recorded.

const grantsPath = "/api/v1/tenants/tenant-a/grants"

// grantAdmin mints a credential holding grant-admin in tenant-a, and returns
// the principal id the act will be attributed to.
func (f *serverFixture) tokenFor(t *testing.T, roles ...identity.Role) (string, string) {
	t.Helper()
	cred, principal, err := f.federation.IssueToken(context.Background(), "tenant-a", identity.TokenRequest{
		DisplayName: "grant desk", Scopes: map[store.Tenant]identity.RoleSet{"tenant-a": roles},
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return cred.String(), principal.ID
}

func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

var aGrant = map[string]any{
	"subject":      "alice@example.com",
	"scope":        map[string]any{"name": "prod-dba", "labels": map[string]string{"env": "prod"}},
	"duration":     "30m",
	"reason":       "INC-9 needs a DBA on the primary",
	"external_ref": "INC-9",
}

type grantBody struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	State     string `json:"state"`
	Origin    string `json:"origin"`
	Reason    string `json:"reason"`
	CreatedBy struct {
		PrincipalID string `json:"principal_id"`
	} `json:"created_by"`
	Scope struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"scope"`
	RevokeReason string `json:"revoke_reason"`
}

type outcomeBody struct {
	Grant   *grantBody `json:"grant"`
	Request *struct {
		ID          string `json:"id"`
		State       string `json:"state"`
		Workflow    string `json:"workflow"`
		WorkflowRef string `json:"workflow_ref"`
		Submitted   bool   `json:"submitted"`
	} `json:"request"`
}

// RBAC: creating and revoking a grant is grant:write. A role without it is
// refused by the route table before any handler runs — the auditor who can
// read every grant, and the policy author who writes the rules grants feed.
func TestARoleWithoutGrantCreationPermissionIsRefused(t *testing.T) {
	f := newServer(t)
	for _, role := range []identity.Role{identity.RoleAuditor, identity.RolePolicyAuthor, identity.RoleFleetAdmin} {
		t.Run(string(role), func(t *testing.T) {
			token, _ := f.tokenFor(t, role)
			resp := f.do(t, "POST", grantsPath, token, jsonBody(t, aGrant))
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("%s created a grant: status %d", role, resp.StatusCode)
			}
			e := decodeError(t, resp)
			if e.Code != north.CodeForbidden || e.Parameters["permission"] != string(identity.PermGrantWrite) {
				t.Errorf("refusal %+v, want forbidden naming grant:write", e)
			}
			resp = f.do(t, "POST", grantsPath+"/g_any/revoke", token, jsonBody(t, map[string]string{"reason": "x"}))
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s reached revoke: status %d", role, resp.StatusCode)
			}
			// Reading is every role's.
			if resp := f.do(t, "GET", grantsPath, token, nil); resp.StatusCode != http.StatusOK {
				t.Errorf("%s could not list grants: %d", role, resp.StatusCode)
			}
		})
	}
	if rows, _ := f.st.Grants().List(t.Context(), "tenant-a", store.GrantQuery{}); len(rows) != 0 {
		t.Fatalf("a refused caller created %d grants", len(rows))
	}
}

func TestTheGrantLifecycleOverTheWire(t *testing.T) {
	f := newServer(t)
	token, principal := f.tokenFor(t, identity.RoleGrantAdmin)

	resp := f.do(t, "POST", grantsPath, token, jsonBody(t, aGrant))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d: %+v", resp.StatusCode, decodeError(t, resp))
	}
	created := decode[outcomeBody](t, resp)
	g := created.Grant
	if g == nil || created.Request != nil {
		t.Fatalf("create answered %+v, want a grant and no request", created)
	}
	if g.State != "active" || g.Origin != "administrator" || g.Subject != "alice@example.com" ||
		g.Scope.Name != "prod-dba" || g.Scope.Labels["env"] != "prod" {
		t.Errorf("created grant = %+v", g)
	}
	// The creator is the credential that called, never anything the body
	// said: the body has no field for it at all.
	if g.CreatedBy.PrincipalID != principal {
		t.Errorf("created_by %q, want the caller's credential %q", g.CreatedBy.PrincipalID, principal)
	}

	// Audited with the actor.
	recs, err := f.st.Audit().Query(t.Context(), "tenant-a", store.AuditQuery{Event: access.EventGrantCreated})
	if err != nil || len(recs) != 1 || recs[0].Attributes[audit.AttrPrincipalID] != principal ||
		recs[0].Attributes[audit.AttrGrantID] != g.ID {
		t.Fatalf("creation records = %+v, %v", recs, err)
	}
	if recs[0].Attributes[audit.AttrCorrelationID] == "" {
		t.Error("the creation record does not tie back to the request log")
	}

	list := decode[struct {
		Grants []grantBody `json:"grants"`
	}](t, f.do(t, "GET", grantsPath+"?subject=alice@example.com&state=active", token, nil))
	if len(list.Grants) != 1 || list.Grants[0].ID != g.ID {
		t.Errorf("active grants = %+v", list.Grants)
	}

	inspect := decode[struct {
		Grant     grantBody `json:"grant"`
		Decisions []any     `json:"decisions"`
	}](t, f.do(t, "GET", grantsPath+"/"+g.ID, token, nil))
	if inspect.Grant.ID != g.ID || inspect.Decisions == nil {
		t.Errorf("inspect = %+v", inspect)
	}

	const reason = "Access withdrawn by the on-call lead; contact #dba."
	resp = f.do(t, "POST", grantsPath+"/"+g.ID+"/revoke", token, jsonBody(t, map[string]string{"reason": reason}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke: status %d: %+v", resp.StatusCode, decodeError(t, resp))
	}
	rev := decode[struct {
		Grant   grantBody `json:"grant"`
		Revoked bool      `json:"revoked"`
		Events  []string  `json:"events"`
	}](t, resp)
	if !rev.Revoked || rev.Grant.State != "revoked" || rev.Grant.RevokeReason != reason || len(rev.Events) == 0 {
		t.Errorf("revocation = %+v", rev)
	}

	resp = f.do(t, "POST", grantsPath+"/"+g.ID+"/revoke", token, jsonBody(t, map[string]string{"reason": "again"}))
	if again := decode[struct {
		Revoked bool      `json:"revoked"`
		Grant   grantBody `json:"grant"`
	}](t, resp); again.Revoked || again.Grant.RevokeReason != reason {
		t.Errorf("a second revoke = %+v, want the first revocation standing", again)
	}

	if resp := f.do(t, "GET", grantsPath+"/g_nope", token, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an absent grant answered %d", resp.StatusCode)
	}
}

// A grant this server will not create is refused naming the field and the
// problem as codes, so a console builds the sentence (M21).
func TestAGrantThatCannotBeActedOnIsRefusedByField(t *testing.T) {
	f := newServer(t)
	token, _ := f.tokenFor(t, identity.RoleGrantAdmin)

	with := func(edit func(map[string]any)) map[string]any {
		b := map[string]any{}
		for k, v := range aGrant {
			b[k] = v
		}
		edit(b)
		return b
	}
	cases := map[string]struct {
		body    any
		field   string
		problem string
	}{
		"no reason":        {with(func(b map[string]any) { delete(b, "reason") }), "reason", access.ProblemRequired},
		"a vague duration": {with(func(b map[string]any) { b["duration"] = "soon" }), "duration", access.ProblemInvalid},
		"a long window":    {with(func(b map[string]any) { b["duration"] = "400h" }), "expires_at", access.ProblemExceedsMaximum},
		"a middle wildcard": {with(func(b map[string]any) {
			b["scope"] = map[string]any{"name": "prod-dba", "targets": []string{"db.*.example.com"}}
		}), "scope.targets", access.ProblemInvalid},
		// A body naming its own creator is refused outright: there is no
		// such field, because the creator is whoever presented the
		// credential.
		"a claimed creator": {with(func(b map[string]any) { b["created_by"] = "somebody-else" }), "body", access.ProblemInvalid},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			resp := f.do(t, "POST", grantsPath, token, jsonBody(t, c.body))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", resp.StatusCode)
			}
			e := decodeError(t, resp)
			if e.Code != north.CodeInvalidRequest || e.Parameters["field"] != c.field || e.Parameters["problem"] != c.problem {
				t.Errorf("refusal %+v, want %s/%s", e, c.field, c.problem)
			}
		})
	}

	if resp := f.do(t, "GET", grantsPath+"?state=stale", token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an unknown state filter answered %d", resp.StatusCode)
	}
	resp := f.do(t, "POST", grantsPath+"/g_any/revoke", token, jsonBody(t, map[string]string{}))
	if e := decodeError(t, resp); resp.StatusCode != http.StatusBadRequest || e.Parameters["field"] != "reason" {
		t.Errorf("a revoke with no reason to show the holder answered %d %+v", resp.StatusCode, e)
	}
}

// With an approval workflow registered, the statuses say which path a create
// took: 202 while the workflow decides, 403 when it refuses — and the request
// can be read and withdrawn.
func TestAWorkflowRequestOverTheWire(t *testing.T) {
	wf := &wireWorkflow{}
	wf.set(ext.GrantPending)
	f := newServerWithGrants(t, access.Options{Workflow: wf, WorkflowProvider: "example/workflow"})
	token, _ := f.tokenFor(t, identity.RoleGrantAdmin)

	resp := f.do(t, "POST", grantsPath, token, jsonBody(t, aGrant))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("create under a pending workflow: status %d", resp.StatusCode)
	}
	pending := decode[outcomeBody](t, resp)
	if pending.Grant != nil || pending.Request == nil || pending.Request.State != "pending" ||
		pending.Request.Workflow != "example/workflow" || !pending.Request.Submitted {
		t.Fatalf("pending create answered %+v", pending)
	}
	reqPath := "/api/v1/tenants/tenant-a/grant-requests/" + pending.Request.ID

	if read := decode[outcomeBody](t, f.do(t, "GET", reqPath, token, nil)); read.Request == nil ||
		read.Request.State != "pending" {
		t.Errorf("reading the request = %+v", read)
	}

	resp = f.do(t, "POST", reqPath+"/cancel", token, jsonBody(t, map[string]string{"reason": "no longer needed"}))
	if cancelled := decode[outcomeBody](t, resp); resp.StatusCode != http.StatusOK || cancelled.Request.State != "cancelled" {
		t.Fatalf("cancel: %d %+v", resp.StatusCode, cancelled)
	}
	resp = f.do(t, "POST", reqPath+"/cancel", token, nil)
	if e := decodeError(t, resp); resp.StatusCode != http.StatusConflict || e.Code != north.CodeGrantRequestNotPending {
		t.Errorf("cancelling a decided request: %d %+v", resp.StatusCode, e)
	}

	// A refusal is a decision, and it is answered as one.
	wf.set(ext.GrantDenied)
	resp = f.do(t, "POST", grantsPath, token, jsonBody(t, aGrant))
	if e := decodeError(t, resp); resp.StatusCode != http.StatusForbidden || e.Code != north.CodeGrantRequestDenied ||
		e.Parameters["request_id"] == "" {
		t.Errorf("a denied create: %d %+v", resp.StatusCode, e)
	}
	if rows, _ := f.st.Grants().List(t.Context(), "tenant-a", store.GrantQuery{}); len(rows) != 0 {
		t.Errorf("the workflow path created %d grants it never approved", len(rows))
	}

	// Approved at once: the grant, and the request that produced it.
	wf.set(ext.GrantApproved)
	resp = f.do(t, "POST", grantsPath, token, jsonBody(t, aGrant))
	approved := decode[outcomeBody](t, resp)
	if resp.StatusCode != http.StatusCreated || approved.Grant == nil || approved.Grant.Origin != "workflow" ||
		approved.Request == nil || approved.Request.State != "approved" {
		t.Errorf("an approved create: %d %+v", resp.StatusCode, approved)
	}
}

// wireWorkflow answers every submission with one outcome, which the test
// changes between requests the server handles on its own goroutines.
type wireWorkflow struct{ outcome atomic.Int32 }

func (w *wireWorkflow) set(o ext.GrantOutcome) { w.outcome.Store(int32(o)) }

func (w *wireWorkflow) Submit(_ context.Context, req ext.GrantRequest) (ext.GrantDecision, error) {
	return ext.GrantDecision{WorkflowRef: "wf-" + req.RequestID, Outcome: ext.GrantOutcome(w.outcome.Load())}, nil
}

func (w *wireWorkflow) Status(_ context.Context, _ ext.Tenant, ref string) (ext.GrantDecision, error) {
	return ext.GrantDecision{WorkflowRef: ref, Outcome: ext.GrantPending}, nil
}

func (w *wireWorkflow) Cancel(context.Context, ext.Tenant, string, string) error { return nil }
