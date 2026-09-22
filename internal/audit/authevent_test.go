// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// This server's own authentication records (0011, M7).

func newEmitter(t *testing.T) (*audit.Emitter, *store.Store) {
	t.Helper()
	st := storetest.New(t)
	in, err := audit.New(audit.Options{Store: st})
	if err != nil {
		t.Fatalf("ingester: %v", err)
	}
	emitter, err := audit.NewEmitter(in)
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}
	return emitter, st
}

func TestABreakGlassLoginIsAssertedInTheAuditRecord(t *testing.T) {
	// M7's acceptance criterion: asserted, not assumed. A reader must not have
	// to conclude "break-glass" from `source == local`.
	emitter, st := newEmitter(t)
	ctx := context.Background()

	if err := emitter.AuthEvent(ctx, "tenant-a", identity.AuthEvent{
		Event:         "login",
		Subject:       "break-glass",
		Login:         "root",
		Source:        identity.SourceLocal,
		BreakGlass:    true,
		PrincipalID:   "sess-1",
		CorrelationID: "corr-1",
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}

	rows, err := audit.NewReader(st).Find(ctx, "tenant-a", audit.Query{Kinds: []string{"auth"}, Limit: 10})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d records", len(rows))
	}
	row := rows[0]
	if row.Attributes[audit.AttrBreakGlass] != "true" {
		t.Fatalf("the record does not assert break_glass: %v", row.Attributes)
	}
	if row.Severity != "critical" {
		t.Errorf("severity %q — a break-glass login filed at info is one nobody's alerting sees", row.Severity)
	}
	if !strings.Contains(row.Message, "break-glass") {
		t.Errorf("the message reads like every other login: %q", row.Message)
	}
	if row.Subject != "break-glass" || row.Login != "root" {
		t.Errorf("the record does not name who: %q / %q", row.Subject, row.Login)
	}
	if row.Attributes[audit.AttrCorrelationID] != "corr-1" {
		t.Errorf("the record does not tie to the request log: %v", row.Attributes)
	}
	// And it is in the chain THIS SERVER writes, not a proxy's.
	if row.Stream != audit.StreamControl {
		t.Errorf("stream %q, want %q", row.Stream, audit.StreamControl)
	}
}

func TestAFederatedLoginRecordsItsMappingVersionAndIsNotBreakGlass(t *testing.T) {
	emitter, st := newEmitter(t)
	ctx := context.Background()

	if err := emitter.AuthEvent(ctx, "tenant-a", identity.AuthEvent{
		Event:          "login",
		Subject:        "okta:00u1",
		Login:          "alice",
		Source:         "okta",
		MappingVersion: 7,
		Groups:         []string{"sre", "oncall"},
		Attributes:     map[string]string{"department": "engineering"},
		PrincipalID:    "sess-2",
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}

	rows, err := audit.NewReader(st).Find(ctx, "tenant-a", audit.Query{Kinds: []string{"auth"}, Limit: 10})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	row := rows[0]
	if row.Attributes[audit.AttrBreakGlass] != "false" {
		t.Errorf("break_glass: %q — the flag is written either way so a reader never has to infer it",
			row.Attributes[audit.AttrBreakGlass])
	}
	if row.Attributes[audit.AttrMappingVersion] != "7" {
		t.Errorf("mapping_version: %q", row.Attributes[audit.AttrMappingVersion])
	}
	if row.Attributes[audit.AttrGroups] != "sre,oncall" {
		t.Errorf("groups: %q", row.Attributes[audit.AttrGroups])
	}
	// A mapped attribute is namespaced, so a claim called `event` cannot
	// overwrite the record's own `event`.
	if row.Attributes[audit.AttrAttributePrefix+"department"] != "engineering" {
		t.Errorf("attributes: %v", row.Attributes)
	}
	if row.Severity != "info" {
		t.Errorf("severity: %q", row.Severity)
	}
}

func TestTwoLoginsAreTwoRecords(t *testing.T) {
	// `record_id` is the ingest path's idempotency key, so an id derived from
	// the event would silently make two break-glass logins one.
	emitter, st := newEmitter(t)
	ctx := context.Background()

	event := identity.AuthEvent{Event: "login", Subject: "break-glass", Source: "local", BreakGlass: true}
	for range 3 {
		if err := emitter.AuthEvent(ctx, "tenant-a", event); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}
	rows, err := audit.NewReader(st).Find(ctx, "tenant-a", audit.Query{Kinds: []string{"auth"}, Limit: 10})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("%d records for 3 logins", len(rows))
	}
}

func TestTheControlChainVerifies(t *testing.T) {
	// `audit-verify --stream control` verifies exactly the records nobody
	// outside this process produced.
	emitter, st := newEmitter(t)
	ctx := context.Background()

	for _, e := range []identity.AuthEvent{
		{Event: "login", Subject: "a", Source: "okta"},
		{Event: "login_denied", Subject: "b", Source: "local", BreakGlass: true, Reason: "bad-password"},
		{Event: "logout", PrincipalID: "sess-1"},
		{Event: "token_issued", PrincipalID: "tok-1", Source: "local"},
	} {
		if err := emitter.AuthEvent(ctx, "tenant-a", e); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}

	result, err := audit.NewVerifier(st).VerifyStream(ctx, "tenant-a", audit.StreamControl)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Break != nil {
		t.Fatalf("the control chain does not verify: %v", result.Break)
	}
	if result.Records != 4 {
		t.Errorf("verified %d records", result.Records)
	}
}

func TestARecordCarriesNoCredential(t *testing.T) {
	// An audit record of a credential is a credential in the audit store, which
	// is the one place in this system designed never to forget anything.
	emitter, st := newEmitter(t)
	ctx := context.Background()

	if err := emitter.AuthEvent(ctx, "tenant-a", identity.AuthEvent{
		Event: "token_issued", PrincipalID: "tok-1", Source: "local",
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	rows, err := audit.NewReader(st).Find(ctx, "tenant-a", audit.Query{Kinds: []string{"auth"}, Limit: 10})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(rows[0].Body), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	for _, forbidden := range []string{"secret", "token_digest", "password", "ht.", "hs."} {
		if strings.Contains(rows[0].Body, forbidden) {
			t.Errorf("the record body carries %q: %s", forbidden, rows[0].Body)
		}
	}
}

func TestAnEmitterNeedsATenant(t *testing.T) {
	emitter, _ := newEmitter(t)
	if err := emitter.AuthEvent(context.Background(), "", identity.AuthEvent{Event: "login"}); err == nil {
		t.Fatal("a record was written with no tenant")
	}
}
