// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// A requester's own door over the wire (0016): the AccessSelf family, whose
// holder is resolved from the credential and never named by the caller. The
// table-driven tests enumerate the ROUTER, as the isolation test does, so a self
// route added later is covered on the day it is added.

const ownPath = "/api/v1/tenants/tenant-a/me/grant-requests"

// anOwnRequest is aGrant without a holder: the holder is whoever asks.
var anOwnRequest = map[string]any{
	"scope":        map[string]any{"name": "prod-dba", "labels": map[string]string{"env": "prod"}},
	"duration":     "30m",
	"reason":       "INC-9 needs a DBA on the primary",
	"external_ref": "INC-9",
}

var sessionSeq atomic.Int64

// session mints a session credential in tenant-a for a person, the way a login
// does: a subject behind it, the source that asserted it, and the break-glass
// flag asserted at minting. It returns the credential and the principal id.
func (f *serverFixture) session(t *testing.T, subject, source string, breakGlass bool, roles ...identity.Role) (string, string) {
	t.Helper()
	cred, err := identity.MintCredential(store.PrincipalSession, "tenant-a")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	codes := make([]string, 0, len(roles))
	for _, r := range roles {
		codes = append(codes, string(r))
	}
	row := store.NorthPrincipal{
		ID:            fmt.Sprintf("sess-test-%d", sessionSeq.Add(1)),
		Kind:          store.PrincipalSession,
		SubjectID:     subject,
		DisplayName:   subject,
		Source:        source,
		BreakGlass:    breakGlass,
		SessionDigest: cred.Digest(),
		Scopes:        map[string][]string{"tenant-a": codes},
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := f.st.NorthPrincipals().Put(context.Background(), "tenant-a", row); err != nil {
		t.Fatalf("session: %v", err)
	}
	return cred.String(), row.ID
}

// requester is a federated person whose only role is requester.
func (f *serverFixture) requester(t *testing.T, subject string) (string, string) {
	t.Helper()
	return f.session(t, subject, "oidc-corp", false, identity.RoleRequester)
}

type ownRequestBody struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	State       string `json:"state"`
	WorkflowRef string `json:"workflow_ref"`
	GrantID     string `json:"grant_id"`
	RequestedBy struct {
		Subject     string `json:"subject"`
		PrincipalID string `json:"principal_id"`
		BreakGlass  bool   `json:"break_glass"`
	} `json:"requested_by"`
}

type ownOutcome struct {
	Grant *struct {
		ID        string `json:"id"`
		Subject   string `json:"subject"`
		Origin    string `json:"origin"`
		CreatedBy struct {
			Subject     string `json:"subject"`
			PrincipalID string `json:"principal_id"`
		} `json:"created_by"`
	} `json:"grant"`
	Request *ownRequestBody `json:"request"`
}

