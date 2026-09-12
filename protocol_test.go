// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Protocol invariants that are cheap to check and expensive to notice late.
//
// docs/PROTOCOL.md asks three mechanical things of every phase: keep the
// decision register at the head of docs/PLAN.md §2 current (§3), give every
// prompt a "Read first" that names sections and decisions (§7), and regenerate
// the composed renumbering table whenever prompt numbers move (§6). Each is a
// habit that either forms on the first phase or does not form at all, and none
// of them fails to compile. This file is what makes them fail instead — under
// `make test`, which CI already runs, rather than as a job of their own.
//
// It is deliberately small. It grows with the repository; it is not built for
// a scale this repository has not reached.
package control_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const planPath = "docs/PLAN.md"

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// section returns the body of the `## <heading prefix>` section of doc, from
// its heading up to the next `## ` heading.
func section(t *testing.T, doc, prefix string) string {
	t.Helper()
	start := strings.Index(doc, "\n## "+prefix)
	if start < 0 {
		t.Fatalf("no section %q in the document", prefix)
	}
	body := doc[start+1:]
	if end := strings.Index(body[1:], "\n## "); end >= 0 {
		body = body[:end+1]
	}
	return body
}

// tableRows returns the cells of every pipe-table row in body, excluding header
// separators. A row is trimmed of its outer pipes and split on the rest.
func tableRows(body string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i, c := range cells {
			cells[i] = strings.TrimSpace(c)
		}
		if strings.HasPrefix(cells[0], "---") {
			continue
		}
		rows = append(rows, cells)
	}
	return rows
}

var (
	decisionHeading = regexp.MustCompile(`(?m)^- \*\*(M\d+) —`)
	registerID      = regexp.MustCompile(`^\*\*(M\d+)\*\*$`)
	statusLive      = regexp.MustCompile(`^(live|withdrawn|amended by M\d+(, M\d+)*)$`)
	amendedBy       = regexp.MustCompile(`M\d+`)
)

// TestDecisionRegisterCoversEveryDecision enforces PROTOCOL §3: a decision's
// current status must be visible without reading the decision.
func TestDecisionRegisterCoversEveryDecision(t *testing.T) {
	decisions := section(t, readFile(t, planPath), "2. Key decisions")

	declared := map[string]bool{}
	for _, m := range decisionHeading.FindAllStringSubmatch(decisions, -1) {
		declared[m[1]] = true
	}
	if len(declared) == 0 {
		t.Fatalf("%s §2: found no decisions — the register check cannot mean anything", planPath)
	}

	registered := map[string]bool{}
	for _, cells := range tableRows(decisions) {
		m := registerID.FindStringSubmatch(cells[0])
		if m == nil {
			continue
		}
		id := m[1]
		if registered[id] {
			t.Errorf("%s §2: decision %s has more than one register row; keep one row per decision", planPath, id)
		}
		registered[id] = true

		if len(cells) != 4 {
			t.Errorf("%s §2: register row for %s has %d columns, want 4 (decision, settles, status, rendered in)", planPath, id, len(cells))
			continue
		}
		status := cells[2]
		if !statusLive.MatchString(status) {
			t.Errorf("%s §2: register row for %s has status %q; want %q, %q, or %q (PROTOCOL §3)",
				planPath, id, status, "live", "withdrawn", "amended by M<n>")
			continue
		}
		if strings.HasPrefix(status, "amended by") {
			for _, by := range amendedBy.FindAllString(status, -1) {
				if !declared[by] {
					t.Errorf("%s §2: register row for %s says it is amended by %s, which is not a decision in §2", planPath, id, by)
				}
			}
		}
	}

	for id := range declared {
		if !registered[id] {
			t.Errorf("%s §2: decision %s has no register row — add one to the register at the head of §2, "+
				"stating what it settles, its status, and where it is rendered (PROTOCOL §3)", planPath, id)
		}
	}
	for id := range registered {
		if !declared[id] {
			t.Errorf("%s §2: the register has a row for %s, which is not a decision in §2 — "+
				"remove the row or restore the decision", planPath, id)
		}
	}
}

var sectionOrDecision = regexp.MustCompile(`§\d|\bM\d+\b`)

