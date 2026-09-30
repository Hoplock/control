// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/store"
)

// Just-in-time grants (PLAN M10, 0012).
//
// Creating and revoking a grant are ADMINISTRATIVE ACTS: `grant:write`, checked
// by the route table like every other permission, and audited with the actor
// inside the same transaction as the act (`internal/access`). Reading one is
// `grant:read`, which every role holds, because an auditor who cannot see who
// held access when is not an auditor.
//
// When a Hoplock Enterprise approval workflow is registered, a create is a
// REQUEST the workflow decides, and these routes say so in their answer: 201
// with the grant when it exists, 202 with the request while it is pending, 403
// `grant_request_denied` when the workflow said no. There is no route around a
// registered workflow — an administrator's grant goes through it.

// grantRequestBody is a create.
type grantRequestBody struct {
	Subject     string         `json:"subject"`
	Scope       grantScopeView `json:"scope"`
	NotBefore   time.Time      `json:"not_before"`
	ExpiresAt   time.Time      `json:"expires_at"`
	Duration    string         `json:"duration"`
	Reason      string         `json:"reason"`
	ReasonCode  string         `json:"reason_code"`
	ExternalRef string         `json:"external_ref"`
}

type grantScopeView struct {
	// Name is the scope a policy rule matches with `grant.scopes`.
	Name string `json:"name"`
	// Targets, Labels and Zones select which targets it covers, ANDed.
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
	Zones   []string          `json:"zones"`
}

type actorView struct {
	Subject     string `json:"subject,omitempty"`
	PrincipalID string `json:"principal_id"`
	BreakGlass  bool   `json:"break_glass"`
}

type externalView struct {
	System      string     `json:"system,omitempty"`
	Reference   string     `json:"reference,omitempty"`
	WindowStart *time.Time `json:"window_start,omitempty"`
	WindowEnd   *time.Time `json:"window_end,omitempty"`
}

// grantView is a grant as the API renders it.
type grantView struct {
	ID      string         `json:"id"`
	Tenant  string         `json:"tenant"`
	Subject string         `json:"subject"`
	Scope   grantScopeView `json:"scope"`
	// NotBefore and ExpiresAt are the window; State is judged at this
	// server's current instant, and three of its four values are nothing
	// more than that judgement.
	NotBefore time.Time `json:"not_before"`
	ExpiresAt time.Time `json:"expires_at"`
	State     string    `json:"state"`
	// Origin is in the policy's vocabulary — `administrator`, `workflow`,
	// `external` — the word a rule's `grant.origins` uses.
	Origin       string        `json:"origin"`
	Reason       string        `json:"reason"`
	ReasonCode   string        `json:"reason_code,omitempty"`
	ExternalRef  string        `json:"external_ref,omitempty"`
	CreatedBy    actorView     `json:"created_by"`
	CreatedAt    time.Time     `json:"created_at"`
	RequestID    string        `json:"request_id,omitempty"`
	WorkflowRef  string        `json:"workflow_ref,omitempty"`
	Approvers    []string      `json:"approvers,omitempty"`
	External     *externalView `json:"external,omitempty"`
	RevokedAt    *time.Time    `json:"revoked_at,omitempty"`
	RevokedBy    *actorView    `json:"revoked_by,omitempty"`
	RevokeReason string        `json:"revoke_reason,omitempty"`
}

type approvalView struct {
	Approver string    `json:"approver"`
	Approved bool      `json:"approved"`
	At       time.Time `json:"at"`
	Reason   string    `json:"reason,omitempty"`
}

