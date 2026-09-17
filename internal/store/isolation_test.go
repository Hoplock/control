// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Cross-tenant isolation is a test class, not a review item (M18).
//
// Every repository is seeded identically under two tenants and then asked for
// the other one's rows. The identical ids are the point: a query that forgot
// its tenant filter finds a row and passes everything else in this package.
func TestNoRepositoryReachesAnotherTenantsRows(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	now := time.Now().UTC()
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		storetest.Seed(t, st, storetest.Fixture{
			Tenant:   tenant,
			Subjects: []store.Subject{storetest.Subject(string(tenant) + "-only")},
			Targets: []store.Target{
				storetest.Target(string(tenant)+"-only", string(tenant)+".example",
					map[string]string{"env": string(tenant)}),
			},
			Proxies:    []store.Proxy{storetest.Proxy(string(tenant)+"-only", "zone-a")},
			Bundles:    []store.PolicyBundle{storetest.Bundle(1, true)},
			Grants:     []store.Grant{storetest.Grant(string(tenant)+"-only", string(tenant)+"-only", time.Hour)},
			UIDCursors: []storetest.UIDCursorFixture{{TargetID: string(tenant) + "-only", First: 100000, RangeEnd: 200000}},
			Audit: []store.AuditRecord{
				storetest.AuditRecord(string(tenant)+"-only", "session", 1),
			},
		})
		if err := st.Decisions().Insert(ctx, tenant, store.Decision{
			ID:           string(tenant) + "-only",
			SubjectID:    string(tenant) + "-only",
			TargetID:     string(tenant) + "-only",
			InputsDigest: "x",
			Snapshot:     json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("seed decision for %s: %v", tenant, err)
		}
	}

	// Everything below asks tenant A for tenant B's row, by B's own id.
	bOnly := string(tenantB) + "-only"

	reads := map[string]func() error{
		"Subjects.Get":            func() error { _, err := st.Subjects().Get(ctx, tenantA, bOnly); return err },
		"Subjects.GetByPrincipal": func() error { _, err := st.Subjects().GetByPrincipal(ctx, tenantA, bOnly); return err },
		"Targets.Get":             func() error { _, err := st.Targets().Get(ctx, tenantA, bOnly); return err },
		"Targets.GetByHostname": func() error {
			_, err := st.Targets().GetByHostname(ctx, tenantA, string(tenantB)+".example")
			return err
		},
		"Proxies.Get":        func() error { _, err := st.Proxies().Get(ctx, tenantA, bOnly); return err },
		"Decisions.Get":      func() error { _, err := st.Decisions().Get(ctx, tenantA, bOnly); return err },
		"Audit.Get":          func() error { _, err := st.Audit().Get(ctx, tenantA, bOnly); return err },
		"Grants.Get":         func() error { _, err := st.Grants().Get(ctx, tenantA, bOnly); return err },
		"UIDCursors.Get":     func() error { _, err := st.UIDCursors().Get(ctx, tenantA, bOnly); return err },
		"UIDCursors.Advance": func() error { _, err := st.UIDCursors().Advance(ctx, tenantA, bOnly, 10); return err },
		"UIDCursors.RaiseFloor": func() error {
			_, err := st.UIDCursors().RaiseFloor(ctx, tenantA, bOnly, 150000)
			return err
		},
		"Proxies.RecordHeartbeat": func() error { return st.Proxies().RecordHeartbeat(ctx, tenantA, bOnly, now) },
		"Grants.Revoke":           func() error { return st.Grants().Revoke(ctx, tenantA, bOnly, now) },
		"Subjects.Delete":         func() error { return st.Subjects().Delete(ctx, tenantA, bOnly) },
		"Targets.Delete":          func() error { return st.Targets().Delete(ctx, tenantA, bOnly) },
		"Proxies.Delete":          func() error { return st.Proxies().Delete(ctx, tenantA, bOnly) },
	}
	for name, read := range reads {
		if err := read(); !store.IsNotFound(err) {
			t.Errorf("%s reached tenant B's row from tenant A: %v, want ErrNotFound", name, err)
		}
	}

	// The list-shaped reads answer with A's rows only.
	lists := map[string]func() (int, error){
		"Targets.ListByLabels": func() (int, error) {
			got, err := st.Targets().ListByLabels(ctx, tenantA, map[string]string{"env": string(tenantB)})
			return len(got), err
		},
		"Proxies.ListByZone": func() (int, error) {
			got, err := st.Proxies().ListByZone(ctx, tenantA, "zone-a")
			return len(got) - 1, err // A's own proxy is expected
		},
		"Grants.ListLive": func() (int, error) {
			got, err := st.Grants().ListLive(ctx, tenantA, bOnly, now)
			return len(got), err
		},
		"Decisions.ListBySubject": func() (int, error) {
			got, err := st.Decisions().ListBySubject(ctx, tenantA, bOnly, 0)
			return len(got), err
		},
		"Audit.Chain": func() (int, error) {
			got, err := st.Audit().Chain(ctx, tenantA, "session", 0, 0)
			return len(got) - 1, err // A's own record is expected
		},
	}
	for name, list := range lists {
		extra, err := list()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if extra != 0 {
			t.Errorf("%s returned %d row(s) belonging to another tenant", name, extra)
		}
	}

	// The writes that succeeded above must not have been writes at all:
	// tenant B's rows are exactly as they were seeded.
	if _, err := st.Grants().Get(ctx, tenantB, bOnly); err != nil {
		t.Errorf("tenant B's grant after tenant A tried to revoke it: %v", err)
	}
	if got, err := st.Grants().ListLive(ctx, tenantB, bOnly, now); err != nil || len(got) != 1 {
		t.Errorf("tenant B's grant is no longer live after tenant A's Revoke: %d, %v", len(got), err)
	}
	cursor, err := st.UIDCursors().Get(ctx, tenantB, bOnly)
	if err != nil {
		t.Fatalf("tenant B's cursor: %v", err)
	}
	if cursor.NextUID != 100000 {
		t.Errorf("tenant B's cursor moved to %d after tenant A's Advance, want 100000", cursor.NextUID)
	}
	if _, err := st.Subjects().Get(ctx, tenantB, bOnly); err != nil {
		t.Errorf("tenant B's subject after tenant A tried to delete it: %v", err)
	}

	// The active bundle is per tenant, and both are version 1: a GetActive
	// that ignored the tenant would still find a row, and it would be the
	// wrong tenant's program (M18 consequence 2).
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		active, err := st.PolicyBundles().GetActive(ctx, tenant)
		if err != nil {
			t.Errorf("GetActive for %s: %v", tenant, err)
			continue
		}
		if active.Version != 1 {
			t.Errorf("GetActive for %s returned version %d, want 1", tenant, active.Version)
		}
	}
}

