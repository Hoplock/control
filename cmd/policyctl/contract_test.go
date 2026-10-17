// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The README's synopsis is the binary's own usage, line for line, and both name
// exactly the commands and flags the code defines: a flag added undocumented,
// or documented and gone, fails here.
func TestTheREADMEIsTheUsageAndTheUsageIsTheCode(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile("(?s)```\n(policyctl .*?)```").FindSubmatch(readme)
	if block == nil {
		t.Fatal("the README has no synopsis block")
	}
	norm := func(s string) []string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			if l = strings.TrimPrefix(strings.Join(strings.Fields(l), " "), "usage: "); l != "" && l != "commands:" {
				out = append(out, l)
			}
		}
		return out
	}
	usageLines := norm(usage[:strings.Index(usage, "-server, -token and -tenant default")])
	if got := norm(string(block[1])); !slices.Equal(got, usageLines) {
		t.Errorf("README synopsis:\n%s\nusage:\n%s", strings.Join(got, "\n"), strings.Join(usageLines, "\n"))
	}

	// What the usage names, command by command.
	documented := map[string][]string{}
	command := ""
	for _, l := range strings.Split(usage, "\n") {
		fields := strings.Fields(l)
		if len(fields) == 0 {
			continue
		}
		if strings.HasPrefix(l, "usage: ") {
			command = "policyctl"
		} else if strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   ") {
			command = fields[0]
		} else if !strings.HasPrefix(l, " ") {
			continue
		}
		for _, f := range regexp.MustCompile(`(?:^|[\s\[])-([a-z][a-z-]*)`).FindAllStringSubmatch(l, -1) {
			if !slices.Contains(documented[command], f[1]) {
				documented[command] = append(documented[command], f[1])
			}
		}
		if _, ok := documented[command]; !ok {
			documented[command] = []string{}
		}
	}

	// What the code defines: each flag set's name, and every flag declared
	// on it.
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	defined := map[string][]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		set := ""
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			lit := func(i int) string {
				if i >= len(call.Args) {
					return ""
				}
				b, ok := call.Args[i].(*ast.BasicLit)
				if !ok {
					return ""
				}
				s, _ := strconv.Unquote(b.Value)
				return s
			}
			switch sel.Sel.Name {
			case "NewFlagSet":
				set = lit(0)
				defined[set] = []string{}
			case "String", "Bool", "Int64", "Int", "Duration":
				if set != "" && lit(0) != "" {
					defined[set] = append(defined[set], lit(0))
				}
			case "Var", "StringVar", "BoolVar":
				if set != "" && lit(1) != "" {
					defined[set] = append(defined[set], lit(1))
				}
			}
			return true
		})
		return true
	})
	for name, flags := range defined {
		slices.Sort(flags)
		doc := slices.Sorted(slices.Values(documented[name]))
		if !slices.Equal(flags, doc) {
			t.Errorf("%s: the code defines %v, the usage documents %v", name, flags, doc)
		}
	}
	for name := range documented {
		if _, ok := defined[name]; !ok {
			t.Errorf("the usage documents %q, which the code does not define", name)
		}
	}
}

func runAgainst(t *testing.T, server string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := map[string]string{"HOPLOCK_SERVER": server}
	code := run(args, &out, &errOut, func(k string) string { return env[k] })
	return code, out.String(), errOut.String()
}

// Exit 2 is "the pipeline is broken", never "the policy is wrong": a server
// outage, a flag the command does not take, and a simulation that asks
// nothing are all 2.
func TestAnOutageAndAUsageErrorExitTwo(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"unavailable","message":"the store is down","correlation_id":"c-1"}`))
	}))
	t.Cleanup(down.Close)
	src := t.TempDir() + "/p.yaml"
	if err := os.WriteFile(src, []byte("schema_version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"a 503", []string{"versions"}},
		{"a 503 on validate", []string{"validate", src}},
	} {
		if code, _, errOut := runAgainst(t, down.URL, tc.args...); code != exitError {
			t.Errorf("%s: exit %d (%s), want %d", tc.name, code, errOut, exitError)
		}
	}

	// A usage error never reaches the server, which would refuse it with a
	// 4xx — exit 1, "the policy is wrong" — and it is not.
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a usage error reached the server: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_request","message":"no","correlation_id":"c-2"}`))
	}))
	t.Cleanup(refusing.Close)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"a flag the command does not take", []string{"versions", "-accept", "x"}},
		{"a simulation that asks nothing", []string{"simulate", src}},
		{"a replay with no start", []string{"simulate", src, "-to", "2026-10-01T00:00:00Z"}},
	} {
		code, _, errOut := runAgainst(t, refusing.URL, tc.args...)
		if code != exitError {
			t.Errorf("%s: exit %d (%s), want %d", tc.name, code, errOut, exitError)
		}
	}
}

// One record per line, whatever a message holds: a finding whose message
// carries a newline or a tab is still one line of tab-separated fields.
func TestARecordIsOneLineWhateverItsMessageHolds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"valid":true,"digest":"sha256:x","rejections":[],"findings":[` +
			`{"id":"gov.x/a/b","severity":"advice","code":"gov.x","message":"two\nlines\tand a tab\r\n"}]}`))
	}))
	t.Cleanup(srv.Close)
	src := t.TempDir() + "/p.yaml"
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runAgainst(t, srv.URL, "validate", src)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if code != exitOK || len(lines) != 2 {
		t.Fatalf("exit %d, %d lines: %q", code, len(lines), out)
	}
	if fields := strings.Split(lines[1], "\t"); len(fields) != 4 || fields[0] != "finding" {
		t.Errorf("the finding is %q, want four fields", lines[1])
	}
}

// policyctl holds an API token and nothing else: it imports the standard
// library alone, so it cannot open the database, compile a bundle, or become a
// second implementation of the rules (PLAN M2).
func TestPolicyctlImportsTheStandardLibraryAlone(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
			t.Errorf("policyctl imports %s", path)
		}
	}
}
