// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/hoplock/control/internal/identity"
)

// THE ROUTE TABLE IS THE ENFORCEMENT POINT (M18, M2).
//
// Every north-bound route is registered here with an ACCESS CLASS, and the
// class — not the handler — decides whether the caller is authenticated, which
// tenant the request resolves to, and which permission it needs. A handler
// cannot forget any of it, because a handler is never asked.
//
// That shape exists because of how this fails otherwise. M18's vulnerability
// class is "a caller names a tenant it was not granted", and the way it ships is
// not a wrong decision in the middleware — it is ONE HANDLER that reads the
// tenant from the path itself. So there is nowhere for a handler to read a
// tenant from except the context the middleware put it in, and
// `TestEveryRouteRefusesATenantOutsideScope` drives every registered route
// rather than a sample, enumerating them from this table. A route added in a
// later phase without isolation fails that test on the day it is added.
//
// 0014 owns this surface's routes and its CLI; what is here is the credential
// model, the middleware, and the routes this phase itself serves.

// Access classifies what a route requires of its caller. Closed set (M13).
type Access int

const (
	// AccessAnonymous is a route that runs before authentication: the login
	// endpoints, and the SAML metadata an IdP fetches. It is spelled out on
	// each such route rather than inferred from a path prefix, so that an
	// unauthenticated route is always a decision somebody wrote down.
	AccessAnonymous Access = iota
	// AccessAuthenticated needs a live principal and nothing else. It is for
	// routes about the CALLER rather than about a tenant's data — reading
	// and ending one's own session.
	AccessAuthenticated
	// AccessTenant needs a live principal, resolves exactly one tenant from
	// that principal's scope, and checks one permission in that tenant. It
	// is the class almost every route belongs to.
	AccessTenant
)

// String renders the class for logs and for the route listing.
func (a Access) String() string {
	switch a {
	case AccessAnonymous:
		return "anonymous"
	case AccessAuthenticated:
		return "authenticated"
	case AccessTenant:
		return "tenant"
	}
	return "unknown"
}

// TenantPathParam is the path segment a route may carry to select a tenant. A
// route without it selects through the header, the query, or the principal's
// sole tenant — which is what keeps single-tenant deployments untouched (M18).
const TenantPathParam = "tenant"

// TenantHeader lets a caller select a tenant without a path parameter.
const TenantHeader = "X-Hoplock-Tenant"

// TenantQueryParam is the third way, for a browser that cannot set a header.
const TenantQueryParam = "tenant"

// Route is one registered north-bound route.
type Route struct {
	// Method is the HTTP method. One method per route: a handler switching
	// on the method is a handler where GET and DELETE share a permission.
	Method string
	// Pattern is the path, in net/http's ServeMux syntax and WITHOUT the
	// method — the router prepends it.
	Pattern string
	// Access is the class. See the constants.
	Access Access
	// Permission is required when Access is AccessTenant, and must be empty
	// otherwise: a permission on an anonymous route is a permission nobody
	// checks, which reads like protection and is not.
	Permission identity.Permission
	// Summary is one line for the route listing and the startup log.
	Summary string
	// Handler serves the request. It reads the caller and the tenant from
	// the context and never from the request.
	Handler http.HandlerFunc
}

// RouteInfo is a registered route as the listing and the tests see it.
type RouteInfo struct {
	Method     string              `json:"method"`
	Pattern    string              `json:"pattern"`
	Access     string              `json:"access"`
	Permission identity.Permission `json:"permission,omitempty"`
	Summary    string              `json:"summary,omitempty"`
}

// String renders a route the way the startup log lists it.
func (r RouteInfo) String() string { return r.Method + " " + r.Pattern }

// Router is the north-bound handler tree.
//
// It dispatches by PATTERN FIRST and then by method, rather than registering
// `net/http`'s "METHOD /path" patterns. The reason is M21: every error this
// surface returns carries a code, typed parameters, an English message and the
// correlation id, and `http.ServeMux`'s own 404 and 405 carry a plain-text
// sentence instead. Owning the dispatch is what lets every answer — including
// "wrong verb" and "no such path" — come out of one envelope.
type Router struct {
	mux      *http.ServeMux
	patterns map[string]map[string]http.Handler
	routes   []RouteInfo
	wrap     func(Route) http.Handler
	notFound http.Handler
}

