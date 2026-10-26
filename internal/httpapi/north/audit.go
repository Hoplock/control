// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// The audit query and retention (0010, 0019).
//
// EVERY FIELD IS NAMED AS THE PROXY NAMES IT — `credential_method`,
// `credential_rung`, `target_kex_algorithm` — because from this phase the
// north-bound names are a compatibility promise (M19). A record is rendered
// with its chain position and hash, deliberately: a reader holding the chain can
// check a record against it without asking this server to vouch for it.
//
// The record-by-id route is what the conformance suite's durability assertion
// reads (`logs.read_url`), which is why it serves a record BY ID: the suite
// substitutes the id it just ingested, and a listing that happened to mention it
// would prove less.

type recordView struct {
	RecordID   string            `json:"record_id"`
	Stream     string            `json:"stream"`
	ChainSeq   int64             `json:"chain_seq"`
	PrevHash   string            `json:"prev_hash,omitempty"`
	Hash       string            `json:"hash"`
	SessionID  string            `json:"session_id"`
	Kind       string            `json:"kind"`
	Severity   string            `json:"severity"`
	Subject    string            `json:"subject,omitempty"`
	Login      string            `json:"login,omitempty"`
	Target     string            `json:"target,omitempty"`
	Message    string            `json:"message,omitempty"`
	Event      string            `json:"event,omitempty"`
	DecisionID string            `json:"decision_id,omitempty"`
	ProxyID    string            `json:"proxy_id,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	// The projections a query filters on, under the proxy's names.
	CredentialMethod  string    `json:"credential_method,omitempty"`
	CredentialRung    *int32    `json:"credential_rung,omitempty"`
	CertificateSerial string    `json:"credential_certificate_serial,omitempty"`
	AlgorithmProfile  string    `json:"algorithm_profile,omitempty"`
	AlgorithmFloor    string    `json:"algorithm_floor,omitempty"`
	AlgorithmBans     []string  `json:"algorithm_bans,omitempty"`
	Negotiated        []string  `json:"negotiated,omitempty"`
	RecordedAt        time.Time `json:"recorded_at"`
	ReceivedAt        time.Time `json:"received_at"`
	// Body is the canonical bytes the hash covers, so a reader can
	// recompute the link themselves.
	Body string `json:"body"`
}

func viewRecord(row store.AuditRecord) recordView {
	return recordView{
		RecordID: row.RecordID, Stream: row.Stream, ChainSeq: row.ChainSeq, PrevHash: row.PrevHash, Hash: row.Hash,
		SessionID: row.SessionID, Kind: row.Kind, Severity: row.Severity, Subject: row.Subject, Login: row.Login,
		Target: row.Target, Message: row.Message, Event: row.Event, DecisionID: row.DecisionID, ProxyID: row.ProxyID,
		Attributes: row.Attributes, CredentialMethod: row.CredentialMethod, CredentialRung: row.CredentialRung,
		CertificateSerial: row.CertificateSerial, AlgorithmProfile: row.AlgorithmProfile,
		AlgorithmFloor: row.AlgorithmFloor, AlgorithmBans: row.AlgorithmBans, Negotiated: row.Negotiated,
		RecordedAt: row.RecordedAt.UTC(), ReceivedAt: row.ReceivedAt.UTC(), Body: row.Body,
	}
}

func viewRecords(rows []store.AuditRecord) []recordView {
	out := make([]recordView, 0, len(rows))
	for _, row := range rows {
		out = append(out, viewRecord(row))
	}
	return out
}

// THE AUDIT LISTS ARE PAGED (M26, §7). Six of them read one page at a time,
// by recorded-at, stream and chain sequence, newest first unless
// `?order=asc`: five over the audit query — the general query, the drift
// feed, the unmet algorithm policy, the gaps and the certificate join — and
// the showcase join, by its record's place. Each builds the selection its
// reader applies, and its cursor binds that selection rendered by auditFilters
// (blockedFilters for the join), so a filter the reader reads is one the
// cursor binds. A window narrows a page and is never its position: many
// records share an instant.
//
// The negotiated sessions are the one list in another order: each session's
// latest matching record, newest first, then its id. That is an aggregate no
// index serves, so each page re-reads its selection (M26.4).

// auditKey is every audit list's order key: recorded-at, stream and chain
// sequence (M26.4), the last unique within a stream.
var auditKey = []keyKind{keyTime, keyString, keyInt}

var (
	auditRecordList       = list{name: "audit_records", key: auditKey}
	driftList             = list{name: "audit_drift", key: auditKey}
	unmetList             = list{name: "audit_unmet", key: auditKey}
	gapList               = list{name: "audit_gaps", key: auditKey}
	certificateRecordList = list{name: "certificate_records", key: auditKey}
	blockedList           = list{name: "audit_blocked_commands", key: auditKey}
	negotiatedList        = list{name: "audit_negotiated", key: []keyKind{keyTime, keyString}}
)

// auditPosition is the position after the last of rows, which at reads as its
// record's instant, stream and chain sequence; nil for no rows.
func auditPosition[T any](rows []T, at func(T) (time.Time, string, int64)) []any {
	if len(rows) == 0 {
		return nil
	}
	recorded, stream, seq := at(rows[len(rows)-1])
	return []any{recorded, stream, seq}
}

// auditFilters is the selection an audit list's cursor binds (M26.2): every
// field of q the reader applies, canonical — kinds and severities sorted and
// once each, instants in RFC 3339 UTC — and the order, which every audit list
// offers both ways. A field added to the query is added here, or
// TestEverySelectionFieldBindsTheAuditCursor fails.
func auditFilters(q store.AuditQuery) []string {
	var out []string
	add := func(name, value string) {
		if value != "" {
			out = append(out, name+"="+value)
		}
	}
	flag := func(name string, on bool) {
		if on {
			out = append(out, name+"=true")
		}
	}
	instant := func(name string, t time.Time) {
		if !t.IsZero() {
			out = append(out, name+"="+t.UTC().Format(time.RFC3339Nano))
		}
	}
	each := func(name string, values []string) {
		for _, v := range slices.Compact(slices.Sorted(slices.Values(values))) {
			out = append(out, name+"="+v)
		}
	}
	add("session", q.SessionID)
	add("subject", q.Subject)
	add("login", q.Login)
	add("target", q.Target)
	add("decision", q.DecisionID)
	add("proxy", q.ProxyID)
	each("kind", q.Kinds)
	each("severity", q.Severities)
	add("event", q.Event)
	add("grant_system", q.GrantSystem)
	add("grant_reference", q.GrantReference)
	if q.DeviceFieldName != "" {
		out = append(out, "device_field="+q.DeviceFieldName+"="+q.DeviceFieldValue)
	}
	flag("no_session", q.NoSession)
	flag("weakened", q.Weakened)
	flag("degraded", q.Degraded)
	add("credential_certificate_serial", q.CertificateSerial)
	flag("has_certificate", q.HasCertificate)
	flag("has_algorithms", q.HasAlgorithms)
	add("algorithm_floor", q.AlgorithmFloor)
	add("banned", q.Banned)
	add("negotiated", q.Negotiated)
	add("device_change_op", q.DeviceChangeOp)
	add("target_account", q.TargetAccount)
	if q.DeviceObjectKind != nil {
		out = append(out, "device_object_kind="+*q.DeviceObjectKind)
	}
	flag("gaps", q.Gaps)
	add("gap_cause", q.GapCause)
	add("gap_kind", q.GapKind)
	add("gap_record_id", q.GapRecordID)
	flag("gap_critical", q.GapCritical)
	instant("span_from", q.SpanFrom)
	instant("span_to", q.SpanTo)
	instant("from", q.From)
	instant("to", q.To)
	if q.Ascending {
		out = append(out, "order=asc")
	} else {
		out = append(out, "order=desc")
	}
	return out
}

// blockedFilters is the selection the showcase join's cursor binds (M26.2):
// every field of q its reader applies, instants in RFC 3339 UTC, and the
// order. The label's value is bound even when empty, because an empty value
// is one the join matches. A field added to the query is added here, or
// TestEveryShowcaseFieldBindsItsCursor fails.
func blockedFilters(q store.BlockedCommandQuery) []string {
	out := []string{"label=" + q.LabelName, "value=" + q.LabelValue, "order=desc"}
	if q.Ascending {
		out[2] = "order=asc"
	}
	if !q.From.IsZero() {
		out = append(out, "from="+q.From.UTC().Format(time.RFC3339Nano))
	}
	if !q.To.IsZero() {
		out = append(out, "to="+q.To.UTC().Format(time.RFC3339Nano))
	}
	return out
}

// ascending reads `?order=`: `asc` is oldest first, and anything else the
// newest first every audit list defaults to.
func ascending(r *http.Request) bool { return r.URL.Query().Get("order") == "asc" }

// window reads `?from=` and `?to=`, RFC 3339, on recorded-at: from inclusive,
// to exclusive, each absent when zero.
func (s *Server) window(w http.ResponseWriter, r *http.Request) (from, to time.Time, ok bool) {
	if from, ok = s.timeParam(w, r, "from"); !ok {
		return time.Time{}, time.Time{}, false
	}
	if to, ok = s.timeParam(w, r, "to"); !ok {
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func boolParam(r *http.Request, name string) bool {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	return v == "true" || v == "1"
}

// handleAuditRecords is the general query over every dimension PLAN §7 names
// and every projection the proxy's records carry.
func (s *Server) handleAuditRecords(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	from, to, ok := s.window(w, r)
	if !ok {
		return
	}
	v := r.URL.Query()
	q := store.AuditQuery{
		SessionID: v.Get("session"), NoSession: boolParam(r, "no_session"),
		Subject: v.Get("subject"), Login: v.Get("login"), Target: v.Get("target"),
		DecisionID: v.Get("decision"), ProxyID: v.Get("proxy"),
		Kinds: v["kind"], Severities: v["severity"], Event: v.Get("event"),
		GrantSystem: v.Get("grant_system"), GrantReference: v.Get("grant_reference"),
		Weakened: boolParam(r, "weakened"), Degraded: boolParam(r, "degraded"),
		CertificateSerial: v.Get("credential_certificate_serial"),
		AlgorithmFloor:    v.Get("algorithm_floor"), Banned: v.Get("banned"), Negotiated: v.Get("negotiated"),
		DeviceChangeOp: v.Get("device_change_op"), TargetAccount: v.Get("target_account"),
		Gaps: boolParam(r, "gaps"), GapCause: v.Get("gap_cause"), GapKind: v.Get("gap_kind"),
		GapRecordID: v.Get("gap_record_id"), GapCritical: boolParam(r, "gap_critical"),
		From: from, To: to, Ascending: ascending(r),
	}
	if q.SessionID != "" && q.NoSession {
		s.invalid(w, r, "no_session", "conflicts_with_session", "")
		return
	}
	if v.Has("device_object_kind") {
		kind := v.Get("device_object_kind")
		if kind == audit.DriftAdministrator {
			kind = ""
		}
		q.DeviceObjectKind = &kind
	}
	if raw := v.Get("device_field"); raw != "" {
		name, value, found := strings.Cut(raw, "=")
		if !found || name == "" {
			s.invalid(w, r, "device_field", access.ProblemInvalid, "")
			return
		}
		q.DeviceFieldName, q.DeviceFieldValue = name, value
	}
	if q.CertificateSerial != "" {
		serial, err := contract.ParseSerial(q.CertificateSerial)
		if err != nil {
			s.invalid(w, r, "credential_certificate_serial", access.ProblemInvalid, "")
			return
		}
		q.CertificateSerial = contract.FormatSerial(serial)
	}
	var okSpan bool
	if q.SpanFrom, okSpan = s.timeParam(w, r, "span_from"); !okSpan {
		return
	}
	if q.SpanTo, okSpan = s.timeParam(w, r, "span_to"); !okSpan {
		return
	}
	p, ok := readPage(w, r, auditRecordList, auditFilters(q)...)
	if !ok {
		return
	}
	page, err := s.reader.Find(r.Context(), tenant, q, p.Page)
	if err != nil {
		s.auditFailure(w, r, "query audit", err)
		return
	}
	s.answerPage(w, r, p, "records", viewRecords(page.Rows), page.More, auditPosition(page.Rows, recordPosition), nil)
}

// recordPosition, driftPosition, unmetPosition and gapPosition are each list's
// item's place in the audit order: its record's. A gap's instant is its
// report's, when the proxy wrote it.
func recordPosition(row store.AuditRecord) (time.Time, string, int64) {
	return row.RecordedAt, row.Stream, row.ChainSeq
}

func driftPosition(c audit.DriftChange) (time.Time, string, int64) {
	return c.RecordedAt, c.Stream, c.ChainSeq
}

func unmetPosition(u audit.UnmetAlgorithmPolicy) (time.Time, string, int64) {
	return u.RecordedAt, u.Stream, u.ChainSeq
}

func gapPosition(g audit.Gap) (time.Time, string, int64) { return g.ReportedAt, g.Stream, g.ChainSeq }

func (s *Server) handleAuditRecord(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	row, err := s.reader.Get(r.Context(), tenant, r.PathValue("record"))
	if err != nil {
		s.auditFailure(w, r, "read audit record", err)
		return
	}
	answer(w, http.StatusOK, viewRecord(row))
}

// handleBlockedCommands is the showcase join: blocked commands on targets
// carrying a label, each with the access decision that permitted the session,
// one page at a time by its record's place, newest first unless `?order=asc`.
func (s *Server) handleBlockedCommands(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	from, to, ok := s.window(w, r)
	if !ok {
		return
	}
	label, value, found := strings.Cut(r.URL.Query().Get("label"), "=")
	if !found || label == "" {
		s.invalid(w, r, "label", access.ProblemRequired, "")
		return
	}
	q := store.BlockedCommandQuery{LabelName: label, LabelValue: value, From: from, To: to, Ascending: ascending(r)}
	p, ok := readPage(w, r, blockedList, blockedFilters(q)...)
	if !ok {
		return
	}
	page, err := s.reader.BlockedCommands(r.Context(), tenant, q, p.Page)
	if err != nil {
		s.auditFailure(w, r, "blocked commands", err)
		return
	}
	s.answerPage(w, r, p, "blocked", page.Rows, page.More, auditPosition(page.Rows, blockedPosition), nil)
}

// blockedPosition is a blocked command's place in the audit order: its
// record's, which the item carries.
func blockedPosition(b store.BlockedCommand) (time.Time, string, int64) {
	return b.Record.RecordedAt, b.Record.Stream, b.Record.ChainSeq
}

// handleDrift is the drift feed: one record per change the proxy made on a
// device, filtered by device, object and operation.
func (s *Server) handleDrift(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	from, to, ok := s.window(w, r)
	if !ok {
		return
	}
	v := r.URL.Query()
	q := audit.DriftQuery{
		Device: v.Get("device"), Account: v.Get("account"), ObjectKind: v.Get("object_kind"), Op: v.Get("op"),
		From: from, To: to, Ascending: ascending(r),
	}
	p, ok := readPage(w, r, driftList, auditFilters(q.Selection())...)
	if !ok {
		return
	}
	page, err := s.reader.DeviceConfigChanges(r.Context(), tenant, q, p.Page)
	if err != nil {
		s.auditFailure(w, r, "drift feed", err)
		return
	}
	s.answerPage(w, r, p, "changes", page.Rows, page.More, auditPosition(page.Rows, driftPosition), nil)
}

// handleAlgorithmPolicyUnmet lists the targets that reported an unmet algorithm
// policy: the axis, the cause, what they offered, the policy and when.
func (s *Server) handleAlgorithmPolicyUnmet(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	from, to, ok := s.window(w, r)
	if !ok {
		return
	}
	q := audit.UnmetQuery{Target: r.URL.Query().Get("target"), From: from, To: to, Ascending: ascending(r)}
	p, ok := readPage(w, r, unmetList, auditFilters(q.Selection())...)
	if !ok {
		return
	}
	page, err := s.reader.AlgorithmPolicyUnmet(r.Context(), tenant, q, p.Page)
	if err != nil {
		s.auditFailure(w, r, "unmet algorithm policy", err)
		return
	}
	s.answerPage(w, r, p, "unmet", page.Rows, page.More, auditPosition(page.Rows, unmetPosition), nil)
}

// handleNegotiated answers "which sessions negotiated this", "which ran under
// this floor" and "which ran under a ban on this" — the runbook's third step
// reads the first to its last page and hands the answer to session_kill.
func (s *Server) handleNegotiated(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	from, to, ok := s.window(w, r)
	if !ok {
		return
	}
	v := r.URL.Query()
	q := audit.NegotiatedQuery{
		Axis: v.Get("axis"), Value: v.Get("value"), Floor: v.Get("floor"),
		BanAxis: v.Get("ban_axis"), BanName: v.Get("ban_name"),
		From: from, To: to,
	}
	set := 0
	for _, on := range []bool{q.Axis != "" || q.Value != "", q.Floor != "", q.BanAxis != "" || q.BanName != ""} {
		if on {
			set++
		}
	}
	if set != 1 {
		s.invalid(w, r, "query", "exactly_one_of_axis_value_floor_or_ban", "")
		return
	}
	// Each question is asked whole: an axis needs its value, and a banned
	// axis its name.
	for _, pair := range [][2]string{{"axis", "value"}, {"ban_axis", "ban_name"}} {
		if (v.Get(pair[0]) == "") != (v.Get(pair[1]) == "") {
			missing := pair[0]
			if v.Get(pair[1]) == "" {
				missing = pair[1]
			}
			s.invalid(w, r, missing, access.ProblemRequired, "")
			return
		}
	}
	sel, _ := q.Selection()
	p, ok := readPage(w, r, negotiatedList, auditFilters(sel)...)
	if !ok {
		return
	}
	page, err := s.reader.SessionsBy(r.Context(), tenant, q, p.Page)
	if err != nil {
		s.auditFailure(w, r, "negotiated sessions", err)
		return
	}
	// truncated says the page is not the whole answer: the runbook's next
	// step ends exactly the sessions listed, so the caller follows next
	// until truncated is false before ending them.
	var last []any
	if n := len(page.Rows); n > 0 {
		last = []any{page.Rows[n-1].LatestAt, page.Rows[n-1].SessionID}
	}
	s.answerPage(w, r, p, "sessions", page.Rows, page.More, last, nil)
}

// handleGaps finds what a proxy could not deliver (Hoplock/proxy#71).
func (s *Server) handleGaps(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	v := r.URL.Query()
	q := audit.GapQuery{
		SessionID: v.Get("session"), NoSession: boolParam(r, "no_session"), ProxyID: v.Get("proxy"),
		Cause: v.Get("cause"), RecordID: v.Get("record_id"), Kind: v.Get("kind"), Critical: boolParam(r, "critical"),
		Ascending: ascending(r),
	}
	var okSpan bool
	if q.SpanFrom, okSpan = s.timeParam(w, r, "span_from"); !okSpan {
		return
	}
	if q.SpanTo, okSpan = s.timeParam(w, r, "span_to"); !okSpan {
		return
	}
	p, ok := readPage(w, r, gapList, auditFilters(q.Selection())...)
	if !ok {
		return
	}
	page, err := s.reader.Gaps(r.Context(), tenant, q, p.Page)
	if err != nil {
		s.auditFailure(w, r, "gaps", err)
		return
	}
	s.answerPage(w, r, p, "gaps", page.Rows, page.More, auditPosition(page.Rows, gapPosition), nil)
}

// handleSessionLoss sums one session's gap reports per cause: two reports of
// one cause are both counted, never the latest alone.
func (s *Server) handleSessionLoss(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	loss, err := s.reader.SessionLoss(r.Context(), tenant, r.PathValue("session"))
	if err != nil {
		s.auditFailure(w, r, "session loss", err)
		return
	}
	if loss == nil {
		loss = []store.GapLoss{}
	}
	answer(w, http.StatusOK, map[string]any{"session_id": r.PathValue("session"), "loss": loss})
}

func (s *Server) handleSessionAlgorithms(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	algs, err := s.reader.Algorithms(r.Context(), tenant, r.PathValue("session"))
	if err != nil {
		s.auditFailure(w, r, "session algorithms", err)
		return
	}
	answer(w, http.StatusOK, algs)
}

// presentedView is one certificate a session presented: the serial its record
// names, and the row it joins to — absent when it names one this server never
// minted, which is itself the finding.
type presentedView struct {
	SessionID   string           `json:"session_id"`
	RecordID    string           `json:"record_id"`
	Serial      string           `json:"credential_certificate_serial"`
	Found       bool             `json:"found"`
	Certificate *certificateView `json:"certificate,omitempty"`
}

// handleSessionCertificates joins a session's provisioning records to the
// certificate rows their serials name.
func (s *Server) handleSessionCertificates(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	certs, truncated, err := s.reader.SessionCertificates(r.Context(), tenant, r.PathValue("session"))
	if err != nil {
		s.auditFailure(w, r, "session certificates", err)
		return
	}
	out := make([]presentedView, 0, len(certs))
	for _, c := range certs {
		v := presentedView{SessionID: c.SessionID, RecordID: c.RecordID, Serial: c.Serial, Found: c.Found}
		if c.Found {
			row := viewCertificate(c.Row)
			v.Certificate = &row
		}
		out = append(out, v)
	}
	// truncated says the session had more records than one read takes
	// (audit.SessionRecordBound): bounded by one session, it is signalled
	// rather than paged (M26.3).
	answer(w, http.StatusOK, map[string]any{
		"session_id": r.PathValue("session"), "certificates": out, "truncated": truncated,
	})
}

// handleCertificateSessions resolves a certificate's serial back to the records
// that name it.
func (s *Server) handleCertificateSessions(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	serial, err := contract.ParseSerial(r.PathValue("serial"))
	if err != nil {
		s.invalid(w, r, "serial", access.ProblemInvalid, "")
		return
	}
	q := audit.CertificateQuery{Serial: serial, Ascending: ascending(r)}
	p, ok := readPage(w, r, certificateRecordList, auditFilters(q.Selection())...)
	if !ok {
		return
	}
	page, err := s.reader.CertificateSessions(r.Context(), tenant, q, p.Page)
	if err != nil {
		s.auditFailure(w, r, "certificate sessions", err)
		return
	}
	s.answerPage(w, r, p, "records", viewRecords(page.Rows), page.More, auditPosition(page.Rows, recordPosition),
		map[string]any{"serial": contract.FormatSerial(serial)})
}

// auditFailure classifies a reader error: a session lookup of "", an absence, a
// bad argument, or an outage.
func (s *Server) auditFailure(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, audit.ErrNoSession):
		s.invalid(w, r, "session", access.ProblemRequired, "")
	case errors.Is(err, audit.ErrRetentionKeyNotConfigured):
		writeError(w, http.StatusServiceUnavailable, Error{
			Code: CodeRetentionKeyNotConfigured, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "this deployment has no key store to sign retention, so nothing was deleted: " +
				"set credential.key_encryption_key_env and restart, or register an ext.KeyStore",
			Parameters: map[string]any{"config_key": "credential.key_encryption_key_env"},
		})
	case audit.IsNotArchived(err):
		var e *audit.NotArchivedError
		errors.As(err, &e)
		writeError(w, http.StatusConflict, Error{
			Code: CodeRetentionNotArchived, CorrelationID: CorrelationIDFrom(r.Context()),
			Parameters: map[string]any{"stream": e.Stream, "archived_through_seq": e.ArchivedThroughSeq},
			Message: fmt.Sprintf("stream %s is archived through %d, so nothing was deleted: "+
				"a pass by sequence deletes only what the archive has confirmed; pass an instant to delete as far as it has",
				e.Stream, e.ArchivedThroughSeq),
		})
	case audit.IsRetentionUnverified(err):
		var e *audit.RetentionUnverifiedError
		errors.As(err, &e)
		params, what := map[string]any{"captures": true}, "the tenant's capture retention"
		if e.Stream != "" {
			params, what = map[string]any{"stream": e.Stream}, "stream "+e.Stream
		}
		writeError(w, http.StatusConflict, Error{
			Code: CodeRetentionUnverified, CorrelationID: CorrelationIDFrom(r.Context()), Parameters: params,
			Message: what + " does not verify where this pass would start, so nothing was deleted: " + e.Detail +
				"; run hoplock-control audit-verify",
		})
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "there is no such record in this tenant",
		})
	case store.IsInvalid(err):
		// The store's own sentence, never err.Error(): that names the Go
		// operation that refused (0023).
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeInvalidRequest, CorrelationID: CorrelationIDFrom(r.Context()),
			Parameters: map[string]any{"field": "query", "problem": "invalid"},
			Message:    "this query cannot be answered: " + store.Reason(err),
		})
	default:
		s.fail(w, r, op, err)
	}
}

// ---------------------------------------------------------------------------
// retention (PLAN §7)
// ---------------------------------------------------------------------------

type retentionBody struct {
	Stream string `json:"stream"`
	// ThroughSeq names the last sequence to delete; Before names an instant
	// instead, and the pass deletes what was received before it. One of
	// the two.
	ThroughSeq int64     `json:"through_seq"`
	Before     time.Time `json:"before"`
}

// handleRetention deletes a contiguous prefix of one stream — never the head,
// never the middle — and records where verification resumes. The pass is
// itself an operator record on the control stream.
func (s *Server) handleRetention(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	var body retentionBody
	if err := decodeJSON(r, &body); err != nil {
		s.refuseBody(w, r, err)
		return
	}
	if strings.TrimSpace(body.Stream) == "" {
		s.invalid(w, r, "stream", access.ProblemRequired, "")
		return
	}
	if body.ThroughSeq < 0 {
		s.invalid(w, r, "through_seq", access.ProblemInvalid, "")
		return
	}
	if (body.ThroughSeq > 0) == !body.Before.IsZero() {
		s.invalid(w, r, "through_seq", "exactly_one_of_through_seq_or_before", "")
		return
	}
	s.trackFeeds(tenant)
	// The pass and its record are one transaction (internal/audit/retention.go).
	out, err := s.emitter.RetainPrefix(r.Context(), tenant, audit.PrefixPass{
		Stream: body.Stream, ThroughSeq: body.ThroughSeq, Before: body.Before,
	}, actor)
	if err != nil {
		s.auditFailure(w, r, "retain", err)
		return
	}
	view := map[string]any{
		"stream": out.Stream, "deleted": out.Deleted, "through_seq": out.ThroughSeq, "through_hash": out.ThroughHash,
		"key": out.KeyName, "held_by_archive": out.HeldByArchive,
	}
	if out.Archived {
		view["archived_through_seq"] = out.ArchivedThroughSeq
	}
	answer(w, http.StatusOK, view)
}

type captureRetentionBody struct {
	Before time.Time `json:"before"`
}

// handleCaptureRetention removes the bytes of session captures recorded before
// an instant, keeping their records: the chain still verifies and the store
// says the bytes are gone rather than changed. An instant ahead of the clock
// runs to now, and the answer's `before` says so.
func (s *Server) handleCaptureRetention(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	actor, ok := s.actorOf(w, r)
	if !ok {
		return
	}
	var body captureRetentionBody
	if err := decodeJSON(r, &body); err != nil {
		s.refuseBody(w, r, err)
		return
	}
	if body.Before.IsZero() {
		s.invalid(w, r, "before", access.ProblemRequired, "")
		return
	}
	s.trackFeeds(tenant)
	out, err := s.emitter.RetainCaptures(r.Context(), tenant, body.Before, actor)
	if err != nil {
		s.auditFailure(w, r, "retain captures", err)
		return
	}
	answer(w, http.StatusOK, map[string]any{
		"captures": out.Emptied, "before": out.Before, "key": out.KeyName, "held_by_archive": out.HeldByArchive,
	})
}

// trackFeeds tells the archive feed and sink delivery, when there are any, that
// a retention pass ran in a tenant. A pass the archive held back is one whose
// tenant the feed must deliver, and one that overtook a sink is one whose sink
// must resume after it; this process may not have seen the tenant since it
// started, because the store cannot list tenants (M18).
func (s *Server) trackFeeds(tenant store.Tenant) {
	if s.archive != nil {
		s.archive.Track(tenant)
	}
	if s.sinks != nil {
		s.sinks.Track(tenant)
	}
}
