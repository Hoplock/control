// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package declarative_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/accessctx/declarative"
	"github.com/hoplock/control/internal/config"
)

// fakeScanner is an external system nobody has heard of: a scan API that says
// which scan, if any, is running against a host.
type fakeScanner struct {
	mu       sync.Mutex
	status   int
	body     string
	delay    time.Duration
	location string
	seen     []*http.Request
}

func (f *fakeScanner) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, r.Clone(context.Background()))
	status, body, delay, location := f.status, f.body, f.delay, f.location
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if location != "" {
		w.Header().Set("Location", location)
	}
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (f *fakeScanner) answer(status int, body string) {
	f.mu.Lock()
	f.status, f.body = status, body
	f.mu.Unlock()
}

func (f *fakeScanner) last() *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) == 0 {
		return nil
	}
	return f.seen[len(f.seen)-1]
}

func ptr[T any](v T) *T { return &v }

func loopback() config.EgressConfig {
	return config.EgressConfig{AllowCIDRs: []string{"127.0.0.0/8", "::1/128"}}
}

func scannerConfig(url string) config.DeclarativeProviderConfig {
	return config.DeclarativeProviderConfig{
		Name: "acme-scanner",
		Probe: &config.DeclarativeProbeConfig{
			URL:     url + "/api/scans/active?host={{target.hostname}}&subject={{subject.id}}",
			Auth:    config.DeclarativeAuthConfig{Type: "bearer", TokenEnv: "ACME_TOKEN"},
			Timeout: 200 * time.Millisecond,
			TTL:     time.Minute,
			Confirm: []config.DeclarativeAssertion{
				{Path: "$.state", Equals: ptr("running")},
				{Path: "$.hosts", Contains: ptr("{{target.hostname}}")},
			},
			Reference:         "$.scan_id",
			WindowStart:       "$.started_at",
			WindowEnd:         "$.ends_at",
			AdditionalContext: "$.profile",
			Assertions:        map[string]string{"profile_name": "$.profile.name"},
		},
		Push: &config.DeclarativePushConfig{
			ID:                "$.event.id",
			Reference:         "$.scan.id",
			Subject:           "$.scan.operator",
			Targets:           "$.scan.hosts[*]",
			WindowStart:       "$.scan.start",
			WindowEnd:         "$.scan.end",
			IssuedAt:          "$.sent_at",
			AdditionalContext: "$.scan.profile",
		},
	}
}

func env(name string) (string, bool) {
	if name == "ACME_TOKEN" {
		return "s3cret-token", true
	}
	return "", false
}

