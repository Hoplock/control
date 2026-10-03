// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package compile_test

import (
	"strings"
	"testing"

	"github.com/hoplock/control/internal/policy/model"
)

// The compiled program carries the `scopes` section for the two readers that
// need it — the layer deciding which external windows a decision may count,
// and the push receiver — and an undeclared scope reads as the zero
// declaration: not privileged, no answer of its own.
func TestTheProgramCarriesScopeDeclarations(t *testing.T) {
	prog := mustCompile(t, header(`scopes:
  vuln-scan: {privileged: true}
  change-window: {unanswered: open}
rules:
  - {id: r, effect: allow, match: {grant: {scopes: [vuln-scan]}}, route: `+allowRoute+`}
`))
	scan, ok := prog.Scope("vuln-scan")
	if !ok || !scan.Privileged {
		t.Errorf("vuln-scan = %+v, %v; want declared privileged", scan, ok)
	}
	if cw, _ := prog.Scope("change-window"); cw.Unanswered != model.UnansweredOpen {
		t.Errorf("change-window = %+v, want unanswered open", cw)
	}
	if d, ok := prog.Scope("prod-dba"); ok || d.Privileged || d.Unanswered != model.UnansweredUnset {
		t.Errorf("an undeclared scope = %+v, %v; want the zero declaration", d, ok)
	}
}

// A privileged scope that falls open is refused at compile time with a message
// an author can act on, like every other rejection.
func TestAPrivilegedScopeThatFallsOpenDoesNotCompile(t *testing.T) {
	rs := compileSource(t, header(`scopes:
  vuln-scan: {privileged: true, unanswered: open}
rules:
  - {id: r, effect: allow, route: `+allowRoute+`}
`))
	if !rs.Has(model.CodeScopePrivilegedFailOpen) {
		t.Fatalf("compiled; want %s, got %v", model.CodeScopePrivilegedFailOpen, rs.Codes())
	}
	for _, r := range rs {
		if r.Code != model.CodeScopePrivilegedFailOpen {
			continue
		}
		msg := r.Message()
		if m := placeholder.FindString(msg); m != "" {
			t.Errorf("message has an unsubstituted parameter %s: %s", m, msg)
		}
		if !strings.Contains(msg, "vuln-scan") || !strings.Contains(msg, "closed") {
			t.Errorf("message does not name the scope and the way out: %s", msg)
		}
		if r.Line != 4 {
			t.Errorf("rejection at line %d, want the declaration's, 4", r.Line)
		}
	}
}
