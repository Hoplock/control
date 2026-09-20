// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"encoding/json"
	"time"
)

// The rows the south-bound API answers out of (0007). They sit in their own
// file rather than in types.go because they are the first tables in this
// schema whose SEMANTICS are owned by the phase that added them: 0001 landed
// columns for later phases to give meaning to, and these arrived the other way
// round.

// SubjectKey is one public key or certificate a subject may offer.
//
// The fingerprint is the key, not the subject: `/v1/auth/cert` arrives with a
// key and has to answer whose it is.
type SubjectKey struct {
	// Fingerprint is OpenSSH's SHA256 form, the same string the proxy
	// relays.
	Fingerprint string
	// SubjectID is whose key it is.
	SubjectID string
	// KeyType is the SSH algorithm name, stored as offered.
	KeyType string
	// IsCertificate says whether the material is a certificate.
	IsCertificate bool
	// ValidFrom and ValidTo are the certificate's validity window. Both
	// zero means no window, which is what a bare key has — NOT a window
	// that has closed.
	ValidFrom time.Time
	ValidTo   time.Time
	// RevokedAt is when the key was withdrawn. Non-zero is a revoked key,
	// and authentication is never cached (proxy §6.4), so this is read on
	// every call rather than distributed.
	RevokedAt time.Time

	CreatedAt time.Time
}

// PasswordDigest is a stored password verifier.
//
// The parameters travel WITH the digest rather than being constants in Go: a
// stored hash is verified with the parameters it was written with, which is
// what lets the cost be raised without invalidating every existing row.
type PasswordDigest struct {
	// SubjectID is whose credential it is.
	SubjectID string
	// Algorithm names the KDF, e.g. "pbkdf2-sha256".
	Algorithm string
	// Iterations is the KDF's work factor.
	Iterations int
	// Salt is per subject and is not a secret.
	Salt []byte
	// Digest is the derived key. It is not reversible and it is not a
	// credential: presenting it does not authenticate anything.
	Digest []byte

	UpdatedAt time.Time
}

// MFAEnrollment names the second-factor provider a subject is enrolled with.
//
// Config is opaque here and belongs to the provider. Absence of a row is a
// real state — the subject has no second factor — rather than a missing
// configuration.
type MFAEnrollment struct {
	SubjectID string
	Provider  string
	Config    json.RawMessage

	UpdatedAt time.Time
}

// MFAChallengeState is where an outstanding challenge has got to. Closed set:
// a state the server does not recognise is not one it can act on.
type MFAChallengeState string

const (
	// MFAChallengePending is issued and not yet answered.
	MFAChallengePending MFAChallengeState = "pending"
	// MFAChallengeApproved is answered yes. The token is spent.
	MFAChallengeApproved MFAChallengeState = "approved"
	// MFAChallengeDenied is answered no. The token is spent.
	MFAChallengeDenied MFAChallengeState = "denied"
	// MFAChallengeExpired ran out of time before it was answered. It is a
	// DENY (PLAN §6) and the token is spent.
	MFAChallengeExpired MFAChallengeState = "expired"
)

// MFAChallenge is one outstanding second factor.
//
// It is a row rather than a map in a process because nothing makes a proxy's
// polls land on the node that issued the challenge (M5). An in-memory
// challenge answers "unknown token" — a 401, a DENY — to a user who did
// nothing wrong, on a deployment that has merely been scaled out.
type MFAChallenge struct {
	// Token is what the proxy polls with. It is high-entropy and opaque.
	Token string
	// SubjectID and Login are who the challenge is for. Login is carried so
	// the audit trail reads as the user typed it.
	SubjectID string
	Login     string
	// Provider and ProviderRef are the provider's handle on the
	// out-of-band factor.
	Provider    string
	ProviderRef string
	// Prompt is shown to the user by the proxy, so it must be safe to
	// disclose.
	Prompt string
	// PollAfterMS is how long the proxy is asked to wait between polls.
	PollAfterMS int32
	// State is the closed set above.
	State MFAChallengeState
	// Polls is how many times it has been polled, which is what poll-rate
	// enforcement counts.
	Polls int
	// IssuedAt, ExpiresAt, LastPolledAt and ResolvedAt bound its life.
	// ResolvedAt non-zero is what makes the token single use.
	IssuedAt     time.Time
	ExpiresAt    time.Time
	LastPolledAt time.Time
	ResolvedAt   time.Time
}

