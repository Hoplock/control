# 0006 — Fleet registry, health & configuration distribution

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§2 (M6)**, §5.2 (route output), §4 (south-bound
  endpoints).
- `docs/learnings/` — read summaries; open `0003` (the `proxies` table) and
  `0002` (the contract's hop metadata).
- In the **Hoplock Proxy repository**, `docs/PLAN.md` **D11** and §6.1 — connection
  direction, relay registration, and why a `relay` hop with no live registration
  must fail rather than fall back to dialling.

## Objective
Model the proxy fleet as a **graph** and compute routes over it. This is what
makes multi-hop invisible: a user types one hostname, and the fact that reaching
it crosses an edge proxy, a regional gateway and an enclave relay is this
server's problem, not theirs.

It is also the only component that can choose a hop's **connection direction**
correctly, because only this server knows which downstream proxies currently
hold a live outbound relay registration.

## In scope

### Enrollment & liveness (`internal/fleet`)
- A proxy enrolls with an identity (its `bastion_id` and public key) and
  declares: its **zone**, which zones it can reach and how (`dial` to a given
  address, or `relay` meaning a downstream proxy registers outbound to it),
  and its capabilities (contract version, supported credential methods).
- Liveness from the revocation stream's subscription plus an explicit heartbeat.
  Define **stale** precisely and make it configurable: a proxy that has not
  been heard from is not a routing option, and routing through one is an outage
  the user experiences as a hang.
- Enrollment is an administrative act: a proxy cannot enroll itself into a zone
  it was not granted. Say how a new proxy is approved (a pre-registered id, an
  enrollment token, or an operator action in 0013) and enforce it. An
  auto-enrolling fleet lets anyone who can reach this server insert a hop into
  other people's routes.

### The graph & pathfinding
- Zones and the edges between them, with each edge carrying its direction and
  cost. Targets belong to zones; users enter at a proxy.
- `Path(entry, target) → []Hop` — shortest viable path, where "viable" means
  every edge is currently live and every hop supports what the next step needs.
- Each hop carries what the contract's hop metadata needs, including the
  **connection direction** and the hop trail that lets a proxy detect loops.
- **No live path is a deliberate, distinguishable outcome**: it is an outage
  (`5xx`, M11), not a deny. A user denied by policy and a user unreachable
  because an enclave relay is down must not receive the same answer — that
  distinction is the whole point of the proxy's disclosure rule.
- Enforce a maximum path length here as well as at the proxy. The proxy
  enforces it as a safety net against a bad answer; this server should not give
  one.

### Configuration distribution
A proxy's bootstrap config is local (it must be, to start at all), but
everything above that — which zones it serves, its relay registrations, its
contract expectations, its log shipping cadence — belongs here, so an operator
configures a fleet rather than N files.

- A **versioned configuration document** per proxy (or per zone, with per-proxy
  overrides), delivered on enrollment and on change.
- Delivery reuses the event stream (0009) rather than inventing a second
  channel: proxies already hold one outbound subscription and must not need a
  second inbound path.
- A proxy reports the config version it is running; drift between desired and
  running is **visible** in the fleet view and in the API. Silent drift across a
  fleet is indistinguishable from a broken rollout.
- Rolling out a bad config must be survivable: keep the previous version, and
  make rollback a first-class operation.

### Health & status
Per proxy: last heartbeat, contract version, running config version, live relay
registrations, current session count, and the last error it reported. This is
what the console's fleet screen (0014) renders and what an operator looks at
first during an incident.

## Out of scope
- Serving `/v1/authorize` (0008 calls into this package).
- The revocation stream itself (0009), though liveness reads its subscription
  state — define the interface here and let 0009 implement it.
- Geo/anycast entry selection: DNS handles which proxy a user reaches. This
  phase starts from the entry proxy as an input.

## Acceptance criteria
- Unit tests over a multi-zone graph: a direct path, a two-hop path, a three-hop
  path, and a path that requires a `relay` edge.
- A relay edge whose downstream proxy has **no live registration** is not
  selected; if it is the only path, the result is an explicit no-path outcome
  that 0008 turns into an outage, never a deny. Test both.
- A stale proxy drops out of routing, and returns when it heartbeats again.
- Loop and max-length: a cyclic graph never yields a path containing a repeat,
  and an over-long path is refused.
- Enrollment: a proxy cannot claim a zone it was not granted; test the
  rejection.
- Determinism: equal-cost paths resolve deterministically (say how — a stable
  tiebreak — so that two nodes answering the same request agree).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0006-fleet-registry-and-graph-learnings.md`. Summary block MUST
give the graph and hop types, the `Path` signature, the liveness interface 0009
implements, the enrollment/approval mechanism, the staleness rule, and the
no-path outcome type that 0008 must translate into an outage.
