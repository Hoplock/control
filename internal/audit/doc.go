// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package audit is the append-only, tamper-evident record: ingest that is
// idempotent on the client-assigned record id, the per-stream hash chain, the
// verifier, the query API, and retention (PLAN M8). Nothing else in this
// module writes audit rows.
package audit
