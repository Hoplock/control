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
	"github.com/hoplock/control/internal/decision"
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
	decision *decision.Service
	events   EventStream
	logs     LogIngest
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
	// Decision answers `/v1/authorize`: the endpoint the whole system
	// turns on (0008).
	Decision *decision.Service
	// Events is the revocation broker this listener streams from
	// (`internal/revoke`, M9).
	Events EventStream
	// Logs is the audit ingester behind the two log paths
	// (`internal/audit`, M8).
	Logs LogIngest
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
	if o.Decision == nil {
		// Same reasoning as the two above, and it bites hardest here: a
		// listener that authenticates everybody and can decide nothing
		// holds every handshake in the estate open to answer `5xx`.
		return nil, fmt.Errorf("httpapi/south: a decision service is required")
	}
	if o.Events == nil {
		// The same rule again, and this one is the contract's own: A
		// SERVER THAT ISSUES CACHE HINTS MUST SERVE THIS STREAM (M9).
		// The hint gate is above both responses that carry one and it
		// reads the subscription state this broker owns, so a listener
		// built without it would not merely lack a route — it would
		// answer every authorize with no hint and no way to say why.
		return nil, fmt.Errorf("httpapi/south: an event stream is required")
	}
	if o.Logs == nil {
		// The same rule a fourth time, and here it is the quietest
		// failure of the four: a listener without an ingester answers
		// 5xx to every batch, the proxy keeps buffering to disk, and
		// nothing looks wrong until the buffer fills or an incident
		// needs a record that was never shipped. An audit store is not
		// optional equipment (M8).
		return nil, fmt.Errorf("httpapi/south: a log ingester is required")
	}

	s := &Server{
		identity:       o.Identity,
		fleet:          o.Fleet,
		decision:       o.Decision,
		events:         o.Events,
		logs:           o.Logs,
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

// servedPaths are the contract endpoints THIS BUILD answers.
//
// EVERY ENDPOINT THE CONTRACT DEFINES IS NOW ON THIS LIST. `enums_test.go`
// holds the other half of that statement — it reads the paths out of the
// vendored document — so an endpoint added upstream and not served here is a
// missing route rather than a shorter list.
var servedPaths = []string{
	contract.PathAuthCert,
	contract.PathAuthPassword,
	contract.PathAuthMFAPoll,
	contract.PathAuthorize,
	contract.PathHostKeyReport,
	contract.PathCapabilitiesReport,
	contract.PathUIDLease,
	contract.PathLogsBatch,
	contract.PathLogsPriority,
	contract.PathProxyEvents,
}

func (s *Server) build() {
	mux := http.NewServeMux()
	h := s.handlers()

	mux.Handle("POST "+contract.PathAuthCert, s.endpoint(h.authenticateCert))
	mux.Handle("POST "+contract.PathAuthPassword, s.endpoint(h.authenticatePassword))
	mux.Handle("POST "+contract.PathAuthMFAPoll, s.endpoint(h.pollMFA))
	mux.Handle("POST "+contract.PathAuthorize, s.endpoint(h.authorize))
	mux.Handle("POST "+contract.PathHostKeyReport, s.endpoint(h.reportHostKey))
	mux.Handle("POST "+contract.PathCapabilitiesReport, s.endpoint(h.reportCapabilities))
	mux.Handle("POST "+contract.PathUIDLease, s.endpoint(h.leaseUIDs))

	// The one 202. It is the contract's own distinction between the two log
	// paths — a batch is ACCEPTED FOR STORAGE and a priority record is
	// DURABLE — and it is worth keeping visible here rather than hiding
	// inside a handler that returns a status.
	mux.Handle("POST "+contract.PathLogsBatch, s.endpointStatus(http.StatusAccepted, h.ingestLogBatch))
	mux.Handle("POST "+contract.PathLogsPriority, s.endpoint(h.ingestLogPriority))

	// The one GET, and the one route that does not go through
	// [Server.endpoint]: it answers a stream rather than a document, so it
	// writes its own status line and its own content type (see events.go).
	mux.Handle("GET "+contract.PathProxyEvents, http.HandlerFunc(h.subscribeEvents))

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
