# Hoplock Control — Implementation Plan

> Status: living document. This is the single source of architectural truth for
> the **Hoplock Control**. Every implementation session MUST read this file and
> `docs/PROTOCOL.md` before starting work. Keep it current: if a prompt changes
> the architecture, the same PR updates this plan.
>
> The **proxy** has its own repository and its own `docs/PLAN.md`, and it owns
> the contract between the two (M1). Where this plan cites a `D`-numbered
> decision (D1–D12), that decision lives in the proxy's plan and is quoted
> here, not owned here.

---

## 1. Product summary

Hoplock Control is the **open-source control plane** for Hoplock: the Policy
Decision Point (PDP) for a fleet of Hoplock Proxy instances, and the system an
operator uses to manage infrastructure access.

Hoplock Proxy is deliberately thin: it terminates SSH, enforces a decision, and
reports what happened. Everything that makes the decision — who someone is, what
they may reach, over which route, with which channels, requests, destinations
and commands, for how long — lives here, together with everything that happens
afterwards: the audit record, the replayable session, and the answer to "why was
I denied?".

### The three repositories

| Repository | Role | Depends on |
| --- | --- | --- |
| `hoplock/proxy` | **Enforces** access. Data plane: SSH proxying, channel/command controls, port-forward policy, multi-hop relay, audit events. | nothing in this list |
| `hoplock/control` (this repo) | **Manages** access. Control plane: proxies, targets, identities, routes, policies, audit, API, console. | the proxy's API contract only |
| `hoplock/enterprise` | **Governs** access at organisational scale. Commercial extensions: approvals, compliance, retention, SIEM/SOAR, HA. | this repo (M15) |

Control never imports Enterprise, and the proxy never imports either. Hoplock
Proxy + Hoplock Control is a complete, useful, self-hosted infrastructure access
system on its own — that is a design constraint (M15), not a marketing claim.

### Two surfaces, one product

South-bound is a machine API serving proxies under hard latency constraints
(M5); north-bound is a human, CI and console API for authoring, investigating
and administering. They share a policy model and a database and nothing else —
different listeners, different authentication, different threat models (M2).

```
   identity provider                       operators, CI, GitOps
   (OIDC / SAML)                           (OIDC / API tokens)
            |                                     |
            v                                     v
     +------------------ Hoplock Control ------------------+
     |  policy engine | fleet graph | identity | grants     |
     |  audit store (append-only, hash-chained)             |
     |  ext/ <---- implemented by Hoplock Enterprise (M15)  |
     +------------------------+-----------------------------+
                              | policy + configuration (south-bound)
                              v
              Hoplock Proxy ------> Hoplock Proxy ------> target
                              ^
                              | audit events, host keys, revocation stream
```

### What one session looks like from this side

1. A proxy authenticates a user's certificate or password (+ MFA) against
   `/v1/auth/*`. This server owns the MFA conversation; the proxy only relays
   and polls.
2. The proxy calls `/v1/authorize` with the identity, the requested target,
   and connection metadata.
3. This server evaluates policy over: the identity's claims and groups, the
   target's labels, any live JIT grant, the time, the source network, and the
   fleet graph. It answers `401` or a **whole-connection policy snapshot** —
   route, channel/request/destination policy, filter policy, target credential
   method, hop direction, and optionally a cache hint.
4. It writes a **decision record** explaining exactly how it got there (M4).
5. The proxy enforces locally for the connection's lifetime and streams logs
   back to `/v1/logs/*`. Nothing on the data path asks this server anything.
6. If access is withdrawn mid-session, this server pushes a `session_kill` down
   the revocation stream the proxy is already holding open (M9).

---

## 2. Key decisions

Each decision has an ID so prompts and learnings can reference it. `M` for
management; the proxy's decisions keep their `D` numbers.

**Register.** One row per decision: what it settles, its current status, and
where it is rendered. The point is that a reader can see from here whether a
decision still says what it appears to say, without reading it
(`docs/PROTOCOL.md` §3). `Rendered in` lists the other sections of this plan
that carry the decision — §10 is the phase table, so it also says which phase
builds it. Update this table in the same PR that adds, amends, or withdraws a
decision.

| Decision | Settles | Status | Rendered in |
| --- | --- | --- | --- |
| **M1** | the contract is owned upstream and vendored read-only | live | §3, §4, §8, §11 |
| **M2** | two surfaces, two listeners, two credential types | live | §3, §10 |
| **M3** | policy is data compiled into a decision program | live | §3, §5, §10 |
| **M4** | every decision is explainable, durable, addressable | live | §3, §5, §10 |
| **M5** | the decision path is stateless, bounded, cheap | live | §3, §10, §11 |
| **M6** | the fleet is a graph, not a list | amended by M17 | §3, §5, §10 |
| **M7** | identity is federated and short-lived | live | §6, §10 |
| **M8** | audit is append-only and tamper-evident | live | §3, §7, §10 |
| **M9** | revocation is fan-out with replay, and the kill switch | live | §3, §5, §10, §11 |
| **M10** | JIT grants are policy inputs, not a bolt-on | live | §3, §5, §10 |
| **M11** | `401` means deny; everything else means outage | live | §3, §4, §8 |
| **M12** | tenancy is in the schema from day one | amended by M18 | §10 |
| **M13** | tech choices (Go, Postgres, closed enum kinds) | live | §3 |
| **M14** | licensing: this repository is the open-source plane | live | §8 |
| **M15** | Enterprise extends this repository; it never forks it | live | §3, §10, §11 |
| **M16** | external access context is an input; its integrations are extensions | live | §5, §7, §10, §11 |
| **M17** | the fleet graph carries capabilities, not just reachability | live | §4, §5, §10 |
| **M18** | tenancy is a request dimension, not a process constant | live | §10, §11 |
| **M19** | a deployment has an identity and can be supervised | live | §3, §10, §11 |
| **M20** | the console is a product surface with a specified design | live | §3, §10 |
| **M21** | the console is localisable; English is the only catalogue | live | §3, §10 |
| **M22** | a south-bound credential carries its tenant and names its proxy | live | §3, §10 |

- **M1 — The contract is owned upstream; this repo vendors it read-only.** The
  PEP↔PDP contract is `api/control.yaml` in the Hoplock Proxy repository,
  `github.com/hoplock/proxy`. This repo keeps a pinned copy under `contract/`,
  together with the upstream commit it came from, and treats it as generated:
  nobody edits it here. A change
  starts in the Hoplock Proxy repository, lands there, and is pulled in with
  `make contract-sync`, which is also a CI check — a silently edited local copy
  is how two components stop agreeing while both test green.

  Conformance is proven, not asserted: `cmd/pdpconform` is a **black-box
  conformance suite** that drives any implementation of the contract over HTTP
  and is run in CI against both this server and Hoplock Proxy's
  `cmd/mock-control`. Running it against the mock is what keeps the suite
  honest — a suite only ever run against the implementation it was written
  beside tests agreement with itself.
