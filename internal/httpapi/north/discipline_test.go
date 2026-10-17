// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The discipline of this surface's answers, held in the source rather than by
// a sample of requests: a handler added later that answers a 401, or a code no
// registry lists, fails here on the day it is written (0023). The south-bound
// listener holds its own the same way (internal/httpapi/south).

// unauthorisers are the functions that may name a 401, and why. Everything
// else that refuses is a 403, a 4xx about the request, or an outage (M11).
var unauthorisers = map[string]string{
	"enforce": "the middleware: no credential, or one not accepted",
	"refused": "a federation or break-glass login this server decided to refuse",
	"refusal": "a host's error writer, naming the 401 in order to refuse it",
}

func TestOnlyTheMiddlewareAndALoginRefusalNameA401(t *testing.T) {
	var offenders []string
	forEachSourceNode(t, ".", func(n ast.Node, fn string, pos token.Position) {
		switch v := n.(type) {
		case *ast.SelectorExpr:
			if x, ok := v.X.(*ast.Ident); ok && x.Name == "http" && v.Sel.Name == "StatusUnauthorized" && unauthorisers[fn] == "" {
				offenders = append(offenders, pos.String()+": http.StatusUnauthorized in "+fn)
			}
		case *ast.BasicLit:
			if v.Kind == token.INT && v.Value == "401" {
				offenders = append(offenders, pos.String()+": the literal 401 in "+fn)
			}
		}
	})
	if len(offenders) > 0 {
		t.Fatalf("a 401 is named outside %v:\n  %s\n\nA 401 is a decision about a credential (M11). A refusal "+
			"of a request is a 403 or a 4xx naming its field, and a failure is a 5xx.",
			slices.Sorted(maps.Keys(unauthorisers)), strings.Join(offenders, "\n  "))
	}
}

// dynamicCodes are the functions that answer a code held in a variable, and
// what bounds it. Every other Error literal names one of the Code constants.
var dynamicCodes = map[string]string{
	"refusePush":     "accessctx's refusal codes, each aliased below and registered",
	"WriteHostError": "a host's declared codes, checked against the server's set before writing",
	"configFailure":  "one of the two config codes, chosen by the fleet's error",
}

func TestEveryCodeAnErrorCarriesIsDeclaredAndRegistered(t *testing.T) {
	declared := codeConstants(t, ".")
	registered := map[string]bool{}
	for _, c := range AllCodes {
		registered[c] = true
	}
	for name, value := range declared {
		if !registered[value] {
			t.Errorf("%s (%q) is declared and not in AllCodes", name, value)
		}
	}
	// accessctx's codes reach a caller through refusePush, so each has an
	// alias here.
	for name, value := range codeConstants(t, filepath.Join("..", "..", "accessctx")) {
		if !registered[value] {
			t.Errorf("accessctx.%s (%q) can be answered by the push receiver and is not in AllCodes", name, value)
		}
	}

	forEachSourceNode(t, ".", func(n ast.Node, fn string, pos token.Position) {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return
		}
		if ident, ok := lit.Type.(*ast.Ident); !ok || ident.Name != "Error" {
			return
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Code" {
				continue
			}
			ident, ok := kv.Value.(*ast.Ident)
			switch {
			case ok && declared[ident.Name] != "":
			case dynamicCodes[fn] != "":
			default:
				t.Errorf("%s: an Error in %s carries a code that is not one of the declared constants", pos, fn)
			}
		}
	})
}

// codeConstants returns the package-level string constants named Code… in a
// directory's non-test files, by name, resolving an alias of another package's
// constant through that package's own declarations.
func codeConstants(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	var aliases [][2]string
	forEachSourceDecl(t, dir, func(decl ast.Decl) {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			return
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Code") || i >= len(vs.Values) {
					continue
				}
				switch v := vs.Values[i].(type) {
				case *ast.BasicLit:
					if s, err := strconv.Unquote(v.Value); err == nil {
						out[name.Name] = s
					}
				case *ast.SelectorExpr:
					aliases = append(aliases, [2]string{name.Name, v.Sel.Name})
				}
			}
		}
	})
	if len(aliases) > 0 {
		other := codeConstants(t, filepath.Join("..", "..", "accessctx"))
		for _, a := range aliases {
			if other[a[1]] == "" {
				t.Fatalf("%s aliases accessctx.%s, which declares no such code", a[0], a[1])
			}
			out[a[0]] = other[a[1]]
		}
	}
	if len(out) == 0 {
		t.Fatalf("no Code constants found in %s, so this test would pass against anything", dir)
	}
	return out
}

// forEachSourceNode visits every node inside a declaration of a directory's
// non-test files, reporting the enclosing function ("(package)" outside one).
func forEachSourceNode(t *testing.T, dir string, visit func(n ast.Node, fn string, pos token.Position)) {
	t.Helper()
	fset := token.NewFileSet()
	forEachSourceDeclIn(t, fset, dir, func(decl ast.Decl) {
		enclosing := "(package)"
		if fn, ok := decl.(*ast.FuncDecl); ok {
			enclosing = fn.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			if n != nil {
				visit(n, enclosing, fset.Position(n.Pos()))
			}
			return true
		})
	})
}

func forEachSourceDecl(t *testing.T, dir string, visit func(ast.Decl)) {
	t.Helper()
	forEachSourceDeclIn(t, token.NewFileSet(), dir, visit)
}

func forEachSourceDeclIn(t *testing.T, fset *token.FileSet, dir string, visit func(ast.Decl)) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			visit(decl)
		}
	}
	if scanned == 0 {
		t.Fatalf("no source files in %s, so this test would pass against anything", dir)
	}
}
