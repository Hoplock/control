# 0027 — shared event plane — Learnings

## Summary
- **Shared state:** the log in `event_log` (per tenant, positions from the deployment-wide sequence `event_log_seq`), each tenant's epoch, head and floor in `event_streams` (its row is the tenant's publish lock), who holds a stream on which node in `event_subscriptions`. Migration `0016`. Per node, in memory only: a cursor per tenant (how far it fanned out) and the copy of other nodes' subscriptions.
- **Event ids:** `evt-<epoch>-<position:%012d>`; a heartbeat or a reconnect's `resync` names a position without taking one, `evt-<epoch>-<position>.<line:%012d>`. The epoch is the tenant stream's (16 hex), so an id from the old in-process broker or another log is `resync`.
- **Floor:** each tenant keeps its newest `events.replay_buffer` (1024); the publish that prunes past them raises `floor_seq` in its transaction. A replay reads the events, then the floor: below it is `resync`.
- **Fan-out:** publish commits, fans out on its own node under the tenant's cursor, then publishes a wake-up (`hoplock.events.logged`, kind `logged`, `{"seq":N}`) on `ext.ClusterCoordinator`. Woken nodes read the tenant's log after their cursor. Every node also reads it every `events.sync_interval` (1 s, ≤ heartbeat): the bound on a lost wake-up or a publisher that died after its commit.
- **Required of `ext.ClusterCoordinator`** (its doc, "What the bus must deliver"): at least once to every node, any order, duplicates fine; a subscription it cannot keep up with CLOSED, never left open skipping; closed with its context; payload and `Origin` opaque; `Node` id unique and stable per process. `extdefault.SingleNode` now closes a subscriber it drops.
- **Decision-path copy:** another node's stream is seen within 2 × `events.sync_interval`; its last-seen trails by ≤ one refresh (2 × heartbeat) more; read per proxy (`LiveSubscription`), memory only. Measured gate read: ~0.3 µs at 1,000 local streams and with 4,000 more remote; `main`'s read was ~90 µs at 1,000.
- **`ext` change:** doc only (`ClusterCoordinator`, `ClusterEvent`), listed under `### Breaking` in `v0.14.0`. Internal: `fleet.SubscriptionState` gains `LiveSubscription`; `fleet.ConfigPublisher.PublishConfigChange` returns only an error.
- **Decisions:** **M25 added** (state in the store, the bus only wakes), **M9 amended by M25** (register row), M5 revised in place (a fourth memory). §3, §4, §5.4, §10, §11 revised.
- **Gotchas:** no store method may read across tenants (M18), so the plane works per tenant and learns tenants from what a node is asked. `revoke.New` without `Store` is the old in-process broker (tests); the daemon always passes one.
- **NEXT:** 0033 builds the supervisory registration on this shape; its "event-bus and replication health" can read a node's cursor lag and `revoke_wake_*` failures. Downstream sync for `hoplock/enterprise` is queued (see the PR).

## Details

### The decision: store log + coordinator wake-up

The prompt offered three places for the state. Recorded as M25 in the plan, in
short:

| Candidate | Verdict |
| --- | --- |
| Durable log and sequence in Postgres, fan-out through `ext.ClusterCoordinator` | **chosen** |
| Postgres alone, `LISTEN`/`NOTIFY` for fan-out | rejected: a cross-node transport of Control's own beside the seam, so two transports, one untested by every deployment that registers a coordinator; clustering is Enterprise's (§11, M15) |
| The coordinator alone | rejected: needs ordering, durable replay and a floor the bus does not promise; adding methods breaks Enterprise (M23) for state Control must read anyway |

The refinement that made the coordinator's contract small: **the bus carries a
doorbell, not the event.** A woken node reads its tenant's log after its own
cursor, so duplicates, reordering and loss on the bus cost nothing but a read
or a delay. That is why the semantics required of Enterprise are only "at least
once, any order, dropped means closed".

The periodic read is the obvious question: is it Control's own cross-node
transport? It bounds a lost wake-up the way 0021's announcer timer bounds a lost
`config_changed`, and the store is already where every node's shared facts live.
It is not membership or an election. A multi-node deployment still needs a real
coordinator for singletons (M19) and for delivery inside a heartbeat. Two
Control processes on the single-node default would each hold every singleton,
so the default still does not make running several nodes supported.

