// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

const testTenant = "acme"

func aRecord() contract.LogRecord {
	return contract.LogRecord{
		RecordID:  "rec-1",
		SessionID: "sess-1",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Kind:      "command",
		Severity:  contract.SeverityInfo,
		Message:   "a command ran",
		Subject:   "alice@example.invalid",
		Login:     "alice",
		Target:    "db-1.example.invalid",
	}
}

func TestParseRefusesWhatItCannotClassify(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*contract.LogRecord){
		"no record id":      func(r *contract.LogRecord) { r.RecordID = "" },
		"no session id":     func(r *contract.LogRecord) { r.SessionID = "" },
		"no kind":           func(r *contract.LogRecord) { r.Kind = "" },
		"an unknown kind":   func(r *contract.LogRecord) { r.Kind = "exfiltration" },
		"an unknown sev":    func(r *contract.LogRecord) { r.Severity = "urgent" },
		"a bad timestamp":   func(r *contract.LogRecord) { r.Timestamp = "last tuesday" },
		"a stray payload":   func(r *contract.LogRecord) { r.Payload = base64.StdEncoding.EncodeToString([]byte("x")) },
		"unreadable base64": func(r *contract.LogRecord) { r.Kind = "stream"; r.Payload = "not base64!!" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := aRecord()
			mutate(&rec)

			_, err := Parse(rec, testTenant, 3, DefaultLimits)
			var malformed *MalformedError
			if !errors.As(err, &malformed) {
				t.Fatalf("Parse: %v, want a MalformedError", err)
			}
			if malformed.Index != 3 {
				t.Errorf("index = %d, want 3: a proxy shipping a batch has to be told which record", malformed.Index)
			}
		})
	}
}

// An `error` record may belong to no session — a device sweep failure belongs
// to the fleet rather than to anybody's connection — and refusing it would
// lose the one record class that reports a failure nobody was present for.
func TestAnErrorRecordMayHaveNoSession(t *testing.T) {
	t.Parallel()

	rec := aRecord()
	rec.Kind = "error"
	rec.SessionID = ""

	if _, err := Parse(rec, testTenant, 0, DefaultLimits); err != nil {
		t.Fatalf("Parse: %v, want it accepted", err)
	}
}

// M18: the tenant comes from the proxy's enrolled identity and a record
// claiming another one is REJECTED — and rejected distinguishably, because
// quietly filing it under the enrolled tenant would hide two customers'
// configurations having been crossed.
func TestARecordNamingAnotherTenantIsRejectedDistinguishably(t *testing.T) {
	t.Parallel()

	rec := aRecord()
	rec.Attributes = map[string]string{AttrTenant: "someone-else"}

	_, err := Parse(rec, testTenant, 0, DefaultLimits)

	var mismatch *TenantMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Parse: %v, want a TenantMismatchError", err)
	}
	var malformed *MalformedError
	if errors.As(err, &malformed) {
		t.Error("a tenant mismatch also reads as a malformed record; they must be distinguishable")
	}
	if mismatch.Claimed != "someone-else" || mismatch.Enrolled != testTenant {
		t.Errorf("mismatch = %+v, want it to name both tenants", mismatch)
	}

	// Naming its OWN tenant is not a mismatch: it is redundant, not wrong.
	rec.Attributes[AttrTenant] = testTenant
	if _, err := Parse(rec, testTenant, 0, DefaultLimits); err != nil {
		t.Errorf("a record naming its own tenant: %v, want it accepted", err)
	}
}

func TestLimitsAreEnforced(t *testing.T) {
	t.Parallel()

	limits := Limits{MaxBatchRecords: 2, MaxRecordBytes: 200, MaxCaptureBytes: 8, MaxAttributes: 2}

	t.Run("a record above the byte bound", func(t *testing.T) {
		t.Parallel()
		rec := aRecord()
		rec.Message = strings.Repeat("x", 400)
		if _, err := Parse(rec, testTenant, 0, limits); err == nil {
			t.Fatal("an oversized record was accepted")
		}
	})

	t.Run("a capture above the capture bound", func(t *testing.T) {
		t.Parallel()
		rec := aRecord()
		rec.Kind = "stream"
		rec.Payload = base64.StdEncoding.EncodeToString([]byte("0123456789"))
		if _, err := Parse(rec, testTenant, 0, limits); err == nil {
			t.Fatal("an oversized capture was accepted")
		}
	})

	t.Run("too many attributes", func(t *testing.T) {
		t.Parallel()
		rec := aRecord()
		rec.Attributes = map[string]string{"a": "1", "b": "2", "c": "3"}
		if _, err := Parse(rec, testTenant, 0, limits); err == nil {
			t.Fatal("a record above the attribute bound was accepted")
		}
	})
}

