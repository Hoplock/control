// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package revoke owns the event bus that fans an operator action out to every
// proxy that needs it: subscriptions, event ids, the replay buffer, and the
// resync answer when a subscriber has fallen too far behind (PLAN M9). It is
// the kill switch, so its delivery guarantees are part of the product.
package revoke