// The audit chain is per tenant (M8, M18): a customer leaving must be able to
// take a chain that still verifies, so two tenants' records interleaved by
// arrival time still produce two chains that each verify alone.
func TestAuditChainsAreIndependentPerTenant(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	// Interleaved by arrival: A, B, A, B, ... with each tenant's own
	// sequence running 1..4 and both using the same stream name.
	const stream = "session"
	for seq := int64(1); seq <= 4; seq++ {
		for _, tenant := range []store.Tenant{tenantA, tenantB} {
			rec := storetest.AuditRecord(string(tenant)+"-r", stream, seq)
			rec.RecordID = string(tenant) + "-r" + string(rune('0'+seq))
			rec.PrevHash = prevHashFor(tenant, seq)
			rec.Hash = hashFor(tenant, seq)
			if err := st.Audit().Append(ctx, tenant, rec); err != nil {
				t.Fatalf("append %s seq %d: %v", tenant, seq, err)
			}
		}
	}

	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		chain, err := st.Audit().Chain(ctx, tenant, stream, 0, 0)
		if err != nil {
			t.Fatalf("Chain for %s: %v", tenant, err)
		}
		if len(chain) != 4 {
			t.Fatalf("Chain for %s returned %d records, want 4 (its own, not both tenants')",
				tenant, len(chain))
		}
		// The chain verifies alone: each record's prev_hash is the
		// previous record's hash, and no record from the other tenant
		// appears in between.
		for i, rec := range chain {
			if rec.ChainSeq != int64(i+1) {
				t.Errorf("%s record %d has seq %d, want %d", tenant, i, rec.ChainSeq, i+1)
			}
			if i > 0 && rec.PrevHash != chain[i-1].Hash {
				t.Errorf("%s chain breaks at seq %d: prev_hash %q, previous hash %q",
					tenant, rec.ChainSeq, rec.PrevHash, chain[i-1].Hash)
			}
		}

		head, err := st.Audit().ChainHead(ctx, tenant, stream)
		if err != nil {
			t.Fatalf("ChainHead for %s: %v", tenant, err)
		}
		if head.ChainSeq != 4 || head.Hash != hashFor(tenant, 4) {
			t.Errorf("ChainHead for %s = seq %d hash %q, want seq 4 hash %q",
				tenant, head.ChainSeq, head.Hash, hashFor(tenant, 4))
		}
	}

	// Both tenants used sequence 1..4 in the same stream, so the unique
	// index that makes a removed record detectable as a gap must be scoped
	// to the tenant — which is exactly what letting both succeed proved.
	if _, err := st.Audit().ChainHead(ctx, tenantA, "no-such-stream"); !store.IsNotFound(err) {
		t.Errorf("ChainHead of an empty stream: %v, want ErrNotFound", err)
	}
}

