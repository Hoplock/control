// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision_test

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/policy/compile"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// candidateRules are inserted ahead of the fixture's own: one closes a target
// that was open and one opens a target that was closed, so a replay has a flip
// in each direction and a decision it must leave alone. Each is narrowed to the
// fixture's subject so that the rule it overtakes stays reachable, which the
// compiler insists on.
const candidateRules = `  - id: lock-cached
    effect: deny
    reason: cached is closed for maintenance
    match:
      subject:
        groups: [sre]
      target:
        hostnames: [cached.example.com]

  - id: open-forbidden
    effect: allow
    match:
      subject:
        ids: [alice@example.com]
      target:
        hostnames: [forbidden.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-key
          username: netadmin
          credential_ref: vault://forbidden

`

func compileSource(t *testing.T, source string) *compile.Program {
	t.Helper()
	b, err := model.Parse([]byte(source))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	prog, err := compile.Compile(b)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return prog
}

// recordSession authorizes a target under its own session id and returns the
// decision id, whatever the answer was.
func (h *harness) recordSession(t *testing.T, target, session string) string {
	t.Helper()
	req := request(target, "proxy-1")
	req.Conn.SessionID = session
	out, err := h.authorize(t, req)
	if err != nil {
		t.Fatalf("authorize %s: %v", target, err)
	}
	if out.Deny != nil {
		return out.Deny.DecisionID
	}
	return out.Response.DecisionID
}

