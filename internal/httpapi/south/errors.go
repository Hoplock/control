// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south

import (
	"errors"
	"net/http"

	"github.com/hoplock/control/internal/contract"
)

// The error mapper, and the reason it is a file of its own.
//
// M11 binds this side hardest: a `401` means this server DECIDED to deny, and
// everything else means outage. Get it wrong and the proxy faithfully tells a
// real user "access denied" while the operator is sent to debug permissions
// during a database failover.
//
// So the discipline here is structural rather than conventional:
//
//   - [statusFor] is the ONLY function in this package that chooses a status
//     code, and it chooses from a classified failure rather than from an
//     error's text or type.
//   - an unclassified error — a database timeout, a context deadline, a
//     wrapped `fmt.Errorf`, a recovered panic — cannot reach 401, because the
//     only branch that produces one requires a [contract.StatusError] whose
//     class is [contract.ErrDenied], and the only way to build one of those is
//     [contract.Denied].
//   - the three places that build a 401 are [deny], [rejectCredential] and
//     [refuseToMint], all below, and `TestOnlyOneFunctionCanProduceA401`
//     fails the build if a fourth appears.
//
// The layers beneath keep the same shape for the same reason: `identity`
// returns a typed [identity.Outcome] whose `Deny` field is the only refusal,
// and `fleet.AuthenticateProxyToken` returns `(caller, ok, error)` where an
// outage can never arrive as `ok == false`.

// deny builds the one error that becomes a 401: a decision this server made on
// purpose about the end user's credential.
//
// The message is deliberately uninformative. A precise denial makes this
// server an oracle for probing the estate; the operator resolves the session
// id into the whole story instead (M4, proxy PLAN §4.3). The REASON is logged
// here and never disclosed.
func deny(message string) error { return contract.Denied(message) }

// rejectCredential builds the 401 for the PROXY's own channel token, which is
// a different refusal from the one above: the contract's `Unauthorized`
// response covers both, and an operator reading a log line needs to know which
// of the two happened.
func rejectCredential() error {
	return contract.Denied("the proxy credential was rejected")
}

// refuseToMint builds the 401 for a brokered-certificate issuance this server
// refuses to make (Hoplock/proxy#68): the decision cited is not one it made for
// the method, or the target or account does not match it. It is the third
// denier, and a decision rather than a reuse of [deny]: it refuses neither the
// user's credential nor the proxy's token — the session was already authorized
// — and an operator reading the log line needs to know that. A store failure
// reading the decision never arrives here; it is a 5xx.
func refuseToMint(code, message string) error { return contract.RefusedToMint(code, message) }

// noAuthority builds the 503 for an issuance with no certificate authority
// behind it: a tenant with none, or a deployment that configured none. It is an
// operator's fix, so it is neither a deny nor a 500.
func noAuthority(message string) error {
	return contract.Unavailable(contract.ErrCodeNoAuthority, message)
}

// statusFor maps a handler's error onto the status code and envelope the
// contract specifies. It is the only place either is chosen.
func statusFor(err error, correlationID string) (int, contract.ErrorResponse) {
	var se *contract.StatusError
	if !errors.As(err, &se) {
		return http.StatusInternalServerError, internalEnvelope(correlationID)
	}

	switch {
	case errors.Is(se.Class, contract.ErrDenied):
		return http.StatusUnauthorized, se.Envelope()
	case errors.Is(se.Class, contract.ErrInvalidRequest):
		return http.StatusBadRequest, se.Envelope()
	case errors.Is(se.Class, contract.ErrUIDRangeExhausted):
		return http.StatusConflict, se.Envelope()
	case errors.Is(se.Class, errNotEnrolled):
		return http.StatusNotFound, se.Envelope()
	case errors.Is(se.Class, errNoSuchRoute):
		// Not a status the contract enumerates, because no conformant proxy
		// asks for a path this server does not serve. It exists so that a
		// request pointed at the wrong port — a north-bound one, most
		// likely — gets the contract's envelope rather than net/http's
		// plain-text default, and so that it is not silently a 500 that an
		// operator would read as an outage.
		return http.StatusNotFound, se.Envelope()
	case errors.Is(se.Class, contract.ErrUnavailable):
		return http.StatusServiceUnavailable, se.Envelope()
	case errors.Is(se.Class, contract.ErrPolicyVersionUnsupported):
		// A proxy this server cannot serve within the vocabulary it
		// declared is a ROLLOUT problem, and saying so with a 5xx is the
		// point: it is not a statement about the user.
		return http.StatusInternalServerError, se.Envelope()
	default:
		// A StatusError with no class is still an unclassified failure.
		// It falls here rather than into any branch above, which is what
		// makes "everything that is not a decision is an outage" true of
		// the default case as well as of the named ones.
		return http.StatusInternalServerError, internalEnvelope(correlationID)
	}
}

// internalEnvelope is the 5xx body. It names the correlation id and nothing
// else: the message must be safe to disclose (M11), and the whole story is in
// this server's own logs under that id.
func internalEnvelope(correlationID string) contract.ErrorResponse {
	msg := "the server failed to process an otherwise valid request"
	if correlationID != "" {
		msg += " (correlation id " + correlationID + ")"
	}
	return contract.ErrorResponse{
		Error: contract.ErrorBody{Code: contract.ErrCodeInternal, Message: msg},
	}
}

// errNotEnrolled classifies a configuration call for a proxy the registry does
// not hold in the caller's tenant (proxy D18): a registry fact, so a 404 and
// never a 401.
var errNotEnrolled = errors.New("south: not enrolled")

func notEnrolled(proxyID string) error {
	return &contract.StatusError{Class: errNotEnrolled, Code: contract.ErrCodeNotEnrolled,
		Message: "no proxy " + proxyID + " is enrolled for this credential's tenant"}
}

// errNoSuchRoute classifies a path this listener does not serve.
var errNoSuchRoute = errors.New("south: no such route")

// notFound builds the 404 for a path that is not on this listener.
func notFound(message string) error {
	return &contract.StatusError{Class: errNoSuchRoute, Code: "not_found", Message: message}
}

// invalid builds the 400 for a request this server could not read or could not
// act on. It is never a 401: nobody was named to refuse.
func invalid(message string) error { return contract.Invalid(message) }

// versionUnsupported builds the 5xx for policy this server cannot express
// within the vocabulary the proxy declared.
//
// It is never a 401 and never a 400: a declared wrong version is a fleet
// mid-upgrade, not a decision about a user and not a malformed caller. The
// message names both versions because the operator's next question is which
// proxy to upgrade.
func versionUnsupported(message string) error { return contract.VersionUnsupported(message) }

// exhausted builds the 409 for a uid cursor that has reached the top of its
// range. The proxy treats it exactly as an exhausted block — outage-class,
// nothing provisioned — and the remedy is the operator's.
func exhausted(message string) error { return contract.Exhausted(message) }