func newProvider(t *testing.T, c config.DeclarativeProviderConfig, eg config.EgressConfig) *declarative.Provider {
	t.Helper()
	p, err := declarative.New(c, eg, declarative.Options{Getenv: env})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

var at = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func query(ref string) ext.AccessContextQuery {
	return ext.AccessContextQuery{
		Tenant:      "acme",
		Subject:     ext.Subject{ID: "svc-scanner&admin=1"},
		Target:      ext.Target{Hostname: "db01.example.com"},
		Privileges:  []string{"vuln-scan"},
		ExternalRef: ref,
		At:          at,
	}
}

const running = `{"state":"running","scan_id":"SCAN-7","hosts":["db01.example.com","db02.example.com"],
  "started_at":"2026-09-30T11:30:00Z","ends_at":1790786400,"profile":{"name":"authenticated-linux"}}`

// The four answers the acceptance criteria name — confirms, denies, times out,
// malformed — and the last two are different errors, so the decision record
// can tell a network from a bug.
func TestTheDeclarativeProbe(t *testing.T) {
	fake := &fakeScanner{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	p := newProvider(t, scannerConfig(srv.URL), loopback())

	if info := p.Describe(); info.Name != "acme-scanner" || !info.Probes || !info.Pushes {
		t.Fatalf("Describe = %+v", info)
	}

	t.Run("confirms", func(t *testing.T) {
		fake.answer(200, running)
		ev, err := p.Probe(t.Context(), query(""))
		if err != nil {
			t.Fatalf("Probe: %v", err)
		}
		if ev.State != ext.WindowConfirmed || ev.Reference != "SCAN-7" || ev.TTL != time.Minute {
			t.Errorf("evidence = %+v", ev)
		}
		if !ev.Window.NotBefore.Equal(time.Date(2026, 9, 30, 11, 30, 0, 0, time.UTC)) ||
			!ev.Window.NotAfter.Equal(time.Unix(1790786400, 0).UTC()) {
			t.Errorf("window = %+v; RFC 3339 and Unix seconds both read", ev.Window)
		}
		if string(ev.AdditionalContext) != `{"name":"authenticated-linux"}` || ev.Assertions["profile_name"] != "authenticated-linux" {
			t.Errorf("additional = %s, assertions = %v", ev.AdditionalContext, ev.Assertions)
		}
		req := fake.last()
		if got := req.Header.Get("Authorization"); got != "Bearer s3cret-token" {
			t.Errorf("Authorization = %q", got)
		}
		// A value cannot add a query parameter or a path segment.
		if q := req.URL.Query(); q.Get("subject") != "svc-scanner&admin=1" || q.Has("admin") || q.Get("host") != "db01.example.com" {
			t.Errorf("the query the scanner saw = %v", req.URL.RawQuery)
		}
	})

	t.Run("denies", func(t *testing.T) {
		fake.answer(200, strings.Replace(running, `"running"`, `"finished"`, 1))
		ev, err := p.Probe(t.Context(), query(""))
		if err != nil || ev.State != ext.WindowNotConfirmed || ev.Assertions["unmet"] != "$.state" {
			t.Errorf("a finished scan = %+v, %v; want not confirmed, naming the unmet assertion", ev, err)
		}
		fake.answer(404, `{"error":"no such scan"}`)
		if ev, err := p.Probe(t.Context(), query("SCAN-7")); err != nil || ev.State != ext.WindowNotConfirmed {
			t.Errorf("a 404 = %+v, %v; want not confirmed", ev, err)
		}
		// A host the scan does not name is not this window.
		other := query("")
		other.Target.Hostname = "web01.example.com"
		fake.answer(200, running)
		if ev, err := p.Probe(t.Context(), other); err != nil || ev.State != ext.WindowNotConfirmed {
			t.Errorf("another host = %+v, %v; want not confirmed", ev, err)
		}
	})

	t.Run("times out", func(t *testing.T) {
		fake.answer(200, running)
		fake.mu.Lock()
		fake.delay = 2 * time.Second
		fake.mu.Unlock()
		defer func() { fake.mu.Lock(); fake.delay = 0; fake.mu.Unlock() }()
		start := time.Now()
		_, err := p.Probe(t.Context(), query(""))
		if !ext.IsUnavailable(err) || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("a slow scanner = %v; want unavailable, wrapping the deadline", err)
		}
		if took := time.Since(start); took > time.Second {
			t.Errorf("the probe took %s; its timeout is 200ms", took)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		for name, body := range map[string]string{
			"not JSON":       `<html>maintenance</html>`,
			"a bad instant":  strings.Replace(running, `1790786400`, `"tomorrow"`, 1),
			"a list context": strings.Replace(running, `{"name":"authenticated-linux"}`, `["x"]`, 1),
			"no reference":   strings.Replace(running, `"scan_id":"SCAN-7",`, ``, 1),
		} {
			fake.answer(200, body)
			_, err := p.Probe(t.Context(), query(""))
			if !ext.IsMalformed(err) {
				t.Errorf("%s: %v; want malformed", name, err)
			}
			if errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("%s: a malformed answer reads as a timeout", name)
			}
		}
	})

	t.Run("an unhealthy or redirecting endpoint is unreachable", func(t *testing.T) {
		fake.answer(503, `{}`)
		if _, err := p.Probe(t.Context(), query("")); !ext.IsUnavailable(err) || errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("a 503 = %v; want unavailable", err)
		}
		fake.mu.Lock()
		fake.location = "http://169.254.169.254/latest/meta-data/"
		fake.mu.Unlock()
		fake.answer(302, ``)
		if _, err := p.Probe(t.Context(), query("")); !ext.IsUnavailable(err) {
			t.Errorf("a redirect = %v; want it refused, not followed", err)
		}
	})
}

// Egress is checked at dial time against the address actually dialled: a
// loopback endpoint is unreachable until the allow-list admits it.
func TestEgressIsRefusedUnlessAllowed(t *testing.T) {
	fake := &fakeScanner{}
	fake.answer(200, running)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	p := newProvider(t, scannerConfig(srv.URL), config.EgressConfig{})
	_, err := p.Probe(t.Context(), query(""))
	if !ext.IsUnavailable(err) || !strings.Contains(err.Error(), "egress") {
		t.Fatalf("a loopback endpoint with no allow-list = %v; want egress refused", err)
	}
	if fake.last() != nil {
		t.Error("the request reached the endpoint")
	}
	if _, err := newProvider(t, scannerConfig(srv.URL), loopback()).Probe(t.Context(), query("")); err != nil {
		t.Errorf("with the allow-list: %v", err)
	}
}

