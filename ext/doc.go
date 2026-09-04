// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package ext is the public extension seam of Hoplock Control: the only
// package in this module that is importable from outside it. Hoplock
// Enterprise imports this module as a library and implements the interfaces
// declared here (PLAN M15); phase 0004 fills the package in.
//
// Two invariants govern anything added here, and both are enforced by tests
// rather than by intention. Control never imports Enterprise — the dependency
// runs one way, and an import-graph guard fails the build if it ever does not.
// And every extension point ships a real default implementation in this
// module, because a seam is not a hole where core functionality used to be:
// Hoplock Proxy plus Hoplock Control alone must be a complete, self-hostable
// product.
//
// Because Enterprise builds against these declarations, this package is a
// compatibility promise in the same sense the vendored wire contract is.
// Changing a signature here breaks a downstream build, so it is done
// deliberately and recorded — never as a drive-by.
package ext
