// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// consoleMappingCorpus is the document the console's claim-mapping editor
// round-trips byte for byte (ui/test/claimMappingDocument.test.ts, 0068).
const consoleMappingCorpus = "../../ui/test/mapping-corpus.yaml"

// The corpus the console's lossless round trip is proven over is a mapping
// this server accepts, and it uses every key the schema has — found by
// walking the document types the parser decodes into, so a key added to the
// schema later fails here until the corpus uses it, and the console's editor
// is proven over it too.
func TestTheConsolesMappingCorpusParses(t *testing.T) {
	doc, err := os.ReadFile(filepath.FromSlash(consoleMappingCorpus))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseMapping(doc); err != nil {
		t.Fatalf("the corpus is not a mapping this server accepts: %v", err)
	}

	var node yaml.Node
	if err := yaml.Unmarshal(doc, &node); err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	var walk func(n *yaml.Node, path string)
	walk = func(n *yaml.Node, path string) {
		switch n.Kind {
		case yaml.DocumentNode, yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c, path)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := path + "." + n.Content[i].Value
				used[key] = true
				walk(n.Content[i+1], key)
			}
		case yaml.ScalarNode, yaml.AliasNode:
			// A value: it names no key.
		}
	}
	walk(&node, "")

	var keys func(t reflect.Type, path string) []string
	keys = func(t reflect.Type, path string) []string {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return nil
		}
		var out []string
		for i := range t.NumField() {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
			if name == "" || name == "-" {
				continue
			}
			key := path + "." + name
			out = append(out, key)
			out = append(out, keys(t.Field(i).Type, key)...)
		}
		return out
	}
	schema := keys(reflect.TypeFor[mappingDocument](), "")
	if len(schema) < 10 {
		t.Fatalf("the schema walk found only %v", schema)
	}
	for _, key := range schema {
		if !used[key] {
			t.Errorf("the corpus never uses %s", strings.TrimPrefix(key, "."))
		}
	}
}
