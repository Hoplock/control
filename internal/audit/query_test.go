// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// seedEstate builds the fixture the showcase query runs over: two targets, one
// labelled `env=prod` and one not, and the decisions that permitted the
// sessions.
func seedEstate(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := t.Context()

	storetest.Seed(t, st, storetest.Fixture{
		Tenant: tenantA,
		Targets: []store.Target{
			{ID: "t-prod", Hostname: "db-1.example.invalid", Zone: "core", Labels: map[string]string{"env": "prod", "tier": "db"}},
			{ID: "t-dev", Hostname: "dev-1.example.invalid", Zone: "core", Labels: map[string]string{"env": "dev"}},
		},
	})

	for _, d := range []store.Decision{
		{
			ID: "dec-prod", SubjectID: "alice@example.invalid", TargetID: "t-prod",
			InputsDigest: "x", Effect: "allow", SessionID: "sess-prod",
			Snapshot: json.RawMessage(`{"route_type":"nexthop"}`),
		},
		{
			ID: "dec-dev", SubjectID: "bob@example.invalid", TargetID: "t-dev",
			InputsDigest: "y", Effect: "allow", SessionID: "sess-dev",
			Snapshot: json.RawMessage(`{"route_type":"direct"}`),
		},
	} {
		d := d
		d.MatchedRule = "rule-" + d.ID
		if err := st.Decisions().Insert(ctx, tenantA, d); err != nil {
			t.Fatalf("seed decision %s: %v", d.ID, err)
		}
	}
}

func blocked(id, session, target, decisionID, command string) contract.LogRecord {
	rec := record(id, session)
	rec.Kind = "command"
	rec.Severity = contract.SeverityCritical
	rec.Target = target
	rec.Attributes = map[string]string{
		"action":             "block_command",
		"command":            command,
		audit.AttrDecisionID: decisionID,
	}
	return rec
}

// PLAN §7, the query that sells the product: every blocked command on
// `env=prod` last week, who ran it, over which route, and which decision
// permitted the access.
func TestTheShowcaseQueryJoinsAuditToDecisions(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	seedEstate(t, st)

	prodBlocked := blocked("rec-b1", "sess-prod", "db-1.example.invalid", "dec-prod", "rm -rf /")
	prodBlocked.Subject = "alice@example.invalid"

	allowed := record("rec-a1", "sess-prod")
	allowed.Target = "db-1.example.invalid"
	allowed.Attributes = map[string]string{"action": "allow", "command": "ls"}

	devBlocked := blocked("rec-b2", "sess-dev", "dev-1.example.invalid", "dec-dev", "shutdown now")
	unknownTargetBlocked := blocked("rec-b3", "sess-prod", "nowhere.example.invalid", "dec-prod", "cat /etc/shadow")

	mustIngest(ctx, t, in, tenantA, proxyA, prodBlocked, allowed, devBlocked, unknownTargetBlocked)

	rows, err := reader.BlockedCommands(ctx, tenantA, "env", "prod", time.Time{}, time.Time{}, 0)
	if err != nil {
		t.Fatalf("BlockedCommands: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want exactly the one blocked command on an env=prod target: %+v", len(rows), rows)
	}

	got := rows[0]
	if got.Record.RecordID != "rec-b1" {
		t.Errorf("record = %q, want rec-b1", got.Record.RecordID)
	}
	if got.Command != "rm -rf /" || got.Action != "block_command" {
		t.Errorf("command/action = %q/%q", got.Command, got.Action)
	}
	if got.Record.Subject != "alice@example.invalid" {
		t.Errorf("subject = %q: 'who ran it' is half the question", got.Record.Subject)
	}
	if !got.DecisionFound {
		t.Fatal("the decision did not join; 0008 stores decision_id on both sides so that it does")
	}
	if got.MatchedRule != "rule-dec-prod" {
		t.Errorf("matched rule = %q, want rule-dec-prod", got.MatchedRule)
	}
	if got.RouteType != "nexthop" {
		t.Errorf("route type = %q, want nexthop: 'over which route' is the other half", got.RouteType)
	}
	if got.TargetLabels["env"] != "prod" || got.TargetZone != "core" {
		t.Errorf("target = zone %q labels %v", got.TargetZone, got.TargetLabels)
	}
}

