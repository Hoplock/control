// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package action_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/action"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/standing"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The action executor against Postgres and a real revocation bus (0039): every
// kind, performed through the services an operator's routes call, with the
// decision service reading what it leaves behind.

const (
	tenant   = store.Tenant("acme")
	other    = store.Tenant("globex")
	provider = "hoplock/soar-test"
	alice    = "alice@example.com"
	bob      = "bob@example.com"
	root     = "root@example.com"
)

const bundle = `
schema_version: 1
tenant: acme
groups: [sre]
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

// recordingRevoker is the revocation stream with a switch: it passes
// everything to the real bus, remembers what it was asked to publish, and
// refuses on demand, which is how the stream being down is tested without a
// down stream.
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

// killsOf counts the kills published for a session id or a subject.
func (r *recordingRevoker) killsOf(session, subject string) int {
	n := 0
	for _, k := range r.killed() {
		if (session != "" && slices.Contains(k.SessionIDs, session)) || (subject != "" && k.Subject == subject) {
			n++
		}
	}
	return n
}

type harness struct {
	t         *testing.T
	store     *store.Store
	revoker   *recordingRevoker
	grants    *access.Service
	standing  *standing.Service
	exec      *action.Executor
	decisions *decision.Service
	emitter   *audit.Emitter
	clock     time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st := storetest.New(t)
	h := &harness{t: t, store: st, clock: time.Now().UTC().Truncate(time.Second)}
	log := slog.New(slog.DiscardHandler)

	for _, tn := range []store.Tenant{tenant, other} {
		storetest.Seed(t, st, storetest.Fixture{
			Tenant: tn,
			Subjects: []store.Subject{
				storetest.Subject(alice, "sre"), storetest.Subject(bob, "sre"),
				{ID: root, Source: "local", DisplayName: "root", Groups: []string{"sre"}, BreakGlass: true},
			},
			Targets: []store.Target{{ID: "t-db", Hostname: "db01.example.com", Zone: "edge", Labels: map[string]string{"env": "prod"}}},
			Proxies: []store.Proxy{{
				ID: "proxy-1", Zone: "edge", PublicKey: []byte("key-proxy-1"),
				State: store.EnrollmentEnrolled, LastHeartbeatAt: h.clock,
			}},
		})
	}
	version, err := st.PolicyBundles().NextVersion(t.Context(), tenant)
	if err != nil {
		t.Fatalf("bundle version: %v", err)
	}
	if err := st.PolicyBundles().Insert(t.Context(), tenant, store.PolicyBundle{
		Version: version, Source: []byte(bundle), Hash: "sha256:fixture", UploadedBy: "action_test",
	}); err != nil {
		t.Fatalf("insert bundle: %v", err)
	}
	if err := st.PolicyBundles().Activate(t.Context(), tenant, version); err != nil {
		t.Fatalf("activate bundle: %v", err)
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
	h.exec, err = action.New(action.Options{
		Provider: provider, Store: st, Standing: h.standing, Grants: h.grants, Revoker: h.revoker,
		Recorder: h.emitter, DefaultTenant: tenant, Logger: log,
	})
	if err != nil {
		t.Fatalf("action: %v", err)
	}
	return h
}

// do executes an action and returns the result and the error.
func (h *harness) do(a ext.Action) (ext.ActionResult, error) {
	h.t.Helper()
	if a.Tenant == "" {
		a.Tenant = ext.Tenant(tenant)
	}
	return h.exec.Execute(h.t.Context(), a)
}

// mustDo executes an action that has to succeed.
func (h *harness) mustDo(a ext.Action) ext.ActionResult {
	h.t.Helper()
	res, err := h.do(a)
	if err != nil {
		h.t.Fatalf("Execute(%s %s): %v", a.Kind, a.ActionID, err)
	}
	return res
}

// authorize asks the decision service as a proxy would, for subject s and
// session id sess.
func (h *harness) authorize(s, sess string) decision.Outcome {
	h.t.Helper()
	version := contract.PolicyVersion
	out, err := h.decisions.Authorize(h.t.Context(), tenant, &contract.AuthorizeRequest{
		Identity: contract.Identity{Subject: s, Login: s, Source: "local"}, Target: "db01.example.com",
		AuthMethod: contract.AuthMethodCert, PolicyVersion: &version,
		Capabilities: &contract.ProxyCapabilities{Execution: []string{string(contract.ExecutionNoInteractiveShell)}},
		Conn:         contract.ConnMeta{SessionID: sess, ProxyID: "proxy-1", ClientAddr: "203.0.113.7:52344", Timestamp: h.clock.Format(time.RFC3339)},
	})
	if err != nil {
		h.t.Fatalf("authorize %s: %v", s, err)
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

// ledger counts a tenant's executions.
func (h *harness) ledger(t store.Tenant) int {
	h.t.Helper()
	var n int
	if err := h.store.Pool().QueryRow(h.t.Context(), `SELECT count(*) FROM action_executions WHERE tenant = $1`, string(t)).Scan(&n); err != nil {
		h.t.Fatalf("count the ledger: %v", err)
	}
	return n
}

// grantFor stores a live grant for a subject and the session it backed.
func (h *harness) grantFor(t store.Tenant, id, subject string, backing ...string) store.Grant {
	h.t.Helper()
	g := store.Grant{
		ID: id, SubjectID: subject, Scope: "break-fix", Origin: store.GrantOriginManual,
		NotBefore: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour),
		CreatedBy: store.GrantActor{Principal: "p-admin"}, Reason: "incident",
	}
	if err := h.store.Grants().Insert(h.t.Context(), t, g); err != nil {
		h.t.Fatalf("insert grant: %v", err)
	}
	for _, sess := range backing {
		if err := h.store.Decisions().Insert(h.t.Context(), t, store.Decision{
			ID: "d-" + sess, SubjectID: subject, InputsDigest: "x", Effect: store.DecisionEffectAllow,
			ProxyID: "proxy-1", SessionID: sess, GrantID: id, DecidedAt: time.Now(),
		}); err != nil {
			h.t.Fatalf("insert decision: %v", err)
		}
	}
	return g
}

func contains(err error, parts ...string) bool {
	if err == nil {
		return false
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			return false
		}
	}
	return true
}
