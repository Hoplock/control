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
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/authoring"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/store"
)

// The policy lifecycle and "explain why" (0019).
//
// UPLOAD → VALIDATE → DIFF → ACTIVATE, over internal/authoring. A bundle is
// immutable once stored; activation names a version, and rolling back is
// activating an older one. Validation is a question and answers 200 with the
// report, valid or not; the acts that need a valid bundle refuse an invalid one
// with `policy_invalid`, the compiler's rejections verbatim in the parameters.
//
// GITOPS is `POST …/policy/apply` with an API token: upload and activate in one
// call, answering machine-readably enough to gate a pull request — the report,
// the version, and on a warning the finding ids to accept on the next run.

// sourceBody carries a bundle's source. YAML inside JSON, so every route on
// this surface reads one content type and strict decoding still applies.
type sourceBody struct {
	Source string `json:"source"`
}

type applyBody struct {
	Source string `json:"source"`
	// Accept names the warnings the author accepts, by id.
	Accept []string `json:"accept"`
}

type activateBody struct {
	Accept []string `json:"accept"`
}

func (s *Server) actorOf(w http.ResponseWriter, r *http.Request) (audit.OperatorActor, bool) {
	a, ok := s.actorFrom(w, r)
	if !ok {
		return audit.OperatorActor{}, false
	}
	return audit.OperatorActor{
		Principal: a.Principal, Subject: a.Subject, BreakGlass: a.BreakGlass, CorrelationID: a.CorrelationID,
	}, true
}

// readSource decodes a source body, refusing an empty source by name.
func (s *Server) readSource(w http.ResponseWriter, r *http.Request, into any, source *string) bool {
	if err := decodeJSON(r, into); err != nil {
		s.refuseBody(w, r, err)
		return false
	}
	if strings.TrimSpace(*source) == "" {
		s.invalid(w, r, "source", access.ProblemRequired, "")
		return false
	}
	return true
}

func (s *Server) handlePolicyValidate(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	var body sourceBody
	if !s.readSource(w, r, &body, &body.Source) {
		return
	}
	p, _ := PrincipalFrom(r.Context())
	author := ""
	if p != nil {
		author = p.Subject
	}
	rep, _, err := s.authoring.Validate(r.Context(), tenant, []byte(body.Source), author)
	if err != nil {
		s.fail(w, r, "validate policy", err)
		return
	}
	answer(w, http.StatusOK, rep)
}

func (s *Server) handlePolicyList(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	versions, err := s.authoring.Versions(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "list policy versions", err)
		return
	}
	if versions == nil {
		versions = []authoring.Version{}
	}
	answer(w, http.StatusOK, map[string]any{"versions": versions})
}

func (s *Server) handlePolicyUpload(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	var body sourceBody
	if !s.readSource(w, r, &body, &body.Source) {
		return
	}
	res, err := s.authoring.Upload(r.Context(), tenant, []byte(body.Source), actor)
	if err != nil {
		s.policyFailure(w, r, "upload policy", err)
		return
	}
	status := http.StatusCreated
	if res.Existing {
		status = http.StatusOK
	}
	answer(w, status, res)
}

// versionParam reads a `{version}` path value.
func (s *Server) versionParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	v, err := strconv.ParseInt(r.PathValue("version"), 10, 64)
	if err != nil || v <= 0 {
		s.invalid(w, r, "version", access.ProblemInvalid, "")
		return 0, false
	}
	return v, true
}

type bundleView struct {
	authoring.Version
	Source string `json:"source"`
}

func (s *Server) handlePolicyGet(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	version, ok := s.versionParam(w, r)
	if !ok {
		return
	}
	v, source, err := s.authoring.Bundle(r.Context(), tenant, version)
	if err != nil {
		s.policyFailure(w, r, "read policy version", err)
		return
	}
	answer(w, http.StatusOK, bundleView{Version: v, Source: string(source)})
}

func (s *Server) handlePolicyActive(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	v, source, err := s.authoring.Active(r.Context(), tenant)
	if err != nil {
		s.policyFailure(w, r, "read active policy", err)
		return
	}
	answer(w, http.StatusOK, bundleView{Version: v, Source: string(source)})
}

