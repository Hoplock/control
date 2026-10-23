// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// resyncIn records an operator act on a tenant's control stream.
func resyncIn(t *testing.T, base string, tenant store.Tenant, token string) {
	t.Helper()
	req, err := http.NewRequest("POST", base+"/api/v1/tenants/"+string(tenant)+"/revocations", strings.NewReader(`{"type":"resync"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resync in %s answered %d", tenant, resp.StatusCode)
	}
}

// recordingSink is a host's sink that keeps what Control handed it.
type recordingSink struct {
	mu      sync.Mutex
	records []ext.AuditRecord
}

func (s *recordingSink) Export(_ context.Context, batch []ext.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, batch...)
	return nil
}

func (s *recordingSink) held() []ext.AuditRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ext.AuditRecord(nil), s.records...)
}

// Every sink a host registers through server.Options.Registry is handed what
// the daemon records — here an operator's act on the control stream — and the
// extension listing shows each one's lag on its registration.
func TestTheDaemonDeliversToEverySinkAHostRegisters(t *testing.T) {
	one, two := &recordingSink{}, &recordingSink{}
	registry := ext.NewRegistry()
	for provider, s := range map[string]*recordingSink{hostProvider + "/siem-one": one, hostProvider + "/siem-two": two} {
		if err := registry.RegisterAuditSink(ext.Registration{Provider: provider}, s); err != nil {
			t.Fatal(err)
		}
	}
	base, st, logs := runHost(t, registry, "audit:\n  sinks:\n    idle_interval: 20ms\n")
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	resync(t, base, issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAdmin}}))
	// A tenant other than the configured one, which the sinks learn of only
	// because a record is written in it.
	resyncIn(t, base, "acme", issue(t, federation, "acme", map[store.Tenant]identity.RoleSet{"acme": {identity.RoleAdmin}}))

	for _, s := range []*recordingSink{one, two} {
		deadline := time.Now().Add(10 * time.Second)
		for {
			seen := map[ext.Tenant]bool{}
			for _, r := range s.held() {
				if r.Stream == "control" && ext.ChainLink(r.Tenant, r.Stream, r.Sequence, r.PrevHash, r.Body) == r.ChainHash {
					seen[r.Tenant] = true
				}
			}
			if seen["default"] && seen["acme"] {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("a sink holds %+v; want each tenant's control stream record", s.held())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	line := logLine(t, logs.String(), "audit records are delivered to every registered sink, and retention does not wait on them")
	if providers, _ := line["providers"].([]any); len(providers) != 2 {
		t.Errorf("the start-up line names providers %v", line["providers"])
	}

	auditor := issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAuditor}})
	resp := get(t, base+"/api/v1/tenants/default/extensions", auditor)
	var out struct {
		Points []struct {
			Point         string `json:"point"`
			Registrations []struct {
				Provider string          `json:"provider"`
				Lag      json.RawMessage `json:"lag"`
			} `json:"registrations"`
		} `json:"points"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	var lagged int
	for _, p := range out.Points {
		for _, r := range p.Registrations {
			if len(r.Lag) > 0 {
				lagged++
				if p.Point != ext.PointAuditSink.String() {
					t.Errorf("%s at %s carries a lag", r.Provider, p.Point)
				}
			}
		}
	}
	if lagged != 2 {
		t.Errorf("%d registrations carry a lag; want each sink's", lagged)
	}
}

// With no sink registered, no delivery runs and no sink position is written —
// and with an archive registered and no sink, only the archive's are.
func TestControlWithNoSinkDeliversNothingAndWritesNoSinkPosition(t *testing.T) {
	for name, archived := range map[string]bool{"alone": false, "with an archive": true} {
		t.Run(name, func(t *testing.T) {
			registry := ext.NewRegistry()
			if archived {
				if err := registry.RegisterArchiveStore(ext.Registration{Provider: hostProvider + "/archive"}, &recordingArchive{}); err != nil {
					t.Fatal(err)
				}
			}
			base, st, logs := runHost(t, registry, "audit:\n  archive:\n    idle_interval: 20ms\n")
			federation, err := identity.NewFederation(st)
			if err != nil {
				t.Fatal(err)
			}
			resync(t, base, issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAdmin}}))
			time.Sleep(200 * time.Millisecond)
			var sinkRows, archiveRows int
			if err := st.Pool().QueryRow(context.Background(), `
				SELECT count(*) FILTER (WHERE consumer <> 'archive'), count(*) FILTER (WHERE consumer = 'archive')
				FROM audit_feed_positions`).Scan(&sinkRows, &archiveRows); err != nil {
				t.Fatal(err)
			}
			if sinkRows != 0 || (archiveRows > 0) != archived {
				t.Errorf("%d sink and %d archive positions written", sinkRows, archiveRows)
			}
			if strings.Contains(logs.String(), "audit_sink") {
				t.Error("a deployment with no sink logged sink delivery")
			}
		})
	}
}
