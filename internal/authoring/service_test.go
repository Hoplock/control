// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package authoring_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	return newHarnessWith(t, nil, validators...)
}

// newHarnessWith is newHarness with the service's options adjusted first.
func newHarnessWith(t *testing.T, adjust func(*authoring.Options), validators ...ext.Bound[ext.PolicyValidator]) *harness {
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
	opts := authoring.Options{
		Store: st, Fleet: registry, Emitter: emitter, Reader: h.reader, Validators: validators, Now: clock,
	}
	if adjust != nil {
		adjust(&opts)
	}
	h.svc, err = authoring.New(opts)
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
	if v, _ := h.svc.Versions(t.Context(), tenant, store.Page{Limit: store.DefaultPageLimit}); len(v.Rows) != 0 {
		t.Errorf("a refused upload stored %d versions", len(v.Rows))
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

	// A rung no enrolled proxy provides: a warning naming the rule, and the
	// proxy in its parameters beside their count (0035); the message gives the
	// count, never the list.
	confined := strings.Replace(restricted("confined", "app.example.com", "/usr/bin/uptime"),
		"execution: account-restricted", "execution: account-confined", 1)
	rep, _, err = h.svc.Validate(ctx, tenant, bundle(confined), "")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	f := findingWith(rep, authoring.CodeNoProxyProvides)
	if f == nil || f.Rule != "confined" {
		t.Fatalf("a rung no proxy provides: %+v", rep.Findings)
	}
	if params, _ := json.Marshal(f.Parameters); !strings.Contains(string(params), `"proxy_id":"proxy-1"`) ||
		f.Parameters["proxies_count"] != 1 || strings.Contains(f.Message, "proxy-1") {
		t.Errorf("a rung no proxy provides names %s and says %q; want proxy-1 in its parameters, counted, and a message giving the count",
			params, f.Message)
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

// A validator's severity is ext's closed enum. One this server does not know
// is a validator bug, and reading it as anything that gates less — advice, by
// ext's String default — would let a governance rule pass silently. It is an
// outage, as a validator's error is.
func TestAValidatorSeverityOutsideTheEnumIsAnOutage(t *testing.T) {
	t.Parallel()
	h := newHarness(t, ext.Bound[ext.PolicyValidator]{
		Registration: ext.Registration{Provider: "example.com/governance"},
		Impl:         governance{findings: []ext.Finding{{Severity: ext.FindingBlocking + 1, Code: "prod.needs_approval", Path: "sre"}}},
	})
	if rep, _, err := h.svc.Validate(t.Context(), tenant, bundle(plain), ""); err == nil {
		t.Fatalf("a finding of severity %d was reported as %+v, want an outage", ext.FindingBlocking+1, rep.Findings)
	}
	if _, err := h.svc.Upload(t.Context(), tenant, bundle(plain), author); err == nil {
		t.Errorf("an upload stored a bundle a validator could not grade")
	}
}

// ---------------------------------------------------------------------------
// the lifecycle under concurrency
// ---------------------------------------------------------------------------

// Identical sources uploaded at once are one version: an identical source is
// the version already stored, never a duplicate (PLAN §5.6), however the
// uploads interleave. Different sources at once each get their own number.
func TestConcurrentUploadsMintOneVersionPerSource(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const n = 8
	errs := make(chan error, 2*n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := h.svc.Upload(t.Context(), tenant, bundle(plain), author)
			errs <- err
		}()
		go func(i int) {
			defer wg.Done()
			_, err := h.svc.Upload(t.Context(), tenant, bundle(plain+restricted("r"+strconv.Itoa(i), "app.example.com", "/usr/bin/uptime")), author)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent upload failed: %v", err)
		}
	}
	page, err := h.svc.Versions(t.Context(), tenant, store.Page{Limit: store.DefaultPageLimit})
	if err != nil || page.More {
		t.Fatalf("versions: %v (more=%t)", err, page.More)
	}
	versions := page.Rows
	digests := map[string]int{}
	for _, v := range versions {
		digests[v.Digest]++
	}
	if len(versions) != n+1 || len(digests) != n+1 {
		t.Errorf("%d versions over %d digests, want %d of each: %v", len(versions), len(digests), n+1, digests)
	}
	if got := len(operatorRecords(t, h, audit.EventPolicyUploaded)); got != n+1 {
		t.Errorf("%d upload records, want %d", got, n+1)
	}
}

// Activations at once leave exactly one version active, and their records are
// a chain: each names, as `before`, the version the previous one made active,
// and an activation that found its version already active records nothing.
// That is what an auditor reads to know which bundle served when.
func TestConcurrentActivationsRecordAChain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const n = 4
	for i := 0; i < n; i++ {
		if _, err := h.svc.Upload(t.Context(), tenant, bundle(plain+restricted("r"+strconv.Itoa(i), "app.example.com", "/usr/bin/uptime")), author); err != nil {
			t.Fatalf("upload: %v", err)
		}
	}
	for round := 0; round < 5; round++ {
		var wg sync.WaitGroup
		for i := 0; i < 2*n; i++ {
			wg.Add(1)
			go func(v int64) {
				defer wg.Done()
				if _, err := h.svc.Activate(t.Context(), tenant, v, nil, author); err != nil {
					t.Errorf("activate %d: %v", v, err)
				}
			}(int64(i%n) + 1)
		}
		wg.Wait()
	}
	recs := operatorRecords(t, h, audit.EventPolicyActivated)
	slices.SortFunc(recs, func(a, b store.AuditRecord) int { return int(a.ChainSeq - b.ChainSeq) })
	before := ""
	for _, rec := range recs {
		got, after := rec.Attributes[audit.AttrBefore], rec.Attributes[audit.AttrAfter]
		if got != before || after == before {
			t.Fatalf("record %d moved %q → %q, but %q was active", rec.ChainSeq, got, after, before)
		}
		before = after
	}
	active, _, err := h.svc.Active(t.Context(), tenant)
	if err != nil || strconv.FormatInt(active.Version, 10) != before {
		t.Errorf("active is %+v (%v), the last record says %q", active, err, before)
	}
}

// ---------------------------------------------------------------------------
// the lifecycle's edges
// ---------------------------------------------------------------------------

// Each act's record is written in the act's own transaction: the stored row
// and its operator record carry one xmin.
func TestUploadAndActivationAreRecordedInTheirOwnTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()
	up, err := h.svc.Upload(ctx, tenant, bundle(plain), author)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	xmin := func(query string, args ...any) string {
		t.Helper()
		var x string
		if err := h.st.Pool().QueryRow(ctx, query, args...).Scan(&x); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return x
	}
	row := `SELECT xmin::text FROM policy_bundles WHERE tenant = $1 AND version = $2`
	rec := `SELECT xmin::text FROM audit_records WHERE tenant = $1 AND event = $2 ORDER BY chain_seq DESC LIMIT 1`
	if a, b := xmin(row, tenant, up.Version.Version), xmin(rec, tenant, audit.EventPolicyUploaded); a != b {
		t.Errorf("the upload committed in %s and its record in %s", a, b)
	}
	if _, err := h.svc.Activate(ctx, tenant, up.Version.Version, nil, author); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if a, b := xmin(row, tenant, up.Version.Version), xmin(rec, tenant, audit.EventPolicyActivated); a != b {
		t.Errorf("the activation committed in %s and its record in %s", a, b)
	}
}

