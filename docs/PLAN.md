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
  server must fan an operator action out to every proxy that needs it, survive
  a subscriber reconnecting with a `last_event_id`, and answer `resync` when it
  cannot replay. In-process broker for the prototype behind an interface, because
  multi-node deployment turns this into the one component that genuinely needs
  shared state.

  Corollary the contract already states and this server must honour: **a server
  that issues cache hints must serve this stream.** Issuing a hint without a
  working revocation path is issuing an access grant that cannot be withdrawn.
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
  2. **Every extension point ships a real default here.** A seam is not a hole
     where core functionality used to be. Control alone must be a complete,
     self-hostable product: a deployment of Hoplock Proxy + Hoplock Control is
     a working infrastructure access system, not a demo waiting for a licence.

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
  **device platform**, an **expiry posture**, an **enforcement rung**, and — since
  contract v3.1 — a set of **additional device fields** (proxy D13, D14, contract
  v4, and proxy phase 0016), and a proxy can satisfy those only if it has the
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

  **Contract v4 makes the capability question two-sourced, and the second source
  is the target.** An enforcement rung depends far more on the target than on the
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
│   ├── contract/           # generated Go types + handlers for the vendored contract
│   ├── store/              # Postgres repositories + migrations
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
│   ├── instance/           # deployment identity, supervisory registration (M19)
│   └── httpapi/
│       ├── south/          # proxy-facing handlers (the contract)
│       └── north/          # admin/operator/CI handlers
├── ext/                    # PUBLIC extension points — the seam Enterprise implements (M15)
├── ui/                     # management console, embedded into the binary
├── contract/               # VENDORED from the Hoplock Proxy repository — read-only (M1)
├── deploy/                 # docker-compose: this server + Postgres + a proxy
├── docs/                   # this plan, protocol, cross-repo protocol, learnings
├── prompts/                # queued and implemented phase prompts
└── migrations/             # versioned SQL, forward-only
```

### Component responsibilities

- **`internal/contract`** — the only package that knows the wire shapes of the
  south-bound API. Everything else speaks domain types, so a contract revision
  in the Hoplock Proxy repository lands in one package here.
- **`internal/policy`** — pure. Parse → validate → compile → evaluate, no HTTP,
  no database, no clock of its own (time is an input). This is the package that
  must be exhaustively tested, because it is where the product's promises are
  kept — and, per M13, the package the language helps least, so its variants are
  closed `Kind` enums that the `exhaustive` linter can check rather than open
  interfaces that it cannot.
- **`internal/decision`** — the composition root for an authorize call: gather
  identity, target labels, grants, fleet path, and connection metadata; evaluate;
  build the snapshot; write the decision record; decide whether to issue a cache
  hint. Latency budget (M5) is enforced here.
- **`internal/fleet`** — the graph, its liveness, and pathfinding. Owns which
  hop direction is possible right now.
- **`internal/audit`** — append-only writer, chain verifier, and query API.
  Nothing else writes audit rows.
- **`internal/revoke`** — subscriptions and fan-out. Owns event ids and replay.
- **`internal/instance`** — this deployment's own identity and version, and the
  outbound registration client that makes it supervisable (M19). It is a
  *client* of something above it, which makes it the only package here that
  dials outward on the management plane; it may never be reachable from the
  decision path.

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
| `POST /v1/hostkeys/report` | Record a reported target host key and answer with the trust decision |
| `POST /v1/capabilities/report` | Record the enforcement rungs one **target** can take, as the proxy found them by probing it (contract v4); answer `accepted` and, optionally, when to report next |
| `POST /v1/logs/batch` | Idempotent bulk ingest into the audit store; `202` |
| `POST /v1/logs/priority` | Single critical record, durable before the ack; `200` |
| `GET /v1/proxies/{id}/events` | Long-lived NDJSON revocation stream with heartbeats, replay, and `resync` |

Five obligations are easy to miss and are graded by the conformance suite:

- **The priority ack means durable.** The proxy acts on a critical security
  event knowing this server recorded it. Acking before the write lands turns
  that guarantee into a lie that only shows up after an incident.
- **Answer within the vocabulary the proxy declared.** Contract v2 made every
  policy field additive with a documented absent-value default, and in exchange
  the proxy **fails a session closed on an authorize field it does not
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

  **The current vocabulary is `4`** (`Hoplock/proxy#25`, merged): the two
  enforcement axes and the session bounds (§5.2). Every v4 field is additive with
  an absent-value default that is exactly what a v3 server produced — proxy-side
  enforcement only, no deadline, no required capture, no grant context, no
  concurrency cap — so the rule above is unchanged in kind and only larger in
  scope. The vendored document is `4.0.0`.

  **Contract v3.1 is the case `policy_version` alone does not cover**, and it is
  worth keeping straight even though v4 moved the number. v3.1 adds the
  `device_field.<name>` namespace (§5.2) and deliberately leaves `policy_version`
  at `3`: the number names the vocabulary a proxy can *read*, and nothing about
  reading a response changed — an older proxy parses a v3.1 route exactly as it
  always did. So version-aware assembly cannot gate a device field, because there
  is no version to gate it on, and it must not try. The document version and the
  negotiated vocabulary are two numbers that move independently, which is why
  neither is derived from the other (0002, 0018).

  What makes that addition safe is the layer below: the proxy skips a rung whose
  fields its driver does not declare, so a field an enforcing proxy cannot honour
  costs the rung rather than widening the session. Which proxy can honour which
  field is a **capability** question (M17), answered from the fleet registry, not
  from `policy_version` — and on a one-rung ladder the cost of getting it wrong is
  a denial, so the check belongs on the issue path. Contract v4 widens that
  question rather than changing it: an enforcement rung depends on the **target**
  far more than on the proxy, which is what the capability report below exists to
  answer.
