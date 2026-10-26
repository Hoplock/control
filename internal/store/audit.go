// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type auditRepo struct{ s *Store }

const auditColumns = `record_id, stream, chain_seq, prev_hash, hash, session_id, kind, severity,
	body, subject, login, target, message, event, decision_id, proxy_id,
	attributes, device_fields,
	enforcement_execution, enforcement_reach, enforcement_verified, enforcement_attested_by,
	credential_method, credential_rung, algorithm_profile,
	grant_system, grant_reference, grant_window_start, grant_window_end,
	grant_additional_kind, grant_additional,
	capture_bytes, capture_sha256, recorded_at, received_at,
	credential_certificate_serial, algorithm_floor, algorithm_bans, algorithm_bans_unmatched,
	negotiated, device_change_op, target_account, device_object_kind,
	gap_cause, gap_records, gap_bytes, gap_first_at, gap_last_at, gap_kinds, gap_critical, gap_record_ids`

func (r auditRepo) Append(ctx context.Context, tenant Tenant, rec AuditRecord) error {
	const op = "store.Audit.Append"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if err := validAuditRecord(op, rec); err != nil {
		return err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	return r.insert(ctx, op, r.s.db, tenant, rec)
}

// AppendChain appends to one tenant's stream under a lock that serialises
// appends to that stream across every node (M8, M18).
//
// THE LOCK IS NOT AN OPTIMISATION. A hash chain's next record depends on the
// head, so two writers reading the same head produce two records claiming the
// same position — one of which the unique index refuses, leaving the other
// chained onto a predecessor it never saw. Postgres holds the lock, rather
// than a mutex in this process, because there is more than one process: a
// second node is the deployment this product is sold into.
//
// `build` is called INSIDE the transaction, with the chain head (the zero
// AuditRecord when the stream is empty) and the subset of `ids` that is not
// already stored, in the order given. It returns the rows to insert, already
// positioned and hashed — positioning and hashing are the domain's, the lock
// and the de-duplication are the database's. Returning fewer rows than it was
// offered is how a caller drops one.
//
// The count returned is how many rows were inserted, which is exactly what
// LogBatchResponse.accepted reports: fewer than sent means duplicates, which
// is the only way a proxy can see its replay being de-duplicated.
func (r auditRepo) AppendChain(
	ctx context.Context,
	tenant Tenant,
	stream string,
	ids []string,
	build func(head AuditRecord, fresh []string) ([]AuditRecord, error),
) (int, error) {
	const op = "store.Audit.AppendChain"
	if err := checkTenant(op, tenant); err != nil {
		return 0, err
	}
	if stream == "" {
		return 0, invalid(op, "stream is required")
	}
	if build == nil {
		return 0, invalid(op, "a build function is required")
	}

	stored := 0
	err := r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		stored = 0

		if err := lockStream(ctx, op, q, tenant, stream); err != nil {
			return err
		}

		fresh, err := r.unseen(ctx, op, q, tenant, ids)
		if err != nil {
			return err
		}

		head, err := r.chainHead(ctx, op, q, tenant, stream)
		if err != nil && !IsNotFound(err) {
			return err
		}
		if IsNotFound(err) {
			head = AuditRecord{}
		}

		recs, err := build(head, fresh)
		if err != nil {
			return err
		}
		for _, rec := range recs {
			if err := validAuditRecord(op, rec); err != nil {
				return err
			}
		}
		if err := r.insertMany(ctx, op, q, tenant, recs); err != nil {
			return err
		}
		stored = len(recs)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return stored, nil
}

// lockStream takes one stream's APPEND LOCK for the rest of the transaction.
// Every append takes it, and so does a retention pass, so a prefix never moves
// while a record is being chained onto the stream. It is one function so that
// the two can never take different locks.
//
// hashtext gives the two int4 keys the advisory lock takes. A collision costs
// two streams a shared lock and nothing else: the chain is still correct, the
// append is merely serialised with a stream it need not have waited for.
func lockStream(ctx context.Context, op string, q querier, tenant Tenant, stream string) error {
	if _, err := q.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		string(tenant), stream); err != nil {
		return wrap(op, err)
	}
	return nil
}

