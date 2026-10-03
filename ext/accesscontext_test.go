// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext_test

import (
	"testing"

	"github.com/hoplock/control/ext"
)

// The zero answer is "could not determine". A provider that forgets to fill
// State in has said nothing a decision may rely on, and the type makes that
// the default rather than a convention (M11).
func TestAnUnfilledAnswerIsUndetermined(t *testing.T) {
	var e ext.AccessEvidence
	if e.State != ext.WindowUndetermined {
		t.Fatalf("zero State = %v, want undetermined", e.State)
	}
	for state, code := range map[ext.WindowState]string{
		ext.WindowUndetermined: "undetermined",
		ext.WindowConfirmed:    "confirmed",
		ext.WindowNotConfirmed: "not_confirmed",
		ext.WindowState(99):    "undetermined",
	} {
		if got := state.String(); got != code {
			t.Errorf("%d.String() = %q, want %q", int(state), got, code)
		}
	}
}
