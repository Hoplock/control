// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/revoke"
)

// Operator actions on the revocation stream (0009, 0019).
//
// PUBLISHED THROUGH revoke.Operator, NEVER BESIDE IT. It already validates what
// the contract requires — exactly one selector, a `session_kill` reason that is
// safe to disclose and never empty — and its receipt says whether the event can
// reach a host-key decision. That field reaches the operator on every answer: a
// subject-scoped `cache_invalidate` cannot match a host-key decision, because
// the proxy keys one on target, port and fingerprint, and an operator told
// "invalidated everything for Alice" without being told a target's host key was
// untouched has been misled by this server.
//
// This route supersedes the debug publish listener (0009), and its body is that
// listener's: `type` plus one selector. The emergency runbook's three steps are
// a publish that adds a ban (`…/policy/apply`), then `cache_invalidate` with
// `all` here, then `session_kill` here for the sessions `…/audit/negotiated`
// returned.

type revocationBody struct {
	// Type is `session_kill`, `cache_invalidate`, `resync` or
	// `host_key_withdraw`.
	Type string `json:"type"`
	// ProxyIDs narrows the audience; empty reaches every subscriber in the
	// tenant, including one that connects later and replays.
	ProxyIDs []string `json:"proxy_ids,omitempty"`

	SessionIDs []string `json:"session_ids,omitempty"`
	Subject    string   `json:"subject,omitempty"`
	Keys       []string `json:"keys,omitempty"`
	All        bool     `json:"all,omitempty"`
	// Reason is shown to the user on a session_kill, verbatim.
	Reason string `json:"reason,omitempty"`

	// HostKey names the recorded host-key decision to withdraw, by the key
	// this server stored on its record.
	HostKey *struct {
		Target      string `json:"target"`
		Port        int32  `json:"target_port,omitempty"`
		Fingerprint string `json:"fingerprint"`
	} `json:"host_key,omitempty"`
}

type revocationReceipt struct {
	Type                   string `json:"type"`
	EventID                string `json:"event_id"`
	Delivered              int    `json:"delivered"`
	CoversHostKeyDecisions bool   `json:"covers_host_key_decisions"`
}

func (s *Server) handleRevocation(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	var body revocationBody
	if err := decodeJSON(r, &body); err != nil {
		s.invalid(w, r, "body", access.ProblemInvalid, "")
		return
	}
	audience := revoke.Everyone()
	if len(body.ProxyIDs) > 0 {
		audience = revoke.ToProxies(body.ProxyIDs...)
	}

	var (
		rcpt revoke.Receipt
		err  error
	)
	ctx := r.Context()
	switch body.Type {
	case "session_kill":
		rcpt, err = s.revocations.Kill(ctx, tenant, audience, revoke.Kill{
			SessionIDs: body.SessionIDs, Subject: body.Subject, All: body.All, Reason: body.Reason,
		})
	case "cache_invalidate":
		rcpt, err = s.revocations.Invalidate(ctx, tenant, audience, revoke.Invalidation{
			Keys: body.Keys, Subject: body.Subject, All: body.All,
		})
	case "resync":
		rcpt, err = s.revocations.Resync(ctx, tenant, audience)
	case "host_key_withdraw":
		if body.HostKey == nil {
			s.invalid(w, r, "host_key", access.ProblemRequired, "")
			return
		}
		rcpt, err = s.revocations.WithdrawHostKey(ctx, tenant, audience, revoke.HostKeyRef{
			Target: body.HostKey.Target, Port: body.HostKey.Port, Fingerprint: body.HostKey.Fingerprint,
		})
	default:
		s.invalid(w, r, "type", access.ProblemInvalid, "")
		return
	}
	if err != nil {
		s.revocationFailure(w, r, err)
		return
	}

	fields := map[string]string{
		"type": body.Type, "event_id": rcpt.EventID, "delivered": strconv.Itoa(rcpt.Delivered),
		"covers_host_key_decisions": strconv.FormatBool(rcpt.CoversHostKeyDecisions),
		"audience":                  audienceString(body.ProxyIDs),
	}
	switch {
	case len(body.SessionIDs) > 0:
		fields["session_ids"] = strings.Join(body.SessionIDs, ",")
	case body.All:
		fields["selector"] = "all"
	case len(body.Keys) > 0:
		fields["keys"] = strconv.Itoa(len(body.Keys))
	}
	if body.Reason != "" {
		fields["reason"] = body.Reason
	}
	act := audit.OperatorAct{
		Event: audit.EventRevocationPublished, Actor: actor, Subject: body.Subject, Fields: fields,
		Message: fmt.Sprintf("%s published", body.Type),
	}
	if body.HostKey != nil {
		act.Target = body.HostKey.Target
		fields["fingerprint"] = body.HostKey.Fingerprint
	}
	// The event is out on the stream; the record of who published it is
	// written after, on its own. A failure to record is an outage the
	// operator must see, because the act happened and its record did not.
	if err := s.emitter.OperatorEvent(ctx, nil, tenant, act); err != nil {
		s.fail(w, r, "record revocation", err)
		return
	}
	answer(w, http.StatusOK, revocationReceipt{
		Type: body.Type, EventID: rcpt.EventID, Delivered: rcpt.Delivered,
		CoversHostKeyDecisions: rcpt.CoversHostKeyDecisions,
	})
}

func audienceString(ids []string) string {
	if len(ids) == 0 {
		return "all"
	}
	return strings.Join(ids, ",")
}

func (s *Server) revocationFailure(w http.ResponseWriter, r *http.Request, err error) {
	id := CorrelationIDFrom(r.Context())
	switch {
	case errors.Is(err, revoke.ErrNoAudience), errors.Is(err, revoke.ErrNoSelector), errors.Is(err, revoke.ErrNoReason):
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeInvalidRequest, CorrelationID: id,
			Parameters: map[string]any{"field": "body", "problem": "invalid"},
			Message:    err.Error(),
		})
	case errors.Is(err, fleet.ErrNoHostKeyRecord), errors.Is(err, fleet.ErrNoHostKeyCacheKey):
		reason := "no_record"
		if errors.Is(err, fleet.ErrNoHostKeyCacheKey) {
			reason = "recorded_before_keys"
		}
		writeError(w, http.StatusConflict, Error{
			Code: CodeHostKeyUnresolved, CorrelationID: id,
			Parameters: map[string]any{"reason": reason, "instead": "resync"},
			Message: "this server cannot resolve that host-key decision to the key it issued, so nothing was " +
				"published; a resync withdraws every cached decision, host keys included",
		})
	case errors.Is(err, revoke.ErrClosed):
		writeError(w, http.StatusServiceUnavailable, Error{
			Code: CodeUnavailable, CorrelationID: id,
			Message: "this server is shutting down and is no longer publishing events; retry against another",
		})
	default:
		s.fail(w, r, "publish revocation", err)
	}
}
