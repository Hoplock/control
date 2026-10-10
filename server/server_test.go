// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
	"github.com/hoplock/control/server"
)

// A test host: what Hoplock Enterprise's main does, against a real database
// and real ports, so that what is proven is the start-up path a host takes and
// not a picture of it.

const (
	hostProvider = "test/enterprise"
	hostSection  = `
enterprise:
  licence_file: /etc/hoplock/licence
  seats: 25
`
)

// lockedBuffer is a log destination the server writes from its goroutines while
// the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// freeAddr returns a loopback address nothing is listening on.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

func configDoc(south, north, dsn string) string {
	return fmt.Sprintf(`
listeners:
  south: %q
  north: %q
database:
  dsn: %q
log:
  level: info
`, south, north, dsn)
}

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// testDatabase is a private, empty schema; the caller migrates it.
func testDatabase(t *testing.T) string {
	t.Helper()
	dsn := storetest.DSN(t)
	return storetest.DSNForSchema(dsn, storetest.NewSchema(t, dsn))
}

// sectionRecorder is a HostConfig that keeps what it was handed.
type sectionRecorder struct {
	mu       sync.Mutex
	sections map[string][]byte
}

func (s *sectionRecorder) config(section string, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sections == nil {
		s.sections = map[string][]byte{}
	}
	s.sections[section] = slices.Clone(raw)
	return nil
}

func (s *sectionRecorder) got() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sections
}

// fakeArchive is the extension the test host registers.
type fakeArchive struct{}

func (fakeArchive) Archive(context.Context, []ext.AuditRecord) error { return nil }
func (fakeArchive) Search(context.Context, ext.ArchiveQuery) (ext.ArchivePage, error) {
	return ext.ArchivePage{}, nil
}

// hostRoutes are the test host's two routes.
func hostRoutes() []server.Route {
	return []server.Route{
		{
			Method: "GET", Pattern: "licence",
			Permission: "license:read",
			Summary:    "the deployment's licence",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				caller, ok := server.CallerFrom(r.Context())
				if !ok {
					server.WriteError(w, r, http.StatusInternalServerError, "internal", nil, "no caller")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(caller)
			}),
		},
		{
			Method: "POST", Pattern: "reports/schedules",
			Permission: "report:write",
			Summary:    "schedule a report",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				server.WriteError(w, r, http.StatusConflict, "schedule_exists", nil, "that schedule exists")
			}),
		},
	}
}

func hostOptions(rec *sectionRecorder, registry *ext.Registry) server.Options {
	return server.Options{
		Provider:     hostProvider,
		Registry:     registry,
		HostSections: []string{"enterprise"},
		HostConfig:   rec.config,
		Routes:       hostRoutes(),
		ErrorCodes:   []string{"schedule_exists"},
	}
}

func migrate(t *testing.T, path string, o server.Options) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := server.Main(context.Background(), []string{"migrate", "--config", path}, &stdout, &stderr, o); err != nil {
		t.Fatalf("migrate: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "migration(s) applied") {
		t.Fatalf("migrate printed %q", stdout.String())
	}
}

// ---------------------------------------------------------------------------
// one start-up path
// ---------------------------------------------------------------------------

