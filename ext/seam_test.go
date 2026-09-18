// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Guards on the shape of the seam itself.
//
// Everything here is a property that has to hold for Hoplock Enterprise to be
// able to build against this package, and none of it fails to compile on its
// own: an interface added without a catalogue entry, a point that promises a
// default nothing supplies, an interface whose doc comment never says what
// happens when nobody implements it. This file is what makes those fail under
// `make test` instead.
package ext_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoplock/control/ext"
)

// supportingInterfaces are the exported interfaces in this package that are
// NOT extension points, each with the reason it is not one. Adding an
// interface to the package therefore forces a decision: give it a catalogue
// entry, or say here why it does not need one.
var supportingInterfaces = map[string]string{
	// A handle handed back by ClusterCoordinator.Lead, not a point.
	"Leadership": "returned by ClusterCoordinator.Lead",
	// Control's side of IdentitySync: the extension calls it.
	"SyncSink": "Control implements it and passes it to IdentitySync.Start",
	// Control's side of ActionHandler: the extension calls it.
	"ActionExecutor": "Control implements it and passes it to ActionHandler.Start",
}

// absentSentence is the phrase every extension-point interface must contain.
// It is checked literally rather than approximately because the prompt for
// this seam asked for "an explicit statement of what Control does when no
// implementation is registered", and a test that accepts a paraphrase accepts
// its absence.
const absentSentence = "When no implementation is registered"

// parsePackage reads this package's non-test sources with their comments.
func parsePackage(t *testing.T) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no source files found in the package directory")
	}
	return files
}

// exportedInterfaces returns every exported interface type declared in the
// package, mapped to its doc comment.
func exportedInterfaces(t *testing.T) map[string]string {
	t.Helper()
	found := make(map[string]string)
	for _, f := range parsePackage(t) {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				if _, ok := ts.Type.(*ast.InterfaceType); !ok {
					continue
				}
				doc := ts.Doc
				if doc == nil {
					doc = gen.Doc
				}
				text := ""
				if doc != nil {
					text = doc.Text()
				}
				found[ts.Name.Name] = text
			}
		}
	}
	return found
}

func TestEveryExportedInterfaceIsAPointOrADeclaredSupportingType(t *testing.T) {
	inCatalogue := make(map[string]bool)
	for _, info := range ext.Points() {
		inCatalogue[info.Interface] = true
	}

	for name := range exportedInterfaces(t) {
		if inCatalogue[name] {
			continue
		}
		if _, known := supportingInterfaces[name]; known {
			continue
		}
		t.Errorf("exported interface %s is neither an extension point in the catalogue nor "+
			"a declared supporting interface; give it a PointInfo row in point.go, or add it to "+
			"supportingInterfaces with the reason it is not a point", name)
	}
}

func TestEveryCatalogueRowNamesARealInterface(t *testing.T) {
	declared := exportedInterfaces(t)
	for _, info := range ext.Points() {
		if _, ok := declared[info.Interface]; !ok {
			t.Errorf("catalogue point %v names interface %q, which this package does not declare",
				info.Point, info.Interface)
		}
	}
}

func TestEveryPointInterfaceSaysWhatHappensWhenNobodyImplementsIt(t *testing.T) {
	declared := exportedInterfaces(t)
	for _, info := range ext.Points() {
		doc, ok := declared[info.Interface]
		if !ok {
			continue // reported by TestEveryCatalogueRowNamesARealInterface
		}
		if !strings.Contains(doc, absentSentence) {
			t.Errorf("%s's doc comment does not contain %q: every extension point states "+
				"explicitly what Control does when nothing is registered, because that sentence "+
				"is the difference between a seam and a hole (PLAN M15)", info.Interface, absentSentence)
		}
	}
}

func TestCatalogueInvariants(t *testing.T) {
	seenInterface := make(map[string]bool)
	for i, info := range ext.Points() {
		if int(info.Point) != i {
			t.Errorf("catalogue row %d carries Point %v: the table is indexed by point and must stay in order", i, info.Point)
		}
		if info.Interface == "" {
			t.Errorf("catalogue row %d names no interface", i)
			continue
		}
		if seenInterface[info.Interface] {
			t.Errorf("interface %s has more than one catalogue row", info.Interface)
		}
		seenInterface[info.Interface] = true

		if strings.TrimSpace(info.Absent) == "" {
			t.Errorf("%s does not say what Control does when nothing is registered", info.Interface)
		}

		// M15's second invariant, as a test. A point where Control supplies
		// nothing is asserting that the capability is an addition; the only
		// disposition consistent with that is "disabled". Anything else
		// would be core functionality moved out to make room for a licence.
		if info.ControlShips == "" && info.WhenAbsent != ext.WhenAbsentDisabled {
			t.Errorf("%s ships nothing from Control but is %v: a point with no Control-side "+
				"implementation must be WhenAbsentDisabled, or it is a hole where core "+
				"functionality used to be (PLAN M15)", info.Interface, info.WhenAbsent)
		}
		if info.WhenAbsent == ext.WhenAbsentCore && info.ControlShips == "" {
			t.Errorf("%s says the behaviour is core to Control and names nothing that supplies it", info.Interface)
		}
	}
}

func TestPointStringIsTheInterfaceName(t *testing.T) {
	for _, info := range ext.Points() {
		if got := info.Point.String(); got != info.Interface {
			t.Errorf("Point(%d).String() = %q, want %q", int(info.Point), got, info.Interface)
		}
	}
	if got := ext.Point(len(ext.Points())).String(); !strings.HasPrefix(got, "Point(") {
		t.Errorf("an out-of-range point rendered as %q; it should be visibly unknown", got)
	}
	if _, ok := ext.Lookup(ext.Point(-1)); ok {
		t.Error("Lookup accepted a negative point")
	}
}

// TestReadmeExampleIsTheCompiledOne keeps README.md's worked example honest.
// A README that shows code nobody compiles is a README that drifts, and this
// one is the first thing an out-of-tree implementer reads: the block in the
// "How to register" section is copied verbatim from example_test.go, and this
// fails if the two ever diverge.
func TestReadmeExampleIsTheCompiledOne(t *testing.T) {
	source, err := os.ReadFile("example_test.go")
	if err != nil {
		t.Fatalf("read example_test.go: %v", err)
	}
	idx := strings.Index(string(source), "type queueSink struct")
	if idx < 0 {
		t.Fatal("example_test.go no longer declares queueSink; update README.md and this guard together")
	}
	example := strings.TrimRight(string(source[idx:]), "\n")

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if !strings.Contains(string(readme), example) {
		t.Error("README.md's worked example is not the one in example_test.go verbatim; " +
			"copy the compiled example into the README rather than editing the README by hand")
	}
}
