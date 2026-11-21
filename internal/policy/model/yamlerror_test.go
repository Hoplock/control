// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// THE YAML LIBRARY'S ENGLISH NEVER REACHES A REJECTION (0065). These tests are
// internal because what they hold is the reading of the library's messages,
// which no caller sees.

// libraryProblems is every problem string yaml.v3's scanner, parser and
// reader can report, read from the library's own source: the arguments that
// name the problem in each call that sets one.
func libraryProblems(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "gopkg.in/yaml.v3").Output()
	if err != nil {
		t.Skipf("cannot locate gopkg.in/yaml.v3's source: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	// The position of the problem argument in each setter's call.
	setters := map[string]int{
		"yaml_parser_set_scanner_error":        3,
		"yaml_parser_set_parser_error":         1,
		"yaml_parser_set_parser_error_context": 3,
		"yaml_parser_set_reader_error":         1,
		"yaml_parser_set_scanner_tag_error":    3,
		// decode.go's own failures, whose fixed ones are problems too.
		"failf": 0,
	}
	var problems []string
	fset := token.NewFileSet()
	for _, name := range []string{"scannerc.go", "parserc.go", "readerc.go", "decode.go"} {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			i, ok := setters[id.Name]
			if !ok || i >= len(call.Args) {
				return true
			}
			if lit, ok := call.Args[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				// A format string is a family, classified by its prefix
				// below, not a problem to look up.
				if s, err := strconv.Unquote(lit.Value); err == nil && !formatVerb.MatchString(s) && !slices.Contains(problems, s) {
					problems = append(problems, s)
				}
			}
			return true
		})
	}
	if len(problems) < 40 {
		t.Fatalf("read only %d problems from yaml.v3's source; the reading of it has drifted", len(problems))
	}
	return problems
}

// formatVerb is a Printf verb, which makes a literal a family of messages.
var formatVerb = regexp.MustCompile(`%[#+\- 0-9.]*[a-z]`)

// internalProblems are the library's reports of its own misuse — a state the
// state machine cannot reach, a reader nobody set — which no document causes.
var internalProblems = []string{
	"invalid parser state",
	"read handler must be set",
	"impossible",
	"attempted to go past the end of stream; corrupted value?",
	"map merge requires map or sequence of maps as the value",
}

// TestEveryProblemTheParserCanReportHasAKind: an upgrade of yaml.v3 that words
// a problem differently would read it as `unreadable`, which is still a code
// but tells an author nothing. This fails first, naming the string.
func TestEveryProblemTheParserCanReportHasAKind(t *testing.T) {
	for _, p := range libraryProblems(t) {
		if slices.Contains(internalProblems, p) {
			continue
		}
		if got := yamlProblemOf(p); got == YAMLUnreadable {
			t.Errorf("yaml.v3 can report %q, and it reads as %s: give it a kind in yamlProblems", p, got)
		}
	}
	for p := range yamlProblems {
		if !slices.Contains(libraryProblems(t), p) {
			t.Errorf("yamlProblems classifies %q, which yaml.v3 does not report: remove it", p)
		}
	}
}

