// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package authoring

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestControlCodesIsEveryFindingCode holds the registry a client enumerates
// (M21) to the constants the checks use: a finding code declared in report.go
// and missing from ControlCodes is one no client can know about, and a code in
// it twice is a code reused.
func TestControlCodesIsEveryFindingCode(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "report.go", nil, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var declared []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				// CodeTenantMismatch is a rejection, not a finding.
				if !strings.HasPrefix(name.Name, "Code") || name.Name == "CodeTenantMismatch" {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok {
					t.Fatalf("%s is not a literal", name.Name)
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: %v", name.Name, err)
				}
				declared = append(declared, v)
			}
		}
	}
	got := slices.Sorted(slices.Values(ControlCodes))
	slices.Sort(declared)
	if !slices.Equal(got, declared) {
		t.Fatalf("ControlCodes = %v\ndeclared     = %v", got, declared)
	}
	if len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Fatalf("a code is listed twice: %v", got)
	}
	for _, code := range got {
		if prefix, _, ok := strings.Cut(code, "."); !ok || !slices.Contains([]string{"satisfiability", "authoring", "algorithm"}, prefix) {
			t.Errorf("%q is outside the three families a client groups by", code)
		}
	}
}
