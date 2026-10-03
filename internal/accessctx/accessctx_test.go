// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package accessctx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/accessctx"
	"github.com/hoplock/control/internal/accessctx/declarative"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// ---------------------------------------------------------------------------
// The seam: registered under 0004's rules, visible in the listing
// ---------------------------------------------------------------------------

// Providers register through the extension registry like every seam: before
// start, never after, a second registration by one provider an error, and
// every one of them in the listing — Control's declarative integrations one
// row each, beside anybody else's. The framework then keys them by the
// external system's name, and two answering to one name is a start-up failure.
func TestProvidersAreRegisteredUnderTheRegistrysRulesAndListed(t *testing.T) {
	scan, err := declarative.New(config.DeclarativeProviderConfig{
		Name: "acme-scanner",
		Push: &config.DeclarativePushConfig{ID: "$.id", Subject: "$.s", Targets: "$.t", WindowEnd: "$.e"},
	}, config.EgressConfig{}, declarative.Options{})
	if err != nil {
		t.Fatalf("declarative: %v", err)
	}
	vendor := &fakeProvider{name: "qualys", probes: true}

	reg := ext.NewRegistry()
	if err := extdefault.Register(reg, extdefault.Deps{}); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if err := reg.RegisterAccessContextProvider(ext.Registration{Provider: declarative.RegistrationPrefix + "acme-scanner"}, scan); err != nil {
		t.Fatalf("register the declarative integration: %v", err)
	}
	if err := reg.RegisterAccessContextProvider(ext.Registration{Provider: "example.com/enterprise/qualys", Version: "1.2.0"}, vendor); err != nil {
		t.Fatalf("register a packaged integration: %v", err)
	}
	var dup *ext.DuplicateError
	if err := reg.RegisterAccessContextProvider(ext.Registration{Provider: "example.com/enterprise/qualys"}, vendor); !errors.As(err, &dup) {
		t.Errorf("a second registration by one provider = %v, want a DuplicateError", err)
	}
	x, err := reg.Seal()
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := reg.RegisterAccessContextProvider(ext.Registration{Provider: "late"}, vendor); err == nil {
		t.Error("a provider registered after the server sealed the registry")
	}

	var listed []string
	for _, st := range x.Status() {
		if st.Info.Point != ext.PointAccessContextProvider {
			continue
		}
		for _, r := range st.Registrations {
			listed = append(listed, r.Provider)
		}
	}
	if strings.Join(listed, ",") != "hoplock/control/declarative/acme-scanner,example.com/enterprise/qualys" {
		t.Errorf("the listing names %v", listed)
	}

	providers, err := accessctx.NewProviders(x.AccessContextProviders(), 0)
	if err != nil {
		t.Fatalf("NewProviders: %v", err)
	}
	if got := strings.Join(providers.Names(), ","); got != "acme-scanner,qualys" {
		t.Errorf("providers by name = %s", got)
	}
	if p, ok := providers.Lookup("acme-scanner"); !ok || !p.Info.Pushes || p.Info.Probes {
		t.Errorf("acme-scanner = %+v", p)
	}

	// Two pieces of code answering to one system name: the binding trusts
	// a name, so it must name one provider.
	twin := ext.NewRegistry()
	if err := extdefault.Register(twin, extdefault.Deps{}); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	_ = twin.RegisterAccessContextProvider(ext.Registration{Provider: "a/qualys"}, vendor)
	_ = twin.RegisterAccessContextProvider(ext.Registration{Provider: "b/qualys"}, &fakeProvider{name: "qualys", pushes: true})
	tx, _ := twin.Seal()
	if _, err := accessctx.NewProviders(tx.AccessContextProviders(), 0); err == nil || !strings.Contains(err.Error(), "a/qualys") {
		t.Errorf("two providers called qualys = %v, want an error naming both", err)
	}
	bad := ext.NewRegistry()
	if err := extdefault.Register(bad, extdefault.Deps{}); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	_ = bad.RegisterAccessContextProvider(ext.Registration{Provider: "c"}, &fakeProvider{name: "Not A Name", pushes: true})
	bx, _ := bad.Seal()
	if _, err := accessctx.NewProviders(bx.AccessContextProviders(), 0); err == nil {
		t.Error("a provider whose name is not a short code was accepted")
	}
}

// ---------------------------------------------------------------------------
// The declarative provider, end to end, against a fake external system
// ---------------------------------------------------------------------------

// scanAPI is a scanner nobody has heard of, answering what the test says.
type scanAPI struct {
	mu     sync.Mutex
	status int
	body   string
	delay  time.Duration
}

func (s *scanAPI) set(status int, body string, delay time.Duration) {
	s.mu.Lock()
	s.status, s.body, s.delay = status, body, delay
	s.mu.Unlock()
}

