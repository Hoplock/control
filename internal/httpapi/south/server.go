// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/identity"
)

// Server is the south-bound listener's handler tree.
//
// It serves THE CONTRACT AND NOTHING ELSE. There is no operator route, no
// policy-authoring route, no debug route and no metrics route on it, and
// `TestNoNorthBoundRouteIsReachable` is what keeps that true — the day
// somebody mounts an admin route on the wrong mux is not the day anyone
// notices (M2).
type Server struct {
	identity *identity.Service
	fleet    *fleet.Registry
	log      *slog.Logger
	now      func() time.Time

	maxBodyBytes   int64
	requestTimeout time.Duration

	handler http.Handler
	routes  []string
}

// Options configures a Server.
type Options struct {
	// Identity owns the authentication conversation (0011 federates it).
	Identity *identity.Service
	// Fleet answers the proxy's own credential, the chain-leg key
	// question, host-key trust, capability reports and uid leases.
	Fleet *fleet.Registry
	// Logger is where the access log goes. Nil takes slog's default.
	Logger *slog.Logger
	// MaxBodyBytes and RequestTimeout override the chain's bounds.
	MaxBodyBytes   int64
	RequestTimeout time.Duration
	// Now overrides the clock. Tests use it; nothing in production should.
	Now func() time.Time
}

// New builds the south-bound handler tree.
//
// It refuses to build without an identity service and a fleet registry rather
// than accepting nil and answering 5xx later: a listener that starts and
// cannot authenticate anybody is a fleet-wide outage discovered one connection
// at a time.
func New(o Options) (*Server, error) {
	if o.Identity == nil {
		return nil, fmt.Errorf("httpapi/south: an identity service is required")
	}
	if o.Fleet == nil {
		return nil, fmt.Errorf("httpapi/south: a fleet registry is required")
	}

	s := &Server{
		identity:       o.Identity,
		fleet:          o.Fleet,
		log:            o.Logger,
		now:            o.Now,
		maxBodyBytes:   o.MaxBodyBytes,
		requestTimeout: o.RequestTimeout,
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
	s.build()
	return s, nil
}

// servedPaths are the contract endpoints THIS PHASE answers.
//
// The rest of the contract is mounted by the phase that implements it —
// `/v1/authorize` (0008), the event stream (0009), log ingest (0010) — and
// until then a request for one is a 404 with the contract's envelope rather
// than a route that pretends. A stub answering a plausible-looking empty
// policy would be worse than absent: the proxy would act on it.
var servedPaths = []string{
	contract.PathAuthCert,
	contract.PathAuthPassword,
	contract.PathAuthMFAPoll,
	contract.PathHostKeyReport,
	contract.PathCapabilitiesReport,
	contract.PathUIDLease,
}

func (s *Server) build() {
	mux := http.NewServeMux()
	h := s.handlers()

	mux.Handle("POST "+contract.PathAuthCert, s.endpoint(h.authenticateCert))
	mux.Handle("POST "+contract.PathAuthPassword, s.endpoint(h.authenticatePassword))
	mux.Handle("POST "+contract.PathAuthMFAPoll, s.endpoint(h.pollMFA))
	mux.Handle("POST "+contract.PathHostKeyReport, s.endpoint(h.reportHostKey))
	mux.Handle("POST "+contract.PathCapabilitiesReport, s.endpoint(h.reportCapabilities))
	mux.Handle("POST "+contract.PathUIDLease, s.endpoint(h.leaseUIDs))

	// The catch-all answers the contract's envelope for anything else,
	// INCLUDING a north-bound path somebody pointed at the wrong port. It
	// sits inside the authentication middleware, so an unauthenticated
	// caller is refused before it can learn which paths exist.
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, r, notFound("this listener serves the south-bound contract only"))
	}))

	s.routes = slices.Clone(servedPaths)
	slices.Sort(s.routes)

	// Outermost first. The correlation id is stamped before anything can
	// fail, so every answer — including a panic's — names one; recovery
	// sits inside logging so a panicking request still produces an access
	// log line; and authentication sits innermost so that nothing before it
	// has to know who is calling.
	s.handler = s.withCorrelationID(
		s.withLogging(
			s.withRecovery(
				s.withLimits(
					s.withProxyAuth(mux)))))
}

// Handler returns the tree.
func (s *Server) Handler() http.Handler { return s.handler }

// Routes returns the contract paths this listener serves, sorted.
//
// It exists so that "no north-bound route is reachable here" is asserted
// against the route table rather than against a list of paths somebody thought
// to try (M2).
func (s *Server) Routes() []string { return slices.Clone(s.routes) }

// ServeHTTP makes the Server a handler in its own right.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// StatusCodeFor exposes the error mapper for a test.
//
// M11's regression test has to be able to ask "what would this error have
// answered" without standing a database up, and the alternative — asserting
// the mapping by reading it — is how the rule quietly stops being true.
func StatusCodeFor(err error) int {
	status, _ := statusFor(err, "")
	return status
}
