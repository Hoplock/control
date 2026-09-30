// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"errors"
	"fmt"
)

// ValidationError is a grant, request or revocation this server will not act
// on. It names the field and a stable problem code rather than a sentence,
// because the console holds the only string catalogue (M21) and builds the
// sentence itself.
type ValidationError struct {
	// Field is the request field at fault, in the API's own spelling.
	Field string
	// Problem is one of the Problem constants.
	Problem string
	// Limit is the bound the value exceeded, when Problem is one of the
	// "too" problems.
	Limit string
}

// Error implements error.
func (e *ValidationError) Error() string {
	if e.Limit != "" {
		return fmt.Sprintf("access: %s is %s (limit %s)", e.Field, e.Problem, e.Limit)
	}
	return fmt.Sprintf("access: %s is %s", e.Field, e.Problem)
}

// The problems a ValidationError names. Stable codes: reword the message
// freely, never reuse a code for a different condition.
const (
	// ProblemRequired is a field that must be present and is not.
	ProblemRequired = "required"
	// ProblemInvalid is a value of the wrong shape.
	ProblemInvalid = "invalid"
	// ProblemTooLong is a value longer than Limit.
	ProblemTooLong = "too_long"
	// ProblemTooMany is a list with more entries than Limit.
	ProblemTooMany = "too_many"
	// ProblemAmbiguous is two fields that say the same thing both set: an
	// expiry and a duration.
	ProblemAmbiguous = "ambiguous"
	// ProblemNotAfterStart is a window that ends at or before it begins.
	ProblemNotAfterStart = "not_after_start"
	// ProblemExceedsMaximum is a window longer than the server's ceiling,
	// named in Limit.
	ProblemExceedsMaximum = "exceeds_maximum"
)

// ErrNotPending is a request that has already been decided. Cancelling one is
// refused rather than ignored, because an operator who believes they stopped a
// request that was in fact approved has been misled.
var ErrNotPending = errors.New("access: the request has already been decided")

// ErrUndelivered is a revocation that is RECORDED and could not be published.
//
// The grant is revoked — no new session may use it, and the audit record is
// written — but the session_kill or the cache_invalidate did not reach the
// revocation stream, so sessions it backed may still be running. Revoking the
// grant again publishes both again: a revocation is idempotent in what it
// records and repeatable in what it sends.
var ErrUndelivered = errors.New("access: the grant is revoked, but ending its sessions could not be published")
