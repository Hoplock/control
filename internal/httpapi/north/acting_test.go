// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The supervising operator a supervisor's call names (0048): written beside
// the `sup-` principal on every record the call writes, refused from anybody
// else, and read by nothing that decides.

// operatorName needs encoding twice over: non-ASCII letters and a `%`.
const operatorName = "José Núñez (MSP on-call, 100%)"

// encodeOperator is the header value for name: its UTF-8, with every octet
// outside printable ASCII and `%` itself percent-encoded.
func encodeOperator(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 0x20 || c > 0x7e || c == '%' {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// sendNamed is a request to srv carrying each of operators as one
// Hoplock-Acting-Operator value, already encoded; none sends no header.
func sendNamed(t *testing.T, srv *httptest.Server, method, path, token string, body any, operators ...string) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	var req *http.Request
	var err error
	if reader != nil {
		req, err = http.NewRequestWithContext(t.Context(), method, srv.URL+path, reader)
	} else {
		req, err = http.NewRequestWithContext(t.Context(), method, srv.URL+path, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, op := range operators {
		req.Header.Add(north.ActingOperatorHeader, op)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// storedRecord is one row of the audit store, read from the body the chain
// hashed rather than from the columns derived from it.
type storedRecord struct {
	id, event  string
	attributes map[string]string
}

// recordIDs is every record id the tenant holds, in every stream.
func recordIDs(t *testing.T, st *store.Store) map[string]bool {
	t.Helper()
	rows, err := st.Pool().Query(t.Context(), `SELECT record_id FROM audit_records WHERE tenant = 'tenant-a'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// recordsSince is every record the tenant holds that before did not: what the
// calls in between committed.
func recordsSince(t *testing.T, st *store.Store, before map[string]bool) []storedRecord {
	t.Helper()
	rows, err := st.Pool().Query(t.Context(), `SELECT record_id, event, body FROM audit_records WHERE tenant = 'tenant-a'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []storedRecord
	for rows.Next() {
		var rec storedRecord
		var body string
		if err := rows.Scan(&rec.id, &rec.event, &body); err != nil {
			t.Fatal(err)
		}
		if before[rec.id] {
			continue
		}
		var hashed struct {
			Attributes map[string]string `json:"attributes"`
		}
		if err := json.Unmarshal([]byte(body), &hashed); err != nil {
			t.Fatalf("record %s: body: %v", rec.id, err)
		}
		rec.attributes = hashed.Attributes
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// requireNamed fails t unless recs holds want and every record names the
// operator beside the supervisor that verified it.
func requireNamed(t *testing.T, recs []storedRecord, want, principal, name string) {
	t.Helper()
	if !slices.ContainsFunc(recs, func(r storedRecord) bool { return r.event == want }) {
		t.Fatalf("the call committed no %s (it committed %d records)", want, len(recs))
	}
	for _, r := range recs {
		if r.attributes[audit.AttrPrincipalID] != principal || r.attributes[audit.AttrActingOperator] != name {
			t.Errorf("%s (%s) names principal %q and operator %q; want %q and %q", r.event, r.id,
				r.attributes[audit.AttrPrincipalID], r.attributes[audit.AttrActingOperator], principal, name)
		}
	}
}

// supervisorWith mints a supervisor's token in tenant-a holding roles there.
func supervisorWith(t *testing.T, issue func() (identity.Credential, *identity.Principal, error)) (string, string) {
	t.Helper()
	cred, principal, err := issue()
	if err != nil {
		t.Fatal(err)
	}
	return cred.String(), principal.ID
}

// EVERY RECORD A NAMED CALL WRITES NAMES THE OPERATOR. Every write on this
// surface (everyWrite's list, which TestEveryWriteCommitsWithItsRecordOrNotAtAll
// runs too), made by a supervisor scoped to it, naming an operator: every
// record it committed carries `acting_operator`, decoded, inside the hashed
// body and beside the supervisor's `sup-` principal. The grant routes and the
// revocation, which write through the grant service and the stream, are run
// after the list. A write added later fails this test until it is listed.
func TestEveryWriteNamesTheActingOperator(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	sup, supID := supervisorWith(t, func() (identity.Credential, *identity.Principal, error) {
		return f.federation.IssueToken(t.Context(), "tenant-a", identity.TokenRequest{
			DisplayName: "msp", Supervisor: true,
			Scopes: map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin}},
		})
	})
	writes, others := everyWrite(t, f, admin)
	requireEveryWriteListed(t, f, writes, others)

	keys := []string{"POST /policy/bundles", "POST /policy/bundles/{version}/activate", "POST /policy/apply"}
	for key := range writes {
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys[3:])
	for _, key := range keys {
		w := writes[key]
		t.Run(key, func(t *testing.T) {
			if w.setup != nil {
				w.setup(t)
			}
			before := recordIDs(t, f.st)
			mustStatus(t, sendNamed(t, f.http, w.method, api+w.path, sup, w.body, encodeOperator(operatorName)), w.status)
			requireNamed(t, recordsSince(t, f.st, before), w.event, supID, operatorName)
		})
	}

	// Through the grant service (GrantEvent), and the stream.
	t.Run("POST /grants, POST /grants/{grant}/revoke", func(t *testing.T) {
		before := recordIDs(t, f.st)
		resp := sendNamed(t, f.http, "POST", api+"/grants", sup, aGrant, encodeOperator(operatorName))
		mustStatus(t, resp, http.StatusCreated)
		var created struct {
			Grant struct {
				ID string `json:"id"`
			} `json:"grant"`
		}
		decodeInto(t, resp, &created)
		requireNamed(t, recordsSince(t, f.st, before), access.EventGrantCreated, supID, operatorName)

		before = recordIDs(t, f.st)
		mustStatus(t, sendNamed(t, f.http, "POST", api+"/grants/"+created.Grant.ID+"/revoke", sup,
			map[string]string{"reason": "INC-9 closed"}, encodeOperator(operatorName)), http.StatusOK)
		requireNamed(t, recordsSince(t, f.st, before), access.EventGrantRevoked, supID, operatorName)
	})
	t.Run("POST /revocations", func(t *testing.T) {
		before := recordIDs(t, f.st)
		mustStatus(t, sendNamed(t, f.http, "POST", api+"/revocations", sup, map[string]any{"type": "resync"},
			encodeOperator(operatorName)), http.StatusOK)
		requireNamed(t, recordsSince(t, f.st, before), audit.EventRevocationPublished, supID, operatorName)
	})

	// The same supervisor naming nobody is recorded exactly as before.
	t.Run("unnamed", func(t *testing.T) {
		before := recordIDs(t, f.st)
		f.putTarget(t, sup, "t-unnamed", "unnamed.example.com")
		recs := recordsSince(t, f.st, before)
		if len(recs) == 0 {
			t.Fatal("the unnamed write committed no record")
		}
		for _, r := range recs {
			if _, has := r.attributes[audit.AttrActingOperator]; has || r.attributes[audit.AttrPrincipalID] != supID {
				t.Errorf("an unnamed call's %s carries %v", r.event, r.attributes)
			}
		}
	})

	// THE NAME IS IN THE HASHED BODY: the chain over every named record
	// verifies, and once one stored body's name is edited it does not.
	verifier := audit.NewVerifier(f.st, audit.WithRetentionKeys(f.lifecycle.keys))
	if res, err := verifier.VerifyStream(t.Context(), "tenant-a", audit.StreamControl); err != nil || res.Break != nil {
		t.Fatalf("the control stream does not verify: %+v, %v", res.Break, err)
	}
	tag, err := f.st.Pool().Exec(t.Context(), `
		UPDATE audit_records SET body = replace(body, $1, $2)
		WHERE tenant = 'tenant-a' AND record_id = (
			SELECT record_id FROM audit_records WHERE tenant = 'tenant-a' AND body LIKE '%' || $1 || '%' LIMIT 1)`,
		`"acting_operator":"`+jsonEscaped(operatorName)+`"`, `"acting_operator":"Somebody Else"`)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("edit a stored name: %v (%d rows)", err, tag.RowsAffected())
	}
	if res, err := verifier.VerifyStream(t.Context(), "tenant-a", audit.StreamControl); err != nil || res.Break == nil {
		t.Fatalf("a stored record whose acting_operator was edited still verifies: %+v, %v", res, err)
	}
}

// jsonEscaped is s as encoding/json writes it inside a string, which is how
// the canonical body holds it.
func jsonEscaped(s string) string {
	raw, _ := json.Marshal(s)
	return strings.Trim(string(raw), `"`)
}

// A push and a binding write record through the access-context service
// (ContextEvent): a named call's records name the operator there too.
func TestAccessContextRecordsNameTheActingOperator(t *testing.T) {
	f := newPushServer(t)
	sup, supID := supervisorWith(t, func() (identity.Credential, *identity.Principal, error) {
		return f.federation.IssueToken(t.Context(), "tenant-a", identity.TokenRequest{
			DisplayName: "msp", Supervisor: true,
			Scopes: map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin, identity.RoleIntegration}},
		})
	})
	name := encodeOperator(operatorName)
	body := map[string]any{
		"mode": "push", "scope": "change-window", "subjects": []string{"alice@example.com"},
		"targets": []string{"*.example.com"}, "max_window": "1h", "push_principals": []string{supID},
	}
	before := recordIDs(t, f.st)
	mustStatus(t, sendNamed(t, f.http, "PUT", bindingPath, sup, body, name), http.StatusOK)
	requireNamed(t, recordsSince(t, f.st, before), "access_context.binding_put", supID, operatorName)

	before = recordIDs(t, f.st)
	var assertion map[string]any
	if err := json.Unmarshal(window("CHG-7/1", "db01.example.com"), &assertion); err != nil {
		t.Fatal(err)
	}
	resp := sendNamed(t, f.http, "POST", pushPath, sup, assertion, name)
	if resp.StatusCode >= 300 {
		t.Fatalf("push answered %d: %+v", resp.StatusCode, decodeError(t, resp))
	}
	recs := recordsSince(t, f.st, before)
	if len(recs) == 0 {
		t.Fatal("the push committed no record")
	}
	requireNamed(t, recs, recs[0].event, supID, operatorName)

	before = recordIDs(t, f.st)
	mustStatus(t, sendNamed(t, f.http, "DELETE", bindingPath, sup, nil, name), http.StatusNoContent)
	requireNamed(t, recordsSince(t, f.st, before), "access_context.binding_deleted", supID, operatorName)
}

// ONLY A SUPERVISOR MAY NAME AN OPERATOR, and only a name this server can
// record faithfully. Every refusal is a 400 naming the header and the problem,
// written before the handler: nothing is changed and nothing is recorded. A bad
// credential is a 401 whatever the header says.
func TestOnlyASupervisorMayNameAnOperatorAndOnlyFaithfully(t *testing.T) {
	f := newServer(t)
	token := f.roleToken(t, identity.RoleAdmin)
	session, _ := f.session(t, "alice@example.com", "oidc-corp", false, identity.RoleAdmin)
	sup := supervisorToken(t, f, map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin}})
	name := encodeOperator(operatorName)

	refused := func(t *testing.T, resp *http.Response, problem string) {
		t.Helper()
		e := decodeError(t, resp)
		if resp.StatusCode != http.StatusBadRequest || e.Code != north.CodeInvalidRequest ||
			e.Parameters["field"] != north.ActingOperatorHeader || e.Parameters["problem"] != problem || e.CorrelationID == "" {
			t.Fatalf("%s %s answered %d %+v; want 400 invalid_request {field: %s, problem: %s}",
				resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, e, north.ActingOperatorHeader, problem)
		}
		if problem == access.ProblemTooLong && e.Parameters["limit"] != "128" {
			t.Fatalf("too_long names limit %v, want \"128\"", e.Parameters["limit"])
		}
	}
	nothingDone := func(t *testing.T, before map[string]bool, target string) {
		t.Helper()
		if recs := recordsSince(t, f.st, before); len(recs) != 0 {
			t.Errorf("a refused call recorded %d record(s): %+v", len(recs), recs)
		}
		if _, err := f.st.Targets().Get(t.Context(), "tenant-a", target); !store.IsNotFound(err) {
			t.Errorf("a refused call wrote target %s (%v)", target, err)
		}
	}
	put := map[string]any{"hostname": "named.example.com", "zone": "edge"}

	for who, cred := range map[string]string{"an ordinary tok- token": token, "a session": session} {
		t.Run(who, func(t *testing.T) {
			before := recordIDs(t, f.st)
			refused(t, sendNamed(t, f.http, "GET", api+"/targets", cred, nil, name), north.ProblemNotSupervisor)
			refused(t, sendNamed(t, f.http, "PUT", api+"/targets/t-named", cred, put, name), north.ProblemNotSupervisor)
			nothingDone(t, before, "t-named")
			// The same credential naming nobody is served.
			mustStatus(t, sendNamed(t, f.http, "GET", api+"/targets", cred, nil), http.StatusOK)
		})
	}
	t.Run("an anonymous route", func(t *testing.T) {
		refused(t, sendNamed(t, f.http, "GET", north.APIPrefix+"/session/methods?tenant=tenant-a", "", nil, name),
			north.ProblemNotSupervisor)
		mustStatus(t, sendNamed(t, f.http, "GET", north.APIPrefix+"/session/methods?tenant=tenant-a", "", nil), http.StatusOK)
	})
	t.Run("a bad credential is a 401 first", func(t *testing.T) {
		for _, cred := range []string{"", "tok-not-a-credential"} {
			resp := sendNamed(t, f.http, "PUT", api+"/targets/t-named", cred, put, "%%%")
			if resp.StatusCode != http.StatusUnauthorized || decodeError(t, resp).Code != north.CodeUnauthenticated {
				t.Fatalf("credential %q with a bad header answered %d, want 401", cred, resp.StatusCode)
			}
		}
	})
	t.Run("a supervisor's value", func(t *testing.T) {
		for _, c := range []struct {
			name    string
			values  []string
			problem string
		}{
			{"does not decode", []string{"100% sure"}, access.ProblemInvalid},
			{"is not utf-8", []string{"%C3%28"}, access.ProblemInvalid},
			{"is empty", []string{""}, access.ProblemInvalid},
			{"has a leading space", []string{"%20alice"}, access.ProblemInvalid},
			{"has a trailing space", []string{"alice%20"}, access.ProblemInvalid},
			{"carries a control character", []string{"ali%0Dce"}, access.ProblemInvalid},
			{"carries a format character", []string{"alice%E2%80%AE"}, access.ProblemInvalid},
			{"is sent twice", []string{"alice", "bob"}, access.ProblemInvalid},
			{"is 129 characters", []string{strings.Repeat("%C3%A9", 129)}, access.ProblemTooLong},
		} {
			t.Run(c.name, func(t *testing.T) {
				before := recordIDs(t, f.st)
				refused(t, sendNamed(t, f.http, "PUT", api+"/targets/t-named", sup, put, c.values...), c.problem)
				refused(t, sendNamed(t, f.http, "GET", api+"/targets", sup, nil, c.values...), c.problem)
				nothingDone(t, before, "t-named")
			})
		}
	})
	t.Run("128 characters is accepted", func(t *testing.T) {
		long := strings.Repeat("é", 128)
		before := recordIDs(t, f.st)
		mustStatus(t, sendNamed(t, f.http, "PUT", api+"/targets/t-long", sup, put, encodeOperator(long)), http.StatusOK)
		recs := recordsSince(t, f.st, before)
		if len(recs) == 0 || recs[0].attributes[audit.AttrActingOperator] != long {
			t.Fatalf("a 128-character name was not recorded: %+v", recs)
		}
	})
}

// THE NAME CHANGES NO ANSWER: the same supervisor's write and read answer the
// same status and body with it and without it.
func TestTheActingOperatorChangesNoAnswer(t *testing.T) {
	f := newServer(t)
	sup := supervisorToken(t, f, map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin}})
	name := encodeOperator(operatorName)
	answerOf := func(t *testing.T, resp *http.Response) string {
		t.Helper()
		var v any
		decodeInto(t, resp, &v)
		raw, _ := json.Marshal(v)
		return fmt.Sprintf("%d %s", resp.StatusCode, raw)
	}

	bind := map[string]any{"subject_id": "erin@example.com", "role": "auditor"}
	named := answerOf(t, sendNamed(t, f.http, "POST", api+"/role-bindings", sup, bind, name))
	mustStatus(t, sendNamed(t, f.http, "POST", api+"/role-bindings/remove", sup, bind), http.StatusOK)
	unnamed := answerOf(t, sendNamed(t, f.http, "POST", api+"/role-bindings", sup, bind))
	if named != unnamed {
		t.Errorf("a write answered\n%s\nnamed, and\n%s\nunnamed", named, unnamed)
	}

	f.putTarget(t, sup, "t-read", "read.example.com")
	named = answerOf(t, sendNamed(t, f.http, "GET", api+"/targets", sup, nil, name))
	unnamed = answerOf(t, sendNamed(t, f.http, "GET", api+"/targets", sup, nil))
	if named != unnamed {
		t.Errorf("a read answered\n%s\nnamed, and\n%s\nunnamed", named, unnamed)
	}
}