// TestTheLibrarysMessagesReadAsCodes: each shape of message yaml.v3 writes
// into a TypeError, and the structured one this package writes itself.
func TestTheLibrarysMessagesReadAsCodes(t *testing.T) {
	for _, tc := range []struct {
		msg    string
		code   Code
		params map[string]string
	}{
		{"field permissions not found in type model.Rule", CodeKeyUnknown, map[string]string{"key": "permissions"}},
		{`mapping key "effect" already defined at line 5`, CodeKeyDuplicate, map[string]string{"key": "effect", "other_line": "5"}},
		{"cannot unmarshal !!str `sre` into []string", CodeValueWrongType, map[string]string{"found": "string", "expected": "list"}},
		{"cannot unmarshal !!seq into model.Route", CodeValueWrongType, map[string]string{"found": "list", "expected": "mapping"}},
		{"cannot unmarshal !!map into model.ChannelType", CodeValueWrongType, map[string]string{"found": "mapping", "expected": "string"}},
		{"cannot unmarshal !!str `x` into int64", CodeValueWrongType, map[string]string{"found": "string", "expected": "integer"}},
		{"cannot unmarshal !foo `x` into *bool", CodeValueWrongType, map[string]string{"found": "other", "expected": "boolean"}},
		{"cannot unmarshal !!int `1` into chan int", CodeValueWrongType, map[string]string{"found": "integer", "expected": "other"}},
		{"field x already set in type model.Rule", CodeDocumentMalformed, map[string]string{"problem": "unreadable"}},
		{"did not find expected key", CodeDocumentMalformed, map[string]string{"problem": "structure"}},
		{"unknown anchor 'a' referenced", CodeDocumentMalformed, map[string]string{"problem": "alias"}},
		{"anchor 'a' value contains itself", CodeDocumentMalformed, map[string]string{"problem": "alias"}},
		{"something this release never wrote", CodeDocumentMalformed, map[string]string{"problem": "unreadable"}},
	} {
		r := rejectionOf(7, tc.msg)
		if r.Code != tc.code || r.Line != 7 {
			t.Errorf("%q: got %s at line %d, want %s at line 7", tc.msg, r.Code, r.Line, tc.code)
			continue
		}
		for k, want := range tc.params {
			if r.Params[k] != want {
				t.Errorf("%q: %s = %q, want %q", tc.msg, k, r.Params[k], want)
			}
		}
		if len(r.Params) != len(tc.params) {
			t.Errorf("%q: parameters %v, want exactly %v", tc.msg, r.Params, tc.params)
		}
	}

	// A rejection an UnmarshalYAML wrote survives the library intact.
	err := nodeReject(&yaml.Node{Line: 3}, CodeValueUnknown, "value", `a "quoted" {value}`, "vocabulary", "day", "allowed", `"monday"`)
	msg := err.Error()
	line, rest := splitYAMLError(strings.TrimPrefix(msg, "yaml: unmarshal errors:\n  "))
	r := rejectionOf(line, rest)
	if r.Code != CodeValueUnknown || r.Line != 3 || r.Params["value"] != `a "quoted" {value}` || r.Params["vocabulary"] != "day" {
		t.Errorf("a structured rejection came back as %+v", r)
	}
}

// TestEveryNamedTypeOfTheSchemaHasAShape: a named type is found by the name
// yaml.v3 prints for it, so the walk must reach the types an author writes.
func TestEveryNamedTypeOfTheSchemaHasAShape(t *testing.T) {
	for name, want := range map[string]Shape{
		"model.Rule": ShapeMapping, "model.Match": ShapeMapping, "model.Route": ShapeMapping,
		"model.ChannelType": ShapeString, "model.Day": ShapeString, "model.Effect": ShapeString,
		"[]model.Rule": ShapeList, "map[string][]string": ShapeMapping, "*model.CacheHint": ShapeMapping,
	} {
		if got := shapeOfGoType(name); got != want {
			t.Errorf("%s reads as %s, want %s", name, got, want)
		}
	}
}

// TestAWordedValueReadsAsWordsAndTravelsAsAnIdentifier: the English is the
// message's, the identifier is the parameter's.
func TestAWordedValueReadsAsWordsAndTravelsAsAnIdentifier(t *testing.T) {
	r := Reject(CodeValueWrongType, "", 4, "found", "string", "expected", "list")
	if got, want := r.Message(), "this value is a string, where the schema expects a list"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if r.Params["found"] != "string" {
		t.Errorf("the parameter travels as %q", r.Params["found"])
	}
	for code, params := range Enumerations() {
		for name, values := range params {
			if !slices.Contains(Parameters(code), name) {
				t.Errorf("%s words %s, which its message does not name", code, name)
			}
			for _, v := range values {
				if _, ok := worded[code][name][v]; !ok {
					t.Errorf("%s's %s value %q has no words", code, name, v)
				}
			}
		}
	}
}
