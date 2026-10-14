// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package authoring

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// A DIFF IS READ BY A REVIEWER, so it says what changed in the policy's own
// terms before it says what changed in its text: which rules were added,
// removed or changed, which moved (order is semantics — first match wins), and
// which document-level declarations differ. The text diff is beneath that, for
// the line-by-line read.

// Diff is what separates two bundles.
type Diff struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
	// Added, Removed and Changed are rule ids.
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Changed []string `json:"changed"`
	// Moved are the rules present in both whose relative order changed,
	// which changes which of them answers a question both match.
	Moved []string `json:"moved"`
	// Document names the top-level declarations that differ: `labels`,
	// `groups`, `scopes`, `algorithm_bans`, `timezone`, `description`.
	Document []string `json:"document"`
	// Text is a unified diff of the sources. TextOmitted reports that the
	// two were too far apart to render one within this server's bound, and
	// the rule-level answer above is the whole diff.
	Text        string `json:"text"`
	TextOmitted bool   `json:"text_omitted,omitempty"`
}

// DiffVersions diffs two stored versions. from may be zero, which diffs
// against nothing: the whole of `to` is added.
func (s *Service) DiffVersions(ctx context.Context, tenant store.Tenant, from, to int64) (Diff, error) {
	var a, b []byte
	if from != 0 {
		got, err := s.st.PolicyBundles().Get(ctx, tenant, from)
		if err != nil {
			return Diff{}, err
		}
		a = got.Source
	}
	got, err := s.st.PolicyBundles().Get(ctx, tenant, to)
	if err != nil {
		return Diff{}, err
	}
	b = got.Source
	d, err := DiffSources(a, b)
	if err != nil {
		return Diff{}, err
	}
	d.From, d.To = from, to
	return d, nil
}

// DiffSources diffs two sources. Both must parse; an empty one is no bundle.
func DiffSources(a, b []byte) (Diff, error) {
	var ba, bb *model.Bundle
	var err error
	if len(bytes.TrimSpace(a)) > 0 {
		if ba, err = model.Parse(a); err != nil {
			return Diff{}, fmt.Errorf("authoring: the old bundle does not parse: %w", err)
		}
	}
	if bb, err = model.Parse(b); err != nil {
		return Diff{}, fmt.Errorf("authoring: the new bundle does not parse: %w", err)
	}
	d := Diff{Added: []string{}, Removed: []string{}, Changed: []string{}, Moved: []string{}, Document: []string{}}
	if ba == nil {
		ba = &model.Bundle{}
	}

	old := map[string]model.Rule{}
	var oldOrder []string
	for _, r := range ba.Rules {
		old[r.ID] = r
		oldOrder = append(oldOrder, r.ID)
	}
	cur := map[string]bool{}
	var common []string
	for _, r := range bb.Rules {
		cur[r.ID] = true
		was, ok := old[r.ID]
		if !ok {
			d.Added = append(d.Added, r.ID)
			continue
		}
		common = append(common, r.ID)
		if !sameYAML(was, r) {
			d.Changed = append(d.Changed, r.ID)
		}
	}
	var oldCommon []string
	for _, id := range oldOrder {
		if !cur[id] {
			d.Removed = append(d.Removed, id)
		} else {
			oldCommon = append(oldCommon, id)
		}
	}
	// A rule moved when its position among the rules both versions hold
	// differs. Reporting each displaced rule, rather than a minimal move set,
	// errs on showing a reviewer more.
	for i := range common {
		if common[i] != oldCommon[i] {
			d.Moved = append(d.Moved, common[i])
		}
	}

	for _, f := range []struct {
		name string
		a, b any
	}{
		{"tenant", ba.Tenant, bb.Tenant},
		{"description", ba.Description, bb.Description},
		{"timezone", ba.Timezone, bb.Timezone},
		{"labels", ba.Labels, bb.Labels},
		{"groups", ba.Groups, bb.Groups},
		{"scopes", ba.Scopes, bb.Scopes},
		{"algorithm_bans", ba.AlgorithmBans, bb.AlgorithmBans},
	} {
		if !sameYAML(f.a, f.b) {
			d.Document = append(d.Document, f.name)
		}
	}

	text, ok := unified(strings.Split(string(a), "\n"), strings.Split(string(b), "\n"))
	d.Text, d.TextOmitted = text, !ok
	if len(bytes.TrimSpace(a)) == 0 {
		d.Text, d.TextOmitted = unifiedAllAdded(strings.Split(string(b), "\n")), false
	}
	return d, nil
}

// sameYAML compares two values by their rendering, which is the authored
// meaning: two rules that render alike say the same thing whatever line they
// started on.
func sameYAML(a, b any) bool {
	ra, errA := yaml.Marshal(a)
	rb, errB := yaml.Marshal(b)
	if errA != nil || errB != nil {
		return reflect.DeepEqual(a, b)
	}
	return bytes.Equal(ra, rb)
}

// maxEdit and maxTrace bound the text diff's work and memory. Past either, the
// rule-level diff stands alone, rather than a request spending its whole budget
// on rendering text.
const (
	maxEdit  = 2000
	maxTrace = 4 << 20
)

type editOp struct {
	kind byte // ' ', '-', '+'
	line string
}

// edits is Myers' O(ND) shortest edit script.
func edits(a, b []string) ([]editOp, bool) {
	n, m := len(a), len(b)
	limit := min(n+m, maxEdit, maxTrace/(2*(n+m)+3))
	offset := n + m + 1
	v := make([]int, 2*(n+m)+3)
	var trace [][]int
	for d := 0; d <= limit; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				return backtrack(trace, a, b, offset), true
			}
		}
	}
	return nil, false
}

func backtrack(trace [][]int, a, b []string, offset int) []editOp {
	x, y := len(a), len(b)
	var ops []editOp
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			ops = append(ops, editOp{' ', a[x-1]})
			x--
			y--
		}
		if d > 0 {
			if x == prevX {
				ops = append(ops, editOp{'+', b[y-1]})
				y--
			} else {
				ops = append(ops, editOp{'-', a[x-1]})
				x--
			}
		}
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

// unified renders an edit script as unified-diff hunks with three lines of
// context.
func unified(a, b []string) (string, bool) {
	ops, ok := edits(a, b)
	if !ok {
		return "", false
	}
	const contextLines = 3
	var out strings.Builder
	oldLine, newLine := make([]int, len(ops)), make([]int, len(ops))
	o, n := 1, 1
	for i, op := range ops {
		oldLine[i], newLine[i] = o, n
		if op.kind != '+' {
			o++
		}
		if op.kind != '-' {
			n++
		}
	}
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := max(0, i-contextLines)
		end := i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-end > 2*contextLines {
				end = min(len(ops), end+contextLines)
				break
			}
			end = run
		}
		oldCount, newCount := 0, 0
		for _, op := range ops[start:end] {
			if op.kind != '+' {
				oldCount++
			}
			if op.kind != '-' {
				newCount++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", oldLine[start], oldCount, newLine[start], newCount)
		for _, op := range ops[start:end] {
			out.WriteByte(op.kind)
			out.WriteString(op.line)
			out.WriteByte('\n')
		}
		i = end
	}
	return out.String(), true
}

func unifiedAllAdded(b []string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "@@ -0,0 +1,%d @@\n", len(b))
	for _, line := range b {
		out.WriteByte('+')
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}
