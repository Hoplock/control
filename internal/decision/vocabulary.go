// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"fmt"

	"github.com/hoplock/control/internal/contract"
)

// Vocabulary negotiation (PLAN §4), in one place.
//
// The proxy declares the highest policy vocabulary it implements in
// `policy_version`, and decodes this response STRICTLY: a field it does not
// understand is not dropped, it fails the session closed — as an outage in
// front of a user rather than a deny. That rule is what makes the vocabulary
// safe to extend, and it only works if this server never answers above the
// declared version.
//
// Two halves, and both are built:
//
//   - `policy_version` is REQUIRED and has no absent-value default. A request
//     that omits it is refused `400 invalid_request` — not answered at a
//     guessed version, and never `401`, because a deny is a decision about a
//     user and this is a malformed caller.
//   - when the policy needs vocabulary the proxy cannot read, the answer is a
//     `5xx` naming the mismatch, never a thinned snapshot. Dropping a
//     permission is merely wrong; dropping a RESTRICTION is a breach, and a
//     mid-upgrade fleet that silently enforces less than its policy says is
//     the exact failure this exists to prevent.
//
// THE TIERS. Upstream states the contract in the present tense, with no
// "since version N" on any field, so the tiers are this server's to keep — the
// same three the proxy's cmd/mock-control keeps (baselineVocabulary,
// vocabularyBrokeredCertificate, vocabularyAlgorithmFloor), and each a constant
// of its own in internal/contract rather than an offset from PolicyVersion:
//
//	contract.VocabularyAlgorithmFloor (6)       algorithm_floor, or a non-empty algorithm_bans
//	contract.VocabularyBrokeredCertificate (5)  a ladder naming brokered-certificate at any rung
//	contract.VocabularyBaseline (4)             everything else
//
// A proxy one or two revisions behind therefore still gets every route it CAN
// read, and is refused — whole, with a 5xx naming both numbers — exactly the
// routes it cannot. Never a thinned answer: a ladder with the certificate rung
// dropped, or a route with its floor or ban removed, is a dropped restriction
// (PLAN §4).

// requiredVersion reports the lowest policy vocabulary that can express this
// response.
//
// It answers per RESPONSE and not per build, which is the point: the refusal is
// per route. Every field added to the contract needs a case here, highest tier
// first, or this server hands it to a proxy that fails the session closed on
// it.
//
// The `device_field.<name>` namespace is deliberately NOT a case here and must
// never become one. It is an open namespace: a name inside it is not a new
// policy field, it demands no bump, and there is no version to gate it on — an
// older proxy parses a route bearing device fields exactly as it parses one
// without them. Whether a driver accepts a given name is a CAPABILITY question
// (M17), answered in capability.go against what the enforcing proxy declared.
// So is whether the proxy enforces a floor level, or can apply a ban without
// emptying an axis: those are per proxy (Hoplock/proxy#72), never a version.
func requiredVersion(resp *contract.AuthorizeResponse) int32 {
	if resp.AlgorithmFloor != "" || !resp.AlgorithmBans.Empty() {
		return contract.VocabularyAlgorithmFloor
	}
	for _, rung := range resp.TargetAuthLadder {
		if rung.Method == contract.TargetAuthBrokeredCertificate {
			return contract.VocabularyBrokeredCertificate
		}
	}
	return contract.VocabularyBaseline
}

// VersionMismatchError is policy this server cannot express within the
// vocabulary the caller declared.
//
// It is a `5xx` on purpose (M11): it is a rollout problem, not a statement
// about the user. It is also a different answer from an ABSENT
// `policy_version`, which is a malformed request and a `400` — a declared wrong
// version is a fleet mid-upgrade, an absent one is a caller that cannot say
// what it reads, and folding the two together loses which of them happened.
type VersionMismatchError struct {
	Declared int32
	Needed   int32
}

func (e *VersionMismatchError) Error() string {
	return fmt.Sprintf("decision: this route needs policy vocabulary %d; the proxy declared %d",
		e.Needed, e.Declared)
}

// checkVersion is the gate every answer passes through.
//
// It runs AFTER assembly and before anything is returned, because the question
// it answers is about the response rather than about the build.
func checkVersion(resp *contract.AuthorizeResponse, declared int32) error {
	if needed := requiredVersion(resp); declared < needed {
		return &VersionMismatchError{Declared: declared, Needed: needed}
	}
	return nil
}
