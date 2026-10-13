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

// `session_id: ""` means the record belongs to NO session, and the contract
// requires a server to accept it on every kind (Hoplock/proxy#71). Today's
// proxy sends three such records — an orphan sweep's device.config.change, the
// sweep's own failure, and a logging.gap over session-less records — and a
// refusal no longer costs latency: the proxy sets the record aside, so it is
// lost to this store.
func TestARecordOfEveryKindMayHaveNoSession(t *testing.T) {
	t.Parallel()
	for _, kind := range Kinds {
		rec := aRecord()
		rec.Kind = kind
		rec.SessionID = ""
		if kind == "stream" {
			rec.Payload = base64.StdEncoding.EncodeToString([]byte("$ "))
		}
		got, err := Parse(rec, testTenant, 0, DefaultLimits)
		if err != nil {
			t.Fatalf("a session-less %s record was refused: %v", kind, err)
		}
		if got.SessionID != "" {
			t.Errorf("a session-less %s record was given session %q", kind, got.SessionID)
		}
	}

	// The sweep failure by name: policy_decision, critical, priority path,
	// and no subject or login. It is the record the old rule refused.
	sweep := aRecord()
	sweep.Kind, sweep.Severity, sweep.SessionID = "policy_decision", contract.SeverityCritical, ""
	sweep.Subject, sweep.Login = "", ""
	sweep.Attributes = map[string]string{AttrEvent: EventSweepFailed, AttrPlatform: "fortios"}
	if _, err := Parse(sweep, testTenant, -1, DefaultLimits); err != nil {
		t.Fatalf("the sweep failure was refused: %v", err)
	}

	// A kind outside the list is still refused, session or not.
	bad := aRecord()
	bad.Kind, bad.SessionID = "exfiltration", ""
	var malformed *MalformedError
	if _, err := Parse(bad, testTenant, 0, DefaultLimits); !errors.As(err, &malformed) {
		t.Fatalf("a session-less record of an unknown kind = %v, want MalformedError", err)
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

// proxy D14: the credential method and its rung are what make degradation
// queryable. One name per field, the proxy's, and the rung counts from 1 exactly
// as it arrived (Hoplock/proxy#66).
func TestTheCredentialIsReadUnderTheProxysNamesOnly(t *testing.T) {
	t.Parallel()

	rec := aRecord()
	rec.Attributes = map[string]string{AttrCredentialMethod: "brokered-key", AttrCredentialRung: "2"}
	row := mustRow(t, rec)
	if row.CredentialMethod != "brokered-key" {
		t.Errorf("method = %q, want brokered-key", row.CredentialMethod)
	}
	if row.CredentialRung == nil || *row.CredentialRung != 2 {
		t.Fatalf("rung = %v, want 2 — kept as it arrived, counting from 1", row.CredentialRung)
	}

	// The retired 0-based pair projects nothing.
	old := aRecord()
	old.Attributes = map[string]string{"target_auth_method": "brokered-key", "target_auth_rung": "1"}
	if got := mustRow(t, old); got.CredentialRung != nil || got.CredentialMethod != "" {
		t.Errorf("a record carrying only target_auth_* projected method %q rung %v", got.CredentialMethod, got.CredentialRung)
	}
	if mustRow(t, aRecord()).CredentialRung != nil {
		t.Error("a record with no rung produced one")
	}
}

func TestTheCertificateSerialIsParsedAsAnUnsignedInteger(t *testing.T) {
	t.Parallel()
	rec := aRecord()
	rec.Attributes = map[string]string{AttrCertificateSerial: "18446744073709551615"}
	if got := mustRow(t, rec).CertificateSerial; got != "18446744073709551615" {
		t.Errorf("serial = %q, want the uint64 maximum, exactly", got)
	}
	rec.Attributes = map[string]string{AttrCertificateSerial: "1e3"}
	if got := mustRow(t, rec).CertificateSerial; got != "" {
		t.Errorf("an unparseable serial projected %q", got)
	}
}

// A gap record is never refused over its gap keys: one that is absent or does
// not parse projects nothing, and the record is stored as it arrived
// (Hoplock/proxy#71).
func TestAGapRecordIsReadLeniently(t *testing.T) {
	t.Parallel()
	rec := aRecord()
	rec.Kind, rec.Severity, rec.SessionID = "error", contract.SeverityWarn, ""
	rec.Attributes = map[string]string{
		AttrEvent: EventLoggingGap, AttrGapCause: "evicted",
		AttrGapRecords: "lots", AttrGapFirstAt: "yesterday", AttrGapKinds: "command,stream",
	}
	row := mustRow(t, rec)
	if row.Gap.Cause != "evicted" || row.Gap.Records != nil || row.Gap.FirstAt != nil || row.Gap.LastAt != nil {
		t.Errorf("gap = %+v, want the cause and nothing from the keys that do not parse", row.Gap)
	}
	if len(row.Gap.Kinds) != 2 {
		t.Errorf("kinds = %v", row.Gap.Kinds)
	}
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
