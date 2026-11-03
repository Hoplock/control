// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
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
	south, north := freeAddrs(t)
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
			Tenant: "default", Source: "okta", ExternalID: "late", Username: "late", Active: true, UpdatedAt: time.Now(),
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
		Tenant: "default", Source: "okta", ExternalID: "u-1", Username: "erin", Active: true, UpdatedAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("UpsertSubject: %v", err)
	}
	link, err := st.DirectoryLinks().Get(ctx, "default", "okta", "u-1")
	if err != nil {
		t.Fatalf("the provisioned link: %v", err)
	}
	if err := sink.DeactivateSubject(ctx, ext.DirectoryRef{Tenant: "default", Source: "okta", ExternalID: "u-1"}, "terminated"); err != nil {
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
			Source     string `json:"source"`
			ExternalID string `json:"external_id"`
		} `json:"directory"`
		Deactivated bool `json:"deactivated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&subject); err != nil {
		t.Fatal(err)
	}
	if !subject.DirectoryManaged || subject.Directory.Provider != hostProvider+"/scim" || subject.Directory.Source != "okta" ||
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
	south, north := freeAddrs(t)
	path := writeFile(t, configDoc(south, north, dsn)+hostSection)
	connector := writeFile(t, `{"issuer":"https://idp.example.com","client_id":"control","redirect_uri":"https://control.example.com/cb"}`)

	migrate(t, path, hostOptions(&sectionRecorder{}, ext.NewRegistry()))
	put := func(registry *ext.Registry, extra ...string) (string, error) {
		o := hostOptions(&sectionRecorder{}, registry)
		var out bytes.Buffer
		args := append([]string{"identity", "connector-put", "--config", path, "--name", "azure", "--kind", "oidc", "--file", connector}, extra...)
		err := server.Main(context.Background(), args, &out, io.Discard, o)
		return out.String(), err
	}

	if _, err := put(ext.NewRegistry(), "--directory-claim", "oid", "--directory-source", "okta"); err == nil || !strings.Contains(err.Error(), "no identity sync is registered") {
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

	if _, err := put(registryWithSync(t, &identitySync{}), "--directory-claim", "two words", "--directory-source", "okta"); err == nil {
		t.Error("a malformed directory claim was accepted")
	}
	// A tie is made after the directory has provisioned (0042).
	provisionUnder(t, st, "okta")
	out, err := put(registryWithSync(t, &identitySync{}), "--directory-claim", "oid", "--directory-source", "okta")
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

// provisionUnder leaves a link in the default tenant under a source, as a sync
// that provisioned from that directory would.
func provisionUnder(t *testing.T, st *store.Store, source string) {
	t.Helper()
	if err := st.DirectoryLinks().Put(context.Background(), "default", store.DirectoryLink{
		Source: source, ExternalID: "u-" + source, SubjectID: "dir-" + source, Provider: hostProvider + "/scim",
		LinkedAt: time.Now().UTC(), Provisioned: true, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

// A tied connector names its claim and its source, both or neither; the
// source is held to its pattern and refused while nothing in the tenant comes
// from it, naming what does; and writing the connector with neither unties it
// (0042).
func TestATieToASourceNothingHasWrittenIsRefused(t *testing.T) {
	dsn := testDatabase(t)
	south, north := freeAddrs(t)
	path := writeFile(t, configDoc(south, north, dsn)+hostSection)
	connector := writeFile(t, `{"issuer":"https://idp.example.com","client_id":"control","redirect_uri":"https://control.example.com/cb"}`)
	migrate(t, path, hostOptions(&sectionRecorder{}, ext.NewRegistry()))
	put := func(extra ...string) (string, error) {
		o := hostOptions(&sectionRecorder{}, registryWithSync(t, &identitySync{}))
		var out bytes.Buffer
		args := append([]string{"identity", "connector-put", "--config", path, "--name", "azure", "--kind", "oidc", "--file", connector}, extra...)
		err := server.Main(context.Background(), args, &out, io.Discard, o)
		return out.String(), err
	}
	st, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	provisionUnder(t, st, "okta-a")
	provisionUnder(t, st, "azure-b")

	for _, tc := range []struct {
		name  string
		args  []string
		names string
	}{
		{"a source with no claim", []string{"--directory-source", "okta-a"}, "--directory-source needs --directory-claim"},
		{"a claim with no source", []string{"--directory-claim", "oid"}, "--directory-claim needs --directory-source"},
		{"a malformed source", []string{"--directory-claim", "oid", "--directory-source", "Okta_A"}, "--directory-source: it is not lower-case"},
		{"a source nothing has written", []string{"--directory-claim", "oid", "--directory-source", "okta"},
			`nothing in tenant default comes from the source "okta"; the sources it has are azure-b, okta-a`},
	} {
		if _, err := put(tc.args...); err == nil || !strings.Contains(err.Error(), tc.names) {
			t.Errorf("%s: %v, want a refusal containing %q", tc.name, err, tc.names)
		}
		if _, err := st.Connectors().Get(context.Background(), "default", "azure"); !store.IsNotFound(err) {
			t.Fatalf("%s: a refused connector was written: %v", tc.name, err)
		}
	}

	out, err := put("--directory-claim", "oid", "--directory-source", "okta-a")
	if err != nil {
		t.Fatalf("a tie to a written source: %v", err)
	}
	if !strings.Contains(out, "directory_claim: oid") || !strings.Contains(out, "directory_source: okta-a") {
		t.Errorf("the command's output does not show the tie: %q", out)
	}
	if c, err := st.Connectors().Get(context.Background(), "default", "azure"); err != nil || c.DirectoryClaim != "oid" || c.DirectorySource != "okta-a" {
		t.Errorf("the stored connector = %+v, %v", c, err)
	}
	if _, err := put(); err != nil {
		t.Fatalf("writing the connector with neither flag: %v", err)
	}
	if c, err := st.Connectors().Get(context.Background(), "default", "azure"); err != nil || c.DirectoryClaim != "" || c.DirectorySource != "" {
		t.Errorf("writing the connector with neither flag left %+v, %v; want it untied", c, err)
	}
}

// Two directories feeding one tenant through the registered sync keep their
// own ids: one id in both is two subjects, each read showing its own source,
// and a deactivation in one reaches nobody in the other (0042).
func TestARegisteredSyncFeedsTwoDirectoriesIntoOneTenant(t *testing.T) {
	sync := &identitySync{}
	base, st, _ := runHost(t, registryWithSync(t, sync), "")
	_, _, _, sink := sync.snapshot()
	if sink == nil {
		t.Fatal("the sync was handed no sink")
	}
	ctx := context.Background()
	for source, username := range map[string]string{"okta-a": "erin", "azure-b": "frank"} {
		if err := sink.UpsertSubject(ctx, ext.SyncedSubject{
			Tenant: "default", Source: source, ExternalID: "00u1", Username: username, Active: true,
			UpdatedAt: time.Now().Add(-time.Minute),
		}); err != nil {
			t.Fatalf("UpsertSubject from %s: %v", source, err)
		}
	}
	if err := sink.DeactivateSubject(ctx, ext.DirectoryRef{Tenant: "default", Source: "okta-a", ExternalID: "00u1"}, "terminated"); err != nil {
		t.Fatalf("DeactivateSubject: %v", err)
	}

	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	auditor := issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAuditor}})
	seen := map[string]bool{}
	for source, deactivated := range map[string]bool{"okta-a": true, "azure-b": false} {
		link, err := st.DirectoryLinks().Get(ctx, "default", source, "00u1")
		if err != nil {
			t.Fatalf("the link from %s: %v", source, err)
		}
		seen[link.SubjectID] = true
		resp := get(t, base+"/api/v1/tenants/default/subjects/"+link.SubjectID, auditor)
		var subject struct {
			Directory struct {
				Source     string `json:"source"`
				ExternalID string `json:"external_id"`
			} `json:"directory"`
			Deactivated bool `json:"deactivated"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&subject); err != nil {
			t.Fatal(err)
		}
		if subject.Directory.Source != source || subject.Directory.ExternalID != "00u1" || subject.Deactivated != deactivated {
			t.Errorf("the subject from %s reads %+v, want its own source and deactivated %v", source, subject, deactivated)
		}
	}
	if len(seen) != 2 {
		t.Errorf("one id in two directories is %d subjects, want two", len(seen))
	}
}