// THE DOOR. A person whose only role is requester asks for themselves and is
// answered 202; the workflow approves; reading the request again applies it.
// The grant is theirs, made by them, decided by the workflow — and the
// workflow saw one person on both sides of the request.
func TestARequesterAsksForThemselvesOverTheWire(t *testing.T) {
	wf := &recordingWorkflow{}
	f := newServerWithGrants(t, access.Options{Workflow: wf, WorkflowProvider: "example/workflow"})
	token, principal := f.requester(t, "alice@example.com")

	resp := f.do(t, "POST", ownPath, token, jsonBody(t, anOwnRequest))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("self create: status %d: %+v", resp.StatusCode, decodeError(t, resp))
	}
	created := decode[ownOutcome](t, resp)
	if created.Grant != nil || created.Request == nil || created.Request.State != "pending" ||
		created.Request.Subject != "alice@example.com" || created.Request.RequestedBy.Subject != "alice@example.com" ||
		created.Request.RequestedBy.PrincipalID != principal {
		t.Fatalf("self create answered %+v", created.Request)
	}
	sub := wf.last()
	if sub.Subject.ID != "alice@example.com" || sub.RequestedBy.ID != "alice@example.com" {
		t.Errorf("the workflow saw subject %q requested by %q; from the self door they are one person",
			sub.Subject.ID, sub.RequestedBy.ID)
	}

	wf.approve(created.Request.WorkflowRef)
	resp = f.do(t, "GET", ownPath+"/"+created.Request.ID, token, nil)
	read := decode[ownOutcome](t, resp)
	if resp.StatusCode != http.StatusOK || read.Grant == nil || read.Request.State != "approved" {
		t.Fatalf("reading the approved request: %d %+v", resp.StatusCode, read)
	}
	if g := read.Grant; g.Subject != "alice@example.com" || g.CreatedBy.Subject != "alice@example.com" ||
		g.CreatedBy.PrincipalID != principal || g.Origin != "workflow" {
		t.Errorf("grant = %+v, want alice's, created by alice, origin workflow", g)
	}

	// The administrator's door, for contrast: the workflow sees two parties.
	admin, adminPrincipal := f.tokenFor(t, identity.RoleGrantAdmin)
	if resp := f.do(t, "POST", grantsPath, admin, jsonBody(t, aGrant)); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("administrator's create: %d", resp.StatusCode)
	}
	if sub := wf.last(); sub.Subject.ID != "alice@example.com" || sub.RequestedBy.ID != adminPrincipal {
		t.Errorf("administrator's door: subject %q requested by %q, want the administrator's credential",
			sub.Subject.ID, sub.RequestedBy.ID)
	}
}

// The requester lists and reads their own, withdraws one that is pending — the
// workflow told, the act audited with them as the actor — and cannot withdraw
// one already decided.
func TestARequesterListsReadsAndWithdrawsTheirOwn(t *testing.T) {
	wf := &recordingWorkflow{}
	f := newServerWithGrants(t, access.Options{Workflow: wf, WorkflowProvider: "example/workflow"})
	token, principal := f.requester(t, "alice@example.com")

	first := decode[ownOutcome](t, f.do(t, "POST", ownPath, token, jsonBody(t, anOwnRequest))).Request
	second := decode[ownOutcome](t, f.do(t, "POST", ownPath, token, jsonBody(t, anOwnRequest))).Request

	list := decode[struct {
		Tenant   string           `json:"tenant"`
		Requests []ownRequestBody `json:"requests"`
	}](t, f.do(t, "GET", ownPath, token, nil))
	if list.Tenant != "tenant-a" || len(list.Requests) != 2 || list.Requests[0].ID != second.ID {
		t.Fatalf("own list = %+v, want both, newest first", list)
	}
	if pending := decode[struct {
		Requests []ownRequestBody `json:"requests"`
	}](t, f.do(t, "GET", ownPath+"?state=cancelled", token, nil)); len(pending.Requests) != 0 {
		t.Errorf("cancelled filter before any cancel = %+v", pending.Requests)
	}
	if resp := f.do(t, "GET", ownPath+"?state=stale", token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an unknown state filter answered %d", resp.StatusCode)
	}

	resp := f.do(t, "POST", ownPath+"/"+first.ID+"/cancel", token, jsonBody(t, map[string]string{"reason": "sorted"}))
	if cancelled := decode[ownOutcome](t, resp); resp.StatusCode != http.StatusOK || cancelled.Request.State != "cancelled" {
		t.Fatalf("cancel own: %d %+v", resp.StatusCode, cancelled.Request)
	}
	if !slices.Contains(wf.cancelled(), first.WorkflowRef) {
		t.Error("the workflow was not told the requester withdrew")
	}
	recs, err := f.st.Audit().Query(t.Context(), "tenant-a", store.AuditQuery{Event: access.EventGrantRequestClosed})
	if err != nil || len(recs) != 1 || recs[0].Attributes[audit.AttrPrincipalID] != principal ||
		recs[0].Attributes[audit.AttrActorSubject] != "alice@example.com" {
		t.Errorf("the withdrawal's record = %+v, %v; want the requester as the actor", recs, err)
	}

	resp = f.do(t, "POST", ownPath+"/"+first.ID+"/cancel", token, nil)
	if e := decodeError(t, resp); resp.StatusCode != http.StatusConflict || e.Code != north.CodeGrantRequestNotPending {
		t.Errorf("cancelling a decided request: %d %+v", resp.StatusCode, e)
	}
}