// A client declaring version 1 — which knows nothing of the header — reads a
// record carrying `acting_operator` exactly as a client of the current version
// does: an attribute key is not a member, and nothing is unexpressible.
func TestAVersionOneClientReadsANamedRecord(t *testing.T) {
	f := newServer(t)
	sup := supervisorToken(t, f, map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin}})
	auditor := f.roleToken(t, identity.RoleAuditor)
	before := recordIDs(t, f.st)
	mustStatus(t, sendNamed(t, f.http, "PUT", api+"/targets/t-v1", sup,
		map[string]any{"hostname": "v1.example.com"}, encodeOperator(operatorName)), http.StatusOK)
	recs := recordsSince(t, f.st, before)
	if len(recs) != 1 {
		t.Fatalf("want one record, got %d", len(recs))
	}

	answers := map[string]string{}
	for _, declared := range []string{"", "1", "2"} {
		for _, path := range []string{"/audit/records/" + recs[0].id, "/audit/records?event=" + audit.EventTargetPut} {
			resp := versioned(t, f, auditor, api+path, declared)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("declared %q, GET %s answered %d: %+v", declared, path, resp.StatusCode, decodeError(t, resp))
			}
			var v any
			decodeInto(t, resp, &v)
			raw, _ := json.Marshal(v)
			if !strings.Contains(string(raw), `"acting_operator":"`+jsonEscaped(operatorName)+`"`) {
				t.Fatalf("declared %q, GET %s does not carry the name: %s", declared, path, raw)
			}
			if prev, ok := answers[path]; ok && prev != string(raw) {
				t.Fatalf("declared %q, GET %s answered\n%s\nwhere another version answered\n%s", declared, path, raw, prev)
			}
			answers[path] = string(raw)
		}
	}
}