// handlePolicyDiff diffs a version against `?against=` — another version, or
// 0 for nothing — defaulting to the active version, which is what a reviewer
// asks: what would activating this change.
func (s *Server) handlePolicyDiff(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	version, ok := s.versionParam(w, r)
	if !ok {
		return
	}
	var against int64
	if raw := strings.TrimSpace(r.URL.Query().Get("against")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			s.invalid(w, r, "against", access.ProblemInvalid, "")
			return
		}
		against = n
	} else {
		active, _, err := s.authoring.Active(r.Context(), tenant)
		switch {
		case err == nil:
			against = active.Version
		case !store.IsNotFound(err):
			s.fail(w, r, "read active policy", err)
			return
		}
	}
	d, err := s.authoring.DiffVersions(r.Context(), tenant, against, version)
	if err != nil {
		s.policyFailure(w, r, "diff policy", err)
		return
	}
	answer(w, http.StatusOK, d)
}

type diffBody struct {
	Source string `json:"source"`
	// Against is a stored version, 0 for nothing; absent takes the active one.
	Against *int64 `json:"against"`
}

// handlePolicyDiffSource diffs a candidate source — one not stored yet — against
// a stored version, by default the active one: what a reviewer asks of a pull
// request before anything is uploaded.
func (s *Server) handlePolicyDiffSource(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	var body diffBody
	if !s.readSource(w, r, &body, &body.Source) {
		return
	}
	var (
		against int64
		old     []byte
	)
	switch {
	case body.Against != nil && *body.Against < 0:
		s.invalid(w, r, "against", access.ProblemInvalid, "")
		return
	case body.Against != nil && *body.Against > 0:
		v, src, err := s.authoring.Bundle(r.Context(), tenant, *body.Against)
		if err != nil {
			s.policyFailure(w, r, "read policy version", err)
			return
		}
		against, old = v.Version, src
	case body.Against == nil:
		v, src, err := s.authoring.Active(r.Context(), tenant)
		switch {
		case err == nil:
			against, old = v.Version, src
		case !store.IsNotFound(err):
			s.fail(w, r, "read active policy", err)
			return
		}
	}
	d, err := authoring.DiffSources(old, []byte(body.Source))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, Error{
			Code: CodePolicyInvalid, CorrelationID: CorrelationIDFrom(r.Context()),
			Parameters: map[string]any{"reason": err.Error()},
			Message:    "the candidate does not parse, so there is nothing to diff; validate it for the rejections",
		})
		return
	}
	d.From = against
	answer(w, http.StatusOK, d)
}

func (s *Server) handlePolicyActivate(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	version, ok := s.versionParam(w, r)
	if !ok {
		return
	}
	var body activateBody
	if err := decodeJSON(r, &body); err != nil {
		s.refuseBody(w, r, err)
		return
	}
	res, err := s.authoring.Activate(r.Context(), tenant, version, body.Accept, actor)
	if err != nil {
		s.policyFailure(w, r, "activate policy", err)
		return
	}
	answer(w, http.StatusOK, res)
}

func (s *Server) handlePolicyApply(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	var body applyBody
	if !s.readSource(w, r, &body, &body.Source) {
		return
	}
	res, err := s.authoring.Apply(r.Context(), tenant, []byte(body.Source), body.Accept, actor)
	if err != nil {
		// The upload may have stored a version before the activation was
		// refused; the refusal names it, so the next run accepts by id.
		s.policyFailureFor(w, r, "apply policy", err, res.Upload.Version.Version)
		return
	}
	answer(w, http.StatusOK, res)
}

// simulateBody names a candidate — a source, a stored version, or neither for
// the active one — and what to ask it.
type simulateBody struct {
	Source  string                       `json:"source"`
	Version int64                        `json:"version"`
	Cases   []authoring.DryRunInput      `json:"cases"`
	Replay  *authoring.ReplayWindow      `json:"replay"`
	Labels  map[string]map[string]string `json:"labels"`
}

func (s *Server) handlePolicySimulate(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	var body simulateBody
	if err := decodeJSON(r, &body); err != nil {
		s.refuseBody(w, r, err)
		return
	}
	res, err := s.authoring.Simulate(r.Context(), tenant, authoring.SimulateRequest{
		Source: []byte(body.Source), Version: body.Version, Cases: body.Cases, Replay: body.Replay,
		Labels: decision.LabelOverride(body.Labels),
	})
	if err != nil {
		s.policyFailure(w, r, "simulate policy", err)
		return
	}
	answer(w, http.StatusOK, res)
}

func (s *Server) policyFailure(w http.ResponseWriter, r *http.Request, op string, err error) {
	s.policyFailureFor(w, r, op, err, 0)
}

