// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// Kinds is the closed set of record kinds the contract defines, in the order
// the document lists them.
//
// IT IS CLOSED, AND AN UNKNOWN KIND IS REFUSED (400) rather than stored.
// Severity is the opposite — migration 0001 says why it carries no CHECK
// constraint — and the difference is not an inconsistency. A severity is a
// three-value scale a reader can act on without knowing the value; a kind is
// what every query in PLAN §7 filters by, so a kind nobody knows is a record
// nobody finds, filed under a name no dashboard, retention rule or export
// mapping mentions. Refusing it is loud: the proxy keeps the record in its
// disk buffer and an operator sees an error, which is the outcome a silently
// unqueryable record does not produce.
//
// Adding one is therefore an upstream change (M1) and never a local edit.
var Kinds = []string{
	"session_start",
	"session_end",
	"auth",
	"authorize",
	"channel_open",
	"channel_close",
	"command",
	"policy_decision",
	"host_key",
	"stream",
	"provisioning",
	"error",
}

// Severities is the contract's closed severity enum.
var Severities = []contract.LogSeverity{
	contract.SeverityInfo,
	contract.SeverityWarn,
	contract.SeverityCritical,
}

// Attribute keys this server reads. Everything else in the map is carried
// through untouched — the namespace is the producer's, and a store that only
// kept the keys it recognised would lose the field a customer's driver added
// yesterday.
//
// TWO KEYS ARE ACCEPTED UNDER TWO NAMES. `docs/PLAN.md` §7 names the credential
// method's two audit fields `target_auth_method` and `target_auth_rung`, after
// the contract's `target_auth_ladder` they index into; `hoplock/proxy` emits
// them as `credential_method` and `credential_rung`. Both are read and both
// land in the same column, because dropping the ones the proxy actually sends
// would leave the degradation query empty against every real deployment. The
// divergence is named upstream rather than absorbed silently — see the PR's
// `## Upstream request`.
const (
	AttrEvent      = "event"
	AttrDecisionID = "decision_id"

	AttrEnforcementExecution  = "enforcement_execution"
	AttrEnforcementReach      = "enforcement_reach"
	AttrEnforcementVerified   = "enforcement_verified"
	AttrEnforcementAttestedBy = "enforcement_attested_by"

	AttrTargetAuthMethod = "target_auth_method"
	AttrTargetAuthRung   = "target_auth_rung"
	AttrCredentialMethod = "credential_method"
	AttrCredentialRung   = "credential_rung"

	AttrAlgorithmProfile = "algorithm_profile"

	AttrGrantSystem           = "grant_system"
	AttrGrantReference        = "grant_reference"
	AttrGrantWindowStart      = "grant_window_start"
	AttrGrantWindowEnd        = "grant_window_end"
	AttrGrantAdditional       = "grant_additional_context"
	AttrGrantAdditionalPrefix = "grant_additional_context."

	AttrDeviceFieldPrefix = "device_field."
)

// Event names this server treats as first-class classes of record.
//
// Both are `kind: provisioning` on the wire — the kind enum is closed and
// neither has one of its own — so the event name is the only thing that
// distinguishes them, which is why it is a column with an index rather than an
// attributes lookup (migration 0006).
const (
	// EventAccountMapping is the ephemeral-account mapping event (proxy
	// §5.3). On a device whose account-name length forced the readable
	// login segment out of the name, THIS RECORD IS THE ONLY PLACE
	// ATTRIBUTION EXISTS: nothing on the target says who the account
	// belonged to, so losing the event loses the ability to answer who did
	// something on a router.
	EventAccountMapping = "device.account.mapping"
	// EventDeviceConfigChange is the device configuration-change event
	// (proxy §5.3). Every provisioning and teardown is a configuration
	// change on the device and a customer's drift detection will see it;
	// making these queryable and exportable is what lets a SIEM auto-close
	// the resulting alerts — and lets an `hl-*` account Hoplock never
	// reported become a detection.
	//
	// `hoplock/proxy` does not emit this name yet. The schema is here and
	// the gap is named upstream rather than approximated.
	EventDeviceConfigChange = "device.config.change"
)

// Record is one ingested log record, validated and canonicalised.
//
// It is the shape the chain hashes and the shape the store writes. It is built
// only by [Parse], so a record that reached this type has already been bounded,
// classified and redacted.
type Record struct {
	RecordID   string
	SessionID  string
	RecordedAt time.Time
	Kind       string
	Severity   contract.LogSeverity
	Message    string
	Subject    string
	Login      string
	Target     string
	Attributes map[string]string
	// Capture is the decoded session-capture payload, empty for every kind
	// but `stream`.
	Capture []byte
	// Redacted names the attribute keys whose values this server replaced
	// before storing. It is itself part of the record, because "a password
	// arrived and was removed" is an audit fact and a silent removal is a
	// record that lies by omission.
	Redacted []string
}

