// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package accessctx_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/accessctx"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The acceptance harness for external access context: a real database, the
// real engine behind /v1/authorize, the real audit chain, the real grant
// service and the real push receiver — and ONE clock, which the test owns.
// Providers are fakes or Control's declarative provider against a fake
// external system; nothing else is.

const (
	tenant  = store.Tenant("acme")
	scanner = "svc-scanner"
	alice   = "alice@example.com"
)

// The policy external windows are an input to. One rule, reachable only
// through a grant: `vuln-scan` is privileged, `change-window` falls open when
// its probe cannot answer, `maintenance` takes the deployment's default, and
// `prod-dba` is an administrator's scope for the equivalence test.
const bundle = `
schema_version: 1
tenant: acme
description: the external access context fixture policy
labels:
  env: [prod, dev]
groups: [dba, secops]
scopes:
  vuln-scan:
    description: the scanner's root window on a production host
    privileged: true
  change-window:
    unanswered: open
rules:
  - id: windowed-prod
    effect: allow
    match:
      target:
        labels: {env: [prod]}
      grant: {required: true, scopes: [vuln-scan, change-window, maintenance, prod-dba]}
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

type harness struct {
	t         *testing.T
	store     *store.Store
	decisions *decision.Service
	grants    *access.Service
	context   *accessctx.Service
	prober    *accessctx.Prober
	providers *accessctx.Providers

	mu    sync.Mutex
	clock time.Time
}

type harnessConfig struct {
	budget      time.Duration
	probeBudget time.Duration
	ceiling     time.Duration
	unanswered  model.Unanswered
	pushRate    float64
	pushBurst   int
	providers   []ext.AccessContextProvider
}

type option func(*harnessConfig)

func withProviders(p ...ext.AccessContextProvider) option {
	return func(c *harnessConfig) { c.providers = append(c.providers, p...) }
}

func withBudgets(decisionBudget, probeBudget time.Duration) option {
	return func(c *harnessConfig) { c.budget, c.probeBudget = decisionBudget, probeBudget }
}

func withUnanswered(u model.Unanswered) option {
	return func(c *harnessConfig) { c.unanswered = u }
}

func withPushRate(rate float64, burst int) option {
	return func(c *harnessConfig) { c.pushRate, c.pushBurst = rate, burst }
}

func newHarness(t *testing.T, opts ...option) *harness {
	t.Helper()
	cfg := harnessConfig{
		budget: 2 * time.Second, probeBudget: 300 * time.Millisecond, ceiling: 4 * time.Hour,
		pushRate: 1000, pushBurst: 1000,
	}
	for _, o := range opts {
		o(&cfg)
	}
	st := storetest.New(t)
	h := &harness{t: t, store: st, clock: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	log := slog.New(slog.DiscardHandler)

	storetest.Seed(t, st, storetest.Fixture{
		Tenant:   tenant,
		Subjects: []store.Subject{storetest.Subject(scanner, "secops"), storetest.Subject(alice, "dba")},
		Targets: []store.Target{
			{ID: "t-db1", Hostname: "db01.example.com", Zone: "edge", Labels: map[string]string{"env": "prod"}},
			{ID: "t-db2", Hostname: "db02.example.com", Zone: "edge", Labels: map[string]string{"env": "prod"}},
			{ID: "t-web", Hostname: "web01.example.com", Zone: "edge", Labels: map[string]string{"env": "dev"}},
		},
		Proxies: []store.Proxy{{
			ID: "proxy-1", Zone: "edge", PublicKey: []byte("key-proxy-1"),
			State: store.EnrollmentEnrolled, LastHeartbeatAt: h.clock,
		}},
	})
	version, err := st.PolicyBundles().NextVersion(t.Context(), tenant)
	if err != nil {
		t.Fatalf("bundle version: %v", err)
	}
	if err := st.PolicyBundles().Insert(t.Context(), tenant, store.PolicyBundle{
		Version: version, Source: []byte(bundle), Hash: "sha256:fixture", UploadedBy: "accessctx_test",
	}); err != nil {
		t.Fatalf("insert bundle: %v", err)
	}
	if err := st.PolicyBundles().Activate(t.Context(), tenant, version); err != nil {
		t.Fatalf("activate bundle: %v", err)
	}

	// The providers are registered the way every provider is: through the
	// extension registry, sealed before anything reads them.
	reg := ext.NewRegistry()
	if err := extdefault.Register(reg, extdefault.Deps{}); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	for _, p := range cfg.providers {
		if err := reg.RegisterAccessContextProvider(ext.Registration{Provider: "test/" + p.Describe().Name}, p); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	x, err := reg.Seal()
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	h.providers, err = accessctx.NewProviders(x.AccessContextProviders(), 4)
	if err != nil {
		t.Fatalf("providers: %v", err)
	}
	h.prober, err = accessctx.NewProber(accessctx.ProberOptions{
		Store: st, Providers: h.providers, Budget: cfg.probeBudget, Ceiling: cfg.ceiling,
		Unanswered: cfg.unanswered, BindingRefresh: time.Nanosecond, Logger: log, Now: h.now,
	})
	if err != nil {
		t.Fatalf("prober: %v", err)
	}

	registry := fleet.New(st, fleet.WithClock(h.now), fleet.WithLogger(log),
		fleet.WithLiveness(fleet.Liveness{HeartbeatTTL: 7 * 24 * time.Hour}))
	h.decisions, err = decision.New(decision.Options{
		Store: st, Fleet: registry, Subjects: identity.NewStoreDirectory(st),
		Logger: log, Now: h.now, Refresh: time.Nanosecond, Budget: cfg.budget, External: h.prober,
	})
	if err != nil {
		t.Fatalf("decision: %v", err)
	}

	bus, err := revoke.New(revoke.Options{Logger: log, Now: h.now})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	ingest, err := audit.New(audit.Options{Store: st, Logger: log})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	emitter, err := audit.NewEmitter(ingest)
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}
	h.grants, err = access.New(access.Options{
		Store: st, Recorder: emitter, Revoker: bus, Settle: time.Millisecond, Logger: log, Now: h.now,
	})
	if err != nil {
		t.Fatalf("access: %v", err)
	}
	h.context, err = accessctx.New(accessctx.Options{
		Store: st, Grants: h.grants, Recorder: emitter, Providers: h.providers, Policy: h.decisions,
		Ceiling: cfg.ceiling, PushRate: cfg.pushRate, PushBurst: cfg.pushBurst, Logger: log, Now: h.now,
	})
	if err != nil {
		t.Fatalf("accessctx: %v", err)
	}
	return h
}

func (h *harness) now() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clock
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.clock = h.clock.Add(d)
	h.mu.Unlock()
}

// The actors: an administrator who writes bindings, and an integration's
// token that pushes.
var (
	admin       = access.Actor{Principal: "p-admin", Subject: "admin@example.com", CorrelationID: "corr-admin"}
	scannerPush = access.Actor{Principal: "p-scanner", DisplayName: "acme-scanner token", CorrelationID: "corr-push"}
	otherPush   = access.Actor{Principal: "p-other", DisplayName: "another integration's token", CorrelationID: "corr-other"}
)

// bind writes a binding through the real API, as 0019's route will.
func (h *harness) bind(b store.AccessContextBinding) store.AccessContextBinding {
	h.t.Helper()
	got, err := h.context.PutBinding(h.t.Context(), tenant, admin, b)
	if err != nil {
		h.t.Fatalf("PutBinding %s: %v", b.Provider, err)
	}
	return got
}

// scannerBinding is the binding a vulnerability scanner gets: it may open
// privileged `vuln-scan` windows for its own service account on production
// database hosts, for at most two hours.
func scannerBinding(provider string, mode store.ExternalMode) store.AccessContextBinding {
	b := store.AccessContextBinding{
		Provider:     provider,
		Mode:         mode,
		Scope:        "vuln-scan",
		Subjects:     []string{scanner},
		Targets:      []string{"*.example.com"},
		TargetLabels: map[string]string{"env": "prod"},
		MaxWindow:    2 * time.Hour,
		Privileged:   true,
		Enabled:      true,
	}
	if mode.Pushes() {
		b.PushPrincipals = []string{scannerPush.Principal}
	}
	return b
}

// authorize asks the real decision path, as proxy-1 would. It returns the
// error rather than failing on it: an outage is an answer these tests assert.
func (h *harness) authorize(subject, target, session string) (decision.Outcome, error) {
	h.t.Helper()
	version := contract.PolicyVersion
	login := subject
	if i := len(login); i > 0 {
		for j := range login {
			if login[j] == '@' {
				login = login[:j]
				break
			}
		}
	}
	return h.decisions.Authorize(h.t.Context(), tenant, &contract.AuthorizeRequest{
		Identity:      contract.Identity{Subject: subject, Login: login, Source: "local"},
		Target:        target,
		AuthMethod:    contract.AuthMethodCert,
		PolicyVersion: &version,
		Conn: contract.ConnMeta{
			SessionID: session, ProxyID: "proxy-1", ClientAddr: "203.0.113.7:52344",
			Timestamp: h.now().Format(time.RFC3339),
		},
	})
}

// recordOf reads the decision record a session's authorize call wrote — by
// session, because an outage hands the caller no decision id.
func (h *harness) recordOf(session string) (store.Decision, recordedInputs, recordedExplanation) {
	h.t.Helper()
	ds, err := h.store.Decisions().ListBySession(h.t.Context(), tenant, session, 1)
	if err != nil || len(ds) != 1 {
		h.t.Fatalf("the record of %s: %v (%d records)", session, err, len(ds))
	}
	var in recordedInputs
	var ex recordedExplanation
	if err := json.Unmarshal(ds[0].Inputs, &in); err != nil {
		h.t.Fatalf("inputs: %v", err)
	}
	if err := json.Unmarshal(ds[0].Explanation, &ex); err != nil {
		h.t.Fatalf("explanation: %v", err)
	}
	return ds[0], in, ex
}

// The parts of a decision record these tests read, in the record's own
// spelling — so a renamed field fails here rather than passing unnoticed.
type recordedInputs struct {
	Grants []struct {
		ID       string `json:"id"`
		Origin   string `json:"origin"`
		External struct {
			System      string `json:"system"`
			Reference   string `json:"reference"`
			Mode        string `json:"mode"`
			AssertionID string `json:"assertion_id"`
			WindowEnd   string `json:"window_end"`
		} `json:"external"`
	} `json:"grants"`
	ExternalContext []accessctx.Entry `json:"external_context"`
}

type recordedExplanation struct {
	Effect     string `json:"effect"`
	Rule       string `json:"rule"`
	Grant      string `json:"grant"`
	Unserved   string `json:"unserved"`
	DenyReason string `json:"deny_reason"`
	External   *struct {
		Provider    string `json:"provider"`
		Reference   string `json:"reference"`
		WindowStart string `json:"window_start"`
		WindowEnd   string `json:"window_end"`
		Arrived     string `json:"arrived"`
		Confirmed   bool   `json:"confirmed"`
		Fell        string `json:"fell"`
	} `json:"external"`
	Unanswered []struct {
		Provider  string `json:"provider"`
		Reference string `json:"reference"`
		Scope     string `json:"scope"`
		Cause     string `json:"cause"`
		Fell      string `json:"fell"`
	} `json:"unanswered"`
}

// controlRecords reads this server's own audit records for one event.
func (h *harness) controlRecords(event string) []store.AuditRecord {
	h.t.Helper()
	recs, err := h.store.Audit().Query(h.t.Context(), tenant, store.AuditQuery{Event: event, Limit: 100})
	if err != nil {
		h.t.Fatalf("audit query: %v", err)
	}
	return recs
}

// grantCount is how many grants the tenant holds, whatever their state.
func (h *harness) grantCount() int {
	h.t.Helper()
	gs, err := h.store.Grants().List(h.t.Context(), tenant, store.GrantQuery{Limit: 1000})
	if err != nil {
		h.t.Fatalf("list grants: %v", err)
	}
	return len(gs)
}

// ---------------------------------------------------------------------------
// fakeProvider: a scripted external system
// ---------------------------------------------------------------------------

// fakeProvider is an integration whose answers a test scripts. Its push body is
// the assertion itself, as JSON — the reading is not what these tests are
// about; the declarative provider's own tests are.
type fakeProvider struct {
	name           string
	probes, pushes bool

	mu     sync.Mutex
	calls  int
	answer func(q ext.AccessContextQuery) (ext.AccessEvidence, error)
	// stall makes Probe sleep WITHOUT watching its context — the
	// deliberately badly behaved provider the budget exists for — until the
	// test releases it.
	stall chan struct{}
}

func (f *fakeProvider) Describe() ext.AccessContextInfo {
	return ext.AccessContextInfo{Name: f.name, Probes: f.probes, Pushes: f.pushes}
}

func (f *fakeProvider) Probe(_ context.Context, q ext.AccessContextQuery) (ext.AccessEvidence, error) {
	f.mu.Lock()
	f.calls++
	answer, stall := f.answer, f.stall
	f.mu.Unlock()
	if stall != nil {
		<-stall
	}
	if answer == nil {
		return ext.AccessEvidence{State: ext.WindowNotConfirmed}, nil
	}
	return answer(q)
}

func (f *fakeProvider) Interpret(_ context.Context, p ext.AccessContextPush) (ext.WindowAssertion, error) {
	var body pushBody
	if err := json.Unmarshal(p.Body, &body); err != nil {
		return ext.WindowAssertion{}, ext.Errorf(ext.PointAccessContextProvider, f.name, "Interpret", ext.KindMalformed, "%v", err)
	}
	return ext.WindowAssertion{
		ID: body.ID, Reference: body.Reference, Subject: body.Subject, Targets: body.Targets, Scope: body.Scope,
		Window:   ext.Window{NotBefore: body.NotBefore, NotAfter: body.NotAfter},
		IssuedAt: body.IssuedAt, AdditionalContext: body.Additional,
	}, nil
}

func (f *fakeProvider) script(answer func(q ext.AccessContextQuery) (ext.AccessEvidence, error)) {
	f.mu.Lock()
	f.answer = answer
	f.mu.Unlock()
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type pushBody struct {
	ID         string          `json:"id"`
	Reference  string          `json:"reference,omitempty"`
	Subject    string          `json:"subject"`
	Targets    []string        `json:"targets"`
	Scope      string          `json:"scope,omitempty"`
	NotBefore  time.Time       `json:"not_before,omitzero"`
	NotAfter   time.Time       `json:"not_after,omitzero"`
	IssuedAt   time.Time       `json:"issued_at,omitzero"`
	Additional json.RawMessage `json:"additional_context,omitempty"`
}

func (b pushBody) bytes() []byte {
	out, err := json.Marshal(b)
	if err != nil {
		panic(err)
	}
	return out
}

// push sends a push through the real receiver.
func (h *harness) push(actor access.Actor, provider string, b pushBody) (accessctx.PushResult, error) {
	h.t.Helper()
	return h.context.Push(h.t.Context(), tenant, actor, provider, "application/json", b.bytes())
}

// scanWindow is SCAN-7: the scanner's two-hour window on db01, starting now.
func (h *harness) scanWindow(id string) pushBody {
	return pushBody{
		ID: id, Reference: "SCAN-7", Subject: scanner, Targets: []string{"db01.example.com"},
		NotBefore: h.now(), NotAfter: h.now().Add(2 * time.Hour), IssuedAt: h.now(),
		Additional: json.RawMessage(`{"scan_profile":"authenticated-linux"}`),
	}
}

// confirm scripts a probe that confirms ref, open from now until `until`.
func confirm(ref string, until time.Time) func(ext.AccessContextQuery) (ext.AccessEvidence, error) {
	return func(q ext.AccessContextQuery) (ext.AccessEvidence, error) {
		return ext.AccessEvidence{
			State: ext.WindowConfirmed, Reference: ref,
			Window:     ext.Window{NotBefore: q.At.Add(-time.Minute), NotAfter: until},
			Assertions: map[string]string{"scan_state": "running"},
			ObservedAt: q.At,
		}, nil
	}
}

func ptr[T any](v T) *T { return &v }

func unreachable(q ext.AccessContextQuery) (ext.AccessEvidence, error) {
	return ext.AccessEvidence{}, ext.Errorf(ext.PointAccessContextProvider, "fake", "Probe", ext.KindUnavailable, "connection refused")
}
