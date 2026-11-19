// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

// Package e2e is the cross-repository scenario suite (PLAN §9, 0061): a REAL
// Hoplock Proxy enforcing decisions from a REAL Hoplock Control, driven over
// the topology in deploy/. Run it with `make e2e` (deploy/README.md).
//
// Every SSH client runs inside the `client` container, which is on the `edge`
// network only, so a session that reached a target went through a proxy. The
// suite reads this server's side through the north-bound API, published on the
// host's loopback, the way an operator's tooling would.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	northURL = "http://127.0.0.1:18081/api/v1/tenants/default"
	// The operator's API token, seeded by deploy/control/seed.template.yaml.
	operatorToken = "ht.default.e2e-operator-secret"
	proxyPort     = "2222"
	// The SSH server users reach. proxy-relay is never reachable by name.
	edgeProxy = "proxy-edge"
)

// deployDir is deploy/, found from this file so the suite runs from anywhere.
func deployDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "deploy")
}

func TestMain(m *testing.M) {
	// The topology is "up" when this server answers and a session through each
	// route class works: the proxies have subscribed to their event streams and
	// the relay has registered. Until then a scenario would fail as an outage
	// that has nothing to do with what it tests.
	deadline := time.Now().Add(3 * time.Minute)
	for {
		_, err := northGet("/proxies?limit=1")
		if err == nil {
			a, _ := sshE(session{who: "alice", target: "appliance", command: "true"})
			z, _ := sshE(session{who: "alice", target: "zone-host", command: "true"})
			if a.code == 0 && z.code == 0 {
				break
			}
			err = fmt.Errorf("the fleet does not serve yet: %s%s", a.stderr, z.stderr)
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "e2e: the topology is not up (%v); run `make e2e-up` first\n", err)
			os.Exit(1)
		}
		time.Sleep(2 * time.Second)
	}
	os.Exit(m.Run())
}

// result is one command's output.
type result struct {
	stdout, stderr string
	code           int
}

func (r result) output() string { return r.stdout + r.stderr }

func (r result) String() string {
	return fmt.Sprintf("exit %d\n--- stdout\n%s--- stderr\n%s", r.code, r.stdout, r.stderr)
}

// runE runs a command on the host and reports how it ended.
func runE(stdin, name string, args ...string) (result, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = deployDir()
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := result{stdout: out.String(), stderr: errb.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		r.code = ee.ExitCode()
		err = nil
	}
	return r, err
}

func run(t *testing.T, stdin, name string, args ...string) result {
	t.Helper()
	r, err := runE(stdin, name, args...)
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return r
}

// compose runs a `docker compose` command against the topology.
func compose(t *testing.T, args ...string) result {
	t.Helper()
	return run(t, "", "docker", append([]string{"compose"}, args...)...)
}

// execIn runs argv inside a running service's container.
func execIn(t *testing.T, service string, argv ...string) result {
	t.Helper()
	return run(t, "", "docker", append([]string{"compose", "exec", "-T", service}, argv...)...)
}

// sshBaseArgs are the client options every scenario shares. LogLevel stays at
// its default on purpose: the proxy's banner — the session id a user quotes to
// support (PLAN §4.3) — is printed at INFO, and a quieter client hides it.
var sshBaseArgs = []string{
	"-o", "StrictHostKeyChecking=no",
	"-o", "UserKnownHostsFile=/dev/null",
	"-o", "BatchMode=yes",
	"-o", "IdentitiesOnly=yes",
	"-o", "PreferredAuthentications=publickey",
	"-o", "ConnectTimeout=10",
	"-o", "ServerAliveInterval=5",
}

// session is one SSH client invocation.
type session struct {
	who     string // alice, bob, carol, svc: the key and the login
	target  string // the D1-encoded target, after the delimiter
	opts    []string
	command string
	stdin   string
}

func (s session) argv() []string {
	argv := append([]string{"ssh"}, sshBaseArgs...)
	argv = append(argv, "-i", "/material/user_"+s.who, "-p", proxyPort, "-l", s.who+"#"+s.target)
	argv = append(argv, s.opts...)
	argv = append(argv, edgeProxy)
	if s.command != "" {
		argv = append(argv, s.command)
	}
	return argv
}

func sshE(s session) (result, error) {
	return runE(s.stdin, "docker", append([]string{"compose", "exec", "-T", "client"}, s.argv()...)...)
}

