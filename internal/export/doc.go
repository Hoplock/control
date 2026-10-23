// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package export delivers the audit store to every registered ext.AuditSink
// (0026, PLAN §7, M8): every committed record of every tenant, per tenant and
// stream in ascending sequence, at least once, from a position Control keeps
// in the store per sink, tenant and stream. Each sink is one more consumer of
// the feed internal/archive built for the archive, named by its
// Registration.Provider, with a feed of its own so one sink's backlog never
// stalls another's.
//
// A sink is a downstream consumer and never a source of truth: nothing here is
// read back as the record, retention does not wait on a sink, and delivery
// writes no audit record of its own. It runs off ingest and the decision path
// (M5). Control ships no sink — the point stays core, and the local store is
// the record (M15, PLAN §11) — so with none registered the daemon builds no
// delivery and nothing here runs.
package export
