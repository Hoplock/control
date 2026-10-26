// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Every north-bound list is bounded by its caller and says where it stopped
// (M26). The test below walks the ROUTER, as the isolation tests do, so a `GET`
// route added later fails it until it is classified here: a paged list, a
// signal-only list, an exemption with its reason, or — until 0037 deletes the
// map — a list a later chunk pages.
//
// A host's routes are not classified. Control cannot see which of them is a
// list, so nothing here enforces the shape on them (M26.7); `server` offers it.

// pagedList is one view of a list answering M26's shape, and how to fill it.
// A route whose filter makes two views of one list names both, and each is
// walked: a cursor from one is refused on the other.
type pagedList struct {
	member string     // the list's member in the answer
	path   string     // the list in tenant-a
	query  url.Values // the view's filter; nil for the whole list
	// seed stores at least two items of the view in tenant-a and returns how
	// many the view then holds.
	seed func(t *testing.T, f *serverFixture) int
	// token is the credential the view is read with: an administrator of
	// tenant-a when nil. A self route is read as the requester whose list
	// it is (0016).
	token func(t *testing.T, f *serverFixture) string
}

// reader is the credential the view is read with.
func (l pagedList) reader(t *testing.T, f *serverFixture) string {
	t.Helper()
	if l.token != nil {
		return l.token(t, f)
	}
	return adminToken(t, f, "tenant-a")
}

// name is the view as a test names it: its route and its filter.
func (l pagedList) name(route string) string {
	if len(l.query) == 0 {
		return route
	}
	return route + "?" + l.query.Encode()
}

// at is the view's path with these parameters beside its filter.
func (l pagedList) at(q url.Values) string {
	all := url.Values{}
	for k, v := range l.query {
		all[k] = slices.Clone(v)
	}
	for k, v := range q {
		all[k] = slices.Clone(v)
	}
	return withQuery(l.path, all)
}

