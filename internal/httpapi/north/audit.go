// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"errors"
	"net/http"
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

// queryParams reads the common window: from, to and limit.
type window struct {
	From, To time.Time
	Limit    int
}

func (s *Server) windowParams(w http.ResponseWriter, r *http.Request) (window, bool) {
	var win window
	var ok bool
	if win.From, ok = s.timeParam(w, r, "from"); !ok {
		return window{}, false
	}
	if win.To, ok = s.timeParam(w, r, "to"); !ok {
		return window{}, false
	}
	if win.Limit, ok = s.limitParam(w, r); !ok {
		return window{}, false
	}
	return win, true
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
	win, ok := s.windowParams(w, r)
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
		From: win.From, To: win.To, Limit: win.Limit, Ascending: v.Get("order") == "asc",
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
	rows, err := s.reader.Find(r.Context(), tenant, q)
	if err != nil {
		s.auditFailure(w, r, "query audit", err)
		return
	}
	answer(w, http.StatusOK, map[string]any{"records": viewRecords(rows)})
}

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
// carrying a label, each with the access decision that permitted the session.
func (s *Server) handleBlockedCommands(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	win, ok := s.windowParams(w, r)
	if !ok {
		return
	}
	label, value, found := strings.Cut(r.URL.Query().Get("label"), "=")
	if !found || label == "" {
		s.invalid(w, r, "label", access.ProblemRequired, "")
		return
	}
	rows, err := s.reader.BlockedCommands(r.Context(), tenant, label, value, win.From, win.To, win.Limit)
	if err != nil {
		s.auditFailure(w, r, "blocked commands", err)
		return
	}
	if rows == nil {
		rows = []store.BlockedCommand{}
	}
	answer(w, http.StatusOK, map[string]any{"blocked": rows})
}

// handleDrift is the drift feed: one record per change the proxy made on a
// device, filtered by device, object and operation.
func (s *Server) handleDrift(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	win, ok := s.windowParams(w, r)
	if !ok {
		return
	}
	v := r.URL.Query()
	rows, err := s.reader.DeviceConfigChanges(r.Context(), tenant, audit.DriftQuery{
		Device: v.Get("device"), Account: v.Get("account"), ObjectKind: v.Get("object_kind"), Op: v.Get("op"),
		From: win.From, To: win.To, Limit: win.Limit,
	})
	if err != nil {
		s.auditFailure(w, r, "drift feed", err)
		return
	}
	if rows == nil {
		rows = []audit.DriftChange{}
	}
	answer(w, http.StatusOK, map[string]any{"changes": rows})
}

// handleAlgorithmPolicyUnmet lists the targets that reported an unmet algorithm
// policy: the axis, the cause, what they offered, the policy and when.
func (s *Server) handleAlgorithmPolicyUnmet(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	win, ok := s.windowParams(w, r)
	if !ok {
		return
	}
	rows, err := s.reader.AlgorithmPolicyUnmet(r.Context(), tenant, r.URL.Query().Get("target"), win.From, win.To, win.Limit)
	if err != nil {
		s.auditFailure(w, r, "unmet algorithm policy", err)
		return
	}
	if rows == nil {
		rows = []audit.UnmetAlgorithmPolicy{}
	}
	answer(w, http.StatusOK, map[string]any{"unmet": rows})
}

// handleNegotiated answers "which sessions negotiated this", "which ran under
// this floor" and "which ran under a ban on this" — the runbook's third step
// reads the first and hands the answer to session_kill.
func (s *Server) handleNegotiated(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	win, ok := s.windowParams(w, r)
	if !ok {
		return
	}
	v := r.URL.Query()
	q := audit.NegotiatedQuery{
		Axis: v.Get("axis"), Value: v.Get("value"), Floor: v.Get("floor"),
		BanAxis: v.Get("ban_axis"), BanName: v.Get("ban_name"),
		From: win.From, To: win.To, Limit: win.Limit,
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
	sessions, truncated, err := s.reader.SessionsBy(r.Context(), tenant, q)
	if err != nil {
		s.auditFailure(w, r, "negotiated sessions", err)
		return
	}
	if sessions == nil {
		sessions = []string{}
	}
	// truncated says the limit cut the answer short: the runbook's next step
	// ends exactly these sessions, so the caller asks again for the rest.
	answer(w, http.StatusOK, map[string]any{"sessions": sessions, "truncated": truncated})
}

// handleGaps finds what a proxy could not deliver (Hoplock/proxy#71).
func (s *Server) handleGaps(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	limit, ok := s.limitParam(w, r)
	if !ok {
		return
	}
	v := r.URL.Query()
	q := audit.GapQuery{
		SessionID: v.Get("session"), NoSession: boolParam(r, "no_session"), ProxyID: v.Get("proxy"),
		Cause: v.Get("cause"), RecordID: v.Get("record_id"), Kind: v.Get("kind"), Critical: boolParam(r, "critical"),
		Limit: limit,
	}
	var okSpan bool
	if q.SpanFrom, okSpan = s.timeParam(w, r, "span_from"); !okSpan {
		return
	}
	if q.SpanTo, okSpan = s.timeParam(w, r, "span_to"); !okSpan {
		return
	}
	gaps, err := s.reader.Gaps(r.Context(), tenant, q)
	if err != nil {
		s.auditFailure(w, r, "gaps", err)
		return
	}
	if gaps == nil {
		gaps = []audit.Gap{}
	}
	answer(w, http.StatusOK, map[string]any{"gaps": gaps})
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
	certs, err := s.reader.SessionCertificates(r.Context(), tenant, r.PathValue("session"))
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
	answer(w, http.StatusOK, map[string]any{"session_id": r.PathValue("session"), "certificates": out})
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
	limit, ok := s.limitParam(w, r)
	if !ok {
		return
	}
	rows, err := s.reader.CertificateSessions(r.Context(), tenant, serial, limit)
	if err != nil {
		s.auditFailure(w, r, "certificate sessions", err)
		return
	}
	answer(w, http.StatusOK, map[string]any{"serial": contract.FormatSerial(serial), "records": viewRecords(rows)})
}

// auditFailure classifies a reader error: a session lookup of "", an absence, a
// bad argument, or an outage.
func (s *Server) auditFailure(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, audit.ErrNoSession):
		s.invalid(w, r, "session", access.ProblemRequired, "")
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
	// The pass and its record are one transaction (internal/audit/retention.go).
	out, err := s.emitter.RetainPrefix(r.Context(), tenant, audit.PrefixPass{
		Stream: body.Stream, ThroughSeq: body.ThroughSeq, Before: body.Before,
	}, actor)
	if err != nil {
		s.auditFailure(w, r, "retain", err)
		return
	}
	answer(w, http.StatusOK, map[string]any{
		"stream": out.Stream, "deleted": out.Deleted, "through_seq": out.ThroughSeq, "through_hash": out.ThroughHash,
	})
}

type captureRetentionBody struct {
	Before time.Time `json:"before"`
}

// handleCaptureRetention removes the bytes of session captures recorded before
// an instant, keeping their records: the chain still verifies and the store
// says the bytes are gone rather than changed.
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
	n, err := s.emitter.RetainCaptures(r.Context(), tenant, body.Before, actor)
	if err != nil {
		s.auditFailure(w, r, "retain captures", err)
		return
	}
	answer(w, http.StatusOK, map[string]any{"captures": n, "before": body.Before.UTC()})
}
