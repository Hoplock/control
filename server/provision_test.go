// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/server"
)

// A host's identity sync, registered through server.Options.Registry (0040,
// M15): started once, after the registry is sealed and before either listener
// opens, handed a working sink, and stopped on shutdown.

// identitySync is a host's ext.IdentitySync that records how it was run.
type identitySync struct {
	startErr error
	probe    []string

	mu          sync.Mutex
	starts      int
	stops       int
	reachable   []string
	sink        ext.SyncSink
	stoppedDone chan struct{}
}

func (s *identitySync) Start(_ context.Context, sink ext.SyncSink) error {
	var reachable []string
	for _, addr := range s.probe {
		if conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			_ = conn.Close()
			reachable = append(reachable, addr)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts++
	s.reachable = reachable
	s.sink = sink
	return s.startErr
}

func (s *identitySync) Stop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stops++
	if s.stoppedDone != nil {
		close(s.stoppedDone)
		s.stoppedDone = nil
	}
	return nil
}

func (s *identitySync) snapshot() (starts, stops int, reachable []string, sink ext.SyncSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts, s.stops, append([]string(nil), s.reachable...), s.sink
}

func registryWithSync(t *testing.T, s *identitySync) *ext.Registry {
	t.Helper()
	registry := ext.NewRegistry()
	if err := registry.RegisterIdentitySync(ext.Registration{Provider: hostProvider + "/scim"}, s); err != nil {
		t.Fatal(err)
	}
	return registry
}

// syncRow reads the identity sync's row of the extension listing.
func syncRow(t *testing.T, base, token string) (registered bool, started *bool, startError string) {
	t.Helper()
	resp := get(t, base+"/api/v1/tenants/default/extensions", token)
	var out struct {
		Points []struct {
			Point      string `json:"point"`
			Registered bool   `json:"registered"`
			Started    *bool  `json:"started"`
			StartError string `json:"start_error"`
		} `json:"points"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for _, p := range out.Points {
		if p.Point == ext.PointIdentitySync.String() {
			return p.Registered, p.Started, p.StartError
		}
	}
	t.Fatal("the extension listing has no row for the identity sync")
	return false, nil, ""
}

func TestTheDaemonStartsTheIdentitySyncOnceBeforeAnyListenerAcceptsAndStopsItOnShutdown(t *testing.T) {
	south, north := freeAddr(t), freeAddr(t)
	sync := &identitySync{probe: []string{south, north}, stoppedDone: make(chan struct{})}
	stopped := sync.stoppedDone
	// Registered before the host runs, so it runs after the host has stopped.
	t.Cleanup(func() {
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("the sync was not stopped on shutdown")
		}
		_, stops, _, sink := sync.snapshot()
		if stops != 1 {
			t.Errorf("Stop was called %d times, want once", stops)
		}
		// A call that arrives once Stop has begun is KindUnavailable.
		if err := sink.UpsertSubject(context.Background(), ext.SyncedSubject{
			Tenant: "default", ExternalID: "late", Username: "late", Active: true, UpdatedAt: time.Now(),
		}); ext.KindOf(err) != ext.KindUnavailable {
			t.Errorf("UpsertSubject after shutdown began: %v, want KindUnavailable", err)
		}
	})

	base, st, _ := runHostAt(t, registryWithSync(t, sync), "", south, north)

	starts, _, reachable, sink := sync.snapshot()
	if starts != 1 {
		t.Fatalf("Start was called %d times, want once", starts)
	}
	if len(reachable) != 0 {
		t.Errorf("%v accepted a connection while Start was running: it has to run before either listener opens", reachable)
	}
	if sink == nil {
		t.Fatal("the sync was handed no sink")
	}

	// The sink works: a person provisioned is a subject the operator reads as
	// the directory's, and the deactivation is shown.
	ctx := context.Background()
	if err := sink.UpsertSubject(ctx, ext.SyncedSubject{
		Tenant: "default", ExternalID: "u-1", Username: "erin", Active: true, UpdatedAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("UpsertSubject: %v", err)
	}
	link, err := st.DirectoryLinks().Get(ctx, "default", "u-1")
	if err != nil {
		t.Fatalf("the provisioned link: %v", err)
	}
	if err := sink.DeactivateSubject(ctx, "default", "u-1", "terminated"); err != nil {
		t.Fatalf("DeactivateSubject: %v", err)
	}
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	auditor := issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAuditor}})
	resp := get(t, base+"/api/v1/tenants/default/subjects/"+link.SubjectID, auditor)
	var subject struct {
		DirectoryManaged bool `json:"directory_managed"`
		Directory        struct {
			Provider   string `json:"provider"`
			ExternalID string `json:"external_id"`
		} `json:"directory"`
		Deactivated bool `json:"deactivated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&subject); err != nil {
		t.Fatal(err)
	}
	if !subject.DirectoryManaged || subject.Directory.Provider != hostProvider+"/scim" ||
		subject.Directory.ExternalID != "u-1" || !subject.Deactivated {
		t.Errorf("the operator's view of the provisioned subject = %+v", subject)
	}

	registered, started, startError := syncRow(t, base, auditor)
	if !registered || started == nil || !*started || startError != "" {
		t.Errorf("the extension row = registered %v, started %v, error %q", registered, started, startError)
	}
}