// requestView is a workflow request as the API renders it.
type requestView struct {
	ID          string         `json:"id"`
	Tenant      string         `json:"tenant"`
	Subject     string         `json:"subject"`
	Scope       grantScopeView `json:"scope"`
	NotBefore   time.Time      `json:"not_before"`
	ExpiresAt   time.Time      `json:"expires_at"`
	Reason      string         `json:"reason"`
	ReasonCode  string         `json:"reason_code,omitempty"`
	ExternalRef string         `json:"external_ref,omitempty"`
	RequestedBy actorView      `json:"requested_by"`
	RequestedAt time.Time      `json:"requested_at"`
	Workflow    string         `json:"workflow"`
	WorkflowRef string         `json:"workflow_ref,omitempty"`
	// Submitted is false while the workflow has not confirmed the request:
	// it could not be reached, and the request will be put to it again.
	Submitted     bool           `json:"submitted"`
	State         string         `json:"state"`
	OutcomeCode   string         `json:"outcome_code,omitempty"`
	OutcomeText   string         `json:"outcome_text,omitempty"`
	Approvals     []approvalView `json:"approvals,omitempty"`
	WindowClamped bool           `json:"window_clamped,omitempty"`
	GrantID       string         `json:"grant_id,omitempty"`
	DecidedAt     *time.Time     `json:"decided_at,omitempty"`
}

// grantOutcome is the answer to a create, a request read, or a cancel.
type grantOutcome struct {
	Grant   *grantView   `json:"grant,omitempty"`
	Request *requestView `json:"request,omitempty"`
}

// actorFrom builds the actor an act is attributed to. It is read from the
// principal the middleware authenticated and from nothing the caller sent: a
// grant's creator is who presented the credential, not who the body says.
func (s *Server) actorFrom(w http.ResponseWriter, r *http.Request) (access.Actor, bool) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		s.fail(w, r, "resolve actor", errNoPrincipal)
		return access.Actor{}, false
	}
	return access.Actor{
		Principal:     p.ID,
		Subject:       p.Subject,
		DisplayName:   p.DisplayName,
		BreakGlass:    p.BreakGlass,
		CorrelationID: CorrelationIDFrom(r.Context()),
	}, true
}

func (s *Server) handleGrantCreate(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorFrom(w, r)
	if !ok {
		return
	}
	var body grantRequestBody
	if err := decodeJSON(r, &body); err != nil {
		s.invalid(w, r, "body", access.ProblemInvalid, "")
		return
	}
	spec := access.Spec{
		Subject: body.Subject,
		Scope: access.Scope{
			Name: body.Scope.Name, Targets: body.Scope.Targets,
			Labels: body.Scope.Labels, Zones: body.Scope.Zones,
		},
		NotBefore:   body.NotBefore,
		ExpiresAt:   body.ExpiresAt,
		Reason:      body.Reason,
		ReasonCode:  body.ReasonCode,
		ExternalRef: body.ExternalRef,
	}
	if strings.TrimSpace(body.Duration) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(body.Duration))
		if err != nil {
			s.invalid(w, r, "duration", access.ProblemInvalid, "")
			return
		}
		spec.Duration = d
	}

	out, err := s.grants.Create(r.Context(), tenant, actor, spec)
	if err != nil {
		s.grantFailure(w, r, "create grant", err)
		return
	}
	s.answerOutcome(w, r, tenant, out)
}

// answerOutcome renders what a create or a request read produced, with the
// status that says which: a grant exists, a request is pending, or a workflow
// refused.
func (s *Server) answerOutcome(w http.ResponseWriter, r *http.Request, tenant store.Tenant, out access.Created) {
	body := s.outcomeView(tenant, out)
	switch {
	case out.Grant != nil:
		answer(w, http.StatusCreated, body)
	case out.Request == nil:
		s.fail(w, r, "render grant outcome", errors.New("httpapi/north: a grant outcome with neither a grant nor a request"))
	case out.Request.State == store.GrantRequestPending:
		answer(w, http.StatusAccepted, body)
	case out.Request.State == store.GrantRequestDenied, out.Request.State == store.GrantRequestExpired:
		writeError(w, http.StatusForbidden, Error{
			Code: CodeGrantRequestDenied, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "the approval workflow did not grant this request",
			Parameters: map[string]any{
				"request_id":   out.Request.ID,
				"state":        string(out.Request.State),
				"outcome_code": out.Request.OutcomeCode,
			},
		})
	case out.Request.State == store.GrantRequestFailed:
		writeError(w, http.StatusBadGateway, Error{
			Code: CodeGrantWorkflowFailed, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "the approval workflow could not decide this request, and asking again would not change that",
			Parameters: map[string]any{
				"request_id":   out.Request.ID,
				"outcome_code": out.Request.OutcomeCode,
			},
		})
	default:
		answer(w, http.StatusOK, body)
	}
}

