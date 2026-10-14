// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package authoring_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/authoring"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

const tenant = store.Tenant("acme")

type harness struct {
	st     *store.Store
	svc    *authoring.Service
	reader *audit.Reader
	now    time.Time
}

func newHarness(t *testing.T, validators ...ext.Bound[ext.PolicyValidator]) *harness {
	t.Helper()
	st := storetest.New(t)
	h := &harness{st: st, reader: audit.NewReader(st), now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	clock := func() time.Time { return h.now }
	registry := fleet.New(st, fleet.WithClock(clock), fleet.WithLogger(slog.New(slog.DiscardHandler)))
	in, err := audit.New(audit.Options{Store: st})
	if err != nil {
		t.Fatalf("ingester: %v", err)
	}
	emitter, err := audit.NewEmitter(in)
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}
	h.svc, err = authoring.New(authoring.Options{
		Store: st, Fleet: registry, Emitter: emitter, Reader: h.reader, Validators: validators, Now: clock,
	})
	if err != nil {
		t.Fatalf("authoring: %v", err)
	}

	caps, err := fleet.MarshalCapabilities(fleet.Capabilities{
		CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthBrokeredKey, contract.TargetAuthEphemeralUser},
		Execution:         []contract.ExecutionRung{contract.ExecutionNoInteractiveShell, contract.ExecutionAccountRestricted},
	})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	storetest.Seed(t, st, storetest.Fixture{
		Tenant: tenant,
		Targets: []store.Target{
			{ID: "t-app", Hostname: "app.example.com", Zone: "edge"},
			{ID: "t-stale", Hostname: "stale.example.com", Zone: "edge"},
			{ID: "t-rungless", Hostname: "rungless.example.com", Zone: "edge"},
		},
		Proxies: []store.Proxy{{
			ID: "proxy-1", Zone: "edge", PublicKey: []byte("k"), State: store.EnrollmentEnrolled,
			LastHeartbeatAt: h.now, DeclaredCapabilities: caps,
		}},
	})
	// app: a fresh record that can take account-restricted. stale: a record
	// a week old. rungless: a fresh record that cannot take it.
	for _, rec := range []store.TargetCapabilityRecord{
		{Hostname: "app.example.com", Execution: []string{"no-interactive-shell", "account-restricted"}, ObservedAt: h.now.Add(-time.Hour)},
		{Hostname: "stale.example.com", Execution: []string{"no-interactive-shell", "account-restricted"}, ObservedAt: h.now.Add(-7 * 24 * time.Hour)},
		{Hostname: "rungless.example.com", Execution: []string{"no-interactive-shell"}, ObservedAt: h.now.Add(-time.Hour)},
	} {
		if err := st.TargetCapabilities().Put(t.Context(), tenant, rec); err != nil {
			t.Fatalf("seed capability record: %v", err)
		}
	}
	return h
}

var author = audit.OperatorActor{Principal: "tok-ci", Subject: "pat@example.com", CorrelationID: "corr-1"}

func bundle(rules string) []byte {
	return []byte(`schema_version: 1
tenant: acme
groups: [sre]
rules:
` + rules)
}

func restricted(id, host string, executables ...string) string {
	var cmds strings.Builder
	for _, e := range executables {
		cmds.WriteString("            - executable: " + e + "\n              form: exact\n              argv: [\"x\"]\n")
	}
	return `  - id: ` + id + `
    effect: allow
    match:
      target:
        hostnames: [` + host + `]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: whitelist
        exec_mode: restricted
        restricted_exec:
          commands:
` + cmds.String() + `      credentials:
        - method: ephemeral-user
          username: svc
          key_type: ed25519
      enforcement:
        execution: account-restricted
`
}

const plain = `  - id: sre
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

func operatorRecords(t *testing.T, h *harness, event string) []store.AuditRecord {
	t.Helper()
	rows, err := h.st.Audit().Query(t.Context(), tenant, store.AuditQuery{Event: event, Limit: 100})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	return rows
}

// Validation returns the compiler's errors verbatim: the rule, the line, the
// code, and what to do instead.
func TestValidationReturnsTheCompilersErrorsVerbatim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	broken := bundle(plain + `  - id: never
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
          credential_ref: vault://y
