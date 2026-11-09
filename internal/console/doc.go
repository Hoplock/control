// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package console serves the operator console (phases 0049–0060, PLAN M20,
// M21, M27): the single-page app built from ui/ into dist/, embedded in the
// binary so that a deployment is still one process, and served under
// /console/ on the north-bound listener and nowhere else.
//
// The console is a client of the north-bound API (M2). This package serves
// files — the shell, its assets, its catalogue and the history fallback that
// answers a deep link with the shell — and never an API answer: it has no
// route, no store and no credential, and imports nothing that has.
//
// dist/ is committed. `npm run build` in ui/ writes it, CI rebuilds it and
// fails on any difference, and `make build` therefore needs no Node.
package console
