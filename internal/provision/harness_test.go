// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package provision_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/provision"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/standing"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The identity sync's sink against Postgres and a real revocation bus (0040):
// every method, with the decision service and the federation service reading
// what it leaves behind.

const (
	tenant   = store.Tenant("acme")
	other    = store.Tenant("globex")
	provider = "hoplock/scim-test"
)

// bundle allows the sre group anywhere, and nobody else.
const bundle = `
schema_version: 1
tenant: acme
groups: [sre, dba]
rules:
  - id: sre-anywhere
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

// mapping is the tenant's claim mapping: the directory's group named "SRE Team"
// (and its id spelling, "grp-sre") is the policy group sre, "DBAs" is dba, and
// the directory's `department` attribute is the policy attribute of that name.
const mapping = `
schema_version: 1
claims:
  - claim: department
    attribute: department
  - claim: email
    attribute: email
groups:
  claim: groups
  map:
    - external: SRE Team
      group: sre
    - external: grp-sre
      group: sre
    - external: DBAs
      group: dba
`

// recordingRevoker is the revocation stream with a switch: it passes
// everything to the real bus, remembers what it was asked to publish, and
// refuses on demand, which is how the stream being down is tested.
type recordingRevoker struct {
	next *revoke.Bus
	fail atomic.Bool

	mu    sync.Mutex
	kills []revoke.Kill
	invs  []revoke.Invalidation
}

var errStreamDown = errors.New("the stream is down")

func (r *recordingRevoker) Kill(ctx context.Context, t store.Tenant, aud revoke.Audience, k revoke.Kill) (revoke.Receipt, error) {
	if r.fail.Load() {
		return revoke.Receipt{}, errStreamDown
	}
	r.mu.Lock()
	r.kills = append(r.kills, k)
	r.mu.Unlock()
	return r.next.Kill(ctx, t, aud, k)
}

func (r *recordingRevoker) Invalidate(ctx context.Context, t store.Tenant, aud revoke.Audience, inv revoke.Invalidation) (revoke.Receipt, error) {
	if r.fail.Load() {
		return revoke.Receipt{}, errStreamDown
	}
	r.mu.Lock()
	r.invs = append(r.invs, inv)
	r.mu.Unlock()
	return r.next.Invalidate(ctx, t, aud, inv)
}

func (r *recordingRevoker) killed() []revoke.Kill {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]revoke.Kill(nil), r.kills...)
}

func (r *recordingRevoker) invalidated() []revoke.Invalidation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]revoke.Invalidation(nil), r.invs...)
}

type harness struct {
	t          *testing.T
	store      *store.Store
	revoker    *recordingRevoker
	grants     *access.Service
	standing   *standing.Service
	sink       *provision.Sink
	decisions  *decision.Service
	federation *identity.Federation
	emitter    *audit.Emitter
	// at is the directory's clock, advanced by tick.
	at time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st := storetest.New(t)
	h := &harness{t: t, store: st, at: time.Now().UTC().Add(-time.Hour).Truncate(time.Second)}
	log := slog.New(slog.DiscardHandler)
	now := time.Now().UTC()

	for _, tn := range []store.Tenant{tenant, other} {
		storetest.Seed(t, st, storetest.Fixture{
			Tenant:  tn,
			Targets: []store.Target{{ID: "t-db", Hostname: "db01.example.com", Zone: "edge"}},
			Proxies: []store.Proxy{{
				ID: "proxy-1", Zone: "edge", PublicKey: []byte("key-proxy-1"),
				State: store.EnrollmentEnrolled, LastHeartbeatAt: now,
			}},
		})
		version, err := st.PolicyBundles().NextVersion(t.Context(), tn)
		if err != nil {
			t.Fatalf("bundle version: %v", err)
		}
		if err := st.PolicyBundles().Insert(t.Context(), tn, store.PolicyBundle{
			Version: version, Source: []byte(strings.Replace(bundle, "tenant: acme", "tenant: "+string(tn), 1)),
			Hash: "sha256:fixture", UploadedBy: "provision_test",
		}); err != nil {
			t.Fatalf("insert bundle: %v", err)
		}
		if err := st.PolicyBundles().Activate(t.Context(), tn, version); err != nil {
			t.Fatalf("activate bundle: %v", err)
		}
	}

	var err error
	h.federation, err = identity.NewFederation(st, identity.WithFederationLogger(log))
	if err != nil {
		t.Fatal(err)
	}
	for _, tn := range []store.Tenant{tenant, other} {
		if _, err := h.federation.PutMapping(t.Context(), tn, []byte(mapping), "provision_test", nil); err != nil {
			t.Fatalf("mapping: %v", err)
		}
	}

	registry := fleet.New(st, fleet.WithLogger(log), fleet.WithLiveness(fleet.Liveness{HeartbeatTTL: 24 * time.Hour}))
	h.decisions, err = decision.New(decision.Options{
		Store: st, Fleet: registry, Subjects: identity.NewStoreDirectory(st), Logger: log, Refresh: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("decision: %v", err)
	}

	bus, err := revoke.New(revoke.Options{Logger: log})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	h.revoker = &recordingRevoker{next: bus}

	ingest, err := audit.New(audit.Options{Store: st, Logger: log})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	h.emitter, err = audit.NewEmitter(ingest)
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}
	h.grants, err = access.New(access.Options{
		Store: st, Recorder: h.emitter, Revoker: h.revoker, Settle: time.Millisecond, Logger: log,
	})
	if err != nil {
		t.Fatalf("access: %v", err)
	}
	h.standing, err = standing.New(standing.Options{
		Store: st, Recorder: h.emitter, Revoker: h.revoker, Settle: time.Millisecond, Logger: log,
	})
	if err != nil {
		t.Fatalf("standing: %v", err)
	}
	h.sink, err = provision.New(provision.Options{
		Provider: provider, Store: st, Grants: h.grants, Revoker: h.revoker, Recorder: h.emitter,
		DefaultTenant: tenant, Settle: time.Millisecond, Logger: log,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	return h
}

// tick advances the directory's clock and returns it.
func (h *harness) tick() time.Time {
	h.at = h.at.Add(time.Second)
	return h.at
}

// person is a directory record of a person, at the next directory instant.
func (h *harness) person(externalID, username string, groups ...string) ext.SyncedSubject {
	return ext.SyncedSubject{
		Tenant: ext.Tenant(tenant), ExternalID: externalID, Username: username,
		DisplayName: strings.ToUpper(username), Email: username + "@example.com",
		Groups: groups, Active: true, UpdatedAt: h.tick(),
	}
}

// group is a directory group, at the next directory instant.
func (h *harness) group(externalID, name string) ext.SyncedGroup {
	return ext.SyncedGroup{Tenant: ext.Tenant(tenant), ExternalID: externalID, Name: name, UpdatedAt: h.tick()}
}

func (h *harness) mustUpsert(s ext.SyncedSubject) {
	h.t.Helper()
	if err := h.sink.UpsertSubject(h.t.Context(), s); err != nil {
		h.t.Fatalf("UpsertSubject(%s): %v", s.ExternalID, err)
	}
}

func (h *harness) mustGroup(g ext.SyncedGroup) {
	h.t.Helper()
	if err := h.sink.UpsertGroup(h.t.Context(), g); err != nil {
		h.t.Fatalf("UpsertGroup(%s): %v", g.ExternalID, err)
	}
}

// link reads the link of an external id in the tenant.
func (h *harness) link(externalID string) store.DirectoryLink {
	h.t.Helper()
	l, err := h.store.DirectoryLinks().Get(h.t.Context(), tenant, externalID)
	if err != nil {
		h.t.Fatalf("link %s: %v", externalID, err)
	}
	return l
}

// subject reads the subject behind an external id.
func (h *harness) subject(externalID string) store.Subject {
	h.t.Helper()
	s, err := h.store.Subjects().Get(h.t.Context(), tenant, h.link(externalID).SubjectID)
	if err != nil {
		h.t.Fatalf("subject of %s: %v", externalID, err)
	}
	return s
}

// authorize asks the decision service as a proxy would.
func (h *harness) authorize(subjectID, sess string) decision.Outcome {
	h.t.Helper()
	version := contract.PolicyVersion
	out, err := h.decisions.Authorize(h.t.Context(), tenant, &contract.AuthorizeRequest{
		Identity: contract.Identity{Subject: subjectID, Login: subjectID, Source: provider}, Target: "db01.example.com",
		AuthMethod: contract.AuthMethodCert, PolicyVersion: &version,
		Capabilities: &contract.ProxyCapabilities{Execution: []string{string(contract.ExecutionNoInteractiveShell)}},
		Conn: contract.ConnMeta{
			SessionID: sess, ProxyID: "proxy-1", ClientAddr: "203.0.113.7:52344",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		h.t.Fatalf("authorize %s: %v", subjectID, err)
	}
	return out
}

// records returns a tenant's records of an event, newest first.
func (h *harness) records(t store.Tenant, event string) []store.AuditRecord {
	h.t.Helper()
	rows, err := h.store.Audit().Query(h.t.Context(), t, store.AuditQuery{Event: event, Limit: 1000})
	if err != nil {
		h.t.Fatalf("query %s: %v", event, err)
	}
	return rows
}

// syncRecords counts every identity_sync.* record in a tenant.
func (h *harness) syncRecords(t store.Tenant) int {
	h.t.Helper()
	n := 0
	for _, e := range []string{
		audit.EventSyncSubjectProvisioned, audit.EventSyncSubjectUpdated, audit.EventSyncSubjectDeactivated,
		audit.EventSyncSubjectReactivated, audit.EventSyncGroupUpserted, audit.EventSyncGroupDeleted,
		audit.EventSyncRefused,
	} {
		n += len(h.records(t, e))
	}
	return n
}

// verify holds the audit chain of both tenants to its hashes.
func (h *harness) verify() {
	h.t.Helper()
	for _, tn := range []store.Tenant{tenant, other} {
		res, err := audit.NewVerifier(h.store).VerifyTenant(h.t.Context(), tn)
		if err != nil {
			h.t.Fatalf("verify %s: %v", tn, err)
		}
		if !res.OK() {
			h.t.Fatalf("the audit chain of %s does not verify: %+v", tn, res)
		}
	}
}

// grantFor stores a live grant for a subject and the sessions it backed.
func (h *harness) grantFor(id, subject string, backing ...string) {
	h.t.Helper()
	g := store.Grant{
		ID: id, SubjectID: subject, Scope: "break-fix", Origin: store.GrantOriginManual,
		NotBefore: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour),
		CreatedBy: store.GrantActor{Principal: "p-admin"}, Reason: "incident",
	}
	if err := h.store.Grants().Insert(h.t.Context(), tenant, g); err != nil {
		h.t.Fatalf("insert grant: %v", err)
	}
	for _, sess := range backing {
		if err := h.store.Decisions().Insert(h.t.Context(), tenant, store.Decision{
			ID: "d-" + sess, SubjectID: subject, InputsDigest: "x", Effect: store.DecisionEffectAllow,
			ProxyID: "proxy-1", SessionID: sess, GrantID: id, DecidedAt: time.Now(),
		}); err != nil {
			h.t.Fatalf("insert decision: %v", err)
		}
	}
}

// field reads one attribute of a record.
func field(r store.AuditRecord, key string) string { return r.Attributes[key] }

func kindOf(err error) ext.Kind { return ext.KindOf(err) }
