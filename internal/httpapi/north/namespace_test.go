// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
)

// controlTenantPrefix is where Control's own tenant routes live.
const controlTenantPrefix = north.APIPrefix + "/tenants/{" + north.TenantPathParam + "}/"

// overlap is one way a host pattern can be written against one of Control's
// routes.
type overlap struct {
	shape, method, pattern string
}

// hostMethods is every method a host route may serve.
var hostMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// overlapsOf builds every overlap shape against a route of Control's, as a
// pattern a host would write. rel is Control's pattern relative to its tenant
// prefix, or "" for a route outside it, which a host can reach only by
// climbing or by an absolute pattern. served is every method Control serves
// on the pattern.
func overlapsOf(route north.RouteInfo, served []string) []overlap {
	full := route.Pattern
	rel, inTenant := strings.CutPrefix(full, controlTenantPrefix)
	escapes := []overlap{
		{"an absolute pattern", route.Method, full},
		{"a climb out", route.Method, "../../" + strings.TrimPrefix(full, north.APIPrefix+"/")},
	}
	if !inTenant {
		return escapes
	}
	segments := strings.Split(rel, "/")
	wild := func(s string) bool { return strings.HasPrefix(s, "{") }
	mapped := func(f func(i int, s string) string) string {
		out := make([]string, len(segments))
		for i, s := range segments {
			out[i] = f(i, s)
		}
		return strings.Join(out, "/")
	}
	hasWild := slices.ContainsFunc(segments, wild)
	hasLiteral := slices.ContainsFunc(segments, func(s string) bool { return !wild(s) })

	out := append(escapes,
		overlap{"Control's exact pattern", route.Method, rel},
		overlap{"a multi-segment wildcard over the whole tree", route.Method, "{hrest...}"},
		overlap{"a multi-segment wildcard inside its family", route.Method, segments[0] + "/{hrest...}"},
		overlap{"a multi-segment wildcard below it", route.Method, rel + "/{hrest...}"},
		overlap{"a trailing-slash subtree over its family", route.Method, segments[0] + "/"},
		overlap{"a trailing-slash subtree below it", route.Method, rel + "/"},
		overlap{"the tenant root", route.Method, "{$}"},
		overlap{"its path with a trailing slash only", route.Method, rel + "/{$}"},
		overlap{"a case variant", route.Method, strings.ToUpper(rel[:1]) + rel[1:]},
		overlap{"a pattern the mux would clean (.)", route.Method, "./" + rel},
		overlap{"a pattern the mux would clean (//)", route.Method, segments[0] + "//" + strings.Join(segments[1:], "/")},
	)
	for _, m := range hostMethods {
		if !slices.Contains(served, m) {
			out = append(out, overlap{"a method beside it", m, rel})
			break
		}
	}
	if hasWild {
		out = append(out, overlap{"a literal under its wildcards", route.Method,
			mapped(func(_ int, s string) string {
				if wild(s) {
					return "x"
				}
				return s
			})})
	}
	if hasLiteral {
		out = append(out, overlap{"a wildcard over its literals", route.Method,
			mapped(func(i int, s string) string {
				if wild(s) {
					return s
				}
				return fmt.Sprintf("{hw%d}", i)
			})})
	}
	if hasWild && hasLiteral {
		// Neither pattern is more specific: /grants/x matches both
		// grants/{grant} and {hw}/x.
		first := slices.IndexFunc(segments, func(s string) bool { return !wild(s) })
		out = append(out, overlap{"a mux conflict", route.Method,
			mapped(func(i int, s string) string {
				switch {
				case wild(s):
					return "x"
				case i == first:
					return "{hw}"
				}
				return s
			})})
	}
	return out
}

// instance is a path Control's pattern serves, with every wildcard "x" — the
// value the literal and conflicting shapes above write — and the tenant
// tenant-a.
func instance(pattern string) string {
	parts := strings.Split(pattern, "/")
	for i, s := range parts {
		switch {
		case s == "{"+north.TenantPathParam+"}":
			parts[i] = "tenant-a"
		case strings.HasPrefix(s, "{"):
			parts[i] = "x"
		}
	}
	return strings.Join(parts, "/")
}