var pagedLists = map[string][]pagedList{
	"GET /api/v1/tenants/{tenant}/targets": {{
		member: "targets", path: "/api/v1/tenants/tenant-a/targets",
		seed: func(t *testing.T, f *serverFixture) int {
			t.Helper()
			for _, id := range []string{"t-3", "t-1", "t-2"} {
				if err := f.st.Targets().Upsert(t.Context(), "tenant-a",
					storetest.Target(id, id+".example", map[string]string{"env": "prod"})); err != nil {
					t.Fatalf("seed target %s: %v", id, err)
				}
			}
			got, err := f.st.Targets().Page(t.Context(), "tenant-a", nil, store.Page{Limit: store.MaxPageLimit})
			if err != nil || got.More {
				t.Fatalf("count targets: %v (more=%t)", err, got.More)
			}
			return len(got.Rows)
		},
	}},
	"GET /api/v1/tenants/{tenant}/subjects": {{
		member: "subjects", path: "/api/v1/tenants/tenant-a/subjects",
		seed: func(t *testing.T, f *serverFixture) int {
			t.Helper()
			for _, id := range []string{"carol@example.com", "alice@example.com", "bob@example.com"} {
				if err := f.st.Subjects().Upsert(t.Context(), "tenant-a", storetest.Subject(id)); err != nil {
					t.Fatalf("seed subject %s: %v", id, err)
				}
			}
			got, err := f.st.Subjects().Page(t.Context(), "tenant-a", store.Page{Limit: store.MaxPageLimit})
			if err != nil || got.More {
				t.Fatalf("count subjects: %v (more=%t)", err, got.More)
			}
			return len(got.Rows)
		},
	}},
	"GET /api/v1/tenants/{tenant}/proxies": {{
		member: "proxies", path: "/api/v1/tenants/tenant-a/proxies",
		seed: func(t *testing.T, f *serverFixture) int {
			t.Helper()
			for _, id := range []string{"p-3", "p-1", "p-2"} {
				if err := f.st.Proxies().Upsert(t.Context(), "tenant-a", storetest.Proxy(id, "zone-a")); err != nil {
					t.Fatalf("seed proxy %s: %v", id, err)
				}
			}
			got, err := f.st.Proxies().Page(t.Context(), "tenant-a", store.Page{Limit: store.MaxPageLimit})
			if err != nil || got.More {
				t.Fatalf("count proxies: %v (more=%t)", err, got.More)
			}
			return len(got.Rows)
		},
	}},
	// The rollout view and its drifted view: each seed puts caught-up and
	// drifted states side by side, so a view that ignored its filter walks
	// more items than it holds.
	"GET /api/v1/tenants/{tenant}/fleet/config/rollouts": {
		{
			member: "rollouts", path: "/api/v1/tenants/tenant-a/fleet/config/rollouts",
			seed: func(t *testing.T, f *serverFixture) int {
				t.Helper()
				seedRollouts(t, f, "tenant-a", map[string]bool{"r-1": true, "r-2": false, "r-3": true})
				return countRollouts(t, f, false)
			},
		},
		{
			member: "rollouts", path: "/api/v1/tenants/tenant-a/fleet/config/rollouts",
			query: url.Values{"drifted": {"true"}},
			seed: func(t *testing.T, f *serverFixture) int {
				t.Helper()
				seedRollouts(t, f, "tenant-a", map[string]bool{"d-1": true, "d-2": false, "d-3": true, "d-4": false, "d-5": true})
				return countRollouts(t, f, true)
			},
		},
	},
	"GET /api/v1/tenants/{tenant}/fleet/algorithms": {{
		member: "declarations", path: "/api/v1/tenants/tenant-a/fleet/algorithms",
		seed: func(t *testing.T, f *serverFixture) int {
			t.Helper()
			for _, id := range []string{"p-3", "p-1", "p-2"} {
				seedDeclaration(t, f, "tenant-a", id, `{}`)
			}
			got, err := f.st.ProxyAlgorithmDeclarations().Page(t.Context(), "tenant-a", store.Page{Limit: store.MaxPageLimit})
			if err != nil || got.More {
				t.Fatalf("count declarations: %v (more=%t)", err, got.More)
			}
			return len(got.Rows)
		},
	}},
	// The two observation lists (M26.5). Each seed shares a hostname across
	// rows, so the walk continues from every component of the key.
	"GET /api/v1/tenants/{tenant}/fleet/target-observations/rungs": {{
		member: "rungs", path: "/api/v1/tenants/tenant-a/fleet/target-observations/rungs",
		seed: func(t *testing.T, f *serverFixture) int {
			t.Helper()
			seedRungs(t, f, "tenant-a", time.Now(), "h-1:22:linux", "h-1:2222:", "h-1:22:", "h-2:22:")
			got, err := f.st.TargetCapabilities().Page(t.Context(), "tenant-a", store.Page{Limit: store.MaxPageLimit})
			if err != nil || got.More {
				t.Fatalf("count rungs: %v (more=%t)", err, got.More)
			}
			return len(got.Rows)
		},
	}},
	// The operator's grants (0033), whole and under each filter. Each seed
	// puts a grant the view leaves out beside the ones it holds, so a view
	// that ignored its filter walks more items than it holds.
	"GET /api/v1/tenants/{tenant}/grants": {
		{
			member: "grants", path: "/api/v1/tenants/tenant-a/grants",
			seed: func(t *testing.T, f *serverFixture) int {
				t.Helper()
				seedGrants(t, f, "tenant-a", "all", "alice@example.com", 3)
				return countGrants(t, f, store.GrantQuery{})
			},
		},
		{
			member: "grants", path: "/api/v1/tenants/tenant-a/grants",
			query: url.Values{"subject": {"bob@example.com"}},
			seed: func(t *testing.T, f *serverFixture) int {
				t.Helper()
				seedGrants(t, f, "tenant-a", "bob", "bob@example.com", 3)
				seedGrants(t, f, "tenant-a", "not-bob", "carol@example.com", 2)
				return countGrants(t, f, store.GrantQuery{SubjectID: "bob@example.com"})
			},
		},
		{
			member: "grants", path: "/api/v1/tenants/tenant-a/grants",
			query: url.Values{"state": {"revoked"}},
			seed: func(t *testing.T, f *serverFixture) int {
				t.Helper()
				seedGrants(t, f, "tenant-a", "revoked", "alice@example.com", 3)
				for i := range 3 {
					if _, err := f.st.Grants().Revoke(t.Context(), "tenant-a", fmt.Sprintf("revoked-%d", i),
						store.GrantRevocation{At: time.Now(), Reason: "test"}); err != nil {
						t.Fatalf("revoke: %v", err)
					}
				}
				return countGrants(t, f, store.GrantQuery{State: store.GrantRevoked, At: time.Now()})
			},
		},
	},
	// A requester's own requests (0016), read as that requester: bob's
	// requests sit between alice's, so a view that read the tenant's would
	// walk more items than alice holds.
	"GET /api/v1/tenants/{tenant}/me/grant-requests": {{
		member: "requests", path: "/api/v1/tenants/tenant-a/me/grant-requests",
		seed: func(t *testing.T, f *serverFixture) int {
			t.Helper()
			seedOwnRequests(t, f, "alice@example.com", "bob@example.com", "alice@example.com", "bob@example.com", "alice@example.com")
			return countOwnRequests(t, f, "alice@example.com")
		},
		token: func(t *testing.T, f *serverFixture) string {
			t.Helper()
			token, _ := f.requester(t, "alice@example.com")
			return token
		},
	}},
	// The role bindings and the outstanding certificates (0034). The
	// bindings' seed holds one role by a subject and by a group of the same
	// name, so the walk continues from every component of the key. The CA
	// route needs the fixture's CA, which the seed issues through.
	"GET /api/v1/tenants/{tenant}/role-bindings": {{
		member: "bindings", path: "/api/v1/tenants/tenant-a/role-bindings",
		seed: func(t *testing.T, f *serverFixture) int {
			t.Helper()
			seedBindings(t, f, "tenant-a",
				store.RoleBinding{SubjectID: "ops", Role: "auditor"},
				store.RoleBinding{GroupID: "ops", Role: "auditor"},
				store.RoleBinding{GroupID: "sre", Role: "admin"})
			got, err := f.st.RoleBindings().Page(t.Context(), "tenant-a", store.Page{Limit: store.MaxPageLimit})
			if err != nil || got.More {
				t.Fatalf("count role bindings: %v (more=%t)", err, got.More)
			}
			return len(got.Rows)
		},
	}},
	"GET /api/v1/tenants/{tenant}/ca/certificates": {{
		member: "certificates", path: "/api/v1/tenants/tenant-a/ca/certificates",
		seed: func(t *testing.T, f *serverFixture) int {
			t.Helper()
			issueCertificates(t, f, "tenant-a", 2)
			got, err := f.ca.OutstandingPage(t.Context(), "tenant-a", store.Page{Limit: store.MaxPageLimit})
			if err != nil || got.More {
				t.Fatalf("count certificates: %v (more=%t)", err, got.More)
			}
			return len(got.Rows)
		},
	}},
	// The policy version history (0035), seeded through the surface as an
	// author uploads.
	"GET /api/v1/tenants/{tenant}/policy/bundles": {{
		member: "versions", path: "/api/v1/tenants/tenant-a/policy/bundles",
		seed: func(t *testing.T, f *serverFixture) int {
			t.Helper()
			uploadVersions(t, f, "bounded", 2)
			return countVersions(t, f)
		},
	}},
	"GET /api/v1/tenants/{tenant}/fleet/target-observations/key-exchanges": {{
		member: "key_exchanges", path: "/api/v1/tenants/tenant-a/fleet/target-observations/key-exchanges",
		seed: func(t *testing.T, f *serverFixture) int {
			t.Helper()
			seedKex(t, f, "tenant-a", time.Now(), "h-1:2222", "h-1:22", "h-2:22")
			got, err := f.st.TargetKexObservations().Page(t.Context(), "tenant-a", store.Page{Limit: store.MaxPageLimit})
			if err != nil || got.More {
				t.Fatalf("count key exchanges: %v (more=%t)", err, got.More)
			}
			return len(got.Rows)
		},
	}},
	// The audit query (0036): five lists, each read by recorded-at, stream
	// and chain sequence, newest first or with `?order=asc`. Every seed
	// ingests its records from two proxies at one instant, so each walk
	// crosses a tie between two streams.
	"GET /api/v1/tenants/{tenant}/audit/records": {
		auditView("records", "/api/v1/tenants/tenant-a/audit/records", nil, store.AuditQuery{}),
		auditView("records", "/api/v1/tenants/tenant-a/audit/records", url.Values{"order": {"asc"}},
			store.AuditQuery{Ascending: true}),
		auditView("records", "/api/v1/tenants/tenant-a/audit/records", url.Values{"kind": {"provisioning", "command"}},
			store.AuditQuery{Kinds: []string{"command", "provisioning"}}),
	},
	"GET /api/v1/tenants/{tenant}/audit/drift": {
		auditView("changes", "/api/v1/tenants/tenant-a/audit/drift", nil, audit.DriftQuery{}.Selection()),
		auditView("changes", "/api/v1/tenants/tenant-a/audit/drift", url.Values{"order": {"asc"}},
			audit.DriftQuery{Ascending: true}.Selection()),
	},
	"GET /api/v1/tenants/{tenant}/audit/algorithm-policy-unmet": {
		auditView("unmet", "/api/v1/tenants/tenant-a/audit/algorithm-policy-unmet", nil, audit.UnmetQuery{}.Selection()),
	},
	"GET /api/v1/tenants/{tenant}/audit/gaps": {
		auditView("gaps", "/api/v1/tenants/tenant-a/audit/gaps", nil, audit.GapQuery{}.Selection()),
	},
	"GET /api/v1/tenants/{tenant}/audit/certificates/{serial}/records": {
		auditView("records", "/api/v1/tenants/tenant-a/audit/certificates/"+boundedSerial+"/records", nil,
			audit.CertificateQuery{Serial: 9}.Selection()),
	},
}

