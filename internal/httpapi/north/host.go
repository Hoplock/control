// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/identity"
)

// A HOST'S ROUTES ENTER THE SAME TABLE (M15, M2, M18).
//
// Hoplock Enterprise serves its API from this listener, behind this middleware,
// because the alternative — a second listener with a second sign-in — is a fork
// of the credential model (its E2) and a second surface M2 has no room for. So a
// host's route is registered through [Router.Register] exactly like Control's:
// there is no second router, no second chain, and the isolation test enumerates
// host routes on the day they are mounted.
//
// A host route is one of two classes, and the middleware resolves what each
// covers before the handler runs:
//
//   - AccessTenant, at /api/v1/tenants/{tenant}/<pattern>: the middleware
//     authenticates, resolves exactly one tenant from the principal's scope
//     and checks the route's permission in it.
//   - AccessCrossTenant, at /api/v1/cross-tenant/<pattern> (M24): the
//     middleware authenticates and resolves the set of tenants in the
//     principal's scope where it holds the route's permission, narrowed by the
//     request's selector and never widened. The permission must be a read.
//
// Three things a host does NOT get, each on purpose:
//
//   - a cross-tenant write, or any route outside these two classes. A write
//     across tenants is N records in N audit chains (M8) with no atomicity and
//     an outcome M11 cannot name, and anonymous, authenticated-only and
//     self-scoped routes are Control's own.
//   - a permission of its own. A host NAMES one of identity.AllPermissions;
//     the role set is fixed and lives in code, so what "admin" means does not
//     depend on which binary is running.
//   - a 401. On this surface that is the middleware's answer alone (M11), and
//     [WriteHostError] renders one as an outage.

// HostRoute is a host binary's north-bound route.
type HostRoute struct {
	// Method is one of hostMethods.
	Method string
	// Pattern is relative to the class's prefix — [HostPrefix] or
	// [CrossTenantPrefix]: no leading "/", no "{tenant}", no "..", no
	// method prefix.
	Pattern string
	// Access is AccessTenant or AccessCrossTenant. Every other class,
	// including the zero value, is refused: a host gets no anonymous,
	// authenticated-only or self-scoped route.
	Access Access
	// Permission is one of identity.AllPermissions; on a cross-tenant route,
	// one the auditor role holds.
	Permission identity.Permission
	// Summary is one line for the route listing.
	Summary string
	// Handler serves the request. [HostCallerFrom] says who is calling.
	Handler http.Handler
}

// hostMethods are the methods a host route may serve.
var hostMethods = []string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
}

// HostPrefix is where every AccessTenant host route lives.
const HostPrefix = APIPrefix + "/tenants/{" + TenantPathParam + "}/"

// HostCaller is who Control authenticated for a host route: a public
// projection of the principal, never the principal itself, whose scope map
// stays unreachable (M18).
type HostCaller struct {
	// Tenant is the one tenant the middleware resolved on a tenant route,
	// and empty on a cross-tenant one.
	Tenant ext.Tenant
	// Tenants is the set the middleware resolved on a cross-tenant route:
	// sorted, never empty. Nil on a tenant route.
	Tenants []ext.Tenant
	// Subject is the person behind a session — ID and Groups. Zero for a
	// machine token. Username and ExternalID are empty: the principal
	// carries neither, and they are never guessed.
	Subject ext.Subject
	// Principal is the credential's id; safe to log.
	Principal string
	// BreakGlass is asserted when the credential was minted (M7).
	BreakGlass bool
	// CorrelationID is the id the request is logged under.
	CorrelationID string
}

// hostRequest is what a host route's request carries beside the caller: the
// codes its server declared and the logger a refused error is written to.
type hostRequest struct {
	caller   HostCaller
	provider string
	codes    map[string]bool
	log      *slog.Logger
}

// HostCallerFrom returns the caller of the host route serving ctx's request.
// ok is false outside a host route.
func HostCallerFrom(ctx context.Context) (HostCaller, bool) {
	hr, ok := ctx.Value(ctxKeyHost).(*hostRequest)
	if !ok {
		return HostCaller{}, false
	}
	c := hr.caller
	c.Tenants = slices.Clone(c.Tenants)
	c.Subject.Groups = slices.Clone(c.Subject.Groups)
	return c, true
}