// Accepting an id that does not arise accepts nothing, and is not an error:
// a CI job carrying an old -accept line forward publishes, and the record
// names only what was accepted.
func TestAcceptingAFindingThatNoLongerArisesAcceptsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	up, err := h.svc.Upload(t.Context(), tenant, bundle(plain), author)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	res, err := h.svc.Activate(t.Context(), tenant, up.Version.Version, []string{"authoring.interpreter_in_allow_list/gone//usr/bin/awk"}, author)
	if err != nil {
		t.Fatalf("activating with a stale acceptance: %v", err)
	}
	if len(res.Accepted) != 0 {
		t.Errorf("accepted = %v, want nothing", res.Accepted)
	}
	recs := operatorRecords(t, h, audit.EventPolicyActivated)
	if len(recs) != 1 || recs[0].Attributes[audit.AttrAccepted] != "" {
		t.Errorf("the record names %q as accepted", recs[0].Attributes[audit.AttrAccepted])
	}
}

// Apply with a warning unaccepted stores the version, inactive, and the
// refusal names it, so the next run accepts by id and stores nothing new.
func TestAnApplyRefusedForAWarningStoresTheVersionAndNamesIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()
	source := bundle(restricted("logs", "app.example.com", "/usr/bin/find"))
	res, err := h.svc.Apply(ctx, tenant, source, nil, author)
	var notAccepted *authoring.NotAcceptedError
	if !errors.As(err, &notAccepted) || res.Upload.Version.Version != 1 || res.Upload.Version.Active {
		t.Fatalf("apply = %+v, %v; want version 1 stored inactive and the warning refused", res.Upload.Version, err)
	}
	if _, _, err := h.svc.Active(ctx, tenant); !store.IsNotFound(err) {
		t.Errorf("a refused apply left a version active: %v", err)
	}
	again, err := h.svc.Apply(ctx, tenant, source, []string{notAccepted.Findings[0].ID}, author)
	if err != nil || !again.Upload.Existing || again.Activate.Version.Version != 1 {
		t.Fatalf("the re-run = %+v, %v; want version 1, existing, activated", again, err)
	}
	if got := len(operatorRecords(t, h, audit.EventPolicyUploaded)); got != 1 {
		t.Errorf("%d upload records, want 1", got)
	}
}

