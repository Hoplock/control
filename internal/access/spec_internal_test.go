// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

var specNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func validSpec() Spec {
	return Spec{
		Subject:  "alice@example.com",
		Scope:    Scope{Name: "prod-dba"},
		Duration: 30 * time.Minute,
		Reason:   "INC-9",
	}
}

func TestASpecIsNormalisedToWhatIsStored(t *testing.T) {
	spec := validSpec()
	spec.Subject = "  alice@example.com "
	spec.Scope.Targets = []string{"DB02.example.com", "db01.example.com", "db02.example.com"}
	spec.Scope.Zones = []string{"eu", "eu", "us"}
	// A window that "started" an hour ago starts now: a grant confers
	// nothing on decisions already taken.
	spec.NotBefore = specNow.Add(-time.Hour)

	got, w, err := normalize(spec, specNow, time.Hour)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.Subject != "alice@example.com" {
		t.Errorf("subject %q", got.Subject)
	}
	if !slices.Equal(got.Scope.Targets, []string{"db01.example.com", "db02.example.com"}) {
		t.Errorf("targets %v, want lower-cased, sorted and de-duplicated", got.Scope.Targets)
	}
	if !slices.Equal(got.Scope.Zones, []string{"eu", "us"}) {
		t.Errorf("zones %v", got.Scope.Zones)
	}
	if !w.notBefore.Equal(specNow) || !w.expiresAt.Equal(specNow.Add(30*time.Minute)) {
		t.Errorf("window [%v, %v)", w.notBefore, w.expiresAt)
	}
	if !got.NotBefore.Equal(w.notBefore) || !got.ExpiresAt.Equal(w.expiresAt) {
		t.Error("the spec and the window disagree")
	}
}

func TestASpecNobodyCanActOnIsRefusedByField(t *testing.T) {
	cases := map[string]struct {
		edit    func(*Spec)
		field   string
		problem string
	}{
		"no subject":         {func(s *Spec) { s.Subject = " " }, "subject", ProblemRequired},
		"no scope name":      {func(s *Spec) { s.Scope.Name = "" }, "scope.name", ProblemRequired},
		"no reason":          {func(s *Spec) { s.Reason = "\t" }, "reason", ProblemRequired},
		"no window":          {func(s *Spec) { s.Duration = 0 }, "expires_at", ProblemRequired},
		"two windows":        {func(s *Spec) { s.ExpiresAt = specNow.Add(time.Hour) }, "expires_at", ProblemAmbiguous},
		"negative duration":  {func(s *Spec) { s.Duration = -time.Minute }, "duration", ProblemInvalid},
		"past the ceiling":   {func(s *Spec) { s.Duration = 25 * time.Hour }, "expires_at", ProblemExceedsMaximum},
		"a middle wildcard":  {func(s *Spec) { s.Scope.Targets = []string{"db.*.example.com"} }, "scope.targets", ProblemInvalid},
		"a bare wildcard":    {func(s *Spec) { s.Scope.Targets = []string{"*"} }, "scope.targets", ProblemInvalid},
		"an empty label":     {func(s *Spec) { s.Scope.Labels = map[string]string{"env": ""} }, "scope.labels", ProblemRequired},
		"a reason code word": {func(s *Spec) { s.ReasonCode = "Incident Response" }, "reason_code", ProblemInvalid},
		"a control char":     {func(s *Spec) { s.Subject = "alice\x1b[2J" }, "subject", ProblemInvalid},
		"a terminal escape":  {func(s *Spec) { s.Reason = "fine\x1b]0;owned\x07" }, "reason", ProblemInvalid},
		"too many targets": {func(s *Spec) {
			for i := range maxSelectorItems + 1 {
				s.Scope.Targets = append(s.Scope.Targets, "h"+strings.Repeat("x", i)+".example.com")
			}
		}, "scope.targets", ProblemTooMany},
		"an expiry in the past": {func(s *Spec) {
			s.Duration = 0
			s.ExpiresAt = specNow.Add(-time.Minute)
		}, "expires_at", ProblemNotAfterStart},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			spec := validSpec()
			c.edit(&spec)
			_, _, err := normalize(spec, specNow, 24*time.Hour)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("normalize = %v, want a *ValidationError", err)
			}
			if ve.Field != c.field || ve.Problem != c.problem {
				t.Errorf("refused %s/%s, want %s/%s", ve.Field, ve.Problem, c.field, c.problem)
			}
		})
	}
}

// A line break in a reason is a person writing; the ceiling names itself.
func TestAReasonMayHaveLinesAndTheCeilingIsNamed(t *testing.T) {
	spec := validSpec()
	spec.Reason = "INC-9\nprimary is read-only"
	if _, _, err := normalize(spec, specNow, time.Hour); err != nil {
		t.Errorf("a two-line reason was refused: %v", err)
	}
	spec.Duration = 2 * time.Hour
	_, _, err := normalize(spec, specNow, time.Hour)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Limit != "1h0m0s" {
		t.Errorf("past the ceiling: %v, want the limit named", err)
	}
}