// Limits bound what one request may carry. They are explicit because an
// unbounded ingest path is a memory-exhaustion surface reachable by any
// enrolled proxy, and because a batch that is too large to answer inside the
// request timeout is a batch the proxy will retry forever.
type Limits struct {
	// MaxBatchRecords is how many records one `/v1/logs/batch` may carry.
	MaxBatchRecords int
	// MaxRecordBytes bounds one record's canonical body, capture excluded.
	MaxRecordBytes int
	// MaxCaptureBytes bounds one `stream` record's decoded payload.
	MaxCaptureBytes int
	// MaxAttributes bounds how many attribute keys one record may carry.
	MaxAttributes int
}

// DefaultLimits are the shipped bounds.
//
// The capture bound is the one with a number behind it rather than a round
// guess: a pty stream is captured as raw chunks, one per read off the wire, so
// a chunk is bounded by the proxy's own read buffer and 1 MiB is two orders of
// magnitude above it. A record above that is not a terminal capture, it is a
// mistake or an attack.
var DefaultLimits = Limits{
	MaxBatchRecords: 1000,
	MaxRecordBytes:  64 << 10,
	MaxCaptureBytes: 1 << 20,
	MaxAttributes:   256,
}

func (l Limits) orDefaults() Limits {
	if l.MaxBatchRecords <= 0 {
		l.MaxBatchRecords = DefaultLimits.MaxBatchRecords
	}
	if l.MaxRecordBytes <= 0 {
		l.MaxRecordBytes = DefaultLimits.MaxRecordBytes
	}
	if l.MaxCaptureBytes <= 0 {
		l.MaxCaptureBytes = DefaultLimits.MaxCaptureBytes
	}
	if l.MaxAttributes <= 0 {
		l.MaxAttributes = DefaultLimits.MaxAttributes
	}
	return l
}

// MalformedError is a record this server refuses. It is a 400 for the request
// that carried it and is never a 401 (M11): a malformed record is a caller
// mistake, not a denial.
type MalformedError struct {
	// Index is the record's position in the batch, or -1 for a single
	// record. A proxy shipping a thousand records needs to be told which.
	Index    int
	RecordID string
	Reason   string
}

func (e *MalformedError) Error() string {
	where := "the record"
	if e.Index >= 0 {
		where = fmt.Sprintf("record %d", e.Index)
	}
	if e.RecordID != "" {
		where += " (" + e.RecordID + ")"
	}
	return where + " is malformed: " + e.Reason
}

// TenantMismatchError is a record whose body names a tenant other than the one
// its proxy is enrolled in (M18).
//
// It is its own type, and the acceptance criterion says why: it must be
// DISTINGUISHABLE from an ingest failure. Filing such a record under the
// enrolled tenant would hide a misconfiguration that matters — two customers'
// records in one chain — and answering 5xx would tell an operator their
// database is broken when their proxy is misconfigured.
type TenantMismatchError struct {
	RecordID string
	Claimed  string
	Enrolled string
}

func (e *TenantMismatchError) Error() string {
	return fmt.Sprintf(
		"record %s names tenant %q and was submitted by a proxy enrolled in %q; a proxy does not assert its own tenancy",
		e.RecordID, e.Claimed, e.Enrolled)
}

// AttrTenant is the attribute a record would name a tenant in. The contract
// carries no tenant field anywhere (M18) — a proxy asserting its own tenancy
// would be a caller asserting its own authority — so this key exists only to
// be REFUSED.
const AttrTenant = "tenant"