func (s *scanAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	status, body, delay := s.status, s.body, s.delay
	s.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// THE ACCEPTANCE CRITERION: the declarative provider works end to end — probe
// confirms, probe denies, probe times out, probe returns malformed data — and
// the last two are distinguishable in the decision record. Through the real
// decision path, on a privileged scope, so the two failures also show
// "privileged access with an unreachable probe is denied, and the record says
// why".
func TestTheDeclarativeProviderEndToEnd(t *testing.T) {
	api := &scanAPI{}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	prov, err := declarative.New(config.DeclarativeProviderConfig{
		Name: "acme-scanner",
		Probe: &config.DeclarativeProbeConfig{
			URL:     srv.URL + "/scans/active?host={{target.hostname}}",
			Timeout: 150 * time.Millisecond,
			Confirm: []config.DeclarativeAssertion{
				{Path: "$.state", Equals: ptr("running")},
				{Path: "$.hosts", Contains: ptr("{{target.hostname}}")},
			},
			Reference: "$.scan_id", WindowStart: "$.started_at", WindowEnd: "$.ends_at",
			Assertions: map[string]string{"profile": "$.profile"},
		},
	}, config.EgressConfig{AllowCIDRs: []string{"127.0.0.0/8"}}, declarative.Options{})
	if err != nil {
		t.Fatalf("declarative: %v", err)
	}
	h := newHarness(t, withProviders(prov))
	h.bind(scannerBinding("acme-scanner", store.ExternalProbe))

	answer := `{"state":"running","scan_id":"SCAN-7","hosts":["db01.example.com"],` +
		`"started_at":"2026-09-30T11:55:00Z","ends_at":"2026-09-30T14:00:00Z","profile":"authenticated-linux"}`

	t.Run("confirms", func(t *testing.T) {
		api.set(200, answer, 0)
		out, err := h.authorize(scanner, "db01.example.com", "s-confirm")
		if err != nil || out.Deny != nil {
			t.Fatalf("a running scan = %+v, %v; want an allow", out.Deny, err)
		}
		// The proxy is told why, as opaque grant context (M16).
		gc := out.Response.GrantContext
		if gc == nil || gc.System != "acme-scanner" || gc.Reference != "SCAN-7" || gc.WindowEnd != "2026-09-30T14:00:00Z" {
			t.Errorf("grant context = %+v", gc)
		}
		_, in, ex := h.recordOf("s-confirm")
		e := in.ExternalContext[0]
		if e.Outcome != accessctx.OutcomeConfirmed || e.Reference != "SCAN-7" || !e.Counted || e.Assertions["profile"] != "authenticated-linux" {
			t.Errorf("the record's probe entry = %+v", e)
		}
		// EXPLAIN names the provider, the reference and the window.
		if x := ex.External; x == nil || x.Provider != "acme-scanner" || x.Reference != "SCAN-7" ||
			x.WindowEnd == "" || x.Arrived != "probed" || !x.Confirmed || ex.Grant != e.Grant {
			t.Errorf("explanation.external = %+v (grant %s)", ex.External, ex.Grant)
		}
	})

	t.Run("denies", func(t *testing.T) {
		api.set(200, strings.Replace(answer, `"running"`, `"finished"`, 1), 0)
		out, err := h.authorize(scanner, "db01.example.com", "s-deny")
		if err != nil || out.Deny == nil {
			t.Fatalf("a finished scan = %+v, %v; want a deny", out, err)
		}
		_, in, ex := h.recordOf("s-deny")
		if e := in.ExternalContext[0]; e.Outcome != accessctx.OutcomeNotConfirmed || e.Counted || e.Fell != "" {
			t.Errorf("the record's probe entry = %+v", e)
		}
		if len(ex.Unanswered) != 0 {
			t.Errorf("a probe that answered no is listed as unanswered: %+v", ex.Unanswered)
		}
	})

	t.Run("times out", func(t *testing.T) {
		api.set(200, answer, 2*time.Second)
		start := time.Now()
		out, err := h.authorize(scanner, "db01.example.com", "s-timeout")
		if err != nil || out.Deny == nil {
			t.Fatalf("a slow scanner on a privileged scope = %+v, %v; want a deny, falling closed", out, err)
		}
		if took := time.Since(start); took > time.Second {
			t.Errorf("the authorize call took %s", took)
		}
		_, in, ex := h.recordOf("s-timeout")
		e := in.ExternalContext[0]
		if e.Outcome != accessctx.OutcomeUndetermined || e.Fell != "closed" || !e.Privileged || e.Counted {
			t.Errorf("the record's probe entry = %+v", e)
		}
		if e.Cause != accessctx.CauseTimeout {
			t.Errorf("cause = %q, want %q", e.Cause, accessctx.CauseTimeout)
		}
		if len(ex.Unanswered) != 1 || ex.Unanswered[0].Cause != accessctx.CauseTimeout || ex.Unanswered[0].Fell != "closed" {
			t.Errorf("explanation.unanswered = %+v", ex.Unanswered)
		}
		api.set(200, answer, 0)
	})

	t.Run("malformed", func(t *testing.T) {
		api.set(200, `<html>maintenance page</html>`, 0)
		out, err := h.authorize(scanner, "db01.example.com", "s-malformed")
		if err != nil || out.Deny == nil {
			t.Fatalf("a nonsense answer on a privileged scope = %+v, %v; want a deny", out, err)
		}
		_, in, _ := h.recordOf("s-malformed")
		e := in.ExternalContext[0]
		if e.Outcome != accessctx.OutcomeUndetermined || e.Cause != accessctx.CauseMalformed || e.Fell != "closed" {
			t.Errorf("the record's probe entry = %+v", e)
		}
	})

	// The last two, side by side: a network and a bug read differently.
	_, timedOut, _ := h.recordOf("s-timeout")
	_, garbled, _ := h.recordOf("s-malformed")
	if timedOut.ExternalContext[0].Cause == garbled.ExternalContext[0].Cause {
		t.Errorf("a timeout and a malformed answer recorded the same cause, %q", garbled.ExternalContext[0].Cause)
	}
}

// ---------------------------------------------------------------------------
// The push: a grant read through the normal path
// ---------------------------------------------------------------------------

// A push creates a GRANT — origin external, the system, the reference, the
// asserted window — and the engine reads it through the normal grant path:
// stored, listed live, translated by access.PolicyGrant, matched by the same
// rule. (The equivalence with an administrator's grant is 0012's test,
// extended to push through this receiver: internal/access.)
func TestAPushIsAGrantTheEngineReadsLikeAnyOther(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))

	res, err := h.push(scannerPush, "acme-scanner", h.scanWindow("evt-1"))
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	g := res.Grant
	if res.Replayed || res.Clamped || g.Origin != store.GrantOriginExternal || g.External.System != "acme-scanner" ||
		g.ExternalRef != "SCAN-7" || g.External.AssertionID != "evt-1" || g.External.Mode != store.ExternalPush ||
		g.Scope != "vuln-scan" || g.ScopeLabels["env"] != "prod" || g.CreatedBy.Principal != scannerPush.Principal {
		t.Fatalf("the grant = %+v", g)
	}
	live, err := h.store.Grants().ListLive(t.Context(), tenant, scanner, h.now().Add(time.Second))
	if err != nil || len(live) != 1 || live[0].ID != g.ID {
		t.Fatalf("the decision path's own query reads %v, %v", live, err)
	}

	h.advance(time.Minute)
	out, err := h.authorize(scanner, "db01.example.com", "s-pushed")
	if err != nil || out.Deny != nil {
		t.Fatalf("the pushed window = %+v, %v; want an allow", out.Deny, err)
	}
	_, in, ex := h.recordOf("s-pushed")
	if ex.Grant != g.ID || len(in.Grants) != 1 || in.Grants[0].External.AssertionID != "evt-1" || in.Grants[0].External.Mode != "push" {
		t.Errorf("the record names grant %s, inputs %+v", ex.Grant, in.Grants)
	}
	if x := ex.External; x == nil || x.Arrived != "pushed" || x.Provider != "acme-scanner" || x.Reference != "SCAN-7" {
		t.Errorf("explanation.external = %+v", ex.External)
	}
	// A host the window does not name stays shut.
	if out, err := h.authorize(scanner, "db02.example.com", "s-other-host"); err != nil || out.Deny == nil {
		t.Errorf("a host outside the window = %+v, %v; want a deny", out, err)
	}

	// The accepted push is audited with the credential that made it.
	recs := h.controlRecords(access.EventGrantCreated)
	if len(recs) != 1 || recs[0].Attributes[audit.AttrPrincipalID] != scannerPush.Principal ||
		recs[0].Attributes[audit.AttrGrantExternalAssertion] != "evt-1" {
		t.Errorf("grant.created records = %+v", recs)
	}
}