// With nothing registered to decide it, the door is shut: 403 with a code of
// its own, and nothing stored — no request, no grant, no record. The
// administrator's direct create is exactly what it was.
func TestWithNoWorkflowTheSelfCreateIsRefusedAndNothingIsStored(t *testing.T) {
	f := newServer(t)
	token, _ := f.requester(t, "alice@example.com")
	before, err := f.st.Audit().Query(t.Context(), "tenant-a", store.AuditQuery{Limit: 1000})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	resp := f.do(t, "POST", ownPath, token, jsonBody(t, anOwnRequest))
	e := decodeError(t, resp)
	if resp.StatusCode != http.StatusForbidden || e.Code != north.CodeGrantWorkflowNotRegistered || e.Message == "" {
		t.Fatalf("self create with no workflow: %d %+v, want 403 %s", resp.StatusCode, e, north.CodeGrantWorkflowNotRegistered)
	}
	if rows, err := f.st.GrantRequests().ListForSubject(t.Context(), "tenant-a", "alice@example.com",
		store.GrantRequestQuery{}); err != nil || len(rows) != 0 {
		t.Errorf("request rows after the refusal: %+v, %v", rows, err)
	}
	if rows, _ := f.st.Grants().List(t.Context(), "tenant-a", store.GrantQuery{}); len(rows) != 0 {
		t.Errorf("grants after the refusal: %+v", rows)
	}
	if after, _ := f.st.Audit().Query(t.Context(), "tenant-a", store.AuditQuery{Limit: 1000}); len(after) != len(before) {
		t.Errorf("the refusal wrote %d audit records", len(after)-len(before))
	}

	admin, _ := f.tokenFor(t, identity.RoleGrantAdmin)
	resp = f.do(t, "POST", grantsPath, admin, jsonBody(t, aGrant))
	if created := decode[outcomeBody](t, resp); resp.StatusCode != http.StatusCreated || created.Grant == nil ||
		created.Grant.Origin != "administrator" {
		t.Errorf("the administrator's direct create changed: %d %+v", resp.StatusCode, created)
	}
}

// The holder is never in the body, and a workflow's "no" creates nothing.
func TestASelfCreateNamingAHolderIsRefusedAndADenialCreatesNothing(t *testing.T) {
	wf := &recordingWorkflow{}
	f := newServerWithGrants(t, access.Options{Workflow: wf, WorkflowProvider: "example/workflow"})
	token, _ := f.requester(t, "alice@example.com")

	naming := map[string]any{"subject": "bob@example.com"}
	for k, v := range anOwnRequest {
		naming[k] = v
	}
	resp := f.do(t, "POST", ownPath, token, jsonBody(t, naming))
	if e := decodeError(t, resp); resp.StatusCode != http.StatusBadRequest || e.Code != north.CodeInvalidRequest ||
		e.Parameters["field"] != "body" {
		t.Errorf("a body naming a holder: %d %+v, want 400 invalid_request", resp.StatusCode, e)
	}

	wf.deny(true)
	resp = f.do(t, "POST", ownPath, token, jsonBody(t, anOwnRequest))
	if e := decodeError(t, resp); resp.StatusCode != http.StatusForbidden || e.Code != north.CodeGrantRequestDenied {
		t.Errorf("a denied self request: %d %+v", resp.StatusCode, e)
	}
	if rows, _ := f.st.Grants().List(t.Context(), "tenant-a", store.GrantQuery{}); len(rows) != 0 {
		t.Errorf("a denied self request created %d grants", len(rows))
	}
}

