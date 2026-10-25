// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

func activeMapping(v int) decision.ActiveMapping {
	return func(context.Context, store.Tenant) (int, error) { return v, nil }
}

// An allow is explained whole: the rule and its line, the obligations, the
// snapshot that was served and the inputs it was decided on.
func TestExplainAnAllow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.recordSession(t, "edge-host.example.com", "sess-allow")

	s, err := decision.Explain(t.Context(), h.store, activeMapping(0), testTenant, id)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if s.Effect != decision.EffectAllow || s.Rule != "sre-anywhere" || s.RuleLine == 0 {
		t.Errorf("story = %s by %q at line %d, want allow by sre-anywhere at its line", s.Effect, s.Rule, s.RuleLine)
	}
	if s.SessionID != "sess-allow" || s.ProxyID != "proxy-1" {
		t.Errorf("story names session %q on %q", s.SessionID, s.ProxyID)
	}
	if len(s.Snapshot) < 3 || len(s.Inputs) < 3 || len(s.Explanation) < 3 {
		t.Errorf("an allow's story is missing a document: snapshot %d, inputs %d, explanation %d bytes",
			len(s.Snapshot), len(s.Inputs), len(s.Explanation))
	}
	if s.Grant != nil {
		t.Errorf("no grant was involved, and the story names %s", s.Grant)
	}
}

// A deny is explained by the rule that denied, and the default deny by its
// absence: nothing matched.
func TestExplainADeny(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.recordSession(t, "forbidden.example.com", "sess-deny")

	stories, truncated, err := decision.ExplainSession(t.Context(), h.store, activeMapping(0), testTenant, "sess-deny")
	if err != nil || truncated {
		t.Fatalf("explain session: %v (truncated=%t)", err, truncated)
	}
	if len(stories) != 1 || stories[0].DecisionID != id {
		t.Fatalf("the session explains as %+v, want the one decision %s", stories, id)
	}
	s := stories[0]
	if s.Effect != decision.EffectDeny || s.Rule != "deny-forbidden" {
		t.Errorf("story = %s by %q, want deny by deny-forbidden", s.Effect, s.Rule)
	}
	var expl struct {
		DenyReason string `json:"deny_reason"`
	}
	if err := json.Unmarshal(s.Explanation, &expl); err != nil || expl.DenyReason == "" {
		t.Errorf("a deny's explanation carries no reason: %s", s.Explanation)
	}
}

// A decision made under a mapping version that has since changed is explained
// by the mapping it was made under, and says the mapping has moved.
func TestExplainADecisionUnderAMappingThatHasSinceChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	subject := storetest.Subject(alice, "sre")
	subject.MappingVersion = 3
	if err := h.store.Subjects().Upsert(t.Context(), testTenant, subject); err != nil {
		t.Fatalf("upsert subject: %v", err)
	}
	id := h.recordSession(t, "edge-host.example.com", "sess-mapped")

	s, err := decision.Explain(t.Context(), h.store, activeMapping(4), testTenant, id)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if s.Mapping != (decision.MappingFacts{Version: 3, Active: 4, Changed: true}) {
		t.Errorf("mapping = %+v, want made under 3, 4 active, changed", s.Mapping)
	}

	s, err = decision.Explain(t.Context(), h.store, activeMapping(3), testTenant, id)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if s.Mapping.Changed {
		t.Errorf("the mapping the decision was made under is still active and the story says it changed")
	}
}

// An unexplainable decision is loud: one whose explanation names a grant its
// inputs do not hold is an integrity fault, never a partial story.
func TestAnUnexplainableDecisionIsLoud(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.recordSession(t, "edge-host.example.com", "sess-x")
	rec, err := h.store.Decisions().Get(t.Context(), testTenant, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	var expl map[string]any
	if err := json.Unmarshal(rec.Explanation, &expl); err != nil {
		t.Fatalf("decode explanation: %v", err)
	}
	expl["grant"] = "grant-nobody-recorded"
	rec.Explanation, _ = json.Marshal(expl)
	rec.ID = "dec-forged"
	if err := h.store.Decisions().Insert(t.Context(), testTenant, rec); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := decision.Explain(t.Context(), h.store, nil, testTenant, "dec-forged"); !errors.Is(err, decision.ErrUnexplainable) {
		t.Errorf("explaining a record naming an absent grant answered %v, want ErrUnexplainable", err)
	}
}

// The empty session id is no session: explaining it is refused, never answered
// with every record that belongs to none.
func TestExplainRefusesTheEmptySession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if _, _, err := decision.ExplainSession(t.Context(), h.store, nil, testTenant, ""); !errors.Is(err, decision.ErrNoSession) {
		t.Errorf("explaining session \"\" answered %v, want ErrNoSession", err)
	}
}

