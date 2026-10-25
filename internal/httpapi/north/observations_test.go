// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
)

// What each build declares and what each target was seen to take (0032): three
// lists in M26's shape. A target's two observations are two lists (M26.5),
// and the combined route they replace is gone.

const (
	declarationsPath = "/fleet/algorithms"
	rungsPath        = "/fleet/target-observations/rungs"
	keyExchangesPath = "/fleet/target-observations/key-exchanges"
)

func tenantPath(tenant store.Tenant, rest string) string {
	return "/api/v1/tenants/" + string(tenant) + rest
}

func seedDeclaration(t *testing.T, f *serverFixture, tenant store.Tenant, proxyID, body string) {
	t.Helper()
	if _, err := f.st.ProxyAlgorithmDeclarations().Put(t.Context(), tenant, store.ProxyAlgorithmDeclaration{
		ProxyID: proxyID, Declaration: []byte(body), Digest: string(tenant) + "-" + proxyID + "-" + body, DeclaredAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed declaration %s/%s: %v", tenant, proxyID, err)
	}
}

// seedRungs stores one rung observation per "host:port:platform", observed at
// observedAt and reported by the tenant's name, so a row shows whose it is.
func seedRungs(t *testing.T, f *serverFixture, tenant store.Tenant, observedAt time.Time, keys ...string) {
	t.Helper()
	for _, k := range keys {
		parts := strings.SplitN(k, ":", 3)
		port, err := strconv.ParseInt(parts[1], 10, 32)
		if err != nil || len(parts) != 3 {
			t.Fatalf("rung key %q is not host:port:platform", k)
		}
		if err := f.st.TargetCapabilities().Put(t.Context(), tenant, store.TargetCapabilityRecord{
			Hostname: parts[0], Port: int32(port), Platform: parts[2], Execution: []string{"account-confined"},
			ObservedAt: observedAt, ReportedBy: string(tenant),
		}); err != nil {
			t.Fatalf("seed rung %s/%s: %v", tenant, k, err)
		}
	}
}

// seedKex stores one key-exchange observation per "host:port", reported by the
// tenant's name.
func seedKex(t *testing.T, f *serverFixture, tenant store.Tenant, observedAt time.Time, keys ...string) {
	t.Helper()
	for _, k := range keys {
		host, rawPort, ok := strings.Cut(k, ":")
		port, err := strconv.ParseInt(rawPort, 10, 32)
		if !ok || err != nil {
			t.Fatalf("key-exchange key %q is not host:port", k)
		}
		if err := f.st.TargetKexObservations().Put(t.Context(), tenant, store.TargetKexObservation{
			Hostname: host, Port: int32(port), FloorMet: "modern-kex", Negotiated: "curve25519-sha256",
			ObservedAt: observedAt, ReportedBy: string(tenant),
		}); err != nil {
			t.Fatalf("seed key exchange %s/%s: %v", tenant, k, err)
		}
	}
}

// pageMembers reads one page as its raw members.
func pageMembers(t *testing.T, resp *http.Response) map[string]json.RawMessage {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("answered %d, want 200: %s", resp.StatusCode, decodeError(t, resp).Message)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("body: %v", err)
	}
	return body
}

// Each list answers its own members and nothing else, and the members that
// describe the list — `ttl_seconds`, `floor_levels` — are the same on every
// page, the last and an empty one included (M26.3).
func TestTheObservationListsDescribeThemselvesOnEveryPage(t *testing.T) {
	f := newServer(t)
	now := time.Now()
	for _, id := range []string{"p-1", "p-2", "p-3"} {
		seedDeclaration(t, f, "tenant-a", id, `{}`)
	}
	seedRungs(t, f, "tenant-a", now, "h-1:22:", "h-1:22:linux", "h-2:22:")
	seedKex(t, f, "tenant-a", now, "h-1:22", "h-1:2222", "h-2:22")

	for _, l := range []struct {
		path      string
		member    string
		describes []string
	}{
		{declarationsPath, "declarations", nil},
		{rungsPath, "rungs", []string{"ttl_seconds"}},
		{keyExchangesPath, "key_exchanges", []string{"ttl_seconds", "floor_levels"}},
	} {
		for _, tenant := range []store.Tenant{"tenant-a", "tenant-b"} {
			t.Run(string(tenant)+l.path, func(t *testing.T) {
				token := adminToken(t, f, tenant)
				first := map[string]string{}
				next := ""
				for pages := 0; ; pages++ {
					if pages > 3 {
						t.Fatal("the walk ran past three pages")
					}
					q := "?limit=2"
					if next != "" {
						q += "&after=" + next
					}
					body := pageMembers(t, f.do(t, "GET", tenantPath(tenant, l.path)+q, token, nil))
					var truncated bool
					if err := json.Unmarshal(body["truncated"], &truncated); err != nil {
						t.Fatalf("page %d: truncated is %s", pages, body["truncated"])
					}
					want := append([]string{l.member, "truncated"}, l.describes...)
					if truncated {
						want = append(want, "next")
					}
					if got := slices.Sorted(maps.Keys(body)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
						t.Fatalf("page %d answered members %v, want %v", pages, got, slices.Sorted(slices.Values(want)))
					}
					for _, member := range l.describes {
						if pages == 0 {
							first[member] = string(body[member])
						} else if string(body[member]) != first[member] {
							t.Errorf("page %d's %s is %s, page 0's was %s: it describes the list, not the page",
								pages, member, body[member], first[member])
						}
					}
					if !truncated {
						break
					}
					_ = json.Unmarshal(body["next"], &next)
				}
				if l.member == "key_exchanges" && first["floor_levels"] != `["modern-kex","pq-hybrid-kex"]` {
					t.Errorf("floor_levels is %s, want the two levels a route may name", first["floor_levels"])
				}
				if ttl, ok := first["ttl_seconds"]; ok && ttl != strconv.Itoa(int((24*time.Hour)/time.Second)) {
					t.Errorf("ttl_seconds is %s, want the target observation window", ttl)
				}
			})
		}
	}
}

