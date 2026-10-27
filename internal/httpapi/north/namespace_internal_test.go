// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/identity"
)

// The four namespaces (M27), held pairwise from each side's own table.

const testProvider = "test/host"

// namespace is one of the four, as a test reads it: a prefix, less the
// prefix nested in it that is another's.
type namespace struct {
	name, prefix, except string
}

func namespaces(provider string) []namespace {
	return []namespace{
		{"Control's API", ControlAPIPrefix, HostAPIPrefix},
		{"a host's API", HostAPIRoot(provider), ""},
		{"Control's console", ConsolePrefix, ""},
		{"a host's console", HostConsoleRoot(provider), ""},
	}
}

// in reports whether every path the pattern can match is in the namespace.
func (n namespace) in(pattern string) bool {
	return inside(pattern, n.prefix) && (n.except == "" || !overlaps(pattern, n.except))
}

// touches reports whether any path the pattern can match is in the
// namespace. A host's root is a host's API wherever it sits, so the nested
// prefix belongs to the namespace that names it, never to the one it is
// carved out of.
func (n namespace) touches(pattern string) bool {
	return overlaps(pattern, n.prefix) && (n.except == "" || !inside(pattern, n.except))
}

// mounted is one pattern a side mounts, and the namespace it must be in.
type mounted struct {
	pattern, owner string
}

// namespaceProblems names every mounted pattern outside its own namespace or
// touching another.
func namespaceProblems(provider string, all []mounted) []string {
	var problems []string
	for _, m := range all {
		for _, n := range namespaces(provider) {
			switch {
			case n.name == m.owner && !n.in(m.pattern):
				problems = append(problems, m.owner+" mounts "+m.pattern+", outside its own namespace")
			case n.name != m.owner && n.touches(m.pattern):
				problems = append(problems, m.owner+" mounts "+m.pattern+", which reaches into "+n.name)
			}
		}
	}
	return problems
}

// everythingMounted reads each side's patterns from its own table: Control's
// routes and the host's from the route listing, and each console's from the
// console listing.
func everythingMounted(s *Server) []mounted {
	var all []mounted
	for _, r := range s.router.Routes() {
		owner := "Control's API"
		if r.Provider != "" {
			owner = "a host's API"
		}
		all = append(all, mounted{r.Pattern, owner})
	}
	for _, c := range s.router.Consoles() {
		owner := "Control's console"
		if c.Provider != "" {
			owner = "a host's console"
		}
		all = append(all, mounted{c.Pattern, owner})
	}
	return all
}

func stubConsole(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>" + name + "</title>"))
	})
}

// testConsoles are a stub for each console, shaped as a single-page app is:
// an index at the root, assets, a catalogue, and a history fallback over the
// rest of the prefix.
func testConsoles() []Console {
	return []Console{
		{
			Patterns: []string{
				ConsolePrefix + "{$}", ConsolePrefix + "assets/", ConsolePrefix + "locales/en.json",
				ConsolePrefix + "{deep...}",
			},
			Catalogue: []string{"audit.title", "nav.fleet"},
			Handler:   stubConsole("control"),
		},
		{
			Provider:  testProvider,
			Patterns:  []string{HostConsoleRoot(testProvider) + "screens.js", HostConsoleRoot(testProvider) + "{deep...}"},
			Catalogue: []string{testProvider + ":approvals.title"},
			Handler:   stubConsole("host"),
		},
	}
}

func testRoutes() []HostRoute {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	return []HostRoute{
		{Method: "GET", Pattern: "reports/{report}", Access: AccessTenant, Permission: identity.PermAuditRead, Summary: "x", Handler: ok},
		{Method: "GET", Pattern: "{rest...}", Access: AccessTenant, Permission: identity.PermAuditRead, Summary: "x", Handler: ok},
		{Method: "GET", Pattern: "reports/all", Access: AccessCrossTenant, Permission: identity.PermAuditRead, Summary: "x", Handler: ok},
	}
}

// consoleServer is a table server with consoles mounted as New mounts them,
// served behind the correlation id as a real server is.
func consoleServer(t *testing.T, routes []HostRoute, consoles []Console) *Server {
	t.Helper()
	s, err := tableServer(testProvider, routes, nil, func(r Route) http.Handler { return r.Handler })
	if err != nil {
		t.Fatal(err)
	}
	if err := s.router.mountConsoles(testProvider, consoles); err != nil {
		t.Fatal(err)
	}
	s.handler = s.withCorrelationID(s.router)
	return s
}