// forgeRecord stores a copy of a real record under a new id, changed by edit.
func (h *harness) forgeRecord(t *testing.T, from, id string, edit func(*store.Decision)) {
	t.Helper()
	rec, err := h.store.Decisions().Get(t.Context(), testTenant, from)
	if err != nil {
		t.Fatalf("get %s: %v", from, err)
	}
	rec.ID = id
	edit(&rec)
	if err := h.store.Decisions().Insert(t.Context(), testTenant, rec); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func rewriteJSON(t *testing.T, raw []byte, edit func(map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	edit(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return out
}

// The grant that decided is the `inputs.grants[]` entry the explanation names,
// WHOLE and from the record alone: no grant row exists for the one below, so a
// story that joined the grant table could not be told at all.
func TestExplainTakesTheGrantFromTheRecordAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.recordSession(t, "edge-host.example.com", "sess-g")
	second := map[string]any{
		"id": "g-recorded-only", "origin": "workflow", "scope": map[string]any{"name": "ops"},
		"expires_at": h.clock.Add(time.Hour).Format(time.RFC3339), "approvers": []any{"oncall@example.com"},
		"request_id": "req-7", "reason": "INC-4711",
	}
	h.forgeRecord(t, id, "dec-granted", func(d *store.Decision) {
		d.Inputs = rewriteJSON(t, d.Inputs, func(doc map[string]any) {
			doc["grants"] = []any{map[string]any{"id": "g-other", "scope": map[string]any{"name": "dev"}}, second}
		})
		d.Explanation = rewriteJSON(t, d.Explanation, func(doc map[string]any) { doc["grant"] = "g-recorded-only" })
	})
	if _, err := h.store.Grants().Get(t.Context(), testTenant, "g-recorded-only"); !store.IsNotFound(err) {
		t.Fatalf("the fixture's grant exists in the table (%v); the test proves nothing", err)
	}
	s, err := decision.Explain(t.Context(), h.store, nil, testTenant, "dec-granted")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(s.Grant, &got); err != nil {
		t.Fatalf("the story's grant does not decode: %v", err)
	}
	if !reflect.DeepEqual(got, mustRoundTrip(t, second)) {
		t.Errorf("the story's grant = %v, want the recorded entry whole: %v", got, second)
	}
}

func mustRoundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// EVERY WAY a record can fail to tell its own story is loud, never a partial
// story: inputs or an explanation that do not decode, inputs with no instant,
// no effect, an effect this server never writes, an allow the engine did not
// make, an allow with no snapshot, an unserved record that does not say why,
// and a grant the inputs do not hold. And two that look odd and are not: an
// allow the fleet then refused, and an unserved record that says why.
func TestEveryUnexplainableRecordIsLoud(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	allow := h.recordSession(t, "edge-host.example.com", "sess-u")
	inputs := func(edit func(map[string]any)) func(*store.Decision) {
		return func(d *store.Decision) { d.Inputs = rewriteJSON(t, d.Inputs, edit) }
	}
	explanation := func(edit func(map[string]any)) func(*store.Decision) {
		return func(d *store.Decision) { d.Explanation = rewriteJSON(t, d.Explanation, edit) }
	}
	cases := []struct {
		name string
		edit func(*store.Decision)
		loud bool
	}{
		{"inputs not an object", func(d *store.Decision) { d.Inputs = []byte(`[]`) }, true},
		{"inputs with no instant", inputs(func(doc map[string]any) { delete(doc, "now") }), true},
		{"no explanation", func(d *store.Decision) { d.Explanation = []byte(`{}`) }, true},
		{"an effect never written", func(d *store.Decision) { d.Effect = "maybe" }, true},
		{"an allow the engine denied", explanation(func(doc map[string]any) { doc["effect"] = "deny" }), true},
		{"an allow with no snapshot", func(d *store.Decision) { d.Snapshot = nil }, true},
		{"an unserved record that says not why", func(d *store.Decision) { d.Effect = decision.EffectUnserved; d.Snapshot = nil }, true},
		{"a grant the inputs do not hold", explanation(func(doc map[string]any) { doc["grant"] = "g-none" }), true},
		{"an allow the fleet refused", func(d *store.Decision) {
			d.Effect, d.Snapshot = decision.EffectDeny, nil
			d.Explanation = rewriteJSON(t, d.Explanation, func(doc map[string]any) { doc["deny_reason"] = "refused at serve" })
		}, false},
		{"an unserved record that says why", func(d *store.Decision) {
			d.Effect, d.Snapshot = decision.EffectUnserved, nil
			d.Explanation = rewriteJSON(t, d.Explanation, func(doc map[string]any) { doc["unserved"] = "no live route" })
		}, false},
	}
	for i, tc := range cases {
		id := "dec-forged-" + strconv.Itoa(i)
		h.forgeRecord(t, allow, id, tc.edit)
		_, err := decision.Explain(t.Context(), h.store, nil, testTenant, id)
		if tc.loud && !errors.Is(err, decision.ErrUnexplainable) {
			t.Errorf("%s: explained with %v, want ErrUnexplainable", tc.name, err)
		}
		if !tc.loud && err != nil {
			t.Errorf("%s: %v, want a story", tc.name, err)
		}
	}
}
