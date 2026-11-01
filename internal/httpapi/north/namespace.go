// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// THIS LISTENER IS FOUR NAMESPACES, AND NO PATH IS IN TWO OF THEM (M27).
//
//   - Control's API, under [ControlAPIPrefix] and outside [HostAPIPrefix]:
//     every route of Control's.
//   - A host's API, under [HostAPIPrefix]: every route a host declares, each
//     under its own provider ([HostAPIRoot]), with its class in the next
//     segment — tenant routes under [HostTenantPrefix], cross-tenant reads
//     under [HostCrossTenantPrefix].
//   - Control's console, under [ConsolePrefix] (0045).
//   - A host's console, under [HostConsolePrefix]: a host's screens, under
//     its own provider ([HostConsoleRoot]).
//
// Before this, a host's tenant routes sat beside Control's, under
// /api/v1/tenants/{tenant}/, and what kept the two apart was a check at
// start-up. It was wrong once (0023), and even right it let a host wildcard or
// subtree claim every path Control had not served yet, so that a later release
// either refused the host at start-up or silently took the path from it. A
// namespace makes the property hold by construction: the router refuses a
// route of Control's that can match a path under [HostAPIPrefix] and a host
// route outside its own root, and a console mount refuses a pattern outside
// its own prefix. The start-up check against Control's table stays, as
// defence in depth.
//
// The consoles are siblings rather than one nested in the other, so that a
// console's history fallback — its index for a deep link it does not know —
// is a subtree of its own prefix and nobody else's. The host's API is nested
// in /api/v1/ because the API version is one segment for the whole surface
// (M19); nothing of Control's has a catch-all there, so the nesting costs no
// fallback.

const (
	// ControlAPIPrefix holds Control's API: every route of Control's, and no
	// path under [HostAPIPrefix].
	ControlAPIPrefix = APIPrefix + "/"
	// HostAPIPrefix holds every route a host declares, each under its own
	// [HostAPIRoot].
	HostAPIPrefix = APIPrefix + "/hosts/"
	// ConsolePrefix holds Control's console: its shell, its assets, its deep
	// links and its catalogue.
	ConsolePrefix = "/console/"
	// HostConsolePrefix holds every host's screens, each under its own
	// [HostConsoleRoot].
	HostConsolePrefix = "/host-console/"
)

// hostTenantSegment and hostCrossTenantSegment name a host route's class, in
// the segment after its provider.
const (
	hostTenantSegment      = "tenants"
	hostCrossTenantSegment = "cross-tenant"
)

// HostAPIRoot is where one host's routes live.
func HostAPIRoot(provider string) string { return HostAPIPrefix + provider + "/" }

// HostTenantPrefix is where one host's AccessTenant routes live. The
// middleware resolves {tenant} exactly as it does on Control's tenant routes
// (M18).
func HostTenantPrefix(provider string) string {
	return HostAPIRoot(provider) + hostTenantSegment + "/{" + TenantPathParam + "}/"
}

// HostCrossTenantPrefix is where one host's AccessCrossTenant routes live, and
// it holds that class and nothing else (M24).
func HostCrossTenantPrefix(provider string) string {
	return HostAPIRoot(provider) + hostCrossTenantSegment + "/"
}

// HostConsoleRoot is where one host's screens live.
func HostConsoleRoot(provider string) string { return HostConsolePrefix + provider + "/" }

