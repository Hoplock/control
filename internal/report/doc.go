// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package report serves every registered ext.ReportProvider (PLAN M15, §7
// "Reports"): it lists what each provider offers a tenant, and generates one
// report on request.
//
// # Control serves, a host packages
//
// The reporting point is core: with nothing registered, the north-bound audit
// query, explain and simulation are the reporting available. A registered pack
// packages those answers into a named, repeatable artefact, and this package is
// how such a pack reaches the people who may read the tenant's audit. Listing
// and on-demand generation are Control's; a host's schedules, campaigns and
// history of generated reports stay the host's, on its own routes (M27).
//
// # Every check that follows from the descriptor is the framework's
//
// A provider's Reports is its licence answer: the list may differ per tenant.
// So every generation asks it again, and generates only a report it listed for
// that tenant, checked against the descriptor it listed — the format, the
// parameters, the period — before Generate is called. A provider that forgets
// to re-check in Generate still cannot produce a pack for a tenant it did not
// list one for. A descriptor this package cannot serve (an empty or repeated
// id, no formats, a format outside the enum, a parameter named twice or not at
// all) is the provider's fault and refuses the whole provider.
//
// # Bounded, capped, and never a short answer
//
// The caller's context carries the bound: every provider call runs on its own
// goroutine, so a provider that ignores its context is abandoned at the bound
// rather than holding the request. Generations are capped per node by a
// counting semaphore, acquired before either provider call and released when
// the report's body is closed — or, for a call abandoned at the bound, when the
// provider finally returns, because a provider still running still costs the
// node whose decision path M5 keeps cheap. The listing degrades per provider:
// one provider's failure is its own entry's error, never a missing entry, which
// would read as "no packs" (M11).
//
// It knows nothing of HTTP. The north-bound handlers (internal/httpapi/north,
// reports.go) map its Kinds onto statuses and codes, and stream the body.
package report
