// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// Server is the north-bound listener's handler tree.
//
// It serves HUMANS, CI AND GITOPS, and never the contract (M2). There is no
// `/v1/authorize` on it, no proxy credential reaches it, and
// `TestNoContractRouteIsReachable` keeps that true: the day somebody mounts a
// contract route on the wrong mux is not the day anyone notices.
//
// WHAT THIS PHASE SERVES. Federation and sessions — the credential model 0014
// was told to resolve its tenant from — and the certificate authority's own
// surface, which is this phase's to build. 0014 adds policy, inventory, audit
// query and the explain endpoint to this same router, which is why the router
// is the enforcement point rather than each handler.
type Server struct {
	auth       Authenticator
	federation *identity.Federation
	ca         *credential.CA
	log        *slog.Logger
	now        func() time.Time

	maxBodyBytes   int64
	requestTimeout time.Duration
	secureCookies  bool
	defaultTenant  store.Tenant

	router  *Router
	handler http.Handler
}

// Options configures a Server.
type Options struct {
	// Federation owns logins, sessions and API tokens (0011).
	Federation *identity.Federation
	// CA is the per-tenant certificate authority (proxy D6a). Nil when this
	// deployment has no key-encryption key configured; see New.
	CA *credential.CA
	// Authenticator resolves a presented credential. Nil takes Federation,
	// which is the production wiring; a test supplies its own.
	Authenticator Authenticator
	// Logger is where the access log goes. Nil takes slog's default.
	Logger *slog.Logger
	// MaxBodyBytes and RequestTimeout override the chain's bounds.
	MaxBodyBytes   int64
	RequestTimeout time.Duration
	// InsecureCookies drops the Secure attribute from the session cookie. It
	// exists for a local development deployment over plain HTTP and for
	// tests, and it is spelled "insecure" so that nobody sets it by
	// accident.
	InsecureCookies bool
	// DefaultTenant is what a pre-authentication route resolves to when the
	// caller names none. It is the single-tenant deployment's whole
	// experience of tenancy: one tenant configured, no route with a required
	// parameter, nobody who has to know (M18).
	DefaultTenant store.Tenant
	// Now overrides the clock. Tests use it.
	Now func() time.Time
}

// New builds the north-bound handler tree.
//
// It refuses to build without a federation service rather than accepting nil and
// answering 5xx later: a listener that starts and cannot authenticate anybody is
// an operator lockout discovered one login at a time.
func New(o Options) (*Server, error) {
	if o.Federation == nil {
		return nil, fmt.Errorf("httpapi/north: a federation service is required")
	}
	// A NIL CA IS ALLOWED, and it is the one dependency that is. The software
	// custodian refuses to exist without a key-encryption key
	// (`credential.key_encryption_key_env`), so a deployment that has not set
	// one has no certificate authority — and refusing to start the whole
	// north-bound surface over that would make the console unreachable to fix
	// it. The routes are still registered, so the isolation test still covers
	// them, and they answer [CodeCANotConfigured] naming the key to set: an
	// operator-shaped answer beats a listener that will not bind.

	s := &Server{
		auth:           o.Authenticator,
		federation:     o.Federation,
		ca:             o.CA,
		log:            o.Logger,
		now:            o.Now,
		maxBodyBytes:   o.MaxBodyBytes,
		requestTimeout: o.RequestTimeout,
		secureCookies:  !o.InsecureCookies,
		defaultTenant:  o.DefaultTenant,
	}
	if s.auth == nil {
		s.auth = o.Federation
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.maxBodyBytes <= 0 {
		s.maxBodyBytes = DefaultMaxBodyBytes
	}
	if s.requestTimeout <= 0 {
		s.requestTimeout = DefaultRequestTimeout
	}

	s.router = NewRouter(s.enforce)
	s.router.SetNotFound(s.notFoundHandler())
	for _, route := range s.routes() {
		if err := s.router.Register(route); err != nil {
			return nil, err
		}
	}

	// The chain, outermost first. The correlation id is first because every
	// line below it names one; recovery is inside the log so a panic is
	// still logged as a request.
	s.handler = s.withCorrelationID(s.withLogging(s.withRecovery(s.withLimits(s.router))))
	return s, nil
}

// ServeHTTP serves the whole tree.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// Routes returns the registered routes, for the startup log and for the tests.
func (s *Server) Routes() []RouteInfo { return s.router.Routes() }

// notFoundHandler answers the envelope for a path this surface does not serve.
//
// It is registered on the router as its catch-all rather than being a route,
// because a catch-all ROUTE would have to declare an access class and an
// unauthenticated 404 is not a class — it is the absence of a route.
func (s *Server) notFoundHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "this server does not serve that path",
		})
	})
}

