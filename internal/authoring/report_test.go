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
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
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

// documentLevel are the codes Control's checks say about the bundle or the
// fleet as a whole, never about one rule: they name no rule and are placed at
// no line. Every other code is about a rule, and the invariant below makes
// each of them fire.
var documentLevel = []string{
	CodeProxiesUnreported, CodeContractVersionUnsupported, CodeBanUnmatched, CodePolicyUnmetTruncated,
}

// ruleLines reads each rule's `- id:` line from the source text itself, so the
// expectation is not taken from the code under test.
func ruleLines(source string) map[string]int {
	out := map[string]int{}
	for i, line := range strings.Split(source, "\n") {
		if id, ok := strings.CutPrefix(strings.TrimSpace(line), "- id: "); ok {
			out[id] = i + 1
		}
	}
	return out
}

// THE INVARIANT (0067): every finding whose rule names a rule of the program
// carries exactly that rule's line, whoever said it, and every finding that
// carries a line names a rule. The bundle makes every rule-level check Control
// has fire, and a validator names a rule, a document pointer, an id no rule
// has, and nothing. A rule-level code this fixture does not fire fails here
// until it does — which is where a new check that names no rule, or another
// rule's line, is caught rather than in review.
func TestEveryFindingThatNamesARuleIsAtThatRulesLine(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	rules, facts := everyKind(now)
	// everyKind fires most rule-level checks. These fire the rest: a target
	// the floored rule reaches with no record at all (unproven, and not
	// reported against the floor), and a build whose declaration says
	// nothing of the profile the ban rule is judged under.
	facts.Satisfaction[1].Targets = append(facts.Satisfaction[1].Targets,
		fleet.TargetVerdict{Hostname: "new.example.com", Missing: []string{"x"}})
	facts.Declarations = append(facts.Declarations, fleet.DeclaredAlgorithms{ProxyID: "proxy-3",
		Declaration: fleet.AlgorithmDeclaration{AlgorithmFloors: []contract.AlgorithmFloorCapability{
			{Level: contract.AlgorithmFloorModernKex, KeyExchanges: []string{contract.KexCurve25519}},
		}}})

	c := candidate(t, rules)
	lines := ruleLines(header + rules)
	for i := 0; i < c.Program.Len(); i++ {
		if r := c.Program.Rule(i); lines[r.ID()] != r.Line() {
			t.Fatalf("rule %q compiled at line %d; its `- id:` is at line %d", r.ID(), r.Line(), lines[r.ID()])
		}
	}

	out := controlFindings(c.Bundle, c.Program, facts, DefaultInterpreters)
	const provider = "example.com/governance"
	for _, path := range []string{"floored", "/rules/0", "nowhere", ""} {
		out = append(out, validatorFinding(provider, ext.Finding{Severity: ext.FindingWarning, Code: "acme.check", Path: path}))
	}
	locate(out, c.Program)

	fired := map[string]bool{}
	for _, f := range out {
		fired[f.Code] = true
		want, names := lines[f.Rule]
		switch {
		case names && f.Line != want:
			t.Errorf("%s names rule %q, at line %d, and carries line %d", f.ID, f.Rule, want, f.Line)
		case !names && f.Line != 0:
			t.Errorf("%s names no rule of the program (rule %q) and carries line %d", f.ID, f.Rule, f.Line)
		}
		if f.Source == SourceControl && !slices.Contains(documentLevel, f.Code) && !names {
			t.Errorf("%s is a rule-level check's and names no rule of the program (rule %q)", f.ID, f.Rule)
		}
		if f.Source == SourceControl && slices.Contains(documentLevel, f.Code) && f.Rule != "" {
			t.Errorf("%s is a document-level check's and names rule %q", f.ID, f.Rule)
		}
	}
	for _, code := range ControlCodes {
		if !slices.Contains(documentLevel, code) && !fired[code] {
			t.Errorf("the fixture does not fire %s: make it fire here, or list it in documentLevel if it is about no rule", code)
		}
	}

	// The validator's findings: placed by Path when it is a rule's id, and
	// otherwise not, with Rule and the id exactly as they were.
	for _, f := range out {
		if f.Source != provider {
			continue
		}
		if f.ID != "acme.check/"+provider+"/"+f.Rule {
			t.Errorf("a validator's finding has id %q, want <code>/<provider>/<path>", f.ID)
		}
		if f.Rule == "floored" && f.Line != lines["floored"] {
			t.Errorf("a validator naming rule floored is at line %d, want %d", f.Line, lines["floored"])
		}
	}
}
