// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package contract

// The vendored document's own version and the commit it came from (M1), so a
// running binary can say which contract it was built against (0046, M19).
//
// They are constants rather than a read of contract/ at run time because the
// binary does not carry that directory. A test reads both out of the vendored
// copy and contract/UPSTREAM and fails when they disagree, so `make
// contract-sync` cannot land without moving them — the same way the enum
// test holds the closed vocabularies.
//
// Neither is PolicyVersion: that is the negotiated vocabulary on
// /v1/authorize, and the document's version moves without it (§4).
const (
	// DocumentVersion is the vendored document's `info.version`.
	DocumentVersion = "4.7.0"
	// VendoredCommit is the upstream commit contract/UPSTREAM records.
	VendoredCommit = "976faaa865cc4aa6692f21a6a277b212411c05ac"
)
