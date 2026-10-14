// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package authoring

import (
	"slices"
	"strings"
	"testing"
)

func allowFor(id, host string) string {
	return `  - id: ` + id + `
    effect: allow
    match:
      target:
        hostnames: [` + host + `]
` + route("")
}

// A diff says what changed in the policy's own terms — rules added, removed,
// changed and moved — and then in its text.
func TestADiffNamesRulesBeforeLines(t *testing.T) {
	t.Parallel()
	a := header + allowFor("one", "a.example.com") + allowFor("two", "b.example.com") + allowFor("three", "c.example.com")
	b := strings.Replace(header, "groups: [sre]", "groups: [sre, dba]", 1) +
		allowFor("three", "c.example.com") + allowFor("one", "a2.example.com") + allowFor("four", "d.example.com")

	d, err := DiffSources([]byte(a), []byte(b))
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !slices.Equal(d.Added, []string{"four"}) || !slices.Equal(d.Removed, []string{"two"}) ||
		!slices.Equal(d.Changed, []string{"one"}) {
		t.Errorf("added %v removed %v changed %v; want [four] [two] [one]", d.Added, d.Removed, d.Changed)
	}
	if !slices.Equal(d.Moved, []string{"three", "one"}) {
		t.Errorf("moved = %v; three now answers before one", d.Moved)
	}
	if !slices.Equal(d.Document, []string{"groups"}) {
		t.Errorf("document = %v, want [groups]", d.Document)
	}
	if d.TextOmitted || !strings.Contains(d.Text, "-        hostnames: [a.example.com]") ||
		!strings.Contains(d.Text, "+        hostnames: [a2.example.com]") || !strings.HasPrefix(d.Text, "@@ -") {
		t.Errorf("text diff:\n%s", d.Text)
	}

	same, err := DiffSources([]byte(a), []byte(a))
	if err != nil || same.Text != "" || len(same.Added)+len(same.Removed)+len(same.Changed)+len(same.Moved) != 0 {
		t.Errorf("a bundle diffed with itself: %+v, %v", same, err)
	}
}
