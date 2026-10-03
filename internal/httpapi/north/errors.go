// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"encoding/json"
	"net/http"

	"github.com/hoplock/control/internal/accessctx"
)

// Errors on this surface are MACHINE-READABLE (M21).
//
// Every error carries a stable `code`, typed `parameters`, an English `message`
// and the correlation id. The console (0016) is the only layer that localises,
// and it can only do that if the sentence is assembled there — so an API that
// answers with prose alone makes a localisable console impossible. 0014 owns the
// full code registry for this surface and extends what is here; the shape is
// fixed now because the first routes are served now, and a second envelope added
// later would be a second envelope forever.
//
// A code is an identifier, not a summary: reword a message whenever it helps,
// never reuse a code for a different condition.

// Error is the response body every failure carries.
type Error struct {
	// Code is the stable identifier a client depends on.
	Code string `json:"code"`
	// Message is English, for `curl`, CI logs and a console that has no
	// catalogue entry yet.
	Message string `json:"message"`
	// Parameters are named and typed, so a console can build a sentence with
	// them in its own word order.
	Parameters map[string]any `json:"parameters,omitempty"`
	// CorrelationID is the id the request was logged under (M11).
	CorrelationID string `json:"correlation_id"`
}

// The codes this surface produces. Adding one is deliberate.
const (
	// CodeUnauthenticated is a missing, malformed, expired or revoked
	// credential. All four answer the same thing, on purpose: telling them
	// apart makes this listener an oracle.
	CodeUnauthenticated = "unauthenticated"
	// CodeForbidden is a live principal without the permission the route
	// needs, in the tenant the request resolved to.
	CodeForbidden = "forbidden"
	// CodeTenantOutOfScope is the M18 refusal: a caller named a tenant
	// outside its scope. It is a DIFFERENT code from CodeForbidden because
	// the two are different operator problems — one is a missing role, the
	// other is a credential being used against the wrong estate.
	CodeTenantOutOfScope = "tenant_out_of_scope"
	// CodeTenantAmbiguous is a principal scoped to several tenants making a
	// request that named none. It is never defaulted: a default would
	// silently pick one.
	CodeTenantAmbiguous = "tenant_ambiguous"
	// CodeNotFound is a route or a resource that is not there.
	CodeNotFound = "not_found"
	// CodeInvalidRequest is a body or a parameter this server will not act
	// on.
	CodeInvalidRequest = "invalid_request"
	// CodeLoginRefused is a federation or break-glass login this server
	// decided to refuse.
	CodeLoginRefused = "login_refused"
	// CodeInternal is everything else, and it is ALWAYS a 5xx: a database
	// failure, a timeout, a panic (M11).
	CodeInternal = "internal"
	// CodeMethodNotAllowed is the wrong verb on a real path.
	CodeMethodNotAllowed = "method_not_allowed"
	// CodeCANotConfigured is a certificate-authority route on a deployment
	// with no key-encryption key. It is its own code rather than an internal
	// error because it is a CONFIGURATION answer, and the message names the
	// key to set.
	CodeCANotConfigured = "ca_not_configured"

	// CodeGrantRequestDenied is a grant a registered approval workflow
	// refused (ext.GrantWorkflow, 0012). It is a DECISION, answered 403 like
	// the other refusals, and its own code because "you may not hold this"
	// and "your credential may not ask" are different conversations.
	CodeGrantRequestDenied = "grant_request_denied"
	// CodeGrantWorkflowFailed is a workflow that answered with something no
	// retry would change: the request is closed as failed.
	CodeGrantWorkflowFailed = "grant_workflow_failed"
	// CodeGrantRequestNotPending is a cancel of a request already decided.
	CodeGrantRequestNotPending = "grant_request_not_pending"
	// CodeGrantRevocationUndelivered is a grant that IS revoked — no new
	// session can use it, and the revocation is recorded — whose
	// session_kill could not be published. Revoking it again re-sends.
	CodeGrantRevocationUndelivered = "grant_revocation_undelivered"

	// The push receiver's refusals (M16, 0013) are accessctx's codes,
	// answered verbatim so an integration's operator and a SIEM rule read
	// one vocabulary. Each is a DECISION about the push — never an outage,
	// which is CodeInternal.
	CodeProviderNotFound    = accessctx.CodeProviderNotFound
	CodePushNotSupported    = accessctx.CodePushNotSupported
	CodeBindingNotFound     = accessctx.CodeBindingNotFound
	CodeBindingDisabled     = accessctx.CodeBindingDisabled
	CodePushNotPermitted    = accessctx.CodePushNotPermitted
	CodePushNotAccepted     = accessctx.CodePushNotAccepted
	CodeRateLimited         = accessctx.CodeRateLimited
	CodeAssertionMalformed  = accessctx.CodeAssertionMalformed
	CodeOutsideScope        = accessctx.CodeOutsideScope
	CodeAssertionConflict   = accessctx.CodeAssertionConflict
	CodeAssertionStale      = accessctx.CodeAssertionStale
	CodeAssertionFromFuture = accessctx.CodeAssertionFromFuture
	CodeWindowClosed        = accessctx.CodeWindowClosed
	// CodePayloadTooLarge is a push body past access_context.max_push_bytes.
	CodePayloadTooLarge = "payload_too_large"
)

// AllCodes is every code this surface defines, so a test can assert that adding
// one is deliberate and that none is reused for a different condition.
var AllCodes = []string{
	CodeUnauthenticated, CodeForbidden, CodeTenantOutOfScope, CodeTenantAmbiguous,
	CodeNotFound, CodeInvalidRequest, CodeLoginRefused, CodeInternal, CodeMethodNotAllowed,
	CodeCANotConfigured,
	CodeGrantRequestDenied, CodeGrantWorkflowFailed, CodeGrantRequestNotPending,
	CodeGrantRevocationUndelivered,
	CodeProviderNotFound, CodePushNotSupported, CodeBindingNotFound, CodeBindingDisabled,
	CodePushNotPermitted, CodePushNotAccepted, CodeRateLimited, CodeAssertionMalformed,
	CodeOutsideScope, CodeAssertionConflict, CodeAssertionStale, CodeAssertionFromFuture,
	CodeWindowClosed, CodePayloadTooLarge,
}

// writeJSON is the only writer in this package, so the content type cannot
// drift between the success and error paths.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// A write failure here is a client that went away. There is nothing to
	// say to it and nothing to recover.
	_ = json.NewEncoder(w).Encode(body)
}

// writeError renders a classified failure.
func writeError(w http.ResponseWriter, status int, e Error) {
	writeJSON(w, status, e)
}