// A stored version this release no longer compiles is refused at activation
// and never served: the pointer stays where it was.
func TestAStoredVersionThatNoLongerCompilesIsNeverActivated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()
	first, err := h.svc.Upload(ctx, tenant, bundle(plain), author)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := h.svc.Activate(ctx, tenant, first.Version.Version, nil, author); err != nil {
		t.Fatalf("activate: %v", err)
	}
	stale := []byte("schema_version: 99\ntenant: acme\nrules: []\n")
	if err := h.st.PolicyBundles().Insert(ctx, tenant, store.PolicyBundle{Version: 2, Source: stale, Hash: authoring.Digest(stale), UploadedBy: "older-release"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := h.svc.Activate(ctx, tenant, 2, nil, author); !errors.As(err, new(*authoring.InvalidError)) {
		t.Fatalf("activating a version that does not compile answered %v, want InvalidError", err)
	}
	if active, _, err := h.svc.Active(ctx, tenant); err != nil || active.Version != 1 {
		t.Errorf("active = %+v, %v; want version 1 still", active, err)
	}
}

// A rollback says so on its record.
func TestARollbackIsRecordedAsOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()
	for _, src := range [][]byte{bundle(plain), bundle(plain + restricted("logs", "app.example.com", "/usr/bin/uptime"))} {
		up, err := h.svc.Upload(ctx, tenant, src, author)
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		if _, err := h.svc.Activate(ctx, tenant, up.Version.Version, nil, author); err != nil {
			t.Fatalf("activate: %v", err)
		}
	}
	if _, err := h.svc.Activate(ctx, tenant, 1, nil, author); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	recs := operatorRecords(t, h, audit.EventPolicyActivated)
	want := []string{"true", "false", "false"} // newest first
	for i, rec := range recs {
		if got := rec.Attributes[audit.AttrOperatorPrefix+"rollback"]; got != want[i] {
			t.Errorf("record %d (%s → %s): rollback %q, want %q", i, rec.Attributes[audit.AttrBefore], rec.Attributes[audit.AttrAfter], got, want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// validators
// ---------------------------------------------------------------------------

type recording struct {
	mu   *sync.Mutex
	docs *[]ext.PolicyDocument
}

func (r recording) Validate(_ context.Context, doc ext.PolicyDocument) ([]ext.Finding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.docs = append(*r.docs, doc)
	return []ext.Finding{{Severity: ext.FindingAdvice, Code: "gov.noted", Path: "sre", Detail: map[string]string{"why": "noted"}}}, nil
}

// Every registered validator runs on validate, upload and activate, and is
// handed the document as it stands at each: no version before upload, the
// stored version at activation, the version active when it is asked, who
// asked and when. Its Detail and Path reach the finding.
func TestEveryValidatorRunsOnEveryActWithTheDocumentOfThatAct(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var a, b []ext.PolicyDocument
	h := newHarness(t,
		ext.Bound[ext.PolicyValidator]{Registration: ext.Registration{Provider: "example.com/a"}, Impl: recording{&mu, &a}},
		ext.Bound[ext.PolicyValidator]{Registration: ext.Registration{Provider: "example.com/b"}, Impl: recording{&mu, &b}},
	)
	ctx := t.Context()
	rep, _, err := h.svc.Validate(ctx, tenant, bundle(plain), "pat@example.com")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	f := findingWith(rep, "gov.noted")
	if f == nil || f.ID != "gov.noted/example.com/a/sre" || f.Rule != "sre" || f.Parameters["why"] != "noted" {
		t.Errorf("the validator's finding = %+v", f)
	}
	first, err := h.svc.Upload(ctx, tenant, bundle(plain), author)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := h.svc.Activate(ctx, tenant, first.Version.Version, nil, author); err != nil {
		t.Fatalf("activate 1: %v", err)
	}
	h.now = h.now.Add(time.Hour)
	second, err := h.svc.Upload(ctx, tenant, bundle(plain+restricted("logs", "app.example.com", "/usr/bin/uptime")), author)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := h.svc.Activate(ctx, tenant, second.Version.Version, nil, author); err != nil {
		t.Fatalf("activate 2: %v", err)
	}

	type seen struct{ version, active, author, digest string }
	want := []seen{
		{"", "", "pat@example.com", authoring.Digest(bundle(plain))},
		{"", "", author.Subject, first.Version.Digest},
		{"1", "", author.Subject, first.Version.Digest},
		{"", "1", author.Subject, second.Version.Digest},
		{"2", "1", author.Subject, second.Version.Digest},
	}
	for name, docs := range map[string][]ext.PolicyDocument{"a": a, "b": b} {
		if len(docs) != len(want) {
			t.Fatalf("validator %s ran %d times, want %d", name, len(docs), len(want))
		}
		for i, d := range docs {
			got := seen{d.Ref.Version, d.ActiveVersion, d.AuthorID, d.Ref.Digest}
			if got != want[i] || d.Ref.Tenant != ext.Tenant(tenant) || len(d.Source) == 0 || d.ContentType == "" {
				t.Errorf("validator %s, call %d: %+v, want %+v", name, i, got, want[i])
			}
		}
		if !docs[4].ProposedAt.Equal(h.now) || !docs[0].ProposedAt.Equal(h.now.Add(-time.Hour)) {
			t.Errorf("validator %s was told proposed at %v and %v", name, docs[0].ProposedAt, docs[4].ProposedAt)
		}
	}
}

// The unmet-policy evidence is read newest first up to a limit, and a read
// that stopped there with more in the window says so: one target reporting
// often can push every other target's records out, and a report silent about
// that reads as clean.
func TestATruncatedUnmetReadSaysSo(t *testing.T) {
	t.Parallel()
	h := newHarnessWith(t, func(o *authoring.Options) { o.UnmetLimit = 2 })
	ingest, err := audit.New(audit.Options{Store: h.st})
	if err != nil {
		t.Fatalf("ingester: %v", err)
	}
	unmet := func(n int) {
		t.Helper()
		var recs []contract.LogRecord
		for i := 0; i < n; i++ {
			recs = append(recs, contract.LogRecord{
				RecordID: "unmet-" + strconv.Itoa(i) + "-" + strconv.Itoa(n), SessionID: "sess-u",
				Timestamp: h.now.Add(-time.Duration(i+1) * time.Minute).Format(time.RFC3339),
				Kind:      "error", Severity: contract.SeverityWarn, Message: "unmet", Target: "noisy.example.com",
				Attributes: map[string]string{
					audit.AttrEvent: audit.EventAlgorithmPolicyUnmet, audit.AttrAlgorithmProfile: "default",
					audit.AttrAlgorithmAxis: "key_exchange", audit.AttrAlgorithmPolicyCause: "profile",
				},
			})
		}
		if _, err := ingest.Ingest(t.Context(), audit.Submission{Tenant: tenant, ProxyID: "proxy-1", Records: recs}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	}
	unmet(2)
	rep, _, err := h.svc.Validate(t.Context(), tenant, bundle(plain), "")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if f := findingWith(rep, authoring.CodePolicyUnmetTruncated); f != nil {
		t.Errorf("a read that reached everything said it was cut short: %+v", f)
	}
	unmet(1)
	rep, _, err = h.svc.Validate(t.Context(), tenant, bundle(plain), "")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	f := findingWith(rep, authoring.CodePolicyUnmetTruncated)
	if f == nil || f.Severity != authoring.SeverityAdvice || f.Parameters["limit"] != 2 {
		t.Errorf("a read cut short at 2 of 3: %+v, want advice naming the limit", f)
	}

	// The read is one page of the audit query, so its limit is one a page
	// may hold; a larger one is refused rather than clamped short of it
	// (M26.1), which would read as a complete answer.
	opts := authoring.Options{Store: h.st, Fleet: fleet.New(h.st), Emitter: emitterFor(t, h.st), Reader: h.reader,
		UnmetLimit: store.MaxPageLimit + 1}
	if _, err := authoring.New(opts); err == nil {
		t.Errorf("an unmet limit of %d, past one page, was accepted", opts.UnmetLimit)
	}
	opts.UnmetLimit = store.MaxPageLimit
	if _, err := authoring.New(opts); err != nil {
		t.Errorf("an unmet limit of one whole page was refused: %v", err)
	}
}

func emitterFor(t *testing.T, st *store.Store) *audit.Emitter {
	t.Helper()
	in, err := audit.New(audit.Options{Store: st})
	if err != nil {
		t.Fatalf("ingester: %v", err)
	}
	emitter, err := audit.NewEmitter(in)
	if err != nil {
		t.Fatalf("emitter: %v", err)
	}
	return emitter
}
