// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"errors"
	"fmt"
	"time"

	"github.com/hoplock/control/internal/store"
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

// ErrNoWorkflow is a requester's own request on a deployment with no approval
// workflow registered (0016). Nothing would decide it, and a request nobody
// decides is a self-grant, so it is refused before anything is stored. It is a
// refusal made on purpose, never an outage (M11): asking again gets the same
// answer until a workflow is registered, and a deployment with none is
// working as designed (M15) — an administrator grants access directly.
var ErrNoWorkflow = errors.New("access: no approval workflow is registered to decide a request")

// ErrAssertionEnded is an open pushed under an id its external system has
// already ended (M16, 0017): the end arrived first, because pushes are not
// delivered in order, and an ended window stays ended. Nothing is stored. It is
// a refusal made on purpose, never an outage (M11); CreateExternal returns it
// as an *EndedError, which says when.
var ErrAssertionEnded = errors.New("access: the external system ended this window before it was opened")

// EndedError is ErrAssertionEnded with the end that was kept.
type EndedError struct {
	// End is the end the external system pushed for the id.
	End store.AccessContextWindowEnd
}

// Error implements error.
func (e *EndedError) Error() string {
	return fmt.Sprintf("%v (at %s)", ErrAssertionEnded, e.End.EndedAt.UTC().Format(time.RFC3339))
}

// Unwrap makes errors.Is(err, ErrAssertionEnded) hold.
func (e *EndedError) Unwrap() error { return ErrAssertionEnded }

// ErrUndelivered is a revocation that is RECORDED and could not be published.
//
// The grant is revoked — no new session may use it, and the audit record is
// written — but the session_kill or the cache_invalidate did not reach the
// revocation stream, so sessions it backed may still be running. Revoking the
// grant again publishes both again: a revocation is idempotent in what it
// records and repeatable in what it sends.
var ErrUndelivered = errors.New("access: the grant is revoked, but ending its sessions could not be published")
