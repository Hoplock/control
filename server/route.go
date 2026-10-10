// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server

import "net/http"

// Route is a host's north-bound route.
//
// Control mounts it on the north-bound listener only (M2), at
// /api/v1/tenants/{tenant}/<Pattern>, as a tenant route: before Handler runs,
// Control authenticates the caller, resolves exactly one tenant from the
// caller's own scope (M18) and checks Permission in that tenant. A caller with
// no credential is answered 401, one without the permission 403, and one naming
// a tenant outside its scope 403 — and none of them reaches Handler.
//
// There is no host route outside a tenant and no cross-tenant one: that needs
// an access class this surface does not have, which is a decision of its own.
//
// Start-up fails, naming the route, on: an empty method or one other than GET,
// POST, PUT, PATCH or DELETE; a Pattern that starts with "/", names
// "{tenant}", climbs with "..", or carries a method prefix; a Permission that
// is not one of Control's; a nil Handler; a duplicate of any route, Control's
// or a host's; a Pattern on a path Control already serves; and a Pattern the
// standard library's mux reports as conflicting with another.
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
	// Summary is one line for the route listing and the start-up log.
	Summary string
	// Handler serves the request. [CallerFrom] says who is calling, and
	// [WriteError] answers a failure. Control's panic recovery, access log,
	// body limit and request deadline apply to it exactly as to Control's
	// own handlers.
	Handler http.Handler
}
