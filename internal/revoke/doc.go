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
// # Whichever node a proxy reaches (0027)
//
// Every guarantee above holds across the nodes of a deployment, because what
// they rest on is shared rather than per process:
//
//   - THE LOG is in the store (event_log). A publish commits the event before it
//     answers, under its tenant's lock, so a tenant's positions commit in order
//     and every node fans out by reading "after the last position I
//     delivered". Its ids are `evt-<epoch>-<position>`, one order per tenant
//     that every node reads alike; a heartbeat or a reconnect's `resync` names
//     a position without taking one (`evt-<epoch>-<position>.<line>`).
//   - THE FLOOR is in the store (event_streams): each tenant keeps its newest
//     `events.replay_buffer` events, and a reconnect from before what was
//     pruned is answered `resync`. A restart replays; it does not resync.
//   - THE FAN-OUT is the log plus a wake-up on ext.ClusterCoordinator's bus,
//     which carries no event and none of the order; a lost wake-up is found by
//     the node's own read of the log every `events.sync_interval`.
//   - THE SUBSCRIPTIONS every node holds are in the store (event_subscriptions)
//     and copied into each node's memory, which is what the cache-hint gate
//     reads: never the store, on the decision path (M5).
//
// A bus built without a store keeps its log in this process instead, with a
// per-process epoch, and knows only its own subscriptions: exactly the broker
// every release before 0027 ran, kept for tests and a database-less embedding.
// The code above the log does not branch on which it has.
package revoke
