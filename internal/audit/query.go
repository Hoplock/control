// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// The read side (PLAN §7).
//
// It is a QUERY LAYER AND NOT AN HTTP SURFACE: the north-bound API (0019) serves
// from it, under its own credential model (M2). Every field a reader returns is
// named as the proxy names it — `credential_method`, `credential_rung`,
// `target_kex_algorithm` — because from 0019 on the north-bound names are a
// compatibility promise (M19).

// ErrNoSession is a by-session lookup asked for the empty session id. "" is no
// session (Hoplock/proxy#71): answering it would return every session-less
// record in the tenant as if they were one session's.
var ErrNoSession = errors.New("audit: an empty session id is no session; look session-less records up by span or proxy")

// Reader answers the questions an investigation arrives with.
type Reader struct {
	store *store.Store
}

// NewReader builds a Reader over a store.
func NewReader(st *store.Store) *Reader { return &Reader{store: st} }

// Query is the general selection, by session, subject, target, decision id,
// time range, kind, severity, event name, grant reference and device field.
type Query = store.AuditQuery

// Record is one stored row as a reader sees it.
type Row = store.AuditRecord

// Find returns the records matching a query, newest first.
func (r *Reader) Find(ctx context.Context, tenant store.Tenant, q Query) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, q)
}

// Get returns one record by its client-assigned id. It is the lookup the
// conformance suite's durability assertion goes through.
func (r *Reader) Get(ctx context.Context, tenant store.Tenant, recordID string) (Row, error) {
	return r.store.Audit().Get(ctx, tenant, recordID)
}

// Session returns every record of one session, oldest first — the order a
// replay reads them in, and the one case where the newest-first default is
// wrong. It is ordered in the database rather than reversed here: with a limit
// in play, reversing a newest-first page would hand back the END of a session
// and call it the beginning.
func (r *Reader) Session(ctx context.Context, tenant store.Tenant, sessionID string, limit int) ([]Row, error) {
	if sessionID == "" {
		return nil, ErrNoSession
	}
	return r.store.Audit().Query(ctx, tenant, Query{
		SessionID: sessionID,
		Ascending: true,
		Limit:     limit,
	})
}

// Capture returns the stored bytes of one `stream` record, for replay.
func (r *Reader) Capture(ctx context.Context, tenant store.Tenant, recordID string) ([]byte, error) {
	return r.store.Audit().Capture(ctx, tenant, recordID)
}

// AccountMappings returns the ephemeral-account mapping events in a window.
//
// It is a query of its own because the record it returns is the only place
// attribution exists on a device whose account-name length forced the readable
// login segment out of the name: nothing on the target says who `hl-a7f3c1`
// belonged to. Asking for it by session would presuppose knowing which session
// to ask about, which is the thing this answers.
func (r *Reader) AccountMappings(ctx context.Context, tenant store.Tenant, from, to time.Time, limit int) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, Query{
		Event: EventAccountMapping,
		From:  from,
		To:    to,
		Limit: limit,
	})
}

// DriftQuery filters the drift feed the way a reconciliation asks: by device,
// by object and by operation.
type DriftQuery struct {
	// Device is the record's target: the device that was changed.
	Device string
	// Account is the changed object's name (`target_account`).
	Account string
	// ObjectKind is `device_object_kind`; DriftAdministrator selects the
	// changes recorded with none, which is how an administrator is recorded.
	ObjectKind string
	// Op is `create`, `modify` or `delete`.
	Op       string
	From, To time.Time
	Limit    int
}

// DriftAdministrator is the object kind a reconciliation names for an
// administrator, which the record carries as an absent `device_object_kind`.
const DriftAdministrator = "administrator"

// DriftChange is one `device.config.change`, with its facts as fields rather
// than an attribute map the caller has to parse.
type DriftChange struct {
	RecordID  string `json:"record_id"`
	SessionID string `json:"session_id,omitempty"`
	Device    string `json:"device"`
	Platform  string `json:"platform,omitempty"`
	Op        string `json:"device_change_op"`
	Account   string `json:"target_account"`
	// ObjectKind is empty for an administrator, as the record has it.
	ObjectKind   string            `json:"device_object_kind,omitempty"`
	DeviceFields map[string]string `json:"device_fields,omitempty"`
	ProxyID      string            `json:"proxy_id,omitempty"`
	RecordedAt   time.Time         `json:"recorded_at"`
}