// providerShape is the shape of a host's provider name: <owner>/<name>, as
// "hoplock/enterprise". It is two path segments in every host prefix, always
// two, so that two providers' roots never nest: "a/b" can never sit inside
// "a/b/tenants/{tenant}/" the way a provider of any depth could.
var providerShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`)

// checkProvider refuses a provider name that cannot be two path segments.
func checkProvider(provider string) error {
	if !providerShape.MatchString(provider) {
		return fmt.Errorf("httpapi/north: provider %q is not <owner>/<name> in lower-case letters, digits and hyphens; it is two segments of every path the host serves", provider)
	}
	return nil
}

// segments splits a ServeMux pattern's path into its segments, without the
// leading "/". A trailing "/" leaves a last segment of "", which is the mux's
// subtree.
func segments(pattern string) []string {
	return strings.Split(strings.TrimPrefix(pattern, "/"), "/")
}

// prefixSegments are a namespace prefix's literal segments.
func prefixSegments(prefix string) []string {
	return strings.Split(strings.Trim(prefix, "/"), "/")
}

// inside reports whether every path a pattern can match is under prefix — the
// prefix's own root without its slash included. Every one of the prefix's
// segments must be matched by the same literal: a wildcard, a subtree or a
// {$} in their place matches paths outside it too.
func inside(pattern, prefix string) bool {
	p, n := segments(pattern), prefixSegments(prefix)
	if !strings.HasPrefix(pattern, "/") || len(p) < len(n) {
		return false
	}
	for i := range n {
		if p[i] != n[i] {
			return false
		}
	}
	return true
}

// overlaps reports whether any path a pattern can match is under prefix.
func overlaps(pattern, prefix string) bool {
	p, n := segments(pattern), prefixSegments(prefix)
	for i := range n {
		if i >= len(p) {
			return false
		}
		switch s := p[i]; {
		case s == "" && i == len(p)-1, strings.HasSuffix(s, "...}"):
			// A subtree, or a remainder: everything below.
			return true
		case s == "{$}":
			return false
		case strings.HasPrefix(s, "{"):
			// One segment, which can be the prefix's.
		case s != n[i]:
			return false
		}
	}
	return true
}

// unclean says why a pattern is one the mux would redirect away from — a "."
// or ".." segment, or an empty one before its end — or "" when it is clean.
// Such a pattern can never be served, and a "." or ".." in it would make a
// prefix check read a path the mux will not.
func unclean(pattern string) string {
	p := segments(pattern)
	for i, s := range p {
		switch {
		case s == "..":
			return "a .. segment"
		case s == ".":
			return "a . segment"
		case s == "" && i < len(p)-1:
			return "an empty segment"
		}
	}
	return ""
}

// checkNamespace refuses a route outside its namespace: one of Control's that
// can match a path outside Control's API, or a host's outside its own root or
// under the other class's subtree.
func checkNamespace(route Route) error {
	name := route.Method + " " + route.Pattern
	if route.Provider == "" {
		if !inside(route.Pattern, ControlAPIPrefix) || overlaps(route.Pattern, HostAPIPrefix) {
			return fmt.Errorf("httpapi/north: Control's route %s is outside Control's API, which is %s and nothing under %s",
				name, ControlAPIPrefix, HostAPIPrefix)
		}
		return nil
	}
	if err := checkProvider(route.Provider); err != nil {
		return err
	}
	var prefix string
	switch route.Access {
	case AccessTenant:
		prefix = HostTenantPrefix(route.Provider)
	case AccessCrossTenant:
		prefix = HostCrossTenantPrefix(route.Provider)
	case AccessAnonymous, AccessAuthenticated, AccessSelf:
		return fmt.Errorf("httpapi/north: %s route %s is %s; a host route is %s or %s",
			route.Provider, name, route.Access, AccessTenant, AccessCrossTenant)
	default:
		return fmt.Errorf("httpapi/north: %s route %s has no access class", route.Provider, name)
	}
	if !strings.HasPrefix(route.Pattern, prefix) || len(route.Pattern) == len(prefix) {
		return fmt.Errorf("httpapi/north: %s route %s is %s but is not under %s",
			route.Provider, name, route.Access, prefix)
	}
	return nil
}

// catalogueSeparator splits a host's catalogue key from its provider, as it
// splits a host's list name (M26.7). No key of Control's carries one.
const catalogueSeparator = ":"

// checkCatalogue refuses a console's catalogue keys (M21) that could collide
// with another console's: one of Control's that carries the separator, or one
// of a host's that is not its provider, the separator and a key, or that is
// one of Control's.
func checkCatalogue(provider string, keys, control []string) error {
	for _, k := range keys {
		switch {
		case k == "":
			return fmt.Errorf("an empty catalogue key")
		case provider == "" && strings.Contains(k, catalogueSeparator):
			return fmt.Errorf("catalogue key %q of Control's carries %q, which only a host's key does", k, catalogueSeparator)
		case provider == "":
		case slices.Contains(control, k):
			return fmt.Errorf("catalogue key %q is Control's", k)
		case !strings.HasPrefix(k, provider+catalogueSeparator) || k == provider+catalogueSeparator:
			return fmt.Errorf("catalogue key %q is not %s%s<key>", k, provider, catalogueSeparator)
		}
	}
	return nil
}
