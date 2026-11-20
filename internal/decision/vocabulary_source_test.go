// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

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

// NO THINNING PATH REMAINS (0064), and this is how that is known rather than
// believed. A thinned answer needs code that reads the declared vocabulary and
// then builds something different for it. So this test reads every non-test
// file of this package and lists the functions that read a request's
// `PolicyVersion`; the only ones allowed are those that validate it, refuse it,
// or write it down. assembleSnapshot is not among them, and it takes no version
// argument: the assembly cannot know which vocabulary was declared, so it
// cannot build an answer for any but the one this server serves.
//
// A session that adds a version-aware branch to the assembly — the "introduced
// in" table this phase removed — fails here first, and has to read why.
func TestOnlyTheGateReadsTheDeclaredVocabulary(t *testing.T) {
	allowed := []string{
		"validate",          // absent or below the minimum is a 400
		"Authorize",         // recorded for the fleet view, then the gate
		"checkVersion",      // any other vocabulary is the 5xx
		"newRecordedInputs", // the decision record says what was declared (M4)
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var readers []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if ok && sel.Sel.Name == "PolicyVersion" {
					// contract.PolicyVersion is the served constant,
					// not a read of what a proxy declared.
					if id, isIdent := sel.X.(*ast.Ident); isIdent && id.Name == "contract" {
						return true
					}
					readers = append(readers, name+": "+fn.Name.Name)
					if !slices.Contains(allowed, fn.Name.Name) {
						t.Errorf("%s reads the declared policy_version in %s; only %v may — "+
							"an answer built differently for a declared vocabulary is a thinning path (0064)",
							fset.Position(sel.Pos()), fn.Name.Name, allowed)
					}
				}
				return true
			})
		}
	}
	if len(readers) == 0 {
		t.Fatal("no function reads the declared policy_version at all; the gate cannot be running")
	}

	// And the assembly takes no version: its signature is the evidence.
	src, err := os.ReadFile("snapshot.go")
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(fset, "snapshot.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "assembleSnapshot" {
			continue
		}
		found = true
		for _, field := range fn.Type.Params.List {
			for _, n := range field.Names {
				if strings.Contains(strings.ToLower(n.Name), "version") {
					t.Errorf("assembleSnapshot takes a parameter %q; the assembly must not know the declared vocabulary", n.Name)
				}
			}
		}
	}
	if !found {
		t.Fatal("snapshot.go no longer defines assembleSnapshot; point this test at the assembly")
	}
}