// Replay reports THE SET of decisions a candidate flips, in each direction, and
// leaves an unchanged decision out of both. Asserting the count alone would
// pass a replay that flipped the wrong two.
func TestReplayReportsTheExactSetOfFlips(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	open := h.recordSession(t, "edge-host.example.com", "sess-open")
	cached := h.recordSession(t, "cached.example.com", "sess-cached")
	forbidden := h.recordSession(t, "forbidden.example.com", "sess-forbidden")

	candidate := compileSource(t, strings.Replace(testBundle, "rules:\n", "rules:\n"+candidateRules, 1))
	rep, err := decision.Replay(t.Context(), h.store.Decisions(), testTenant, candidate, decision.ReplayQuery{
		From: h.clock.Add(-time.Minute), To: h.clock.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rep.Replayed != 3 || rep.Unchanged != 1 || rep.Truncated || len(rep.Unreplayable) != 0 {
		t.Fatalf("replayed %d, unchanged %d, truncated %v, unreplayable %v; want 3, 1, false, none",
			rep.Replayed, rep.Unchanged, rep.Truncated, rep.Unreplayable)
	}

	flipped := func(groups []decision.FlipGroup) []string {
		var ids []string
		for _, g := range groups {
			if g.Count != len(g.Flips) {
				t.Errorf("group %s→%s counts %d and holds %d", g.From, g.To, g.Count, len(g.Flips))
			}
			for _, f := range g.Flips {
				ids = append(ids, f.DecisionID)
			}
		}
		slices.Sort(ids)
		return ids
	}
	if got := flipped(rep.AllowToDeny); !slices.Equal(got, []string{cached}) {
		t.Errorf("allow→deny = %v, want exactly [%s]", got, cached)
	}
	if got := flipped(rep.DenyToAllow); !slices.Equal(got, []string{forbidden}) {
		t.Errorf("deny→allow = %v, want exactly [%s]", got, forbidden)
	}
	for _, g := range append(slices.Clone(rep.AllowToDeny), rep.DenyToAllow...) {
		for _, f := range g.Flips {
			if f.DecisionID == open {
				t.Errorf("the unchanged decision %s was reported as a flip", open)
			}
		}
	}
	if g := rep.AllowToDeny[0]; g.WasRule != "cached" || g.NowRule != "lock-cached" {
		t.Errorf("allow→deny group rules = %q → %q, want cached → lock-cached", g.WasRule, g.NowRule)
	}
	if g := rep.DenyToAllow[0]; g.WasRule != "deny-forbidden" || g.NowRule != "open-forbidden" {
		t.Errorf("deny→allow group rules = %q → %q, want deny-forbidden → open-forbidden", g.WasRule, g.NowRule)
	}
}

// Replay asks the recorded question, at the recorded instant. The active policy
// replayed over its own records changes nothing — the property that makes a
// replay's flips mean "the candidate", never "time has passed".
func TestReplayingTheActivePolicyFlipsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.recordSession(t, "edge-host.example.com", "sess-a")
	h.recordSession(t, "forbidden.example.com", "sess-b")

	// A week later, nothing about the recorded questions has moved.
	rep, err := decision.Replay(t.Context(), h.store.Decisions(), testTenant, compileSource(t, testBundle), decision.ReplayQuery{
		From: h.clock.Add(-time.Hour), To: h.clock.Add(7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rep.Replayed != 2 || rep.Unchanged != 2 || len(rep.AllowToDeny)+len(rep.DenyToAllow) != 0 {
		t.Errorf("replaying the active policy: replayed %d, unchanged %d, flips %v/%v",
			rep.Replayed, rep.Unchanged, rep.AllowToDeny, rep.DenyToAllow)
	}
}

// The window is [from, to), and a limit that cuts it short says so.
func TestReplayBoundsItsWindowAndSaysWhenItWasCut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.recordSession(t, "edge-host.example.com", "sess-a")
	h.recordSession(t, "cached.example.com", "sess-b")

	prog := compileSource(t, testBundle)
	rep, err := decision.Replay(t.Context(), h.store.Decisions(), testTenant, prog, decision.ReplayQuery{
		From: h.clock.Add(-time.Minute), To: h.clock.Add(time.Minute), Limit: 1,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rep.Replayed != 1 || !rep.Truncated {
		t.Errorf("limit 1 over two records: replayed %d, truncated %v; want 1, true", rep.Replayed, rep.Truncated)
	}

	rep, err = decision.Replay(t.Context(), h.store.Decisions(), testTenant, prog, decision.ReplayQuery{
		From: h.clock.Add(time.Second), To: h.clock.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rep.Replayed != 0 {
		t.Errorf("a window after every record replayed %d", rep.Replayed)
	}
}

// A relabel is simulated like any other change: the candidate is the active
// policy, and the question is asked with the target's new labels.
func TestARelabelIsSimulatedByNamingTheNewLabels(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.recordSession(t, "edge-host.example.com", "sess-a")

	const prodOnly = `  - id: prod-is-closed
    effect: deny
    reason: production is change-frozen
    match:
      target:
        labels:
          env: [dev]

`
	prog := compileSource(t, strings.Replace(testBundle, "rules:\n", "rules:\n"+prodOnly, 1))
	rep, err := decision.Replay(t.Context(), h.store.Decisions(), testTenant, prog, decision.ReplayQuery{
		From: h.clock.Add(-time.Minute), To: h.clock.Add(time.Minute),
		Labels: decision.LabelOverride{"edge-host.example.com": {"env": "dev"}},
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(rep.AllowToDeny) != 1 || rep.AllowToDeny[0].Flips[0].DecisionID != id {
		t.Fatalf("relabelling edge-host to env=dev: allow→deny = %+v, want %s", rep.AllowToDeny, id)
	}
}

// A dry run is pure: synthetic questions, answered by the candidate alone.
func TestADryRunAnswersSyntheticQuestions(t *testing.T) {
	t.Parallel()
	prog := compileSource(t, testBundle)
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	got := decision.DryRun(prog, []decision.DryRunCase{
		{Name: "sre", Input: model.Input{Now: at, Subject: model.Subject{ID: alice, Groups: []string{"sre"}},
			Target: model.Target{Hostname: "edge-host.example.com"}}},
		{Name: "forbidden", Input: model.Input{Now: at, Subject: model.Subject{ID: alice, Groups: []string{"sre"}},
			Target: model.Target{Hostname: "forbidden.example.com"}}},
		{Name: "stranger", Input: model.Input{Now: at, Subject: model.Subject{ID: "eve@example.com"},
			Target: model.Target{Hostname: "edge-host.example.com"}}},
	})
	want := []struct{ effect, rule string }{{"allow", "sre-anywhere"}, {"deny", "deny-forbidden"}, {"deny", ""}}
	for i, w := range want {
		if got[i].Effect != w.effect || got[i].Rule != w.rule {
			t.Errorf("%s: %s by %q, want %s by %q", got[i].Name, got[i].Effect, got[i].Rule, w.effect, w.rule)
		}
	}
}

// forge stores a copy of a real record under a new id, its inputs or its
// explanation rewritten, so replay can be shown a record it cannot read.
func (h *harness) forge(t *testing.T, from, id string, inputs func(map[string]any) any, explanation func(map[string]any) any) {
	t.Helper()
	rec, err := h.store.Decisions().Get(t.Context(), testTenant, from)
	if err != nil {
		t.Fatalf("get %s: %v", from, err)
	}
	rewrite := func(raw []byte, f func(map[string]any) any) []byte {
		if f == nil {
			return raw
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out, err := json.Marshal(f(doc))
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		return out
	}
	rec.ID = id
	rec.Inputs = rewrite(rec.Inputs, inputs)
	rec.Explanation = rewrite(rec.Explanation, explanation)
	if err := h.store.Decisions().Insert(t.Context(), testTenant, rec); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

// A record replay cannot read faithfully is LISTED, with why, and never
// replayed as a different question: a source address or a grant window that
// does not parse, an instant that was not recorded, inputs or an explanation
// that do not decode. Replaying any of them would answer something nobody
// asked — a grant whose expiry did not parse reads as expired, and the
// decision "flips".
func TestARecordReplayCannotReadIsListedNeverReplayed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	good := h.recordSession(t, "edge-host.example.com", "sess-good")
	context := func(key string, v any) func(map[string]any) any {
		return func(doc map[string]any) any {
			doc["context"].(map[string]any)[key] = v
			return doc
		}
	}
	h.forge(t, good, "dec-addr", context("source_addr", "not-an-address"), nil)
	h.forge(t, good, "dec-grant", func(doc map[string]any) any {
		doc["grants"] = []any{map[string]any{"id": "g-1", "scope": map[string]any{"name": "ops"}, "expires_at": "yesterday"}}
		return doc
	}, nil)
	h.forge(t, good, "dec-window", func(doc map[string]any) any {
		doc["grants"] = []any{map[string]any{"id": "g-2", "scope": map[string]any{"name": "ops"},
			"expires_at": h.clock.Add(time.Hour).Format(time.RFC3339),
			"external":   map[string]any{"system": "scanner", "reference": "scan-1", "window_end": "soon"}}}
		return doc
	}, nil)
	h.forge(t, good, "dec-instant", func(doc map[string]any) any { delete(doc, "now"); return doc }, nil)
	h.forge(t, good, "dec-inputs", func(map[string]any) any { return []any{} }, nil)
	h.forge(t, good, "dec-explanation", nil, func(map[string]any) any { return map[string]any{} })

	rep, err := decision.Replay(t.Context(), h.store.Decisions(), testTenant, compileSource(t, testBundle), decision.ReplayQuery{
		From: h.clock.Add(-time.Minute), To: h.clock.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	var listed []string
	for _, u := range rep.Unreplayable {
		if u.Reason == "" {
			t.Errorf("%s is listed with no reason", u.DecisionID)
		}
		listed = append(listed, u.DecisionID)
	}
	slices.Sort(listed)
	want := []string{"dec-addr", "dec-explanation", "dec-grant", "dec-inputs", "dec-instant", "dec-window"}
	if !slices.Equal(listed, want) {
		t.Errorf("unreplayable = %v, want %v", listed, want)
	}
	if rep.Replayed != 1 || rep.Unchanged != 1 {
		t.Errorf("replayed %d, unchanged %d; want the one readable record, unchanged", rep.Replayed, rep.Unchanged)
	}
}

// window is a RecordWindow that counts what replay asked of it.
type window struct {
	records []store.Decision
	calls   []string
}

func (w *window) ListRange(_ context.Context, tenant store.Tenant, from, to time.Time, limit int) ([]store.Decision, bool, error) {
	w.calls = append(w.calls, fmt.Sprintf("%s %s %s %d", tenant, from.Format(time.RFC3339), to.Format(time.RFC3339), limit))
	return w.records, false, nil
}

// REPLAY IS PURE. Its only access to state is the record window it is handed
// (decision.RecordWindow, one method), so a store read beyond the window
// cannot be added without changing the signature this test calls; it asks
// that window once, for exactly the window and the limit; and no file on its
// path reads a clock. An answer computed twice, an hour of wall clock apart,
// is the same answer.
func TestReplayIsPure(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.recordSession(t, "edge-host.example.com", "sess-pure")
	rec, err := h.store.Decisions().Get(t.Context(), testTenant, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if n := reflect.TypeFor[decision.RecordWindow]().NumMethod(); n != 1 {
		t.Fatalf("a replay may read through %d methods; it may read through ListRange alone", n)
	}

	w := &window{records: []store.Decision{rec}}
	q := decision.ReplayQuery{From: h.clock.Add(-time.Minute), To: h.clock.Add(time.Minute), Limit: 7}
	prog := compileSource(t, testBundle)
	first, err := decision.Replay(t.Context(), w, testTenant, prog, q)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	again, err := decision.Replay(t.Context(), w, testTenant, prog, q)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	wantCall := fmt.Sprintf("%s %s %s 7", testTenant, q.From.Format(time.RFC3339), q.To.Format(time.RFC3339))
	if len(w.calls) != 2 || w.calls[0] != wantCall || w.calls[1] != wantCall {
		t.Errorf("replay asked its window %v, want %q once per replay", w.calls, wantCall)
	}
	if first.Replayed != 1 || !reflect.DeepEqual(first, again) {
		t.Errorf("two replays of one window disagree: %+v, %+v", first, again)
	}

	// No clock on the path: replay itself, and the engine it evaluates with.
	clocks := []string{"Now", "Since", "Until", "After", "Tick", "NewTimer", "NewTicker", "AfterFunc"}
	files := []string{"replay.go"}
	for _, dir := range []string{"../policy/eval", "../policy/compile", "../policy/model"} {
		matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range matches {
			if !strings.HasSuffix(m, "_test.go") {
				files = append(files, m)
			}
		}
	}
	for _, file := range files {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "time" && slices.Contains(clocks, sel.Sel.Name) {
				t.Errorf("%s reads a clock: time.%s", file, sel.Sel.Name)
			}
			return true
		})
	}
}
