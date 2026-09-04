// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package decision is the composition root for an authorize call: gather
// identity, target labels, grants, the fleet path, and connection metadata,
// evaluate, build the snapshot, write the decision record, and decide whether
// to issue a cache hint. The latency budget (PLAN M5) is enforced here.
package decision
