// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Kind classifies a store failure. It is a closed enum rather than a set of
// sentinel values so that a caller switching on it can be checked for
// exhaustiveness by the linter (PLAN M13) — a new kind then shows up as a
// build failure in every caller that must decide what to do about it, rather
// than falling into somebody's default branch.
type Kind int

const (
	// KindInternal is the zero value on purpose: an unclassified failure is
	// an outage, never a deny (M11). Getting this wrong in the safe
	// direction costs a 5xx; getting it wrong in the other direction sends
	// an operator to debug permissions during a database outage.
	KindInternal Kind = iota
	// KindNotFound means the row is absent. This is the ONLY kind a caller
	// may turn into a deny, and only when absence is genuinely the answer.
	KindNotFound
	// KindConflict means the database refused a write that collided with an
	// existing row — a duplicate audit record_id, a second active bundle.
	// Whether that is an error or the expected outcome is the caller's to
	// decide; idempotent ingest (M8) treats it as success.
	KindConflict
	// KindExhausted means the resource is used up rather than missing. The
	// uid allocation cursor at the top of its range is the case that exists
	// today, and the contract answers it with 409.
	KindExhausted
	// KindInvalid means the arguments cannot describe a legal row: an empty
	// tenant, a block size of zero. It is the caller's bug, not the
	// database's state.
	KindInvalid
	// KindUnavailable means the database could not be reached or did not
	// answer in time. Named separately from KindInternal because it is the
	// failure M11 was written about, and a caller that logs it wants to say
	// "database" rather than "something".
	KindUnavailable
)

// String renders the kind for logs and error messages.
func (k Kind) String() string {
	switch k {
	case KindInternal:
		return "internal"
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindExhausted:
		return "exhausted"
	case KindInvalid:
		return "invalid"
	case KindUnavailable:
		return "unavailable"
	}
	return "internal"
}

// Sentinels for errors.Is. They carry no context of their own; a real failure
// is an *Error wrapping one of these, and the helpers below are the intended
// way to ask about it.
var (
	// ErrNotFound reports an absent row.
	ErrNotFound = errors.New("store: not found")
	// ErrConflict reports a uniqueness violation.
	ErrConflict = errors.New("store: conflict")
	// ErrExhausted reports a resource used up rather than missing.
	ErrExhausted = errors.New("store: exhausted")
	// ErrInvalid reports arguments that cannot describe a legal row.
	ErrInvalid = errors.New("store: invalid argument")
	// ErrUnavailable reports a database that could not be reached.
	ErrUnavailable = errors.New("store: database unavailable")
)

// Error is the store's error type. Op names the repository method so a log
// line says where the failure happened without a stack trace, and Kind says
// what a caller may conclude from it.
//
// The distinction between KindNotFound and everything else is the whole point
// of this type. Collapsing them is how a database outage becomes a 401 three
// phases later (M11), and it collapses quietly: `err != nil` reads the same
// either way.
type Error struct {
	// Op is the repository method, e.g. "store.Subjects.Get".
	Op string
	// Kind classifies the failure.
	Kind Kind
	// Err is the underlying cause, kept for logs. It is never returned to a
	// south-bound caller: a Postgres message can name a column or a
	// constraint, and neither is safe to disclose (PLAN §8).
	Err error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s: %s", e.Op, e.Kind)
	}
	return fmt.Sprintf("%s: %s: %v", e.Op, e.Kind, e.Err)
}

// Unwrap exposes the cause, and Is maps the Kind onto the sentinel so that
// errors.Is(err, ErrNotFound) works without the caller knowing about Kind.
func (e *Error) Unwrap() error { return e.Err }

// Is reports whether this error matches one of the package sentinels.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.Kind == KindNotFound
	case ErrConflict:
		return e.Kind == KindConflict
	case ErrExhausted:
		return e.Kind == KindExhausted
	case ErrInvalid:
		return e.Kind == KindInvalid
	case ErrUnavailable:
		return e.Kind == KindUnavailable
	}
	return false
}

// IsNotFound reports whether err means "the row is not there".
//
// It is deliberately narrow: a timeout, a closed pool, a syntax error and a
// panic all answer false, because the only safe reading of any of them is
// "we do not know".
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsConflict reports whether err means "a row like that already exists".
func IsConflict(err error) bool { return errors.Is(err, ErrConflict) }

// IsExhausted reports whether err means "the resource is used up".
func IsExhausted(err error) bool { return errors.Is(err, ErrExhausted) }

// IsInvalid reports whether err means "those arguments cannot describe a legal
// row". It is the caller's bug, never the database's state.
func IsInvalid(err error) bool { return errors.Is(err, ErrInvalid) }

// IsUnavailable reports whether err means the database could not be reached.
// This is the failure M11 was written about: it is an outage, never a deny.
func IsUnavailable(err error) bool { return errors.Is(err, ErrUnavailable) }

// ErrorKind reports the Kind of err, or KindInternal when err is not a store
// error — an unrecognised failure is an outage (M11).
func ErrorKind(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

// notFound builds an absent-row error.
func notFound(op string) error {
	return &Error{Op: op, Kind: KindNotFound}
}

// invalid builds an illegal-argument error.
func invalid(op, msg string) error {
	return &Error{Op: op, Kind: KindInvalid, Err: errors.New(msg)}
}

// exhausted builds a used-up-resource error.
func exhausted(op, msg string) error {
	return &Error{Op: op, Kind: KindExhausted, Err: errors.New(msg)}
}

// wrap classifies a pgx/Postgres error.
//
// Everything it cannot positively identify becomes KindInternal, which is the
// direction that fails safe: an unknown Postgres error read as "not found"
// would be read one layer up as a deny.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return &Error{Op: op, Kind: KindNotFound, Err: err}
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgerrUniqueViolation:
			return &Error{Op: op, Kind: KindConflict, Err: err}
		case pgerrCheckViolation, pgerrExclusionViolation:
			// A CHECK is a rule the schema states about legal rows — the
			// uid cursor's forward-only trigger raises one — so a violation
			// is an illegal write, not an outage.
			return &Error{Op: op, Kind: KindInvalid, Err: err}
		case pgerrForeignKeyViolation, pgerrNotNullViolation:
			return &Error{Op: op, Kind: KindInvalid, Err: err}
		}
		return &Error{Op: op, Kind: KindInternal, Err: err}
	}

	var connErr *pgconn.ConnectError
	if errors.As(err, &connErr) {
		return &Error{Op: op, Kind: KindUnavailable, Err: err}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &Error{Op: op, Kind: KindUnavailable, Err: err}
	}

	return &Error{Op: op, Kind: KindInternal, Err: err}
}

// isUndefinedTable reports a query against a table that does not exist, which
// is how a database that has never been migrated answers.
func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrUndefinedTable
}

// Postgres SQLSTATE codes this package classifies. Spelled out rather than
// inlined so a reader can see which ones are handled and which fall through to
// KindInternal.
const (
	pgerrUniqueViolation     = "23505"
	pgerrForeignKeyViolation = "23503"
	pgerrNotNullViolation    = "23502"
	pgerrCheckViolation      = "23514"
	pgerrExclusionViolation  = "23P01"
	pgerrUndefinedTable      = "42P01"
)
