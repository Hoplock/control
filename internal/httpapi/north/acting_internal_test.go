// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"strings"
	"testing"

	"github.com/hoplock/control/internal/access"
)

// The name a supervisor's call carries is decoded, then checked, on its own:
// every refusal names its problem, and an accepted name comes back exactly as
// it was encoded (0048).
func TestTheActingOperatorIsDecodedThenChecked(t *testing.T) {
	cases := []struct {
		name, raw, want, problem string
	}{
		{name: "plain ascii", raw: "Alice Example", want: "Alice Example"},
		{name: "ascii punctuation kept raw", raw: "o'brien, j. (ops) +1", want: "o'brien, j. (ops) +1"},
		{name: "a plus is a plus", raw: "a+b", want: "a+b"},
		{name: "non-ascii percent-encoded", raw: "Jos%C3%A9%20Mar%C3%ADa%20N%C3%BA%C3%B1ez", want: "José María Núñez"},
		{name: "cjk", raw: "%E5%B1%B1%E7%94%B0%E5%A4%AA%E9%83%8E", want: "山田太郎"},
		{name: "an encoded percent", raw: "100%25 on call", want: "100% on call"},
		{name: "lower-case hex", raw: "%c3%a9", want: "é"},
		{name: "case and form kept", raw: "e%CC%81VA", want: "éVA"},
		{name: "exactly 128 characters", raw: strings.Repeat("%C3%A9", 128), want: strings.Repeat("é", 128)},

		{name: "empty", raw: "", problem: access.ProblemInvalid},
		{name: "a raw percent that does not decode", raw: "100% sure", problem: access.ProblemInvalid},
		{name: "a truncated escape", raw: "abc%C", problem: access.ProblemInvalid},
		{name: "a raw octet outside printable ascii", raw: "Jos\xc3\xa9", problem: access.ProblemInvalid},
		{name: "a raw tab", raw: "a\tb", problem: access.ProblemInvalid},
		{name: "not utf-8 once decoded", raw: "%C3%28", problem: access.ProblemInvalid},
		{name: "an encoded control character", raw: "a%0Ab", problem: access.ProblemInvalid},
		{name: "an encoded nul", raw: "a%00b", problem: access.ProblemInvalid},
		{name: "a right-to-left override", raw: "alice%E2%80%AEtxt.exe", problem: access.ProblemInvalid},
		{name: "a zero-width joiner", raw: "a%E2%80%8Db", problem: access.ProblemInvalid},
		{name: "a non-breaking space", raw: "a%C2%A0b", problem: access.ProblemInvalid},
		{name: "a leading space", raw: "%20alice", problem: access.ProblemInvalid},
		{name: "a trailing space", raw: "alice%20", problem: access.ProblemInvalid},
		{name: "only a space", raw: "%20", problem: access.ProblemInvalid},
		{name: "129 characters", raw: strings.Repeat("a", 129), problem: access.ProblemTooLong},
		{name: "129 encoded characters", raw: strings.Repeat("%C3%A9", 129), problem: access.ProblemTooLong},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, problem := DecodeActingOperator(c.raw)
			if problem != c.problem || got != c.want {
				t.Fatalf("DecodeActingOperator(%q) = %q, %q; want %q, %q", c.raw, got, problem, c.want, c.problem)
			}
		})
	}
}