// TestEveryPromptReadFirstNamesTheSectionsItNeeds enforces PROTOCOL §7, which
// is what makes §1's "read the sections your prompt names" possible at all.
func TestEveryPromptReadFirstNamesTheSectionsItNeeds(t *testing.T) {
	var prompts []string
	for _, dir := range []string{"prompts/queued", "prompts/implemented"} {
		matches, err := filepath.Glob(filepath.Join(dir, "*.md"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		prompts = append(prompts, matches...)
	}
	if len(prompts) == 0 {
		t.Fatalf("found no prompt files — this check cannot mean anything")
	}

	for _, path := range prompts {
		body := readFile(t, path)
		start := strings.Index(body, "## Read first")
		if start < 0 {
			t.Errorf("%s: no \"## Read first\" block — every prompt must open with one (PROTOCOL §7)", path)
			continue
		}
		block := body[start+len("## Read first"):]
		if end := strings.Index(block, "\n## "); end >= 0 {
			block = block[:end]
		}
		if !sectionOrDecision.MatchString(block) {
			t.Errorf("%s: \"Read first\" names no plan section (§N) and no decision (M<n>). "+
				"A prompt that names no sections is a defective prompt, not a licence to read the whole plan "+
				"(PROTOCOL §1, §7) — name the docs/PLAN.md sections and decision ids this phase needs", path)
		}
	}
}

var (
	renumberNote = regexp.MustCompile(`(?m)^> \*\*Renumbering note \(([^)]*?)( revision)?\)\.\*\*`)
	numberMove   = regexp.MustCompile(`(\d{4})→(\d{4})`)
	promptNumber = regexp.MustCompile(`^\d{4}$`)
)

// TestRenumberMappingIsComposed enforces PROTOCOL §6: the per-revision notes
// stay as the record of why, and one table says what an old number resolves to
// now — composed, so no reader composes the notes by hand.
func TestRenumberMappingIsComposed(t *testing.T) {
	phases := section(t, readFile(t, planPath), "10. Phased delivery")

	// The moves each renumbering note records, keyed by revision label.
	noteMoves := map[string]map[string]string{}
	noteIdx := renumberNote.FindAllStringSubmatchIndex(phases, -1)
	for i, loc := range noteIdx {
		label := phases[loc[2]:loc[3]]
		end := len(phases)
		if i+1 < len(noteIdx) {
			end = noteIdx[i+1][0]
		}
		moves := map[string]string{}
		for _, m := range numberMove.FindAllStringSubmatch(phases[loc[1]:end], -1) {
			moves[m[1]] = m[2]
		}
		noteMoves[label] = moves
	}

	type row struct{ revision, was, now, phase string }
	var rows []row
	var order []string
	seenRevision := map[string]bool{}
	for _, cells := range tableRows(phases) {
		if len(cells) != 4 || !promptNumber.MatchString(cells[1]) {
			continue
		}
		r := row{revision: cells[0], was: cells[1], now: cells[2], phase: strings.Trim(cells[3], "`")}
		rows = append(rows, r)
		if !seenRevision[r.revision] {
			seenRevision[r.revision] = true
			order = append(order, r.revision)
		}
	}

	if len(noteMoves) == 0 {
		if len(rows) > 0 {
			t.Errorf("%s §10: the mapping table has rows but no renumbering note records a move", planPath)
		}
		return // No numbers have ever moved; there is nothing to compose.
	}

	// Every move a note records must have a row, and every row must have a move.
	inTable := map[string]bool{}
	for _, r := range rows {
		inTable[r.revision+" "+r.was] = true
	}
	for label, moves := range noteMoves {
		if _, ok := seenRevision[label]; !ok && len(moves) > 0 {
			t.Errorf("%s §10: the %q renumbering note moves prompt numbers but has no rows in the mapping table — "+
				"regenerate the table in the PR that renumbers (PROTOCOL §6)", planPath, label)
			continue
		}
		for was := range moves {
			if !inTable[label+" "+was] {
				t.Errorf("%s §10: the %q note moves %s but the mapping table has no row for it (PROTOCOL §6)",
					planPath, label, was)
			}
		}
	}

	revisionRank := map[string]int{}
	for i, label := range order {
		revisionRank[label] = i
	}

	for _, r := range rows {
		moves, ok := noteMoves[r.revision]
		if !ok {
			t.Errorf("%s §10: mapping row %q/%s names a revision that no renumbering note records", planPath, r.revision, r.was)
			continue
		}
		n, ok := moves[r.was]
		if !ok {
			t.Errorf("%s §10: mapping row %q/%s is not a move the %q note records", planPath, r.revision, r.was, r.revision)
			continue
		}
		// Compose through every later revision, which is the work the table
		// exists to have done once.
		for _, label := range order[revisionRank[r.revision]+1:] {
			if next, moved := noteMoves[label][n]; moved {
				n = next
			}
		}
		if n != r.now {
			t.Errorf("%s §10: mapping row %q/%s says it is now %s, but composing the notes gives %s — "+
				"regenerate the table (PROTOCOL §6)", planPath, r.revision, r.was, r.now, n)
			continue
		}
		if !strings.HasPrefix(r.phase, r.now+"-") {
			t.Errorf("%s §10: mapping row %q/%s resolves to %s but names phase %q — the number and the phase disagree",
				planPath, r.revision, r.was, r.now, r.phase)
			continue
		}
		if !promptExists(r.phase) {
			t.Errorf("%s §10: mapping row %q/%s resolves to phase %q, which is no longer a prompt file — "+
				"the table is stale (PROTOCOL §6)", planPath, r.revision, r.was, r.phase)
		}
	}
}

func promptExists(name string) bool {
	for _, dir := range []string{"prompts/queued", "prompts/implemented"} {
		if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%s.md", name))); err == nil {
			return true
		}
	}
	return false
}
