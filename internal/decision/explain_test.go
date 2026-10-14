// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

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

	stories, err := decision.ExplainSession(t.Context(), h.store, activeMapping(0), testTenant, "sess-deny")
	if err != nil {
		t.Fatalf("explain session: %v", err)
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
	if _, err := decision.ExplainSession(t.Context(), h.store, nil, testTenant, ""); !errors.Is(err, decision.ErrNoSession) {
		t.Errorf("explaining session \"\" answered %v, want ErrNoSession", err)
	}
}