// WriteHostError answers a host route's request in this surface's envelope
// (M21), or refuses to.
//
// A REFUSAL IS AN OUTAGE, AND LOUD. Each of these is a host programming error,
// and each is rendered as `500 internal` with the correlation id and logged:
//
//   - a call outside a host route;
//   - a code the host did not declare and Control does not define;
//   - a status outside 4xx and 5xx;
//   - a 401, or the code `unauthenticated`: on this surface both are the
//     middleware's alone (M11);
//   - the code `internal` with a status that is not 5xx: it always means an
//     outage, never a refusal (M11);
//   - an empty message: every error carries an English one (M21).
func WriteHostError(w http.ResponseWriter, r *http.Request, status int, code string, params map[string]any, message string) {
	hr := hostRequestFrom(r)
	if problem := hr.refusal(status, code, message); problem != "" {
		hr.refuse(w, r, "a host route answered an error Control does not allow", "northbound_host_error_refused", problem,
			"status", status, "code", code)
		return
	}
	writeError(w, status, Error{Code: code, Message: message, Parameters: params, CorrelationID: CorrelationIDFrom(r.Context())})
}

// hostRequestFrom returns the host route serving r's request, or nil outside
// one.
func hostRequestFrom(r *http.Request) *hostRequest {
	hr, _ := r.Context().Value(ctxKeyHost).(*hostRequest)
	return hr
}

// refuse answers a host programming error as `500 internal` with the
// correlation id, and logs it under event with the problem and attrs. hr may
// be nil: a call outside a host route is logged to the default logger.
func (hr *hostRequest) refuse(w http.ResponseWriter, r *http.Request, msg, event, problem string, attrs ...any) {
	ctx := r.Context()
	id := CorrelationIDFrom(ctx)
	log, provider := slog.Default(), ""
	if hr != nil {
		log, provider = hr.log, hr.provider
	}
	args := append([]any{"event", event, "provider", provider, "problem", problem}, attrs...)
	args = append(args, "path", r.URL.Path, "correlation_id", id)
	log.ErrorContext(ctx, msg, args...)
	writeError(w, http.StatusInternalServerError, Error{
		Code: CodeInternal, CorrelationID: id,
		Message: "this server could not complete the request; quote the correlation id",
	})
}

// refusal says why an error may not be written, or "" when it may.
func (hr *hostRequest) refusal(status int, code, message string) string {
	switch {
	case hr == nil:
		return "WriteError was called outside a host route"
	case status < 400 || status > 599:
		return "the status is not an error status"
	case status == http.StatusUnauthorized || code == CodeUnauthenticated:
		return "a 401 on this surface is the middleware's alone"
	case code == CodeInternal && status < 500:
		return "internal is always a 5xx"
	case !hr.codes[code]:
		return "the code was not declared"
	case strings.TrimSpace(message) == "":
		return "the error has no message"
	}
	return ""
}

// snakeCase is the shape of every code on this surface.
var snakeCase = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// declaredCodes is Control's codes and a host's, refusing a host code that is
// malformed, repeated, or already Control's.
//
// The set is per SERVER rather than a change to AllCodes: AllCodes is what
// Control defines, and a process that built two servers — every test binary
// here does — must not have one's host codes leak into the other's.
func declaredCodes(provider string, host []string) (map[string]bool, error) {
	codes := make(map[string]bool, len(AllCodes)+len(host))
	for _, c := range AllCodes {
		codes[c] = true
	}
	for _, c := range host {
		switch {
		case !snakeCase.MatchString(c):
			return nil, fmt.Errorf("httpapi/north: %s declares error code %q, which is not snake_case", provider, c)
		case slices.Contains(AllCodes, c):
			return nil, fmt.Errorf("httpapi/north: %s declares error code %q, which is Control's own", provider, c)
		case codes[c]:
			return nil, fmt.Errorf("httpapi/north: %s declares error code %q twice", provider, c)
		}
		codes[c] = true
	}
	return codes, nil
}

