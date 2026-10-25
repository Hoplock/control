// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"go/ast"
	"go/token"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// A keyInt32 component (a target port) round-trips at either end of its
// range. Past it, a cursor does not decode — the caller's position names no
// row a 32-bit column can hold — and the server mints none.
func TestAnInt32ComponentKeepsToItsRange(t *testing.T) {
	ports := list{name: "ports", key: []keyKind{keyString, keyInt32}}
	digest := filterDigest(nil)
	for _, n := range []int64{0, 22, math.MaxInt32, math.MinInt32} {
		raw, err := encodeCursor(ports, digest, []any{"h", n})
		if err != nil {
			t.Fatalf("encode %d: %v", n, err)
		}
		got, err := decodeCursor(raw, ports, digest)
		if err != nil || got[1] != n {
			t.Errorf("%d decoded as %v (%v), want int64 %d", n, got, err, n)
		}
	}
	for _, n := range []int64{math.MaxInt32 + 1, math.MinInt32 - 1, math.MaxInt64} {
		if raw, err := encodeCursor(ports, digest, []any{"h", n}); err == nil {
			t.Errorf("minted %q for %d, which no 32-bit column holds", raw, n)
		}
		raw := encodeRaw(t, map[string]any{"v": 1, "l": ports.name, "f": digest, "k": []string{"h", strconv.FormatInt(n, 10)}})
		if got, err := decodeCursor(raw, ports, digest); err == nil {
			t.Errorf("decoded %d as %v, want a refusal", n, got)
		}
	}
	if raw, err := encodeCursor(ports, digest, []any{"h", int32(22)}); err == nil {
		t.Errorf("minted %q from an int32; a position is typed as the store's Page takes it, int64", raw)
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
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/list?"+query, nil)
	p, ok := readPage(w, r, everyKind)
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

// ---------------------------------------------------------------------------
// a host's lists (M26.7)
// ---------------------------------------------------------------------------

// hostPageRequest is a request on a host route of provider, logging to logs.
func hostPageRequest(provider, target string, logs *bytes.Buffer) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	hr := &hostRequest{provider: provider, log: slog.New(slog.NewJSONHandler(logs, nil))}
	return r.WithContext(context.WithValue(r.Context(), ctxKeyHost, hr))
}

func readHostPageFor(t *testing.T, query string, filters ...string) (HostPage, bool, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	p, ok := ReadHostPage(w, hostPageRequest("test/host", "/list?"+query, &bytes.Buffer{}), "archive", 2, filters...)
	return p, ok, w
}

func TestAHostPageIsReadThroughControlsPath(t *testing.T) {
	// Every refusal is Control's own, byte for byte but the correlation id.
	for _, query := range []string{"limit=0", "limit=x", "limit=10001", "after=not-a-cursor"} {
		_, ok, host := readHostPageFor(t, query)
		_, _, control := readPageFor(t, query)
		if ok || host.Code != control.Code || host.Body.String() != control.Body.String() {
			t.Errorf("?%s: a host list answered %d %s, Control's %d %s", query, host.Code, host.Body, control.Code, control.Body)
		}
	}

	p, ok, w := readHostPageFor(t, "")
	if !ok || p.Limit != store.DefaultPageLimit || p.After != nil {
		t.Fatalf("the first page read %+v ok=%t (%s)", p, ok, w.Body)
	}
	if p.req.list.name != "test/host:archive" || len(p.req.list.key) != 2 {
		t.Errorf("a host's list is %+v, want test/host:archive with two string components", p.req.list)
	}

	raw, err := encodeCursor(hostList("test/host", "archive", 2), filterDigest([]string{"state=open"}), []any{"2026", "r-9"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	p, ok, w = readHostPageFor(t, "limit=3&after="+raw, "state=open")
	if !ok || p.Limit != 3 || len(p.After) != 2 || p.After[0] != "2026" || p.After[1] != "r-9" {
		t.Fatalf("a cursor this list minted read %+v ok=%t (%s)", p, ok, w.Body)
	}

	// A cursor binds the list, its provider and its filters, both ways.
	foreign := map[string]string{
		"under other filters":    raw,
		"from Control's targets": mustCursor(t, targetList, filterDigest(nil), []any{"t-1"}),
		"from another host":      mustCursor(t, hostList("other/host", "archive", 2), filterDigest(nil), []any{"2026", "r-9"}),
		"from another list":      mustCursor(t, hostList("test/host", "inbox", 2), filterDigest(nil), []any{"2026", "r-9"}),
	}
	for name, raw := range foreign {
		if _, ok, w := readHostPageFor(t, "after="+raw); ok || w.Code != http.StatusBadRequest {
			t.Errorf("a cursor %s was continued by a host list: %d %s", name, w.Code, w.Body)
		}
	}
	hostCursor := mustCursor(t, hostList("test/host", "targets", 1), filterDigest(nil), []any{"t-1"})
	if _, err := decodeCursor(hostCursor, targetList, filterDigest(nil)); err == nil {
		t.Error("Control's targets continued a host list's cursor of the same name and shape")
	}
}

func mustCursor(t *testing.T, l list, filters string, last []any) string {
	t.Helper()
	raw, err := encodeCursor(l, filters, last)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return raw
}

func TestAHostPageIsAnsweredInControlsShape(t *testing.T) {
	p, _, _ := readHostPageFor(t, "limit=1", "state=open")
	w := httptest.NewRecorder()
	WriteHostPage(w, hostPageRequest("test/host", "/list", &bytes.Buffer{}), p, "requests", []string{"r-1"}, true,
		[]string{"2026", "r-1"}, map[string]any{"tenant": "a"})
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
		t.Fatalf("answered %d %s", w.Code, w.Body)
	}
	if body["truncated"] != true || body["tenant"] != "a" {
		t.Errorf("body %v", body)
	}
	key, err := decodeCursor(body["next"].(string), p.req.list, filterDigest([]string{"state=open"}))
	if err != nil || key[0] != "2026" || key[1] != "r-1" {
		t.Errorf("next decodes as %v, %v", key, err)
	}

	w = httptest.NewRecorder()
	WriteHostPage(w, hostPageRequest("test/host", "/list", &bytes.Buffer{}), p, "requests", nil, false, nil, nil)
	if got := w.Body.String(); w.Code != http.StatusOK || !strings.Contains(got, `"requests":[]`) || strings.Contains(got, "next") {
		t.Errorf("an empty last page answered %d %s", w.Code, got)
	}
}

func TestAHostListThatCannotBeAnsweredIsAnOutage(t *testing.T) {
	outage := func(t *testing.T, w *httptest.ResponseRecorder, logs *bytes.Buffer, problem string) {
		t.Helper()
		var e Error
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || w.Code != http.StatusInternalServerError || e.Code != CodeInternal {
			t.Errorf("answered %d %s, want 500 internal", w.Code, w.Body)
		}
		if !strings.Contains(logs.String(), `"event":"`+hostListEvent+`"`) || !strings.Contains(logs.String(), problem) {
			t.Errorf("logged %q, want %s naming %q", logs, hostListEvent, problem)
		}
	}

	for name, read := range map[string]struct {
		list   string
		keyLen int
		hosted bool
		want   string
	}{
		"outside a host route": {list: "archive", keyLen: 1, want: "outside a host route"},
		"no name":              {keyLen: 1, hosted: true, want: "no name"},
		"no key":               {list: "archive", hosted: true, want: "no components"},
	} {
		t.Run("read "+name, func(t *testing.T) {
			var logs bytes.Buffer
			r := httptest.NewRequest(http.MethodGet, "/list", nil)
			if read.hosted {
				r = hostPageRequest("test/host", "/list", &logs)
			} else {
				prev := slog.Default()
				slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
				t.Cleanup(func() { slog.SetDefault(prev) })
			}
			w := httptest.NewRecorder()
			if _, ok := ReadHostPage(w, r, read.list, read.keyLen); ok {
				t.Fatal("accepted")
			}
			outage(t, w, &logs, read.want)
		})
	}

	p, _, _ := readHostPageFor(t, "")
	for name, write := range map[string]struct {
		page   HostPage
		member string
		items  any
		more   bool
		last   []string
		extra  map[string]any
		want   string
	}{
		"a List not read":       {page: HostPage{Limit: 1}, member: "items", items: []string{}, want: "not read by ReadList"},
		"no member":             {page: p, items: []string{}, want: "member is empty"},
		"member truncated":      {page: p, member: "truncated", items: []string{}, want: "member is empty"},
		"member next":           {page: p, member: "next", items: []string{}, want: "member is empty"},
		"extra truncated":       {page: p, member: "items", items: []string{}, extra: map[string]any{"truncated": 1}, want: "collides with truncated"},
		"extra next":            {page: p, member: "items", items: []string{}, extra: map[string]any{"next": 1}, want: "collides with next"},
		"extra the member":      {page: p, member: "items", items: []string{}, extra: map[string]any{"items": 1}, want: "collides with items"},
		"items not a list":      {page: p, member: "items", items: map[string]int{}, want: "not a list"},
		"items bytes":           {page: p, member: "items", items: []byte("[]"), want: "bytes"},
		"more and no last":      {page: p, member: "items", items: []string{"a"}, more: true, want: "names no last item"},
		"a last too short":      {page: p, member: "items", items: []string{"a"}, more: true, last: []string{"a"}, want: "wrong number"},
		"a last too long":       {page: p, member: "items", items: []string{"a"}, more: true, last: []string{"a", "b", "c"}, want: "wrong number"},
		"outside a host route":  {page: p, member: "items", items: []string{}, want: "outside a host route"},
		"an empty last, more":   {page: p, member: "items", items: []string{"a"}, more: true, last: []string{}, want: "wrong number"},
		"a nil pointer to list": {page: p, member: "items", items: (*[]string)(nil), want: "not a list"},
	} {
		t.Run("write "+name, func(t *testing.T) {
			var logs bytes.Buffer
			r := hostPageRequest("test/host", "/list", &logs)
			if name == "outside a host route" {
				r = httptest.NewRequest(http.MethodGet, "/list", nil)
				prev := slog.Default()
				slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
				t.Cleanup(func() { slog.SetDefault(prev) })
			}
			w := httptest.NewRecorder()
			WriteHostPage(w, r, write.page, write.member, write.items, write.more, write.last, write.extra)
			outage(t, w, &logs, write.want)
		})
	}
}

// A host's list is "<provider>:<name>" in its cursor, so no list of Control's
// may name itself with a ":" — or a host's cursor could be one of Control's.
func TestNoControlListNameHasAColon(t *testing.T) {
	fset := token.NewFileSet()
	found := 0
	forEachSourceDeclIn(t, fset, ".", func(decl ast.Decl) {
		// hostList names a host's lists, which is what the colon is for.
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "hostList" {
			return
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if ident, ok := lit.Type.(*ast.Ident); !ok || ident.Name != "list" {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "name" {
					continue
				}
				name, ok := kv.Value.(*ast.BasicLit)
				if !ok || name.Kind != token.STRING {
					t.Errorf("%s: a list of Control's is named by %T, not a literal this test can read", fset.Position(kv.Pos()), kv.Value)
					continue
				}
				found++
				if strings.Contains(name.Value, ":") {
					t.Errorf("%s: list %s names itself with a colon", fset.Position(kv.Pos()), name.Value)
				}
			}
			return true
		})
	})
	if found < 2 {
		t.Errorf("found %d of Control's lists; this test no longer sees them", found)
	}
}