// putMapping writes a claim mapping through the API, as an operator does.
func putMapping(t *testing.T, base, token, document string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"document": document})
	req, err := http.NewRequest(http.MethodPut, base+"/api/v1/tenants/default/identity/claim-mapping", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT claim-mapping answered %d: %s", resp.StatusCode, b)
	}
}

// Two directories feed one tenant through a registered sync, each with a group
// called `admins`; a mapping that scopes `admins` to one of them is applied to
// both directories' subjects with no further sync call (0043).
func TestATightenedMappingReachesBothDirectoriesWithNoSyncCall(t *testing.T) {
	sync := &identitySync{}
	base, st, _ := runHost(t, registryWithSync(t, sync), "")
	_, _, _, sink := sync.snapshot()
	if sink == nil {
		t.Fatal("the sync was handed no sink")
	}
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	admin := issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAdmin}})
	putMapping(t, base, admin, "schema_version: 1\ngroups:\n  claim: groups\n  map:\n    - external: admins\n      group: all-admins\n")

	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	for source, username := range map[string]string{"okta-a": "erin", "azure-b": "frank"} {
		if err := sink.UpsertGroup(ctx, ext.SyncedGroup{
			Tenant: "default", Source: source, ExternalID: "g1", Name: "admins", UpdatedAt: at,
		}); err != nil {
			t.Fatalf("UpsertGroup from %s: %v", source, err)
		}
		if err := sink.UpsertSubject(ctx, ext.SyncedSubject{
			Tenant: "default", Source: source, ExternalID: "00u1", Username: username, Groups: []string{"g1"},
			Active: true, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("UpsertSubject from %s: %v", source, err)
		}
	}
	groupsOf := func(source string) []string {
		t.Helper()
		link, err := st.DirectoryLinks().Get(ctx, "default", source, "00u1")
		if err != nil {
			t.Fatal(err)
		}
		s, err := st.Subjects().Get(ctx, "default", link.SubjectID)
		if err != nil {
			t.Fatal(err)
		}
		return s.Groups
	}
	for _, source := range []string{"okta-a", "azure-b"} {
		if got := groupsOf(source); !slices.Equal(got, []string{"all-admins"}) {
			t.Fatalf("before scoping, the %s subject has %v", source, got)
		}
	}

	// Tightened, with no sync call after it: okta-a's admins is a-admins
	// alone, and under require_directories azure-b's maps nothing.
	putMapping(t, base, admin, "schema_version: 1\ngroups:\n  claim: groups\n  require_directories: true\n"+
		"  map:\n    - external: admins\n      directories: [okta-a]\n      group: a-admins\n"+
		"    - external: admins\n      group: all-admins\n")
	auditor := issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAuditor}})
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp := get(t, base+"/api/v1/tenants/default/identity/claim-mapping", auditor)
		var read struct {
			Directories []string `json:"directories"`
			Stale       int64    `json:"stale_directory_subjects"`
		}
		err := json.NewDecoder(resp.Body).Decode(&read)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(read.Directories, []string{"okta-a"}) {
			t.Fatalf("the read names directories %v", read.Directories)
		}
		if read.Stale == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d directory subjects never reached the new mapping", read.Stale)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := groupsOf("okta-a"); !slices.Equal(got, []string{"a-admins"}) {
		t.Errorf("the okta-a subject has %v, want a-admins alone", got)
	}
	if got := groupsOf("azure-b"); len(got) != 0 {
		t.Errorf("the azure-b subject has %v, want nothing under require_directories", got)
	}
}