// A sync whose Start fails does not stop Control: it serves, logs the failure
// at ERROR with the provider and the error's kind, and the listing shows the
// sync registered and not started.
func TestASyncWhoseStartFailsDoesNotStopControl(t *testing.T) {
	sync := &identitySync{startErr: ext.Errorf(ext.PointIdentitySync, hostProvider+"/scim", "Start", ext.KindInvalid, "no bearer token configured")}
	base, st, logs := runHost(t, registryWithSync(t, sync), "")

	if starts, _, _, _ := sync.snapshot(); starts != 1 {
		t.Fatalf("Start was called %d times, want once", starts)
	}
	line := logLine(t, logs.String(), "the identity sync failed to start; Control serves, and identities arrive at login")
	if line["level"] != "ERROR" || line["provider"] != hostProvider+"/scim" || line["kind"] != "invalid" {
		t.Errorf("the start-up failure was logged as %v", line)
	}
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	auditor := issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAuditor}})
	registered, started, startError := syncRow(t, base, auditor)
	if !registered || started == nil || *started || startError != "invalid" {
		t.Errorf("the extension row = registered %v, started %v, error %q; want registered, not started, invalid", registered, started, startError)
	}
	if resp := get(t, base+"/api/v1/session/methods", ""); resp.StatusCode != 200 {
		t.Errorf("Control answered %d with a sync that failed to start", resp.StatusCode)
	}
}

// With no sync registered no sink exists, nothing about the sync is logged, the
// point's row says nothing of a start, and no table of the sink's is written.
func TestControlWithNoIdentitySyncBuildsNoSink(t *testing.T) {
	base, st, logs := runHost(t, ext.NewRegistry(), "")

	if strings.Contains(logs.String(), "identity_sync") {
		t.Errorf("a deployment with no sync logged about one:\n%s", logs.String())
	}
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	auditor := issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAuditor}})
	registered, started, startError := syncRow(t, base, auditor)
	if registered || started != nil || startError != "" {
		t.Errorf("the extension row = registered %v, started %v, error %q; want an unregistered point", registered, started, startError)
	}
	var rows int
	if err := st.Pool().QueryRow(context.Background(), `SELECT count(*) FROM directory_links`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("%d directory links with no sync", rows)
	}
}

// A connector may name a directory claim only while a sync is registered: one
// no sync could ever match would refuse every login through the connector.
func TestADirectoryClaimNeedsARegisteredSync(t *testing.T) {
	dsn := testDatabase(t)
	path := writeFile(t, configDoc(freeAddr(t), freeAddr(t), dsn)+hostSection)
	connector := writeFile(t, `{"issuer":"https://idp.example.com","client_id":"control","redirect_uri":"https://control.example.com/cb"}`)

	migrate(t, path, hostOptions(&sectionRecorder{}, ext.NewRegistry()))
	put := func(registry *ext.Registry, extra ...string) (string, error) {
		o := hostOptions(&sectionRecorder{}, registry)
		var out bytes.Buffer
		args := append([]string{"identity", "connector-put", "--config", path, "--name", "azure", "--kind", "oidc", "--file", connector}, extra...)
		err := server.Main(context.Background(), args, &out, io.Discard, o)
		return out.String(), err
	}

	if _, err := put(ext.NewRegistry(), "--directory-claim", "oid"); err == nil || !strings.Contains(err.Error(), "no identity sync is registered") {
		t.Errorf("a directory claim with no sync: %v, want a refusal saying none is registered", err)
	}
	st, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Connectors().Get(context.Background(), "default", "azure"); !store.IsNotFound(err) {
		t.Errorf("a refused connector was written: %v", err)
	}

	if _, err := put(registryWithSync(t, &identitySync{}), "--directory-claim", "two words"); err == nil {
		t.Error("a malformed directory claim was accepted")
	}
	out, err := put(registryWithSync(t, &identitySync{}), "--directory-claim", "oid")
	if err != nil {
		t.Fatalf("a directory claim with a sync registered: %v", err)
	}
	if !strings.Contains(out, "directory_claim: oid") {
		t.Errorf("the command's output does not show the claim: %q", out)
	}
	c, err := st.Connectors().Get(context.Background(), "default", "azure")
	if err != nil || c.DirectoryClaim != "oid" {
		t.Errorf("the stored connector = %+v, %v", c, err)
	}
	// Without the flag, a connector is written as it always was.
	if _, err := put(ext.NewRegistry()); err != nil {
		t.Errorf("a connector with no directory claim and no sync: %v", err)
	}
}
