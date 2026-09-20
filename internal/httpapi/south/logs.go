// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south

import (
	"context"
	"errors"
	"net/http"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
)

// The two log ingest paths (PLAN §4, §7, M8).
//
// They share one function underneath because the durability guarantee is the
// same on both: [audit.Ingester.Ingest] returns only after the commit, and
// there is no buffer, queue or goroutine between the request and the write.
// The difference the contract draws is the status code and the shape of the
// answer — `202` and a count, against `200` and a boolean whose truth is the
// whole point.

// LogIngest is the seam the listener writes through. It is an interface rather
// than the concrete ingester so that a test can drive the HTTP layer without a
// database, and so that this package keeps knowing nothing about the chain.
type LogIngest interface {
	IngestBatch(ctx context.Context, tenant store.Tenant, proxyID string, req *contract.LogBatchRequest) (*contract.LogBatchResponse, error)
	IngestPriority(ctx context.Context, tenant store.Tenant, proxyID string, req *contract.LogPriorityRequest) (*contract.LogPriorityResponse, error)
}

func (h handlers) ingestLogBatch(ctx context.Context, r *http.Request) (any, error) {
	var req contract.LogBatchRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	return h.IngestBatch(ctx, &req)
}

// IngestBatch implements contract.LogIngester.
//
// The count it answers with is the number STORED, never the number received.
// The contract documents "fewer than sent means the rest were duplicates; the
// proxy may drop them", so the count is the only way a proxy draining a disk
// buffer can see its replay being de-duplicated — and it is also why a batch
// carrying one malformed record fails whole rather than being partly accepted:
// a shortfall caused by anything other than duplicates would tell the proxy to
// discard records this server never stored.
func (h handlers) IngestBatch(ctx context.Context, req *contract.LogBatchRequest) (*contract.LogBatchResponse, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return nil, errNoCaller
	}
	resp, err := h.s.logs.IngestBatch(ctx, caller.Tenant, caller.ProxyID, req)
	if err != nil {
		return nil, h.logIngestError(ctx, contract.PathLogsBatch, caller, err)
	}
	return resp, nil
}

func (h handlers) ingestLogPriority(ctx context.Context, r *http.Request) (any, error) {
	var req contract.LogPriorityRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	return h.IngestPriority(ctx, &req)
}

// IngestPriority implements contract.LogIngester.
//
// THE ACK MEANS DURABLE. The proxy acts on a critical security event — killing
// a session, most often — knowing this server recorded it, so a `200` written
// before the row is committed turns that guarantee into a lie that only
// surfaces after an incident. The write is therefore synchronous and a write
// that did not land is a `5xx`, never a cheerful `accepted: true`. It is the
// same discipline `/v1/capabilities/report` keeps and for the same reason.
func (h handlers) IngestPriority(ctx context.Context, req *contract.LogPriorityRequest) (*contract.LogPriorityResponse, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return nil, errNoCaller
	}
	resp, err := h.s.logs.IngestPriority(ctx, caller.Tenant, caller.ProxyID, req)
	if err != nil {
		return nil, h.logIngestError(ctx, contract.PathLogsPriority, caller, err)
	}
	return resp, nil
}

// logIngestError classifies an ingest failure.
//
// A malformed record and a tenant mismatch are both `400` and both say what
// was wrong, because the caller is a proxy with a bug or a misconfiguration
// and an opaque refusal gives an operator nothing to act on. NEITHER IS A
// `401` (M11): this server has not denied anybody access, and answering
// `unauthorized` to a proxy shipping a record with a bad timestamp would send
// somebody to look at credentials.
//
// The tenant mismatch is logged at WARN and separately from the malformed
// case, because it is the one that means two customers' configurations have
// been crossed — quietly filing the record under the enrolled tenant would
// hide exactly that.
func (h handlers) logIngestError(ctx context.Context, path string, caller fleet.ProxyCaller, err error) error {
	var mismatch *audit.TenantMismatchError
	if errors.As(err, &mismatch) {
		h.s.log.WarnContext(ctx, "an audit record named a tenant its proxy is not enrolled in",
			"event", "audit_tenant_mismatch",
			"path", path,
			"proxy_id", caller.ProxyID,
			"enrolled_tenant", string(caller.Tenant),
			"claimed_tenant", mismatch.Claimed,
			"record_id", mismatch.RecordID,
		)
		return invalid(mismatch.Error())
	}

	var malformed *audit.MalformedError
	if errors.As(err, &malformed) {
		return invalid(malformed.Error())
	}
	if errors.Is(err, audit.ErrEmptyBatch) {
		return invalid("a batch must carry at least one record")
	}
	return err
}
