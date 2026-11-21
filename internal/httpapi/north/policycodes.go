// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"net/http"
	"strings"

	"github.com/hoplock/control/internal/authoring"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/policy/model"
)

// POLICY'S REFUSALS AS CODES (0065; PLAN M19, M21). From version 8 every
// policy refusal an operator meets arrives as a code and typed parameters,
// never a sentence the server wrote: a compiler rejection whose parameter was
// prose is split into codes and parameters (internal/policy/model), an
// unreplayable record names its cause, a refused simulation is `{field,
// problem}`, and a candidate diff that does not parse carries its rejections.
//
// A client declaring 7 or older is served exactly what it was served before —
// the earlier codes, with their prose `detail`, the reason alone, `{field:
// body, problem: invalid, reason}`, and `{reason}` — so the change is
// additive (docs/API-VERSIONING.md) and north_api_min does not move. Prose
// may be reworded between versions, and this is: what `detail` and `reason`
// say is the new English. A code and the parameters a client reads are not.
const policyCodesSince = 8

// servedBeforePolicyCodes reports whether this answer goes to a client that
// declared a version before policyCodesSince.
func servedBeforePolicyCodes(w http.ResponseWriter) bool {
	v, ok := servedVersion(w)
	return ok && v.version < policyCodesSince
}

// rejectionsServed is a list of rejections as the version served reads it.
func rejectionsServed(w http.ResponseWriter, rs []authoring.Rejection) []authoring.Rejection {
	if !servedBeforePolicyCodes(w) || rs == nil {
		return rs
	}
	out := make([]authoring.Rejection, 0, len(rs))
	for _, r := range rs {
		out = append(out, rejectionBeforePolicyCodes(r))
	}
	return out
}

// rejectionBeforePolicyCodes is one rejection as version 7 said it: each code
// added in 8 under the code that carried it then, and each parameter that
// replaced prose given back as `detail`.
func rejectionBeforePolicyCodes(r authoring.Rejection) authoring.Rejection {
	p := r.Parameters
	as := func(code model.Code, detail string, keep ...string) authoring.Rejection {
		params := map[string]string{"detail": detail}
		for _, k := range keep {
			params[k] = p[k]
		}
		return authoring.Rejection{Code: string(code), Rule: r.Rule, Line: r.Line, Parameters: params, Message: r.Message}
	}
	field := "`" + p["field"] + "`"
	//exhaustive:ignore every other code carried no prose, so it reads the same in every version.
	switch code := model.Code(r.Code); code {
	case model.CodeDocumentMalformed:
		return as(code, model.ParameterWords(code, "problem", p["problem"]))
	case model.CodeKeyUnknown, model.CodeKeyDuplicate, model.CodeValueWrongType, model.CodeValueUnknown,
		model.CodeDurationMalformed, model.CodeMatchInvalidWindow:
		// Each was a document that did not decode, and said so as prose.
		return as(model.CodeDocumentMalformed, model.Rejection{Code: code, Params: p}.Message())
	case model.CodeRouteFilterRuleInvalid:
		return as(code, "it names no "+field, "index")
	case model.CodeRouteRestrictedCmdInvalid:
		return as(code, "it names no "+field, "index", "executable")
	case model.CodeRouteRestrictedCmdField:
		return as(model.CodeRouteRestrictedCmdInvalid, "form `"+p["form"]+"` does not take "+field, "index", "executable")
	case model.CodeRouteRestrictedArgInvalid:
		return as(model.CodeRouteRestrictedCmdInvalid, "argument "+p["argument"]+": it names no "+field, "index", "executable")
	case model.CodeRouteRestrictedArgField:
		return as(model.CodeRouteRestrictedCmdInvalid,
			"argument "+p["argument"]+": kind `"+p["kind"]+"` does not take "+field, "index", "executable")
	case model.CodeObligationContradicts:
		return as(code, "the route sets "+field+": false, so the rule both requires and waives the same thing", "obligation")
	case model.CodeObligationApprovers:
		return as(model.CodeObligationContradicts,
			"approver groups belong to `require-approval` and mean nothing on this obligation", "obligation")
	default:
		return r
	}
}

// reportServed is a validation report as the version served reads it.
func reportServed(w http.ResponseWriter, rep authoring.Report) authoring.Report {
	rep.Rejections = rejectionsServed(w, rep.Rejections)
	return rep
}

// simulationServed is a simulation's answer as the version served reads it:
// before 8, an unreplayable record carries its reason alone.
func simulationServed(w http.ResponseWriter, res authoring.SimulateResult) authoring.SimulateResult {
	if !servedBeforePolicyCodes(w) || res.Replay == nil || len(res.Replay.Unreplayable) == 0 {
		return res
	}
	rep := *res.Replay
	rep.Unreplayable = make([]decision.Unreplayable, 0, len(res.Replay.Unreplayable))
	for _, u := range res.Replay.Unreplayable {
		rep.Unreplayable = append(rep.Unreplayable, decision.Unreplayable{DecisionID: u.DecisionID, Reason: u.Reason})
	}
	res.Replay = &rep
	return res
}

// refuseSimulation answers a simulation this server will not run.
func refuseSimulation(w http.ResponseWriter, r *http.Request, sim *authoring.ErrSimulation) {
	params := map[string]any{"field": sim.Field, "problem": sim.Problem}
	if servedBeforePolicyCodes(w) {
		params = map[string]any{"field": "body", "problem": "invalid", "reason": sim.Reason}
	}
	writeError(w, http.StatusBadRequest, Error{
		Code: CodeInvalidRequest, CorrelationID: CorrelationIDFrom(r.Context()), Parameters: params,
		Message: "this simulation cannot be run: " + sim.Reason,
	})
}

// refuseUnparsedDiff answers a candidate diff whose source does not parse:
// its rejections, as an upload of it would be refused, so a client sets each
// at its line. Before 8 it was the parser's text alone.
func refuseUnparsedDiff(w http.ResponseWriter, r *http.Request, invalid *authoring.InvalidError) {
	params := map[string]any{"rejections": rejectionsServed(w, invalid.Report.Rejections), "digest": invalid.Report.Digest}
	if servedBeforePolicyCodes(w) {
		messages := make([]string, 0, len(invalid.Report.Rejections))
		for _, rj := range invalid.Report.Rejections {
			messages = append(messages, rj.Message)
		}
		params = map[string]any{"reason": "authoring: the new bundle does not parse: " + strings.Join(messages, "; ")}
	}
	writeError(w, http.StatusUnprocessableEntity, Error{
		Code: CodePolicyInvalid, CorrelationID: CorrelationIDFrom(r.Context()), Parameters: params,
		Message: "the candidate does not parse, so there is nothing to diff; each rejection names the line and what to do instead",
	})
}
