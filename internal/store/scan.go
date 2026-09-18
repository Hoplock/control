// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"encoding/json"
	"time"
)

// rowScanner is the single-row half of pgx.Row and pgx.Rows, so a scan helper
// can serve both a QueryRow lookup and a loop over a result set.
type rowScanner interface {
	Scan(dest ...any) error
}

// nonNilStrings normalises a nil slice to an empty one.
//
// The columns are NOT NULL with a default, and a nil Go slice encodes as SQL
// NULL rather than as an empty array — which fails the constraint at write
// time instead of at review time. Normalising here means a caller never has to
// know that.
func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// nonNilBytes normalises a nil byte slice to an empty one, for the same reason:
// a nil []byte encodes as SQL NULL, and a NOT NULL bytea column would refuse it
// with a constraint violation rather than an argument error.
func nonNilBytes(v []byte) []byte {
	if v == nil {
		return []byte{}
	}
	return v
}

// nonNilMap normalises a nil map to an empty one, for the same reason.
func nonNilMap(v map[string]string) map[string]string {
	if v == nil {
		return map[string]string{}
	}
	return v
}

// nonNilJSON normalises absent JSON to an empty object. A jsonb column
// declared NOT NULL cannot take a nil RawMessage, and "the caller sent no
// payload" is better stored as `{}` than refused.
func nonNilJSON(v json.RawMessage) json.RawMessage {
	if len(v) == 0 {
		return json.RawMessage(`{}`)
	}
	return v
}

// nullableTime maps the zero time onto SQL NULL. Several columns mean
// "has not happened yet" — a proxy that has never reported, a grant that was
// never revoked — and NULL says that where an epoch timestamp would claim it
// happened in 1970.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// timeOrZero maps SQL NULL back onto the zero time.
func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
