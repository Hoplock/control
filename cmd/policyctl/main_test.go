// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/authoring"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// policyctl against the real north-bound surface, in process: every command,
// its output's record kinds, and its exit code. The output is asserted line by
// line because scripts read it line by line.

type fixture struct {
	st     *store.Store
	server string
	token  string
	dir    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := storetest.New(t)
	log := slog.New(slog.DiscardHandler)
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	bus, err := revoke.New(revoke.Options{Logger: log})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	ingest, err := audit.New(audit.Options{Store: st})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	emitter, err := audit.NewEmitter(ingest)
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}
	grants, err := access.New(access.Options{Store: st, Recorder: emitter, Revoker: bus, Logger: log, Settle: time.Millisecond})
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	registry := fleet.New(st, fleet.WithSubscriptionState(bus), fleet.WithLogger(log))
	lifecycle, err := authoring.New(authoring.Options{Store: st, Fleet: registry, Emitter: emitter, Reader: audit.NewReader(st)})
	if err != nil {
		t.Fatalf("authoring: %v", err)
	}
	server, err := north.New(north.Options{
		Federation: federation, Grants: grants, Store: st, Fleet: registry, Authoring: lifecycle,
		Revocations: revoke.NewOperator(bus, registry), Audit: emitter,
		DefaultTenant: "default", InsecureCookies: true, Logger: log,
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	cred, _, err := federation.IssueToken(t.Context(), "default", identity.TokenRequest{
		DisplayName: "ci", Scopes: map[store.Tenant]identity.RoleSet{"default": {identity.RolePolicyAuthor}},
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return &fixture{st: st, server: ts.URL, token: cred.String(), dir: t.TempDir()}
}

func (f *fixture) file(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(f.dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (f *fixture) run(t *testing.T, args ...string) (int, []string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := map[string]string{"HOPLOCK_SERVER": f.server, "HOPLOCK_TOKEN": f.token}
	code := run(args, &out, &errOut, func(k string) string { return env[k] })
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if out.Len() == 0 {
		lines = nil
	}
	return code, lines, errOut.String()
}

func kinds(lines []string) []string {
	var out []string
	for _, l := range lines {
		k, _, _ := strings.Cut(l, "\t")
		out = append(out, k)
	}
	return out
}

func has(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

const header = "schema_version: 1\ntenant: default\ngroups: [sre]\nrules:\n"

const sre = `  - id: sre
    effect: allow
    match:
      subject:
        groups: [sre]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-key
          username: netadmin
          credential_ref: vault://x
`

const awk = `  - id: logs
    effect: allow
    match:
      target:
        hostnames: [app.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: whitelist
        exec_mode: restricted
        restricted_exec:
          commands:
            - executable: /usr/bin/awk
              form: exact
              argv: ["x"]
      credentials:
        - method: ephemeral-user
          username: svc
          key_type: ed25519
      enforcement:
        execution: account-restricted
`

func TestValidate(t *testing.T) {
	f := newFixture(t)
	good := f.file(t, "good.yaml", header+sre)
	code, lines, _ := f.run(t, "validate", good)
	if code != exitOK || len(lines) == 0 || !strings.HasPrefix(lines[0], "valid\ttrue\tsha256:") {
		t.Fatalf("validate good = %d %q", code, lines)
	}

	bad := f.file(t, "bad.yaml", header+sre+strings.Replace(sre, "id: sre", "id: never", 1))
	code, lines, _ = f.run(t, "validate", bad)
	if code != exitRefused || !has(lines, "rejection\trule.unreachable\tnever\t19\tline 19: rule \"never\"") {
		t.Fatalf("validate bad = %d %q", code, lines)
	}

	code, lines, _ = f.run(t, "-json", "validate", good)
	var rep map[string]any
	if code != exitOK || len(lines) != 1 || json.Unmarshal([]byte(lines[0]), &rep) != nil || rep["valid"] != true {
		t.Errorf("-json validate = %d %q", code, lines)
	}
}

func TestApplyActivateVersionsAndDiff(t *testing.T) {
	f := newFixture(t)
	first := f.file(t, "first.yaml", header+sre)
	code, lines, errOut := f.run(t, "apply", first)
	if code != exitOK || !has(lines, "version\t1\tsha256:") || !has(lines, "activated\t1\tprevious\t0\trollback\tfalse") {
		t.Fatalf("apply = %d %q %s", code, lines, errOut)
	}

	// A warning stops the apply: exit 1, and the id to accept on stderr.
	second := f.file(t, "second.yaml", header+sre+awk)
	code, lines, errOut = f.run(t, "apply", second)
	if code != exitRefused || len(lines) != 0 || !strings.Contains(errOut, "refused\t409\tfindings_not_accepted") {
		t.Fatalf("apply with a warning = %d %q %s", code, lines, errOut)
	}
	var id string
	for _, l := range strings.Split(errOut, "\n") {
		if strings.HasPrefix(l, "unaccepted\t") {
			id = strings.Split(l, "\t")[1]
		}
	}
	if !strings.HasPrefix(id, authoring.CodeInterpreter) {
		t.Fatalf("no unaccepted interpreter finding in %s", errOut)
	}

	code, lines, _ = f.run(t, "diff", second)
	if code != exitOK || lines[0] != "from\t1" || !has(lines, "added\tlogs") || !has(lines, "text\t+  - id: logs") {
		t.Fatalf("diff = %d %q", code, lines)
	}

	code, lines, errOut = f.run(t, "apply", second, "-accept", id)
	if code != exitOK || !has(lines, "activated\t2\tprevious\t1") || !has(lines, "accepted\t"+id) {
		t.Fatalf("apply accepted = %d %q %s", code, lines, errOut)
	}

	code, lines, errOut = f.run(t, "activate", "1")
	if code != exitOK || !has(lines, "activated\t1\tprevious\t2\trollback\ttrue") {
		t.Fatalf("rollback = %d %q %s", code, lines, errOut)
	}
	code, lines, _ = f.run(t, "versions")
	if code != exitOK || len(lines) != 2 || !strings.HasPrefix(lines[0], "version\t2\t") || !strings.HasSuffix(lines[1], "\tactive") {
		t.Fatalf("versions = %d %q", code, lines)
	}
}

func TestSimulateAndExplain(t *testing.T) {
	f := newFixture(t)
	if code, _, errOut := f.run(t, "apply", f.file(t, "p.yaml", header+sre)); code != exitOK {
		t.Fatalf("apply: %s", errOut)
	}
	inputs := json.RawMessage(`{"now":"2026-10-01T12:00:00Z","subject":{"id":"alice@example.com","groups":["sre"],"mfa":false,"known":true,"break_glass":false},"target":{"hostname":"app.example.com","known":true},"context":{"proxy_id":"proxy-1"},"connection":{},"external_context":[]}`)
	decided := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := f.st.Decisions().Insert(t.Context(), "default", store.Decision{
		ID: "dec-1", SubjectID: "alice@example.com", Effect: "allow", SessionID: "sess-1", ProxyID: "proxy-1",
		InputsDigest: "x", Inputs: inputs, Explanation: json.RawMessage(`{"effect":"allow","rule":"sre","rule_line":5}`),
		Snapshot: json.RawMessage(`{"route_type":"direct"}`), DecidedAt: decided,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// A candidate that denies what was allowed: one flip, and -fail-on-flip
	// makes it a CI gate.
	deny := f.file(t, "deny.yaml", header+`  - id: closed
    effect: deny
    reason: closed
    match:
      target:
        hostnames: [app.example.com]
`+sre)
	cases := f.file(t, "cases.json", `[{"name":"alice","now":"2026-10-01T12:00:00Z","subject":{"id":"alice@example.com","groups":["sre"]},"target":{"hostname":"app.example.com"}}]`)
	code, lines, errOut := f.run(t, "simulate", deny, "-from", "2026-10-01T00:00:00Z", "-to", "2026-10-02T00:00:00Z",
		"-cases", cases, "-fail-on-flip")
	if code != exitRefused {
		t.Fatalf("simulate with a flip under -fail-on-flip = %d %q %s", code, lines, errOut)
	}
	if !has(lines, "case\talice\tdeny\tclosed") || !has(lines, "replayed\t1\tunchanged\t0") ||
		!has(lines, "flip\tallow->deny\tdec-1\talice@example.com\tapp.example.com\tsre\tclosed") {
		t.Fatalf("simulate = %q", lines)
	}

	code, lines, _ = f.run(t, "explain", "dec-1")
	if code != exitOK || !has(lines, "decision\tdec-1\tallow\tsre\t5\tsession\tsess-1") || !has(lines, "mapping\t0") {
		t.Fatalf("explain = %d %q", code, lines)
	}
	code, lines, _ = f.run(t, "explain", "-session", "sess-1")
	if code != exitOK || !slices.Contains(kinds(lines), "decision") {
		t.Fatalf("explain -session = %d %q", code, lines)
	}
	code, _, errOut = f.run(t, "explain", "dec-nobody")
	if code != exitRefused || !strings.Contains(errOut, "refused\t404\tnot_found") {
		t.Fatalf("explain unknown = %d %s", code, errOut)
	}
}

func TestUsageAndTransportErrorsExitTwo(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"validate", "x"}, &out, &errOut, func(string) string { return "" }); code != exitError {
		t.Errorf("no server = %d", code)
	}
	if code := run([]string{"-server", "http://127.0.0.1:1", "frobnicate"}, &out, &errOut, func(string) string { return "" }); code != exitError {
		t.Errorf("unknown command = %d", code)
	}
	if code := run([]string{"-server", "http://127.0.0.1:1", "versions"}, &out, &errOut, func(string) string { return "" }); code != exitError {
		t.Errorf("unreachable server = %d", code)
	}
}