func TestAHostStartsWithItsExtensionItsSectionAndItsRoutes(t *testing.T) {
	dsn := testDatabase(t)
	south, north := freeAddr(t), freeAddr(t)
	path := writeFile(t, configDoc(south, north, dsn)+hostSection)

	rec := &sectionRecorder{}
	registry := ext.NewRegistry()
	if err := registry.RegisterArchiveStore(ext.Registration{Provider: hostProvider + "/archive"}, fakeArchive{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	o := hostOptions(rec, registry)
	migrate(t, path, o)
	if len(rec.got()) != 0 {
		t.Fatal("migrate handed the host its section; only the daemon does")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := &lockedBuffer{}
	done := make(chan error, 1)
	go func() { done <- server.Main(ctx, []string{"--config", path}, io.Discard, logs, o) }()

	base := "http://" + north
	waitServing(t, base+"/api/v1/session/methods", done, logs)

	// Both listeners serve. The south-bound one answers the contract's
	// envelope to a caller with no credential.
	resp := get(t, "http://"+south+"/v1/authorize", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the south-bound listener answered %d, want 401", resp.StatusCode)
	}

	// The host read its section before anything served.
	raw, ok := rec.got()["enterprise"]
	if !ok {
		t.Fatalf("HostConfig was not handed the enterprise section: %v", rec.got())
	}
	var section struct {
		LicenceFile string `yaml:"licence_file"`
		Seats       int    `yaml:"seats"`
	}
	if err := yaml.Unmarshal(raw, &section); err != nil || section.Seats != 25 || section.LicenceFile == "" {
		t.Errorf("the section came back as %+v (%v):\n%s", section, err, raw)
	}

	// The sealed set holds the host's extension AND Control's defaults, and
	// the log says which routes are not Control's.
	started := logLine(t, logs.String(), "starting")
	providers, _ := started["extension_providers"].([]any)
	for _, want := range []string{hostProvider + "/archive", ext.ProviderControl} {
		if !slices.Contains(providers, any(want)) {
			t.Errorf("extension_providers = %v, want %s among them", providers, want)
		}
	}
	if started["host"] != hostProvider {
		t.Errorf("the start-up line names host %v, want %s", started["host"], hostProvider)
	}
	if !strings.Contains(logs.String(), `"provider":"`+hostProvider+`"`) {
		t.Error("no north-bound route line names the host as its provider")
	}

	// A host route answers behind Control's middleware.
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	cred, principal, err := federation.IssueToken(ctx, "default", identity.TokenRequest{
		DisplayName: "auditor", Scopes: map[store.Tenant]identity.RoleSet{"default": {identity.RoleAuditor}},
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	resp = get(t, base+"/api/v1/tenants/default/licence", cred.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the host route answered %d", resp.StatusCode)
	}
	var caller server.Caller
	if err := json.NewDecoder(resp.Body).Decode(&caller); err != nil {
		t.Fatalf("caller: %v", err)
	}
	if caller.Tenant != "default" || caller.Principal != principal.ID || caller.CorrelationID == "" || caller.BreakGlass {
		t.Errorf("CallerFrom gave %+v", caller)
	}

	for _, c := range []struct {
		name, token string
		status      int
		code        string
	}{
		{"an auditor may not schedule a report", cred.String(), http.StatusForbidden, "forbidden"},
		{"nobody may without a credential", "", http.StatusUnauthorized, "unauthenticated"},
	} {
		resp := post(t, base+"/api/v1/tenants/default/reports/schedules", c.token)
		if resp.StatusCode != c.status {
			t.Errorf("%s: answered %d, want %d", c.name, resp.StatusCode, c.status)
		}
		if code := envelopeCode(t, resp); code != c.code {
			t.Errorf("%s: code %q, want %q", c.name, code, c.code)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Main returned %v after its context ended, want nil", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Main did not return after its context ended")
	}
}

func TestMigrateAcceptsAHostSectionAndStillRefusesAnUndeclaredKey(t *testing.T) {
	dsn := testDatabase(t)
	rec := &sectionRecorder{}
	o := hostOptions(rec, nil)
	doc := configDoc("127.0.0.1:0", "127.0.0.1:1", dsn)

	migrate(t, writeFile(t, doc+hostSection), o)

	var stdout, stderr bytes.Buffer
	err := server.Main(context.Background(),
		[]string{"migrate", "--config", writeFile(t, doc+hostSection+"surprise: 1\n")}, &stdout, &stderr, o)
	if err == nil || !strings.Contains(err.Error(), "field surprise not found") {
		t.Errorf("migrate with an undeclared key = %v, want the strict decoder's refusal", err)
	}

	// Control alone has no host sections, so the same file is refused.
	err = server.Main(context.Background(),
		[]string{"migrate", "--config", writeFile(t, doc+hostSection)}, &stdout, &stderr, server.Options{})
	if err == nil || !strings.Contains(err.Error(), "field enterprise not found") {
		t.Errorf("Control alone with an enterprise section = %v, want it refused", err)
	}
}

var errLicence = errors.New("the licence file names no seats")

func TestAHostConfigErrorFailsRunBeforeAnyListenerBinds(t *testing.T) {
	south, north := freeAddr(t), freeAddr(t)
	// The database is unreachable on purpose: reaching for it would fail
	// differently, so the error proves the host's refusal came first.
	doc := configDoc(south, north, "postgres://nobody@127.0.0.1:1/none?sslmode=disable") + hostSection

	o := hostOptions(&sectionRecorder{}, nil)
	o.HostConfig = func(string, []byte) error { return errLicence }
	err := server.Run(context.Background(), strings.NewReader(doc), o)
	if !errors.Is(err, errLicence) {
		t.Fatalf("Run = %v, want the host's own error", err)
	}

	for _, addr := range []string{south, north} {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Errorf("%s was bound by a server that refused to start: %v", addr, err)
			continue
		}
		_ = l.Close()
	}
}

func TestAHostThatShadowsListenersIsRefusedBeforeTheFileIsRead(t *testing.T) {
	o := hostOptions(&sectionRecorder{}, nil)
	o.HostSections = []string{"listeners"}

	if err := server.Run(context.Background(), unreadable{t}, o); err == nil {
		t.Fatal("Run accepted a host section named listeners")
	}
	absent := filepath.Join(t.TempDir(), "absent.yaml")
	err := server.Main(context.Background(), []string{"--config", absent}, io.Discard, io.Discard, o)
	if err == nil || strings.Contains(err.Error(), "absent.yaml") {
		t.Fatalf("Main = %v, want the section refused before the file was opened", err)
	}
}

type unreadable struct{ t *testing.T }

func (u unreadable) Read([]byte) (int, error) {
	u.t.Fatal("the configuration was read before the host was checked")
	return 0, io.EOF
}

// Each of these is a host that can never start, refused before anything is
// read, opened or bound — which is why each passes a configuration path that
// does not exist and still expects the host's refusal rather than the file's.
func TestAHostThatCannotStartIsRefusedAtOnce(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent.yaml")
	for name, mutate := range map[string]func(*server.Options){
		"routes with no provider":         func(o *server.Options) { o.Provider = "" },
		"Control's own provider name":     func(o *server.Options) { o.Provider = ext.ProviderControl },
		"sections with no HostConfig":     func(o *server.Options) { o.HostConfig = nil },
		"a permission Control lacks":      func(o *server.Options) { o.Routes[0].Permission = "licence:invent" },
		"a pattern naming the tenant":     func(o *server.Options) { o.Routes[0].Pattern = "{tenant}/licence" },
		"a route on Control's own path":   func(o *server.Options) { o.Routes[0].Pattern = "grants" },
		"a mux conflict":                  func(o *server.Options) { o.Routes[0].Pattern = "grants/{id}" },
		"an error code Control owns":      func(o *server.Options) { o.ErrorCodes = []string{"forbidden"} },
		"an error code not in snake_case": func(o *server.Options) { o.ErrorCodes = []string{"ScheduleExists"} },
	} {
		t.Run(name, func(t *testing.T) {
			o := hostOptions(&sectionRecorder{}, nil)
			mutate(&o)
			err := server.Main(context.Background(), []string{"--config", absent}, io.Discard, io.Discard, o)
			if err == nil {
				t.Fatal("Main accepted the host")
			}
			if strings.Contains(err.Error(), "absent.yaml") {
				t.Fatalf("the file was read before the host was refused: %v", err)
			}
		})
	}
}

func TestControlAloneIsUnchanged(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := server.Main(context.Background(), []string{"--version"}, &stdout, &stderr, server.Options{}); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if !strings.HasPrefix(stdout.String(), "hoplock-control ") {
		t.Errorf("--version printed %q", stdout.String())
	}
	err := server.Main(context.Background(), []string{"migrat"}, &stdout, &stderr, server.Options{})
	if err == nil || !strings.Contains(err.Error(), "known: migrate, seed, audit-verify, identity, ca") {
		t.Errorf("an unknown subcommand = %v", err)
	}
}

func TestCallerFromOutsideAHostRouteSaysSo(t *testing.T) {
	if _, ok := server.CallerFrom(context.Background()); ok {
		t.Error("CallerFrom found a caller outside a host route")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// waitServing polls url until it answers 200, failing early if Main returns.
func waitServing(t *testing.T, url string, done <-chan error, logs *lockedBuffer) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("Main returned before serving: %v\n%s", err, logs.String())
		default:
		}
		if resp, err := http.Get(url); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never answered 200\n%s", url, logs.String())
}

func get(t *testing.T, url, token string) *http.Response {
	t.Helper()
	return do(t, http.MethodGet, url, token)
}

func post(t *testing.T, url, token string) *http.Response {
	t.Helper()
	return do(t, http.MethodPost, url, token)
}

func do(t *testing.T, method, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func envelopeCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var e struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	return e.Code
}

// logLine returns the first JSON log line whose message is msg.
func logLine(t *testing.T, logs, msg string) map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(logs))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		var line map[string]any
		if json.Unmarshal(scanner.Bytes(), &line) == nil && line["msg"] == msg {
			return line
		}
	}
	t.Fatalf("no %q line in the log:\n%s", msg, logs)
	return nil
}

// The worked example in ext/README.md is server/example_test.go verbatim, so
// the first thing a host author reads is code that compiles (the same rule ext's
// own example is held to).
func TestTheReadmeExampleIsTheCompiledOne(t *testing.T) {
	source, err := os.ReadFile("example_test.go")
	if err != nil {
		t.Fatalf("read example_test.go: %v", err)
	}
	idx := strings.Index(string(source), "// archive is a host")
	if idx < 0 {
		t.Fatal("example_test.go no longer starts its example where this guard looks; update both together")
	}
	example := strings.TrimRight(string(source[idx:]), "\n")

	readme, err := os.ReadFile(filepath.Join("..", "ext", "README.md"))
	if err != nil {
		t.Fatalf("read ext/README.md: %v", err)
	}
	if !strings.Contains(string(readme), example) {
		t.Error("ext/README.md's host example is not server/example_test.go verbatim; " +
			"copy the compiled example into the README rather than editing the README by hand")
	}
}
