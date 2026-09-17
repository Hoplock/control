// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"errors"
	"fmt"
)

// Kind classifies a failure returned across the extension seam. It is a closed
// enum for the same reason store.Kind is: a caller switching on it can be
// checked for exhaustiveness, so a kind added later fails the build in every
// place that must decide what to do about it.
//
// The zero value matters. An extension that returns a bare error, or one this
// package cannot classify, is KindInternal — an outage. That is M11 applied to
// third-party code: the only kind that may ever become a `401` is KindDenied,
// and an implementation has to choose it deliberately. A vendor integration
// that times out must never be able to tell a user "access denied".
type Kind int

const (
	// KindInternal is the zero value on purpose: an unclassified failure is
	// an outage, never a deny (M11).
	KindInternal Kind = iota
	// KindUnavailable means the extension could not reach whatever it
	// depends on, or that dependency did not answer in time. Distinguished
	// from KindInternal because it is the failure M11 was written about and
	// because M16's probe path has to tell "unreachable" from "malformed".
	KindUnavailable
	// KindInvalid means the arguments cannot describe a legal request. It is
	// the caller's bug, not the extension's state.
	KindInvalid
	// KindNotFound means the thing asked about does not exist here.
	KindNotFound
	// KindConflict means the request collided with something already
	// present — a duplicate registration, a workflow reference reused.
	KindConflict
	// KindDisabled means the capability is not configured at all. Control
	// returns it when a caller reaches a seam whose absent behaviour is
	// "disabled" (see PointInfo.WhenAbsent).
	KindDisabled
	// KindMalformed means the external system answered, and its answer could
	// not be understood. Separate from KindUnavailable because M16 requires a
	// decision record to tell those two apart: one is an outage in the
	// network, the other is a broken integration.
	KindMalformed
	// KindDenied means the extension decided, on purpose, that the answer is
	// no. This is the ONLY kind that may become a deny (M11), and an
	// implementation returns it only when refusal is the answer rather than
	// the symptom.
	KindDenied
)

// String renders the kind for logs and error messages.
func (k Kind) String() string {
	switch k {
	case KindInternal:
		return "internal"
	case KindUnavailable:
		return "unavailable"
	case KindInvalid:
		return "invalid"
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindDisabled:
		return "disabled"
	case KindMalformed:
		return "malformed"
	case KindDenied:
		return "denied"
	}
	return "internal"
}

// Sentinels for errors.Is. They carry no context of their own: a real failure
// is an *Error wrapping one of these, and the Is* helpers below are the
// intended way to ask about it.
var (
	// ErrUnavailable reports a dependency that could not be reached.
	ErrUnavailable = errors.New("ext: unavailable")
	// ErrInvalid reports arguments that cannot describe a legal request.
	ErrInvalid = errors.New("ext: invalid")
	// ErrNotFound reports something absent.
	ErrNotFound = errors.New("ext: not found")
	// ErrConflict reports a collision with something already present.
	ErrConflict = errors.New("ext: conflict")
	// ErrDisabled reports a capability that is not configured.
	ErrDisabled = errors.New("ext: disabled")
	// ErrMalformed reports an answer that could not be understood.
	ErrMalformed = errors.New("ext: malformed")
	// ErrDenied reports a deliberate refusal.
	ErrDenied = errors.New("ext: denied")
	// ErrNoEvidence reports that an AccessContextProvider has nothing to say
	// about the access being asked about. It is not a denial and not a
	// failure: silence is evidence of absence only if policy says it is, and
	// that is policy's decision to make, not the provider's.
	ErrNoEvidence = errors.New("ext: no evidence")
)

// Error is the error type of this seam. Op names the operation ("AuditSink.Export"),
// Provider names the registered implementation it came from, and Kind is what
// the caller switches on.
type Error struct {
	// Point is the extension point involved.
	Point Point
	// Provider is the registered provider name, empty when the failure is
	// Control's own.
	Provider string
	// Op is the operation, conventionally "Interface.Method".
	Op string
	// Kind classifies the failure; see Kind's documentation for why the zero
	// value is an outage.
	Kind Kind
	// Err is the underlying cause, wrapping one of the sentinels above when
	// the kind has one.
	Err error
}

// Error implements error.
func (e *Error) Error() string {
	provider := e.Provider
	if provider == "" {
		provider = "hoplock/control"
	}
	if e.Err == nil {
		return fmt.Sprintf("ext: %s: %s [%s: %s]", e.Op, e.Kind, e.Point, provider)
	}
	return fmt.Sprintf("ext: %s: %s: %v [%s: %s]", e.Op, e.Kind, e.Err, e.Point, provider)
}

// Unwrap exposes the cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

// Errorf builds an *Error wrapping the sentinel that matches kind, so that
// both errors.Is(err, ErrUnavailable) and a Kind switch work on the result.
func Errorf(point Point, provider, op string, kind Kind, format string, args ...any) *Error {
	cause := fmt.Errorf(format, args...)
	if sentinel := sentinelFor(kind); sentinel != nil {
		cause = fmt.Errorf("%w: %s", sentinel, cause)
	}
	return &Error{Point: point, Provider: provider, Op: op, Kind: kind, Err: cause}
}

// sentinelFor maps a kind to its errors.Is sentinel. KindInternal has none:
// an unclassified failure is exactly the case where there is nothing specific
// to match on.
func sentinelFor(kind Kind) error {
	switch kind {
	case KindInternal:
		return nil
	case KindUnavailable:
		return ErrUnavailable
	case KindInvalid:
		return ErrInvalid
	case KindNotFound:
		return ErrNotFound
	case KindConflict:
		return ErrConflict
	case KindDisabled:
		return ErrDisabled
	case KindMalformed:
		return ErrMalformed
	case KindDenied:
		return ErrDenied
	}
	return nil
}

// KindOf reports how to treat err. An error that is not an *Error and matches
// no sentinel is KindInternal — the safe direction (M11).
func KindOf(err error) Kind {
	if err == nil {
		return KindInternal
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	for _, k := range []Kind{KindUnavailable, KindInvalid, KindNotFound, KindConflict, KindDisabled, KindMalformed, KindDenied} {
		if errors.Is(err, sentinelFor(k)) {
			return k
		}
	}
	return KindInternal
}

// IsUnavailable reports whether err is a dependency that could not be reached.
func IsUnavailable(err error) bool { return KindOf(err) == KindUnavailable }

// IsInvalid reports whether err is a caller's bug.
func IsInvalid(err error) bool { return KindOf(err) == KindInvalid }

// IsNotFound reports whether err is something absent.
func IsNotFound(err error) bool { return KindOf(err) == KindNotFound }

// IsConflict reports whether err is a collision.
func IsConflict(err error) bool { return KindOf(err) == KindConflict }

// IsDisabled reports whether err is an unconfigured capability.
func IsDisabled(err error) bool { return KindOf(err) == KindDisabled }

// IsMalformed reports whether err is an answer that could not be understood.
func IsMalformed(err error) bool { return KindOf(err) == KindMalformed }

// IsDenied reports whether err is a deliberate refusal — the one kind that may
// become a deny (M11).
func IsDenied(err error) bool { return KindOf(err) == KindDenied }

// IsNoEvidence reports whether err means the provider had nothing to say. See
// ErrNoEvidence: this is neither a denial nor a failure.
func IsNoEvidence(err error) bool { return errors.Is(err, ErrNoEvidence) }
