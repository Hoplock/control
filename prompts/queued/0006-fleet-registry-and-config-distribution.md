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
- A proxy enrolls with an identity (its `proxy_id` and public key) and
  declares: its **zone**, which zones it can reach and how (`dial` to a given
  address, or `relay` meaning a downstream proxy registers outbound to it),
  and its capabilities (contract version, supported credential methods).

  The enrolled contract version is **operational data, not the authority on
  what a given connection may be answered with**: the proxy declares that
  per call in `policy_version` on the authorize request (contract v2), and
  0008 answers within *that*. Treat the enrolled value as a fleet-readiness
  signal — "can this zone be routed through yet" — and never as a substitute
  for the request field, which is the only one that cannot be stale.
- Liveness from the revocation stream's subscription plus an explicit heartbeat.
  Define **stale** precisely and make it configurable: a proxy that has not
  been heard from is not a routing option, and routing through one is an outage
  the user experiences as a hang.
- Enrollment is an administrative act: a proxy cannot enroll itself into a zone
  it was not granted. Say how a new proxy is approved (a pre-registered id, an
  enrollment token, or an operator action in 0014) and enforce it. An
  auto-enrolling fleet lets anyone who can reach this server insert a hop into
  other people's routes.

### The graph & pathfinding
- Zones and the edges between them, with each edge carrying its direction and
  cost. Targets belong to zones; users enter at a proxy.
- `Path(entry, target) → []Hop` — shortest viable path, where "viable" means
  every edge is currently live and every hop supports what the next step needs.
- Each hop carries what the contract's hop metadata needs, including the
  **connection direction** and the hop trail that lets a proxy detect loops.
- The trail is not only an output. Since the proxy's phase 0008
  (`Hoplock/proxy#6`, merged) an authorize request arrives carrying
  `conn.hop_trail`: the ids the session has *already* traversed. `Path` must
  therefore take a real entry point rather than assuming the user's — the second
  hop of a chain asks for itself, from where it stands (PLAN §2 M6). Shape the
  signature for that now; 0008 is what passes the trail in, and a `Path` that
  can only start at a user's entry proxy is one 0008 has to work around.
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
what the console's fleet screen (0016) renders and what an operator looks at
first during an incident.

### Declared capabilities (M17)

Enrollment and heartbeat carry more than reachability. A route may name a
credential method, a device platform, an expiry posture, an enforcement rung, or
— since contract v3.1 — one or more **additional device fields**
(`device_field.<name>`, proxy D13, D14, phase 0016, and contract v4), and a proxy
can serve those only if it has the driver and the local material. So a proxy
declares what it can provide, this registry stores it, and 0008 treats it as a
**constraint on what a decision may say** — not as advice.

Device fields make the declared set two levels deep and it must be stored that
way: a driver declares the field **names** it accepts, per platform, and the set
is open — the contract enumerates no names, so this registry must store whatever
a proxy declares rather than validating against a list of its own. `vdom` on
`fortigate` is the one documented today; it is an example, not the schema.

The consequence of a mismatch is specific, and worth building for: a rung naming
a field the enforcing proxy's driver does not declare is a **skipped rung** on
the proxy, not a dropped field (proxy D14 — an unknown parameter may be a
constraint, so a proxy that cannot honour one must not connect). It is therefore
invisible in the response: the ladder just gets shorter, and on a one-rung ladder
the session is denied. Nothing downstream can reconstruct why, which is what
makes storing the declared names here load-bearing rather than informational.

### Capabilities have two sources since contract v4, and the second is the target

`Hoplock/proxy#25` (merged) adds the enforcement rung, and it breaks an
assumption the paragraphs above quietly make: that a capability is a property of
the *proxy*. An enforcement rung depends far more on the **target** — whether it
runs systemd, whether cgroup v2 is mounted, whether SELinux is enforcing,
whether netfilter is reachable, whether it is a Linux host at all — and none of
that is in a policy database or in an enrollment payload. So build the store to
hold both from the start; retrofitting a second key onto a proxy-keyed table is
the expensive version of this.

- **Per proxy** — `AuthorizeRequest.capabilities`, the rungs a *build* implements,
  declared per call beside `policy_version`. Absent declares nothing. This is the
  same "operational data, not authority" distinction as the enrolled contract
  version above: the request field is the one that cannot be stale.
- **Per target** — `POST /v1/capabilities/report`, the rungs one *target* can take,
  probed after the proxy has logged in. `/v1/authorize` happens **before** the
  proxy has ever touched the target, so a first-ever connection has nothing to put
  on the request; the report is the only path by which this fact arrives at all.
  **0007 serves the endpoint** (it is the sibling of `/v1/hostkeys/report` and
  takes that shape); define the store and its interface here, as this phase
  already does for the liveness interface 0009 implements. Key it by target — and
  by `platform` where one is reported, since an `ephemeral-account` device is
  observed through a driver.

