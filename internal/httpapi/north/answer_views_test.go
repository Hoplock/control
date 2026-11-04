// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The four answers 0045 names: each view's members, by name, as a client
// reads them off the wire.

// members decodes one object and returns its member names, sorted.
func members(t *testing.T, raw json.RawMessage) (map[string]json.RawMessage, []string) {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return obj, slices.Sorted(maps.Keys(obj))
}

func getJSON(t *testing.T, f *serverFixture, path, token string) map[string]json.RawMessage {
	t.Helper()
	resp := f.do(t, "GET", path, token, nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s answered %d", path, resp.StatusCode)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTheShowcaseJoinNamesEveryMemberAndAnAgedOutDecisionIsNotFound(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	ctx := t.Context()
	if err := f.st.Targets().Upsert(ctx, "tenant-a", storetest.Target("t-core", "core-1.example", map[string]string{"env": "prod"})); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Targets().Upsert(ctx, "tenant-a", storetest.Target("t-bare", "bare-1.example", nil)); err != nil {
		t.Fatal(err)
	}
	decidedAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := f.st.Decisions().Insert(ctx, "tenant-a", store.Decision{
		ID: "dec-found", SubjectID: "alice@example.com", Effect: "allow", SessionID: "sess-j", ProxyID: "proxy-j",
		MatchedRule: "sre-prod", InputsDigest: "x", Inputs: json.RawMessage(`{}`), DecidedAt: decidedAt,
		Snapshot: json.RawMessage(`{"route_type":"nexthop","target":"core-1.example"}`),
	}); err != nil {
		t.Fatal(err)
	}
	in, err := audit.New(audit.Options{Store: f.st})
	if err != nil {
		t.Fatal(err)
	}
	cmd := func(id, target, decision string, at time.Time) contract.LogRecord {
		return contract.LogRecord{
			RecordID: id, SessionID: "sess-j", Kind: "command", Severity: contract.SeverityInfo,
			Timestamp: at.Format(time.RFC3339), Target: target, Message: "blocked",
			Attributes: map[string]string{
				"action": store.ActionBlockCommand, "command": "rm -rf /", audit.AttrDecisionID: decision,
			},
		}
	}
	if _, err := in.Ingest(ctx, audit.Submission{Tenant: "tenant-a", ProxyID: "proxy-j", Records: []contract.LogRecord{
		cmd("join-found", "core-1.example", "dec-found", decidedAt.Add(time.Minute)),
		cmd("join-gone", "core-1.example", "dec-aged-out", decidedAt.Add(2*time.Minute)),
	}}); err != nil {
		t.Fatal(err)
	}

	body := getJSON(t, f, "/api/v1/tenants/tenant-a/audit/blocked-commands?label=env%3Dprod&order=asc", token)
	var items []json.RawMessage
	if err := json.Unmarshal(body["blocked"], &items); err != nil || len(items) != 2 {
		t.Fatalf("blocked = %s (%v), want two items", body["blocked"], err)
	}

	found, names := members(t, items[0])
	if want := []string{"action", "command", "decided_at", "decision_found", "matched_rule", "record", "route_type", "target_labels", "target_zone"}; !slices.Equal(names, want) {
		t.Errorf("a found decision's item names %v, want %v", names, want)
	}
	for member, want := range map[string]string{
		"command": `"rm -rf /"`, "action": `"` + store.ActionBlockCommand + `"`, "target_zone": `"zone-a"`,
		"target_labels": `{"env":"prod"}`, "decision_found": `true`, "matched_rule": `"sre-prod"`,
		"route_type": `"nexthop"`, "decided_at": `"2026-10-01T12:00:00Z"`,
	} {
		if got := string(found[member]); got != want {
			t.Errorf("%s = %s, want %s", member, got, want)
		}
	}
	// The record is an audit record's item, one record shape for a client.
	record, recordNames := members(t, found["record"])
	for _, want := range []string{"record_id", "stream", "chain_seq", "hash", "recorded_at", "body"} {
		if !slices.Contains(recordNames, want) {
			t.Errorf("the item's record has no %s: %v", want, recordNames)
		}
	}
	for _, gone := range []string{"Grant", "Enforcement", "Device", "Gap", "RecordID"} {
		if slices.Contains(recordNames, gone) {
			t.Errorf("the item's record still carries %s", gone)
		}
	}
	if string(record["record_id"]) != `"join-found"` {
		t.Errorf("record_id = %s", record["record_id"])
	}

	gone, names := members(t, items[1])
	if want := []string{"action", "command", "decision_found", "record", "target_labels", "target_zone"}; !slices.Equal(names, want) {
		t.Errorf("an aged-out decision's item names %v, want %v: no rule, no instant", names, want)
	}
	if string(gone["decision_found"]) != "false" {
		t.Errorf("decision_found = %s, want false", gone["decision_found"])
	}
}

func TestASessionsLossNamesEveryMemberPerCause(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	in, err := audit.New(audit.Options{Store: f.st})
	if err != nil {
		t.Fatal(err)
	}
	gap := func(id, cause, records, bytes string) contract.LogRecord {
		return contract.LogRecord{
			RecordID: id, SessionID: "sess-loss", Kind: "error", Severity: contract.SeverityInfo,
			Timestamp: time.Now().UTC().Format(time.RFC3339), Message: "gap",
			Attributes: map[string]string{
				audit.AttrEvent: audit.EventLoggingGap, audit.AttrGapCause: cause,
				audit.AttrGapRecords: records, audit.AttrGapBytes: bytes,
			},
		}
	}
	if _, err := in.Ingest(t.Context(), audit.Submission{Tenant: "tenant-a", ProxyID: "proxy-loss", Records: []contract.LogRecord{
		gap("loss-1", "evicted", "4", "1000"), gap("loss-2", "evicted", "6", "1048"), gap("loss-3", "refused", "1", "10"),
	}}); err != nil {
		t.Fatal(err)
	}

	body := getJSON(t, f, "/api/v1/tenants/tenant-a/audit/sessions/sess-loss/loss", token)
	var rows []json.RawMessage
	if err := json.Unmarshal(body["loss"], &rows); err != nil || len(rows) != 2 {
		t.Fatalf("loss = %s (%v), want a row per cause", body["loss"], err)
	}
	want := map[string]string{
		"evicted": `{"cause":"evicted","reports":2,"records":10,"bytes":2048}`,
		"refused": `{"cause":"refused","reports":1,"records":1,"bytes":10}`,
	}
	for _, row := range rows {
		obj, _ := members(t, row)
		var cause string
		_ = json.Unmarshal(obj["cause"], &cause)
		if got := string(row); got != want[cause] {
			t.Errorf("the %s row = %s, want %s", cause, got, want[cause])
		}
	}

	body = getJSON(t, f, "/api/v1/tenants/tenant-a/audit/sessions/sess-none/loss", token)
	if string(body["loss"]) != "[]" {
		t.Errorf("no loss = %s, want []", body["loss"])
	}
}

func TestTheFleetViewNamesEveryMemberAndNeverAYearOneInstant(t *testing.T) {
	f := newServer(t)
	token := adminToken(t, f, "tenant-a")
	ctx := t.Context()
	for _, id := range []string{"p-quiet", "p-heard"} {
		if err := f.st.Proxies().Upsert(ctx, "tenant-a", storetest.Proxy(id, "zone-a")); err != nil {
			t.Fatal(err)
		}
	}
	heard := time.Now().UTC().Truncate(time.Second)
	if err := f.st.Proxies().RecordHealth(ctx, "tenant-a", store.ProxyHealthReport{
		ProxyID: "p-heard", At: heard, SessionCount: 3, LastError: "upstream refused",
	}); err != nil {
		t.Fatal(err)
	}

	body := getJSON(t, f, "/api/v1/tenants/tenant-a/proxies", token)
	if strings.Contains(string(body["proxies"]), "0001-01-01") {
		t.Errorf("the fleet view renders a year-one instant: %s", body["proxies"])
	}
	var items []json.RawMessage
	if err := json.Unmarshal(body["proxies"], &items); err != nil || len(items) != 2 {
		t.Fatalf("proxies = %s (%v)", body["proxies"], err)
	}
	always := []string{"capabilities", "config", "contract_version", "live", "proxy_id", "relay_registrations", "session_count", "state", "zone"}

	heardItem, names := members(t, items[0])
	if want := slices.Sorted(slices.Values(append(slices.Clone(always), "last_error", "last_error_at", "last_heartbeat_at"))); !slices.Equal(names, want) {
		t.Errorf("a proxy that reported names %v, want %v", names, want)
	}
	if string(heardItem["proxy_id"]) != `"p-heard"` || string(heardItem["session_count"]) != "3" ||
		string(heardItem["last_error"]) != `"upstream refused"` || string(heardItem["relay_registrations"]) != "[]" {
		t.Errorf("the reported proxy = %s", items[0])
	}
	var at time.Time
	if err := json.Unmarshal(heardItem["last_heartbeat_at"], &at); err != nil || !at.Equal(heard) || at.Location() != time.UTC {
		t.Errorf("last_heartbeat_at = %s, want %s in UTC", heardItem["last_heartbeat_at"], heard.Format(time.RFC3339))
	}

	quiet, names := members(t, items[1])
	if !slices.Equal(names, always) {
		t.Errorf("a proxy that never reported names %v, want %v: no instant", names, always)
	}
	if string(quiet["proxy_id"]) != `"p-quiet"` {
		t.Errorf("the quiet proxy = %s", items[1])
	}

	// Nothing configured: the defaults, in whole seconds.
	if got, want := string(body["liveness"]),
		`{"heartbeat_ttl_seconds":90,"relay_registration_ttl_seconds":60,"target_capability_ttl_seconds":86400,"report_after_seconds":21600}`; got != want {
		t.Errorf("liveness = %s, want %s", got, want)
	}
}
