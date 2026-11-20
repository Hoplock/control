// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package contract

// THE ONE PLACE THIS SERVER'S CONTRACT NUMBERS ARE SPELLED (0064).
//
// Hoplock Control implements exactly one contract: the vendored one (M1). It
// therefore has exactly one policy vocabulary it serves and exactly one
// document version it was built against, and both are defined here and
// nowhere else. TestTheNumbersAreTheVendoredDocuments reads each out of
// contract/control.yaml and fails when they disagree, so a `make
// contract-sync` that moves either one stops at that test and a human decides
// what the new number means for this server. That is the point at which
// supporting a second vocabulary would become a deliberate decision rather
// than an accident. TestNoSecondVersionLiteral (version_literal_test.go) keeps
// every other file in the tree from spelling either number.
//
// They are constants rather than a read of contract/ at run time because the
// binary does not carry that directory.
//
// THEY ARE TWO NUMBERS, AND NEITHER IS DERIVED FROM THE OTHER (PLAN §4).
// `policy_version` governs /v1/authorize and nothing else — that is the
// response a proxy decodes strictly — so a field on another endpoint, a whole
// new endpoint and a tightening all move the document without moving the
// vocabulary, while a new field or enum value inside the authorize response
// moves both. Nor does the document version only rise: upstream
// Hoplock/proxy#53 moved it DOWN, 4.3.0 to 4.0.0, and Hoplock/proxy#66 then
// moved it back to 4.3.0, so that string has named two different documents. A
// version string identifies no document; the checksum in contract/UPSTREAM
// does (`make contract-check`), and VendoredCommit says where it came from.
//
// AND NEITHER IS THE NORTH-BOUND API'S (M19). That surface has an installed
// base — a supervisor reads many deployments at many releases — so it keeps a
// range and its own negotiation (internal/httpapi/north/apiversion.go,
// docs/API-VERSIONING.md), and nothing here moves it.
const (
	// PolicyVersion is the one policy vocabulary this server serves: the
	// number a proxy declares on AuthorizeRequest.policy_version, and the only
	// one this server answers. A proxy declaring any other — lower or higher —
	// is refused with a 5xx naming both numbers and its proxy id, never
	// answered at a guess and never 401 (PLAN §4, M11). It is the vocabulary
	// of the vendored document, which states it as "the current vocabulary".
	PolicyVersion int32 = 6

	// PolicyVersionMinimum is the least value `policy_version` may carry (the
	// schema's `minimum`). It is NOT a vocabulary this server serves: it is
	// where "malformed" ends and "a vocabulary this server does not serve"
	// begins, so a request below it is a 400 and one at or above it that is
	// not PolicyVersion is the mismatch 5xx.
	PolicyVersionMinimum int32 = 1

	// DocumentVersion is the vendored document's `info.version`, reported as
	// `versions.contract` on `GET …/instance` (0046, M19). It identifies a
	// revision for a reader; it does not identify the bytes.
	DocumentVersion = "4.7.0"

	// VendoredCommit is the upstream commit contract/UPSTREAM records.
	VendoredCommit = "976faaa865cc4aa6692f21a6a277b212411c05ac"
)
