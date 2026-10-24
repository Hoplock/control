// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/server"
)

// A host's lists answer in Control's bounded shape (M26.7), through a host
// started as Enterprise starts one, against a real database: the refusals, a
// walk, the cursor's bindings, and every way a host can get the façade wrong.

// listRecords are each tenant's record ids in the test host's lists. No id
// names a tenant, so a tenant found in a cursor came from somewhere else.
var listRecords = map[ext.Tenant][]string{
	"alpha": {"r-1", "r-3", "r-5"},
	"bravo": {"r-2", "r-4"},
}

// pageOf is the page of ids, sorted, after list's position.
func pageOf(ids []string, list server.List) (page []string, more bool, last []string) {
	start := 0
	if list.After != nil {
		var found bool
		if start, found = slices.BinarySearch(ids, list.After[0]); found {
			start++
		}
	}
	page = ids[start:min(start+list.Limit, len(ids))]
	if more = start+len(page) < len(ids); more {
		last = []string{page[len(page)-1]}
	}
	return page, more, last
}

// pageIDs is a host list named name over the ids of the tenants Control
// resolved, filtered by ?prefix=, which its cursor binds.
func pageIDs(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		prefix := r.URL.Query().Get("prefix")
		list, ok := server.ReadList(w, r, name, 1, "prefix="+prefix)
		if !ok {
			return
		}
		caller, _ := server.CallerFrom(r.Context())
		covers := caller.Tenants
		if covers == nil {
			covers = []ext.Tenant{caller.Tenant}
		}
		var ids []string
		for _, t := range covers {
			for _, id := range listRecords[t] {
				if strings.HasPrefix(id, prefix) {
					ids = append(ids, id)
				}
			}
		}
		slices.Sort(ids)
		page, more, last := pageOf(ids, list)
		list.Write(w, r, "items", page, more, last, map[string]any{"tenants": covers})
	}
}

// brokenList misuses the façade in the way its {case} names.
func brokenList(w http.ResponseWriter, r *http.Request) {
	switch r.PathValue("case") {
	case "no-name":
		server.ReadList(w, r, "", 1)
		return
	case "no-key":
		server.ReadList(w, r, "broken", 0)
		return
	case "unread":
		server.List{Limit: 1}.Write(w, r, "items", []string{}, false, nil, nil)
		return
	}
	list, ok := server.ReadList(w, r, "broken", 2)
	if !ok {
		return
	}
	items := []string{"a"}
	key := []string{"a", "1"}
	switch r.PathValue("case") {
	case "no-member":
		list.Write(w, r, "", items, false, nil, nil)
	case "member-truncated":
		list.Write(w, r, "truncated", items, false, nil, nil)
	case "member-next":
		list.Write(w, r, "next", items, false, nil, nil)
	case "extra-truncated":
		list.Write(w, r, "items", items, false, nil, map[string]any{"truncated": false})
	case "extra-next":
		list.Write(w, r, "items", items, false, nil, map[string]any{"next": ""})
	case "extra-member":
		list.Write(w, r, "items", items, false, nil, map[string]any{"items": nil})
	case "not-a-list":
		list.Write(w, r, "items", map[string]string{"a": "b"}, false, nil, nil)
	case "more-no-last":
		list.Write(w, r, "items", items, true, nil, nil)
	case "short-last":
		list.Write(w, r, "items", items, true, key[:1], nil)
	case "long-last":
		list.Write(w, r, "items", items, true, append(key, "x"), nil)
	default:
		list.Write(w, r, "items", items, true, key, nil)
	}
}

// listHostOptions is the test host with its lists.
func listHostOptions() server.Options {
	o := hostOptions(&sectionRecorder{}, nil)
	o.Routes = append(o.Routes,
		server.Route{Method: "GET", Pattern: "archive", Permission: "audit:read", Summary: "the example's list", Handler: http.HandlerFunc(listArchive)},
		server.Route{Method: "GET", Pattern: "items", Permission: "audit:read", Summary: "a list", Handler: pageIDs("items")},
		server.Route{Method: "GET", Pattern: "items/other", Permission: "audit:read", Summary: "another list", Handler: pageIDs("other")},
		server.Route{Method: "GET", Pattern: "items/all", Permission: "audit:read", Access: server.AccessCrossTenant,
			Summary: "a list across tenants", Handler: pageIDs("items-all")},
		server.Route{Method: "GET", Pattern: "broken/{case}", Permission: "audit:read", Summary: "a list misused", Handler: http.HandlerFunc(brokenList)},
	)
	return o
}

