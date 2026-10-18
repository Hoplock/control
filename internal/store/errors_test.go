// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// A store error's reason is fit to answer a caller with: this package's own
// sentence without the operation that wrote it, and nothing of the database's
// text, which names tables and constraints (0023).
func TestAReasonNamesNoOperationAndNoDatabaseText(t *testing.T) {
	t.Parallel()
	check := &pgconn.PgError{Code: pgerrCheckViolation, Message: `new row for relation "targets" violates check constraint "targets_zone_check"`}
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"this package's refusal": {invalid("store.Audit.RetainPrefix", "the head of stream s is 4, and the head is never retained"),
			"the head of stream s is 4, and the head is never retained"},
		"wrapped by its caller": {fmt.Errorf("audit: retain: %w", invalid("store.Targets.Upsert", "target hostname is required")),
			"target hostname is required"},
		"the database's refusal":  {wrap("store.Targets.Upsert", check), "invalid"},
		"a unique violation":      {wrap("store.Targets.Upsert", &pgconn.PgError{Code: pgerrUniqueViolation}), "conflict"},
		"an absent row":           {wrap("store.Targets.Get", pgx.ErrNoRows), "not_found"},
		"a bare kind":             {notFound("store.Targets.Get"), "not_found"},
		"not this package's":      {errors.New("from somewhere else"), ""},
		"a conflict this wrote":   {conflict("store.X.Put", "already decided"), "already decided"},
		"exhausted with sentence": {exhausted("store.UIDs.Lease", "the range is used up"), "the range is used up"},
	} {
		got := Reason(c.err)
		if got != c.want {
			t.Errorf("%s: Reason = %q, want %q", name, got, c.want)
		}
		if strings.Contains(got, "store.") || strings.Contains(got, "relation") {
			t.Errorf("%s: %q names an operation or a table", name, got)
		}
	}
}
