# SecureCommandProxy Management — Implementation Plan

> Status: living document. This is the single source of architectural truth for
> the **management server**. Every implementation session MUST read this file and
> `docs/PROTOCOL.md` before starting work. Keep it current: if a prompt changes
> the architecture, the same PR updates this plan.
>
> The **bastion** has its own repository and its own `docs/PLAN.md`, and it owns
> the contract between the two (M1). Where this plan cites a `D`-numbered
> decision (D1–D12), that decision lives in the bastion's plan and is quoted
> here, not owned here.

---

## 1. Product summary

The management server is the **Policy Decision Point (PDP)** for a fleet of
SecureCommandProxy bastions. The bastion is deliberately thin: it terminates
SSH, enforces a decision, and reports what happened. Everything that makes the
decision — who someone is, what they may reach, over which route, with which
channels, requests, destinations and commands, for how long, and who approved it
— lives here. So does everything that happens afterwards: the audit record, the
replayable session, and the answer to "why was I denied?".

Two things follow from that, and they shape the whole repository.

**This is where the product is.** A bastion without this server is an SSH proxy
that cannot decide anything. Policy authoring, simulation, identity federation,
just-in-time access and approvals, the tamper-evident audit store, and the SIEM
export are all north-bound features, and they are what a buyer actually
evaluates. The bastion's job is to enforce faithfully and to fail legibly.

**It has two surfaces, and they are not the same product.** South-bound is a
machine API serving bastions under hard latency constraints (M5); north-bound is
a human and CI API for authoring, approving, investigating and exporting. They
share a policy model and a database and nothing else — different listeners,
different authentication, different threat models (M2).

```
   humans, CI, GitOps                      bastion fleet
   (OIDC / API tokens)                     (bearer -> mTLS)
            |                                     |
     north-bound API                       south-bound API
            |                                     |
            +--------------+   +------------------+
                           |   |
                     policy model + decision engine
                     fleet graph | identity | grants
                           |   |
                        storage (Postgres)
                           |
                     audit store (append-only, hash-chained)
                           |
                     SIEM export (downstream consumer)
```

### What one session looks like from this side

1. A bastion authenticates a user's certificate or password (+ MFA) against
   `/v1/auth/*`. This server owns the MFA conversation; the bastion only relays
   and polls.
2. The bastion calls `/v1/authorize` with the identity, the requested target,
   and connection metadata.
3. This server evaluates policy over: the identity's claims and groups, the
   target's labels, any live JIT grant, the time, the source network, and the
   fleet graph. It answers `401` or a **whole-connection policy snapshot** —
   route, channel/request/destination policy, filter policy, target credential
   method, hop direction, and optionally a cache hint.
4. It writes a **decision record** explaining exactly how it got there (M4).
5. The bastion enforces locally for the connection's lifetime and streams logs
   back to `/v1/logs/*`. Nothing on the data path asks this server anything.
6. If access is withdrawn mid-session, this server pushes a `session_kill` down
   the revocation stream the bastion is already holding open (M9).

---

## 2. Key decisions

Each decision has an ID so prompts and learnings can reference it. `M` for
management; the bastion's decisions keep their `D` numbers.

- **M1 — The contract is owned upstream; this repo vendors it read-only.** The
  PEP↔PDP contract is `api/management.yaml` in the bastion repository,
  `github.com/mauroasilva/SecureCommandProxy`. This
  repo keeps a pinned copy under `contract/`, together with the upstream commit
  it came from, and treats it as generated: nobody edits it here. A change
  starts in the bastion repo, lands there, and is pulled in with
  `make contract-sync`, which is also a CI check — a silently edited local copy
  is how two components stop agreeing while both test green.

  Conformance is proven, not asserted: `cmd/pdpconform` is a **black-box
  conformance suite** that drives any implementation of the contract over HTTP
  and is run in CI against both this server and the bastion repo's
  `cmd/mock-management`. Running it against the mock is what keeps the suite
  honest — a suite only ever run against the implementation it was written
  beside tests agreement with itself.