- **M2 — Two surfaces, two listeners, two authentication models.** South-bound
  (proxies) and north-bound (humans, CI, GitOps) never share a port, a
  middleware chain, or a credential type. A proxy token that can reach a
  policy-authoring endpoint is privilege escalation from "can ask about
  decisions" to "can author them", and the cheapest way to guarantee it cannot
  happen is for the two never to be routable from the same listener. South-bound
  is a bearer token in the prototype with mTLS as the intended production form
  (the proxy's contract already treats this as a thin seam); north-bound is
  OIDC for humans and scoped API tokens for automation.

  What is *in* the south-bound token — and why it is not merely an opaque
  string — is **M22**.

  **The north-bound listener is bound by 0011, not by 0014.** This plan used to
  say 0014 owned its bring-up, on the reasoning that 0014 owned its credential
  model and a bearer path added earlier would pre-empt that design. 0011 is what
  that was waiting for: it *is* the credential model — an OIDC session or a
  scoped API token, carrying the set of tenants it may act in, checked by one
  middleware that resolves exactly one tenant and one permission per request
  (M18). So the listener comes up with the routes 0011 owns (federation,
  sessions, the certificate authority's operator surface) and 0014 adds routes
  to a listener that already authenticates. What M2 forbids is untouched: the
  two surfaces still share no port, no middleware chain and no credential type,
  and `TestNoContractRouteIsReachableOnThisListener` is what keeps that true
  from this side.

  **The rule is about surfaces, not about a count of ports.** Until the
  north-bound API exists (0014) there are two operator actions this server has
  to be able to take, and the contract states outright that neither is on `/v1`
  (§4) — because no proxy performs either, so an endpoint for one would be
  something every Hoplock Control implements and nothing calls:

  - **publishing a revocation event**, because gap recovery is not gradeable
    without it. Phase 0009 serves it from a listener of its own
    (`events.publish_listener`).
  - **reading a stored audit record back**, because the priority ack's
    durability guarantee is otherwise unobservable — a `200` that means "stored"
    can only be graded by asking for the record straight afterwards. Phase 0010
    serves it from a listener of its own (`audit.read_listener`), on a third
    port, because publishing the kill switch and reading everybody's audit
    records are different privileges and a credential for one is not a
    credential for the other.

  Both are **off unless configured** and refuse to bind without a credential of
  their own. Neither is a new surface: each is the north-bound surface's
  temporary front door, on a separate port rather than the north-bound one
  because each predates the credential model 0011 landed — a bearer path on the
  north-bound port before then would have pre-empted that design and left the
  port half-real. They stay on their own ports now rather than being folded in,
  because 0014 **deletes** them (below) and moving them twice is two migrations
  for the same removal. What M2 forbids still holds without exception: neither
  shares the south-bound port, chain, or credential.

  **0014 deletes them rather than folding them in.** A finished product has no
  debug endpoint, so the rule that let these exist at all
  (`docs/PROTOCOL.md` §3) required a named successor whose own prompt carries
  the removal — and 0014's does, file by file, with an acceptance criterion. A
  supersession that leaves the old path bound has superseded nothing.
- **M3 — Policy is data compiled into a decision program, not an embedded
  general-purpose language.** The policy input vocabulary is closed and known:
  subject, claims, groups, device posture, source network, time, target labels,
  channel type, in-channel request, forwarding destination, global request,
  command, and live grants. The output vocabulary is equally closed: a route, a
  channel/request/destination policy, a filter policy, a credential method, a
  cache hint, and obligations (record, approve, step up).

  Rego/OPA was considered and rejected **for the decision path**. A closed
  vocabulary buys four things this product sells: exhaustive validation at
  authoring time (an unreachable or contradictory rule is a compile error, not a
  runtime surprise), explanations that name a rule rather than a term binding,
  simulation over historical inputs that is guaranteed total, and a latency
  bound that does not depend on how someone wrote their policy. The compiler is
  a boundary, so a future customer demand for arbitrary logic can be met by
  adding a backend rather than by rewriting the engine.
- **M4 — Every decision is explainable, durable, and addressable.** Each
  evaluation produces a **decision record**: the inputs it saw, the rules it
  matched, the obligations it emitted, and the resulting snapshot, stored under
  the `decision_id` the contract already carries. This is the other half of the
  proxy's disclosure rule (proxy PLAN §4.3): the user is told "access
  denied" and a session id — deliberately vague, because a precise denial makes
  the proxy an oracle for probing the estate — and the operator resolves that
  id here into the whole story. Vague to the user, total to the auditor; that
  pair only works if this side is genuinely total.

  The same records back policy simulation ("what would this bundle have done to
  last week's traffic?"), which is what makes a policy change reviewable rather
  than a leap.
- **M5 — The decision path is stateless, bounded, and cheap.** A proxy holds a
  user's SSH handshake open while this server answers, on every connection, per
  hop. So: no unbounded work in an authorize call, compiled policy served from
  memory, database reads on the decision path limited to what is indexed and
  small, and a hard server-side timeout that answers rather than hangs — a
  timeout the proxy classifies as an outage is strictly better than a slow
  answer that looks like one. Horizontal scale-out is the only scaling story;
  nothing on the decision path may be node-local state.

  **The magnitude is not modest.** The first target deployment is a telco with
  ~300,000 network devices and ~50,000 Linux hosts. If machine-to-machine health
  checking of that estate runs over SSH on a one-minute interval, the decision
  path sees ~5,800 authorize calls per second **sustained**, plus the
  authenticate call the proxy may never cache (proxy §6.4). That figure is
  arithmetic rather than measurement — the proxy's scale-harness phase
  (0020, "replace the arithmetic with measurement") exists to replace it,
  and the SSH-versus-SNMP assumption underneath it may well collapse it by two
  orders of magnitude — but it is the right order of magnitude to design the
  decision path against, and it is the reason M5 is a decision rather than an
  aspiration. Anything that is "fine at a hundred a second" is not fine here.
- **M6 — The fleet is a graph, not a list.** Proxies enroll, heartbeat, and
  declare their zone, their reachability, and which connection directions they
  support (proxy D11: `dial` or an outbound-registered `relay`). A route is a
  **path computed over that graph** from the user's entry proxy to the target's
  zone, and the hop metadata the proxy receives is one step of it.

  This is what makes multi-hop invisible: the user types one hostname, and the
  fact that reaching it crosses an edge proxy, a regional gateway, and an
  enclave relay is this server's problem. It is also the only place that can
  choose `relay` correctly, because only this server knows which downstream
  proxies currently have a live outbound registration — which is exactly why
  direction is a routing decision and not a proxy config flag.

  **Each hop asks for itself, so each path is computed from the asking hop**
  (proxy D2, and its phase 0008 now sends this). The entry proxy is where the
  *user* arrived; the proxy asking on the second leg is one step further in, and
  the path it needs starts where it stands. `conn.proxy_id` says where that is
  and `conn.hop_trail` says how the session got there — so the same login and
  target legitimately answer `nexthop` at the edge and `direct` behind it, and
  answering the edge's route to the inner proxy would build a loop. §4 states
  the obligation; 0006 computes the path and 0008 serves it.
- **M7 — Identity is federated and short-lived.** OIDC and SAML are brokered
  here; the proxy never talks to an IdP (proxy D4 makes it identity-shaped
  precisely so that this can be added without touching it). IdP claims and
  groups map into policy attributes through an explicit, versioned mapping —
  never by trusting a raw claim name straight from a token. Local credentials
  exist for development and for break-glass only, are flagged as such in the
  audit record, and are never the production path.
- **M8 — Audit is append-only and tamper-evident.** Records are immutable, hash
  chained per stream so that a removed or altered record breaks verification, and
  ingest is idempotent on the client-assigned `record_id` the contract already
  defines (a proxy draining its disk buffer after an outage will resend). The
  audit store is the system of record; the SIEM export is a downstream consumer
  and is never read back as truth. Retention is a policy, applied by a documented
  job, that records what it deleted.
- **M9 — Revocation is fan-out with replay, and it is the kill switch.** The
  proxy holds one long-lived outbound NDJSON subscription (proxy §6.4). This
  server fans an operator action out to every proxy that needs it, survives a
  subscriber reconnecting with a `last_event_id`, and answers `resync` when it
  cannot replay. In-process broker for the prototype (`internal/revoke`),
  because multi-node deployment turns this into the one component that
  genuinely needs shared state: the event ids are a counter in memory and the
  replay buffer is a ring in memory, so an id minted by an earlier process is
  answered with `resync` rather than believed.

  **A slow subscriber is dropped, not waited for.** Blocking the publisher
  would make one stalled proxy an outage for the fleet and growing its queue
  would make it an out-of-memory; a dropped subscriber reconnects into replay
  or `resync`, so it costs that proxy its cache and nothing else.

  Corollary the contract already states and this server honours: **a server
  that issues cache hints must serve this stream.** Issuing a hint without a
  working revocation path is issuing an access grant that cannot be withdrawn —
  which is why the hint gate reads live subscription state and why, before this
  stream existed, both responses that carry a hint answered without one.
- **M10 — JIT grants are policy inputs, not a bolt-on.** "Developer requests 30
  minutes on prod, on-call approves, access disappears afterwards" is modelled as
  a first-class **grant** object — subject, scope, expiry, approvers, and the
  request that produced it — and the decision engine reads grants as another
  input. Not a special case bypassing the engine: a special case would be
  invisible to simulation and to "explain why", the two features that make the
  rest of the policy story credible. Approval notifications go out through a
  notifier interface (Slack, Teams, webhook, email) that has no other job.

  A grant's **origin varies and is recorded**: an administrator created it by
  hand, an approval workflow produced it (Enterprise E8), or an external system
  asserted a window and a provider confirmed it (M16). All three are the same
  object to the engine — that is the point — but the record carries which one it
  was and, where it applies, the external reference (ticket, scan, incident) and
  the window that was asserted. "Explain why" that cannot name the ticket is not
  an explanation.
- **M11 — `401` means deny; everything else means outage.** The contract's
  ground rule binds this side hardest. A database timeout, a compile error, or a
  panic must never surface as `401`, because the proxy will faithfully tell a
  user "access denied" and the operator will spend the outage debugging
  permissions. Deny is a decision this server made on purpose; every other
  failure is a `5xx` with a message safe to disclose and a correlation id.
- **M12 — Tenancy is in the schema from day one.** The prototype is
  single-tenant and nothing in the API exposes tenancy. Every table still
  carries the tenant column and every query still filters on it. It is nearly
  free now, and retrofitting it into a populated audit store later is a
  migration nobody wants to run.

  **Amended by M18**, which makes tenancy a dimension a caller selects rather
  than a constant the process is configured with. M12 bought the column; M18
  pays for the plumbing.
- **M13 — Tech choices.** Go (same floor policy as the proxy: the `go`
  directive is the minimum, CI tracks the latest stable, and the floor moves only
  when a dependency moves it). Postgres via `pgx`, with forward-only versioned
  migrations. YAML config. JSON over HTTPS. Structured logging. No ORM: the
  decision path's queries are few, hot, and worth reading.

  **Go is not chosen on its own merits; it is forced, and by decisions already
  taken here.** M15 has Enterprise import this module and implement `ext/`
  *in process* — a cross-repository plugin seam, which requires one language and
  one runtime on both sides. M1 makes it three repositories rather than two: the
  proxy is Go, `contract/` is vendored from it, and `cmd/pdpconform` must drive
  this server **and** the proxy's `cmd/mock-control`, so a non-Go control plane
  means the suite that keeps the two honest can share neither types nor
  fixtures with one of them. The SSH CA (proxy D6a) sharpens that: certificates
  must match the proxy byte for byte, and `x/crypto/ssh` is already on the far
  end. Add the deployment shape this category of software ships in — one static
  binary with the console embedded, no runtime for an operator to install — and
  a decision path that is I/O-bound fan-out rather than computation, with M9's
  long-lived per-proxy subscriptions being precisely what goroutines are for.

  **The cost is real and it lands on one package.** M3 promises a closed input
  and output vocabulary, an unreachable or contradictory rule caught at
  authoring time, and simulation that is guaranteed total. Those are sum-type
  promises, and Go has neither sum types nor exhaustive matching: add an
  obligation kind or an output axis and nothing in the language names the type
  switch you did not update. So `internal/policy`'s correctness rests on tests
  and lint rather than on the type system, and saying so is more useful than
  pretending otherwise. Three things follow, and they are requirements rather
  than suggestions:

  1. `exhaustive` is in the linter set (`.golangci.yml`) for this reason alone,
     configured so that a `default` clause does **not** excuse an unhandled
     member — defaulting is exactly how a newly added case silently inherits
     the old behaviour, which here means a policy output nobody authored.
  2. The compiled decision program is a **closed tagged representation**: each
     variant axis carries one named `Kind` enum with its members declared as
     constants, so the linter has something to check. Open interfaces where an
     enum would do defeat the only guard available (0005).
  3. `internal/policy` stays pure — no HTTP, no database, no ambient clock — so
     that M3's "the compiler is a boundary" remains a real escape hatch and not
     a slogan. If the evaluator ever has to be something other than Go, that
     boundary is where it gets replaced.

  Rust and Elixir were the two alternatives with an honest case — Rust for the
  compiler and for tail latency under M5, Elixir for M9's fan-out and
  supervision — and both lose on the same thing: they fork the estate away from
  a Go proxy and break M15's in-process seam. GC tail latency against M5's
  budget is a measurement question for the proxy's scale harness (its phase
  0020), not an argument to have here.
- **M14 — Licensing.** This repository is the **open-source** control plane;
  its licence is chosen at scaffold time (phase 0001) and applied per file via
  `docs/LICENSE-HEADER.md`. Hoplock Enterprise is separately licensed and
  carries its own entitlement machinery — no licence check, entitlement gate, or
  "upgrade to unlock" path belongs in this repository.

- **M15 — Hoplock Enterprise extends this repository; it never forks it.**
  Commercial functionality lives in `github.com/hoplock/enterprise`, which
  imports this module as a library, implements the interfaces in `ext/`
  (phase 0004), and ships its own binary. Two invariants make that work, and
  both are enforced by tests rather than by intention:

  1. **Control never imports Enterprise.** The dependency runs one way. An
     import-graph test fails the build if it ever does not.
  2. **Every extension point has a real answer here.** A seam is not a hole
     where core functionality used to be. Control alone must be a complete,
     self-hostable product: a deployment of Hoplock Proxy + Hoplock Control is
     a working infrastructure access system, not a demo waiting for a licence.

     That answer takes one of exactly three forms, and `ext.PointInfo` records
     which, so the claim is checkable rather than aspirational. **Core:**
     Control's own code path continues and the seam is purely additive — the
     local audit store, manual grants, its own software keys, the compiler's
     checks — and the catalogue names the phase that builds it. **Default:**
     everything above the seam goes through it, so Control's wiring registers an
     implementation behind it (`internal/extdefault`), and the registry refuses
     to seal if one was promised and is missing. **Disabled:** the point adds a
     capability Control never claimed — a long-term archive, directory
     provisioning, somebody else's automation pulling a lever an operator can
     already pull — and says so out loud. A point that supplies nothing from
     Control and is not the third case fails the build, which is what stops
     "ships a real default" from decaying into a promise.

  The line is **governance and scale, not capability**. Control decides access,
  distributes policy, records what happened, and lets an operator explain any
  decision. Enterprise adds who-may-ask-and-who-must-approve, long-horizon
  retention and search, compliance reporting, enterprise integrations
  (SCIM, SIEM/SOAR, HSM/KMS), and the operational shape large deployments need
  (HA, air-gapped, licensing). Where a feature could plausibly sit on either
  side, it goes here — an artificially crippled open-source core is a worse
  product and a worse business.

- **M16 — External access context is an input, and the integrations that supply
  it are extensions (new).** Some access is legitimate only while something
  outside Hoplock says so: a vulnerability scan is running against this host, a
  change ticket is approved and inside its window, an incident is open. Proxy
  D15 places that squarely here — the proxy stays ignorant of Qualys, of BMC
  Helix, and of whatever a customer builds, because a push that grants access
  and a probe that validates one are both **policy inputs**, consumed while this
  server decides.

  It arrives in two directions and they are **not redundant**:

  - **Push** — an external system tells Hoplock that a window has opened. Fast,
    and it is the only direction that works when the external system cannot be
    reached at decision time. It is also spoofable and lossy.
  - **Probe** — Hoplock asks the external system whether the access is
    legitimate right now. Authoritative, and it costs a network round trip on
    the decision path, which M5 governs.

  So the default composition is: a **push opens a pending window**, and a
  **probe confirms at authorize time**. Where the probe cannot be reached, the
  answer is configurable and **fails closed for privileged grants**, because the
  access this exists to gate is the access least safe to grant on a stale
  assertion.

  Three properties belong to the framework rather than to each integration, and
  a customer-written provider gets them for free precisely because they are not
  its job:

  1. **A push is untrusted input that names a target.** It is authenticated, and
     it is constrained to a **pre-registered scope** — the subjects, targets, and
     maximum window that integration may ever grant. Without that constraint the
     push endpoint *is* an access-granting API with a vendor's software on the
     other end, which is proxy D15's warning and this repository's problem to
     answer. It is administrative in exactly the sense Enterprise E7 means, and
     more so: granting access is a larger privilege than ending a session.
  2. **Replay, idempotency, and clock skew** are handled once. An integration
     that gets these wrong is the weakest link in a chain that includes every
     other integration.
  3. **A server-side ceiling on window length**, applied regardless of what the
     external system asserted. An integration may ask for less than the ceiling
     and never more.

  A confirmed window **is a grant** (M10) — not a parallel path into the
  decision. It carries its origin and its external reference, the engine reads
  it like any other grant, and simulation and "explain why" keep telling the
  truth, which is the entire reason M10 refused a bolt-on in the first place.

  The seam is `ext.AccessContextProvider` (0004), and per M15 it ships a **real
  default here**: a *declarative HTTP provider* configured rather than coded —
  a probe endpoint, its authentication, a request template, assertions over the
  response, and a cache TTL, plus a webhook receiver with a field mapping. That
  default is not a stub standing in for the product; it is how a self-hosting
  customer integrates a system nobody has heard of, and it covers most scanners
  and ITSM systems without anyone writing Go. Packaged, vendor-specific
  integrations — Qualys and BMC Helix first — are Enterprise's, and they are
  packaging and support rather than capability, which is exactly where M15 draws
  the line.

- **M17 — The fleet graph carries capabilities, not just reachability (amends
  M6, new).** M6 has proxies declare their zone, reachability, and connection
  directions, because only this server can compute a path. The same argument
  now applies one level down: a route may name a **credential method**, a
  **device platform**, an **expiry posture**, an **enforcement rung**, and a set
  of **additional device fields** (proxy D13, D14, and proxy phase 0016), and a
  proxy can satisfy those only if it has the
  driver, the local material, and a target that supports them.

  A decision naming something the enforcing proxy cannot provide is not a
  near-miss — it is a session that is denied as an outage, or worse, one whose
  audit record claims a control that was never applied. So enrollment and
  heartbeat carry the proxy's declared capabilities, the decision engine treats
  them as a constraint on what it may answer, and the north-bound API shows an
  operator why a policy cannot be satisfied on a given proxy **before** they
  publish it rather than after a user complains.

  Device fields sharpen that point rather than adding a new kind of problem. Each
  driver **declares the field names it accepts**, so what is carried is
  per-platform field names as well as methods — and the set is open, because the
  contract enumerates no names (proxy D13 makes customer-written drivers
  first-class). A rung naming a field its driver does not declare is a **skipped
  rung** on the proxy, not a dropped field: an unknown parameter may be a
  constraint, and a proxy that cannot honour one must not connect (proxy D14).
  That is what makes the addition safe, and it is also why the mismatch is worth
  catching here — a skipped rung is invisible in the response, so the ladder just
  gets shorter, and on a one-rung ladder the session is denied and no one
  authored the denial.

  This also makes proxy D14's ladder authorable with intent: knowing which
  methods a proxy actually has is what separates "prefer the strong method, fall
  back to the weaker one" from "write a ladder and hope".

  **The capability question is two-sourced, and the second source is the
  target.** An enforcement rung depends far more on the target than on the
  proxy — whether it runs systemd, whether cgroup v2 is mounted, whether SELinux
  is enforcing, whether netfilter is reachable, whether it is a Linux host at all
  — and none of that is knowable from a policy database. So capability facts now
  arrive on two paths and this registry holds both:

  - **Per proxy** — `AuthorizeRequest.capabilities`, the rungs a *build*
    implements, declared beside `policy_version` on the same pattern as the
    enrolled credential methods. Absent declares nothing.
  - **Per target** — `POST /v1/capabilities/report` (§4), the rungs one *target*
    can take, discovered by probing it after login. Authorize happens before the
    proxy has ever touched the target, so a first-ever connection has nothing to
    put on the request; the report is how that fact arrives at all. The server
    owns the freshness of its own record and says so with
    `report_after_seconds` — a proxy may re-observe sooner, never later.

  The two are ANDed, and the fail-safe direction is the one that matters: a
  capability record that is stale, undated, or absent provides nothing that has to
  be **applied**, while leaving every rung that needs nothing of the target — the
  two proxy-side defaults, and an attested rung, which nobody applies — available.
  That is precisely how an appliance nobody can probe still carries a real
  enforcement claim rather than dropping to "none available".

- **M18 — Tenancy is a request dimension, not a process constant (amends M12,
  new).** M12 put the tenant column on every table and a filter on every query,
  and it was right about the cheap half. The expensive half is that
  `config.example.yaml` binds a whole *process* to one tenant — "the tenant this
  deployment operates as" — so the column is currently a constant the server
  writes, never a dimension a caller selects. That is the retrofit M12 set out to
  avoid, just one layer up: a schema ready for tenancy under a server that cannot
  express it.

  So tenancy becomes a dimension **resolved from the caller, never asserted by
  it**, and the resolution differs by surface because the threat models do (M2):

  - **North-bound** — from the authenticated principal's scope. A token or an
    OIDC session names the tenants it may act in; a tenant in a path or a body is
    a selector *within* that set, never a widening of it. A caller that can name
    a tenant it was not granted is the whole vulnerability class this decision
    exists to close.
  - **South-bound** — from the proxy's enrolled identity. A proxy belongs to a
    tenant at enrollment and **the contract carries no tenant field**: a proxy
    asserting its own tenancy would be a caller asserting its own authority, and
    the answer to that is the same one §4 gives for `conn.hop_trail` — it may
    narrow, never widen. This is why multi-tenancy needs **no change to
    `hoplock/proxy`**, which is the strongest evidence the seam is in the right
    place.

  Four consequences are load-bearing, and each is a place where "filter on the
  column" is not enough:

  1. **The fleet graph is per tenant (M6).** Pathfinding must never route a
     session through another tenant's proxy. A cross-tenant hop is not an
     information leak, it is one customer's traffic traversing another
     customer's infrastructure.
  2. **The compiled decision program is per tenant (M3, M5).** Tenancy selects
     which program is served, at compile time; it is not a predicate evaluated
     per request. M5's budget does not have room for a tenant filter on the hot
     path, and a filter is the wrong shape anyway — a tenant is not a rule.
  3. **The audit chain is per tenant (M8).** Each tenant's stream is
     independently verifiable and independently exportable, because a customer
     leaving must be able to take a chain that still verifies. A single chain
     spanning tenants makes departure either a broken chain or a disclosure.
  4. **The SSH CA is per tenant (M7, proxy D6a).** One tenant's targets must not
     trust another tenant's CA. Key material is per tenant from the first
     issuance, for the same reason the audit chain is.

  Single-tenant operation stays the default and stays invisible: the config key
  remains, one tenant is resolved for every caller, and an operator who never
  wants tenancy never sees it. Cross-tenant isolation is a **test class**, not a
  review item — every repository, the graph, the compiler, and the audit chain
  each carry a two-tenant test that asserts one cannot reach the other.

  Governance on top of this — delegated administration, per-tenant entitlements,
  cross-tenant reporting — is Enterprise's (its E11). The mechanism is here
  because the queries are here, and a seam that hands another module the job of
  filtering correctly is a seam that will eventually be used incorrectly.

- **M19 — A deployment has an identity, and it can be supervised (new).** A
  Control deployment currently has no name for itself. That is fine while it is
  the only one an operator runs, and it is the blocker for everything above it:
  a managed-service provider running forty deployments, a customer with one per
  region, or an operator with a staging and a production instance all need to
  ask "which deployment am I looking at, what version does it speak, and is it
  healthy" before they can ask anything else.

  Three things land here, and the third is the one with a real design in it:

  1. **Instance identity, and health at every level it has.** A stable id, a
     display name, the software version, the contract version it vendors, and
     its tenant set — reported north-bound and rendered in the console.

     Identity and observability are **different questions and must not be
     collapsed into one answer.** The identity is the logical deployment's, and
     a node never appears in it: a supervisor that treats nodes as identities
     counts three customers where there is one. But health is reported at every
     level the deployment actually has — the deployment, each **node** in it, and
     the **proxy fleet** it serves — because "is this deployment healthy" is a
     question nobody can answer at the top level alone. A three-node cluster with
     one node down is *degraded*, and a summary that says only `healthy` or only
     `unreachable` has thrown away the fact that matters.

     So the north-bound surface reports, beneath one identity: node membership,
     each node's version (a rolling upgrade is legitimately mixed-version and
     must read as **in progress** rather than as a fault), which node holds each
     leader-elected job and the supervisory registration, event-bus and
     replication health, and the fleet summary M6 and 0006 already compute.
     Anyone operating this deployment — its own operator, or a supervisor they
     have consented to — is answering an incident with it.
  2. **The north-bound API is a compatibility promise.** Until now it has been
     an internal surface: operators, CI and the console, all shipping in lockstep
     with the server. A supervisor consuming many deployments meets **version
     skew** — one customer on 1.4, another on 1.7 — so the north-bound surface
     gets an explicit version, negotiated on the same principle as
     `policy_version` does south-bound (§4): the client names what it can read,
     and the server never answers outside it. **This retires an assumption
     phase 0018 currently rests on** — "this product ships its proxy and its
     server together and has no installed base" stops being true the moment
     anyone operates a fleet of deployments, and 0018 must say so.
  3. **Outbound supervisory registration.** A supervised deployment sits behind
     NAT and the supervisor cannot dial in. Rather than invent a mechanism, use
     the one the fleet already proved one level down: proxy D11 has a downstream
     proxy **register outbound** and become reachable as a `relay`, and M9 has
     it hold one long-lived stream with replay and `resync`. A deployment
     registering outbound to a supervisor is the same shape, one level up, and
     it should be recognisably the same shape in the code.

  Four rules make it safe, and they are the framework's rather than each
  supervisor's, on M16's reasoning:

  - **Registration is opt-in, configured locally, and revocable locally.** A
    deployment is supervised because its operator configured it to be, and they
    can stop it without the supervisor's cooperation. Anything else is a
    back door with a business model.
  - **The supervisor's credential is scoped and its use is visible.** It is a
    north-bound token like any other (M2) — subject to M18's tenant scoping and
    the scope grammar — and every action taken through it is in *this*
    deployment's audit trail, attributed to the supervisor. The operator being
    supervised must be able to read what was done to them.
  - **A supervisor is never on the decision path.** It may not be an M16 probe
     source, it may not hold a lock the authorize path takes, and a supervisor
     that is unreachable, slow, or hostile must change nothing about whether a
     user reaches a machine. M11's distinction applies with no exceptions: a
     supervisory failure is an outage in a management view, never a deny.
  - **Registration is a cluster singleton.** Exactly one node per logical
     deployment holds it, or one deployment appears as N. It acquires the
     singleton through `ext.ClusterCoordinator` (0004) — whose default
     implementation already answers "I am the leader, there is one node" — so a
     single-node deployment needs no clustering and a clustered one gets a real
     election from Enterprise (its E9) with no second code path.

  Per M15 this ships a **real default**: the registration client and the
  identity endpoints are here, and they are how any operator points a deployment
  at their own dashboard. The supervisory plane that consumes many of them is
  Enterprise's (its E14). The line is the same one M16 draws — the seam and its
  honest default here, the packaged product on top of it there.

- **M20 — The console is a product surface, and its design is specified rather
  than improvised (new).** Phase 0016 builds an operator console, and every
  acceptance criterion it carries is functional: does the screen work, does RBAC
  hold, does `make build` run without Node. Nothing in this plan said what the
  result should *look* like, and a phase whose criteria can all be met by
  unstyled HTML will, under `docs/PROTOCOL.md` §3's scope discipline, be met by
  unstyled HTML. That is the failure this decision closes, and it closes it the
  only way this repository closes anything: by writing it down where a session
  and a reviewer both have to read it.

  **`ui/DESIGN.md` is that document** — the token set, the type scale, the
  component inventory, the state matrix, and the enforcement — and it is durable
  truth in the sense of §9: a session that finds it wrong changes it in the same
  PR rather than diverging from it quietly.

  Three things make this a decision rather than a preference:

  1. **The console is where this product is judged.** A self-hoster evaluates
     Hoplock by opening it. Everything above — the explanation record (M4), the
     fleet graph (M6), simulation — is invisible until a screen renders it, and
     an operator who does not trust the surface does not trust the decision
     behind it. The "why was I denied" screen is the demo that sells the
     product (§7); it is also the screen someone reads at 02:00.
  2. **Some of the design is architecture wearing a different hat.** M11 says a
     deny is a decision and everything else is an outage — so the console must
     not paint them the same colour, or it re-creates in the UI the exact
     confusion M11 exists to prevent. M19 says a rolling upgrade is *in progress*
     and not a fault, so it must not render red. M18 says tenancy is invisible to
     a deployment that does not use it, so the chrome shows it only in scope.
     M17's "stale, undated or absent are one case" has a matching status that is
     neither healthy nor failed. These are not taste; they are decisions that
     have a visual consequence, and a console that gets them wrong is wrong.
  3. **"Looks good" is not reviewable; a token file and a screenshot set are.**
     So the design ships with the same kind of guard the rest of the repository
     uses — a lint that forbids a colour outside the token file, a contrast test
     over the tokens themselves in both themes, automated accessibility
     assertions on every screen, and a committed screenshot baseline that a
     deliberate change updates in the same PR. `exhaustive` is in the linter set
     because M13 admits Go cannot check M3's promises; the same reasoning
     applies here, and for the same reason it is a requirement rather than a
     suggestion.

  The constraint this must not break is the one M2 already sets: the console is
  a north-bound API client with no privilege of its own, and no amount of design
  earns it a private path to the database. Bundle budget, no runtime network
  fetches (an air-gapped deployment must render identically, and a security
  product must not report operator activity to a CDN), and the single-binary
  embed are part of the design, not exceptions to it.

- **M21 — The console is localisable from its first screen, and English is the
  only locale that ships (new).** What ships multilingual is the *machinery*: a
  string catalogue, ICU messages, `Intl` formatting, logical CSS properties, and
  layouts that absorb a longer translation. Exactly one catalogue is written —
  `en` — and no translation is commissioned here.

  The argument for doing it now rather than later is the one **M12** and **M18**
  already made for tenancy, and it is the same shape: a dimension retrofitted
  into a built system is a migration nobody wants to run. For a console it means
  revisiting every string, every control sized to its English label, every
  hand-formatted date and every `margin-left` at once. It is nearly free before
  the first screen exists and it is a rewrite afterwards.

  **The rule that makes it work: localisation is a property of rendering, never
  of storage or of the wire.** Four consequences, and each lands in a phase
  before the console:

  1. **Records store codes and data, never sentences (M8).** An audit record is
     append-only and hash-chained; a translated string inside one means the chain
     covers the translation, and a chain that verifies for an English reader and
     not a German one is not a chain. It could not be otherwise in any case:
     south-bound records arrive over a contract this repository does not own
     (M1), so rendering is the only layer where localisation *can* live.
  2. **The north-bound API answers with a stable code and typed parameters**, and
     the console owns the sentence (0014). M11's correlation id is unchanged and
     M19 makes this part of the compatibility promise — prose can be reworded
     between versions, a code cannot, which is what a client across a version skew
     needs.
  3. **Compiler rejections are structured the same way (0005).** "Every rejection
     names the rule, the line, and what to do instead" stays true and gains a
     code and parameters beside the English text, because that text is a product
     surface an author reads — in the console, in `policyctl`, and in CI.
  4. **Operator-authored content is never translated.** Rule names, zone names,
     target labels, grant reasons and policy source are data, not UI strings.
     Translating a rule name would have "explain why" (M4) cite a rule that does
     not exist in the bundle, which breaks the one promise that view makes.

  **Server logs stay English**, deliberately. They are read by the operator and
  by whoever supports them, and a log the vendor cannot read is worse than one in
  a second language.

  **"Ready for a second locale" is proved, not asserted.** The claim is about
  locales nobody has written, so the only honest test is a **pseudolocale** — a
  build that accents every catalogue string and pads it by 40%. An unaccented
  string is a hardcoded one and a clipped layout is one that will not survive
  German, and both fail in CI today, with one catalogue committed. A mirrored
  pseudolocale does the same for RTL. This is the difference between a console
  that is localisable and one that says it is.

  One cost is stated rather than discovered: **M20 forbids runtime network
  fetches**, so a locale in a script the bundled faces do not cover — CJK,
  Arabic, Devanagari — requires bundling another face. That is a real size
  decision for whoever adds it, not a `<link>`, and it is the honest price of an
  air-gapped console.
- **M22 — A south-bound credential carries its tenant and names its proxy
  (new).** M2 settles that the proxy→server channel has a credential of its own
  and that it is a bearer token in the prototype. It does not settle what is in
  one, and "an opaque string" turned out to be the wrong answer twice over. So:

  > A south-bound token is `<tenant>.<secret>`, minted per proxy at enrollment,
  > stored only as the SHA-256 of its secret half, and bound to the proxy it was
  > issued to.

  **The tenant is in the credential because of M18.** Tenancy is a request
  dimension the caller selects, never a process constant — and south-bound the
  selector has to come from somewhere. The three candidates are a field on the
  wire, a header, or the credential. The first is refused by M1: the contract is
  owned upstream and grows no tenant field, and a proxy asserting its own
  tenancy would be a caller asserting its own authority. The second is the same
  thing wearing a hat. The third works because **this server minted it**: the
  tenant is parsed from the credential and the secret is then verified against
  the rows under that tenant, so a forged prefix simply fails the comparison in
  a tenant where no such token exists. Nothing looks a token up across tenants,
  and no repository method could express it (M18).

  That this needs no change to `hoplock/proxy` at all is the strongest evidence
  the seam is in the right place, and it is why multi-tenancy costs the wire
  contract nothing.

  **The proxy id is in the credential because a lease is attributable.** A
  granted uid block records the `lease_id` an incident resolves a uid back to
  (§4), and that answer is worthless if the credential presenting the request
  could name anybody. So a bound token is refused for another proxy's traffic.

  **A token with no proxy id is a real state and not a half-filled row**: it
  authenticates "a proxy of this tenant" and nothing narrower. It exists for
  bootstrapping and for the conformance harness, which drives two proxy ids
  through one listener because uid exclusivity is per *target* and a harness
  that could only present one proxy would not grade that. It is spelled out at
  the call site; a deployment's steady state is bound tokens, minted by
  enrollment.

  **The shape is the enrollment token's on purpose** (`fleet.EnrollmentToken`),
  because it answers the same question — which tenant is this credential good
  for — and two spellings of one answer is how they drift.

  Enrollment mints the token **inside the transaction that admits the proxy**. A
  fleet member admitted with no way to call the API is a half-enrollment an
  operator repairs by hand, and there is no endpoint it could have asked for one
  on.

  **What this does not settle** is mTLS, which M2 already names as the intended
  production form. The seam is one interface and one middleware, and a
  certificate carries a subject that can say both of these things — so mTLS
  replaces the transport of this decision without replacing the decision.

---

## 3. Architecture & repository layout

```
control/
├── cmd/
│   ├── hoplock-control/    # the server daemon (both listeners)
│   ├── pdpconform/         # black-box contract conformance suite (M1)
│   └── policyctl/          # CLI: validate, simulate, explain, apply a bundle
├── internal/
│   ├── config/             # YAML config loader
│   ├── contract/           # hand-written Go types + handler interfaces for the vendored contract
│   ├── store/              # Postgres repositories, and migrations/ — the embedded SQL
│   ├── policy/
│   │   ├── model/          # the policy bundle: parse, validate, version
│   │   ├── compile/        # bundle -> decision program
│   │   └── eval/           # the evaluator + explanation records (M3, M4)
│   ├── fleet/              # proxy enrollment, heartbeat, zone graph, pathfinding (M6)
│   ├── identity/           # IdP brokers (OIDC/SAML), claim mapping, MFA orchestration
│   ├── credential/         # target-credential brokerage + SSH CA (proxy D6a)
│   ├── decision/           # authorize: assemble inputs, evaluate, snapshot, record
│   ├── revoke/             # event bus, subscriptions, replay buffer (M9)
│   ├── audit/              # ingest, hash chain, query, retention (M8)
│   ├── export/             # SIEM sinks (Splunk/Sentinel/Elastic)
│   ├── access/             # JIT requests, approvals, grants, notifiers (M10)
│   ├── extdefault/         # Control's own implementations behind the ext/ seam (M15)
│   ├── instance/           # deployment identity, supervisory registration (M19)
│   └── httpapi/
│       ├── south/          # proxy-facing handlers (the contract)
│       └── north/          # admin/operator/CI handlers
├── ext/                    # PUBLIC extension points — the seam Enterprise implements (M15)
├── ui/                     # management console, embedded into the binary
│   └── DESIGN.md           # the console's design system — binding, not advisory (M20)
├── contract/               # VENDORED from the Hoplock Proxy repository — read-only (M1)
├── deploy/                 # docker-compose: this server + Postgres + a proxy
├── docs/                   # this plan, protocol, cross-repo protocol, learnings
└── prompts/                # queued and implemented phase prompts
```

The forward-only SQL lives in `internal/store/migrations/` rather than in a
top-level `migrations/`, and the reason is mechanical: `go:embed` cannot reach
outside its own package directory, so a top-level directory would need a
top-level *package* to embed it — and `ext/` is the only non-internal package
this module has (M15). Reading the files off disk at runtime was the
alternative, and it gives up the one-binary deployment for nothing.

### Component responsibilities

- **`internal/contract`** — the only package that knows the wire shapes of the
  south-bound API. Everything else speaks domain types, so a contract revision
  in the Hoplock Proxy repository lands in one package here. The types are
  **hand-written and tested against the vendored document** rather than
  generated from it: the contract's two open namespaces (`TargetAuth.params`
  and the `device_field.` names inside it) and its one `oneOf`
  (`grant_context.additional_context`, a string or an object and nothing else)
  are shapes a generator renders as `map[string]any`, which would put the
  absent-value discipline back in every caller's hands. What a generator would
  have caught — an enum drifting, a field renamed, a path removed — is caught
  instead by a test that reads `contract/control.yaml` and compares it with the
  constants, in both directions.
