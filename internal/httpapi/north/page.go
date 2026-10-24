// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/store"
)

// Bounded lists (M26). Every list this surface answers reads `?limit=` and
// `?after=` through readPage and writes its page through answerPage, so the
// limit's rule, the cursor's encoding and the answer's shape live here and
// nowhere else. Beneath them the store reads the page (store.Page).
//
// A cursor is a position, not a scope. It binds the list, its filters and its
// order, so one minted for another of them is refused rather than misread, and
// it never carries the tenant or a self route's subject (M18): both are the
// middleware's, and the reader filters by them on every page. It carries no
// MAC, because a cursor minted on one node is continued on another (M5) and a
// forged position reads only a list the caller may read whole.

// keyKind is the kind of one component of a list's order key.
type keyKind int

const (
	keyString keyKind = iota
	keyInt
	keyTime
	keyBool
)

// list names one north-bound list and its order key (M26).
type list struct {
	name string    // the cursor's "l": unique among Control's lists
	key  []keyKind // the order key's components, in order
}

// pageRequest is one page asked of a list: the store's Page, and what the
// cursor continuing it must bind.
type pageRequest struct {
	store.Page
	list    list
	filters string // the digest of the canonical filters ("f")
}

// cursorVersion is the cursor encoding's version. A change to the encoding
// bumps it, which refuses every older cursor loudly rather than misreading
// it (M26.2).
const cursorVersion = 1

// cursorTime is how a time component is written: RFC 3339 in UTC, with every
// digit of the nanoseconds, so a position is exact whatever the column holds.
const cursorTime = "2006-01-02T15:04:05.000000000Z07:00"

// cursor is a cursor's decoded form. Nothing in it is hidden from the caller,
// and nothing in it was not handed to them.
type cursor struct {
	V int      `json:"v"`
	L string   `json:"l"`
	F string   `json:"f"`
	K []string `json:"k"`
}

// filterDigest is a list's canonical filters, bound into its cursor: the hex
// of the first 16 bytes of SHA-256 over the filters, sorted, joined by \x1f.
func filterDigest(filters []string) string {
	sorted := slices.Sorted(slices.Values(filters))
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\x1f")))
	return hex.EncodeToString(sum[:16])
}