// Freshness is judged per item, in the one window the list states, exactly as
// the decision path judges it: within the window is fresh, past it or undated
// is not (M17).
func TestEachObservationCarriesItsOwnFreshness(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	now := time.Now()
	seedRungs(t, f, "tenant-a", now.Add(-time.Hour), "fresh:22:")
	seedRungs(t, f, "tenant-a", now.Add(-48*time.Hour), "stale:22:")
	seedRungs(t, f, "tenant-a", time.Time{}, "undated:22:")
	seedKex(t, f, "tenant-a", now.Add(-time.Hour), "fresh:22")
	seedKex(t, f, "tenant-a", now.Add(-48*time.Hour), "stale:22")

	for path, member := range map[string]string{rungsPath: "rungs", keyExchangesPath: "key_exchanges"} {
		t.Run(member, func(t *testing.T) {
			got := map[string]bool{}
			p := readListPage(t, f.do(t, "GET", tenantPath("tenant-a", path), token, nil), member)
			for _, item := range p.items {
				var v struct {
					Target string `json:"target"`
					Fresh  *bool  `json:"fresh"`
				}
				if err := json.Unmarshal(item, &v); err != nil || v.Fresh == nil {
					t.Fatalf("item %s carries no fresh: %v", item, err)
				}
				got[v.Target] = *v.Fresh
			}
			want := map[string]bool{"fresh": true, "stale": false}
			if member == "rungs" {
				want["undated"] = false
			}
			if !maps.Equal(got, want) {
				t.Errorf("freshness by target is %v, want %v", got, want)
			}
		})
	}
}

// The combined route is withdrawn (M26.5): it answers Control's 404 like any
// path this server does not serve, and the router no longer has it.
func TestTheCombinedObservationRouteIsWithdrawn(t *testing.T) {
	f := newServer(t)
	seedRungs(t, f, "tenant-a", time.Now(), "h-1:22:")
	resp := f.do(t, "GET", tenantPath("tenant-a", "/fleet/target-observations"), adminToken(t, f, "tenant-a"), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the withdrawn route answered %d, want 404", resp.StatusCode)
	}
	if e := decodeError(t, resp); e.Code != "not_found" || e.CorrelationID == "" {
		t.Errorf("the withdrawn route answered %+v, want Control's not_found envelope with a correlation id", e)
	}
	for _, route := range f.server.Routes() {
		if strings.HasSuffix(route.Pattern, "/fleet/target-observations") {
			t.Errorf("the router still serves %s", route)
		}
	}
}

