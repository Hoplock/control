// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package instance is this deployment's own identity, its health at every
// level it has, and the outbound registration that makes it supervisable
// (0046, PLAN M19).
//
// Three things live here, and they are kept apart on purpose:
//
//   - IDENTITY (identity.go) is the logical deployment's: one instance id,
//     generated once and kept in the store, a display name from
//     configuration, the version set and the tenant set. A node never carries
//     an identity of its own.
//   - HEALTH (health.go, node.go) is a different question with a different
//     shape: the deployment, each node in it, and the proxy fleet it serves,
//     each state observed at a time, and "not observed" never rendered as
//     healthy.
//   - SUPERVISION (feed.go, register.go) is a client of something above this
//     deployment — the only package here that dials outward on the management
//     plane. It is opt-in, revocable locally, a cluster singleton acquired
//     through ext.ClusterCoordinator, and it carries out identity, health and a
//     coarse event feed, and carries in nothing that acts.
//
// NOTHING HERE IS ON THE DECISION PATH. Health reads through a store pool of
// its own, at most once per its minimum interval, and the registration has its
// own HTTP client and its own goroutines. A supervisor that is unreachable,
// failing or hanging changes nothing about whether a user reaches a machine
// (M5, M11).
package instance