// SUBJECT ISOLATION, over every AccessSelf route: with two requesters in one
// tenant, B's request is not found by A on every route that names a request,
// and never appears in A's list.
func TestEverySelfRouteKeepsOneRequestersRequestsFromAnother(t *testing.T) {
	wf := &recordingWorkflow{}
	f := newServerWithGrants(t, access.Options{Workflow: wf, WorkflowProvider: "example/workflow"})
	tokenA, _ := f.requester(t, "alice@example.com")
	tokenB, _ := f.requester(t, "bob@example.com")
	bs := decode[ownOutcome](t, f.do(t, "POST", ownPath, tokenB, jsonBody(t, anOwnRequest))).Request
	if bs == nil {
		t.Fatal("B raised no request")
	}

	self := 0
	for _, route := range f.server.Routes() {
		if route.Access != "self" {
			continue
		}
		self++
		t.Run(route.Method+" "+route.Pattern, func(t *testing.T) {
			path := strings.ReplaceAll(samplePath(route.Pattern, "tenant-a"), "sample", bs.ID)
			switch {
			case strings.Contains(route.Pattern, "{request}"):
				resp := f.do(t, route.Method, path, tokenA, jsonBody(t, map[string]string{}))
				if e := decodeError(t, resp); resp.StatusCode != http.StatusNotFound || e.Code != north.CodeNotFound {
					t.Fatalf("A reached B's request: %d %+v, want 404 not_found", resp.StatusCode, e)
				}
			case route.Method == "GET":
				list := decode[struct {
					Requests []ownRequestBody `json:"requests"`
				}](t, f.do(t, route.Method, path, tokenA, nil))
				for _, r := range list.Requests {
					if r.ID == bs.ID || r.Subject != "alice@example.com" {
						t.Errorf("A's list holds %s for %s", r.ID, r.Subject)
					}
				}
			case route.Method == "POST":
				// The create names no request; what it makes is A's.
				out := decode[ownOutcome](t, f.do(t, route.Method, path, tokenA, jsonBody(t, anOwnRequest)))
				if out.Request == nil || out.Request.Subject != "alice@example.com" {
					t.Errorf("A's create made %+v", out.Request)
				}
			default:
				t.Fatalf("no isolation assertion covers %s; add one", route)
			}
		})
	}
	if self == 0 {
		t.Fatal("no self routes are registered, so this test asserted nothing")
	}
	if got, _ := f.st.GrantRequests().Get(t.Context(), "tenant-a", bs.ID); got.State != store.GrantRequestPending {
		t.Errorf("B's request is %s after A's attempts", got.State)
	}
}

// A credential with nobody behind it has no own records, on every self route —
// even holding the role.
func TestEverySelfRouteRefusesAMachineToken(t *testing.T) {
	wf := &recordingWorkflow{}
	f := newServerWithGrants(t, access.Options{Workflow: wf, WorkflowProvider: "example/workflow"})
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleRequester}})

	for _, route := range f.server.Routes() {
		if route.Access != "self" {
			continue
		}
		t.Run(route.Method+" "+route.Pattern, func(t *testing.T) {
			resp := f.do(t, route.Method, samplePath(route.Pattern, "tenant-a"), token, jsonBody(t, anOwnRequest))
			if e := decodeError(t, resp); resp.StatusCode != http.StatusForbidden || e.Code != north.CodeSubjectRequired {
				t.Fatalf("a machine token answered %d %+v, want 403 %s", resp.StatusCode, e, north.CodeSubjectRequired)
			}
		})
	}
	if len(wf.submitted()) != 0 {
		t.Error("a machine token's self request reached the workflow")
	}
}

