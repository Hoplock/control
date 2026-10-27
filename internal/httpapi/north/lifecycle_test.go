// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/authoring"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The 0019 surface over the wire: the policy lifecycle, the overrides, the
// audit trail of every act, the operator actions and the error envelope.

func (f *serverFixture) roleToken(t *testing.T, roles ...identity.Role) string {
	t.Helper()
	return f.token(t, "tenant-a", map[store.Tenant]identity.RoleSet{"tenant-a": roles})
}

func (f *serverFixture) send(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case nil:
	case string:
		raw = []byte(b)
	default:
		var err error
		if raw, err = json.Marshal(b); err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	var reader *bytes.Reader
	if raw != nil {
		reader = bytes.NewReader(raw)
		return f.do(t, method, path, token, reader)
	}
	return f.do(t, method, path, token, nil)
}

func decodeInto(t *testing.T, resp *http.Response, into any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		t.Fatalf("decode %d body: %v", resp.StatusCode, err)
	}
}

func mustStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		var e north.Error
		_ = json.NewDecoder(resp.Body).Decode(&e)
		t.Fatalf("%s %s answered %d (%s: %s %v), want %d",
			resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, e.Code, e.Message, e.Parameters, want)
	}
}

const api = "/api/v1/tenants/tenant-a"

func bundleFor(rules string) string {
	return "schema_version: 1\ntenant: tenant-a\ngroups: [sre]\nrules:\n" + rules
}

const sreRule = `  - id: sre
    effect: allow
    match:
      subject:
        groups: [sre]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-key
          username: netadmin
          credential_ref: vault://x
`

func restrictedRule(executable string) string {
	return `  - id: logs
    effect: allow
    match:
      target:
        hostnames: [app.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: whitelist
        exec_mode: restricted
        restricted_exec:
          commands:
            - executable: ` + executable + `
              form: exact
              argv: ["x"]
      credentials:
        - method: ephemeral-user
          username: svc
          key_type: ed25519
      enforcement:
        execution: account-restricted
`
}

func (f *serverFixture) operatorRecords(t *testing.T, event string) []store.AuditRecord {
	t.Helper()
	rows, err := f.st.Audit().Query(t.Context(), "tenant-a", store.AuditQuery{Event: event, Limit: 100})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	return rows
}

// ---------------------------------------------------------------------------
// role enforcement, per route
// ---------------------------------------------------------------------------

// Every one of Control's tenant routes, called by every role: refused with
// `forbidden` exactly when the role lacks the route's permission, and never a
// 5xx when it holds it. The routes come from the router, so a route added
// later is covered on the day it is added.
func TestRoleEnforcementPerRoute(t *testing.T) {
	f := newServer(t)
	tokens := map[identity.Role]string{}
	for _, role := range identity.AllRoles {
		tokens[role] = f.roleToken(t, role)
	}
	covered := 0
	for _, route := range f.server.Routes() {
		if route.Access != "tenant" || route.Provider != "" {
			continue
		}
		covered++
		path := samplePath(route.Pattern, "tenant-a")
		for _, role := range identity.AllRoles {
			allowed := role.Can(identity.Permission(route.Permission))
			t.Run(route.Method+" "+route.Pattern+" as "+string(role), func(t *testing.T) {
				resp := f.do(t, route.Method, path, tokens[role], bytes.NewReader([]byte(`{}`)))
				if !allowed {
					if resp.StatusCode != http.StatusForbidden {
						t.Fatalf("answered %d, want 403: %s does not hold %s", resp.StatusCode, role, route.Permission)
					}
					if e := decodeError(t, resp); e.Code != north.CodeForbidden || e.Parameters["permission"] != string(route.Permission) {
						t.Fatalf("refusal = %s %v, want forbidden naming %s", e.Code, e.Parameters, route.Permission)
					}
					return
				}
				if resp.StatusCode >= 500 {
					t.Fatalf("answered %d to a caller holding %s", resp.StatusCode, route.Permission)
				}
				if resp.StatusCode == http.StatusForbidden {
					if e := decodeError(t, resp); e.Code == north.CodeForbidden {
						t.Fatalf("refused %s, which holds %s", role, route.Permission)
					}
				}
			})
		}
	}
	if covered < 50 {
		t.Fatalf("only %d tenant routes were walked", covered)
	}
}