// boundedSerial is the certificate serial seedAuditLists' records name.
const boundedSerial = "9"

// auditView is one view of an audit list: it seeds every audit list and
// counts the records sel selects, which is what the view holds.
func auditView(member, path string, query url.Values, sel store.AuditQuery) pagedList {
	return pagedList{member: member, path: path, query: query, seed: func(t *testing.T, f *serverFixture) int {
		t.Helper()
		seedAuditLists(t, f, "tenant-a")
		got, err := f.st.Audit().Page(t.Context(), "tenant-a", sel, store.Page{Limit: store.MaxPageLimit})
		if err != nil || got.More {
			t.Fatalf("count %s: %v (more=%t)", path, err, got.More)
		}
		return len(got.Rows)
	}}
}

var auditSeq atomic.Int64

// auditInstant is when every record seedAuditLists ingests happened.
var auditInstant = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// seedAuditLists ingests into tenant, from each of two proxies and all at
// auditInstant, a record for each audit list: a command, a drift change, an
// unmet algorithm policy, a gap and a provisioning record naming
// boundedSerial. So every list holds records of one instant in two streams,
// and a walk one item a page crosses the tiebreak (M26.4).
func seedAuditLists(t *testing.T, f *serverFixture, tenant store.Tenant) {
	t.Helper()
	in, err := audit.New(audit.Options{Store: f.st})
	if err != nil {
		t.Fatal(err)
	}
	n := auditSeq.Add(1)
	at := auditInstant.Format(time.RFC3339)
	for _, proxy := range []string{"proxy-x", "proxy-y"} {
		rec := func(kind, name string, attrs map[string]string) contract.LogRecord {
			return contract.LogRecord{
				RecordID: fmt.Sprintf("%s-%s-%d", name, proxy, n), SessionID: fmt.Sprintf("sess-%d", n),
				Kind: kind, Severity: contract.SeverityInfo, Timestamp: at, Message: name,
				Target: "db-1.example", Attributes: attrs,
			}
		}
		recs := []contract.LogRecord{
			rec("command", "cmd", nil),
			rec("provisioning", "drift", map[string]string{
				audit.AttrEvent: audit.EventDeviceConfigChange, audit.AttrDeviceChangeOp: "create",
				audit.AttrTargetAccount: "hl-" + proxy,
			}),
			rec("error", "unmet", map[string]string{
				audit.AttrEvent: audit.EventAlgorithmPolicyUnmet, audit.AttrAlgorithmAxis: "host_key",
				audit.AttrAlgorithmPolicyCause: "profile", audit.AttrAlgorithmProfile: "default",
			}),
			rec("error", "gap", map[string]string{
				audit.AttrEvent: audit.EventLoggingGap, audit.AttrGapCause: "evicted", audit.AttrGapRecords: "1",
			}),
			rec("provisioning", "cert", map[string]string{
				audit.AttrCredentialMethod: "brokered-certificate", audit.AttrCredentialRung: "1",
				audit.AttrCertificateSerial: boundedSerial,
			}),
		}
		if _, err := in.Ingest(t.Context(), audit.Submission{Tenant: tenant, ProxyID: proxy, Records: recs}); err != nil {
			t.Fatalf("seed audit records from %s: %v", proxy, err)
		}
	}
}