- **A capability report is an observation, and it constrains rather than
  grants.** Since contract v4 the proxy probes a target it has just logged into
  and reports, on `POST /v1/capabilities/report`, which enforcement rungs that
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

- **Heartbeats are liveness, and their absence is a signal.** A proxy that
  stops hearing them reconnects and, past its staleness threshold, stops serving
  cached decisions entirely. A server that stalls its heartbeat writer degrades
  the whole fleet to uncached — correctly, but for the wrong reason.
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

---

## 5. Policy model & evaluation (M3, M4)

### 5.1 Inputs

| Axis | Examples |
| --- | --- |
| Subject | subject id, IdP source, groups, claims, authentication method, MFA |
| Device | posture attributes when an endpoint supplies them (optional) |
| Context | time of day, day of week, source network/geo, the proxy asking (`conn.proxy_id` — the entry proxy on a user's first hop, an inner hop on a chained one) |
| Target | hostname, labels (`env=prod`, `kind=appliance`, `owner=payments`), zone |
| Session | requested channel type, in-channel request, forwarding destination, global request, command |
| Grants | live JIT grants for this subject and scope (M10), including windows confirmed from external context (M16) |
| External context | a scan, ticket, or incident asserted by an integration and confirmed at decision time (M16) |

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
  accept, with a one-entry ladder meaning "this method or nothing";
- **device platform and expiry posture** on `ephemeral-account` routes (proxy
  D13), and a **per-route algorithm profile** where the target speaks something
  `x/crypto` does not enable by default;
- **additional device fields** on those same routes — the open
  `device_field.<name>` namespace contract v3.1 adds beside the five
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
- **enforcement rung** per axis (`enforcement`, contract v4), where the route
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
  route and silently multiply the window. Reaching it is neither a denial nor an
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

The proxy may reuse an authorize decision only when this server attaches a
hint (proxy §6.4), and the lifetime is this server's to set. So the hint is
part of policy, authored per rule: omit it for anything sensitive and every
connection is re-decided. Two invariants this server must never violate:

- **A key is never shared across identities.** The key selects the sharing
  scope; one shared across subjects serves one user another user's policy.
- **Never issue a hint the revocation stream cannot withdraw** (M9). If the
  event path for a proxy is unhealthy, stop issuing hints to it — a cached
  allow with no way to revoke it is just a slower revocation.

---

## 6. Identity, MFA, and credentials

- **Federation (M7).** OIDC and SAML brokers behind one interface. An explicit
  mapping turns IdP claims and groups into policy attributes; the mapping is
  versioned, validated, and visible in the decision record, because "why did
  Alice match the `sre` rule" is answered by the mapping as often as by the rule.
- **MFA orchestration.** The contract makes MFA entirely this server's concern:
  the proxy relays and polls. That means owning challenge lifetime, poll
  intervals, replay resistance, and the deny-on-expiry path. Determinism matters
  for tests — the proxy's mock models it with a "pending polls" counter, and
  the conformance suite depends on that behaviour being reproducible here.
- **Credential brokerage (proxy D6a).** The route names the target credential
  method. Beyond selecting it, this server is the natural home for the
  credentials themselves: an SSH CA issuing short-lived, narrowly-scoped target
  certificates per session beats a long-lived management certificate sitting on
  every proxy's disk. That is an **additive** contract change (the proxy's
  `target_auth` object is extensible on purpose) and therefore starts in the
  Hoplock Proxy repository, not here — this plan records the intent and phase 0011 builds
  the CA behind it.

