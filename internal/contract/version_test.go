// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// THE TIE BETWEEN version.go AND THE VENDORED DOCUMENT (0064).
//
// The constants in version.go are this server's claim about which contract it
// implements. That claim is worth something only if it is checked against the
// vendored bytes rather than against memory, so this test reads each number out
// of contract/control.yaml and contract/UPSTREAM and compares. A `make
// contract-sync` that moves either number fails here, and the message says
// what the next session has to decide.
//
// The reader is a function of the bytes so that the failure can be PROVED:
// TestTheTieFailsWhenTheDocumentMoves feeds it the vendored document with each
// number moved and asserts every move is caught. A tie that has only ever been
// seen to pass has not been seen to tie anything.

// documentNumbers is what the vendored document says about this server's two
// numbers, read from every place it says them.
type documentNumbers struct {
	// Version is `info.version`.
	Version string
	// Vocabularies is every statement of the current vocabulary: each "The
	// current vocabulary is `N`" in the document's prose, and the
	// `policy_version` field's example. The document states it in more than
	// one place, and a revision that moved one and not another is a document
	// this server cannot claim to implement until a human reads it.
	Vocabularies []vocabularyStatement
	// Minimum is the `policy_version` field's `minimum`, or nil.
	Minimum *int32
}

type vocabularyStatement struct {
	where string
	value int32
}

// currentVocabulary matches the document's present-tense statement of the
// vocabulary, across the line break the prose wraps it at.
var currentVocabulary = regexp.MustCompile("The current vocabulary is\\s+`([0-9]+)`")

