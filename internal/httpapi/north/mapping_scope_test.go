// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The claim mapping over the wire, with directory-scoped group entries and the
// recalculation a write starts (0043).

// recordingRecalculation is the recalculation as these tests see it: what was
// started and tracked, and nothing run.
type recordingRecalculation struct {
	mu      sync.Mutex
	started []store.Tenant
	tracked []store.Tenant
}

func (r *recordingRecalculation) Start(t store.Tenant) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, t)
}

func (r *recordingRecalculation) Track(t store.Tenant) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tracked = append(r.tracked, t)
}

func (r *recordingRecalculation) tracks() []store.Tenant {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.tracked)
}

func (r *recordingRecalculation) starts() []store.Tenant {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.started)
}

const scopedDocument = `schema_version: 1
groups:
  claim: groups
  require_directories: true
  map:
    - external: BigCorporationAdmins
      directories: [jp, de, fr]
      group: corp-admins
    - external: admins
      directories: [de]
      group: de-admins
    - external: engineering
      group: engineering
`

func TestTheClaimMappingReadShowsDirectoriesAndWhatIsStale(t *testing.T) {
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAuditor}})
	if _, err := f.federation.PutMapping(t.Context(), "tenant-a", []byte(scopedDocument), "test", nil); err != nil {
		t.Fatal(err)
	}
	// A directory subject derived before the mapping existed: on version 0.
	f.provision(t, "dir-dana", "00u1", false)

	resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/identity/claim-mapping", token, nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body struct {
		Version                int      `json:"version"`
		Groups                 []string `json:"groups"`
		Directories            []string `json:"directories"`
		RequireDirectories     bool     `json:"require_directories"`
		StaleDirectorySubjects *int64   `json:"stale_directory_subjects"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(body.Directories, []string{"de", "fr", "jp"}) || !body.RequireDirectories {
		t.Errorf("directories %v, require_directories %t", body.Directories, body.RequireDirectories)
	}
	if !slices.Equal(body.Groups, []string{"corp-admins", "de-admins", "engineering"}) {
		t.Errorf("groups %v: the scoped entries' groups are ones the mapping can produce", body.Groups)
	}
	if body.StaleDirectorySubjects == nil || *body.StaleDirectorySubjects != 1 {
		t.Errorf("stale_directory_subjects %v, want 1", body.StaleDirectorySubjects)
	}
	if !slices.Contains(f.recalc.tracks(), "tenant-a") {
		t.Error("a tenant whose mapping is read is not reported to the sweep")
	}
}

func TestAMappingWithNoDirectoriesReadsAsBefore(t *testing.T) {
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAuditor}})
	resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/identity/claim-mapping", token, nil)
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if d, ok := body["directories"].([]any); !ok || len(d) != 0 {
		t.Errorf("directories %v, want an empty list", body["directories"])
	}
	if body["require_directories"] != false || body["stale_directory_subjects"] != float64(0) {
		t.Errorf("require_directories %v, stale %v", body["require_directories"], body["stale_directory_subjects"])
	}
}

// The write starts the tenant's recalculation once it has committed, answers
// without waiting for it, and writes no subject itself.
func TestAMappingWriteStartsTheRecalculation(t *testing.T) {
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin}})
	f.provision(t, "dir-dana", "00u1", false)

	put := func(document string) *http.Response {
		b, _ := json.Marshal(map[string]string{"document": document})
		return f.do(t, "PUT", "/api/v1/tenants/tenant-a/identity/claim-mapping", token, strings.NewReader(string(b)))
	}

	bad := put(strings.Replace(scopedDocument, "[de]", "[]", 1))
	_ = bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest || len(f.recalc.starts()) != 0 {
		t.Fatalf("a rejected mapping answered %d and started %v", bad.StatusCode, f.recalc.starts())
	}

	resp := put(scopedDocument)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 3 || body["version"] != float64(1) {
		t.Errorf("the PUT's answer changed: %v", body)
	}
	if got := f.recalc.starts(); !slices.Equal(got, []store.Tenant{"tenant-a"}) {
		t.Errorf("started %v, want tenant-a once", got)
	}
	row, err := f.st.Subjects().Get(t.Context(), "tenant-a", "dir-dana")
	if err != nil || row.MappingVersion != 0 {
		t.Errorf("the write's transaction rewrote a subject: version %d, %v", row.MappingVersion, err)
	}
	if n, err := f.st.DirectoryLinks().StaleCount(t.Context(), "tenant-a", 1); err != nil || n != 1 {
		t.Errorf("stale after the write: %d, %v", n, err)
	}
}

func TestTheMappingsRejectionCodesReachTheCaller(t *testing.T) {
	f := newServer(t)
	token := f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin}})
	doc := "schema_version: 1\ngroups:\n  claim: groups\n  map:\n" +
		"    - external: a\n      directories: [Okta]\n      group: a\n" +
		"    - external: b\n      directories: []\n      group: b\n" +
		"    - external: c\n      directories: [de, de]\n      group: c\n"
	b, _ := json.Marshal(map[string]string{"document": doc})
	resp := f.do(t, "PUT", "/api/v1/tenants/tenant-a/identity/claim-mapping", token, strings.NewReader(string(b)))
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Parameters struct {
			Rejections []struct {
				Code  string `json:"code"`
				Field string `json:"field"`
			} `json:"rejections"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range body.Parameters.Rejections {
		got = append(got, r.Code+"@"+r.Field)
	}
	want := []string{
		identity.RejectMappingDirectoryName + "@groups.map[0].directories[0]",
		identity.RejectMappingDirectoriesEmpty + "@groups.map[1].directories",
		identity.RejectMappingDirectoryDup + "@groups.map[2].directories[1]",
	}
	if resp.StatusCode != http.StatusBadRequest || !slices.Equal(got, want) {
		t.Errorf("answered %d with %v, want %v", resp.StatusCode, got, want)
	}
}
