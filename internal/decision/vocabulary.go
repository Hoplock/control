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
// `policy_version`, and decodes the authorize response STRICTLY: a field or an
// enum value it does not understand is not dropped, it fails the session
// closed — as an outage in front of a user rather than a deny. That rule is
// what makes the vocabulary safe to extend, and it only works if this server
// never answers above the declared version.
//
// THIS SERVER SERVES EXACTLY ONE VOCABULARY: contract.PolicyVersion, the
// vendored document's (0064). Proxy and server ship together and move
// together, so a fleet mid-upgrade is a rollout-ordering problem rather than a
// code path, and there is no older vocabulary here to answer within. Three
// answers, and they must stay three:
//
//   - `policy_version` ABSENT, or below the field's minimum: `400
//     invalid_request` (validate). A malformed caller that never said what it
//     reads — not answered at a guessed version, because guessing a version is
//     guessing which restrictions it would silently drop, and never `401`.
//   - `policy_version` is PolicyVersion: answered as the policy says. The
//     assembly takes no version at all, so there is nothing in it to thin.
//   - ANY OTHER `policy_version`, lower or higher: a `5xx` naming the number
//     the proxy declared, the number this server serves, and the proxy
//     (checkVersion). An operator reading it must conclude "this proxy is the
//     wrong build", not "the server is broken". It is not a deny (M11): a
//     version mismatch says nothing about the user. And it is never a thinned
//     `200`: dropping a permission is merely wrong, dropping a RESTRICTION is
//     a silently widened session, which is the failure this whole obligation
//     exists to prevent.
//
// WHAT THIS REPLACED. Until 0064 the gate was tiered: a route naming
// `brokered-certificate` needed vocabulary 5 and one carrying a floor or a ban
// needed 6, so a proxy declaring 4 was refused only the routes it could not
// read and served the rest. Those tiers refused rather than thinned, and
// folding them into this gate is safe only because this gate is stricter:
// every route a tier refused is refused here too, and so is every route a tier
// served below PolicyVersion. A future session that wants to serve a second
// vocabulary again needs the argument above, not just this conclusion — and it
// would be adding a per-field "needs vocabulary N" table that this server must
// then keep true for every field, which is the code that is never exercised
// until the day it is wrong.
//
// WHAT IS NOT A VERSION, and must not be folded into this gate:
//
//   - The `device_field.<name>` namespace. It is open on purpose: a name in it
//     is not a new policy field and demands no bump. Whether a driver accepts
//     a name is a CAPABILITY question (M17), answered in capability.go.
//   - The per-level floor rule. A proxy declaring PolicyVersion may still
//     enforce fewer `algorithm_floor` levels than the contract names, so a
//     level it did not declare in `capabilities.algorithm_floors` is refused as
//     a capability shortfall beside the rungs, and the operator looks at that
//     proxy's build rather than at the rollout.
//   - The per-proxy ban rule (Hoplock/proxy#72): a route whose bans leave an
//     axis of the asking proxy's declared lists empty is a capability
//     shortfall too, whatever version it declared.

// VersionMismatchError is a request declaring a policy vocabulary this server
// does not serve.
//
// It is a `5xx` on purpose (M11): it is a rollout problem, not a statement
// about the user. It is also a different answer from an ABSENT
// `policy_version`, which is a malformed request and a `400` — a declared wrong
// version is a proxy built against a different contract revision, an absent
// one is a caller that cannot say what it reads, and folding the two together
// loses which of them happened.
type VersionMismatchError struct {
	// Declared is what the proxy said it reads.
	Declared int32
	// Served is the one vocabulary this server answers in.
	Served int32
	// ProxyID is the proxy that asked, so the operator knows which build to
	// look at.
	ProxyID string
}

// Error is safe to disclose: it names two numbers and the caller's own id.
func (e *VersionMismatchError) Error() string {
	return fmt.Sprintf("proxy %q declared policy_version %d and this server serves policy_version %d only: "+
		"the proxy and this server are builds of different contract revisions, and they move together — "+
		"deploy the proxy build that declares %d, or the server that serves %d",
		e.ProxyID, e.Declared, e.Served, e.Served, e.Declared)
}

// checkVersion is the gate every authorize request passes through, after
// validation and BEFORE anything is read or evaluated.
//
// It runs first because its answer does not depend on the policy: a proxy
// that cannot read this server's vocabulary cannot read any answer from it, a
// deny included, so the refusal is the same for every route — deterministic,
// which is what lets the conformance suite assert it exactly. Running it after
// evaluation would answer the same wrong build `401` on one target and `5xx`
// on the next, and send its operator to debug permissions for the first.
func checkVersion(req *contract.AuthorizeRequest) error {
	if declared := *req.PolicyVersion; declared != contract.PolicyVersion {
		return &VersionMismatchError{Declared: declared, Served: contract.PolicyVersion, ProxyID: req.Conn.ProxyID}
	}
	return nil
}
