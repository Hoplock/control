// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Every north-bound list is bounded by its caller and says where it stopped
// (M26). The test below walks the ROUTER, as the isolation tests do, so a `GET`
// route added later fails it until it is classified here: a paged list, a
// signal-only list, an exemption with its reason, or — until 0037 deletes the
// map — a list a later chunk pages.
//
// A host's routes are not classified. Control cannot see which of them is a
// list, so nothing here enforces the shape on them (M26.7); `server` offers it.

// pagedList is one list answering M26's shape, and how to fill it.
type pagedList struct {
	member string // the list's member in the answer
	path   string // the list in tenant-a, with no filter
	// seed stores at least two items in tenant-a and returns how many the
	// list then holds.
	seed func(t *testing.T, f *serverFixture) int
}

var pagedLists = map[string]pagedList{
	"GET /api/v1/tenants/{tenant}/targets": {
		member: "targets", path: "/api/v1/tenants/tenant-a/targets",
		seed: func(t *testing.T, f *serverFixture) int {
			t.Helper()
			for _, id := range []string{"t-3", "t-1", "t-2"} {
				if err := f.st.Targets().Upsert(t.Context(), "tenant-a",
					storetest.Target(id, id+".example", map[string]string{"env": "prod"})); err != nil {
					t.Fatalf("seed target %s: %v", id, err)
				}
			}
			got, err := f.st.Targets().Page(t.Context(), "tenant-a", nil, store.Page{Limit: store.MaxPageLimit})
			if err != nil || got.More {
				t.Fatalf("count targets: %v (more=%t)", err, got.More)
			}
			return len(got.Rows)
		},
	},
	"GET /api/v1/tenants/{tenant}/subjects": {
		member: "subjects", path: "/api/v1/tenants/tenant-a/subjects",
		seed: func(t *testing.T, f *serverFixture) int {
			t.Helper()
			for _, id := range []string{"carol@example.com", "alice@example.com", "bob@example.com"} {
				if err := f.st.Subjects().Upsert(t.Context(), "tenant-a", storetest.Subject(id)); err != nil {
					t.Fatalf("seed subject %s: %v", id, err)
				}
			}
			got, err := f.st.Subjects().Page(t.Context(), "tenant-a", store.Page{Limit: store.MaxPageLimit})
			if err != nil || got.More {
				t.Fatalf("count subjects: %v (more=%t)", err, got.More)
			}
			return len(got.Rows)
		},
	},
}

// signalLists are the lists bounded by one object (M26.3): each keeps its
// server-side bound, takes no limit and no cursor, and names the member that
// says the bound cut it.
var signalLists = map[string]string{}

// exemptRoutes answer one object, or a list bounded by configuration rather
// than by the estate (M26), each with its reason (0028's inventory).
var exemptRoutes = map[string]string{
	"GET /api/v1/session/methods":                                              "one per configured sign-in method",
	"GET /api/v1/session/federated/{connector}/callback":                       "a sign-in step, not a list",
	"GET /api/v1/session/federated/{connector}/metadata":                       "one document",
	"GET /api/v1/session":                                                      "one object",
	"GET /api/v1/tenants/{tenant}/ca":                                          "the trust bundle is answered whole or not at all; one key per rotation inside its overlap",
	"GET /api/v1/tenants/{tenant}/grant-requests/{request}":                    "one object",
	"GET /api/v1/tenants/{tenant}/me/grant-requests/{request}":                 "one object",
	"GET /api/v1/tenants/{tenant}/identity/claim-mapping":                      "one object; its lists are the document's, bounded by the body it was written in",
	"GET /api/v1/tenants/{tenant}/policy/bundles/{version}":                    "one object",
	"GET /api/v1/tenants/{tenant}/policy/bundles/{version}/diff":               "one diff of two stored documents, each bounded by the upload body",
	"GET /api/v1/tenants/{tenant}/policy/active":                               "one object",
	"GET /api/v1/tenants/{tenant}/decisions/{decision}":                        "one object",
	"GET /api/v1/tenants/{tenant}/targets/{target}":                            "one object",
	"GET /api/v1/tenants/{tenant}/subjects/{subject}":                          "one object",
	"GET /api/v1/tenants/{tenant}/extensions":                                  "one row per extension point",
	"GET /api/v1/tenants/{tenant}/access-context/providers":                    "one per configured provider",
	"GET /api/v1/tenants/{tenant}/access-context/bindings":                     "one per configured provider in the tenant (primary key (tenant, provider))",
	"GET /api/v1/tenants/{tenant}/access-context/providers/{provider}/binding": "one object",
	"GET /api/v1/tenants/{tenant}/audit/records/{record}":                      "one object",
	"GET /api/v1/tenants/{tenant}/audit/sessions/{session}/loss":               "one row per gap cause, of which the contract names two",
}

