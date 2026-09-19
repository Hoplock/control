// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package south serves the proxy-facing contract on the south-bound listener
// (PLAN M2). Its authentication is a proxy credential and never an operator
// one. A 401 here means the server decided to deny; every other failure is a
// 5xx that says outage (PLAN M11).
//
// It serves THE CONTRACT AND NOTHING ELSE — no operator route, no debug route,
// no metrics route — and the route table is asserted rather than trusted,
// because the day somebody mounts an admin route on the wrong mux is not the
// day anyone notices. Endpoints a later phase owns are absent rather than
// stubbed: a stub answering a plausible-looking empty policy would be worse
// than a 404, because the proxy would act on it.
//
// It is also the only package that speaks both vocabularies. The wire shapes
// belong to internal/contract and the domain shapes to internal/identity,
// internal/fleet and internal/decision (PLAN §3), so a contract revision lands
// here and stops.
//
// Two source-level tests in discipline_test.go keep M11 structural rather than
// conventional: one fails the build if a function other than the two named
// ones can construct a 401, the other if anything but the mapper names an HTTP
// status constant.
package south
