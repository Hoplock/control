// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"crypto/sha256"
	"encoding/base64"
	"maps"
	"slices"

	"github.com/hoplock/control/internal/store"
)

// ClaimChainHop names the proxy whose key authenticated a chain leg.
//
// The identity itself is the USER's, established here: a hop learns who is
// connecting from this server, never from the proxy in front of it (proxy
// PLAN §6.1, D11). The claim is the one thing the leg adds, and it is audit
// data — 0008 pairs it with `conn.hop_trail`, which is the only other view of
// a chain this server has.
//
// The name matches the proxy mock's `ClaimChainHop`, because the conformance
// suite grades against that behaviour.
const ClaimChainHop = "chain_hop_proxy_id"

// SourceLocal names the identity source of a subject held in this server's own
// store, as distinct from one a federated IdP asserted (M7). 0011 adds the
// brokers; until then every identity carries a source so the audit trail can
// say who decided.
const SourceLocal = "local"

// Identity is an authenticated principal, in this module's own vocabulary.
//
// It is deliberately NOT the contract's `Identity`: the wire shape belongs to
// `internal/contract` and the translation belongs to the HTTP layer (PLAN §3),
// so a contract revision lands in one package rather than in the identity
// resolver.
type Identity struct {
	// Subject is the stable identifier policy matches on.
	Subject string
	// Login is the SSH login as the user typed it, target segment already
	// stripped by the proxy (proxy D1).
	Login string
	// DisplayName is for operators, never for matching.
	DisplayName string
	// Source names where the identity came from: an IdP connector's name,
	// or SourceLocal.
	Source string
	// Principals are the login names this subject may present.
	Principals []string
	// Groups are what policy matches on (M3).
	Groups []string
	// Claims are what the source asserted, plus anything this server
	// established about the connection itself — ClaimChainHop being the one
	// that exists today.
	Claims map[string]string
}

// WithClaim returns a copy carrying one more claim.
//
// A copy rather than a mutation because an Identity is built from a stored
// subject and may be shared; a chain leg adding a claim to the row's map would
// put one connection's hop on every later answer.
func (i Identity) WithClaim(name, value string) Identity {
	out := i
	out.Claims = make(map[string]string, len(i.Claims)+1)
	maps.Copy(out.Claims, i.Claims)
	out.Claims[name] = value
	return out
}

// fromSubject converts a stored subject into an identity for one login.
func fromSubject(s store.Subject, login string) Identity {
	return Identity{
		Subject:     s.ID,
		Login:       login,
		DisplayName: s.DisplayName,
		Source:      sourceOf(s),
		Principals:  slices.Clone(s.Principals),
		Groups:      slices.Clone(s.Groups),
		Claims:      maps.Clone(s.Claims),
	}
}

// sourceOf answers what a stored subject's source is, never empty.
//
// The conformance suite asserts an identity names one, and the reason is worth
// keeping: an identity with no source is one the audit trail cannot attribute,
// and "" is what a half-seeded row produces.
func sourceOf(s store.Subject) string {
	if s.Source == "" {
		return SourceLocal
	}
	return s.Source
}

// KeyFingerprint renders an SSH key blob as OpenSSH does: `SHA256:` followed
// by the unpadded standard base64 of the key's SHA-256.
//
// It is the string `ssh-keygen -lf` prints, the string the proxy relays in
// `public_key.fingerprint`, and — critically — the same expression the
// `proxies.key_fingerprint` column is generated with, so the chain-leg lookup
// compares like with like. Changing one without the other would make every
// chain leg a 401.
func KeyFingerprint(blob []byte) string {
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}
