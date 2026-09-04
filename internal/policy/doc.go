// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package policy is the pure policy pipeline: parse and validate a bundle
// (model), compile it into a decision program (compile), and evaluate that
// program against a closed input vocabulary (eval). Nothing here touches HTTP,
// the database, or a clock of its own — time is an input (PLAN M3, M4).
package policy
