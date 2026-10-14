// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// BOTH DEBUG PATHS ARE GONE, AND THIS SAYS SO (0019). `docs/PROTOCOL.md` §3
// allows a debug endpoint only against a named production successor whose
// prompt carries the removal; 0019's north-bound surface was the successor of
// the revocation publish path (0009) and the log read path (0010), and a
// supersession that left either bound would have superseded nothing.
//
// It reads every Go file under cmd/ and internal/ — tests included, since a
// test server bound to a `/debug/` pattern is a pattern somebody copies — and
// fails on any mux registration or route pattern naming one. The proxy repo's
// mock keeps its own `/debug/` hooks; they are named in YAML, never bound here.
func TestNoGoFileBindsADebugRoute(t *testing.T) {
	root := filepath.Join("..", "..")
	var bound []string
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					sel, ok := x.Fun.(*ast.SelectorExpr)
					if !ok || len(x.Args) == 0 {
						return true
					}
					switch sel.Sel.Name {
					case "Handle", "HandleFunc":
						if pattern, ok := literal(x.Args[0]); ok && namesDebug(pattern) {
							bound = append(bound, fset.Position(x.Pos()).String()+": "+pattern)
						}
					}
				case *ast.KeyValueExpr:
					if key, ok := x.Key.(*ast.Ident); ok && key.Name == "Pattern" {
						if pattern, ok := literal(x.Value); ok && namesDebug(pattern) {
							bound = append(bound, fset.Position(x.Pos()).String()+": "+pattern)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	for _, b := range bound {
		t.Errorf("a /debug/ route is bound: %s", b)
	}
}

func literal(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// namesDebug reports a pattern under /debug/, with or without a method.
func namesDebug(pattern string) bool {
	if _, path, ok := strings.Cut(pattern, " "); ok {
		pattern = path
	}
	return strings.HasPrefix(pattern, "/debug/") || strings.Contains(pattern, "}/debug/")
}
