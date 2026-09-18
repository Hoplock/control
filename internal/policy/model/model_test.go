// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/policy/model"
)

func parse(t *testing.T, src string) *model.Bundle {
	t.Helper()
	b, err := model.Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse:\n%v", err)
	}
	return b
}

const simple = `schema_version: 1
tenant: acme
rules:
  - id: first
    effect: allow
    route: {intent: direct, channels: [session], filter: {mode: whitelist}}
  - id: second
    effect: deny
    reason: no
`

// TestParseAttachesLines: a rejection that cannot place itself in the file is a
// rejection an author has to go looking for.
func TestParseAttachesLines(t *testing.T) {
	b := parse(t, simple)
	if got := b.Rules[0].Line(); got != 4 {
		t.Errorf("first rule at line %d, want 4", got)
	}
	if got := b.Rules[1].Line(); got != 7 {
		t.Errorf("second rule at line %d, want 7", got)
	}
}

// TestParseIsStrict: a key this server does not know is a policy somebody
// believes is in force and is not.
func TestParseIsStrict(t *testing.T) {
	_, err := model.Parse([]byte(`schema_version: 1
tenant: acme
rulez:
  - {id: r, effect: allow}
`))
	if err == nil {
		t.Fatal("an unknown key was accepted")
	}
	var rs model.Rejections
	if !errors.As(err, &rs) || !rs.Has(model.CodeDocumentMalformed) {
		t.Fatalf("want a malformed-document rejection, got %v", err)
	}
	if rs[0].Line == 0 {
		t.Error("the rejection does not say where the unknown key is")
	}
}

// TestEnumErrorsNameTheVocabulary. An enum mistake is the commonest thing an
// author hits, and "not one of a, b, c" is the difference between a fix and a
// search.
func TestEnumErrorsNameTheVocabulary(t *testing.T) {
	_, err := model.Parse([]byte(`schema_version: 1
tenant: acme
rules:
  - id: r
    effect: permit
`))
	if err == nil {
		t.Fatal("an unknown effect was accepted")
	}
	msg := err.Error()
	for _, want := range []string{`"permit"`, `"allow"`, `"deny"`, "line 5"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %q:\n%s", want, msg)
		}
	}
}

// TestDigestIsStable: an explanation names the exact document it came from
// (M4), so the digest has to be a function of the bytes and nothing else.
func TestDigestIsStable(t *testing.T) {
	a, b := parse(t, simple), parse(t, simple)
	if a.Digest() != b.Digest() {
		t.Fatalf("digest differs between parses: %s vs %s", a.Digest(), b.Digest())
	}
	if len(a.Digest()) != 64 {
		t.Errorf("digest = %q, want a hex SHA-256", a.Digest())
	}
	c := parse(t, simple+"  # a trailing comment\n")
	if c.Digest() == a.Digest() {
		t.Error("two different documents share a digest")
	}
	if string(a.Source()) != simple {
		t.Error("the bundle does not carry the bytes it was parsed from")
	}
}

// TestTimezoneIsResolvedAtParseTime keeps evaluation pure: the location is
// resolved once, from tzdata embedded in this binary, so the same bundle reads
// the same times on every host.
func TestTimezoneIsResolvedAtParseTime(t *testing.T) {
	b := parse(t, `schema_version: 1
tenant: acme
timezone: Europe/Lisbon
rules: [{id: r, effect: deny, reason: no}]
`)
	if b.Location().String() != "Europe/Lisbon" {
		t.Fatalf("location = %s, want Europe/Lisbon", b.Location())
	}
	// A summer instant in Lisbon is UTC+1, which is the whole reason the
	// zone is a name rather than an offset.
	local := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).In(b.Location())
	if local.Hour() != 13 {
		t.Errorf("local hour = %d, want 13 (WEST)", local.Hour())
	}

	bad, err := model.Parse([]byte(`schema_version: 1
tenant: acme
timezone: Mars/Olympus
rules: [{id: r, effect: deny, reason: no}]
`))
	if err != nil {
		t.Fatalf("an unknown zone must not throw the document away: %v", err)
	}
	if bad.Location() != time.UTC {
		t.Errorf("location = %s, want UTC as the fallback", bad.Location())
	}
	if !bad.Validate().Has(model.CodeTimezoneUnknown) {
		t.Error("an unknown zone must still be reported")
	}
}