// controlPaths is Control's own table, mounted on a mux of its own so that a
// host route is judged against Control's routes and never against another of
// the host's.
type controlPaths struct {
	patterns map[string]bool
	mux      *http.ServeMux
}

func newControlPaths() *controlPaths {
	return &controlPaths{patterns: map[string]bool{}, mux: http.NewServeMux()}
}

// add records one of Control's patterns. The router has already mounted it,
// so it mounts here too.
func (c *controlPaths) add(pattern string) {
	if c.patterns[pattern] {
		return
	}
	c.patterns[pattern] = true
	c.mux.Handle(pattern, http.NotFoundHandler())
}

// hostSampleSegment stands in for a host pattern's wildcard when asking which
// of Control's patterns would serve it: a value no Control route names.
const hostSampleSegment = "hoplock-host-sample"

// servedBy returns the Control pattern that serves a host pattern's paths, or
// "". That is the pattern itself, or one of Control's wildcards that a literal
// host path falls under — `targets/special` beside Control's
// `targets/{target}` — which the mux would otherwise let the host's more
// specific pattern take from Control.
func (c *controlPaths) servedBy(pattern string) string {
	if c.patterns[pattern] {
		return pattern
	}
	segments := strings.Split(pattern, "/")
	for i, segment := range segments {
		switch {
		case segment == "{$}":
			segments[i] = ""
		case strings.HasPrefix(segment, "{"):
			segments[i] = hostSampleSegment
		}
	}
	_, matched := c.mux.Handler(&http.Request{Method: http.MethodGet, URL: &url.URL{Path: strings.Join(segments, "/")}})
	if c.patterns[matched] {
		return matched
	}
	return ""
}

// hostRoute turns a host's route into a table entry, refusing one Control
// will not mount. Control's own patterns are passed in because a host may not
// take a path Control serves: neither by adding a method to it — the 405 and
// the listing would then describe a path that is half Control's — nor by a
// literal path under one of Control's wildcards, which would take that path
// from Control.
func (s *Server) hostRoute(provider string, hr HostRoute, control *controlPaths) (Route, error) {
	name := fmt.Sprintf("%s route %s %q", provider, hr.Method, hr.Pattern)
	switch {
	case hr.Method == "":
		return Route{}, fmt.Errorf("httpapi/north: %s has no method", name)
	case !slices.Contains(hostMethods, hr.Method):
		return Route{}, fmt.Errorf("httpapi/north: %s: the method is not one of %v", name, hostMethods)
	case hr.Handler == nil:
		return Route{}, fmt.Errorf("httpapi/north: %s has no handler", name)
	case !slices.Contains(identity.AllPermissions, hr.Permission):
		return Route{}, fmt.Errorf("httpapi/north: %s names permission %q, which Control does not define; a host names a permission and never declares one", name, hr.Permission)
	}
	var prefix string
	switch hr.Access {
	case AccessTenant:
		prefix = HostPrefix
	case AccessCrossTenant:
		prefix = CrossTenantPrefix
	case AccessAnonymous, AccessAuthenticated, AccessSelf:
		return Route{}, fmt.Errorf("httpapi/north: %s is %s; a host route is %s or %s",
			name, hr.Access, AccessTenant, AccessCrossTenant)
	default:
		return Route{}, fmt.Errorf("httpapi/north: %s has no access class Control defines; a host route is %s or %s",
			name, AccessTenant, AccessCrossTenant)
	}
	if err := checkHostPattern(prefix, hr.Pattern); err != nil {
		return Route{}, fmt.Errorf("httpapi/north: %s: %w", name, err)
	}
	pattern := prefix + hr.Pattern
	switch served := control.servedBy(pattern); {
	case served == pattern:
		return Route{}, fmt.Errorf("httpapi/north: %s collides with Control's route at %s", name, pattern)
	case served != "":
		return Route{}, fmt.Errorf("httpapi/north: %s would take paths Control serves at %s", name, served)
	}

	handler := hr.Handler
	return Route{
		Method:     hr.Method,
		Pattern:    pattern,
		Access:     hr.Access,
		Permission: hr.Permission,
		Summary:    hr.Summary,
		Provider:   provider,
		Handler: func(w http.ResponseWriter, r *http.Request) {
			handler.ServeHTTP(w, r.WithContext(s.withHostRequest(r, provider)))
		},
	}, nil
}

