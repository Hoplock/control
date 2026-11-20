// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package control_test

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/contract"
)

// NO SECOND VERSION LITERAL (0064).
//
// Hoplock Control implements one contract, and internal/contract/version.go is
// the one place its two numbers are spelled: the policy vocabulary it serves
// (`PolicyVersion`) and the document version it vendors (`DocumentVersion`).
// That file's test ties both to contract/control.yaml. This test keeps every
// OTHER file in the tree — code, tests, fixtures, deployment manifests, seed
// data, the console's e2e fixtures — from spelling either one, because a
// second copy is a number that does not move when the contract does: a
// fixture declaring the old vocabulary is refused by this server the day the
// constant moves, and a manifest naming the old document version is a
// deployment that says it speaks a contract it does not.
//
// What it looks for, line by line, in every text file outside the excluded
// directories below:
//
//   - a key or identifier naming a vocabulary — `policy_version`,
//     `PolicyVersion`, `contract_version`, `ContractVersion`, or one named
//     `Vocabulary…` — assigned or compared to a non-zero integer literal.
//     Zero is allowed: it is "declared nothing", the stored value of a proxy
//     that has never asked, and not a vocabulary;
//   - DocumentVersion's value, anywhere;
//   - a key naming the contract's document version (`contract`,
//     `contract_version`, `document_version`) given a dotted version.
//
// Read the number from internal/contract instead. A browser-side fixture,
// which cannot import a Go package, reads it out of the vendored document as
// ui/e2e/fixture/contract.ts does.
//
// What it does NOT look at, and why:
//
//   - contract/: the vendored document is the source both numbers are read
//     from.
//   - Markdown: the plan, the learnings, the changelog and the prompts are a
//     record of which number held when, and history is not a literal any
//     program reads.
//   - a directory named `dist`: built output, generated from sources this
//     scans; node_modules and .git.
//   - the north-bound API's version (`Hoplock-Api-Version`, `north_api`),
//     the registration protocol's, the module's release, and a policy
//     BUNDLE's version (`policy/bundles`, a tenant's active `policy_version`
//     on the fleet health answer): different numbers with lifecycles of their
//     own (M19, M23), and none of them is the contract's.
//
// A literal passed positionally — `authorize(route, 5)` — is not a shape a
// line scan can tell from any other integer, so the review owns it; the tests
// in internal/decision and cmd/pdpconform derive every version they send from
// the constants, which is the pattern to copy.
func TestNoSecondVersionLiteral(t *testing.T) {
	root := repoRootDir(t)
	found := scanVersionLiterals(t, root)
	for _, f := range found {
		t.Errorf("%s:%d spells a contract version (%s): read it from internal/contract (version.go) instead — "+
			"a second copy does not move when the contract does: %s", f.file, f.line, f.why, f.text)
	}

	// The check must have looked at the places a literal used to be, or it
	// proves nothing about them.
	for _, want := range []string{
		filepath.Join("internal", "contract", "version.go"),
		filepath.Join("cmd", "pdpconform", "testdata", "control-expectations.yaml"),
		filepath.Join("ui", "e2e", "fixture", "algorithms.ts"),
		filepath.Join("deploy", "compose.yaml"),
	} {
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			t.Errorf("%s is gone; point this test at where its content moved", want)
		}
	}
}