// policyFailureFor classifies an authoring error: a bundle the compiler
// refused, warnings to accept, a validator's block, a request to fix, an
// absence, or an outage — never a 401 (M11).
func (s *Server) policyFailureFor(w http.ResponseWriter, r *http.Request, op string, err error, version int64) {
	id := CorrelationIDFrom(r.Context())
	var (
		invalidErr *authoring.InvalidError
		notAcc     *authoring.NotAcceptedError
		blocked    *authoring.BlockedError
		simErr     *authoring.ErrSimulation
	)
	params := map[string]any{}
	if version != 0 {
		params["version"] = version
	}
	switch {
	case errors.As(err, &invalidErr):
		params["rejections"] = invalidErr.Report.Rejections
		params["digest"] = invalidErr.Report.Digest
		writeError(w, http.StatusUnprocessableEntity, Error{
			Code: CodePolicyInvalid, CorrelationID: id, Parameters: params,
			Message: "the bundle does not compile; each rejection names the rule, the line and what to do instead",
		})
	case errors.As(err, &notAcc):
		params["findings"] = notAcc.Findings
		writeError(w, http.StatusConflict, Error{
			Code: CodeFindingsNotAccepted, CorrelationID: id, Parameters: params,
			Message: "this activation carries warnings the author has not accepted; accept each by its id to " +
				"publish knowingly, or change the bundle",
		})
	case errors.As(err, &blocked):
		params["findings"] = blocked.Findings
		writeError(w, http.StatusUnprocessableEntity, Error{
			Code: CodePolicyBlocked, CorrelationID: id, Parameters: params,
			Message: "a registered policy validator blocks this activation",
		})
	case errors.As(err, &simErr):
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeInvalidRequest, CorrelationID: id,
			Parameters: map[string]any{"field": "body", "problem": "invalid", "reason": simErr.Reason},
			Message:    "this simulation cannot be run: " + simErr.Reason,
		})
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: id,
			Message: "there is no such policy version in this tenant, or no active one",
		})
	default:
		s.fail(w, r, op, err)
	}
}

// ---------------------------------------------------------------------------
// explain (M4)
// ---------------------------------------------------------------------------

func (s *Server) handleExplainDecision(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	story, err := decision.Explain(r.Context(), s.st, s.mapping, tenant, r.PathValue("decision"))
	if err != nil {
		s.explainFailure(w, r, err)
		return
	}
	answer(w, http.StatusOK, story)
}

func (s *Server) handleExplainSession(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	stories, err := decision.ExplainSession(r.Context(), s.st, s.mapping, tenant, r.PathValue("session"))
	if err != nil {
		s.explainFailure(w, r, err)
		return
	}
	if len(stories) == 0 {
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "this server took no decision for that session in this tenant",
		})
		return
	}
	answer(w, http.StatusOK, map[string]any{"session_id": r.PathValue("session"), "decisions": stories})
}

// explainFailure answers an unexplainable record LOUDLY: logged at error and
// answered as an outage with its own code, because the pair "vague to the user,
// total to the auditor" only works if this endpoint is genuinely total.
func (s *Server) explainFailure(w http.ResponseWriter, r *http.Request, err error) {
	id := CorrelationIDFrom(r.Context())
	switch {
	case errors.Is(err, decision.ErrUnexplainable):
		s.log.ErrorContext(r.Context(), "a stored decision cannot be explained",
			"event", "decision_unexplainable", "path", r.URL.Path, "correlation_id", id, "error", err.Error())
		writeError(w, http.StatusInternalServerError, Error{
			Code: CodeDecisionUnexplainable, CorrelationID: id,
			Message: "this decision's record does not carry its own explanation; that is an integrity fault in " +
				"this server's records, logged under the correlation id",
		})
	case errors.Is(err, decision.ErrNoSession):
		s.invalid(w, r, "session", access.ProblemRequired, "")
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: id, Message: "this server took no such decision in this tenant",
		})
	default:
		s.fail(w, r, "explain", err)
	}
}

// timeParam reads an RFC 3339 query parameter; absent is zero.
func (s *Server) timeParam(w http.ResponseWriter, r *http.Request, name string) (time.Time, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		s.invalid(w, r, name, access.ProblemInvalid, "")
		return time.Time{}, false
	}
	return t, true
}

// limitParam reads `?limit=`; absent is zero, which takes the store's default.
func (s *Server) limitParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		s.invalid(w, r, "limit", access.ProblemInvalid, "")
		return 0, false
	}
	return n, true
}
