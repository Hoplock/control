// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"context"
	"errors"
	"fmt"
)

// The server-side interfaces the contract implies: one method per operation,
// named for the document's operationId. The south-bound HTTP layer
// (internal/httpapi/south, phases 0007 onward) is the only implementer of the
// transport; these interfaces are what it routes to, so the endpoints can be
// built and graded one at a time.
//
// They are split rather than gathered into one fat interface because the
// contract's own seams are real: a log ingester needs no policy engine, a uid
// cursor needs no identity, and a phase that builds one should not have to stub
// the other nine.

// Authenticator owns the user→proxy authentication conversation, including MFA.
// The proxy never contacts an MFA provider itself.
type Authenticator interface {
	AuthenticateCert(context.Context, *AuthenticateCertRequest) (*AuthenticateResponse, error)
	AuthenticatePassword(context.Context, *AuthenticatePasswordRequest) (*AuthenticateResponse, error)
	PollMFA(context.Context, *MFAPollRequest) (*AuthenticateResponse, error)
}

// Authorizer evaluates policy for the asking hop and returns the
// whole-connection snapshot.
//
// It answers for the hop that asked — conn.proxy_id plus conn.hop_trail — and
// it must answer within the vocabulary the request declared: a server holding
// policy it cannot express within that version says so with a 5xx rather than
// sending fields that will be refused (PLAN §4).
type Authorizer interface {
	Authorize(context.Context, *AuthorizeRequest) (*AuthorizeResponse, error)
}

// HostKeyReporter records a reported target host key and answers with the trust
// decision, plus an optional cache hint.
type HostKeyReporter interface {
	ReportHostKey(context.Context, *HostKeyReportRequest) (*HostKeyReportResponse, error)
}

// CapabilityReporter records what one target can enforce, as the proxy found it
// by probing. A report is an observation: it constrains what a policy author may
// choose and grants nothing.
type CapabilityReporter interface {
	ReportCapabilities(context.Context, *CapabilityReportRequest) (*CapabilityReportResponse, error)
}

// UIDLeaser grants a proxy an exclusive block of ephemeral uids for one target,
// out of a per-target cursor that ONLY EVER ADVANCES.
//
// The invariant is the entire endpoint: a uid inside a granted block is never
// inside any other grant, for this proxy or any other, ever again — whether the
// block was used, abandoned, or allowed to expire. There is therefore no
// release call and nothing to reclaim. An implementation that cannot grant a
// non-empty block returns ErrUIDRangeExhausted, which the transport answers 409.
type UIDLeaser interface {
	LeaseUIDs(context.Context, *UIDLeaseRequest) (*UIDLeaseResponse, error)
}

// LogIngester takes session log records on the two paths the contract defines.
//
// IngestBatch is the throughput path and de-duplicates on record_id, because a
// proxy draining a disk buffer will resend. IngestPriority's acknowledgement
// means the record is DURABLE: acking before the write lands turns the
// guarantee the proxy acts on into a lie that only shows up after an incident.
type LogIngester interface {
	IngestBatch(context.Context, *LogBatchRequest) (*LogBatchResponse, error)
	IngestPriority(context.Context, *LogPriorityRequest) (*LogPriorityResponse, error)
}

// EventPublisher is the revocation stream: a long-lived NDJSON subscription per
// proxy id.
//
// Emit is called for every event the subscription should deliver, in order, and
// returns the first error that ends the stream. The implementation MUST emit
// heartbeats at a steady interval — a stream that goes silent is
// indistinguishable from a healthy idle one — and MUST decide gap recovery for
// itself: either replay everything after lastEventID in order, or emit a
// `resync` as the FIRST event and nothing older. An empty lastEventID is a
// fresh subscription: start from now and replay nothing.
type EventPublisher interface {
	Subscribe(ctx context.Context, proxyID, lastEventID string, emit func(*RevocationEvent) error) error
}

// Southbound is everything the contract's ten operations need, for a server
// that implements all of them. A phase that implements one embeds the narrow
// interface instead.
type Southbound interface {
	Authenticator
	Authorizer
	HostKeyReporter
	CapabilityReporter
	UIDLeaser
	LogIngester
	EventPublisher
}

// The classified failures the transport turns into status codes. M11 is the
// whole reason this is an enumerated set rather than "return an error": a
// database timeout, a compile error, or a panic must never surface as 401,
// because the proxy will faithfully tell a user "access denied" and the
// operator will spend the outage debugging permissions.
var (
	// ErrDenied is a DECISION: the credential was rejected, or the identity may
	// not reach the target. 401, and nothing else may produce one.
	ErrDenied = errors.New("denied")
	// ErrInvalidRequest is a malformed or invalid request body. 400.
	ErrInvalidRequest = errors.New("invalid request")
	// ErrUIDRangeExhausted is the uid cursor having reached the top of its
	// range. 409, and never a 200 carrying an empty or inverted block.
	ErrUIDRangeExhausted = errors.New("uid range exhausted")
	// ErrPolicyVersionUnsupported is policy this server cannot express within
	// the vocabulary the proxy declared. 5xx, on purpose: it is a rollout
	// problem, not a statement about the user.
	ErrPolicyVersionUnsupported = errors.New("policy version unsupported")
)

// StatusError carries a classified failure together with the envelope the
// contract specifies, so a handler names the answer rather than leaving the
// transport to guess it.
type StatusError struct {
	// Class is one of the sentinels above, or nil for an unclassified failure —
	// which is a 5xx, because everything that is not a decision is an outage.
	Class error
	// Code is the stable machine-readable code on the envelope.
	Code string
	// Message is human-readable and MUST be safe to disclose: no credentials,
	// no policy internals, no other users.
	Message string
}

func (e *StatusError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *StatusError) Unwrap() error { return e.Class }

// Denied builds the one error that becomes a 401. Spelling it out at each call
// site is the point: a deny is a decision somebody made on purpose.
func Denied(message string) *StatusError {
	return &StatusError{Class: ErrDenied, Code: ErrCodeUnauthorized, Message: message}
}

// Invalid builds the 400 answer for a malformed request.
func Invalid(message string) *StatusError {
	return &StatusError{Class: ErrInvalidRequest, Code: ErrCodeInvalidRequest, Message: message}
}

// Exhausted builds the 409 answer for a uid cursor at the top of its range.
func Exhausted(message string) *StatusError {
	return &StatusError{Class: ErrUIDRangeExhausted, Code: ErrCodeExhausted, Message: message}
}

// VersionUnsupported builds the 5xx answer for policy this server cannot express
// within the declared vocabulary.
func VersionUnsupported(message string) *StatusError {
	return &StatusError{Class: ErrPolicyVersionUnsupported, Code: ErrCodeVersionUnsupported, Message: message}
}

// Envelope renders an error as the wire envelope the document specifies.
func (e *StatusError) Envelope() ErrorResponse {
	code, msg := e.Code, e.Message
	if code == "" {
		code = ErrCodeInternal
	}
	if msg == "" {
		msg = "the server failed to process an otherwise valid request"
	}
	return ErrorResponse{Error: ErrorBody{Code: code, Message: msg}}
}