// hashFor and prevHashFor stand in for the real chain 0010 computes. What is
// being tested here is that the STORE keeps two chains apart, not how a hash
// is built, and a harness that computed the real one would be testing itself.
func hashFor(tenant store.Tenant, seq int64) string {
	return string(tenant) + "-hash-" + string(rune('0'+seq))
}

func prevHashFor(tenant store.Tenant, seq int64) string {
	if seq == 1 {
		return ""
	}
	return hashFor(tenant, seq-1)
}

// record_id uniqueness is enforced by the DATABASE (M8).
//
// 0010 depends on it being a database guarantee across concurrent writers on
// different nodes: a proxy draining its disk buffer after an outage resends,
// and a read-then-write in Go is a race with a plausible-looking test.
func TestDuplicateAuditRecordIsRejectedByTheDatabase(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	rec := storetest.AuditRecord("r-1", "session", 1)
	if err := st.Audit().Append(ctx, tenantA, rec); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// A resend: same record_id, different everything else, as a proxy
	// replaying a buffer with a re-serialised payload would send.
	resend := rec
	resend.ChainSeq = 2
	resend.Payload = json.RawMessage(`{"resent":true}`)
	err := st.Audit().Append(ctx, tenantA, resend)
	if !store.IsConflict(err) {
		t.Fatalf("resending record_id %q: %v, want ErrConflict", rec.RecordID, err)
	}

	// Rejected, not deduplicated: the stored record is the first one.
	got, err := st.Audit().Get(ctx, tenantA, "r-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ChainSeq != 1 {
		t.Errorf("the resend overwrote the stored record: seq %d, want 1", got.ChainSeq)
	}

	// The constraint is the database's, not the repository's: the same
	// insert through raw SQL, with no Go guard anywhere in the path, must
	// fail too.
	_, rawErr := st.Pool().Exec(ctx, `
		INSERT INTO audit_records (tenant, record_id, stream, chain_seq, kind, severity, payload, recorded_at)
		VALUES ($1, 'r-1', 'session', 9, 'k', 'info', '{}'::jsonb, now())`, tenantA)
	if rawErr == nil {
		t.Error("a raw duplicate insert succeeded: record_id uniqueness is not enforced by the database")
	}

	// And the same record_id under another tenant is not a duplicate: the
	// key is scoped to the tenant, so two customers' proxies cannot collide.
	if err := st.Audit().Append(ctx, tenantB, rec); err != nil {
		t.Errorf("the same record_id under another tenant: %v, want it accepted", err)
	}
}