// The case the prompt names: an auditor reads everything and activates nothing.
func TestAnAuditorIsRefusedAPolicyActivation(t *testing.T) {
	f := newServer(t)
	author, auditor := f.roleToken(t, identity.RolePolicyAuthor), f.roleToken(t, identity.RoleAuditor)
	resp := f.send(t, "POST", api+"/policy/bundles", author, map[string]any{"source": bundleFor(sreRule)})
	mustStatus(t, resp, http.StatusCreated)

	resp = f.send(t, "POST", api+"/policy/bundles/1/activate", auditor, map[string]any{})
	mustStatus(t, resp, http.StatusForbidden)
	if e := decodeError(t, resp); e.Code != north.CodeForbidden || e.Parameters["permission"] != string(identity.PermPolicyPublish) {
		t.Errorf("refusal = %s %v", e.Code, e.Parameters)
	}
	resp = f.send(t, "GET", api+"/policy/bundles/1", auditor, nil)
	mustStatus(t, resp, http.StatusOK)
}

// ---------------------------------------------------------------------------
// the lifecycle, end to end
// ---------------------------------------------------------------------------

func TestUploadValidateActivateRollbackOverTheWire(t *testing.T) {
	f := newServer(t)
	author := f.roleToken(t, identity.RolePolicyAuthor)

	// A deliberately broken bundle: the second rule can never match.
	broken := bundleFor(sreRule + strings.Replace(sreRule, "id: sre", "id: never", 1))
	resp := f.send(t, "POST", api+"/policy/validate", author, map[string]any{"source": broken})
	mustStatus(t, resp, http.StatusOK)
	var rep authoring.Report
	decodeInto(t, resp, &rep)
	if rep.Valid || len(rep.Rejections) != 1 {
		t.Fatalf("the broken bundle validated: %+v", rep)
	}
	if r := rep.Rejections[0]; r.Rule != "never" || r.Line != 19 || !strings.Contains(r.Message, `line 19: rule "never"`) ||
		!strings.Contains(r.Message, "move it above sre") || r.Code != "rule.unreachable" {
		t.Errorf("rejection = %+v; it must name the rule, the line and what to do instead", r)
	}
	resp = f.send(t, "POST", api+"/policy/bundles", author, map[string]any{"source": broken})
	mustStatus(t, resp, http.StatusUnprocessableEntity)
	if e := decodeError(t, resp); e.Code != north.CodePolicyInvalid || e.Parameters["rejections"] == nil || e.CorrelationID == "" {
		t.Errorf("uploading a broken bundle: %+v", e)
	}

	// Two good versions, activated in turn, then a rollback.
	for i, source := range []string{bundleFor(sreRule), bundleFor(sreRule + restrictedRule("/usr/bin/uptime"))} {
		resp = f.send(t, "POST", api+"/policy/bundles", author, map[string]any{"source": source})
		mustStatus(t, resp, http.StatusCreated)
		var up authoring.UploadResult
		decodeInto(t, resp, &up)
		if up.Version.Version != int64(i+1) {
			t.Fatalf("upload %d stored version %d", i+1, up.Version.Version)
		}
	}
	resp = f.send(t, "GET", api+"/policy/bundles/2/diff?against=1", author, nil)
	mustStatus(t, resp, http.StatusOK)
	var d authoring.Diff
	decodeInto(t, resp, &d)
	if !slices.Equal(d.Added, []string{"logs"}) {
		t.Errorf("diff 1→2 added %v, want [logs]", d.Added)
	}

	for _, v := range []string{"1", "2", "1"} {
		resp = f.send(t, "POST", api+"/policy/bundles/"+v+"/activate", author, map[string]any{})
		mustStatus(t, resp, http.StatusOK)
	}
	var res authoring.ActivateResult
	decodeInto(t, resp, &res)
	if !res.Rollback || res.Previous != 2 || res.Version.Version != 1 {
		t.Errorf("the rollback answered %+v", res)
	}
	resp = f.send(t, "GET", api+"/policy/active", author, nil)
	mustStatus(t, resp, http.StatusOK)
	var active struct {
		Version int64  `json:"version"`
		Source  string `json:"source"`
	}
	decodeInto(t, resp, &active)
	if active.Version != 1 || active.Source != bundleFor(sreRule) {
		t.Errorf("active = %d", active.Version)
	}
	if got := len(f.operatorRecords(t, audit.EventPolicyActivated)); got != 3 {
		t.Errorf("%d activation records, want 3", got)
	}
}