// seedGrants stores n live grants for holder in tenant, prefix-0 the newest,
// one second apart.
func seedGrants(t *testing.T, f *serverFixture, tenant store.Tenant, prefix, holder string, n int) {
	t.Helper()
	at := time.Now().UTC()
	for i := range n {
		id := fmt.Sprintf("%s-%d", prefix, i)
		if err := f.st.Grants().Insert(t.Context(), tenant, storetest.Grant(id, holder, time.Hour)); err != nil {
			t.Fatalf("seed grant %s: %v", id, err)
		}
		if _, err := f.st.Pool().Exec(t.Context(), `UPDATE grants SET created_at = $3 WHERE tenant = $1 AND grant_id = $2`,
			tenant, id, at.Add(-time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("place grant %s: %v", id, err)
		}
	}
}

func countGrants(t *testing.T, f *serverFixture, q store.GrantQuery) int {
	t.Helper()
	got, err := f.st.Grants().Page(t.Context(), "tenant-a", q, store.Page{Limit: store.MaxPageLimit})
	if err != nil || got.More {
		t.Fatalf("count grants: %v (more=%t)", err, got.More)
	}
	return len(got.Rows)
}

var ownRequestSeq atomic.Int64

// seedOwnRequests stores one pending request in tenant-a for each holder, the
// first the newest, one second apart.
func seedOwnRequests(t *testing.T, f *serverFixture, holders ...string) {
	t.Helper()
	at := time.Now().UTC()
	for i, holder := range holders {
		req := storetest.GrantRequest(fmt.Sprintf("req-%d", ownRequestSeq.Add(1)), holder, time.Hour)
		req.RequestedAt = at.Add(-time.Duration(i) * time.Second)
		if err := f.st.GrantRequests().Insert(t.Context(), "tenant-a", req); err != nil {
			t.Fatalf("seed request %s: %v", req.ID, err)
		}
	}
}

func countOwnRequests(t *testing.T, f *serverFixture, holder string) int {
	t.Helper()
	got, err := f.st.GrantRequests().PageForSubject(t.Context(), "tenant-a", holder, store.GrantRequestQuery{},
		store.Page{Limit: store.MaxPageLimit})
	if err != nil || got.More {
		t.Fatalf("count requests: %v (more=%t)", err, got.More)
	}
	return len(got.Rows)
}

// seedRollouts stores a published configuration state per proxy, drifted
// (never reported) or caught up (applied on the desired document).
func seedRollouts(t *testing.T, f *serverFixture, tenant store.Tenant, drifted map[string]bool) {
	t.Helper()
	for id, d := range drifted {
		if err := f.st.ProxyConfigs().PutState(t.Context(), tenant, store.ProxyConfigState{
			ProxyID: id, DesiredVersion: 1, DesiredHash: string(tenant) + "-h1", DesiredDocument: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("seed state %s: %v", id, err)
		}
		if d {
			continue
		}
		if err := f.st.ProxyConfigs().PutReport(t.Context(), tenant, id, store.ConfigReport{
			State: "applied", RunningVersion: "v1", RunningHash: "sha256:" + string(tenant) + "-h1", LastHeardAt: time.Now(),
		}, 1, string(tenant)+"-h1"); err != nil {
			t.Fatalf("seed report %s: %v", id, err)
		}
	}
}

func countRollouts(t *testing.T, f *serverFixture, drifted bool) int {
	t.Helper()
	got, err := f.st.ProxyConfigs().PageStates(t.Context(), "tenant-a", drifted, store.Page{Limit: store.MaxPageLimit})
	if err != nil || got.More {
		t.Fatalf("count rollouts (drifted %t): %v (more=%t)", drifted, err, got.More)
	}
	return len(got.Rows)
}

// signalList is a list bounded by one object (M26.3): it keeps its
// server-side bound, takes no limit and no cursor, and names the member that
// says the bound cut it.
type signalList struct {
	member string
	// seed stores, in tenant-a, the object the route's sample path names:
	// each `{name}` segment of the pattern is "sample" (samplePath).
	seed func(t *testing.T, f *serverFixture)
}

var signalLists = map[string]signalList{
	// A session's decisions (0035), bounded by its hops.
	"GET /api/v1/tenants/{tenant}/sessions/{session}/decisions": {
		member: "truncated",
		seed: func(t *testing.T, f *serverFixture) {
			t.Helper()
			if err := f.st.Decisions().Insert(t.Context(), "tenant-a", storyRecord("sample-1", "sample", time.Now().UTC())); err != nil {
				t.Fatalf("seed decision: %v", err)
			}
		},
	},
	// The decisions a grant's view samples (0033).
	"GET /api/v1/tenants/{tenant}/grants/{grant}": {
		member: "decisions_truncated",
		seed: func(t *testing.T, f *serverFixture) {
			t.Helper()
			if err := f.st.Grants().Insert(t.Context(), "tenant-a", storetest.Grant("sample", "alice@example.com", time.Hour)); err != nil {
				t.Fatalf("seed grant: %v", err)
			}
		},
	},
}

// exemptRoutes answer one object, or a list bounded by configuration rather
// than by the estate (M26), each with its reason (0028's inventory).
var exemptRoutes = map[string]string{
	"GET /api/v1/session/methods":                                              "one per configured sign-in method",
	"GET /api/v1/session/federated/{connector}/callback":                       "a sign-in step, not a list",
	"GET /api/v1/session/federated/{connector}/metadata":                       "one document",
	"GET /api/v1/session":                                                      "one object",
	"GET /api/v1/tenants/{tenant}/ca":                                          "the trust bundle is answered whole or not at all; one key per rotation inside its overlap",
	"GET /api/v1/tenants/{tenant}/grant-requests/{request}":                    "one object",
	"GET /api/v1/tenants/{tenant}/me/grant-requests/{request}":                 "one object",
	"GET /api/v1/tenants/{tenant}/identity/claim-mapping":                      "one object; its lists are the document's, bounded by the body it was written in",
	"GET /api/v1/tenants/{tenant}/policy/bundles/{version}":                    "one object",
	"GET /api/v1/tenants/{tenant}/policy/bundles/{version}/diff":               "one diff of two stored documents, each bounded by the upload body",
	"GET /api/v1/tenants/{tenant}/policy/active":                               "one object",
	"GET /api/v1/tenants/{tenant}/decisions/{decision}":                        "one object",
	"GET /api/v1/tenants/{tenant}/targets/{target}":                            "one object",
	"GET /api/v1/tenants/{tenant}/subjects/{subject}":                          "one object",
	"GET /api/v1/tenants/{tenant}/extensions":                                  "one row per extension point",
	"GET /api/v1/tenants/{tenant}/access-context/providers":                    "one per configured provider",
	"GET /api/v1/tenants/{tenant}/access-context/bindings":                     "one per configured provider in the tenant (primary key (tenant, provider))",
	"GET /api/v1/tenants/{tenant}/access-context/providers/{provider}/binding": "one object",
	"GET /api/v1/tenants/{tenant}/audit/records/{record}":                      "one object",
	"GET /api/v1/tenants/{tenant}/audit/sessions/{session}/loss":               "one row per gap cause, of which the contract names two",
}

// pendingBounded are the lists a later chunk pages or signals, by the chunk
// that takes each (0028's inventory). Each chunk deletes its rows and moves
// them to the map above that rules them; 0037 deletes this map.
var pendingBounded = map[string]string{
	"GET /api/v1/tenants/{tenant}/audit/blocked-commands":                "0037",
	"GET /api/v1/tenants/{tenant}/audit/negotiated":                      "0037",
	"GET /api/v1/tenants/{tenant}/audit/sessions/{session}/certificates": "0037",
	"GET /api/v1/tenants/{tenant}/audit/sessions/{session}/algorithms":   "0037",
}

// listClasses is the four maps, so the check can be run over another set.
type listClasses struct {
	paged   map[string][]pagedList
	signal  map[string]signalList
	exempt  map[string]string
	pending map[string]string
}

var chunkNumber = regexp.MustCompile(`^[0-9]{4}$`)

// problems names every way routes and the classes disagree: a Control `GET`
// route in no class or in more than one, an entry naming no such route, an
// exemption with no reason, and a pending list with no chunk.
func (c listClasses) problems(routes []north.RouteInfo) []string {
	var out []string
	served := map[string]bool{}
	for _, route := range routes {
		if route.Method != http.MethodGet || route.Provider != "" {
			continue
		}
		name := route.String()
		served[name] = true
		var in []string
		if _, ok := c.paged[name]; ok {
			in = append(in, "pagedLists")
		}
		if _, ok := c.signal[name]; ok {
			in = append(in, "signalLists")
		}
		if _, ok := c.exempt[name]; ok {
			in = append(in, "exemptRoutes")
		}
		if _, ok := c.pending[name]; ok {
			in = append(in, "pendingBounded")
		}
		switch len(in) {
		case 0:
			out = append(out, name+" is a GET route in none of pagedLists, signalLists, exemptRoutes and pendingBounded: "+
				"page it, signal it, or exempt it with its reason (M26)")
		case 1:
		default:
			out = append(out, name+" is classified more than once: "+strings.Join(in, ", "))
		}
	}
	classified := slices.Concat(slices.Collect(maps.Keys(c.paged)), slices.Collect(maps.Keys(c.signal)),
		slices.Collect(maps.Keys(c.exempt)), slices.Collect(maps.Keys(c.pending)))
	for _, name := range classified {
		if !served[name] {
			out = append(out, name+" is classified but is not a GET route Control serves")
		}
	}
	for name, reason := range c.exempt {
		if strings.TrimSpace(reason) == "" {
			out = append(out, name+" is exempt with no reason")
		}
	}
	for name, chunk := range c.pending {
		if !chunkNumber.MatchString(chunk) {
			out = append(out, fmt.Sprintf("%s is pending under %q, which is not a four-digit chunk", name, chunk))
		}
	}
	slices.Sort(out)
	return out
}

func classes() listClasses {
	return listClasses{paged: pagedLists, signal: signalLists, exempt: exemptRoutes, pending: pendingBounded}
}

// listPage is one page of a list as the client reads it.
type listPage struct {
	items     []json.RawMessage
	truncated bool
	next      string
	hasNext   bool
}

func readListPage(t *testing.T, resp *http.Response, member string) listPage {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("answered %d, want 200: %s", resp.StatusCode, decodeError(t, resp).Message)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("body: %v", err)
	}
	var p listPage
	raw, ok := body[member]
	if !ok || string(raw) == "null" {
		t.Fatalf("the answer has no %q list: %v", member, body)
	}
	if err := json.Unmarshal(raw, &p.items); err != nil {
		t.Fatalf("%s: %v", member, err)
	}
	truncated, ok := body["truncated"]
	if !ok || json.Unmarshal(truncated, &p.truncated) != nil {
		t.Fatalf("the answer has no boolean truncated: %s", truncated)
	}
	if next, ok := body["next"]; ok {
		p.hasNext = true
		if err := json.Unmarshal(next, &p.next); err != nil || p.next == "" {
			t.Fatalf("next is %s, want a cursor", next)
		}
	}
	return p
}

// expectInvalid asserts a 400 invalid_request naming field, problem and, when
// it is not empty, the limit.
func expectInvalid(t *testing.T, resp *http.Response, field, problem, limit string) {
	t.Helper()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("answered %d, want 400", resp.StatusCode)
	}
	e := decodeError(t, resp)
	if e.Code != north.CodeInvalidRequest || e.Parameters["field"] != field || e.Parameters["problem"] != problem {
		t.Fatalf("answered %s %v, want %s {field: %s, problem: %s}", e.Code, e.Parameters, north.CodeInvalidRequest, field, problem)
	}
	if got, _ := e.Parameters["limit"].(string); got != limit {
		t.Fatalf("limit parameter %v, want %q", e.Parameters["limit"], limit)
	}
	if e.CorrelationID == "" {
		t.Error("the refusal carries no correlation id")
	}
}