// TestUsernameForms: a literal and the two derivations from the authenticated
// subject are expressible; the identity's client-typed `login` is not, and
// there is no syntax in which to write it.
func TestUsernameForms(t *testing.T) {
	cases := []struct {
		yaml    string
		source  model.UsernameSource
		subject string
		want    string
	}{
		{`svc-admin`, model.UsernameLiteral, "alice@example.com", "svc-admin"},
		{`{from: subject}`, model.UsernameSubject, "alice@example.com", "alice@example.com"},
		{`{from: subject-local-part}`, model.UsernameSubjectLocalPart, "alice@example.com", "alice"},
		{`{from: subject-local-part}`, model.UsernameSubjectLocalPart, "alice", "alice"},
	}
	for _, tc := range cases {
		b := parse(t, `schema_version: 1
tenant: acme
rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials: [{method: static-key, username: `+tc.yaml+`}]
`)
		u := b.Rules[0].Route.Credentials[0].Username
		if u.Source != tc.source {
			t.Errorf("%s: source = %q, want %q", tc.yaml, u.Source, tc.source)
		}
		if got := u.Resolve(tc.subject); got != tc.want {
			t.Errorf("%s: resolve(%q) = %q, want %q", tc.yaml, tc.subject, got, tc.want)
		}
	}

	_, err := model.Parse([]byte(`schema_version: 1
tenant: acme
rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials: [{method: static-key, username: {from: login}}]
`))
	if err == nil {
		t.Fatal("`from: login` must not be expressible: it is the substitution the contract closed")
	}
}

func TestTimeWindow(t *testing.T) {
	cases := []struct {
		window   string
		inWindow []string
		outside  []string
	}{
		{"09:00-17:00", []string{"09:00", "12:30", "17:00"}, []string{"08:59", "17:01", "23:59"}},
		{"22:00-06:00", []string{"22:00", "23:59", "00:00", "06:00"}, []string{"21:59", "06:01", "12:00"}},
	}
	for _, tc := range cases {
		b := parse(t, `schema_version: 1
tenant: acme
rules:
  - id: r
    effect: deny
    reason: no
    match: {context: {time_of_day: "`+tc.window+`"}}
`)
		w := b.Rules[0].Match.Context.TimeOfDay
		if w == nil {
			t.Fatalf("%s: window not parsed", tc.window)
		}
		if w.String() != tc.window {
			t.Errorf("%s: round-trips as %s", tc.window, w)
		}
		for _, hm := range tc.inWindow {
			if !w.Contains(minuteOf(t, hm)) {
				t.Errorf("%s should contain %s", tc.window, hm)
			}
		}
		for _, hm := range tc.outside {
			if w.Contains(minuteOf(t, hm)) {
				t.Errorf("%s should not contain %s", tc.window, hm)
			}
		}
	}

	if _, err := model.Parse([]byte(`schema_version: 1
tenant: acme
rules:
  - {id: r, effect: deny, reason: no, match: {context: {time_of_day: "9am to 5pm"}}}
`)); err == nil {
		t.Error("a malformed time window was accepted")
	}
}

func minuteOf(t *testing.T, hm string) int {
	t.Helper()
	ts, err := time.Parse("15:04", hm)
	if err != nil {
		t.Fatalf("bad time %q: %v", hm, err)
	}
	return ts.Hour()*60 + ts.Minute()
}

func TestHostPatterns(t *testing.T) {
	valid := []string{"db01.prod.example.com", "*.prod.example.com", "db01"}
	for _, p := range valid {
		if !model.ValidHostPattern(p) {
			t.Errorf("%q should be a valid pattern", p)
		}
	}
	for _, p := range []string{"", "*", "db*.prod", "*.prod.*"} {
		if model.ValidHostPattern(p) {
			t.Errorf("%q should not be a valid pattern", p)
		}
	}

	if !model.MatchesHostPattern("DB01.Prod.Example.COM", "*.prod.example.com") {
		t.Error("matching must be case-insensitive: DNS is")
	}
	if model.MatchesHostPattern("prod.example.com", "*.prod.example.com") {
		t.Error("a wildcard must not match the bare suffix")
	}

	covers := []struct{ a, b string }{
		{"*.example.com", "db01.example.com"},
		{"*.example.com", "*.prod.example.com"},
		{"*.example.com", "*.example.com"},
		{"db01", "db01"},
	}
	for _, c := range covers {
		if !model.HostPatternCovers(c.a, c.b) {
			t.Errorf("%q should cover %q", c.a, c.b)
		}
	}
	for _, c := range []struct{ a, b string }{
		{"*.prod.example.com", "*.example.com"},
		{"db01", "db02"},
		{"db01.example.com", "*.example.com"},
	} {
		if model.HostPatternCovers(c.a, c.b) {
			t.Errorf("%q should not cover %q", c.a, c.b)
		}
	}
}

