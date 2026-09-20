// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package revoke owns the event bus that fans an operator action out to every
// proxy that needs it: subscriptions, event ids, the replay buffer, and the
// resync answer when a subscriber has fallen too far behind (PLAN M9). It is
// the kill switch, so its delivery guarantees are part of the product.
//
// # What the guarantees are
//
// Four of them, and each one is load-bearing rather than a nicety:
//
//   - ORDERED PER SUBSCRIBER, with monotonic event ids. A proxy stores the last
//     id it processed and resumes from it, so an out-of-order delivery makes a
//     later reconnect skip everything between.
//   - REPLAY OR RESYNC, NEVER A SILENT SKIP. A reconnecting subscriber either
//     gets every event after its `last_event_id`, in order, or gets `resync` as
//     the FIRST line and nothing older. Quietly resuming live delivery over a
//     gap is the one behaviour worse than both.
//   - HEARTBEATS WITHIN THE INTERVAL THIS SERVER ADVERTISES, and that interval
//     within the contract's ceiling ([contract.MaxHeartbeatIntervalSeconds]).
//     Meeting either half alone is a failure — see [Bus.HeartbeatInterval].
//   - A SLOW SUBSCRIBER IS DROPPED, NOT WAITED FOR. It cannot stall the bus or
//     grow memory without bound; it reconnects into replay or resync. See
//     [Options.SubscriberQueue].
//
// # What it deliberately is not
//
// The broker is IN-PROCESS (M9). A single node is the whole prototype, and
// every guarantee above is true of one node only: the event ids are a counter
// in memory, the replay buffer is a ring in memory, and both are lost on a
// restart — which is why an id minted by an earlier run is answered with
// `resync` rather than believed. What a multi-node implementation would have to
// provide is written down in this phase's learnings file, because the shape of
// this package is the shape of that requirement.
package revoke
