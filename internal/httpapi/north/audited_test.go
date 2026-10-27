// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// Every write on this surface commits with its operator record or not at all
// (PLAN §3, M8): an act the chain does not state, or a record of an act that
// never happened, is the silent gap the audit store exists to rule out.
//
// For each write a trigger refuses the record it must commit with; the write
// must then answer 5xx and leave what it would have changed as it was. With
// the trigger gone, the write succeeds and its record names the actor, and the
// before and after where the act has them. Every non-GET tenant route Control
// serves is listed here as a write or as something else, with the reason, so a
// write added later fails this test until it is listed (0023).
func TestEveryWriteCommitsWithItsRecordOrNotAtAll(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	ctx := t.Context()
	const tenant = store.Tenant("tenant-a")

	render := func(t *testing.T, v any, err error) string {
		switch {
		case store.IsNotFound(err):
			return "absent"
		case err != nil:
			t.Fatalf("read state: %v", err)
		}
		return fmt.Sprintf("%+v", v)
	}
	count := func(t *testing.T, query string) string {
		var n int
		if err := f.st.Pool().QueryRow(ctx, query, string(tenant)).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return fmt.Sprint(n)
	}
	activeVersion := func(t *testing.T) string {
		b, err := f.st.PolicyBundles().GetActive(ctx, tenant)
		return render(t, b.Version, err)
	}
	target := func(id string) func(*testing.T) string {
		return func(t *testing.T) string { tg, err := f.st.Targets().Get(ctx, tenant, id); return render(t, tg, err) }
	}
	subject := func(id string) func(*testing.T) string {
		return func(t *testing.T) string {
			sub, err := f.st.Subjects().Get(ctx, tenant, id)
			return render(t, sub, err)
		}
	}
	desired := func(zone string) func(*testing.T) string {
		return func(t *testing.T) string {
			d, err := f.st.ProxyConfigs().GetDesired(ctx, tenant, store.ConfigScope{Kind: store.ConfigScopeZone, ID: zone})
			return render(t, d.Version, err)
		}
	}
	mustSend := func(t *testing.T, method, path string, body any, want int) {
		t.Helper()
		mustStatus(t, f.send(t, method, api+path, admin, body), want)
	}
	ingest := func(t *testing.T, records ...contract.LogRecord) {
		t.Helper()
		in, err := audit.New(audit.Options{Store: f.st})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := in.Ingest(ctx, audit.Submission{Tenant: tenant, ProxyID: "proxy-ret", Records: records}); err != nil {
			t.Fatal(err)
		}
	}
	type write struct {
		event         string
		setup         func(t *testing.T)
		method, path  string
		body          any
		status        int
		state         func(t *testing.T) string
		before, after string // the record's attributes, when the act has them
		// row selects the xmin of the row the act leaves ($1 is the
		// tenant): it must be the record's, one transaction for both.
		row string
	}
	writes := map[string]write{
		"POST /policy/bundles": {
			row:   `SELECT xmin::text FROM policy_bundles WHERE tenant = $1 AND version = 1`,
			event: audit.EventPolicyUploaded, method: "POST", path: "/policy/bundles",
			body: map[string]any{"source": bundleFor(sreRule)}, status: http.StatusCreated,
			state: func(t *testing.T) string { return count(t, `SELECT count(*) FROM policy_bundles WHERE tenant = $1`) },
			after: "1",
		},
		"POST /policy/bundles/{version}/activate": {
			row:   `SELECT xmin::text FROM policy_bundles WHERE tenant = $1 AND active`,
			event: audit.EventPolicyActivated, method: "POST", path: "/policy/bundles/1/activate",
			body: map[string]any{}, status: http.StatusOK, state: activeVersion, after: "1",
		},
		"POST /policy/apply": {
			row:   `SELECT xmin::text FROM policy_bundles WHERE tenant = $1 AND active`,
			event: audit.EventPolicyActivated, method: "POST", path: "/policy/apply",
			body:   map[string]any{"source": strings.Replace(bundleFor(sreRule), "id: sre", "id: sre-applied", 1)},
			status: http.StatusOK, state: activeVersion, before: "1", after: "2",
		},
		"PUT /targets/{target}": {
			row:   `SELECT xmin::text FROM targets WHERE tenant = $1 AND target_id = 't-put'`,
			event: audit.EventTargetPut, method: "PUT", path: "/targets/t-put",
			body:   map[string]any{"hostname": "put.example.com", "zone": "edge", "labels": map[string]string{"env": "prod"}},
			status: http.StatusOK, state: target("t-put"), after: "env=prod",
		},
		"DELETE /targets/{target}": {
			event: audit.EventTargetDeleted, method: "DELETE", path: "/targets/t-del", status: http.StatusNoContent,
			setup: func(t *testing.T) {
				mustSend(t, "PUT", "/targets/t-del", map[string]any{"hostname": "del.example.com", "labels": map[string]string{"env": "dev"}}, http.StatusOK)
			},
			state: target("t-del"), before: "env=dev",
		},
		"POST /targets/relabel": {
			row:   `SELECT xmin::text FROM targets WHERE tenant = $1 AND target_id = 't-relabel'`,
			event: audit.EventTargetsRelabelled, method: "POST", path: "/targets/relabel",
			body: map[string]any{"apply": true, "changes": []any{
				map[string]any{"target": "t-relabel", "labels": map[string]string{"env": "dev"}},
			}},
			status: http.StatusOK,
			setup: func(t *testing.T) {
				mustSend(t, "PUT", "/targets/t-relabel", map[string]any{"hostname": "relabel.example.com", "labels": map[string]string{"env": "prod"}}, http.StatusOK)
			},
			state: target("t-relabel"),
		},
		"PUT /subjects/{subject}": {
			row:   `SELECT xmin::text FROM subjects WHERE tenant = $1 AND subject_id = 'carol@example.com'`,
			event: audit.EventSubjectPut, method: "PUT", path: "/subjects/carol@example.com",
			body: map[string]any{"groups": []string{"sre"}}, status: http.StatusOK,
			state: subject("carol@example.com"), after: "sre",
		},
		"DELETE /subjects/{subject}": {
			event: audit.EventSubjectDeleted, method: "DELETE", path: "/subjects/dave@example.com", status: http.StatusNoContent,
			setup: func(t *testing.T) {
				mustSend(t, "PUT", "/subjects/dave@example.com", map[string]any{"groups": []string{"ops"}}, http.StatusOK)
			},
			state: subject("dave@example.com"), before: "ops",
		},
		"POST /subjects/{subject}/lock": {
			row:   `SELECT xmin::text FROM subject_locks WHERE tenant = $1 AND subject_id = 'gina@example.com'`,
			event: audit.EventSubjectLocked, method: "POST", path: "/subjects/gina@example.com/lock",
			body: map[string]any{"reason_code": "compromised"}, status: http.StatusOK,
			setup: func(t *testing.T) {
				mustSend(t, "PUT", "/subjects/gina@example.com", map[string]any{"groups": []string{"sre"}}, http.StatusOK)
			},
			state: func(t *testing.T) string { return count(t, `SELECT count(*) FROM subject_locks WHERE tenant = $1`) },
		},
		"POST /subjects/{subject}/unlock": {
			event: audit.EventSubjectUnlocked, method: "POST", path: "/subjects/hank@example.com/unlock",
			body: map[string]any{"reason_code": "cleared"}, status: http.StatusOK,
			setup: func(t *testing.T) {
				mustSend(t, "PUT", "/subjects/hank@example.com", map[string]any{"groups": []string{"sre"}}, http.StatusOK)
				mustSend(t, "POST", "/subjects/hank@example.com/lock", map[string]any{}, http.StatusOK)
			},
			state: func(t *testing.T) string { return count(t, `SELECT count(*) FROM subject_locks WHERE tenant = $1`) },
		},
		"POST /targets/{target}/quarantine": {
			row:   `SELECT xmin::text FROM target_quarantines WHERE tenant = $1 AND target_id = 't-quarantine'`,
			event: audit.EventTargetQuarantined, method: "POST", path: "/targets/t-quarantine/quarantine",
			body: map[string]any{"reason_code": "compromised"}, status: http.StatusOK,
			setup: func(t *testing.T) {
				mustSend(t, "PUT", "/targets/t-quarantine", map[string]any{"hostname": "quarantine.example.com", "labels": map[string]string{"env": "prod"}}, http.StatusOK)
			},
			state: func(t *testing.T) string {
				return count(t, `SELECT count(*) FROM target_quarantines WHERE tenant = $1`)
			},
		},
		"POST /targets/{target}/release": {
			event: audit.EventTargetReleased, method: "POST", path: "/targets/t-release/release",
			body: map[string]any{"reason_code": "cleared"}, status: http.StatusOK,
			setup: func(t *testing.T) {
				mustSend(t, "PUT", "/targets/t-release", map[string]any{"hostname": "release.example.com", "labels": map[string]string{"env": "prod"}}, http.StatusOK)
				mustSend(t, "POST", "/targets/t-release/quarantine", map[string]any{}, http.StatusOK)
			},
			state: func(t *testing.T) string {
				return count(t, `SELECT count(*) FROM target_quarantines WHERE tenant = $1`)
			},
		},
		"POST /role-bindings": {
			row:   `SELECT xmin::text FROM identity_role_bindings WHERE tenant = $1 AND subject_id = 'erin@example.com'`,
			event: audit.EventRoleBound, method: "POST", path: "/role-bindings",
			body: map[string]any{"subject_id": "erin@example.com", "role": "auditor"}, status: http.StatusOK,
			state: func(t *testing.T) string {
				return count(t, `SELECT count(*) FROM identity_role_bindings WHERE tenant = $1`)
			},
		},
		"POST /role-bindings/remove": {
			event: audit.EventRoleUnbound, method: "POST", path: "/role-bindings/remove",
			body: map[string]any{"subject_id": "frank@example.com", "role": "auditor"}, status: http.StatusOK,
			setup: func(t *testing.T) {
				mustSend(t, "POST", "/role-bindings", map[string]any{"subject_id": "frank@example.com", "role": "auditor"}, http.StatusOK)
			},
			state: func(t *testing.T) string {
				return count(t, `SELECT count(*) FROM identity_role_bindings WHERE tenant = $1`)
			},
		},
		"PUT /identity/claim-mapping": {
			row:   `SELECT xmin::text FROM claim_mappings WHERE tenant = $1 AND active`,
			event: audit.EventClaimMappingActivated, method: "PUT", path: "/identity/claim-mapping",
			body:   map[string]any{"document": "schema_version: 1\nclaims:\n  - claim: email\n    attribute: email\n"},
			status: http.StatusOK,
			state:  func(t *testing.T) string { return count(t, `SELECT count(*) FROM claim_mappings WHERE tenant = $1`) },
			after:  "1",
		},
		"POST /proxies/enrollments": {
			row:   `SELECT xmin::text FROM proxy_enrollments WHERE tenant = $1 AND proxy_id = 'proxy-new'`,
			event: audit.EventProxyApproved, method: "POST", path: "/proxies/enrollments",
			body: map[string]any{"proxy_id": "proxy-new", "zones": []string{"edge"}}, status: http.StatusCreated,
			state: func(t *testing.T) string { return count(t, `SELECT count(*) FROM proxy_enrollments WHERE tenant = $1`) },
		},
		"POST /proxies/{proxy}/withdraw": {
			row:   `SELECT xmin::text FROM proxies WHERE tenant = $1 AND proxy_id = 'proxy-old'`,
			event: audit.EventProxyWithdrawn, method: "POST", path: "/proxies/proxy-old/withdraw", status: http.StatusOK,
			setup: func(t *testing.T) {
				if err := f.st.Proxies().Upsert(ctx, tenant, store.Proxy{ID: "proxy-old", Zone: "edge", State: store.EnrollmentEnrolled}); err != nil {
					t.Fatal(err)
				}
				if err := f.st.ProxyTokens().Insert(ctx, tenant, store.ProxyAPIToken{
					TokenID: "tok-old", ProxyID: "proxy-old", TokenHash: []byte("0123456789abcdef0123456789abcdef"),
					IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
				}); err != nil {
					t.Fatal(err)
				}
			},
			state: func(t *testing.T) string {
				p, err := f.st.Proxies().Get(ctx, tenant, "proxy-old")
				return render(t, p.State, err) + "/" + count(t, `SELECT count(*) FROM proxy_api_tokens WHERE tenant = $1 AND revoked_at IS NULL`)
			},
			before: "enrolled", after: "revoked",
		},
		"PUT /fleet/config/{scope}/{id}": {
			row:   `SELECT xmin::text FROM proxy_config_desired WHERE tenant = $1 AND scope_id = 'pub'`,
			event: audit.EventFleetConfigPublished, method: "PUT", path: "/fleet/config/zone/pub",
			body: map[string]any{"settings": map[string]any{"logging.batch_size": 50}}, status: http.StatusOK,
			state: desired("pub"), after: "1",
		},
		"POST /fleet/config/{scope}/{id}/rollback": {
			row:   `SELECT xmin::text FROM proxy_config_desired WHERE tenant = $1 AND scope_id = 'back'`,
			event: audit.EventFleetConfigRolledBack, method: "POST", path: "/fleet/config/zone/back/rollback", status: http.StatusOK,
			setup: func(t *testing.T) {
				for _, n := range []int{50, 60} {
					mustSend(t, "PUT", "/fleet/config/zone/back", map[string]any{"settings": map[string]any{"logging.batch_size": n}}, http.StatusOK)
				}
			},
			state: desired("back"), before: "2", after: "1",
		},
		"POST /audit/retention": {
			row:   `SELECT xmin::text FROM audit_retention WHERE tenant = $1`,
			event: audit.EventAuditRetained, method: "POST", path: "/audit/retention", status: http.StatusOK,
			body: map[string]any{"stream": audit.StreamFor("proxy-ret"), "through_seq": 2},
			setup: func(t *testing.T) {
				var recs []contract.LogRecord
				for i := range 4 {
					recs = append(recs, contract.LogRecord{
						RecordID: fmt.Sprintf("ret-%d", i), SessionID: "sess-ret", Kind: "session_start",
						Severity: contract.SeverityInfo, Timestamp: time.Now().UTC().Format(time.RFC3339),
					})
				}
				ingest(t, recs...)
			},
			state: func(t *testing.T) string {
				r, err := f.st.Audit().Retention(ctx, tenant, audit.StreamFor("proxy-ret"))
				return render(t, r.ThroughSeq, err)
			},
		},
		"POST /audit/captures/retention": {
			row:   `SELECT xmin::text FROM audit_captures WHERE tenant = $1 AND record_id = 'cap-1'`,
			event: audit.EventAuditRetained, method: "POST", path: "/audit/captures/retention",
			body: map[string]any{"before": time.Now().Add(-24 * time.Hour)}, status: http.StatusOK,
			setup: func(t *testing.T) {
				ingest(t, contract.LogRecord{
					RecordID: "cap-1", SessionID: "sess-cap", Kind: "stream", Severity: contract.SeverityInfo,
					Timestamp: time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
					Payload:   base64.StdEncoding.EncodeToString([]byte("id\r\n")),
				})
			},
			state: func(t *testing.T) string {
				return count(t, `SELECT count(*) FROM audit_captures WHERE tenant = $1 AND retained_at IS NOT NULL`)
			},
		},
	}
	others := map[string]string{
		"POST /policy/validate":                               "a read: compiles and reports, stores nothing",
		"POST /policy/diff":                                   "a read: diffs a source against a stored version",
		"POST /policy/simulate":                               "a read: simulation stores nothing",
		"POST /revocations":                                   "no transaction: an event on the bus, recorded after it (TestEveryMutatingActionIsAuditedWithItsActor)",
		"PUT /access-context/providers/{provider}/binding":    "accessctx's own transaction: TestABindingWriteCommitsWithItsRecordOrNotAtAll",
		"DELETE /access-context/providers/{provider}/binding": "accessctx's own transaction: TestABindingWriteCommitsWithItsRecordOrNotAtAll",
		"POST /ca/rotate":                                     "0011's, audited by the CA",
		"POST /grants":                                        "0012's, audited by internal/access in the grant's transaction",
		"POST /grants/{grant}/revoke":                         "0012's, audited by internal/access",
		"POST /grant-requests/{request}/cancel":               "0012's, audited by internal/access",
		"POST /access-context/{provider}/push":                "0013's, audited by accessctx",
		"POST /me/grant-requests":                             "0016's, audited by internal/access",
		"POST /me/grant-requests/{request}/cancel":            "0016's, audited by internal/access",
	}
	for _, route := range f.server.Routes() {
		if route.Method == http.MethodGet || route.Provider != "" || (route.Access != "tenant" && route.Access != "self") {
			continue
		}
		key := route.Method + " /" + strings.TrimPrefix(route.Pattern, controlTenantPrefix)
		if _, ok := writes[key]; !ok && others[key] == "" {
			t.Errorf("%s is neither a write this test checks nor listed with a reason", key)
		}
	}

	if _, err := f.st.Pool().Exec(ctx, `
		CREATE FUNCTION refuse_record() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.event = TG_ARGV[0] THEN
				RAISE EXCEPTION 'the record of % cannot be written', TG_ARGV[0];
			END IF;
			RETURN NEW;
		END $$`); err != nil {
		t.Fatal(err)
	}
	// In a fixed order: some writes act on what an earlier one stored.
	keys := []string{
		"POST /policy/bundles", "POST /policy/bundles/{version}/activate", "POST /policy/apply",
	}
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
			was := w.state(t)
			if _, err := f.st.Pool().Exec(ctx, fmt.Sprintf(
				`CREATE TRIGGER refuse_record BEFORE INSERT ON audit_records FOR EACH ROW EXECUTE FUNCTION refuse_record('%s')`,
				w.event)); err != nil {
				t.Fatal(err)
			}
			resp := f.send(t, w.method, api+w.path, admin, w.body)
			if _, err := f.st.Pool().Exec(ctx, `DROP TRIGGER refuse_record ON audit_records`); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode < 500 {
				t.Errorf("with its record refused, the write answered %d", resp.StatusCode)
			}
			if now := w.state(t); now != was {
				t.Errorf("with its record refused, the write happened anyway:\n was %s\n now %s", was, now)
			}

			recorded := len(f.operatorRecords(t, w.event))
			mustStatus(t, f.send(t, w.method, api+w.path, admin, w.body), w.status)
			recs := f.operatorRecords(t, w.event)
			if len(recs) <= recorded {
				t.Fatalf("the write recorded no %s", w.event)
			}
			attrs := recs[0].Attributes
			if attrs[audit.AttrPrincipalID] == "" {
				t.Errorf("the record names no actor: %v", attrs)
			}
			if w.before != "" && attrs[audit.AttrBefore] != w.before {
				t.Errorf("before = %q, want %q", attrs[audit.AttrBefore], w.before)
			}
			if w.after != "" && attrs[audit.AttrAfter] != w.after {
				t.Errorf("after = %q, want %q", attrs[audit.AttrAfter], w.after)
			}
			if w.row != "" {
				var act, rec string
				if err := f.st.Pool().QueryRow(ctx, w.row, string(tenant)).Scan(&act); err != nil {
					t.Fatalf("%s: %v", w.row, err)
				}
				if err := f.st.Pool().QueryRow(ctx, `SELECT xmin::text FROM audit_records WHERE tenant = $1 AND record_id = $2`,
					string(tenant), recs[0].RecordID).Scan(&rec); err != nil {
					t.Fatal(err)
				}
				if act != rec {
					t.Errorf("the act was committed by transaction %s and its record by %s", act, rec)
				}
			}
		})
	}
}