// fail renders an outage and writes the real error down.
//
// EVERY error that reaches here is a 5xx. A refusal never does: a refusal is
// built on purpose by the code that decided to refuse (M11), which is why this
// function has no branch that could answer 401.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, op string, err error) {
	id := CorrelationIDFrom(r.Context())
	s.log.ErrorContext(r.Context(), "north-bound request could not be served",
		"event", "northbound_failure",
		"op", op,
		"path", r.URL.Path,
		"correlation_id", id,
		"error", err.Error(),
	)
	writeError(w, http.StatusInternalServerError, Error{
		Code: CodeInternal, CorrelationID: id,
		Message: "this server could not complete the request; quote the correlation id",
	})
}

// refused renders a deliberate refusal, disclosing only what is safe.
//
// A broker's Detail is NEVER in the body: "the assertion's audience was
// https://other" is a diagnosis in a log and an oracle in a response.
func (s *Server) refused(w http.ResponseWriter, r *http.Request, be *identity.BrokerError) {
	id := CorrelationIDFrom(r.Context())
	s.log.WarnContext(r.Context(), "a login was refused",
		"event", "northbound_login_refused",
		"code", be.Code,
		"detail", be.Detail,
		"path", r.URL.Path,
		"correlation_id", id,
	)
	status := http.StatusUnauthorized
	if be.Code == identity.RejectFederationNoSuchOne {
		status = http.StatusNotFound
	}
	writeError(w, status, Error{
		Code: CodeLoginRefused, CorrelationID: id,
		Message:    be.Message,
		Parameters: map[string]any{"reason": be.Code},
	})
}

// answer writes a success body.
func answer(w http.ResponseWriter, status int, body any) { writeJSON(w, status, body) }

// requireTenant reads the tenant the middleware resolved.
//
// A handler on an AccessTenant route can rely on it being there; the branch
// exists so that a handler wired onto the wrong access class fails loudly rather
// than operating on the empty tenant, which `store.checkTenant` would then
// refuse one layer down with a much less obvious message.
func (s *Server) requireTenant(w http.ResponseWriter, r *http.Request) (store.Tenant, bool) {
	tenant, ok := TenantFrom(r.Context())
	if !ok {
		s.fail(w, r, "resolve tenant",
			fmt.Errorf("httpapi/north: %s %s ran without a resolved tenant; its access class is wrong",
				r.Method, r.URL.Path))
		return "", false
	}
	return tenant, true
}

// Ensure prepares a tenant's certificate authority at start-up.
//
// It is here rather than in the CA because the listener is what knows a tenant
// is going to be served: a deployment configured for one tenant gets its CA on
// boot, so the first session does not pay for a key generation.
func (s *Server) Ensure(ctx context.Context, tenant store.Tenant) error {
	if tenant == "" || s.ca == nil {
		return nil
	}
	_, err := s.ca.Ensure(ctx, tenant)
	return err
}

// errNoPrincipal and errTrailingBody are the two internal faults this package
// can produce, named so a 5xx log line says which.
var (
	errNoPrincipal  = errors.New("httpapi/north: an authenticated route ran without a principal")
	errTrailingBody = errors.New("httpapi/north: the request body carried more than one document")
)