// pendingBounded are the lists a later chunk pages or signals, by the chunk
// that takes each (0028's inventory). Each chunk deletes its rows and moves
// them to the map above that rules them; 0037 deletes this map.
var pendingBounded = map[string]string{
	"GET /api/v1/tenants/{tenant}/proxies":                               "0031",
	"GET /api/v1/tenants/{tenant}/fleet/config/rollouts":                 "0031",
	"GET /api/v1/tenants/{tenant}/fleet/algorithms":                      "0032",
	"GET /api/v1/tenants/{tenant}/fleet/target-observations":             "0032",
	"GET /api/v1/tenants/{tenant}/grants":                                "0033",
	"GET /api/v1/tenants/{tenant}/me/grant-requests":                     "0033",
	"GET /api/v1/tenants/{tenant}/grants/{grant}":                        "0033",
	"GET /api/v1/tenants/{tenant}/role-bindings":                         "0034",
	"GET /api/v1/tenants/{tenant}/ca/certificates":                       "0034",
	"GET /api/v1/tenants/{tenant}/policy/bundles":                        "0035",
	"GET /api/v1/tenants/{tenant}/sessions/{session}/decisions":          "0035",
	"GET /api/v1/tenants/{tenant}/audit/records":                         "0036",
	"GET /api/v1/tenants/{tenant}/audit/drift":                           "0036",
	"GET /api/v1/tenants/{tenant}/audit/algorithm-policy-unmet":          "0036",
	"GET /api/v1/tenants/{tenant}/audit/gaps":                            "0036",
	"GET /api/v1/tenants/{tenant}/audit/certificates/{serial}/records":   "0036",
	"GET /api/v1/tenants/{tenant}/audit/blocked-commands":                "0037",
	"GET /api/v1/tenants/{tenant}/audit/negotiated":                      "0037",
	"GET /api/v1/tenants/{tenant}/audit/sessions/{session}/certificates": "0037",
	"GET /api/v1/tenants/{tenant}/audit/sessions/{session}/algorithms":   "0037",
}

// listClasses is the four maps, so the check can be run over another set.
type listClasses struct {
	paged   map[string]pagedList
	signal  map[string]string
	exempt  map[string]string
	pending map[string]string
}

var chunkNumber = regexp.MustCompile(`^[0-9]{4}$`)

// problems names every way routes and the classes disagree: a Control `GET`
// route in no class or in more than one, an entry naming no such route, an
// exemption with no reason, and a pending list with no chunk.
func (c listClasses) problems(routes []north.RouteInfo) []string {
	var out []string
	served := map[string]bool{}
	for _, route := range routes {
		if route.Method != http.MethodGet || route.Provider != "" {
			continue
		}
		name := route.String()
		served[name] = true
		var in []string
		if _, ok := c.paged[name]; ok {
			in = append(in, "pagedLists")
		}
		if _, ok := c.signal[name]; ok {
			in = append(in, "signalLists")
		}
		if _, ok := c.exempt[name]; ok {
			in = append(in, "exemptRoutes")
		}
		if _, ok := c.pending[name]; ok {
			in = append(in, "pendingBounded")
		}
		switch len(in) {
		case 0:
			out = append(out, name+" is a GET route in none of pagedLists, signalLists, exemptRoutes and pendingBounded: "+
				"page it, signal it, or exempt it with its reason (M26)")
		case 1:
		default:
			out = append(out, name+" is classified more than once: "+strings.Join(in, ", "))
		}
	}
	classified := slices.Concat(slices.Collect(maps.Keys(c.paged)), slices.Collect(maps.Keys(c.signal)),
		slices.Collect(maps.Keys(c.exempt)), slices.Collect(maps.Keys(c.pending)))
	for _, name := range classified {
		if !served[name] {
			out = append(out, name+" is classified but is not a GET route Control serves")
		}
	}
	for name, reason := range c.exempt {
		if strings.TrimSpace(reason) == "" {
			out = append(out, name+" is exempt with no reason")
		}
	}
	for name, chunk := range c.pending {
		if !chunkNumber.MatchString(chunk) {
			out = append(out, fmt.Sprintf("%s is pending under %q, which is not a four-digit chunk", name, chunk))
		}
	}
	slices.Sort(out)
	return out
}

