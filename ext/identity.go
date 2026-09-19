// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"context"
	"crypto"
	"time"
)

// SyncedSubject is one identity as an external directory holds it.
type SyncedSubject struct {
	// Tenant is required.
	Tenant Tenant
	// ExternalID is the directory's stable identifier. It is the join key,
	// and it must not change when a person's name or address does.
	ExternalID string
	// Username is the login name.
	Username string
	// DisplayName and Email are what an operator recognises the person by.
	DisplayName string
	Email       string
	// Groups are the external group identifiers the subject belongs to.
	Groups []string
	// Active reports whether the directory considers the identity live.
	// De-provisioning sets this false rather than deleting, because a
	// deleted subject with audit history is a hole in the history.
	Active bool
	// Attributes are further directory fields, as codes and values.
	Attributes map[string]string
	// UpdatedAt is the directory's own modification time, used to resolve
	// out-of-order delivery.
	UpdatedAt time.Time
}

// SyncedGroup is one group as an external directory holds it.
type SyncedGroup struct {
	// Tenant is required.
	Tenant Tenant
	// ExternalID is the directory's stable identifier for the group.
	ExternalID string
	// Name is the group name.
	Name string
	// UpdatedAt is the directory's own modification time.
	UpdatedAt time.Time
}

// SyncSink is Control's side of identity provisioning: what an IdentitySync
// implementation calls to apply what it learned. The direction is inverted
// on purpose. Provisioning is push-shaped — SCIM is a server the directory
// calls — so the extension owns the listener and Control owns the effect, and
// an implementation never writes to Control's storage itself.
//
// Every method is idempotent on ExternalID: a directory that replays is
// normal, and a sink that doubles on replay is a bug in Control rather than in
// the directory.
type SyncSink interface {
	// UpsertSubject creates or updates an identity.
	UpsertSubject(ctx context.Context, s SyncedSubject) error
	// DeactivateSubject marks an identity as no longer live and revokes what
	// it currently holds. It does not delete: the audit history stays
	// attributable.
	DeactivateSubject(ctx context.Context, tenant Tenant, externalID, reasonCode string) error
	// UpsertGroup creates or updates a group.
	UpsertGroup(ctx context.Context, g SyncedGroup) error
	// DeleteGroup removes a group. Membership derived from it stops
	// applying; the subjects remain.
	DeleteGroup(ctx context.Context, tenant Tenant, externalID string) error
}

// IdentitySync provisions and de-provisions identities ahead of login. What
// varies is the protocol a customer's directory speaks and the shape of the
// mapping into it — SCIM is the common case, and it is not the only one.
//
// When no implementation is registered, identities arrive at login from the
// federated identity provider and nothing is provisioned ahead of time. That
// is a complete story: a subject exists here the first time they authenticate,
// their claims map to groups and roles, and access follows. What provisioning
// adds is the two things login cannot do — an identity that exists *before*
// anyone uses it, so access can be granted in advance, and de-provisioning that
// takes effect when someone leaves rather than when they next fail to log in.
// It is an addition, not core functionality moved behind a licence.
//
// Start is called once, after the registry is sealed and before the listeners
// open. It must return promptly: an implementation that needs to keep running
// starts its own goroutines and stops them on Stop. The context passed to Start
// is the server's lifetime.
type IdentitySync interface {
	// Start begins provisioning, applying what it learns through sink.
	Start(ctx context.Context, sink SyncSink) error
	// Stop ends it. It is called on shutdown and must be safe to call
	// without a preceding successful Start.
	Stop(ctx context.Context) error
}

// KeyAlgorithm names a key type. It is a closed enum: an algorithm Control
// cannot name is one it cannot ask a key store to produce.
type KeyAlgorithm int

const (
	// KeyAlgorithmEd25519 is the default for new signing keys.
	KeyAlgorithmEd25519 KeyAlgorithm = iota
	// KeyAlgorithmECDSAP256 is for peers that cannot do Ed25519.
	KeyAlgorithmECDSAP256
	// KeyAlgorithmRSA4096 is for the peers that can do neither.
	KeyAlgorithmRSA4096
)

// String renders the algorithm as the stable code that crosses the wire.
func (a KeyAlgorithm) String() string {
	switch a {
	case KeyAlgorithmEd25519:
		return "ed25519"
	case KeyAlgorithmECDSAP256:
		return "ecdsa-p256"
	case KeyAlgorithmRSA4096:
		return "rsa-4096"
	}
	return "ed25519"
}

// KeyRef names one key. Keys are per-tenant, because a certificate authority
// shared across tenants is a certificate authority that can sign for the wrong
// estate (M12).
type KeyRef struct {
	// Tenant owns the key.
	Tenant Tenant
	// Name is the key's role, as a stable code: "ssh-ca", "audit-chain".
	Name string
}

// KeyInfo describes a key without exposing it.
type KeyInfo struct {
	// Ref names the key.
	Ref KeyRef
	// Algorithm is what it is.
	Algorithm KeyAlgorithm
	// Public is the public half.
	Public crypto.PublicKey
	// CreatedAt is when it came into being.
	CreatedAt time.Time
	// Custodian describes where the private half lives, as an
	// operator-facing diagnostic: "software", "pkcs11:slot-3",
	// "aws-kms:arn:...". It is recorded so that an auditor asking "where is
	// the CA key" has an answer that does not depend on asking an engineer.
	Custodian string
}

// KeyStore holds the private keys this deployment signs with. What varies is
// custody: whether the private half may exist as bytes in this process at all.
// An organisation with an HSM requirement is not asking for different
// cryptography, it is asking for the same cryptography with the key somewhere
// this process cannot read it — so the seam is the signer, never the key.
//
// When no implementation is registered, Control holds its own software keys and
// signs with them. That is a working certificate authority and a working audit
// chain; it is what a self-hosted deployment runs, and it is not a downgrade
// waiting for a licence. What a key store adds is custody.
//
// Signer returns a crypto.Signer rather than a Sign method, because that is the
// interface the SSH and X.509 libraries already take: an HSM-backed key then
// works everywhere a software key does, with no second code path. The context
// on Signer bounds obtaining the signer; an implementation whose signing
// operation itself needs a deadline applies its own, since crypto.Signer has
// nowhere to carry one.
type KeyStore interface {
	// Generate creates a key at ref and returns what it made. Generating a
	// ref that already exists is an ErrConflict *Error — key rotation
	// creates a new name, it does not silently replace a key that
	// certificates are still chained to.
	Generate(ctx context.Context, ref KeyRef, algo KeyAlgorithm) (KeyInfo, error)
	// Describe returns a key's public half and provenance. An absent key is
	// an ErrNotFound *Error.
	Describe(ctx context.Context, ref KeyRef) (KeyInfo, error)
	// Signer returns a signer for the key's private half.
	Signer(ctx context.Context, ref KeyRef) (crypto.Signer, error)
	// List returns every key the tenant holds.
	List(ctx context.Context, tenant Tenant) ([]KeyInfo, error)
	// Destroy removes a key. It is irreversible, and Control calls it only
	// when an operator has said so explicitly.
	Destroy(ctx context.Context, ref KeyRef) error
}