// unseen returns the ids not already stored, in the order given and without
// repeats — a batch that repeats an id within itself is de-duplicated here
// too, because the second copy would otherwise take a chain position and then
// be refused by the unique index, aborting the whole batch.
func (r auditRepo) unseen(ctx context.Context, op string, q querier, tenant Tenant, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx,
		`SELECT record_id FROM audit_records WHERE tenant = $1 AND record_id = ANY($2)`,
		tenant, ids)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	seen := make(map[string]struct{}, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrap(op, err)
		}
		seen[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, wrap(op, err)
	}

	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

// auditInsertColumns is the column list every insert writes, and
// auditInsertArity is how many placeholders one row needs. They are together
// so that a column added to one without the other fails to compile the SQL
// rather than shifting every value one to the left.
const auditInsertColumns = `tenant, record_id, stream, chain_seq, prev_hash, hash,
	session_id, kind, severity, body, subject, login, target,
	message, event, decision_id, proxy_id, attributes, device_fields,
	enforcement_execution, enforcement_reach, enforcement_verified,
	enforcement_attested_by, credential_method, credential_rung,
	algorithm_profile, grant_system, grant_reference,
	grant_window_start, grant_window_end, grant_additional_kind,
	grant_additional, capture_bytes, capture_sha256, recorded_at,
	credential_certificate_serial, algorithm_floor, algorithm_bans, algorithm_bans_unmatched,
	negotiated, device_change_op, target_account, device_object_kind,
	gap_cause, gap_records, gap_bytes, gap_first_at, gap_last_at, gap_kinds, gap_critical, gap_record_ids`

const auditInsertArity = 51

// maxInsertParameters keeps a multi-row insert inside Postgres' 65535-parameter
// limit. A batch above it is split rather than refused: the batch bound is
// internal/audit's to set, and this is a wire limit rather than a policy.
const maxInsertParameters = 60000

// insert writes one row. Only the single-record helper uses it; ingest goes
// through insertMany.
func (r auditRepo) insert(ctx context.Context, op string, q querier, tenant Tenant, rec AuditRecord) error {
	return r.insertMany(ctx, op, q, tenant, []AuditRecord{rec})
}

// insertMany writes a batch as ONE multi-row insert per chunk.
//
// Deliberately no ON CONFLICT DO NOTHING. `record_id` is the idempotency key
// (M8) and a resend must be OBSERVABLY a duplicate; swallowing the conflict
// here would make every batch look fully accepted. AppendChain removes the
// duplicates before it positions anything, so a conflict reaching this point
// is a real race — a writer on another stream took the id between the check
// and the insert — and the transaction is rolled back rather than partly
// applied.
//
// ONE STATEMENT RATHER THAN N. The rows are already inside one transaction, so
// correctness does not need this; throughput does. A proxy's batch is the
// throughput path (contract D8) and a round trip per record turns a 500-record
// batch into 500 of them, which is the difference between a fleet's disk
// buffers draining and a fleet's disk buffers filling.
func (r auditRepo) insertMany(ctx context.Context, op string, q querier, tenant Tenant, recs []AuditRecord) error {
	if len(recs) == 0 {
		return nil
	}

	perChunk := maxInsertParameters / auditInsertArity
	for start := 0; start < len(recs); start += perChunk {
		end := min(start+perChunk, len(recs))

		var (
			values strings.Builder
			args   = make([]any, 0, (end-start)*auditInsertArity)
		)
		for i, rec := range recs[start:end] {
			if i > 0 {
				values.WriteString(", ")
			}
			values.WriteByte('(')
			for j := range auditInsertArity {
				if j > 0 {
					values.WriteString(", ")
				}
				values.WriteByte('$')
				values.WriteString(strconv.Itoa(len(args) + j + 1))
			}
			values.WriteByte(')')

			recordedAt := rec.RecordedAt
			if recordedAt.IsZero() {
				recordedAt = time.Now().UTC()
			}
			args = append(args,
				tenant, rec.RecordID, rec.Stream, rec.ChainSeq, rec.PrevHash, rec.Hash,
				rec.SessionID, rec.Kind, rec.Severity, rec.Body, rec.Subject, rec.Login, rec.Target,
				rec.Message, rec.Event, rec.DecisionID, rec.ProxyID,
				nonNilMap(rec.Attributes), nonNilMap(rec.DeviceFields),
				rec.Enforcement.Execution, rec.Enforcement.Reach, rec.Enforcement.Verified,
				rec.Enforcement.AttestedBy, rec.CredentialMethod, rec.CredentialRung,
				rec.AlgorithmProfile, rec.Grant.System, rec.Grant.Reference,
				rec.Grant.WindowStart, rec.Grant.WindowEnd, rec.Grant.AdditionalKind,
				rec.Grant.Additional, rec.CaptureBytes, rec.CaptureSHA256, recordedAt,
				rec.CertificateSerial, rec.AlgorithmFloor, nonNilStrings(rec.AlgorithmBans),
				nonNilStrings(rec.AlgorithmBansUnmatched), nonNilStrings(rec.Negotiated),
				rec.Device.Op, rec.Device.Account, rec.Device.ObjectKind,
				rec.Gap.Cause, rec.Gap.Records, rec.Gap.Bytes, rec.Gap.FirstAt, rec.Gap.LastAt,
				nonNilStrings(rec.Gap.Kinds), rec.Gap.Critical, nonNilStrings(rec.Gap.RecordIDs))
		}

		if _, err := q.Exec(ctx,
			`INSERT INTO audit_records (`+auditInsertColumns+`) VALUES `+values.String(), args...); err != nil {
			return wrap(op, err)
		}
	}
	return nil
}

