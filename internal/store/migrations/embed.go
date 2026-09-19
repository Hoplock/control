// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package migrations holds the forward-only SQL that builds the Postgres
// schema, embedded so that the server is one binary with nothing to install
// beside it (PLAN §3, M13).
//
// The SQL lives here rather than in a top-level `migrations/` directory for a
// single mechanical reason: `go:embed` cannot reach outside its own package
// directory, so a top-level directory would need a top-level *package* to
// embed it — and `ext/` is the only non-internal package this module has
// (M15). See docs/PLAN.md §3.
//
// Files are named `NNNN_short_description.sql`, applied in ascending numeric
// order, and are never edited once merged: correcting one means adding the
// next (PLAN §8).
package migrations

import "embed"

// FS holds every migration file in this package.
//
//go:embed *.sql
var FS embed.FS