// DeviceConfigChanges is the drift feed (Hoplock/proxy#66): one record per
// configuration change the proxy made on a device. A customer's drift detection
// sees every one as an unexplained change; this is the query that lets a SIEM
// close them, and an orphan sweep's change comes back with no session and no
// device fields, because a sweep has no route.
func (r *Reader) DeviceConfigChanges(ctx context.Context, tenant store.Tenant, q DriftQuery) ([]DriftChange, error) {
	sq := Query{
		Event: EventDeviceConfigChange, Target: q.Device,
		TargetAccount: q.Account, DeviceChangeOp: q.Op,
		From: q.From, To: q.To, Limit: q.Limit,
	}
	if q.ObjectKind != "" {
		kind := q.ObjectKind
		if kind == DriftAdministrator {
			kind = ""
		}
		sq.DeviceObjectKind = &kind
	}
	rows, err := r.store.Audit().Query(ctx, tenant, sq)
	if err != nil {
		return nil, err
	}
	out := make([]DriftChange, 0, len(rows))
	for _, row := range rows {
		out = append(out, DriftChange{
			RecordID: row.RecordID, SessionID: row.SessionID, Device: row.Target,
			Platform: row.Attributes[AttrPlatform], Op: row.Device.Op, Account: row.Device.Account,
			ObjectKind: row.Device.ObjectKind, DeviceFields: row.DeviceFields,
			ProxyID: row.ProxyID, RecordedAt: row.RecordedAt,
		})
	}
	return out, nil
}

// UnderGrant returns every record of every session that ran under one external
// system's reference — "show me every session that ran under this scan" (M16).
func (r *Reader) UnderGrant(ctx context.Context, tenant store.Tenant, system, reference string, limit int) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, Query{
		GrantSystem:    system,
		GrantReference: reference,
		Limit:          limit,
	})
}

// ByDeviceField returns the records provisioned with one device field value —
// `vdom=root` against `vdom=global` being the difference between an
// administrator confined to one virtual domain and a global one on the same
// host.
func (r *Reader) ByDeviceField(ctx context.Context, tenant store.Tenant, name, value string, limit int) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, Query{
		DeviceFieldName:  name,
		DeviceFieldValue: value,
		Limit:            limit,
	})
}

// BlockedCommands is the query that sells the product (PLAN §7):
//
//	every blocked command on env=prod last week, who ran it, over which
//	route, and which decision permitted the access.
//
// It is a join from audit to decisions, which is why 0008 stores `decision_id`
// on both sides, and to targets, because `env=prod` is a label and labels are
// policy inputs rather than record attributes. The approved grant that made
// the access possible is the third leg; the grant CONTEXT is already on every
// row (M16) and the grant record itself arrives with 0012.
func (r *Reader) BlockedCommands(ctx context.Context, tenant store.Tenant, label, value string, from, to time.Time, limit int) ([]store.BlockedCommand, error) {
	return r.store.Audit().BlockedCommands(ctx, tenant, store.BlockedCommandQuery{
		LabelName:  label,
		LabelValue: value,
		From:       from,
		To:         to,
		Limit:      limit,
	})
}

// DegradedCredentials returns the records whose session ran on something other
// than the credential method policy preferred: `credential_rung` above 1, the
// rung counting from 1 (proxy D14, Hoplock/proxy#66). A record with no rung is
// not degraded.
//
// It is a query rather than a report because the answer is a COUNT ACROSS AN
// ESTATE: one session accepting its second choice is a fact about that target,
// and a thousand of them is a deployment where the preferred method does not
// work.
func (r *Reader) DegradedCredentials(ctx context.Context, tenant store.Tenant, from, to time.Time, limit int) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, Query{Degraded: true, From: from, To: to, Limit: limit})
}

// Weakened returns the records whose target leg ran on a legacy profile: the
// profile is present and is not `default`. The proxy stamps `default` too, so an
// absent profile is a record that is not about a target leg and is never read as
// one. This is how an operator learns a route runs on SHA-1 from the record
// rather than by reading policy (Hoplock/proxy#66).
func (r *Reader) Weakened(ctx context.Context, tenant store.Tenant, from, to time.Time, limit int) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, Query{Weakened: true, From: from, To: to, Limit: limit})
}