// versionDoc is the slice of the OpenAPI document readNumbers needs.
type versionDoc struct {
	Info struct {
		Version     string `yaml:"version"`
		Description string `yaml:"description"`
	} `yaml:"info"`
	Components struct {
		Schemas map[string]struct {
			Properties map[string]struct {
				Description string    `yaml:"description"`
				Example     yaml.Node `yaml:"example"`
				Minimum     yaml.Node `yaml:"minimum"`
			} `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func readNumbers(raw []byte) (documentNumbers, error) {
	var doc versionDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return documentNumbers{}, err
	}
	out := documentNumbers{Version: doc.Info.Version}
	prose := func(where, text string) error {
		for _, m := range currentVocabulary.FindAllStringSubmatch(text, -1) {
			v, err := strconv.ParseInt(m[1], 10, 32)
			if err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			out.Vocabularies = append(out.Vocabularies, vocabularyStatement{where: where, value: int32(v)})
		}
		return nil
	}
	if err := prose("info.description", doc.Info.Description); err != nil {
		return documentNumbers{}, err
	}
	field, ok := doc.Components.Schemas["AuthorizeRequest"].Properties["policy_version"]
	if !ok {
		return documentNumbers{}, fmt.Errorf("the document has no AuthorizeRequest.policy_version")
	}
	if err := prose("AuthorizeRequest.policy_version.description", field.Description); err != nil {
		return documentNumbers{}, err
	}
	if field.Example.Kind != 0 {
		var v int32
		if err := field.Example.Decode(&v); err != nil {
			return documentNumbers{}, fmt.Errorf("AuthorizeRequest.policy_version.example: %w", err)
		}
		out.Vocabularies = append(out.Vocabularies, vocabularyStatement{
			where: "AuthorizeRequest.policy_version.example", value: v,
		})
	}
	if field.Minimum.Kind != 0 {
		var v int32
		if err := field.Minimum.Decode(&v); err != nil {
			return documentNumbers{}, fmt.Errorf("AuthorizeRequest.policy_version.minimum: %w", err)
		}
		out.Minimum = &v
	}
	return out, nil
}

// disagreements lists every way the document's numbers differ from version.go.
// Empty is the only passing answer.
func disagreements(n documentNumbers) []string {
	var out []string
	if n.Version != DocumentVersion {
		out = append(out, fmt.Sprintf(
			"info.version is %q and DocumentVersion is %q: the vendored document moved. Move DocumentVersion, and "+
				"check whether policy_version moved with it — the two are independent and either may move alone (PLAN §4)",
			n.Version, DocumentVersion))
	}
	if len(n.Vocabularies) == 0 {
		out = append(out, "the document states no current vocabulary (neither the prose \"The current vocabulary is `N`\" "+
			"nor policy_version's example): this test can no longer tell which vocabulary it describes")
	}
	for _, s := range n.Vocabularies {
		if s.value != PolicyVersion {
			out = append(out, fmt.Sprintf(
				"%s states vocabulary %d and PolicyVersion is %d: the vendored document moved the vocabulary. "+
					"This server serves exactly one (0064), so moving the constant changes which proxies it answers — "+
					"every proxy declaring %d is refused once it moves. Ship the proxy build that declares the new one with "+
					"it, and decide what each new field means for the decision path before you do (PLAN §4)",
				s.where, s.value, PolicyVersion, PolicyVersion))
		}
	}
	switch {
	case n.Minimum == nil:
		out = append(out, "AuthorizeRequest.policy_version carries no minimum; PolicyVersionMinimum has nothing to agree with")
	case *n.Minimum != PolicyVersionMinimum:
		out = append(out, fmt.Sprintf("policy_version's minimum is %d and PolicyVersionMinimum is %d",
			*n.Minimum, PolicyVersionMinimum))
	}
	if PolicyVersion < PolicyVersionMinimum {
		out = append(out, fmt.Sprintf("PolicyVersion %d is below the field's minimum %d", PolicyVersion, PolicyVersionMinimum))
	}
	return out
}

func vendoredBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "contract", "control.yaml"))
	if err != nil {
		t.Fatalf("read vendored contract: %v", err)
	}
	return b
}

func TestTheNumbersAreTheVendoredDocuments(t *testing.T) {
	n, err := readNumbers(vendoredBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range disagreements(n) {
		t.Error(d)
	}
	// Three statements today: two in prose and the example. Fewer means the
	// reader stopped finding one, which would leave the tie weaker than it
	// reads.
	if len(n.Vocabularies) < 3 {
		t.Errorf("read %d statements of the current vocabulary (%v), want the two in prose and the example",
			len(n.Vocabularies), n.Vocabularies)
	}

	b, err := os.ReadFile(filepath.Join("..", "..", "contract", "UPSTREAM"))
	if err != nil {
		t.Fatal(err)
	}
	var commit string
	for line := range strings.SplitSeq(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "commit="); ok {
			commit = strings.TrimSpace(v)
		}
	}
	if commit != VendoredCommit {
		t.Errorf("VendoredCommit = %q, contract/UPSTREAM says %q", VendoredCommit, commit)
	}
}

// TestTheTieFailsWhenTheDocumentMoves proves the failure rather than assuming
// it: the vendored document, with each number moved the way an upstream
// revision would move it, must be caught — up, down, in one place only, and
// with the statement gone.
func TestTheTieFailsWhenTheDocumentMoves(t *testing.T) {
	raw := vendoredBytes(t)
	next := strconv.Itoa(int(PolicyVersion) + 1)
	prev := strconv.Itoa(int(PolicyVersion) - 1)

	cases := []struct {
		name   string
		mutate func(*testing.T, *yaml.Node)
	}{
		{"the document version rises", func(t *testing.T, n *yaml.Node) {
			setScalar(t, n, "9.9.9", "info", "version")
		}},
		{"the document version falls, as #53 moved it", func(t *testing.T, n *yaml.Node) {
			setScalar(t, n, "0.0.1", "info", "version")
		}},
		{"the vocabulary rises everywhere", func(t *testing.T, n *yaml.Node) {
			replaceVocabulary(t, n, next)
			setScalar(t, n, next, policyVersionPath("example")...)
		}},
		{"the vocabulary falls everywhere", func(t *testing.T, n *yaml.Node) {
			replaceVocabulary(t, n, prev)
			setScalar(t, n, prev, policyVersionPath("example")...)
		}},
		{"only the example moves", func(t *testing.T, n *yaml.Node) {
			setScalar(t, n, next, policyVersionPath("example")...)
		}},
		{"only the prose moves", func(t *testing.T, n *yaml.Node) {
			replaceVocabulary(t, n, next)
		}},
		{"the minimum moves", func(t *testing.T, n *yaml.Node) {
			setScalar(t, n, next, policyVersionPath("minimum")...)
		}},
		{"the document stops stating a vocabulary", func(t *testing.T, n *yaml.Node) {
			replaceVocabularyText(t, n, "")
			deleteKey(t, n, "example", policyVersionPath()...)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var root yaml.Node
			if err := yaml.Unmarshal(raw, &root); err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, &root)
			mutated, err := yaml.Marshal(&root)
			if err != nil {
				t.Fatal(err)
			}
			n, err := readNumbers(mutated)
			if err != nil {
				t.Fatal(err)
			}
			if got := disagreements(n); len(got) == 0 {
				t.Errorf("a document with %s passes the tie; the next contract sync would land unnoticed", tc.name)
			}
		})
	}

	// And the unmutated document, round-tripped through the same marshal,
	// still passes — so each failure above is the mutation's and not the
	// round trip's.
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	same, err := yaml.Marshal(&root)
	if err != nil {
		t.Fatal(err)
	}
	n, err := readNumbers(same)
	if err != nil {
		t.Fatal(err)
	}
	if got := disagreements(n); len(got) != 0 {
		t.Errorf("the round-tripped document disagrees with itself: %v", got)
	}
}

func policyVersionPath(rest ...string) []string {
	return append([]string{"components", "schemas", "AuthorizeRequest", "properties", "policy_version"}, rest...)
}

// mappingAt walks a mapping path and returns the node at its end.
func mappingAt(t *testing.T, root *yaml.Node, path ...string) *yaml.Node {
	t.Helper()
	n := root
	if n.Kind == yaml.DocumentNode {
		n = n.Content[0]
	}
	for _, key := range path {
		if n.Kind != yaml.MappingNode {
			t.Fatalf("%s: not a mapping", strings.Join(path, "."))
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
			}
		}
		if next == nil {
			t.Fatalf("the document has no %s", strings.Join(path, "."))
		}
		n = next
	}
	return n
}

func setScalar(t *testing.T, root *yaml.Node, value string, path ...string) {
	t.Helper()
	mappingAt(t, root, path...).Value = value
}

func deleteKey(t *testing.T, root *yaml.Node, key string, path ...string) {
	t.Helper()
	m := mappingAt(t, root, path...)
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
	t.Fatalf("no %s under %s", key, strings.Join(path, "."))
}

// replaceVocabulary moves every prose statement of the vocabulary to v.
func replaceVocabulary(t *testing.T, root *yaml.Node, v string) {
	t.Helper()
	replaceVocabularyText(t, root, "The current vocabulary is `"+v+"`")
}

func replaceVocabularyText(t *testing.T, root *yaml.Node, with string) {
	t.Helper()
	for _, path := range [][]string{{"info", "description"}, policyVersionPath("description")} {
		n := mappingAt(t, root, path...)
		if !currentVocabulary.MatchString(n.Value) {
			t.Fatalf("%s states no vocabulary to move", strings.Join(path, "."))
		}
		n.Value = currentVocabulary.ReplaceAllString(n.Value, with)
	}
}
