// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The north-bound middleware chain.
//
// It is this listener's OWN chain and shares nothing with the south-bound one
// (M2). The two never share a port, a chain, or a credential type, because a
// proxy token that can reach a policy-authoring endpoint is privilege escalation
// from "can ask about decisions" to "can author them".
//
// THE TENANT IS RESOLVED HERE AND NOWHERE ELSE (M18). A handler reads it from
// the context or not at all.

// CorrelationHeader is the request header a caller may set to tie its own logs
// to this server's. The value is echoed on every response, including errors.
const CorrelationHeader = "X-Correlation-Id"

// Defaults for the chain.
const (
	// DefaultMaxBodyBytes caps a request body. A policy bundle is the
	// largest thing this surface will ever accept (0014); this is generous
	// for it and finite for everything else.
	DefaultMaxBodyBytes int64 = 4 << 20
	// DefaultRequestTimeout bounds a single request. A server-side timeout
	// that ANSWERS beats a slow answer that looks like an outage.
	DefaultRequestTimeout = 30 * time.Second
	correlationIDBytes    = 9
)

type ctxKey int

const (
	ctxKeyCorrelationID ctxKey = iota
	ctxKeyPrincipal
	ctxKeyTenant
	ctxKeyFacts
)

// requestFacts is what the access log learns from a middleware INSIDE it.
//
// It exists because the principal and the tenant are resolved below the logging
// middleware, on a child context the log line cannot see. A mutable holder
// placed on the way down and filled on the way through is the alternative to
// either logging before the answer is known (no principal, no tenant) or moving
// authentication above the log (no line at all for a request that fails it).
type requestFacts struct {
	principal *identity.Principal
	tenant    store.Tenant
}

func factsFrom(ctx context.Context) *requestFacts {
	f, _ := ctx.Value(ctxKeyFacts).(*requestFacts)
	return f
}

// CorrelationIDFrom returns the id this request is logged under.
func CorrelationIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyCorrelationID).(string)
	return id
}

// PrincipalFrom returns the authenticated caller.
//
// It is the ONLY thing a handler may consult about who is calling. There is no
// accessor that answers "which tenants could this caller reach" — a handler that
// needs a tenant uses [TenantFrom], which is the one the middleware resolved.
func PrincipalFrom(ctx context.Context) (*identity.Principal, bool) {
	p, ok := ctx.Value(ctxKeyPrincipal).(*identity.Principal)
	return p, ok && p != nil
}

// TenantFrom returns the ONE tenant this request resolved to.
//
// Every request resolves to exactly one, so an audit record never has to say
// "some of them" (M18). A handler on an AccessTenant route can rely on it; on
// any other class it is empty, and that is deliberate — a route that needs a
// tenant is a route that declares it needs one.
func TenantFrom(ctx context.Context) (store.Tenant, bool) {
	t, ok := ctx.Value(ctxKeyTenant).(store.Tenant)
	return t, ok && t != ""
}

// Authenticator resolves a presented north-bound credential.
//
// The three-way return is M11's: an error is an OUTAGE, `ok == false` is the
// only thing that becomes a 401, and there is no path by which a database
// failure answers "not recognised".
type Authenticator interface {
	Authenticate(ctx context.Context, presented string) (*identity.Principal, bool, error)
}

// enforce is the single implementation of every access class.
//
// It is the function the router's wrap resolves to, and it is the whole of
// M18's north-bound half. Read it top to bottom: correlation, credential,
// tenant, permission — and note that each step's failure has its own code,
// because "you are not signed in", "you cannot do that here" and "that is not
// your tenant" are three different operator problems.
func (s *Server) enforce(route Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id := CorrelationIDFrom(ctx)

		if route.Access == AccessAnonymous {
			route.Handler(w, r)
			return
		}

		presented, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, Error{
				Code: CodeUnauthenticated, CorrelationID: id,
				Message: "this request carried no credential",
			})
			return
		}
		principal, ok, err := s.auth.Authenticate(ctx, presented)
		if err != nil {
			s.fail(w, r, "authenticate", err)
			return
		}
		if !ok {
			// A missing, malformed, expired and revoked credential all
			// answer the same thing. Telling them apart would make this
			// listener an oracle.
			writeError(w, http.StatusUnauthorized, Error{
				Code: CodeUnauthenticated, CorrelationID: id,
				Message: "that credential was not accepted",
			})
			return
		}
		ctx = context.WithValue(ctx, ctxKeyPrincipal, principal)
		if facts := factsFrom(ctx); facts != nil {
			facts.principal = principal
		}

		if route.Access == AccessAuthenticated {
			route.Handler(w, r.WithContext(ctx))
			return
		}

		tenant, selected := selectedTenant(r)
		if !selected {
			sole, ok := principal.SoleTenant()
			if !ok {
				// A principal scoped to several tenants making a
				// request that named none. Never defaulted: a
				// default would silently pick one.
				writeError(w, http.StatusBadRequest, Error{
					Code: CodeTenantAmbiguous, CorrelationID: id,
					Message:    "this credential can act in several tenants, so the request must name one",
					Parameters: map[string]any{"tenants": tenantStrings(principal.ScopedTenants())},
				})
				return
			}
			tenant = sole
		}

		// THE REFUSAL M18 EXISTS FOR. A tenant named in a path, a header
		// or a query is a SELECTOR within the principal's scope, never a
		// widening of it — and it is refused here, before the handler
		// runs, because the failure mode is one handler that forgot.
		if !principal.MayActIn(tenant) {
			s.logRefusal(ctx, r, principal, tenant, CodeTenantOutOfScope)
			writeError(w, http.StatusForbidden, Error{
				Code: CodeTenantOutOfScope, CorrelationID: id,
				Message:    "this credential cannot act in that tenant",
				Parameters: map[string]any{"tenant": tenant.String()},
			})
			return
		}
		if !principal.Can(tenant, route.Permission) {
			s.logRefusal(ctx, r, principal, tenant, CodeForbidden)
			writeError(w, http.StatusForbidden, Error{
				Code: CodeForbidden, CorrelationID: id,
				Message: "this credential does not hold the permission this route needs",
				Parameters: map[string]any{
					"tenant":     tenant.String(),
					"permission": string(route.Permission),
				},
			})
			return
		}

		ctx = context.WithValue(ctx, ctxKeyTenant, tenant)
		if facts := factsFrom(ctx); facts != nil {
			facts.tenant = tenant
		}
		route.Handler(w, r.WithContext(ctx))
	})
}