func ssh(t *testing.T, s session) result {
	t.Helper()
	r, err := sshE(s)
	if err != nil {
		t.Fatalf("ssh: %v", err)
	}
	return r
}

// sessionIDPattern finds the session id the proxy quotes to the user.
var sessionIDPattern = regexp.MustCompile(`Session (sess-[0-9a-f]+)\.`)

func sessionIDOf(t *testing.T, r result) string {
	t.Helper()
	m := sessionIDPattern.FindStringSubmatch(r.output())
	if m == nil {
		t.Fatalf("the proxy quoted no session id:\n%s", r)
	}
	return m[1]
}

func wantContains(t *testing.T, r result, what, substr string) {
	t.Helper()
	if !strings.Contains(r.output(), substr) {
		t.Fatalf("%s: want %q in the output:\n%s", what, substr, r)
	}
}

func wantNotContains(t *testing.T, r result, what, substr string) {
	t.Helper()
	if strings.Contains(r.output(), substr) {
		t.Fatalf("%s: did not want %q in the output:\n%s", what, substr, r)
	}
}

func wantExit(t *testing.T, r result, what string, code int) {
	t.Helper()
	if r.code != code {
		t.Fatalf("%s: want exit %d:\n%s", what, code, r)
	}
}

// The texts a user is told (Hoplock Proxy PLAN §4.3). The deny is deliberately
// vague; the outage says it is one and gives the session id; neither leaks the
// rule, which is the operator's to read through explain.
const (
	textDenied = "Access denied."
	textOutage = "This is a service problem, not a permissions problem"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", within, what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// --- the north-bound API ---

var httpClient = &http.Client{Timeout: 15 * time.Second}

func northDo(method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, northURL+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+operatorToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

func northGet(path string) ([]byte, error) {
	code, b, err := northDo(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %d %s", path, code, b)
	}
	return b, nil
}

// north GETs path and decodes the answer into v.
func north(t *testing.T, path string, v any) {
	t.Helper()
	b, err := northGet(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("GET %s: %v\n%s", path, err, b)
	}
}

// northPost POSTs body and wants one of the listed statuses.
func northPost(t *testing.T, path string, body any, want ...int) map[string]any {
	t.Helper()
	code, b, err := northDo(http.MethodPost, path, body)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range want {
		if code == w {
			var out map[string]any
			_ = json.Unmarshal(b, &out)
			return out
		}
	}
	t.Fatalf("POST %s: got %d, want %v\n%s", path, code, want, b)
	return nil
}

// decision is the part of an explain answer (GET …/sessions/{id}/decisions)
// the scenarios read.
type decision struct {
	DecisionID  string         `json:"decision_id"`
	SessionID   string         `json:"session_id"`
	ProxyID     string         `json:"proxy_id"`
	Effect      string         `json:"effect"`
	Rule        string         `json:"rule"`
	Inputs      map[string]any `json:"inputs"`
	Explanation map[string]any `json:"explanation"`
	Snapshot    map[string]any `json:"snapshot"`
}

func decisionsOf(t *testing.T, sessionID string) []decision {
	t.Helper()
	var out struct {
		Decisions []decision `json:"decisions"`
	}
	north(t, "/sessions/"+url.PathEscape(sessionID)+"/decisions", &out)
	return out.Decisions
}

// record is one stored audit record, as the audit query answers it. Body is
// the canonical JSON the chain hashes; Doc decodes it.
type record struct {
	RecordID   string            `json:"record_id"`
	Kind       string            `json:"kind"`
	Event      string            `json:"event"`
	Message    string            `json:"message"`
	ProxyID    string            `json:"proxy_id"`
	SessionID  string            `json:"session_id"`
	Subject    string            `json:"subject"`
	Target     string            `json:"target"`
	Attributes map[string]string `json:"attributes"`
	Body       string            `json:"body"`
}

// Doc is the record's body, decoded.
func (r record) Doc() map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(r.Body), &m)
	return m
}

// recordsOf is every stored record of one session, oldest first.
func recordsOf(t *testing.T, sessionID string) []record {
	t.Helper()
	var out struct {
		Records []record `json:"records"`
	}
	north(t, "/audit/records?order=asc&limit=500&session="+url.QueryEscape(sessionID), &out)
	return out.Records
}

// field reads a dotted path out of a decoded JSON object.
func field(m map[string]any, path string) any {
	var cur any = m
	for _, part := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[part]
	}
	return cur
}

// jsonText renders a decoded JSON value back as text, for comparing a number
// the API answers with a string a record carries.
func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
