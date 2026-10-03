// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package accessctx is external access context (PLAN M16): the framework
// around ext.AccessContextProvider that lets a system outside Hoplock open a
// time-boxed, target-scoped window — by push, by probe, or by both — without
// that system's software becoming an unaudited access-granting API.
//
// It owns the three properties M16 says belong to the framework rather than to
// each integration, so that a customer-written provider gets them for free:
//
//   - SCOPE. Every push is checked against its integration's scope binding —
//     which subjects it may grant to, which targets it may name, the longest
//     window it may open, whether it may open privileged access at all, and
//     which credentials may push for it. A push outside it is refused and
//     audited as an attempted privilege escalation (Service.Push).
//   - REPLAY, IDEMPOTENCY AND CLOCK SKEW. An assertion's id is its idempotency
//     key; a stale or future-dated one is refused; a window's start is read
//     through the skew a deployment tolerates.
//   - THE CEILING. A window is clamped — never refused — to the shorter of
//     the binding's maximum and the server's own, measured from when it opens.
//
// And it owns the probe path on the authorize call (Prober.Consider): which
// windows a decision asks about, inside a fixed share of the decision budget
// (M5), cached by the provider's TTL, and what an unanswered probe means — the
// scope's `unanswered` setting, closed for anything the policy marks
// privileged. Nothing here decides access. A confirmed window becomes a GRANT,
// stored through internal/access (push) or built for the one decision that
// relied on it (probe), and the engine reads it like any other (M10).
//
// Control's own provider — configured rather than coded — is in the
// declarative subpackage.
package accessctx
