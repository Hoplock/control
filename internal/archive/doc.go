// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package archive feeds a registered ext.ArchiveStore everything the audit
// store records (0025, PLAN §7, M8): every committed record, per tenant and
// stream in ascending sequence, at least once, and each session capture once
// its record is confirmed. How far the archive has confirmed is kept in the
// store, so retention — which never deletes past it — and every node read the
// same answer (M5).
//
// It runs off ingest and the decision path. Nothing a proxy waits on calls
// into it; the ingester only tells it which tenants it has seen, which never
// blocks. With no archive registered, the daemon builds no feed.
package archive
