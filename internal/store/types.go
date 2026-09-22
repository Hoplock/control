// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"encoding/json"
	"time"
)

// Tenant names the tenant a call operates in.
//
// It is a named type rather than a string, and it is an ARGUMENT rather than a
// field on the store, because M18 makes tenancy a dimension the caller selects
// and never a constant the process is configured with. Every repository method
// takes one as its second parameter, after the context — a signature that
// cannot express a cross-tenant read is worth far more than a convention a
// reviewer has to notice, and `TestRepositoryMethodsTakeATenant` fails the
// build if a method is ever added without one.
//
// Where the tenant comes from is a decision of the surface, never of the
// caller: north-bound it is resolved from the authenticated principal's scope,
// south-bound from the proxy's enrolled identity, and the wire contract carries
// no tenant field at all (M18).
type Tenant string

// String renders the tenant. It is safe in logs: a tenant id is not a secret.
func (t Tenant) String() string { return string(t) }

// Subject is who is asking. The IdP integration (0011) extends this; what is
// here is enough to answer an auth call.
type Subject struct {
	// ID is the subject's stable identifier within the tenant.
	ID string
	// Source names where the subject came from: an IdP connector's name, or
	// "local" for the development and break-glass path (M7).
	Source string
	// DisplayName is for operators, never for matching.
	DisplayName string
	// Principals are the login names this subject may present.
	Principals []string
	// Groups are the groups policy matches on (M3).
	Groups []string
	// Claims are the MAPPED policy attributes, not raw IdP claims: 0011
	// applies the tenant's versioned claim mapping before this row is
	// written, so nothing an IdP asserted reaches policy without having
	// been named in that mapping (M7).
	Claims map[string]string
	// BreakGlass reports that this subject's local credential is a
	// break-glass one. It is asserted by whoever creates the subject and is
	// carried into every decision and audit record the subject touches: a
	// break-glass login that looks like a normal one is an audit failure
	// (M7). It is never inferred from Source, because "local" will one day
	// mean something else.
	BreakGlass bool
	// MappingVersion is the claim-mapping version that produced Claims and
	// the mapped half of Groups; zero when no mapping was involved (a local
	// subject, or a tenant that federates with nobody). A decision record
	// names it (M4), so it is read on the hot path — which is why it is a
	// column on this row rather than a join.
	MappingVersion int
	// CreatedAt and UpdatedAt are set by the database.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Target is what is being reached. 0006 owns registration; 0008 routes to it.
type Target struct {
	// ID is the target's stable identifier within the tenant.
	ID string
	// Hostname is what the user typed, and the key the decision path looks
	// the target up by.
	Hostname string
	// Zone places the target in the fleet graph (M6).
	Zone string
	// Labels are what policy matches on (M3). The keys are open: policy
	// authors invent them.
	Labels map[string]string
	// CredentialMethod is a hint recording what the target is known to
	// accept. The authoritative method is the one policy emits on the
	// authorize response.
	CredentialMethod string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// EnrollmentState is a proxy's position in the enrollment lifecycle. Closed
// set: a state the server does not recognise is not one it can act on. 0006
// owns the transitions between them.
type EnrollmentState string

const (
	// EnrollmentPending means the proxy has presented itself and has not
	// been accepted.
	EnrollmentPending EnrollmentState = "pending"
	// EnrollmentEnrolled means the proxy is a member of the fleet.
	EnrollmentEnrolled EnrollmentState = "enrolled"
	// EnrollmentRevoked means the proxy was a member and is not any more.
	EnrollmentRevoked EnrollmentState = "revoked"
)

// Proxy is one member of the fleet. 0006 owns the semantics; the row lands
// here because the decision path reads it (M6).
type Proxy struct {
	// ID is the proxy's identifier within the tenant.
	ID string
	// Zone is the zone it declares.
	Zone string
	// PublicKey is the key it enrolled with.
	PublicKey []byte
	// State is its enrollment state.
	State EnrollmentState
	// LastHeartbeatAt is zero when the proxy has never reported.
	LastHeartbeatAt time.Time
	// ContractVersion is the policy vocabulary the proxy declared at
	// enrollment. It is a FLEET-READINESS signal — "can this zone be routed
	// through yet" — and never the authority on what a given connection may
	// be answered with: the proxy declares that per call in `policy_version`
	// on the authorize request, and 0008 answers within that. A stored value
	// can be stale; a request field cannot.
	ContractVersion int
	// DeclaredCapabilities is what this proxy's BUILD says it can provide
	// (M17), stored as the proxy sent it. The device-field namespace is open
	// and the contract enumerates no names, so a registry validating against
	// a list of its own would reject exactly the customer-written driver
	// proxy D13 makes first-class. 0006 owns the shape.
	DeclaredCapabilities json.RawMessage
	// SessionCount, LastError and LastErrorAt are health, as an operator
	// needs them during an incident (0016 renders them).
	SessionCount int
	LastError    string
	LastErrorAt  time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// PolicyBundle is one version of a tenant's policy source. Rows are immutable
// except for Active: an explanation (M4) points at a bundle by version, and a
// bundle that can be edited in place makes every historical explanation a
// guess.
type PolicyBundle struct {
	// Version is monotonic within the tenant.
	Version int64
	// Source is the bundle document exactly as uploaded.
	Source []byte
	// Hash is the digest of Source, computed by the caller (0014) so that
	// the value stored is the one the uploader was told.
	Hash string
	// UploadedBy names the principal that uploaded it.
	UploadedBy string
	// UploadedAt is set by the database.
	UploadedAt time.Time
	// Active marks the one bundle currently served for this tenant. At most
	// one row per tenant may carry it, enforced by a partial unique index.
	Active bool
}

// Decision is the decision record (M4): what an authorize call saw, what it
// matched, and what it answered. 0008 writes these.
type Decision struct {
	// ID is the decision_id the contract already carries, and the id an
	// operator resolves a user's "access denied, session <id>" into.
	ID string
	// SubjectID and TargetID name what the decision was about.
	SubjectID string
	TargetID  string
	// InputsDigest fixes the inputs the evaluation saw, so a simulation can
	// say whether it is replaying the same question.
	InputsDigest string
	// Inputs is the whole question, as a document: subject, target, context
	// — INCLUDING the hop trail the decision was taken under — and the live
	// grants. The digest beside it is an identity for the same thing and
	// not a substitute: "which hop asked, and what had it already been
	// through" is unrecoverable afterwards (PLAN §5.3).
	Inputs json.RawMessage
	// Explanation is the engine's own account of the answer: effect, basis,
	// the terms that matched, the deny reason, the bundle digest.
	Explanation json.RawMessage
	// Effect is `allow` or `deny`. A denial carries no snapshot, so without
	// it a deny and a snapshot that failed to serialise read alike — and
	// the deny path is the one an operator arrives on (M4).
	Effect string
	// ProxyID is the hop that asked, and SessionID the session it asked
	// for. The user is told a session id, so that is the id an operator
	// starts from.
	ProxyID   string
	SessionID string
	// MatchedRule names the rule that produced the answer. Empty means no
	// rule matched, which is itself an explanation.
	MatchedRule string
	// Obligations are what the decision emitted (record, approve, step up).
	Obligations []string
	// Snapshot is the response returned to the proxy, verbatim.
	Snapshot json.RawMessage
	// DecidedAt is set by the database unless the caller supplies it.
	DecidedAt time.Time
}

// AuditRecord is one row of the append-only audit store (M8).
//
// `Body` is the record; everything beside it is a derived index. The chain
// hash covers `Body`'s bytes exactly, so a verifier re-hashes the stored text
// and consults nothing else — see migration 0006 for why that forces text
// rather than jsonb.
type AuditRecord struct {
	// RecordID is assigned by the CLIENT and is the idempotency key: a
	// proxy draining its disk buffer after an outage resends, and the
	// second insert is refused by the database rather than deduplicated in
	// Go.
	RecordID string
	// Stream names the chain this record belongs to. Chains are per tenant
	// per stream (M8, M18) so that a tenant's history verifies on its own
	// and can be exported without the rest.
	Stream string
	// ChainSeq is this record's position in its chain, starting at 1.
	ChainSeq int64
	// PrevHash and Hash are the chain fields. PrevHash is empty at
	// ChainSeq 1 and is the predecessor's Hash everywhere else.
	PrevHash string
	Hash     string
	// SessionID ties records to a session, and is empty for records that
	// belong to no session.
	SessionID string
	// Kind and Severity are the record's classification, carried through
	// from the contract's log record.
	Kind     string
	Severity string
	// Body is the canonical JSON this server hashed, stored verbatim.
	Body string
	// Subject, Login, Target and Message are the record's own fields,
	// lifted out of Body so they can be filtered on.
	Subject string
	Login   string
	Target  string
	Message string
	// Event is the producer's own event name. It is what identifies the
	// ephemeral-account mapping event and the device configuration-change
	// event, both of which share a Kind with ordinary traffic.
	Event string
	// DecisionID joins to the decision that permitted the access (M4), and
	// ProxyID names the enrolled proxy that ingested the record — which is
	// where the tenant came from (M18), never the body.
	DecisionID string
	ProxyID    string
	// Attributes is the record's whole attribute map.
	Attributes map[string]string
	// DeviceFields are the `device_field.<name>` values the account was
	// provisioned with (proxy D13). Open, opaque, and never credential
	// material.
	DeviceFields map[string]string
	// Enforcement is the rung IN FORCE, never the rung policy asked for.
	Enforcement EnforcementFacts
	// TargetAuthMethod is the ladder entry that was satisfied, and
	// TargetAuthRung its 0-based index into the ladder. Nil means the
	// record stated no rung, which is not the same fact as rung 0.
	TargetAuthMethod string
	TargetAuthRung   *int32
	// AlgorithmProfile is the profile the proxy→target leg ran under.
	// Anything but `default` is a deliberate weakening.
	AlgorithmProfile string
	// Grant is the external grant context the session ran under (M16).
	Grant GrantContextFacts
	// CaptureBytes and CaptureSHA256 describe the session capture stored in
	// audit_captures, without carrying it.
	CaptureBytes  int32
	CaptureSHA256 string
	// RecordedAt is when the client says the event happened; ReceivedAt is
	// when this server stored it. They differ by however long the proxy was
	// buffering, which is exactly the gap an auditor wants to see.
	RecordedAt time.Time
	ReceivedAt time.Time
}

// EnforcementFacts is the enforcement rung a session actually ran on.
//
// Verified is a POINTER because nil and false are different facts: nil is "the
// record stated no rung", false is "a rung that this system verified nothing
// about" — an attested one, where the target enforces something already. A
// schema that collapsed them would turn an unverified claim into an apparent
// guarantee, which is the liability the contract's attribution rule exists to
// avoid.
type EnforcementFacts struct {
	Execution  string
	Reach      string
	Verified   *bool
	AttestedBy string
}

// Stated reports whether the record said anything about a rung at all.
func (e EnforcementFacts) Stated() bool {
	return e.Execution != "" || e.Reach != "" || e.Verified != nil || e.AttestedBy != ""
}

// GrantContextFacts is why access was granted, as an external system asserted
// it (M16). It is carried verbatim and never parsed into policy.
type GrantContextFacts struct {
	System      string
	Reference   string
	WindowStart *time.Time
	WindowEnd   *time.Time
	// AdditionalKind is `string`, `object`, or empty when there is none.
	// Additional is the text that arrived, neither coerced into the other
	// shape nor re-rendered.
	AdditionalKind string
	Additional     string
}

// Stated reports whether the record carried any grant context.
func (g GrantContextFacts) Stated() bool {
	return g.System != "" || g.Reference != "" || g.WindowStart != nil ||
		g.WindowEnd != nil || g.AdditionalKind != ""
}

// AdditionalContextKind values for GrantContextFacts.AdditionalKind.
const (
	AdditionalContextString = "string"
	AdditionalContextObject = "object"
)

// GrantOrigin says how a grant came to exist. All three are the same object to
// the decision engine — that is the point of M10 — but "explain why" that
// cannot name where the access came from is not an explanation.
type GrantOrigin string

const (
	// GrantOriginManual is an administrator creating a grant by hand
	// (0012).
	GrantOriginManual GrantOrigin = "manual"
	// GrantOriginWorkflow is an approval workflow producing one
	// (Hoplock Enterprise E8).
	GrantOriginWorkflow GrantOrigin = "workflow"
	// GrantOriginExternal is an external system asserting a window that a
	// provider confirmed (M16).
	GrantOriginExternal GrantOrigin = "external"
)

// Grant is a JIT access grant (M10), read by the decision engine as one more
// policy input rather than as a special case that bypasses it.
type Grant struct {
	// ID identifies the grant within the tenant.
	ID string
	// SubjectID is who the grant is for.
	SubjectID string
	// Scope is what it covers. 0012 owns its shape; this layer stores it.
	Scope string
	// NotBefore and ExpiresAt bound the window. ExpiresAt is strictly after
	// NotBefore, enforced by the schema.
	NotBefore time.Time
	ExpiresAt time.Time
	// Origin says which of the three ways produced it.
	Origin GrantOrigin
	// ApprovalRef points at the approval that produced it, where one did.
	ApprovalRef string
	// ExternalRef is the ticket, scan, or incident an external assertion
	// named (M16).
	ExternalRef string
	// RevokedAt is set when the grant was withdrawn before its expiry. A
	// revoked grant is never a decision input again.
	RevokedAt time.Time

	CreatedAt time.Time
}

// Live reports whether the grant is a decision input at instant t.
func (g Grant) Live(t time.Time) bool {
	if !g.RevokedAt.IsZero() {
		return false
	}
	return !t.Before(g.NotBefore) && t.Before(g.ExpiresAt)
}

// UIDCursor is the per-target allocation cursor (PLAN §4). The whole storage
// requirement for uid non-reuse is one integer per target, and its only legal
// movement is upward.
type UIDCursor struct {
	// TargetID is the target this cursor allocates for.
	TargetID string
	// NextUID is the lowest uid never yet handed out.
	NextUID int64
	// RangeEnd is one past the highest uid this target may allocate. A
	// cursor that has reached it answers 409 — outage-class to the proxy,
	// and the remedy is the operator's.
	RangeEnd int64

	UpdatedAt time.Time
}

// Remaining reports how many uids the cursor can still hand out.
func (c UIDCursor) Remaining() int64 { return c.RangeEnd - c.NextUID }

// UIDBlock is an exclusive half-open block of uids, [From, To).
//
// There is no expiry and no release path on purpose: a block is gone once
// granted, whether it was used, abandoned, or allowed to expire. A server that
// recycled an unused block to save uids would silently break the one guarantee
// the endpoint exists for.
type UIDBlock struct {
	From int64
	To   int64
}

// Size reports how many uids the block covers.
func (b UIDBlock) Size() int64 { return b.To - b.From }
