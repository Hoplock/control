// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The claim mapping editor's two reads over the wire (0068): the dry run and
// the tenant's directory sources.

const (
	validatePath    = "/api/v1/tenants/tenant-a/identity/claim-mapping/validate"
	directoriesPath = "/api/v1/tenants/tenant-a/identity/directories"
)

// previewDocument maps a claim with permitted values, one without, and a
// group name served by an unscoped entry and by one scoped to `lab`.
const previewDocument = `schema_version: 1
description: preview
claims:
  - claim: dept
    attribute: department
    values: [eng, ops]
  - claim: email
    attribute: email
groups:
  claim: groups
  map:
    - external: Engineers
      group: eng
    - external: Engineers
      directories: [lab]
      group: lab-eng
    - external: Admins
      group: admins
`

// countRecords is how many audit records the tenant holds.
func countRecords(t *testing.T, f *serverFixture) int {
	t.Helper()
	var n int
	if err := f.st.Pool().QueryRow(t.Context(), `SELECT count(*) FROM audit_records WHERE tenant = 'tenant-a'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The dry run stores nothing and records nothing: the active version, its
// digest, the audit store and the recalculation are as they were, whatever
// it is asked — a document that parses, one that does not, with samples.
func TestTheDryRunStoresAndRecordsNothing(t *testing.T) {
	f := newServer(t)
	auditor := f.roleToken(t, identity.RoleAuditor)
	if _, err := f.federation.PutMapping(t.Context(), "tenant-a", []byte(scopedDocument), "test", nil); err != nil {
		t.Fatal(err)
	}
	before, err := f.federation.ActiveMapping(t.Context(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	records := countRecords(t, f)

	for _, body := range []map[string]any{
		{"document": previewDocument},
		{"document": previewDocument, "samples": []map[string]any{{"claims": map[string]string{"dept": "eng"}}}},
		{"document": "schema_version: 2\n"},
	} {
		resp := f.send(t, "POST", validatePath, auditor, body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("dry run answered %d", resp.StatusCode)
		}
	}

	after, err := f.federation.ActiveMapping(t.Context(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != before.Version || after.Digest != before.Digest {
		t.Errorf("the active mapping moved from %d (%s) to %d (%s)", before.Version, before.Digest, after.Version, after.Digest)
	}
	if got := countRecords(t, f); got != records {
		t.Errorf("the dry run recorded %d audit records", got-records)
	}
	if starts := f.recalc.starts(); len(starts) != 0 {
		t.Errorf("the dry run started a recalculation: %v", starts)
	}
}

// A document the PUT refuses, the dry run refuses in exactly the same words:
// the status, the code, the field and problem, and every rejection with its
// field. An empty document is the PUT's `required`.
func TestTheDryRunRefusesAsTheWriteRefuses(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	for _, document := range []string{
		"schema_version: 2\n",
		"schema_version: 1\nclaims: [{claim: '', attribute: hoplock.x}]\n",
		strings.Replace(scopedDocument, "[de]", "[]", 1),
		strings.Replace(scopedDocument, "[jp, de, fr]", "[jp, de, de, Bad]", 1),
		"schema_version: 1\nattributes: []\n",
		"not: [valid",
		"  ",
	} {
		put := f.send(t, "PUT", "/api/v1/tenants/tenant-a/identity/claim-mapping", admin, map[string]any{"document": document})
		var refusedPut north.Error
		decodeInto(t, put, &refusedPut)
		dry := f.send(t, "POST", validatePath, admin, map[string]any{"document": document})
		var refusedDry north.Error
		decodeInto(t, dry, &refusedDry)

		if put.StatusCode != http.StatusBadRequest || dry.StatusCode != put.StatusCode {
			t.Fatalf("%q: PUT %d, dry run %d", document, put.StatusCode, dry.StatusCode)
		}
		refusedPut.CorrelationID, refusedDry.CorrelationID = "", ""
		if refusedDry.Code != refusedPut.Code || refusedDry.Message != refusedPut.Message ||
			!equalJSON(refusedDry.Parameters, refusedPut.Parameters) {
			t.Errorf("%q:\n  PUT     %+v\n  dry run %+v", document, refusedPut, refusedDry)
		}
	}
	if active, err := f.federation.ActiveMapping(t.Context(), "tenant-a"); err != nil || active.Version != 0 {
		t.Errorf("a refused write wrote version %d (%v)", active.Version, err)
	}
}

// Each sample is answered as the server's own mapping code maps it: a sign-in
// through Apply, its groups under `groups.claim`, matching unscoped entries
// only; a directory's identity through ApplyDirectory, a scoped entry for its
// source instead of the unscoped one. Both are held to Mapping's answer and to
// the values written out, so neither can drift from the other.
func TestTheDryRunMapsEachSampleAsTheServerWould(t *testing.T) {
	f := newServer(t)
	auditor := f.roleToken(t, identity.RoleAuditor)
	m, err := identity.ParseMapping([]byte(previewDocument))
	if err != nil {
		t.Fatal(err)
	}
	signIn := map[string]any{
		"claims": map[string]string{"dept": "eng", "email": "dana@example.com", "title": "ignored"},
		"groups": []string{"Engineers", "Admins", "Unknown"},
	}
	refused := map[string]any{"claims": map[string]string{"dept": "sales"}, "groups": []string{}}
	directory := map[string]any{
		"claims":           map[string]string{"dept": "ops", "email": "eli@example.com"},
		"groups":           []string{"Engineers"},
		"directory_source": "lab",
	}
	otherDirectory := map[string]any{
		"claims": map[string]string{}, "groups": []string{"Engineers"}, "directory_source": "corp",
	}

	resp := f.send(t, "POST", validatePath, auditor, map[string]any{
		"document": previewDocument, "samples": []any{signIn, refused, directory, otherDirectory},
	})
	var body struct {
		Digest             string   `json:"digest"`
		Attributes         []string `json:"attributes"`
		Groups             []string `json:"groups"`
		Directories        []string `json:"directories"`
		RequireDirectories *bool    `json:"require_directories"`
		GroupMap           []struct {
			External    string   `json:"external"`
			Group       string   `json:"group"`
			Directories []string `json:"directories"`
		} `json:"group_map"`
		Samples []struct {
			Attributes map[string]string `json:"attributes"`
			Groups     []string          `json:"groups"`
		} `json:"samples"`
	}
	decodeInto(t, resp, &body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if body.Digest != m.Digest || !slices.Equal(body.Attributes, []string{"department", "email"}) ||
		!slices.Equal(body.Groups, []string{"admins", "eng", "lab-eng"}) || !slices.Equal(body.Directories, []string{"lab"}) ||
		body.RequireDirectories == nil || *body.RequireDirectories || len(body.GroupMap) != 3 ||
		!slices.Equal(body.GroupMap[1].Directories, []string{"lab"}) {
		t.Errorf("the document's facts: %+v", body)
	}

	want := []identity.Attributes{
		m.Apply(identity.Assertion{
			Claims:      map[string]string{"dept": "eng", "email": "dana@example.com", "title": "ignored"},
			MultiClaims: map[string][]string{"groups": {"Engineers", "Admins", "Unknown"}},
		}),
		m.Apply(identity.Assertion{Claims: map[string]string{"dept": "sales"}, MultiClaims: map[string][]string{"groups": {}}}),
		m.ApplyDirectory("lab", map[string]string{"dept": "ops"}, "eli@example.com", []string{"Engineers"}),
		m.ApplyDirectory("corp", map[string]string{}, "", []string{"Engineers"}),
	}
	literal := []struct {
		attributes map[string]string
		groups     []string
	}{
		{map[string]string{"department": "eng", "email": "dana@example.com"}, []string{"admins", "eng"}},
		{map[string]string{}, []string{}},
		{map[string]string{"department": "ops", "email": "eli@example.com"}, []string{"lab-eng"}},
		{map[string]string{}, []string{"eng"}},
	}
	if len(body.Samples) != len(want) {
		t.Fatalf("%d samples answered, want %d", len(body.Samples), len(want))
	}
	for i, got := range body.Samples {
		if !maps.Equal(got.Attributes, want[i].Claims) || !slices.Equal(got.Groups, nonNil(want[i].Groups)) {
			t.Errorf("sample %d = %+v; the mapping's own code says %+v", i, got, want[i])
		}
		if !maps.Equal(got.Attributes, literal[i].attributes) || !slices.Equal(got.Groups, literal[i].groups) {
			t.Errorf("sample %d = %+v; want %+v", i, got, literal[i])
		}
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// The dry run's own refusals: more samples than it maps, and a sample whose
// directory is not spelled as a source. Each names its field.
func TestTheDryRunRefusesSamplesOutsideItsBounds(t *testing.T) {
	f := newServer(t)
	auditor := f.roleToken(t, identity.RoleAuditor)
	many := make([]map[string]any, north.MaxMappingSamples+1)
	for i := range many {
		many[i] = map[string]any{"claims": map[string]string{}}
	}
	e := requireRefusal(t, f.send(t, "POST", validatePath, auditor, map[string]any{"document": previewDocument, "samples": many}),
		http.StatusBadRequest, north.CodeInvalidRequest, "samples")
	if e.Parameters["problem"] != "too_many" || e.Parameters["limit"] != strconv.Itoa(north.MaxMappingSamples) {
		t.Errorf("too many samples: %+v", e.Parameters)
	}
	e = requireRefusal(t, f.send(t, "POST", validatePath, auditor, map[string]any{
		"document": previewDocument, "samples": []any{map[string]any{"directory_source": "Not A Source"}},
	}), http.StatusBadRequest, north.CodeInvalidRequest, "samples[0].directory_source")
	if e.Parameters["problem"] != "invalid" {
		t.Errorf("a misspelt source: %+v", e.Parameters)
	}
}

// Every role that reads identities may ask the dry run and read the sources;
// only identity:write is let write, and the refusal is the API's own.
func TestTheEditorsReadsAreIdentityReadAndTheWriteIsNot(t *testing.T) {
	f := newServer(t)
	f.provision(t, "dir-dana", "00u1", false)
	for _, role := range []identity.Role{identity.RoleAuditor, identity.RoleAdmin} {
		token := f.roleToken(t, role)
		dry := f.send(t, "POST", validatePath, token, map[string]any{"document": previewDocument})
		_ = dry.Body.Close()
		list := f.send(t, "GET", directoriesPath, token, nil)
		_ = list.Body.Close()
		if dry.StatusCode != http.StatusOK || list.StatusCode != http.StatusOK {
			t.Errorf("%s: dry run %d, sources %d", role, dry.StatusCode, list.StatusCode)
		}
	}
	auditor := f.roleToken(t, identity.RoleAuditor)
	requireRefusal(t, f.send(t, "PUT", "/api/v1/tenants/tenant-a/identity/claim-mapping", auditor, map[string]any{"document": previewDocument}),
		http.StatusForbidden, north.CodeForbidden, "")
}

// The sources list names each directory that has written in the tenant, with
// its provisioned identities and the deactivated ones among them counted.
func TestTheDirectorySourcesAreListedWithTheirIdentities(t *testing.T) {
	f := newServer(t)
	auditor := f.roleToken(t, identity.RoleAuditor)

	resp := f.send(t, "GET", directoriesPath, auditor, nil)
	var empty map[string]any
	decodeInto(t, resp, &empty)
	if d, ok := empty["directories"].([]any); !ok || len(d) != 0 || empty["truncated"] != false {
		t.Fatalf("a tenant with no directory: %v", empty)
	}

	f.provision(t, "dir-dana", "00u1", false)
	f.provision(t, "dir-eli", "00u2", true)
	now := time.Now().UTC()
	if err := f.st.DirectoryLinks().Put(t.Context(), "tenant-a", store.DirectoryLink{
		Source: "lab", ExternalID: "00u1", SubjectID: "dir-lab-1", Provider: "hoplock/scim",
		LinkedAt: now, Provisioned: true, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	resp = f.send(t, "GET", directoriesPath+"?limit=1", auditor, nil)
	var page struct {
		Directories []struct {
			Source      string `json:"source"`
			Linked      int64  `json:"linked"`
			Deactivated int64  `json:"deactivated"`
		} `json:"directories"`
		Truncated bool   `json:"truncated"`
		Next      string `json:"next"`
	}
	decodeInto(t, resp, &page)
	if len(page.Directories) != 1 || page.Directories[0].Source != "lab" || page.Directories[0].Linked != 1 ||
		page.Directories[0].Deactivated != 0 || !page.Truncated || page.Next == "" {
		t.Fatalf("first page: %+v", page)
	}
	resp = f.send(t, "GET", directoriesPath+"?after="+page.Next, auditor, nil)
	decodeInto(t, resp, &page)
	if len(page.Directories) != 1 || page.Directories[0].Source != "okta-a" || page.Directories[0].Linked != 2 ||
		page.Directories[0].Deactivated != 1 || page.Truncated {
		t.Errorf("second page: %+v", page)
	}
}

// A sample may name a provisioned directory identity, and is then mapped from
// what its directory last wrote — through provision.Mapped, the recalculation's
// own path — rather than from what the caller types. What a directory wrote is
// not otherwise readable here, so only identity:write is answered; a role that
// reads identities is refused by the API, not only by a hidden control. A name
// with anything else beside it, or one no directory provisioned, is refused.
func TestADryRunSampleMayNameADirectoryIdentityForAWriter(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	auditor := f.roleToken(t, identity.RoleAuditor)
	now := time.Now().UTC()
	if err := f.st.Subjects().Upsert(t.Context(), "tenant-a", store.Subject{ID: "dir-lab-1", Source: "hoplock/scim"}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.DirectoryGroups().Put(t.Context(), "tenant-a", store.DirectoryGroup{
		Source: "lab", ExternalID: "g-9", Provider: "hoplock/scim", Name: "Engineers",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.DirectoryLinks().Put(t.Context(), "tenant-a", store.DirectoryLink{
		Source: "lab", ExternalID: "00u1", SubjectID: "dir-lab-1", Provider: "hoplock/scim",
		LinkedAt: now, Provisioned: true, UpdatedAt: now, Email: "lab@example.com",
		Attributes: map[string]string{"dept": "ops"}, GroupIDs: []string{"g-9"},
	}); err != nil {
		t.Fatal(err)
	}
	named := map[string]any{"document": previewDocument, "samples": []any{map[string]any{"subject": "dir-lab-1"}}}

	resp := f.send(t, "POST", validatePath, admin, named)
	var body struct {
		Samples []struct {
			Attributes      map[string]string `json:"attributes"`
			Groups          []string          `json:"groups"`
			DirectorySource string            `json:"directory_source"`
		} `json:"samples"`
	}
	decodeInto(t, resp, &body)
	if resp.StatusCode != http.StatusOK || len(body.Samples) != 1 {
		t.Fatalf("named sample: %d %+v", resp.StatusCode, body)
	}
	got := body.Samples[0]
	if !maps.Equal(got.Attributes, map[string]string{"department": "ops", "email": "lab@example.com"}) ||
		!slices.Equal(got.Groups, []string{"lab-eng"}) || got.DirectorySource != "lab" {
		t.Errorf("named sample = %+v; want what the recalculation would write", got)
	}

	e := requireRefusal(t, f.send(t, "POST", validatePath, auditor, named), http.StatusForbidden, north.CodeForbidden, "")
	if e.Parameters["permission"] != string(identity.PermIdentityWrite) {
		t.Errorf("an auditor's named sample: %+v", e.Parameters)
	}
	for _, sample := range []map[string]any{
		{"subject": "dir-lab-1", "claims": map[string]string{}},
		{"subject": "dir-lab-1", "directory_source": "lab"},
		{"subject": "nobody"},
	} {
		requireRefusal(t, f.send(t, "POST", validatePath, admin, map[string]any{"document": previewDocument, "samples": []any{sample}}),
			http.StatusBadRequest, north.CodeInvalidRequest, "samples[0].subject")
	}
}
