// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"

	"github.com/hoplock/control/internal/store"
)

// The local password verifier.
//
// It is PBKDF2-HMAC-SHA256 from the standard library, which is a deliberate
// choice rather than the best available one. A memory-hard KDF (argon2id,
// scrypt) resists offline cracking better and would be the right answer for a
// product whose primary credential is a password — but this one's is not:
// passwords here are the fallback the proxy tries only after certificate
// authentication was not accepted, and 0011 replaces this path with an IdP
// broker, at which point no password digest exists in this database at all.
// Buying a dependency for a table that is scheduled to empty is the wrong
// trade; buying a salted, iterated, constant-time verifier from the standard
// library is the right one.
//
// If that ordering ever changes — if a deployment means to keep local
// passwords as a first-class credential — the `algorithm` column is how a
// second KDF lands beside this one: verify with what a row was written with,
// re-hash on the next successful authentication.

const (
	// AlgorithmPBKDF2SHA256 names the one KDF this phase writes.
	AlgorithmPBKDF2SHA256 = "pbkdf2-sha256"
	// DefaultPBKDF2Iterations is the work factor NEW digests are written
	// with. Raising it applies to the next write, because a stored digest
	// is always verified with the parameters stored beside it.
	DefaultPBKDF2Iterations = 600_000
	// pbkdf2SaltBytes and pbkdf2KeyBytes size the salt and derived key.
	pbkdf2SaltBytes = 16
	pbkdf2KeyBytes  = 32
)

// HashPassword derives a storable verifier for a subject.
//
// The plaintext is used and dropped. It is never returned, logged, or put in
// an error: the one place a password exists is in transit (PLAN §7), and an
// error message is a place it would survive an incident in.
func HashPassword(subjectID, password string) (store.PasswordDigest, error) {
	salt := make([]byte, pbkdf2SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return store.PasswordDigest{}, fmt.Errorf("identity.HashPassword: %w", err)
	}
	digest, err := pbkdf2.Key(sha256.New, password, salt, DefaultPBKDF2Iterations, pbkdf2KeyBytes)
	if err != nil {
		// The error names the KDF and the subject, never the input.
		return store.PasswordDigest{}, fmt.Errorf("identity.HashPassword: %s for %q: %w",
			AlgorithmPBKDF2SHA256, subjectID, err)
	}
	return store.PasswordDigest{
		SubjectID:  subjectID,
		Algorithm:  AlgorithmPBKDF2SHA256,
		Iterations: DefaultPBKDF2Iterations,
		Salt:       salt,
		Digest:     digest,
	}, nil
}

// verifyPassword reports whether password matches the stored digest.
//
// The comparison is constant-time, and a digest written with an algorithm this
// build does not implement is an ERROR rather than a mismatch: "I cannot check
// this" is an outage, and answering `false` would turn an operator's
// half-finished migration into a fleet of users being told access was denied
// (M11).
func verifyPassword(d store.PasswordDigest, password string) (bool, error) {
	switch d.Algorithm {
	case AlgorithmPBKDF2SHA256:
		got, err := pbkdf2.Key(sha256.New, password, d.Salt, d.Iterations, len(d.Digest))
		if err != nil {
			return false, fmt.Errorf("identity: verify %q: %w", d.SubjectID, err)
		}
		return subtle.ConstantTimeCompare(got, d.Digest) == 1, nil
	default:
		return false, fmt.Errorf("identity: subject %q has a password digest written with %q, "+
			"which this build cannot verify", d.SubjectID, d.Algorithm)
	}
}