// ---------------------------------------------------------------------------
// Scope enforcement, the ceiling, replay
// ---------------------------------------------------------------------------

// A push naming a target outside its binding is refused, audited as an
// attempted privilege escalation, and creates nothing. So is one from a
// credential the binding does not name, one asking for another scope, and one
// for a subject the binding does not grant to.
func TestAPushOutsideItsScopeIsAnEscalationAttemptAndCreatesNothing(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))

	cases := map[string]struct {
		actor  access.Actor
		mutate func(*pushBody)
		code   string
		field  string
	}{
		"a host it has never scanned": {scannerPush, func(b *pushBody) {
			b.Targets = []string{"db01.example.com", "web01.example.com"}
		}, accessctx.CodeOutsideScope, "targets[1]"},
		"a host outside its domain": {scannerPush, func(b *pushBody) {
			b.Targets = []string{"db01.corp.internal"}
		}, accessctx.CodeOutsideScope, "targets[0]"},
		"a wildcard wider than its own": {scannerPush, func(b *pushBody) {
			b.Targets = []string{"*.com"}
		}, accessctx.CodeOutsideScope, "targets[0]"},
		"another subject": {scannerPush, func(b *pushBody) {
			b.Subject = alice
		}, accessctx.CodeOutsideScope, "subject"},
		"another scope": {scannerPush, func(b *pushBody) {
			b.Scope = "prod-dba"
		}, accessctx.CodeOutsideScope, "scope"},
		"another integration's token": {otherPush, func(*pushBody) {}, accessctx.CodePushNotPermitted, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b := h.scanWindow("evt-" + strings.ReplaceAll(name, " ", "-"))
			tc.mutate(&b)
			_, err := h.push(tc.actor, "acme-scanner", b)
			ref, ok := accessctx.AsRefusal(err)
			if !ok || ref.Code != tc.code || ref.Field != tc.field || !ref.Escalation {
				t.Fatalf("push = %v; want %s on %q, as an escalation", err, tc.code, tc.field)
			}
		})
	}
	if n := h.grantCount(); n != 0 {
		t.Errorf("%d grants exist after refused pushes, want none", n)
	}
	recs := h.controlRecords(accessctx.EventEscalation)
	if len(recs) != len(cases) {
		t.Fatalf("%d escalation records for %d attempts", len(recs), len(cases))
	}
	for _, r := range recs {
		if r.Severity != string(contract.SeverityCritical) || r.Attributes[audit.AttrContextProvider] != "acme-scanner" {
			t.Errorf("escalation record = %+v", r)
		}
	}
	// The record names what was asked for — the host the scanner never
	// scanned is the interesting fact.
	found := false
	for _, r := range recs {
		if strings.Contains(r.Attributes[audit.AttrContextTargets], "web01.example.com") && r.Attributes[audit.AttrContextField] == "targets[1]" {
			found = true
		}
	}
	if !found {
		t.Error("no escalation record names the host outside the scope")
	}
}

