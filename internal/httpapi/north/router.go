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
// tenant the request resolves to (or, on a cross-tenant read, which set: M24),
// and which permission it needs. A handler cannot forget any of it, because a
// handler is never asked.
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
// 0019 owns this surface's routes and its CLI; what is here is the credential
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
	// AccessSelf is AccessTenant narrowed to the caller (0016): a live
	// principal WITH A PERSON BEHIND IT, exactly one tenant resolved and one
	// permission checked as AccessTenant does, and then the subject resolved
	// from the principal and put on the context ([SubjectFrom]). It is for a
	// caller's own records in a tenant — a requester's own grant requests.
	//
	// "Own" is part of the route's definition, never a branch inside a
	// handler: whose record it is cannot be known until the row is read, and
	// a permission that depended on that would be one the handler decides
	// (§6). So the subject is resolved here from the credential, the way M18
	// resolves the tenant, and no path, query or body carries one. A machine
	// token has no own records and is refused (`subject_required`).
	AccessSelf
	// AccessCrossTenant is a read that spans tenants (M24), mounted under
	// [CrossTenantPrefix] and nowhere else. It needs a live principal, and
	// then resolves a SET: every tenant in the principal's scope where it
	// holds the route's permission, narrowed by the request's tenant
	// selector and never widened by it. A named tenant that fails is
	// refused rather than dropped, so a report never covers fewer tenants
	// than were asked for while looking complete. A handler reads the set
	// with [TenantsFrom] and never chooses it.
	//
	// It is reads only — a permission the auditor role holds — because a
	// write across tenants is N records in N independent audit chains (M8)
	// with nothing making them atomic, and a partial outcome is neither a
	// success nor a refusal (M11). And only a host serves one: Control's
	// own routes stay per tenant, and aggregating is Enterprise's (its E11).
	AccessCrossTenant
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
	case AccessSelf:
		return "self"
	case AccessCrossTenant:
		return "cross-tenant"
	}
	return "unknown"
}

// CrossTenantPrefix is where every AccessCrossTenant route lives, and it holds
// that class and nothing else. It sits beside /api/v1/tenants/ and
// /api/v1/session so that an operator reading a log line can tell from the path
// alone which class answered (M24).
const CrossTenantPrefix = APIPrefix + "/cross-tenant/"

// underCrossTenant reports whether a pattern is in the cross-tenant subtree,
// including the bare prefix a ServeMux would treat as that subtree's root.
func underCrossTenant(pattern string) bool {
	return strings.HasPrefix(pattern, CrossTenantPrefix) ||
		pattern == strings.TrimSuffix(CrossTenantPrefix, "/")
}

// namesTenantParam reports whether a pattern carries the tenant path
// parameter, as a single segment or as a remainder.
func namesTenantParam(pattern string) bool {
	for _, segment := range strings.Split(pattern, "/") {
		if !strings.HasPrefix(segment, "{") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}"), "...")
		if name == TenantPathParam {
			return true
		}
	}
	return false
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
	// Permission is required when Access is AccessTenant, AccessSelf or
	// AccessCrossTenant — on the last, one the auditor role holds — and must
	// be empty otherwise: a permission on an anonymous route is a permission
	// nobody checks, which reads like protection and is not.
	Permission identity.Permission
	// Summary is one line for the route listing and the startup log.
	Summary string
	// Handler serves the request. It reads the caller and the tenant — or on
	// a cross-tenant read the set — from the context and never from the
	// request.
	Handler http.HandlerFunc
	// Provider names the host binary a route came from (M15), and is empty
	// for Control's own. It travels into the listing so that an operator can
	// see which routes are not Control's.
	Provider string
}

// RouteInfo is a registered route as the listing and the tests see it.
type RouteInfo struct {
	Method     string              `json:"method"`
	Pattern    string              `json:"pattern"`
	Access     string              `json:"access"`
	Permission identity.Permission `json:"permission,omitempty"`
	Summary    string              `json:"summary,omitempty"`
	// Provider is the host binary that registered the route; empty for
	// Control's own.
	Provider string `json:"provider,omitempty"`
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
// mistakes that would otherwise ship as "it seemed to work". The cross-tenant
// prefix holds that class and nothing else, in both directions, so it stays off
// Control's paths structurally rather than by convention (M24).
func (r *Router) Register(route Route) error {
	if route.Method == "" || route.Pattern == "" {
		return fmt.Errorf("httpapi/north: a route needs a method and a pattern")
	}
	if route.Handler == nil {
		return fmt.Errorf("httpapi/north: route %s %s has no handler", route.Method, route.Pattern)
	}
	if route.Access != AccessCrossTenant && underCrossTenant(route.Pattern) {
		return fmt.Errorf(
			"httpapi/north: route %s %s is %s but sits under %s, which holds cross-tenant reads and nothing else",
			route.Method, route.Pattern, route.Access, CrossTenantPrefix)
	}
	switch route.Access {
	case AccessCrossTenant:
		if err := checkCrossTenant(route); err != nil {
			return err
		}
	case AccessTenant, AccessSelf:
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
		if err := r.handle(route.Pattern); err != nil {
			return fmt.Errorf("httpapi/north: route %s %s: %w", route.Method, route.Pattern, err)
		}
		byMethod = map[string]http.Handler{}
		r.patterns[route.Pattern] = byMethod
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
		Provider:   route.Provider,
	})
	return nil
}

// checkCrossTenant refuses a cross-tenant route that is not a host's read under
// the cross-tenant prefix (M24).
//
// "A read" is a permission the auditor role holds, rather than a list of its
// own: the auditor is defined as "can read everything and change nothing", and a
// second list would be a second definition of "read" that could drift from it.
func checkCrossTenant(route Route) error {
	name := route.Method + " " + route.Pattern
	switch {
	case route.Permission == "":
		return fmt.Errorf(
			"httpapi/north: route %s is cross-tenant but names no permission; an unchecked route is not a protected one",
			name)
	case !identity.RoleAuditor.Can(route.Permission):
		return fmt.Errorf(
			"httpapi/north: route %s is cross-tenant but names permission %q, which is not a read: a cross-tenant route names a permission the %s role holds",
			name, route.Permission, identity.RoleAuditor)
	case route.Provider == "":
		return fmt.Errorf(
			"httpapi/north: route %s is cross-tenant and is Control's own; Control serves no cross-tenant route, only a host does (M24)",
			name)
	case !strings.HasPrefix(route.Pattern, CrossTenantPrefix) || len(route.Pattern) == len(CrossTenantPrefix):
		return fmt.Errorf("httpapi/north: route %s is cross-tenant but is not under %s", name, CrossTenantPrefix)
	case namesTenantParam(route.Pattern):
		return fmt.Errorf(
			"httpapi/north: route %s is cross-tenant but names {%s}; the set it covers is resolved, never a path segment",
			name, TenantPathParam)
	}
	return nil
}

// handle mounts a pattern on the mux, turning the mux's refusal into an error.
//
// `http.ServeMux.Handle` PANICS on a pattern that conflicts with one already
// mounted — `grants/{id}` beside `grants/{grant}`, or two wildcards that both
// match one path with neither more specific. For Control's own table that is a
// programming error caught by every test; for a host's routes it is input this
// server was handed, and a host must never be able to crash start-up with it.
func (r *Router) handle(pattern string) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("the pattern cannot be mounted: %v", rec)
		}
	}()
	r.mux.Handle(pattern, r.dispatch(pattern))
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
