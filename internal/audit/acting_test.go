// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/accessctx"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The supervising operator a supervisor's call named (0048) is written by
// every emitter a north-bound call reaches, from the actor it was handed,
// beside the principal: present when the actor carries one, absent when it
// does not, and inside the hashed body, so the chain covers it.
func TestEveryEmitterWritesTheActingOperatorFromItsActor(t *testing.T) {
	const name = "José Núñez"
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	g := store.Grant{ID: "g_1", SubjectID: "alice", Scope: "s", Origin: store.GrantOriginManual,
		NotBefore: now, ExpiresAt: now.Add(time.Hour)}

	emitters := map[string]func(ctx context.Context, e *audit.Emitter, st *store.Store, acting string) error{
		audit.EventTargetPut: func(ctx context.Context, e *audit.Emitter, st *store.Store, acting string) error {
			return st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
				return e.OperatorEvent(ctx, tx, "tenant-a", audit.OperatorAct{
					Event: audit.EventTargetPut, At: now,
					Actor: audit.OperatorActor{Principal: "sup-1", ActingOperator: acting},
				})
			})
		},
		access.EventGrantCreated: func(ctx context.Context, e *audit.Emitter, st *store.Store, acting string) error {
			return st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
				return e.GrantEvent(ctx, tx, "tenant-a", access.Event{
					Name: access.EventGrantCreated, Grant: &g, At: now,
					Actor: access.Actor{Principal: "sup-1", ActingOperator: acting},
				})
			})
		},
		accessctx.EventBindingPut: func(ctx context.Context, e *audit.Emitter, st *store.Store, acting string) error {
			return st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
				return e.ContextEvent(ctx, tx, "tenant-a", accessctx.Event{
					Name: accessctx.EventBindingPut, Provider: "itsm", At: now,
					Binding: &store.AccessContextBinding{Provider: "itsm", Mode: store.ExternalPush, Scope: "w"},
					Actor:   access.Actor{Principal: "sup-1", ActingOperator: acting},
				})
			})
		},
		"logout": func(ctx context.Context, e *audit.Emitter, _ *store.Store, acting string) error {
			return e.AuthEvent(ctx, "tenant-a", identity.AuthEvent{Event: "logout", PrincipalID: "sup-1", ActingOperator: acting})
		},
	}
	for event, emit := range emitters {
		t.Run(event, func(t *testing.T) {
			emitter, st := newEmitter(t)
			ctx := t.Context()
			if err := emit(ctx, emitter, st, name); err != nil {
				t.Fatalf("named: %v", err)
			}
			if err := emit(ctx, emitter, st, ""); err != nil {
				t.Fatalf("unnamed: %v", err)
			}
			recs, err := st.Audit().Query(ctx, "tenant-a", store.AuditQuery{Event: event})
			if err != nil || len(recs) != 2 {
				t.Fatalf("records: %d, %v", len(recs), err)
			}
			var named, unnamed int
			for _, r := range recs {
				if r.Attributes[audit.AttrPrincipalID] != "sup-1" {
					t.Errorf("principal_id = %q", r.Attributes[audit.AttrPrincipalID])
				}
				got, has := r.Attributes[audit.AttrActingOperator]
				switch {
				case has && got == name:
					named++
				case !has:
					unnamed++
				default:
					t.Errorf("acting_operator = %q", got)
				}
				if r.Attributes[audit.AttrActorSubject] == name || r.Subject == name {
					t.Errorf("the name was written as a subject: %+v", r)
				}
			}
			if named != 1 || unnamed != 1 {
				t.Fatalf("named %d, unnamed %d; want one of each", named, unnamed)
			}

			verifier := audit.NewVerifier(st)
			if res, err := verifier.VerifyStream(ctx, "tenant-a", audit.StreamControl); err != nil || res.Break != nil {
				t.Fatalf("the chain over a named record does not verify: %v, %v", res.Break, err)
			}
			tag, err := st.Pool().Exec(ctx, `UPDATE audit_records SET body = replace(body, $1, $2)
				WHERE tenant = 'tenant-a' AND body LIKE '%' || $1 || '%'`,
				`"acting_operator":"José Núñez"`, `"acting_operator":"Someone Else"`)
			if err != nil || tag.RowsAffected() != 1 {
				t.Fatalf("edit the stored name: %v (%d rows)", err, tag.RowsAffected())
			}
			if res, err := verifier.VerifyStream(ctx, "tenant-a", audit.StreamControl); err != nil || res.Break == nil {
				t.Fatalf("a record whose stored acting_operator was edited still verifies: %+v, %v", res, err)
			}
		})
	}
}
