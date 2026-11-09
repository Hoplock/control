// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/store"
)

// The group reads and the holder filters (0054, north-bound version 4).
// TestEveryListIsBoundedOrSaysWhyNot holds the group list and each filtered
// view to M26's shape; these hold what each answers.

type groupItem struct {
	ID      string   `json:"id"`
	Members int      `json:"member_count"`
	Roles   []string `json:"roles"`
	Record  *struct {
		DisplayName string `json:"display_name"`
		Source      string `json:"source"`
	} `json:"record"`
}

type groupPage struct {
	Groups    []groupItem `json:"groups"`
	Truncated bool        `json:"truncated"`
	Next      string      `json:"next"`
}

// A group is any name the tenant holds — on a subject, as a binding's holder,
// or as a record — listed once, with its members counted from the subjects
// and its roles from the bindings. A record is reported only where one
// exists, and another tenant's groups are not listed.
func TestTheGroupListIsEveryNameInTheTenantOnce(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	seedMembers(t, f, "tenant-a", map[string][]string{
		"zz-a@example.com": {"zz-sre", "zz-dev"},
		"zz-b@example.com": {"zz-sre"},
	})
	seedMembers(t, f, "tenant-b", map[string][]string{"zz-c@example.com": {"zz-other"}})
	seedBindings(t, f, "tenant-a",
		store.RoleBinding{GroupID: "zz-sre", Role: "fleet-admin"},
		store.RoleBinding{GroupID: "zz-sre", Role: "auditor"},
		store.RoleBinding{GroupID: "zz-auditors", Role: "auditor"})
	if err := f.st.Groups().Upsert(t.Context(), "tenant-a", store.Group{ID: "zz-oncall", DisplayName: "On call"}); err != nil {
		t.Fatal(err)
	}

	page := decode[groupPage](t, f.do(t, "GET", "/api/v1/tenants/tenant-a/groups", token, nil))
	got := map[string]groupItem{}
	var names []string
	for _, g := range page.Groups {
		if len(g.ID) > 3 && g.ID[:3] == "zz-" {
			got[g.ID] = g
			names = append(names, g.ID)
		}
	}
	if want := []string{"zz-auditors", "zz-dev", "zz-oncall", "zz-sre"}; !slices.Equal(names, want) {
		t.Fatalf("listed %v, want %v", names, want)
	}
	if g := got["zz-sre"]; g.Members != 2 || !slices.Equal(g.Roles, []string{"auditor", "fleet-admin"}) || g.Record != nil {
		t.Errorf("zz-sre = %+v, want 2 members, roles auditor and fleet-admin, no record", g)
	}
	if g := got["zz-auditors"]; g.Members != 0 || !slices.Equal(g.Roles, []string{"auditor"}) {
		t.Errorf("zz-auditors = %+v, want no members and the auditor role", g)
	}
	if g := got["zz-oncall"]; g.Record == nil || g.Record.DisplayName != "On call" || g.Record.Source != "local" || g.Roles == nil {
		t.Errorf("zz-oncall = %+v, want its local record and an empty role list", g)
	}

	one := decode[groupItem](t, f.do(t, "GET", "/api/v1/tenants/tenant-a/groups/zz-dev", token, nil))
	if one.ID != "zz-dev" || one.Members != 1 || len(one.Roles) != 0 {
		t.Errorf("GET …/groups/zz-dev = %+v", one)
	}
	for _, missing := range []string{"zz-none", "zz-other"} {
		if resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/groups/"+missing, token, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET …/groups/%s answered %d, want 404", missing, resp.StatusCode)
		}
	}
}

type subjectIDs struct {
	Subjects []struct {
		ID string `json:"id"`
	} `json:"subjects"`
}

