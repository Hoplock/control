// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

import "fmt"

// MatchedTerm is one input value that made a rule match.
//
// An explanation is a first-class output of this engine, not a log line (M4):
// the proxy tells a user "access denied" and a session id, deliberately vague,
// and the operator resolves that id here into the whole story. Vague to the
// user, total to the auditor — and that pair only works if this side is
// genuinely total, which means the terms are recorded as values rather than
// rendered into a sentence somebody has to parse back.
type MatchedTerm struct {
	// Axis is which input axis the term came from.
	Axis MatchAxis
	// Term is the name of the constraint, e.g. "groups" or "labels.env".
	Term string
	// Value is the input value that satisfied it.
	Value string
}

// String renders the term the way an operator reads it.
func (t MatchedTerm) String() string {
	return fmt.Sprintf("%s.%s=%s", t.Axis, t.Term, t.Value)
}