// NewRouter builds a router whose wrap function applies the access class.
//
// The wrap function is supplied by the server rather than built in, so that the
// router can be unit-tested with a wrap that records what it was asked to
// enforce — and so there is exactly one implementation of the enforcement, in
// middleware.go.
func NewRouter(wrap func(Route) http.Handler) *Router {
	r := &Router{
		mux:      http.NewServeMux(),
		patterns: map[string]map[string]http.Handler{},
		wrap:     wrap,
	}
	return r
}

// SetNotFound sets what answers a path no route serves.
func (r *Router) SetNotFound(h http.Handler) {
	r.notFound = h
	r.mux.Handle("/", h)
}

// Register adds a route, refusing one that is not fully classified.
//
// The refusals are the point. A route with no access class, a tenant route with
// no permission, and an anonymous route carrying one are all configuration
// mistakes that would otherwise ship as "it seemed to work".
func (r *Router) Register(route Route) error {
	if route.Method == "" || route.Pattern == "" {
		return fmt.Errorf("httpapi/north: a route needs a method and a pattern")
	}
	if route.Handler == nil {
		return fmt.Errorf("httpapi/north: route %s %s has no handler", route.Method, route.Pattern)
	}
	switch route.Access {
	case AccessTenant:
		if route.Permission == "" {
			return fmt.Errorf(
				"httpapi/north: route %s %s resolves a tenant but names no permission; an unchecked route is not a protected one",
				route.Method, route.Pattern)
		}
		if !slices.Contains(identity.AllPermissions, route.Permission) {
			return fmt.Errorf(
				"httpapi/north: route %s %s names permission %q, which this build does not define",
				route.Method, route.Pattern, route.Permission)
		}
	case AccessAnonymous, AccessAuthenticated:
		if route.Permission != "" {
			return fmt.Errorf(
				"httpapi/north: route %s %s is %s but names permission %q, which nothing would check",
				route.Method, route.Pattern, route.Access, route.Permission)
		}
	default:
		return fmt.Errorf("httpapi/north: route %s %s has no access class",
			route.Method, route.Pattern)
	}

	byMethod, seen := r.patterns[route.Pattern]
	if !seen {
		byMethod = map[string]http.Handler{}
		r.patterns[route.Pattern] = byMethod
		r.mux.Handle(route.Pattern, r.dispatch(route.Pattern))
	}
	if _, dup := byMethod[route.Method]; dup {
		return fmt.Errorf("httpapi/north: route %s %s is registered twice", route.Method, route.Pattern)
	}
	byMethod[route.Method] = r.wrap(route)

	r.routes = append(r.routes, RouteInfo{
		Method:     route.Method,
		Pattern:    route.Pattern,
		Access:     route.Access.String(),
		Permission: route.Permission,
		Summary:    route.Summary,
	})
	return nil
}

// dispatch picks the handler for a pattern by method.
func (r *Router) dispatch(pattern string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		byMethod := r.patterns[pattern]
		if h, ok := byMethod[req.Method]; ok {
			h.ServeHTTP(w, req)
			return
		}
		allowed := slices.Sorted(maps.Keys(byMethod))
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeError(w, http.StatusMethodNotAllowed, Error{
			Code:          CodeMethodNotAllowed,
			Message:       "that path does not serve this method",
			Parameters:    map[string]any{"allowed": allowed},
			CorrelationID: CorrelationIDFrom(req.Context()),
		})
	})
}

// Routes returns every registered route.
//
// The isolation test enumerates from here rather than from a hand-written list,
// which is what makes it total over routes that do not exist yet.
func (r *Router) Routes() []RouteInfo { return slices.Clone(r.routes) }

// ServeHTTP dispatches.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) { r.mux.ServeHTTP(w, req) }

// SelectsTenantInPath reports whether a pattern carries the tenant selector.
func SelectsTenantInPath(pattern string) bool {
	return strings.Contains(pattern, "{"+TenantPathParam+"}")
}