func adminToken(t *testing.T, f *serverFixture, tenants ...store.Tenant) string {
	t.Helper()
	scopes := map[store.Tenant]identity.RoleSet{}
	for _, tenant := range tenants {
		scopes[tenant] = identity.RoleSet{identity.RoleAdmin}
	}
	return f.token(t, tenants[0], scopes)
}

func withQuery(path string, q url.Values) string { return path + "?" + q.Encode() }

func TestEveryListIsBoundedOrSaysWhyNot(t *testing.T) {
	f := newServer(t)
	if problems := classes().problems(f.server.Routes()); len(problems) > 0 {
		t.Fatalf("the router and the list classes disagree:\n  %s", strings.Join(problems, "\n  "))
	}
	token := adminToken(t, f, "tenant-a")

	firstNext := map[string]string{}
	views := map[string]pagedList{}
	for route, ls := range pagedLists {
		for _, l := range ls {
			views[l.name(route)] = l
		}
	}
	for view, l := range views {
		t.Run(view, func(t *testing.T) {
			want := l.seed(t, f)
			if want < 2 {
				t.Fatalf("seed left %d items; a walk of one page proves nothing about the next", want)
			}
			token := l.reader(t, f)

			// 1–3: every refusal is the grammar every malformed parameter
			// uses, never a clamp (M26.1, M26.2, M21).
			expectInvalid(t, f.do(t, "GET", l.at(url.Values{"limit": {"10001"}}), token, nil),
				"limit", access.ProblemExceedsMaximum, "10000")
			for _, bad := range []string{"0", "x"} {
				expectInvalid(t, f.do(t, "GET", l.at(url.Values{"limit": {bad}}), token, nil),
					"limit", access.ProblemInvalid, "")
			}
			expectInvalid(t, f.do(t, "GET", l.at(url.Values{"after": {"not-a-cursor"}}), token, nil),
				"after", access.ProblemInvalid, "")

			// 4: a walk one item at a time visits every item exactly once.
			seen := map[string]bool{}
			q := url.Values{"limit": {"1"}}
			for pages := 0; ; pages++ {
				if pages > want {
					t.Fatalf("the walk ran past %d pages for %d items", pages, want)
				}
				p := readListPage(t, f.do(t, "GET", l.at(q), token, nil), l.member)
				if pages == 0 {
					firstNext[view] = p.next
				}
				if len(p.items) != 1 {
					t.Fatalf("page %d holds %d items, want 1", pages, len(p.items))
				}
				item := string(p.items[0])
				if seen[item] {
					t.Fatalf("page %d repeats %s", pages, item)
				}
				seen[item] = true
				if p.truncated != p.hasNext {
					t.Fatalf("page %d: truncated=%t but next present=%t; next is present exactly when truncated", pages, p.truncated, p.hasNext)
				}
				if !p.truncated {
					break
				}
				q.Set("after", p.next)
			}
			if len(seen) != want {
				t.Fatalf("the walk visited %d items, the list holds %d", len(seen), want)
			}
		})
	}

	// 5: a cursor is bound to its list and its filter. One view's position,
	// presented to another list or to another view of its own, is refused
	// rather than misread.
	for from, next := range firstNext {
		for to, l := range views {
			if to == from {
				continue
			}
			t.Run(from+" → "+to, func(t *testing.T) {
				expectInvalid(t, f.do(t, "GET", l.at(url.Values{"after": {next}}), l.reader(t, f), nil),
					"after", access.ProblemInvalid, "")
			})
		}
	}

	for route, l := range signalLists {
		t.Run(route, func(t *testing.T) {
			l.seed(t, f)
			pattern := strings.TrimPrefix(route, "GET ")
			resp := f.do(t, "GET", samplePath(pattern, "tenant-a"), token, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("answered %d, want 200", resp.StatusCode)
			}
			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("body: %v", err)
			}
			if _, ok := body[l.member].(bool); !ok {
				t.Fatalf("%s is %#v, want a boolean", l.member, body[l.member])
			}
			// It takes no cursor and no limit: a bound the server keeps is
			// not the caller's to move (M26.3).
			for _, q := range []string{"?limit=1", "?after=not-a-cursor"} {
				resp := f.do(t, "GET", samplePath(pattern, "tenant-a")+q, token, nil)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("%s answered %d, want the same 200 as without it", q, resp.StatusCode)
				}
			}
		})
	}
}

