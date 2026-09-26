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
- `docs/PLAN.md` **§7** from "The enforcement rung is an audit fact" to the end
  of that list, and §5.2's `algorithm_profile` bullet, for what
  `Hoplock/proxy#66` changed on the records this store ingests. Also open
  `0010`'s learnings at "The rung in force, and the two attribute names" and
  "The two device events": #66 overturns parts of both (see "The records the
  proxy emits since proxy phase 0043" below). In the **Hoplock Proxy
  repository**, read `docs/PLAN.md` §7 at "The record says what the proxy
  actually did (phase 0043)" and `api/README.md` "Algorithm profile". They are
  cited here and never restated.
- `docs/PLAN.md` §4 at **"A certificate is minted per session, never carried
  on a decision"** and the vocabulary note under "Answer within the vocabulary
  the proxy declared"; §5.2's credential-ladder bullet; **§6 "Credential
  brokerage (proxy D6a)"**; §7 at "A brokered certificate is recorded by its
  serial"; and **M5**, **M11** and **M22**. Together they cover what
  `Hoplock/proxy#68` changed (see "Brokered certificates reach a proxy"
  below). Open `0011`'s learnings at "CROSS-REPO DEPENDENCY" only to see what
  was asked for. Upstream answered in a different shape, and this prompt
  states the shape to build. In the **Hoplock Proxy repository**, read
  `docs/PLAN.md` §5.4 and **D6a**, and `api/README.md` "Brokered
  certificates". They are cited here and never restated.

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

