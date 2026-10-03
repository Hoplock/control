// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package accessctx

import (
	"errors"
	"fmt"
	"time"
)

// The codes a refused push is answered with. They are stable: an integration's
// operator, a SIEM rule and the console filter on them. Reword freely; never
// reuse one for another condition.
const (
	// CodeProviderNotFound is a push naming no provider this server runs.
	CodeProviderNotFound = "provider_not_found"
	// CodePushNotSupported is a provider that takes no pushes.
	CodePushNotSupported = "push_not_supported"
	// CodeBindingNotFound is a provider with no scope binding in the tenant:
	// an integration nobody has said may assert anything.
	CodeBindingNotFound = "binding_not_found"
	// CodeBindingDisabled is a binding kept but not in force.
	CodeBindingDisabled = "binding_disabled"
	// CodePushNotPermitted is a credential the binding does not name pushing
	// for it — another integration's token, most likely. An escalation
	// attempt.
	CodePushNotPermitted = "push_not_permitted"
	// CodePushNotAccepted is a push to a binding that only probes.
	CodePushNotAccepted = "push_not_accepted"
	// CodeRateLimited is a push past the integration's rate.
	CodeRateLimited = "rate_limited"
	// CodeAssertionMalformed is a push that could not be read into a window,
	// or a window of the wrong shape. Field names the part at fault.
	CodeAssertionMalformed = "assertion_malformed"
	// CodeOutsideScope is a well-formed window the binding does not admit.
	// Field names the part outside it. An escalation attempt.
	CodeOutsideScope = "outside_scope"
	// CodeAssertionConflict is an assertion id already used for a different
	// window: the same id twice is the same window, never two.
	CodeAssertionConflict = "assertion_conflict"
	// CodeAssertionStale is an assertion older than the server accepts — the
	// replay an id alone cannot catch.
	CodeAssertionStale = "assertion_stale"
	// CodeAssertionFromFuture is an assertion issued further in the future
	// than the tolerated clock skew.
	CodeAssertionFromFuture = "assertion_from_future"
	// CodeWindowClosed is a window that ended before it arrived.
	CodeWindowClosed = "window_closed"
)

// Refusal is a push refused on purpose. It is never an outage: the push was
// read and judged, and the answer is no. Everything else this package returns
// is an outage (M11).
type Refusal struct {
	// Code is one of the Code constants.
	Code string
	// Field names the part of the push at fault, in the assertion's own
	// spelling (`targets[1]`, `window.not_after`), when there is one.
	Field string
	// Detail is an operator-facing diagnostic. It names what was refused and
	// never the binding's contents.
	Detail string
	// RetryAfter is when a rate-limited push may be tried again.
	RetryAfter time.Duration
	// Escalation reports that the refusal was audited as an attempted
	// privilege escalation.
	Escalation bool
}

// Error implements error.
func (r *Refusal) Error() string {
	s := "accessctx: push refused: " + r.Code
	if r.Field != "" {
		s += " (" + r.Field + ")"
	}
	if r.Detail != "" {
		s += ": " + r.Detail
	}
	return s
}

// AsRefusal reports whether err is a refusal, and which.
func AsRefusal(err error) (*Refusal, bool) {
	var r *Refusal
	ok := errors.As(err, &r)
	return r, ok
}

// UndeterminedError is a decision that depended on an external window nobody
// could confirm, in a scope whose `unanswered` setting is `outage`. It is an
// outage by construction (M11): the authorize call answers 5xx, and the
// proxy tells the user the service is unavailable rather than that their
// access was refused because a scanner's API was slow.
type UndeterminedError struct {
	// Provider and Reference name the window, and Cause why it went
	// unanswered (`timeout`, `unavailable`, `malformed`, ...).
	Provider  string
	Reference string
	Cause     string
}

// Error implements error.
func (e *UndeterminedError) Error() string {
	ref := e.Reference
	if ref == "" {
		ref = "(none pushed)"
	}
	return fmt.Sprintf("accessctx: the decision depends on a window from %s, reference %s, that could not be confirmed (%s), and its scope answers an unanswered probe with an outage",
		e.Provider, ref, e.Cause)
}
