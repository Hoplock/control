// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package declarative

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// A request template is text with placeholders, and the placeholders are a
// CLOSED set: what a probe may say about the access it asks about, and nothing
// else. It is not text/template on purpose — a template language with
// functions, conditionals and range is a program, and the configuration is
// untrusted-ish input. Every value is escaped for where it lands: a URL
// component, or the inside of a JSON string.
var placeholders = map[string]bool{
	"tenant":          true,
	"subject.id":      true,
	"target.hostname": true,
	"target.zone":     true,
	"reference":       true,
	"scope":           true,
	"at":              true,
}

// sentinel stands in for every placeholder when a template's shape is checked
// at start-up. It survives URL and JSON escaping unchanged, so wherever it
// appears after parsing is where a value would appear.
const sentinel = "hoplockplaceholder"

type template struct {
	parts []templatePart
}

type templatePart struct {
	literal     string
	placeholder string
}

func parseTemplate(s string) (template, error) {
	var t template
	for s != "" {
		open := strings.Index(s, "{{")
		if open < 0 {
			t.parts = append(t.parts, templatePart{literal: s})
			break
		}
		if open > 0 {
			t.parts = append(t.parts, templatePart{literal: s[:open]})
		}
		end := strings.Index(s[open:], "}}")
		if end < 0 {
			return template{}, fmt.Errorf("an unclosed `{{`")
		}
		name := strings.TrimSpace(s[open+2 : open+end])
		if !placeholders[name] {
			return template{}, fmt.Errorf("`{{%s}}` is not a placeholder; use tenant, subject.id, target.hostname, target.zone, reference, scope or at", name)
		}
		t.parts = append(t.parts, templatePart{placeholder: name})
		s = s[open+end+2:]
	}
	return t, nil
}

func (t template) render(values map[string]string, escape func(string) string) string {
	var b strings.Builder
	for _, p := range t.parts {
		if p.placeholder == "" {
			b.WriteString(p.literal)
			continue
		}
		b.WriteString(escape(values[p.placeholder]))
	}
	return b.String()
}

func (t template) sample() string {
	return t.render(nil, func(string) string { return sentinel })
}

func (t template) empty() bool { return len(t.parts) == 0 }

// urlEscape makes a value safe as a path segment and as a query value alike:
// it escapes `/`, `?`, `#`, `&` and `=`, so a subject id cannot add a path
// segment or a query parameter, and it writes a space as %20 rather than `+`.
func urlEscape(v string) string {
	return strings.ReplaceAll(url.QueryEscape(v), "+", "%20")
}

// jsonEscape makes a value safe inside a JSON string literal: it can end
// neither the string nor the document.
func jsonEscape(v string) string {
	b, err := json.Marshal(v)
	if err != nil || len(b) < 2 {
		return ""
	}
	return string(b[1 : len(b)-1])
}