// `identity mapping-put` prints the directories a mapping lists and whether it
// requires them, with no database on a dry run; stored, it names a listed
// source nothing has written without refusing it, and says how the server
// reaches the tenant's directory subjects; `mapping-show` prints how many are
// not yet on the active version (0043).
func TestTheMappingCommandsShowDirectoriesAndWhatRemains(t *testing.T) {
	dsn := testDatabase(t)
	south, north := freeAddrs(t)
	path := writeFile(t, configDoc(south, north, dsn)+hostSection)
	doc := writeFile(t, "schema_version: 1\ngroups:\n  claim: groups\n  require_directories: true\n  map:\n"+
		"    - external: admins\n      directories: [okta-a, emea]\n      group: a-admins\n")
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := server.Main(context.Background(), append([]string{"identity"}, args...), &out, io.Discard,
			hostOptions(&sectionRecorder{}, ext.NewRegistry()))
		return out.String(), err
	}

	// A dry run reads no configuration and opens no database.
	out, err := run("mapping-put", "--config", "/nonexistent.yaml", "--file", doc, "--dry-run")
	if err != nil || !strings.Contains(out, "directories its group entries list: emea, okta-a") ||
		!strings.Contains(out, "require_directories: true") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}

	migrate(t, path, hostOptions(&sectionRecorder{}, ext.NewRegistry()))
	st, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	provisionUnder(t, st, "okta-a")

	out, err = run("mapping-put", "--config", path, "--file", doc)
	if err != nil {
		t.Fatalf("mapping-put: %v\n%s", err, out)
	}
	if !strings.Contains(out, "directories nothing in tenant default has written yet: emea (") || strings.Contains(out, "yet: emea, okta-a") {
		t.Errorf("mapping-put does not name the unwritten source alone:\n%s", out)
	}
	if !strings.Contains(out, "recalculates the tenant's directory subjects") {
		t.Errorf("mapping-put does not say how the mapping reaches directory subjects:\n%s", out)
	}
	if m, err := identity.ActiveMapping(context.Background(), st, "default"); err != nil || m.Version != 1 {
		t.Errorf("the mapping was not stored: %v, %v", m, err)
	}

	out, err = run("mapping-show", "--config", path)
	if err != nil || !strings.Contains(out, "directory subjects not yet on this version: ") {
		t.Errorf("mapping-show: %v\n%s", err, out)
	}
}
