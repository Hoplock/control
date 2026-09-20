// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// The local publish path: `POST /debug/revoke`.
//
// IT EXISTS BECAUSE THE NORTH-BOUND API DOES NOT YET (0014), and for the same
// reason `seed` does. The difference is that this one is also the contract's
// own answer rather than only a gap: upstream Hoplock/proxy#56 states outright
// that NOTHING ON `/v1` PUBLISHES AN EVENT — an event originates from an
// operator action, on a surface the contract does not describe, and putting
// one on `/v1` would make every Hoplock Control implement an API no proxy
// calls. So the publish path is the implementation's to expose, the
// conformance suite takes it as an input (`events.publish_url`), and gap
// recovery is gradeable at all only because it exists: the suite has to make
// this server emit an event while a subscriber is away.
//
// Four properties keep it from becoming a back door, and the first two are
// not conveniences:
//
//   - IT IS OFF UNLESS CONFIGURED. `events.publish_listener` is empty by
//     default, and a deployment that does not set it has no publish port at
//     all.
//   - IT REQUIRES A CREDENTIAL. What it publishes is the kill switch; an
//     unauthenticated one is worse than none. `events.publish_token` is
//     required whenever the listener is bound, and config refuses the pair
//     without it.
//   - IT IS A SEPARATE PORT. The south-bound listener serves the contract and
//     nothing else (M2), so this is never routable from it.
//   - IT ONLY PUBLISHES. There is no read path, no listing, and no way to ask
//     it what any proxy holds.
//
// When 0014 lands this becomes a thin client of that API, or it goes away.

// publishRequest is the body `POST /debug/revoke` takes.
//
// The suite asserts NOTHING about this shape — only that posting to the URL
// makes an event happen — so it is this server's to choose. It is chosen to
// mirror the domain vocabulary rather than the wire event: an operator names
// who to reach and what to withdraw, and this server decides what that is on
// the wire.
type publishRequest struct {
	// Type is `session_kill`, `cache_invalidate`, `resync`, or
	// `host_key_withdraw` — the last being an operator action rather than
	// a wire type, because a host-key decision is withdrawn by publishing
	// the key it was issued under.
	Type string `json:"type"`
	// ProxyIDs addresses a named set; empty addresses every subscriber.
	ProxyIDs []string `json:"proxy_ids,omitempty"`

	SessionIDs []string `json:"session_ids,omitempty"`
	Subject    string   `json:"subject,omitempty"`
	Keys       []string `json:"keys,omitempty"`
	All        bool     `json:"all,omitempty"`
	Reason     string   `json:"reason,omitempty"`

	HostKey *struct {
		Target      string `json:"target"`
		Port        int32  `json:"target_port,omitempty"`
		Fingerprint string `json:"fingerprint"`
	} `json:"host_key,omitempty"`
}

// publishResponse is what an operator gets back.
//
// `covers_host_key_decisions` is on it deliberately. A subject-scoped
// invalidation cannot reach a host-key decision — the proxy keys one on
// target, port and fingerprint, not on a person — and an operator who believed
// otherwise would have been misled by this server. A revocation that silently
// misses is worse than one that refuses (PLAN §5.4).
type publishResponse struct {
	EventID                string `json:"event_id"`
	Delivered              int    `json:"delivered"`
	CoversHostKeyDecisions bool   `json:"covers_host_key_decisions"`
}

// publishServer is the handler tree for the publish listener.
type publishServer struct {
	op     *revoke.Operator
	tenant store.Tenant
	token  string
	log    *slog.Logger
}

func newPublishServer(op *revoke.Operator, tenant store.Tenant, token string, log *slog.Logger) (*publishServer, error) {
	if token == "" {
		// Config refuses this pairing too; the second check is here
		// because this constructor is what a test would reach for, and
		// an unauthenticated kill switch must not be reachable by
		// forgetting an argument.
		return nil, fmt.Errorf("publish: a token is required to bind the publish listener")
	}
	return &publishServer{op: op, tenant: tenant, token: token, log: log}, nil
}

func (p *publishServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /debug/revoke", http.HandlerFunc(p.publish))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusNotFound, "this listener publishes revocation events and serves nothing else")
	}))
	return mux
}

