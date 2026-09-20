// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

// The repository surface, in one place so it can be read in one sitting.
//
// Two shapes hold for every method below, and a test enforces each:
//
//   - the first parameter is a context.Context, and the second is a Tenant.
//     There is no method that can express a cross-tenant read, because there
//     is no method that does not name the tenant it reads in (M18).
//   - absence and failure are different errors. IsNotFound(err) is true only
//     when the row is genuinely not there; a timeout, a closed pool or a
//     syntax error all answer false, because the only safe reading of any of
//     them is "we do not know" (M11).
//
// Behaviour belongs to later phases. What is here is tables and access: 0005
// owns policy semantics, 0006 the fleet, 0010 the audit chain, 0012 the grant
// workflow.

// SubjectRepository stores who is asking (M7).
type SubjectRepository interface {
	// Get returns a subject by id. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, subjectID string) (Subject, error)
	// GetByPrincipal returns the subject that may present login. Absent is
	// ErrNotFound; two subjects sharing a principal within one tenant is a
	// configuration error this layer surfaces rather than resolves.
	GetByPrincipal(ctx context.Context, tenant Tenant, principal string) (Subject, error)
	// Upsert writes a subject, replacing any existing row with the same id.
	Upsert(ctx context.Context, tenant Tenant, s Subject) error
	// Delete removes a subject. Absent is ErrNotFound.
	Delete(ctx context.Context, tenant Tenant, subjectID string) error
}

// TargetRepository stores what is being reached.
type TargetRepository interface {
	// Get returns a target by id. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, targetID string) (Target, error)
	// GetByHostname is the decision path's lookup: one index hit returning
	// the labels policy matches on, with no join and no second round trip.
	GetByHostname(ctx context.Context, tenant Tenant, hostname string) (Target, error)
	// ListByLabels returns every target carrying all of the given labels.
	// This is an operator and simulation query, not a decision-path one.
	ListByLabels(ctx context.Context, tenant Tenant, labels map[string]string) ([]Target, error)
	// Upsert writes a target, replacing any existing row with the same id.
	Upsert(ctx context.Context, tenant Tenant, t Target) error
	// Delete removes a target. Absent is ErrNotFound.
	Delete(ctx context.Context, tenant Tenant, targetID string) error
}

// ProxyRepository stores the fleet. 0006 owns what the states mean.
type ProxyRepository interface {
	// Get returns a proxy by id. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, proxyID string) (Proxy, error)
	// ListByZone returns every proxy in a zone, in id order.
	ListByZone(ctx context.Context, tenant Tenant, zone string) ([]Proxy, error)
	// Upsert writes a proxy, replacing any existing row with the same id.
	// It does not touch LastHeartbeatAt unless the value given is non-zero,
	// so a re-enrollment cannot silently mark a silent proxy as live.
	Upsert(ctx context.Context, tenant Tenant, p Proxy) error
	// List returns every proxy in the tenant, in id order. This is the
	// graph load (0006): pathfinding needs the whole fleet at once, and the
	// fleet is small relative to the estate.
	List(ctx context.Context, tenant Tenant) ([]Proxy, error)
	// GetByKeyFingerprint answers "is this key one of ours" — the chain-leg
	// question on /v1/auth/cert (proxy D11, 0007). It reads the enrolled
	// rows themselves, through a column generated from `public_key`, rather
	// than a list of proxy key fingerprints kept beside them: two answers to
	// one question drift the first time a proxy re-enrolls with a new key.
	// Absent is ErrNotFound, which here means "not one of ours" and is not a
	// deny on its own.
	GetByKeyFingerprint(ctx context.Context, tenant Tenant, fingerprint string) (Proxy, error)
	// RecordHealth stamps what a heartbeat carried: liveness, session count,
	// last error, and — where the report named them — the contract version
	// and the declared capability set.
	//
	// Absent is ErrNotFound: a heartbeat from a proxy this server has no row
	// for is not a row to create, it is an unenrolled proxy, and creating
	// the row would be the auto-enrollment 0006 exists to refuse.
	RecordHealth(ctx context.Context, tenant Tenant, h ProxyHealthReport) error
	// Delete removes a proxy. Absent is ErrNotFound.
	Delete(ctx context.Context, tenant Tenant, proxyID string) error
}