// listPage is one page as a client reads it.
type listPage struct {
	status    int
	items     []string
	truncated bool
	next      *string
	body      map[string]any
}

func readListPage(t *testing.T, url, token string) listPage {
	t.Helper()
	resp := get(t, url, token)
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("%s: body: %v", url, err)
	}
	p := listPage{status: resp.StatusCode, body: body}
	if p.status != http.StatusOK {
		return p
	}
	member := "items"
	for _, m := range []string{"records", "subjects"} {
		if _, ok := body[m]; ok {
			member = m
		}
	}
	raw, ok := body[member].([]any)
	if !ok {
		t.Fatalf("%s: %s is %#v, want a list", url, member, body[member])
	}
	p.items = []string{}
	for _, v := range raw {
		p.items = append(p.items, v.(string))
	}
	truncated, ok := body["truncated"].(bool)
	if !ok {
		t.Fatalf("%s: truncated is %#v, want always present", url, body["truncated"])
	}
	p.truncated = truncated
	if next, ok := body["next"].(string); ok {
		p.next = &next
	}
	if p.truncated != (p.next != nil) {
		t.Errorf("%s: truncated %t with next %v; next is present exactly when truncated", url, p.truncated, p.next)
	}
	return p
}

func TestTheListBoundsAreControls(t *testing.T) {
	if server.DefaultListLimit != store.DefaultPageLimit || server.MaxListLimit != store.MaxPageLimit {
		t.Errorf("server's bounds %d/%d, Control's %d/%d: one rule for every list (M26.1)",
			server.DefaultListLimit, server.MaxListLimit, store.DefaultPageLimit, store.MaxPageLimit)
	}
}