// checkHostPattern refuses a pattern that is not a plain path under its class's
// prefix.
func checkHostPattern(prefix, pattern string) error {
	switch {
	case pattern == "":
		return fmt.Errorf("the pattern is empty")
	case strings.HasPrefix(pattern, "/"):
		return fmt.Errorf("the pattern is relative to %s and may not start with /", prefix)
	case strings.ContainsAny(pattern, " \t"):
		return fmt.Errorf("the pattern carries a method prefix or a space; the method is a field of its own")
	case slices.Contains(strings.Split(pattern, "/"), ".."):
		return fmt.Errorf("the pattern climbs out of %s with a .. segment", prefix)
	case namesTenantParam(pattern):
		return fmt.Errorf("the pattern names {%s}; Control resolves the tenant, or the set, before the handler runs", TenantPathParam)
	}
	return nil
}

// withHostRequest builds the caller projection for a request the middleware
// has already authenticated, resolved and checked.
func (s *Server) withHostRequest(r *http.Request, provider string) context.Context {
	ctx := r.Context()
	caller := HostCaller{CorrelationID: CorrelationIDFrom(ctx)}
	if t, ok := TenantFrom(ctx); ok {
		caller.Tenant = ext.Tenant(t)
	}
	if ts, ok := TenantsFrom(ctx); ok {
		caller.Tenants = make([]ext.Tenant, 0, len(ts))
		for _, t := range ts {
			caller.Tenants = append(caller.Tenants, ext.Tenant(t))
		}
	}
	if p, ok := PrincipalFrom(ctx); ok {
		caller.Principal = p.ID
		caller.BreakGlass = p.BreakGlass
		if p.Subject != "" {
			caller.Subject = ext.Subject{ID: p.Subject, Groups: slices.Clone(p.Groups)}
		}
	}
	return context.WithValue(ctx, ctxKeyHost, &hostRequest{
		caller: caller, provider: provider, codes: s.codes, log: s.log,
	})
}

// registerTable registers Control's routes, then the host's, through the one
// enforcement point.
func (s *Server) registerTable(provider string, host []HostRoute) error {
	control := newControlPaths()
	for _, route := range s.routes() {
		if err := s.router.Register(route); err != nil {
			return err
		}
		control.add(route.Pattern)
	}
	for _, hr := range host {
		route, err := s.hostRoute(provider, hr, control)
		if err != nil {
			return err
		}
		if err := s.router.Register(route); err != nil {
			return fmt.Errorf("%w (registered by %s)", err, provider)
		}
	}
	return nil
}

// CheckHost refuses a host's routes and error codes without serving anything,
// so a host binary that can never start says so before any database is opened
// or any port is bound.
func CheckHost(provider string, routes []HostRoute, codes []string) error {
	_, err := Table(provider, routes, codes)
	return err
}

// Table returns the route listing a server built with these host routes would
// serve — Control's routes and the host's — by performing the same
// registration [New] performs, against a table nothing will serve.
//
// It is what lets the south-bound listener's tests enumerate this surface, host
// routes included, without standing up the services behind it (M2).
func Table(provider string, routes []HostRoute, codes []string) ([]RouteInfo, error) {
	if (len(routes) > 0 || len(codes) > 0) && provider == "" {
		return nil, errNoHostProvider
	}
	declared, err := declaredCodes(provider, codes)
	if err != nil {
		return nil, err
	}
	s := &Server{codes: declared, log: slog.Default()}
	s.router = NewRouter(func(Route) http.Handler { return http.NotFoundHandler() })
	if err := s.registerTable(provider, routes); err != nil {
		return nil, err
	}
	return s.router.Routes(), nil
}

// errNoHostProvider refuses host routes nobody can attribute.
var errNoHostProvider = errors.New("httpapi/north: host routes and error codes need a provider to name in the route listing")
