# 0014 — North-bound API & policy lifecycle

## Read first
- `docs/PROTOCOL.md` — session workflow, and in **§3** the rule that a debug
  endpoint may not outlive the phase that needed it. **This phase is limb 4 of
  that rule for two debug paths** (below): the removals are obligations here,
  not suggestions.
- `docs/PLAN.md` — especially **§2 (M2, M3, M4, M9, M15, M17)**, §5 (the bundle
  and the explanation, including §5.2's enforcement rungs and session deadline,
  and §5.4's cache-hint invariants), §7 (audit query).
- `docs/learnings/` — read summaries; open `0005` (bundle, compiler errors,
  explanation type), `0006` (the capability query this surface exposes), `0008`
  (decision records), `0010` (audit query layer), `0009` (publishing an operator
  event), `0007` (listener conventions), `0004` (the extension registry this
  surface exposes). Also open `0006` at "The cross-repo dependency": this phase
  closes it (see "Fleet configuration becomes deliverable" below).
- `docs/PLAN.md` §4, **"Configuration distribution: the delivery exists
  upstream (proxy D18)"**. In the **Hoplock Proxy repository**, read `docs/PLAN.md`
  **D18** and `api/README.md` "Fleet configuration". D18 is cited in this
  repository and never restated, and it is the reasoning behind every rule in
  that section.

## Objective
Give humans and CI a surface. This is the phase where the product becomes
operable rather than merely correct: authoring with real validation, **policy
simulation**, **"explain why this was denied"**, audit query, and the operator
actions that were internal until now.

## In scope

### The north-bound listener (`internal/httpapi/north`)
- A **separate listener** with its own authentication: OIDC for humans, scoped
  API tokens for automation (M2). Roles at minimum: read-only auditor, policy
  author, approver, admin. A token's scope is enforced per route.
- Every mutating action is itself audited: who, what, when, and the before/after
  version. An audit system whose own administration is unaudited has a hole
  exactly where it matters.
- Never routable from the south-bound listener — the mirror of 0007's test.

### Policy lifecycle
- **Upload → validate → diff → activate**, with bundles immutable and versioned
  (0003). Activation names the version; rollback is activating an older one.
- **Validation returns the compiler's errors verbatim** (0005): the rule, the
  line, and what to do instead. This is a product surface, so test its text —
  and its `code` and parameters, which 0005 carries beside the text for the
  reason below.
- **Simulation** — the feature that makes a policy change reviewable instead of
  a leap:
  - *dry-run*: evaluate a candidate bundle against synthetic inputs;
  - *replay*: evaluate it against **recorded decision inputs** from a time range
    (0008's records) and report what would change — every decision that flips
    allow→deny or deny→allow, grouped so a human can actually read it.
  - Replay must be pure and total: the engine takes time as an input precisely
    so this works (0005). If it is not total, say why.
- **GitOps**: a bundle can be applied from CI with an API token, and the
  response is machine-readable enough to gate a pull request.

### Can this policy actually be satisfied? (M17)

Validation that compiles is not validation that can be served. 0006 builds the
query; **this phase is where an operator sees the answer, before publishing
rather than after a user complains**. It spans both capability sources: the
proxies that would enforce the policy, and the targets it
would be enforced on.

- **A rung no proxy in the path can provide, or no target can take, is a policy
  that denies at connect time.** Show it at publish time: which rule, which
  route, which proxies or targets fall short, and what they do provide. The
  failure it prevents is specific and silent — a rung the enforcing proxy cannot
  honour is a *skipped rung*, so the ladder just gets shorter with no error
  anywhere, and on a one-rung ladder the session is denied and nobody authored the
  denial.
- **Warn, do not refuse, where the shortfall is capability rather than
  correctness.** A capability record that is stale, undated or absent is not proof
  a target cannot take a rung — it is the absence of proof, and it fails safe by
  providing nothing that must be *applied*. Blocking publication on it would make
  an unprobeable appliance unauthorable, which is the opposite of what attested
  rungs exist for. Internal contradictions are a different matter: those are
  compiler errors (0005) and reach this surface as errors.
- **An allow-list containing an interpreter is not an allow-list, and catching
  that is this server's job.** `find`, `awk`, `less`, `vi`, `tar`, `python` and
  most editors hand back a shell (GTFOBins), so a `restricted_exec` list naming
  one does not deliver the boundary an `account-restricted` or `account-confined`
  rung claims — its real guarantee drops to `no-interactive-shell` at best. The
  contract states this as **a documented rule enforced by Control at authoring
  time**, and deliberately *not* as a proxy-side refusal: a shipped deny-list of
  interpreter names in the data plane would be a blacklist masquerading as a
  boundary, incomplete the day it shipped and liable to refuse a route over a name
  collision.

  So this is a **warning the author must see and may override**, never a silent
  pass and never a hard refusal. Ship a starting list, make it configurable, say
  in the text *why* the named executable weakens the claim, and record that the
  author accepted it — the rung is a claim about a mechanism, bounded by the list
  it renders, and the policy author owns that trade-off. A check that cannot be
  overridden will be worked around; one that is never shown is not a check.

### A cache hint that outlives what gated it (0005)

The compiler refuses a cache key shared across identities and stops there on
purpose. Everything else about a hint is a judgement, and `internal/policy` has
no severity to express one in: a `model.Rejection` refuses, and refusing here
would overrule an author on a call that is legitimately theirs.

The judgement is this. A hint's key is built from a closed set of components —
`subject`, `target`, `target-port`, `proxy`, `auth-method`, `rule` — and a rule
may match on things none of them names: `context.days`, `context.time_of_day`,
`device` posture. A decision gated on one of those is reused for up to
`ttl_seconds` after the condition stopped holding.

Most of that is self-limiting and must **not** be warned about. Every time bound
in a snapshot is an absolute instant, so a replayed decision always grants less
than a fresh one would and never more; and a rule matching a live grant always
carries a deadline, because the grant's expiry bounds it even where the route
authored no duration (0005). The proxy arms that deadline before it provisions or
dials and fires it at once when it has already passed, so a stale grant-gated
decision never reaches the target.

What is left is narrow and real: a rule matching `context.days`,
`context.time_of_day` or `device`, carrying a cache hint, on a route with **no
`max_session_duration`**. No grant supplies a bound and the snapshot carries no
`session_deadline`, so `ttl_seconds` bounds admission and nothing bounds the
session — a connection admitted after the window closed runs until somebody
closes it.

Report it as a **warning the author may override**, on the same terms as the
interpreter warning above and for the same reason: "office-hours access, sessions
unbounded once started" is a policy somebody may genuinely mean. Name the rule,
the matched term that the key does not carry, and the two fixes — add a
`max_session_duration`, or drop the hint.

`PolicyValidator` is `WhenAbsentCore` (0004), so this is Control's own
publish-time check beside the capability and interpreter checks above, sharing
their reporting shape and their override path, rather than a registered default.
A validator an operator registers adds governance rules on top of it and never
replaces it. The severity vocabulary to report it in already exists —
`ext.FindingAdvice`, `ext.FindingWarning`, `ext.FindingBlocking` — and the line it
sits on is the one `ext.PolicyValidator`'s own doc comment draws: the compiler
decides whether a policy is *valid*, this surface decides whether it is
advisable.

### Explain a decision (M4)
Given a `decision_id` or a session id, return the whole story: the inputs, the
matched rule, the mapping version that produced the attributes (0011), the
obligations, the snapshot, and — if a grant was involved — which one and who
approved it.

This is the other half of the proxy's disclosure rule: the user is told
"access denied" and a session id, deliberately vague so the proxy is not an
oracle for probing the estate, and an operator resolves that id here into
everything. Vague to the user, total to the auditor — and the pair only works if
this endpoint is genuinely total. Make an unexplainable decision impossible to
represent, or make it loud.

### Inventory
CRUD for the things an operator manages, all RBAC-gated and all audited:
targets and their labels (labels are policy inputs, so editing one changes
decisions — the audit record matters), identities and groups, roles, grants
(0012), and proxy enrollment/approval (0006). Label edits are the sharpest
edge here: a bulk relabel is a bulk policy change, so it is diffable and
appears in simulation like any other change.

### Audit query & operator actions
- Query over 0010's store, including the showcase join (blocked commands on
  `env=prod`, with the access that permitted them).
- **Retention, and the one rule it must not break.** Deleting a record out of
  the middle of a hash chain leaves its successor pointing at a hash nothing
  produces, which is indistinguishable from tampering — the mechanism cannot
  tell a policy from an attacker and must not try. A retention job therefore
  deletes a contiguous PREFIX of a stream and records the sequence it deleted
  up to and the hash of the last record it removed, so verification resumes
  from there rather than reporting a break (PLAN §7). Session captures are the
  separate and easier case: they live in their own table and the record keeps
  their digest, so deleting captures while keeping records leaves a chain that
  still verifies and a store that can say the bytes are gone rather than
  changed. `hoplock-control audit-verify` (0010) is what has to keep passing
  across a retention pass, and a test should run it after one.
- Operator actions: kill a session, kill everything for a subject, invalidate
  cached decisions (publishing through 0009), withdraw a **host-key** decision
  by the key stored on its record, and enroll/approve a proxy (0006). Each
  requires the right role and each is audited.

  **Publish through `revoke.Operator` rather than beside it** (0009). It already
  validates what the contract requires — exactly one selector, a `session_kill`
  reason that is safe to disclose and never empty — and its `Receipt` reports
  `CoversHostKeyDecisions`. **That field has to reach the operator.** A
  subject-scoped `cache_invalidate` cannot match a host-key decision, because
  the proxy keys one on target, port and fingerprint rather than on a person
  (PLAN §5.4); an operator who publishes "invalidate everything for Alice" and
  is not told that a target's host key was untouched has been misled by this
  server, and a revocation that silently misses is worse than one that refuses.

### Fleet configuration becomes deliverable (proxy D18, `Hoplock/proxy#65`)

0006 built configuration distribution below the wire: versioned documents,
composition, rollback, and drift. It left `fleet.ConfigPublisher` a visible
no-op because the contract had no event that could say "your desired
configuration moved". It raised the need upstream (`Hoplock/control#27`), and
the proxy answered it as its phase 0042, merged as **`Hoplock/proxy#65`**:
contract **`4.2.0`**, `policy_version` **still `4`**, with a new decision,
**proxy D18**. That PR's `## Cross-repo impact` section put five obligations on
this repository, and they are this phase's.

**Why here and not in an earlier phase.** This is the first phase where an
operator can publish a configuration at all. Until the north-bound surface
exists, `Registry.PublishConfig` has no caller. Wiring the publisher earlier
would have needed a publish path with no successor to delete it
(`docs/PROTOCOL.md` §3), and the conformance cases below need a publish hook
that only this surface can supply. So publishing a document (zone or proxy
scope), rolling it back, and reading the fleet's configuration state are
north-bound routes here, gated and audited like every other mutating action
(Inventory, above). The south-bound half is served in the same PR, because a
publish that nothing can fetch has delivered nothing.

1. **Re-vendor the contract at `4.2.0`.** Run
   `make contract-sync REF=48fed4c09f7eaf6810a31a11ee14806b13c57674` (the
   merge of `Hoplock/proxy#65`), or a later upstream `main`. If you use a later
   `main`, every contract change between the two is also this phase's to read
   and state. Never hand-edit `contract/` (M1). `policy_version` does not move.
   The document gains an event type and two endpoints, so every check keyed on
   the vocabulary stays green, and the checksum in `contract/UPSTREAM` is what
   moves. The re-vendor also moves the proxy commit the `conform` CI job builds
   `cmd/mock-control` from. That is what gives the mock the fetch, the report,
   and `POST /debug/config`, and the conformance cases below depend on it.
2. **Wire `fleet.ConfigPublisher` to emit `config_changed` {`version`,
   `hash`}.** Emit it on the stream 0009 built (`internal/revoke`), one event
   per affected proxy, naming that proxy's **composed** document. A zone
   publish that re-materialises twelve proxies is twelve notifications, and a
   no-op publish that bumps no proxy's version (0006) emits none. The event
   **names the document and never carries it**, because the stream is
   replayable and a replayed document is a stale one applied as if current
   (D18). It is retained for replay like any other event, which is safe because
   the proxy re-fetches the current document. On the wire `version` is an
   **opaque string** and `hash` is an opaque content identifier the proxy only
   compares for equality (the contract's example is `sha256:<hex>`). Render this
   server's `int64` version and bare hex hash into those strings in **one**
   place, and use that one place for the event, the fetch, the `ETag`, and
   parsing the report back. Then the noop's default goes: the Registry is
   constructed with the real publisher. Rewrite the doc comments on
   `ConfigPublisher`, `noopConfigPublisher`, `WithConfigPublisher` and the
   package comment in `internal/fleet/config.go`. They say the seam "cannot be
   connected to the wire yet", and after this phase that is false.

   A publisher failure must **not** roll back the publish. The desired version
   is durable and drift is visible, so the proxy catches up on its next stream
   (re)connect, when it re-fetches anyway (0006's reasoning for the no-op still
   holds for a transient failure). Surface the failure to the operator on the
   response rather than swallowing it.
3. **Serve `GET /v1/proxies/{proxy_id}/config`** on the south-bound listener
   (0007), authenticated and tenant-resolved exactly as 0009 does
   `/v1/proxies/{proxy_id}/events`:
   - `200` with the `ProxyConfigDocument` (`version`, `hash`, `settings`) and
     `ETag` equal to the `hash` as an entity tag (`"sha256:…"`);
   - `304` with no body when `If-None-Match` names the hash still desired.
     Compare it as an entity tag, so quoting is not a mismatch. This is what
     makes the proxy's fetch on every (re)connect and after `resync` cheap;
   - `204` when nothing is published for this proxy, in which case the proxy
     runs on its bootstrap file alone;
   - `404` with code `not_enrolled` when the registry holds no proxy by this id
     **in the caller's tenant**. This is a registry fact, not a deny, so it is
     never `401` (M11). A `401` would also be indistinguishable to the proxy
     from a rejected token, which is the one case where its subscription stops
     retrying.
4. **Record `POST /v1/proxies/{proxy_id}/config/report`, and drive drift and
   `last_error` from it.** Answer `{"accepted": true}` once the report is
   durably recorded, and never before. Store what the contract carries:
   `running_*`, `desired_*`, `state` (`applied`, `pending_restart`, `rejected`,
   `fetch_failed`), `restart_required`, `last_error`, and `reported_at`. That is
   a migration (0003's forward-only rules), because 0006's
   `proxy_config_state` holds only the running version and hash. Drift is then
   derived from the report, not from `DesiredVersion != RunningVersion`
   alone. A proxy reporting `pending_restart` has not finished the rollout even
   though it holds the new document. One reporting `rejected` needs an
   operator, and the fleet view shows the `last_error` (keys, never values).
   One reporting `fetch_failed` is retrying. Absent `running_*` means "on its
   bootstrap file alone", and it is not version zero reported as a number. A
   `running_version` that does not parse as one of this server's versions is
   drift, and it is reported as drift, not as a `400`, because the proxy is
   saying truthfully what it runs. The report is the only proxy→server call
   that carries this (D18). 0006's `Heartbeat.RunningConfigVersion` has no wire
   caller. Decide whether it goes or stays as a non-contract input, and say
   which in your learnings. Two sources for one fact is how the fleet view
   starts disagreeing with itself.
5. **Publish only D18's fleet-owned keys.** A setting is fleet-owned only by
   being listed, and the list is `ProxyConfigDocument.settings` in the
   vendored `contract/control.yaml`. Everything else is bootstrap-only: what
   the proxy needs to reach or be recognised by Hoplock Control, any path to
   host material, any listener, and `control.cache.stale_after`. The proxy
   rejects a document naming any other key **whole**. So a publish, **at either
   scope**, that names one is **refused at publish time** with an M21 code
   naming the key. Staging it would produce a rollout that can only ever
   report `rejected`. Keys are flat and dotted, which makes 0006's top-level
   key replacement exactly per-setting replacement. A nested object is not a
   fleet-owned key and is refused on the same rule. Check each value's shape
   against the contract's statement (durations as Go duration strings, counts
   as integers, addresses as strings). The proxy's own validation remains the
   authority, and a document it rejects anyway arrives as `rejected` on the
   report, which is item 4's job to show.

   Hold the list in **one** place, and add a test that ties it to the vendored
   document. Today the list is prose in that schema's `description`, not an
   enum, so the test reads the backticked keys out of the description. The
   next `make contract-sync` that adds or removes a fleet-owned key must fail
   that test rather than slip past it. Whether a key applies live or at restart
   is the proxy's business, and this server stores neither.
6. **Grade it in `cmd/pdpconform`** against both this server and the proxy's
   mock, in the layers 0002 and 0009 established. Contract-level cases:
   - the fetch with no document published answers `204`;
   - after a publish, the stream carries `config_changed` whose `version` and
     `hash` equal what the fetch then serves;
   - the fetch answers `200` with `ETag` equal to the body's `hash`;
   - the same fetch with that `ETag` as `If-None-Match` answers `304` with no
     body;
   - an id the server has not enrolled answers `404` with the code
     `not_enrolled`, and never `401`;
   - the report answers `200 {"accepted": true}`.

   Publishing is not on the contract, so the suite takes it as an input the
   way it takes `events.publish_url`, `publish_body` and `publish_token`.
   Suggested keys are `config.publish_url`, `config.publish_body` and
   `config.publish_token`. They point at the mock's `POST /debug/config` in
   `mock-expectations.yaml` and at this phase's north-bound publish route in
   `control-expectations.yaml`. Grade what the server answers, never the
   literals: `version` and `hash` are opaque, and the mock's are not ours.
   `mock-fixtures.yaml` gains the mock's `fleet_config` block (`enrolled` so
   that `404` is reachable, and no `document`, so the run starts on `204`).
   Update `cmd/pdpconform/README.md`'s key table to match. This server adds
   **no** `/debug/` route for any of it. The acceptance criterion below that no
   Go file binds one covers this too.

### Delete the two debug paths this phase supersedes

`docs/PROTOCOL.md` §3 lets a phase add a debug endpoint only when a named
production API will supersede it and **that phase's prompt carries the
removal**. This is that phase, for both of them. Neither removal is optional and
neither is a follow-up: the production route and the deletion land in the same
PR, because a supersession that leaves the old path bound has superseded
nothing.

**The revocation publish path (0009).** Once the north-bound surface publishes
operator events, delete:

- `cmd/hoplock-control/publish.go` and `cmd/hoplock-control/publish_test.go`;
- `EventsConfig.PublishListener` and `EventsConfig.PublishToken` in
  `internal/config/config.go`, their validation in `EventsConfig.validate`, and
  their block in `config.example.yaml`;
- `startPublishListener` and its shutdown handling in
  `cmd/hoplock-control/serve.go`;
- the `events:` block in the `conform-self` job's `ci-config.yaml`
  (`.github/workflows/ci.yml`).

Then repoint `events.publish_url` and `events.publish_token` in
`cmd/pdpconform/testdata/control-expectations.yaml` at the north-bound route and
a scoped API token, and seed that token the way `control-seed.yaml` seeds the
south-bound one. The suite asserts nothing about the shape of that path — only
that posting to it makes an event happen — so no suite code changes.

**The log read path (0010).** Once the north-bound surface serves the audit
query, delete:

- `cmd/hoplock-control/auditread.go` and `cmd/hoplock-control/auditread_test.go`;
- `AuditConfig.ReadListener` and `AuditConfig.ReadToken` in
  `internal/config/config.go`, the two branches of `AuditConfig.validate` that
  check them (the bounds beside them — `max_batch_records`, `max_record_bytes`,
  `max_capture_bytes` — STAY: they are ingest limits and nothing supersedes
  them), and the `read_listener`/`read_token` half of the `audit:` block in
  `config.example.yaml`;
- `startAuditReadListener` and its shutdown handling in
  `cmd/hoplock-control/serve.go`, including the `auditReader` arm of the
  listener error channel;
- the `audit:` block in the `conform-self` job's `ci-config.yaml`
  (`.github/workflows/ci.yml`) — the two read keys only.

Then repoint `logs.read_url` and `logs.read_token` in
`cmd/pdpconform/testdata/control-expectations.yaml` at the north-bound record
route and a scoped API token, and seed that token the way `control-seed.yaml`
seeds the south-bound one. **Keep the `{record_id}` placeholder**: the suite
substitutes the id it just ingested, which is what makes the durability
assertion exact rather than a listing that happens to mention the id
(`cmd/pdpconform/checks_logs.go`). No suite code changes.

The north-bound replacement must serve a record BY ID, because that is what the
suite's substitution asks for. `audit.Reader.Get` is the call; the rendering in
`auditread.go` (`auditRecordView`) is a starting point rather than a
requirement, and it carries the chain fields deliberately — a reader who can
see the position and the hash can check a record against a chain they already
hold without asking this server to vouch for it.

Note the asymmetry deliberately: `hoplock-control seed` is **not** on this list.
It is a command rather than a bound endpoint — nothing serves it, so it cannot
be reached — and 0007 already records that it becomes a thin client of this API
or goes away. Decide which, and say which in your learnings.

### What is extending this deployment (M15)
Expose the sealed extension registry read-only: one entry per `ext` point, with
the providers registered against it and — for a point where nothing is — what
Control does instead. `ext.Extensions.Status()` already produces exactly that,
including the empty rows, so this is a rendering rather than a computation.

It is not a nicety. An operator debugging why a grant needed an approval, or why
audit records are reaching a SIEM, has to be able to see that an extension is in
play; an invisible extension is indistinguishable from a bug in Control. The
start-up log already prints the same listing (0004), and this is the copy
somebody can reach without shell access to the host.

Read-only, and no privilege to change it: registration happens before the
server starts and is immutable afterwards (0004), so there is nothing here to
mutate and an endpoint that appeared to offer it would be lying.

### `cmd/policyctl`
The same operations from a terminal: `validate`, `diff`, `simulate`, `apply`,
`explain`. It talks to the north-bound API — never to the database directly, or
it becomes a second implementation of the rules.

### Tenancy on the surface (M18)
Every north-bound route resolves exactly one tenant from the caller's scope
(0011) before it does anything else. Three rules keep that honest:

- **A tenant is a selector, never a widening.** The middleware refuses a tenant
  outside the principal's set before a handler runs.
- **Single-tenant deployments look untouched.** With one tenant, no route gains
  a required parameter and no response gains a field an operator must care
  about. Tenancy that is visible to someone who does not use it is a tax on the
  common case.
- **Cross-tenant reads do not exist on this surface.** A caller with scope in
  three tenants makes three requests. Aggregating them is a governance feature
  and it is Enterprise's (its E11); an aggregate route here would be the one
  place where a missing filter leaks everything at once.

### Errors are machine-readable (M21)
Every error this surface returns carries a **stable `code`**, typed
**parameters**, an English **message**, and the correlation id M11 already
requires. The console (0016) is the only layer that localises, and it can only
do that if the sentence is assembled there — so an API that answers with prose
alone makes a localisable console impossible, and that is decided here, two
phases earlier, not discovered there.

- A code is an identifier, not a summary: reword a message whenever it helps,
  never reuse a code for a different condition. M19 makes this surface a
  compatibility promise, and a code is the part of an error a client across a
  version skew can actually depend on.
- Parameters are typed and named (the target, the rule, the tenant, the limit),
  so a client can build a sentence with them in its own word order.
- The English message stays in the response. `policyctl`, CI logs and `curl` are
  first-class consumers, and none of them has a catalogue.
- This is **not** localisation on the server: there is no `Accept-Language`
  handling here and no translated response, now or later (M21).

## Out of scope
- The management console (0016). It is a **client** of this API and lives in
  this repository under `ui/` (PLAN §3) — not a separate project, and not a
  privileged path of its own. Every capability it has, this surface grants it,
  which is why it cannot be built before this phase exists.
- JIT requests and approvals (0012), though `explain` must be ready to name a
  grant.
- SIEM export (0014).

## Acceptance criteria
- Role enforcement is tested per route, including an auditor token being refused
  a policy activation.
- A north-bound route is not reachable on the south-bound listener, and vice
  versa.
- Upload → validate (with a deliberately broken bundle, asserting the error text
  names the rule and the line) → activate → rollback, end to end.
- Simulation replay over seeded decision records reports the exact set of flipped
  decisions for a candidate bundle — assert the set, not just the count.
- **Satisfiability, both directions.** A bundle naming an enforcement rung no
  enrolled proxy provides is reported at publish time, naming the rule and the
  proxies; a bundle naming a rung the target's capability record says it cannot
  take is reported the same way; and a bundle whose targets have **stale, undated
  or absent** records publishes with a warning rather than a refusal. Assert the
  last case explicitly — refusing it is the plausible-looking bug that makes every
  unprobeable appliance unauthorable.
- **The interpreter warning fires and is overridable.** A `restricted_exec`
  allow-list containing an interpreter, under an `account-restricted` or
  `account-confined` rung, produces a warning naming the executable and the rung's
  real guarantee; publication succeeds when the author accepts it, and the
  acceptance is audited like any other mutating action.
- **The cache-hint warning fires only where it should.** A rule matching
  `context.time_of_day` (or `context.days`, or `device`) that carries a cache
  hint and authors no `max_session_duration` produces an overridable warning
  naming the rule and the unkeyed term. The same rule *with* a
  `max_session_duration`, and a grant-gated rule with a hint and no duration,
  produce none — assert both negatives, because a warning that fires on every
  cached rule is one authors learn to click through.
- `explain` returns a complete story for an allow, for a deny, and for a
  decision made under a mapping version that has since changed.
- Every mutating action appears in the audit store with the actor.
- `policyctl` covers each operation and its output is stable enough to script.
- **The extension listing covers every point, including the empty ones.** With a
  fake extension registered at one point, the listing names its provider; with
  nothing registered at another, the listing still carries that point and says
  what Control does instead. A test asserts the listing has a row per
  `ext.Points()` entry, so a seam added later cannot become invisible by being
  forgotten here.
- **Every error response carries a code, parameters, an English message and the
  correlation id** (M21), asserted across the error paths this phase produces —
  validation, RBAC refusal, satisfiability, not-found and outage. A test
  enumerates the codes so that adding one is deliberate and reusing one for a
  different condition fails.
- Every route is exercised by a table test asserting tenant isolation, and the
  test enumerates routes from the router rather than from a hand-written list —
  a route added later without isolation must fail this test.
- With a single tenant configured, the API's shape and responses are identical
  to those a pre-M18 client expects.
- **Both debug paths are gone, and a test says so.** No Go file under `cmd/` or
  `internal/` binds a `/debug/` route; the config keys named above no longer
  parse (a document setting `events.publish_listener` is refused as an unknown
  key, which the strict loader already gives you); and the conformance suite
  reaches `events.publish_url` and `logs.read_url` at north-bound routes under a
  scoped token. A run of the suite that still passes against the old paths has
  proven nothing about the new ones.

  Three things that look like exceptions and are not.
  `cmd/pdpconform/testdata/mock-expectations.yaml` names `/debug/revoke` and
  `/debug/logs` on **the proxy repo's** `cmd/mock-control`: those are the
  mock's own hooks, they stay, and they are not this repository's to delete.
  `hoplock-control seed` is a command rather than a bound route, so nothing
  serves it — see above. And `hoplock-control audit-verify` (0010) is also a
  command rather than a bound route: it is the verifier an operator runs and a
  customer is handed, it is not a debug path, and it stays.
- **A publication that cannot reach a host-key decision says so on the
  response.** Invalidating by subject reports that host-key decisions were not
  covered; invalidating by key, or a resync, reports that they were. Assert
  both, because the failure is an operator believing a withdrawal covered
  something it could not touch.

- **Fleet configuration is delivered, end to end in-process** (proxy D18). With
  the contract re-vendored at `4.2.0` and `policy_version` unchanged at `4`, a
  zone publish emits exactly one `config_changed` per re-materialised proxy,
  naming its composed document. A no-op publish emits none. The fetch answers
  `200`+`ETag`, `304` on the held hash, `204` with nothing published, and
  `404 not_enrolled` for an unknown id or an id in another tenant. Assert that
  last case explicitly, because it is the tenancy leak on this route. A report
  of each `state` shows in the fleet view as that state, with `last_error` for
  `rejected` and `fetch_failed`, and a `pending_restart` proxy is **not**
  counted as caught up. A publish naming a bootstrap-only key, a key the
  contract does not list, or a nested object is refused at publish time naming
  the key. The test tying the fleet-owned list to `contract/control.yaml` fails
  when the two disagree: prove it, don't assume it. `make conform` passes with
  the configuration cases against this server **and** against the proxy's mock.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0014-northbound-api-and-policy-lifecycle-learnings.md`. Summary
block MUST give the route table with required roles, the bundle lifecycle states,
the simulation API and its purity requirements, the `explain` response shape, and
the `policyctl` command set. It must also give the fleet-configuration delivery:
the contract commit re-vendored, how `int64` versions and hashes render onto the
wire, where the fleet-owned key list lives and what ties it to the contract, the
report's storage and how drift is derived from it, the fate of
`Heartbeat.RunningConfigVersion`, and the conformance keys added. Phase 0012 adds routes to this surface and phase
0017 drives it end to end.
