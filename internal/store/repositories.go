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
	// RecordHeartbeat stamps a proxy as having reported at t. Absent is
	// ErrNotFound: a heartbeat from a proxy this server has no row for is
	// not a row to create, it is a question for 0006.
	RecordHeartbeat(ctx context.Context, tenant Tenant, proxyID string, t time.Time) error
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
