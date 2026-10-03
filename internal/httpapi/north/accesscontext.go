// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/accessctx"
)

// The push receiver for external access context (PLAN M16, 0013).
//
// What this file does is transport: it reads the body within its bound, hands
// it to internal/accessctx with the tenant the route resolved and the
// credential that called, and renders what came back. Every judgement — the
// binding, the credential it names, the rate, replay, skew, the scope, the
// ceiling — is accessctx's, so that the push receiver and anything else that
// ever admits a window are one implementation.
//
// A refusal is a decision about the push and is answered as one, with
// accessctx's code; anything else is an outage, CodeInternal, and the
// integration may retry (M11).

// pushView is what an admitted push is answered with: the grant the window
// became, and how it was bounded. Not the whole grant — the integration role
// reads nothing here, including the grants its pushes became, beyond the
// answer to its own act.
type pushView struct {
	Grant          string    `json:"grant"`
	State          string    `json:"state"`
	NotBefore      time.Time `json:"not_before"`
	ExpiresAt      time.Time `json:"expires_at"`
	Replayed       bool      `json:"replayed"`
	Clamped        bool      `json:"clamped"`
	CeilingSeconds int64     `json:"ceiling_seconds,omitempty"`
}

func (s *Server) handleAccessContextPush(w http.ResponseWriter, r *http.Request) {
	tenant, _ := TenantFrom(r.Context())
	actor, ok := s.actorFrom(w, r)
	if !ok {
		return
	}
	provider := r.PathValue("provider")
	if s.context == nil {
		s.refusePush(w, r, &accessctx.Refusal{Code: accessctx.CodeProviderNotFound})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, s.maxPushBytes+1))
	if err != nil {
		s.invalid(w, r, "body", access.ProblemInvalid, "")
		return
	}
	if int64(len(body)) > s.maxPushBytes {
		writeError(w, http.StatusRequestEntityTooLarge, Error{
			Code: CodePayloadTooLarge, CorrelationID: CorrelationIDFrom(r.Context()),
			Message:    "a push body is a window assertion, and this one is larger than the receiver accepts",
			Parameters: map[string]any{"limit": s.maxPushBytes},
		})
		return
	}

	res, err := s.context.Push(r.Context(), tenant, actor, provider, r.Header.Get("Content-Type"), body)
	var refusal *accessctx.Refusal
	var verr *access.ValidationError
	switch {
	case errors.As(err, &refusal):
		s.refusePush(w, r, refusal)
		return
	case errors.As(err, &verr):
		s.invalid(w, r, verr.Field, verr.Problem, verr.Limit)
		return
	case err != nil:
		s.fail(w, r, "access-context push", err)
		return
	}

	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	view := pushView{
		Grant:     res.Grant.ID,
		State:     string(res.Grant.State(s.now())),
		NotBefore: res.Grant.NotBefore.UTC(),
		ExpiresAt: res.Grant.ExpiresAt.UTC(),
		Replayed:  res.Replayed,
		Clamped:   res.Clamped,
	}
	if res.Ceiling > 0 {
		view.CeilingSeconds = int64(res.Ceiling / time.Second)
	}
	writeJSON(w, status, view)
}

// refusePush answers a refused push with its code. A refusal names the part of
// the push at fault, never the binding's contents.
func (s *Server) refusePush(w http.ResponseWriter, r *http.Request, ref *accessctx.Refusal) {
	status := http.StatusForbidden
	message := "the push was refused"
	switch ref.Code {
	case accessctx.CodeProviderNotFound:
		status, message = http.StatusNotFound, "no integration by this name runs on this server"
	case accessctx.CodeRateLimited:
		status, message = http.StatusTooManyRequests, "this integration is pushing faster than it may"
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(ref.RetryAfter/time.Second))))
	case accessctx.CodeAssertionConflict:
		status, message = http.StatusConflict, "this assertion id is already a different window"
	case accessctx.CodeAssertionMalformed, accessctx.CodeAssertionStale,
		accessctx.CodeAssertionFromFuture, accessctx.CodeWindowClosed:
		status, message = http.StatusBadRequest, "the window this push asserts cannot be acted on"
	case accessctx.CodeOutsideScope, accessctx.CodePushNotPermitted:
		message = "the push reaches outside what this integration may assert; it was recorded as an escalation attempt"
	}
	params := map[string]any{}
	if ref.Field != "" {
		params["field"] = ref.Field
	}
	if ref.Detail != "" {
		params["detail"] = ref.Detail
	}
	writeError(w, status, Error{
		Code: ref.Code, CorrelationID: CorrelationIDFrom(r.Context()),
		Message: message, Parameters: params,
	})
}