- **`internal/policy`** — pure. Parse → validate → compile → evaluate, no HTTP,
  no database, no clock of its own (time is an input). This is the package that
  must be exhaustively tested, because it is where the product's promises are
  kept — and, per M13, the package the language helps least, so its variants are
  closed `Kind` enums that the `exhaustive` linter can check rather than open
  interfaces that it cannot.
- **`internal/decision`** — the composition root for an authorize call: gather
  identity, target labels, grants, fleet path, and connection metadata; evaluate;
  build the snapshot; write the decision record; decide whether to issue a cache
  hint. Latency budget (M5) is enforced here, as a hard deadline on the call and
  by holding the compiled program and the fleet graph **in memory** — a bundle
  is compiled once per version and a graph is three whole-tenant reads, and
  neither belongs on a path a user's handshake is held open for. The decision
  record is written **synchronously**, before the answer is returned: a decision
  this server cannot explain afterwards is one it does not serve, on the allow
  path and the deny path alike (M4).
- **`internal/fleet`** — the graph, its liveness, and pathfinding. Owns which
  hop direction is possible right now.

  It also owns the two things that hang off the same rows, because both are
  properties of a proxy rather than of a policy: the **capability store** both
  sources write to (M17 — the proxy build's declared set, and the per-target
  reports `/v1/capabilities/report` accumulates), and **configuration
  distribution** — a versioned document per zone and per proxy, composed into one
  effective document per proxy, with rollback and with drift between desired and
  running visible rather than derived. The running side is what the proxy says
  on `POST /v1/proxies/{proxy_id}/config/report`, and the only keys a document
  may carry are the ones proxy D18 makes fleet-owned (§4, "Configuration
  distribution").

  And it owns **everything a proxy reports about a target**, which is the same
  rule stated from the other side: the capability store above, the **host-key
  record** (`/v1/hostkeys/report`, proxy D7) and the **uid allocation cursor and
  its leases** (`/v1/uids/lease`, §4). All three are keyed by target, all three
  arrive on the south-bound listener, and all three are observations rather than
  decisions. Splitting them would put half of one answer in a second package;
  keeping them here is what makes "several names for one host are several
  records" a single known imprecision rather than three.

  It also answers the two proxy-credential questions, for the same
  one-source-of-truth reason: whether a presented **channel token** is one this
  server minted (M22), and whether an offered **key belongs to one of the
  fleet's own proxies** — the chain leg on `/v1/auth/cert` (proxy D11). The second reads the
  enrolled rows themselves through a column generated from `public_key`; a list
  of proxy key fingerprints maintained beside them would drift the first time a
  proxy re-enrolled with a new key, silently, in the direction that
  authenticates.

  The package is split so that the part worth proving is provable without a
  database: `Graph` and its `Path` are pure values over pure inputs, and
  `Registry` is what loads those inputs out of `internal/store` and applies the
  staleness rule. A path is a function of the nodes, the edges, the live relay
  registrations and the clock, and nothing else.
- **`internal/identity`** — who is asking: the south-bound authentication
  conversation, the IdP brokers, the claim mapping, RBAC's vocabulary, and the
  north-bound credential model. It resolves an offered key or a password to an
  identity and owns what the contract makes this server's alone: challenge
  lifetime, poll-rate enforcement, single use, and expiry as a deny (§6). The
  factor itself sits behind an `MFAProvider` seam and the identities behind a
  `Directory` one, and what must NOT move behind either seam is anything about
  the conversation, because that is the same whoever supplies the factor.

  It answers `(Outcome, error)` rather than `(Identity, error)`, and the split
  is M11 made structural: a non-nil error is an OUTAGE with nothing to inspect,
  and a deny is a field on the outcome that only code building one on purpose
  can set. The two cannot be mistaken for each other by a caller in a hurry.
  `BrokerError` is the same split one layer along, for a login: a refusal
  carries a code and a message safe to render, and everything else is an outage.

  `Mapping` is the one place an IdP's vocabulary becomes this server's, and it
  is an allow-list (§6). `Role`, `Permission` and `RoleSet` are the whole of
  RBAC's vocabulary, as constants rather than rows. `Principal` is what the
  north-bound middleware puts on a request: its scope map is **unexported**, so
  the only questions a caller can ask are "may this principal act in tenant T"
  and "what roles does it hold there" — which is what stops a cross-tenant
  aggregate route from being easy to write (M18).
- **`internal/credential`** — the per-tenant SSH certificate authority (proxy
  D6a, §6). It mints over a public key the proxy generated, so no private key
  crosses this API; it holds its own through the `ext.KeyStore` seam, so custody
  can move to an HSM with no second code path; and it says plainly what SSH can
  and cannot enforce rather than implying a hostname scope that does not exist.
  `seam.go` is the one file that names what the contract is missing, refuses to
  put it on the wire, and fails the build when it lands upstream (M1).
- **`internal/httpapi/south`** — the proxy-facing transport, and the only place
  that speaks both the wire vocabulary and the domain one. It owns the
  middleware chain (correlation ids, the access log that never writes a body, a
  body limit, a request deadline, panic recovery, the proxy credential) and the
  **error mapper**, which is the single place a status code is chosen. Two
  source-level tests keep that structural rather than conventional: one fails
  the build if a third function can construct a 401, the other if a handler
  names a status constant.
- **`internal/httpapi/north`** — the operator-facing transport, and the place
  tenancy is resolved (M18). Its **route table is the enforcement point**: every
  route is registered with an access class and, where it resolves a tenant, one
  permission, and one middleware authenticates, resolves exactly one tenant from
  the principal's scope, and checks that permission before a handler runs. A
  handler reads the tenant from the request context or not at all, and the
  isolation test enumerates the router rather than a hand-written list — so a
  route added in a later phase without isolation fails it on the day it is added.
  Its errors carry a stable code, typed parameters, an English message and the
  correlation id (M21), which is also why it owns its own 404 and 405 rather than
  letting `http.ServeMux` answer them in prose.
- **`internal/audit`** — append-only writer, chain verifier, and query API.
  Nothing else writes audit rows. One record is stored twice over: `body` holds
  the canonical JSON the chain hashed, as text rather than jsonb so a verifier
  re-hashes the stored bytes with nothing in between, and every other column on
  the row is a derived index recomputable from it. Session capture lives in a
  table of its own, covered by the chain through a digest inside the hashed
  body rather than by being in it.
- **`internal/revoke`** — subscriptions and fan-out. Owns event ids and replay.
- **`internal/extdefault`** — Control's own side of the extension seam: what
  this repository registers into an `ext.Registry` before the server starts, so
  a deployment with no Hoplock Enterprise present is a complete product rather
  than a set of holes (M15). It is a separate package from `ext` because `ext`
  is what Enterprise imports and stays interface-only; a default belongs on this
  side of that line. Most of Control's answers are *not* here and that is the
  design: where Control's behaviour when nothing is registered is its own core
  code path — the local audit store, manual grants, its own software keys, the
  compiler's checks — the seam is additive and there is no default to register.
  What lands here is the narrower set where everything above the seam goes
  through it, which today is the single-node cluster coordinator.
- **`internal/instance`** — this deployment's own identity and version, and the
  outbound registration client that makes it supervisable (M19). It is a
  *client* of something above it, which makes it the only package here that
  dials outward on the management plane; it may never be reachable from the
  decision path.
- **`ui`** — the operator console, a north-bound API client with no privilege of
  its own (M2) and no database access at all, built from embedded assets so a
  deployment stays one binary. Its visual design is governed by `ui/DESIGN.md`
  and is enforced rather than reviewed by eye (M20); where the design encodes a
  decision from this plan — a deny and an outage are different things (M11), a
  rolling upgrade is in progress and not a fault (M19) — the design document
  says which decision and why. It is also the **only** layer that localises
  (M21): it holds the string catalogues, and everything beneath it stores and
  transmits codes.

---

## 4. The south-bound API (the contract)

The contract is upstream (M1). This server implements every endpoint the proxy
calls, and the conformance suite is the definition of "implements":

| Path | What this server must do |
| --- | --- |
| `POST /v1/auth/cert` | Resolve an offered key/certificate to an identity with claims, or deny — where the key may be a **user's** or one of the **fleet's own proxies'** (a chain leg, below) |
| `POST /v1/auth/password` | Verify, then own the MFA conversation: return `authenticated` or `mfa_required` + a challenge |
| `POST /v1/auth/mfa/poll` | Resolve an outstanding challenge; deny on expiry or unknown token |
| `POST /v1/authorize` | Evaluate policy **for the asking hop** (`conn.proxy_id` + `conn.hop_trail`); return `401` or the whole-connection snapshot + `decision_id` (+ optional cache hint) |
| `POST /v1/hostkeys/report` | Record a reported target host key and answer with the trust decision, plus an optional cache hint (§5.4) that lets the proxy stop re-reporting that exact key |
| `POST /v1/capabilities/report` | Record the enforcement rungs one **target** can take, as the proxy found them by probing it; answer `accepted` and, optionally, when to report next |
| `POST /v1/uids/lease` | Grant a proxy an **exclusive block of ephemeral uids for one target** out of a per-target allocation cursor that **only ever advances**; `409` when the cursor has reached the top of the range |
| `POST /v1/logs/batch` | Idempotent bulk ingest into the audit store; `202` |
| `POST /v1/logs/priority` | Single critical record, durable before the ack; `200` |
| `GET /v1/proxies/{proxy_id}/events` | Long-lived NDJSON revocation stream with heartbeats, replay, and `resync` — and `config_changed`, which names a proxy's desired configuration document without carrying it (proxy D18) |
| `GET /v1/proxies/{proxy_id}/config` | Serve the proxy's **current** desired configuration document: `200` with the document and its `hash` as the `ETag`, `304` when `If-None-Match` names the hash still desired, `204` when nothing is published for it, `404` `not_enrolled` for an id the registry does not hold (proxy D18) |
| `POST /v1/proxies/{proxy_id}/config/report` | Record which document the proxy is **running** and what became of the desired one (`state`, `restart_required`, `last_error`); answer `accepted`. This is the running side of drift (proxy D18) |

Six obligations are easy to miss and are graded by the conformance suite:

- **The priority ack means durable.** The proxy acts on a critical security
  event knowing this server recorded it. Acking before the write lands turns
  that guarantee into a lie that only shows up after an incident.

  **Nothing on the contract reads a record back, and that is deliberate**
  (upstream `Hoplock/proxy#56`, merged): a proxy writes logs and never queries
  them, so an operator read API on `/v1` would be one every Hoplock Control
  implements and no proxy calls. The same answer covers publishing an event,
  which gap recovery needs in order to be gradeable at all. Both guarantees are
  therefore observable only through paths **this server** exposes outside `/v1`,
  and the conformance suite takes them as inputs — `logs.read_url` (phase 0010)
  and `events.publish_url`, which 0009 serves from a listener of its own that is
  bound only when `events.publish_listener` is configured and credentialled.
  Neither is a licence to add the endpoint to `/v1`.
- **Answer within the vocabulary the proxy declared.** Every policy field is
  additive within a vocabulary and carries a documented absent-value default, and
  in exchange the proxy **fails a session closed on an authorize field it does not
  understand** rather than dropping it — an unknown field may be a restriction,
  and a dropped restriction is a silently widened session. The proxy therefore
  sends `policy_version` on the authorize request, naming the highest vocabulary
  it implements, and this server MUST NOT answer with policy fields introduced
  after it.

  This is the obligation that fails the worst: get it wrong and every session
  through an older proxy in the fleet breaks at once, as an outage rather than a
  deny, at exactly the moment a fleet is mid-upgrade. A server holding policy it
  cannot express within the declared version says so (`5xx`, M11) instead of
  sending fields that will be refused — the proxy's mock does exactly this and
  is the reference behaviour.

  **The current vocabulary is `4`**, exported upstream as
  `control.PolicyVersion`: the two enforcement axes and the session bounds
  (§5.2). The vendored document is `4.1.0` (`Hoplock/proxy#56`, vendored by
  phase 0009). Upstream is already at `4.3.0`, and **0014** re-vendors it:
  `Hoplock/proxy#65` (merged) made it `4.2.0` for fleet configuration (proxy
  D18), and `Hoplock/proxy#66` (merged) made it `4.3.0` for the `default`
  algorithm-profile tightening. The vocabulary stands still at `4` through all
  three. That the document moved while the vocabulary did not is the normal
  case rather than an anomaly. The number governs `/v1/authorize` and nothing
  else: `#56` added a field to the event stream, `#65` added an event type and
  two endpoints, and `#66` changed what an existing value means at the
  target's handshake. Read both numbers out of `contract/control.yaml`, never
  from this line (0018).

  **`policy_version` is REQUIRED on the request, with no absent-value default.**
  A request that omits it is refused — `400 invalid_request`, not a guessed
  version and not a `401` (M11) — because a proxy that cannot say what it is
  able to read is one this server would have to guess for, and the guess decides
  which restrictions get silently dropped. That is a different answer from the
  mismatch `5xx` above and must not be folded into it: a declared wrong version
  is a rollout problem, an absent one is a malformed caller. 0008 builds both,
  0002 grades both.

  **The document version and the negotiated vocabulary are two numbers, and
  neither is derived from the other.** `policy_version` governs `/v1/authorize`
  **and nothing else** — that is the response the proxy decodes strictly, and so
  the only place an unknown field could be a dropped restriction. Three kinds of
  contract change therefore move `info.version` without moving the vocabulary,
  and this server must absorb all three:

  - **A field on another endpoint.** `HostKeyReportResponse.cache` (§5.4) is the
    contract's own worked example: a proxy that has never heard of it ignores it
    and keeps reporting every connection, which is correct behaviour rather than
    a thinned answer.
  - **A whole new endpoint.** `POST /v1/uids/lease` is outside the number
    entirely, because the number gates a vocabulary rather than a surface.
  - **A tightening.** `TargetAuth.params.username` is required on every
    credential method the contract defines (§5.2), and a route omitting it is
    refused at the first authorize call. A tightening adds no field and changes
    no field's meaning, so it is **not expressible through the version at all**:
    a proxy that was never told parses the route exactly as it always did. The
    contract announces it as a break instead. There is therefore no version at
    which omitting it is correct and nothing here may offer one — 0005 rejects
    it at authoring time, 0008 before the response is written, 0002 grades it.
    The contract carries a second break since `Hoplock/proxy#66`:
    `algorithm_profile: default` now means the SSH library's secure set
    (§5.2). No parser sees a difference, so it bites at the target's handshake
    rather than at the authorize call, and nothing on this side can refuse it
    early. What this side owes is guidance to the policy author (§5.2).

  So the drift check keys off the checksum in `contract/UPSTREAM` and never off
  `policy_version` (0002, 0018). Nor may it assume the document version only
  rises: the collapse noted below moved it **down**, `4.3.0` → `4.0.0`, and
  `Hoplock/proxy#56` then moved it up to `4.1.0` for a field on the event
  stream, `Hoplock/proxy#65` to `4.2.0` for a new event type and two new
  endpoints, and `Hoplock/proxy#66` to `4.3.0` for a tightening — the
  vocabulary stood still at `4` through all four, which is the whole point.
  `#66` also shows that a version string does not name a document: `4.3.0` is
  the number `#53` moved the document down *from*, so two different contracts
  have now carried it. The checksum is what identifies the vendored copy.
  `#65` is also the contract's own proof that a new event **type** needs no
  bump: "a proxy ignores a type it does not recognise" was already in
  `RevocationEvent.type`, and it is what licenses `config_changed`.

  **One live vocabulary, and removing versions is not removing versioning.**
  Upstream `Hoplock/proxy#53` (merged) collapsed the contract: it deleted the
  superseded vocabularies and the entire revision history from `api/control.yaml`
  and `api/README.md`, so both documents now read in the present tense with no
  "since version N" annotation on any field and no revision sections to cite.
  Proxy and Control ship together and no older peer has ever been deployed, so a
  shape kept alive for one was debt bought for nothing.

  **What that did not touch is everything above.** `policy_version` is still on
  the wire, still required, still honoured; the MUST-NOT-answer-above rule still
  stands; the `5xx` for a proxy this server cannot serve still stands. The
  mechanism is what carries the **next** vocabulary, and a revision now *replaces*
  the current one rather than running beside it. A session that reads "one
  vocabulary" as "there is nothing to negotiate" would delete the only thing
  standing between a fleet mid-upgrade and a silently widened session; 0018
  narrows what this server *supports* to one value and explicitly may not remove
  the field.

  **The `device_field.<name>` namespace is not a version axis** (§5.2). It is
  deliberately open, so a name inside it is not a new policy field and demands no
  bump: version-aware assembly cannot gate a device field, because there is no
  version to gate it on, and it must not try.

  What makes that openness safe is the layer below: the proxy skips a rung whose
  fields its driver does not declare, so a field an enforcing proxy cannot honour
  costs the rung rather than widening the session. Which proxy can honour which
  field is a **capability** question (M17), answered from the fleet registry, not
  from `policy_version` — and on a one-rung ladder the cost of getting it wrong is
  a denial, so the check belongs on the issue path. The enforcement vocabulary
  widens that question rather than changing it: an enforcement rung depends on the
  **target** far more than on the proxy, which is what the capability report below
  exists to answer.
- **A capability report is an observation, and it constrains rather than
  grants.** The proxy probes a target it has just logged into and reports, on
  `POST /v1/capabilities/report`, which enforcement rungs that
  target can actually take. This server accumulates those reports (0006, M17) and
  uses them — together with the proxy build's own `AuthorizeRequest.capabilities`
  — to constrain what a policy author may choose per route.

  It is the authorize response, never the report, that authorises a rung. That is
  what makes a **stale, undated, or entirely absent** record safe, and those three
  are deliberately **one case**: they provide nothing that has to be *applied*,
  they leave untouched every rung that needs nothing of the target (the two
  proxy-side defaults and an attested rung), and the proxy re-checks the rung
  against the live target when it provisions. So the worst a stale record can
  cause is a refused session — never a session running below the rung its own
  audit record claims. A record with no `observed_at` is treated as stale, because
  a capability with no date has no shelf life.

- **The uid allocation cursor only ever advances, and nothing is ever
  reclaimed.** The non-reuse floor under an `ephemeral-user` account's uid lives
  **here**, not on the
  target: `POST /v1/uids/lease` grants a proxy an exclusive block
  `[uid_from, uid_to)` for one target, out of a per-target cursor this server
  advances **under a lock, on grant**. The whole storage requirement is one
  integer per target.

  The invariant is the entire endpoint:

  > A uid inside a granted block is never inside any other grant — for this
  > proxy or any other, ever again — whether the block was **used, abandoned,
  > or allowed to expire**.

  It follows that there is **no release call and nothing to reclaim**: a server
  that "recycled" an unused block to save uids would silently break the one
  guarantee the endpoint exists for, that a fresh ephemeral account never
  inherits ownership of the files a torn-down one left behind. `term_seconds`
  bounds how long the proxy keeps *allocating* from a block; it is not what
  makes the uids non-reusable, so an expired block is one this proxy stops
  using, never one this server hands to somebody else.

  Two consequences this server owns. **`observed_floor` may only ever raise the
  cursor, never lower it** — it is a target's word relayed by the proxy, so it
  is this server's to clamp or ignore, and the exposure is worth stating: root
  on a target can report a large floor and burn that target's range. That is
  the availability side of an invariant that already prefers refusing to
  reusing, and it reaches no other target. And **a cursor that has reached the
  top of its range answers `409`**, which the proxy treats exactly as an
  exhausted block: outage-class, nothing provisioned, and the remedy is the
  operator's.

  **This is not on the decision path and does not dent M5.** The call is made
  once per *block*, not once per session — there is no per-session write and no
  read-modify-write where latency is measured.

  **The floor is deliberately NOT a field on the authorize response**, and this
  is the reasoning to keep rather than the conclusion. `/v1/authorize` is
  **cacheable** (§5.4) and the proxy serves a cached decision while this server
  is unreachable — so a floor carried on it is replayed from whenever it was
  cached, and **a stale floor is a lowered floor**, which is precisely the uid
  reuse the mechanism exists to prevent. The same disqualifies anything else
  cacheable, including the `cache` hint on the host-key response: the property
  that rules it out is cacheability itself, not which endpoint it rode on. A
  lease is exempt for one reason only — it is **exclusive**, so replaying it
  grants the same block to the same proxy, and replay is harmless rather than
  merely unlikely.

  The operational consequence of not implementing the endpoint is stated rather
  than discovered: **a Control that does not serve it refuses every
  `ephemeral-user` route in practice**, because the proxy fails **closed**
  rather than falling back to a floor it cannot trust. It is the one place an
  otherwise purely additive revision is not optional for us.

- **Heartbeats are liveness, and their absence is a signal.** A proxy that
  stops hearing them reconnects and, past its staleness threshold, stops serving
  cached decisions entirely. A server that stalls its heartbeat writer degrades
  the whole fleet to uncached — correctly, but for the wrong reason.

  **This is two obligations, not one** (upstream `Hoplock/proxy#56`, merged).
  The stream now carries `RevocationEvent.heartbeat_interval_seconds` — the
  interval the server says it is keeping **now** — so "within the interval the
  server advertises" is a claim read off the wire rather than a number typed
  into a conformance harness. This server must keep the interval it advertises,
  **and** that interval must be within the ceiling of **10 seconds or less**, so
  that two consecutive intervals fit inside the proxy's 20s reconnect timeout
  and one lost heartbeat is not mistaken for a dead stream. Meeting either half
  alone is a failure: a server advertising 600s and honestly keeping to it
  passes its own claim and breaks every proxy in the fleet.

  Absent stays legal and means what every server did before the field existed —
  the reader falls back to its own timers — and the field **advertises rather
  than configures**: a reader may use it to notice a dead stream *sooner* than
  its own timeout and must never extend that timeout to accommodate a large
  advertised interval. Sooner always, later never, the same rule as
  `cache.ttl_seconds` and `report_after_seconds`; the inverse would let a broken
  or hostile server silence itself indefinitely by announcing that it intends
  to, which is §6.4's fail-closed rule turned upside down.

  **One number, both halves.** The interval this server keeps and the interval
  it advertises are configured as one value (`events.heartbeat_interval`,
  default 5s), because two numbers that can drift apart will and the drift is
  invisible until a fleet is already reconnecting; the advertisement is that
  value rounded **up** to the whole second, so this server never claims an
  interval it does not keep. A value whose advertisement would exceed the
  ceiling is refused when the configuration loads rather than clamped: the
  process does not start.
- **A chained hop is a caller, and this server is what makes chaining work.**
  Proxy phase 0008 (`Hoplock/proxy#6`, merged) turned multi-hop on, and it added
  no field to the contract: both halves are behaviour this server owes.

  On `/v1/auth/cert`, a key belonging to one of the fleet's own proxies is a
  **chain leg** (proxy D11, PLAN §6.1) — the hop in front offers its own key,
  never the user's, alongside the user's `login`. The answer is that **user's**
  identity, established here. The proxy asserts nothing about who is connecting;
  it relays a key and a login exactly as it does for a user's own client, so a
  compromised proxy can offer only its own key and this server decides what that
  key may reach. **Without this a chained hop cannot authenticate at all.**

  On `/v1/authorize`, `conn.hop_trail` — in the contract since phase 0002 —
  carries the proxy ids the session has already traversed, oldest first, empty on
  the user's first hop. Every hop asks for itself with the same identity and the
  same final target (proxy D2), so the trail plus `conn.proxy_id` is **the only
  thing that distinguishes the second leg of a chain from a fresh connection**,
  and therefore the only view of a chain this server has. It may narrow a
  decision — a loop or the hop cap is a refusal — and it may never widen one:
  every entry in it can only cause a refusal, which is exactly why it is safe to
  accept from a caller. The authority on a leg is the previous hop's key, above.

  Phases: 0007 and 0008 respectively; 0017 proves the pair against a real proxy.

### Configuration distribution: the delivery exists upstream (proxy D18)

An operator configures a fleet rather than N files (M6, phase 0006). Delivery
**reuses the event stream** rather than inventing a second channel, because
proxies already hold one outbound subscription and must not need a second
inbound path — the same reasoning that made the revocation stream outbound in
the first place (proxy §6.4).

**How it got here.** When 0006 was built, `RevocationEvent.type` could not say
"your desired configuration moved", and the contract is owned upstream and
vendored read-only (M1). So 0006 built everything below the wire, left
`fleet.ConfigPublisher` a visible no-op, and raised the need upstream
(`docs/CROSS-REPO-PROTOCOL.md` §3.2, `Hoplock/control#27`). The proxy answered
it as its phase 0042, merged as **`Hoplock/proxy#65`** (contract **`4.2.0`**,
`policy_version` still `4`), with a new decision, **proxy D18**. That upstream
change is what this section now describes. The vendored `contract/` is still
`4.1.0` until **0014** re-vendors it, and until then the publisher stays a
no-op and a publish stages and shows as drift.

**What is now true, and what it obliges here.** Cite proxy D18 for the
reasoning. Do not restate it. The wire has three parts, and this server owes
the server half of each:

- **`config_changed` names the document and never carries it.** The event has
  `version` and `hash`, and nothing else. The stream is replayable from a
  `last_event_id`, and a document carried on it would be replayed, which means
  a stale configuration applied as if it were current. `fleet.ConfigPublisher`
  therefore emits a notification per affected proxy, naming that proxy's
  composed document, on the stream 0009 built. On the wire, `version` is an
  **opaque string** and `hash` is an opaque content identifier the proxy only
  ever compares for equality (the contract's example is `sha256:<hex>`). This
  server's `int64` version and bare hex hash are rendered into those strings.
  The strings are not a second numbering.
- **`GET /v1/proxies/{proxy_id}/config` serves the document desired *now*.**
  It returns `200` with the `ProxyConfigDocument` and its `hash` as the `ETag`.
  It returns `304` with no body when `If-None-Match` names the hash still
  desired, which is what makes a replayed notification, and the fetch the proxy
  makes on every stream (re)connect and after `resync`, cost at most a `304`. It
  returns `204` when nothing is published for the proxy, and the proxy then
  runs on its bootstrap file. It returns `404` with the code `not_enrolled` for
  an id the registry does not hold. That is a registry fact and not a deny, so
  it is never `401` (M11).
- **`POST /v1/proxies/{proxy_id}/config/report` is the running side of
  drift.** It is the only proxy→server call that carries what a proxy runs. A
  document is reported running (`running_version`/`running_hash`) only when
  **every** setting in it is in force. `state` is `applied`,
  `pending_restart` (with `restart_required` naming the startup-only settings
  holding it back, and *nothing* in it applied), `rejected`, or `fetch_failed`,
  and the last two come with a `last_error` that names keys and never values.
  Drift and the fleet view's error are driven from this report. A desired
  version compared against a heartbeat number is not enough, because
  `pending_restart` and `rejected` are distinct states and an operator must see
  them.

**Only fleet-owned settings may be published.** D18 draws the line: a setting
stays bootstrap-only if the proxy needs it to reach Hoplock Control or to be
recognised by it, if it names material on the host, if it binds a listener, or
if it is the proxy's own judgement of whether it can still hear Control
(`control.cache.stale_after`). A setting is **fleet-owned only by being
listed**. The list is `ProxyConfigDocument.settings` in the vendored
`contract/control.yaml`, keyed by dotted bootstrap key. Read it there and do
not copy it here, because a second copy drifts. A document naming any other
key is **rejected whole** by the proxy, so a publish that names one is refused
**at publish time**, naming the key. Accepting it would stage a rollout that
could only ever be reported `rejected`. Composition's top-level key replacement
(0006) is exactly per-setting replacement over those flat dotted keys.

**A bad document never takes a proxy out of service**, and configuration is not
on the data path (D18). Nothing on this server's side of the delivery may end a
session, refuse a connection, or touch a cached decision. `config_changed`
shares the stream with revocations, but it is not a revocation.

**Publishing is not on the contract**, for the same reason reading a log back
is not (§4, above): an operator's publish is not proxy-facing. It is this
server's own north-bound action (0014). The conformance suite takes it as an
input, as it does `events.publish_url`, and the proxy's `cmd/mock-control`
`POST /debug/config` is the reference shape.

---

## 5. Policy model & evaluation (M3, M4)

### 5.1 Inputs

| Axis | Examples |
| --- | --- |
| Subject | subject id, IdP source, groups, claims, authentication method, MFA |
| Device | posture attributes when an endpoint supplies them (optional) |
| Context | time of day, day of week, source network/geo, the proxy asking (`conn.proxy_id` — the entry proxy on a user's first hop, an inner hop on a chained one) |
| Target | hostname, labels (`env=prod`, `kind=appliance`, `owner=payments`), zone |
| Grants | live JIT grants for this subject and scope (M10), including windows confirmed from external context (M16) |
| External context | a scan, ticket, or incident asserted by an integration and confirmed at decision time (M16) |

The **session axes** — channel type, in-channel request, forwarding destination,
global request, command — are outputs rather than inputs, and they are absent
from the table above on purpose. The proxy asks once and enforces for the
connection's lifetime (proxy D2): at the moment a decision is made no channel has
been opened and no command has been typed. What the engine emits is the
allow-list the proxy then enforces against each of them as it arrives (§5.2), so
a rule has nothing to match on and `internal/policy` offers no way to try.

### 5.2 Outputs

A **whole-connection snapshot**, because the proxy asks once and enforces for
the connection's lifetime (proxy D2):

- route: `direct` or `nexthop`, plus the next step and hop metadata including
  **connection direction** (proxy D11, computed from the fleet graph, M6);
- **channel types** permitted, in both directions;
- **in-channel requests** permitted, subsystems named individually;
- **forwarding destinations** permitted (host/CIDR + port);
- **global requests** permitted;
- **filter policy**: either an ordered rule list (guardrail) or a restricted-exec
  allow-list (boundary) — never both (proxy D12);
- **target credential method** and its parameters (proxy D6a) — an **ordered
  ladder** since proxy D14, so the PDP states its preference *and* what it will
  accept, with a one-entry ladder meaning "this method or nothing". Every entry
  names the account it will log in as: `username` is **required on every method
  the contract defines** — `ephemeral-user`, `ephemeral-account`, `static-key`
  and `brokered-key` alike, one of the contract's two tightenings (§4) — and
  it is never derived from the identity's `login`, which is a client-typed
  string. A route that omits it is refused by the proxy at the first authorize
  call rather than served, so the check belongs at authoring time (0005) and
  again before the response is written (0008);
- **device platform and expiry posture** on `ephemeral-account` routes (proxy
  D13), and a **per-route algorithm profile** (`algorithm_profile`), a named
  preset (`default`, `legacy-rsa-sha1`, `legacy-device`), absent ⇒ `default`,
  and anything else a deliberate weakening. The proxy applies it to **every**
  connection the route causes to the target: the session leg, the provisioning
  login, a device driver's CLI, and the teardown and orphan sweeps after them.
  **`default` is the SSH library's secure set** (`Hoplock/proxy#66`). It offers
  no SHA-1 key exchange, no `hmac-sha1-96`, and no `ssh-rsa` or `ssh-dss` host
  key. `legacy-rsa-sha1` adds `ssh-rsa`. `legacy-device` adds that plus the
  SHA-1 key exchanges, CBC ciphers, `hmac-sha1-96` and `ssh-dss` host keys. The
  contract's description is the authority for those lists, and nothing here
  copies them. So the guidance a policy author needs, and 0014's authoring
  surface gives, is this: a device that speaks **only** SHA-1 key exchange,
  `ssh-rsa` or `ssh-dss` does not connect under `default` and needs a legacy
  profile. The proxy reports each such target as
  `target.algorithm_policy_unmet`, naming the axis and what the target offered
  (§7). That record is how an operator finds which routes need one. The
  profile in force is on the record either way (§7);
- **additional device fields** on those same routes — the open
  `device_field.<name>` namespace that sits beside the five
  `ephemeral-account` parameters (proxy phase 0016). Some devices are not one
  target: a FortiGate running virtual domains is one unit partitioned into many,
  and `device_field.vdom` names the virtual domain a VDOM-scoped administrator is
  created in — absent, the administrator is **global**. The endpoint cannot carry
  this, because `host`/`port` is what DNS resolves, what the host key is pinned
  to, and what the audit record names.

  The contract does not enumerate the fields and never will (proxy D13 makes
  customer-written drivers first-class), so the set is as open as the set of
  platforms and this snapshot carries it as **data, opaque to the contract**.
  What is checked is the **shape**: `<name>` is lowercase letters, digits,
  hyphens and underscores, at most 64 characters; the value is non-empty and at
  most 256 characters; at most 16 fields ride on one ladder entry. They are
  policy metadata, never credential material, and they are audit facts (§7);
- **enforcement rung** per axis (`enforcement`), where the route
  stands somewhere other than proxy-side enforcement. There are **two axes**,
  because what a session may *execute* and what it may *reach* are separate
  questions with separate mechanisms, and a route may stand on a different rung
  of each:

  | `enforcement.execution` | `enforcement.reach` |
  | --- | --- |
  | `proxy-inspected` *(absent-value default)* | `proxy-channel-policy` *(absent-value default)* |
  | `no-interactive-shell` | `account-egress-restricted` |
  | `account-restricted` | `account-network-isolated` |
  | `account-confined` | `platform-attested` |
  | `platform-authorized` | |
  | `platform-attested` | |

  Four rules bind what this server may answer, and each is ours to enforce
  before the response is written rather than the proxy's to discover:

  - **Absent means proxy-side enforcement only** on both axes — exactly what a v3
    server produced. Emit the field only where the route genuinely stands
    somewhere else; an emitted default is noise in an audit record.
  - **Applied and attested are different kinds.** An *applied* rung is one the
    proxy configures per session and tears down, and it needs the proxy to
    administer the account — which only `ephemeral-user` and `ephemeral-account`
    do. An *attested* rung (`platform-attested`, either axis) is one the target
    enforces already, configured by somebody who is not this product; the proxy
    applies nothing and records who says so. So **an applied rung must never be
    chosen for a route whose every ladder entry is `brokered-key` or
    `static-key`** — the proxy refuses that response outright, and a policy that
    can only fail at connect time fails in front of a user. An **attested** rung
    on such a route is fine, and it is the enforcement claim the appliance estate
    actually carries: "none available" is the answer this vocabulary exists to
    stop giving.
  - **The rung is a property of the route, not of a ladder entry.** A ladder entry
    that cannot carry it is a *skipped rung* on the proxy (proxy D14) and the
    proxy walks on; it never runs the session without the rung its record would
    claim. One policy stating two different guarantees would leave the audit
    record unable to say which was in force.
  - **The claim must agree with the rest of the snapshot.** `no-interactive-shell`
    requires `permitted_requests` to be present and to deny both `shell` and
    `pty-req`; `account-restricted` and `account-confined` require
    `filter_policy.exec_mode: restricted`; `platform-authorized` requires
    `platform_role`; `account-egress-restricted` requires a non-empty
    `permitted_destinations`; an attested rung requires `attestation`
    (`asserted_by` and `reference`, both required) and no other rung may carry
    one. The proxy refuses a response that disagrees with itself, so the
    agreement is checked here.

  What a rung may be chosen at all is a **capability** question on two levels:
  the proxy build's (`AuthorizeRequest.capabilities`) and the target's (the
  reports of §4), and M17 is where both live;
- **session deadline** (`session_deadline`) — an **absolute instant**, not a
  duration, which the proxy enforces locally so it survives this server being
  unreachable (proxy D16). A duration would re-anchor on every hop of a chained
  route and silently multiply the window. A bundle authors a *maximum duration*
  and the engine resolves it against its time input, bounded by the expiry and
  the asserted window of any grant that supplied the access — which is what "this
  server sets it having already weighed the window" means below. Reaching it is neither a denial nor an
  outage: the session is closed and the close is explained;
- **required session capture** (`require_session_capture`) — the route runs only
  if the session is recorded, checked before the target leg is dialled. It is the
  compensating control that makes an unbounded-privilege grant defensible: root
  on a target can disable that target's auditing and scrub its traces, and cannot
  touch a session captured in the proxy;
- **concurrency caps** (`concurrency`) per subject and/or target, which only the
  proxy can count — it holds the session registry. Absent or `0` is uncapped, and
  exceeding a cap is a **policy denial**, never an outage: the estate is healthy
  and the answer is "no";
- **grant context** (`grant_context`) — the external system, its reference, and
  the window it asserted, plus `additional_context`, which admits a JSON **string
  or a JSON object** and nothing else. The proxy carries all of it opaquely into
  every log record for the session and **never parses it, never matches on it,
  and never makes a decision from it** — that decision was made here, before the
  response was written. `window_start`/`window_end` are recorded, not enforced;
  the bound the proxy enforces is `session_deadline`, which this server sets
  having already weighed the window;
- **cache hint**, issued deliberately (5.4);
- **obligations**: record the session, require approval, require step-up auth.
  Session recording stops being advisory on unbounded-privilege routes: proxy
  D16 makes it a requirement the proxy refuses to serve without.

### 5.3 Evaluation

Ordered rules over a closed vocabulary, first match wins, with an explicit
default-deny at the end that is always present and always logged as the reason
when it fires. Compilation rejects, at authoring time: unreachable rules,
contradictory obligations, references to labels or groups that do not exist,
and any policy that grants a channel axis it does not constrain (a rule
permitting `direct-tcpip` with no destination list is a mistake, not a
wildcard — and the compiler says so rather than silently opening the estate).

Every evaluation emits a decision record (M4) naming the matched rule, the
inputs that made it match, and the obligations emitted.

`conn.hop_trail` is deliberately **not** in the 5.1 table: it is a routing input,
not a policy-matching axis. It selects where the path starts and it refuses
loops and over-long chains (§4), and it may only ever narrow an answer — a rule
that *granted* on the strength of a hop id would turn a field any caller can
write into authority, which is the one thing the trail must never become. The
decision record stores it as an input all the same, because "which hop asked,
and what had it already been through" is unrecoverable afterwards.

### 5.4 Cache hints are a policy decision, not an optimisation

The proxy may reuse a decision only when this server attaches a hint (proxy
§6.4), and the lifetime is this server's to set. So the hint is part of policy,
authored per rule: omit it for anything sensitive and every connection is
re-decided. Two invariants this server must never violate:

- **A key is never shared across identities.** The key selects the sharing
  scope; one shared across subjects serves one user another user's policy.
- **Never issue a hint the revocation stream cannot withdraw** (M9). If the
  event path for a proxy is unhealthy, stop issuing hints to it — a cached
  allow with no way to revoke it is just a slower revocation.

**A cacheable response may never carry a monotonic floor**, which is the rule
that put the uid floor on its own endpoint rather than on a field (§4). A cached
decision is replayed from whenever it was taken, so any *high-water mark* riding
on one is served stale — and a stale floor is a **lowered** floor. That
disqualifies both responses this hint rides on, for the same reason and not
because of anything specific to authorize. The test is cacheability, not
endpoint: if a value is only safe when it is fresh, it does not belong on
anything this section governs.

**The same hint rides on two responses**: `/v1/authorize` and
`POST /v1/hostkeys/report`. It is the same object under the same rules — one opaque server key, a
server-owned lifetime, one revocation stream, and both invariants above, the M9
one included.

**The issue path is one function, and the M9 invariant is the gate on it.**
0008 built `fleet.Registry.CacheHint`: it takes the liveness read
(`EventStreamHealthy`), derives the opaque key from the components the rule
named, and clamps the lifetime downward to the server's ceiling. It lives in
`internal/fleet` rather than in either handler because two copies of "may I hint
this proxy right now" would be two places to get M9 wrong, and they would not
fail together.

**What the gate answers depends on the stream, and that is the whole of the M9
rule.** Liveness is a live event subscription (`internal/revoke` serves it and
implements `fleet.SubscriptionState`), so a proxy that holds one is hinted and a
proxy that does not is answered without a hint — on both responses, through the
one function. Absent means what every server did before the field existed: the
proxy re-asks. Before 0009 wired the stream the answer was *no* on both, which
was the behaviour rather than a placeholder; 0009 turned hints on by supplying
the subscription source, not by changing the rule in front of them.

The host-key lifetime is this server's own (`fleet.host_key_cache_ttl`, default
5m, clamped downward by `decision.max_cache_ttl`), because a host-key answer has
no rule to author one; an authorize lifetime comes from the rule that decided
it.

What is specific to the host-key response is the shape the proxy
reuses it on, and three consequences this server owns:

- **The proxy keys host-key reuse on `target`, `target_port` and
  `host_key.fingerprint`, and on nothing wider.** What it reuses is therefore
  the answer to "may this target, presenting *this* key, be reached" — a target
  presenting a different key is a different lookup, misses, and is reported, so
  a man-in-the-middle, a rotated key and a rebuilt host all still reach this
  server on the first connection that sees the new key (proxy D7). Hinting here
  authorises exactly that and nothing more: the lookup shape belongs to the
  proxy, and only the permission to reuse it belongs to this server.
- **A `reject` and a `known: false` are never reused, however they are hinted.**
  A rejected host key is a security event this server must keep seeing, and
  reusing a first sighting would replay "trusted on first use" into the audit
  log for every later connection. So a hint on either is not a widening — it is
  dead weight, and this server does not issue one.
- **A subject-scoped `cache_invalidate` does not drop a host-key decision.** A
  host-key decision is not made for a subject, so `subject` cannot match one.
  Withdrawing a host-key decision means publishing that decision's own `key`, or
  `resync`, so the key is stored on the host-key record (`target_host_keys.
  cache_key`, migration 0005) and resolved from it rather than re-derived — a
  key recomputed under a later revision of the scope would match nothing any
  proxy holds, and the withdrawal would report success having dropped nothing.
  The operator surface therefore says on every publication **what it covered**,
  because a revocation that silently misses is worse than one that refuses.

---

## 6. Identity, MFA, and credentials

- **Federation (M7).** OIDC and SAML brokers behind one interface
  (`identity.Broker`). The two protocols agree on the only thing this server
  needs — a browser goes somewhere, comes back carrying something, and what it
  carries names a person — so everything above the seam is written once:
  the flow row, its single use, the claim mapping, the session. That is what
  makes the mapping's guarantee provable rather than asserted: there is one
  place claims become attributes.

  **OIDC** is authorization code + PKCE only, with the ID token verified against
  the issuer's published keys. The implicit and hybrid flows are not implemented
  (they put a token in a URL, and therefore in a browser history, a referrer
  header and a proxy log) and PKCE is not optional. **SAML** is Web SSO,
  HTTP-Redirect out and HTTP-POST back, with a narrow profile stated rather than
  configured: no IdP-initiated login, no unsigned assertion under any
  configuration, and `InResponseTo` checked against the flow this server started.
  Encrypted assertions are not implemented and are a feature rather than an
  omission.

  **An explicit mapping** turns IdP claims and groups into policy attributes; it
  is versioned, validated at authoring time, and named in the decision record,
  because "why did Alice match the `sre` rule" is answered by the mapping as
  often as by the rule. It is an **allow-list, not a transform**: a claim it does
  not name does not become an attribute — not renamed, not passed through, not
  namespaced — and a claim may not be mapped onto a name this server sets for
  itself (`chain_hop_proxy_id`, anything under `hoplock.`). Transformation
  languages are deliberately absent: a mapping language is a policy language,
  and this product already has one. Multi-IdP federation, complex
  claim-transformation rules and SCIM provisioning are Enterprise's, behind
  `ext.IdentitySync`.

  **A flow is a row, not process memory**, and single use — the same two rules
  the MFA challenges below live by, for the same two reasons: nothing makes a
  browser's callback land on the node that started the flow, and a replayed
  callback must be told "spent" rather than "never issued".

  **A claim mapping a tenant has not authored maps nothing.** That is the safe
  direction: identities arrive with no attributes and no groups, and a policy
  that grants on attributes grants nothing. A stored mapping that no longer
  parses is an **outage**, never an empty mapping — falling back would silently
  strip every attribute in the tenant, which reads as a permissions bug and
  would be debugged as one.

- **Users, groups, roles and RBAC (M7, M18).** Local users and groups are
  first-class records, so a deployment works before any IdP is connected and
  break-glass access exists when the IdP is down. Group membership from either
  source feeds policy identically — a rule cannot tell where a group came from,
  which is why the two are merged into one sorted list on the subject row and
  nothing downstream records which half was which.

  **The role set is fixed and lives in code** (`internal/identity/rbac.go`):
  auditor, policy-author, grant-admin, fleet-admin, admin, over a closed
  permission enum. There is no `roles` table, because a role whose permissions
  are rows is a role whose permissions can be widened by an UPDATE. What *is*
  data is who holds which role, per tenant: a role granted in tenant A confers
  nothing in tenant B, including to an administrator, and that is the binding's
  primary key rather than a filter somebody remembers to apply.

  **RBAC is enforced in one place**: the north-bound route table
  (`internal/httpapi/north`). Every route is registered with an access class and,
  where it resolves a tenant, one permission; the middleware authenticates,
  resolves exactly one tenant from the principal's scope, and checks that
  permission before a handler runs. A handler is never asked, because M18's
  failure mode is not a wrong decision in the middleware — it is one handler that
  read the tenant from the path itself. The console (0016) reaches it through the
  API, so there is no second enforcement point to keep in step.

- **Break-glass is asserted, never inferred (M7).** A local credential is flagged
  at the moment it is minted; the flag travels on the principal, is stored on the
  credential row and on the subject row, and is written into the decision record
  and the audit record. A reader never has to conclude "break-glass" from
  `source == local`, because "local" will one day mean something else. The audit
  record is part of the login rather than a side effect of it: a break-glass
  login this server cannot write down is one it refuses, since an unrecorded one
  is worse than one that looks normal.
- **MFA orchestration.** The contract makes MFA entirely this server's concern:
  the proxy relays and polls. That means owning challenge lifetime, poll
  intervals, replay resistance, and the deny-on-expiry path — none of which is
  a provider's business, because all of it is the same whichever provider is in
  play. A provider supplies a factor and answers "how is it going"; everything
  else is `internal/identity`'s.

  Four rules that follow, and each has a test:

  - **A challenge is single use.** A resolved one is never replayable,
    whichever way it resolved — an approved one most of all, since replaying it
    turns one approval into an unlimited supply. The row is kept and refused
    rather than deleted, so a later poll is told "spent" rather than "never
    issued": two different facts that deserve two different audit records even
    though they share a status code.
  - **Expiry is a deny**, never a `200` that leaves the proxy polling.
  - **Challenges are rows, not process memory.** Nothing makes a proxy's polls
    land on the node that issued one, so an in-memory challenge answers
    "unknown token" — a deny — to a user who did nothing wrong, on a deployment
    that has merely been scaled out (M5).
  - **Poll rate and a poll budget are both bounded.** A poll inside the
    advertised interval is answered from the stored row without consulting the
    provider; past the budget the challenge is abandoned. A caller that ignores
    `poll_after_ms` is finite either way.

  Determinism matters for tests — the proxy's mock models it with a "pending
  polls" counter, and the conformance suite depends on that behaviour being
  reproducible here, so `identity.ScriptedMFA` mirrors that fixture field for
  field. It is a CI facility and not a second factor: a subject enrolled with
  it has one that answers the way its row says it will.
- **Credential brokerage (proxy D6a).** The route names the target credential
  method. Beyond selecting it, this server is the natural home for the
  credentials themselves: an SSH CA issuing short-lived, narrowly-scoped target
  certificates per session beats a long-lived management certificate sitting on
  every proxy's disk.

  **The CA is built** (`internal/credential`): per tenant, keyed through the
  `ext.KeyStore` seam so that custody can move to an HSM without a second code
  path, with the software custodian holding its private half as AES-256-GCM
  ciphertext under a key-encryption key the database never sees. Certificates are
  minted over a public key **the proxy generated** — no private key ever crosses
  this API — and are scoped to a principal, a target, a session and a window of
  minutes. What SSH can enforce is the principal, the window and the critical
  options; there is no hostname field, so target scoping is enforced at issuance
  and recorded in `ssh_certificates.target`, which is what makes it auditable.

  **Rotation answers the outstanding-certificate question rather than deferring
  it.** A routine rotation retires the key into the trust bundle for an overlap
  longer than the maximum certificate lifetime, so nothing it signed outlives the
  trust in it and no session drops. A compromise rotation removes it from the
  bundle at once and revokes every outstanding certificate it signed. "We rotate"
  without both answers is not a rotation story.

  **It cannot reach a proxy yet, and that is upstream's.** `TargetAuth` is
  extensible for exactly this — the schema says so outright — but the method value
  and its parameters are the Hoplock Proxy repository's to define, and `contract/`
  is vendored read-only here (M1). A new method is vocabulary, so it bumps
  `policy_version` upstream (§4). `internal/credential/seam.go` names the exact
  shape, refuses to put it on the wire, and carries the test that fails the build
  on the day the method lands in the vendored document.

---

## 7. Audit, telemetry, and export (M8)

- **Ingest** — idempotent on `record_id`, batch and priority paths, with the
  priority path durable before it acks (§4). Idempotency is the database's: the
  submitted ids are checked and the survivors inserted under a lock held on the
  stream, so concurrent writers on different nodes cannot both take a chain
  position. **A batch is all or nothing.** The contract documents `accepted` as
  "fewer than sent means the rest were duplicates; the proxy may drop them", so
  a count short by anything other than duplicates would tell the proxy to
  discard records this server never stored — a malformed record therefore fails
  its whole request with a `400` and stores none of it.
- **A record belongs to a session unless nobody was present for it.** Two
  kinds of record arrive with `session_id: ""`: an `error` record for a sweep
  failure, and a sweep's `device.config.change` (a sweep belongs to nobody's
  session). Both are stored as belonging to no session, and `""` is never
  looked up as a session. Refusing either would fail its batch, and the proxy
  retries the oldest segment of its disk buffer until the server takes it. So
  all of that proxy's delivery, priority records included, would stall behind
  one record. 0010 accepts the first. **0014** widens the rule to the second.
- **The kind enum is closed and an unknown kind is refused**; severity is not,
  and the asymmetry is deliberate. A severity is a three-value scale a reader
  can act on without knowing the value; a kind is what every query below filters
  by, so a kind nobody knows is a record nobody finds. Refusing it is loud — the
  proxy keeps the record and an operator sees an error — where accepting it is
  silent. Adding one is an upstream change (M1).
- **Storage** — append-only, hash-chained per tenant per stream; a verifier can
  prove no record was altered or removed, and reports the first break with the
  record, the position and both hashes. A **stream is the submitting proxy**:
  the unit that already writes in order, so serialising it costs nothing, where
  one chain per deployment would put the whole fleet behind one lock and one
  chain per session would make a deleted session undetectable. Session capture
  (pty streams) is stored in its own table so no query over the records drags a
  megabyte of terminal output along, and the chain covers it through a digest
  inside the hashed record.
- **What the chain does and does not defend against.** It detects anybody who
  can reach the database but cannot rewrite every later record in the stream: a
  stray `UPDATE`, an application bug, a restored row, a partially-successful
  attacker. It does **not** defend against one who can rewrite the whole chain,
  because the verifier's only input is the database. Closing that needs an
  anchor published outside this system on a schedule, which is future work and
  is deliberately not claimed: an audit store that overstates its guarantee is
  worse than one that states a smaller one, because the overstatement is what
  somebody builds a compliance claim on.
- **Redaction is recorded, not silent.** A password-shaped attribute is
  replaced before anything is hashed, and the key is listed in the record's own
  `redacted` field — a record carrying a credential is still a record of
  something that happened, so refusing it would delete the evidence of the bug
  that produced it, and removing the value without saying so would be a record
  that lies by omission.
- **Query** — by session, subject, target, decision id, proxy, time range,
  kind, severity, event name, grant reference and device field. "Show me every
  blocked command on `env=prod` last week, and who approved the access that
  made it possible" is one query joining audit to decisions and to targets —
  `env=prod` is a label, and labels live on the target rather than on the
  record — and it is the demo that sells the product. The layer is built here;
  the HTTP surface over it is 0014's, with one exception: **a record read-back
  path outside `/v1`**, because nothing on the contract reads a record back and
  the priority ack's durability guarantee is otherwise unobservable. It is off
  unless configured, has a credential of its own, sits on its own port, and is
  deleted by 0014.
- **Device fields are audit facts.** The ephemeral-account mapping record carries
  the `device_field.<name>` values the session was provisioned with (§5.2),
  because on a device that is one unit partitioned into many the
  target string alone does not say which partition the administrator was created
  in: `device_field.vdom` is the difference between an account scoped to one
  virtual domain and a **global** administrator on the same host. Storing them as
  opaque data is right; dropping them because the contract does not enumerate
  them is not.
- **The enforcement rung is an audit fact, and it is the rung that was in
  force** — never the one policy requested. The rung puts four fields on the
  record: `enforcement_execution`, `enforcement_reach`, `enforcement_verified`
  (`false` on an attested rung, because nothing here verified it), and
  `enforcement_attested_by`. Whether the record says `account-restricted` or
  `proxy-inspected` is the whole point of the vocabulary, so a session that ran on
  a different rung than the policy asked for must say so: a ladder degrades and a
  rung can be unavailable, and a record repeating the request would be a record
  that lies. The same holds for the credential method (proxy D14), which puts two
  more fields on the record: `credential_method` and `credential_rung`, the
  satisfied entry's position in `target_auth_ladder` **counting from 1**. A
  degraded credential is therefore rung `> 1`. These are the only names the
  proxy emits (`Hoplock/proxy#66`), and 0014 makes them the only names this
  store reads. 0010 also read a `target_auth_*` pair (0-based), which the
  proxy's own contract text had published by mistake and `#66` corrected.
  Beside them sits `algorithm_profile` (§5.2), the profile **in force** on the
  target leg, because anything but `default` is a deliberate weakening of the
  proxy→target leg. The proxy stamps it on the session's `provisioning` record
  and on the `device.account.mapping` event **always, `default` included**. So
  an absent profile means the record is not about a target leg (a hop, or a
  failure before provisioning), never `default`, and a weakening is a query
  over one field. All three are **audit facts and never user-facing ones** —
  the single place §4.3's disclosure rule does not apply, because the rung in
  force is information about the estate rather than about the user's own
  request.
- **Every change the proxy makes on a device is a record.**
  `device.config.change` is `kind: provisioning`, `severity: info`, and it
  arrives on the **batch** path, one record per change: `platform`,
  `device_change_op` (`create`, `modify` or `delete`), the object as
  `target_account` plus `device_object_kind` when it is not an administrator,
  the route's `device_field.<name>` values, and the session id if there is
  one. A sweep's change carries no session id and no device fields, because a
  sweep has no route. It is the drift reconciliation feed: a
  customer's configuration monitoring sees every one of these changes, and this
  store is what lets a SIEM explain them (Enterprise E7 exports it). It is
  indexed by device, object and operation (0014).
- **A target the route's profile cannot reach is a record, not a mystery.**
  `target.algorithm_policy_unmet` is an `error` record at `warn` naming
  `algorithm_profile`, `target_addr`, `algorithm_axis` and
  `target_algorithms_offered`. The user is told only that it is an outage. It
  is how an operator finds the devices the secure `default` no longer reaches
  (§5.2), and 0014 shows it to the policy author as a warning.
- **Grant context rides every record for a session** (`grant_context`),
  copied through by the proxy as opaque data. Store it as it arrives —
  including `additional_context`, which is a string **or** an object — and never
  parse it into policy: it is what lets an auditor answer "why was this allowed"
  without joining two systems by hand, and M16 is what puts it there.
- **Retention has to remove a chain's TAIL, never a record out of its middle.**
  Deleting a record leaves its successor pointing at a hash nothing produces,
  which is indistinguishable from tampering — the mechanism cannot tell a
  policy from an attacker, and it must not try. So a retention job (0014) walks
  a stream from its start, deletes a contiguous prefix, and records the
  sequence it deleted up to and the hash of the last record it removed;
  verification then starts from there instead of from 1, and the deleted span
  is a documented fact rather than a break. Capture bytes are the exception
  that motivates it: a pty stream dwarfs everything else in the store, so
  "delete captures older than N days, keep the records" is the first retention
  rule anybody writes — and it is safe precisely because the record keeps the
  digest, so what is verifiable afterwards is that the capture is gone rather
  than that it was changed.
- **Export** — Splunk / Sentinel / Elastic sinks behind one interface, with
  backpressure and retry. Downstream consumer only (M8).
- **Redaction** — the initial-auth password never reaches this server and must
  never be written even if a malformed record contains one. Assert it in tests,
  because "the proxy promises not to send it" is not a control on this side.

---

## 8. Cross-cutting conventions

- **Module path**: `github.com/hoplock/control` — it tracks
  the repository URL, not the product name, because a Go module path that does
  not resolve to its repository cannot be fetched by path. The component is still
  called Hoplock Control everywhere prose refers to it.
- **Go**: the `go` directive is the floor; CI builds on both the floor and the
  latest stable, with `GOTOOLCHAIN: local` so the floor is enforced rather than
  asserted. Same reasoning as the proxy's PLAN §8.
- **Config**: YAML, documented in `config.example.yaml`, strict decoding
  (unknown keys are an error).
- **License (M14)**: Apache-2.0 `LICENSE` plus the per-file SPDX header in
  `docs/LICENSE-HEADER.md`.
- **Errors/logging**: no secrets, no credentials, no tokens in errors or logs.
  Every response carries a correlation id; every `5xx` says outage, never deny
  (M11).
- **Migrations**: forward-only, versioned, applied by an explicit command —
  `hoplock-control migrate` (`--dry-run` prints what would be applied and
  changes nothing), also reachable as `make migrate`. Never automatically on
  boot in production, where two nodes starting at once must not race; the
  server has no code path that migrates. A migration's checksum is recorded
  when it is applied, so editing a merged one is an error rather than a silent
  divergence between two deployments.
- **Testing**: unit tests per package; the policy engine tested exhaustively and
  in isolation; Postgres-backed tests against a real database in CI; the
  conformance suite (M1) run against this server **and** the proxy's mock.
- **CI**: build, vet, test, lint, migrations check, contract-drift check,
  conformance, `govulncheck`.

---

## 9. Test topology

The prototype's full topology runs in one CI job with `docker compose`:

| Node | Container |
| --- | --- |
| Postgres | stock image |
| Management server | `cmd/hoplock-control` |
| Proxy | Hoplock Proxy's image, pointed at this server |
| Target | `sshd` image with the provisioning account and an appliance-like account |
| Client | thin image running scenario SSH clients |

The point of this topology is the thing neither repo can test alone: a **real
proxy** driven by a **real PDP**. The Hoplock Proxy repository proves it enforces what a
mock tells it; this repo proves it decides correctly in isolation; only here do
"decides" and "enforces" meet.

---

## 10. Phased delivery

One prompt = one PR = one phase (see `prompts/queued/`).

| # | Phase | Delivers |
| --- | --- | --- |
| 0001 | Project scaffold & conventions | module, layout, licence + headers, Makefile, CI skeleton, config loader |
| 0002 | Contract vendoring & conformance harness | `contract/`, drift check, `cmd/pdpconform` proven against Hoplock Proxy's mock |
| 0003 | Storage layer & migrations | Postgres repositories, forward-only migrations, tenancy columns (M12), the per-target uid allocation cursor |
| 0004 | **Extension points** | public `ext/` package, registration, import-graph guard (M15) |
| 0005 | Policy model & decision engine | bundle parse/validate/compile/evaluate + decision records (M3, M4) |
| 0006 | Fleet registry, health & config distribution | enrollment, heartbeat, zone graph, pathfinding, hop direction, versioned config rollout (M6), the capability store both sources write to (M17) |
| 0007 | South-bound authentication | the south-bound listener and its credential (M22), `/v1/auth/*`, MFA orchestration, host-key reporting (no cache hint until 0009 can withdraw one), `/v1/capabilities/report`, `/v1/uids/lease` and its monotonic cursor |
| 0008 | South-bound authorize & route | `/v1/authorize`: snapshot assembly, cache hints, latency budget (M5) |
| 0009 | Revocation & event fan-out | `/v1/proxies/{proxy_id}/events`, event bus, replay, resync, kill switch (M9) |
| 0010 | Audit ingest & tamper-evident store | batch + priority ingest, the per-tenant-per-stream hash chain and its verifier, redaction, the query layer, and the record read-back path 0014 deletes (M8) |
| 0011 | Identity, users, groups, roles & RBAC | local identity, groups, the fixed role set and its one enforcement point, OIDC/SAML brokers behind one interface, the versioned claim mapping, a real out-of-band MFA provider, the per-tenant SSH CA and its rotation story, and the north-bound listener's credential model — a caller never asserts its own tenant (M7, M18, M2) |
| 0012 | Access grants | manual time-boxed grants; `ext.GrantWorkflow` seam for Enterprise (M10) |
| 0013 | External access context | `ext.AccessContextProvider`, push receiver with scope binding, probe path inside the authorize budget, declarative HTTP provider as the default (M16) |
| 0014 | North-bound API, inventory & policy lifecycle | authoring, versioning, validation, **simulation**, **explain**, targets/identities CRUD, GitOps (M2, M4), machine-readable error codes (M21); fleet configuration made deliverable — the contract re-vendored at `4.3.0`, `fleet.ConfigPublisher` wired to `config_changed`, the config fetch and report served, publish limited to proxy D18's fleet-owned keys; the records proxy phase 0043 emits (`Hoplock/proxy#66`) ingested and answered for — a sweep's session-less change record accepted, the drift feed indexed, `credential_method`/`credential_rung` (counting from 1) the only names, the weakening and degradation queries, and the `target.algorithm_policy_unmet` authoring warning |
| 0015 | Instance identity & supervisory registration | a deployment's own identity and version, the north-bound compatibility promise, and outbound registration to a supervisor (M19) |
| 0016 | Management console | operator web UI served from the binary: fleet, explain, audit, policy, inventory — built to `ui/DESIGN.md` and its enforcement (M20), localisable with English the only catalogue (M21) |
| 0017 | Cross-repo E2E topology, CI gate & hardening | real proxy + real control plane + Postgres + target, scenario suite, `govulncheck` |
| 0018 | One contract version, end to end | a single supported `policy_version` tied to the vendored document, a loud refusal for any other and a `400` for an absent one, no thinning path |
| 0019 | Post-quantum posture | TLS on this server's own listeners with the wire posture stated and asserted rather than inherited from the ingress, a hybrid key exchange required where an operator says so, and the algorithm vocabulary plumbed so a post-quantum signature is an enum member rather than a redesign (M2, M13) |

> **Audits are not in this table, and not in the queue.**
> `prompts/audit/` holds prompts that run repeatedly against the whole
> repository rather than once at a point in this sequence
> (`docs/PROTOCOL.md` §6). There is one today:
> `prompts/audit/cross-repo-impact.md`, which audits what `hoplock/proxy` has
> made true and this repository has not caught up with — every merged upstream
> PR that touched a shared surface, traced into these prompts, plus a check of
> the contract document itself that trusts no PR body. It is run when the user
> names it, **before** building on text the proxy may have moved underneath us.
>
> It exists because `docs/CROSS-REPO-PROTOCOL.md` §4.1 puts the downstream look on
> the upstream author at merge time, and two of those looks have now described
> text this repository did not contain. A check that runs once, from one side,
> needs a compensating pass from this one.

> **Renumbering note (multi-instance revision).** Phase 0015 is new: a
> deployment that can be *supervised* needs its own identity, a north-bound
> surface it can promise across versions, and a way to register outbound to
> something above it (M19). It sits after the north-bound API because it is that
> API's compatibility story, and before the console because the console renders
> a deployment's identity. Under `docs/PROTOCOL.md` §6 the queued prompts below
> it were renumbered — **0015→0016, 0016→0017, 0017→0018** — and nothing is
> implemented yet, so no frozen name moved. Anything written before this
> revision that hands work to "0015" means the console, now **0016**; to "0016"
> means the E2E topology, now **0017**.
>
> Tenancy (M18) added no phase. It is woven into 0003, 0005, 0006, 0010, 0011
> and 0014 instead, for exactly M12's reason: a dimension retrofitted into a
> populated store is a migration nobody wants to run, and one retrofitted into a
> compiled policy program is worse.
>
> This revision is **downstream-driven**: `hoplock/enterprise` needs both seams
> to build a multi-instance supervisory plane (its E14). Per
> `docs/CROSS-REPO-PROTOCOL.md` §2 it merges here first, and Enterprise
> describes it only afterwards. It reached this plan with no flow to carry it —
> which is one of the two cases `docs/CROSS-REPO-PROTOCOL.md` §3.2 now names,
> and why that section is a flow with an owner and a runnable kickoff rather
> than a rule ending in "tell the user". A request arriving here today follows
> §3.2 and `docs/KICKOFF.md`'s "Upstream request" block, and the phase that
> answers it owes a downstream sync **back** to the repository that asked (§5).
> That is what phase 0015 would owe `hoplock/enterprise` once it merges.

> **Renumbering note (privileged-access revision).** Phase 0013 is new: external
> access context (M16) has to exist before the north-bound API is built, because
> that surface exposes provider registration, scope bindings, and the reason a
> grant exists. Under `docs/PROTOCOL.md` §6 the queued prompts below it were
> renumbered — **0013→0014, 0014→0015, 0015→0016** — and nothing is implemented
> yet, so no frozen name moved. Anything written before this revision that hands
> work to "0013" means the north-bound API, now **0014**.
>
> This revision follows an upstream one in `hoplock/proxy` (decisions D13–D17
> there). Per `docs/CROSS-REPO-PROTOCOL.md` §2 it must not merge before that one
> does.

**What an old number resolves to.** The notes above are the record of *why* each
renumber happened. This table is the answer to "a document says 0014 — what is
that now?", already composed so that nobody resolves an old number by composing
notes by hand (`docs/PROTOCOL.md` §6). Rows are grouped by revision, **oldest
first**, and each row's result is composed through every revision below it.
Regenerate it in the PR that renumbers.

| Revision | Written as | Is now | Phase |
| --- | --- | --- | --- |
| privileged-access | 0013 | 0014 | `0014-northbound-api-and-policy-lifecycle` |
| privileged-access | 0014 | 0016 | `0016-management-console` |
| privileged-access | 0015 | 0017 | `0017-e2e-topology-and-ci` |
| multi-instance | 0015 | 0016 | `0016-management-console` |
| multi-instance | 0016 | 0017 | `0017-e2e-topology-and-ci` |
| multi-instance | 0017 | 0018 | `0018-single-contract-version` |

Nothing is implemented yet, so no frozen name has ever moved: every row above
renumbered a queued prompt only.

Ordering rationale worth keeping: the conformance harness (0002) comes second so
that every later phase has a red/green target it did not write itself; the
policy engine (0005) precedes every endpoint that uses it and is pure, so it can
be made correct before HTTP exists; the fleet graph (0006) precedes authorize
(0008) because a route is a path over it; and the north-bound surface (0014)
comes after the south-bound one is real, because simulation and explanation need
decision records to have been produced by something.

Prompts may add or re-order later phases; any prompt that introduces new queued
prompts MUST preserve the numbering invariants in `docs/PROTOCOL.md`.

> **On 0018 running last.** It is an audit, and an audit wants everything that
> could hold a version number to exist first. The position is not an invitation
> to build multi-version machinery in the meantime: this product ships its proxy
> and its server together and has no installed base, so every phase before it
> should already carry one version in one place, and 0018 should find little.
> What it does own is the decision that a mismatch is a **loud refusal** rather
> than a thinned answer, and the documents that still describe a mid-upgrade
> fleet (§4 above among them).

---

## 11. Out of scope for the prototype

**Hoplock Enterprise's, by design (M15)** — approval workflows around grants,
SCIM and advanced enterprise IdP, long-term audit retention/archive and session
search, compliance reporting, SIEM export and SOAR actions, HSM/KMS,
high availability and clustering, air-gapped deployment, licensing,
enterprise support tooling, and the **packaged Qualys and BMC Helix access-context
integrations** (M16 — the seam and its declarative default are here, in 0013;
the vendor packaging is not). Each has a seam in `ext/` (0004); none has a
crippled placeholder here.

Genuinely out of scope for now:

- Editing the contract here (M1 — it is upstream, always).
- Multi-tenant **governance** — delegated administration, per-tenant
  entitlements, cross-tenant reporting. The *mechanism* is here (M18: tenancy
  resolved from the caller, per-tenant graph, program, chain and CA); the
  governance on top of it is Enterprise's (its E11).
- The **supervisory plane** itself — the thing that manages many deployments.
  This repository makes a deployment identifiable and supervisable (M19) and
  ships the registration client as its real default; aggregating a fleet of
  deployments is Enterprise's (its E14).
- HA/multi-region deployment. The decision path is designed to scale out (M5)
  and the event bus is behind `ext.ClusterCoordinator` (M9, 0004), but this
  repository runs one node; clustering is Enterprise.
- Endpoint agents and device posture collection. The policy model has a slot for
  posture attributes; nothing here collects them.
- Long-term storage tiering of session recordings.
