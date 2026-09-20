// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"fmt"

	"github.com/hoplock/control/internal/store"
)

// Verifier walks a chain and reports the first break.
//
// IT VERIFIES ONE TENANT AT A TIME, and reports which tenant it verified. A
// verifier that can only check the whole store is not usable by a customer
// entitled to see only their own part of it (M18) — and "take your records
// with you and check them" is the property the per-tenant chain exists to give.
type Verifier struct {
	store *store.Store
	// page is how many records are read per round trip. A chain can be
	// arbitrarily long and the verifier holds two records at a time, so the
	// page is about round trips rather than memory.
	page int
	// captures re-digests the session capture bytes as well as the record.
	// It is off by default because it reads every captured byte in the
	// store, which is the one thing a routine verification should not do
	// by accident.
	captures bool
}

// VerifierOption configures a Verifier.
type VerifierOption func(*Verifier)

// WithPageSize overrides how many records are read per round trip.
func WithPageSize(n int) VerifierOption {
	return func(v *Verifier) {
		if n > 0 {
			v.page = n
		}
	}
}

// WithCaptureVerification re-digests stored session captures against the
// digest inside each hashed record.
func WithCaptureVerification(on bool) VerifierOption {
	return func(v *Verifier) { v.captures = on }
}

// NewVerifier builds a verifier over a store.
func NewVerifier(st *store.Store, opts ...VerifierOption) *Verifier {
	v := &Verifier{store: st, page: 500}
	for _, o := range opts {
		o(v)
	}
	return v
}

// Result is what one verification found.
type Result struct {
	Tenant string
	// Streams is one entry per chain, in the order verified.
	Streams []StreamResult
}

// OK reports whether every stream verified.
func (r Result) OK() bool {
	for _, s := range r.Streams {
		if s.Break != nil {
			return false
		}
	}
	return true
}

// Records is how many records were walked across every stream.
func (r Result) Records() int64 {
	var n int64
	for _, s := range r.Streams {
		n += s.Records
	}
	return n
}

// StreamResult is one chain's outcome.
type StreamResult struct {
	Stream string
	// Records is how many were walked before the answer was reached.
	Records int64
	// Head is the hash the chain ends on, empty when it broke. It is what a
	// departing customer records so a later verification can prove the
	// chain has not been rewritten since.
	Head string
	// Break is nil when the chain verified.
	Break *Break
}

// VerifyTenant walks every stream a tenant has.
func (v *Verifier) VerifyTenant(ctx context.Context, tenant store.Tenant) (Result, error) {
	streams, err := v.store.Audit().Streams(ctx, tenant)
	if err != nil {
		return Result{}, err
	}
	out := Result{Tenant: string(tenant)}
	for _, s := range streams {
		sr, err := v.VerifyStream(ctx, tenant, s)
		if err != nil {
			return Result{}, err
		}
		out.Streams = append(out.Streams, sr)
	}
	return out, nil
}

// VerifyStream walks one chain, in sequence order, from the beginning.
//
// It stops at the FIRST break. A verifier that carried on would report every
// later record as broken too — they all chain onto the altered one — and bury
// the finding that matters in a list of consequences.
func (v *Verifier) VerifyStream(ctx context.Context, tenant store.Tenant, stream string) (StreamResult, error) {
	out := StreamResult{Stream: stream}

	var (
		prev     store.AuditRecord
		afterSeq int64
		expected int64 = 1
	)
	for {
		page, err := v.store.Audit().Chain(ctx, tenant, stream, afterSeq, v.page)
		if err != nil {
			return StreamResult{}, err
		}
		if len(page) == 0 {
			return out, nil
		}
		for _, rec := range page {
			if b := verifyLink(string(tenant), stream, prev, rec, expected); b != nil {
				out.Break = b
				return out, nil
			}
			if v.captures && rec.CaptureSHA256 != "" {
				if b, err := v.verifyCapture(ctx, tenant, stream, rec); err != nil {
					return StreamResult{}, err
				} else if b != nil {
					out.Break = b
					return out, nil
				}
			}
			prev = rec
			out.Records++
			out.Head = rec.Hash
			afterSeq = rec.ChainSeq
			expected = rec.ChainSeq + 1
		}
	}
}

func (v *Verifier) verifyCapture(ctx context.Context, tenant store.Tenant, stream string, rec store.AuditRecord) (*Break, error) {
	bytes, err := v.store.Audit().Capture(ctx, tenant, rec.RecordID)
	if err != nil {
		if store.IsNotFound(err) {
			return &Break{
				Tenant: string(tenant), Stream: stream, Kind: BreakCaptureMismatch,
				ChainSeq: rec.ChainSeq, RecordID: rec.RecordID,
				Want:   rec.CaptureSHA256,
				Detail: "the record describes a session capture and none is stored",
			}, nil
		}
		return nil, err
	}
	if got := digest(bytes); got != rec.CaptureSHA256 {
		return &Break{
			Tenant: string(tenant), Stream: stream, Kind: BreakCaptureMismatch,
			ChainSeq: rec.ChainSeq, RecordID: rec.RecordID,
			Want: rec.CaptureSHA256, Got: got,
			Detail: fmt.Sprintf("the stored capture is %d bytes and does not match the digest in the record", len(bytes)),
		}, nil
	}
	return nil, nil
}
