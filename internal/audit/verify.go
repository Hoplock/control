// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	// keys checks retention seals (0024). Nil is a verifier with no key
	// store, which reports every seal it meets as one it cannot check.
	keys *RetentionKeys
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

// WithRetentionKeys sets the key store retention seals are checked against:
// the one the server signs with, built from the same configuration.
func WithRetentionKeys(keys *RetentionKeys) VerifierOption {
	return func(v *Verifier) { v.keys = keys }
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
	// RetainedThrough is the sequence a retention pass deleted through, zero
	// when none has: the walk started at the next one, chained onto the hash
	// the pass recorded, rather than at sequence 1 (PLAN §7).
	RetainedThrough int64
	// CapturesRetained counts captures whose bytes a retention pass removed:
	// gone deliberately, which is not changed. Counted only when captures are
	// verified.
	CapturesRetained int64
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

// VerifyStream walks one chain, in sequence order, from where it starts: the
// beginning, or — after a retention pass deleted a prefix — the record after the
// one the pass recorded, chained onto that record's hash. Resuming there is what
// lets a lawful deletion verify while a deletion from the middle still breaks.
//
// The marker is checked before anything is walked (0024): one whose seal does
// not hold is a BreakRetentionMarker at the marker, because a deletion made to
// look lawful is exactly a marker a writer of the database wrote.
//
// It stops at the FIRST break. A verifier that carried on would report every
// later record as broken too — they all chain onto the altered one — and bury
// the finding that matters in a list of consequences.
func (v *Verifier) VerifyStream(ctx context.Context, tenant store.Tenant, stream string) (StreamResult, error) {
	var marker *store.AuditRetention
	switch got, err := v.store.Audit().Retention(ctx, tenant, stream); {
	case err == nil:
		marker = &got
	case !store.IsNotFound(err):
		return StreamResult{}, err
	}
	if marker != nil {
		problem, err := v.keys.check(ctx, tenant, sealOf(*marker),
			RetentionMessage(tenant, stream, marker.ThroughSeq, marker.ThroughHash))
		if err != nil {
			return StreamResult{}, err
		}
		if problem != "" {
			return StreamResult{Stream: stream, RetainedThrough: marker.ThroughSeq, Break: &Break{
				Tenant: string(tenant), Stream: stream, Kind: BreakRetentionMarker,
				ChainSeq: marker.ThroughSeq,
				Detail: fmt.Sprintf("the retention marker through %d (hash %s) does not verify: %s",
					marker.ThroughSeq, marker.ThroughHash, problem),
			}}, nil
		}
	}
	return v.walk(ctx, tenant, stream, marker)
}

// walk verifies a chain from a marker taken as given (nil: from sequence 1).
// Only VerifyStream starts one, after checking the marker's seal.
func (v *Verifier) walk(ctx context.Context, tenant store.Tenant, stream string, marker *store.AuditRetention) (StreamResult, error) {
	out := StreamResult{Stream: stream}

	var (
		prev     store.AuditRecord
		afterSeq int64
		expected int64 = 1
		mark     captureMark
	)
	if marker != nil {
		prev = store.AuditRecord{Hash: marker.ThroughHash}
		afterSeq, expected = marker.ThroughSeq, marker.ThroughSeq+1
		out.RetainedThrough = marker.ThroughSeq
	}
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
				b, err := v.verifyCapture(ctx, tenant, stream, rec)
				if store.IsCaptureRetained(err) {
					b, err = v.verifyRetainedCapture(ctx, tenant, stream, rec, &mark)
					if err == nil && b == nil {
						out.CapturesRetained++
					}
				}
				if err != nil {
					return StreamResult{}, err
				}
				if b != nil {
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

// captureMark is the tenant's capture watermark as one walk found it: read and
// checked once, at the first retained capture.
type captureMark struct {
	loaded  bool
	before  time.Time
	problem string
}

// verifyRetainedCapture decides whether a capture whose bytes are gone is gone
// by a signed pass (0024): the tenant's watermark must carry a seal that holds,
// and the record's own timestamp — read from the hashed body, never from the
// derived column a writer could move — must be before it.
func (v *Verifier) verifyRetainedCapture(ctx context.Context, tenant store.Tenant, stream string, rec store.AuditRecord, mark *captureMark) (*Break, error) {
	if !mark.loaded {
		switch wm, err := v.store.Audit().CaptureRetention(ctx, tenant); {
		case store.IsNotFound(err):
			mark.problem = "no signed capture pass has reached this tenant"
		case err != nil:
			return nil, err
		default:
			problem, err := v.keys.check(ctx, tenant,
				store.RetentionSeal{KeyName: wm.KeyName, Signature: wm.Signature}, CaptureRetentionMessage(tenant, wm.Before))
			if err != nil {
				return nil, err
			}
			if problem != "" {
				mark.problem = "the capture retention watermark does not verify: " + problem
			}
			mark.before = wm.Before
		}
		mark.loaded = true
	}
	brk := func(detail string) *Break {
		return &Break{
			Tenant: string(tenant), Stream: stream, Kind: BreakRetentionMarker,
			ChainSeq: rec.ChainSeq, RecordID: rec.RecordID, Want: rec.CaptureSHA256,
			Detail: "the session capture is marked removed by retention, and " + detail,
		}
	}
	if mark.problem != "" {
		return brk(mark.problem), nil
	}
	var body struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(rec.Body), &body); err != nil {
		return brk("its record's timestamp cannot be read"), nil
	}
	at, err := time.Parse(time.RFC3339Nano, body.Timestamp)
	if err != nil {
		return brk("its record's timestamp cannot be read"), nil
	}
	if !at.Before(mark.before) {
		return brk(fmt.Sprintf("its record was made at %s, which no signed capture pass reached (the watermark is %s)",
			at.UTC().Format(time.RFC3339Nano), mark.before.UTC().Format(time.RFC3339Nano))), nil
	}
	return nil, nil
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