func TestAHostListIsBoundedAndWalkedAsControlsAre(t *testing.T) {
	base, federation, _ := startHostWith(t, listHostOptions())
	token := issue(t, federation, "alpha", map[store.Tenant]identity.RoleSet{
		"alpha": {identity.RoleAuditor}, "bravo": {identity.RoleAuditor}, "default": {identity.RoleAuditor},
	})
	items := base + "/api/v1/tenants/alpha/items"
	subjects := base + "/api/v1/tenants/alpha/subjects"

	t.Run("every refusal is a Control list's", func(t *testing.T) {
		for _, query := range []string{
			"limit=10001", "limit=99999999999999999999", "limit=0", "limit=-1", "limit=-99999999999999999999",
			"limit=x", "limit=1.5", "limit=1e3", "after=not-a-cursor",
		} {
			host, control := readListPage(t, items+"?"+query, token), readListPage(t, subjects+"?"+query, token)
			if host.status != http.StatusBadRequest || host.body["code"] != "invalid_request" {
				t.Errorf("?%s: a host list answered %d %v, want 400 invalid_request", query, host.status, host.body)
				continue
			}
			hostParams, _ := json.Marshal(host.body["parameters"])
			controlParams, _ := json.Marshal(control.body["parameters"])
			if host.status != control.status || host.body["code"] != control.body["code"] || string(hostParams) != string(controlParams) {
				t.Errorf("?%s: a host list answered %d %v %s, Control's %d %v %s", query,
					host.status, host.body["code"], hostParams, control.status, control.body["code"], controlParams)
			}
		}
	})

	t.Run("a walk", func(t *testing.T) {
		// The example's own list, in the single-tenant deployment it pages.
		archive := base + "/api/v1/tenants/default/archive"
		var walked []string
		url, pages := archive+"?limit=2", 0
		for {
			p := readListPage(t, url, token)
			if p.status != http.StatusOK {
				t.Fatalf("%s answered %d %v", url, p.status, p.body)
			}
			pages++
			walked = append(walked, p.items...)
			if !p.truncated {
				if len(p.items) != 1 {
					t.Errorf("the last page holds %v, want the one item left", p.items)
				}
				break
			}
			if len(p.items) != 2 {
				t.Errorf("a page that continues holds %v, want exactly the limit", p.items)
			}
			url = archive + "?limit=2&after=" + *p.next
		}
		if want := archived["default"]; pages != 3 || !slices.Equal(walked, want) {
			t.Errorf("walked %v in %d pages, want %v in 3", walked, pages, want)
		}
		if p := readListPage(t, archive, token); len(p.items) != 5 || p.truncated {
			t.Errorf("no limit answered %v truncated=%t, want every item under the default", p.items, p.truncated)
		}
	})

	t.Run("a cursor binds its list and its filters, both ways", func(t *testing.T) {
		first := readListPage(t, items+"?limit=1", token)
		if first.next == nil {
			t.Fatalf("no cursor: %v", first.body)
		}
		if p := readListPage(t, items+"?after="+*first.next, token); p.status != http.StatusOK || !slices.Equal(p.items, []string{"r-3", "r-5"}) {
			t.Errorf("the list's own cursor answered %d %v", p.status, p.items)
		}
		filtered := readListPage(t, items+"?prefix=r-&limit=1", token)
		subjectsCursor := mintCursor(t, "subjects", "e3b0c44298fc1c149afbf4c8996fb924", "s-1")
		if p := readListPage(t, subjects+"?after="+subjectsCursor, token); p.status != http.StatusOK {
			t.Fatalf("the hand-minted subjects cursor is not one subjects continues: %d %v", p.status, p.body)
		}
		for name, url := range map[string]string{
			"a host cursor on Control's subjects":    subjects + "?after=" + *first.next,
			"Control's subjects cursor on a host's":  items + "?after=" + subjectsCursor,
			"a cursor from another of the host's":    base + "/api/v1/tenants/alpha/items/other?after=" + *first.next,
			"a cursor from a list across tenants":    items + "?after=" + *readListPage(t, base+"/api/v1/cross-tenant/items/all?limit=1", token).next,
			"a cursor under other filters":           items + "?after=" + *filtered.next,
			"a cursor without the filter it was for": items + "?prefix=r-&after=" + *first.next,
		} {
			p := readListPage(t, url, token)
			params, _ := p.body["parameters"].(map[string]any)
			if p.status != http.StatusBadRequest || params["field"] != "after" || params["problem"] != "invalid" {
				t.Errorf("%s answered %d %v, want 400 naming after", name, p.status, p.body)
			}
		}
		if p := readListPage(t, items+"?prefix=r-&after="+*filtered.next, token); p.status != http.StatusOK {
			t.Errorf("a filtered cursor under its own filters answered %d %v", p.status, p.body)
		}
	})

	t.Run("no tenant and no set in a cursor", func(t *testing.T) {
		// A tenant route: the cursor holds a position, and replayed under
		// another tenant's path it continues that tenant's list.
		alpha := readListPage(t, items+"?limit=1", token)
		assertNoScope(t, *alpha.next, "test/enterprise:items")
		bravo := readListPage(t, base+"/api/v1/tenants/bravo/items?after="+*alpha.next, token)
		if !slices.Equal(bravo.items, []string{"r-2", "r-4"}) {
			t.Errorf("alpha's cursor under bravo's path answered %v, want bravo's list after r-1", bravo.items)
		}

		// A cross-tenant route: the same cursor whatever set Control
		// resolved, and continued under any set.
		all := base + "/api/v1/cross-tenant/items/all"
		every := readListPage(t, all+"?limit=1", token)
		narrowed := readListPage(t, all+"?limit=1&tenant=alpha", token)
		if every.next == nil || narrowed.next == nil || *every.next != *narrowed.next {
			t.Fatalf("the first page's cursor differs by the set: %v, %v", every.body, narrowed.body)
		}
		assertNoScope(t, *every.next, "test/enterprise:items-all")
		if p := readListPage(t, all+"?tenant=bravo&after="+*every.next, token); !slices.Equal(p.items, []string{"r-2", "r-4"}) {
			t.Errorf("the cursor under bravo alone answered %v", p.items)
		}
		if p := readListPage(t, all+"?after="+*every.next, token); !slices.Equal(p.items, []string{"r-2", "r-3", "r-4", "r-5"}) {
			t.Errorf("the cursor under every tenant answered %v", p.items)
		}
	})
}

