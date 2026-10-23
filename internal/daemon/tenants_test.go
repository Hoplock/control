// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/httpapi/south"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Every live tenant is polled (0016). The store cannot enumerate tenants (M18),
// so the workflow poller walks the tenants this process has seen — and after a
// restart, that used to be the configured one alone. The poller now learns
// every tenant a grant could be used in, by seeing it: a proxy of that tenant
// opening its event subscription, through the very listener serve builds.

const (
	configuredTenant = store.Tenant("acme")
	otherTenant      = store.Tenant("globex")
	holder           = "alice@example.com"
)

const otherTenantBundle = `
schema_version: 1
tenant: globex
description: a grant decides prod access
labels:
  env: [prod]
rules:
  - id: jit-dba
    effect: allow
    match:
      target:
        labels: {env: [prod]}
      grant: {required: true, scopes: [prod-dba]}
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: ephemeral-user
          username: {from: subject-local-part}
          key_type: ed25519
          lifetime_seconds: 900
      max_session_duration: 4h
`

func TestADecisionIsAppliedInATenantWhoseProxySubscribesAfterARestart(t *testing.T) {
	st := storetest.New(t)
	ctx := t.Context()
	log := slog.New(slog.DiscardHandler)
	now := time.Now().UTC()

	storetest.Seed(t, st, storetest.Fixture{
		Tenant:   otherTenant,
		Subjects: []store.Subject{storetest.Subject(holder)},
		Targets: []store.Target{{
			ID: "t-db", Hostname: "db01.example.com", Zone: "edge", Labels: map[string]string{"env": "prod"},
		}},
		Proxies: []store.Proxy{{
			ID: "proxy-g1", Zone: "edge", PublicKey: []byte("key-proxy-g1"),
			State: store.EnrollmentEnrolled, LastHeartbeatAt: now,
		}},
	})
	version, err := st.PolicyBundles().NextVersion(ctx, otherTenant)
	if err != nil {
		t.Fatalf("bundle version: %v", err)
	}
	if err := st.PolicyBundles().Insert(ctx, otherTenant, store.PolicyBundle{
		Version: version, Source: []byte(otherTenantBundle), Hash: "sha256:fixture", UploadedBy: "tenants_test",
	}); err != nil {
		t.Fatalf("insert bundle: %v", err)
	}
	if err := st.PolicyBundles().Activate(ctx, otherTenant, version); err != nil {
		t.Fatalf("activate bundle: %v", err)
	}

	bus, err := revoke.New(revoke.Options{HeartbeatInterval: 100 * time.Millisecond, Logger: log})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = bus.Close(ctx)
	})
	registry := fleet.New(st, fleet.WithSubscriptionState(bus), fleet.WithLogger(log))
	ingest, err := audit.New(audit.Options{Store: st, Logger: log})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	emitter, err := audit.NewEmitter(ingest)
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}
	decisions, err := decision.New(decision.Options{
		Store: st, Fleet: registry, Subjects: identity.NewStoreDirectory(st), Logger: log, Refresh: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("decision: %v", err)
	}

	// Before the restart: a request raised in the tenant that is NOT the
	// configured one, still pending when the process went away.
	wf := &approvingLater{}
	cfg := &config.Config{Tenant: string(configuredTenant)}
	cfg.Grants.WorkflowPollInterval = 25 * time.Millisecond
	extensions := withWorkflow(t, wf)
	notifier, err := buildNotifier(cfg, extensions, log)
	if err != nil {
		t.Fatalf("notifier: %v", err)
	}
	before, err := buildGrants(cfg, st, emitter, bus, notifier, extensions, log)
	if err != nil {
		t.Fatalf("grants before the restart: %v", err)
	}
	out, err := before.Create(ctx, otherTenant, access.Actor{Principal: "p-admin", Subject: "admin@example.com"},
		access.Spec{
			Subject: holder, Scope: access.Scope{Name: "prod-dba", Labels: map[string]string{"env": "prod"}},
			Duration: time.Hour, Reason: "INC-9 needs a DBA on the primary",
		})
	if err != nil || out.Request == nil || out.Request.State != store.GrantRequestPending {
		t.Fatalf("raise the request: %+v, %v", out.Request, err)
	}

	// The restart: a fresh service, as serve builds it, polling on its clock.
	// Only the configured tenant is tracked.
	grants, err := buildGrants(cfg, st, emitter, bus, notifier, extensions, log)
	if err != nil {
		t.Fatalf("grants after the restart: %v", err)
	}
	pollCtx, stopPolling := context.WithCancel(ctx)
	defer stopPolling()
	go grants.Watch(pollCtx, cfg.Grants.WorkflowPollInterval)

	handler, err := south.New(south.Options{
		Identity: identity.NewService(identity.NewStoreDirectory(st)),
		Fleet:    registry,
		Decision: decisions,
		Events:   trackSubscriptions(bus, grants, registry, nil),
		Logs:     ingest,
		Logger:   log,
	})
	if err != nil {
		t.Fatalf("south: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// The approver answers. Nobody reads the request and nobody raises
	// another — so, with nothing else having happened, the poller does not
	// know this tenant and nothing is applied.
	wf.approve()
	time.Sleep(6 * cfg.Grants.WorkflowPollInterval)
	if rows, _ := st.Grants().List(ctx, otherTenant, store.GrantQuery{}); len(rows) != 0 {
		t.Fatalf("a grant exists before any proxy of %s subscribed: the poller listed tenants it was never shown", otherTenant)
	}

	// A proxy of that tenant reconnects: its credential names the tenant,
	// and the listener resolved it.
	token, _, err := registry.IssueProxyToken(ctx, otherTenant, "proxy-g1", "bound", time.Time{})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	subCtx, unsubscribe := context.WithCancel(ctx)
	defer unsubscribe()
	req, err := http.NewRequestWithContext(subCtx, http.MethodGet, srv.URL+"/v1/proxies/proxy-g1/events", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.String())
	resp, err := srv.Client().Do(req) //nolint:bodyclose // closed below
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subscribe answered %d", resp.StatusCode)
	}

	// Within a poll: the grant exists, and the holder's next authorize allows.
	deadline := time.Now().Add(5 * time.Second)
	var rows []store.Grant
	for time.Now().Before(deadline) {
		rows, _ = st.Grants().List(ctx, otherTenant, store.GrantQuery{})
		if len(rows) > 0 {
			break
		}
		time.Sleep(cfg.Grants.WorkflowPollInterval / 5)
	}
	if len(rows) != 1 || rows[0].SubjectID != holder || rows[0].Origin != store.GrantOriginWorkflow {
		t.Fatalf("grants in %s after its proxy subscribed: %+v", otherTenant, rows)
	}
	pv := contract.PolicyVersion
	decided, err := decisions.Authorize(ctx, otherTenant, &contract.AuthorizeRequest{
		Identity:      contract.Identity{Subject: holder, Login: "alice", Source: "local"},
		Target:        "db01.example.com",
		AuthMethod:    contract.AuthMethodCert,
		PolicyVersion: &pv,
		Conn: contract.ConnMeta{
			SessionID: "sess-1", ProxyID: "proxy-g1", ClientAddr: "203.0.113.7:52344",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if decided.Deny != nil {
		t.Errorf("the approved grant did not allow: %s", decided.Deny.Reason)
	}
}

func withWorkflow(t *testing.T, wf ext.GrantWorkflow) *ext.Extensions {
	t.Helper()
	reg := ext.NewRegistry()
	if err := extdefault.Register(reg, extdefault.Deps{}); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if err := reg.RegisterGrantWorkflow(ext.Registration{Provider: "example/workflow", Version: "test"}, wf); err != nil {
		t.Fatalf("register: %v", err)
	}
	sealed, err := reg.Seal()
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return sealed
}

// approvingLater keeps every request pending until approve, then approves any
// it is asked about.
type approvingLater struct {
	mu       sync.Mutex
	approved bool
}

func (w *approvingLater) approve() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.approved = true
}

func (w *approvingLater) Submit(_ context.Context, req ext.GrantRequest) (ext.GrantDecision, error) {
	return ext.GrantDecision{WorkflowRef: "wf-" + req.RequestID, Outcome: ext.GrantPending}, nil
}

func (w *approvingLater) Status(_ context.Context, _ ext.Tenant, ref string) (ext.GrantDecision, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.approved {
		return ext.GrantDecision{WorkflowRef: ref, Outcome: ext.GrantApproved}, nil
	}
	return ext.GrantDecision{WorkflowRef: ref, Outcome: ext.GrantPending}, nil
}

func (w *approvingLater) Cancel(context.Context, ext.Tenant, string, string) error { return nil }
