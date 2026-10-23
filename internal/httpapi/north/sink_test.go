// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/archive"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/export"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Retention and the extension listing with sinks registered (0026).

type idleSink struct{}

func (idleSink) Export(context.Context, []ext.AuditRecord) error { return nil }

const (
	siemOne = "example.com/siem-one"
	siemTwo = "example.com/siem-two"
)

// sinkServer is a server whose deployment registered two sinks, and an archive
// too when archived is set. Delivery is never started, so the positions are
// the test's to write.
func sinkServer(t *testing.T, archived bool) *serverFixture {
	t.Helper()
	st := storetest.New(t)
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	reg := ext.NewRegistry()
	for _, p := range []string{siemOne, siemTwo} {
		if err := reg.RegisterAuditSink(ext.Registration{Provider: p, Version: "1.0.0"}, idleSink{}); err != nil {
			t.Fatal(err)
		}
	}
	if archived {
		if err := reg.RegisterArchiveStore(ext.Registration{Provider: "example.com/archive"}, idleArchive{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := extdefault.Register(reg, extdefault.Deps{}); err != nil {
		t.Fatal(err)
	}
	extensions, err := reg.Seal()
	if err != nil {
		t.Fatal(err)
	}
	sinks, err := export.New(export.Options{
		Store: st, Sinks: extensions.AuditSinks(), BatchRecords: 10, AttemptTimeout: time.Second,
		IdleInterval: time.Second, BackoffCap: time.Second, MaxInFlight: 1, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	o := north.Options{
		Federation: federation, Grants: newGrants(t, st, access.Options{}), Extensions: extensions,
		Sinks: sinks, DefaultTenant: "tenant-a", InsecureCookies: true,
	}
	if archived {
		feed, err := archive.New(archive.Options{
			Store: st, Archive: idleArchive{}, BatchRecords: 10, CaptureBatchBytes: 1 << 20,
			AttemptTimeout: time.Second, IdleInterval: time.Second, BackoffCap: time.Second,
			Logger: slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatal(err)
		}
		o.Archive = feed
	}
	o = withLifecycle(t, st, o)
	ingest, err := audit.New(audit.Options{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := extdefault.NewSoftwareKeyStore(st, testKEK)
	if err != nil {
		t.Fatal(err)
	}
	if o.Audit, err = audit.NewEmitter(ingest, audit.WithRetentionSigner(audit.NewRetentionKeys(keys)), audit.WithArchive(archived)); err != nil {
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

// confirmSink moves a sink's position in the tenant's control stream to seq.
func confirmSink(t *testing.T, st *store.Store, tenant store.Tenant, provider string, seq int64) {
	t.Helper()
	recs, err := st.Audit().Chain(t.Context(), tenant, audit.StreamControl, seq-1, 1)
	if err != nil || len(recs) != 1 {
		t.Fatalf("control record %d: %v", seq, err)
	}
	if _, err := st.Feed().Confirm(t.Context(), tenant, store.SinkConsumer(provider), audit.StreamControl, seq, recs[0].Hash); err != nil {
		t.Fatal(err)
	}
}

// Retention does not wait on a sink: a pass by sequence beyond every sink's
// position deletes what it asked for. With an archive registered too, only the
// archive holds it back.
func TestRetentionDoesNotWaitOnASink(t *testing.T) {
	t.Run("sinks alone", func(t *testing.T) {
		f := sinkServer(t, false)
		admin := f.roleToken(t, identity.RoleAdmin)
		for range 4 {
			mustStatus(t, f.send(t, "POST", api+"/revocations", admin, map[string]any{"type": "resync"}), http.StatusOK)
		}
		confirmSink(t, f.st, "tenant-a", siemOne, 1)
		resp := f.send(t, "POST", api+"/audit/retention", admin, map[string]any{"stream": audit.StreamControl, "through_seq": 3})
		mustStatus(t, resp, http.StatusOK)
		var out retentionAnswer
		decodeInto(t, resp, &out)
		if out.Deleted != 3 || out.HeldByArchive == nil || *out.HeldByArchive || out.ArchivedThroughSeq != nil {
			t.Errorf("a pass behind two sinks answered %+v; want 3 deleted, nothing held", out)
		}
	})
	t.Run("sinks and an archive", func(t *testing.T) {
		f := sinkServer(t, true)
		admin := f.roleToken(t, identity.RoleAdmin)
		for range 4 {
			mustStatus(t, f.send(t, "POST", api+"/revocations", admin, map[string]any{"type": "resync"}), http.StatusOK)
		}
		confirmControl(t, f.st, "tenant-a", 2)
		resp := f.send(t, "POST", api+"/audit/retention", admin, map[string]any{"stream": audit.StreamControl, "through_seq": 3})
		mustStatus(t, resp, http.StatusConflict)
		if e := decodeError(t, resp); e.Code != north.CodeRetentionNotArchived || e.Parameters["archived_through_seq"] != float64(2) {
			t.Errorf("a pass beyond the archive answered %+v", e)
		}
		resp = f.send(t, "POST", api+"/audit/retention", admin, map[string]any{"stream": audit.StreamControl, "through_seq": 2})
		mustStatus(t, resp, http.StatusOK)
		var out retentionAnswer
		decodeInto(t, resp, &out)
		if out.Deleted != 2 || out.ArchivedThroughSeq == nil || *out.ArchivedThroughSeq != 2 {
			t.Errorf("a pass the archive holds, behind two sinks at nothing, answered %+v", out)
		}
	})
}

type sinkLag struct {
	StreamsBehind       int              `json:"streams_behind"`
	OldestUnconfirmedAt time.Time        `json:"oldest_unconfirmed_at"`
	Failing             bool             `json:"failing"`
	SkippedByRetention  *int64           `json:"skipped_by_retention"`
	CapturesWaiting     *json.RawMessage `json:"captures_waiting"`
}

type sinkRow struct {
	Point         string `json:"point"`
	Registrations []struct {
		Provider string   `json:"provider"`
		Lag      *sinkLag `json:"lag"`
	} `json:"registrations"`
	Lag *sinkLag `json:"lag"`
}

func sinkRows(t *testing.T, f *serverFixture, tenant store.Tenant) []sinkRow {
	t.Helper()
	token := f.token(t, tenant, map[store.Tenant]identity.RoleSet{tenant: {identity.RoleAuditor}})
	resp := f.send(t, "GET", "/api/v1/tenants/"+string(tenant)+"/extensions", token, nil)
	mustStatus(t, resp, http.StatusOK)
	var out struct {
		Points []sinkRow `json:"points"`
	}
	decodeInto(t, resp, &out)
	return out.Points
}

// GET …/extensions shows each sink's lag on its registration, for the
// caller's tenant and only that tenant's, under the field names the archive's
// lag uses; the archive's lag keeps its row and gains whether it is failing.
func TestTheExtensionListingShowsEachSinksLagForTheCallersTenant(t *testing.T) {
	f := sinkServer(t, true)
	admin := f.roleToken(t, identity.RoleAdmin)
	for range 3 {
		mustStatus(t, f.send(t, "POST", api+"/revocations", admin, map[string]any{"type": "resync"}), http.StatusOK)
	}
	adminB := f.token(t, "tenant-b", map[store.Tenant]identity.RoleSet{"tenant-b": {identity.RoleAdmin}})
	for range 2 {
		mustStatus(t, f.send(t, "POST", "/api/v1/tenants/tenant-b/revocations", adminB, map[string]any{"type": "resync"}), http.StatusOK)
	}
	// Tenant A: one is behind and failing, two is caught up. Tenant B: the
	// other way round, so each tenant's answer can only be its own.
	confirmSink(t, f.st, "tenant-a", siemOne, 1)
	if _, ok, err := f.st.Feed().Claim(t.Context(), "tenant-a", store.SinkConsumer(siemOne), audit.StreamControl, "n", time.Hour); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := f.st.Feed().Fail(t.Context(), "tenant-a", store.SinkConsumer(siemOne), audit.StreamControl, "n", time.Hour); err != nil {
		t.Fatal(err)
	}
	confirmSink(t, f.st, "tenant-a", siemTwo, 3)
	confirmSink(t, f.st, "tenant-b", siemOne, 2)

	want := map[store.Tenant]map[string]struct {
		behind  int
		failing bool
	}{
		"tenant-a": {siemOne: {1, true}, siemTwo: {0, false}},
		"tenant-b": {siemOne: {0, false}, siemTwo: {1, false}},
	}
	for tenant, sinks := range want {
		for _, row := range sinkRows(t, f, tenant) {
			switch row.Point {
			case ext.PointAuditSink.String():
				if row.Lag != nil {
					t.Errorf("%s: the sink point's row carries a lag of its own", tenant)
				}
				if len(row.Registrations) != 2 {
					t.Fatalf("%s: %d sink registrations", tenant, len(row.Registrations))
				}
				for _, reg := range row.Registrations {
					w, l := sinks[reg.Provider], reg.Lag
					if l == nil {
						t.Errorf("%s: %s has no lag", tenant, reg.Provider)
						continue
					}
					if l.StreamsBehind != w.behind || l.Failing != w.failing || l.OldestUnconfirmedAt.IsZero() != (w.behind == 0) ||
						l.SkippedByRetention == nil || *l.SkippedByRetention != 0 || l.CapturesWaiting != nil {
						t.Errorf("%s: %s lag %+v, want %d behind, failing %v, none skipped, no captures", tenant, reg.Provider, *l, w.behind, w.failing)
					}
				}
			case ext.PointArchiveStore.String():
				if row.Lag == nil || row.Lag.CapturesWaiting == nil || row.Lag.SkippedByRetention != nil || row.Lag.Failing {
					t.Errorf("%s: the archive's lag is %+v", tenant, row.Lag)
				}
				for _, reg := range row.Registrations {
					if reg.Lag != nil {
						t.Errorf("%s: the archive's registration carries a lag", tenant)
					}
				}
			default:
				if row.Lag != nil {
					t.Errorf("%s: the %s row carries a lag", tenant, row.Point)
				}
				for _, reg := range row.Registrations {
					if reg.Lag != nil {
						t.Errorf("%s: %s at %s carries a lag", tenant, reg.Provider, row.Point)
					}
				}
			}
		}
	}

	for _, row := range sinkRows(t, newServer(t), "tenant-a") {
		if row.Point == ext.PointAuditSink.String() && (row.Lag != nil || len(row.Registrations) != 0) {
			t.Errorf("with no sink registered the sink row is %+v", row)
		}
	}
}
