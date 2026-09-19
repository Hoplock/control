// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The distinction this file exercises is the one M11 is about: a database that
// did not answer must never be readable as "the row is not there", because one
// layer up that becomes a 401 and sends an operator to debug permissions during
// an outage.
func TestNotFoundAndFailureAreDistinguishable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		wantKind Kind
		notFound bool
	}{
		{
			name:     "absent row",
			err:      wrap("store.Test", pgx.ErrNoRows),
			wantKind: KindNotFound,
			notFound: true,
		},
		{
			name:     "duplicate key",
			err:      wrap("store.Test", &pgconn.PgError{Code: pgerrUniqueViolation}),
			wantKind: KindConflict,
		},
		{
			name:     "check constraint",
			err:      wrap("store.Test", &pgconn.PgError{Code: pgerrCheckViolation}),
			wantKind: KindInvalid,
		},
		{
			name:     "deadline exceeded",
			err:      wrap("store.Test", context.DeadlineExceeded),
			wantKind: KindUnavailable,
		},
		{
			name:     "connection refused",
			err:      wrap("store.Test", &pgconn.ConnectError{}),
			wantKind: KindUnavailable,
		},
		{
			name: "an unrecognised Postgres error is an outage, not an absence",
			// 42601 is a syntax error: a bug in this repository, and
			// the kind of failure that must never read as a deny.
			err:      wrap("store.Test", &pgconn.PgError{Code: "42601"}),
			wantKind: KindInternal,
		},
		{
			name:     "an error from nowhere in particular is an outage",
			err:      wrap("store.Test", errors.New("something")),
			wantKind: KindInternal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ErrorKind(tc.err); got != tc.wantKind {
				t.Errorf("ErrorKind() = %v, want %v", got, tc.wantKind)
			}
			if got := IsNotFound(tc.err); got != tc.notFound {
				t.Errorf("IsNotFound() = %v, want %v", got, tc.notFound)
			}
		})
	}
}

func TestWrapPassesNilThrough(t *testing.T) {
	t.Parallel()
	if err := wrap("store.Test", nil); err != nil {
		t.Errorf("wrap(nil) = %v, want nil", err)
	}
}

func TestErrorKindOfAnUnrelatedErrorIsInternal(t *testing.T) {
	t.Parallel()
	// Not a store error at all. Reading it as anything but an outage would
	// be guessing (M11).
	if got := ErrorKind(errors.New("from somewhere else")); got != KindInternal {
		t.Errorf("ErrorKind() = %v, want KindInternal", got)
	}
	if IsNotFound(errors.New("from somewhere else")) {
		t.Error("IsNotFound() on an unrelated error = true, want false")
	}
}

func TestErrorSentinelsMatchTheirKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind     Kind
		sentinel error
	}{
		{KindNotFound, ErrNotFound},
		{KindConflict, ErrConflict},
		{KindExhausted, ErrExhausted},
		{KindInvalid, ErrInvalid},
		{KindUnavailable, ErrUnavailable},
	}
	for _, tc := range tests {
		err := &Error{Op: "store.Test", Kind: tc.kind}
		if !errors.Is(err, tc.sentinel) {
			t.Errorf("errors.Is(%v, %v) = false, want true", err, tc.sentinel)
		}
		for _, other := range tests {
			if other.kind == tc.kind {
				continue
			}
			if errors.Is(err, other.sentinel) {
				t.Errorf("errors.Is(%v, %v) = true, want false", err, other.sentinel)
			}
		}
	}
}

// The zero Kind must be the safe one: a caller that forgets to set it says
// "outage", never "denied".
func TestZeroKindIsInternal(t *testing.T) {
	t.Parallel()
	var k Kind
	if k != KindInternal {
		t.Errorf("zero Kind = %v, want KindInternal", k)
	}
	if IsNotFound(&Error{Op: "store.Test"}) {
		t.Error("an unclassified store error reads as not-found, want outage")
	}
}
