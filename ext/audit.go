// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strconv"
	"time"
)

// AuditRecord is one entry from the local append-only audit store (M8), as it
// crosses this seam. It carries the chain position as well as the content,
// because a sink that wants to prove it received everything needs to be able
// to see a gap, and a sink that re-receives a record needs to recognise it.
//
// It carries everything the record's link covers — Tenant, Stream, Sequence,
// PrevHash and Body — so whoever holds it can recompute ChainHash with
// ChainLink and walk the chain without asking Control to vouch for it.
type AuditRecord struct {
	// Tenant owns the record.
	Tenant Tenant
	// RecordID is the store's identifier for the record. It is stable and
	// unique per tenant, and it is what makes export idempotent.
	RecordID string
	// Stream names the chain the record belongs to. A tenant's records form
	// one hash chain per stream (M8): `control` for Control's own records,
	// `proxy:<id>` for a proxy's, and `unattributed` for records ingested by
	// a credential bound to no proxy. Sequence is a position in this chain,
	// so two streams' sequences are unrelated and only (Tenant, Stream,
	// Sequence) names a position.
	Stream string
	// Sequence is the record's position in its stream's hash chain, from 1.
	// Consecutive sequences with no gap within one stream is the sink-side
	// proof that nothing was dropped — except across a retained prefix,
	// which the control stream records. The first sequence an archive or a
	// sink receives in a stream is one after the stream's retention marker
	// when Control had retained a prefix before it was registered; and a
	// sink, which retention does not wait on, receives the next sequence
	// after a marker a later pass moved past its position (AuditSink). In
	// both cases the pass's `operator.audit.retained` record on the control
	// stream names the stream, the last sequence it deleted and that
	// record's hash — which is this record's PrevHash — and a gap no such
	// record explains is a drop.
	Sequence uint64
	// PrevHash is the ChainHash of the record before this one in the stream,
	// and empty at Sequence 1.
	PrevHash string
	// ChainHash is the chain hash at this record: ChainLink over Tenant,
	// Stream, Sequence, PrevHash and Body. A holder of a run of records
	// verifies it by recomputing each ChainHash and checking that each
	// PrevHash is the ChainHash before it.
	ChainHash string
	// Body is the canonical JSON Control hashed, verbatim, as the chain
	// covers it. Every other field here except the chain's own is derived
	// from it. A session capture's digest is inside it; the bytes are not
	// (Capture).
	Body string
	// OccurredAt is when the recorded thing happened, not when it was
	// exported.
	OccurredAt time.Time
	// Kind is a stable event code — never a rendered sentence (M21).
	Kind string
	// SubjectID, TargetID and SessionID tie the record to the rest of the
	// story; any of them may be empty.
	SubjectID string
	TargetID  string
	SessionID string
	// DecisionID addresses the decision record that explains an access
	// (M4). It is the identifier a proxy already told the user.
	DecisionID string
	// Attributes are the record's structured fields, codes rather than prose.
	Attributes map[string]string
}

// ChainVersion is the encoding ChainLink computes, and it is inside every link.
// A later encoding is a new version, computed by a new function beside this
// one, and never a change to this one: an archive verifies records written
// years earlier, and a link that changed meaning under it would make a record
// it holds verify under two readings, or under none.
const ChainVersion = "hoplock.chain/1"

