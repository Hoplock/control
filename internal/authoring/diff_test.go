// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package authoring

import (
	"slices"
	"strconv"
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

// Moved is right when rules are inserted, removed and reordered at once: only
// the rules both versions hold are compared, by their order among themselves.
func TestMovedIsJudgedAmongTheRulesBothVersionsHold(t *testing.T) {
	t.Parallel()
	a := header + allowFor("a", "a.example.com") + allowFor("b", "b.example.com") +
		allowFor("c", "c.example.com") + allowFor("d", "d.example.com")
	// a removed, x inserted first, c and b swapped, d still last.
	b := header + allowFor("x", "x.example.com") + allowFor("c", "c.example.com") +
		allowFor("b", "b.example.com") + allowFor("d", "d.example.com")
	d, err := DiffSources([]byte(a), []byte(b))
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !slices.Equal(d.Added, []string{"x"}) || !slices.Equal(d.Removed, []string{"a"}) || len(d.Changed) != 0 {
		t.Errorf("added %v removed %v changed %v", d.Added, d.Removed, d.Changed)
	}
	if !slices.Equal(d.Moved, []string{"c", "b"}) {
		t.Errorf("moved = %v, want [c b]; d kept its place among the rules both hold", d.Moved)
	}
	// A rule that only shifted lines because one was inserted above it is
	// neither changed nor moved.
	shifted := header + allowFor("x", "x.example.com") + allowFor("a", "a.example.com") + allowFor("b", "b.example.com") +
		allowFor("c", "c.example.com") + allowFor("d", "d.example.com")
	if d, err := DiffSources([]byte(a), []byte(shifted)); err != nil || len(d.Changed)+len(d.Moved) != 0 {
		t.Errorf("an insertion above: changed %v moved %v (%v)", d.Changed, d.Moved, err)
	}
}

// Against nothing, everything is added and the text is the whole source; a
// source that does not parse is refused rather than diffed.
func TestADiffAgainstNothingAndAgainstNonsense(t *testing.T) {
	t.Parallel()
	b := header + allowFor("one", "a.example.com")
	d, err := DiffSources(nil, []byte(b))
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !slices.Equal(d.Added, []string{"one"}) || len(d.Removed)+len(d.Moved) != 0 || d.TextOmitted ||
		!strings.HasPrefix(d.Text, "@@ -0,0 +1,") || strings.Count(d.Text, "\n+") != strings.Count(b, "\n") {
		t.Errorf("against nothing: %+v", d)
	}
	if !slices.Contains(d.Document, "tenant") || !slices.Contains(d.Document, "groups") {
		t.Errorf("against nothing, the document-level keys = %v", d.Document)
	}
	for _, bad := range [][2]string{{"rules: [", b}, {b, "rules: ["}} {
		if _, err := DiffSources([]byte(bad[0]), []byte(bad[1])); err == nil {
			t.Errorf("a source that does not parse was diffed: %q → %q", bad[0][:8], bad[1][:8])
		}
	}
}

// The text diff is bounded, and says so when the bound stopped it: the
// rule-level answer is then the whole diff.
func TestTheTextDiffIsBoundedAndSaysWhenItWasOmitted(t *testing.T) {
	t.Parallel()
	var a, b strings.Builder
	a.WriteString(header)
	b.WriteString(header)
	for i := 0; i < 1500; i++ {
		a.WriteString(allowFor("r"+strconv.Itoa(i), "a"+strconv.Itoa(i)+".example.com"))
		b.WriteString(allowFor("r"+strconv.Itoa(i), "b"+strconv.Itoa(i)+".example.com"))
	}
	d, err := DiffSources([]byte(a.String()), []byte(b.String()))
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !d.TextOmitted || d.Text != "" {
		t.Errorf("3000 edits over 66000 lines, past the bound of %d: omitted %v, %d bytes of text",
			maxTrace/(2*66000+3), d.TextOmitted, len(d.Text))
	}
	if len(d.Changed) != 1500 {
		t.Errorf("the rule-level answer names %d changed rules, want 1500", len(d.Changed))
	}
}

// Within the bound the text is rendered, however many edits; the bound shrinks
// as the sources grow, so a small source is always rendered whole.
func TestATextDiffWithinTheBoundIsRendered(t *testing.T) {
	t.Parallel()
	var a, b strings.Builder
	a.WriteString(header)
	b.WriteString(header)
	for i := 0; i < 20; i++ {
		a.WriteString(allowFor("r"+strconv.Itoa(i), "a"+strconv.Itoa(i)+".example.com"))
		b.WriteString(allowFor("r"+strconv.Itoa(i), "b"+strconv.Itoa(i)+".example.com"))
	}
	d, err := DiffSources([]byte(a.String()), []byte(b.String()))
	if err != nil || d.TextOmitted || strings.Count(d.Text, "\n-        hostnames") != 20 {
		t.Errorf("20 changed rules: omitted %v, text:\n%s (%v)", d.TextOmitted, d.Text, err)
	}
}
