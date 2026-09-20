// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/hoplock/control/internal/fleet"
)

// The south-bound middleware chain.
//
// It is this listener's OWN chain and shares nothing with the north-bound one
// (M2). The two never share a port, a chain, or a credential type, because a
// proxy token that can reach a policy-authoring endpoint is privilege
// escalation from "can ask about decisions" to "can author them" — and the
// cheapest way to guarantee that cannot happen is for the two never to be
// routable from the same listener.

// CorrelationHeader is the request header a caller may set to tie its own logs
// to this server's. The value is echoed on every response, including errors,
// because an operator reading a 5xx needs the id the message names.
const CorrelationHeader = "X-Correlation-Id"

// Defaults for the chain. Each bounds something a caller would otherwise be
// able to choose for this server.
const (
	// DefaultMaxBodyBytes caps a request body. Every south-bound payload is
	// a small JSON object; the largest is a log batch, which this phase
	// does not serve.
	DefaultMaxBodyBytes int64 = 1 << 20
	// DefaultRequestTimeout bounds a single request. A server-side timeout
	// that ANSWERS is strictly better than a slow answer that looks like an
	// outage (M5), so this is a deadline on the handler rather than a
	// suggestion.
	DefaultRequestTimeout = 10 * time.Second
	// correlationIDBytes sizes a generated correlation id.
	correlationIDBytes = 9
)

type ctxKey int

const (
	ctxKeyCorrelationID ctxKey = iota
	ctxKeyCaller
)

// correlationIDFrom returns the id this request is logged under, empty when
// the chain did not run (which only happens in a handler called directly).
func correlationIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyCorrelationID).(string)
	return id
}

// callerFrom returns the verified proxy caller.
//
// It is read from the context rather than passed as an argument because the
// contract's handler interfaces (0002) take only a request — and the tenant is
// NOT on the wire (M18), so the context is the only place it can travel.
func callerFrom(ctx context.Context) (fleet.ProxyCaller, bool) {
	c, ok := ctx.Value(ctxKeyCaller).(fleet.ProxyCaller)
	return c, ok
}

// withCorrelationID stamps every request with an id, taking the caller's where
// one was offered.
func (s *Server) withCorrelationID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitiseCorrelationID(r.Header.Get(CorrelationHeader))
		if id == "" {
			id = newCorrelationID()
		}
		w.Header().Set(CorrelationHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyCorrelationID, id)))
	})
}

// sanitiseCorrelationID bounds and filters a caller-supplied id.
//
// It goes into log lines and into a 5xx message, so a caller must not be able
// to put a newline, a control character or a kilobyte of text in either. An id
// that does not survive the filter is discarded rather than repaired.
func sanitiseCorrelationID(v string) string {
	const maxLen = 64
	if len(v) == 0 || len(v) > maxLen {
		return ""
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == ':':
		default:
			return ""
		}
	}
	return v
}

func newCorrelationID() string {
	buf := make([]byte, correlationIDBytes)
	if _, err := rand.Read(buf); err != nil {
		// A correlation id is for reading logs, so failing the request
		// over one would trade an outage for a diagnostic. Say so in the
		// id itself rather than pretend.
		return "no-correlation-id"
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// statusRecorder captures the status code for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap is what lets [http.ResponseController] reach the real writer through
// this wrapper.
//
// Without it, a `Flush` or a write deadline on the revocation stream resolves
// to "not supported" and the subscription ends on its first line — the access
// log would have silently turned the fleet's only inbound channel into a
// single event per connection.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// withLogging writes one line per request.
//
// WHAT IT DOES NOT LOG IS THE POINT. No body, no header values, no query
// string: `/v1/auth/password` carries a password, and the one place a password
// exists is in transit (PLAN §7). The fields below are the method, the path,
// the outcome and who asked — every one of which is safe to write down.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		attrs := []any{
			"event", "southbound_request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", s.now().Sub(start).Milliseconds(),
			"correlation_id", correlationIDFrom(r.Context()),
		}
		if c, ok := callerFrom(r.Context()); ok {
			attrs = append(attrs, "tenant", c.Tenant.String(), "token_id", c.TokenID)
			if c.Bound() {
				attrs = append(attrs, "proxy_id", c.ProxyID)
			}
		}
		if rec.status >= http.StatusInternalServerError {
			s.log.ErrorContext(r.Context(), "south-bound request failed", attrs...)
			return
		}
		s.log.InfoContext(r.Context(), "south-bound request", attrs...)
	})
}

