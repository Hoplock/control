// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/server"
)

// recordingArchive is a host's archive that keeps what Control handed it.
type recordingArchive struct {
	mu      sync.Mutex
	records []ext.AuditRecord
}

func (a *recordingArchive) Archive(_ context.Context, batch []ext.AuditRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, batch...)
	return nil
}

func (a *recordingArchive) ArchiveCaptures(context.Context, []ext.Capture) error { return nil }

func (a *recordingArchive) Search(context.Context, ext.ArchiveQuery) (ext.ArchivePage, error) {
	return ext.ArchivePage{}, nil
}

func (a *recordingArchive) held() []ext.AuditRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ext.AuditRecord(nil), a.records...)
}

// runHost migrates a fresh database and runs the test host through Main with a
// registry and extra configuration, until the test ends.
func runHost(t *testing.T, registry *ext.Registry, extra string) (base string, st *store.Store, logs *lockedBuffer) {
	t.Helper()
	dsn := testDatabase(t)
	south, north := freeAddr(t), freeAddr(t)
	path := writeFile(t, configDoc(south, north, dsn)+hostSection+extra)
	o := hostOptions(&sectionRecorder{}, registry)
	migrate(t, path, o)

	ctx, cancel := context.WithCancel(context.Background())
	logs = &lockedBuffer{}
	done := make(chan error, 1)
	go func() { done <- server.Main(ctx, []string{"--config", path}, io.Discard, logs, o) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Main: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("Main did not return after its context ended")
		}
	})
	base = "http://" + north
	waitServing(t, base+"/api/v1/session/methods", done, logs)
	st, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(st.Close)
	return base, st, logs
}

// resync records an operator act on the tenant's control stream.
func resync(t *testing.T, base, token string) {
	t.Helper()
	req, err := http.NewRequest("POST", base+"/api/v1/tenants/default/revocations", strings.NewReader(`{"type":"resync"}`))
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
		t.Fatalf("resync answered %d", resp.StatusCode)
	}
}

// A host's archive, registered through server.Options.Registry, is handed what
// the daemon records — here an operator's act on the control stream — and the
// extension listing shows its lag.
func TestTheDaemonFeedsTheArchiveAHostRegisters(t *testing.T) {
	archive := &recordingArchive{}
	registry := ext.NewRegistry()
	if err := registry.RegisterArchiveStore(ext.Registration{Provider: hostProvider + "/archive"}, archive); err != nil {
		t.Fatal(err)
	}
	base, st, logs := runHost(t, registry, "audit:\n  archive:\n    idle_interval: 20ms\n")
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	admin := issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAdmin}})
	resync(t, base, admin)

	deadline := time.Now().Add(10 * time.Second)
	for {
		held := archive.held()
		if len(held) > 0 && held[0].Stream == "control" && held[0].Tenant == "default" &&
			ext.ChainLink(held[0].Tenant, held[0].Stream, held[0].Sequence, held[0].PrevHash, held[0].Body) == held[0].ChainHash {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the archive holds %+v; want the control stream's record", held)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if line := logLine(t, logs.String(), "audit records are fed to a registered archive, and retention waits on it"); line["provider"] != hostProvider+"/archive" {
		t.Errorf("the start-up line names provider %v", line["provider"])
	}

	auditor := issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAuditor}})
	resp := get(t, base+"/api/v1/tenants/default/extensions", auditor)
	var out struct {
		Points []struct {
			Point string          `json:"point"`
			Lag   json.RawMessage `json:"lag"`
		} `json:"points"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for _, p := range out.Points {
		if (p.Point == ext.PointArchiveStore.String()) != (len(p.Lag) > 0) {
			t.Errorf("the %s row's lag is %s", p.Point, p.Lag)
		}
	}
}

// With no archive registered, no feed runs and no position is written.
func TestControlAloneFeedsNoArchiveAndWritesNoPosition(t *testing.T) {
	base, st, logs := runHost(t, nil, "")
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	resync(t, base, issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAdmin}}))
	time.Sleep(200 * time.Millisecond)
	var positions int
	if err := st.Pool().QueryRow(context.Background(), `SELECT count(*) FROM audit_feed_positions`).Scan(&positions); err != nil {
		t.Fatal(err)
	}
	if positions != 0 {
		t.Errorf("%d feed positions written with no archive registered", positions)
	}
	if strings.Contains(logs.String(), "archive_feed") {
		t.Error("a deployment with no archive logged an archive feed")
	}
}