// Parse validates one contract record and turns it into a Record.
//
// `enrolled` is the tenant the submitting proxy belongs to, resolved from its
// credential and never from the record.
func Parse(rec contract.LogRecord, enrolled string, index int, limits Limits) (Record, error) {
	limits = limits.orDefaults()

	bad := func(format string, args ...any) error {
		return &MalformedError{Index: index, RecordID: rec.RecordID, Reason: fmt.Sprintf(format, args...)}
	}

	switch {
	case strings.TrimSpace(rec.RecordID) == "":
		return Record{}, bad("record_id is required")
	case strings.TrimSpace(rec.SessionID) == "" && rec.Kind != "error":
		// An `error` record can belong to no session — a sweep failure
		// on a device belongs to the fleet rather than to anybody's
		// connection — and refusing it would lose the one record class
		// that reports a failure nobody was present for.
		return Record{}, bad("session_id is required for a %s record", rec.Kind)
	case rec.Kind == "":
		return Record{}, bad("kind is required")
	case !slices.Contains(Kinds, rec.Kind):
		return Record{}, bad("%q is not a record kind this contract defines (%s)", rec.Kind, strings.Join(Kinds, ", "))
	case !slices.Contains(Severities, rec.Severity):
		return Record{}, bad("%q is not a severity this contract defines", rec.Severity)
	case len(rec.Attributes) > limits.MaxAttributes:
		return Record{}, bad("carries %d attributes, above the %d this server accepts", len(rec.Attributes), limits.MaxAttributes)
	}

	at, err := time.Parse(time.RFC3339, rec.Timestamp)
	if err != nil {
		return Record{}, bad("timestamp %q is not RFC 3339", rec.Timestamp)
	}

	if claimed, ok := rec.Attributes[AttrTenant]; ok && claimed != "" && claimed != enrolled {
		return Record{}, &TenantMismatchError{RecordID: rec.RecordID, Claimed: claimed, Enrolled: enrolled}
	}

	out := Record{
		RecordID:   rec.RecordID,
		SessionID:  rec.SessionID,
		RecordedAt: at.UTC(),
		Kind:       rec.Kind,
		Severity:   rec.Severity,
		Message:    rec.Message,
		Subject:    rec.Subject,
		Login:      rec.Login,
		Target:     rec.Target,
		Attributes: map[string]string{},
	}
	for k, v := range rec.Attributes {
		out.Attributes[k] = v
	}

	if rec.Payload != "" {
		if rec.Kind != "stream" {
			return Record{}, bad("carries a payload but is a %s record; payload is for stream capture only", rec.Kind)
		}
		raw, err := base64.StdEncoding.DecodeString(rec.Payload)
		if err != nil {
			return Record{}, bad("payload is not base64")
		}
		if len(raw) > limits.MaxCaptureBytes {
			return Record{}, bad("capture is %d bytes, above the %d this server accepts", len(raw), limits.MaxCaptureBytes)
		}
		out.Capture = raw
	}

	out.Redacted = Redact(&out)

	body, err := out.Body()
	if err != nil {
		return Record{}, bad("cannot be encoded: %v", err)
	}
	if len(body) > limits.MaxRecordBytes {
		return Record{}, bad("is %d bytes, above the %d this server accepts", len(body), limits.MaxRecordBytes)
	}
	return out, nil
}

