// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ONE RESOLUTION SERVES THE CA AND RETENTION (0062).
//
// Where this deployment's keys live is decided once, by buildKeyStore, and the
// certificate authority and the retention keys are both handed what it
// returned. Two resolutions could disagree — the CA in Control's custodian and
// retention in the host's HSM, or the reverse — and nothing at run time would
// say so. This test reads the package's source and fails if:
//
//   - anything but buildKeyStore builds a key store (the software custodian's
//     constructor, or a registered store read off the extensions);
//   - anything but buildCA builds a certificate authority;
//   - a function hands buildCA a key store, or audit.NewRetentionKeys one,
//     that is not the result of its own buildKeyStore call;
//   - a function that builds both hands them two different results, or calls
//     buildKeyStore twice.
func TestTheCAAndRetentionShareOneKeyStoreResolution(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	type use struct {
		resolved []string // identifiers assigned from buildKeyStore
		ca       []string // the key store argument of buildCA
		ret      []string // the key store argument of audit.NewRetentionKeys
	}
	uses := map[string]*use{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			u := &use{}
			uses[fn.Name.Name] = u
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.AssignStmt:
					if len(n.Rhs) == 1 && calls(n.Rhs[0], "", "buildKeyStore") {
						if id, ok := n.Lhs[0].(*ast.Ident); ok {
							u.resolved = append(u.resolved, id.Name)
						}
					}
				case *ast.CallExpr:
					pos := fset.Position(n.Pos())
					switch {
					case calls(n, "extdefault", "NewSoftwareKeyStore") && fn.Name.Name != "buildKeyStore":
						t.Errorf("%s: %s builds the software custodian; only buildKeyStore may", pos, fn.Name.Name)
					case isMethod(n, "KeyStore") && fn.Name.Name != "buildKeyStore":
						t.Errorf("%s: %s reads a registered key store; only buildKeyStore may", pos, fn.Name.Name)
					case calls(n, "credential", "New") && fn.Name.Name != "buildCA":
						t.Errorf("%s: %s builds a certificate authority; only buildCA may", pos, fn.Name.Name)
					case calls(n, "", "buildCA"):
						if len(n.Args) != 4 {
							t.Errorf("%s: buildCA takes the configuration, the store, the resolved key store and a logger", pos)
							break
						}
						u.ca = append(u.ca, root(n.Args[2]))
					case calls(n, "audit", "NewRetentionKeys") && len(n.Args) == 1:
						u.ret = append(u.ret, root(n.Args[0]))
					}
				}
				return true
			})
		}
	}

	if u := uses["buildCA"]; u == nil {
		t.Fatal("buildCA is gone; this test must follow it")
	}
	built := map[string]bool{}
	for name, u := range uses {
		if len(u.resolved) > 1 {
			t.Errorf("%s resolves the key store %d times; once is the rule", name, len(u.resolved))
		}
		for _, arg := range append(append([]string{}, u.ca...), u.ret...) {
			if len(u.resolved) == 0 || arg != u.resolved[0] {
				t.Errorf("%s hands a key store (%q) that is not its own buildKeyStore result %v", name, arg, u.resolved)
			}
		}
		if len(u.ca) > 0 {
			built["ca"] = true
		}
		if len(u.ret) > 0 {
			built["retention"] = true
		}
		if len(u.ca) > 0 && len(u.ret) > 0 {
			built["both:"+name] = true
		}
	}
	if !built["both:serve"] {
		t.Errorf("serve does not build the CA and the retention keys from one resolution: %+v", uses["serve"])
	}
	for _, fn := range []string{"openCA", "openRetentionKeys"} {
		u := uses[fn]
		if u == nil || len(u.resolved) != 1 {
			t.Errorf("%s does not resolve the key store through buildKeyStore", fn)
		}
	}
}

// calls reports whether e calls pkg.name, or name when pkg is empty.
func calls(e ast.Expr, pkg, name string) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return pkg == "" && fun.Name == name
	case *ast.SelectorExpr:
		x, ok := fun.X.(*ast.Ident)
		return ok && pkg != "" && x.Name == pkg && fun.Sel.Name == name
	}
	return false
}

// isMethod reports whether call is a zero-argument method call named name.
func isMethod(call *ast.CallExpr, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name && len(call.Args) == 0
}

// root is the identifier an argument is read from: `keys` for `keys` or
// `keys.store`.
func root(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return root(e.X)
	}
	return ""
}
