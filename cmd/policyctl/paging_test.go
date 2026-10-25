// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The version history and a session's decisions, as the server bounds them
// (M26, 0035): `versions` reads every page, and `explain -session` says when
// the server cut a session's decisions.

// `versions` prints every version over several pages, newest first, once each,
// against the real surface: 501 versions are two pages at the server's
// default limit, and the command takes no limit of its own.
func TestVersionsPrintsEveryPage(t *testing.T) {
	f := newFixture(t)
	const n = store.DefaultPageLimit + 1
	for v := int64(1); v <= n; v++ {
		if err := f.st.PolicyBundles().Insert(t.Context(), "default", storetest.Bundle(v, v == 7)); err != nil {
			t.Fatalf("insert v%d: %v", v, err)
		}
	}

	code, lines, errOut := f.run(t, "versions")
	if code != exitOK || len(lines) != n {
		t.Fatalf("versions = %d, %d lines, %s; want %d lines", code, len(lines), errOut, n)
	}
	for i, l := range lines {
		want := fmt.Sprintf("version\t%d\t", n-i)
		if !strings.HasPrefix(l, want) {
			t.Fatalf("line %d is %q, want it to start %q: every version once, newest first", i, l, want)
		}
		if active := strings.HasSuffix(l, "\tactive"); active != (n-i == 7) {
			t.Errorf("line %d is %q; version 7 alone is active", i, l)
		}
	}

	// -json is one answer in the server's shape, holding every page.
	code, lines, _ = f.run(t, "-json", "versions")
	var body struct {
		Versions []struct {
			Version int64 `json:"version"`
		} `json:"versions"`
		Truncated *bool   `json:"truncated"`
		Next      *string `json:"next"`
	}
	if code != exitOK || len(lines) != 1 || json.Unmarshal([]byte(lines[0]), &body) != nil {
		t.Fatalf("-json versions = %d %d lines", code, len(lines))
	}
	if len(body.Versions) != n || body.Versions[0].Version != n || body.Versions[n-1].Version != 1 ||
		body.Truncated == nil || *body.Truncated || body.Next != nil {
		t.Errorf("-json versions holds %d versions, truncated %v, next %v; want all %d, false and no next",
			len(body.Versions), body.Truncated, body.Next, n)
	}
}

// pagedServer answers the version list three versions a page, continuing from
// the cursor it minted, and answers failPage (1-based) with an outage.
func pagedServer(t *testing.T, total, failPage int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/policy/bundles") {
			http.NotFound(w, r)
			return
		}
		after := total + 1
		if raw := r.URL.Query().Get("after"); raw != "" {
			var err error
			if after, err = strconv.Atoi(strings.TrimPrefix(raw, "v")); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		if page := (total-after+1)/3 + 1; page == failPage {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":"internal","message":"the store is down","correlation_id":"c-1"}`))
			return
		}
		var versions []map[string]any
		v := after - 1
		for ; v >= 1 && len(versions) < 3; v-- {
			versions = append(versions, map[string]any{
				"version": v, "digest": "sha256:" + strconv.Itoa(v), "uploaded_by": "ci",
				"uploaded_at": time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), "active": false,
			})
		}
		body := map[string]any{"versions": versions, "truncated": v >= 1}
		if v >= 1 {
			body["next"] = "v" + strconv.Itoa(v+1)
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A failure on any page is the command's error (exit 2), and nothing is
// printed: the versions before it are never presented as the whole history.
func TestVersionsPrintsNothingWhenALaterPageFails(t *testing.T) {
	code, out, _ := runAgainst(t, pagedServer(t, 8, 0).URL, "versions")
	if code != exitOK || strings.Count(out, "version\t") != 8 || !strings.HasPrefix(out, "version\t8\t") {
		t.Fatalf("eight versions over three pages = %d %q", code, out)
	}
	for _, args := range [][]string{{"versions"}, {"-json", "versions"}} {
		code, out, errOut := runAgainst(t, pagedServer(t, 8, 2).URL, args...)
		if code != exitError || out != "" || !strings.Contains(errOut, "500") {
			t.Errorf("%v with its second page failing = %d, stdout %q, stderr %q; want exit 2 and nothing printed", args, code, out, errOut)
		}
	}
}

// `explain -session` warns on stderr when the server says it cut a session's
// decisions, prints the stories it was given, and exits 0. An answer that was
// not cut has no warning.
func TestExplainSessionWarnsWhenTheServerCutTheAnswer(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for session, n := range map[string]int{"sess-whole": 2, "sess-cut": 101} {
		for i := range n {
			if err := f.st.Decisions().Insert(t.Context(), "default", store.Decision{
				ID: fmt.Sprintf("%s-%03d", session, i), SubjectID: "alice@example.com", Effect: "deny",
				SessionID: session, ProxyID: "proxy-1", InputsDigest: "x",
				Inputs:      json.RawMessage(`{"now":"2026-10-01T12:00:00Z"}`),
				Explanation: json.RawMessage(`{"effect":"deny","rule":"closed","rule_line":3}`),
				DecidedAt:   at.Add(time.Duration(i) * time.Second),
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
	}

	code, lines, errOut := f.run(t, "explain", "-session", "sess-cut")
	if code != exitOK {
		t.Fatalf("explain -session on a cut answer = %d %s; the answer is still the server's", code, errOut)
	}
	if !strings.Contains(errOut, "warning") || !strings.Contains(errOut, "sess-cut") || !strings.Contains(errOut, "100") {
		t.Errorf("stderr is %q, want a warning naming the session and how many it was given", errOut)
	}
	if got := strings.Count(strings.Join(kinds(lines), "\n"), "decision"); got != 100 {
		t.Errorf("printed %d decisions, want the 100 the server answered", got)
	}
	if !strings.HasPrefix(lines[0], "decision\tsess-cut-100\t") {
		t.Errorf("the first story is %q, want the newest", lines[0])
	}

	code, lines, errOut = f.run(t, "-json", "explain", "-session", "sess-cut")
	if code != exitOK || len(lines) != 1 || !strings.Contains(errOut, "warning") {
		t.Errorf("-json explain -session on a cut answer = %d, %d lines, stderr %q; want the answer and the warning", code, len(lines), errOut)
	}

	code, lines, errOut = f.run(t, "explain", "-session", "sess-whole")
	if code != exitOK || errOut != "" || len(lines) == 0 {
		t.Errorf("explain -session on a whole answer = %d, stderr %q; want no warning", code, errOut)
	}
}