// mintCursor is a cursor in M26's documented encoding.
func mintCursor(t *testing.T, list, filters string, key ...string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"v": 1, "l": list, "f": filters, "k": key})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// assertNoScope decodes a cursor and fails if it holds anything but M26's four
// members, or names any tenant.
func assertNoScope(t *testing.T, cursor, list string) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("cursor %q: %v", cursor, err)
	}
	var c map[string]any
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("cursor %s: %v", raw, err)
	}
	keys := slices.Sorted(func(yield func(string) bool) {
		for k := range c {
			if !yield(k) {
				return
			}
		}
	})
	if !slices.Equal(keys, []string{"f", "k", "l", "v"}) || c["l"] != list {
		t.Errorf("cursor %s: members %v, list %v; want f, k, l, v naming %s", raw, keys, c["l"], list)
	}
	for _, tenant := range []string{"alpha", "bravo", "default"} {
		if strings.Contains(string(raw), tenant) {
			t.Errorf("cursor %s names tenant %s", raw, tenant)
		}
	}
}

func TestAHostListMisusedIsAnOutageAndLogged(t *testing.T) {
	base, federation, logs := startHostWith(t, listHostOptions())
	token := issue(t, federation, "alpha", map[store.Tenant]identity.RoleSet{"alpha": {identity.RoleAuditor}})

	if p := readListPage(t, base+"/api/v1/tenants/alpha/broken/well-formed", token); p.status != http.StatusOK {
		t.Fatalf("the control case answered %d %v", p.status, p.body)
	}
	for name, problem := range map[string]string{
		"no-name":          "the list has no name",
		"no-key":           "the list's order key has no components",
		"unread":           "the List was not read by ReadList",
		"no-member":        "the member is empty, truncated or next",
		"member-truncated": "the member is empty, truncated or next",
		"member-next":      "the member is empty, truncated or next",
		"extra-truncated":  "an extra member collides with truncated",
		"extra-next":       "an extra member collides with next",
		"extra-member":     "an extra member collides with items",
		"not-a-list":       "the items are not a list",
		"more-no-last":     "the page continues and names no last item",
		"short-last":       "the last item's key has the wrong number of components",
		"long-last":        "the last item's key has the wrong number of components",
	} {
		t.Run(name, func(t *testing.T) {
			p := readListPage(t, base+"/api/v1/tenants/alpha/broken/"+name, token)
			id, _ := p.body["correlation_id"].(string)
			if p.status != http.StatusInternalServerError || p.body["code"] != "internal" || id == "" {
				t.Fatalf("answered %d %v, want 500 internal with a correlation id", p.status, p.body)
			}
			line := refusedListLine(t, logs.String(), id)
			if line["problem"] != problem || line["provider"] != hostProvider || line["level"] != "ERROR" {
				t.Errorf("logged %v, want the problem %q at ERROR naming %s", line, problem, hostProvider)
			}
		})
	}
}

// refusedListLine returns the refused-list log line for one correlation id.
func refusedListLine(t *testing.T, logs, id string) map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(logs))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		var line map[string]any
		if json.Unmarshal(scanner.Bytes(), &line) == nil &&
			line["event"] == "northbound_host_list_refused" && line["correlation_id"] == id {
			return line
		}
	}
	t.Fatalf("no northbound_host_list_refused line for %s in the log:\n%s", id, logs)
	return nil
}

func TestTheListFacadeOutsideAHostRouteIsAnOutage(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/list?"+url.Values{"limit": {"1"}}.Encode(), nil)
	w := httptest.NewRecorder()
	if _, ok := server.ReadList(w, r, "items", 1); ok || w.Code != http.StatusInternalServerError {
		t.Errorf("ReadList outside a host route answered ok=%t %d, want 500", ok, w.Code)
	}
	w = httptest.NewRecorder()
	server.List{}.Write(w, r, "items", []string{}, false, nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("List.Write outside a host route answered %d, want 500", w.Code)
	}
}
