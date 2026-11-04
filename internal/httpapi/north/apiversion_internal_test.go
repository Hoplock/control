// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// futureView is a view as it would stand at version 3: one member from the
// start, one added in 2 with an absent value, and one added in 3.
type futureView struct {
	Name    string         `json:"name"`
	Labels  map[string]int `json:"labels,omitempty" since:"2"`
	Window  *time.Time     `json:"window" since:"3"`
	Nested  []futureChild  `json:"nested"`
	Ignored string         `json:"-"`
}

type futureChild struct {
	ID    string `json:"id"`
	Extra bool   `json:"extra" since:"3"`
}

// serveAt answers body through the negotiation, serving versions 1 to 3.
func serveAt(t *testing.T, declared string, body any) (*http.Response, map[string]any) {
	t.Helper()
	// These views are this test's, not the surface's: the answer-name probe
	// (which holds every `since` at or below APIVersion) is not shown them.
	probe := answerProbe
	answerProbe = nil
	defer func() { answerProbe = probe }()
	s := &Server{apiVersion: 3, minAPIVersion: 1}
	h := s.withAPIVersion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, body)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if declared != "" {
		req.Header.Set(APIVersionHeader, declared)
	}
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Result(), out
}

// A client declaring an older version never receives a member introduced
// after it: left out while it holds its absent value, and a 5xx — never a
// thinned body, never a 401 — when it does not.
func TestAnOlderClientNeverReceivesANewerMember(t *testing.T) {
	absent := futureView{Name: "a", Nested: []futureChild{{ID: "c"}}}

	resp, got := serveAt(t, "1", absent)
	if resp.StatusCode != http.StatusOK || resp.Header.Get(APIVersionHeader) != "1" {
		t.Fatalf("v1, absent values: %d served %s", resp.StatusCode, resp.Header.Get(APIVersionHeader))
	}
	for _, k := range []string{"labels", "window"} {
		if _, ok := got[k]; ok {
			t.Fatalf("v1 received %q, introduced later: %v", k, got)
		}
	}
	child := got["nested"].([]any)[0].(map[string]any)
	if _, ok := child["extra"]; ok {
		t.Fatalf("v1 received a nested member introduced in 3: %v", child)
	}
	if got["name"] != "a" || child["id"] != "c" {
		t.Fatalf("v1 lost a member it knows: %v", got)
	}

	// Version 2 knows labels, and is sent them.
	withLabels := absent
	withLabels.Labels = map[string]int{"x": 1}
	if _, got := serveAt(t, "2", withLabels); got["labels"] == nil {
		t.Fatalf("v2 was not sent labels: %v", got)
	}

	// A value v1 cannot express is a 5xx with a correlation id.
	for _, declared := range []string{"1", "2"} {
		at := time.Now()
		held := absent
		held.Window = &at
		resp, got := serveAt(t, declared, held)
		if resp.StatusCode != http.StatusInternalServerError || got["code"] != CodeAPIVersionUnexpressible {
			t.Fatalf("v%s holding a window: %d %v", declared, resp.StatusCode, got)
		}
		if _, ok := got["name"]; ok {
			t.Fatalf("v%s was sent a thinned body: %v", declared, got)
		}
	}

	// The current version is encoded exactly as encoding/json would.
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	full := futureView{Name: "a", Labels: map[string]int{"x": 1}, Window: &at, Nested: []futureChild{{ID: "c", Extra: true}}}
	_, got = serveAt(t, "3", full)
	want, _ := json.Marshal(full)
	var wantMap map[string]any
	_ = json.Unmarshal(want, &wantMap)
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(wantMap)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("v3 = %s, want %s", gotJSON, wantJSON)
	}
}

// Projection agrees with encoding/json on every member an older client may
// see: an answer projected to the version it was written at encodes the same.
func TestProjectionAgreesWithEncodingJSON(t *testing.T) {
	type inner struct {
		A int    `json:"a,omitempty"`
		B string `json:"b,string"`
	}
	type embedded struct {
		E string `json:"e"`
	}
	type view struct {
		embedded
		I   inner           `json:"i"`
		P   *inner          `json:"p"`
		Z   time.Time       `json:"z,omitzero"`
		M   map[string]any  `json:"m"`
		Raw json.RawMessage `json:"raw"`
		N   []int           `json:"n"`
	}
	v := view{embedded: embedded{E: "e"}, I: inner{B: "x"}, M: map[string]any{"k": []string{"v"}}, Raw: json.RawMessage(`{"q":1}`)}
	p, err := project(reflect.ValueOf(v), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(p)
	b, _ := json.Marshal(v)
	var am, bm any
	_ = json.Unmarshal(a, &am)
	_ = json.Unmarshal(b, &bm)
	aj, _ := json.Marshal(am)
	bj, _ := json.Marshal(bm)
	if string(aj) != string(bj) {
		t.Fatalf("projected %s, encoding/json %s", aj, bj)
	}
}