// selectedTenant reads the selector, in the order a caller would expect.
//
// Three ways rather than one, because M18 requires single-tenant deployments to
// look untouched: a route may carry `{tenant}`, a machine client may set a
// header, and a browser that can set neither may use a query parameter. None of
// them is trusted — all three are selectors checked against the principal's
// scope immediately above.
func selectedTenant(r *http.Request) (store.Tenant, bool) {
	if v := strings.TrimSpace(r.PathValue(TenantPathParam)); v != "" {
		return store.Tenant(v), true
	}
	if v := strings.TrimSpace(r.Header.Get(TenantHeader)); v != "" {
		return store.Tenant(v), true
	}
	if v := strings.TrimSpace(r.URL.Query().Get(TenantQueryParam)); v != "" {
		return store.Tenant(v), true
	}
	return "", false
}

func tenantStrings(tenants []store.Tenant) []string {
	out := make([]string, 0, len(tenants))
	for _, t := range tenants {
		out = append(out, t.String())
	}
	return out
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

// sanitiseCorrelationID bounds and filters a caller-supplied id. It goes into
// log lines and into an error body, so a caller must not be able to put a
// newline, a control character or a kilobyte of text in either.
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
		return "no-correlation-id"
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// withLogging writes one line per request.
//
// WHAT IT DOES NOT LOG IS THE POINT. No body, no header values, no query
// string: the login route carries a password and every authenticated route
// carries a credential in a header (0007's rule on the other listener, and the
// same reason). The fields below are all safe to write down.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		rec := &statusRecorder{ResponseWriter: w}
		facts := &requestFacts{}
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyFacts, facts))
		next.ServeHTTP(rec, r)

		attrs := []any{
			"event", "northbound_request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", s.now().Sub(start).Milliseconds(),
			"correlation_id", CorrelationIDFrom(r.Context()),
		}
		if p := facts.principal; p != nil {
			attrs = append(attrs,
				"principal_id", p.ID,
				"principal_kind", string(p.Kind),
				"subject", p.Subject,
				// A break-glass request is visible in the access log
				// as well as in the audit record, because the access
				// log is what an operator is already reading (M7).
				"break_glass", p.BreakGlass,
			)
		}
		if facts.tenant != "" {
			attrs = append(attrs, "tenant", facts.tenant.String())
		}
		if rec.status >= http.StatusInternalServerError {
			s.log.ErrorContext(r.Context(), "north-bound request failed", attrs...)
			return
		}
		s.log.InfoContext(r.Context(), "north-bound request", attrs...)
	})
}

// withRecovery turns a panic into a 5xx with a correlation id. A panic is an
// OUTAGE and must never surface as a 401 (M11).
func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			id := CorrelationIDFrom(r.Context())
			s.log.ErrorContext(r.Context(), "north-bound handler panicked",
				"event", "northbound_panic",
				"path", r.URL.Path,
				"correlation_id", id,
				"panic", rec,
			)
			writeError(w, http.StatusInternalServerError, Error{
				Code: CodeInternal, CorrelationID: id,
				Message: "this server could not complete the request; quote the correlation id",
			})
		}()
		next.ServeHTTP(w, r)
	})
}

// withLimits bounds the body and the time a request may take.
func (s *Server) withLimits(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
		ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken reads the Authorization header, or the session cookie a browser
// carries. A missing and a malformed credential are indistinguishable from a
// wrong one to the caller, on purpose.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	v := r.Header.Get("Authorization")
	if len(v) > len(prefix) && strings.EqualFold(v[:len(prefix)], prefix) {
		token := strings.TrimSpace(v[len(prefix):])
		if token != "" {
			return token, true
		}
	}
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		return c.Value, true
	}
	return "", false
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

// Unwrap is what lets http.ResponseController reach the real writer.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logRefusal records an authorization refusal with the detail the caller is not
// told.
//
// The code and the tenant are written HERE rather than disclosed in full,
// because a precise refusal on this surface is an enumeration oracle for which
// tenants exist.
func (s *Server) logRefusal(ctx context.Context, r *http.Request, p *identity.Principal, tenant store.Tenant, code string) {
	s.log.WarnContext(ctx, "north-bound request refused",
		"event", "northbound_refused",
		"code", code,
		"path", r.URL.Path,
		"method", r.Method,
		"principal_id", p.ID,
		"subject", p.Subject,
		"requested_tenant", tenant.String(),
		"scoped_tenants", tenantStrings(p.ScopedTenants()),
		"correlation_id", CorrelationIDFrom(ctx),
	)
}
