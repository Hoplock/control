# 0027 — Shared event plane: the event stream correct on several nodes

## Read first
- `docs/PROTOCOL.md`: the session workflow, §3, §4 and §5.
- `docs/CROSS-REPO-PROTOCOL.md`: §1, §4.1 and §4.3. `hoplock/enterprise`
  implements `ext.ClusterCoordinator` for a clustered deployment (its E9), and
  this phase makes what Control needs from that seam load-bearing.
- `docs/PLAN.md`:
  - §2: **M9** (revocation is fan-out with replay), **M5** (no decision-path
    state that lives on one node only; the memories it allows and their bounds),
    **M15**, **M18**, **M11**, **M19** (a deployment has one identity, and
    nodes beneath it) and **M23**;
  - §3: `internal/revoke`, `internal/fleet` and `ext/`;
  - §4: the event stream's obligations (heartbeats, replay from
    `last_event_id`, `resync`), and "Configuration distribution (proxy D18)";
  - §5.4: cache hints, and the live-subscription gate they depend on;
  - §11: "HA/multi-region deployment".
- `docs/learnings/`: read every summary. Read `0009` **whole**: its Details give
  what multi-node must provide and what each part prevents. Read `0004`
  (`ext.ClusterCoordinator` and its single-node default), `0016` (tenants
  learned from subscriptions), `0020` (M5's node memories) and `0021`
  ("Delivery without reconnect", and how configuration delivery became correct
  on several nodes) where an item below touches them.
- `ext/cluster.go` and `internal/extdefault/cluster.go`.

## Why this phase exists
The event stream is in memory, one per process (M9: "in-process broker for the
prototype"). Event ids are a per-process counter, the replay buffer is a ring,
and the set of live subscriptions is a map. On several nodes:

- a `session_kill` published on node A reaches only the proxies whose streams A
  holds. Every other proxy keeps its sessions, and nothing says so. This
  **fails unsafely**, and it is the reason Control runs as one node today
  (§11);
- the cache-hint gate (§5.4) reads only its own node's subscriptions. It fails
  safely, by withholding hints, but it is decision-path state that lives on one
  node, which M5 forbids;
- a proxy that reconnects to another node with a `last_event_id` gets `resync`
  and loses its whole decision cache, even when nothing was missed.

`ext.ClusterCoordinator` already declares a deployment-wide bus
(`Publish`/`Subscribe`) and elections, with a single-node default. Nothing on
the event stream goes through it. Fleet configuration delivery is already
correct on several nodes: 0021 made each node's announcer deliver owed
announcements to the proxies whose streams it holds, using durable markers.
Revocations have no such marker.

## Objective
Make the event stream correct whichever node a proxy's stream is on. This is
Control's half: the guarantees, the shared state and the use of the seam. The
election and the cross-node transport are Enterprise's (its E9). Control alone
stays a complete single-node product (M15), with no second code path.

## In scope

### What must hold on N nodes
These are 0009's prerequisites. Each one gets a test.
1. **Cross-node fan-out.** An event published on any node reaches every live
   subscription it is addressed to, on whichever node that subscription is
   held, or the publisher learns that it did not reach it.
2. **One order, cluster-wide.** Event ids are monotonic across the deployment,
   so a `last_event_id` minted by one node means the same thing to another.
3. **Durable replay with a known floor.** A proxy reconnecting to any node is
   replayed what it missed when that is still held, and answered `resync`
   (never a gap) when it is not. A restart does not, on its own, force every
   proxy to `resync`.
4. **Cluster-wide subscription state.** The cache-hint gate and the liveness
   signal read whether a proxy holds a live subscription anywhere. The read on
   the decision path stays in memory, as a copy with a stated bound (M5's
   rule), and is never a database read per authorize.
5. **Failover.** A node that dies loses no event a client was told was
   published.

### Where the state lives: decide, then build
The candidates, and the questions to settle, each recorded with its answer:
- **The durable log and the sequence in Postgres**, Control's own store, with
  fan-out through `ext.ClusterCoordinator`'s bus. One node keeps working with
  the in-process default, and a clustered coordinator (E9) carries the fan-out.
- **Postgres alone** (`LISTEN`/`NOTIFY` for fan-out). This fan-out is Control's
  own, so check it against §11's "clustering is Enterprise" and M15.
- **The coordinator alone.** This needs ordering and replay the seam does not
  promise. Adding a method to `ext.ClusterCoordinator` is an incompatible
  change Enterprise has to implement (M23 §5). If you need one, list it under
  `### Breaking`.

Whatever you choose, state the delivery semantics you require of the seam (at
least once, ordering, what a dropped subscriber means) in `ext/cluster.go`'s
documentation. Enterprise's implementation is written against that text.

### Revocations, configuration and the existing surfaces
- `revoke.Bus`'s surface (`Subscribe`, `Kill`, `Invalidate`, `Resync`,
  `ConfigChanged`, `LiveSubscriptions`) stays what its callers see. Its
  behaviour changes underneath it.
- Configuration delivery's durable markers (0021) stay correct. Keep them, or
  route the announcement through the plane, and say which and why.
- The slow-subscriber rule (M9: dropped, never waited for) holds on every node.
- Tenancy (M18): every shared row and topic carries the tenant, and a
  two-tenant test proves one tenant's events never reach another tenant's
  subscription on any node.

## Out of scope
- An election, membership or a cross-node transport of Control's own beyond
  what the decision above settles. Clustering is Enterprise's (its E9).
- HA of Postgres, and multi-region.
- The supervisory registration (0032), which builds on this phase's shape.

## Acceptance criteria
- An in-process test runs two buses over one store and a coordinator that fans
  out between them. In it, a `session_kill` published on node A reaches a proxy
  subscribed on node B, and so does a `config_changed`.
- A proxy reconnecting to node B with node A's `last_event_id` is replayed
  what it missed, and is answered `resync` below the floor. After a restart,
  replay works without forcing every proxy to `resync`.
- The cache-hint gate on node A sees a subscription held on node B within its
  stated bound, and the decision path's latency is unchanged (measure it).
- A node killed mid-publish loses no acknowledged event.
- Single-node behaviour is unchanged: every revocation and conformance case
  from earlier phases passes untouched.
- `go build`, `go vet`, `go test ./...` and `-race` on the changed packages
  pass, and `golangci-lint` at CI's pin reports 0 issues.
- `make contract-check`, `license-check`, `release-check` and `make conform`
  pass.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0027-shared-event-plane-learnings.md`. Its summary block MUST
give:
- where each piece of shared state lives;
- the event id format;
- the replay floor and how it is kept;
- the delivery semantics required of `ext.ClusterCoordinator`;
- the bound on the decision path's subscription copy;
- any `ext` change.

Amend M9 by a decision of its own, or revise it in place, and update the §2
register in this PR. Rewrite §11's "HA/multi-region deployment" bullet to say
what is now true.

**Release.** The next MINOR after the latest tag (M23).

**Cross-repo impact.** `hoplock/enterprise` implements the seam this phase now
depends on. State the semantics it must provide, and any `ext` change by
identifier, in the PR's `## Cross-repo impact` section
(`docs/CROSS-REPO-PROTOCOL.md` §4.1). Queue the "Downstream sync" kickoff from
`docs/KICKOFF.md` as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md` once the PR
is open, and name the file (§4.3). For `hoplock/proxy`, write **None**: the
contract's stream is unchanged, and a proxy cannot tell which node it reached.
