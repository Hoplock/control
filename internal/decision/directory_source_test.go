// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/store"
)

// A directory subject's decision record names the directory its groups came
// from beside the mapping version, and explain shows it; a subject no
// directory provisioned has none (0043). It is read from the link row the
// standing read already joins, so the decision path's statement count is
// TestStandingDenialsCostTheDecisionPathNoRoundTrip's, unchanged.
func TestTheRecordNamesTheDirectoryASubjectsGroupsCameFrom(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	subjectOf := func(story decision.Story) map[string]any {
		t.Helper()
		var in struct {
			Subject map[string]any `json:"subject"`
		}
		if err := json.Unmarshal(story.Inputs, &in); err != nil {
			t.Fatal(err)
		}
		return in.Subject
	}

	out := h.mustAllow(t, request("edge-host.example.com", "proxy-1"))
	story, err := decision.Explain(t.Context(), h.store, nil, testTenant, out.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := subjectOf(story)["directory_source"]; ok || story.Mapping.DirectorySource != "" {
		t.Errorf("a subject no directory provisioned is recorded with a directory source: %s", story.Inputs)
	}

	if err := h.store.DirectoryLinks().Put(t.Context(), testTenant, store.DirectoryLink{
		Source: "okta-a", ExternalID: "ext-" + alice, SubjectID: alice, Provider: "hoplock/scim",
		LinkedAt: h.clock.Add(-time.Hour), Provisioned: true, UpdatedAt: h.clock.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	out = h.mustAllow(t, request("edge-host.example.com", "proxy-1"))
	story, err = decision.Explain(t.Context(), h.store, nil, testTenant, out.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := subjectOf(story)["directory_source"]; got != "okta-a" {
		t.Errorf("the record's subject names directory source %v, want okta-a: %s", got, story.Inputs)
	}
	if story.Mapping.DirectorySource != "okta-a" {
		t.Errorf("explain shows mapping %+v, want the directory source beside the version", story.Mapping)
	}
}