// CertificatePresented answers "which certificate did this session present"
// (Hoplock/proxy#68): the session's records naming a serial, joined to the row
// this server minted under it. A serial that names no row is reported with
// Found false — a record can outlive a certificate row, and a guess would be an
// invention.
type CertificatePresented struct {
	SessionID string               `json:"session_id"`
	RecordID  string               `json:"record_id"`
	Serial    string               `json:"credential_certificate_serial"`
	Found     bool                 `json:"found"`
	Row       store.SSHCertificate `json:"-"`
}

// SessionCertificates returns the certificates one session presented.
func (r *Reader) SessionCertificates(ctx context.Context, tenant store.Tenant, sessionID string) ([]CertificatePresented, error) {
	if sessionID == "" {
		return nil, ErrNoSession
	}
	rows, err := r.store.Audit().Query(ctx, tenant, Query{SessionID: sessionID, Ascending: true})
	if err != nil {
		return nil, err
	}
	var out []CertificatePresented
	for _, row := range rows {
		if row.CertificateSerial == "" {
			continue
		}
		p := CertificatePresented{SessionID: row.SessionID, RecordID: row.RecordID, Serial: row.CertificateSerial}
		serial, err := contract.ParseSerial(row.CertificateSerial)
		if err == nil && serial <= 1<<63-1 {
			cert, err := r.store.SSHCertificates().Get(ctx, tenant, int64(serial))
			switch {
			case err == nil:
				p.Found, p.Row = true, cert
			case !store.IsNotFound(err):
				return nil, err
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// CertificateSessions answers the reverse: which sessions presented one
// certificate, by its serial.
func (r *Reader) CertificateSessions(ctx context.Context, tenant store.Tenant, serial uint64, limit int) ([]Row, error) {
	return r.store.Audit().Query(ctx, tenant, Query{
		CertificateSerial: contract.FormatSerial(serial), Limit: limit,
	})
}

// UnmetAlgorithmPolicy is one `target.algorithm_policy_unmet`: a target the
// route's algorithm policy could not reach, which the user saw only as an
// outage (Hoplock/proxy#66, #69).
type UnmetAlgorithmPolicy struct {
	RecordID string `json:"record_id"`
	Target   string `json:"target"`
	Addr     string `json:"target_addr,omitempty"`
	// Axis is the axis that failed; Cause is `profile`, `floor` or `ban`.
	Axis  string `json:"algorithm_axis"`
	Cause string `json:"algorithm_policy_cause,omitempty"`
	// Offered is what the target offered on the axis. OfferedKnown is false
	// when the record carried no list — on `public_key_auth` that means the
	// library could not say, never that the target offered nothing.
	Offered      []string `json:"target_algorithms_offered,omitempty"`
	OfferedKnown bool     `json:"offered_known"`
	// The policy it failed under.
	Profile    string    `json:"algorithm_profile,omitempty"`
	Floor      string    `json:"algorithm_floor,omitempty"`
	Bans       []string  `json:"algorithm_bans,omitempty"`
	SessionID  string    `json:"session_id,omitempty"`
	ProxyID    string    `json:"proxy_id,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// AlgorithmPolicyUnmet lists the targets that have reported an unmet algorithm
// policy, newest first — what an operator reads to choose a route's profile,
// floor or bans, and what the publish-time report warns from. An empty target
// lists every one.
func (r *Reader) AlgorithmPolicyUnmet(ctx context.Context, tenant store.Tenant, target string, from, to time.Time, limit int) ([]UnmetAlgorithmPolicy, error) {
	rows, err := r.store.Audit().Query(ctx, tenant, Query{
		Event: EventAlgorithmPolicyUnmet, Target: target, From: from, To: to, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]UnmetAlgorithmPolicy, 0, len(rows))
	for _, row := range rows {
		offered, known := row.Attributes[AttrTargetAlgorithmsOffered]
		out = append(out, UnmetAlgorithmPolicy{
			RecordID: row.RecordID, Target: row.Target, Addr: row.Attributes[AttrTargetAddr],
			Axis: row.Attributes[AttrAlgorithmAxis], Cause: row.Attributes[AttrAlgorithmPolicyCause],
			Offered: splitList(offered), OfferedKnown: known,
			Profile: row.AlgorithmProfile, Floor: row.AlgorithmFloor, Bans: row.AlgorithmBans,
			SessionID: row.SessionID, ProxyID: row.ProxyID, RecordedAt: row.RecordedAt,
		})
	}
	return out, nil
}

// NegotiatedQuery is one of the three questions the algorithm records answer
// (Hoplock/proxy#69). Set exactly one of Negotiated, Floor or Banned.
type NegotiatedQuery struct {
	// Axis and Value: which sessions negotiated this value on this axis —
	// the runbook's third step.
	Axis, Value string
	// Floor: which sessions ran under this floor.
	Floor string
	// BanAxis and BanName: which sessions ran under a ban on this
	// identifier.
	BanAxis, BanName string
	From, To         time.Time
	Limit            int
}

// SessionsBy answers the first two questions: the distinct sessions whose
// records match, newest first. Session-less records name no session and are
// not returned.
func (r *Reader) SessionsBy(ctx context.Context, tenant store.Tenant, q NegotiatedQuery) ([]string, error) {
	sq := Query{From: q.From, To: q.To, Limit: q.Limit}
	switch {
	case q.Axis != "" && q.Value != "":
		sq.Negotiated = q.Axis + ":" + q.Value
	case q.Floor != "":
		sq.AlgorithmFloor = q.Floor
	case q.BanAxis != "" && q.BanName != "":
		sq.Banned = q.BanAxis + ":" + q.BanName
	default:
		return nil, errors.New("audit: name an axis and value, a floor, or a banned axis and name")
	}
	rows, err := r.store.Audit().Query(ctx, tenant, sq)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, row := range rows {
		if row.SessionID != "" && !slices.Contains(out, row.SessionID) {
			out = append(out, row.SessionID)
		}
	}
	return out, nil
}

// SessionAlgorithms is the third question, and the only per-session proof that
// a floor or a ban held: what the session's target leg negotiated, beside the
// policy in force.
type SessionAlgorithms struct {
	SessionID string `json:"session_id"`
	// Negotiated is axis → values, as the proxy recorded them.
	Negotiated map[string][]string `json:"negotiated"`
	// PublicKeyAlgorithmsOffered is what the proxy OFFERED to authenticate
	// with, not what authentication used.
	PublicKeyAlgorithmsOffered []string `json:"target_public_key_algorithms_offered,omitempty"`
	Profile                    string   `json:"algorithm_profile,omitempty"`
	Floor                      string   `json:"algorithm_floor,omitempty"`
	Bans                       []string `json:"algorithm_bans,omitempty"`
	BansUnmatched              []string `json:"algorithm_bans_unmatched,omitempty"`
}

// Algorithms reads one session's negotiated values and the policy in force.
func (r *Reader) Algorithms(ctx context.Context, tenant store.Tenant, sessionID string) (SessionAlgorithms, error) {
	if sessionID == "" {
		return SessionAlgorithms{}, ErrNoSession
	}
	rows, err := r.store.Audit().Query(ctx, tenant, Query{SessionID: sessionID, Ascending: true})
	if err != nil {
		return SessionAlgorithms{}, err
	}
	out := SessionAlgorithms{SessionID: sessionID, Negotiated: map[string][]string{}}
	for _, row := range rows {
		for _, entry := range row.Negotiated {
			axis, value, _ := strings.Cut(entry, ":")
			if !slices.Contains(out.Negotiated[axis], value) {
				out.Negotiated[axis] = append(out.Negotiated[axis], value)
			}
		}
		if v := row.Attributes[AttrTargetPublicKeyAlgorithmsOffered]; v != "" {
			out.PublicKeyAlgorithmsOffered = splitList(v)
		}
		if row.AlgorithmProfile != "" {
			out.Profile, out.Floor = row.AlgorithmProfile, row.AlgorithmFloor
			out.Bans, out.BansUnmatched = row.AlgorithmBans, row.AlgorithmBansUnmatched
		}
	}
	return out, nil
}

// GapQuery selects `logging.gap` records (Hoplock/proxy#71).
type GapQuery struct {
	SessionID string
	// NoSession selects the gaps over records that belonged to no session.
	NoSession bool
	ProxyID   string
	// Cause is `evicted` (this server never received the records — an
	// outage longer than the proxy's window) or `refused` (it received them
	// and refused them itself — a defect, in a proxy's records or in this
	// server's validation).
	Cause string
	// SpanFrom and SpanTo select gaps whose span overlaps the range: the
	// MISSING records' timestamps, never the gap record's own.
	SpanFrom, SpanTo time.Time
	// RecordID finds the refusal that named one record, so an operator
	// holding an id from a proxy's set-aside area finds this server's answer.
	RecordID string
	// Kind selects gaps that lost a record of this kind; Critical those
	// that hid something critical.
	Kind     string
	Critical bool
	Limit    int
}

// Gap is one `logging.gap` record, its keys as fields.
type Gap struct {
	RecordID         string     `json:"record_id"`
	SessionID        string     `json:"session_id,omitempty"`
	ProxyID          string     `json:"proxy_id,omitempty"`
	Severity         string     `json:"severity"`
	Cause            string     `json:"gap_cause"`
	Records          *int64     `json:"gap_records,omitempty"`
	Bytes            *int64     `json:"gap_bytes,omitempty"`
	FirstAt          *time.Time `json:"gap_first_at,omitempty"`
	LastAt           *time.Time `json:"gap_last_at,omitempty"`
	Kinds            []string   `json:"gap_kinds,omitempty"`
	Critical         *int64     `json:"gap_critical,omitempty"`
	RecordIDs        []string   `json:"gap_record_ids,omitempty"`
	RecordIDsTrimmed bool       `json:"gap_record_ids_truncated,omitempty"`
	RefusalCode      string     `json:"refusal_code,omitempty"`
	RefusalMessage   string     `json:"refusal_message,omitempty"`
	// ReportedAt is the gap record's own timestamp: when the proxy wrote
	// the report, which is often after session_end.
	ReportedAt time.Time `json:"reported_at"`
}

// Gaps lists what a proxy could not deliver, newest report first.
func (r *Reader) Gaps(ctx context.Context, tenant store.Tenant, q GapQuery) ([]Gap, error) {
	rows, err := r.store.Audit().Query(ctx, tenant, Query{
		Gaps: true, SessionID: q.SessionID, NoSession: q.NoSession, ProxyID: q.ProxyID,
		GapCause: q.Cause, SpanFrom: q.SpanFrom, SpanTo: q.SpanTo,
		GapRecordID: q.RecordID, GapKind: q.Kind, GapCritical: q.Critical, Limit: q.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Gap, 0, len(rows))
	for _, row := range rows {
		out = append(out, GapOf(row))
	}
	return out, nil
}

// GapOf renders one stored gap record.
func GapOf(row Row) Gap {
	return Gap{
		RecordID: row.RecordID, SessionID: row.SessionID, ProxyID: row.ProxyID, Severity: row.Severity,
		Cause: row.Gap.Cause, Records: row.Gap.Records, Bytes: row.Gap.Bytes,
		FirstAt: row.Gap.FirstAt, LastAt: row.Gap.LastAt, Kinds: row.Gap.Kinds,
		Critical: row.Gap.Critical, RecordIDs: row.Gap.RecordIDs,
		RecordIDsTrimmed: row.Attributes[AttrGapRecordIDsTruncated] == "true",
		RefusalCode:      row.Attributes[AttrRefusalCode],
		RefusalMessage:   row.Attributes[AttrRefusalMessage],
		ReportedAt:       row.RecordedAt,
	}
}

// SessionLoss is a session's loss per cause, SUMMED over every report of that
// cause: the proxy never rewrites a report once sent, so a session can carry
// several of one cause, each counting records no other counts. Keeping only
// the latest would under-report.
func (r *Reader) SessionLoss(ctx context.Context, tenant store.Tenant, sessionID string) ([]store.GapLoss, error) {
	if sessionID == "" {
		return nil, ErrNoSession
	}
	return r.store.Audit().GapLoss(ctx, tenant, sessionID)
}
