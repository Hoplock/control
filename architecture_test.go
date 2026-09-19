// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// The architectural invariant of the whole product, enforced by CI rather than
// by good intentions.
//
// Hoplock Enterprise imports this module; this module never imports Hoplock
// Enterprise (PLAN M15). That sentence is easy to agree with and easy to break
// by accident — one convenient type, one shared helper — and by the time it is
// noticed the two repositories are one product that cannot be built separately.
// So it is a test, it walks the whole import graph, and it fails loudly.
//
// It lives at the module root, beside the other invariants that are cheap to
// check and expensive to notice late (see protocol_test.go).
package control_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hoplock/control/ext"
)

// enterpriseModule is the module this repository may never import. The prefix
// is matched on a path boundary, so a hypothetical unrelated module whose name
// merely starts with the same letters is not a false positive.
const enterpriseModule = "github.com/hoplock/enterprise"

// modulePath is this module, used to recognise its own internal packages.
const modulePath = "github.com/hoplock/control"

// importViolation is one forbidden import, located well enough to fix.
type importViolation struct {
	File   string
	Import string
}

// scanImports walks every Go source file under root — tests included, because
// a test that imports Enterprise makes this module depend on it just as surely
// as production code does — and reports the imports for which forbidden
// returns true.
func scanImports(t *testing.T, root string, forbidden func(path string) bool) []importViolation {
	t.Helper()
	var found []importViolation
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			// A file that does not parse is a build failure elsewhere;
			// this guard reports what it can rather than masking it.
			t.Errorf("parse %s: %v", path, err)
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for _, spec := range f.Imports {
			p, uerr := strconv.Unquote(spec.Path.Value)
			if uerr != nil {
				continue
			}
			if forbidden(p) {
				found = append(found, importViolation{File: rel, Import: p})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].File != found[j].File {
			return found[i].File < found[j].File
		}
		return found[i].Import < found[j].Import
	})
	return found
}

// underModule reports whether importPath is module itself or a package inside
// it.
func underModule(importPath, module string) bool {
	return importPath == module || strings.HasPrefix(importPath, module+"/")
}

func TestNoPackageImportsHoplockEnterprise(t *testing.T) {
	violations := scanImports(t, ".", func(p string) bool {
		return underModule(p, enterpriseModule)
	})
	for _, v := range violations {
		t.Errorf("%s imports %s: the dependency runs one way — Hoplock Enterprise imports this module, "+
			"never the reverse (PLAN M15). If a phase seems to need something from Enterprise, it needs "+
			"an extension point in ext/ and a real default here instead.", v.File, v.Import)
	}
}

// TestTheImportGuardActuallyCatchesOne proves the guard rather than asserting
// it. The prompt for this seam asked for the import-graph test to be proven by
// temporarily adding an offending import; doing that in the tree would break
// the build, so the same scanner is pointed at a throwaway tree that contains
// one. A guard that has never been shown to fire is a guard nobody has tested.
func TestTheImportGuardActuallyCatchesOne(t *testing.T) {
	dir := t.TempDir()
	offending := filepath.Join(dir, "offender", "offender.go")
	if err := os.MkdirAll(filepath.Dir(offending), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	source := "package offender\n\nimport (\n\t_ \"" + enterpriseModule + "/licence\"\n\t_ \"strings\"\n)\n"
	if err := os.WriteFile(offending, []byte(source), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A module whose path merely starts with the same letters is not a
	// violation; the prefix is matched on a path boundary.
	innocent := filepath.Join(dir, "innocent", "innocent.go")
	if err := os.MkdirAll(filepath.Dir(innocent), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(innocent, []byte("package innocent\n\nimport _ \""+enterpriseModule+"-tools\"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := scanImports(t, dir, func(p string) bool { return underModule(p, enterpriseModule) })
	if len(got) != 1 {
		t.Fatalf("the guard found %v, want exactly the one offending import", got)
	}
	if !strings.Contains(got[0].File, "offender") || got[0].Import != enterpriseModule+"/licence" {
		t.Errorf("the guard found %v, want the offending import in offender.go", got[0])
	}
}

// TestExtImportsNothingInternal keeps the public seam cheap to import. ext is
// what Hoplock Enterprise builds against; an internal import there would drag
// this module's implementation — its database driver, its HTTP stack — into
// every consumer that only wanted the interfaces.
//
// Its own tests are exempt from nothing here: they are scanned too, because an
// external test package in ext/ is still code in this directory that a reader
// will copy from.
func TestExtImportsNothingInternal(t *testing.T) {
	violations := scanImports(t, "ext", func(p string) bool {
		return underModule(p, modulePath+"/internal")
	})
	for _, v := range violations {
		t.Errorf("ext/%s imports %s: ext is interface-only, so that importing it costs a consumer "+
			"nothing but the interfaces (PLAN M15). Control's own implementations behind the seam "+
			"belong in internal/extdefault.", v.File, v.Import)
	}
}

// phaseNumber matches a four-digit phase number as PLAN §10 and the prompt
// filenames spell one.
var phaseNumber = regexp.MustCompile(`\b0\d{3}\b`)

// TestEveryPhasePromisedByTheSeamIsARealPhase ties the extension catalogue to
// the delivery plan. Most points say Control's behaviour when nothing is
// registered is core code, and name the phase that builds it; a phase number
// that does not exist in PLAN §10 is a promise nobody is going to keep.
func TestEveryPhasePromisedByTheSeamIsARealPhase(t *testing.T) {
	plan := readFile(t, planPath)
	delivery := section(t, plan, "10.")

	known := make(map[string]bool)
	for _, row := range tableRows(delivery) {
		if len(row) == 0 {
			continue
		}
		if phaseNumber.MatchString(row[0]) {
			known[phaseNumber.FindString(row[0])] = true
		}
	}
	if len(known) == 0 {
		t.Fatal("no phase numbers found in PLAN §10; this guard is reading the wrong section")
	}

	for _, info := range ext.Points() {
		for _, n := range phaseNumber.FindAllString(info.ControlShips, -1) {
			if !known[n] {
				t.Errorf("%s says Control supplies it in phase %s, which is not in PLAN §10",
					info.Interface, n)
			}
		}
	}
}