`)
	rep, cand, err := h.svc.Validate(t.Context(), tenant, broken, "pat@example.com")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cand != nil || rep.Valid || len(rep.Rejections) != 1 {
		t.Fatalf("an unreachable rule validated: valid %v, rejections %+v", rep.Valid, rep.Rejections)
	}
	r := rep.Rejections[0]
	if r.Code != "rule.unreachable" || r.Rule != "never" || r.Line != 19 {
		t.Errorf("rejection = %s on %q at line %d, want rule.unreachable on never at line 19", r.Code, r.Rule, r.Line)
	}
	for _, want := range []string{"line 19", `rule "never"`, "move it above sre", "[rule.unreachable]"} {
		if !strings.Contains(r.Message, want) {
			t.Errorf("the message %q does not carry %q", r.Message, want)
		}
	}
	if r.Parameters["shadowed_by"] == "" && len(r.Parameters) == 0 {
		t.Errorf("the rejection carries no parameters: %+v", r)
	}

	if _, err := h.svc.Upload(t.Context(), tenant, broken, author); !errors.As(err, new(*authoring.InvalidError)) {
		t.Errorf("uploading a broken bundle answered %v, want InvalidError", err)
	}
	if v, _ := h.svc.Versions(t.Context(), tenant); len(v) != 0 {
		t.Errorf("a refused upload stored %d versions", len(v))
	}
}

// Upload → activate → upload → activate → rollback, with an audit record of
// every act naming the actor and the versions it moved between.
func TestTheBundleLifecycleEndToEnd(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()

	first, err := h.svc.Upload(ctx, tenant, bundle(plain), author)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if first.Version.Version != 1 || first.Version.Active || first.Existing {
		t.Fatalf("first upload = %+v, want version 1, inactive", first.Version)
	}
	again, err := h.svc.Upload(ctx, tenant, bundle(plain), author)
	if err != nil || !again.Existing || again.Version.Version != 1 {
		t.Fatalf("re-uploading identical source = %+v, %v; want version 1, existing", again, err)
	}
	if _, err := h.svc.Activate(ctx, tenant, 1, nil, author); err != nil {
		t.Fatalf("activate 1: %v", err)
	}

	second := bundle(plain + restricted("logs", "app.example.com", "/usr/bin/uptime"))
	res, err := h.svc.Apply(ctx, tenant, second, nil, author)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Activate.Version.Version != 2 || res.Activate.Previous != 1 || res.Activate.Rollback {
		t.Errorf("apply = %+v, want 2 activated over 1", res.Activate)
	}

	back, err := h.svc.Activate(ctx, tenant, 1, nil, author)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if !back.Rollback || back.Previous != 2 {
		t.Errorf("rollback = %+v, want a rollback from 2", back)
	}
	active, _, err := h.svc.Active(ctx, tenant)
	if err != nil || active.Version != 1 {
		t.Fatalf("active after rollback = %+v, %v", active, err)
	}
	same, err := h.svc.Activate(ctx, tenant, 1, nil, author)
	if err != nil || !same.Unchanged {
		t.Errorf("activating the active version = %+v, %v; want unchanged", same, err)
	}

	uploads := operatorRecords(t, h, audit.EventPolicyUploaded)
	activations := operatorRecords(t, h, audit.EventPolicyActivated)
	if len(uploads) != 2 || len(activations) != 3 {
		t.Fatalf("recorded %d uploads and %d activations, want 2 and 3", len(uploads), len(activations))
	}
	for _, rec := range activations {
		if rec.Attributes[audit.AttrPrincipalID] != "tok-ci" || rec.Attributes[audit.AttrActorSubject] != "pat@example.com" {
			t.Errorf("an activation record does not name its actor: %v", rec.Attributes)
		}
		if rec.SessionID != "" || rec.Stream != audit.StreamControl {
			t.Errorf("an operator record is on stream %q with session %q", rec.Stream, rec.SessionID)
		}
	}
	newest := activations[0]
	if newest.Attributes[audit.AttrBefore] != "2" || newest.Attributes[audit.AttrAfter] != "1" {
		t.Errorf("the rollback's record moved %q → %q, want 2 → 1", newest.Attributes[audit.AttrBefore], newest.Attributes[audit.AttrAfter])
	}
}

// The interpreter warning stops an activation until it is accepted, by id, and
// the acceptance is on the activation's audit record.
func TestTheInterpreterWarningIsOverriddenByAcceptingIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()
	up, err := h.svc.Upload(ctx, tenant, bundle(restricted("logs", "app.example.com", "/usr/bin/find")), author)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	_, err = h.svc.Activate(ctx, tenant, up.Version.Version, nil, author)
	var notAccepted *authoring.NotAcceptedError
	if !errors.As(err, &notAccepted) || len(notAccepted.Findings) != 1 {
		t.Fatalf("activating with an unaccepted interpreter warning answered %v", err)
	}
	id := notAccepted.Findings[0].ID
	if notAccepted.Findings[0].Code != authoring.CodeInterpreter {
		t.Fatalf("the unaccepted finding is %s", notAccepted.Findings[0].Code)
	}

	res, err := h.svc.Activate(ctx, tenant, up.Version.Version, []string{id}, author)
	if err != nil {
		t.Fatalf("activating with the warning accepted: %v", err)
	}
	if len(res.Accepted) != 1 || res.Accepted[0] != id {
		t.Errorf("accepted = %v, want [%s]", res.Accepted, id)
	}
	recs := operatorRecords(t, h, audit.EventPolicyActivated)
	if len(recs) != 1 || recs[0].Attributes[audit.AttrAccepted] != id {
		t.Errorf("the activation record names accepted findings %q, want %q", recs[0].Attributes[audit.AttrAccepted], id)
	}
}

// Satisfiability against a real fleet, both directions — and the case that must
// publish: a target whose record is stale is advice, never a refusal.
func TestSatisfiabilityAgainstTheFleet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()

	// A rung the target's fresh record says it cannot take: a warning.
	rep, _, err := h.svc.Validate(ctx, tenant, bundle(restricted("tight", "rungless.example.com", "/usr/bin/uptime")), "")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if f := findingWith(rep, authoring.CodeTargetCannotTake); f == nil || f.Severity != authoring.SeverityWarning || f.Rule != "tight" {
		t.Errorf("a target that cannot take the rung: %+v", rep.Findings)
	}

	// A rung no enrolled proxy provides: a warning naming rule and proxy.
	confined := strings.Replace(restricted("confined", "app.example.com", "/usr/bin/uptime"),
		"execution: account-restricted", "execution: account-confined", 1)
	rep, _, err = h.svc.Validate(ctx, tenant, bundle(confined), "")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	f := findingWith(rep, authoring.CodeNoProxyProvides)
	if f == nil || f.Rule != "confined" || !strings.Contains(f.Message, "proxy-1") {
		t.Errorf("a rung no proxy provides: %+v", rep.Findings)
	}

	// STALE, UNDATED OR ABSENT publishes. Refusing it is the plausible bug
	// that makes every unprobeable appliance unauthorable.
	stale := bundle(restricted("stale", "stale.example.com", "/usr/bin/uptime"))
	up, err := h.svc.Upload(ctx, tenant, stale, author)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if f := findingWith(up.Report, authoring.CodeTargetUnproven); f == nil || f.Severity != authoring.SeverityAdvice {
		t.Fatalf("a stale record: %+v, want advice", up.Report.Findings)
	}
	if _, err := h.svc.Activate(ctx, tenant, up.Version.Version, nil, author); err != nil {
		t.Errorf("a bundle whose target's record is stale was refused: %v", err)
	}
}

// A registered validator's blocking finding stops activation, and cannot be
// accepted away; its error is an outage.
type governance struct {
	findings []ext.Finding
	err      error
	// seen records the versions it was handed, when set.
	seen *[]string
}

func (g governance) Validate(_ context.Context, doc ext.PolicyDocument) ([]ext.Finding, error) {
	if g.seen != nil {
		*g.seen = append(*g.seen, doc.Ref.Version)
	}
	return g.findings, g.err
}

// A validator is told which version it is judging once Control has assigned
// one: never at validate or upload, always at activation, which is the
// judgement that decides.
func TestAValidatorIsToldTheVersionItIsActivating(t *testing.T) {
	t.Parallel()
	var seen []string
	h := newHarness(t, ext.Bound[ext.PolicyValidator]{
		Registration: ext.Registration{Provider: "example.com/governance"}, Impl: governance{seen: &seen},
	})
	up, err := h.svc.Upload(t.Context(), tenant, bundle(plain), author)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := h.svc.Activate(t.Context(), tenant, up.Version.Version, nil, author); err != nil {
		t.Fatalf("activate: %v", err)
	}
	want := []string{"", strconv.FormatInt(up.Version.Version, 10)}
	if !slices.Equal(seen, want) {
		t.Fatalf("the validator was handed versions %q, want %q", seen, want)
	}
}

func TestAValidatorAddsGovernanceOnTop(t *testing.T) {
	t.Parallel()
	h := newHarness(t, ext.Bound[ext.PolicyValidator]{
		Registration: ext.Registration{Provider: "example.com/governance"},
		Impl: governance{findings: []ext.Finding{{
			Severity: ext.FindingBlocking, Code: "prod.needs_approval", Path: "sre", Message: "production needs an approval obligation",
		}}},
	})
	up, err := h.svc.Upload(t.Context(), tenant, bundle(plain), author)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	f := findingWith(up.Report, "prod.needs_approval")
	if f == nil || f.Source != "example.com/governance" || f.Severity != authoring.SeverityBlocking {
		t.Fatalf("the validator's finding: %+v", up.Report.Findings)
	}
	_, err = h.svc.Activate(t.Context(), tenant, up.Version.Version, []string{f.ID}, author)
	if !errors.As(err, new(*authoring.BlockedError)) {
		t.Errorf("a blocking finding, accepted, answered %v; want BlockedError", err)
	}

	broken := newHarness(t, ext.Bound[ext.PolicyValidator]{
		Registration: ext.Registration{Provider: "example.com/down"}, Impl: governance{err: errors.New("unreachable")},
	})
	if _, _, err := broken.svc.Validate(t.Context(), tenant, bundle(plain), ""); err == nil {
		t.Errorf("a validator that failed produced a report rather than an outage")
	}
}

func findingWith(rep authoring.Report, code string) *authoring.Finding {
	for i := range rep.Findings {
		if rep.Findings[i].Code == code {
			return &rep.Findings[i]
		}
	}
	return nil
}
