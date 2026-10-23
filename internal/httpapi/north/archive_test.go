// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/archive"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Retention and the extension listing with an archive registered (0025).

type idleArchive struct{}

func (idleArchive) Archive(context.Context, []ext.AuditRecord) error     { return nil }
func (idleArchive) ArchiveCaptures(context.Context, []ext.Capture) error { return nil }
func (idleArchive) Search(context.Context, ext.ArchiveQuery) (ext.ArchivePage, error) {
	return ext.ArchivePage{}, nil
}

// archivedServer is a server whose deployment registered an archive: the
// emitter gates retention on it, and the listing shows its lag through a feed
// that is never started, so the confirmations are the test's to write.
func archivedServer(t *testing.T) *serverFixture {
	t.Helper()
	st := storetest.New(t)
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	reg := ext.NewRegistry()
	if err := reg.RegisterArchiveStore(ext.Registration{Provider: "example.com/archive"}, idleArchive{}); err != nil {
		t.Fatal(err)
	}
	if err := extdefault.Register(reg, extdefault.Deps{}); err != nil {
		t.Fatal(err)
	}
	extensions, err := reg.Seal()
	if err != nil {
		t.Fatal(err)
	}
	feed, err := archive.New(archive.Options{
		Store: st, Archive: idleArchive{}, BatchRecords: 10, CaptureBatchBytes: 1 << 20,
		AttemptTimeout: time.Second, IdleInterval: time.Second, BackoffCap: time.Second,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	o := withLifecycle(t, st, north.Options{
		Federation: federation, Grants: newGrants(t, st, access.Options{}), Extensions: extensions,
		Archive: feed, DefaultTenant: "tenant-a", InsecureCookies: true,
	})
	ingest, err := audit.New(audit.Options{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := extdefault.NewSoftwareKeyStore(st, testKEK)
	if err != nil {
		t.Fatal(err)
	}
	if o.Audit, err = audit.NewEmitter(ingest, audit.WithRetentionSigner(audit.NewRetentionKeys(keys)), audit.WithArchive(true)); err != nil {
		t.Fatal(err)
	}
	server, err := north.New(o)
	if err != nil {
		t.Fatal(err)
	}
	f := &serverFixture{st: st, federation: federation, server: server}
	f.http = httptest.NewServer(server)
	t.Cleanup(f.http.Close)
	return f
}

func confirmControl(t *testing.T, st *store.Store, tenant store.Tenant, seq int64) {
	t.Helper()
	recs, err := st.Audit().Chain(t.Context(), tenant, audit.StreamControl, seq-1, 1)
	if err != nil || len(recs) != 1 {
		t.Fatalf("control record %d: %v", seq, err)
	}
	if _, err := st.Feed().Confirm(t.Context(), tenant, store.ArchiveConsumer, audit.StreamControl, seq, recs[0].Hash); err != nil {
		t.Fatal(err)
	}
}

type retentionAnswer struct {
	Deleted            int64  `json:"deleted"`
	ThroughSeq         int64  `json:"through_seq"`
	Key                string `json:"key"`
	HeldByArchive      *bool  `json:"held_by_archive"`
	ArchivedThroughSeq *int64 `json:"archived_through_seq"`
}

// A pass by sequence beyond the archive is 409 retention_not_archived naming
// the stream and how far it is archived, and changes nothing; a pass by
// instant is held back and says so; once the archive catches up, the pass by
// sequence deletes.
func TestRetentionIsHeldBackByTheArchiveOverTheWire(t *testing.T) {
	f := archivedServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	for range 4 {
		mustStatus(t, f.send(t, "POST", api+"/revocations", admin, map[string]any{"type": "resync"}), http.StatusOK)
	}
	confirmControl(t, f.st, "tenant-a", 2)

	before, _ := f.st.Audit().Chain(t.Context(), "tenant-a", audit.StreamControl, 0, 100)
	resp := f.send(t, "POST", api+"/audit/retention", admin, map[string]any{"stream": audit.StreamControl, "through_seq": 3})
	mustStatus(t, resp, http.StatusConflict)
	e := decodeError(t, resp)
	if e.Code != north.CodeRetentionNotArchived || e.Parameters["stream"] != audit.StreamControl ||
		e.Parameters["archived_through_seq"] != float64(2) || e.CorrelationID == "" || e.Message == "" {
		t.Errorf("a pass beyond the archive answered %+v", e)
	}
	if after, _ := f.st.Audit().Chain(t.Context(), "tenant-a", audit.StreamControl, 0, 100); len(after) != len(before) {
		t.Errorf("a refused pass changed the stream from %d records to %d", len(before), len(after))
	}
	if _, err := f.st.Audit().Retention(t.Context(), "tenant-a", audit.StreamControl); !store.IsNotFound(err) {
		t.Errorf("a refused pass moved the marker: %v", err)
	}

	resp = f.send(t, "POST", api+"/audit/retention", admin, map[string]any{"stream": audit.StreamControl, "before": time.Now().Add(time.Minute)})
	mustStatus(t, resp, http.StatusOK)
	var held retentionAnswer
	decodeInto(t, resp, &held)
	if held.Deleted != 2 || held.ThroughSeq != 2 || held.HeldByArchive == nil || !*held.HeldByArchive ||
		held.ArchivedThroughSeq == nil || *held.ArchivedThroughSeq != 2 || !audit.IsRetentionKey(held.Key) {
		t.Errorf("a pass by instant answered %+v, want 1..2 deleted, held back at 2", held)
	}

	confirmControl(t, f.st, "tenant-a", 4)
	resp = f.send(t, "POST", api+"/audit/retention", admin, map[string]any{"stream": audit.StreamControl, "through_seq": 3})
	mustStatus(t, resp, http.StatusOK)
	var done retentionAnswer
	decodeInto(t, resp, &done)
	if done.Deleted != 1 || done.HeldByArchive == nil || *done.HeldByArchive || done.ArchivedThroughSeq == nil {
		t.Errorf("the pass once caught up answered %+v", done)
	}

	resp = f.send(t, "POST", api+"/audit/captures/retention", admin, map[string]any{"before": time.Now()})
	mustStatus(t, resp, http.StatusOK)
	var captures struct {
		HeldByArchive *bool `json:"held_by_archive"`
	}
	decodeInto(t, resp, &captures)
	if captures.HeldByArchive == nil || *captures.HeldByArchive {
		t.Errorf("a capture pass with nothing waiting answered held_by_archive %v", captures.HeldByArchive)
	}
}

// With no archive registered both answers say held_by_archive false, and the
// prefix answer names no archive position.
func TestRetentionAnswersSayNoArchiveHeldThemBack(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	for range 4 {
		mustStatus(t, f.send(t, "POST", api+"/revocations", admin, map[string]any{"type": "resync"}), http.StatusOK)
	}
	resp := f.send(t, "POST", api+"/audit/retention", admin, map[string]any{"stream": audit.StreamControl, "through_seq": 2})
	mustStatus(t, resp, http.StatusOK)
	var out retentionAnswer
	decodeInto(t, resp, &out)
	if out.Deleted != 2 || out.HeldByArchive == nil || *out.HeldByArchive || out.ArchivedThroughSeq != nil {
		t.Errorf("a pass with no archive answered %+v", out)
	}
	resp = f.send(t, "POST", api+"/audit/captures/retention", admin, map[string]any{"before": time.Now()})
	mustStatus(t, resp, http.StatusOK)
	var captures struct {
		HeldByArchive *bool `json:"held_by_archive"`
	}
	decodeInto(t, resp, &captures)
	if captures.HeldByArchive == nil || *captures.HeldByArchive {
		t.Errorf("a capture pass with no archive answered held_by_archive %v", captures.HeldByArchive)
	}
}

type extensionRow struct {
	Point string `json:"point"`
	Lag   *struct {
		StreamsBehind       int       `json:"streams_behind"`
		OldestUnconfirmedAt time.Time `json:"oldest_unconfirmed_at"`
		CapturesWaiting     int64     `json:"captures_waiting"`
	} `json:"lag"`
}

func extensionRows(t *testing.T, f *serverFixture, tenant store.Tenant) []extensionRow {
	t.Helper()
	token := f.token(t, tenant, map[store.Tenant]identity.RoleSet{tenant: {identity.RoleAuditor}})
	resp := f.send(t, "GET", "/api/v1/tenants/"+string(tenant)+"/extensions", token, nil)
	mustStatus(t, resp, http.StatusOK)
	var out struct {
		Points []extensionRow `json:"points"`
	}
	decodeInto(t, resp, &out)
	return out.Points
}

// The archive's row on GET …/extensions carries the caller's tenant's lag, and
// only that tenant's; no other row carries one, and with no archive registered
// none does.
func TestTheExtensionListingShowsTheArchivesLagForTheCallersTenant(t *testing.T) {
	f := archivedServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	for range 3 {
		mustStatus(t, f.send(t, "POST", api+"/revocations", admin, map[string]any{"type": "resync"}), http.StatusOK)
	}
	confirmControl(t, f.st, "tenant-a", 1)
	// Tenant B's stream of the same name is wholly archived, so whatever
	// the listing says for it is tenant B's alone.
	adminB := f.token(t, "tenant-b", map[store.Tenant]identity.RoleSet{"tenant-b": {identity.RoleAdmin}})
	for range 2 {
		mustStatus(t, f.send(t, "POST", "/api/v1/tenants/tenant-b/revocations", adminB, map[string]any{"type": "resync"}), http.StatusOK)
	}
	confirmControl(t, f.st, "tenant-b", 2)

	for tenant, want := range map[store.Tenant]int{"tenant-a": 1, "tenant-b": 0} {
		var lagged int
		for _, row := range extensionRows(t, f, tenant) {
			if row.Lag == nil {
				continue
			}
			lagged++
			if row.Point != ext.PointArchiveStore.String() {
				t.Errorf("%s: the %s row carries a lag", tenant, row.Point)
			}
			if row.Lag.StreamsBehind != want || row.Lag.CapturesWaiting != 0 || row.Lag.OldestUnconfirmedAt.IsZero() != (want == 0) {
				t.Errorf("%s: lag %+v, want %d stream(s) behind", tenant, *row.Lag, want)
			}
		}
		if lagged != 1 {
			t.Errorf("%s: %d rows carry a lag, want the archive's", tenant, lagged)
		}
	}

	for _, row := range extensionRows(t, newServer(t), "tenant-a") {
		if row.Lag != nil {
			t.Errorf("with no archive registered the %s row carries a lag", row.Point)
		}
	}
}
