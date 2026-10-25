// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The role bindings and the outstanding certificates, paged (0034).
// TestEveryListIsBoundedOrSaysWhyNot holds both lists to M26's shape; these
// hold what only they promise.

// seedBindings binds each of bindings in tenant.
func seedBindings(t *testing.T, f *serverFixture, tenant store.Tenant, bindings ...store.RoleBinding) {
	t.Helper()
	for _, b := range bindings {
		if err := f.st.RoleBindings().Bind(t.Context(), tenant, b); err != nil {
			t.Fatalf("bind %+v in %s: %v", b, tenant, err)
		}
	}
}

// issueCertificates has the fixture's CA issue n certificates in tenant, to
// subjects `<tenant>-<i>@example.com`, oldest first.
func issueCertificates(t *testing.T, f *serverFixture, tenant store.Tenant, n int) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if _, err := f.ca.Issue(t.Context(), tenant, credential.IssueRequest{
			SubjectID: string(tenant) + "-" + string(rune('0'+i)) + "@example.com", Principals: []string{"deploy"},
			Target: "db-1.prod", PublicKey: wire.Marshal(), ValidFor: 30 * time.Minute,
		}); err != nil {
			t.Fatalf("issue in %s: %v", tenant, err)
		}
	}
}

type bindingPage struct {
	Bindings []struct {
		SubjectID string `json:"subject_id"`
		GroupID   string `json:"group_id"`
		Role      string `json:"role"`
	} `json:"bindings"`
	Roles     []string `json:"roles"`
	Truncated bool     `json:"truncated"`
	Next      string   `json:"next"`
}

// holders is each binding on the page as role/subject/id or role/group/id.
func (p bindingPage) holders() []string {
	var out []string
	for _, b := range p.Bindings {
		if b.GroupID != "" {
			out = append(out, b.Role+"/group/"+b.GroupID)
		} else {
			out = append(out, b.Role+"/subject/"+b.SubjectID)
		}
	}
	return out
}

// The bindings are listed per role, a subject's before a group's, each by its
// holder, and `roles` — the fixed set a binding may name — is on every page,
// the same each time.
func TestTheRoleBindingListIsOrderedByRoleAndHolderAndNamesItsRoles(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	seedBindings(t, f, "tenant-a",
		store.RoleBinding{GroupID: "zz-ops", Role: "auditor"},
		store.RoleBinding{SubjectID: "zz-ops", Role: "auditor"},
		store.RoleBinding{GroupID: "zz-sre", Role: "admin"},
		store.RoleBinding{SubjectID: "zz-bob", Role: "admin"},
		store.RoleBinding{SubjectID: "zz-alice", Role: "admin"},
	)

	whole := decode[bindingPage](t, f.do(t, "GET", "/api/v1/tenants/tenant-a/role-bindings", token, nil))
	var walked []string
	for path, pages := "/api/v1/tenants/tenant-a/role-bindings?limit=2", 0; ; pages++ {
		if pages > len(whole.Bindings) {
			t.Fatal("the walk did not end")
		}
		resp := f.do(t, "GET", path, token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s answered %d", path, resp.StatusCode)
		}
		page := decode[bindingPage](t, resp)
		if !slices.Equal(page.Roles, rolesOf(identity.AllRoles)) {
			t.Errorf("page %d names roles %v, want %v", pages, page.Roles, identity.AllRoles)
		}
		walked = append(walked, page.holders()...)
		if !page.Truncated {
			break
		}
		path = "/api/v1/tenants/tenant-a/role-bindings?limit=2&after=" + page.Next
	}
	if !slices.Equal(walked, whole.holders()) {
		t.Errorf("two a page walked %v, one page holds %v", walked, whole.holders())
	}
	var seeded []string
	for _, h := range walked {
		if strings.Contains(h, "/zz-") {
			seeded = append(seeded, h)
		}
	}
	want := []string{"admin/subject/zz-alice", "admin/subject/zz-bob", "admin/group/zz-sre", "auditor/subject/zz-ops", "auditor/group/zz-ops"}
	if !slices.Equal(seeded, want) {
		t.Errorf("the seeded bindings are listed %v, want %v", seeded, want)
	}
}

func rolesOf(roles []identity.Role) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, string(r))
	}
	return out
}