func (s *Server) handleGrantList(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	q := store.GrantQuery{SubjectID: strings.TrimSpace(r.URL.Query().Get("subject"))}
	if state := strings.TrimSpace(r.URL.Query().Get("state")); state != "" {
		switch st := store.GrantState(state); st {
		case store.GrantScheduled, store.GrantActive, store.GrantExpired, store.GrantRevoked:
			q.State = st
		default:
			s.invalid(w, r, "state", access.ProblemInvalid, "")
			return
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			s.invalid(w, r, "limit", access.ProblemInvalid, "")
			return
		}
		q.Limit = n
	}
	rows, err := s.grants.List(r.Context(), tenant, q)
	if err != nil {
		s.grantFailure(w, r, "list grants", err)
		return
	}
	now := s.grants.Now()
	out := make([]grantView, 0, len(rows))
	for _, g := range rows {
		out = append(out, viewGrant(tenant, g, now))
	}
	answer(w, http.StatusOK, map[string]any{"tenant": tenant.String(), "grants": out})
}

type grantDecisionView struct {
	DecisionID string    `json:"decision_id"`
	SessionID  string    `json:"session_id,omitempty"`
	ProxyID    string    `json:"proxy_id,omitempty"`
	Effect     string    `json:"effect"`
	Rule       string    `json:"rule,omitempty"`
	DecidedAt  time.Time `json:"decided_at"`
}

func (s *Server) handleGrantGet(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	g, decisions, err := s.grants.Inspect(r.Context(), tenant, r.PathValue("grant"))
	if err != nil {
		s.grantFailure(w, r, "inspect grant", err)
		return
	}
	// What the grant let anybody do: the decisions it supplied, newest
	// first. Each is resolvable into the whole story by its id (M4).
	used := make([]grantDecisionView, 0, len(decisions))
	for _, d := range decisions {
		used = append(used, grantDecisionView{
			DecisionID: d.ID, SessionID: d.SessionID, ProxyID: d.ProxyID,
			Effect: d.Effect, Rule: d.MatchedRule, DecidedAt: d.DecidedAt.UTC(),
		})
	}
	answer(w, http.StatusOK, map[string]any{
		"grant":     viewGrant(tenant, g, s.grants.Now()),
		"decisions": used,
	})
}

type revokeBody struct {
	// Reason is SHOWN TO THE HOLDER, verbatim, as their sessions end — the
	// contract's disclosure rule. It is recorded too, so the auditor reads
	// what the user read.
	Reason string `json:"reason"`
}

type grantSessionView struct {
	ProxyID   string `json:"proxy_id"`
	SessionID string `json:"session_id"`
}

type revocationView struct {
	Grant grantView `json:"grant"`
	// Revoked is false when the grant had already been revoked: the first
	// revocation stands, and this call sent its session_kill again.
	Revoked  bool               `json:"revoked"`
	Sessions []grantSessionView `json:"sessions"`
	// Widened reports that every session of the holder was ended, because
	// the grant backed more than this server ends by name.
	Widened bool     `json:"widened"`
	Events  []string `json:"events"`
}