func classes() listClasses {
	return listClasses{paged: pagedLists, signal: signalLists, exempt: exemptRoutes, pending: pendingBounded}
}

// listPage is one page of a list as the client reads it.
type listPage struct {
	items     []json.RawMessage
	truncated bool
	next      string
	hasNext   bool
}

func readListPage(t *testing.T, resp *http.Response, member string) listPage {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("answered %d, want 200: %s", resp.StatusCode, decodeError(t, resp).Message)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("body: %v", err)
	}
	var p listPage
	raw, ok := body[member]
	if !ok || string(raw) == "null" {
		t.Fatalf("the answer has no %q list: %v", member, body)
	}
	if err := json.Unmarshal(raw, &p.items); err != nil {
		t.Fatalf("%s: %v", member, err)
	}
	truncated, ok := body["truncated"]
	if !ok || json.Unmarshal(truncated, &p.truncated) != nil {
		t.Fatalf("the answer has no boolean truncated: %s", truncated)
	}
	if next, ok := body["next"]; ok {
		p.hasNext = true
		if err := json.Unmarshal(next, &p.next); err != nil || p.next == "" {
			t.Fatalf("next is %s, want a cursor", next)
		}
	}
	return p
}

// expectInvalid asserts a 400 invalid_request naming field, problem and, when
// it is not empty, the limit.
func expectInvalid(t *testing.T, resp *http.Response, field, problem, limit string) {
	t.Helper()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("answered %d, want 400", resp.StatusCode)
	}
	e := decodeError(t, resp)
	if e.Code != north.CodeInvalidRequest || e.Parameters["field"] != field || e.Parameters["problem"] != problem {
		t.Fatalf("answered %s %v, want %s {field: %s, problem: %s}", e.Code, e.Parameters, north.CodeInvalidRequest, field, problem)
	}
	if got, _ := e.Parameters["limit"].(string); got != limit {
		t.Fatalf("limit parameter %v, want %q", e.Parameters["limit"], limit)
	}
	if e.CorrelationID == "" {
		t.Error("the refusal carries no correlation id")
	}
}

func adminToken(t *testing.T, f *serverFixture, tenants ...store.Tenant) string {
	t.Helper()
	scopes := map[store.Tenant]identity.RoleSet{}
	for _, tenant := range tenants {
		scopes[tenant] = identity.RoleSet{identity.RoleAdmin}
	}
	return f.token(t, tenants[0], scopes)
}

func withQuery(path string, q url.Values) string { return path + "?" + q.Encode() }

