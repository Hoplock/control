// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package decision is the composition root for an authorize call: gather
// identity, target labels, grants, the fleet path, and connection metadata,
// evaluate, build the snapshot, write the decision record, and decide whether
// to issue a cache hint. The latency budget (PLAN M5) is enforced here.
//
// Nothing in this package decides anything on its own. The engine is pure and
// lives in internal/policy (M3); the graph, its liveness and the capability
// records are internal/fleet's (M6, M17); the identity was established at
// authentication (0007). What lives here is the ORDER those are composed in,
// and the refusals — which is exactly the part with no other home.
//
// The files, and what each is responsible for:
//
//   - service.go — the order, and the three answers one call can give: a
//     response, a [Denial] somebody authored, and an error, which is an outage.
//     A transport cannot turn the third into the second by accident, because
//     the third is not in [Outcome].
//   - inputs.go — what a rule may match on, gathered from this server's own
//     records rather than from the caller's claim about who is connecting.
//   - route.go — where the path starts (the proxy ASKING), and the three things
//     `conn.hop_trail` is for: the starting point, the loop refusal, and the hop
//     budget. Every failure here is an outage and never a `401` (M11).
//   - snapshot.go — the engine's output vocabulary rendered as the contract's,
//     and the checks that run before a response is written.
//   - capability.go — the issue-path constraint (M17): a rung, a platform or a
//     device field the enforcing proxy or the target cannot take.
//   - vocabulary.go — `policy_version`, both halves.
//   - record.go — the decision record (M4), on the allow path, the deny path,
//     and the path that could not be served.
//   - program.go, graph.go — the compiled policy and the fleet graph, held in
//     memory because a proxy is holding a user's handshake open (M5).
package decision