func (s *Server) handleGrantRevoke(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorFrom(w, r)
	if !ok {
		return
	}
	var body revokeBody
	if err := decodeJSON(r, &body); err != nil {
		s.invalid(w, r, "body", access.ProblemInvalid, "")
		return
	}
	rev, err := s.grants.Revoke(r.Context(), tenant, actor, r.PathValue("grant"), body.Reason)
	if access.IsUndelivered(err) {
		writeError(w, http.StatusServiceUnavailable, Error{
			Code: CodeGrantRevocationUndelivered, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "the grant is revoked and no new session can use it, but ending its sessions could not be " +
				"published; revoke it again to re-send",
			Parameters: map[string]any{"grant_id": rev.Grant.ID, "revoked": true},
		})
		return
	}
	if err != nil {
		s.grantFailure(w, r, "revoke grant", err)
		return
	}
	sessions := make([]grantSessionView, 0, len(rev.Sessions))
	for _, gs := range rev.Sessions {
		sessions = append(sessions, grantSessionView{ProxyID: gs.ProxyID, SessionID: gs.SessionID})
	}
	answer(w, http.StatusOK, revocationView{
		Grant:    viewGrant(tenant, rev.Grant, s.grants.Now()),
		Revoked:  rev.Revoked,
		Sessions: sessions,
		Widened:  rev.Widened,
		Events:   nonNilStrings(rev.EventIDs),
	})
}

func (s *Server) handleGrantRequestGet(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	out, err := s.grants.Request(r.Context(), tenant, r.PathValue("request"))
	if err != nil {
		s.grantFailure(w, r, "read grant request", err)
		return
	}
	// A read answers 200 whatever the request's state: reading a denied
	// request is not itself refused.
	answer(w, http.StatusOK, s.outcomeView(tenant, out))
}

type cancelBody struct {
	Reason string `json:"reason"`
}

func (s *Server) handleGrantRequestCancel(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorFrom(w, r)
	if !ok {
		return
	}
	var body cancelBody
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &body); err != nil {
			s.invalid(w, r, "body", access.ProblemInvalid, "")
			return
		}
	}
	out, err := s.grants.Cancel(r.Context(), tenant, actor, r.PathValue("request"), body.Reason)
	if errors.Is(err, access.ErrNotPending) {
		state := ""
		if out.Request != nil {
			state = string(out.Request.State)
		}
		writeError(w, http.StatusConflict, Error{
			Code: CodeGrantRequestNotPending, CorrelationID: CorrelationIDFrom(r.Context()),
			Message:    "the request has already been decided, so there is nothing to cancel",
			Parameters: map[string]any{"request_id": r.PathValue("request"), "state": state},
		})
		return
	}
	if err != nil {
		s.grantFailure(w, r, "cancel grant request", err)
		return
	}
	answer(w, http.StatusOK, s.outcomeView(tenant, out))
}

// grantFailure classifies an error from the grant service: a refusal the
// caller can fix, an absence, or an outage — and never a 401 (M11).
func (s *Server) grantFailure(w http.ResponseWriter, r *http.Request, op string, err error) {
	var ve *access.ValidationError
	switch {
	case errors.As(err, &ve):
		s.invalid(w, r, ve.Field, ve.Problem, ve.Limit)
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "there is no such grant or request in this tenant",
		})
	default:
		s.fail(w, r, op, err)
	}
}

// invalid renders a request this server will not act on, naming the field and
// the problem as codes so a console builds the sentence (M21).
func (s *Server) invalid(w http.ResponseWriter, r *http.Request, field, problem, limit string) {
	params := map[string]any{"field": field, "problem": problem}
	if limit != "" {
		params["limit"] = limit
	}
	writeError(w, http.StatusBadRequest, Error{
		Code: CodeInvalidRequest, CorrelationID: CorrelationIDFrom(r.Context()),
		Message:    "this request cannot be acted on: " + field + " is " + strings.ReplaceAll(problem, "_", " "),
		Parameters: params,
	})
}