// Two tenants (M18): a position from tenant-a's bindings continues tenant-b's
// from the same place with tenant-b's bindings alone, the cursor names no
// tenant, and a tenant-a credential carrying it does not reach tenant-b.
func TestABindingCursorFromOneTenantReadsOnlyAnothersBindings(t *testing.T) {
	f := newServer(t)
	bind := func(tenant store.Tenant, subjects ...string) {
		t.Helper()
		for _, s := range subjects {
			seedBindings(t, f, tenant, store.RoleBinding{SubjectID: s, Role: "admin"})
		}
	}
	bind("tenant-a", "a-1", "m-a", "z-a")
	bind("tenant-b", "b-1", "n-b", "z-b")
	token := adminToken(t, f, "tenant-a", "tenant-b")

	first := decode[bindingPage](t, f.do(t, "GET", "/api/v1/tenants/tenant-a/role-bindings?limit=2", token, nil))
	if got := first.holders(); !slices.Equal(got, []string{"admin/subject/a-1", "admin/subject/m-a"}) || !first.Truncated {
		t.Fatalf("tenant-a's first page of two: %v (truncated %t)", got, first.Truncated)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first.Next)
	if err != nil {
		t.Fatalf("next %q: %v", first.Next, err)
	}
	if strings.Contains(string(raw), "tenant") {
		t.Errorf("the cursor %s names a tenant", raw)
	}

	rest := decode[bindingPage](t, f.do(t, "GET", "/api/v1/tenants/tenant-b/role-bindings?after="+first.Next, token, nil))
	if got := rest.holders(); !slices.Equal(got, []string{"admin/subject/n-b", "admin/subject/z-b"}) || rest.Truncated {
		t.Errorf("tenant-b after tenant-a's m-a answered %v (truncated %t), want only tenant-b's bindings after m-a", got, rest.Truncated)
	}

	onlyA := adminToken(t, f, "tenant-a")
	if resp := f.do(t, "GET", "/api/v1/tenants/tenant-b/role-bindings?after="+first.Next, onlyA, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a tenant-a credential carrying a cursor reached tenant-b: %d", resp.StatusCode)
	}
}

type certificatePage struct {
	Tenant       string            `json:"tenant"`
	Certificates []json.RawMessage `json:"certificates"`
	Truncated    bool              `json:"truncated"`
	Next         string            `json:"next"`
}

// subjects is each certificate's subject on the page, and it fails the test
// on a certificate whose bytes are in the answer.
func (p certificatePage) subjects(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, raw := range p.Certificates {
		var c map[string]any
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("certificate %s: %v", raw, err)
		}
		if _, ok := c["certificate"]; ok {
			t.Errorf("the list hands back a certificate: %s", raw)
		}
		subject, _ := c["subject"].(string)
		out = append(out, subject)
	}
	return out
}

// The certificates are listed newest first, every page names its tenant, and
// none carries the certificate itself. A position from tenant-a's list
// continues tenant-b's from the same instant with tenant-b's certificates
// alone (M18).
func TestTheCertificateListIsNewestFirstAndKeepsToItsTenant(t *testing.T) {
	f := newServer(t)
	issueCertificates(t, f, "tenant-b", 1) // tenant-b-0, the oldest of all
	issueCertificates(t, f, "tenant-a", 3) // tenant-a-0 … tenant-a-2
	issueCertificates(t, f, "tenant-b", 1) // tenant-b-0 again, the newest of all
	token := adminToken(t, f, "tenant-a", "tenant-b")

	read := func(path string) certificatePage {
		t.Helper()
		resp := f.do(t, "GET", path, token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s answered %d", path, resp.StatusCode)
		}
		return decode[certificatePage](t, resp)
	}
	var walked []string
	var firstNext string
	for path, pages := "/api/v1/tenants/tenant-a/ca/certificates?limit=1", 0; ; pages++ {
		if pages > 3 {
			t.Fatal("the walk did not end")
		}
		page := read(path)
		if page.Tenant != "tenant-a" {
			t.Errorf("page %d says it is %q", pages, page.Tenant)
		}
		walked = append(walked, page.subjects(t)...)
		if pages == 0 {
			firstNext = page.Next
		}
		if !page.Truncated {
			break
		}
		path = "/api/v1/tenants/tenant-a/ca/certificates?limit=1&after=" + page.Next
	}
	if want := []string{"tenant-a-2@example.com", "tenant-a-1@example.com", "tenant-a-0@example.com"}; !slices.Equal(walked, want) {
		t.Errorf("tenant-a's walk read %v, want %v", walked, want)
	}

	// After tenant-a's newest: tenant-b's certificate issued before it, and
	// not the one issued after.
	rest := read("/api/v1/tenants/tenant-b/ca/certificates?after=" + firstNext)
	if rest.Tenant != "tenant-b" || rest.Truncated {
		t.Errorf("tenant-b after tenant-a's position: tenant %q, truncated %t", rest.Tenant, rest.Truncated)
	}
	if got := rest.subjects(t); len(got) != 1 || got[0] != "tenant-b-0@example.com" {
		t.Errorf("tenant-b after tenant-a's position read %v, want its one certificate issued before it", got)
	}
	if whole := read("/api/v1/tenants/tenant-b/ca/certificates"); len(whole.Certificates) != 2 {
		t.Errorf("tenant-b's whole list holds %d certificates, want 2", len(whole.Certificates))
	}
}
