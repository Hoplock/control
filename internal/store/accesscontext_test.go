// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// External access context (0013, M16): the assertion key on a grant, and the
// scope bindings.

func pushedGrant(id, assertion string, now time.Time) store.Grant {
	return store.Grant{
		ID:           id,
		SubjectID:    "svc-scanner",
		Scope:        "vuln-scan",
		ScopeTargets: []string{"db01.example.com"},
		NotBefore:    now,
		ExpiresAt:    now.Add(time.Hour),
		Origin:       store.GrantOriginExternal,
		Reason:       "window asserted by acme-scanner",
		ExternalRef:  "SCAN-7",
		External: store.GrantExternal{
			System: "acme-scanner", WindowStart: now, WindowEnd: now.Add(2 * time.Hour),
			AssertionID: assertion, Mode: store.ExternalPushProbe,
		},
	}
}

// The same assertion from the same system can be stored once, however two
// pushes race: the second insert is a conflict and the first row is what the
// idempotency lookup returns. The same id from ANOTHER system is another
// assertion.
func TestAnAssertionIsStoredOnce(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := st.Grants().Insert(ctx, tenantA, pushedGrant("g-1", "a-1", now)); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	err := st.Grants().Insert(ctx, tenantA, pushedGrant("g-2", "a-1", now))
	if !store.IsConflict(err) {
		t.Fatalf("a second grant for one assertion = %v, want a conflict", err)
	}

	got, err := st.Grants().GetByAssertion(ctx, tenantA, "acme-scanner", "a-1")
	if err != nil {
		t.Fatalf("GetByAssertion: %v", err)
	}
	if got.ID != "g-1" || got.External.AssertionID != "a-1" || got.External.Mode != store.ExternalPushProbe {
		t.Errorf("GetByAssertion = %+v, want g-1 with its assertion and mode", got)
	}

	other := pushedGrant("g-3", "a-1", now)
	other.External.System = "itsm"
	if err := st.Grants().Insert(ctx, tenantA, other); err != nil {
		t.Errorf("the same id from another system was refused: %v", err)
	}
	if _, err := st.Grants().GetByAssertion(ctx, tenantB, "acme-scanner", "a-1"); !store.IsNotFound(err) {
		t.Errorf("another tenant's assertion = %v, want not found", err)
	}

	// An assertion id with no mode, or a mode with no id, is a grant whose
	// idempotency key means nothing, and the schema refuses it.
	broken := pushedGrant("g-4", "a-2", now)
	broken.External.Mode = ""
	if err := st.Grants().Insert(ctx, tenantA, broken); err == nil {
		t.Error("an assertion id without a mode was stored")
	}
}

func binding() store.AccessContextBinding {
	return store.AccessContextBinding{
		Provider:       "acme-scanner",
		Mode:           store.ExternalPushProbe,
		Scope:          "vuln-scan",
		Subjects:       []string{"svc-scanner"},
		SubjectGroups:  []string{"secops"},
		Targets:        []string{"*.prod.example.com"},
		TargetLabels:   map[string]string{"env": "prod"},
		TargetZones:    []string{"edge"},
		MaxWindow:      4 * time.Hour,
		Privileged:     true,
		PushPrincipals: []string{"p-scanner"},
		Enabled:        true,
		Description:    "the scanner's maintenance windows",
		UpdatedBy:      store.GrantActor{Subject: "admin", Principal: "p-admin"},
	}
}

// A binding round-trips every field, a replace keeps the row's creation time,
// and the schema refuses a binding that may grant to nobody or name every
// target.
func TestABindingRoundTripsAndIsReplacedInPlace(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	want := binding()
	if err := st.AccessContextBindings().Put(ctx, tenantA, want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := st.AccessContextBindings().Get(ctx, tenantA, "acme-scanner")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	created := got.CreatedAt
	want.CreatedAt, want.UpdatedAt = got.CreatedAt, got.UpdatedAt
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, want)
	}

	narrowed := binding()
	narrowed.Enabled = false
	narrowed.MaxWindow = time.Hour
	if err := st.AccessContextBindings().Put(ctx, tenantA, narrowed); err != nil {
		t.Fatalf("replace: %v", err)
	}
	again, err := st.AccessContextBindings().Get(ctx, tenantA, "acme-scanner")
	if err != nil {
		t.Fatalf("Get after replace: %v", err)
	}
	if again.Enabled || again.MaxWindow != time.Hour || !again.CreatedAt.Equal(created) {
		t.Errorf("after replace = %+v; want disabled, one hour, created %v", again, created)
	}

	list, err := st.AccessContextBindings().List(ctx, tenantA)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v; want one binding", list, err)
	}
	if other, err := st.AccessContextBindings().List(ctx, tenantB); err != nil || len(other) != 0 {
		t.Errorf("another tenant sees %v, %v", other, err)
	}

	nobody := binding()
	nobody.Provider, nobody.Subjects, nobody.SubjectGroups = "nobody", nil, nil
	if err := st.AccessContextBindings().Put(ctx, tenantA, nobody); err == nil {
		t.Error("a binding that may grant to nobody was stored")
	}
	everything := binding()
	everything.Provider, everything.Targets, everything.TargetLabels, everything.TargetZones = "everything", nil, nil, nil
	if err := st.AccessContextBindings().Put(ctx, tenantA, everything); err == nil {
		t.Error("a binding that may name every target was stored")
	}

	deleted, err := st.AccessContextBindings().Delete(ctx, tenantA, "acme-scanner")
	if err != nil || !deleted {
		t.Fatalf("Delete = %v, %v", deleted, err)
	}
	if _, err := st.AccessContextBindings().Get(ctx, tenantA, "acme-scanner"); !store.IsNotFound(err) {
		t.Errorf("Get after Delete = %v, want not found", err)
	}
	if deleted, err := st.AccessContextBindings().Delete(ctx, tenantA, "acme-scanner"); err != nil || deleted {
		t.Errorf("a second Delete = %v, %v; want nothing deleted", deleted, err)
	}
}