func validAuditRecord(op string, rec AuditRecord) error {
	switch {
	case rec.RecordID == "":
		return invalid(op, "record id is required")
	case rec.Stream == "":
		return invalid(op, "stream is required")
	case rec.ChainSeq <= 0:
		return invalid(op, "chain sequence must be positive")
	}
	return nil
}

func (r auditRepo) Get(ctx context.Context, tenant Tenant, recordID string) (AuditRecord, error) {
	const op = "store.Audit.Get"
	if err := checkTenant(op, tenant); err != nil {
		return AuditRecord{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	row := r.s.db.QueryRow(ctx, `
		SELECT `+auditColumns+`
		FROM audit_records
		WHERE tenant = $1 AND record_id = $2`, tenant, recordID)
	return scanAuditRecord(op, row)
}

func (r auditRepo) Chain(ctx context.Context, tenant Tenant, stream string, afterSeq int64, limit int) ([]AuditRecord, error) {
	const op = "store.Audit.Chain"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// Ordered by chain_seq, never by arrival: two tenants' records
	// interleaved by arrival time still produce two chains that each verify
	// alone, because the chain is keyed per tenant per stream (M8, M18).
	rows, err := r.s.db.Query(ctx, `
		SELECT `+auditColumns+`
		FROM audit_records
		WHERE tenant = $1 AND stream = $2 AND chain_seq > $3
		ORDER BY chain_seq
		LIMIT $4`, tenant, stream, afterSeq, boundLimit(limit))
	if err != nil {
		return nil, wrap(op, err)
	}
	return collectAuditRecords(op, rows)
}

func (r auditRepo) ChainHead(ctx context.Context, tenant Tenant, stream string) (AuditRecord, error) {
	const op = "store.Audit.ChainHead"
	if err := checkTenant(op, tenant); err != nil {
		return AuditRecord{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	return r.chainHead(ctx, op, r.s.db, tenant, stream)
}

func (r auditRepo) chainHead(ctx context.Context, op string, q querier, tenant Tenant, stream string) (AuditRecord, error) {
	row := q.QueryRow(ctx, `
		SELECT `+auditColumns+`
		FROM audit_records
		WHERE tenant = $1 AND stream = $2
		ORDER BY chain_seq DESC
		LIMIT 1`, tenant, stream)
	return scanAuditRecord(op, row)
}

// Streams lists the streams a tenant has records in, which is what a whole
// verification walks and what a tenant takes with it on departure (M18).
func (r auditRepo) Streams(ctx context.Context, tenant Tenant) ([]string, error) {
	const op = "store.Audit.Streams"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT DISTINCT stream FROM audit_records WHERE tenant = $1 ORDER BY stream`, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, s)
	}
	return out, wrap(op, rows.Err())
}

// PutCapture stores the bytes of a session capture, keyed by the record that
// describes them. It is idempotent on the record id for the same reason ingest
// is: a resent record must not double-write its payload.
func (r auditRepo) PutCapture(ctx context.Context, tenant Tenant, recordID string, bytes []byte) error {
	const op = "store.Audit.PutCapture"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	if recordID == "" {
		return invalid(op, "record id is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	_, err := r.s.db.Exec(ctx, `
		INSERT INTO audit_captures (tenant, record_id, bytes)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant, record_id) DO NOTHING`, tenant, recordID, nonNilBytes(bytes))
	return wrap(op, err)
}

// Capture returns the stored bytes of a session capture. A record with no
// capture is ErrNotFound rather than an empty slice: "nothing was captured"
// and "the capture is gone" are different answers to a replay request.
func (r auditRepo) Capture(ctx context.Context, tenant Tenant, recordID string) ([]byte, error) {
	const op = "store.Audit.Capture"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	var (
		out      []byte
		retained *time.Time
	)
	err := r.s.db.QueryRow(ctx, `
		SELECT bytes, retained_at FROM audit_captures WHERE tenant = $1 AND record_id = $2`,
		tenant, recordID).Scan(&out, &retained)
	if err != nil {
		return nil, wrap(op, err)
	}
	if retained != nil {
		return nil, fmt.Errorf("%s: %w", op, ErrCaptureRetained)
	}
	return out, nil
}

// AuditQuery selects records. Every field is optional and an empty one does
// not filter; the tenant is not a field because it is an argument (M18) and a
// query that could omit it is one that will.
type AuditQuery struct {
	SessionID  string
	Subject    string
	Login      string
	Target     string
	DecisionID string
	ProxyID    string
	Kinds      []string
	Severities []string
	Event      string
	// GrantSystem and GrantReference answer "every session that ran under
	// this scan" (M16).
	GrantSystem    string
	GrantReference string
	// DeviceField matches one `device_field.<name>` exactly. The namespace
	// is open, so it is a name/value pair rather than a column.
	DeviceFieldName  string
	DeviceFieldValue string
	// NoSession selects only records that belong to no session
	// (`session_id: ""`, Hoplock/proxy#71). An empty SessionID is "do not
	// filter", so this is how the session-less records are asked for.
	NoSession bool
	// Weakened selects records whose profile is present and not `default`
	// (Hoplock/proxy#66): an absent profile is not a target-leg record and
	// is never read as `default`, nor `default` as a weakening.
	Weakened bool
	// Degraded selects records whose credential rung is above 1: the rung
	// counts from 1 and the first choice is rung 1 (Hoplock/proxy#66).
	Degraded bool
	// CertificateSerial selects the records naming one brokered certificate,
	// by its canonical decimal; HasCertificate the records naming any.
	CertificateSerial string
	HasCertificate    bool
	// HasAlgorithms selects the records that say something about a target
	// leg's algorithms: what it negotiated, or the profile in force on it.
	HasAlgorithms bool
	// AlgorithmFloor selects records stamped with exactly this floor.
	AlgorithmFloor string
	// Banned selects records whose bans include one `axis:name`.
	Banned string
	// Negotiated selects records whose target leg negotiated one
	// `axis:value`.
	Negotiated string
	// DeviceChangeOp, TargetAccount and DeviceObjectKind filter the drift
	// feed. DeviceObjectKind is a pointer because empty is a value: it is
	// how a change to an administrator is recorded.
	DeviceChangeOp   string
	TargetAccount    string
	DeviceObjectKind *string
	// Gaps selects `logging.gap` records; GapCause, GapKind, GapRecordID and
	// GapCritical narrow them. GapRecordID finds the refusal that named one
	// record; GapCritical the gaps that hid something critical.
	Gaps        bool
	GapCause    string
	GapKind     string
	GapRecordID string
	GapCritical bool
	// SpanFrom and SpanTo select gaps whose span — the MISSING records' own
	// timestamps, gap_first_at to gap_last_at — overlaps the range; never
	// the gap record's own timestamp, which is when the proxy wrote the
	// report. A gap with no span matches no span query.
	SpanFrom time.Time
	SpanTo   time.Time
	// From is inclusive and To is exclusive, both on recorded_at — the
	// instant the event happened, never the instant it arrived.
	From time.Time
	To   time.Time
	// Limit is Query's and Sessions': bounded by MaxPageLimit, zero taking
	// DefaultPageLimit. Page reads its page's limit and never this one.
	Limit int
	// Ascending reads oldest first. The default is newest first, because
	// every caller looking at a WINDOW wants the recent end of it; a
	// session is the exception and is read forwards, which is the order a
	// replay needs.
	Ascending bool
}

// auditOrder is every audit read's one total order (M26.4): recorded-at,
// stream and chain sequence, newest first unless the query is Ascending.
// `audit_records_order_idx` holds it, read backwards for Ascending.
//
// The tie-break is not decoration. Records arriving in one batch carry
// timestamps to whatever precision the proxy sent — RFC 3339 to the second is
// normal — so a session's records routinely share an instant, and ordering on
// the timestamp alone would return them in whatever order the planner chose.
// The chain position within a stream is the arrival order, which is the
// closest thing to a true sequence this store has, and `(tenant, stream,
// chain_seq)` is unique, so the key is total and a page's position is exact.
func auditOrder(ascending bool) order {
	return order{columns: []string{"recorded_at", "stream", "chain_seq"}, desc: !ascending}
}

// Query answers the first records matching a selection, up to its Limit, for
// a reader inside Control that takes no position. A north-bound list reads
// Page.
func (r auditRepo) Query(ctx context.Context, tenant Tenant, q AuditQuery) ([]AuditRecord, error) {
	got, err := r.page(ctx, "store.Audit.Query", tenant, q, Page{Limit: boundLimit(q.Limit)})
	return got.Rows, err
}

// Page reads one page of the records matching a selection, in its direction
// (M26): at most p.Limit records after the position p.After, which is the
// last record's recorded-at, stream and chain sequence (time.Time, string,
// int64). q.Limit is not read. A window narrows the page but is never its
// position, because many records share an instant.
func (r auditRepo) Page(ctx context.Context, tenant Tenant, q AuditQuery, p Page) (Paged[AuditRecord], error) {
	return r.page(ctx, "store.Audit.Page", tenant, q, p)
}

func (r auditRepo) page(ctx context.Context, op string, tenant Tenant, q AuditQuery, p Page) (Paged[AuditRecord], error) {
	if err := checkTenant(op, tenant); err != nil {
		return Paged[AuditRecord]{}, err
	}
	sql, args, err := auditPageSQL(op, tenant, q, p)
	if err != nil {
		return Paged[AuditRecord]{}, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	return queryPage(ctx, r.s.db, op, p, sql, args, scanAuditRecord)
}

// Sessions answers the distinct sessions whose records match a selection,
// newest first, and never the empty session, which is no session
// (Hoplock/proxy#71). Its limit counts SESSIONS: a session with several
// matching records takes one place, so the answer is the one a caller ending
// sessions needs. It reads one session past the limit to say whether the
// answer stopped short, because a caller that ends the sessions it was given
// must know whether those were all of them (0023). Ascending is not read.
func (r auditRepo) Sessions(ctx context.Context, tenant Tenant, q AuditQuery) ([]string, bool, error) {
	const op = "store.Audit.Sessions"
	if err := checkTenant(op, tenant); err != nil {
		return nil, false, err
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	limit := boundLimit(q.Limit)
	where, args := auditWhere(tenant, q)
	args = append(args, limit+1)
	rows, err := r.s.db.Query(ctx, `
		SELECT session_id
		FROM audit_records
		WHERE `+where+` AND session_id <> ''
		GROUP BY session_id
		ORDER BY max(recorded_at) DESC, session_id
		LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, false, wrap(op, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, false, wrap(op, err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, wrap(op, err)
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// auditPageSQL renders the statement Page and Query run. It is one function so
// that the tests which ask Postgres for the statement's plan read exactly what
// runs.
func auditPageSQL(op string, tenant Tenant, q AuditQuery, p Page) (string, []any, error) {
	o := auditOrder(q.Ascending)
	if err := checkPage(op, o, p); err != nil {
		return "", nil, err
	}
	where, args := auditWhere(tenant, q)
	tail, args := o.clause(p, args)
	return `
		SELECT ` + auditColumns + `
		FROM audit_records
		WHERE ` + where + tail, args, nil
}

// auditWhere renders a selection's WHERE clause, the tenant always `$1`.
//
// AN INDEX IS ONLY USED BY A PREDICATE IT CAN ANSWER. The array columns carry
// GIN indexes (migration 0012), and GIN answers containment, `@>`, and never
// `$1 = ANY(column)`: the two mean the same thing for one value and only the
// first can use the index, so a filter written the second way reads every
// record in the tenant. The partial index on the certificate serial covers
// only non-empty serials, and a plan prepared for any parameter cannot prove
// `= $n` implies that, so the filter says it in so many words. The test that
// EXPLAINs each of these is TestEveryIndexedAuditFilterCanUseItsIndex.
func auditWhere(tenant Tenant, q AuditQuery) (string, []any) {
	var (
		where = []string{"tenant = $1"}
		args  = []any{tenant}
	)
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if q.SessionID != "" {
		add("session_id = $%d", q.SessionID)
	}
	if q.Subject != "" {
		add("subject = $%d", q.Subject)
	}
	if q.Login != "" {
		add("login = $%d", q.Login)
	}
	if q.Target != "" {
		add("target = $%d", q.Target)
	}
	if q.DecisionID != "" {
		add("decision_id = $%d", q.DecisionID)
	}
	if q.ProxyID != "" {
		add("proxy_id = $%d", q.ProxyID)
	}
	if len(q.Kinds) > 0 {
		add("kind = ANY($%d)", q.Kinds)
	}
	if len(q.Severities) > 0 {
		add("severity = ANY($%d)", q.Severities)
	}
	if q.Event != "" {
		add("event = $%d", q.Event)
	}
	if q.GrantSystem != "" {
		add("grant_system = $%d", q.GrantSystem)
	}
	if q.GrantReference != "" {
		add("grant_reference = $%d", q.GrantReference)
	}
	if q.DeviceFieldName != "" {
		add("device_fields @> $%d::jsonb", jsonPair(q.DeviceFieldName, q.DeviceFieldValue))
	}
	if q.NoSession {
		where = append(where, "session_id = ''")
	}
	if q.Weakened {
		where = append(where, "algorithm_profile <> '' AND algorithm_profile <> 'default'")
	}
	if q.Degraded {
		where = append(where, "credential_rung > 1")
	}
	if q.CertificateSerial != "" {
		add("credential_certificate_serial = $%d AND credential_certificate_serial <> ''", q.CertificateSerial)
	}
	if q.HasCertificate {
		where = append(where, "credential_certificate_serial <> ''")
	}
	if q.HasAlgorithms {
		where = append(where, "(negotiated <> '{}' OR algorithm_profile <> '')")
	}
	if q.AlgorithmFloor != "" {
		add("algorithm_floor = $%d", q.AlgorithmFloor)
	}
	if q.Banned != "" {
		add("algorithm_bans @> ARRAY[$%d::text]", q.Banned)
	}
	if q.Negotiated != "" {
		add("negotiated @> ARRAY[$%d::text]", q.Negotiated)
	}
	if q.DeviceChangeOp != "" {
		add("device_change_op = $%d", q.DeviceChangeOp)
	}
	if q.TargetAccount != "" {
		add("target_account = $%d", q.TargetAccount)
	}
	if q.DeviceObjectKind != nil {
		add("device_object_kind = $%d", *q.DeviceObjectKind)
	}
	if q.Gaps || q.GapCause != "" || q.GapKind != "" || q.GapRecordID != "" || q.GapCritical {
		where = append(where, "gap_cause <> ''")
	}
	if q.GapCause != "" {
		add("gap_cause = $%d", q.GapCause)
	}
	if q.GapKind != "" {
		add("gap_kinds @> ARRAY[$%d::text]", q.GapKind)
	}
	if q.GapRecordID != "" {
		add("gap_record_ids @> ARRAY[$%d::text]", q.GapRecordID)
	}
	if q.GapCritical {
		where = append(where, "gap_critical > 0")
	}
	if !q.SpanFrom.IsZero() {
		add("gap_last_at >= $%d", q.SpanFrom)
	}
	if !q.SpanTo.IsZero() {
		add("gap_first_at < $%d", q.SpanTo)
	}
	if !q.From.IsZero() {
		add("recorded_at >= $%d", q.From)
	}
	if !q.To.IsZero() {
		add("recorded_at < $%d", q.To)
	}
	return strings.Join(where, " AND "), args
}

// GapLoss is one session's loss for one cause, SUMMED over its reports: a
// session can carry more than one report of one cause, because the proxy never
// rewrites a report once sent, and each counts records no other one counts.
type GapLoss struct {
	Cause   string
	Reports int64
	Records int64
	Bytes   int64
}

// GapLoss sums one session's `logging.gap` reports per cause. The session id
// must not be empty: "" is no session, not a session.
func (r auditRepo) GapLoss(ctx context.Context, tenant Tenant, sessionID string) ([]GapLoss, error) {
	const op = "store.Audit.GapLoss"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, invalid(op, "a session id is required; an empty one is no session")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	rows, err := r.s.db.Query(ctx, `
		SELECT gap_cause, count(*), COALESCE(sum(gap_records), 0), COALESCE(sum(gap_bytes), 0)
		FROM audit_records
		WHERE tenant = $1 AND session_id = $2 AND gap_cause <> ''
		GROUP BY gap_cause ORDER BY gap_cause`, tenant, sessionID)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []GapLoss
	for rows.Next() {
		var g GapLoss
		if err := rows.Scan(&g.Cause, &g.Reports, &g.Records, &g.Bytes); err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, g)
	}
	return out, wrap(op, rows.Err())
}

// BlockedCommandQuery selects the showcase join (PLAN §7): blocked commands on
// targets carrying a label, in a window, resolved to the decision that
// permitted the access.
type BlockedCommandQuery struct {
	// LabelName and LabelValue filter on the TARGET's labels — `env=prod`
	// in the query that sells the product. Labels live on `targets` and are
	// policy inputs (M3), which is why this is a join rather than an
	// attribute match.
	LabelName  string
	LabelValue string
	From       time.Time
	To         time.Time
	Limit      int
}

// BlockedCommand is one row of that join: what was blocked, who ran it, over
// which route, and under which decision.
type BlockedCommand struct {
	Record AuditRecord
	// Command and Action come from the record's attributes, hoisted because
	// they are the answer rather than context for it.
	Command string
	Action  string
	// TargetZone and TargetLabels come from the target row.
	TargetZone   string
	TargetLabels map[string]string
	// The decision half. DecisionFound is false when the record named a
	// decision this store does not have, which is a real state — a record
	// can outlive a retention pass over decisions — and reporting it as an
	// empty rule would be this server inventing an explanation.
	DecisionFound bool
	MatchedRule   string
	RouteType     string
	DecisionAt    time.Time
}

func (r auditRepo) BlockedCommands(ctx context.Context, tenant Tenant, q BlockedCommandQuery) ([]BlockedCommand, error) {
	const op = "store.Audit.BlockedCommands"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if q.LabelName == "" {
		return nil, invalid(op, "a target label is required")
	}

	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	// A LEFT JOIN onto decisions, an inner one onto targets. The asymmetry
	// is the point: a blocked command on a target this store does not know
	// is outside the question being asked, but a blocked command whose
	// decision has aged out is still a blocked command and dropping it
	// would under-report an incident.
	rows, err := r.s.db.Query(ctx, `
		SELECT `+prefixed(auditColumns, "a")+`,
		       t.zone, t.labels,
		       d.decision_id IS NOT NULL, coalesce(d.matched_rule, ''),
		       coalesce(d.snapshot->>'route_type', ''), d.decided_at
		FROM audit_records a
		JOIN targets t ON t.tenant = a.tenant AND t.hostname = a.target
		LEFT JOIN decisions d ON d.tenant = a.tenant AND d.decision_id = a.decision_id
		WHERE a.tenant = $1
		  AND a.kind = $2
		  AND a.attributes->>'action' = $3
		  AND t.labels @> $4::jsonb
		  AND ($5::timestamptz IS NULL OR a.recorded_at >= $5)
		  AND ($6::timestamptz IS NULL OR a.recorded_at < $6)
		ORDER BY a.recorded_at DESC, a.stream DESC, a.chain_seq DESC
		LIMIT $7`,
		tenant, KindCommand, ActionBlockCommand,
		jsonPair(q.LabelName, q.LabelValue),
		nullableTime(q.From), nullableTime(q.To), boundLimit(q.Limit))
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []BlockedCommand
	for rows.Next() {
		var (
			row       BlockedCommand
			decidedAt *time.Time
			labels    map[string]string
			zone      string
			found     bool
			rule      string
			routeType string
		)
		dest := auditScanDest(&row.Record)
		dest = append(dest, &zone, &labels, &found, &rule, &routeType, &decidedAt)
		if err := rows.Scan(dest...); err != nil {
			return nil, wrap(op, err)
		}
		row.TargetZone = zone
		row.TargetLabels = labels
		row.DecisionFound = found
		row.MatchedRule = rule
		row.RouteType = routeType
		row.DecisionAt = timeOrZero(decidedAt)
		row.Command = row.Record.Attributes["command"]
		row.Action = row.Record.Attributes["action"]
		out = append(out, row)
	}
	return out, wrap(op, rows.Err())
}

// KindCommand and ActionBlockCommand are the two values the showcase join
// filters on. They are the contract's own vocabulary — the record kind and the
// filter action the proxy records — and they are named here so the query does
// not carry bare strings.
const (
	KindCommand        = "command"
	ActionBlockCommand = "block_command"
)

func scanAuditRecord(op string, row rowScanner) (AuditRecord, error) {
	var rec AuditRecord
	if err := row.Scan(auditScanDest(&rec)...); err != nil {
		return AuditRecord{}, wrap(op, err)
	}
	return rec, nil
}

// auditScanDest lists the scan targets for auditColumns, in order. It is one
// function so that the column list and the scan cannot drift apart; a column
// added to one and not the other fails at the first query rather than at the
// first wrong answer.
func auditScanDest(rec *AuditRecord) []any {
	return []any{
		&rec.RecordID, &rec.Stream, &rec.ChainSeq, &rec.PrevHash, &rec.Hash,
		&rec.SessionID, &rec.Kind, &rec.Severity,
		&rec.Body, &rec.Subject, &rec.Login, &rec.Target, &rec.Message, &rec.Event,
		&rec.DecisionID, &rec.ProxyID, &rec.Attributes, &rec.DeviceFields,
		&rec.Enforcement.Execution, &rec.Enforcement.Reach,
		&rec.Enforcement.Verified, &rec.Enforcement.AttestedBy,
		&rec.CredentialMethod, &rec.CredentialRung, &rec.AlgorithmProfile,
		&rec.Grant.System, &rec.Grant.Reference,
		&rec.Grant.WindowStart, &rec.Grant.WindowEnd,
		&rec.Grant.AdditionalKind, &rec.Grant.Additional,
		&rec.CaptureBytes, &rec.CaptureSHA256, &rec.RecordedAt, &rec.ReceivedAt,
		&rec.CertificateSerial, &rec.AlgorithmFloor, &rec.AlgorithmBans, &rec.AlgorithmBansUnmatched,
		&rec.Negotiated, &rec.Device.Op, &rec.Device.Account, &rec.Device.ObjectKind,
		&rec.Gap.Cause, &rec.Gap.Records, &rec.Gap.Bytes, &rec.Gap.FirstAt, &rec.Gap.LastAt,
		&rec.Gap.Kinds, &rec.Gap.Critical, &rec.Gap.RecordIDs,
	}
}

func collectAuditRecords(op string, rows pgx.Rows) ([]AuditRecord, error) {
	defer rows.Close()

	var out []AuditRecord
	for rows.Next() {
		rec, err := scanAuditRecord(op, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, wrap(op, rows.Err())
}

// prefixed qualifies every column in a list with a table alias, so a join can
// reuse the same list a single-table select does.
func prefixed(columns, alias string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// jsonPair renders a one-key containment operand for a jsonb @> match.
//
// The marshal cannot fail for a map of strings, and swallowing the error keeps
// a filter from needing a second return value everywhere it is built.
func jsonPair(name, value string) string {
	encoded, err := json.Marshal(map[string]string{name: value})
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// boundLimit bounds a read that is not a page — Chain, Query, Sessions and
// BlockedCommands — when its caller asks for no limit or for more than a
// page. A query surface that can be asked for the whole store is one
// request away from being an outage (M5's reasoning, one layer out). Its
// numbers are M26's, so an internal read and a page agree on what "a page"
// is; a north-bound list refuses rather than clamps (store.Page).
func boundLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultPageLimit
	case limit > MaxPageLimit:
		return MaxPageLimit
	default:
		return limit
	}
}