// The interpreter warning stops an activation until accepted; publication
// succeeds when it is, and the acceptance is on the audit record. GitOps sees
// the same refusal machine-readably, with the version it stored.
func TestTheInterpreterWarningIsAcceptedOverTheWire(t *testing.T) {
	f := newServer(t)
	author := f.roleToken(t, identity.RolePolicyAuthor)
	source := bundleFor(restrictedRule("/usr/bin/awk"))

	resp := f.send(t, "POST", api+"/policy/apply", author, map[string]any{"source": source})
	mustStatus(t, resp, http.StatusConflict)
	e := decodeError(t, resp)
	if e.Code != north.CodeFindingsNotAccepted {
		t.Fatalf("code %q", e.Code)
	}
	findings, _ := e.Parameters["findings"].([]any)
	if len(findings) != 1 || e.Parameters["version"] != float64(1) {
		t.Fatalf("parameters = %v; want the one finding and the stored version", e.Parameters)
	}
	finding := findings[0].(map[string]any)
	if finding["code"] != authoring.CodeInterpreter || !strings.Contains(finding["message"].(string), "awk") {
		t.Fatalf("finding = %v", finding)
	}

	resp = f.send(t, "POST", api+"/policy/apply", author, map[string]any{"source": source, "accept": []string{finding["id"].(string)}})
	mustStatus(t, resp, http.StatusOK)
	recs := f.operatorRecords(t, audit.EventPolicyActivated)
	if len(recs) != 1 || recs[0].Attributes[audit.AttrAccepted] != finding["id"] {
		t.Errorf("the activation record does not name the accepted finding: %+v", recs)
	}
	if recs[0].Attributes[audit.AttrPrincipalID] == "" {
		t.Errorf("the activation record names no actor")
	}
}

// ---------------------------------------------------------------------------
// every mutating action is audited with its actor
// ---------------------------------------------------------------------------

func TestEveryMutatingActionIsAuditedWithItsActor(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	acts := []struct {
		method, path string
		body         any
		event        string
		want         int
	}{
		{"POST", "/policy/bundles", map[string]any{"source": bundleFor(sreRule)}, audit.EventPolicyUploaded, http.StatusCreated},
		{"POST", "/policy/bundles/1/activate", map[string]any{}, audit.EventPolicyActivated, http.StatusOK},
		{"PUT", "/targets/t-1", map[string]any{"hostname": "app.example.com", "zone": "edge", "labels": map[string]string{"env": "prod"}}, audit.EventTargetPut, http.StatusOK},
		{"POST", "/targets/relabel", map[string]any{"apply": true, "changes": []any{map[string]any{"target": "t-1", "labels": map[string]string{"env": "dev"}}}}, audit.EventTargetsRelabelled, http.StatusOK},
		{"DELETE", "/targets/t-1", nil, audit.EventTargetDeleted, http.StatusNoContent},
		{"PUT", "/subjects/bob@example.com", map[string]any{"groups": []string{"sre"}, "principals": []string{"bob"}}, audit.EventSubjectPut, http.StatusOK},
		{"POST", "/role-bindings", map[string]any{"subject_id": "bob@example.com", "role": "auditor"}, audit.EventRoleBound, http.StatusOK},
		{"POST", "/role-bindings/remove", map[string]any{"subject_id": "bob@example.com", "role": "auditor"}, audit.EventRoleUnbound, http.StatusOK},
		{"DELETE", "/subjects/bob@example.com", nil, audit.EventSubjectDeleted, http.StatusNoContent},
		{"POST", "/proxies/enrollments", map[string]any{"proxy_id": "proxy-9", "zones": []string{"edge"}}, audit.EventProxyApproved, http.StatusCreated},
		{"PUT", "/fleet/config/zone/edge", map[string]any{"settings": map[string]any{"logging.batch_size": 50}}, audit.EventFleetConfigPublished, http.StatusOK},
		{"PUT", "/fleet/config/zone/edge", map[string]any{"settings": map[string]any{"logging.batch_size": 60}}, audit.EventFleetConfigPublished, http.StatusOK},
		{"POST", "/fleet/config/zone/edge/rollback", nil, audit.EventFleetConfigRolledBack, http.StatusOK},
		{"POST", "/revocations", map[string]any{"type": "cache_invalidate", "all": true}, audit.EventRevocationPublished, http.StatusOK},
		{"PUT", "/identity/claim-mapping", map[string]any{"document": "schema_version: 1\nclaims:\n  - claim: email\n    attribute: email\n"}, audit.EventClaimMappingActivated, http.StatusOK},
	}
	for _, a := range acts {
		before := len(f.operatorRecords(t, a.event))
		resp := f.send(t, a.method, api+a.path, admin, a.body)
		mustStatus(t, resp, a.want)
		recs := f.operatorRecords(t, a.event)
		if len(recs) <= before {
			t.Errorf("%s %s wrote no %s record", a.method, a.path, a.event)
			continue
		}
		if recs[0].Attributes[audit.AttrPrincipalID] == "" {
			t.Errorf("%s %s's record names no actor: %v", a.method, a.path, recs[0].Attributes)
		}
	}
}