// The check above is only worth having if it fails. A route nobody classified,
// one classified twice, an entry naming no route, an exemption with no reason
// and a pending list with no chunk each fail it.
func TestTheListCheckFailsOnARouteNobodyClassified(t *testing.T) {
	var routes []north.RouteInfo
	c := classes()
	for _, m := range []map[string]string{c.exempt, c.pending} {
		for name := range m {
			routes = append(routes, routeNamed(t, name))
		}
	}
	for name := range c.signal {
		routes = append(routes, routeNamed(t, name))
	}
	for name := range c.paged {
		routes = append(routes, routeNamed(t, name))
	}
	if problems := c.problems(routes); len(problems) != 0 {
		t.Fatalf("the classes disagree with their own routes: %v", problems)
	}

	unclassified := north.RouteInfo{Method: "GET", Pattern: "/api/v1/tenants/{tenant}/widgets"}
	host := north.RouteInfo{Method: "GET", Pattern: "/api/v1/tenants/{tenant}/reports", Provider: "test/host"}
	write := north.RouteInfo{Method: "POST", Pattern: "/api/v1/tenants/{tenant}/widgets"}
	if got := c.problems(append(slices.Clone(routes), unclassified, host, write)); len(got) != 1 ||
		!strings.HasPrefix(got[0], "GET /api/v1/tenants/{tenant}/widgets is a GET route in none") {
		t.Errorf("an unclassified GET route beside a host's route and a write: %v, want one problem naming it", got)
	}

	twice := listClasses{paged: c.paged, signal: c.signal, exempt: withEntry(c.exempt, "GET /api/v1/tenants/{tenant}/targets", "one object"), pending: c.pending}
	if got := twice.problems(routes); len(got) != 1 || !strings.Contains(got[0], "classified more than once") {
		t.Errorf("a route both paged and exempt: %v", got)
	}
	gone := listClasses{paged: c.paged, signal: c.signal, exempt: c.exempt, pending: withEntry(c.pending, "GET /api/v1/tenants/{tenant}/gone", "0031")}
	if got := gone.problems(routes); len(got) != 1 || !strings.Contains(got[0], "is not a GET route Control serves") {
		t.Errorf("a pending entry naming no route: %v", got)
	}
	reasonless := listClasses{paged: c.paged, signal: c.signal, exempt: withEntry(c.exempt, "GET /api/v1/session", " "), pending: c.pending}
	if got := reasonless.problems(routes); len(got) != 1 || !strings.Contains(got[0], "exempt with no reason") {
		t.Errorf("an exemption with no reason: %v", got)
	}
	chunkless := listClasses{paged: c.paged, signal: c.signal, exempt: c.exempt, pending: withEntry(c.pending, "GET /api/v1/tenants/{tenant}/audit/negotiated", "later")}
	if got := chunkless.problems(routes); len(got) != 1 || !strings.Contains(got[0], "not a four-digit chunk") {
		t.Errorf("a pending list with no chunk: %v", got)
	}
}

func routeNamed(t *testing.T, name string) north.RouteInfo {
	t.Helper()
	method, pattern, ok := strings.Cut(name, " ")
	if !ok {
		t.Fatalf("%q is not a route name", name)
	}
	return north.RouteInfo{Method: method, Pattern: pattern}
}

func withEntry(m map[string]string, k, v string) map[string]string {
	out := map[string]string{k: v}
	for name, val := range m {
		if name != k {
			out[name] = val
		}
	}
	return out
}

