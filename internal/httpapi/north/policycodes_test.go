// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// POLICY'S REFUSALS AS CODES, OVER THE WIRE (0065; PLAN M19, M21). From
// version 8 each refusal is a code and typed parameters; a client declaring 7
// is served what it was served before.

// sendAt is a request declaring an API version ("" declares none).
func (f *serverFixture) sendAt(t *testing.T, declared, method, path, token string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, f.http.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if declared != "" {
		req.Header.Set("Hoplock-Api-Version", declared)
	}
	resp, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

type wireRejection struct {
	Code       string            `json:"code"`
	Line       int               `json:"line"`
	Parameters map[string]string `json:"parameters"`
	Message    string            `json:"message"`
}

type wireRefusal struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Parameters struct {
		Field      string          `json:"field"`
		Problem    string          `json:"problem"`
		Reason     string          `json:"reason"`
		Digest     string          `json:"digest"`
		Rejections []wireRejection `json:"rejections"`
	} `json:"parameters"`
}

// An unknown key arrives as its own code naming the key; at 7, as the code
// that carried it then, with the English as `detail`.
func TestARejectionIsACodeAndParametersFromVersion8(t *testing.T) {
	f := newServer(t)
	author := f.roleToken(t, identity.RolePolicyAuthor)
	source := strings.Replace(bundleFor(sreRule), "    effect: allow\n", "    effect: allow\n    permissions: root\n", 1)

	for _, declared := range []string{"", "8"} {
		resp := f.sendAt(t, declared, "POST", api+"/policy/validate", author, map[string]any{"source": source})
		mustStatus(t, resp, http.StatusOK)
		var rep struct{ Rejections []wireRejection }
		decodeInto(t, resp, &rep)
		if len(rep.Rejections) != 1 || rep.Rejections[0].Code != "policy.key_unknown" ||
			rep.Rejections[0].Parameters["key"] != "permissions" || len(rep.Rejections[0].Parameters) != 1 {
			t.Errorf("declared %q: %+v", declared, rep.Rejections)
		}
	}
	resp := f.sendAt(t, "7", "POST", api+"/policy/validate", author, map[string]any{"source": source})
	mustStatus(t, resp, http.StatusOK)
	var old struct{ Rejections []wireRejection }
	decodeInto(t, resp, &old)
	if len(old.Rejections) != 1 || old.Rejections[0].Code != "policy.document_malformed" ||
		!strings.Contains(old.Rejections[0].Parameters["detail"], "permissions") || old.Rejections[0].Line != 7 {
		t.Errorf("declared 7: %+v", old.Rejections)
	}

	// The upload's refusal says the same, in both.
	resp = f.sendAt(t, "", "POST", api+"/policy/bundles", author, map[string]any{"source": source})
	var up wireRefusal
	decodeInto(t, resp, &up)
	if resp.StatusCode != http.StatusUnprocessableEntity || len(up.Parameters.Rejections) != 1 ||
		up.Parameters.Rejections[0].Code != "policy.key_unknown" {
		t.Errorf("upload: %d %+v", resp.StatusCode, up)
	}
	resp = f.sendAt(t, "7", "POST", api+"/policy/bundles", author, map[string]any{"source": source})
	var upOld wireRefusal
	decodeInto(t, resp, &upOld)
	if len(upOld.Parameters.Rejections) != 1 || upOld.Parameters.Rejections[0].Code != "policy.document_malformed" ||
		upOld.Parameters.Rejections[0].Parameters["detail"] == "" {
		t.Errorf("upload declared 7: %+v", upOld)
	}
}

// A candidate diff that does not parse carries its rejections, as an upload
// does; at 7, the parser's text alone.
func TestAnUnparsedDiffCarriesItsRejections(t *testing.T) {
	f := newServer(t)
	author := f.roleToken(t, identity.RolePolicyAuthor)
	source := "schema_version: 1\ntenant: tenant-a\nrules:\n\t- id: r\n"

	resp := f.sendAt(t, "", "POST", api+"/policy/diff", author, map[string]any{"source": source})
	var e wireRefusal
	decodeInto(t, resp, &e)
	if resp.StatusCode != http.StatusUnprocessableEntity || e.Code != "policy_invalid" || e.Parameters.Reason != "" ||
		len(e.Parameters.Rejections) != 1 || e.Parameters.Digest == "" {
		t.Fatalf("diff: %d %+v", resp.StatusCode, e)
	}
	if r := e.Parameters.Rejections[0]; r.Code != "policy.document_malformed" || r.Line != 4 ||
		r.Parameters["problem"] != "tab_indentation" {
		t.Errorf("the rejection = %+v; want the tab named at its line", r)
	}

	resp = f.sendAt(t, "7", "POST", api+"/policy/diff", author, map[string]any{"source": source})
	var old wireRefusal
	decodeInto(t, resp, &old)
	if resp.StatusCode != http.StatusUnprocessableEntity || old.Parameters.Reason == "" || old.Parameters.Rejections != nil {
		t.Errorf("diff declared 7: %d %+v", resp.StatusCode, old)
	}
}

