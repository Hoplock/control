// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package ext is the public extension seam of Hoplock Control: the only
// package in this module that is importable from outside it. Hoplock
// Enterprise imports this module as a library and implements the interfaces
// declared here (PLAN M15). See README.md in this directory for how to
// implement a point, how to register one, and how to add one.
//
// Two invariants govern anything added here, and both are enforced by tests
// rather than by intention. Control never imports Enterprise — the dependency
// runs one way, and an import-graph guard fails the build if it ever does not.
// And every extension point has a real answer in this module, because a seam is
// not a hole where core functionality used to be: Hoplock Proxy plus Hoplock
// Control alone must be a complete, self-hostable product. That answer takes
// one of three forms, and PointInfo records which, so the claim is checkable
// rather than aspirational — Control's own code path continues (the local audit
// store, manual grants, its own software keys, the compiler's checks), or
// Control's wiring registers an implementation behind the seam, or the point
// adds a capability Control never claimed and says so. A point that ships
// nothing from Control and is not the third case fails the build.
//
// The package is interface-only, and importing it costs a consumer nothing but
// the interfaces: Control's own implementations behind the seam live in
// internal/extdefault, and a guard at the module root fails the build if
// anything here reaches into internal.
//
// Because Enterprise builds against these declarations, this package is a
// compatibility promise in the same sense the vendored wire contract is.
// Changing a signature here breaks a downstream build, so it is done
// deliberately and recorded — never as a drive-by.
package ext