// A cursor is a position, not a scope (M18). It carries the list, the filters'
// digest and the key, and never the tenant it was minted in; presented under
// another tenant's path it continues THAT tenant's list from the position,
// which the middleware has already let the caller read.
func TestACursorFromOneTenantContinuesAnothersList(t *testing.T) {
	f := newServer(t)
	for tenant, ids := range map[store.Tenant][]string{
		"tenant-a": {"a-1", "a-2", "m-a", "z-a"},
		"tenant-b": {"b-1", "m-b", "z-b"},
	} {
		for _, id := range ids {
			if err := f.st.Targets().Upsert(t.Context(), tenant, storetest.Target(id, id+".example", nil)); err != nil {
				t.Fatalf("seed %s: %v", id, err)
			}
		}
	}
	token := adminToken(t, f, "tenant-a", "tenant-b")

	first := readListPage(t, f.do(t, "GET", "/api/v1/tenants/tenant-a/targets?limit=2", token, nil), "targets")
	if !first.truncated {
		t.Fatalf("tenant-a's first page of two does not continue")
	}

	// Nothing in the cursor names the tenant.
	raw, err := base64.RawURLEncoding.DecodeString(first.next)
	if err != nil {
		t.Fatalf("next %q is not unpadded base64url: %v", first.next, err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("next decodes to %q: %v", raw, err)
	}
	if keys := slices.Sorted(maps.Keys(fields)); !slices.Equal(keys, []string{"f", "k", "l", "v"}) {
		t.Errorf("the cursor's members are %v, want exactly f, k, l, v", keys)
	}
	if fields["v"] != float64(1) || fields["l"] != "targets" {
		t.Errorf("the cursor is %s, want v 1 for the targets list", raw)
	}
	if k, _ := fields["k"].([]any); len(k) != 1 || k[0] != "a-2" {
		t.Errorf("the cursor's key is %v, want the last item handed: [a-2]", fields["k"])
	}
	if strings.Contains(string(raw), "tenant") {
		t.Errorf("the cursor %s names a tenant", raw)
	}

	rest := readListPage(t, f.do(t, "GET", "/api/v1/tenants/tenant-b/targets?after="+first.next, token, nil), "targets")
	var ids []string
	for _, item := range rest.items {
		var v struct{ ID string }
		if err := json.Unmarshal(item, &v); err != nil {
			t.Fatalf("item %s: %v", item, err)
		}
		ids = append(ids, v.ID)
	}
	if want := []string{"b-1", "m-b", "z-b"}; !slices.Equal(ids, want) || rest.truncated || rest.hasNext {
		t.Errorf("tenant-b after a-2 answered %v (truncated %t), want only tenant-b's rows after a-2: %v", ids, rest.truncated, want)
	}

	// The middleware still decides who reads tenant-b: a cursor carries no
	// authority with it.
	onlyA := adminToken(t, f, "tenant-a")
	resp := f.do(t, "GET", "/api/v1/tenants/tenant-b/targets?after="+first.next, onlyA, nil)
	if resp.StatusCode != http.StatusForbidden || decodeError(t, resp).Code != north.CodeTenantOutOfScope {
		t.Errorf("a tenant-a credential carrying a cursor reached tenant-b: %d", resp.StatusCode)
	}
}

// The fleet's two lists (0031). The fleet view's `liveness` and the rollout
// view's `fleet_owned_keys` describe the list rather than the page, so every
// page carries them, the last and an empty one included (M26.3).
func TestTheFleetListsDescribeThemselvesOnEveryPage(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	for _, id := range []string{"p-1", "p-2", "p-3"} {
		if err := f.st.Proxies().Upsert(t.Context(), "tenant-a", storetest.Proxy(id, "zone-a")); err != nil {
			t.Fatalf("seed proxy %s: %v", id, err)
		}
	}
	seedRollouts(t, f, "tenant-a", map[string]bool{"p-1": true, "p-2": false, "p-3": true})

	for path, member := range map[string]string{
		"/api/v1/tenants/tenant-a/proxies":                            "liveness",
		"/api/v1/tenants/tenant-a/fleet/config/rollouts":              "fleet_owned_keys",
		"/api/v1/tenants/tenant-a/fleet/config/rollouts?drifted=true": "fleet_owned_keys",
		"/api/v1/tenants/tenant-b/proxies":                            "liveness",
		"/api/v1/tenants/tenant-b/fleet/config/rollouts":              "fleet_owned_keys",
	} {
		t.Run(path, func(t *testing.T) {
			tok := token
			if strings.Contains(path, "tenant-b") {
				tok = adminToken(t, f, "tenant-b") // an empty list
			}
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			var first string
			for next, pages := "", 0; ; pages++ {
				if pages > 3 {
					t.Fatal("the walk ran past three pages")
				}
				q := sep + "limit=2"
				if next != "" {
					q += "&after=" + next
				}
				resp := f.do(t, "GET", path+q, tok, nil)
				var body map[string]json.RawMessage
				if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil {
					t.Fatalf("page %d answered %d", pages, resp.StatusCode)
				}
				described := string(body[member])
				if described == "" || described == "null" {
					t.Fatalf("page %d has no %s: %v", pages, member, body)
				}
				if first == "" {
					first = described
				} else if described != first {
					t.Errorf("page %d's %s is %s, page 0's was %s: it describes the list, not the page", pages, member, described, first)
				}
				var truncated bool
				_ = json.Unmarshal(body["truncated"], &truncated)
				if !truncated {
					break
				}
				_ = json.Unmarshal(body["next"], &next)
			}
		})
	}
}

// The drifted view is drift read in the store (0031): every page holds its
// limit unless it is the last, no row on it is caught up, and a walk of it
// visits exactly the proxies that have not caught up. A `drifted` other than
// `true` is the whole view.
func TestADriftedPageHoldsOnlyDriftAndIsFull(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	drifted := map[string]bool{}
	for i := range 9 {
		drifted[fmt.Sprintf("p-%d", i)] = i%3 != 1 // caught-up rows sit between drifted ones
	}
	seedRollouts(t, f, "tenant-a", drifted)
	// Nothing published is never drift (0021).
	if err := f.st.ProxyConfigs().PutState(t.Context(), "tenant-a", store.ProxyConfigState{ProxyID: "p-9"}); err != nil {
		t.Fatalf("seed p-9: %v", err)
	}
	var want []string
	for id, d := range drifted {
		if d {
			want = append(want, id)
		}
	}
	slices.Sort(want)

	base := "/api/v1/tenants/tenant-a/fleet/config/rollouts"
	var seen []string
	q := url.Values{"drifted": {"true"}, "limit": {"2"}}
	for pages := 0; ; pages++ {
		if pages > len(want) {
			t.Fatal("the drifted walk did not end")
		}
		p := readListPage(t, f.do(t, "GET", withQuery(base, q), token, nil), "rollouts")
		if p.truncated && len(p.items) != 2 {
			t.Fatalf("drifted page %d holds %d rows and continues: drift was filtered after the page", pages, len(p.items))
		}
		for _, item := range p.items {
			var row struct {
				ProxyID        string `json:"proxy_id"`
				DesiredVersion string `json:"desired_version"`
				CaughtUp       bool   `json:"caught_up"`
			}
			if err := json.Unmarshal(item, &row); err != nil {
				t.Fatalf("row %s: %v", item, err)
			}
			if row.CaughtUp || row.DesiredVersion == "" {
				t.Errorf("the drifted view holds %s, which is not drift", item)
			}
			seen = append(seen, row.ProxyID)
		}
		if !p.truncated {
			break
		}
		q.Set("after", p.next)
	}
	if !slices.Equal(seen, want) {
		t.Errorf("the drifted walk saw %v, want %v", seen, want)
	}

	for _, other := range []string{"false", "yes", ""} {
		p := readListPage(t, f.do(t, "GET", withQuery(base, url.Values{"drifted": {other}}), token, nil), "rollouts")
		if len(p.items) != len(drifted)+1 || p.truncated {
			t.Errorf("?drifted=%s answered %d rows (truncated %t), want the whole view's %d", other, len(p.items), p.truncated, len(drifted)+1)
		}
	}
}

// Two tenants (M18): neither fleet list leaks across, from any cursor. Both
// tenants hold identically named proxies and states; a position from
// tenant-a's view continues tenant-b's with tenant-b's rows alone.
func TestNeitherFleetListLeaksAcrossTenantsFromAnyCursor(t *testing.T) {
	f := newServer(t)
	for _, tenant := range []store.Tenant{"tenant-a", "tenant-b"} {
		zone := "zone-" + strings.TrimPrefix(string(tenant), "tenant-")
		for _, id := range []string{"p-1", "p-2", "p-3", "p-4"} {
			if err := f.st.Proxies().Upsert(t.Context(), tenant, storetest.Proxy(id, zone)); err != nil {
				t.Fatalf("seed %s/%s: %v", tenant, id, err)
			}
		}
		seedRollouts(t, f, tenant, map[string]bool{"p-1": true, "p-2": false, "p-3": true, "p-4": true})
	}
	token := adminToken(t, f, "tenant-a", "tenant-b")

	// whose names the tenant a row belongs to, from what each list shows.
	whose := map[string]func(json.RawMessage) string{
		"proxies": func(item json.RawMessage) string {
			var v struct{ ProxyID, Zone string }
			_ = json.Unmarshal(item, &v)
			return v.ProxyID + "@" + strings.Replace(v.Zone, "zone-", "tenant-", 1)
		},
		"rollouts": func(item json.RawMessage) string {
			var v struct {
				ProxyID     string `json:"proxy_id"`
				DesiredHash string `json:"desired_hash"`
			}
			_ = json.Unmarshal(item, &v)
			tenant, _, _ := strings.Cut(strings.TrimPrefix(v.DesiredHash, "sha256:"), "-h1")
			return v.ProxyID + "@" + tenant
		},
	}
	for _, view := range []struct{ path, member, query string }{
		{"proxies", "proxies", ""},
		{"fleet/config/rollouts", "rollouts", ""},
		{"fleet/config/rollouts", "rollouts", "drifted=true&"},
	} {
		t.Run(view.path+"?"+view.query, func(t *testing.T) {
			at := func(tenant, rest string) string {
				return "/api/v1/tenants/" + tenant + "/" + view.path + "?" + view.query + rest
			}
			first := readListPage(t, f.do(t, "GET", at("tenant-a", "limit=1"), token, nil), view.member)
			if !first.truncated {
				t.Fatal("tenant-a's first page of one does not continue")
			}
			if got := whose[view.member](first.items[0]); got != "p-1@tenant-a" {
				t.Fatalf("tenant-a's first row is %s, want p-1@tenant-a", got)
			}
			var rows []string
			for next := first.next; ; {
				p := readListPage(t, f.do(t, "GET", at("tenant-b", "limit=1&after="+next), token, nil), view.member)
				for _, item := range p.items {
					rows = append(rows, whose[view.member](item))
				}
				if !p.truncated {
					break
				}
				next = p.next
			}
			for _, row := range rows {
				if !strings.HasSuffix(row, "@tenant-b") || strings.HasPrefix(row, "p-1@") {
					t.Errorf("tenant-b after tenant-a's p-1 answered %v: only tenant-b's rows, after p-1", rows)
					break
				}
			}
			if len(rows) == 0 {
				t.Error("tenant-b after tenant-a's p-1 answered nothing")
			}
		})
	}
}

// The cursor binds the filters the reader applied: one minted under a label
// filter does not continue the list under another, and it does continue the
// same filter however the query spells it.
func TestATargetCursorIsBoundToItsLabelFilter(t *testing.T) {
	f := newServer(t)
	for i, env := range []string{"prod", "dev", "prod", "prod", "dev"} {
		id := fmt.Sprintf("t-%d", i)
		if err := f.st.Targets().Upsert(t.Context(), "tenant-a",
			storetest.Target(id, id+".example", map[string]string{"env": env, "role": "db"})); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	token := adminToken(t, f, "tenant-a")
	base := "/api/v1/tenants/tenant-a/targets"

	first := readListPage(t, f.do(t, "GET", base+"?label=env=prod&label=role=db&limit=1", token, nil), "targets")
	if !first.truncated {
		t.Fatal("the filtered list's first page of one does not continue")
	}
	for _, other := range []string{"", "label=env=prod&", "label=env=dev&label=role=db&"} {
		expectInvalid(t, f.do(t, "GET", base+"?"+other+"after="+first.next, token, nil), "after", access.ProblemInvalid, "")
	}
	// The same filter, reordered, spaced and with a repeated key whose last
	// value is the one applied, continues the list.
	same := "label=role=db&label=env=dev&label=%20env%20=%20prod%20&after=" + first.next
	rest := readListPage(t, f.do(t, "GET", base+"?"+same, token, nil), "targets")
	if len(rest.items) != 2 || rest.truncated {
		t.Errorf("the same filter continued with %d items (truncated %t), want the two prod targets after t-0", len(rest.items), rest.truncated)
	}
}

// A failure while a page is read is a 5xx with its correlation id, never the
// rows read before it answered as though the list had ended (M26.6, M11). Each
// list here is given a row its reader cannot scan, after one it can, so the
// statement fails part-way through the page. The fleet view's page is three
// reads, and the row is in the second: a read that fails fails the page.
func TestAFailureDuringAListPageIsAnOutageNeverAShortPage(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	for route, unscannable := range map[string]string{
		"/api/v1/tenants/tenant-a/targets": `
			INSERT INTO targets (tenant, target_id, hostname, zone, labels)
			VALUES ('tenant-a', 'u-1', 'u-1.example', 'zone-a', '{}'),
			       ('tenant-a', 'u-2', 'u-2.example', 'zone-a', '{"env": 1}')`,
		"/api/v1/tenants/tenant-a/subjects": `
			INSERT INTO subjects (tenant, subject_id, source, claims)
			VALUES ('tenant-a', 'u-1', 'local', '{}'),
			       ('tenant-a', 'u-2', 'local', '{"sub": 1}')`,
		"/api/v1/tenants/tenant-a/proxies": `
			INSERT INTO proxies (tenant, proxy_id, zone, public_key, enrollment_state)
			VALUES ('tenant-a', 'u-1', 'zone-a', 'k-1', 'enrolled'),
			       ('tenant-a', 'u-2', 'zone-a', 'k-2', 'enrolled');
			INSERT INTO relay_registrations (tenant, upstream_proxy_id, downstream_proxy_id, last_seen_at)
			VALUES ('tenant-a', 'u-2', 'u-2-down', 'infinity')`,
		"/api/v1/tenants/tenant-a/fleet/config/rollouts?drifted=true": `
			INSERT INTO proxy_config_state (tenant, proxy_id, desired_version, restart_required)
			VALUES ('tenant-a', 'u-1', 1, '{}'),
			       ('tenant-a', 'u-2', 1, '{NULL}')`,
	} {
		t.Run(route, func(t *testing.T) {
			if _, err := f.st.Pool().Exec(t.Context(), unscannable); err != nil {
				t.Fatalf("seed: %v", err)
			}
			resp := f.do(t, "GET", route, token, nil)
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("a page that failed part-way answered %d, want 500", resp.StatusCode)
			}
			if e := decodeError(t, resp); e.Code != north.CodeInternal || e.CorrelationID == "" {
				t.Errorf("answered %s with correlation id %q, want %s with one", e.Code, e.CorrelationID, north.CodeInternal)
			}
		})
	}
}