// A refused simulation names its field and problem; at 7, `body`, `invalid`
// and the reason.
func TestARefusedSimulationNamesItsFieldAndProblem(t *testing.T) {
	f := newServer(t)
	author := f.roleToken(t, identity.RolePolicyAuthor)
	source := bundleFor(sreRule)
	now := time.Now().UTC()
	for _, tc := range []struct {
		name           string
		body           map[string]any
		field, problem string
	}{
		{"nothing asked", map[string]any{"source": source}, "body", "nothing_asked"},
		{"source and version", map[string]any{"source": source, "version": 1, "cases": []any{map[string]any{}}}, "version", "ambiguous"},
		{"a window backwards", map[string]any{"source": source, "replay": map[string]any{"from": now, "to": now.Add(-time.Hour)}}, "replay.to", "not_after_start"},
		{"a case with no instant", map[string]any{"source": source, "cases": []any{map[string]any{"now": now}, map[string]any{}}}, "cases[1].now", "required"},
	} {
		resp := f.sendAt(t, "", "POST", api+"/policy/simulate", author, tc.body)
		var e wireRefusal
		decodeInto(t, resp, &e)
		if resp.StatusCode != http.StatusBadRequest || e.Code != "invalid_request" ||
			e.Parameters.Field != tc.field || e.Parameters.Problem != tc.problem || e.Parameters.Reason != "" || e.Message == "" {
			t.Errorf("%s: %d %+v, want {%s, %s}", tc.name, resp.StatusCode, e, tc.field, tc.problem)
		}
		resp = f.sendAt(t, "7", "POST", api+"/policy/simulate", author, tc.body)
		var old wireRefusal
		decodeInto(t, resp, &old)
		if old.Parameters.Field != "body" || old.Parameters.Problem != "invalid" || old.Parameters.Reason == "" {
			t.Errorf("%s declared 7: %+v", tc.name, old)
		}
	}
}

// An unreplayable record names its cause; at 7, its reason alone.
func TestAnUnreplayableRecordNamesItsCause(t *testing.T) {
	f := newServer(t)
	author := f.roleToken(t, identity.RolePolicyAuthor)
	if err := f.st.Decisions().Insert(t.Context(), "tenant-a", store.Decision{
		ID: "dec-bad", Effect: "deny", InputsDigest: "x", Inputs: json.RawMessage(`{}`),
		Explanation: json.RawMessage(`{"effect":"deny"}`), DecidedAt: time.Now(),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	body := map[string]any{"source": bundleFor(sreRule), "replay": map[string]any{
		"from": time.Now().Add(-time.Hour).UTC(), "to": time.Now().Add(time.Hour).UTC(),
	}}
	type unreplayable struct {
		DecisionID string            `json:"decision_id"`
		Code       *string           `json:"code"`
		Parameters map[string]string `json:"parameters"`
		Reason     string            `json:"reason"`
	}
	var res struct {
		Replay struct {
			Unreplayable []unreplayable `json:"unreplayable"`
		} `json:"replay"`
	}
	resp := f.sendAt(t, "", "POST", api+"/policy/simulate", author, body)
	mustStatus(t, resp, http.StatusOK)
	decodeInto(t, resp, &res)
	if u := res.Replay.Unreplayable; len(u) != 1 || u[0].Code == nil || *u[0].Code != "unreplayable.instant_missing" || u[0].Reason == "" {
		t.Fatalf("unreplayable = %+v", u)
	}
	resp = f.sendAt(t, "7", "POST", api+"/policy/simulate", author, body)
	mustStatus(t, resp, http.StatusOK)
	res.Replay.Unreplayable = nil
	decodeInto(t, resp, &res)
	if u := res.Replay.Unreplayable; len(u) != 1 || u[0].Code != nil || u[0].Parameters != nil || u[0].Reason == "" {
		t.Errorf("unreplayable declared 7 = %+v", u)
	}
}