// answer is everything a test can see about who answered a request.
type answer struct {
	Status   int
	ServedBy string
	Location string
	Code     string
}

func ask(h http.Handler, method, path string) answer {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	a := answer{Status: rec.Code, ServedBy: rec.Header().Get(north.ServedByHeader), Location: rec.Header().Get("Location")}
	var e north.Error
	if json.Unmarshal(rec.Body.Bytes(), &e) == nil {
		a.Code = e.Code
	}
	return a
}

// requestsBeside are the requests that must be answered exactly as they are with no
// host mounted: an instance of Control's route under its own method and under
// one it does not serve, and paths under Control's prefix that nobody serves.
func requestsBeside(route north.RouteInfo, served []string) [][2]string {
	path := instance(route.Pattern)
	tenantRoot := instance(controlTenantPrefix)
	out := [][2]string{
		{route.Method, path},
		{route.Method, path + "/"},
		{route.Method, path + "/hoplock-unserved"},
		{route.Method, tenantRoot},
		{route.Method, tenantRoot + "hoplock-unserved"},
		{route.Method, north.APIPrefix + "/hoplock-unserved"},
	}
	for _, m := range hostMethods {
		if !slices.Contains(served, m) {
			out = append(out, [2]string{m, path})
			break
		}
	}
	return out
}

// Every way a host pattern can overlap one of Control's routes, tried against
// every route of Control's real table: each shape is refused at start-up, or
// is mounted and changes the answer to no request on Control's paths — the
// paths Control serves, under their own method and under one they do not, and
// the paths under its prefix that nobody serves, which stay Control's 404.
func TestEveryWayAHostCanOverlapControlIsRefusedOrAnswersNothingOfControls(t *testing.T) {
	table, err := north.Table("", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	bare, err := north.ServeTable("", nil)
	if err != nil {
		t.Fatal(err)
	}
	served := map[string][]string{}
	for _, route := range table {
		served[route.Pattern] = append(served[route.Pattern], route.Method)
	}
	hostAnswer := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(north.ServedByHeader, "host")
		w.WriteHeader(http.StatusTeapot)
	})

	verdicts := map[string]map[string]int{}
	walked := 0
	for _, route := range table {
		walked++
		for _, o := range overlapsOf(route, served[route.Pattern]) {
			host := north.HostRoute{Method: o.method, Pattern: o.pattern, Access: north.AccessTenant,
				Permission: identity.PermAuditRead, Summary: "x", Handler: hostAnswer}
			verdict := "refused"
			if north.CheckHost(testHostProvider, []north.HostRoute{host}, nil) == nil {
				verdict = "accepted"
				h, err := north.ServeTable(testHostProvider, []north.HostRoute{host})
				if err != nil {
					t.Fatalf("%s %s: CheckHost accepted what ServeTable refused: %v", o.method, o.pattern, err)
				}
				for _, p := range requestsBeside(route, served[route.Pattern]) {
					if got, want := ask(h, p[0], p[1]), ask(bare, p[0], p[1]); got != want {
						verdict = "takes from Control"
						t.Errorf("%s (%s %s) beside %s: %s %s answered %+v, want Control's %+v",
							o.shape, o.method, o.pattern, route, p[0], p[1], got, want)
					}
				}
			}
			if verdicts[o.shape] == nil {
				verdicts[o.shape] = map[string]int{}
			}
			verdicts[o.shape][verdict]++
		}
	}
	if walked < 70 {
		t.Fatalf("walked %d of Control's routes, so this asserted little", walked)
	}
	shapes := make([]string, 0, len(verdicts))
	for s := range verdicts {
		shapes = append(shapes, s)
	}
	sort.Strings(shapes)
	for _, s := range shapes {
		t.Logf("%-55s %v", s, verdicts[s])
	}
}
