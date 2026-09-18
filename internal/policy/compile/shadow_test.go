// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package compile_test

import (
	"strings"
	"testing"

	"github.com/hoplock/control/internal/policy/model"
)

// Unreachability is decided conservatively: where containment cannot be proven
// the rule is left alone. So this table has two halves, and the second half —
// the pairs that must NOT be reported — is the more important one. A missed
// shadow is a rule nobody needed; a false one is the compiler refusing a policy
// that was correct.
func TestUnreachableRules(t *testing.T) {
	cases := []struct {
		name        string
		first       string
		second      string
		unreachable bool
	}{
		{
			name:        "identical matches",
			first:       `{target: {zones: [dc1]}}`,
			second:      `{target: {zones: [dc1]}}`,
			unreachable: true,
		},
		{
			name:        "a catch-all shadows everything after it",
			first:       `{}`,
			second:      `{subject: {groups: [sre]}}`,
			unreachable: true,
		},
		{
			name:        "a wider group list shadows a narrower one",
			first:       `{subject: {groups: [sre, dba]}}`,
			second:      `{subject: {groups: [sre]}}`,
			unreachable: true,
		},
		{
			name:        "an extra constraint only narrows the later rule",
			first:       `{target: {zones: [dc1]}}`,
			second:      `{target: {zones: [dc1]}, subject: {groups: [sre]}}`,
			unreachable: true,
		},
		{
			name:        "a wildcard hostname shadows a name under it",
			first:       `{target: {hostnames: ["*.example.com"]}}`,
			second:      `{target: {hostnames: [db01.example.com]}}`,
			unreachable: true,
		},
		{
			name:        "a wider prefix shadows a narrower one",
			first:       `{context: {source_cidrs: ["10.0.0.0/8"]}}`,
			second:      `{context: {source_cidrs: ["10.1.0.0/16", "10.2.3.4"]}}`,
			unreachable: true,
		},
		{
			name:        "a wider label vocabulary shadows a narrower one",
			first:       `{target: {labels: {env: [prod, dev]}}}`,
			second:      `{target: {labels: {env: [prod], kind: [host]}}}`,
			unreachable: true,
		},
		{
			name:        "an unconstrained MFA term shadows a constrained one",
			first:       `{target: {zones: [dc1]}}`,
			second:      `{target: {zones: [dc1]}, subject: {mfa: true}}`,
			unreachable: true,
		},
		{
			name:        "a wider time window shadows a narrower one",
			first:       `{context: {time_of_day: "09:00-17:00"}}`,
			second:      `{context: {time_of_day: "10:00-12:00"}}`,
			unreachable: true,
		},
		{
			name:        "a wider day list shadows a narrower one",
			first:       `{context: {days: [monday, tuesday]}}`,
			second:      `{context: {days: [monday]}}`,
			unreachable: true,
		},
		{
			name:        "an unconstrained device axis shadows a posture requirement",
			first:       `{target: {zones: [dc1]}}`,
			second:      `{target: {zones: [dc1]}, device: {required: true}}`,
			unreachable: true,
		},
		{
			name:        "an unconstrained grant axis shadows a grant requirement",
			first:       `{target: {zones: [dc1]}}`,
			second:      `{target: {zones: [dc1]}, grant: {required: true}}`,
			unreachable: true,
		},

		// ---- and the pairs that must be left alone ----
		{
			name:   "a narrower group list does not shadow a wider one",
			first:  `{subject: {groups: [sre]}}`,
			second: `{subject: {groups: [sre, dba]}}`,
		},
		{
			name:   "an exact hostname does not shadow a wildcard",
			first:  `{target: {hostnames: [db01.example.com]}}`,
			second: `{target: {hostnames: ["*.example.com"]}}`,
		},
		{
			name:   "a narrower prefix does not shadow a wider one",
			first:  `{context: {source_cidrs: ["10.1.0.0/16"]}}`,
			second: `{context: {source_cidrs: ["10.0.0.0/8"]}}`,
		},
		{
			name:   "an MFA constraint does not shadow an unconstrained rule",
			first:  `{subject: {mfa: true}}`,
			second: `{target: {zones: [dc1]}}`,
		},
		{
			name:   "a posture requirement does not shadow a rule without one",
			first:  `{device: {required: true}}`,
			second: `{target: {zones: [dc1]}}`,
		},
		{
			name:   "a grant requirement does not shadow a rule without one",
			first:  `{grant: {required: true}}`,
			second: `{target: {zones: [dc1]}}`,
		},
		{
			name:   "a narrower time window does not shadow a wider one",
			first:  `{context: {time_of_day: "10:00-12:00"}}`,
			second: `{context: {time_of_day: "09:00-17:00"}}`,
		},
		{
			name:   "an overnight window does not shadow the working day",
			first:  `{context: {time_of_day: "22:00-06:00"}}`,
			second: `{context: {time_of_day: "09:00-17:00"}}`,
		},
		{
			name:   "different zones do not shadow each other",
			first:  `{target: {zones: [dc1]}}`,
			second: `{target: {zones: [dc2]}}`,
		},
		{
			name:   "an extra label key on the earlier rule narrows it",
			first:  `{target: {labels: {env: [prod], kind: [host]}}}`,
			second: `{target: {labels: {env: [prod]}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := header(`labels:
  env: [prod, dev]
  kind: [host, appliance]
groups: [sre, dba]
rules:
  - {id: earlier, effect: deny, reason: first, match: ` + tc.first + `}
  - {id: later, effect: deny, reason: second, match: ` + tc.second + `}
`)
			rs := compileSource(t, src)
			reported := rs.Has(model.CodeRuleUnreachable)
			if reported != tc.unreachable {
				t.Fatalf("unreachable reported = %v, want %v (rejections: %v)", reported, tc.unreachable, rs)
			}
			if !tc.unreachable {
				return
			}
			var found model.Rejection
			for _, r := range rs {
				if r.Code == model.CodeRuleUnreachable {
					found = r
				}
			}
			if found.Rule != "later" {
				t.Errorf("the rejection names %q, want the rule that can never match", found.Rule)
			}
			if !strings.Contains(found.Message(), "earlier") {
				t.Errorf("the rejection does not name the rule that shadows it: %s", found.Message())
			}
		})
	}
}

// TestOnlyTheFirstShadowIsReported: a rule shadowed by three earlier ones is
// still one mistake, and three messages about it would bury the other
// rejections in the same compile.
func TestOnlyTheFirstShadowIsReported(t *testing.T) {
	rs := compileSource(t, header(`rules:
  - {id: a, effect: deny, reason: x, match: {}}
  - {id: b, effect: deny, reason: x, match: {}}
  - {id: c, effect: deny, reason: x, match: {}}
`))
	var count int
	for _, r := range rs {
		if r.Code == model.CodeRuleUnreachable {
			count++
			if r.Params["shadowed_by"] != "a" {
				t.Errorf("rule %q says it is shadowed by %q, want the first", r.Rule, r.Params["shadowed_by"])
			}
		}
	}
	if count != 2 {
		t.Errorf("reported %d unreachable rules, want one per shadowed rule", count)
	}
}