func TestEveryListIsBoundedOrSaysWhyNot(t *testing.T) {
	f := newServer(t)
	if problems := classes().problems(f.server.Routes()); len(problems) > 0 {
		t.Fatalf("the router and the list classes disagree:\n  %s", strings.Join(problems, "\n  "))
	}
	token := adminToken(t, f, "tenant-a")

	firstNext := map[string]string{}
	for route, l := range pagedLists {
		t.Run(route, func(t *testing.T) {
			want := l.seed(t, f)
			if want < 2 {
				t.Fatalf("seed left %d items; a walk of one page proves nothing about the next", want)
			}

			// 1–3: every refusal is the grammar every malformed parameter
			// uses, never a clamp (M26.1, M26.2, M21).
			expectInvalid(t, f.do(t, "GET", withQuery(l.path, url.Values{"limit": {"10001"}}), token, nil),
				"limit", access.ProblemExceedsMaximum, "10000")
			for _, bad := range []string{"0", "x"} {
				expectInvalid(t, f.do(t, "GET", withQuery(l.path, url.Values{"limit": {bad}}), token, nil),
					"limit", access.ProblemInvalid, "")
			}
			expectInvalid(t, f.do(t, "GET", withQuery(l.path, url.Values{"after": {"not-a-cursor"}}), token, nil),
				"after", access.ProblemInvalid, "")

			// 4: a walk one item at a time visits every item exactly once.
			seen := map[string]bool{}
			q := url.Values{"limit": {"1"}}
			for pages := 0; ; pages++ {
				if pages > want {
					t.Fatalf("the walk ran past %d pages for %d items", pages, want)
				}
				p := readListPage(t, f.do(t, "GET", withQuery(l.path, q), token, nil), l.member)
				if pages == 0 {
					firstNext[route] = p.next
				}
				if len(p.items) != 1 {
					t.Fatalf("page %d holds %d items, want 1", pages, len(p.items))
				}
				item := string(p.items[0])
				if seen[item] {
					t.Fatalf("page %d repeats %s", pages, item)
				}
				seen[item] = true
				if p.truncated != p.hasNext {
					t.Fatalf("page %d: truncated=%t but next present=%t; next is present exactly when truncated", pages, p.truncated, p.hasNext)
				}
				if !p.truncated {
					break
				}
				q.Set("after", p.next)
			}
			if len(seen) != want {
				t.Fatalf("the walk visited %d items, the list holds %d", len(seen), want)
			}
		})
	}

	// 5: a cursor is bound to its list. One list's position, presented to
	// another, is refused rather than misread.
	for from, next := range firstNext {
		for to, l := range pagedLists {
			if to == from {
				continue
			}
			t.Run(from+" → "+to, func(t *testing.T) {
				expectInvalid(t, f.do(t, "GET", withQuery(l.path, url.Values{"after": {next}}), token, nil),
					"after", access.ProblemInvalid, "")
			})
		}
	}

	for route, member := range signalLists {
		t.Run(route, func(t *testing.T) {
			pattern := strings.TrimPrefix(route, "GET ")
			resp := f.do(t, "GET", samplePath(pattern, "tenant-a"), token, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("answered %d, want 200", resp.StatusCode)
			}
			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("body: %v", err)
			}
			if _, ok := body[member].(bool); !ok {
				t.Fatalf("%s is %#v, want a boolean", member, body[member])
			}
		})
	}
}

// The check above is only worth having if it fails. A route nobody classified,
// one classified twice, an entry naming no route, an exemption with no reason
// and a pending list with no chunk each fail it.
func TestTheListCheckFailsOnARouteNobodyClassified(t *testing.T) {
	var routes []north.RouteInfo
	c := classes()
	for _, m := range []map[string]string{c.signal, c.exempt, c.pending} {
		for name := range m {
			routes = append(routes, routeNamed(t, name))
		}
	}
	for name := range c.paged {
		routes = append(routes, routeNamed(t, name))
	}
	if problems := c.problems(routes); len(problems) != 0 {
		t.Fatalf("the classes disagree with their own routes: %v", problems)
	}

	unclassified := north.RouteInfo{Method: "GET", Pattern: "/api/v1/tenants/{tenant}/widgets"}
	host := north.RouteInfo{Method: "GET", Pattern: "/api/v1/tenants/{tenant}/reports", Provider: "test/host"}
	write := north.RouteInfo{Method: "POST", Pattern: "/api/v1/tenants/{tenant}/widgets"}
	if got := c.problems(append(slices.Clone(routes), unclassified, host, write)); len(got) != 1 ||
		!strings.HasPrefix(got[0], "GET /api/v1/tenants/{tenant}/widgets is a GET route in none") {
		t.Errorf("an unclassified GET route beside a host's route and a write: %v, want one problem naming it", got)
	}

	twice := listClasses{paged: c.paged, signal: c.signal, exempt: withEntry(c.exempt, "GET /api/v1/tenants/{tenant}/targets", "one object"), pending: c.pending}
	if got := twice.problems(routes); len(got) != 1 || !strings.Contains(got[0], "classified more than once") {
		t.Errorf("a route both paged and exempt: %v", got)
	}
	gone := listClasses{paged: c.paged, signal: c.signal, exempt: c.exempt, pending: withEntry(c.pending, "GET /api/v1/tenants/{tenant}/gone", "0031")}
	if got := gone.problems(routes); len(got) != 1 || !strings.Contains(got[0], "is not a GET route Control serves") {
		t.Errorf("a pending entry naming no route: %v", got)
	}
	reasonless := listClasses{paged: c.paged, signal: c.signal, exempt: withEntry(c.exempt, "GET /api/v1/session", " "), pending: c.pending}
	if got := reasonless.problems(routes); len(got) != 1 || !strings.Contains(got[0], "exempt with no reason") {
		t.Errorf("an exemption with no reason: %v", got)
	}
	chunkless := listClasses{paged: c.paged, signal: c.signal, exempt: c.exempt, pending: withEntry(c.pending, "GET /api/v1/tenants/{tenant}/proxies", "later")}
	if got := chunkless.problems(routes); len(got) != 1 || !strings.Contains(got[0], "not a four-digit chunk") {
		t.Errorf("a pending list with no chunk: %v", got)
	}
}

