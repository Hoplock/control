// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"context"
	"io"
	"time"
)

// AuditRecord is one entry from the local append-only audit store (M8), as it
// crosses this seam. It carries the chain position as well as the content,
// because a sink that wants to prove it received everything needs to be able
// to see a gap, and a sink that re-receives a record needs to recognise it.
type AuditRecord struct {
	// Tenant owns the record.
	Tenant Tenant
	// RecordID is the store's identifier for the record. It is stable and
	// unique per tenant, and it is what makes export idempotent.
	RecordID string
	// Sequence is the record's position in the tenant's hash chain.
	// Consecutive sequences with no gap is the sink-side proof that nothing
	// was dropped.
	Sequence uint64
	// ChainHash is the chain hash at this record, so a sink can verify the
	// chain it was handed without asking Control to vouch for it.
	ChainHash string
	// OccurredAt is when the recorded thing happened, not when it was
	// exported.
	OccurredAt time.Time
	// Kind is a stable event code — never a rendered sentence (M21).
	Kind string
	// SubjectID, TargetID, SessionID and DecisionID tie the record to the
	// rest of the story; any of them may be empty.
	SubjectID string
	TargetID  string
	SessionID string
	// DecisionID addresses the decision record that explains an access
	// (M4). It is the identifier a proxy already told the user.
	DecisionID string
	// Attributes are the record's structured fields, codes rather than prose.
	Attributes map[string]string
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
// Export must be idempotent on RecordID. Control retries a batch it could not
// confirm, and a sink that duplicates on retry produces an audit trail that
// double-counts. Returning an error means the batch was not delivered and
// Control will offer it again; returning nil means the sink has taken
// responsibility for it.
type AuditSink interface {
	// Export delivers a batch in ascending Sequence order. The context
	// carries Control's deadline for the attempt; a sink that ignores it
	// delays every later batch behind it.
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
// Control will not delete a local record that an archive was asked for and has
// not confirmed. Archive must therefore be idempotent on RecordID and must not
// report success for a record it has not durably stored.
type ArchiveStore interface {
	// Archive stores a batch durably, in ascending Sequence order.
	Archive(ctx context.Context, batch []AuditRecord) error
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
