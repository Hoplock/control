// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"

	"github.com/hoplock/control/internal/httpapi/north"
)

// DefaultListLimit and MaxListLimit are the bounds of every north-bound list,
// Control's and a host's (M26).
const (
	DefaultListLimit = 500
	MaxListLimit     = 10000
)

// List is one page a caller asked of a host's list: how many items it may
// hold, and the position it continues from.
//
// A host's list answers as Control's lists do, so one client reads both
// (M26):
//
//	GET …?limit=100&after=<cursor>
//	{"requests": [...], "truncated": true, "next": "<cursor>"}
//
// The host reads the request with [ReadList], fetches up to Limit items after
// After in its list's order, plus one more to learn whether the list goes on,
// and answers with [List.Write].
//
// The cursor is opaque to the caller and minted by Control. It binds the
// list's name, the host's provider and the request's filters, so a cursor from
// one of Control's lists, from another of the host's or under other filters
// is refused rather than misread. It never carries the tenant, nor on a
// cross-tenant read the set of tenants (M18, M24): both are Control's
// middleware's, resolved from the caller on every request, and the host
// filters by [Caller] on every page. A cursor is a position, not a scope.
type List struct {
	// Limit is 1 to MaxListLimit; DefaultListLimit when the caller named none.
	Limit int
	// After is the order key of the last item the caller was handed, one
	// string per component, exactly as the host passed it to Write. Nil
	// starts the list. The host parses it and owns what each component means.
	After []string

	page north.HostPage
}

// ReadList reads ?limit= and ?after= for one of a host's lists.
//
// name names the list among the host's own; Control prefixes it with the
// host's provider, so a cursor from one of Control's lists or another
// host's is refused. filters are the request's own filter values as
// "name=value", which the cursor binds: every query parameter that changes
// what the list holds or its order belongs here, and nothing Control
// resolves — never the tenant or the set. keyLen is the number of components
// in the list's order key.
//
// On a bad limit or cursor it writes Control's 400 refusal and answers
// false: `400 invalid_request` with `{field: limit, problem: invalid}`,
// `{field: limit, problem: exceeds_maximum, limit: "10000"}` above
// MaxListLimit — refused, never clamped — or `{field: after, problem:
// invalid}`. A client that is refused a cursor drops it and starts again.
//
// IT FAILS CLOSED, as [WriteError] does. Outside a host route, with an empty
// name or with a keyLen below 1, it answers `500 internal` with the
// correlation id and logs it: each is a host programming error.
func ReadList(w http.ResponseWriter, r *http.Request, name string, keyLen int, filters ...string) (List, bool) {
	p, ok := north.ReadHostPage(w, r, name, keyLen, filters...)
	if !ok {
		return List{}, false
	}
	return List{Limit: p.Limit, After: p.After, page: p}, true
}

// Write answers one page in M26's shape:
// {member: items, "truncated": more, "next": <cursor after last>, extra...}.
// last is read only when more is true. items must encode as a JSON array;
// nil is written as [].
//
// more is true when the list holds an item after this page: read Limit+1
// items and answer the first Limit of them. last is then the order key of
// the page's last item, as After will hand it back. "next" is present
// exactly when more is true. extra holds members that describe the list
// rather than the page, the same on every page.
//
// IT FAILS CLOSED, as [WriteError] does. Each of these is a host programming
// error, answered as `500 internal` with the correlation id and logged, and
// never as a page in another shape:
//
//   - a call outside a host route, or on a List that ReadList did not return;
//   - a member that is empty, "truncated" or "next";
//   - an extra key that is the member, "truncated" or "next";
//   - items that do not encode as a JSON array;
//   - more with a nil last, or a last whose length is not ReadList's keyLen.
func (l List) Write(w http.ResponseWriter, r *http.Request, member string, items any, more bool, last []string, extra map[string]any) {
	north.WriteHostPage(w, r, l.page, member, items, more, last, extra)
}
