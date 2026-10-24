// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/store"
)

// The paging mechanism's parts, held without a database: the cursor codec, the
// limit's rule and the answer's shape (M26). bounded_test.go holds every list
// on the router to them.

// everyKind is a list whose key has one component of each kind.
var everyKind = list{name: "every-kind", key: []keyKind{keyString, keyInt, keyTime, keyBool}}

func TestACursorRoundTripsEveryKind(t *testing.T) {
	at := time.Date(2026, 10, 24, 9, 30, 15, 123456789, time.FixedZone("CEST", 2*60*60))
	cases := map[string][]any{
		"ordinary":        {"t-1", int64(42), at, true},
		"zero values":     {"", int64(0), time.Unix(0, 0), false},
		"negative int":    {"x", int64(-7), at, false},
		"extreme int":     {"x", int64(-1 << 63), at, true},
		"separator chars": {"a\x1fb=c,d/e?f", int64(1<<63 - 1), at, true},
		"unicode":         {"ünïcødé ✓", int64(1), at, false},
	}
	digest := filterDigest([]string{"label=env=prod"})
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := encodeCursor(everyKind, digest, key)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if strings.ContainsAny(raw, "=+/") {
				t.Errorf("cursor %q is not unpadded base64url", raw)
			}
			got, err := decodeCursor(raw, everyKind, digest)
			if err != nil {
				t.Fatalf("decode %q: %v", raw, err)
			}
			if got[0] != key[0] || got[1] != key[1] || got[3] != key[3] {
				t.Errorf("decoded %v, want %v", got, key)
			}
			gotTime, ok := got[2].(time.Time)
			if !ok || !gotTime.Equal(key[2].(time.Time)) || gotTime.Location() != time.UTC {
				t.Errorf("time decoded as %v (%T), want %v in UTC", got[2], got[2], key[2])
			}
		})
	}
}

