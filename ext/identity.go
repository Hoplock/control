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
	// and it must not change when a person's name or address does. It is
	// unique per tenant across everything the one registered sync provisions:
	// a sync that feeds several directories into one sink namespaces their ids
	// itself, because two directories that both call someone `00u1` are two
	// people here only if their ids differ.
	ExternalID string
	// Username is the login name, and the subject's only one: it replaces the
	// name an earlier upsert gave, and a name another subject in the tenant
	// already presents is refused (KindConflict). Optional.
	Username string
	// DisplayName and Email are what an operator recognises the person by.
	// Email is also offered to the tenant's claim mapping as the claim
	// `email`.
	DisplayName string
	Email       string
	// Groups are the directory's group ExternalIDs, not names: each resolves
	// to the Name its UpsertGroup gave, and a group the sink has no name for
	// yet maps nothing until it does.
	Groups []string
	// Active reports whether the directory considers the identity live.
	// De-provisioning sets this false rather than deleting, because a
	// deleted subject with audit history is a hole in the history. An upsert
	// with Active false is a deactivation, with exactly DeactivateSubject's
	// effect.
	Active bool
	// Attributes are further directory fields, as codes and values. They are
	// offered to the tenant's claim mapping as claims, and only what it names
	// becomes a policy attribute. A code Control sets for itself
	// (`chain_hop_proxy_id`, anything under `hoplock.`) is dropped.
	Attributes map[string]string
	// UpdatedAt is the directory's own modification time, used to resolve
	// out-of-order delivery. It is required, and compared to the microsecond.
	UpdatedAt time.Time
}

// SyncedGroup is one group as an external directory holds it.
type SyncedGroup struct {
	// Tenant is required.
	Tenant Tenant
	// ExternalID is the directory's stable identifier for the group: what
	// SyncedSubject.Groups names.
	ExternalID string
	// Name is the group name, and what the tenant's claim mapping matches
	// (`groups.map`), so one mapping serves a login and the directory alike.
	// A renamed group maps nothing until the mapping names the new name: a
	// rename narrows access and never widens it. Required.
	Name string
	// UpdatedAt is the directory's own modification time. It is required.
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
// the directory. Control's sink is safe for concurrent use: two calls on one
// ExternalID are serialised, calls on different ids run concurrently, and a
// group change waits for the subject writes in flight in its tenant.
//
// ORDER. An upsert is applied only when its UpdatedAt is later than the last
// one applied for the id; one that is not changes nothing and writes no
// record, which is what a replay or a stale write is. DeactivateSubject
// carries no time, so Control's own clock orders it: an upsert with Active
// true lifts a standing deactivation only when its UpdatedAt is later than
// both the last applied one and the instant Control applied the
// deactivation. A directory clock behind Control's delays a re-activation and
// never shortens a deactivation. A deactivation for an ExternalID Control has
// not seen is kept, and the create that arrives later provisions the subject
// already deactivated; a DeleteGroup for an unknown id keeps a tombstone the
// same way, and an UpsertGroup older than a tombstone changes nothing.
//
// The directory is the only writer of what it provisions. A provisioned
// subject's groups, attributes and login name come from here, and an
// operator's write of the subject is refused. What the directory holds reaches
// policy only through the tenant's claim mapping (Control's decision M7):
// attributes and the email are offered as claims and group names under the
// mapping's group claim, and only what the mapping names counts. A connector
// may name a `directory_claim`, the claim carrying this ExternalID, and a login
// through it reaches the provisioned subject or is refused; it never creates a
// subject of its own. An existing subject is never adopted or merged.
//
// Every applied call is recorded on the tenant's control stream with the sync
// as the actor (`identity-sync:<provider>`, no person), and so is every
// refusal.
//
// ERRORS. Every method returns an *Error, or nil for something done or found
// to be already so:
//   - KindInvalid: a field is malformed (the error names it); nothing was
//     applied. Do not retry it unchanged.
//   - KindConflict: the Username is one another subject in the tenant already
//     presents; nothing was applied. Resolve it in the directory, or have an
//     operator resolve it here.
//   - KindUnavailable: the change was applied and stands, and what it owes the
//     revocation stream (a kill, an invalidation, a revocation) could not be
//     delivered; or Control is stopping the sync. Safe to retry: the same call
//     delivers the rest and writes no second record.
//   - KindInternal: a store failure. Safe to retry.
type SyncSink interface {
	// UpsertSubject creates or updates an identity, joined on ExternalID and
	// never on the username.
	UpsertSubject(ctx context.Context, s SyncedSubject) error
	// DeactivateSubject marks an identity as no longer live and revokes what
	// it currently holds. It does not delete: the audit history stays
	// attributable.
	//
	// From the instant it commits, every authorize for the subject is a deny
	// (`subject_deactivated`) whatever policy, grants or windows say, and only
	// the directory lifts it. Then, through the services an operator's levers
	// call and with the sync as the actor: its north-bound sessions and API
	// tokens are revoked and its outstanding SSH certificates marked revoked
	// (in the same transaction); every live or scheduled grant is revoked,
	// which ends the sessions each backed; every pending grant request is
	// withdrawn; and its cached decisions are dropped and its sessions ended.
	// The holder is shown Control's text ("Your account was deactivated by
	// your organisation's directory."), never reasonCode, which is a code and
	// is recorded. A deactivation already standing writes no second record
	// and still revokes whatever remains and ends the sessions again, so a
	// retry after KindUnavailable finishes it. A re-activation restores
	// nothing that was revoked.
	DeactivateSubject(ctx context.Context, tenant Tenant, externalID, reasonCode string) error
	// UpsertGroup creates or updates a group. One that names a group anew,
	// renames it, or re-creates it after a delete re-derives every subject
	// the directory says is a member.
	UpsertGroup(ctx context.Context, g SyncedGroup) error
	// DeleteGroup removes a group. Membership derived from it stops
	// applying, and its members' cached decisions are dropped; the subjects
	// remain.
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
//
// A Start that returns an error, panics or does not return within 30 seconds is
// a broken registration: Control logs it at ERROR with the provider and the
// error's kind, serves, lists the sync as registered and not started, and never
// calls Start again. Access must not depend on somebody else's software. A
// directory that is merely unreachable is not a Start error: return nil and
// reconnect on your own goroutines. Stop is called on shutdown, before the
// listeners drain and bounded by the shutdown deadline, and a sink call made
// once it has begun is KindUnavailable.
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
