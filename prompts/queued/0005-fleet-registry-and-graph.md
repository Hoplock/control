# 0005 — Fleet registry & zone graph

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§2 (M6)**, §5.2 (route output), §4 (south-bound
  endpoints).
- `docs/learnings/` — read summaries; open `0003` (the `bastions` table) and
  `0002` (the contract's hop metadata).
- In the **bastion repository**, `docs/PLAN.md` **D11** and §6.1 — connection
  direction, relay registration, and why a `relay` hop with no live registration
  must fail rather than fall back to dialling.

## Objective
Model the bastion fleet as a **graph** and compute routes over it. This is what
makes multi-hop invisible: a user types one hostname, and the fact that reaching
it crosses an edge bastion, a regional gateway and an enclave relay is this
server's problem, not theirs.

It is also the only component that can choose a hop's **connection direction**
correctly, because only this server knows which downstream bastions currently
hold a live outbound relay registration.

## In scope

### Enrollment & liveness (`internal/fleet`)
- A bastion enrolls with an identity (its `bastion_id` and public key) and
  declares: its **zone**, which zones it can reach and how (`dial` to a given
  address, or `relay` meaning a downstream bastion registers outbound to it),
  and its capabilities (contract version, supported credential methods).
- Liveness from the revocation stream's subscription plus an explicit heartbeat.
  Define **stale** precisely and make it configurable: a bastion that has not
  been heard from is not a routing option, and routing through one is an outage
  the user experiences as a hang.
- Enrollment is an administrative act: a bastion cannot enroll itself into a zone
  it was not granted. Say how a new bastion is approved (a pre-registered id, an
  enrollment token, or an operator action in 0011) and enforce it. An
  auto-enrolling fleet lets anyone who can reach this server insert a hop into
  other people's routes.

### The graph & pathfinding
- Zones and the edges between them, with each edge carrying its direction and
  cost. Targets belong to zones; users enter at a bastion.
- `Path(entry, target) → []Hop` — shortest viable path, where "viable" means
  every edge is currently live and every hop supports what the next step needs.
- Each hop carries what the contract's hop metadata needs, including the
  **connection direction** and the hop trail that lets a bastion detect loops.
- **No live path is a deliberate, distinguishable outcome**: it is an outage
  (`5xx`, M11), not a deny. A user denied by policy and a user unreachable
  because an enclave relay is down must not receive the same answer — that
  distinction is the whole point of the bastion's disclosure rule.
- Enforce a maximum path length here as well as at the bastion. The bastion
  enforces it as a safety net against a bad answer; this server should not give
  one.

## Out of scope
- Serving `/v1/authorize` (0007 calls into this package).
- The revocation stream itself (0008), though liveness reads its subscription
  state — define the interface here and let 0008 implement it.
- Geo/anycast entry selection: DNS handles which bastion a user reaches. This
  phase starts from the entry bastion as an input.

## Acceptance criteria
- Unit tests over a multi-zone graph: a direct path, a two-hop path, a three-hop
  path, and a path that requires a `relay` edge.
- A relay edge whose downstream bastion has **no live registration** is not
  selected; if it is the only path, the result is an explicit no-path outcome
  that 0007 turns into an outage, never a deny. Test both.
- A stale bastion drops out of routing, and returns when it heartbeats again.
- Loop and max-length: a cyclic graph never yields a path containing a repeat,
  and an over-long path is refused.
- Enrollment: a bastion cannot claim a zone it was not granted; test the
  rejection.
- Determinism: equal-cost paths resolve deterministically (say how — a stable
  tiebreak — so that two nodes answering the same request agree).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0005-fleet-registry-and-graph-learnings.md`. Summary block MUST
give the graph and hop types, the `Path` signature, the liveness interface 0008
implements, the enrollment/approval mechanism, the staleness rule, and the
no-path outcome type that 0007 must translate into an outage.