- **M2 — Two surfaces, two listeners, two authentication models.** South-bound
  (bastions) and north-bound (humans, CI, GitOps) never share a port, a
  middleware chain, or a credential type. A bastion token that can reach a
  policy-authoring endpoint is privilege escalation from "can ask about
  decisions" to "can author them", and the cheapest way to guarantee it cannot
  happen is for the two never to be routable from the same listener. South-bound
  is a bearer token in the prototype with mTLS as the intended production form
  (the bastion's contract already treats this as a thin seam); north-bound is
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
  bastion's disclosure rule (bastion PLAN §4.3): the user is told "access
  denied" and a session id — deliberately vague, because a precise denial makes
  the bastion an oracle for probing the estate — and the operator resolves that
  id here into the whole story. Vague to the user, total to the auditor; that
  pair only works if this side is genuinely total.

  The same records back policy simulation ("what would this bundle have done to
  last week's traffic?"), which is what makes a policy change reviewable rather
  than a leap.
- **M5 — The decision path is stateless, bounded, and cheap.** A bastion holds a
  user's SSH handshake open while this server answers, on every connection, per
  hop. So: no unbounded work in an authorize call, compiled policy served from
  memory, database reads on the decision path limited to what is indexed and
  small, and a hard server-side timeout that answers rather than hangs — a
  timeout the bastion classifies as an outage is strictly better than a slow
  answer that looks like one. Horizontal scale-out is the only scaling story;
  nothing on the decision path may be node-local state.
- **M6 — The fleet is a graph, not a list.** Bastions enroll, heartbeat, and
  declare their zone, their reachability, and which connection directions they
  support (bastion D11: `dial` or an outbound-registered `relay`). A route is a
  **path computed over that graph** from the user's entry bastion to the target's
  zone, and the hop metadata the bastion receives is one step of it.

  This is what makes multi-hop invisible: the user types one hostname, and the
  fact that reaching it crosses an edge bastion, a regional gateway, and an
  enclave relay is this server's problem. It is also the only place that can
  choose `relay` correctly, because only this server knows which downstream
  bastions currently have a live outbound registration — which is exactly why
  direction is a routing decision and not a bastion config flag.
- **M7 — Identity is federated and short-lived.** OIDC and SAML are brokered
  here; the bastion never talks to an IdP (bastion D4 makes it identity-shaped
  precisely so that this can be added without touching it). IdP claims and
  groups map into policy attributes through an explicit, versioned mapping —
  never by trusting a raw claim name straight from a token. Local credentials
  exist for development and for break-glass only, are flagged as such in the
  audit record, and are never the production path.
- **M8 — Audit is append-only and tamper-evident.** Records are immutable, hash
  chained per stream so that a removed or altered record breaks verification, and
  ingest is idempotent on the client-assigned `record_id` the contract already
  defines (a bastion draining its disk buffer after an outage will resend). The
  audit store is the system of record; the SIEM export is a downstream consumer
  and is never read back as truth. Retention is a policy, applied by a documented
  job, that records what it deleted.
- **M9 — Revocation is fan-out with replay, and it is the kill switch.** The
  bastion holds one long-lived outbound NDJSON subscription (bastion §6.4). This
  server must fan an operator action out to every bastion that needs it, survive
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
- **M11 — `401` means deny; everything else means outage.** The contract's
  ground rule binds this side hardest. A database timeout, a compile error, or a
  panic must never surface as `401`, because the bastion will faithfully tell a
  user "access denied" and the operator will spend the outage debugging
  permissions. Deny is a decision this server made on purpose; every other
  failure is a `5xx` with a message safe to disclose and a correlation id.
- **M12 — Tenancy is in the schema from day one.** The prototype is
  single-tenant and nothing in the API exposes tenancy. Every table still
  carries the tenant column and every query still filters on it. It is nearly
  free now, and retrofitting it into a populated audit store later is a
  migration nobody wants to run.
- **M13 — Tech choices.** Go (same floor policy as the bastion: the `go`
  directive is the minimum, CI tracks the latest stable, and the floor moves only
  when a dependency moves it). Postgres via `pgx`, with forward-only versioned
  migrations. YAML config. JSON over HTTPS. Structured logging. No ORM: the
  decision path's queries are few, hot, and worth reading.
- **M14 — Proprietary/closed source.** Private repo, all rights reserved, per-file
  SPDX + copyright headers, same as the bastion (`docs/LICENSE-HEADER.md`).

---

## 3. Architecture & repository layout

```
commandproxymanagemente/
├── cmd/
│   ├── management/         # the server daemon (both listeners)
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
│   ├── fleet/              # bastion enrollment, heartbeat, zone graph, pathfinding (M6)
│   ├── identity/           # IdP brokers (OIDC/SAML), claim mapping, MFA orchestration
│   ├── credential/         # target-credential brokerage + SSH CA (bastion D6a)
│   ├── decision/           # authorize: assemble inputs, evaluate, snapshot, record
│   ├── revoke/             # event bus, subscriptions, replay buffer (M9)
│   ├── audit/              # ingest, hash chain, query, retention (M8)
│   ├── export/             # SIEM sinks (Splunk/Sentinel/Elastic)
│   ├── access/             # JIT requests, approvals, grants, notifiers (M10)
│   └── httpapi/
│       ├── south/          # bastion-facing handlers (the contract)
│       └── north/          # admin/operator/CI handlers
├── contract/               # VENDORED from the bastion repo — read-only (M1)
├── deploy/                 # docker-compose: this server + Postgres + a bastion
├── docs/                   # this plan, protocol, learnings
├── prompts/                # queued and implemented phase prompts
└── migrations/             # versioned SQL, forward-only
```

### Component responsibilities

- **`internal/contract`** — the only package that knows the wire shapes of the
  south-bound API. Everything else speaks domain types, so a contract revision
  in the bastion repo lands in one package here.
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

The contract is upstream (M1). This server implements every endpoint the bastion
calls, and the conformance suite is the definition of "implements":

| Path | What this server must do |
| --- | --- |
| `POST /v1/auth/cert` | Resolve an offered key/certificate to an identity with claims, or deny |
| `POST /v1/auth/password` | Verify, then own the MFA conversation: return `authenticated` or `mfa_required` + a challenge |
| `POST /v1/auth/mfa/poll` | Resolve an outstanding challenge; deny on expiry or unknown token |
| `POST /v1/authorize` | Evaluate policy; return `401` or the whole-connection snapshot + `decision_id` (+ optional cache hint) |
| `POST /v1/hostkeys/report` | Record a reported target host key and answer with the trust decision |
| `POST /v1/logs/batch` | Idempotent bulk ingest into the audit store; `202` |
| `POST /v1/logs/priority` | Single critical record, durable before the ack; `200` |
| `GET /v1/bastions/{id}/events` | Long-lived NDJSON revocation stream with heartbeats, replay, and `resync` |

Two obligations are easy to miss and are graded by the conformance suite:

- **The priority ack means durable.** The bastion acts on a critical security
  event knowing this server recorded it. Acking before the write lands turns
  that guarantee into a lie that only shows up after an incident.
- **Heartbeats are liveness, and their absence is a signal.** A bastion that
  stops hearing them reconnects and, past its staleness threshold, stops serving
  cached decisions entirely. A server that stalls its heartbeat writer degrades
  the whole fleet to uncached — correctly, but for the wrong reason.

---

## 5. Policy model & evaluation (M3, M4)

### 5.1 Inputs

| Axis | Examples |
| --- | --- |
| Subject | subject id, IdP source, groups, claims, authentication method, MFA |
| Device | posture attributes when an endpoint supplies them (optional) |
| Context | time of day, day of week, source network/geo, entry bastion |
| Target | hostname, labels (`env=prod`, `kind=appliance`, `owner=payments`), zone |
| Session | requested channel type, in-channel request, forwarding destination, global request, command |
| Grants | live JIT grants for this subject and scope (M10) |

### 5.2 Outputs

A **whole-connection snapshot**, because the bastion asks once and enforces for
the connection's lifetime (bastion D2):

- route: `direct` or `nexthop`, plus the next step and hop metadata including
  **connection direction** (bastion D11, computed from the fleet graph, M6);
- **channel types** permitted, in both directions;
- **in-channel requests** permitted, subsystems named individually;
- **forwarding destinations** permitted (host/CIDR + port);
- **global requests** permitted;
- **filter policy**: either an ordered rule list (guardrail) or a restricted-exec
  allow-list (boundary) — never both (bastion D12);
- **target credential method** and its parameters (bastion D6a);
- **cache hint**, issued deliberately (5.4);
- **obligations**: record the session, require approval, require step-up auth.

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

### 5.4 Cache hints are a policy decision, not an optimisation

The bastion may reuse an authorize decision only when this server attaches a
hint (bastion §6.4), and the lifetime is this server's to set. So the hint is
part of policy, authored per rule: omit it for anything sensitive and every
connection is re-decided. Two invariants this server must never violate:

- **A key is never shared across identities.** The key selects the sharing
  scope; one shared across subjects serves one user another user's policy.
- **Never issue a hint the revocation stream cannot withdraw** (M9). If the
  event path for a bastion is unhealthy, stop issuing hints to it — a cached
  allow with no way to revoke it is just a slower revocation.

---

## 6. Identity, MFA, and credentials

- **Federation (M7).** OIDC and SAML brokers behind one interface. An explicit
  mapping turns IdP claims and groups into policy attributes; the mapping is
  versioned, validated, and visible in the decision record, because "why did
  Alice match the `sre` rule" is answered by the mapping as often as by the rule.
- **MFA orchestration.** The contract makes MFA entirely this server's concern:
  the bastion relays and polls. That means owning challenge lifetime, poll
  intervals, replay resistance, and the deny-on-expiry path. Determinism matters
  for tests — the bastion's mock models it with a "pending polls" counter, and
  the conformance suite depends on that behaviour being reproducible here.
- **Credential brokerage (bastion D6a).** The route names the target credential
  method. Beyond selecting it, this server is the natural home for the
  credentials themselves: an SSH CA issuing short-lived, narrowly-scoped target
  certificates per session beats a long-lived management certificate sitting on
  every bastion's disk. That is an **additive** contract change (the bastion's
  `target_auth` object is extensible on purpose) and therefore starts in the
  bastion repo, not here — this plan records the intent and phase 0010 builds
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
  because "the bastion promises not to send it" is not a control on this side.

---

## 8. Cross-cutting conventions

- **Module path**: `github.com/mauroasilva/commandproxymanagemente` — it tracks
  the repository URL, not the product name, because a Go module path that does
  not resolve to its repository cannot be fetched by path. The component is still
  called the management server everywhere prose refers to it.
- **Go**: the `go` directive is the floor; CI builds on both the floor and the
  latest stable, with `GOTOOLCHAIN: local` so the floor is enforced rather than
  asserted. Same reasoning as the bastion's PLAN §8.
- **Config**: YAML, documented in `config.example.yaml`, strict decoding
  (unknown keys are an error).
- **License (M14)**: proprietary `LICENSE` plus the per-file header in
  `docs/LICENSE-HEADER.md`.
- **Errors/logging**: no secrets, no credentials, no tokens in errors or logs.
  Every response carries a correlation id; every `5xx` says outage, never deny
  (M11).
- **Migrations**: forward-only, versioned, applied by an explicit command — never
  automatically on boot in production, where two nodes starting at once must not
  race.
- **Testing**: unit tests per package; the policy engine tested exhaustively and
  in isolation; Postgres-backed tests against a real database in CI; the
  conformance suite (M1) run against this server **and** the bastion's mock.
- **CI**: build, vet, test, lint, migrations check, contract-drift check,
  conformance, `govulncheck`.

---

## 9. Test topology

The prototype's full topology runs in one CI job with `docker compose`:

| Node | Container |
| --- | --- |
| Postgres | stock image |
| Management server | `cmd/management` |
| Bastion | the bastion repo's image, pointed at this server |
| Target | `sshd` image with the provisioning account and an appliance-like account |
| Client | thin image running scenario SSH clients |

The point of this topology is the thing neither repo can test alone: a **real
bastion** driven by a **real PDP**. The bastion repo proves it enforces what a
mock tells it; this repo proves it decides correctly in isolation; only here do
"decides" and "enforces" meet.

---

## 10. Phased delivery

One prompt = one PR = one phase (see `prompts/queued/`).

| # | Phase | Delivers |
| --- | --- | --- |
| 0001 | Project scaffold & conventions | module, layout, license + headers, Makefile, CI skeleton, config loader |
| 0002 | Contract vendoring & conformance harness | `contract/`, drift check, `cmd/pdpconform` proven against the bastion's mock |
| 0003 | Storage layer & migrations | Postgres repositories, forward-only migrations, tenancy columns (M12) |
| 0004 | Policy model & decision engine | bundle parse/validate/compile/evaluate + decision records (M3, M4) |
| 0005 | Fleet registry & zone graph | enrollment, heartbeat, reachability, pathfinding, hop direction (M6) |
| 0006 | South-bound authentication | `/v1/auth/*`, MFA orchestration, host-key reporting |
| 0007 | South-bound authorize & route | `/v1/authorize`: snapshot assembly, cache hints, latency budget (M5) |
| 0008 | Revocation & event fan-out | `/v1/bastions/{id}/events`, event bus, replay, resync, kill switch (M9) |
| 0009 | Log ingest & tamper-evident audit store | batch + priority ingest, hash chain, verifier, query (M8) |
| 0010 | Identity federation & credential brokerage | OIDC/SAML brokers, claim mapping, SSH CA for target credentials (M7) |
| 0011 | North-bound API & policy lifecycle | authoring, versioning, validation, **simulation**, **explain a decision**, GitOps (M2, M4) |
| 0012 | JIT access requests & approvals | grants, request/approve flow, notifiers, expiry (M10) |
| 0013 | SIEM export & retention | Splunk/Sentinel/Elastic sinks, backpressure, retention jobs |
| 0014 | Cross-repo E2E topology, CI gate & hardening | real bastion + real PDP + Postgres + target, scenario suite, `govulncheck` |

Ordering rationale worth keeping: the conformance harness (0002) comes second so
that every later phase has a red/green target it did not write itself; the
policy engine (0004) precedes every endpoint that uses it and is pure, so it can
be made correct before HTTP exists; the fleet graph (0005) precedes authorize
(0007) because a route is a path over it; and the north-bound surface (0011)
comes after the south-bound one is real, because simulation and explanation need
decision records to have been produced by something.

Prompts may add or re-order later phases; any prompt that introduces new queued
prompts MUST preserve the numbering invariants in `docs/PROTOCOL.md`.

---

## 11. Out of scope for the prototype

- Editing the contract here (M1 — it is upstream, always).
- Multi-tenant operation (schema is ready, M12; the API and UI are not).
- A web UI. The north-bound API and `policyctl` are the surfaces; a UI is a
  consumer of them and a separate project.
- HA/multi-region deployment of this server. The decision path is designed to
  scale out (M5) and the event bus is behind an interface (M9), but the
  prototype runs one node.
- Endpoint agents and device posture collection. The policy model has a slot for
  posture attributes; nothing here collects them.
- Long-term storage tiering of session recordings.
