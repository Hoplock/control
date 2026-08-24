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
  arithmetic rather than measurement — proxy phase 0017 exists to replace it,
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
- **M13 — Tech choices.** Go (same floor policy as the proxy: the `go`
  directive is the minimum, CI tracks the latest stable, and the floor moves only
  when a dependency moves it). Postgres via `pgx`, with forward-only versioned
  migrations. YAML config. JSON over HTTPS. Structured logging. No ORM: the
  decision path's queries are few, hot, and worth reading.
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
  **device platform**, an **expiry posture**, and an **enforcement rung** (proxy
  D13, D14, and the enforcement-point revision), and a proxy can satisfy those
  only if it has the driver, the local material, and a target that supports
  them.

  A decision naming something the enforcing proxy cannot provide is not a
  near-miss — it is a session that is denied as an outage, or worse, one whose
  audit record claims a control that was never applied. So enrollment and
  heartbeat carry the proxy's declared capabilities, the decision engine treats
  them as a constraint on what it may answer, and the north-bound API shows an
  operator why a policy cannot be satisfied on a given proxy **before** they
  publish it rather than after a user complains.

  This also makes proxy D14's ladder authorable with intent: knowing which
  methods a proxy actually has is what separates "prefer the strong method, fall
  back to the weaker one" from "write a ladder and hope".

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
  kept.
- **`internal/decision`** — the composition root for an authorize call: gather
  identity, target labels, grants, fleet path, and connection metadata; evaluate;
  build the snapshot; write the decision record; decide whether to issue a cache
  hint. Latency budget (M5) is enforced here.
- **`internal/fleet`** — the graph, its liveness, and pathfinding. Owns which
  hop direction is possible right now.
- **`internal/audit`** — append-only writer, chain verifier, and query API.
  Nothing else writes audit rows.
- **`internal/revoke`** — subscriptions and fan-out. Owns event ids and replay.

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
| `POST /v1/logs/batch` | Idempotent bulk ingest into the audit store; `202` |
| `POST /v1/logs/priority` | Single critical record, durable before the ack; `200` |
| `GET /v1/proxies/{id}/events` | Long-lived NDJSON revocation stream with heartbeats, replay, and `resync` |

Four obligations are easy to miss and are graded by the conformance suite:

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

  Phases: 0007 and 0008 respectively; 0016 proves the pair against a real proxy.

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
- **enforcement rung** per axis, where the route stands somewhere other than
  proxy-side enforcement;
- **session deadline** — an absolute instant the proxy enforces locally, so it
  survives this server being unreachable (proxy D16);
- **concurrency caps** per subject and/or target, which only the proxy can
  count;
- **grant context** — the external system, its reference, and the window —
  carried opaquely by the proxy into every log record for the session;
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
| 0006 | Fleet registry, health & config distribution | enrollment, heartbeat, zone graph, pathfinding, hop direction, versioned config rollout (M6) |
| 0007 | South-bound authentication | `/v1/auth/*`, MFA orchestration, host-key reporting |
| 0008 | South-bound authorize & route | `/v1/authorize`: snapshot assembly, cache hints, latency budget (M5) |
| 0009 | Revocation & event fan-out | `/v1/proxies/{id}/events`, event bus, replay, resync, kill switch (M9) |
| 0010 | Audit ingest & tamper-evident store | batch + priority ingest, hash chain, verifier, query (M8) |
| 0011 | Identity, users, groups, roles & RBAC | local identity, roles, RBAC, OIDC/SAML federation, claim mapping, SSH CA (M7) |
| 0012 | Access grants | manual time-boxed grants; `ext.GrantWorkflow` seam for Enterprise (M10) |
| 0013 | External access context | `ext.AccessContextProvider`, push receiver with scope binding, probe path inside the authorize budget, declarative HTTP provider as the default (M16) |
| 0014 | North-bound API, inventory & policy lifecycle | authoring, versioning, validation, **simulation**, **explain**, targets/identities CRUD, GitOps (M2, M4) |
| 0015 | Management console | operator web UI served from the binary: fleet, explain, audit, policy, inventory |
| 0016 | Cross-repo E2E topology, CI gate & hardening | real proxy + real control plane + Postgres + target, scenario suite, `govulncheck` |

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
- Multi-tenant operation (schema is ready, M12; the API and UI are not).
- HA/multi-region deployment. The decision path is designed to scale out (M5)
  and the event bus is behind `ext.ClusterCoordinator` (M9, 0004), but this
  repository runs one node; clustering is Enterprise.
- Endpoint agents and device posture collection. The policy model has a slot for
  posture attributes; nothing here collects them.
- Long-term storage tiering of session recordings.