// PLAN §7: the initial-auth password never reaches this server and must never
// be written even if a malformed record contains one. This is the unit half;
// TestAPasswordIsNeverOnDisk is the half that checks the database.
func TestAPasswordShapedFieldIsRedactedAndTheRemovalIsRecorded(t *testing.T) {
	t.Parallel()

	rec := aRecord()
	rec.Attributes = map[string]string{
		"password":                "hunter2",
		"device_field.password":   "hunter2",
		"grant_additional_ctx":    "kept",
		"command":                 "ls -la",
		"target_account":          "hl-a7f3c1",
		"credential_secret_thing": "hunter2",
	}

	parsed, err := Parse(rec, testTenant, 0, DefaultLimits)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	for _, key := range []string{"password", "device_field.password", "credential_secret_thing"} {
		if got := parsed.Attributes[key]; got != RedactedValue {
			t.Errorf("%s = %q, want %q", key, got, RedactedValue)
		}
	}
	if got := parsed.Attributes["command"]; got != "ls -la" {
		t.Errorf("command = %q: a redactor that mangles real evidence is worse than the value it removed", got)
	}
	if len(parsed.Redacted) != 3 {
		t.Errorf("redacted = %v, want the three credential keys", parsed.Redacted)
	}

	body, err := parsed.Body()
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if strings.Contains(string(body), "hunter2") {
		t.Fatal("the password survives into the hashed body")
	}
	if !strings.Contains(string(body), `"redacted"`) {
		t.Error("the body does not record that anything was removed; a silent removal is a record that lies by omission")
	}
}

// proxy D14 and PLAN §7: the rung the record carries is the rung IN FORCE, and
// `enforcement_verified` must survive as the three-valued fact it is.
func TestTheEnforcementRungIsProjectedAsFourFields(t *testing.T) {
	t.Parallel()

	t.Run("an attested rung reads back unverified with its attester", func(t *testing.T) {
		t.Parallel()
		rec := aRecord()
		rec.Attributes = map[string]string{
			AttrEnforcementExecution:  "account-restricted",
			AttrEnforcementReach:      "network-confined",
			AttrEnforcementVerified:   "false",
			AttrEnforcementAttestedBy: "platform-team",
		}
		row := mustRow(t, rec)

		if row.Enforcement.Verified == nil || *row.Enforcement.Verified {
			t.Fatalf("verified = %v, want an explicit false: an attested rung that reads like an applied one "+
				"turns an unverified claim into an apparent guarantee", row.Enforcement.Verified)
		}
		if row.Enforcement.AttestedBy != "platform-team" {
			t.Errorf("attested_by = %q, want it kept so a reader can go and ask that team", row.Enforcement.AttestedBy)
		}
		if row.Enforcement.Execution != "account-restricted" || row.Enforcement.Reach != "network-confined" {
			t.Errorf("the two axes did not survive: %+v", row.Enforcement)
		}
	})

	t.Run("a record stating no rung leaves verified unset", func(t *testing.T) {
		t.Parallel()
		row := mustRow(t, aRecord())
		if row.Enforcement.Verified != nil {
			t.Errorf("verified = %v, want nil: 'the record said nothing' is not 'a rung that was not verified'",
				*row.Enforcement.Verified)
		}
		if row.Enforcement.Stated() {
			t.Error("Stated() is true for a record carrying no rung")
		}
	})
}

// proxy D14: the credential method and its 0-based ladder index are what make
// degradation queryable, and the proxy emits them under names PLAN §7 does not
// use. Both are read; both land in the same column.
func TestTheCredentialMethodIsReadUnderBothNames(t *testing.T) {
	t.Parallel()

	cases := map[string]map[string]string{
		"the plan's names": {AttrTargetAuthMethod: "ephemeral-key", AttrTargetAuthRung: "2"},
		"the proxy's":      {AttrCredentialMethod: "ephemeral-key", AttrCredentialRung: "2"},
	}
	for name, attrs := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := aRecord()
			rec.Attributes = attrs
			row := mustRow(t, rec)

			if row.TargetAuthMethod != "ephemeral-key" {
				t.Errorf("method = %q, want ephemeral-key", row.TargetAuthMethod)
			}
			if row.TargetAuthRung == nil || *row.TargetAuthRung != 2 {
				t.Fatalf("rung = %v, want 2", row.TargetAuthRung)
			}
		})
	}

	t.Run("rung 0 is not the same fact as no rung", func(t *testing.T) {
		t.Parallel()
		rec := aRecord()
		rec.Attributes = map[string]string{AttrTargetAuthMethod: "ephemeral-key", AttrTargetAuthRung: "0"}
		row := mustRow(t, rec)
		if row.TargetAuthRung == nil || *row.TargetAuthRung != 0 {
			t.Fatalf("rung = %v, want an explicit 0 — the method policy preferred", row.TargetAuthRung)
		}
		if mustRow(t, aRecord()).TargetAuthRung != nil {
			t.Error("a record with no rung produced one")
		}
	})
}