// A record naming a decision this store does not have is still a blocked
// command. Dropping it would under-report an incident; reporting an empty rule
// as though it were the answer would be this server inventing an explanation.
func TestABlockedCommandWithNoDecisionRowIsStillReported(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	seedEstate(t, st)
	mustIngest(ctx, t, in, tenantA, proxyA,
		blocked("rec-orphan", "sess-prod", "db-1.example.invalid", "dec-aged-out", "curl evil"))

	rows, err := reader.BlockedCommands(ctx, tenantA, "env", "prod", time.Time{}, time.Time{}, 0)
	if err != nil {
		t.Fatalf("BlockedCommands: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].DecisionFound {
		t.Error("a decision that is not stored was reported as found")
	}
	if rows[0].MatchedRule != "" {
		t.Errorf("matched rule = %q, want empty rather than invented", rows[0].MatchedRule)
	}
}

// M16: "show me every session that ran under this scan" is the query grant
// context exists for, and it is indexed on system + reference.
func TestSessionsAreFoundByTheGrantTheyRanUnder(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	underScan := record("rec-scan-1", "sess-scan")
	underScan.Attributes = map[string]string{
		audit.AttrGrantSystem:                       "qualys",
		audit.AttrGrantReference:                    "SCAN-2026-09-04-1183",
		audit.AttrGrantAdditionalPrefix + "profile": "authenticated-linux",
	}
	underTicket := record("rec-chg-1", "sess-chg")
	underTicket.Attributes = map[string]string{
		audit.AttrGrantSystem:     "bmc-helix",
		audit.AttrGrantReference:  "CHG-1234",
		audit.AttrGrantAdditional: "raised by the on-call, window agreed with the DBAs",
	}
	plain := record("rec-plain", "sess-plain")

	mustIngest(ctx, t, in, tenantA, proxyA, underScan, underTicket, plain)

	rows, err := reader.UnderGrant(ctx, tenantA, "qualys", "SCAN-2026-09-04-1183", 0)
	if err != nil {
		t.Fatalf("UnderGrant: %v", err)
	}
	if len(rows) != 1 || rows[0].RecordID != "rec-scan-1" {
		t.Fatalf("got %d rows %v, want just the scan's", len(rows), rows)
	}
	if rows[0].Grant.AdditionalKind != store.AdditionalContextObject {
		t.Errorf("additional_context kind = %q, want %q", rows[0].Grant.AdditionalKind, store.AdditionalContextObject)
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(rows[0].Grant.Additional), &obj); err != nil {
		t.Fatalf("the object form did not round-trip through the database: %v", err)
	}
	if obj["profile"] != "authenticated-linux" {
		t.Errorf("additional_context = %v", obj)
	}

	ticket, err := reader.UnderGrant(ctx, tenantA, "bmc-helix", "CHG-1234", 0)
	if err != nil {
		t.Fatalf("UnderGrant: %v", err)
	}
	if len(ticket) != 1 {
		t.Fatalf("got %d rows for the ticket, want 1", len(ticket))
	}
	if ticket[0].Grant.AdditionalKind != store.AdditionalContextString {
		t.Errorf("additional_context kind = %q, want %q", ticket[0].Grant.AdditionalKind, store.AdditionalContextString)
	}
	var text string
	if err := json.Unmarshal([]byte(ticket[0].Grant.Additional), &text); err != nil {
		t.Fatalf("the string form did not round-trip through the database: %v", err)
	}
	if text != "raised by the on-call, window agreed with the DBAs" {
		t.Errorf("additional_context = %q, want it verbatim", text)
	}
}

// PLAN §7: the record reports the rung that was IN FORCE, never the one policy
// requested — so a session whose policy asked for one rung and whose record
// says another must read back as what happened.
func TestTheStoreReturnsTheRungInForceNotTheRungRequested(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	// Policy asked for `account-confined` at rung 0 of the ladder. The
	// deployment could not provide it, so the session ran attested at rung
	// 2. The record carries what happened.
	degraded := record("rec-degraded", "sess-degraded")
	degraded.Kind = "provisioning"
	degraded.Attributes = map[string]string{
		audit.AttrEvent:                      audit.EventAccountMapping,
		audit.AttrEnforcementExecution:       "no-interactive-shell",
		audit.AttrEnforcementReach:           "unrestricted",
		audit.AttrEnforcementVerified:        "false",
		audit.AttrEnforcementAttestedBy:      "network-team",
		audit.AttrTargetAuthMethod:           "static-password",
		audit.AttrTargetAuthRung:             "2",
		audit.AttrAlgorithmProfile:           "legacy-sha1",
		audit.AttrDeviceFieldPrefix + "vdom": "global",
	}

	preferred := record("rec-preferred", "sess-preferred")
	preferred.Attributes = map[string]string{
		audit.AttrTargetAuthMethod: "ephemeral-key",
		audit.AttrTargetAuthRung:   "0",
	}

	mustIngest(ctx, t, in, tenantA, proxyA, degraded, preferred)

	got, err := reader.Get(ctx, tenantA, "rec-degraded")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Enforcement.Execution != "no-interactive-shell" || got.Enforcement.Reach != "unrestricted" {
		t.Errorf("enforcement = %+v, want the rung in force", got.Enforcement)
	}
	if got.Enforcement.Verified == nil || *got.Enforcement.Verified {
		t.Fatalf("verified = %v, want an explicit false through the database", got.Enforcement.Verified)
	}
	if got.Enforcement.AttestedBy != "network-team" {
		t.Errorf("attested_by = %q, want it intact so a reader can go and ask", got.Enforcement.AttestedBy)
	}
	if got.TargetAuthMethod != "static-password" || got.TargetAuthRung == nil || *got.TargetAuthRung != 2 {
		t.Errorf("credential = %q rung %v, want static-password at rung 2", got.TargetAuthMethod, got.TargetAuthRung)
	}
	if got.AlgorithmProfile != "legacy-sha1" {
		t.Errorf("algorithm profile = %q: an operator learns a route runs on SHA-1 from the record", got.AlgorithmProfile)
	}

	// The degradation query: rung above 0, across the estate.
	degradedRows, err := reader.DegradedCredentials(ctx, tenantA, time.Time{}, time.Time{}, 0)
	if err != nil {
		t.Fatalf("DegradedCredentials: %v", err)
	}
	if len(degradedRows) != 1 || degradedRows[0].RecordID != "rec-degraded" {
		t.Fatalf("degraded = %v, want only the session that accepted its second choice", degradedRows)
	}

	// And the device fields are queryable in their own right: `vdom=global`
	// is a global administrator, `vdom=root` is one confined to a virtual
	// domain, and both records name the same host.
	global, err := reader.ByDeviceField(ctx, tenantA, "vdom", "global", 0)
	if err != nil {
		t.Fatalf("ByDeviceField: %v", err)
	}
	if len(global) != 1 || global[0].RecordID != "rec-degraded" {
		t.Fatalf("vdom=global returned %v", global)
	}
	if confined, err := reader.ByDeviceField(ctx, tenantA, "vdom", "root", 0); err != nil || len(confined) != 0 {
		t.Fatalf("vdom=root returned %v (%v), want nothing", confined, err)
	}
}

// The ephemeral-account mapping event is a first-class record: on a device
// whose account name had to drop its login segment, it is the only place
// attribution exists.
func TestTheAccountMappingEventIsQueryableOnItsOwn(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	mapping := record("rec-mapping", "sess-map")
	mapping.Kind = "provisioning"
	mapping.Severity = contract.SeverityCritical
	mapping.Subject = "alice@example.invalid"
	mapping.Attributes = map[string]string{
		audit.AttrEvent:    audit.EventAccountMapping,
		"target_account":   "hl-a7f3c1",
		"name_constrained": "true",
	}
	configChange := record("rec-config", "sess-map")
	configChange.Kind = "provisioning"
	configChange.Attributes = map[string]string{audit.AttrEvent: audit.EventDeviceConfigChange}

	mustIngest(ctx, t, in, tenantA, proxyA, mapping, configChange, record("rec-other", "sess-map"))

	mappings, err := reader.AccountMappings(ctx, tenantA, time.Time{}, time.Time{}, 0)
	if err != nil {
		t.Fatalf("AccountMappings: %v", err)
	}
	if len(mappings) != 1 || mappings[0].RecordID != "rec-mapping" {
		t.Fatalf("mappings = %v, want just the mapping event", mappings)
	}
	if mappings[0].Subject != "alice@example.invalid" {
		t.Error("the mapping event lost the attribution it exists to carry")
	}

	changes, err := reader.DeviceConfigChanges(ctx, tenantA, time.Time{}, time.Time{}, 0)
	if err != nil {
		t.Fatalf("DeviceConfigChanges: %v", err)
	}
	if len(changes) != 1 || changes[0].RecordID != "rec-config" {
		t.Fatalf("changes = %v, want just the configuration-change event", changes)
	}
}

// The dimensions PLAN §7 names, each one filtering on its own.
func TestRecordsAreFoundByEveryDimensionTheyCarry(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	one := record("rec-one", "sess-one")
	one.Subject = "alice@example.invalid"
	one.Target = "db-1.example.invalid"
	one.Severity = contract.SeverityCritical
	one.Attributes = map[string]string{audit.AttrDecisionID: "dec-1"}

	two := record("rec-two", "sess-two")
	two.Subject = "bob@example.invalid"
	two.Target = "web-1.example.invalid"
	two.Kind = "session_start"

	mustIngest(ctx, t, in, tenantA, proxyA, one, two)

	cases := map[string]struct {
		query audit.Query
		want  string
	}{
		"by session":     {audit.Query{SessionID: "sess-one"}, "rec-one"},
		"by subject":     {audit.Query{Subject: "bob@example.invalid"}, "rec-two"},
		"by target":      {audit.Query{Target: "db-1.example.invalid"}, "rec-one"},
		"by decision id": {audit.Query{DecisionID: "dec-1"}, "rec-one"},
		"by kind":        {audit.Query{Kinds: []string{"session_start"}}, "rec-two"},
		"by severity":    {audit.Query{Severities: []string{string(contract.SeverityCritical)}}, "rec-one"},
		"by proxy":       {audit.Query{ProxyID: proxyA, SessionID: "sess-two"}, "rec-two"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rows, err := reader.Find(ctx, tenantA, tc.query)
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			if len(rows) != 1 || rows[0].RecordID != tc.want {
				t.Fatalf("got %v, want just %s", rows, tc.want)
			}
		})
	}

	t.Run("by time range", func(t *testing.T) {
		future := time.Now().UTC().Add(time.Hour)
		rows, err := reader.Find(ctx, tenantA, audit.Query{From: future})
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("a window starting in the future returned %d rows", len(rows))
		}
	})
}

// A session reads forwards, because that is the order a replay needs.
func TestASessionReadsInReplayOrder(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	base := time.Now().UTC().Add(-time.Hour)
	var recs []contract.LogRecord
	for i := range 5 {
		r := record(("rec-ordered-" + string(rune('a'+i))), "sess-ordered")
		r.Timestamp = base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		recs = append(recs, r)
	}
	mustIngest(ctx, t, in, tenantA, proxyA, recs...)

	rows, err := reader.Session(ctx, tenantA, "sess-ordered", 0)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d records, want 5", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].RecordedAt.Before(rows[i-1].RecordedAt) {
			t.Fatalf("record %d is older than the one before it; a session is read forwards", i)
		}
	}
}
