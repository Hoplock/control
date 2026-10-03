// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package declarative

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// A deliberately small JSON path: enough to reach a field in any answer a
// scanner or an ITSM system gives, and nothing a reviewer cannot read at a
// glance. `$` is the document; `.name` and `['name']` step into an object;
// `[n]` into a list; `[*]` into every element (or every value of an object, in
// key order). No filters, no recursion, no functions — the configuration is
// untrusted-ish input, and a path language with a program in it is a second
// policy language nobody reviews.
type jsonPath struct {
	raw   string
	steps []pathStep
}

type pathStep struct {
	key      string
	index    int
	isIndex  bool
	wildcard bool
}

func parsePath(raw string) (jsonPath, error) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "$") {
		return jsonPath{}, fmt.Errorf("path %q does not start at the document, `$`", raw)
	}
	p := jsonPath{raw: s}
	rest := s[1:]
	for rest != "" {
		switch rest[0] {
		case '.':
			end := 1
			for end < len(rest) && isKeyByte(rest[end]) {
				end++
			}
			if end == 1 {
				return jsonPath{}, fmt.Errorf("path %q has an empty name after `.`", raw)
			}
			p.steps = append(p.steps, pathStep{key: rest[1:end]})
			rest = rest[end:]
		case '[':
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return jsonPath{}, fmt.Errorf("path %q has an unclosed `[`", raw)
			}
			inner := rest[1:end]
			switch {
			case inner == "*":
				p.steps = append(p.steps, pathStep{wildcard: true})
			case len(inner) >= 2 && inner[0] == '\'' && inner[len(inner)-1] == '\'':
				key := inner[1 : len(inner)-1]
				if key == "" || strings.ContainsAny(key, "'[]") {
					return jsonPath{}, fmt.Errorf("path %q has a quoted name this path language cannot read", raw)
				}
				p.steps = append(p.steps, pathStep{key: key})
			default:
				n, err := strconv.Atoi(inner)
				if err != nil || n < 0 {
					return jsonPath{}, fmt.Errorf("path %q: `[%s]` is not an index, `*` or a quoted name", raw, inner)
				}
				p.steps = append(p.steps, pathStep{index: n, isIndex: true})
			}
			rest = rest[end+1:]
		default:
			return jsonPath{}, fmt.Errorf("path %q: unexpected %q", raw, rest[:1])
		}
	}
	return p, nil
}

func isKeyByte(c byte) bool {
	return c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// values returns every value the path reaches in doc, in document order. A
// step that does not apply — a name on a list, an index past the end — reaches
// nothing, which is an answer ("not there") rather than an error.
func (p jsonPath) values(doc any) []any {
	cur := []any{doc}
	for _, st := range p.steps {
		var next []any
		for _, v := range cur {
			switch {
			case st.wildcard:
				switch t := v.(type) {
				case []any:
					next = append(next, t...)
				case map[string]any:
					for _, k := range slices.Sorted(maps.Keys(t)) {
						next = append(next, t[k])
					}
				}
			case st.isIndex:
				if l, ok := v.([]any); ok && st.index < len(l) {
					next = append(next, l[st.index])
				}
			default:
				if m, ok := v.(map[string]any); ok {
					if x, ok := m[st.key]; ok {
						next = append(next, x)
					}
				}
			}
		}
		cur = next
	}
	return cur
}

// flatValues is values, with a single list result opened into its elements:
// `$.hosts` and `$.hosts[*]` say the same thing about a list of hosts.
func (p jsonPath) flatValues(doc any) []any {
	vs := p.values(doc)
	if len(vs) == 1 {
		if l, ok := vs[0].([]any); ok {
			return l
		}
	}
	return vs
}

// decodeJSON reads a document keeping numbers as their text, so a value is
// compared as it was written rather than as a float64 someone rounded.
func decodeJSON(body []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("more than one JSON document")
	}
	return doc, nil
}

// text renders a value for comparison: a string as itself, a number as it was
// written, a boolean or null as its literal, anything else as compact JSON.
func text(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// scalar is the one value a path reaches when exactly one string or number is
// expected — an id, a subject, a reference. Anything else is not one.
func scalar(p jsonPath, doc any) (string, bool) {
	vs := p.values(doc)
	if len(vs) != 1 {
		return "", false
	}
	switch t := vs[0].(type) {
	case string:
		return t, true
	case json.Number:
		return t.String(), true
	}
	return "", false
}
