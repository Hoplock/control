// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package compile turns a validated policy bundle into a decision program
// (PLAN M3). The compiler is deliberately a boundary: a future demand for a
// different authoring language is met by adding a backend here rather than by
// rewriting the evaluator.
package compile