// `?group=` answers the group's members and nobody else.
func TestTheSubjectListNarrowsToAGroupsMembers(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	seedMembers(t, f, "tenant-a", map[string][]string{
		"zz-a@example.com": {"zz-sre"}, "zz-b@example.com": {"zz-dev"}, "zz-c@example.com": {"zz-dev", "zz-sre"},
	})
	page := decode[subjectIDs](t, f.do(t, "GET", "/api/v1/tenants/tenant-a/subjects?group=zz-sre", token, nil))
	var ids []string
	for _, s := range page.Subjects {
		ids = append(ids, s.ID)
	}
	if want := []string{"zz-a@example.com", "zz-c@example.com"}; !slices.Equal(ids, want) {
		t.Errorf("members of zz-sre = %v, want %v", ids, want)
	}
}

// `?role=`, `?subject=` and `?group=` each keep their bindings; a role this
// build does not define and two holders at once are refused, as the grammar
// every malformed parameter uses.
func TestTheRoleBindingListNarrowsByRoleAndHolder(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	seedBindings(t, f, "tenant-a",
		store.RoleBinding{SubjectID: "zz-ops", Role: "auditor"},
		store.RoleBinding{SubjectID: "zz-ops", Role: "fleet-admin"},
		store.RoleBinding{GroupID: "zz-ops", Role: "grant-admin"},
		store.RoleBinding{GroupID: "zz-sre", Role: "auditor"})

	for query, want := range map[string][]string{
		"subject=zz-ops":            {"auditor/subject/zz-ops", "fleet-admin/subject/zz-ops"},
		"group=zz-ops":              {"grant-admin/group/zz-ops"},
		"role=auditor&group=zz-sre": {"auditor/group/zz-sre"},
	} {
		page := decode[bindingPage](t, f.do(t, "GET", "/api/v1/tenants/tenant-a/role-bindings?"+query, token, nil))
		if got := page.holders(); !slices.Equal(got, want) {
			t.Errorf("?%s = %v, want %v", query, got, want)
		}
	}
	auditors := decode[bindingPage](t, f.do(t, "GET", "/api/v1/tenants/tenant-a/role-bindings?role=auditor", token, nil))
	for _, h := range auditors.holders() {
		if h[:len("auditor/")] != "auditor/" {
			t.Errorf("?role=auditor listed %s", h)
		}
	}

	for query, field := range map[string]string{
		"role=root":                   "role",
		"subject=zz-ops&group=zz-ops": "group",
	} {
		resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/role-bindings?"+query, token, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("?%s answered %d, want 400", query, resp.StatusCode)
			continue
		}
		e := decode[north.Error](t, resp)
		if e.Code != "invalid_request" || e.Parameters["field"] != field {
			t.Errorf("?%s refused as %+v, want invalid_request on %s", query, e, field)
		}
	}
}

// A filter added to a list later binds only when it is used (M26.2: a
// cursor never expires): the unfiltered subject and role-binding lists mint
// cursors under the empty filter digest, as they did before `?group=`,
// `?role=` and `?subject=` existed, so a client's cursor from an earlier
// release still continues them.
func TestAnUnfilteredListStillBindsNoFilter(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	seedMembers(t, f, "tenant-a", map[string][]string{"zz-a@example.com": {"zz-sre"}, "zz-b@example.com": nil})
	seedBindings(t, f, "tenant-a",
		store.RoleBinding{SubjectID: "zz-a@example.com", Role: "auditor"},
		store.RoleBinding{GroupID: "zz-sre", Role: "auditor"})
	const none = "e3b0c44298fc1c149afbf4c8996fb924" // the digest of no filters
	for _, path := range []string{"/api/v1/tenants/tenant-a/subjects?limit=1", "/api/v1/tenants/tenant-a/role-bindings?limit=1"} {
		page := decode[struct {
			Next string `json:"next"`
		}](t, f.do(t, "GET", path, token, nil))
		raw, err := base64.RawURLEncoding.DecodeString(page.Next)
		if err != nil {
			t.Fatalf("%s: cursor %q: %v", path, page.Next, err)
		}
		var c struct {
			F string `json:"f"`
		}
		if err := json.Unmarshal(raw, &c); err != nil || c.F != none {
			t.Errorf("%s binds filter digest %q (%v), want the empty one %q", path, c.F, err, none)
		}
	}
}