// A window longer than the ceiling is CLAMPED, not refused, and the clamp is
// recorded: on the answer, and on the audit record of the grant. The ceiling is
// the binding's maximum or the server's, whichever is shorter, from when the
// window opens.
func TestAWindowLongerThanTheCeilingIsClampedAndTheClampRecorded(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush)) // two hours; the server's ceiling is four

	b := h.scanWindow("evt-long")
	b.NotAfter = h.now().Add(72 * time.Hour)
	res, err := h.push(scannerPush, "acme-scanner", b)
	if err != nil {
		t.Fatalf("a long window was refused: %v", err)
	}
	if !res.Clamped || res.Ceiling != 2*time.Hour || !res.Grant.ExpiresAt.Equal(h.now().Add(2*time.Hour)) {
		t.Errorf("result = clamped %v, ceiling %s, expires %v", res.Clamped, res.Ceiling, res.Grant.ExpiresAt)
	}
	// The window asserted is kept as asserted; the window granted is the
	// one that bites.
	if !res.Grant.External.WindowEnd.Equal(b.NotAfter) {
		t.Errorf("asserted end = %v, want %v recorded verbatim", res.Grant.External.WindowEnd, b.NotAfter)
	}
	recs := h.controlRecords(access.EventGrantCreated)
	if len(recs) != 1 || recs[0].Attributes[audit.AttrGrantExternalClamped] != "true" ||
		recs[0].Attributes[audit.AttrGrantExternalCeiling] != "7200" {
		t.Errorf("the grant's audit record = %+v", recs)
	}

	// A window that asks for less gets what it asked for.
	short := h.scanWindow("evt-short")
	short.NotAfter = h.now().Add(30 * time.Minute)
	res, err = h.push(scannerPush, "acme-scanner", short)
	if err != nil || res.Clamped || !res.Grant.ExpiresAt.Equal(short.NotAfter) {
		t.Errorf("a short window = %+v, %v", res, err)
	}
}

