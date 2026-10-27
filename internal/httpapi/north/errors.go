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
// and the correlation id. The console (0044) is the only layer that localises,
// and it can only do that if the sentence is assembled there — so an API that
// answers with prose alone makes a localisable console impossible. 0019 owns the
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
	// CodeSubjectRequired is a credential with no person behind it — a
	// machine token — on a route about the caller's own records (AccessSelf,
	// 0016). A token has no "own" requests, and one it raised for "itself"
	// would name nobody.
	CodeSubjectRequired = "subject_required"
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
	// CodeRetentionKeyNotConfigured is a retention pass on a deployment with
	// no key store to sign it (0024): no key-encryption key and no
	// registered ext.KeyStore. It is a configuration answer, like
	// ca_not_configured, and never a 401 (M11): nothing is deleted, because
	// a marker nothing signed is one a writer of the database could have
	// written.
	CodeRetentionKeyNotConfigured = "retention_key_not_configured"
	// CodeRetentionUnverified is a retention pass refused because where it
	// would start does not verify (0024): the stream's marker or the
	// tenant's capture watermark carries no seal that holds, or the stream
	// no longer starts where its marker says. A new seal over that start
	// would launder it, so the operator verifies the stream first.
	CodeRetentionUnverified = "retention_unverified"
	// CodeRetentionNotArchived is a retention pass by sequence beyond what
	// the registered archive has confirmed (0025): Control never deletes a
	// record, or a capture of one, the archive does not hold. It is a
	// refusal on purpose, answered 409 with the stream and how far it is
	// archived, never a 5xx and never a 401 (M11); nothing was deleted,
	// sealed or recorded. A pass by instant is held back instead.
	CodeRetentionNotArchived = "retention_not_archived"

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
	// CodeGrantWorkflowNotRegistered is a requester's own request on a
	// deployment with no approval workflow registered (0016). Nothing would
	// decide it, and a request nobody decides is a self-grant, so nothing is
	// stored. It is a 403 and a refusal made on purpose, like 0013's
	// push_not_supported, never a 5xx: asking again gets the same answer
	// until a workflow is registered, and a deployment with none is working
	// as designed (M15). It is not grant_request_denied, because no workflow
	// said no.
	CodeGrantWorkflowNotRegistered = "grant_workflow_not_registered"
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

	// The policy lifecycle (0019).
	//
	// CodePolicyInvalid is a bundle the compiler refused. Its parameters
	// carry the rejections verbatim — code, rule, line, parameters and the
	// compiler's own text — because what to do instead is in them.
	CodePolicyInvalid = "policy_invalid"
	// CodeFindingsNotAccepted is an activation with warnings the author has
	// not accepted. Its parameters list them, each with the id to accept:
	// that is the override, and it is never a refusal of the policy.
	CodeFindingsNotAccepted = "findings_not_accepted"
	// CodePolicyBlocked is an activation a registered validator's blocking
	// finding stopped. No acceptance moves it.
	CodePolicyBlocked = "policy_blocked"
	// CodeDecisionUnexplainable is a stored decision that does not carry its
	// own story (M4). It is an integrity fault in this server's records and
	// answered as an outage, loudly, never as a partial story.
	CodeDecisionUnexplainable = "decision_unexplainable"
	// CodeConfigKeyNotFleetOwned and CodeConfigValueInvalid are a fleet
	// configuration publish naming a setting the proxy keeps in its
	// bootstrap file, or a value not of its key's shape (proxy D18).
	CodeConfigKeyNotFleetOwned = "config_key_not_fleet_owned"
	CodeConfigValueInvalid     = "config_value_invalid"
	// CodeHostKeyUnresolved is a host-key withdrawal this server cannot
	// resolve to the key it issued: no such recorded decision, or one
	// recorded before keys were issued, whose only honest withdrawal is a
	// resync. Nothing was published.
	CodeHostKeyUnresolved = "host_key_unresolved"
	// CodeUnavailable is a request this server cannot serve right now and
	// will on a retry: the revocation stream is shutting down.
	CodeUnavailable = "unavailable"

	// The standing denials (0039, M28).
	//
	// CodeTargetQuarantined is a delete of a target that is quarantined:
	// deleting the row would lift the quarantine silently, so it is refused,
	// answered 409, until the quarantine is released.
	CodeTargetQuarantined = "target_quarantined"
	// CodeDenialUndelivered is a lock or a quarantine that IS in force and
	// recorded, whose cached-decision invalidation or session kill could not
	// be published. Asking for it again re-sends.
	CodeDenialUndelivered = "standing_denial_undelivered"
)

// AllCodes is every code this surface defines, so a test can assert that adding
// one is deliberate and that none is reused for a different condition.
var AllCodes = []string{
	CodeUnauthenticated, CodeForbidden, CodeTenantOutOfScope, CodeTenantAmbiguous,
	CodeSubjectRequired,
	CodeNotFound, CodeInvalidRequest, CodeLoginRefused, CodeInternal, CodeMethodNotAllowed,
	CodeCANotConfigured, CodeRetentionKeyNotConfigured, CodeRetentionUnverified, CodeRetentionNotArchived,
	CodeGrantRequestDenied, CodeGrantWorkflowFailed, CodeGrantRequestNotPending,
	CodeGrantRevocationUndelivered, CodeGrantWorkflowNotRegistered,
	CodeProviderNotFound, CodePushNotSupported, CodeBindingNotFound, CodeBindingDisabled,
	CodePushNotPermitted, CodePushNotAccepted, CodeRateLimited, CodeAssertionMalformed,
	CodeOutsideScope, CodeAssertionConflict, CodeAssertionStale, CodeAssertionFromFuture,
	CodeWindowClosed, CodePayloadTooLarge,
	CodePolicyInvalid, CodeFindingsNotAccepted, CodePolicyBlocked, CodeDecisionUnexplainable,
	CodeConfigKeyNotFleetOwned, CodeConfigValueInvalid, CodeHostKeyUnresolved, CodeUnavailable,
	CodeTargetQuarantined, CodeDenialUndelivered,
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
