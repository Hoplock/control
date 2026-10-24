// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server

import "net/http"

// Access is a host route's class: what Control resolves before Handler runs.
// Closed set; the zero value is AccessTenant, so a Route written before Access
// existed is unchanged.
type Access int

const (
	// AccessTenant mounts the route at /api/v1/tenants/{tenant}/<Pattern>.
	// Control resolves exactly one tenant from the caller's scope and checks
	// Permission in it. Caller.Tenant is that tenant.
	AccessTenant Access = iota
	// AccessCrossTenant mounts the route at /api/v1/cross-tenant/<Pattern>.
	// Control resolves every tenant in the caller's scope where it holds
	// Permission, narrowed by the request's tenant selector and never widened,
	// and refuses a named tenant that fails rather than dropping it.
	// Caller.Tenants is that set. Permission must be a read: one the auditor
	// role holds. Control serves no route of this class of its own (M24).
	AccessCrossTenant
)

// Route is a host's north-bound route.
//
// Control mounts it on the north-bound listener only (M2), under the prefix its
// Access names, and before Handler runs it authenticates the caller and
// resolves what the request covers from the caller's own scope (M18): on an
// AccessTenant route exactly one tenant, in which it checks Permission; on an
// AccessCrossTenant route the set of tenants in which the caller holds
// Permission. A caller with no credential is answered 401, one without the
// permission 403, and one naming a tenant outside its scope 403 — and none of
// them reaches Handler.
//
// There is no cross-tenant write: a write across tenants is one request making
// changes in several tenants' independent audit chains, with nothing to make
// them atomic and no answer for a partial outcome (M24).
//
// A route that answers a list is expected to read the page with [ReadList] and
// answer it with [List.Write], so that it is bounded by its caller and says
// where it stopped exactly as Control's lists do, and one client reads both
// (M26). Control cannot see which route is a list, so nothing enforces this:
// a host's own tests must.
//
// Start-up fails, naming the route, on: an Access that is neither of the two;
// an empty method or one other than GET, POST, PUT, PATCH or DELETE; a Pattern
// that starts with "/", names "{tenant}", climbs with "..", or carries a method
// prefix; a Permission that is not one of Control's, or on an AccessCrossTenant
// route one the auditor role does not hold; a nil Handler; a duplicate of any
// route, Control's or a host's; a Pattern on a path Control already serves —
// one of its patterns, or a literal under one of its wildcards, such as
// "targets/special" beside Control's "targets/{target}", which the mux would
// otherwise route to the host; and a Pattern the standard library's mux
// reports as conflicting with another.
type Route struct {
	// Method is the HTTP method; one per route.
	Method string
	// Pattern is relative, in net/http's ServeMux syntax, without the
	// method: "reports/{report}". No leading "/", no "{tenant}", no "..".
	Pattern string
	// Permission is one of Control's permission codes ("audit:read",
	// "report:write", ...). A host names a permission and never declares
	// one: the roles that grant each are fixed in Control, so what "admin"
	// means does not depend on which binary is running.
	Permission string
	// Access is the route's class. The zero value is AccessTenant.
	Access Access
	// Summary is one line for the route listing and the start-up log.
	Summary string
	// Handler serves the request. [CallerFrom] says who is calling, and
	// [WriteError] answers a failure. Control's panic recovery, access log,
	// body limit and request deadline apply to it exactly as to Control's
	// own handlers.
	Handler http.Handler
}
