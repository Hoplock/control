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
//   - `contract.Denied` and `contract.RefusedToMint` — the only constructors of
//     an error the mapper can turn into a 401 — are called from a named,
//     reviewed set of functions, and nothing but the mapper names the 401
//     itself, as `http.StatusUnauthorized` or as the number.
//   - `statusFor` is the only function that names an HTTP status constant, so
//     a handler cannot choose one for itself; the few that write their own
//     SUCCESS status are named, and may still never name the 401.
//
// Both are source-level assertions and both are cheap. The alternative is a
// comment nobody has to read.

// deniers are the functions allowed to construct a 401.
//
// [deny] is the end user's credential being refused; [rejectCredential] is the
// PROXY's own channel token being refused. They are two different facts that
// share a status code, and an operator reading a log line needs to know which
// happened — which is why there is more than one.
//
// ADDING A NAME HERE IS A DECISION, not a fix for a failing test. Every entry
// is a place that can tell a real user "access denied", so the question to
// answer first is whether the thing being refused is a decision this server
// made on purpose or a failure it suffered. If it is the second, it is a 5xx
// and it belongs nowhere near this list.
//
// [refuseToMint] is the third, added by 0019 as a decision: a brokered-
// certificate issuance this server refuses because the decision it cites is not
// one it made for the method, or does not match (Hoplock/proxy#68). It refuses
// neither the user's credential nor the proxy's token — the session was
// already authorized — so by this comment's own reasoning it is a denier of its
// own rather than a reuse of [deny].
var deniers = []string{"deny", "rejectCredential", "refuseToMint"}

func TestOnlyOneFunctionCanProduceA401(t *testing.T) {
	t.Parallel()

	var offenders []string
	for _, ctor := range []string{"Denied", "RefusedToMint"} {
		forEachCall(t, "contract", ctor, func(file, fn string, pos token.Position) {
			if !slices.Contains(deniers, fn) {
				offenders = append(offenders, pos.String()+" in "+fn)
			}
		})
	}
	// And nothing builds the class by hand: the mapper reads it, and only
	// the constructors above produce it.
	forEachSelector(t, "contract", func(sel, file, fn string, pos token.Position) {
		if sel == "ErrDenied" && fn != "statusFor" {
			offenders = append(offenders, pos.String()+": contract.ErrDenied in "+fn)
		}
	})
	// Nor does anything write the status itself. The test below lets a few
	// functions name a status constant, because their SUCCESS has a shape the
	// adapter does not; none of them may name this one, and no function may
	// spell it as a number, which no selector check would see.
	forEachSelector(t, "http", func(sel, file, fn string, pos token.Position) {
		if sel == "StatusUnauthorized" && fn != "statusFor" {
			offenders = append(offenders, pos.String()+": http.StatusUnauthorized in "+fn)
		}
	})
	forEachIntLiteral(t, func(value, file, fn string, pos token.Position) {
		if value == "401" {
			offenders = append(offenders, pos.String()+": the literal 401 in "+fn)
		}
	})
	if len(offenders) > 0 {
		t.Fatalf("a 401 is built outside %v:\n  %s\n\n"+
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

	// Five of these WRITE an answer and two only READ one. The middleware
	// produces the two answers no handler can — the recovered panic and the
	// 200 on the success path — while `Write` records the status net/http
	// implies for an unheadered body and `withLogging` compares the recorded
	// status against a threshold to pick a log level. `openStream` is the
	// revocation stream's success status, written before the stream has
	// anything to say because a subscription has to be open first; every
	// refusal on that route is still decided before it and still goes
	// through the mapper.
	//
	// `build` is the ROUTER rather than a handler, and it is the one place a
	// route's own success code is declared: `/v1/logs/batch` answers 202
	// because the contract distinguishes "accepted for storage" from
	// "durable", and naming that beside the route is what keeps it
	// reviewable in one place. It still cannot choose what a caller is told
	// on a FAILURE, which is what this test is really about.
	//
	// `serveConfig` is the fleet configuration fetch (proxy D18), whose
	// success is a 200 with the document, a 304 naming the held hash, or a
	// 204 with nothing published; every refusal on it goes through the
	// mapper.
	allowed := []string{"statusFor", "withRecovery", "endpoint", "writeError", "Write", "withLogging", "openStream", "build", "serveConfig"}

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
	forEachNode(t, func(n ast.Node, file, fn string, pos token.Position) {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != pkg {
			return
		}
		visit(sel.Sel.Name, file, fn, pos)
	})
}

// forEachIntLiteral visits every integer literal in this package's non-test
// files.
func forEachIntLiteral(t *testing.T, visit func(value, file, fn string, pos token.Position)) {
	t.Helper()
	forEachNode(t, func(n ast.Node, file, fn string, pos token.Position) {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.INT {
			visit(lit.Value, file, fn, pos)
		}
	})
}

// forEachNode visits every node inside a function in this package's non-test
// files, reporting the enclosing function.
func forEachNode(t *testing.T, visit func(n ast.Node, file, fn string, pos token.Position)) {
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
			// A package-level declaration is reported as the function
			// "(package)", which no allow-list names.
			enclosing := "(package)"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				enclosing = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				if n != nil {
					visit(n, name, enclosing, fset.Position(n.Pos()))
				}
				return true
			})
		}
	}
	if scanned == 0 {
		t.Fatal("no source files were scanned, so this test would pass against anything")
	}
}