// TestRejectionIsStructured: the console renders the code and the parameters in
// the operator's locale (M21); the English is a convenience for a terminal, not
// the rejection.
func TestRejectionIsStructured(t *testing.T) {
	r := model.Reject(model.CodeMatchUnknownGroup, "prod-shell", 12, "group", "sre")
	if r.Code != model.CodeMatchUnknownGroup {
		t.Errorf("code = %q", r.Code)
	}
	if r.Params["group"] != "sre" {
		t.Errorf("params = %v, want the group carried as data", r.Params)
	}
	if !strings.Contains(r.Message(), "sre") {
		t.Errorf("message does not carry the parameter: %s", r.Message())
	}
	rendered := r.Error()
	for _, want := range []string{"line 12", `rule "prod-shell"`, string(model.CodeMatchUnknownGroup)} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered error does not contain %q: %s", want, rendered)
		}
	}
}

func TestRejectionsAreOrderedAndQueryable(t *testing.T) {
	rs := model.Rejections{
		model.Reject(model.CodeNoRules, "", 9),
		model.Reject(model.CodeRuleIDMissing, "", 2),
	}.Sorted()
	if rs[0].Line != 2 {
		t.Errorf("rejections not sorted by line: %v", rs)
	}
	if !rs.Has(model.CodeNoRules) || rs.Has(model.CodeTenantMissing) {
		t.Errorf("Has is wrong: %v", rs.Codes())
	}
	if !strings.Contains(model.Rejections(rs).Error(), "2 rejections") {
		t.Errorf("summary line is wrong: %s", rs.Error())
	}
	if !strings.Contains(model.Rejections{rs[0]}.Error(), "1 rejection") {
		t.Error("summary line does not agree with itself on one rejection")
	}
}

// TestAdditionalContextIsStringOrObject. A number, a list or a boolean is a
// contract violation rather than something to coerce, because this is stored
// verbatim for an auditor.
func TestAdditionalContextIsStringOrObject(t *testing.T) {
	var text model.AdditionalContext
	if err := json.Unmarshal([]byte(`"approved by change board"`), &text); err != nil {
		t.Fatalf("string: %v", err)
	}
	if text.Text != "approved by change board" || text.Fields != nil {
		t.Errorf("string decoded as %+v", text)
	}
	out, err := json.Marshal(text)
	if err != nil || string(out) != `"approved by change board"` {
		t.Errorf("string round-trip = %s, %v", out, err)
	}

	var object model.AdditionalContext
	if err := json.Unmarshal([]byte(`{"ticket":"CHG1"}`), &object); err != nil {
		t.Fatalf("object: %v", err)
	}
	if object.Fields["ticket"] != "CHG1" {
		t.Errorf("object decoded as %+v", object)
	}

	for _, bad := range []string{`42`, `[1,2]`, `true`, `null`} {
		var v model.AdditionalContext
		if err := json.Unmarshal([]byte(bad), &v); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}

func TestDurationIsAuthoredAsText(t *testing.T) {
	b := parse(t, `schema_version: 1
tenant: acme
rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      max_session_duration: 45m
`)
	if got := b.Rules[0].Route.MaxSessionDuration.Duration(); got != 45*time.Minute {
		t.Errorf("duration = %v, want 45m", got)
	}
	if _, err := model.Parse([]byte(`schema_version: 1
tenant: acme
rules:
  - {id: r, effect: allow, route: {intent: direct, channels: [session], filter: {mode: whitelist}, max_session_duration: "half an hour"}}
`)); err == nil {
		t.Error("a malformed duration was accepted")
	}
}