// The same assertion id twice is one grant. A different window under a reused
// id is a conflict, and a revoked window stays revoked when pushed again.
func TestTheSameAssertionTwiceIsOneGrant(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))

	first, err := h.push(scannerPush, "acme-scanner", h.scanWindow("evt-1"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	h.advance(10 * time.Second)
	again, err := h.push(scannerPush, "acme-scanner", h.scanWindowAt(first.Grant.External.WindowStart, "evt-1"))
	if err != nil || !again.Replayed || again.Grant.ID != first.Grant.ID {
		t.Fatalf("the replay = %+v, %v; want the first grant, replayed", again, err)
	}
	if n := h.grantCount(); n != 1 {
		t.Fatalf("%d grants for one assertion", n)
	}

	changed := h.scanWindowAt(first.Grant.External.WindowStart, "evt-1")
	changed.Targets = []string{"db02.example.com"}
	if _, err := h.push(scannerPush, "acme-scanner", changed); !isRefusal(err, accessctx.CodeAssertionConflict) {
		t.Errorf("a different window under a used id = %v, want a conflict", err)
	}

	if _, err := h.grants.Revoke(t.Context(), tenant, admin, first.Grant.ID, "scan cancelled by the SOC"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	after, err := h.push(scannerPush, "acme-scanner", h.scanWindowAt(first.Grant.External.WindowStart, "evt-1"))
	if err != nil || !after.Replayed || after.Grant.RevokedAt.IsZero() {
		t.Errorf("pushed again after revocation = %+v, %v; want the revoked grant", after.Grant, err)
	}
	if n := h.grantCount(); n != 1 {
		t.Errorf("%d grants after a replay of a revoked window", n)
	}
}

func (h *harness) scanWindowAt(start time.Time, id string) pushBody {
	b := h.scanWindow(id)
	b.NotBefore, b.NotAfter = start, start.Add(2*time.Hour)
	return b
}

func isRefusal(err error, code string) bool {
	r, ok := accessctx.AsRefusal(err)
	return ok && r.Code == code
}

// An assertion too old to be fresh, or issued in the future, is refused —
// the replay an id alone cannot catch — and so is a window already over.
// A start a little ahead of this server's clock is read through the skew.
func TestStaleFutureAndClosedAssertionsAreRefused(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true}
	h := newHarness(t, withProviders(fake))
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))

	stale := h.scanWindow("evt-stale")
	stale.IssuedAt = h.now().Add(-time.Hour)
	if _, err := h.push(scannerPush, "acme-scanner", stale); !isRefusal(err, accessctx.CodeAssertionStale) {
		t.Errorf("an hour-old assertion = %v", err)
	}
	future := h.scanWindow("evt-future")
	future.IssuedAt = h.now().Add(time.Hour)
	if _, err := h.push(scannerPush, "acme-scanner", future); !isRefusal(err, accessctx.CodeAssertionFromFuture) {
		t.Errorf("an assertion from the future = %v", err)
	}
	over := h.scanWindow("evt-over")
	over.NotBefore, over.NotAfter = h.now().Add(-2*time.Hour), h.now().Add(-time.Minute)
	if _, err := h.push(scannerPush, "acme-scanner", over); !isRefusal(err, accessctx.CodeWindowClosed) {
		t.Errorf("a window already over = %v", err)
	}
	if n := len(h.controlRecords(accessctx.EventPushRefused)); n != 3 {
		t.Errorf("%d refusal records, want 3", n)
	}

	skewed := h.scanWindow("evt-skewed")
	skewed.NotBefore = h.now().Add(30 * time.Second)
	res, err := h.push(scannerPush, "acme-scanner", skewed)
	if err != nil || !res.Grant.NotBefore.Equal(h.now()) {
		t.Errorf("a start 30s ahead = %+v, %v; want it open now", res.Grant, err)
	}
	later := h.scanWindow("evt-later")
	later.NotBefore, later.NotAfter = h.now().Add(time.Hour), h.now().Add(2*time.Hour)
	res, err = h.push(scannerPush, "acme-scanner", later)
	if err != nil || !res.Grant.NotBefore.Equal(later.NotBefore) || res.Grant.State(h.now()) != store.GrantScheduled {
		t.Errorf("a window an hour ahead = %+v, %v; want a scheduled grant", res.Grant, err)
	}
}

// Who may push is decided before anything is read: a provider that does not
// exist, one with no binding, a binding that is off or only probes, a body
// that cannot be read, and a rate past the binding's.
func TestPushesNobodyMayMakeAreRefusedFirst(t *testing.T) {
	fake := &fakeProvider{name: "acme-scanner", pushes: true, probes: true}
	h := newHarness(t, withProviders(fake), withPushRate(0.001, 1))

	if _, err := h.push(scannerPush, "nobody", h.scanWindow("e")); !isRefusal(err, accessctx.CodeProviderNotFound) {
		t.Errorf("an unknown provider = %v", err)
	}
	if _, err := h.push(scannerPush, "acme-scanner", h.scanWindow("e")); !isRefusal(err, accessctx.CodeBindingNotFound) {
		t.Errorf("no binding = %v", err)
	}
	b := scannerBinding("acme-scanner", store.ExternalPush)
	b.Enabled = false
	h.bind(b)
	if _, err := h.push(scannerPush, "acme-scanner", h.scanWindow("e")); !isRefusal(err, accessctx.CodeBindingDisabled) {
		t.Errorf("a disabled binding = %v", err)
	}
	h.bind(scannerBinding("acme-scanner", store.ExternalProbe))
	if _, err := h.push(scannerPush, "acme-scanner", h.scanWindow("e")); !isRefusal(err, accessctx.CodePushNotPermitted) {
		// A probe binding names no push credential, so the token is not
		// one it names: refused before the mode is even read.
		t.Errorf("a probe-only binding = %v", err)
	}
	h.bind(scannerBinding("acme-scanner", store.ExternalPush))
	if _, err := h.context.Push(t.Context(), tenant, scannerPush, "acme-scanner", "application/json", []byte("not json")); !isRefusal(err, accessctx.CodeAssertionMalformed) {
		t.Errorf("an unreadable body = %v", err)
	}
	if _, err := h.push(scannerPush, "acme-scanner", h.scanWindow("e2")); !isRefusal(err, accessctx.CodeRateLimited) {
		t.Errorf("a push past the rate = %v", err)
	} else if r, _ := accessctx.AsRefusal(err); r.RetryAfter <= 0 {
		t.Errorf("a rate refusal says when to retry: %+v", r)
	}
}

// ---------------------------------------------------------------------------
// The probe path: the budget, the three answers, the three shapes
// ---------------------------------------------------------------------------