// encodeCursor mints the cursor that continues l after the item whose sort key
// is last. A component of the wrong type is this server's bug, not the
// caller's, and fails rather than minting a cursor no page would accept.
func encodeCursor(l list, filters string, last []any) (string, error) {
	if len(last) != len(l.key) {
		return "", fmt.Errorf("httpapi/north: list %s: cursor key has %d components, want %d", l.name, len(last), len(l.key))
	}
	c := cursor{V: cursorVersion, L: l.name, F: filters, K: make([]string, len(last))}
	for i, v := range last {
		s, ok := encodeComponent(l.key[i], v)
		if !ok {
			return "", fmt.Errorf("httpapi/north: list %s: cursor key component %d is %T, not its kind", l.name, i, v)
		}
		c.K[i] = s
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("httpapi/north: list %s: %w", l.name, err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func encodeComponent(kind keyKind, v any) (string, bool) {
	switch kind {
	case keyString:
		s, ok := v.(string)
		return s, ok
	case keyInt:
		n, ok := v.(int64)
		return strconv.FormatInt(n, 10), ok
	case keyTime:
		t, ok := v.(time.Time)
		return t.UTC().Format(cursorTime), ok
	case keyBool:
		b, ok := v.(bool)
		return strconv.FormatBool(b), ok
	}
	return "", false
}

// errCursor is every reason a cursor is not continued. Which one it was is
// not the caller's to act on: they drop the cursor and start again (M26.2).
var errCursor = errors.New("httpapi/north: not a cursor this list continues")

// decodeCursor reads raw as a position in l under filters, typed as the
// store's Page takes it. Anything else — another encoding, another list,
// other filters, another key — is errCursor.
func decodeCursor(raw string, l list, filters string) ([]any, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, errCursor
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c cursor
	if err := dec.Decode(&c); err != nil || dec.More() {
		return nil, errCursor
	}
	if c.V != cursorVersion || c.L != l.name || c.F != filters || len(c.K) != len(l.key) {
		return nil, errCursor
	}
	out := make([]any, len(c.K))
	for i, s := range c.K {
		v, ok := decodeComponent(l.key[i], s)
		if !ok {
			return nil, errCursor
		}
		out[i] = v
	}
	return out, nil
}

func decodeComponent(kind keyKind, s string) (any, bool) {
	switch kind {
	case keyString:
		return s, true
	case keyInt:
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil
	case keyTime:
		t, err := time.Parse(time.RFC3339Nano, s)
		return t.UTC(), err == nil
	case keyBool:
		switch s {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return nil, false
}

// readPage reads ?limit= and ?after= for one list under its canonical
// filters ("name=value", sorted here). On a refusal it has written the 400
// and answers false.
func readPage(w http.ResponseWriter, r *http.Request, l list, filters ...string) (pageRequest, bool) {
	q := r.URL.Query()
	p := pageRequest{list: l, filters: filterDigest(filters)}

	// The limit is the caller's, within one maximum, and never clamped
	// (M26.1): nothing answers shorter than asked without saying so.
	p.Limit = store.DefaultPageLimit
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		switch {
		case (err == nil && n > store.MaxPageLimit) || (errors.Is(err, strconv.ErrRange) && n > 0):
			invalid(w, r, "limit", access.ProblemExceedsMaximum, strconv.Itoa(store.MaxPageLimit))
			return pageRequest{}, false
		case err != nil || n <= 0:
			invalid(w, r, "limit", access.ProblemInvalid, "")
			return pageRequest{}, false
		}
		p.Limit = n
	}

	if raw := q.Get("after"); raw != "" {
		after, err := decodeCursor(raw, l, p.filters)
		if err != nil {
			invalid(w, r, "after", access.ProblemInvalid, "")
			return pageRequest{}, false
		}
		p.After = after
	}
	return p, true
}

// answerPage writes one page: member → items (never null), "truncated" →
// more, "next" → the cursor after last when more, and every extra member
// unchanged.
//
// The extra members describe the list rather than the page, so they are the
// same on every page (M26.3); the paging members are written after them and
// cannot be overwritten by one.
func (s *Server) answerPage(w http.ResponseWriter, r *http.Request, p pageRequest, member string, items any, more bool, last []any, extra map[string]any) {
	body, err := pageBody(p, member, items, more, last, extra)
	if err != nil {
		s.fail(w, r, "mint "+p.list.name+" cursor", err)
		return
	}
	answer(w, http.StatusOK, body)
}

// pageBody is one page's answer, built before anything is written, so a
// cursor that cannot be minted is an outage and never a page without it.
func pageBody(p pageRequest, member string, items any, more bool, last []any, extra map[string]any) (map[string]any, error) {
	body := make(map[string]any, len(extra)+3)
	maps.Copy(body, extra)
	if v := reflect.ValueOf(items); !v.IsValid() || (v.Kind() == reflect.Slice && v.IsNil()) {
		items = []any{}
	}
	body[member] = items
	body["truncated"] = more
	if more {
		next, err := encodeCursor(p.list, p.filters, last)
		if err != nil {
			return nil, err
		}
		body["next"] = next
	}
	return body, nil
}

// A HOST'S LISTS TAKE THE SAME SHAPE (M26.7, M15).
//
// server.ReadList and server.List.Write forward to the pair below, which reads
// and writes through readPage and pageBody: one codec, one limit rule, one set
// of refusals, for Control's lists and a host's alike. A host's list is named
// "<provider>:<name>" in its cursor ("l"). None of Control's list names has a
// ":", so a cursor from one of Control's lists is never continued by a host's,
// nor the other way round. Every component of a host's order key is a string:
// the host parses it and owns what it means.
//
// Control cannot see which of a host's routes is a list, so nothing makes one
// answer through this pair. What this pair does enforce, it enforces as
// WriteHostError does: a host programming error is `500 internal`, logged,
// and never a page in another shape.

// HostPage is one page a caller asked of a host's list.
type HostPage struct {
	// Limit is 1 to store.MaxPageLimit; store.DefaultPageLimit when the
	// caller named none.
	Limit int
	// After is the order key of the last item the caller was handed, one
	// string per component, or nil on the first page.
	After []string

	req pageRequest
}

// hostListEvent is the event a host list's programming error is logged under.
const hostListEvent = "northbound_host_list_refused"

// hostList is a host's list as a cursor names it.
func hostList(provider, name string, keyLen int) list {
	return list{name: provider + ":" + name, key: slices.Repeat([]keyKind{keyString}, keyLen)}
}

// ReadHostPage reads ?limit= and ?after= for the host list name, whose order
// key has keyLen components, under the request's filters ("name=value"). On a
// bad limit or cursor it has written Control's 400 and answers false; outside
// a host route, or for an empty name or a key of no components, it has
// written `500 internal` and logged it.
func ReadHostPage(w http.ResponseWriter, r *http.Request, name string, keyLen int, filters ...string) (HostPage, bool) {
	hr := hostRequestFrom(r)
	problem := ""
	switch {
	case hr == nil:
		problem = "ReadList was called outside a host route"
	case name == "":
		problem = "the list has no name"
	case keyLen < 1:
		problem = "the list's order key has no components"
	}
	if problem != "" {
		hr.refuse(w, r, "a host list was read in a way Control does not allow", hostListEvent, problem,
			"list", name, "key_components", keyLen)
		return HostPage{}, false
	}
	p, ok := readPage(w, r, hostList(hr.provider, name, keyLen), filters...)
	if !ok {
		return HostPage{}, false
	}
	page := HostPage{Limit: p.Limit, req: p}
	if p.After != nil {
		page.After = make([]string, len(p.After))
		for i, v := range p.After {
			page.After[i] = v.(string) // every component of a host's key is keyString
		}
	}
	return page, true
}

// WriteHostPage answers one page of a host's list in M26's shape, or refuses
// to. Each of these is a host programming error, answered as `500 internal`
// and logged:
//
//   - a call outside a host route, or with a page ReadHostPage did not read;
//   - a member that is empty, "truncated" or "next";
//   - an extra member named as the list's member, "truncated" or "next";
//   - items that do not encode as a JSON array;
//   - more with a nil last, or a last whose length is not the key's.
func WriteHostPage(w http.ResponseWriter, r *http.Request, p HostPage, member string, items any, more bool, last []string, extra map[string]any) {
	hr := hostRequestFrom(r)
	problem := hostPageRefusal(hr, p, member, items, more, last, extra)
	var body map[string]any
	if problem == "" {
		key := make([]any, len(last))
		for i, v := range last {
			key[i] = v
		}
		var err error
		if body, err = pageBody(p.req, member, items, more, key, extra); err != nil {
			problem = err.Error()
		}
	}
	if problem != "" {
		hr.refuse(w, r, "a host list answered a page Control does not allow", hostListEvent, problem,
			"list", p.req.list.name, "member", member)
		return
	}
	answer(w, http.StatusOK, body)
}

// hostPageRefusal says why a host's page may not be written, or "" when it
// may.
func hostPageRefusal(hr *hostRequest, p HostPage, member string, items any, more bool, last []string, extra map[string]any) string {
	reserved := func(name string) bool { return name == "truncated" || name == "next" }
	switch {
	case hr == nil:
		return "List.Write was called outside a host route"
	case p.req.list.name == "":
		return "the List was not read by ReadList"
	case member == "" || reserved(member):
		return "the member is empty, truncated or next"
	case more && last == nil:
		return "the page continues and names no last item"
	case more && len(last) != len(p.req.list.key):
		return "the last item's key has the wrong number of components"
	}
	for k := range extra {
		if k == member || reserved(k) {
			return "an extra member collides with " + k
		}
	}
	if v := reflect.ValueOf(items); v.IsValid() {
		switch {
		case v.Kind() != reflect.Slice && v.Kind() != reflect.Array:
			return "the items are not a list"
		case v.Type().Elem().Kind() == reflect.Uint8:
			return "the items are bytes, which encode as a string"
		}
	}
	return ""
}
