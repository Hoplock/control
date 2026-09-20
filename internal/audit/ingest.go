// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// Ingester is the write side of the audit store: both contract log paths, the
// chain, and the idempotency that makes a proxy's replay safe.
type Ingester struct {
	store  *store.Store
	limits Limits
	log    *slog.Logger
}

// Options configures an Ingester.
type Options struct {
	Store  *store.Store
	Limits Limits
	Logger *slog.Logger
}

// New builds an Ingester.
func New(o Options) (*Ingester, error) {
	if o.Store == nil {
		// The same rule the south-bound listener keeps: a component
		// that starts without what it needs answers 5xx one request at
		// a time instead of failing at boot, and an audit path that
		// cannot store is a fleet writing into nothing.
		return nil, fmt.Errorf("audit: a store is required")
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Ingester{store: o.Store, limits: o.Limits.orDefaults(), log: log}, nil
}

// Submission is one ingest request: the records, and who submitted them.
//
// THE TENANT COMES FROM THE PROXY'S ENROLLED IDENTITY and never from a record
// (M18). The contract carries no tenant field, because a proxy asserting its
// own tenancy would be a caller asserting its own authority.
type Submission struct {
	Tenant  store.Tenant
	ProxyID string
	Records []contract.LogRecord
	// Priority marks the single-record path, whose ack means durable.
	Priority bool
}

// Receipt is what an ingest produced.
type Receipt struct {
	// Stored is how many records were written. Fewer than submitted means
	// the rest were duplicates, which is exactly what
	// LogBatchResponse.accepted reports.
	Stored int
	// Duplicates is Stored subtracted from the submission, named so a
	// caller does not have to subtract to log it.
	Duplicates int
	// RecordIDs are the ids written, in order. The priority path answers
	// with the first of them as its receipt.
	RecordIDs []string
}

// ErrEmptyBatch is a batch with no records. The contract requires at least one
// (`minItems: 1`), so an empty one is a malformed request rather than a
// successful ingest of nothing.
var ErrEmptyBatch = errors.New("audit: a batch must carry at least one record")

// Ingest validates, redacts, chains and stores a submission.
//
// IT RETURNS ONLY AFTER THE COMMIT. The priority path's ack means DURABLE —
// the proxy acts on a critical security event knowing this server recorded it
// — and there is no buffering, no queue and no goroutine between the request
// and the write on either path. The batch path has the same property for free,
// which is why there is one function rather than two: a fast path that acked
// early would be a durability guarantee that held only for the records nobody
// was in a hurry about.
//
// A BATCH IS ALL OR NOTHING. A record this server refuses fails the whole
// request with a 400 and stores none of it, rather than storing the valid ones
// and reporting a shortfall. The contract decides that: `accepted` is
// documented as "fewer than sent means the rest were duplicates; the proxy may
// drop them", so a count short by the invalid records would tell the proxy to
// DISCARD them. Partial acceptance under that wording is silent, permanent
// audit loss, and it would be invisible until somebody went looking for a
// record that never arrived.
func (in *Ingester) Ingest(ctx context.Context, sub Submission) (Receipt, error) {
	if len(sub.Records) == 0 {
		return Receipt{}, ErrEmptyBatch
	}
	if len(sub.Records) > in.limits.MaxBatchRecords {
		return Receipt{}, &MalformedError{
			Index: -1,
			Reason: fmt.Sprintf("the batch carries %d records, above the %d this server accepts",
				len(sub.Records), in.limits.MaxBatchRecords),
		}
	}
	if sub.Tenant == "" {
		return Receipt{}, fmt.Errorf("audit: a tenant is required")
	}

	index := func(i int) int {
		if sub.Priority {
			return -1
		}
		return i
	}

	parsed := make([]Record, 0, len(sub.Records))
	rows := make(map[string]store.AuditRecord, len(sub.Records))
	ids := make([]string, 0, len(sub.Records))
	for i, raw := range sub.Records {
		rec, err := Parse(raw, string(sub.Tenant), index(i), in.limits)
		if err != nil {
			return Receipt{}, err
		}
		row, err := rec.Row(sub.ProxyID)
		if err != nil {
			return Receipt{}, &MalformedError{Index: index(i), RecordID: rec.RecordID, Reason: err.Error()}
		}
		parsed = append(parsed, rec)
		rows[rec.RecordID] = row
		ids = append(ids, rec.RecordID)
	}

	// The captures go in FIRST, before the record that describes them is
	// chained. Ordering it the other way round would leave a window where a
	// verified record names a capture that is not there yet, and a
	// verification running in that window would report a break that is not
	// one. A capture whose record is then refused as a duplicate is an
	// orphaned blob, which is the cheaper of the two failures and is
	// idempotent on the record id anyway.
	for _, rec := range parsed {
		if len(rec.Capture) == 0 {
			continue
		}
		if err := in.store.Audit().PutCapture(ctx, sub.Tenant, rec.RecordID, rec.Capture); err != nil {
			return Receipt{}, err
		}
	}

	stream := StreamFor(sub.ProxyID)
	var written []string
	stored, err := in.store.Audit().AppendChain(ctx, sub.Tenant, stream, ids,
		func(head store.AuditRecord, fresh []string) ([]store.AuditRecord, error) {
			written = written[:0]
			out := make([]store.AuditRecord, 0, len(fresh))
			prev := head
			for _, id := range fresh {
				row := Position(string(sub.Tenant), stream, prev, rows[id])
				out = append(out, row)
				written = append(written, id)
				prev = row
			}
			return out, nil
		})
	if err != nil {
		return Receipt{}, err
	}

	return Receipt{
		Stored:     stored,
		Duplicates: len(sub.Records) - stored,
		RecordIDs:  written,
	}, nil
}

// IngestBatch implements the contract's batch path.
func (in *Ingester) IngestBatch(ctx context.Context, tenant store.Tenant, proxyID string, req *contract.LogBatchRequest) (*contract.LogBatchResponse, error) {
	rcpt, err := in.Ingest(ctx, Submission{Tenant: tenant, ProxyID: proxyID, Records: req.Records})
	if err != nil {
		return nil, err
	}
	if rcpt.Duplicates > 0 {
		in.log.InfoContext(ctx, "an audit batch carried records already stored",
			"event", "audit_batch_deduplicated",
			"proxy_id", proxyID,
			"submitted", len(req.Records),
			"stored", rcpt.Stored,
			"duplicates", rcpt.Duplicates,
		)
	}
	return &contract.LogBatchResponse{Accepted: int32(rcpt.Stored)}, nil
}

// IngestPriority implements the contract's priority path.
//
// `accepted` is true on a 200 and the record IS durable by then. A resend of a
// record already stored is still `accepted: true` with the same receipt: the
// proxy asked whether the event is recorded, and it is. Answering false would
// make a retry after a lost response look like a failure and drive the proxy
// to act as though a security event had gone unrecorded when it had not.
func (in *Ingester) IngestPriority(ctx context.Context, tenant store.Tenant, proxyID string, req *contract.LogPriorityRequest) (*contract.LogPriorityResponse, error) {
	if _, err := in.Ingest(ctx, Submission{
		Tenant:   tenant,
		ProxyID:  proxyID,
		Records:  []contract.LogRecord{req.Record},
		Priority: true,
	}); err != nil {
		return nil, err
	}
	return &contract.LogPriorityResponse{Accepted: true, ReceiptID: req.Record.RecordID}, nil
}
