// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/hoplock/control/internal/store"
)

// enrollmentTokenSecretBytes is how much entropy an enrollment token carries.
const enrollmentTokenSecretBytes = 32

// tokenSeparator splits a token's tenant from its secret. It is a character
// that cannot appear in the base64url alphabet, so a secret can never be
// mistaken for part of a tenant name.
const tokenSeparator = "."

// EnrollmentToken is the credential a proxy presents to enroll.
//
// It carries its own tenant, and that is the whole design: SOUTH-BOUND TENANCY IS
// RESOLVED FROM THE ENROLMENT CREDENTIAL (M18), and the credential is something
// this server minted and handed to an operator — not something the proxy asserts.
// The tenant in it is a SELECTOR within what the credential is good for, exactly
// as a north-bound token's tenant is a selector inside its scope, never a
// widening of it.
//
// Two consequences worth stating, because the alternative designs are tempting:
//
//   - **Nothing looks a token up across tenants.** The tenant is parsed from the
//     credential and the secret is then verified against the one row under
//     (tenant, proxy_id). Every repository method still names its tenant (M18),
//     so there is no method that could express a cross-tenant read even by
//     mistake. A forged tenant prefix simply fails the hash comparison in a
//     tenant where no such grant exists.
//   - **The contract grows no tenant field.** A proxy asserting its own tenancy
//     would be a caller asserting its own authority. This is why multi-tenancy
//     needs no change to `hoplock/proxy` at all, which is the strongest evidence
//     the seam is in the right place.
type EnrollmentToken struct {
	// Tenant is the tenant the grant lives in.
	Tenant store.Tenant
	// Secret is the high-entropy half. It is never stored — only its SHA-256
	// is — so a dump of the enrollment table cannot enroll anything.
	Secret string
}

// String renders the token in the form an operator hands to a proxy.
//
// It is deliberately NOT a fmt.Stringer-shaped redaction: this value exists to be
// transported once, at issuance, and a String that hid the secret would make the
// issuing path silently useless. What must never be logged is the token, and that
// is a rule about callers, stated on [MintEnrollmentToken].
func (t EnrollmentToken) String() string {
	return string(t.Tenant) + tokenSeparator + t.Secret
}

// Hash is what the grant stores.
func (t EnrollmentToken) Hash() []byte {
	sum := sha256.Sum256([]byte(t.Secret))
	return sum[:]
}

// MintEnrollmentToken issues a token for a tenant.
//
// The caller shows the result to the operator ONCE and stores only [Hash]. It
// must not be logged, echoed in an error, or written to an audit record: an audit
// record of a credential is a credential in the audit store, which is the one
// place in this system designed never to forget anything.
func MintEnrollmentToken(tenant store.Tenant) (EnrollmentToken, error) {
	if tenant == "" {
		return EnrollmentToken{}, fmt.Errorf("fleet.MintEnrollmentToken: tenant is required")
	}
	if strings.Contains(string(tenant), tokenSeparator) {
		// A tenant containing the separator would make the token ambiguous,
		// and an ambiguous credential is one that can be made to resolve to
		// the wrong tenant. Refuse at issuance rather than at verification.
		return EnrollmentToken{}, fmt.Errorf(
			"fleet.MintEnrollmentToken: tenant %q contains %q, which an enrollment token cannot carry",
			tenant, tokenSeparator)
	}

	buf := make([]byte, enrollmentTokenSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return EnrollmentToken{}, fmt.Errorf("fleet.MintEnrollmentToken: %w", err)
	}
	return EnrollmentToken{
		Tenant: tenant,
		Secret: base64.RawURLEncoding.EncodeToString(buf),
	}, nil
}

