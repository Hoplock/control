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
	"github.com/hoplock/control/internal/audit"
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

// The showcase join and the negotiated sessions (0037) hold the same two
// promises: a cursor binds the selection its reader applies — the label and
// its value, the question asked, the window — and never the tenant, so
// tenant-a's position read in tenant-b continues tenant-b's list from that
// position, with tenant-b's items alone (M18).
func TestTheJoinAndTheNegotiatedSessionsBindTheirSelectionAndCrossNoTenant(t *testing.T) {
	f := newServer(t)
	seedAuditLists(t, f, "tenant-a")
	seedAuditLists(t, f, "tenant-b")
	token := adminToken(t, f, "tenant-a", "tenant-b")
	ctx := t.Context()
	window := "2026-01-01T00:00:00Z"
	prod := store.BlockedCommandQuery{LabelName: "env", LabelValue: "prod"}
	negotiated := store.AuditQuery{Negotiated: "key_exchanges:" + boundedKex}

	for _, c := range []struct {
		list, member string
		query        url.Values
		// id reads an item's id; after is tenant-b's list in the store,
		// continued from the position of tenant-a's item.
		id     func(json.RawMessage) string
		after  func(item json.RawMessage) []string
		others []url.Values // other selections the cursor is refused under
	}{
		{
			list: "blocked-commands", member: "blocked", query: url.Values{"label": {"env=prod"}},
			id: func(item json.RawMessage) string { return decodeBlocked(t, item).Record.RecordID },
			after: func(item json.RawMessage) []string {
				rec := decodeBlocked(t, item).Record
				got, err := f.st.Audit().BlockedCommands(ctx, "tenant-b", prod,
					store.Page{Limit: store.MaxPageLimit, After: []any{rec.RecordedAt, rec.Stream, rec.ChainSeq}})
				if err != nil {
					t.Fatal(err)
				}
				var out []string
				for _, b := range got.Rows {
					out = append(out, b.Record.RecordID)
				}
				return out
			},
			others: []url.Values{
				{"label": {"env=dev"}}, {"label": {"tier=prod"}},
				{"label": {"env=prod"}, "from": {window}}, {"label": {"env=prod"}, "order": {"asc"}},
			},
		},
		{
			list: "negotiated", member: "sessions",
			query: url.Values{"axis": {"key_exchanges"}, "value": {boundedKex}},
			id:    func(item json.RawMessage) string { return decodeSession(t, item).SessionID },
			after: func(item json.RawMessage) []string {
				s := decodeSession(t, item)
				got, err := f.st.Audit().Sessions(ctx, "tenant-b", negotiated,
					store.Page{Limit: store.MaxPageLimit, After: []any{s.LatestAt, s.SessionID}})
				if err != nil {
					t.Fatal(err)
				}
				var out []string
				for _, s := range got.Rows {
					out = append(out, s.SessionID)
				}
				return out
			},
			others: []url.Values{
				{"axis": {"key_exchanges"}, "value": {"curve25519-sha256"}},
				{"axis": {"ciphers"}, "value": {boundedKex}},
				{"floor": {"modern-kex"}},
				{"ban_axis": {"key_exchanges"}, "ban_name": {boundedKex}},
				{"axis": {"key_exchanges"}, "value": {boundedKex}, "to": {window}},
			},
		},
	} {
		t.Run(c.list, func(t *testing.T) {
			path := func(tenant string) string { return "/api/v1/tenants/" + tenant + "/audit/" + c.list }
			with := func(extra url.Values) url.Values {
				q := url.Values{}
				for k, v := range c.query {
					q[k] = v
				}
				for k, v := range extra {
					q[k] = v
				}
				return q
			}
			first := readListPage(t, f.do(t, "GET", withQuery(path("tenant-a"), with(url.Values{"limit": {"1"}})), token, nil), c.member)
			if !first.truncated || len(first.items) != 1 {
				t.Fatal("tenant-a's first page of one does not continue")
			}

			rest := readListPage(t, f.do(t, "GET", withQuery(path("tenant-b"), with(url.Values{"after": {first.next}})), token, nil), c.member)
			var got []string
			for _, item := range rest.items {
				got = append(got, c.id(item))
			}
			want := c.after(first.items[0])
			if len(want) == 0 || !slices.Equal(got, want) {
				t.Errorf("tenant-b after tenant-a's first item answered %v, want tenant-b's own after that position: %v", got, want)
			}

			for _, other := range c.others {
				other.Set("after", first.next)
				expectInvalid(t, f.do(t, "GET", withQuery(path("tenant-a"), other), token, nil), "after", access.ProblemInvalid, "")
			}
		})
	}
}

func decodeBlocked(t *testing.T, item json.RawMessage) store.BlockedCommand {
	t.Helper()
	var b store.BlockedCommand
	if err := json.Unmarshal(item, &b); err != nil {
		t.Fatalf("item %s: %v", item, err)
	}
	return b
}

func decodeSession(t *testing.T, item json.RawMessage) audit.NegotiatedSession {
	t.Helper()
	var s audit.NegotiatedSession
	if err := json.Unmarshal(item, &s); err != nil {
		t.Fatalf("item %s: %v", item, err)
	}
	return s
}