// withRecovery turns a panic into a 5xx with a correlation id.
//
// A panic is an OUTAGE and must never surface as a 401 (M11). It is caught
// here rather than left to net/http's own recovery so that the answer is the
// contract's envelope: a caller that got a closed connection cannot tell an
// outage from a network fault, and the proxy's fail-closed rule then has
// nothing to classify.
func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			id := correlationIDFrom(r.Context())
			s.log.ErrorContext(r.Context(), "south-bound handler panicked",
				"event", "southbound_panic",
				"path", r.URL.Path,
				"correlation_id", id,
				"panic", rec,
			)
			writeJSON(w, http.StatusInternalServerError, internalEnvelope(id))
		}()
		next.ServeHTTP(w, r)
	})
}

// withLimits bounds the body and the time a request may take.
//
// THE REVOCATION STREAM IS EXEMPT FROM THE DEADLINE AND FROM NOTHING ELSE. It
// is a subscription the proxy holds open for as long as it is running, so a
// request timeout would cut it every ten seconds and turn the fleet's only
// inbound channel into a reconnect storm — which is the shape of an outage
// rather than of a bound. What it keeps is the body limit (a GET carries none)
// and the whole chain above and below it: the correlation id, the access log,
// panic recovery, and the proxy credential. The bound that replaces the
// deadline is per-write and lives on the handler, because what needs bounding
// on a stream is a peer that has stopped reading, not one that is idle.
func (s *Server) withLimits(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)

		if isEventStream(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withProxyAuth verifies the proxy's channel credential (M2).
//
// EVERY route on this listener goes through it, including the catch-all, so
// there is no path — present or future, spelled right or wrong — that answers
// anything to an unauthenticated caller. One route answering 200 to a token
// another rejects is how a listener ends up with an unauthenticated corner.
//
// The three-way return from the registry is what keeps M11 honest here: a
// database failure arrives as an error and becomes a 5xx, and only an explicit
// `ok == false` becomes the 401.
func (s *Server) withProxyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := bearerToken(r)
		if !ok {
			s.writeError(w, r, rejectCredential())
			return
		}
		caller, ok, err := s.fleet.AuthenticateProxyToken(r.Context(), presented)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		if !ok {
			s.writeError(w, r, rejectCredential())
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyCaller, caller)))
	})
}

// bearerToken reads the Authorization header. A missing or malformed header is
// indistinguishable from a wrong token to the caller, on purpose.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	v := r.Header.Get("Authorization")
	if len(v) <= len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(v[len(prefix):])
	return token, token != ""
}

// writeJSON writes a response body. It is the only writer in this package, so
// the content type cannot drift between the success and error paths.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// A write failure here is a client that went away. There is nothing to
	// say to it and nothing to recover, and logging one per dropped
	// connection would be noise during exactly the incident an operator is
	// trying to read the log through.
	_ = json.NewEncoder(w).Encode(body)
}

// writeError renders a classified failure and logs the parts that are not
// disclosed.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	id := correlationIDFrom(r.Context())
	status, envelope := statusFor(err, id)
	if status >= http.StatusInternalServerError {
		// The disclosed message names only the correlation id, so the real
		// error has to be written down here or it is lost.
		s.log.ErrorContext(r.Context(), "south-bound request could not be served",
			"event", "southbound_failure",
			"path", r.URL.Path,
			"correlation_id", id,
			"error", err.Error(),
		)
	}
	writeJSON(w, status, envelope)
}

// logDeny records a refusal with the reason this server decided on.
//
// The reason is written HERE and is never disclosed: a precise denial makes
// this server an oracle for probing the estate, and the operator resolves the
// session id into the whole story instead (M4).
func (s *Server) logDeny(ctx context.Context, path, reason string, attrs ...any) {
	s.log.InfoContext(ctx, "south-bound request denied",
		append([]any{
			"event", "southbound_deny",
			"path", path,
			"reason", reason,
			"correlation_id", correlationIDFrom(ctx),
		}, attrs...)...)
}
