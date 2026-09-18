// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package extdefault is Control's own side of the extension seam: the
// implementations this repository registers into an ext.Registry before the
// server starts, so that a deployment with no Hoplock Enterprise present is a
// complete product rather than a set of holes (PLAN M15).
//
// It exists as a package of its own, rather than as code inside ext, for one
// reason: ext is what Hoplock Enterprise imports, and it stays interface-only
// so that importing it drags in nothing. A default belongs on this side of that
// line.
//
// Most of Control's answers are not here, and that is the design rather than an
// omission. Where Control's behaviour when nothing is registered is its own
// core code path — the local audit store, manual grants, its own software keys,
// the compiler's checks — the seam is additive and there is no default to
// register; ext.PointInfo records that as ext.WhenAbsentCore and names the
// phase that builds it. What lands here is the narrower set where Control must
// put an implementation *behind* the seam because everything above it goes
// through the seam: today that is the single-node cluster coordinator.
package extdefault
