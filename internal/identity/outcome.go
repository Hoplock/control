// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import "time"

// Outcome is what one authentication attempt produced.
//
// IT IS THE MECHANISM THAT KEEPS M11 HONEST, and it is worth saying why it is
// a struct rather than an error. A deny is a DECISION this server made on
// purpose; a database timeout, a provider that did not answer, and a panic are
// outages. If both travelled as `error`, telling them apart would be a
// convention — a sentinel somebody remembers to wrap, a helper somebody
// remembers to call — and the cost of forgetting is that the proxy faithfully
// tells a real user "access denied" and sends the operator to debug
// permissions during an outage.
//
// So the two travel on different return values. Every method on [Service]
// returns `(Outcome, error)`, and:
//
//   - a non-nil error is an OUTAGE, always, with no exceptions and nothing to
//     inspect. The transport answers 5xx and there is no path by which it
//     could answer anything else.
//   - a deny is `Outcome.Deny != nil` and can only be produced by code that
//     built a [Denial] on purpose.
//
// Exactly one of the three fields is set on a successful call.
type Outcome struct {
	// Identity is set when the attempt authenticated.
	Identity *Identity
	// Challenge is set when a second factor is outstanding.
	Challenge *Challenge
	// Deny is set when this server decided to refuse.
	Deny *Denial
}

// Authenticated builds the outcome for a resolved identity.
func Authenticated(id Identity) Outcome { return Outcome{Identity: &id} }

// Pending builds the outcome for an outstanding challenge.
func Pending(c Challenge) Outcome { return Outcome{Challenge: &c} }

// Denied builds a refusal. Spelling it out at each call site is the point: the
// only way to produce a 401 is to have decided to.
func Denied(reason DenyReason) Outcome { return Outcome{Deny: &Denial{Reason: reason}} }

// DenyReason says which refusal this was, for the server's own logs and audit
// records. It is a closed set so the `exhaustive` linter can check a caller
// that switches on it (M13).
//
// It is NEVER disclosed to the caller. The message the proxy relays says only
// that the credential was refused: a precise denial makes this server an
// oracle for probing the estate, and the operator resolves the session id into
// the whole story instead (M4, proxy PLAN §4.3).
type DenyReason string

const (
	// DenyUnknownKey is an offered key that belongs to no subject and to no
	// proxy in the fleet.
	DenyUnknownKey DenyReason = "unknown-key"
	// DenyKeyRevoked is a key this server has withdrawn.
	DenyKeyRevoked DenyReason = "key-revoked"
	// DenyKeyNotYetValid and DenyKeyExpired are the two ends of a
	// certificate's validity window.
	DenyKeyNotYetValid DenyReason = "key-not-yet-valid"
	DenyKeyExpired     DenyReason = "key-expired"
	// DenyLoginMismatch is a key that resolves to a subject which may not
	// present the offered login.
	DenyLoginMismatch DenyReason = "login-mismatch"
	// DenyUnknownLogin is a login that resolves to no identity. On a chain
	// leg it is the refusal that keeps a recognised proxy key from being a
	// wildcard.
	DenyUnknownLogin DenyReason = "unknown-login"
	// DenyNoPassword is a subject with no password credential. It is a
	// separate reason from a wrong one for the server's own logs only.
	DenyNoPassword DenyReason = "no-password"
	// DenyBadPassword is a password that did not verify.
	DenyBadPassword DenyReason = "bad-password"
	// DenyUnknownChallenge is a poll for a token this server never issued.
	DenyUnknownChallenge DenyReason = "unknown-challenge"
	// DenyChallengeSpent is a poll for a challenge that has already
	// answered, whichever way it answered. A resolved challenge is never
	// replayable.
	DenyChallengeSpent DenyReason = "challenge-spent"
	// DenyChallengeExpired is a challenge that ran out of time. Expiry is a
	// deny (PLAN §6), never a 200 that leaves the proxy polling.
	DenyChallengeExpired DenyReason = "challenge-expired"
	// DenyChallengeAbandoned is a challenge polled past its budget. A
	// caller that ignores `poll_after_ms` is not one this server keeps a
	// challenge open for.
	DenyChallengeAbandoned DenyReason = "challenge-abandoned"
	// DenyMFARefused is the second factor answering no.
	DenyMFARefused DenyReason = "mfa-refused"
)

// Denial is a refusal this server decided on.
type Denial struct {
	// Reason is for this server's logs and audit records, never for the
	// caller.
	Reason DenyReason
}

// Challenge is an outstanding second factor, as the caller needs it.
type Challenge struct {
	// Token is what the proxy polls with. It stays valid until ExpiresAt
	// and is never rotated mid-flight — the conformance suite asserts that,
	// and a proxy that had to track a moving token would have to write one
	// back into a request it has already sent.
	Token string
	// Prompt is shown to the user, so it must be safe to disclose.
	Prompt string
	// PollAfter is how long the proxy is asked to wait between polls.
	PollAfter time.Duration
	// ExpiresAt is when the challenge becomes a deny.
	ExpiresAt time.Time
}