// encodeRaw builds a cursor body by hand, for the refusals no server would
// mint.
func encodeRaw(t *testing.T, body any) string {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func TestACursorThisListDoesNotContinueIsRefused(t *testing.T) {
	digest := filterDigest(nil)
	at := time.Date(2026, 10, 24, 9, 30, 0, 0, time.UTC).Format(cursorTime)
	valid := map[string]any{"v": 1, "l": everyKind.name, "f": digest, "k": []string{"s", "1", at, "true"}}
	if _, err := decodeCursor(encodeRaw(t, valid), everyKind, digest); err != nil {
		t.Fatalf("the hand-built valid cursor was refused: %v", err)
	}
	with := func(field string, v any) map[string]any {
		c := map[string]any{}
		for k, val := range valid {
			c[k] = val
		}
		c[field] = v
		return c
	}
	validRaw := encodeRaw(t, valid)

	cases := map[string]string{
		"another version":        encodeRaw(t, with("v", 2)),
		"no version":             encodeRaw(t, map[string]any{"l": everyKind.name, "f": digest, "k": valid["k"]}),
		"another list":           encodeRaw(t, with("l", "subjects")),
		"other filters":          encodeRaw(t, with("f", filterDigest([]string{"label=env=prod"}))),
		"a key too short":        encodeRaw(t, with("k", []string{"s", "1", at})),
		"a key too long":         encodeRaw(t, with("k", []string{"s", "1", at, "true", "x"})),
		"no key":                 encodeRaw(t, with("k", nil)),
		"an int that is not":     encodeRaw(t, with("k", []string{"s", "one", at, "true"})),
		"an int out of range":    encodeRaw(t, with("k", []string{"s", "9223372036854775808", at, "true"})),
		"a time that is not":     encodeRaw(t, with("k", []string{"s", "1", "yesterday", "true"})),
		"a time with no zone":    encodeRaw(t, with("k", []string{"s", "1", "2026-10-24T09:30:00", "true"})),
		"a bool that is not":     encodeRaw(t, with("k", []string{"s", "1", at, "yes"})),
		"a bool spelled as 1":    encodeRaw(t, with("k", []string{"s", "1", at, "1"})),
		"a component not text":   encodeRaw(t, with("k", []any{"s", 1, at, "true"})),
		"an unknown member":      encodeRaw(t, with("tenant", "tenant-a")),
		"bad base64":             "not a cursor!",
		"padded base64":          base64.URLEncoding.EncodeToString([]byte(`{"v":1}`)),
		"standard base64":        strings.NewReplacer("-", "+", "_", "/").Replace(validRaw) + "+/",
		"bad JSON":               base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,`)),
		"JSON that is no cursor": base64.RawURLEncoding.EncodeToString([]byte(`["v",1]`)),
		"trailing data":          trailing(t, valid),
		"empty":                  base64.RawURLEncoding.EncodeToString([]byte(``)),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := decodeCursor(raw, everyKind, digest); err == nil {
				t.Errorf("decoded %q as %v, want a refusal", raw, got)
			}
		})
	}
}

// trailing is a valid cursor body with a second JSON value after it.
func trailing(t *testing.T, body any) string {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(append(b, []byte(" {}")...))
}

// A component of the wrong type is this server's bug: it fails rather than
// minting a cursor no page would accept.
func TestACursorIsNotMintedFromAKeyOfTheWrongShape(t *testing.T) {
	for name, key := range map[string][]any{
		"too short":        {"s", int64(1), time.Now()},
		"an int not int64": {"s", 1, time.Now(), true},
		"a string not one": {1, int64(1), time.Now(), true},
		"a time not one":   {"s", int64(1), "2026-10-24", true},
		"a bool not one":   {"s", int64(1), time.Now(), "true"},
	} {
		if raw, err := encodeCursor(everyKind, filterDigest(nil), key); err == nil {
			t.Errorf("%s: minted %q", name, raw)
		}
	}
}

func TestTheFilterDigestIsCanonical(t *testing.T) {
	a := filterDigest([]string{"label=env=prod", "label=role=db"})
	if b := filterDigest([]string{"label=role=db", "label=env=prod"}); a != b {
		t.Errorf("the digest depends on the filters' order: %s, %s", a, b)
	}
	if len(a) != 32 || strings.Trim(a, "0123456789abcdef") != "" {
		t.Errorf("digest %q is not 16 bytes of hex", a)
	}
	for _, other := range [][]string{nil, {"label=env=prod"}, {"label=env=prod", "label=role=web"}} {
		if filterDigest(other) == a {
			t.Errorf("filters %v share a digest with another set", other)
		}
	}
	// The caller's slice is the caller's: sorting it here would reorder a
	// list it still holds.
	in := []string{"b", "a"}
	filterDigest(in)
	if in[0] != "b" {
		t.Errorf("filterDigest sorted its argument in place: %v", in)
	}
}

func readPageFor(t *testing.T, query string) (pageRequest, bool, *httptest.ResponseRecorder) {
	t.Helper()
	s := &Server{log: slog.New(slog.DiscardHandler)}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/list?"+query, nil)
	p, ok := s.readPage(w, r, everyKind)
	return p, ok, w
}

func TestTheLimitIsTheCallersWithinOneMaximum(t *testing.T) {
	accepted := map[string]int{
		"":                  store.DefaultPageLimit,
		"limit=":            store.DefaultPageLimit,
		"limit=1":           1,
		"limit=%207%20":     7,
		"limit=10000":       store.MaxPageLimit,
		"limit=3&limit=900": 3,
	}
	for query, want := range accepted {
		p, ok, w := readPageFor(t, query)
		if !ok || p.Limit != want {
			t.Errorf("?%s: limit %d ok=%t (%s), want %d", query, p.Limit, ok, w.Body.String(), want)
		}
	}

	refused := map[string]Error{
		"limit=10001":                 {Parameters: map[string]any{"field": "limit", "problem": access.ProblemExceedsMaximum, "limit": "10000"}},
		"limit=99999999999999999999":  {Parameters: map[string]any{"field": "limit", "problem": access.ProblemExceedsMaximum, "limit": "10000"}},
		"limit=0":                     {Parameters: map[string]any{"field": "limit", "problem": access.ProblemInvalid}},
		"limit=-1":                    {Parameters: map[string]any{"field": "limit", "problem": access.ProblemInvalid}},
		"limit=-99999999999999999999": {Parameters: map[string]any{"field": "limit", "problem": access.ProblemInvalid}},
		"limit=x":                     {Parameters: map[string]any{"field": "limit", "problem": access.ProblemInvalid}},
		"limit=1.5":                   {Parameters: map[string]any{"field": "limit", "problem": access.ProblemInvalid}},
		"limit=1e3":                   {Parameters: map[string]any{"field": "limit", "problem": access.ProblemInvalid}},
		"after=not-a-cursor":          {Parameters: map[string]any{"field": "after", "problem": access.ProblemInvalid}},
	}
	for query, want := range refused {
		_, ok, w := readPageFor(t, query)
		if ok {
			t.Errorf("?%s was accepted", query)
			continue
		}
		var got Error
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusBadRequest || got.Code != CodeInvalidRequest {
			t.Errorf("?%s answered %d %s, want 400 %s", query, w.Code, w.Body.String(), CodeInvalidRequest)
			continue
		}
		if len(got.Parameters) != len(want.Parameters) {
			t.Errorf("?%s parameters %v, want %v", query, got.Parameters, want.Parameters)
		}
		for k, v := range want.Parameters {
			if got.Parameters[k] != v {
				t.Errorf("?%s parameter %s = %v, want %v", query, k, got.Parameters[k], v)
			}
		}
	}
}

func TestAPositionIsReadFromItsCursor(t *testing.T) {
	at := time.Date(2026, 10, 24, 9, 30, 0, 0, time.UTC)
	raw, err := encodeCursor(everyKind, filterDigest(nil), []any{"t-9", int64(3), at, false})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	p, ok, w := readPageFor(t, "limit=2&after="+raw)
	if !ok {
		t.Fatalf("a cursor this list minted was refused: %s", w.Body.String())
	}
	if p.Limit != 2 || len(p.After) != 4 || p.After[0] != "t-9" || p.After[1] != int64(3) ||
		!p.After[2].(time.Time).Equal(at) || p.After[3] != false {
		t.Errorf("read page %+v", p)
	}
	if p.list.name != everyKind.name || p.filters != filterDigest(nil) {
		t.Errorf("the page does not carry what its next cursor must bind: %+v", p)
	}
}

func answerFor(t *testing.T, items any, more bool, last []any, extra map[string]any) (map[string]any, int) {
	t.Helper()
	s := &Server{log: slog.New(slog.DiscardHandler)}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/list", nil)
	p := pageRequest{Page: store.Page{Limit: 1}, list: everyKind, filters: filterDigest(nil)}
	s.answerPage(w, r, p, "items", items, more, last, extra)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("answer body %q: %v", w.Body.String(), err)
	}
	return body, w.Code
}

func TestAPageSaysWhereItStopped(t *testing.T) {
	at := time.Date(2026, 10, 24, 9, 30, 0, 0, time.UTC)

	// An empty page that ends: an empty list, never null, and no cursor.
	body, code := answerFor(t, []string(nil), false, nil, nil)
	if code != http.StatusOK || body["truncated"] != false {
		t.Errorf("an empty page answered %d %v", code, body)
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("an empty page's items are %#v, want []", body["items"])
	}
	if _, ok := body["next"]; ok {
		t.Errorf("a page that ends carries next: %v", body)
	}
	if body, _ := answerFor(t, nil, false, nil, nil); body["items"] == nil {
		t.Errorf("an untyped nil page answered items %#v, want []", body["items"])
	}

	// A page that continues carries the cursor after its last item, and the
	// list's own members unchanged; the paging members are not theirs.
	body, code = answerFor(t, []string{"a"}, true, []any{"a", int64(1), at, true},
		map[string]any{"roles": []string{"admin"}, "truncated": "overwritten", "next": "overwritten"})
	if code != http.StatusOK || body["truncated"] != true {
		t.Fatalf("a page that continues answered %d %v", code, body)
	}
	next, _ := body["next"].(string)
	key, err := decodeCursor(next, everyKind, filterDigest(nil))
	if err != nil || key[0] != "a" {
		t.Errorf("next %q decodes as %v, %v; want the position after a", next, key, err)
	}
	if roles, _ := body["roles"].([]any); len(roles) != 1 || roles[0] != "admin" {
		t.Errorf("an extra member was not answered unchanged: %v", body)
	}

	// A key this list cannot encode is an outage, never a page whose cursor
	// would be refused (M11).
	body, code = answerFor(t, []string{"a"}, true, []any{"a"}, nil)
	if code != http.StatusInternalServerError || body["code"] != CodeInternal {
		t.Errorf("a page whose cursor cannot be minted answered %d %v, want 500 %s", code, body, CodeInternal)
	}
}
