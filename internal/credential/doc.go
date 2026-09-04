// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package credential brokers the credentials a proxy uses to reach a target,
// including the SSH certificate authority (proxy D6a). Material is short-lived
// and scoped to a decision; nothing here writes a credential, a key, or a token
// into a log or an error (PLAN §8).
package credential