// PolicyBundleRepository stores versioned policy source (M3, M4).
type PolicyBundleRepository interface {
	// Insert stores a new bundle version. A version that already exists is
	// ErrConflict — bundles are immutable, so the answer to "it is already
	// there" is never to overwrite it.
	Insert(ctx context.Context, tenant Tenant, b PolicyBundle) error
	// Get returns one version. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, version int64) (PolicyBundle, error)
	// GetActive returns the bundle currently served for this tenant.
	// Absent is ErrNotFound, which is a real state: a tenant with no active
	// bundle has no policy, and the decision path must say so rather than
	// deny (M11).
	GetActive(ctx context.Context, tenant Tenant) (PolicyBundle, error)
	// Activate makes one version the active one and deactivates the rest,
	// atomically. Absent is ErrNotFound.
	Activate(ctx context.Context, tenant Tenant, version int64) error
	// NextVersion returns the version number a new bundle should carry.
	NextVersion(ctx context.Context, tenant Tenant) (int64, error)
}

// DecisionRepository stores decision records (M4).
type DecisionRepository interface {
	// Insert stores a decision record. A duplicate decision id is
	// ErrConflict.
	Insert(ctx context.Context, tenant Tenant, d Decision) error
	// Get resolves a decision id into the whole story. Absent is
	// ErrNotFound.
	Get(ctx context.Context, tenant Tenant, decisionID string) (Decision, error)
	// ListBySubject returns a subject's most recent decisions, newest
	// first, bounded by limit.
	ListBySubject(ctx context.Context, tenant Tenant, subjectID string, limit int) ([]Decision, error)
	// ListBySession returns the decisions taken for one SSH session, newest
	// first. It is the lookup an operator arrives with, because a session id
	// is what the user was told; a chained session has one record per hop.
	ListBySession(ctx context.Context, tenant Tenant, sessionID string, limit int) ([]Decision, error)
}

// AuditRepository is the append-only audit store (M8). 0010 owns the chain;
// this is the writer and the reader beneath it.
type AuditRepository interface {
	// Append stores one record. A record_id already present is
	// ErrConflict, refused by the database rather than deduplicated in Go —
	// 0010 depends on that being a database guarantee across concurrent
	// writers on different nodes.
	Append(ctx context.Context, tenant Tenant, r AuditRecord) error
	// Get returns one record by its client-assigned id. Absent is
	// ErrNotFound.
	Get(ctx context.Context, tenant Tenant, recordID string) (AuditRecord, error)
	// Chain returns a stream's records in sequence order, from after
	// afterSeq, bounded by limit. Passing 0 for afterSeq starts at the
	// beginning. This is what verification walks.
	Chain(ctx context.Context, tenant Tenant, stream string, afterSeq int64, limit int) ([]AuditRecord, error)
	// ChainHead returns the last record in a stream, which is what the next
	// record chains onto. A stream with no records is ErrNotFound.
	ChainHead(ctx context.Context, tenant Tenant, stream string) (AuditRecord, error)
	// AppendChain appends to one stream under a lock that serialises
	// appends to it across every node, de-duplicating on record_id first
	// and handing the chain head to `build` so the caller can position and
	// hash what survives. It returns how many rows were inserted.
	AppendChain(ctx context.Context, tenant Tenant, stream string, ids []string,
		build func(head AuditRecord, fresh []string) ([]AuditRecord, error)) (int, error)
	// Streams lists the streams a tenant has records in — what a whole
	// verification walks, and what a departing tenant takes (M18).
	Streams(ctx context.Context, tenant Tenant) ([]string, error)
	// Query selects records over the dimensions PLAN §7 names.
	Query(ctx context.Context, tenant Tenant, q AuditQuery) ([]AuditRecord, error)
	// BlockedCommands is the showcase join: blocked commands on targets
	// carrying a label, resolved to the decision that permitted the access.
	BlockedCommands(ctx context.Context, tenant Tenant, q BlockedCommandQuery) ([]BlockedCommand, error)
	// PutCapture stores a session capture's bytes; Capture reads them back.
	// They are a separate table because a pty stream is large and no query
	// over the records wants to drag it along.
	PutCapture(ctx context.Context, tenant Tenant, recordID string, bytes []byte) error
	Capture(ctx context.Context, tenant Tenant, recordID string) ([]byte, error)
}

// GrantRepository stores JIT access grants (M10).
type GrantRepository interface {
	// Insert stores a grant. A duplicate id is ErrConflict.
	Insert(ctx context.Context, tenant Tenant, g Grant) error
	// Get returns one grant. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, grantID string) (Grant, error)
	// ListLive is the decision path's query: the grants that are inputs for
	// this subject at instant t. Revoked and expired grants are excluded by
	// the index, not by the caller.
	ListLive(ctx context.Context, tenant Tenant, subjectID string, at time.Time) ([]Grant, error)
	// Revoke withdraws a grant at time t. Absent is ErrNotFound; a grant
	// already revoked keeps its original revocation time, because the
	// question an auditor asks is when access stopped, and the second
	// answer is not more true than the first.
	Revoke(ctx context.Context, tenant Tenant, grantID string, t time.Time) error
}