// The name rides to a host's route and to a report provider through
// HostCallerFrom, on a call that named one.
func TestTheHostCallerCarriesTheActingOperator(t *testing.T) {
	sox := &packs{offers: map[ext.Tenant][]ext.ReportDescriptor{"tenant-a": {accessReview}}}
	f := newReportFixture(t, reportConfig{}, reportReg{"example/sox", "1.0.0", sox})
	sup := supervisorToken(t, f.serverFixture, map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAuditor}})
	body := generation("example/sox")
	body["format"] = "json" // what the pack's default Generate answers in
	resp := sendNamed(t, f.http, "POST", generatePath("tenant-a"), sup, body, encodeOperator(operatorName))
	mustStatus(t, resp, http.StatusOK)

	sox.mu.Lock()
	callers := append(slices.Clone(sox.reportsCallers), sox.generateCallers...)
	sox.mu.Unlock()
	if len(callers) != 2 {
		t.Fatalf("the provider saw %d calls, want Reports and Generate", len(callers))
	}
	for _, c := range callers {
		if c.ActingOperator != operatorName || !identity.IsSupervisor(c.Principal) {
			t.Errorf("the provider saw caller %+v, want the supervisor naming %q", c, operatorName)
		}
	}

	// The access log line says who the supervisor named, beside the
	// principal; and so does a refusal's.
	var line map[string]any
	for _, l := range f.logs.lines("northbound_request") {
		if l["path"] == generatePath("tenant-a") {
			line = l
		}
	}
	if line == nil || line["acting_operator"] != operatorName || !identity.IsSupervisor(fmt.Sprint(line["principal_id"])) {
		t.Errorf("the access log line is %v", line)
	}
	mustStatus(t, sendNamed(t, f.http, "GET", "/api/v1/tenants/tenant-b/reports", sup, nil, encodeOperator(operatorName)),
		http.StatusForbidden)
	var refusal map[string]any
	for _, l := range f.logs.lines("northbound_refused") {
		if l["path"] == "/api/v1/tenants/tenant-b/reports" {
			refusal = l
		}
	}
	if refusal == nil || refusal["acting_operator"] != operatorName {
		t.Errorf("the refusal's line is %v", refusal)
	}
	// Unnamed, neither line carries the key.
	mustStatus(t, sendNamed(t, f.http, "GET", reportsPath("tenant-a"), sup, nil), http.StatusOK)
	for _, l := range f.logs.lines("northbound_request") {
		if l["path"] == reportsPath("tenant-a") {
			if _, has := l["acting_operator"]; has {
				t.Errorf("an unnamed call's line carries acting_operator: %v", l)
			}
		}
	}
}