// THE BUDGET. A probe cannot exceed its share of the authorize budget, proven
// with a deliberately slow fake that does not even watch its context: the
// authorize call still answers inside M5's timeout, and — on a scope whose
// unanswered setting is the default, outage — it is an OUTAGE, never a deny
// (M11), with the record saying which probe and why.
func TestASlowProbeCannotSpendTheDecisionsBudget(t *testing.T) {
	slow := &fakeProvider{name: "ticketing", probes: true, stall: make(chan struct{})}
	t.Cleanup(func() { close(slow.stall) })
	h := newHarness(t, withProviders(slow), withBudgets(400*time.Millisecond, 100*time.Millisecond))
	h.bind(store.AccessContextBinding{
		Provider: "ticketing", Mode: store.ExternalProbe, Scope: "maintenance",
		Subjects: []string{alice}, Targets: []string{"db01.example.com"}, MaxWindow: time.Hour, Enabled: true,
	})

	start := time.Now()
	out, err := h.authorize(alice, "db01.example.com", "s-slow")
	took := time.Since(start)
	if took > 400*time.Millisecond {
		t.Errorf("the authorize call took %s; the decision budget is 400ms and the probe's share 100ms", took)
	}
	if out.Deny != nil {
		t.Fatalf("an unanswered probe became a deny (%+v): M11 says it is an outage", out.Deny)
	}
	var undetermined *accessctx.UndeterminedError
	if !errors.As(err, &undetermined) || undetermined.Provider != "ticketing" || undetermined.Cause != accessctx.CauseTimeout {
		t.Fatalf("authorize = %v; want an outage naming the ticketing probe's timeout", err)
	}
	if errors.Is(err, contract.ErrDenied) {
		t.Fatal("the outage is classified as a denial")
	}
	d, in, ex := h.recordOf("s-slow")
	if d.Effect != "unserved" || !strings.Contains(ex.Unserved, "ticketing") {
		t.Errorf("record effect %q, unserved %q", d.Effect, ex.Unserved)
	}
	if e := in.ExternalContext[0]; e.Cause != accessctx.CauseTimeout || e.Fell != "outage" || e.Counted {
		t.Errorf("the record's probe entry = %+v", e)
	}

	// An outage is reserved for a decision that DEPENDS on the window. The
	// same unanswered probe beside an administrator's grant that allows on
	// its own is not an outage: the answer stands without it.
	if _, err := h.grants.Create(t.Context(), tenant, admin, access.Spec{
		Subject: alice, Scope: access.Scope{Name: "prod-dba", Targets: []string{"db01.example.com"}},
		Duration: time.Hour, Reason: "INC-9",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if out, err := h.authorize(alice, "db01.example.com", "s-slow-granted"); err != nil || out.Deny != nil {
		t.Errorf("with a grant that allows on its own = %+v, %v; want an allow", out.Deny, err)
	}
}

// Privileged access with an unreachable probe is denied by default — even where
// the deployment's own default would fall open — and the record says why.
func TestPrivilegedAccessWithAnUnreachableProbeIsDenied(t *testing.T) {
	down := &fakeProvider{name: "acme-scanner", probes: true}
	down.script(unreachable)
	h := newHarness(t, withProviders(down), withUnanswered(model.UnansweredOpen))
	h.bind(scannerBinding("acme-scanner", store.ExternalProbe))

	out, err := h.authorize(scanner, "db01.example.com", "s-down")
	if err != nil || out.Deny == nil {
		t.Fatalf("privileged access with the scanner down = %+v, %v; want a deny", out, err)
	}
	_, in, ex := h.recordOf("s-down")
	e := in.ExternalContext[0]
	if e.Outcome != accessctx.OutcomeUndetermined || e.Cause != accessctx.CauseUnavailable || !e.Privileged || e.Fell != "closed" {
		t.Errorf("the record's probe entry = %+v", e)
	}
	if len(ex.Unanswered) != 1 || ex.Unanswered[0].Provider != "acme-scanner" || ex.Unanswered[0].Fell != "closed" {
		t.Errorf("explanation.unanswered = %+v", ex.Unanswered)
	}
}

// M16's default composition: a push opens a PENDING window and a probe
// confirms it. Unconfirmed it is not an input; confirmed it is, narrowed to
// what the probe said; refuted it is not; and unanswered it falls the way its
// scope says — here, `change-window`, open.
func TestAPushedWindowCountsOnlyWhileItsProbeConfirmsIt(t *testing.T) {
	itsm := &fakeProvider{name: "itsm", pushes: true, probes: true}
	h := newHarness(t, withProviders(itsm))
	h.bind(store.AccessContextBinding{
		Provider: "itsm", Mode: store.ExternalPushProbe, Scope: "change-window",
		SubjectGroups: []string{"dba"}, Targets: []string{"db01.example.com", "db02.example.com"},
		MaxWindow: 4 * time.Hour, PushPrincipals: []string{scannerPush.Principal}, Enabled: true,
	})
	res, err := h.push(scannerPush, "itsm", pushBody{
		ID: "CHG-42/1", Reference: "CHG-42", Subject: alice, Targets: []string{"db01.example.com"},
		NotAfter: h.now().Add(3 * time.Hour),
	})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if res.Grant.External.Mode != store.ExternalPushProbe {
		t.Fatalf("the pushed window's mode = %q", res.Grant.External.Mode)
	}

	// Refuted: the ticket is no longer in its window.
	itsm.script(func(ext.AccessContextQuery) (ext.AccessEvidence, error) {
		return ext.AccessEvidence{State: ext.WindowNotConfirmed}, nil
	})
	if out, err := h.authorize(alice, "db01.example.com", "s-refuted"); err != nil || out.Deny == nil {
		t.Errorf("a refuted window = %+v, %v; want a deny", out, err)
	}

	// Confirmed, and the probe — the authoritative direction — says the
	// window ends sooner than the push did.
	ends := h.now().Add(90 * time.Minute)
	var asked ext.AccessContextQuery
	itsm.script(func(q ext.AccessContextQuery) (ext.AccessEvidence, error) {
		asked = q
		return confirm("CHG-42", ends)(q)
	})
	out, err := h.authorize(alice, "db01.example.com", "s-confirmed")
	if err != nil || out.Deny != nil {
		t.Fatalf("a confirmed window = %+v, %v; want an allow", out.Deny, err)
	}
	if asked.ExternalRef != "CHG-42" || asked.Privileges[0] != "change-window" {
		t.Errorf("the probe was asked %+v; want it asked about the pushed window", asked)
	}
	if want := ends.UTC().Format(time.RFC3339); out.Response.SessionDeadline != want {
		t.Errorf("session deadline = %s, want the probe's end %s", out.Response.SessionDeadline, want)
	}
	_, _, ex := h.recordOf("s-confirmed")
	if x := ex.External; x == nil || x.Arrived != "pushed-and-probed" || !x.Confirmed || x.Reference != "CHG-42" {
		t.Errorf("explanation.external = %+v", ex.External)
	}

	// Unanswered, on a scope that falls open: it counts, and says so.
	itsm.script(unreachable)
	out, err = h.authorize(alice, "db01.example.com", "s-open")
	if err != nil || out.Deny != nil {
		t.Fatalf("an unanswered change window = %+v, %v; want an allow, falling open", out.Deny, err)
	}
	_, in, ex := h.recordOf("s-open")
	if e := in.ExternalContext[0]; e.Fell != "open" || !e.Counted || e.Cause != accessctx.CauseUnavailable {
		t.Errorf("the record's probe entry = %+v", e)
	}
	if x := ex.External; x == nil || x.Confirmed || x.Fell != "open" {
		t.Errorf("explanation.external = %+v; want it to say the window counted unconfirmed", ex.External)
	}
}

// The same scan is asked about once per connection and a scanner opens many:
// an answer is reused for the provider's TTL, and the record says it was.
func TestAProbeAnswerIsReusedForItsTTL(t *testing.T) {
	scan := &fakeProvider{name: "acme-scanner", probes: true}
	scan.script(func(q ext.AccessContextQuery) (ext.AccessEvidence, error) {
		ev, err := confirm("SCAN-7", q.At.Add(time.Hour))(q)
		ev.TTL = time.Minute
		return ev, err
	})
	h := newHarness(t, withProviders(scan))
	h.bind(scannerBinding("acme-scanner", store.ExternalProbe))

	for i, s := range []string{"s-1", "s-2", "s-3"} {
		if out, err := h.authorize(scanner, "db01.example.com", s); err != nil || out.Deny != nil {
			t.Fatalf("connection %d = %+v, %v", i, out.Deny, err)
		}
	}
	if n := scan.callCount(); n != 1 {
		t.Errorf("three connections inside the TTL asked the scanner %d times, want once", n)
	}
	_, in, _ := h.recordOf("s-3")
	if !in.ExternalContext[0].Cached || in.ExternalContext[0].ObservedAt == "" {
		t.Errorf("a reused answer = %+v; want it marked cached, with when it was fetched", in.ExternalContext[0])
	}
	// Every decision the window supplies names the same grant.
	_, first, _ := h.recordOf("s-1")
	if first.ExternalContext[0].Grant != in.ExternalContext[0].Grant || in.ExternalContext[0].Grant == "" {
		t.Errorf("grants %q and %q for one window", first.ExternalContext[0].Grant, in.ExternalContext[0].Grant)
	}

	h.advance(2 * time.Minute)
	if _, err := h.authorize(scanner, "db01.example.com", "s-4"); err != nil {
		t.Fatalf("after the TTL: %v", err)
	}
	if n := scan.callCount(); n != 2 {
		t.Errorf("after the TTL the scanner was asked %d times in all, want twice", n)
	}

	// Nobody is asked about an access the binding does not cover.
	if out, err := h.authorize(alice, "db01.example.com", "s-alice"); err != nil || out.Deny == nil {
		t.Errorf("a subject outside the binding = %+v, %v; want a deny", out, err)
	}
	if n := scan.callCount(); n != 2 {
		t.Errorf("a probe was made for a subject the binding does not grant to")
	}
}

// A provider that panics is a failed probe, never a crashed server.
func TestAProviderThatPanicsIsAFailedProbe(t *testing.T) {
	bad := &fakeProvider{name: "acme-scanner", probes: true}
	bad.script(func(ext.AccessContextQuery) (ext.AccessEvidence, error) { panic("vendor bug") })
	h := newHarness(t, withProviders(bad))
	h.bind(scannerBinding("acme-scanner", store.ExternalProbe))
	out, err := h.authorize(scanner, "db01.example.com", "s-panic")
	if err != nil || out.Deny == nil {
		t.Fatalf("a panicking provider on a privileged scope = %+v, %v; want a deny", out, err)
	}
	_, in, _ := h.recordOf("s-panic")
	if e := in.ExternalContext[0]; e.Cause != accessctx.CauseFailed || !strings.Contains(e.Detail, "panicked") {
		t.Errorf("the record's probe entry = %+v", e)
	}
}

// ---------------------------------------------------------------------------
// Bindings
// ---------------------------------------------------------------------------

// A binding is validated against what the server runs and audited in the
// transaction that writes it; one that could grant to anybody, name anything,
// outlast the ceiling or accept pushes nobody may make cannot be written.
func TestABindingIsValidatedAndAudited(t *testing.T) {
	probeOnly := &fakeProvider{name: "acme-scanner", probes: true}
	h := newHarness(t, withProviders(probeOnly))

	for name, tc := range map[string]struct {
		mutate func(*store.AccessContextBinding)
		field  string
	}{
		"an unknown provider":  {func(b *store.AccessContextBinding) { b.Provider = "nobody" }, "provider"},
		"a mode it lacks":      {func(b *store.AccessContextBinding) { b.Mode = store.ExternalPush }, "mode"},
		"nobody to grant to":   {func(b *store.AccessContextBinding) { b.Subjects = nil }, "subjects"},
		"every target":         {func(b *store.AccessContextBinding) { b.Targets, b.TargetLabels = nil, nil }, "targets"},
		"past the ceiling":     {func(b *store.AccessContextBinding) { b.MaxWindow = 24 * time.Hour }, "max_window"},
		"push credentials":     {func(b *store.AccessContextBinding) { b.PushPrincipals = []string{"p-x"} }, "push_principals"},
		"an unusable scope":    {func(b *store.AccessContextBinding) { b.Scope = "vuln\tscan" }, "scope"},
		"a bad target pattern": {func(b *store.AccessContextBinding) { b.Targets = []string{"db*.example.com"} }, "targets"},
	} {
		b := scannerBinding("acme-scanner", store.ExternalProbe)
		tc.mutate(&b)
		var verr *access.ValidationError
		if _, err := h.context.PutBinding(t.Context(), tenant, admin, b); !errors.As(err, &verr) || verr.Field != tc.field {
			t.Errorf("%s: %v; want a validation error on %s", name, err, tc.field)
		}
	}

	h.bind(scannerBinding("acme-scanner", store.ExternalProbe))
	recs := h.controlRecords(accessctx.EventBindingPut)
	if len(recs) != 1 || recs[0].Attributes[audit.AttrPrincipalID] != admin.Principal ||
		recs[0].Attributes[audit.AttrBindingPrivileged] != "true" || recs[0].Attributes[audit.AttrBindingScope] != "vuln-scan" ||
		recs[0].Attributes[audit.AttrBindingTargetLabels] != "env=prod" || recs[0].Attributes[audit.AttrGrantLabels] != "" {
		t.Errorf("binding records = %+v", recs)
	}
	deleted, err := h.context.DeleteBinding(t.Context(), tenant, admin, "acme-scanner")
	if err != nil || !deleted || len(h.controlRecords(accessctx.EventBindingDeleted)) != 1 {
		t.Errorf("delete = %v, %v", deleted, err)
	}
}

// A binding that may not open privileged access cannot be used to, by push or
// by probe: the policy marks the scope, the binding must agree.
func TestABindingThatMayNotOpenPrivilegedAccessCannot(t *testing.T) {
	both := &fakeProvider{name: "acme-scanner", probes: true, pushes: true}
	both.script(confirm("SCAN-7", time.Date(2026, 9, 30, 14, 0, 0, 0, time.UTC)))
	h := newHarness(t, withProviders(both))
	b := scannerBinding("acme-scanner", store.ExternalPush)
	b.Privileged = false
	h.bind(b)
	if _, err := h.push(scannerPush, "acme-scanner", h.scanWindow("evt-1")); !isRefusal(err, accessctx.CodeOutsideScope) {
		t.Errorf("a privileged scope through an unprivileged binding = %v; want refused as outside its scope", err)
	}

	p := scannerBinding("acme-scanner", store.ExternalProbe)
	p.Privileged = false
	h.bind(p)
	out, err := h.authorize(scanner, "db01.example.com", "s-unprivileged")
	if err != nil || out.Deny == nil {
		t.Fatalf("a probe through an unprivileged binding = %+v, %v; want a deny", out, err)
	}
	_, in, _ := h.recordOf("s-unprivileged")
	if e := in.ExternalContext[0]; e.Cause != accessctx.CauseBindingNotPrivileged || e.Counted {
		t.Errorf("the record's entry = %+v", e)
	}
	if n := both.callCount(); n != 0 {
		t.Errorf("the provider was asked %d times about a window that could not count", n)
	}
}