func (s *Server) outcomeView(tenant store.Tenant, out access.Created) grantOutcome {
	var body grantOutcome
	if out.Grant != nil {
		v := viewGrant(tenant, *out.Grant, s.grants.Now())
		body.Grant = &v
	}
	if out.Request != nil {
		v := viewRequest(tenant, *out.Request, s.grants.WorkflowProvider())
		body.Request = &v
	}
	return body
}

func viewGrant(tenant store.Tenant, g store.Grant, now time.Time) grantView {
	v := grantView{
		ID:      g.ID,
		Tenant:  tenant.String(),
		Subject: g.SubjectID,
		Scope: grantScopeView{
			Name: g.Scope, Targets: nonNilStrings(g.ScopeTargets),
			Labels: nonNilLabels(g.ScopeLabels), Zones: nonNilStrings(g.ScopeZones),
		},
		NotBefore:   g.NotBefore.UTC(),
		ExpiresAt:   g.ExpiresAt.UTC(),
		State:       string(g.State(now)),
		Origin:      string(access.PolicyOrigin(g.Origin)),
		Reason:      g.Reason,
		ReasonCode:  g.ReasonCode,
		ExternalRef: g.ExternalRef,
		CreatedBy:   viewActor(g.CreatedBy),
		CreatedAt:   g.CreatedAt.UTC(),
		RequestID:   g.RequestID,
		WorkflowRef: g.ApprovalRef,
		Approvers:   g.Approvers,
	}
	if e := g.External; e.System != "" || !e.WindowStart.IsZero() || !e.WindowEnd.IsZero() {
		v.External = &externalView{
			System: e.System, Reference: g.ExternalRef,
			WindowStart: timePtr(e.WindowStart), WindowEnd: timePtr(e.WindowEnd),
		}
	}
	if !g.RevokedAt.IsZero() {
		v.RevokedAt = timePtr(g.RevokedAt)
		by := viewActor(g.RevokedBy)
		v.RevokedBy = &by
		v.RevokeReason = g.RevokeReason
	}
	return v
}

func viewRequest(tenant store.Tenant, req store.GrantRequest, workflow string) requestView {
	v := requestView{
		ID:      req.ID,
		Tenant:  tenant.String(),
		Subject: req.SubjectID,
		Scope: grantScopeView{
			Name: req.Scope, Targets: nonNilStrings(req.ScopeTargets),
			Labels: nonNilLabels(req.ScopeLabels), Zones: nonNilStrings(req.ScopeZones),
		},
		NotBefore:     req.NotBefore.UTC(),
		ExpiresAt:     req.ExpiresAt.UTC(),
		Reason:        req.Reason,
		ReasonCode:    req.ReasonCode,
		ExternalRef:   req.ExternalRef,
		RequestedBy:   viewActor(req.RequestedBy),
		RequestedAt:   req.RequestedAt.UTC(),
		Workflow:      req.WorkflowProvider,
		WorkflowRef:   req.WorkflowRef,
		Submitted:     req.WorkflowRef != "" || req.State != store.GrantRequestPending,
		State:         string(req.State),
		OutcomeCode:   req.OutcomeCode,
		OutcomeText:   req.OutcomeText,
		WindowClamped: req.WindowClamped,
		GrantID:       req.GrantID,
		DecidedAt:     timePtr(req.DecidedAt),
	}
	if v.Workflow == "" {
		v.Workflow = workflow
	}
	for _, a := range req.Approvals {
		v.Approvals = append(v.Approvals, approvalView{
			Approver: a.Approver, Approved: a.Approved, At: a.At.UTC(), Reason: a.Reason,
		})
	}
	return v
}

func viewActor(a store.GrantActor) actorView {
	return actorView{Subject: a.Subject, PrincipalID: a.Principal, BreakGlass: a.BreakGlass}
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilLabels(v map[string]string) map[string]string {
	if v == nil {
		return map[string]string{}
	}
	return v
}