// UIDCursorRepository owns the uid non-reuse floor (PLAN §4).
//
// There is no Release and no Reclaim, and their absence is the design: a
// granted block is gone — used, abandoned or expired alike — so the only
// durable state is the integer.
type UIDCursorRepository interface {
	// Get returns a target's cursor. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, targetID string) (UIDCursor, error)
	// Create initialises a cursor for a target. An existing cursor is
	// ErrConflict: re-creating one is how a cursor would go backwards.
	Create(ctx context.Context, tenant Tenant, targetID string, first, rangeEnd int64) error
	// Advance grants an exclusive block of size uids and moves the cursor
	// past it, under a row lock so two concurrent leases for one target can
	// never read the same value.
	//
	// A cursor with fewer than size uids left is ErrExhausted, which the
	// contract answers with 409 — outage-class to the proxy, nothing
	// provisioned, and the remedy is the operator's. Absent is ErrNotFound.
	Advance(ctx context.Context, tenant Tenant, targetID string, size int64) (UIDBlock, error)
	// RaiseFloor lifts the cursor to at least floor and returns the cursor
	// as it stands afterwards.
	//
	// It can only raise. `observed_floor` is a target's word relayed by the
	// proxy, so it is this server's to clamp or ignore: root on a target
	// can report a large floor and burn that target's range, which is the
	// availability side of an invariant that already prefers refusing to
	// reusing, and it reaches no other target. A floor below the cursor is
	// not an error — it is a stale observation, and ignoring it is the
	// correct answer.
	RaiseFloor(ctx context.Context, tenant Tenant, targetID string, floor int64) (UIDCursor, error)
}

// The south-bound API's repositories (0007). Like the fleet's below, they sit
// beside the tables they are about rather than inside them: a subject's keys
// outlive any one of them, a host key outlives the proxy that reported it, and
// a challenge outlives nothing at all.

// SubjectKeyRepository stores the public keys and certificates a subject may
// offer on `/v1/auth/cert`.
type SubjectKeyRepository interface {
	// GetByFingerprint is the auth path's lookup: a key arrives and this
	// answers whose it is. Absent is ErrNotFound — which is a DENY one
	// layer up and nothing else is, because everything the wrapper cannot
	// positively identify is an outage (M11).
	GetByFingerprint(ctx context.Context, tenant Tenant, fingerprint string) (SubjectKey, error)
	// ListBySubject returns a subject's keys, in fingerprint order.
	ListBySubject(ctx context.Context, tenant Tenant, subjectID string) ([]SubjectKey, error)
	// Put writes a key, replacing any existing row with the same
	// fingerprint.
	Put(ctx context.Context, tenant Tenant, k SubjectKey) error
	// Revoke withdraws a key at time at. A key already revoked keeps its
	// original instant. Absent is ErrNotFound.
	Revoke(ctx context.Context, tenant Tenant, fingerprint string, at time.Time) error
}

// SubjectPasswordRepository stores the local password verifier (0011
// federates it away).
type SubjectPasswordRepository interface {
	// Get returns a subject's verifier. Absent is ErrNotFound, and it is a
	// real state: a subject with no password cannot authenticate with one.
	Get(ctx context.Context, tenant Tenant, subjectID string) (PasswordDigest, error)
	// Put writes a verifier, replacing any existing one.
	Put(ctx context.Context, tenant Tenant, d PasswordDigest) error
	// Delete removes one. Absent is ErrNotFound.
	Delete(ctx context.Context, tenant Tenant, subjectID string) error
}