---

## 7. Audit, telemetry, and export (M8)

- **Ingest** — idempotent on `record_id`, batch and priority paths, with the
  priority path durable before it acks (§4).
- **Storage** — append-only, hash-chained per stream; a verifier can prove no
  record was altered or removed. Session capture (pty streams) is stored so a
  session can be replayed, with the size and retention implications made
  explicit rather than discovered.
- **Query** — by session, subject, target, decision id, time range, and event
  type. "Show me every blocked command on `env=prod` last week, and who
  approved the access that made it possible" is one query joining audit to
  grants, and it is the demo that sells the product.
- **Device fields are audit facts.** The ephemeral-account mapping record carries
  the `device_field.<name>` values the session was provisioned with (§5.2,
  contract v3.1), because on a device that is one unit partitioned into many the
  target string alone does not say which partition the administrator was created
  in: `device_field.vdom` is the difference between an account scoped to one
  virtual domain and a **global** administrator on the same host. Storing them as
  opaque data is right; dropping them because the contract does not enumerate
  them is not.
- **The enforcement rung is an audit fact, and it is the rung that was in
  force** — never the one policy requested. Contract v4 puts four fields on the
  record: `enforcement_execution`, `enforcement_reach`, `enforcement_verified`
  (`false` on an attested rung, because nothing here verified it), and
  `enforcement_attested_by`. Whether the record says `account-restricted` or
  `proxy-inspected` is the whole point of the vocabulary, so a session that ran on
  a different rung than the policy asked for must say so: a ladder degrades and a
  rung can be unavailable, and a record repeating the request would be a record
  that lies. The same holds for the credential method (proxy D14).
- **Grant context rides every record for a session** (`grant_context`, contract
  v4), copied through by the proxy as opaque data. Store it as it arrives —
  including `additional_context`, which is a string **or** an object — and never
  parse it into policy: it is what lets an auditor answer "why was this allowed"
  without joining two systems by hand, and M16 is what puts it there.
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
- **Migrations**: forward-only, versioned, applied by an explicit command — never
  automatically on boot in production, where two nodes starting at once must not
  race.
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
| 0003 | Storage layer & migrations | Postgres repositories, forward-only migrations, tenancy columns (M12) |
| 0004 | **Extension points** | public `ext/` package, registration, import-graph guard (M15) |
| 0005 | Policy model & decision engine | bundle parse/validate/compile/evaluate + decision records (M3, M4) |
| 0006 | Fleet registry, health & config distribution | enrollment, heartbeat, zone graph, pathfinding, hop direction, versioned config rollout (M6), the capability store both sources write to (M17) |
| 0007 | South-bound authentication | `/v1/auth/*`, MFA orchestration, host-key reporting, `/v1/capabilities/report` |
| 0008 | South-bound authorize & route | `/v1/authorize`: snapshot assembly, cache hints, latency budget (M5) |
| 0009 | Revocation & event fan-out | `/v1/proxies/{id}/events`, event bus, replay, resync, kill switch (M9) |
| 0010 | Audit ingest & tamper-evident store | batch + priority ingest, hash chain, verifier, query (M8) |
| 0011 | Identity, users, groups, roles & RBAC | local identity, roles, RBAC, OIDC/SAML federation, claim mapping, SSH CA (M7) |
| 0012 | Access grants | manual time-boxed grants; `ext.GrantWorkflow` seam for Enterprise (M10) |
| 0013 | External access context | `ext.AccessContextProvider`, push receiver with scope binding, probe path inside the authorize budget, declarative HTTP provider as the default (M16) |
| 0014 | North-bound API, inventory & policy lifecycle | authoring, versioning, validation, **simulation**, **explain**, targets/identities CRUD, GitOps (M2, M4) |
| 0015 | Instance identity & supervisory registration | a deployment's own identity and version, the north-bound compatibility promise, and outbound registration to a supervisor (M19) |
| 0016 | Management console | operator web UI served from the binary: fleet, explain, audit, policy, inventory |
| 0017 | Cross-repo E2E topology, CI gate & hardening | real proxy + real control plane + Postgres + target, scenario suite, `govulncheck` |
| 0018 | One contract version, end to end | a single supported `policy_version` tied to the vendored document, a loud refusal for any other, no thinning path |

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
> describes it only afterwards.

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