// PolicyGrant is the whole translation into the engine, and it drops nothing
// the engine matches on.
func TestPolicyGrantCarriesEverythingTheEngineReads(t *testing.T) {
	g := store.Grant{
		ID: "g-1", SubjectID: "alice", Scope: "prod-dba",
		ScopeTargets: []string{"*.db.example.com"}, ScopeLabels: map[string]string{"env": "prod"},
		ScopeZones: []string{"eu"},
		NotBefore:  specNow, ExpiresAt: specNow.Add(time.Hour),
		Origin: store.GrantOriginExternal, RequestID: "gr-1", Approvers: []string{"carol"},
		ExternalRef: "CHG-1",
		External: store.GrantExternal{
			System: "itsm", WindowStart: specNow, WindowEnd: specNow.Add(30 * time.Minute),
			AdditionalKind: store.AdditionalContextObject, Additional: `{"risk":"low"}`,
		},
	}
	got := PolicyGrant(g)
	if got.ID != "g-1" || got.Subject != "alice" || got.Origin != model.GrantOriginExternal ||
		got.Scope.Name != "prod-dba" || !slices.Equal(got.Scope.Targets, g.ScopeTargets) ||
		got.Scope.Labels["env"] != "prod" || !slices.Equal(got.Scope.Zones, g.ScopeZones) ||
		!got.NotBefore.Equal(g.NotBefore) || !got.ExpiresAt.Equal(g.ExpiresAt) ||
		got.RequestRef != "gr-1" || !slices.Equal(got.Approvers, g.Approvers) {
		t.Errorf("PolicyGrant = %+v", got)
	}
	if e := got.External; e == nil || e.System != "itsm" || e.Reference != "CHG-1" ||
		!e.WindowEnd.Equal(g.External.WindowEnd) || e.AdditionalContext == nil ||
		e.AdditionalContext.Fields["risk"] != "low" {
		t.Errorf("external = %+v", got.External)
	}

	// The grant covers what its selector says and nothing else.
	if !got.Covers(model.Target{Hostname: "pg.db.example.com", Labels: map[string]string{"env": "prod"}, Zone: "eu"}) {
		t.Error("the grant does not cover a target its selector names")
	}
	if got.Covers(model.Target{Hostname: "pg.db.example.com", Labels: map[string]string{"env": "dev"}, Zone: "eu"}) {
		t.Error("the grant covers a target outside its labels")
	}
}

func TestEveryStoredOriginMapsOntoThePolicyVocabulary(t *testing.T) {
	for stored, want := range map[store.GrantOrigin]model.GrantOrigin{
		store.GrantOriginManual:   model.GrantOriginAdministrator,
		store.GrantOriginWorkflow: model.GrantOriginWorkflow,
		store.GrantOriginExternal: model.GrantOriginExternal,
		"somebody-else":           "",
	} {
		if got := PolicyOrigin(stored); got != want {
			t.Errorf("PolicyOrigin(%q) = %q, want %q", stored, got, want)
		}
	}
}

func TestAdditionalContextIsReadBackAsTheShapeItWasStoredAs(t *testing.T) {
	text := additionalContext(store.GrantExternal{AdditionalKind: store.AdditionalContextString, Additional: `"approved"`})
	if text == nil || text.Text != "approved" || text.Fields != nil {
		t.Errorf("string form = %+v", text)
	}
	// Text that does not parse as the shape it claims is dropped, never
	// coerced: it is carried verbatim for an auditor.
	for _, bad := range []store.GrantExternal{
		{AdditionalKind: store.AdditionalContextObject, Additional: `"not an object"`},
		{AdditionalKind: store.AdditionalContextString, Additional: `{"not":"a string"}`},
		{AdditionalKind: "", Additional: `"ignored"`},
	} {
		if got := additionalContext(bad); got != nil {
			t.Errorf("additionalContext(%+v) = %+v, want nothing", bad, got)
		}
	}
}

// clamp narrows an approval to the window asked for, and says when it did.
func TestAnApprovedWindowIsClampedToTheRequest(t *testing.T) {
	from, to := specNow, specNow.Add(time.Hour)
	cases := []struct {
		approved         ext.Window
		wantFrom, wantTo time.Time
		clamped          bool
	}{
		{ext.Window{}, from, to, false},
		{ext.Window{NotBefore: from.Add(time.Minute), NotAfter: to.Add(-time.Minute)},
			from.Add(time.Minute), to.Add(-time.Minute), false},
		{ext.Window{NotBefore: from.Add(-time.Hour)}, from, to, true},
		{ext.Window{NotAfter: to.Add(time.Hour)}, from, to, true},
	}
	for _, c := range cases {
		gotFrom, gotTo, clamped := clamp(from, to, c.approved)
		if !gotFrom.Equal(c.wantFrom) || !gotTo.Equal(c.wantTo) || clamped != c.clamped {
			t.Errorf("clamp(%+v) = [%v, %v) clamped %v", c.approved, gotFrom, gotTo, clamped)
		}
	}
}