// Every path each side mounts is inside its own prefix and reaches into no
// other's: Control's whole table, a host's routes of both classes, and a stub
// of each console. A route Control adds later is walked the day it is added.
func TestEveryPathIsInsideItsOwnNamespaceAndNoOther(t *testing.T) {
	s := consoleServer(t, testRoutes(), testConsoles())
	all := everythingMounted(s)
	owners := map[string]int{}
	for _, m := range all {
		owners[m.owner]++
	}
	if owners["Control's API"] < 70 || owners["a host's API"] != 3 || owners["Control's console"] != 4 || owners["a host's console"] != 2 {
		t.Fatalf("walked %v, so this asserted little", owners)
	}
	for _, p := range namespaceProblems(testProvider, all) {
		t.Error(p)
	}
}

// The walk above fails on each way a path can stray: shown once each.
func TestTheNamespaceWalkFailsOnEveryPathThatStrays(t *testing.T) {
	for _, m := range []mounted{
		{HostAPIPrefix + "x", "Control's API"},
		{HostTenantPrefix(testProvider) + "targets", "Control's API"},
		{APIPrefix + "/{family}/x", "Control's API"},
		{APIPrefix + "/", "Control's API"},
		{"/session", "Control's API"},
		{APIPrefix + "/tenants/{tenant}/reports", "a host's API"},
		{HostTenantPrefix("other/host") + "reports", "a host's API"},
		{ConsolePrefix + "x", "a host's API"},
		{"/{rest...}", "Control's console"},
		{"/", "Control's console"},
		{APIPrefix + "/console", "Control's console"},
		{HostConsoleRoot(testProvider) + "x", "Control's console"},
		{ConsolePrefix + "hosts/test/host/x", "a host's console"},
		{HostConsoleRoot("other/host") + "x", "a host's console"},
		{"/{prefix}/test/host/x", "a host's console"},
	} {
		if len(namespaceProblems(testProvider, []mounted{m})) == 0 {
			t.Errorf("%s mounting %s passed the walk", m.owner, m.pattern)
		}
	}
}

func decodeCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e Error
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		return ""
	}
	return e.Code
}

// Each namespace answers only inside its own prefix: a console's history
// fallback never answers an API path or another console's, an unknown path
// outside every prefix is Control's 404 envelope, and only the bare "/"
// redirects — to Control's console, once there is one.
func TestEachNamespaceAnswersOnlyInsideItsOwnPrefix(t *testing.T) {
	s := consoleServer(t, nil, testConsoles())
	for _, c := range []struct {
		path, want string
	}{
		{"/console/", "control"},
		{"/console/audit/records?subject=alice", "control"},
		{"/console/fleet/proxies/p-1", "control"},
		{HostConsoleRoot(testProvider) + "approvals/1", "host"},
		{HostConsoleRoot(testProvider) + "screens.js", "host"},
		{"/nowhere", "not_found"},
		{"/consoles/x", "not_found"},
		{"/console-x", "not_found"},
		{"/host-console/", "not_found"},
		{HostConsoleRoot("other/host") + "approvals/1", "not_found"},
		{"/api/v1/", "not_found"},
		{"/api/v1/nowhere", "not_found"},
		{"/api/v1/console/", "not_found"},
		{"/api/v1/tenants/a/nowhere", "not_found"},
		{HostAPIRoot(testProvider) + "nowhere", "not_found"},
		{HostAPIRoot("other/host") + "tenants/a/x", "not_found"},
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", c.path, nil))
		switch got := rec.Body.String(); {
		case c.want == "not_found":
			if rec.Code != http.StatusNotFound || decodeCode(t, rec) != CodeNotFound {
				t.Errorf("%s answered %d %q, want Control's 404 envelope", c.path, rec.Code, got)
			}
		case !strings.Contains(got, "<title>"+c.want+"</title>"):
			t.Errorf("%s answered %d %q, want %s's console", c.path, rec.Code, got, c.want)
		}
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != ConsolePrefix {
		t.Errorf("/ answered %d to %q, want a redirect to %s", rec.Code, rec.Header().Get("Location"), ConsolePrefix)
	}
	for _, path := range []string{"/x/", "/index.html"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d; only the bare / redirects", path, rec.Code)
		}
	}

	// With no console of Control's there is nowhere to send "/", so it is
	// Control's 404 like any other path outside every prefix.
	bare := consoleServer(t, nil, nil)
	rec = httptest.NewRecorder()
	bare.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusNotFound || decodeCode(t, rec) != CodeNotFound {
		t.Errorf("/ with no console answered %d, want Control's 404 envelope", rec.Code)
	}
}