// canonical is the record's wire form for hashing and storage.
//
// It is a struct rather than a map so the field order is fixed by the source
// rather than by whatever a map iterator does, and every value in it is either
// a string or a sorted map of strings, so encoding is deterministic.
// `additional_context` is the one exception and is handled below.
type canonical struct {
	Version    string            `json:"v"`
	RecordID   string            `json:"record_id"`
	SessionID  string            `json:"session_id,omitempty"`
	Timestamp  string            `json:"timestamp"`
	Kind       string            `json:"kind"`
	Severity   string            `json:"severity"`
	Message    string            `json:"message,omitempty"`
	Subject    string            `json:"subject,omitempty"`
	Login      string            `json:"login,omitempty"`
	Target     string            `json:"target,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Redacted   []string          `json:"redacted,omitempty"`
	// CaptureBytes and CaptureSHA256 stand in for the capture itself. The
	// bytes live in their own table (migration 0006) and the chain covers
	// them through this digest: altering a capture is detectable without
	// the chain ever carrying a megabyte of terminal output.
	CaptureBytes  int    `json:"capture_bytes,omitempty"`
	CaptureSHA256 string `json:"capture_sha256,omitempty"`
}

// bodyVersion prefixes every canonical body.
//
// It is inside the hashed bytes on purpose: a later revision that changes the
// encoding must not be able to produce the same hash for a different reading of
// the same record, and a verifier that meets a version it does not know must
// say so rather than report a break.
const bodyVersion = "hoplock.audit/1"

// Body renders the record's canonical bytes — what the chain hashes and what
// the store keeps.
func (r Record) Body() ([]byte, error) {
	c := canonical{
		Version:    bodyVersion,
		RecordID:   r.RecordID,
		SessionID:  r.SessionID,
		Timestamp:  r.RecordedAt.UTC().Format(time.RFC3339Nano),
		Kind:       r.Kind,
		Severity:   string(r.Severity),
		Message:    r.Message,
		Subject:    r.Subject,
		Login:      r.Login,
		Target:     r.Target,
		Attributes: r.Attributes,
		Redacted:   r.Redacted,
	}
	if len(r.Capture) > 0 {
		c.CaptureBytes = len(r.Capture)
		c.CaptureSHA256 = digest(r.Capture)
	}
	// encoding/json emits struct fields in declaration order and map keys
	// in sorted order, which is the whole of the determinism this needs.
	return json.Marshal(c)
}

// Row projects a record onto the store row, deriving every indexed column.
//
// The body is authoritative and the columns are how it is found (migration
// 0006). Nothing here may invent a value the body does not carry: a column
// filled from a guess is a query answering confidently about a fact nobody
// recorded.
func (r Record) Row(proxyID string) (store.AuditRecord, error) {
	body, err := r.Body()
	if err != nil {
		return store.AuditRecord{}, err
	}

	row := store.AuditRecord{
		RecordID:     r.RecordID,
		SessionID:    r.SessionID,
		Kind:         r.Kind,
		Severity:     string(r.Severity),
		Body:         string(body),
		Subject:      r.Subject,
		Login:        r.Login,
		Target:       r.Target,
		Message:      r.Message,
		Event:        r.Attributes[AttrEvent],
		DecisionID:   r.Attributes[AttrDecisionID],
		ProxyID:      proxyID,
		Attributes:   r.Attributes,
		DeviceFields: prefixMap(r.Attributes, AttrDeviceFieldPrefix),
		RecordedAt:   r.RecordedAt,
	}

	row.Enforcement = store.EnforcementFacts{
		Execution:  r.Attributes[AttrEnforcementExecution],
		Reach:      r.Attributes[AttrEnforcementReach],
		AttestedBy: r.Attributes[AttrEnforcementAttestedBy],
	}
	if v, ok := r.Attributes[AttrEnforcementVerified]; ok {
		// Only a value this server can read becomes a claim. An
		// unparseable one leaves the column NULL — "the record said
		// nothing" — rather than defaulting to false, which would
		// report an applied rung as unverified, or to true, which is
		// the failure this field exists to prevent.
		if b, err := strconv.ParseBool(v); err == nil {
			row.Enforcement.Verified = &b
		}
	}

	row.TargetAuthMethod = firstOf(r.Attributes, AttrTargetAuthMethod, AttrCredentialMethod)
	if v := firstOf(r.Attributes, AttrTargetAuthRung, AttrCredentialRung); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n >= 0 {
			rung := int32(n)
			row.TargetAuthRung = &rung
		}
	}
	row.AlgorithmProfile = r.Attributes[AttrAlgorithmProfile]

	row.Grant = store.GrantContextFacts{
		System:    r.Attributes[AttrGrantSystem],
		Reference: r.Attributes[AttrGrantReference],
	}
	if t, ok := parseTime(r.Attributes[AttrGrantWindowStart]); ok {
		row.Grant.WindowStart = &t
	}
	if t, ok := parseTime(r.Attributes[AttrGrantWindowEnd]); ok {
		row.Grant.WindowEnd = &t
	}
	row.Grant.AdditionalKind, row.Grant.Additional = additionalContext(r.Attributes)

	if len(r.Capture) > 0 {
		row.CaptureBytes = int32(len(r.Capture))
		row.CaptureSHA256 = digest(r.Capture)
	}
	return row, nil
}

// additionalContext resolves `additional_context` out of the attribute map,
// admitting a string AND an object without coercing either into the other.
//
// The contract says the value is a JSON string or a JSON object and that
// anything else is a violation rather than something to coerce, because the
// proxy stores it verbatim for an auditor. The two arrive differently:
//
//   - the STRING form is one attribute, `grant_additional_context`;
//   - the OBJECT form is one attribute per field under
//     `grant_additional_context.`, which is the device-field pattern and is
//     there for the same reason — an auditor asks which sessions a change
//     ticket authorised, and a whole object flattened into one string turns
//     that question into a substring search.
//
// So the object is reassembled here, its field values kept exactly as they
// arrived, and the kind recorded beside it. A record carrying both forms keeps
// the object and records the string as one of its fields under the empty key,
// because dropping either would be this server choosing what an integration
// meant.
func additionalContext(attrs map[string]string) (kind, value string) {
	fields := prefixMap(attrs, AttrGrantAdditionalPrefix)
	text, hasText := attrs[AttrGrantAdditional]

	switch {
	case len(fields) == 0 && !hasText:
		return "", ""
	case len(fields) == 0:
		encoded, err := json.Marshal(text)
		if err != nil {
			return "", ""
		}
		return store.AdditionalContextString, string(encoded)
	default:
		if hasText {
			fields[""] = text
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			return "", ""
		}
		return store.AdditionalContextObject, string(encoded)
	}
}

// prefixMap lifts one namespace out of the attribute map, with the prefix
// stripped. It returns nil rather than an empty map when nothing matches, so
// "no device fields" and "device fields this server dropped" stay distinct.
func prefixMap(attrs map[string]string, prefix string) map[string]string {
	var out map[string]string
	for k, v := range attrs {
		if !strings.HasPrefix(k, prefix) || len(k) == len(prefix) {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[strings.TrimPrefix(k, prefix)] = v
	}
	return out
}

func firstOf(attrs map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := attrs[k]; v != "" {
			return v
		}
	}
	return ""
}

func parseTime(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}