1. **Re-vendor the contract at `4.4.0`.** Run
   `make contract-sync REF=4582c391a8a21338434997341b6c06a5c187fc0a` (the
   merge of `Hoplock/proxy#68`, which sits on top of `#66` and `#65`), or a
   later upstream `main`. If you use a later `main`, every contract change
   between the two is also this phase's to read and state. Never hand-edit
   `contract/` (M1). At that ref, `api/control.yaml` has the sha256
   `c5321fb505ce2e052f3ae91148c8291496658d9a6db178d01050dd50998b0cf1`, which
   is what `contract/UPSTREAM` should then record. Three upstream PRs arrive
   together, and only the last one moves `policy_version`. `#65` adds an event
   type and two endpoints (`4.2.0`). `#66` adds no field,
   no endpoint and no enum value (`4.3.0`). It changes descriptions only:
   `algorithm_profile: default` now means the SSH library's **secure set**, a
   tightening the document announces as a **break** beside `params.username`;
   the profile applies to every connection a route causes to its target; and
   the `target_auth_ladder` text now names the audit fields `credential_method`
   and `credential_rung` (counting from 1) where it used to publish
   `target_auth_*` (0-based). `#68` adds the `brokered-certificate` method and
   `POST /v1/credentials/certificate` (`4.4.0`). The method moves
   `policy_version` from **`4`** to **`5`**, because an unknown `method` value
   refuses the whole authorize response exactly as an unknown field does. So
   this re-vendor changes more than the checksum in `contract/UPSTREAM`.
   `internal/contract`'s enum test fails on the new `TargetAuth.method` value
   until it has a constant. Its path test fails on the new path until that has
   one, as it does on `#65`'s two. "Brokered certificates reach a proxy",
   below, is what makes both pass honestly. Note that
   `4.3.0` is a number the document has carried before: `#53` moved it
   **down** from `4.3.0` to `4.0.0`. A version string therefore does not
   identify a document, and nothing here may use one to. The checksum does
   (0002, 0018). The re-vendor also moves the proxy commit the `conform` CI
   job builds `cmd/mock-control` from. That is what gives the mock the fetch,
   the report, and `POST /debug/config`, and the conformance cases below
   depend on it.
   `#66` changes none of the mock's handlers. `#68` adds the mock's issuance
   handler and its `certificate_authority` fixture block. It also adds a
   second vocabulary tier to the mock, so a proxy declaring `4` is refused
   only the routes that name the method.
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

   A publisher failure must **not** roll back the publish, because the desired
   version is durable. But **"the proxy catches up on its next reconnect" is
   not a delivery guarantee.** The proxy has **no periodic fetch**. Upstream
   `ConfigSync.Run` wakes only on a notification, a stream (re)connect or
   `resync`, or a retry timer that is armed only after a *failed fetch*. So a
   notification lost while the stream stays healthy leaves that proxy on the
   old document until something makes it reconnect, and that can be hours. A
   successful fetch returning `304` does not arm anything either. Close the gap
   on this side, with one of these:
   - **Retained before acknowledged.** The publish is not acknowledged until
     the `config_changed` is in the stream's replay log. Then a subscriber
     that misses it live receives it on replay, or gets `resync`, which also
     makes it fetch. This means designing for the moment between the desired
     version committing and the event being appended: a crash there must not
     leave a committed version that nothing will ever announce. Two ways to do
     that are to re-announce every proxy whose desired version is newer than
     its last announced one at startup, or to write an outbox row in the same
     transaction.
   - **Retried.** Keep an "announced" marker per proxy, and re-emit until it
     is confirmed. The report is what confirms it: its `desired_hash` equals
     the hash announced.

   Either way the property is the same, and it is the one to test: after a
   publish, every live subscriber whose document changed learns about it
   without reconnecting, even when the first emit failed. Report the failure
   to the operator on the response as well. Do not swallow it and do not rely
   on it alone, because it describes the attempt, not delivery. The in-memory
   ring 0009 built is emptied by a restart, and it is not shared across nodes
   (0009's learnings, "Multi-node"). Say which of the two approaches you chose,
   and how it survives a restart.
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
   durably recorded, and never before. **Reports repeat, and storing them
   must be idempotent.** The proxy reports after **every** sync (upstream
   `SyncOnce` ends in `report` whatever the outcome). That includes each
   stream (re)connect, each `resync`, each failed-fetch retry every 30s, and
   each `304`. Most reports this server receives are therefore identical to
   the previous one from the same proxy. An unchanged report updates only the
   "last reported" time. It is **not** a fleet event: it writes no audit
   record, no state-transition history, and no drift-changed notification,
   and it does not count as a rollout step. Otherwise every reconnect storm
   becomes a flood of apparent configuration activity. Decide "unchanged" from
   the substantive fields (`running_*`, `desired_*`, `state`, the *set* in
   `restart_required`, `last_error`), **not** from `reported_at`: the proxy
   resets that each time it re-evaluates a document, so identical content can
   arrive with a new timestamp. Ordering needs a rule too. A report whose
   `reported_at` is older than the one stored must not overwrite it, because
   two reports in flight can arrive out of order. Store what the contract
   carries:
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
   match must be **strict**. The same description backticks *values* such as
   `"30s"` and `"5m"` (the duration examples), so a loose "anything in
   backticks" match picks up non-keys. Match only dotted keys, something like
   `` `([a-z_]+(?:\.[a-z_]+)+)` ``, and assert the count of keys the extraction
   finds. Against `Hoplock/proxy#65`'s text that pattern extracts exactly
   **17** keys, which is D18's count, and none of the values. A description
   reworded into a shape the pattern misses then fails the test rather than
   producing an empty list that trivially agrees. The next
   `make contract-sync` that adds or removes a fleet-owned key must fail that
   test rather than slip past it. Whether a key applies live or at restart is
   the proxy's business, and this server stores neither.
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
   - the report answers `200 {"accepted": true}`. The report the suite sends
     must be **valid**: `state` is one of the four enum values, and
     `reported_at` is present (both are `required` in the contract). The mock's
     handler answers `400` without them, and a case sending a bare `{}` would
     fail against the mock for reasons that have nothing to do with the
     server under test. Also send the same report twice and require `200`
     both times. A server that rejects the repeat breaks the proxy's report
     after every sync.

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

### The records the proxy emits since proxy phase 0043 (`Hoplock/proxy#66`)

0010 built the audit store and raised three shapes upstream
(`Hoplock/control#32`): `algorithm_profile` on the record, the
`device.config.change` event, and one name for the credential method and its
rung. The proxy answered as its phase 0043, merged as **`Hoplock/proxy#66`**.
That PR's `## Cross-repo impact` section puts the obligations below on this
repository, and they are this phase's for two reasons. This is the phase that
serves the audit query (above), and every query below is one that surface
exposes. And from this phase on the north-bound field names are a
compatibility promise (M19), so they have to be right before they ship. None
of it is a contract change, because `LogRecord.attributes` is an open string
map. The only `contract/` change is the text item 1 re-vendors.

1. **Ingest a sweep's change record, which belongs to no session.** When the
   proxy's device reaper removes an orphan, it emits `device.config.change`
   with `kind: provisioning` and **`session_id: ""`**. A sweep is somebody
   else's leftover, and naming the session that triggered it would attribute
   the removal to the wrong person. Today `audit.Parse` refuses an empty
   `session_id` for every kind except `error` (`internal/audit/record.go`), and
   a batch is all or nothing (PLAN §7). So one such record fails its whole
   batch with a `400`. **This stops that proxy's audit delivery entirely.** The
   proxy treats a `400` like any other delivery failure: it spills the batch to
   its disk buffer and retries the oldest buffered segment until the server
   takes it. While anything is buffered, every later record joins the back of
   that queue, **priority records included** (upstream
   `internal/logging/shipper.go`, `sendBatch`/`sendPriority`/`drainBuffer`).
   So the first orphan a device sweep removed would stall the mapping events
   behind it indefinitely. The buffer has no size bound of its own, so it
   would grow on the proxy's disk until a write failed and records started
   being dropped. The stall itself is the proxy's to fix, and it is raised
   upstream in the `## Upstream request` of the PR that added this item
   (`Hoplock/control#39`). The request asks for a bounded buffer that evicts
   the oldest records, and for a refused record that no longer blocks the
   ones behind it. Neither answer removes this item. Once the proxy stops
   retrying, a refused sweep record is not late but **lost**, so this server
   must still accept it.

   The rule is that a record may lack a session only when nobody was present
   for it. That covers `error` records (a sweep failure) and a sweep's
   `device.config.change`. Widen the exemption to exactly that event under
   `provisioning`, and say why in the code comment, as the `error` case already
   does. Every other record still needs a `session_id`. An unknown shape stays
   loud on purpose (PLAN §7, the kind rule), and any other session-less record
   the proxy adds later will arrive through a sync that names it. An empty
   session id is **not a session**, either: the by-session lookup and
   `explain` must refuse `""` rather than return every sweep record in the
   tenant.
2. **Index `device.config.change`, the drift feed.** It is one record per
   configuration change the proxy made on a device. `kind: provisioning`,
   `severity: info`, and it arrives on the **batch** path. Never expect it on
   the priority path. Its attributes are
   `platform`, `device_change_op` (`create` | `modify` | `delete`),
   `target_account` (the object's name), `device_object_kind` (present only
   when the object is not an administrator, so absent means administrator),
   and the route's `device_field.<name>` values. The record's `target` is the
   device. A session's changes carry its session id and its device fields. A
   sweep's carry neither, because a sweep has no route. There is no credential
   material: installing a credential is a `modify` of the administrator, and
   what was installed is not named. `Reader.DeviceConfigChanges` exists (0010)
   and filters on the `event` column only. Give it the filters a drift
   reconciliation asks with: the device (`target`), the object
   (`target_account`, `device_object_kind`) and the operation. Return those as
   fields of the row, not as an attribute map the caller has to parse. Whether
   they become derived columns (a forward-only migration, 0003) or an indexed
   query over `attributes` is your decision, and your learnings say which.
   Rewrite the comment on `EventDeviceConfigChange` that says the proxy does
   not emit it yet. Exporting the feed to a customer's SIEM is Enterprise's
   (its E7). Making it queryable here is what that export reads.
3. **`algorithm_profile` is always present on a target-leg record.** The proxy
   stamps the profile **in force** on the session's `provisioning` record and
   on the `device.account.mapping` event, and it stamps **`default` too**. So
   an absent profile never means `default`. It means the record is not about a
   target leg (a hop, or a failure before provisioning). Keep an absent
   profile as empty in the store, never backfilled to `default`, and never read
   it as a weakening. The weakening query is then one filter: the profile is
   present and is not `default`. Serve it: "which sessions ran on a legacy
   profile, against which targets" is how an operator learns a route runs on
   SHA-1 from the record rather than by reading policy, which is the promise
   the contract makes.
4. **One name per field: `credential_method` and `credential_rung`, counting
   from 1.** Those are the only names the proxy emits, and a test upstream
   holds that. The `target_auth_*` pair (0-based) came from the proxy's own
   contract text, which published it by mistake, and `#66` corrects that text
   (item 1). So drop `AttrTargetAuthMethod`, `AttrTargetAuthRung` and the
   read of two names (`firstOf` in `internal/audit/record.go`). Then rename
   the audit fields still named after that pair: the columns, in a new
   forward-only migration (0003), plus
   `store.AuditRecord.TargetAuthMethod`/`TargetAuthRung`, the query layer, and
   the doc comments that call the rung 0-based. The north-bound surface then
   exposes one spelling, the proxy's. The contract's own `target_auth_ladder`
   and `contract.TargetAuthMethod` (the ladder entry's `method`) are wire
   names, not audit fields, and they stay. Keep the value exactly as it arrived,
   counting from 1: every projection must be recomputable from the hashed
   body (0010), so a converted value in the column would disagree with the
   record it came from. **The degradation query is wrong today because of
   this.** `Reader.DegradedCredentials` keeps rows whose rung is `> 0`, and
   every rung a real proxy sends is `>= 1`, so it reports every session as
   degraded. The first choice is rung `1`, and a degraded credential is rung
   `> 1`. No stored row needs rewriting, because no producer has ever sent
   anything but `credential_rung` counting from 1. Say that in your learnings
   so the next reader does not go looking for a data migration.
5. **Tell a policy author what `default` no longer reaches, and how to find
   it.** `algorithm_profile: default` is now the SSH library's **secure set**.
   It offers no SHA-1 key exchange, no `hmac-sha1-96`, and no `ssh-rsa` or
   `ssh-dss` host key. `legacy-rsa-sha1` adds `ssh-rsa`. `legacy-device` adds
   that plus the SHA-1 key exchanges, the CBC ciphers, `hmac-sha1-96` and
   `ssh-dss` host keys. The contract's `algorithm_profile` description is the
   authority, so cite it; never copy its algorithm lists into code here,
   because upstream pins them with a test and a copy would drift. So a device
   that speaks **only** SHA-1 key exchange, `ssh-rsa` or `ssh-dss` stops
   connecting under `default` and needs a legacy profile. The proxy reports
   each such target as **`target.algorithm_policy_unmet`**: an `error` record
   at `warn`, on the batch path, with `algorithm_profile`, `target_addr`,
   `algorithm_axis` (`key_exchange`, `host_key`, `cipher`, `mac`,
   `compression`) and `target_algorithms_offered` (comma-separated). The user
   is told only that it is an outage. Two things, then:
   - **Findable.** The audit surface lists the targets that have reported it,
     with the axis, what the target offered, the profile it failed under, and
     when. That list is what an operator reads to choose a route's profile.
   - **Shown at authoring time**, on M17's terms (above). If the store holds a
     `target.algorithm_policy_unmet` for a target a candidate route reaches,
     under the same profile the route names, the satisfiability report warns.
     The warning names the target, the axis, what the target offered, and when
     it was seen. It warns and never refuses: the record is a past observation,
     and the device may have been upgraded since. Match the record to the
     target the way the showcase join already matches a record to one (0010).

### Brokered certificates reach a proxy (proxy phase 0044, `Hoplock/proxy#68`)

0011 built the per-tenant SSH certificate authority and could not reach a
proxy with it, because the contract had no method that could name a
certificate. So 0011 left the authority behind `internal/credential/seam.go`,
which refuses on purpose and carries a tripwire, and raised the need upstream
(`Hoplock/control#35`). The proxy answered as its phase 0044, merged as
**`Hoplock/proxy#68`**: contract **`4.4.0`**, `policy_version` **`5`**. There
is no new decision, because the method is what proxy **D6a** already
promised: a Control that mints credentials arrives as another method. That
PR's `## Cross-repo impact` section puts six obligations on this repository,
and they are this phase's. The re-vendor is the first of them (item 1 of the
fleet-configuration list above). The other five, and the grading, follow.

They are this phase's because the re-vendor brings the method in whether
anything here is ready for it or not. So the phase that runs
`make contract-sync` is the phase that has to make it true. A method that
bundles can name with nothing behind it to issue certificates would be a route
every proxy fails as an outage.

**Upstream did not take the shape 0011 asked for, so read this part twice.**
0011 asked for the certificate, its serial and the CA bundle as parameters on
the ladder entry. `seam.go` and 0011's learnings both still describe that
shape. But the entry rides the authorize decision, which is cacheable and
served to every connection it covers. A certificate on it would be replayed
past its own expiry, which is the argument PLAN §4 already makes for keeping
the uid floor off it. The certificate also cannot exist yet at that point,
because it is signed over a key the proxy generates after the route is
decided. So the entry carries **policy**, and a new endpoint returns the
**artifacts**, once per session. The reasoning is proxy PLAN §5.4's: cite it,
and do not restate it.

1. **Vocabulary `5`, and the method is never sent to a proxy that declared
   less.** The re-vendor adds `brokered-certificate` to `TargetAuth.method`.
   Declare `contract.TargetAuthBrokeredCertificate`, with `Provisions()`
   answering `false` as its own case, and the path constant beside the
   others. The proxy creates nothing on the target, so a route that names
   only this method can reach only an **attested** rung (PLAN §5.2). Add the
   method to the policy model too: `model.CredentialMethod`,
   `credentialMethods`, and its own `Provisions()` case. Also add it to the
   contract list in `internal/policy/model/contract_agreement_test.go`. That
   test lists the contract's constants by hand, so a constant added on one
   side only still passes it, and it leaves the method silently unauthorable.
   The build having an issuer is a capability question M17 already asks.
   A proxy built without one skips the rung (proxy §5.4), and the pre-publish
   query checks a ladder entry's method against the proxy's declared
   `Capabilities.CredentialMethods` (0006).

   Then move `contract.PolicyVersion` to **`5`** in this phase. No test ties
   the constant to the document until 0018, so nothing forces the move, and
   that is the danger. Left at `4`, this server would hand a proxy that
   declared `4` an entry it refuses the whole response over, which reaches the
   user as an outage. The gate for this is already built, in
   `internal/decision/vocabulary.go`. `requiredVersion` gains its first real
   case: it answers `5` for a response whose ladder names
   `brokered-certificate` at any rung, and a named baseline `4` otherwise.
   `checkVersion` then refuses such a response to a proxy that declared `4`,
   with the existing `5xx` naming both numbers, and never with a `401` (M11).
   So a proxy one revision behind still gets
   every route it can read, which is what the mock does (`baselineVocabulary`
   `4` and `vocabularyBrokeredCertificate` `5` in its `vocabularyVersion`).
   Keep the baseline a constant of its own rather than `PolicyVersion - 1`.
   The next revision will move `PolicyVersion` again, and this method's tier
   must not move with it. The refusal is per route, and it is whole. Take a
   proxy declaring `4` for a route whose ladder is `brokered-certificate` then
   `brokered-key`. It is refused. It is never answered with the second rung
   alone, because dropping a rung the policy wrote is the thinned answer PLAN
   §4 forbids.
2. **The ladder entry is policy: `username`, `key_type`, `lifetime_seconds`,
   and nothing else.** `username` is required, as on every method (PLAN
   §5.2). `key_type` is the algorithm of the key pair the proxy generates for
   the session (`ed25519` by default, or `rsa`). `lifetime_seconds` is an
   **upper bound** on the certificate's validity. Absent, `key_type` leaves the
   proxy's default, and `lifetime_seconds` leaves this server's own lifetime.
   **`certificate`, `certificate_serial` and `ca_public_keys` are never
   parameters.** A proxy that implements the method refuses a parameter it
   does not know, before it generates anything, so an entry carrying any of
   the three fails the session closed.

   Two renderers exist today, and only one may survive. The decision path
   renders every entry in `internal/decision/snapshot.go` (`credentialEntry`)
   from the policy model, and it already writes exactly those three names
   when they are set. `credential.LadderEntry` in `seam.go` is called by
   nothing, and it renders the shape upstream refused. Keep one and delete
   the other. If `LadderEntry` survives, it takes no `Issued` and no trust
   bundle, because nothing it renders comes from an issuance, and the
   decision path is what calls it.

   The compiler has to let an author write the two optional names.
   `checkParamScope` (`internal/policy/compile/compile.go`) permits
   `key_type` on `ephemeral-user` only, and `lifetime_seconds` on
   `ephemeral-user` and `ephemeral-account` only. Both gain the new method,
   and the doc comments on `model.CredentialEntry` that name those methods
   change with them. Nothing in a bundle can spell the three artifacts, and
   that must stay true. An **applied** enforcement rung on a route whose every
   entry is `brokered-certificate` is refused at authoring time, as it is on
   `brokered-key`. The compiler keys that check off `Provisions()`, so item
   1's `false` is what makes it hold. Test it anyway.

   **Change the tripwire. Do not delete it.**
   `TestTheBrokeredCertificateMethodIsNotYetInTheContract`
   (`internal/credential/ca_test.go`) is the signal 0011 left for this
   phase. Do not just remove the refusal it guards. The body behind the
   refusal renders all three artifacts, so un-refusing it puts upstream's
   rejected shape on the wire. Change the test to pin the new shape instead.
   A rendered `brokered-certificate` entry names the method and carries
   `username`. It carries no parameter outside `username`, `key_type` and
   `lifetime_seconds`. The test fails, naming the parameter, if
   `certificate`, `certificate_serial` or `ca_public_keys` appears. Rename it
   to what it now asserts. `TestALadderEntryStillRequiresAUsername` keeps
   holding, and `ErrMethodNotInContract` goes. `seam.go` exists to name what
   the contract is missing, and after this phase nothing is missing. Fold what
   survives into the package, and rewrite the comments that describe the gap:
   `seam.go`'s header and the "WHAT TRAVELS" note at the top of `ca.go`. PLAN
   §3 and §6 describe the seam too, so revise them in this PR.
3. **Serve `POST /v1/credentials/certificate` over `credential.CA.Issue`,
   which replaces the seam's refusal.** Put it on the south-bound listener
   (0007). Authenticate it and resolve its tenant from the proxy's credential,
   exactly as every contract route does (M22). The request is
   `{session_id, decision_id, target, username, public_key}`, of which only
   `session_id` and `public_key` are required. The answer is
   `{certificate, serial, valid_before, ca_public_keys}`, of which only the
   last is optional. The contract's `CertificateRequest` and
   `CertificateResponse` are the authority on both. `Issue` was built for this
   call and already does most of it. What the handler owes:
   - **Sign exactly the submitted key, as a user certificate.** `public_key`
     is in `authorized_keys` form. One that does not parse, or that is itself
     a certificate, is a `400`. `Issue` already refuses the second case and
     already sets `ssh.UserCert`. The principal is the route's `username` and
     nothing else. The subject is the decision's. The session is the one the
     request names, and that is what `ssh_certificates.session_id` records.
   - **Bind it to a decision this server made.** Every authorize answer this
     server gives carries a `decision_id` (M4), and the contract lets a server
     that requires one refuse a call without it, so require it. Read the
     decision from the store, in the caller's tenant, and cross-check it
     before signing:
     - the decision allowed;
     - its snapshot's ladder names `brokered-certificate`;
     - `target` equals the target that answer named;
     - `username` equals that entry's `username`.

     A call that fails any of these is a `401`. The mock's codes for it are
     `unknown_decision` and `decision_mismatch`, and the mock is the
     reference behaviour.
     Do **not** require the request's `session_id` to equal the stored
     decision's. A cached decision serves many sessions, and each of them asks
     for its own certificate citing the same `decision_id`. You also decide
     whether issuance must come from the proxy the decision was made for (its
     `proxy_id`, M22). Say which in your learnings.
   - **Every such `401` is a refusal to mint, and never a failure (M11).** A
     store error while reading the decision, or while writing the
     certificate's row, is a `5xx`. The proxy treats every non-`200` from this
     endpoint as an outage and never as a second denial, because the session
     was already authorized. That is the contract's one stated exception to
     "`401` is a deny". It changes what the proxy does with the answer, not
     what this server may send. In `internal/httpapi/south`,
     `TestOnlyOneFunctionCanProduceA401` allows two deniers, and its comment
     says that adding one is a decision. This is one. A refusal to mint is
     neither the user's credential being refused nor the proxy's token, so by
     that comment's own reasoning it gets a third denier rather than reusing
     `deny`.
   - **Bound it by `lifetime_seconds`, and never let it run forever.** The
     certificate lives for the CA's own validity
     (`credential.certificate_validity`), shortened to the route's
     `lifetime_seconds` when that is less. It is never lengthened to it: the
     route states an upper bound, not a request. `IssueRequest.ValidFor` is a
     request honoured up to `max_certificate_validity`. So passing the route's
     bound straight in lengthens every certificate whose bound exceeds the
     default. `Issue` always sets `ValidBefore`, and it must keep doing so. A
     certificate with no expiry is not a per-session credential, and the
     proxy refuses one.
   - **`valid_before` is the certificate's own instant, to the second.** The
     proxy refuses a response whose `valid_before` differs from the
     certificate's. `Issued.ValidBefore` keeps the sub-second part that the
     certificate's own field truncates. So render `valid_before` from the
     certificate, or truncate before formatting it as RFC 3339.
   - **`serial` is a decimal string, and never a JSON number**
     (`^[0-9]{1,20}$`). An SSH serial is a `uint64`, and JSON numbers are not
     safely integral above 2^53. Render it with `strconv` in one place, and
     never "fix" the type.
   - **`503` when there is no authority.** There are two cases: a tenant with
     no CA (`credential.ErrNoCA`), and a deployment that configured none (no
     `credential.key_encryption_key_env`). 0011's north-bound surface already
     answers the second with `503 ca_not_configured`. It is an operator's fix,
     so it is not a deny and not a `500`. `statusFor` is the only place a
     status is chosen, and it has no `503` today, so add the class there.
   - **`ca_public_keys`, if you send it, is the tenant's current trust
     bundle** (`Describe`), read per call so that a rotation reaches the proxy
     fresh. The proxy decodes it, carries it, and acts on none of it.
     Publishing trust to a target is a provisioning act, and this method
     performs none (proxy §5.4).
4. **The serial comes back on a record, and it is the join key.** The proxy
   stamps the serial it was issued as **`credential_certificate_serial`** on
   the session's `provisioning` record, the one that names
   `credential_method`. That record is `info` and arrives on the **batch**
   path (proxy PLAN §7). The contract's own text calls it the session's
   "authorize record". The proxy's code and its 0044 learnings put it on the
   provisioning record instead, because the authorize record is written
   before issuance. So key off the record that names `credential_method`,
   not off the word. `LogRecord.attributes` is an open map, so ingest needs no
   change to accept the field. What is owed is the join the serial exists
   for. The audit surface this phase serves must answer two questions: which
   certificate did this session present, and which session presented this
   certificate. `ssh_certificates` keeps the serial per tenant as an integer,
   and the record carries a decimal string. Parse it in one place as an
   unsigned integer, and never through a float. If you index it as a derived
   column (a forward-only migration, 0003), it must be recomputable from the
   hashed body like every other derived column (0010). The certificate itself
   is never on a record, and nothing here may put it there.
5. **One issuance per session, and nothing answers one from memory.** Every
   call signs the key it was sent, under a new serial, and writes its own
   row. Nothing memoises an issuance:
   - not by `decision_id`, because a decision reused across connections cites
     the same one from each;
   - not by `session_id`;
   - not by key.

   The contract makes one call per session the proxy's rule, and upstream
   enforces it in its types: its caching client implements no issuer. This
   side's half is never to be the cache. The authorize decision stays
   cacheable, because the entry carries only policy, and PLAN §5.4's hint
   rules apply to it unchanged. A certificate is the one thing no hint may
   cover.
6. **Grade it in `cmd/pdpconform`**, against both this server and the proxy's
   mock (M1), in the layers 0002 established. Contract-level cases:
   - a route whose ladder names `brokered-certificate`, declared at `5`, is
     answered with an entry that carries `username` and no name outside the
     three policy ones;
   - the same route declared at `4` is refused with a `5xx` and the envelope.
     It is never a `401`, and never a `200` with the rung dropped;
   - an issuance citing that decision, with a fresh key, answers `200` with a
     certificate that parses as a **user** certificate over **that** key.
     `serial` is a decimal string equal to the certificate's own serial.
     `valid_before` equals the certificate's own instant and is in the
     future. When the route sets `lifetime_seconds`, `valid_before` is no
     later than that bound from now, allowing the proxy's 30 seconds of clock
     skew;
   - two issuances citing one decision, with two keys, answer two serials;
   - an issuance citing a `decision_id` the server never made answers `401`,
     and never `200`.

   The mock needs a `certificate_authority` block in `mock-fixtures.yaml`,
   naming an ed25519 private key in OpenSSH format that the `conform` job
   generates. Without one, every issuance answers `503`, and the mock refuses
   a key of any other type at startup. It also needs a route whose ladder
   names the method. `routes[].certificate_fault` is the mock's own, and it
   stays out of the suite's fixtures unless a case needs it. The `503` case
   needs a server with no authority, so grade it where a fixture can provide
   one, which is this server's expectations with a tenant that has no CA. Do
   not grade it at the contract level. Update `cmd/pdpconform/README.md`'s key
   table.

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
- Revoking a brokered certificate mid-session. The revocation stream's
  `session_kill` already ends the session. A certificate revocation event, or
  a revocation-list check on the session path, would be a later phase if
  upstream queues one (proxy §5.4). The CA's own rotation and revocation
  (0011) do not change.
- Publishing the CA's trust bundle to a target. `ca_public_keys` is carried by
  the proxy and acted on by nothing.

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
  the contract re-vendored (item 1), a zone publish emits exactly one
  `config_changed` per re-materialised proxy,
  naming its composed document. A no-op publish emits none. The fetch answers
  `200`+`ETag`, `304` on the held hash, `204` with nothing published, and
  `404 not_enrolled` for an unknown id or an id in another tenant. Assert that
  last case explicitly, because it is the tenancy leak on this route. A report
  of each `state` shows in the fleet view as that state, with `last_error` for
  `rejected` and `fetch_failed`, and a `pending_restart` proxy is **not**
  counted as caught up. **Delivery without reconnect:** with the first
  `config_changed` emit made to fail, a subscribed proxy whose stream stays up
  still learns about the new document (through the replay log or through
  retry, per item 2), and this survives a server restart between commit and
  emit. **Idempotent reports:** a hundred identical reports leave one stored
  state and zero audit records or fleet events beyond the first. A report
  with a newer `reported_at` and identical content is still "unchanged". An
  out-of-order older report does not overwrite a newer one. The key-list
  test's pattern ignores the backticked duration values in the description,
  and fails when the extraction finds the wrong number of keys. A publish
  naming a bootstrap-only key, a key the
  contract does not list, or a nested object is refused at publish time naming
  the key. The test tying the fleet-owned list to `contract/control.yaml` fails
  when the two disagree: prove it, don't assume it. `make conform` passes with
  the configuration cases against this server **and** against the proxy's mock.
- **The records proxy phase 0043 emits are stored and answered for**
  (`Hoplock/proxy#66`). A batch mixing session records with a sweep's
  `device.config.change` (`session_id: ""`) is accepted and stores every
  record. A session-less `provisioning` record with any other event, and a
  session-less record of any kind but `error` or `provisioning`, still fail
  their batch. The by-session lookup refuses `""`. The
  drift-feed query filters by device, object and operation, and returns a
  sweep's change with no session and no device fields. The weakening query
  returns a record under `legacy-device` and **not** one under `default`, nor
  one with no profile. Assert those two negatives, because reading absence as
  `default`, or `default` as a weakening, are the plausible bugs. The
  degradation query returns a record with `credential_rung` `2` and not one
  with `1`, nor one with no rung. A record carrying only `target_auth_rung`
  projects no rung, and neither `target_auth_method` nor `target_auth_rung`
  (nor a Go field named after either) is left in `internal/` or on the
  north-bound surface outside merged migrations. A candidate route naming
  `default` for a target with a stored `target.algorithm_policy_unmet` under
  `default` publishes **with a warning** naming the target, the axis and what
  it offered. The same route naming `legacy-device` gets no warning from that
  record.
- **Brokered certificates reach a proxy** (`Hoplock/proxy#68`). With the
  contract re-vendored at `4.4.0`, `contract.PolicyVersion` is `5`. A route
  naming `brokered-certificate` at any rung is refused to a proxy declaring
  `4`, with a `5xx` naming both numbers, while a route without it is answered
  to that proxy unchanged. Assert both.

  The rendered entry carries `username`, and at most `key_type` and
  `lifetime_seconds` besides. The changed tripwire fails when `certificate`,
  `certificate_serial` or `ca_public_keys` appears: prove it by making it
  fail once. The policy model and the contract agree member for member. An
  applied rung on a route whose only method is `brokered-certificate` is
  refused at authoring time.

  An issuance returns a user certificate over exactly the submitted key, with
  the route's `username` as its only principal. `serial` is a decimal string
  equal to the certificate's own serial, and `valid_before` equals the
  certificate's own instant, to the second. The lifetime is the shorter of
  the route's bound and the CA's own validity. Assert that a route bound above
  the CA default does **not** lengthen it.

  Each of these answers `401`, through the new denier: a `decision_id` this
  server never made, a decision that denied, one naming no certificate rung,
  and a `target` or `username` the decision did not name. A tenant with no
  authority answers `503`. A store failure answers `5xx`, never `401`.

  Two issuances citing one cached decision produce two certificates, two
  serials and two rows. A stored `provisioning` record's
  `credential_certificate_serial` resolves to the row minted for that
  session, and the row resolves back to the record. `make conform` passes
  with the certificate cases against this server **and** against the proxy's
  mock.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0014-northbound-api-and-policy-lifecycle-learnings.md`. Summary
block MUST give the route table with required roles, the bundle lifecycle states,
the simulation API and its purity requirements, the `explain` response shape, and
the `policyctl` command set. It must also give the fleet-configuration delivery:
the contract commit re-vendored, how `int64` versions and hashes render onto the
wire, where the fleet-owned key list lives and what ties it to the contract, the
report's storage and how drift is derived from it, the fate of
`Heartbeat.RunningConfigVersion`, and the conformance keys added. And it must
give what `Hoplock/proxy#66` changed here: which records may lack a session,
the credential columns' names and the degradation threshold, the drift feed's
filters and where they are stored, the weakening query, and where the
algorithm-policy warning reads its evidence. And it must give what
`Hoplock/proxy#68` changed here:

- where the vocabulary tier lives and what it refuses;
- which renderer produces a `brokered-certificate` entry, and what the
  tripwire now pins;
- the issuance route's status codes, and the denier its `401` goes through;
- whether issuance is bound to the proxy the decision was made for;
- how a certificate's lifetime is computed;
- how a record's serial joins its certificate row.

Phase 0012 adds routes to this surface and phase 0017 drives it end to end.