// A console is refused at start-up when any pattern leaves its own prefix,
// and a host's catalogue when a key could be Control's (M21).
func TestAConsoleOutsideItsPrefixIsRefused(t *testing.T) {
	control, host := testConsoles()[0], testConsoles()[1]
	with := func(c Console, patterns ...string) Console {
		c.Patterns = patterns
		return c
	}
	keys := func(c Console, catalogue ...string) Console {
		c.Catalogue = catalogue
		return c
	}
	for name, consoles := range map[string][]Console{
		"Control's console at /":                       {with(control, "/")},
		"Control's console with a catch-all":           {with(control, "/{path...}")},
		"Control's console under the API":              {with(control, APIPrefix+"/console/")},
		"Control's console in a host's":                {with(control, HostConsoleRoot(testProvider)+"x")},
		"Control's console beside its prefix":          {with(control, "/consoles/")},
		"Control's console with a wildcard prefix":     {with(control, "/{c}/x")},
		"Control's console climbing out":               {with(control, ConsolePrefix+"../api/v1/x")},
		"Control's console with a . segment":           {with(control, ConsolePrefix+"./x")},
		"Control's console with an empty segment":      {with(control, ConsolePrefix+"a//b")},
		"Control's console with a method":              {with(control, "GET "+ConsolePrefix)},
		"Control's console with a host":                {with(control, "example.com"+ConsolePrefix)},
		"Control's console mounted twice":              {control, control},
		"a host's screens inside Control's console":    {control, with(host, ConsolePrefix+"hosts/test/host/")},
		"a host's screens under another host":          {with(host, HostConsoleRoot("other/host")+"x")},
		"a host's screens at the host-console root":    {with(host, HostConsolePrefix+"{deep...}")},
		"a host's screens under the API":               {with(host, HostAPIRoot(testProvider)+"screens/")},
		"a host that is not this server's":             {func() Console { c := host; c.Provider = "other/host"; return c }()},
		"a console with no handler":                    {{Patterns: []string{ConsolePrefix}}},
		"a console that mounts nothing":                {{Handler: stubConsole("x")}},
		"two consoles on one pattern":                  {control, with(control, ConsolePrefix+"assets/")},
		"a host key equal to Control's":                {control, keys(host, "audit.title")},
		"a host key equal to Control's, mounted first": {keys(host, "nav.fleet"), control},
		"a host key outside its provider":              {keys(host, "approvals.title")},
		"a host key under another provider":            {keys(host, "other/host:approvals.title")},
		"a host key that is only its provider":         {keys(host, testProvider+":")},
		"an empty host key":                            {keys(host, "")},
		"a key of Control's in a host's namespace":     {keys(control, "test/host:approvals.title")},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := tableServer(testProvider, nil, nil, func(r Route) http.Handler { return r.Handler })
			if err != nil {
				t.Fatal(err)
			}
			err = s.router.mountConsoles(testProvider, consoles)
			if err == nil {
				t.Fatalf("mounted anyway: %+v", s.router.Consoles())
			}
			// Refused as Control's, and not only as outside the host's
			// keys: Control's catalogue is judged first, whatever the
			// order the consoles were handed in.
			if strings.Contains(name, "equal to Control's") && !strings.Contains(err.Error(), "is Control's") {
				t.Errorf("refused, but not as Control's key: %v", err)
			}
		})
	}
}

// servedBy is the defence in depth behind the namespaces: if a route of
// Control's ever sat in a host's namespace, the host could still not take it.
// The router refuses such a route, so this test builds Control's paths by hand
// as if it had not.
func TestServedByStillRefusesAHostPathControlServes(t *testing.T) {
	s, err := tableServer("", nil, nil, func(r Route) http.Handler { return r.Handler })
	if err != nil {
		t.Fatal(err)
	}
	control := newControlPaths()
	control.add(HostTenantPrefix(testProvider) + "targets/{target}")
	control.add(HostTenantPrefix(testProvider) + "grants")
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, pattern := range []string{"targets/special", "targets/{t}", "grants"} {
		hr := HostRoute{Method: "PATCH", Pattern: pattern, Access: AccessTenant, Permission: identity.PermAuditRead, Handler: ok}
		if _, err := s.hostRoute(testProvider, hr, control); err == nil {
			t.Errorf("a host took %s, which Control serves in its namespace", pattern)
		}
	}
	hr := HostRoute{Method: "GET", Pattern: "reports", Access: AccessTenant, Permission: identity.PermAuditRead, Handler: ok}
	if _, err := s.hostRoute(testProvider, hr, control); err != nil {
		t.Errorf("a path Control does not serve was refused: %v", err)
	}
}