// ParseEnrollmentToken reads a presented token.
//
// It resolves the tenant and nothing else. A malformed token is refused as
// [ErrEnrollmentRejected] rather than as a parse error, because "this is not
// shaped like a token" and "this token's secret is wrong" are the same fact to a
// caller and telling them apart would make enrollment an oracle.
func ParseEnrollmentToken(presented string) (EnrollmentToken, error) {
	tenant, secret, ok := strings.Cut(presented, tokenSeparator)
	if !ok || tenant == "" || secret == "" {
		return EnrollmentToken{}, ErrEnrollmentRejected
	}
	return EnrollmentToken{Tenant: store.Tenant(tenant), Secret: secret}, nil
}

// EnrollmentGrant is what an operator pre-registers: the proxy id, the zones it
// may claim, and the token it must present.
type EnrollmentGrant struct {
	// ProxyID is the id this grant is for.
	ProxyID string
	// GrantedZones are the zones the proxy may claim. EMPTY GRANTS NOTHING —
	// the fail-safe reading of a row somebody half-filled in.
	GrantedZones []Zone
	// CreatedBy names the operator.
	CreatedBy string
}

// EnrollmentRequest is what a proxy presents.
type EnrollmentRequest struct {
	// Token is the credential, as presented. It resolves the tenant.
	Token string
	// ProxyID is the id the proxy claims.
	ProxyID string
	// Zone is the zone it claims. Checked against the grant.
	Zone Zone
	// PublicKey is the key it enrolls with.
	PublicKey []byte
	// ContractVersion is the policy vocabulary this build implements. Stored as
	// a fleet-readiness signal; never the authority on what a connection may
	// be answered with (that is `policy_version` on each authorize request).
	ContractVersion int
	// Capabilities is what this build declares it can provide (M17).
	Capabilities Capabilities
	// Edges is the reachability it declares. It is the WHOLE set: an edge it
	// stops declaring is removed rather than left routable.
	Edges []Edge
}

// checkGrant is the enforcement half of "enrollment is an administrative act".
//
// It answers four separate refusals rather than one, because they are four
// different operator problems — no grant issued, token already used, token
// expired, zone not granted — and an enrollment that fails for an unsaid reason
// is one somebody debugs by retrying.
func checkGrant(grant store.ProxyEnrollment, req EnrollmentRequest, token EnrollmentToken, now nowFunc) error {
	if subtle.ConstantTimeCompare(grant.TokenHash, token.Hash()) != 1 {
		return ErrEnrollmentRejected
	}
	if !grant.ConsumedAt.IsZero() {
		return ErrEnrollmentSpent
	}
	if !grant.ExpiresAt.IsZero() && now().After(grant.ExpiresAt) {
		return ErrEnrollmentExpired
	}
	if !slices.Contains(grant.GrantedZones, string(req.Zone)) {
		// THE REFUSAL THIS EXISTS FOR. A proxy that could claim any zone could
		// insert itself as a hop into other people's routes, and every session
		// crossing that zone would traverse kit nobody authorised.
		return fmt.Errorf("%w: %q claims zone %q, granted %v",
			ErrZoneNotGranted, req.ProxyID, req.Zone, grant.GrantedZones)
	}
	return nil
}

// errNoGrant maps the store's absence onto this package's vocabulary.
func errNoGrant(err error) error {
	if store.IsNotFound(err) {
		return ErrEnrollmentUnknown
	}
	return err
}

// asEnrollmentFailure reports whether err means "this enrollment was refused on
// purpose", as distinct from "this server could not tell".
//
// The distinction is M11 one layer down: a refused enrollment is a decision, and
// a database timeout during an enrollment is an outage. Folding the two together
// here would have an operator reading "not granted" during a Postgres failover.
func asEnrollmentFailure(err error) bool {
	return errors.Is(err, ErrEnrollmentUnknown) ||
		errors.Is(err, ErrEnrollmentSpent) ||
		errors.Is(err, ErrEnrollmentExpired) ||
		errors.Is(err, ErrEnrollmentRejected) ||
		errors.Is(err, ErrZoneNotGranted)
}