// TestTheVersionLiteralCheckCatchesEachShape proves the scan: every shape it
// exists for is caught in a throwaway tree, the one definition is not, and
// neither is anything it deliberately leaves alone.
func TestTheVersionLiteralCheckCatchesEachShape(t *testing.T) {
	served := strconv.Itoa(int(contract.PolicyVersion))
	other := strconv.Itoa(int(contract.PolicyVersion) - 1)
	doc := contract.DocumentVersion
	// Built by concatenation so that this file's own lines do not match.
	pv, cv := "policy_"+"version", "contract_"+"version"

	caught := map[string]string{
		"a.yaml":            pv + ": " + served + "\n",
		"b.ts":              "      " + pv + ": " + other + ",\n",
		"c.json":            `{"` + pv + `": ` + served + `}` + "\n",
		"d_test.go":         "\tp.Contract" + "Version = " + other + "\n",
		"e.go":              "const Vocabulary" + "Baseline int32 = " + other + "\n",
		"f.go":              "\tif h.Contract" + "Version != " + other + " {\n",
		"g.yaml":            "versions:\n  contract: \"" + doc + "\"\n",
		"h.sh":              "echo " + doc + "\n",
		"i.yaml":            cv + ": " + served + "\n",
		"j.tsx":             "  " + cv + ": " + served + ",\n",
		"k.go":              "\treq.Policy" + "Version = " + served + "\n",
		"seed/l.yaml":       "  - " + pv + ": " + other + "\n",
		"deploy/m.yaml":     "    " + cv + ": " + served + "\n",
		"Makefile":          "DOC := " + doc + "\n",
		"deploy/Dockerfile": "LABEL contract=" + doc + "\n",
	}
	allowed := map[string]string{
		filepath.Join("internal", "contract", "version.go"): "\tPolicy" + "Version int32 = " + served + "\n\tDocument" + "Version = \"" + doc + "\"\n",
		"notes.md":            pv + ": " + served + " was the vocabulary in " + doc + "\n",
		"contract/x.yaml":     pv + ": " + served + "\n",
		"dist/bundle.js":      pv + ": " + served + "\n",
		"zero.ts":             cv + ": 0,\n",
		"fine.go":             "\tif h.Contract" + "Version != 0 {\n",
		"bundle.yaml":         "schema_version: 1\nversion: 3\n",
		"north.go":            "\tAPIVersion = 6\n",
		"derived.ts":          "      " + pv + ": POLICY_VERSION,\n",
		"derived.go":          "\treq.Policy" + "Version = &contract.Policy" + "Version\n",
		"node_modules/a.json": pv + ": " + served + "\n",
	}

	dir := t.TempDir()
	for name, body := range caught {
		writeTree(t, dir, name, body)
	}
	for name, body := range allowed {
		writeTree(t, dir, name, body)
	}

	var files []string
	for _, f := range scanVersionLiterals(t, dir) {
		files = append(files, filepath.ToSlash(f.file))
	}
	for name := range caught {
		if !slices.Contains(files, name) {
			t.Errorf("the check missed %s (%q)", name, caught[name])
		}
	}
	for name := range allowed {
		if slices.Contains(files, filepath.ToSlash(name)) {
			t.Errorf("the check flagged %s (%q), which it must leave alone", name, allowed[name])
		}
	}
}

func writeTree(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

type versionLiteral struct {
	file string
	line int
	why  string
	text string
}

var (
	// A vocabulary key or identifier, then an assignment or a comparison,
	// then an integer.
	vocabularyLiteral = regexp.MustCompile(
		`(?i)\b(policy_?version|contract_?version|vocabulary[a-z_]*)\b["']?\s*(?:int32|int)?\s*(?::=|==|!=|:|=)\s*(-?[0-9]+)\b`)
	// A key naming the contract's document version, then a dotted version.
	documentKeyLiteral = regexp.MustCompile(
		`(?i)\b(contract|contract_version|document_version)\b["']?\s*[:=]\s*["']?[0-9]+\.[0-9]+\.[0-9]+`)
)

// definition is the one file allowed to spell the numbers.
var definition = filepath.Join("internal", "contract", "version.go")

// scannedExt are the text files a version could be written in.
var scannedExt = []string{
	".go", ".yaml", ".yml", ".json", ".sql", ".ts", ".tsx", ".js", ".mjs", ".cjs",
	".sh", ".toml", ".env", ".conf", ".tmpl", ".template", ".txt",
}

func scanVersionLiterals(t *testing.T, root string) []versionLiteral {
	t.Helper()
	var found []versionLiteral
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch {
			case rel == "contract", d.Name() == ".git", d.Name() == "node_modules", d.Name() == "dist":
				return fs.SkipDir
			}
			return nil
		}
		if rel == definition || !scanned(d.Name()) {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for n := 1; sc.Scan(); n++ {
			line := sc.Text()
			why := ""
			for _, m := range vocabularyLiteral.FindAllStringSubmatch(line, -1) {
				if m[2] != "0" {
					why = "a vocabulary literal"
				}
			}
			if why == "" && strings.Contains(line, contract.DocumentVersion) {
				why = "the document version"
			}
			if why == "" && documentKeyLiteral.MatchString(line) {
				why = "a document version"
			}
			if why != "" {
				found = append(found, versionLiteral{file: rel, line: n, why: why, text: strings.TrimSpace(line)})
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return found
}

func scanned(name string) bool {
	switch {
	case strings.HasSuffix(name, ".md"):
		return false
	case name == "package-lock.json":
		// Dependency versions, every one of them dotted.
		return false
	case name == "Makefile" || strings.HasPrefix(name, "Dockerfile"):
		return true
	}
	return slices.Contains(scannedExt, filepath.Ext(name))
}
