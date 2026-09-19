// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package fleet owns the proxy graph: enrollment, heartbeat, zones,
// reachability, declared capabilities, and the pathfinding that turns "user
// here, target there" into a route (PLAN M6, M17). It is the only package that
// knows which hop direction is possible right now.
//
// The package is split so that the part worth proving is provable without a
// database:
//
//   - [Graph] and [Graph.Path] are pure values over pure inputs. A path is a
//     function of the nodes, the edges, the live relay registrations and the
//     clock, and nothing else.
//   - [Registry] loads those inputs out of internal/store, applies the
//     staleness rule, and composes. It owns enrollment, heartbeat, the
//     capability stores and the configuration rollout.
//
// Three things in here are load-bearing beyond their size, and each is written
// down where it happens:
//
//   - **No live path is an outage, never a deny** ([NoPathError], PLAN M11). A
//     user denied by policy and a user unreachable because an enclave relay is
//     down must not receive the same answer; that distinction is the whole
//     point of the proxy's disclosure rule.
//   - **A relay edge with no live registration is not selected, and is never
//     downgraded to a dial** (proxy D11). Downgrading would punch through the
//     boundary the mode exists to preserve.
//   - **A path never crosses a tenant** (M18). A [Graph] is built from one
//     tenant's rows, so a cross-tenant edge is unrepresentable rather than
//     filtered late — a viable-looking path filtered at the end is one some
//     later refactor forgets to filter.
package fleet