// ChainLink computes the hash of a record at a position in a chain, exactly as
// Control's own chain does: Control computes every link it stores by calling
// this function, so the two cannot differ.
//
// The encoding, which is a promise: SHA-256 over six parts, each written as its
// length in bytes in decimal ASCII, a colon, and the part itself — ChainVersion,
// the tenant, the stream, the sequence in decimal ASCII, prevHash, and body —
// rendered as `sha256:` followed by the lower-case hex digest. The length
// prefixes are what keep two different tuples from producing one byte stream.
// The tenant and the stream are inside the hash, so a record moved to another
// tenant's chain, or another stream, at the same position does not verify
// (M18).
//
// What a verified run proves is what Control's verifier proves: no record in
// it was altered, reordered or removed from between two others. It does not
// prove that nothing was removed from either end, and it is no defence against
// whoever can rewrite a whole stream (PLAN §7).
func ChainLink(tenant Tenant, stream string, sequence uint64, prevHash, body string) string {
	h := sha256.New()
	for _, part := range []string{ChainVersion, string(tenant), stream, strconv.FormatUint(sequence, 10), prevHash, body} {
		_, _ = io.WriteString(h, strconv.Itoa(len(part)))
		_, _ = io.WriteString(h, ":")
		_, _ = io.WriteString(h, part)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Capture is one session capture's bytes as they cross the seam to an archive
// (ArchiveStore.ArchiveCaptures). The record it belongs to carries its digest
// inside the hashed Body, so the chain covers the bytes without carrying them;
// Control offers a capture only after the archive has confirmed that record.
type Capture struct {
	// Tenant owns the capture.
	Tenant Tenant
	// RecordID is the record whose digest covers these bytes, and what makes
	// archiving a capture idempotent.
	RecordID string
	// SessionID is the session the capture is of.
	SessionID string
	// Digest is the record's `capture_sha256`, verbatim as the chain covers
	// it: `sha256:` followed by the lower-case hex SHA-256 of Body's bytes.
	// An archive that recomputes it from what it read can tell a capture it
	// stored intact from one that was changed.
	Digest string
	// Size is the record's `capture_bytes`: how many bytes Body yields, so an
	// archive can bound a read before it starts.
	Size int64
	// Body is the bytes. It is valid only until ArchiveCaptures returns.
	Body io.Reader
}

// AuditSink exports audit records to a system outside Hoplock. What varies is
// where an organisation's records have to end up — Splunk, Sentinel, Elastic,
// a queue, a file drop — and that is a question about their estate rather than
// about access control, which is why it is a seam rather than a setting.
//
// When no implementation is registered, audit records are written to the local
// tamper-evident store and exported nowhere. That is a complete deployment:
// the records exist, the chain is verifiable, and the north-bound API queries
// them. A sink adds a destination; it never becomes the only copy, and Control
// does not delete a record because a sink accepted it.
//
// What Control does with one or more registered (0026):
//
//   - It hands every sink every record it commits, of every tenant, per tenant
//     and per stream in ascending Sequence, at least once: each batch holds one
//     tenant's and one stream's records, contiguous from the last batch the
//     sink confirmed in that stream. Which tenants a sink forwards, and where,
//     is its own.
//   - A nil return confirms the batch: the sink has taken responsibility for
//     it, and Control durably moves the sink's position to the batch's last
//     record. An error confirms nothing, and the same records are offered
//     again after a backoff that grows to a cap. A batch can also arrive twice
//     after a restart, or from two nodes whose turns overlapped, so Export
//     must be idempotent on RecordID: a sink that duplicates on retry
//     produces an audit trail that double-counts.
//   - The position is Control's, kept in its store per sink, tenant and
//     stream, so delivery resumes after a restart on any node with no gap. A
//     sink keeps no cursor of its own.
//   - A sink is named by its Registration.Provider, which is unique within
//     this point. A provider that stops registering keeps its positions and
//     resumes from them when it registers again; a provider registering under
//     a new name is a new sink. A new sink starts each stream at the stream's
//     chain start: its first record, or the one after its retention marker.
//   - Control never has two batches of one stream in flight to one sink, and
//     offers a stream's next batch only after the previous one returned. It
//     may have batches of different streams or tenants in flight to one sink
//     at once, up to a bound in its configuration, so Export must be safe for
//     concurrent use. The context carries Control's deadline for the attempt.
//     A call past it confirms nothing, and its stream is not offered again
//     until it returns; until then it also keeps one of the sink's places in
//     flight, so a sink that ignores its deadline stalls only itself. (On
//     several nodes, a call held past the stream's lease — its backoff and
//     three deadlines more — may see another node offer the stream's batch
//     again, which is a duplicate, never a skip.)
//   - Retention does not wait on a sink, which is a downstream consumer and
//     never the record (M8). When a retention pass removes records a sink has
//     not confirmed, the sink resumes after the pass's marker, and the gap is
//     counted on its lag. The pass's `operator.audit.retained` record on the
//     control stream, which the sink is handed like every other record, names
//     the stream, the last sequence deleted and its hash, which is the
//     PrevHash of the first record after the gap; a later pass over the
//     control stream may in turn remove that record, and its own record says
//     so. Keeping records past retention is ArchiveStore's job.
//   - Delivery runs off ingest and the decision path: no proxy's request waits
//     on a sink, one sink that fails, hangs or lags slows no other sink, and a
//     tenant or stream that fails slows no other tenant's or stream's delivery
//     to the same sink. Delivery writes no audit record of its own; a failure
//     shows on the sink's lag on the extension listing and in the log.
//   - Captures' bytes never cross to a sink; a record carries its capture's
//     digest inside Body.
type AuditSink interface {
	// Export delivers one tenant's and one stream's batch, in ascending
	// Sequence order. It may be called concurrently for different streams,
	// never for one stream at once. The context carries Control's deadline
	// for the attempt.
	Export(ctx context.Context, batch []AuditRecord) error
}

// ArchiveQuery selects records from the archive. An empty Kinds, SubjectID,
// TargetID or SessionID does not filter on that field.
type ArchiveQuery struct {
	// Tenant is required; an archive search that crosses tenants is a bug,
	// not a feature.
	Tenant Tenant
	// From and To bound OccurredAt. A zero value is unbounded on that side.
	From time.Time
	To   time.Time
	// Kinds restricts to these event codes.
	Kinds []string
	// SubjectID, TargetID and SessionID restrict to one actor, target or
	// session.
	SubjectID string
	TargetID  string
	SessionID string
	// Text is a free-text term, which an archive may or may not support.
	// An archive that does not support it returns an ErrInvalid *Error
	// rather than silently ignoring it — a search that quietly drops a
	// filter answers a question nobody asked.
	Text string
	// Limit caps the page size. Zero means the archive's own default.
	Limit int
	// Cursor continues a previous page; empty starts at the beginning.
	Cursor string
}

// ArchivePage is one page of archive results.
type ArchivePage struct {
	// Records are the matching records, oldest first.
	Records []AuditRecord
	// NextCursor continues the search. Empty means this was the last page.
	NextCursor string
}

// ArchiveStore keeps audit records past the point where local retention would
// delete them, and searches what it kept. What varies is how long an
// organisation must keep records and what it costs to keep them there: seven
// years of session history belongs in object storage with a search index in
// front of it, not in the database on the decision path (M5).
//
// When no implementation is registered, retention deletes records when their
// window closes and there is no long-term copy. Nothing is lost that Control
// promised to keep: the retention window is the promise, and an archive
// extends it. This is a capability Control never claimed rather than one moved
// out of it.
//
// What Control does with one registered (0025, 0026):
//
//   - It hands the archive every record it commits, per tenant and per stream,
//     in ascending Sequence, at least once: each batch holds one stream's
//     records, contiguous from the last batch the archive confirmed in that
//     stream. A stream Control had retained a prefix of before the archive was
//     registered starts after its retention marker. A batch can arrive twice —
//     after an error, after a restart, or from two nodes whose turns
//     overlapped — so Archive and ArchiveCaptures must be idempotent on
//     RecordID.
//   - A nil return confirms the batch, and Control durably records how far the
//     archive has confirmed each stream. An error confirms nothing, and the
//     same records are offered again after a backoff. A stream that fails
//     holds back no other stream and no other tenant.
//   - It hands over each session capture's bytes through ArchiveCaptures once
//     the record whose digest covers them is confirmed, so the archive always
//     holds the record before its capture.
//   - It never deletes a record, or empties a capture, that the archive has not
//     confirmed, whether or not it was offered yet. A retention pass by
//     sequence beyond the confirmation is refused (`409
//     retention_not_archived`), a pass by instant stops at it, and a capture
//     pass stops before the earliest capture not yet confirmed; each answer
//     says the archive held it back.
//   - Control never has two calls of one stream in flight to the archive — a
//     stream's ArchiveCaptures follows its Archive, never beside it — and
//     offers a stream's next batch only after the previous call returned. It
//     may have calls for different streams or tenants in flight at once, up
//     to a bound in its configuration, so Archive and ArchiveCaptures must be
//     safe for concurrent use. A call past its deadline confirms nothing, and
//     its stream is not offered again until it returns; until then it also
//     keeps one of the archive's places in flight, so an archive that ignores
//     its deadline stalls only its own delivery, and holds retention back.
//     (On several nodes, a call held past the stream's lease — its backoff
//     and three deadlines more — may see another node offer the stream's
//     batch again, which is a duplicate, never a skip.)
//   - Delivery runs off ingest and the decision path: no proxy's request waits
//     on the archive, and an archive that never returns slows nothing but the
//     delivery to it.
//   - Control calls Search nowhere. It is the host's, for its own routes.
//
// So Archive and ArchiveCaptures must not report success for anything they have
// not durably stored: a nil return is what lets Control delete its own copy.
type ArchiveStore interface {
	// Archive stores a batch of one stream's records durably, in ascending
	// Sequence order. It may be called concurrently for different streams,
	// never for one stream at once. The context carries Control's deadline
	// for the attempt.
	Archive(ctx context.Context, batch []AuditRecord) error
	// ArchiveCaptures stores a batch of session captures durably. Each
	// capture's record was confirmed by Archive before it is offered. The
	// batch is bounded by count and by total Size, and each Body is valid
	// only until the call returns. The context carries Control's deadline
	// for the attempt.
	ArchiveCaptures(ctx context.Context, batch []Capture) error
	// Search returns one page of archived records.
	Search(ctx context.Context, q ArchiveQuery) (ArchivePage, error)
}

// ReportFormat is how a report's body is encoded. It is a closed enum: a
// format Control cannot describe to a caller is a format the north-bound API
// cannot offer.
type ReportFormat int

const (
	// ReportFormatJSON is a machine-readable body.
	ReportFormatJSON ReportFormat = iota
	// ReportFormatCSV is a tabular body.
	ReportFormatCSV
	// ReportFormatPDF is a rendered document.
	ReportFormatPDF
)

// String renders the format as the stable code that crosses the wire.
func (f ReportFormat) String() string {
	switch f {
	case ReportFormatJSON:
		return "json"
	case ReportFormatCSV:
		return "csv"
	case ReportFormatPDF:
		return "pdf"
	}
	return "json"
}

// ReportParameter describes one input a report accepts.
type ReportParameter struct {
	// Name is the parameter's stable code.
	Name string
	// Required reports whether Generate fails without it.
	Required bool
	// Values enumerates the accepted values, when the parameter is a closed
	// set. Empty means free-form.
	Values []string
}

// ReportDescriptor describes one report a provider offers. It carries codes,
// not titles: the console holds the catalogue that turns ReportID into a name
// somebody reads (M21).
type ReportDescriptor struct {
	// ReportID is the report's stable code, unique within the provider.
	ReportID string
	// Formats are the formats this report can be produced in.
	Formats []ReportFormat
	// Parameters are the inputs it accepts beyond the time range.
	Parameters []ReportParameter
}

// ReportRequest asks for one report.
type ReportRequest struct {
	// Tenant is required.
	Tenant Tenant
	// ReportID names the report.
	ReportID string
	// From and To bound the reporting period.
	From time.Time
	To   time.Time
	// Parameters carries the report's own inputs.
	Parameters map[string]string
	// Format is the requested encoding; it must be one the descriptor lists.
	Format ReportFormat
}

// Report is a generated report. Body is streamed rather than buffered because
// a compliance pack over a year of a large estate is not a value to hold in
// memory, and the caller closes it.
type Report struct {
	// ReportID echoes the request.
	ReportID string
	// GeneratedAt is when the provider produced it.
	GeneratedAt time.Time
	// Format is the encoding actually produced.
	Format ReportFormat
	// ContentType is the media type of Body.
	ContentType string
	// Body is the report. The caller closes it.
	Body io.ReadCloser
}

// ReportProvider offers packaged reports over what this deployment recorded.
// What varies is which regime an organisation is audited against — the shape
// of an access-review pack for SOX is not the shape of one for PCI — and that
// is a compliance question rather than an access-control one.
//
// When no implementation is registered, the north-bound audit and decision
// queries are the whole of the reporting available: an operator can ask who
// reached what, when, and why any decision went the way it did, and can export
// the answer. A provider packages those answers into a named, repeatable
// artefact; it does not unlock the underlying data, which was never locked.
type ReportProvider interface {
	// Reports lists what this provider offers for the tenant. The list may
	// differ per tenant, because a reporting pack can be licensed per
	// tenant.
	Reports(ctx context.Context, tenant Tenant) ([]ReportDescriptor, error)
	// Generate produces one report. An unknown ReportID is an ErrNotFound
	// *Error; an unsupported format or a missing required parameter is
	// ErrInvalid.
	Generate(ctx context.Context, req ReportRequest) (Report, error)
}