### Why per tenant, and the global sequence anyway

`store` enforces that every repository method names its tenant
(`TestRepositoryMethodsTakeATenant`), and no read lists tenants. So a node
cannot "read everything after position N". It reads per tenant, for the tenants
it holds subscriptions in. Commit order is therefore needed only within a
tenant, and the tenant's `event_streams` row (`SELECT … FOR UPDATE`) gives it
the way `lockStream` does for the audit chain. Positions still come from one
sequence, so an id is unique deployment-wide and "monotonic across the
deployment" holds literally, not only per tenant. Positions are not contiguous
(other tenants interleave, rollbacks leave holes) and nothing reads them as if
they were.

The epoch is per tenant (created with the stream row), not per deployment,
because a deployment-wide row would be a table without a tenant column
(`TestEveryTableCarriesTheTenantColumn` allows only `schema_migrations`).

### Ordering on one node: the cursor

`cursor.mu` serialises "read the tenant's log after head, fan out, move head"
on a node. Every path that delivers takes it:

- **publish** holds it across the append and the read-back, so nothing else on
  the node delivers the event first and `Receipt.Delivered` is exact for this
  node;
- **Subscribe** takes it, catches up (an id another node minted may be past
  this node's head while its wake-up is in flight, and is not "unknown" for
  that), then registers at `head` under `Bus.mu`. Every later event is queued
  to the new subscriber by a later fan-out, and the replay covers
  `(last_event_id, head]`. That is 0009's "register and plan under one lock",
  with the cursor as the lock;
- **a wake-up** and **the sync** take it and catch up.

`head` is written holding both `cursor.mu` and `Bus.mu`, so a heartbeat can
read it under `Bus.mu` alone. A heartbeat names the head because everything at
or before it that was addressed to the subscriber is already ahead of it in the
subscriber's queue.

A cursor is created on the first subscription in a tenant (`open` reads the
head) and kept for the process. An event logged between that read and the
cursor's publication is picked up by the catch-up every subscription does
before it registers, so no wake-up is needed for it.

### Replay and the floor, read in that order

A prune deletes and raises the floor in one transaction. Reading the events and
then the floor means a floor still at or below `from` proves no prune passed
`from` before the events were read. Read the other way round, a prune between
the two reads would hand out a replay with a hole in it.

`TestAnUnreplayableIDGetsResyncAsTheFirstLine` (0009, untouched) builds ids by
hand, including `evt-<epoch>-999999999999`: the in-process log keeps the 0009
shape (8-hex run id) so every 0009 test passes without an edit.

### The subscription copy and its bound

Each node writes its own streams (`Put` when new or when last-seen moved a
refresh, `2 × heartbeat`, past the row; `Remove` when ended) and reads every
other node's rows for each tracked tenant every sync. "Tracked" means asked
about through `LiveSubscription(s)`, or holding a local stream. The first
question about a never-seen tenant is answered from local streams only, and the
copy covers it from the next sync on. A node never reads its own rows: its own
streams come from memory, and a row it wrote before a restart (same node id) is
one it no longer holds. Rows older than 10 minutes are expired by any node every
60 syncs. A read failure drops that tenant's copy: hints to remote-held proxies
are withheld, which is M9's safe side.

Measured on the two-node in-process test with a 100 ms sync: appeared after
~201 ms, disappeared ~201 ms after the stream ended, against a stated bound of
200 ms plus query time.

### The decision path, measured

`BenchmarkTheHintGateRead` (in `internal/revoke`, needs Postgres) runs
`fleet.Registry.EventStreamHealthy` over the real bus. On this container (4
vCPU Xeon @ 2.1 GHz):

| | per call |
| --- | --- |
| `main` (0026): gate built the tenant's whole map, 1,000 local streams | 85–101 µs, 98 KB, 7 allocs |
| 0027, 1,000 local streams | 0.30–0.37 µs, ~30 B, 1 alloc |
| 0027, 1,000 local + 4,000 on another node | 0.35–0.36 µs, ~70 B, 2 allocs |
| 0027 whole-tenant read (graph load, views), 1,000 + 4,000 | ~660 µs |