func (p *publishServer) publish(w http.ResponseWriter, r *http.Request) {
	if !p.authorised(r) {
		writeProblem(w, http.StatusUnauthorized, "the publish credential was rejected")
		return
	}

	var req publishRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "the request body is not the JSON object this endpoint expects")
			return
		}
	}
	if req.Type == "" {
		// A body-less POST is what the simplest harness sends, and the
		// suite asserts only that an event happens. An invalidation of
		// everything is the least surprising thing to mean by it: it is
		// idempotent, it touches no running session, and a proxy that
		// receives one drops its cache and re-asks.
		req.Type = "cache_invalidate"
		req.All = true
	}

	rcpt, err := p.run(r.Context(), req)
	if err != nil {
		status, msg := publishFailure(err)
		p.log.WarnContext(r.Context(), "a revocation publish was refused",
			"event", "revoke_publish_refused",
			"type", req.Type,
			"error", err.Error(),
		)
		writeProblem(w, status, msg)
		return
	}

	p.log.InfoContext(r.Context(), "revocation event published",
		"event", "revoke_publish",
		"type", req.Type,
		"event_id", rcpt.EventID,
		"delivered", rcpt.Delivered,
		"covers_host_key_decisions", rcpt.CoversHostKeyDecisions,
	)
	writeJSON(w, http.StatusOK, publishResponse{
		EventID:                rcpt.EventID,
		Delivered:              rcpt.Delivered,
		CoversHostKeyDecisions: rcpt.CoversHostKeyDecisions,
	})
}

func (p *publishServer) run(ctx context.Context, req publishRequest) (revoke.Receipt, error) {
	audience := revoke.Everyone()
	if len(req.ProxyIDs) > 0 {
		audience = revoke.ToProxies(req.ProxyIDs...)
	}

	switch req.Type {
	case "session_kill":
		return p.op.Kill(ctx, p.tenant, audience, revoke.Kill{
			SessionIDs: req.SessionIDs,
			Subject:    req.Subject,
			All:        req.All,
			Reason:     req.Reason,
		})
	case "cache_invalidate":
		return p.op.Invalidate(ctx, p.tenant, audience, revoke.Invalidation{
			Keys:    req.Keys,
			Subject: req.Subject,
			All:     req.All,
		})
	case "resync":
		return p.op.Resync(ctx, p.tenant, audience)
	case "host_key_withdraw":
		if req.HostKey == nil {
			return revoke.Receipt{}, fmt.Errorf("publish: host_key_withdraw needs a host_key")
		}
		return p.op.WithdrawHostKey(ctx, p.tenant, audience, revoke.HostKeyRef{
			Target:      req.HostKey.Target,
			Port:        req.HostKey.Port,
			Fingerprint: req.HostKey.Fingerprint,
		})
	default:
		return revoke.Receipt{}, fmt.Errorf("publish: %q is not an event this server publishes", req.Type)
	}
}

// publishFailure classifies what went wrong.
//
// An operator mistake is a 400 and says what was wrong; everything else is a
// 500 and says nothing beyond that. The one that is easy to get wrong is
// [fleet.ErrNoHostKeyRecord]: it is NOT a success with nothing to do, because
// the whole point of resolving the key from the record is that a withdrawal
// which drops nothing must not report that it dropped something.
func publishFailure(err error) (int, string) {
	switch {
	case errors.Is(err, revoke.ErrNoAudience),
		errors.Is(err, revoke.ErrNoSelector),
		errors.Is(err, revoke.ErrNoReason),
		errors.Is(err, fleet.ErrNoHostKeyRecord),
		errors.Is(err, fleet.ErrNoHostKeyCacheKey):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, revoke.ErrClosed):
		return http.StatusServiceUnavailable, "this server is shutting down and is no longer publishing events"
	case strings.HasPrefix(err.Error(), "publish: "):
		return http.StatusBadRequest, err.Error()
	default:
		return http.StatusInternalServerError, "the event could not be published"
	}
}

// authorised checks the bearer token in constant time.
//
// The comparison is constant-time because the value is a credential and the
// listener answers as fast as an attacker can ask: a byte-at-a-time comparison
// over a loop of requests is how a token is recovered without ever guessing one.
func (p *publishServer) authorised(r *http.Request) bool {
	const prefix = "Bearer "
	v := r.Header.Get("Authorization")
	if len(v) <= len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
		return false
	}
	presented := strings.TrimSpace(v[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(presented), []byte(p.token)) == 1
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeProblem(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": msg}})
}
