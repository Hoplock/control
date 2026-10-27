// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package standing places and lifts the standing denials Control applies above
// the policy engine: a subject lock and a target quarantine (PLAN M28).
//
// # What a standing denial is
//
// While one stands, every authorize call it covers is a deny, whatever policy,
// grants or windows say. It is applied by the decision service, after the
// engine has no say and before it is asked (internal/decision), and it is NOT a
// policy input: a rule can match an input or ignore it, and a lock a policy
// author can write around is not a lock. M3 keeps the engine's vocabulary
// closed, and M17 is the precedent for a constraint the decision service
// applies on top of what policy answered.
//
// # One service, two callers
//
// An operator places and lifts these on the north-bound API, and a registered
// ext.ActionHandler places them through internal/action. Both call this
// package, so there is one implementation of what a lock does: the row, the
// record, the sessions it ends and the cached decisions it drops. An inbound
// action never reaches a lever an operator cannot reach (M15).
//
// # Placing is a transaction and a publication
//
// The row and its audit record commit together. What the act owes the
// revocation stream (the subject's cached decisions dropped, their sessions
// ended) is published after the commit, because the stream is not part of the
// database transaction, and it is published again by anything that asks for
// the same act twice. A publication that failed after the commit leaves the
// denial standing: the caller is told the sessions were not, and asking again
// is how they are.
package standing