So the decision path is not slower, and this read no longer grows with the
deployment. The whole-tenant read is still a full copy. It runs in the graph
load (every `decision.DefaultRefreshInterval`) and operator views, not per
authorize. The `main` figure came from a throwaway benchmark in a worktree of
`origin/main` and is not committed.

### Configuration delivery (0021) through the plane

`config_changed` goes through the log like any revocation. 0021's markers stay,
because the desired document's commit and the announcement are two
transactions, so a crash between them still needs the retry. What changed is
the meaning of "the stream took it". Before, a node's bus reached only its own
subscriptions, so only a local delivery could settle the marker, and the
announcer announced only to proxies subscribed on its node. Now a logged event
reaches the holding node and is replayed on reconnect, so the marker settles at
log time, and any node's announcer retries any owed proxy. Duplicate
announcements cost a proxy nothing (it compares the hash). A proxy subscribed
nowhere gets one logged announcement, and fetches on reconnect as it always
did. `fleet.ConfigPublisher` lost its `delivered bool`, and `Announce` no
longer reads subscription state.

### Single node and the store-less bus

The daemon builds the bus over the store and the registered coordinator
(`daemon.buildBus`, which takes the node id from `coordinator.Node`). On one
node that is `extdefault.SingleNode`, whose in-process bus delivers each node's
own wake-up back to it, where `Origin` filters it out. There is one path.

`revoke.New` without `Options.Store` keeps a `memoryLog`, which is the 0009
broker exactly: per-process epoch, a ring per tenant. It exists so the 0009
tests run untouched without a database, and so a database-less embedding still
works. The code above `eventLog` does not branch on which log it has.

One single-node behaviour moved on purpose: the ring was one per process across
tenants, and is now one per tenant. A tenant's retention can only grow, and one
tenant's burst no longer resyncs another's proxies (M18).

### Test map

| Prerequisite | Test (`internal/revoke/plane_test.go`) |
| --- | --- |
| cross-node fan-out (kill and `config_changed`) | `TestAnEventPublishedOnOneNodeReachesASubscriberOnAnother` |
| one order | `TestConcurrentPublishesOnTwoNodesArriveInOneOrder` |
| replay on another node, floor | `TestAReconnectToAnotherNodeReplaysWhatItMissedOrResyncs` |
| restart replays | `TestARestartReplaysRatherThanResyncs` |
| subscription state and its bound | `TestTheHintGateSeesASubscriptionHeldOnAnotherNode` |
| failover: acknowledged event not lost | `TestANodeThatDiesAfterLoggingLosesNoAcknowledgedEvent` |
| dropped from the bus | `TestANodeDroppedFromTheBusResubscribesAndCatchesUp` |
| tenancy on every node | `TestOneTenantsEventsNeverReachAnothersSubscriptionOnAnyNode` |

The two "nodes" share one `extdefault.SingleNode` under per-node `Node` ids,
which is exactly a clustered bus from each node's side. The store half is in
`internal/store/events_test.go`, including
`TestConcurrentAppendsCommitInPositionOrder`: a reader following the log while
four writers append never sees a position commit behind one it already passed.
The south-bound HTTP harness now builds its bus over the store, so every
`events_test.go` case runs over the shared log. `make conform` against the real
binary: 58/58. Every 0009 bus test passes unedited, with `-race`, three runs.

### Follow-ups, none blocking

- **0033** reports "event-bus and replication health" (M19). The signals are
  here: a cursor's distance behind its tenant's head, and the `revoke_wake_*` /
  `revoke_presence_*` warnings. Nothing exposes them yet.
- `events.replay_buffer` defaults to 1024 per tenant. With the log durable, a
  larger value is cheap, and a `config_changed` to a zone of more proxies than
  that resyncs every reconnecting proxy. Raising the default was left out to
  keep this phase in scope.
- The coordinator's `Members` is still unused by Control. Nothing here needed
  it, because rows age out by last-seen rather than by membership.
