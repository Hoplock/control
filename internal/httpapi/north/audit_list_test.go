// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/store"
)

// The audit lists' cursors (0036, M26.2, M18). A cursor binds the selection
// the reader applies, spelt canonically, and the certificate join's serial;
// it never carries the tenant, so presented under another tenant's path it
// continues that tenant's records from the position, and only theirs.
func TestAnAuditCursorBindsItsSelectionAndCrossesNoTenant(t *testing.T) {
	f := newServer(t)
	seedAuditLists(t, f, "tenant-a")
	seedAuditLists(t, f, "tenant-b")
	token := adminToken(t, f, "tenant-a", "tenant-b")
	records := "/api/v1/tenants/tenant-a/audit/records"

	recordIDs := func(p listPage) []string {
		t.Helper()
		out := make([]string, 0, len(p.items))
		for _, item := range p.items {
			var v struct {
				RecordID string `json:"record_id"`
			}
			if err := json.Unmarshal(item, &v); err != nil {
				t.Fatalf("item %s: %v", item, err)
			}
			out = append(out, v.RecordID)
		}
		return out
	}

	// One tenant's position, another tenant's records.
	first := readListPage(t, f.do(t, "GET", withQuery(records, url.Values{"limit": {"1"}}), token, nil), "records")
	if !first.truncated {
		t.Fatal("tenant-a's first page of one does not continue")
	}
	rest := readListPage(t, f.do(t, "GET",
		withQuery("/api/v1/tenants/tenant-b/audit/records", url.Values{"after": {first.next}}), token, nil), "records")
	got := recordIDs(rest)
	theirs, err := f.st.Audit().Page(t.Context(), "tenant-b", store.AuditQuery{}, store.Page{Limit: store.MaxPageLimit})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range got {
		if !slices.ContainsFunc(theirs.Rows, func(r store.AuditRecord) bool { return r.RecordID == id }) {
			t.Errorf("tenant-b's list, continued from tenant-a's position, answered %s, which is not tenant-b's", id)
		}
	}
	if len(got) == 0 || len(got) >= len(theirs.Rows) {
		t.Errorf("tenant-b after tenant-a's first record answered %d of its %d records; the seeds share an instant, so it continues partway",
			len(got), len(theirs.Rows))
	}

	// The same kinds spelt in another order continue the list; one kind
	// fewer, a window, or the other order do not.
	kinds := url.Values{"kind": {"provisioning", "command"}, "limit": {"1"}}
	byKind := readListPage(t, f.do(t, "GET", withQuery(records, kinds), token, nil), "records")
	if !byKind.truncated {
		t.Fatal("the kind view's first page of one does not continue")
	}
	resp := f.do(t, "GET", withQuery(records, url.Values{"kind": {"command", "provisioning"}, "after": {byKind.next}}), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the same kinds in another order refused the cursor: %d", resp.StatusCode)
	}
	for _, other := range []url.Values{
		{"kind": {"command"}},
		{"kind": {"provisioning", "command"}, "from": {"2026-01-01T00:00:00Z"}},
		{"kind": {"provisioning", "command"}, "order": {"asc"}},
	} {
		other.Set("after", byKind.next)
		expectInvalid(t, f.do(t, "GET", withQuery(records, other), token, nil), "after", access.ProblemInvalid, "")
	}

	// The serial is in the path, binds the cursor, and is on every page.
	certs := "/api/v1/tenants/tenant-a/audit/certificates/" + boundedSerial + "/records"
	q := url.Values{"limit": {"1"}}
	for pages := 0; ; pages++ {
		resp := f.do(t, "GET", withQuery(certs, q), token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("page %d answered %d", pages, resp.StatusCode)
		}
		var body struct {
			Serial    string `json:"serial"`
			Truncated bool   `json:"truncated"`
			Next      string `json:"next"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Serial != boundedSerial {
			t.Errorf("page %d names serial %q, want %s on every page", pages, body.Serial, boundedSerial)
		}
		if pages == 0 {
			expectInvalid(t, f.do(t, "GET", withQuery("/api/v1/tenants/tenant-a/audit/certificates/10/records",
				url.Values{"after": {body.Next}}), token, nil), "after", access.ProblemInvalid, "")
		}
		if !body.Truncated {
			break
		}
		if pages > 10 {
			t.Fatal("the certificate join never ended")
		}
		q.Set("after", body.Next)
	}
}
