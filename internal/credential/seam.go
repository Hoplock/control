// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"errors"
	"fmt"

	"github.com/hoplock/control/internal/contract"
)

// THE SEAM, NAMED FOR WHAT IS MISSING.
//
// The authority above is complete: it mints short-lived, narrowly-scoped
// certificates, it rotates, it revokes, and it verifies. What it cannot do is
// REACH A PROXY, because the contract has no way to say "use this certificate".
// `TargetAuth` is extensible for exactly this (proxy D6a, and the schema says so
// outright: "a future Hoplock Control that mints per-session target credentials
// itself arrives as another `method` plus its own parameters") — but the method
// value and its parameters are the Hoplock Proxy repository's to define, and
// `contract/` is vendored read-only here (M1).
//
// WHY NOT JUST EMIT ONE ANYWAY. The proxy decodes the authorize response
// STRICTLY and fails the session closed on anything it does not recognise. An
// invented method value would not degrade gracefully and would not be skipped as
// an unimplemented rung: it would reach a user as an outage. A new method is
// also a VOCABULARY change, so it bumps `policy_version` upstream — which is the
// mechanism that lets a fleet upgrade without an outage, and it is upstream's to
// operate.
//
// So this file is the seam and the tripwire, both. [ErrMethodNotInContract] is
// what the decision path would get if it asked for a certificate rung today, and
// `TestBrokeredCertificateIsNotYetInTheContract` fails the build on the day the
// method lands in the vendored document — which is the signal for the phase that
// syncs it to come here and delete the refusal.

// BrokeredCertificateMethod is the `TargetAuth.method` value this phase needs.
//
// It is spelled out here rather than left implicit so that the upstream request
// and the code agree on one string. It is deliberately NOT declared as a
// `contract.TargetAuthMethod` constant: `internal/contract` mirrors the vendored
// document and nothing else, and a constant there would be this repository
// inventing vocabulary (M1).
const BrokeredCertificateMethod = "brokered-certificate"

// The parameters the method needs, named here for the same reason.
const (
	// ParamCertificate carries the issued certificate in `authorized_keys`
	// form. It is the certificate ONLY: the proxy generated the key pair, so
	// no private key ever crosses this API — which is what makes the method
	// additive rather than a new class of secret in transit.
	ParamCertificate = "certificate"
	// ParamCertificateSerial lets the proxy name the certificate in its own
	// audit records, and lets an operator match a session to a row in
	// `ssh_certificates`.
	ParamCertificateSerial = "certificate_serial"
	// ParamCAPublicKeys is the trust bundle a target must hold, so a proxy
	// administering a target can publish it. Absent means the proxy is not
	// the one that publishes trust.
	ParamCAPublicKeys = "ca_public_keys"
)

// ErrMethodNotInContract reports that a brokered certificate cannot be put on
// the wire yet.
//
// It is an OUTAGE-class error and never a denial (M11): a decision that allowed
// and cannot be served is an `unserved` record and a `5xx`, not an "access
// denied" to a user who did nothing wrong.
var ErrMethodNotInContract = errors.New(
	"credential: the contract has no `brokered-certificate` method yet, so an issued certificate cannot be named on an authorize response; see the upstream request in this phase's learnings")

// LadderEntry renders an issued certificate as a `target_auth_ladder` entry.
//
// IT REFUSES, TODAY, ALWAYS. The body below is the whole of what the phase that
// syncs the contract has to turn on, and it is written out rather than left to
// that session's imagination — the shape it builds is the shape named in the
// upstream request, so the two cannot drift.
func LadderEntry(username string, issued Issued, trustBundle []TrustedKey) (contract.TargetAuth, error) {
	if username == "" {
		// `username` is required on every method the contract defines,
		// and it is never defaulted to the identity's login — a
		// client-typed string the proxy must not base an authorization
		// decision on. The same rule will hold for this method.
		return contract.TargetAuth{}, fmt.Errorf("credential: a brokered certificate rung must name the account to log in as")
	}
	if !methodInContract(BrokeredCertificateMethod) {
		return contract.TargetAuth{}, ErrMethodNotInContract
	}

	params := map[string]string{
		contract.ParamUsername: username,
		ParamCertificate:       issued.Certificate,
		ParamCertificateSerial: fmt.Sprintf("%d", issued.Serial),
	}
	if len(trustBundle) > 0 {
		lines := make([]string, 0, len(trustBundle))
		for _, k := range trustBundle {
			lines = append(lines, k.PublicKey)
		}
		params[ParamCAPublicKeys] = joinLines(lines)
	}
	return contract.TargetAuth{
		Method: contract.TargetAuthMethod(BrokeredCertificateMethod),
		Params: params,
	}, nil
}

// methodInContract reports whether the vendored contract defines a method value.
//
// It reads the CONSTANTS rather than the YAML, because `internal/contract`'s enum
// test already pins those to the document in both directions (0002) — so this is
// the cheap half of the same question, and the test that fails when the method
// lands is in that package's vocabulary rather than in a second parse of the
// contract.
func methodInContract(method string) bool {
	for _, m := range []contract.TargetAuthMethod{
		contract.TargetAuthEphemeralUser,
		contract.TargetAuthBrokeredKey,
		contract.TargetAuthEphemeralAccount,
		contract.TargetAuthStaticKey,
	} {
		if string(m) == method {
			return true
		}
	}
	return false
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}
