// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// A grant record is an administrative act, not a session. It must never be
// filed under the grant CONTEXT columns an external assertion's sessions are
// indexed by (M16) — or "which sessions did ticket CHG-1 authorise" would
// answer with a record that is not a session.
func TestAGrantRecordIsNeverFiledAsASessionsGrantContext(t *testing.T) {
	st := storetest.New(t)
	ctx := t.Context()
	ingest, err := audit.New(audit.Options{Store: st})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	emitter, err := audit.NewEmitter(ingest)
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}

	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	g := store.Grant{
		ID: "g_1", SubjectID: "alice", Scope: "scan-window", ScopeTargets: []string{"db01.example.com"},
		NotBefore: now, ExpiresAt: now.Add(time.Hour), Origin: store.GrantOriginExternal,
		ExternalRef: "CHG-1",
		External:    store.GrantExternal{System: "itsm", WindowStart: now, WindowEnd: now.Add(time.Hour)},
	}
	err = st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		return emitter.GrantEvent(ctx, tx, "acme", access.Event{
			Name: access.EventGrantCreated, Grant: &g, At: now,
			Actor: access.Actor{Principal: "p-1", Subject: "admin"},
		})
	})
	if err != nil {
		t.Fatalf("GrantEvent: %v", err)
	}

	recs, err := st.Audit().Query(ctx, "acme", store.AuditQuery{Event: access.EventGrantCreated})
	if err != nil || len(recs) != 1 {
		t.Fatalf("records: %d, %v", len(recs), err)
	}
	r := recs[0]
	if r.Grant.Stated() {
		t.Errorf("the grant record was filed with grant context %+v", r.Grant)
	}
	if byTicket, err := st.Audit().Query(ctx, "acme", store.AuditQuery{
		GrantSystem: "itsm", GrantReference: "CHG-1",
	}); err != nil || len(byTicket) != 0 {
		t.Errorf("the ticket's session query returned %d records, %v — want none", len(byTicket), err)
	}
	if r.Attributes[audit.AttrGrantExternalRef] != "CHG-1" || r.Target != "db01.example.com" ||
		r.Attributes[audit.AttrGrantOrigin] != "external" {
		t.Errorf("record attributes %v target %q", r.Attributes, r.Target)
	}
}

// Written inside a transaction that rolls back, the record is gone with it:
// there is no account of an act that never happened.
func TestAGrantRecordRollsBackWithItsAct(t *testing.T) {
	st := storetest.New(t)
	ctx := t.Context()
	ingest, _ := audit.New(audit.Options{Store: st})
	emitter, _ := audit.NewEmitter(ingest)

	g := store.Grant{ID: "g_1", SubjectID: "alice", Scope: "s", Origin: store.GrantOriginManual,
		NotBefore: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	_ = st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := emitter.GrantEvent(ctx, tx, "acme", access.Event{
			Name: access.EventGrantCreated, Grant: &g, Actor: access.Actor{Principal: "p-1"},
		}); err != nil {
			t.Fatalf("GrantEvent: %v", err)
		}
		return errors.New("the act failed after its record was written")
	})
	if recs, err := st.Audit().Query(ctx, "acme", store.AuditQuery{Event: access.EventGrantCreated}); err != nil || len(recs) != 0 {
		t.Errorf("a rolled-back act left %d records, %v", len(recs), err)
	}
}