func routeNamed(t *testing.T, name string) north.RouteInfo {
	t.Helper()
	method, pattern, ok := strings.Cut(name, " ")
	if !ok {
		t.Fatalf("%q is not a route name", name)
	}
	return north.RouteInfo{Method: method, Pattern: pattern}
}

func withEntry(m map[string]string, k, v string) map[string]string {
	out := map[string]string{k: v}
	for name, val := range m {
		if name != k {
			out[name] = val
		}
	}
	return out
}

// A cursor is a position, not a scope (M18). It carries the list, the filters'
// digest and the key, and never the tenant it was minted in; presented under
// another tenant's path it continues THAT tenant's list from the position,
// which the middleware has already let the caller read.
func TestACursorFromOneTenantContinuesAnothersList(t *testing.T) {
	f := newServer(t)
	for tenant, ids := range map[store.Tenant][]string{
		"tenant-a": {"a-1", "a-2", "m-a", "z-a"},
		"tenant-b": {"b-1", "m-b", "z-b"},
	} {
		for _, id := range ids {
			if err := f.st.Targets().Upsert(t.Context(), tenant, storetest.Target(id, id+".example", nil)); err != nil {
				t.Fatalf("seed %s: %v", id, err)
			}
		}
	}
	token := adminToken(t, f, "tenant-a", "tenant-b")

	first := readListPage(t, f.do(t, "GET", "/api/v1/tenants/tenant-a/targets?limit=2", token, nil), "targets")
	if !first.truncated {
		t.Fatalf("tenant-a's first page of two does not continue")
	}

	// Nothing in the cursor names the tenant.
	raw, err := base64.RawURLEncoding.DecodeString(first.next)
	if err != nil {
		t.Fatalf("next %q is not unpadded base64url: %v", first.next, err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("next decodes to %q: %v", raw, err)
	}
	if keys := slices.Sorted(maps.Keys(fields)); !slices.Equal(keys, []string{"f", "k", "l", "v"}) {
		t.Errorf("the cursor's members are %v, want exactly f, k, l, v", keys)
	}
	if fields["v"] != float64(1) || fields["l"] != "targets" {
		t.Errorf("the cursor is %s, want v 1 for the targets list", raw)
	}
	if k, _ := fields["k"].([]any); len(k) != 1 || k[0] != "a-2" {
		t.Errorf("the cursor's key is %v, want the last item handed: [a-2]", fields["k"])
	}
	if strings.Contains(string(raw), "tenant") {
		t.Errorf("the cursor %s names a tenant", raw)
	}

	rest := readListPage(t, f.do(t, "GET", "/api/v1/tenants/tenant-b/targets?after="+first.next, token, nil), "targets")
	var ids []string
	for _, item := range rest.items {
		var v struct{ ID string }
		if err := json.Unmarshal(item, &v); err != nil {
			t.Fatalf("item %s: %v", item, err)
		}
		ids = append(ids, v.ID)
	}
	if want := []string{"b-1", "m-b", "z-b"}; !slices.Equal(ids, want) || rest.truncated || rest.hasNext {
		t.Errorf("tenant-b after a-2 answered %v (truncated %t), want only tenant-b's rows after a-2: %v", ids, rest.truncated, want)
	}

	// The middleware still decides who reads tenant-b: a cursor carries no
	// authority with it.
	onlyA := adminToken(t, f, "tenant-a")
	resp := f.do(t, "GET", "/api/v1/tenants/tenant-b/targets?after="+first.next, onlyA, nil)
	if resp.StatusCode != http.StatusForbidden || decodeError(t, resp).Code != north.CodeTenantOutOfScope {
		t.Errorf("a tenant-a credential carrying a cursor reached tenant-b: %d", resp.StatusCode)
	}
}

// The cursor binds the filters the reader applied: one minted under a label
// filter does not continue the list under another, and it does continue the
// same filter however the query spells it.
func TestATargetCursorIsBoundToItsLabelFilter(t *testing.T) {
	f := newServer(t)
	for i, env := range []string{"prod", "dev", "prod", "prod", "dev"} {
		id := fmt.Sprintf("t-%d", i)
		if err := f.st.Targets().Upsert(t.Context(), "tenant-a",
			storetest.Target(id, id+".example", map[string]string{"env": env, "role": "db"})); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	token := adminToken(t, f, "tenant-a")
	base := "/api/v1/tenants/tenant-a/targets"

	first := readListPage(t, f.do(t, "GET", base+"?label=env=prod&label=role=db&limit=1", token, nil), "targets")
	if !first.truncated {
		t.Fatal("the filtered list's first page of one does not continue")
	}
	for _, other := range []string{"", "label=env=prod&", "label=env=dev&label=role=db&"} {
		expectInvalid(t, f.do(t, "GET", base+"?"+other+"after="+first.next, token, nil), "after", access.ProblemInvalid, "")
	}
	// The same filter, reordered, spaced and with a repeated key whose last
	// value is the one applied, continues the list.
	same := "label=role=db&label=env=dev&label=%20env%20=%20prod%20&after=" + first.next
	rest := readListPage(t, f.do(t, "GET", base+"?"+same, token, nil), "targets")
	if len(rest.items) != 2 || rest.truncated {
		t.Errorf("the same filter continued with %d items (truncated %t), want the two prod targets after t-0", len(rest.items), rest.truncated)
	}
}

// A failure while a page is read is a 5xx with its correlation id, never the
// rows read before it answered as though the list had ended (M26.6, M11). Each
// list here is given a row its reader cannot scan, after one it can, so the
// statement fails part-way through the page.
func TestAFailureDuringAListPageIsAnOutageNeverAShortPage(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	for route, unscannable := range map[string]string{
		"/api/v1/tenants/tenant-a/targets": `
			INSERT INTO targets (tenant, target_id, hostname, zone, labels)
			VALUES ('tenant-a', 'u-1', 'u-1.example', 'zone-a', '{}'),
			       ('tenant-a', 'u-2', 'u-2.example', 'zone-a', '{"env": 1}')`,
		"/api/v1/tenants/tenant-a/subjects": `
			INSERT INTO subjects (tenant, subject_id, source, claims)
			VALUES ('tenant-a', 'u-1', 'local', '{}'),
			       ('tenant-a', 'u-2', 'local', '{"sub": 1}')`,
	} {
		t.Run(route, func(t *testing.T) {
			if _, err := f.st.Pool().Exec(t.Context(), unscannable); err != nil {
				t.Fatalf("seed: %v", err)
			}
			resp := f.do(t, "GET", route, token, nil)
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("a page that failed part-way answered %d, want 500", resp.StatusCode)
			}
			if e := decodeError(t, resp); e.Code != north.CodeInternal || e.CorrelationID == "" {
				t.Errorf("answered %s with correlation id %q, want %s with one", e.Code, e.CorrelationID, north.CodeInternal)
			}
		})
	}
}
