// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// M11 asks for the 401 path to be STRUCTURALLY hard to get wrong rather than a
// convention, and a convention is exactly what "only build a deny on purpose"
// becomes once somebody adds a seventh handler in a hurry.
//
// So this reads the package's own source and checks the shape of it:
//
//   - `contract.Denied` — the only constructor of an error the mapper can turn
//     into a 401 — is called from a named, reviewed set of functions.
//   - `statusFor` is the only function that names an HTTP status constant, so
//     a handler cannot choose one for itself.
//
// Both are source-level assertions and both are cheap. The alternative is a
// comment nobody has to read.

// deniers are the functions allowed to construct a 401.
//
// [deny] is the end user's credential being refused; [rejectCredential] is the
// PROXY's own channel token being refused. They are two different facts that
// share a status code, and an operator reading a log line needs to know which
// happened — which is why there are two rather than one.
//
// ADDING A NAME HERE IS A DECISION, not a fix for a failing test. Every entry
// is a place that can tell a real user "access denied", so the question to
// answer first is whether the thing being refused is a decision this server
// made on purpose or a failure it suffered. If it is the second, it is a 5xx
// and it belongs nowhere near this list.
var deniers = []string{"deny", "rejectCredential"}

func TestOnlyOneFunctionCanProduceA401(t *testing.T) {
	t.Parallel()

	var offenders []string
	forEachCall(t, "contract", "Denied", func(file, fn string, pos token.Position) {
		if !slices.Contains(deniers, fn) {
			offenders = append(offenders, pos.String()+" in "+fn)
		}
	})
	if len(offenders) > 0 {
		t.Fatalf("contract.Denied is called outside %v:\n  %s\n\n"+
			"A 401 is a DECISION this server made on purpose (M11). If what you are refusing is a "+
			"failure — a database timeout, a provider that did not answer, a panic — return the error "+
			"and let the mapper answer 5xx. If it really is a decision, add the function to `deniers` "+
			"above and say in its doc comment what is being refused.",
			deniers, strings.Join(offenders, "\n  "))
	}
}

// A handler that reached for a status code directly would be a handler
// choosing its own answer, which is how the mapping quietly stops being the
// mapping.
func TestOnlyTheMapperNamesAStatusCode(t *testing.T) {
	t.Parallel()

	// Four of these WRITE an answer and two only READ one. The middleware
	// produces the two answers no handler can — the recovered panic and the
	// 200 on the success path — while `Write` records the status net/http
	// implies for an unheadered body and `withLogging` compares the recorded
	// status against a threshold to pick a log level. None of the six lets a
	// handler choose what the caller is told.
	allowed := []string{"statusFor", "withRecovery", "endpoint", "writeError", "Write", "withLogging"}

	var offenders []string
	forEachSelector(t, "http", func(sel, file, fn string, pos token.Position) {
		if !strings.HasPrefix(sel, "Status") {
			return
		}
		if slices.Contains(allowed, fn) {
			return
		}
		offenders = append(offenders, pos.String()+": http."+sel+" in "+fn)
	})
	if len(offenders) > 0 {
		t.Fatalf("an HTTP status constant is named outside %v:\n  %s\n\n"+
			"statusFor is the only place a status code is chosen, and it chooses from a classified "+
			"failure rather than from an error's text or type.",
			allowed, strings.Join(offenders, "\n  "))
	}
}

// forEachCall visits every call to pkg.name in this package's non-test files,
// reporting the enclosing function.
func forEachCall(t *testing.T, pkg, name string, visit func(file, fn string, pos token.Position)) {
	t.Helper()
	forEachSelector(t, pkg, func(sel, file, fn string, pos token.Position) {
		if sel == name {
			visit(file, fn, pos)
		}
	})
}

// forEachSelector visits every `pkg.Something` reference in this package's
// non-test files.
func forEachSelector(t *testing.T, pkg string, visit func(sel, file, fn string, pos token.Position)) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	fset := token.NewFileSet()
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++

		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok || ident.Name != pkg {
					return true
				}
				visit(sel.Sel.Name, name, fn.Name.Name, fset.Position(sel.Pos()))
				return true
			})
		}
	}
	if scanned == 0 {
		t.Fatal("no source files were scanned, so this test would pass against anything")
	}
}