Three rules to build rather than infer:

- **Stale, undated and absent are one case, and it fails safe.** A record older
  than its TTL, a record whose `observed_at` is missing, and no record at all are
  treated identically: they provide **nothing that has to be applied**. A record
  with no observation time is stale by definition — a capability with no date has
  no shelf life. What they must *not* affect is a rung needing nothing of the
  target: the two proxy-side defaults and an attested rung, which nobody applies.
  That is precisely how an appliance nobody can probe still carries a real
  enforcement claim rather than dropping to "none available", and it is the
  assertion most likely to be got wrong by an implementation that treats "no
  capabilities known" as "deny everything".
- **The server owns the freshness of its own record.** Answer
  `report_after_seconds` and decide the interval here; a proxy may re-observe
  sooner, never later. It is the same reasoning as a cache TTL.
- **A report is an observation and grants nothing.** The authority for a rung is
  the authorize response, and the proxy re-checks the rung against the live target
  when it provisions. So the worst a stale record can cause is a **refused
  session** — never a session running below the rung its own audit record claims.
  Do not build a path where a report can widen anything.

Two further consequences worth building for rather than discovering:

- **Capabilities are versioned and can go stale.** A proxy that has been
  upgraded advertises more; one that has been downgraded advertises less. A
  decision built on a capability set the proxy no longer has must fail as an
  outage rather than as a session whose audit record claims a control that was
  never applied. Decide how staleness is bounded — heartbeat freshness is the
  obvious lever — and write it down.
- **An operator must see the mismatch before publishing, not after.** The
  north-bound API (0014) needs to answer "which proxies can actually satisfy
  this policy", which is a query over this data. Build the query here; 0014
  exposes it. Device fields belong in that answer for the reason above — a
  policy naming `device_field.vdom` where the enforcing proxy's FortiGate driver
  does not declare `vdom` is a ladder that quietly loses a rung in production and
  says nothing at publish time unless this query says it.

### The graph is per tenant (M18)
Pathfinding must never cross a tenant boundary. This is not an information leak
to be filtered out of a response — a cross-tenant hop is one customer's session
traversing another customer's infrastructure, and it is the single worst thing
this graph can do.

- A proxy belongs to a tenant **at enrollment**, resolved from its enrollment
  credential. The contract carries no tenant field and must not grow one: a
  proxy asserting its own tenancy is a caller asserting its own authority
  (M18), and the enrolment credential is what the server already trusts.
- `Path` is computed within one tenant's subgraph. An edge to another tenant's
  zone does not exist rather than being pruned late — a viable-looking path that
  is filtered at the end is a path some later refactor will forget to filter.
- Zones are per tenant, so two tenants may both name a zone `prod` and mean
  different things. Do not make zone names globally unique to dodge this; that
  is a customer-visible constraint invented to simplify an internal lookup.

## Out of scope
- Serving `/v1/authorize` (0008 calls into this package).
- The revocation stream itself (0009), though liveness reads its subscription
  state — define the interface here and let 0009 implement it.
- Geo/anycast entry selection: DNS handles which proxy a user reaches. This
  phase starts from whichever proxy is asking, given as an input — the user's
  entry proxy on a first hop, an inner hop on a chained one.

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
- **Capability records: the three fail-safe states are one.** A target with a
  fresh record, one with an expired record, one whose record has no
  `observed_at`, and one with no record at all — the last three yield the same
  answer, and all four still allow the two proxy-side default rungs and an
  attested rung. Assert the undated case explicitly; it is the one an
  implementation is most likely to treat as fresh.
- The pre-publish query (for 0014) answers, for a given policy, which proxies can
  satisfy it **and** which targets can take its enforcement rung — over both
  capability sources, not just the proxy-declared one.
- Determinism: equal-cost paths resolve deterministically (say how — a stable
  tiebreak — so that two nodes answering the same request agree).
- **No path crosses a tenant.** Build a graph where the *only* route from
  tenant A's entry proxy to a reachable zone passes through tenant B's proxy,
  and assert the result is the explicit no-path outcome (an outage, M11) — never
  a path, and never a deny.
- Two tenants using identical zone names, proxy zones and target labels resolve
  independently; neither can observe the other's proxies in the fleet view.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0006-fleet-registry-and-graph-learnings.md`. Summary block MUST
give the graph and hop types, the `Path` signature, the liveness interface 0009
implements, the enrollment/approval mechanism, the staleness rule, and the
no-path outcome type that 0008 must translate into an outage.
