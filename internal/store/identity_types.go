// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"encoding/json"
	"time"
)

// The rows federation, RBAC and the north-bound credential model are made of
// (0011). Semantics belong to `internal/identity`; what is here is shape.

// Group is a first-class group record.
//
// `subjects.groups` stays the decision path's read — one row, no join, on the
// hot path (M5) — and this is the authority those names are computed from. A
// rule must not be able to tell a local group from a mapped one, so Source is
// recorded for the audit trail and matched on by nothing.
type Group struct {
	// ID is the name policy matches on, and the name an operator types.
	ID string
	// DisplayName and Description are for operators.
	DisplayName string
	Description string
	// Source is "local" or the connector that asserted the group.
	Source string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// RoleBinding grants a role to a subject or to a group, inside one tenant.
//
// Exactly one of SubjectID and GroupID is set. A role granted in tenant A
// confers nothing in tenant B — including to an administrator — and that is
// the primary key rather than a filter somebody remembers to apply (M18).
type RoleBinding struct {
	// SubjectID is set when the binding is to a person or a machine.
	SubjectID string
	// GroupID is set when the binding is to a group.
	GroupID string
	// Role is the role's stable code. The role set itself is fixed in Go
	// (`internal/identity`), not in a table.
	Role string
	// GrantedBy names who made the binding, for the audit trail.
	GrantedBy string

	CreatedAt time.Time
}

// ConnectorKind is what protocol a federation connector speaks. Closed set: a
// kind this server cannot name is one it cannot broker (M13).
type ConnectorKind string

const (
	// ConnectorOIDC is OpenID Connect.
	ConnectorOIDC ConnectorKind = "oidc"
	// ConnectorSAML is SAML 2.0 web SSO.
	ConnectorSAML ConnectorKind = "saml"
)

// Connector is one tenant's federation configuration.
//
// Config holds everything EXCEPT the client secret, which is never a row: the
// document names the environment variable the secret is read from, so a
// database dump is not a set of federation credentials.
type Connector struct {
	// Name identifies the connector within the tenant and appears in a
	// subject's Source, so it is part of the audit trail.
	Name string
	// Kind is oidc or saml.
	Kind ConnectorKind
	// DisplayName is what a login page shows.
	DisplayName string
	// Enabled is whether logins through it are accepted.
	Enabled bool
	// Config is the broker's own configuration document.
	Config json.RawMessage

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ClaimMapping is one immutable version of a tenant's claim mapping.
//
// Versioned the way policy bundles are, and for the same reason: a decision
// record names the version it used, and that version has to still be readable
// years later (M7, M4).
type ClaimMapping struct {
	// Version is the monotonic version number a decision record names.
	Version int
	// Document is the mapping exactly as submitted. The digest covers these
	// bytes, so it is stored as text and never re-encoded.
	Document string
	// Digest is the SHA-256 of Document, hex-encoded.
	Digest string
	// Active reports whether this is the version logins use.
	Active bool
	// CreatedBy names who submitted it.
	CreatedBy string

	CreatedAt time.Time
}

// FederatedIdentity is the join between an IdP's subject and this server's.
//
// The key is (tenant, connector, external subject): a subject id is unique
// within a tenant and never globally, so two tenants whose IdPs both call
// someone `00u1` resolve to two different subjects (M18).
type FederatedIdentity struct {
	Connector       string
	ExternalSubject string
	SubjectID       string

	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// FlowState is one outstanding federation login.
//
// Rows rather than process memory, for the reason PLAN §6 gives about MFA
// challenges: nothing makes a browser's callback land on the node that started
// the flow. Single use, for the reason it gives about replay.
type FlowState struct {
	// State is the opaque value echoed by the IdP, and the row's key.
	State string
	// Connector and Kind name the broker the flow belongs to. A callback
	// presented to the wrong connector is refused rather than guessed at.
	Connector string
	Kind      ConnectorKind
	// Nonce and PKCEVerifier are OIDC's replay and code-interception
	// defences.
	Nonce        string
	PKCEVerifier string
	// RequestID is the SAML AuthnRequest id the Response must name in
	// InResponseTo.
	RequestID string
	// RedirectURI is where the browser is sent once the flow completes.
	RedirectURI string

	CreatedAt  time.Time
	ExpiresAt  time.Time
	ConsumedAt time.Time
}

// PrincipalKind is what sort of north-bound credential a principal is. Closed
// set (M13).
type PrincipalKind string

const (
	// PrincipalToken is a scoped API token: automation, CI, GitOps.
	PrincipalToken PrincipalKind = "token"
	// PrincipalSession is a logged-in human.
	PrincipalSession PrincipalKind = "session"
)

// NorthPrincipal is an authenticated north-bound caller as it is stored.
//
// Scopes is the set M18 calls "the tenants it may act in": a tenant named in
// a path, a query or a body is a SELECTOR within this set and never a widening
// of it. The map's value is the roles the principal holds IN THAT TENANT,
// because a role granted in tenant A confers nothing in tenant B.
type NorthPrincipal struct {
	// ID identifies the credential; it is safe to log.
	ID string
	// Kind is token or session.
	Kind PrincipalKind
	// SubjectID is the person behind a session; empty for a machine token.
	SubjectID string
	// DisplayName is for operators.
	DisplayName string
	// Source is "local" for the break-glass path, or the connector that
	// asserted the identity.
	Source string
	// BreakGlass is asserted at the moment the credential is minted and
	// never inferred afterwards. A break-glass login that looks like a
	// normal one is an audit failure (M7).
	BreakGlass bool
	// MappingVersion is the claim-mapping version that produced this
	// principal's attributes; zero when no mapping was involved.
	MappingVersion int
	// TokenDigest and SessionDigest are SHA-256 of the presented secret.
	// Exactly one is set, and neither can reconstruct the credential.
	TokenDigest   string
	SessionDigest string
	// Scopes maps tenant -> roles.
	Scopes map[string][]string

	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	RevokedAt  time.Time
}

// Live reports whether the principal may still be used at the given instant.
func (p NorthPrincipal) Live(now time.Time) bool {
	if !p.RevokedAt.IsZero() {
		return false
	}
	if !p.ExpiresAt.IsZero() && !now.Before(p.ExpiresAt) {
		return false
	}
	return true
}

// CAKey is one tenant's certificate-authority key as it is stored.
//
// Key material is per tenant from the first issuance: one tenant's targets
// must not trust another tenant's CA (M18, M7, proxy D6a).
type CAKey struct {
	// KeyID names the key. Rotation creates a NEW id rather than replacing
	// a key certificates are already chained to.
	KeyID string
	// Algorithm is the key type, as `ext.KeyAlgorithm.String()` renders it.
	Algorithm string
	// PublicKey is the SSH wire encoding of the public half.
	PublicKey []byte
	// PrivateRef names the custodian: "software", "pkcs11:slot-3",
	// "aws-kms:arn:...". It is recorded so that an auditor asking "where is
	// the CA key" has an answer that does not depend on asking an engineer.
	PrivateRef string
	// PrivateKey is AES-256-GCM ciphertext, and is written only by the
	// software custodian. A key store holding the private half elsewhere
	// leaves it nil.
	PrivateKey []byte
	// Active is whether new certificates are signed with this key. At most
	// one per tenant.
	Active bool
	// TrustedUntil is the rotation story in one field: a retired key stays
	// in the trust bundle until this instant, so certificates it already
	// signed keep working for their remaining life. A compromise rotation
	// sets it to the rotation instant, and the key leaves the bundle at
	// once. Zero on an active key.
	TrustedUntil time.Time
	// Comment is an operator's note about the rotation that created it.
	Comment string

	CreatedAt time.Time
	RetiredAt time.Time
}

// SSHCertificate is one issued target certificate.
//
// The row outlives the certificate on purpose: "which certificate was minted
// for which session, against which target, under which CA key" is the question
// an incident asks, and an expired certificate is exactly the one somebody is
// asking about.
type SSHCertificate struct {
	// Serial is the per-tenant monotonic serial.
	Serial int64
	// KeyID is the certificate's `key_id` field, which is what a target's
	// own log records.
	KeyID string
	// CAKeyID names the key that signed it.
	CAKeyID string
	// SubjectID is who it was minted for.
	SubjectID string
	// SessionID ties it to the session that needed it.
	SessionID string
	// Principals are the logins it is valid for: the account on the target,
	// and nothing else.
	Principals []string
	// Target, TargetPort and SourceAddress are the scope. SSH itself cannot
	// enforce a hostname, so issuance is the enforcement point and this row
	// is its record.
	Target        string
	TargetPort    int
	SourceAddress string

	ValidAfter  time.Time
	ValidBefore time.Time
	IssuedAt    time.Time

	RevokedAt     time.Time
	RevokedReason string

	// Certificate is the certificate as `ssh.MarshalAuthorizedKey` renders
	// it. There is no private key here and there never will be: the proxy
	// generates the key pair and this server signs the public half.
	Certificate string
}

// SoftwareKey is one key the default custodian holds (`ext.KeyStore`).
//
// It is separate from [CAKey] because that type is the CA's lifecycle — which
// key is active, which retired keys are still trusted — and this one is one
// custodian's material. An HSM-backed key store writes no row of this kind at
// all.
type SoftwareKey struct {
	// Name is the key's role plus its generation, e.g. `ssh-ca:a1b2c3`.
	Name string
	// Algorithm is as `ext.KeyAlgorithm.String()` renders it.
	Algorithm string
	// PublicKey is the SSH wire encoding of the public half.
	PublicKey []byte
	// PrivateKey is AES-256-GCM ciphertext. There is no code path that
	// writes a plaintext private key here.
	PrivateKey []byte

	CreatedAt time.Time
}
