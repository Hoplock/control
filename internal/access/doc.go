// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package access owns time-boxed grants (PLAN M10): creating one, reading it,
// revoking it, and the requests that precede one when a Hoplock Enterprise
// approval workflow is registered (ext.GrantWorkflow).
//
// THREE RULES, and every function here keeps them:
//
//   - A grant is a POLICY INPUT, never a bypass. The engine reads it like any
//     other input (PolicyGrants is the whole translation) and has no branch
//     on how it came to exist, so simulation and "explain why" tell the same
//     story for an administrator's grant, a workflow's and an external
//     system's (M10, M16).
//   - EXPIRY IS A PREDICATE, NOT A JOB. A grant is live exactly when the
//     evaluation's time input falls inside its window and it has not been
//     revoked. Nothing here has to run for a grant to stop working, so there
//     is no sweeper whose failure would be standing production access.
//   - ACCESS ENDING NEVER LOOKS LIKE A CRASH. Revoking a grant ends the
//     sessions it backed with a session_kill whose reason the holder is shown
//     (M9), and a grant created or revoked is audited in the same transaction
//     that creates or revokes it: an act this server cannot write down is one
//     it does not perform.
package access
