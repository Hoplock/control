// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The grant lists, paged (0033). TestEveryListIsBoundedOrSaysWhyNot holds both
// lists and the grant view to M26's shape; these hold what only they promise.

// idsOf reads the id member of every item on a page.
func idsOf(t *testing.T, p listPage) []string {
	t.Helper()
	ids := make([]string, 0, len(p.items))
	for _, item := range p.items {
		var v struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(item, &v); err != nil {
			t.Fatalf("item %s: %v", item, err)
		}
		ids = append(ids, v.ID)
	}
	return ids
}

// A requester's list is the subject the middleware resolved, on every page
// (M18, 0016). The cursor alice is handed names neither her nor the tenant;
// presented by bob it continues HIS list from that place, and reads only his
// requests.
func TestARequestersCursorPresentedByAnotherReadsOnlyTheirOwnRequests(t *testing.T) {
	f := newServer(t)
	seedOwnRequests(t, f, "alice@example.com", "bob@example.com", "alice@example.com", "bob@example.com", "alice@example.com", "bob@example.com")
	alice, _ := f.requester(t, "alice@example.com")
	bob, _ := f.requester(t, "bob@example.com")

	first := readListPage(t, f.do(t, "GET", ownPath+"?limit=1", alice, nil), "requests")
	if !first.truncated || len(first.items) != 1 {
		t.Fatalf("alice's first page of one: %d items, truncated %t", len(first.items), first.truncated)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first.next)
	if err != nil {
		t.Fatalf("next %q: %v", first.next, err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("next decodes to %q: %v", raw, err)
	}
	if keys := slices.Sorted(maps.Keys(fields)); !slices.Equal(keys, []string{"f", "k", "l", "v"}) || fields["l"] != "own_requests" {
		t.Errorf("the cursor is %s, want exactly f, k, l, v for own_requests", raw)
	}
	if strings.Contains(string(raw), "alice") || strings.Contains(string(raw), "tenant") {
		t.Errorf("the cursor %s names its holder or its tenant", raw)
	}

	rest := readListPage(t, f.do(t, "GET", ownPath+"?after="+first.next, bob, nil), "requests")
	if rest.truncated || len(rest.items) != 3 {
		t.Fatalf("bob after alice's position: %d items (truncated %t), want his three", len(rest.items), rest.truncated)
	}
	for _, item := range rest.items {
		var v struct {
			Subject string `json:"subject"`
		}
		if err := json.Unmarshal(item, &v); err != nil || v.Subject != "bob@example.com" {
			t.Errorf("bob's page holds %s", item)
		}
	}
}

// Two tenants (M18): a position from tenant-a's grants continues tenant-b's
// with tenant-b's grants alone, and every page says which tenant it is.
func TestAGrantCursorFromOneTenantReadsOnlyAnothersGrants(t *testing.T) {
	f := newServer(t)
	seedGrants(t, f, "tenant-a", "a", "alice@example.com", 3)
	seedGrants(t, f, "tenant-b", "b", "alice@example.com", 3)
	token := adminToken(t, f, "tenant-a", "tenant-b")

	// grantPage is one page of the list as a client reads it.
	type grantPage struct {
		Tenant string `json:"tenant"`
		Grants []struct {
			ID string `json:"id"`
		} `json:"grants"`
		Next string `json:"next"`
	}
	read := func(path string) grantPage {
		t.Helper()
		resp := f.do(t, "GET", path, token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s answered %d", path, resp.StatusCode)
		}
		return decode[grantPage](t, resp)
	}

	first := read("/api/v1/tenants/tenant-a/grants?limit=1")
	if first.Tenant != "tenant-a" || first.Next == "" || len(first.Grants) != 1 || first.Grants[0].ID != "a-0" {
		t.Fatalf("tenant-a's first page: %+v", first)
	}
	var ids []string
	for next, pages := first.Next, 0; next != ""; pages++ {
		if pages > 3 {
			t.Fatal("tenant-b's walk did not end")
		}
		page := read("/api/v1/tenants/tenant-b/grants?limit=1&after=" + next)
		if page.Tenant != "tenant-b" {
			t.Errorf("tenant-b page %d says it is %q", pages, page.Tenant)
		}
		for _, g := range page.Grants {
			ids = append(ids, g.ID)
		}
		next = page.Next
	}
	// tenant-b's grants were seeded a moment after tenant-a's, a second
	// apart: its newest is ahead of the position, and the other two follow.
	if want := []string{"b-1", "b-2"}; !slices.Equal(ids, want) {
		t.Errorf("tenant-b after tenant-a's position read %v, want %v", ids, want)
	}
}

// A state is judged at the server's instant on every page (0033), so a grant
// can change state between pages, and the walk still visits each matching
// grant at most once: its place is its creation, which never moves.
func TestAGrantWalkUnderAStateFilterSurvivesExpiryMidWalk(t *testing.T) {
	start := time.Now().UTC()
	var mu sync.Mutex
	clock := start
	f := newServerWithGrants(t, access.Options{Now: func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}})
	token := adminToken(t, f, "tenant-a")

	// g-0 is the newest; g-2 expires once the clock has moved a minute on.
	for i, expires := range []time.Duration{time.Hour, time.Hour, 30 * time.Second, time.Hour} {
		id := fmt.Sprintf("g-%d", i)
		g := storetest.Grant(id, "alice@example.com", time.Hour)
		g.NotBefore, g.ExpiresAt = start.Add(-time.Hour), start.Add(expires)
		if err := f.st.Grants().Insert(t.Context(), "tenant-a", g); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		if _, err := f.st.Pool().Exec(t.Context(), `UPDATE grants SET created_at = $3 WHERE tenant = $1 AND grant_id = $2`,
			"tenant-a", id, start.Add(-time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("place %s: %v", id, err)
		}
	}

	base := "/api/v1/tenants/tenant-a/grants?state=active&limit=1"
	var seen []string
	next := ""
	for pages := 0; ; pages++ {
		if pages > 4 {
			t.Fatal("the walk did not end")
		}
		q := base
		if next != "" {
			q += "&after=" + next
		}
		p := readListPage(t, f.do(t, "GET", q, token, nil), "grants")
		seen = append(seen, idsOf(t, p)...)
		if !p.truncated {
			break
		}
		next = p.next
		mu.Lock()
		clock = clock.Add(time.Minute)
		mu.Unlock()
	}
	if want := []string{"g-0", "g-1", "g-3"}; !slices.Equal(seen, want) {
		t.Errorf("the active walk saw %v, want %v: each live grant once, and g-2 not once it had expired", seen, want)
	}
}

// A grant's view samples the newest 50 decisions it supplied, and says when
// that bound cut the sample (M26.3). It takes no cursor: the whole of what a
// grant backed is the audit query's.
func TestAGrantsViewSaysWhenItsDecisionSampleIsCut(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	at := time.Now().UTC()
	for grant, n := range map[string]int{"g-fifty": 50, "g-fifty-one": 51} {
		if err := f.st.Grants().Insert(t.Context(), "tenant-a", storetest.Grant(grant, "alice@example.com", time.Hour)); err != nil {
			t.Fatalf("seed %s: %v", grant, err)
		}
		for i := range n {
			if err := f.st.Decisions().Insert(t.Context(), "tenant-a", store.Decision{
				ID: fmt.Sprintf("%s-d-%02d", grant, i), SubjectID: "alice@example.com", InputsDigest: "x",
				Effect: store.DecisionEffectAllow, GrantID: grant, DecidedAt: at.Add(time.Duration(i) * time.Second),
			}); err != nil {
				t.Fatalf("seed decision: %v", err)
			}
		}
	}

	for grant, want := range map[string]bool{"g-fifty": false, "g-fifty-one": true} {
		t.Run(grant, func(t *testing.T) {
			resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/grants/"+grant, token, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("answered %d: %s", resp.StatusCode, decodeError(t, resp).Message)
			}
			body := decode[struct {
				Decisions []struct {
					DecisionID string `json:"decision_id"`
				} `json:"decisions"`
				Truncated *bool `json:"decisions_truncated"`
			}](t, resp)
			if body.Truncated == nil || *body.Truncated != want || len(body.Decisions) != 50 {
				t.Fatalf("%d decisions, decisions_truncated %v; want 50 and %t", len(body.Decisions), body.Truncated, want)
			}
			if newest := grant + "-d-49"; grant == "g-fifty" && body.Decisions[0].DecisionID != newest {
				t.Errorf("the sample starts at %s, want the newest, %s", body.Decisions[0].DecisionID, newest)
			}
			if newest := grant + "-d-50"; grant == "g-fifty-one" && body.Decisions[0].DecisionID != newest {
				t.Errorf("the sample starts at %s, want the newest, %s", body.Decisions[0].DecisionID, newest)
			}
		})
	}

	resp := f.do(t, "GET", "/api/v1/tenants/tenant-a/grants/nobody", token, nil)
	if resp.StatusCode != http.StatusNotFound || decodeError(t, resp).Code != north.CodeNotFound {
		t.Errorf("an absent grant answered %d, want 404", resp.StatusCode)
	}
}
