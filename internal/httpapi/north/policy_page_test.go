// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The policy version history, paged, and a session's decisions, signalled
// (0035). TestEveryListIsBoundedOrSaysWhyNot holds both to M26's shapes; these
// hold what only they promise.

// uploadVersions uploads n distinct versions of a valid bundle in tenant-a
// through the surface, as an author would.
func uploadVersions(t *testing.T, f *serverFixture, tag string, n int) {
	t.Helper()
	token := adminToken(t, f, "tenant-a")
	for i := range n {
		resp := f.send(t, "POST", api+"/policy/bundles", token,
			map[string]any{"source": bundleFor(sreRule) + fmt.Sprintf("# %s %d\n", tag, i)})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("upload %s %d answered %d: %s", tag, i, resp.StatusCode, decodeError(t, resp).Message)
		}
	}
}

func countVersions(t *testing.T, f *serverFixture) int {
	t.Helper()
	got, err := f.st.PolicyBundles().Page(t.Context(), "tenant-a", store.Page{Limit: store.MaxPageLimit})
	if err != nil || got.More {
		t.Fatalf("count versions: %v (more=%t)", err, got.More)
	}
	return len(got.Rows)
}

// The history is newest first, and a version in it is what a version is —
// its number, digest, uploader, instant and whether it is active — never its
// source, however large: the answer is the same few hundred bytes whether the
// newest source is a line or over a megabyte.
func TestTheVersionListIsNewestFirstAndReadsNoSource(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	large := bytes.Repeat([]byte("# a large policy\n"), 1<<16)
	for v := int64(1); v <= 3; v++ {
		b := storetest.Bundle(v, false)
		if v == 3 {
			b.Source = large
		}
		if err := f.st.PolicyBundles().Insert(t.Context(), "tenant-a", b); err != nil {
			t.Fatalf("insert v%d: %v", v, err)
		}
	}
	if err := f.st.PolicyBundles().Activate(t.Context(), "tenant-a", 2); err != nil {
		t.Fatalf("activate: %v", err)
	}

	resp := f.do(t, "GET", api+"/policy/bundles", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("answered %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 2048 {
		t.Errorf("the list of three versions is %d bytes; a version's source is not in it", len(raw))
	}
	var body struct {
		Versions  []map[string]any `json:"versions"`
		Truncated *bool            `json:"truncated"`
		Next      *string          `json:"next"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.Truncated == nil || *body.Truncated || body.Next != nil {
		t.Errorf("one page of the whole history says truncated=%v next=%v, want false and no next", body.Truncated, body.Next)
	}
	var versions []float64
	for _, v := range body.Versions {
		if keys := slices.Sorted(maps.Keys(v)); !slices.Equal(keys, []string{"active", "digest", "uploaded_at", "uploaded_by", "version"}) {
			t.Errorf("a version answers %v; it is its number, digest, uploader, instant and state", keys)
		}
		versions = append(versions, v["version"].(float64))
		if want := v["version"].(float64) == 2; v["active"] != want {
			t.Errorf("version %v active=%v, want %t", v["version"], v["active"], want)
		}
	}
	if !slices.Equal(versions, []float64{3, 2, 1}) {
		t.Errorf("the history is %v, want newest first: [3 2 1]", versions)
	}

	// One version's route still answers it whole.
	resp = f.do(t, "GET", api+"/policy/bundles/3", token, nil)
	var one struct {
		Source string `json:"source"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&one) != nil || one.Source != string(large) {
		t.Errorf("GET …/policy/bundles/3 answered %d with %d bytes of source, want its whole source", resp.StatusCode, len(one.Source))
	}
}

// storyRecord is a decision a story can be told of: a deny with its instant
// and effect, the least a record needs (M4).
func storyRecord(id, session string, at time.Time) store.Decision {
	return store.Decision{
		ID: id, SubjectID: "alice@example.com", Effect: "deny", SessionID: session, ProxyID: "proxy-1",
		InputsDigest: "x", Inputs: json.RawMessage(`{"now":"` + at.Format(time.RFC3339Nano) + `"}`),
		Explanation: json.RawMessage(`{"effect":"deny","rule":"closed","rule_line":3}`), DecidedAt: at,
	}
}

// A session's decisions keep the server's bound of 100, the newest, and say
// when it cut them (M26.3). The list takes no limit and no cursor: both are
// ignored, and the answer is the same.
func TestASessionsDecisionsSayWhenTheirBoundCutsThem(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	at := time.Now().UTC()
	for session, n := range map[string]int{"sess-hundred": 100, "sess-past": 101} {
		for i := range n {
			if err := f.st.Decisions().Insert(t.Context(), "tenant-a",
				storyRecord(fmt.Sprintf("%s-%03d", session, i), session, at.Add(time.Duration(i)*time.Millisecond))); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
	}

	for session, want := range map[string]bool{"sess-hundred": false, "sess-past": true} {
		for _, q := range []string{"", "?limit=1", "?after=not-a-cursor"} {
			t.Run(session+q, func(t *testing.T) {
				resp := f.do(t, "GET", api+"/sessions/"+session+"/decisions"+q, token, nil)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("answered %d: %s", resp.StatusCode, decodeError(t, resp).Message)
				}
				body := decode[struct {
					SessionID string `json:"session_id"`
					Decisions []struct {
						DecisionID string `json:"decision_id"`
					} `json:"decisions"`
					Truncated *bool `json:"truncated"`
				}](t, resp)
				if body.Truncated == nil || *body.Truncated != want || len(body.Decisions) != 100 {
					t.Fatalf("%d decisions, truncated %v; want 100 and %t", len(body.Decisions), body.Truncated, want)
				}
				newest := fmt.Sprintf("%s-%03d", session, map[bool]int{false: 99, true: 100}[want])
				if body.Decisions[0].DecisionID != newest || body.SessionID != session {
					t.Errorf("the answer for %s starts at %s, want the newest, %s", body.SessionID, body.Decisions[0].DecisionID, newest)
				}
			})
		}
	}

	resp := f.do(t, "GET", api+"/sessions/sess-nobody/decisions", token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a session with no decision answered %d, want 404", resp.StatusCode)
	}
}