// What configuration loading cannot check, building the provider does — and a
// mistake names the provider and the key.
func TestAProviderThatCouldBePointedElsewhereDoesNotBuild(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*config.DeclarativeProviderConfig)
		key    string
	}{
		"a placeholder in the host": {func(c *config.DeclarativeProviderConfig) {
			c.Probe.URL = "https://{{tenant}}.scanner.example.com/scans"
		}, "probe.url"},
		"http to a public host": {func(c *config.DeclarativeProviderConfig) {
			c.Probe.URL = "http://scanner.example.com/scans"
		}, "probe.url"},
		"credentials in the URL": {func(c *config.DeclarativeProviderConfig) {
			c.Probe.URL = "https://user:pw@scanner.example.com/scans"
		}, "probe.url"},
		"an unknown placeholder": {func(c *config.DeclarativeProviderConfig) {
			c.Probe.URL = "https://scanner.example.com/scans/{{subject.password}}"
		}, "probe.url"},
		"a body placeholder outside a string": {func(c *config.DeclarativeProviderConfig) {
			c.Probe.Method, c.Probe.Body = "POST", `{"host": {{target.hostname}}}`
		}, "probe.body"},
		"a secret that is not set": {func(c *config.DeclarativeProviderConfig) {
			c.Probe.Auth.TokenEnv = "NOT_SET"
		}, "probe.auth"},
		"a path that is not one": {func(c *config.DeclarativeProviderConfig) {
			c.Probe.Reference = "scan_id"
		}, "probe.reference"},
		"two predicates": {func(c *config.DeclarativeProviderConfig) {
			c.Probe.Confirm[0].Exists = ptr(true)
		}, "probe.confirm[0]"},
		"a push path that is not one": {func(c *config.DeclarativeProviderConfig) {
			c.Push.ID = "$.event[x]"
		}, "push.id"},
	} {
		c := scannerConfig("https://scanner.example.com")
		tc.mutate(&c)
		_, err := declarative.New(c, loopback(), declarative.Options{Getenv: env})
		if err == nil || !strings.Contains(err.Error(), "providers[acme-scanner]."+tc.key) {
			t.Errorf("%s: err = %v; want it refused naming %s", name, err, tc.key)
		}
	}
}

// A push is read through the mapping, and judged by nobody here.
func TestTheDeclarativePushMapping(t *testing.T) {
	p := newProvider(t, scannerConfig("https://scanner.example.com"), loopback())
	body := `{"event":{"id":"evt-1"},"sent_at":"2026-09-30T11:59:00Z",
	  "scan":{"id":"SCAN-7","operator":"svc-scanner","hosts":["db01.example.com","*.db.example.com"],
	          "start":"2026-09-30T12:00:00Z","end":"2026-09-30T16:00:00Z","profile":"authenticated-linux"}}`
	a, err := p.Interpret(t.Context(), ext.AccessContextPush{Tenant: "acme", Body: []byte(body), ReceivedAt: at})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if a.ID != "evt-1" || a.Reference != "SCAN-7" || a.Subject != "svc-scanner" ||
		len(a.Targets) != 2 || a.Targets[1] != "*.db.example.com" ||
		!a.Window.NotAfter.Equal(time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)) ||
		!a.IssuedAt.Equal(time.Date(2026, 9, 30, 11, 59, 0, 0, time.UTC)) ||
		string(a.AdditionalContext) != `"authenticated-linux"` {
		t.Errorf("assertion = %+v (additional %s)", a, a.AdditionalContext)
	}

	for name, bad := range map[string]string{
		"not JSON":         `scan started`,
		"no id":            strings.Replace(body, `"event":{"id":"evt-1"},`, ``, 1),
		"a numeric target": strings.Replace(body, `"db01.example.com",`, `42,`, 1),
		"a list context":   strings.Replace(body, `"profile":"authenticated-linux"`, `"profile":[1]`, 1),
		"a bad end":        strings.Replace(body, `"end":"2026-09-30T16:00:00Z"`, `"end":"later"`, 1),
	} {
		if _, err := p.Interpret(t.Context(), ext.AccessContextPush{Body: []byte(bad)}); !ext.IsMalformed(err) {
			t.Errorf("%s: %v; want malformed", name, err)
		}
	}

	probeOnly := scannerConfig("https://scanner.example.com")
	probeOnly.Push = nil
	if _, err := newProvider(t, probeOnly, loopback()).Interpret(t.Context(), ext.AccessContextPush{Body: []byte(body)}); !ext.IsDisabled(err) {
		t.Errorf("a provider with no push mapping = %v; want disabled", err)
	}
	var info = newProvider(t, probeOnly, loopback()).Describe()
	if info.Pushes {
		t.Error("a probe-only provider says it takes pushes")
	}
	if _, err := json.Marshal(a); err != nil {
		t.Fatal(err)
	}
}