// MFARepository stores second-factor enrollments and the challenges
// outstanding against them (PLAN §6).
//
// The challenges are rows rather than a map in a process because nothing makes
// a proxy's polls land on the node that issued one (M5).
type MFARepository interface {
	// GetEnrollment returns which provider a subject is enrolled with.
	// Absent is ErrNotFound and means no second factor.
	GetEnrollment(ctx context.Context, tenant Tenant, subjectID string) (MFAEnrollment, error)
	// PutEnrollment writes one, replacing any existing row.
	PutEnrollment(ctx context.Context, tenant Tenant, e MFAEnrollment) error
	// CreateChallenge issues a challenge. A token already present is
	// ErrConflict: re-issuing over an outstanding challenge would resurrect
	// a spent token.
	CreateChallenge(ctx context.Context, tenant Tenant, c MFAChallenge) error
	// GetChallenge reads one without polling it. Absent is ErrNotFound.
	GetChallenge(ctx context.Context, tenant Tenant, token string) (MFAChallenge, error)
	// PollChallenge takes the challenge under a row lock and stamps the
	// poll. The lock is what stops two concurrent polls both resolving one
	// challenge. Absent is ErrNotFound.
	//
	// It returns the row AS IT STOOD BEFORE THIS POLL, with Polls already
	// counting it — so LastPolledAt is the PREVIOUS poll's instant, which
	// is what poll-rate enforcement measures against. Returning the stamp
	// just written would make every poll look like it arrived zero
	// milliseconds after the last one.
	PollChallenge(ctx context.Context, tenant Tenant, token string, at time.Time) (MFAChallenge, error)
	// ResolveChallenge spends a challenge, once. A challenge that is no
	// longer outstanding is ErrConflict, whichever way it went — which is
	// what makes the token single-use.
	ResolveChallenge(ctx context.Context, tenant Tenant, token string, state MFAChallengeState, at time.Time) error
}

// TargetHostKeyRepository records what a target has been seen presenting
// (proxy D7).
type TargetHostKeyRepository interface {
	// Record writes a sighting and reports whether this exact key had been
	// seen before. The boolean comes from the write itself rather than from
	// a preceding read, so two proxies reporting one new key concurrently
	// agree on which of them saw it first.
	Record(ctx context.Context, tenant Tenant, k TargetHostKey) (TargetHostKey, bool, error)
	// ListForTarget returns every key this target has presented, oldest
	// first. A CHANGED key is a question about the set, not about the row
	// just written.
	ListForTarget(ctx context.Context, tenant Tenant, hostname string, port int32) ([]TargetHostKey, error)
}

// UIDLeaseRepository is the append-only record of granted uid blocks.
//
// There is no Release, no Delete and no lookup by block, and their absence is
// the design: nothing may read this table in order to decide what to allocate.
// The cursor is the only allocator (PLAN §4).
type UIDLeaseRepository interface {
	// Record appends the audit row for a granted block.
	Record(ctx context.Context, tenant Tenant, l UIDLease) error
	// Get resolves a lease id — the incident query. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, leaseID string) (UIDLease, error)
	// ListForTarget returns a target's grants, lowest block first.
	ListForTarget(ctx context.Context, tenant Tenant, targetID string) ([]UIDLease, error)
}

// ProxyTokenRepository stores the proxy->server channel credential (M2).
//
// Only the hash of a token's secret half is stored, and no method here takes a
// usable credential: the caller hashes, this looks up.
type ProxyTokenRepository interface {
	// Insert stores a minted token. A duplicate id or secret is
	// ErrConflict.
	Insert(ctx context.Context, tenant Tenant, t ProxyAPIToken) error
	// GetByHash is the middleware's lookup. Absent is ErrNotFound.
	GetByHash(ctx context.Context, tenant Tenant, hash []byte) (ProxyAPIToken, error)
	// Revoke withdraws a token at time at. Absent is ErrNotFound.
	Revoke(ctx context.Context, tenant Tenant, tokenID string, at time.Time) error
	// ListByProxy returns the tokens issued to one proxy, oldest first.
	ListByProxy(ctx context.Context, tenant Tenant, proxyID string) ([]ProxyAPIToken, error)
}

// The fleet registry's repositories (0006). They sit beside ProxyRepository
// above rather than inside it because they are different tables with different
// lifetimes: a grant outlives an enrollment, an edge outlives a heartbeat, and
// a configuration version outlives the proxy that ran it.

// ProxyEnrollmentRepository stores the grants a proxy may enroll against.
//
// Enrollment is an administrative act (0006): a proxy cannot enroll itself into
// a zone it was not granted, because an auto-enrolling fleet lets anyone who
// can reach this server insert a hop into other people's routes.
type ProxyEnrollmentRepository interface {
	// Create issues a grant. An existing grant for the id is ErrConflict:
	// re-issuing one silently is how a spent token comes back to life.
	Create(ctx context.Context, tenant Tenant, e ProxyEnrollment) error
	// Get returns a grant. Absent is ErrNotFound.
	Get(ctx context.Context, tenant Tenant, proxyID string) (ProxyEnrollment, error)
	// Consume marks the grant spent, atomically. A grant already consumed is
	// ErrConflict — the check and the write are one statement, so two
	// concurrent enrollments cannot both win.
	Consume(ctx context.Context, tenant Tenant, proxyID string, at time.Time) error
	// Delete withdraws a grant. Absent is ErrNotFound.
	Delete(ctx context.Context, tenant Tenant, proxyID string) error
}