// A requester reaches nothing tenant-wide, over every AccessTenant route — the
// administrator's create and cancel included.
func TestARequesterReachesNothingTenantWide(t *testing.T) {
	f := newServer(t)
	token, _ := f.requester(t, "alice@example.com")

	tenant := 0
	for _, route := range f.server.Routes() {
		if route.Access != "tenant" {
			continue
		}
		tenant++
		t.Run(route.Method+" "+route.Pattern, func(t *testing.T) {
			resp := f.do(t, route.Method, samplePath(route.Pattern, "tenant-a"), token, jsonBody(t, map[string]string{}))
			if e := decodeError(t, resp); resp.StatusCode != http.StatusForbidden || e.Code != north.CodeForbidden {
				t.Fatalf("a requester answered %d %+v, want 403 forbidden", resp.StatusCode, e)
			}
		})
	}
	if tenant == 0 {
		t.Fatal("no tenant routes are registered, so this test asserted nothing")
	}
}

// Break-glass reaches the workflow from the credential that raised the
// request, as it was minted — and is never inferred from a login source.
func TestBreakGlassReachesTheWorkflowFromTheCredential(t *testing.T) {
	cases := []struct {
		name       string
		source     string
		breakGlass bool
		self       bool
	}{
		{"break-glass, self door", "local", true, true},
		{"break-glass, administrator's create", "local", true, false},
		{"federated, self door", "oidc-corp", false, true},
		// "local" is a source, not a flag: never inferred from it.
		{"local without the flag, self door", "local", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wf := &recordingWorkflow{}
			f := newServerWithGrants(t, access.Options{Workflow: wf, WorkflowProvider: "example/workflow"})
			path, body, role := ownPath, anOwnRequest, identity.RoleRequester
			if !c.self {
				path, body, role = grantsPath, aGrant, identity.RoleGrantAdmin
			}
			token, _ := f.session(t, "root@example.com", c.source, c.breakGlass, role)
			if resp := f.do(t, "POST", path, token, jsonBody(t, body)); resp.StatusCode != http.StatusAccepted {
				t.Fatalf("create: %d %+v", resp.StatusCode, decodeError(t, resp))
			}
			if got := wf.last().BreakGlass; got != c.breakGlass {
				t.Errorf("the workflow saw BreakGlass = %v, want %v", got, c.breakGlass)
			}
		})
	}
}

// recordingWorkflow keeps every request pending until told otherwise, and
// records what it was asked, on the server's own goroutines.
type recordingWorkflow struct {
	mu       sync.Mutex
	submits  []ext.GrantRequest
	approved map[string]bool
	cancels  []string
	denying  bool
}

func (w *recordingWorkflow) Submit(_ context.Context, req ext.GrantRequest) (ext.GrantDecision, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.submits = append(w.submits, req)
	if w.denying {
		return ext.GrantDecision{WorkflowRef: "wf-" + req.RequestID, Outcome: ext.GrantDenied}, nil
	}
	return ext.GrantDecision{WorkflowRef: "wf-" + req.RequestID, Outcome: ext.GrantPending}, nil
}

func (w *recordingWorkflow) Status(_ context.Context, _ ext.Tenant, ref string) (ext.GrantDecision, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.approved[ref] {
		return ext.GrantDecision{
			WorkflowRef: ref, Outcome: ext.GrantApproved,
			Approvals: []ext.GrantApproval{{Approver: ext.Subject{ID: "carol@example.com"}, Approved: true}},
		}, nil
	}
	return ext.GrantDecision{WorkflowRef: ref, Outcome: ext.GrantPending}, nil
}

func (w *recordingWorkflow) Cancel(_ context.Context, _ ext.Tenant, ref, _ string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cancels = append(w.cancels, ref)
	return nil
}

func (w *recordingWorkflow) approve(ref string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.approved == nil {
		w.approved = map[string]bool{}
	}
	w.approved[ref] = true
}

func (w *recordingWorkflow) deny(on bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.denying = on
}

func (w *recordingWorkflow) submitted() []ext.GrantRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.submits)
}

func (w *recordingWorkflow) last() ext.GrantRequest {
	subs := w.submitted()
	if len(subs) == 0 {
		return ext.GrantRequest{}
	}
	return subs[len(subs)-1]
}

func (w *recordingWorkflow) cancelled() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.cancels)
}