// ---------------------------------------------------------------------------
// operator actions
// ---------------------------------------------------------------------------

// A publication that cannot reach a host-key decision says so: invalidating by
// subject does not cover them, invalidating by key and a resync do.
func TestARevocationSaysWhetherItCoversHostKeyDecisions(t *testing.T) {
	f := newServer(t)
	desk := f.roleToken(t, identity.RoleGrantAdmin)
	cases := []struct {
		body   map[string]any
		covers bool
	}{
		{map[string]any{"type": "cache_invalidate", "subject": "alice@example.com"}, false},
		{map[string]any{"type": "cache_invalidate", "keys": []string{"k1"}}, true},
		{map[string]any{"type": "resync"}, true},
	}
	for _, c := range cases {
		resp := f.send(t, "POST", api+"/revocations", desk, c.body)
		mustStatus(t, resp, http.StatusOK)
		var rcpt struct {
			EventID string `json:"event_id"`
			Covers  bool   `json:"covers_host_key_decisions"`
		}
		decodeInto(t, resp, &rcpt)
		if rcpt.EventID == "" || rcpt.Covers != c.covers {
			t.Errorf("%v: event %q covers %v, want %v", c.body, rcpt.EventID, rcpt.Covers, c.covers)
		}
	}
	// revoke.Operator's own validation answers: a kill with no reason.
	resp := f.send(t, "POST", api+"/revocations", desk, map[string]any{"type": "session_kill", "all": true})
	mustStatus(t, resp, http.StatusBadRequest)
	// A host key nobody recorded is unresolved, and nothing is published.
	resp = f.send(t, "POST", api+"/revocations", desk, map[string]any{
		"type": "host_key_withdraw", "host_key": map[string]any{"target": "x.example.com", "fingerprint": "SHA256:none"},
	})
	mustStatus(t, resp, http.StatusConflict)
	if e := decodeError(t, resp); e.Code != north.CodeHostKeyUnresolved || e.Parameters["instead"] != "resync" {
		t.Errorf("unresolved host key: %+v", e)
	}
}

// A fleet configuration naming a key the proxy keeps in its bootstrap file is
// refused at publish time, naming the key; so is a nested object.
func TestAConfigurationPublishIsRefusedNamingTheKey(t *testing.T) {
	f := newServer(t)
	fleetAdmin := f.roleToken(t, identity.RoleFleetAdmin)
	for _, c := range []struct {
		settings map[string]any
		key      string
		code     string
	}{
		{map[string]any{"control.url": "https://elsewhere"}, "control.url", north.CodeConfigKeyNotFleetOwned},
		{map[string]any{"control.cache.stale_after": "1h"}, "control.cache.stale_after", north.CodeConfigKeyNotFleetOwned},
		{map[string]any{"logging": map[string]any{"batch_size": 5}}, "logging", north.CodeConfigKeyNotFleetOwned},
		{map[string]any{"logging.batch_size": "many"}, "logging.batch_size", north.CodeConfigValueInvalid},
	} {
		resp := f.send(t, "PUT", api+"/fleet/config/zone/edge", fleetAdmin, map[string]any{"settings": c.settings})
		mustStatus(t, resp, http.StatusUnprocessableEntity)
		if e := decodeError(t, resp); e.Code != c.code || e.Parameters["key"] != c.key {
			t.Errorf("%v: %s naming %v, want %s naming %s", c.settings, e.Code, e.Parameters["key"], c.code, c.key)
		}
	}
}

// ---------------------------------------------------------------------------
// explain over the wire
// ---------------------------------------------------------------------------