// Live reports whether the challenge may still be polled at instant t.
func (c MFAChallenge) Live(t time.Time) bool {
	return c.State == MFAChallengePending && c.ResolvedAt.IsZero() && t.Before(c.ExpiresAt)
}

// HostKeyDecisionKind is the trust answer recorded for a target host key.
// Closed set, matching the contract's `decision` — the values are the wire's,
// and `internal/store` does not import `internal/contract` for the same reason
// the hop directions do not: a storage column that changes when a wire enum is
// renamed is a migration bought for nothing.
type HostKeyDecisionKind string

const (
	// HostKeyAccepted means the proxy may proceed.
	HostKeyAccepted HostKeyDecisionKind = "accept"
	// HostKeyRejected means it may not, and the report is a security event.
	HostKeyRejected HostKeyDecisionKind = "reject"
)

// TargetHostKey is one key a target has been seen presenting (proxy D7).
//
// The identity of a record is (hostname, port, fingerprint), which is exactly
// the shape the proxy keys reuse on. A target presenting a different key is a
// different record, which is what makes a changed key detectable without any
// bookkeeping on top: a first sighting for a target that already has one.
type TargetHostKey struct {
	Hostname string
	Port     int32
	// Fingerprint is OpenSSH's SHA256 form.
	Fingerprint string
	KeyType     string
	// Decision is what was decided, which is not necessarily what the
	// current rule would decide: the response said it, so the record keeps
	// it.
	Decision HostKeyDecisionKind
	// FirstSeenAt and LastSeenAt bound the sightings; FirstReportedBy and
	// LastReportedBy name the proxies that reported them.
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	FirstReportedBy string
	LastReportedBy  string
	// CacheKey is the opaque key this server issues for the decision about
	// this exact (hostname, port, fingerprint), and the key an operator
	// publishes to withdraw it.
	//
	// It is STORED rather than recomputed on demand because what a
	// withdrawal has to name is the key that was issued: a key recomputed
	// by a later revision of the scope would match nothing any proxy
	// holds, and the withdrawal would report success having dropped
	// nothing (PLAN §5.4, migration 0005). Empty means no key was recorded
	// for this sighting, which is what a row written before 0005 has.
	CacheKey string
}

// UIDLease is the audit record of one granted block.
//
// It is APPEND-ONLY and nothing reads it in order to allocate. 0001 said what
// it must not be: a row this layer can hand back. There is no release, no
// expiry sweep and no free list — a granted block is gone, whether it was
// used, abandoned, or allowed to expire.
type UIDLease struct {
	// LeaseID is what an incident resolves a uid back to.
	LeaseID string
	// TargetID is the cursor's key, so the lease and the cursor agree about
	// what a target is by construction.
	TargetID string
	// ProxyID is who it was granted to.
	ProxyID string
	// From and To are the half-open block [From, To).
	From int64
	To   int64
	// TermSeconds is how long that proxy was told it may keep allocating.
	// It is recorded for an incident, never acted on here.
	TermSeconds int32

	GrantedAt time.Time
}

// ProxyAPIToken is a proxy's channel credential (M2).
//
// Only the hash of the secret is stored. ProxyID EMPTY is a real state and
// means the token is not bound to one proxy: a bootstrap or harness credential
// that authenticates "a proxy of this tenant" and nothing narrower.
type ProxyAPIToken struct {
	TokenID string
	ProxyID string
	// TokenHash is the SHA-256 of the secret half.
	TokenHash []byte
	// Label is for an operator listing credentials.
	Label string

	IssuedAt  time.Time
	ExpiresAt time.Time
	RevokedAt time.Time
}

// Usable reports whether the token may authenticate a request at instant t.
func (t ProxyAPIToken) Usable(at time.Time) bool {
	if !t.RevokedAt.IsZero() {
		return false
	}
	return t.ExpiresAt.IsZero() || at.Before(t.ExpiresAt)
}
