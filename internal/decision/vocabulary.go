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
// REMOVING THE OLDER VOCABULARIES DID NOT REMOVE THE VERSIONING. Upstream
// collapsed the contract to one live vocabulary stated in the present tense,
// with no "since version N" annotation on any field — so `requiredVersion`
// answers the current baseline for every response this server can build today.
// It is not vestigial: it is where the NEXT revision tiers its own fields, as a
// case that runs first, and the proxy's `cmd/mock-control` keeps the same shape
// for the same reason. A session reading "one vocabulary" as "nothing to
// negotiate" would delete the only thing standing between a fleet mid-upgrade
// and a silently widened session.

// requiredVersion reports the lowest policy vocabulary that can express this
// response.
//
// It answers per RESPONSE and not per build, which is the point: the refusal is
// per route, so a proxy one revision behind still gets every route it CAN read.
// With one live vocabulary that is every route or none, and the next revision
// is what makes the distinction bite again:
//
//	switch {
//	case resp.SomeFieldAddedInVocabulary5 != nil:
//		return 5
//	}
//
// Every field added to the contract needs such a case, or this server hands it
// to a proxy that fails the session closed on it.
//
// The `device_field.<name>` namespace is deliberately NOT a case here and must
// never become one. It is an open namespace: a name inside it is not a new
// policy field, it demands no bump, and there is no version to gate it on — an
// older proxy parses a route bearing device fields exactly as it parses one
// without them. Whether a driver accepts a given name is a CAPABILITY question
// (M17), answered in capability.go against what the enforcing proxy declared.
func requiredVersion(resp *contract.AuthorizeResponse) int32 {
	_ = resp
	return contract.PolicyVersion
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
