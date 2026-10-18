// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Signed retention over the wire (0024).

// With no key store, both retention routes answer 503
// retention_key_not_configured naming the setting, never a 401 (M11), and
// nothing is deleted.
func TestRetentionWithoutAKeyStoreIsRefusedOverTheWire(t *testing.T) {
	st := storetest.New(t)
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	ingest, err := audit.New(audit.Options{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	keyless, err := audit.NewEmitter(ingest)
	if err != nil {
		t.Fatal(err)
	}
	o := withLifecycle(t, st, north.Options{
		Federation: federation, Grants: newGrants(t, st, access.Options{}),
		DefaultTenant: "tenant-a", InsecureCookies: true,
	})
	o.Audit = keyless
	server, err := north.New(o)
	if err != nil {
		t.Fatal(err)
	}
	f := &serverFixture{st: st, federation: federation, server: server}
	f.http = httptest.NewServer(server)
	t.Cleanup(f.http.Close)
	admin := f.roleToken(t, identity.RoleAdmin)
	for range 4 {
		mustStatus(t, f.send(t, "POST", api+"/revocations", admin, map[string]any{"type": "resync"}), http.StatusOK)
	}

	for path, body := range map[string]any{
		"/audit/retention":          map[string]any{"stream": audit.StreamControl, "through_seq": 2},
		"/audit/captures/retention": map[string]any{"before": time.Now()},
	} {
		resp := f.send(t, "POST", api+path, admin, body)
		mustStatus(t, resp, http.StatusServiceUnavailable)
		e := decodeError(t, resp)
		if e.Code != north.CodeRetentionKeyNotConfigured || e.Parameters["config_key"] != "credential.key_encryption_key_env" || e.CorrelationID == "" {
			t.Errorf("%s answered %+v", path, e)
		}
	}
	if chain, _ := st.Audit().Chain(t.Context(), "tenant-a", audit.StreamControl, 0, 100); len(chain) != 4 {
		t.Errorf("%d of 4 records left after refused passes", len(chain))
	}
	if _, err := st.Audit().Retention(t.Context(), "tenant-a", audit.StreamControl); !store.IsNotFound(err) {
		t.Errorf("the marker moved: %v", err)
	}
}

// A pass over a marker whose seal does not hold answers 409
// retention_unverified naming the stream, and deletes nothing; a lawful pass
// answers the key that signed it.
func TestARetentionPassOverAnUnverifiedMarkerIsAConflict(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	for range 6 {
		mustStatus(t, f.send(t, "POST", api+"/revocations", admin, map[string]any{"type": "resync"}), http.StatusOK)
	}
	resp := f.send(t, "POST", api+"/audit/retention", admin, map[string]any{"stream": audit.StreamControl, "through_seq": 2})
	mustStatus(t, resp, http.StatusOK)
	var out struct {
		Key string `json:"key"`
	}
	decodeInto(t, resp, &out)
	if !audit.IsRetentionKey(out.Key) {
		t.Errorf("a lawful pass answered key %q", out.Key)
	}

	if _, err := f.st.Pool().Exec(t.Context(),
		`UPDATE audit_retention SET signature = '' WHERE tenant = 'tenant-a' AND stream = $1`, audit.StreamControl); err != nil {
		t.Fatal(err)
	}
	before, _ := f.st.Audit().Chain(t.Context(), "tenant-a", audit.StreamControl, 0, 100)
	resp = f.send(t, "POST", api+"/audit/retention", admin, map[string]any{"stream": audit.StreamControl, "through_seq": 4})
	mustStatus(t, resp, http.StatusConflict)
	if e := decodeError(t, resp); e.Code != north.CodeRetentionUnverified || e.Parameters["stream"] != audit.StreamControl {
		t.Errorf("a pass over an unsigned marker answered %+v", e)
	}
	if after, _ := f.st.Audit().Chain(t.Context(), "tenant-a", audit.StreamControl, 0, 100); len(after) != len(before) {
		t.Errorf("a refused pass changed the stream from %d records to %d", len(before), len(after))
	}
}