// ProxyEdgeRepository stores declared reachability — the graph's edge set (M6).
type ProxyEdgeRepository interface {
	// List returns every edge in the tenant, in (proxy, zone, direction, next
	// proxy) order — the primary key's order. It is the graph load, and the
	// ordering is part of the stable tiebreak that makes two nodes answer the
	// same request identically.
	List(ctx context.Context, tenant Tenant) ([]ProxyEdge, error)
	// ReplaceForProxy makes edges the proxy's complete edge set, in one
	// transaction. An enrollment declares reachability as a whole, so a
	// re-declaration that dropped a zone must remove the edge rather than
	// leave a stale one routable.
	ReplaceForProxy(ctx context.Context, tenant Tenant, proxyID string, edges []ProxyEdge) error
}

// RelayRegistrationRepository stores which downstream proxies hold an outbound
// relay connection open right now (proxy D11).
type RelayRegistrationRepository interface {
	// List returns every registration in the tenant, in
	// (upstream, downstream) order.
	List(ctx context.Context, tenant Tenant) ([]RelayRegistration, error)
	// ReplaceForUpstream makes downstream the complete set of registrations
	// this upstream is holding, as of at, in one transaction.
	//
	// It is a replacement rather than a touch because the upstream reports
	// what it holds, and a registration it has stopped reporting is one that
	// dropped. A relay edge to a dropped registration must fall out of
	// routing immediately: the alternative is a route that hangs.
	ReplaceForUpstream(ctx context.Context, tenant Tenant, upstreamProxyID string, downstream []string, at time.Time) error
}

// ProxyConfigRepository stores versioned configuration and its rollout state.
type ProxyConfigRepository interface {
	// InsertVersion stores an immutable document version. A version already
	// present is ErrConflict.
	InsertVersion(ctx context.Context, tenant Tenant, v ProxyConfigVersion) error
	// GetVersion returns one version. Absent is ErrNotFound.
	GetVersion(ctx context.Context, tenant Tenant, scope ConfigScope, version int64) (ProxyConfigVersion, error)
	// NextVersion returns the version a new document for this scope takes.
	NextVersion(ctx context.Context, tenant Tenant, scope ConfigScope) (int64, error)
	// SetDesired publishes a version, recording what it displaced so that a
	// rollback is a first-class operation rather than an operator retyping
	// yesterday's document under pressure.
	SetDesired(ctx context.Context, tenant Tenant, scope ConfigScope, version int64, publishedBy string, at time.Time) error
	// GetDesired returns which version of a scope is published. Absent is
	// ErrNotFound, which is a real state: a scope nobody has published has
	// no desired version, and composing one would be inventing it.
	GetDesired(ctx context.Context, tenant Tenant, scope ConfigScope) (ProxyConfigDesired, error)
	// PutState writes the composed document a proxy should be running. It
	// does not touch what the proxy reported.
	PutState(ctx context.Context, tenant Tenant, s ProxyConfigState) error
	// GetState returns one proxy's rollout state. Absent is ErrNotFound.
	GetState(ctx context.Context, tenant Tenant, proxyID string) (ProxyConfigState, error)
	// ListStates returns every proxy's rollout state, in id order. Drift is
	// read off it, and silent drift across a fleet is indistinguishable from
	// a broken rollout.
	ListStates(ctx context.Context, tenant Tenant) ([]ProxyConfigState, error)
	// ReportRunning records what a proxy says it is running. Absent is
	// ErrNotFound.
	ReportRunning(ctx context.Context, tenant Tenant, proxyID string, version int64, hash string, at time.Time) error
}

// TargetCapabilityRepository stores what one TARGET can take (M17).
//
// It is the second capability source, and the only one that can exist: an
// authorize call happens before the proxy has ever touched the target, so a
// first-ever connection has nothing to declare.
type TargetCapabilityRepository interface {
	// Put records an observation, replacing any earlier one for the same
	// (hostname, port, platform).
	Put(ctx context.Context, tenant Tenant, r TargetCapabilityRecord) error
	// Get returns one record. Absent is ErrNotFound — and absent, stale and
	// undated are ONE case to the caller above (M17), so this error is not a
	// failure to report, it is an input.
	Get(ctx context.Context, tenant Tenant, hostname string, port int32, platform string) (TargetCapabilityRecord, error)
	// List returns every record in the tenant, in (hostname, port, platform)
	// order. The pre-publish query (0014) walks it.
	List(ctx context.Context, tenant Tenant) ([]TargetCapabilityRecord, error)
}