func TestExplainOverTheWire(t *testing.T) {
	f := newServer(t)
	auditor := f.roleToken(t, identity.RoleAuditor)
	ctx := t.Context()
	inputs := json.RawMessage(`{"now":"2026-10-01T12:00:00Z","subject":{"id":"alice@example.com","mfa":false,"known":true,"break_glass":false,"mapping_version":2},"target":{"hostname":"app.example.com","known":true},"context":{"proxy_id":"proxy-1"},"grants":[{"id":"g-1","origin":"administrator","scope":{"name":"ops","targets":null,"labels":null,"zones":null},"not_before":"2026-10-01T11:00:00Z","expires_at":"2026-10-01T13:00:00Z","reason":"incident","reason_code":"","created_by":{"subject":"pat","principal":"tok","break_glass":false},"request_id":"","workflow_ref":"","approvers":null,"external":{"system":"","reference":"","window_start":"","window_end":"","mode":"","assertion_id":""}}],"connection":{},"external_context":[]}`)
	for _, d := range []store.Decision{
		{ID: "dec-allow", SubjectID: "alice@example.com", Effect: "allow", SessionID: "sess-1", ProxyID: "proxy-1",
			InputsDigest: "x", Inputs: inputs, Snapshot: json.RawMessage(`{"route_type":"direct"}`),
			Explanation: json.RawMessage(`{"effect":"allow","rule":"ops","rule_line":4,"grant":"g-1"}`), DecidedAt: time.Now()},
		{ID: "dec-forged", SubjectID: "alice@example.com", Effect: "allow", SessionID: "sess-2", ProxyID: "proxy-1",
			InputsDigest: "x", Inputs: inputs,
			Explanation: json.RawMessage(`{"effect":"allow","rule":"ops","grant":"g-unknown"}`), DecidedAt: time.Now()},
	} {
		if err := f.st.Decisions().Insert(ctx, "tenant-a", d); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	resp := f.send(t, "GET", api+"/decisions/dec-allow", auditor, nil)
	mustStatus(t, resp, http.StatusOK)
	var story struct {
		Rule    string          `json:"rule"`
		Grant   json.RawMessage `json:"grant"`
		Mapping struct {
			Version int  `json:"version"`
			Changed bool `json:"changed"`
		} `json:"mapping"`
	}
	decodeInto(t, resp, &story)
	if story.Rule != "ops" || !bytes.Contains(story.Grant, []byte(`"id":"g-1"`)) || story.Mapping.Version != 2 {
		t.Errorf("story = %+v, grant %s", story, story.Grant)
	}
	resp = f.send(t, "GET", api+"/sessions/sess-1/decisions", auditor, nil)
	mustStatus(t, resp, http.StatusOK)

	resp = f.send(t, "GET", api+"/decisions/dec-forged", auditor, nil)
	mustStatus(t, resp, http.StatusInternalServerError)
	if e := decodeError(t, resp); e.Code != north.CodeDecisionUnexplainable || e.CorrelationID == "" {
		t.Errorf("an unexplainable record answered %+v", e)
	}
	resp = f.send(t, "GET", api+"/decisions/dec-nobody", auditor, nil)
	mustStatus(t, resp, http.StatusNotFound)
}

// ---------------------------------------------------------------------------
// the audit record by id, and retention
// ---------------------------------------------------------------------------

func TestTheAuditRecordRouteServesARecordByID(t *testing.T) {
	f := newServer(t)
	admin, auditor := f.roleToken(t, identity.RoleAdmin), f.roleToken(t, identity.RoleAuditor)
	resp := f.send(t, "POST", api+"/revocations", admin, map[string]any{"type": "resync"})
	mustStatus(t, resp, http.StatusOK)
	recs := f.operatorRecords(t, audit.EventRevocationPublished)
	if len(recs) != 1 {
		t.Fatalf("records: %d", len(recs))
	}
	resp = f.send(t, "GET", api+"/audit/records/"+recs[0].RecordID, auditor, nil)
	mustStatus(t, resp, http.StatusOK)
	var rec struct {
		RecordID string `json:"record_id"`
		Hash     string `json:"hash"`
		Body     string `json:"body"`
		ChainSeq int64  `json:"chain_seq"`
	}
	decodeInto(t, resp, &rec)
	if rec.RecordID != recs[0].RecordID || rec.Hash == "" || rec.Body == "" || rec.ChainSeq == 0 {
		t.Errorf("record = %+v; it must carry its chain position and hash", rec)
	}
	resp = f.send(t, "GET", api+"/audit/records/no-such-record", auditor, nil)
	mustStatus(t, resp, http.StatusNotFound)
}

func TestRetentionOverTheWireKeepsTheChainVerifying(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	for range 5 {
		mustStatus(t, f.send(t, "POST", api+"/revocations", admin, map[string]any{"type": "resync"}), http.StatusOK)
	}
	resp := f.send(t, "POST", api+"/audit/retention", admin, map[string]any{"stream": audit.StreamControl, "through_seq": 3})
	mustStatus(t, resp, http.StatusOK)
	var out struct {
		Deleted    int64 `json:"deleted"`
		ThroughSeq int64 `json:"through_seq"`
	}
	decodeInto(t, resp, &out)
	if out.Deleted != 3 || out.ThroughSeq != 3 {
		t.Fatalf("retention = %+v", out)
	}
	result, err := audit.NewVerifier(f.st, audit.WithRetentionKeys(f.lifecycle.keys)).VerifyTenant(t.Context(), "tenant-a")
	if err != nil || !result.OK() {
		t.Fatalf("verification after retention: %v %+v", err, result.Streams)
	}
	retained := f.operatorRecords(t, audit.EventAuditRetained)
	if len(retained) != 1 {
		t.Fatalf("the retention pass is not on the record")
	}
	// The record of the pass commits in the deletion's own transaction, so
	// neither exists without the other (0021): rows one transaction wrote
	// carry its id.
	if marker, rec := xminOf(t, f, `SELECT xmin::text FROM audit_retention WHERE tenant = 'tenant-a' AND stream = $1`, audit.StreamControl),
		xminOf(t, f, `SELECT xmin::text FROM audit_records WHERE tenant = 'tenant-a' AND record_id = $1`, retained[0].RecordID); marker != rec {
		t.Errorf("the marker was written by transaction %s and the record of the pass by %s", marker, rec)
	}
	// The head is never retained.
	resp = f.send(t, "POST", api+"/audit/retention", admin, map[string]any{"stream": audit.StreamControl, "through_seq": 100})
	mustStatus(t, resp, http.StatusBadRequest)
}

// A capture retention pass and its record commit together too (0021).
func TestCaptureRetentionOverTheWireIsRecordedInItsOwnTransaction(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	in, err := audit.New(audit.Options{Store: f.st})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Ingest(t.Context(), audit.Submission{Tenant: "tenant-a", ProxyID: "proxy-a", Records: []contract.LogRecord{{
		RecordID: "rec-capture", SessionID: "sess-capture", Kind: "stream", Severity: contract.SeverityInfo,
		Timestamp: time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
		Payload:   base64.StdEncoding.EncodeToString([]byte("whoami\r\n")),
	}}}); err != nil {
		t.Fatal(err)
	}
	resp := f.send(t, "POST", api+"/audit/captures/retention", admin, map[string]any{"before": time.Now().Add(-24 * time.Hour)})
	mustStatus(t, resp, http.StatusOK)
	retained := f.operatorRecords(t, audit.EventAuditRetained)
	if len(retained) != 1 {
		t.Fatalf("the capture retention pass is not on the record")
	}
	if capture, rec := xminOf(t, f, `SELECT xmin::text FROM audit_captures WHERE tenant = 'tenant-a' AND record_id = $1`, "rec-capture"),
		xminOf(t, f, `SELECT xmin::text FROM audit_records WHERE tenant = 'tenant-a' AND record_id = $1`, retained[0].RecordID); capture != rec {
		t.Errorf("the capture was emptied by transaction %s and the record of the pass written by %s", capture, rec)
	}
}

func xminOf(t *testing.T, f *serverFixture, query, arg string) string {
	t.Helper()
	var xmin string
	if err := f.st.Pool().QueryRow(t.Context(), query, arg).Scan(&xmin); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return xmin
}

// ---------------------------------------------------------------------------
// the codes
// ---------------------------------------------------------------------------

// The registry of codes this surface produces, held whole: adding one is
// deliberate — this list changes in the same commit — and a code reused for a
// different condition would have to be removed from here first.
func TestTheCodeRegistryIsHeldWhole(t *testing.T) {
	want := []string{
		"assertion_conflict", "assertion_from_future", "assertion_malformed", "assertion_stale",
		"binding_disabled", "binding_not_found", "ca_not_configured", "config_key_not_fleet_owned",
		"config_value_invalid", "decision_unexplainable", "findings_not_accepted", "forbidden",
		"grant_request_denied", "grant_request_not_pending", "grant_revocation_undelivered",
		"grant_workflow_failed", "grant_workflow_not_registered", "host_key_unresolved", "internal",
		"invalid_request", "login_refused", "method_not_allowed", "not_found", "outside_scope",
		"payload_too_large", "policy_blocked", "policy_invalid", "provider_not_found", "push_not_accepted",
		"push_not_permitted", "push_not_supported", "rate_limited", "retention_key_not_configured",
		"retention_not_archived", "retention_unverified", "standing_denial_undelivered", "subject_required",
		"target_quarantined", "tenant_ambiguous", "tenant_out_of_scope", "unauthenticated", "unavailable",
		"window_closed",
	}
	got := slices.Sorted(slices.Values(north.AllCodes))
	if !slices.Equal(got, want) {
		t.Errorf("the code registry is\n%v\nwant\n%v", got, want)
	}
}

// The error paths this phase adds each carry a code, parameters where they
// have any, an English message and the correlation id: validation, RBAC,
// satisfiability, not-found and an outage.
func TestThePhasesErrorPathsCarryTheEnvelope(t *testing.T) {
	f := newServer(t)
	author, auditor := f.roleToken(t, identity.RolePolicyAuthor), f.roleToken(t, identity.RoleAuditor)
	if err := f.st.Decisions().Insert(t.Context(), "tenant-a", store.Decision{
		ID: "dec-bad", Effect: "deny", InputsDigest: "x", Inputs: json.RawMessage(`{}`),
		Explanation: json.RawMessage(`{}`), DecidedAt: time.Now(),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	cases := []struct {
		name, method, path, token string
		body                      any
		status                    int
		code                      string
		params                    bool
	}{
		{"validation", "POST", "/policy/bundles", author, map[string]any{"source": "schema_version: 9\n"}, 422, north.CodePolicyInvalid, true},
		{"rbac", "POST", "/policy/apply", auditor, map[string]any{"source": "x"}, 403, north.CodeForbidden, true},
		{"satisfiability", "POST", "/policy/apply", author, map[string]any{"source": bundleFor(restrictedRule("/bin/sh"))}, 409, north.CodeFindingsNotAccepted, true},
		{"not found", "GET", "/policy/bundles/42", auditor, nil, 404, north.CodeNotFound, false},
		{"outage", "GET", "/decisions/dec-bad", auditor, nil, 500, north.CodeDecisionUnexplainable, false},
	}
	for _, c := range cases {
		resp := f.send(t, c.method, api+c.path, c.token, c.body)
		if resp.StatusCode != c.status {
			t.Errorf("%s: answered %d, want %d", c.name, resp.StatusCode, c.status)
			continue
		}
		e := decodeError(t, resp)
		if e.Code != c.code || e.Message == "" || e.CorrelationID == "" || (c.params && len(e.Parameters) == 0) {
			t.Errorf("%s: envelope %+v", c.name, e)
		}
	}
}

// The serial filter reads the one canonical rendering, whatever was typed.
func TestTheSerialFilterIsCanonical(t *testing.T) {
	f := newServer(t)
	auditor := f.roleToken(t, identity.RoleAuditor)
	mustStatus(t, f.send(t, "GET", api+"/audit/records?credential_certificate_serial="+contract.FormatSerial(42), auditor, nil), http.StatusOK)
	mustStatus(t, f.send(t, "GET", api+"/audit/records?credential_certificate_serial=-1", auditor, nil), http.StatusBadRequest)
}

// ---------------------------------------------------------------------------
// the emergency runbook (Hoplock/proxy#69)
// ---------------------------------------------------------------------------

// negotiatedSessions is the runbook's third step as its doc says to take it
// (PLAN §5.2, §7): the negotiated sessions a question names, read to the last
// page, following next until truncated is false. A limit of 0 asks for none.
func negotiatedSessions(t *testing.T, f *serverFixture, token, question string, limit int) []string {
	t.Helper()
	var out []string
	q := question
	if limit > 0 {
		q += fmt.Sprintf("&limit=%d", limit)
	}
	for pages := 0; ; pages++ {
		if pages > 1000 {
			t.Fatal("the negotiated sessions never ended")
		}
		resp := f.send(t, "GET", api+"/audit/negotiated?"+q, token, nil)
		mustStatus(t, resp, http.StatusOK)
		var page struct {
			Sessions []struct {
				SessionID string `json:"session_id"`
			} `json:"sessions"`
			Truncated bool   `json:"truncated"`
			Next      string `json:"next"`
		}
		decodeInto(t, resp, &page)
		for _, s := range page.Sessions {
			out = append(out, s.SessionID)
		}
		if !page.Truncated {
			return out
		}
		q = question + fmt.Sprintf("&limit=%d&after=%s", limit, url.QueryEscape(page.Next))
	}
}

// The runbook ends exactly the sessions its third step lists, so that step is
// read to the end (M26): walked a page at a time, the question a ban asks
// lists every session that ran under the ban, each once, and none that did
// not, whichever page boundary falls between two sessions of one instant.
func TestTheRunbooksQueryWalkedToTheEndListsEverySessionABanMatches(t *testing.T) {
	f := newServer(t)
	auditor := f.roleToken(t, identity.RoleAuditor)
	ingest, err := audit.New(audit.Options{Store: f.st})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var recs []contract.LogRecord
	var banned []string
	for i := range 7 {
		bans := "aes128-cbc,aes128-ctr"
		session := fmt.Sprintf("sess-ban-%d", i)
		if i%3 == 2 {
			bans, session = "aes128-ctr", fmt.Sprintf("sess-other-%d", i)
		} else {
			banned = append(banned, session)
		}
		// Every record of a session is stamped with the ban, and the
		// sessions share instants in pairs.
		for leg := range 2 {
			recs = append(recs, contract.LogRecord{
				RecordID: fmt.Sprintf("%s-%d", session, leg), SessionID: session, Kind: "provisioning",
				Severity: contract.SeverityInfo, Message: "provisioned",
				Timestamp: at.Add(-time.Duration(i/2) * time.Minute).Format(time.RFC3339),
				Attributes: map[string]string{
					audit.AttrAlgorithmProfile: "default", audit.AttrAlgorithmBansPrefix + "ciphers": bans,
				},
			})
		}
	}
	if _, err := ingest.Ingest(t.Context(), audit.Submission{Tenant: "tenant-a", ProxyID: "proxy-ban", Records: recs}); err != nil {
		t.Fatal(err)
	}

	for _, limit := range []int{1, 2, 3, 0} {
		got := negotiatedSessions(t, f, auditor, "ban_axis=ciphers&ban_name=aes128-cbc", limit)
		if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(banned))) {
			t.Errorf("limit %d: the walk listed %v, want every session under the ban once: %v", limit, got, banned)
		}
	}
}

// The runbook's three steps are each callable over this surface and each
// audited: a publish that adds a ban everywhere, `cache_invalidate` with `all`,
// and `session_kill` for the sessions the by-value query returns.
func TestTheEmergencyRunbookIsCallableAndAudited(t *testing.T) {
	f := newServer(t)
	author, desk, auditor := f.roleToken(t, identity.RolePolicyAuthor), f.roleToken(t, identity.RoleGrantAdmin),
		f.roleToken(t, identity.RoleAuditor)
	ctx := t.Context()

	// A session that negotiated the exchange about to be banned.
	ingest, err := audit.New(audit.Options{Store: f.st})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := ingest.Ingest(ctx, audit.Submission{Tenant: "tenant-a", ProxyID: "proxy-1", Records: []contract.LogRecord{{
		RecordID: "neg-1", SessionID: "sess-sha1", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Kind: "provisioning", Severity: contract.SeverityInfo, Message: "negotiated",
		Attributes: map[string]string{
			audit.AttrEvent: audit.EventAlgorithmsNegotiated, audit.AttrTargetKexAlgorithm: "diffie-hellman-group14-sha1",
		},
	}}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// 1. A ban everywhere: the bundle's own algorithm_bans. No proxy has
	// declared what it offers, so whether the name is a typo cannot be
	// judged: that is advice (M17), and the ban publishes at once — the
	// runbook's first step never waits on an acceptance nothing evidences.
	banned := strings.Replace(bundleFor(sreRule), "rules:\n",
		"algorithm_bans:\n  key_exchanges: [diffie-hellman-group14-sha1]\nrules:\n", 1)
	resp := f.send(t, "POST", api+"/policy/apply", author, map[string]any{"source": banned})
	mustStatus(t, resp, http.StatusOK)
	var applied authoring.ApplyResult
	decodeInto(t, resp, &applied)
	for _, finding := range applied.Activate.Report.Findings {
		if finding.Severity == authoring.SeverityWarning && finding.Code == authoring.CodeBanUnmatched {
			t.Errorf("a ban no build could judge was a warning: %+v", finding)
		}
	}

	// 2. Drop every cached decision, so the ban reaches the next connection.
	mustStatus(t, f.send(t, "POST", api+"/revocations", desk, map[string]any{"type": "cache_invalidate", "all": true}), http.StatusOK)

	// 3. End the sessions that negotiated it, every page of them.
	found := negotiatedSessions(t, f, auditor, "axis=key_exchanges&value=diffie-hellman-group14-sha1", 0)
	if !slices.Equal(found, []string{"sess-sha1"}) {
		t.Fatalf("the by-value query found %v", found)
	}
	mustStatus(t, f.send(t, "POST", api+"/revocations", desk, map[string]any{
		"type": "session_kill", "session_ids": found, "reason": "an insecure key exchange was withdrawn",
	}), http.StatusOK)

	if recs := f.operatorRecords(t, audit.EventPolicyActivated); len(recs) != 1 || recs[0].Attributes[audit.AttrAfter] != "1" {
		t.Errorf("the ban's activation is not on the record: %+v", recs)
	}
	revocations := f.operatorRecords(t, audit.EventRevocationPublished)
	if len(revocations) != 2 {
		t.Fatalf("%d revocation records, want 2", len(revocations))
	}
	if kill := revocations[0].Attributes; kill[audit.AttrOperatorPrefix+"type"] != "session_kill" ||
		kill[audit.AttrOperatorPrefix+"session_ids"] != "sess-sha1" {
		t.Errorf("the kill's record = %v", kill)
	}
	if inv := revocations[1].Attributes; inv[audit.AttrOperatorPrefix+"type"] != "cache_invalidate" ||
		inv[audit.AttrOperatorPrefix+"selector"] != "all" {
		t.Errorf("the invalidation's record = %v", inv)
	}
}