// M16: additional_context is a JSON string OR a JSON object and the store
// admits both rather than coercing either into the other.
func TestGrantContextAdmitsAStringAndAnObject(t *testing.T) {
	t.Parallel()

	t.Run("the string form", func(t *testing.T) {
		t.Parallel()
		rec := aRecord()
		rec.Attributes = map[string]string{
			AttrGrantSystem:     "qualys",
			AttrGrantReference:  "SCAN-2026-09-04-1183",
			AttrGrantAdditional: "authenticated linux scan, requested by soc-oncall",
		}
		row := mustRow(t, rec)

		if row.Grant.AdditionalKind != store.AdditionalContextString {
			t.Fatalf("kind = %q, want %q", row.Grant.AdditionalKind, store.AdditionalContextString)
		}
		var got string
		if err := json.Unmarshal([]byte(row.Grant.Additional), &got); err != nil {
			t.Fatalf("stored value is not a JSON string: %v", err)
		}
		if got != "authenticated linux scan, requested by soc-oncall" {
			t.Errorf("value = %q, want it verbatim", got)
		}
	})

	t.Run("the object form", func(t *testing.T) {
		t.Parallel()
		rec := aRecord()
		rec.Attributes = map[string]string{
			AttrGrantSystem:                        "bmc-helix",
			AttrGrantReference:                     "CHG-1234",
			AttrGrantAdditionalPrefix + "profile":  "authenticated-linux",
			AttrGrantAdditionalPrefix + "approver": "soc-oncall",
		}
		row := mustRow(t, rec)

		if row.Grant.AdditionalKind != store.AdditionalContextObject {
			t.Fatalf("kind = %q, want %q", row.Grant.AdditionalKind, store.AdditionalContextObject)
		}
		var got map[string]string
		if err := json.Unmarshal([]byte(row.Grant.Additional), &got); err != nil {
			t.Fatalf("stored value is not a JSON object: %v", err)
		}
		if got["profile"] != "authenticated-linux" || got["approver"] != "soc-oncall" {
			t.Errorf("value = %v, want both fields kept as fields", got)
		}
	})

	t.Run("no grant context at all", func(t *testing.T) {
		t.Parallel()
		row := mustRow(t, aRecord())
		if row.Grant.Stated() || row.Grant.AdditionalKind != "" {
			t.Errorf("grant = %+v, want nothing", row.Grant)
		}
	})
}

// proxy D13 / PLAN §7: device fields are an open map, stored opaquely, and
// `vdom` is the difference between a confined administrator and a global one
// on the same host.
func TestDeviceFieldsAreLiftedIntoTheirOwnMap(t *testing.T) {
	t.Parallel()

	rec := aRecord()
	rec.Kind = "provisioning"
	rec.Attributes = map[string]string{
		AttrEvent:                      EventAccountMapping,
		AttrDeviceFieldPrefix + "vdom": "global",
		AttrDeviceFieldPrefix + "adom": "root",
		"target_account":               "hl-a7f3c1",
	}
	row := mustRow(t, rec)

	if row.Event != EventAccountMapping {
		t.Errorf("event = %q, want the mapping event to be a column", row.Event)
	}
	if row.DeviceFields["vdom"] != "global" || row.DeviceFields["adom"] != "root" {
		t.Fatalf("device fields = %v, want both kept with the prefix stripped", row.DeviceFields)
	}
	if _, stillThere := row.Attributes[AttrDeviceFieldPrefix+"vdom"]; !stillThere {
		t.Error("lifting a device field removed it from the attribute map; the projection is an index, not a move")
	}
}

// The canonical body is what the chain hashes, so it must be byte-identical
// for the same record every time — including across attribute map iterations,
// which Go deliberately randomises.
func TestTheCanonicalBodyIsDeterministic(t *testing.T) {
	t.Parallel()

	rec := aRecord()
	rec.Attributes = map[string]string{}
	for _, k := range []string{"z", "y", "x", "w", "v", "u", "t", "s"} {
		rec.Attributes[k] = k
	}

	first := mustBody(t, rec)
	for range 50 {
		if got := mustBody(t, rec); got != first {
			t.Fatalf("the canonical body is not stable:\n %s\n %s", first, got)
		}
	}
	if !strings.HasPrefix(first, `{"v":"`+bodyVersion+`"`) {
		t.Errorf("the body does not lead with its encoding version: %s", first)
	}
}

func mustRow(t *testing.T, rec contract.LogRecord) store.AuditRecord {
	t.Helper()
	parsed, err := Parse(rec, testTenant, 0, DefaultLimits)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	row, err := parsed.Row("proxy-1")
	if err != nil {
		t.Fatalf("Row: %v", err)
	}
	return row
}

func mustBody(t *testing.T, rec contract.LogRecord) string {
	t.Helper()
	parsed, err := Parse(rec, testTenant, 0, DefaultLimits)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	body, err := parsed.Body()
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	return string(body)
}
