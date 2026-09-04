// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package fleet owns the proxy graph: enrollment, heartbeat, zones,
// reachability, declared capabilities, and the pathfinding that turns "user
// here, target there" into a route (PLAN M6, M17). It is the only package that
// knows which hop direction is possible right now.
package fleet