// Two tenants (M18): no list reads across. Both tenants hold rows under the
// same keys; a position from tenant-a's list continues tenant-b's with
// tenant-b's rows alone, and a tenant-a credential carrying it is refused
// tenant-b.
func TestNoObservationListReadsAcrossTenants(t *testing.T) {
	f := newServer(t)
	now := time.Now()
	for _, tenant := range []store.Tenant{"tenant-a", "tenant-b"} {
		for _, id := range []string{"p-1", "p-2", "p-3"} {
			// The level names the tenant, so a row shows whose it is.
			seedDeclaration(t, f, tenant, id, `{"algorithm_floors":[{"level":"`+string(tenant)+`","key_exchanges":[]}]}`)
		}
		seedRungs(t, f, tenant, now, "h-1:22:", "h-1:22:linux", "h-1:2222:")
		seedKex(t, f, tenant, now, "h-1:22", "h-1:2222", "h-2:22")
	}
	both := adminToken(t, f, "tenant-a", "tenant-b")
	onlyA := adminToken(t, f, "tenant-a")

	whose := func(member string, item json.RawMessage) string {
		var v struct {
			ReportedBy  string `json:"reported_by"`
			Declaration struct {
				Floors []struct {
					Level string `json:"level"`
				} `json:"algorithm_floors"`
			} `json:"declaration"`
		}
		if err := json.Unmarshal(item, &v); err != nil {
			t.Fatalf("%s item %s: %v", member, item, err)
		}
		if member == "declarations" {
			if len(v.Declaration.Floors) != 1 {
				t.Fatalf("declaration %s has no floor naming its tenant", item)
			}
			return v.Declaration.Floors[0].Level
		}
		return v.ReportedBy
	}
	for path, member := range map[string]string{
		declarationsPath: "declarations", rungsPath: "rungs", keyExchangesPath: "key_exchanges",
	} {
		t.Run(member, func(t *testing.T) {
			first := readListPage(t, f.do(t, "GET", tenantPath("tenant-a", path)+"?limit=1", both, nil), member)
			if !first.truncated || whose(member, first.items[0]) != "tenant-a" {
				t.Fatalf("tenant-a's first page is %s (truncated %t), want tenant-a's and more", first.items, first.truncated)
			}
			rest := readListPage(t, f.do(t, "GET", tenantPath("tenant-b", path)+"?after="+first.next, both, nil), member)
			if len(rest.items) != 2 || rest.truncated {
				t.Fatalf("tenant-b after tenant-a's first item answered %d items (truncated %t), want tenant-b's other two",
					len(rest.items), rest.truncated)
			}
			for _, item := range rest.items {
				if got := whose(member, item); got != "tenant-b" {
					t.Errorf("tenant-b's list holds %s's item %s", got, item)
				}
			}
			resp := f.do(t, "GET", tenantPath("tenant-b", path)+"?after="+first.next, onlyA, nil)
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("a tenant-a credential carrying a cursor read tenant-b's %s: %d", member, resp.StatusCode)
			}
		})
	}
}

// A port is held in 32 bits. A cursor naming one past that is not a position
// in either observation list: refused as any cursor the list does not
// continue, never handed to the store to fail as an outage (M26.2, M11).
func TestAPortCursorOutsideItsRangeIsRefused(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	now := time.Now()
	seedRungs(t, f, "tenant-a", now, "h-1:22:", "h-2:22:")
	seedKex(t, f, "tenant-a", now, "h-1:22", "h-2:22")

	for path, member := range map[string]string{rungsPath: "rungs", keyExchangesPath: "key_exchanges"} {
		t.Run(member, func(t *testing.T) {
			first := readListPage(t, f.do(t, "GET", tenantPath("tenant-a", path)+"?limit=1", token, nil), member)
			raw, err := base64.RawURLEncoding.DecodeString(first.next)
			if err != nil {
				t.Fatalf("next %q: %v", first.next, err)
			}
			var c map[string]any
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatalf("next decodes to %s: %v", raw, err)
			}
			k, _ := c["k"].([]any)
			if len(k) < 2 || k[0] != "h-1" || k[1] != "22" {
				t.Fatalf("the cursor's key is %v, want h-1 and port 22 first", c["k"])
			}
			for _, port := range []string{"2147483647", "2147483648", "-2147483649", "4294967318"} {
				k[1] = port
				forged, err := json.Marshal(c)
				if err != nil {
					t.Fatal(err)
				}
				resp := f.do(t, "GET", tenantPath("tenant-a", path)+"?after="+base64.RawURLEncoding.EncodeToString(forged), token, nil)
				if port == "2147483647" {
					// The largest port a column holds is a position, past
					// every row of h-1.
					if p := readListPage(t, resp, member); len(p.items) != 1 || p.truncated {
						t.Errorf("after (h-1, %s): %d items (truncated %t), want h-2's one", port, len(p.items), p.truncated)
					}
					continue
				}
				expectInvalid(t, resp, "after", "invalid", "")
			}
		})
	}
}

// A declaration on the page that does not decode is this server's own write
// gone wrong: the page is an outage with its correlation id, never a page short
// of it (M26.6, M11).
func TestAnUndecodableDeclarationFailsItsPage(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	seedDeclaration(t, f, "tenant-a", "p-1", `{}`)
	seedDeclaration(t, f, "tenant-a", "p-2", `{"algorithm_floors":`)

	resp := f.do(t, "GET", tenantPath("tenant-a", declarationsPath), token, nil)
	if resp.StatusCode < 500 {
		t.Fatalf("a page holding an undecodable declaration answered %d, want a 5xx", resp.StatusCode)
	}
	if e := decodeError(t, resp); e.CorrelationID == "" {
		t.Errorf("the outage carries no correlation id: %+v", e)
	}
	// The page before it is whole.
	if p := readListPage(t, f.do(t, "GET", tenantPath("tenant-a", declarationsPath)+"?limit=1", token, nil), "declarations"); len(p.items) != 1 || !p.truncated {
		t.Errorf("the page before the undecodable declaration: %d items (truncated %t), want p-1 and more", len(p.items), p.truncated)
	}
}
