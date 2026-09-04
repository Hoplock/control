// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package eval executes a compiled decision program and produces the decision
// record that explains it: the inputs seen, the rules matched, the obligations
// emitted, and the resulting snapshot (PLAN M4). Evaluation is total and
// bounded, because the proxy holds a user's handshake open while it runs (M5).
package eval
