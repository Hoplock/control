// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"time"

	"github.com/hoplock/control/internal/store"
)

// The read side (PLAN §7).
//
// It is a QUERY LAYER AND NOT AN HTTP SURFACE. 0014 owns the north-bound API
// and this is what it will serve from; building the routes here would put the
// operator surface on a listener whose credential model has not been designed
// (M2). The one exception is the record read-back this phase must expose for
// the priority ack to be observable at all — see cmd/hoplock-control/auditread.go.

// Reader answers the questions an investigation arrives with.
type Reader struct {
	store *store.Store
}

// NewReader builds a Reader over a store.
func NewReader(st *store.Store) *Reader { return &Reader{store: st} }

// Query is the general selection, by session, subject, target, decision id,
// time range, kind, severity, event name, grant reference and device field.
type Query = store.AuditQuery

// Record is one stored row as a reader sees it.
type Row = store.AuditRecord

// Find returns the records matching a query, newest first.
func (r *Reader) Find(ctx context.Context, tenant store.Tenant, q Query) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, q)
}

// Get returns one record by its client-assigned id. It is the lookup the
// conformance suite's durability assertion goes through.
func (r *Reader) Get(ctx context.Context, tenant store.Tenant, recordID string) (Row, error) {
	return r.store.Audit().Get(ctx, tenant, recordID)
}

// Session returns every record of one session, oldest first — the order a
// replay reads them in, and the one case where the newest-first default is
// wrong. It is ordered in the database rather than reversed here: with a limit
// in play, reversing a newest-first page would hand back the END of a session
// and call it the beginning.
func (r *Reader) Session(ctx context.Context, tenant store.Tenant, sessionID string, limit int) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, Query{
		SessionID: sessionID,
		Ascending: true,
		Limit:     limit,
	})
}

// Capture returns the stored bytes of one `stream` record, for replay.
func (r *Reader) Capture(ctx context.Context, tenant store.Tenant, recordID string) ([]byte, error) {
	return r.store.Audit().Capture(ctx, tenant, recordID)
}

// AccountMappings returns the ephemeral-account mapping events in a window.
//
// It is a query of its own because the record it returns is the only place
// attribution exists on a device whose account-name length forced the readable
// login segment out of the name: nothing on the target says who `hl-a7f3c1`
// belonged to. Asking for it by session would presuppose knowing which session
// to ask about, which is the thing this answers.
func (r *Reader) AccountMappings(ctx context.Context, tenant store.Tenant, from, to time.Time, limit int) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, Query{
		Event: EventAccountMapping,
		From:  from,
		To:    to,
		Limit: limit,
	})
}

// DeviceConfigChanges returns the device configuration-change events in a
// window. A customer's drift detection sees every one of these as an
// unexplained change; this is the query that lets a SIEM close them.
func (r *Reader) DeviceConfigChanges(ctx context.Context, tenant store.Tenant, from, to time.Time, limit int) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, Query{
		Event: EventDeviceConfigChange,
		From:  from,
		To:    to,
		Limit: limit,
	})
}

// UnderGrant returns every record of every session that ran under one external
// system's reference — "show me every session that ran under this scan" (M16).
func (r *Reader) UnderGrant(ctx context.Context, tenant store.Tenant, system, reference string, limit int) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, Query{
		GrantSystem:    system,
		GrantReference: reference,
		Limit:          limit,
	})
}

// ByDeviceField returns the records provisioned with one device field value —
// `vdom=root` against `vdom=global` being the difference between an
// administrator confined to one virtual domain and a global one on the same
// host.
func (r *Reader) ByDeviceField(ctx context.Context, tenant store.Tenant, name, value string, limit int) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, Query{
		DeviceFieldName:  name,
		DeviceFieldValue: value,
		Limit:            limit,
	})
}

// BlockedCommands is the query that sells the product (PLAN §7):
//
//	every blocked command on env=prod last week, who ran it, over which
//	route, and which decision permitted the access.
//
// It is a join from audit to decisions, which is why 0008 stores `decision_id`
// on both sides, and to targets, because `env=prod` is a label and labels are
// policy inputs rather than record attributes. The approved grant that made
// the access possible is the third leg; the grant CONTEXT is already on every
// row (M16) and the grant record itself arrives with 0012.
func (r *Reader) BlockedCommands(ctx context.Context, tenant store.Tenant, label, value string, from, to time.Time, limit int) ([]store.BlockedCommand, error) {
	return r.store.Audit().BlockedCommands(ctx, tenant, store.BlockedCommandQuery{
		LabelName:  label,
		LabelValue: value,
		From:       from,
		To:         to,
		Limit:      limit,
	})
}

// DegradedCredentials returns the sessions that ran on something other than
// the credential method policy preferred — every record whose
// `target_auth_rung` is above 0 (proxy D14).
//
// It is a query rather than a report because the answer is a COUNT ACROSS AN
// ESTATE: one session accepting its second choice is a fact about that target,
// and a thousand of them is a deployment where the preferred method does not
// work. Neither is visible from a session at a time.
func (r *Reader) DegradedCredentials(ctx context.Context, tenant store.Tenant, from, to time.Time, limit int) ([]Row, error) {
	rows, err := r.store.Audit().Query(ctx, tenant, Query{From: from, To: to, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, row := range rows {
		if row.TargetAuthRung != nil && *row.TargetAuthRung > 0 {
			out = append(out, row)
		}
	}
	return out, nil
}
