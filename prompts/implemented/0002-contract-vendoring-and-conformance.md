# 0002 — Contract vendoring & conformance harness

## Read first
- `docs/PROTOCOL.md` — session workflow, especially §3 ("never edit
  `contract/`") and §9.
- `docs/PLAN.md` — especially **§2 (M1)**, §4 (the endpoint table and the
  obligations the suite must grade — including the uid allocation cursor, whose
  monotonicity no single-request assertion can see).
- `docs/learnings/` — read summaries; open `0001` (Makefile targets, CI shape).
- In the **Hoplock Proxy repository**: `api/control.yaml` and `api/README.md`.
  Read the ground rules, the endpoint table, and — before writing any
  assertion — **"Versioning: one live vocabulary, and a proxy that fails
  closed"** and **"The policy vocabulary"**. The latter's **"Absent-value
  defaults, in one table"** subsection carries the absent-value default of every
  policy field in one place, which is what tells you whether a missing field in
  a response is a pass or a failure. Read individual schemas as you need them.
  Do not read the proxy's Go code.

  **Both documents state one live vocabulary, in the present tense, and carry no
  revision history.** Upstream `Hoplock/proxy#53` (merged) deleted it: there is
  no "v3→v3.1", "v3.1→v4", "v4→v4.1", "v4.1→v4.2" or "v4.2→v4.3" section to
  read, no "Policy vocabulary v2"/"v4" section, and no "since version N"
  annotation on any field. Do not go looking for them and do not cite one: every
  rule they carried is now stated in the present tense beside the thing it
  governs. A prompt or assertion that names a revision by number is describing a
  section that does not exist.

  Read the **"Additional device fields (`device_field.<name>`)"** subsection
  under "Ephemeral accounts on devices" — an open namespace the contract
  deliberately does not enumerate. Then read **"Ephemeral uid blocks"**, the
  `/v1/uids/lease` path, and the `UIDLeaseRequest`/`UIDLeaseResponse` schemas,
  **in full**, before writing a single lease assertion: the monotonic-cursor
  invariant is the whole endpoint, and it is the one thing the suite has to be
  built to catch.

  **The two numbers are still two numbers**, and "Versioning" is where the
  document says so. The **document** version (`info.version`) and the
  **negotiated policy vocabulary** (`policy_version`) are independent, because
  the negotiated one "governs `/v1/authorize` and nothing else" — that is the
  response decoded strictly, and so the only place an unknown field could be a
  dropped restriction. The document names its own worked examples of what falls
  outside it: `HostKeyReportResponse.cache`, a field on another endpoint, and
  `POST /v1/uids/lease`, a whole endpoint. Read that section before writing any
  assertion that ties one number to the other, and see "The two numbers" below.

## Objective
Bring the contract into this repo as a **vendored, verifiable artifact**, and
build the **black-box conformance suite** that decides whether an implementation
of it is correct. Everything after this phase is graded by what you build here,
so the suite's honesty matters more than its size.

## In scope

### Vendoring (M1)
- `contract/control.yaml` — a byte-for-byte copy of Hoplock Proxy's
  `api/control.yaml`, plus `contract/UPSTREAM` recording the source
  repository, the commit SHA, and the date it was taken. **Keep the upstream
  filename.** A vendored artifact that is renamed on the way in makes every
  cross-repo conversation ("look at `control.yaml`") ambiguous, and
  `make contract-sync` has one less thing to get wrong.
- `make contract-sync` — fetches a given ref from the Hoplock Proxy repository, replaces the
  copy, and rewrites `contract/UPSTREAM`. Takes the ref as a variable so a
  session can pin a specific commit.
- `make contract-check` — recomputes the copy's checksum and fails if it does not
  match what `contract/UPSTREAM` records. This is what catches a local edit. Wire
  it into CI as its own job so the failure names itself.
- `contract/README.md` — one screen: this directory is generated, here is how to
  change the contract (in the Hoplock Proxy repository, then sync), here is why editing it
  locally is the specific failure this rule prevents.

### Generated types (`internal/contract`)
- Go types for every payload in the document, and the server-side handler
  interfaces they imply. Generate them if you can do so reproducibly (`make
  generate`, checked in, CI verifies regeneration is a no-op); hand-write them
  only if generation costs more than it saves, and say which you chose and why in
  your learnings.
- Named constants for every enum, and a test asserting they match the document —
  the Hoplock Proxy repository does exactly this and it is what catches an enum drifting.
- This package is the **only** place that knows wire shapes (PLAN §3).

### The conformance suite (`cmd/pdpconform`)
A binary that takes a base URL, a bearer token, and a fixture/expectation file,
drives the contract over real HTTP, and reports pass/fail per assertion.

It must cover, at minimum:
- **Auth**: cert success and deny; password without MFA; password with MFA
  (`mfa_required` → poll pending → poll authenticated); MFA deny; MFA expiry;
  an unknown challenge token.
- **Authorize**: `direct`, `nexthop`, `401`, and a response carrying every field
  the document defines — assert **shape**, not policy content, because policy is
  the implementation's business and the contract's is the envelope.
- **`username` on every credential method**: `params.username` is required on
  every method the contract defines, so every `brokered-key` entry of a
  `target_auth_ladder` in a fixture names a `username`, and the suite asserts the
  field is **present** rather than that some particular value round-trips. A
  fixture or expectation asserting a `brokered-key` response shape *without* one
  encodes a contract nobody serves and is a bug to fix rather than a case to
  keep: the proxy refuses such a route at the first authorize call, so an
  implementation the suite passes on it would fail in front of a user. Write it
  as a required-field assertion and **not** as an absent-value default — the
  absent-value discipline covers fields whose omission *means* something, and
  omission here means the route is refused. Do not tie it to `policy_version`
  either. This is the contract's one **tightening**, and the document's
  "Versioning" section explains why a tightening is not expressible through the
  version at all: it adds no field and changes no field's meaning, so it is
  announced as a break instead. There is no version at which omitting it is
  correct.
- **Vocabulary negotiation**: the same authorize request sent with
  `policy_version` set to the current version and to `1`. Assert the low-version
  answer carries **no field introduced after version 1** — or is a `5xx` naming
  the mismatch, which is the correct answer when the policy needs one. Both are
  passes; a thinned snapshot that drops a restriction and returns `200` is not,
  and neither is a `401`. This is the assertion that stops a server from
  breaking every older proxy in a fleet mid-upgrade, and no other assertion here
  catches it: a response tested only at the current version looks perfect.
- **A request with no `policy_version` is refused.** The field is in
  `AuthorizeRequest`'s `required` list and has **no absent-value default**
  (upstream `Hoplock/proxy#53`, merged — it previously carried `default: 1`), so
  send the same authorize request with the field omitted entirely and assert the
  server answers `400 invalid_request`. Not `200` with a guessed version, and
  not a `401`: a deny is a policy decision about a user and this is a malformed
  request. **Write this as its own case, distinct from the `1` case above** —
  they look similar and are not: `1` is a proxy that told the truth about being
  old, and absent is a proxy that said nothing. Only the first can be answered
  safely, because guessing a version for the second is guessing which
  restrictions it would silently drop. A suite that only ever sends the field
  cannot tell a server that requires it from one that defaults it.
- **Additional device fields** (`device_field.<name>`): an
  `ephemeral-account` rung carrying device fields round-trips with the names and
  values intact and **unenumerated** — the suite asserts the shape the contract
  states (name lowercase letters, digits, hyphens and underscores and ≤64
  characters; value non-empty and ≤256 characters; ≤16 fields per entry) and
  asserts nothing about which names are meaningful, because the contract does not
  say and a suite that hard-codes `vdom` will fail the first customer driver.
  Assert too that a device field demands **no higher `policy_version` than the
  route carrying it otherwise would** — the namespace is open, and a name inside
  it is not a new policy field, so a proxy is served a route bearing device
  fields at whatever version that route needs without them. Write that assertion
  against the rule, not against a literal: pinning it to a number makes it stale
  at the next revision, and a suite that expects a bump here is asserting a rule
  the contract does not have.
- **Host keys**: first sighting and a known key; and that a `cache` hint on the
  response round-trips as the same `CacheHint` shape `/v1/authorize` answers
  with, while a response **carrying none is equally a pass** — absent means
  "report every connection". `HostKeyReportResponse.cache` is the contract's own
  worked example of a field outside `policy_version`, so do not gate this case
  on a version. Assert the envelope only: whether a given key
  is worth hinting is the implementation's business (0007), not the contract's.
- **UID leases** (`POST /v1/uids/lease`): a lease returns a block
  with `uid_to` strictly greater than `uid_from`, and a block requested inside
  `[range_min, range_max]` comes back inside it — a server that ignores the
  range is caught here rather than by a proxy refusing every block it is
  granted.

  The assertion that matters is **non-overlap, and it is the only one that
  grades the invariant**: lease repeatedly for the same target and assert no
  granted block ever intersects an earlier one — including blocks the suite
  deliberately **abandons** (leases and never allocates from) and blocks it lets
  **expire** past `term_seconds`. A server that reclaims either to save uids is
  the exact failure this endpoint exists to prevent, and it passes every
  assertion that only checks one lease at a time. Assert across **two distinct
  `proxy_id`s** too: exclusivity is per target, not per proxy, and a server that
  keyed its cursor by proxy would look perfect to a single-proxy suite.

  Assert that `observed_floor` **may raise the cursor and may never lower it**:
  a lease sent with an `observed_floor` above the last grant comes back at or
  above it, and one sent with a floor *below* the last grant does not pull the
  cursor back down. Assert `409` when the cursor has reached the top of the
  range, with the contract's error envelope — not a `200` carrying an empty or
  inverted block.

  Grade the **shape** only, as everywhere else here: how large a block is, what
  `term_seconds` a server chooses, and how it clamps a hostile `observed_floor`
  are the implementation's business (0007). Whether a uid can ever be handed out
  twice is the contract's.
- **Logs**: batch ingest returns `202` and counts accepted records; **the same
  batch replayed does not double-count** (idempotency on `record_id` — a proxy
  draining a disk buffer will resend); priority ingest returns `200`.
- **Events**: subscribe, receive a heartbeat, receive a published event, drop the
  connection, resubscribe with `last_event_id` and get either replay or
  `resync` — and assert that whichever the server chose, it did not silently skip
  events.
- **Error discipline (M11)**: malformed request → `400`; bad token → `401` with
  the contract's error envelope; and every response carries the envelope the
  document specifies.

Two obligations from PLAN §4 need real assertions rather than a status check:
- **The priority ack means durable.** Assert the record is retrievable
  immediately after the ack, through whatever read path the implementation
  exposes to the suite (define this as a suite input — a query URL — so the
  suite stays black-box).
- **Heartbeats arrive within the interval the server advertises**, so a proxy's
  staleness detection is not tripped by a healthy server.

### Proving the suite (this is the point of the phase)
Run `cmd/pdpconform` in CI against the **Hoplock Proxy repository's
`cmd/mock-control`**, which already implements the contract. A suite that has
only ever been run against the implementation it was written beside tests
agreement with itself. If the mock fails an assertion, decide honestly which of
the two is wrong: a genuine mock bug is a finding to report to the user (it is a
change in the *other* repo), and a suite bug is yours to fix.

### A note on contract versions

The vendored contract is a moving target and this phase builds the machinery,
not a snapshot. Seven revisions landed upstream in the time it took to queue
this phase — the most recent of them a **collapse** (upstream
`Hoplock/proxy#53`, merged) that deleted every superseded vocabulary from the
document and moved `info.version` **down**, from `4.3.0` to `4.0.0`. That is the
actual argument: the drift check and the conformance suite must treat a version
change as routine, and must not assume it only ever goes up. If
`make contract-sync` is painful to run twice in a week, it is wrong. Vendor
whatever is current when you run; nothing here waits for the next revision.

### The two numbers

They are not the same number and nothing here may treat them as one. The
**document** version (`info.version`, `4.0.0` as vendored) and the **negotiated
policy vocabulary** (`policy_version`, `4`) move independently, and the
contract's "Versioning" section says why: `policy_version` **governs
`/v1/authorize` and nothing else**, because that is the response the proxy
decodes strictly and so the only place an unknown field could be a dropped
restriction. Everything outside that response is outside the number. The
document names two of its own worked examples — `HostKeyReportResponse.cache`,
a field on a different endpoint, and `POST /v1/uids/lease`, a whole endpoint —
and a **tightening** is a third kind of case: making an existing parameter
required adds no field and changes no field's meaning, so it is not expressible
through the version at all and is announced as a break instead
(`params.username` is the one in force).

So the relationship is not "the document only ever grows what the number gates".
A document change can narrow what a conformant server may answer with, can add a
whole endpoint the number says nothing about, and — as `#53` showed — can move
`info.version` backwards while `policy_version` stands still at `4`. Do not
derive one number from the other, do not assert a relationship between them, and
do not let the drift check key off `policy_version`: the checksum in
`contract/UPSTREAM` is what catches a changed document. Both numbers above are
what upstream carries today and neither is a target to pin — read them out of
the document you vendor.

**Removing superseded versions is not removing versioning.** `#53` deleted the
older *vocabularies*; the mechanism that carries the *next* one is intact and is
this suite's business as much as it ever was. `policy_version` is still on the
wire, still required, still honoured, and the MUST-NOT-answer-above rule still
stands — which is exactly why the negotiation assertions above are not optional.
A suite that reads the collapse as "there is only one version now, so there is
nothing to negotiate" would delete the only assertion that catches a server
breaking a fleet mid-upgrade.

## Out of scope
- Implementing any endpoint here (0007 onwards). The suite is written before the
  server exists, on purpose.
- Changing the contract (M1). If it is ambiguous, record the ambiguity in your
  learnings and tell the user — an ambiguity in a contract between two
  components is a finding, not something to resolve unilaterally.

## Acceptance criteria
- `make contract-check` passes on a clean tree and **fails** if a byte of
  `contract/control.yaml` is changed (test this, don't assume it).
- **The v4 surface is graded, not just parsed.** The suite asserts
  `POST /v1/capabilities/report` (a recorded report answers `accepted: true`, and
  `report_after_seconds` bounds how long a proxy may wait before re-observing —
  sooner is always allowed, later is not) and the absent-value default of every
  v4 policy field — an authorize
  response naming no `enforcement` object means proxy-side enforcement on both
  axes, no `session_deadline` means no deadline, absent
  `require_session_capture` means `false`, and absent `concurrency` means
  uncapped. An absent-value assertion that passes vacuously is the failure mode
  here: assert the *default*, not merely that the field may be missing.
- **The uid-lease invariant is graded across leases, not within one.** The suite
  proves that repeated leases for one target never overlap — with an abandoned
  block and an expired block among them, and with two different `proxy_id`s —
  and that `observed_floor` can raise the cursor but never lower it. A suite that
  leases once and checks the block looks fine is exactly the suite that lets a
  uid be granted twice.
- `internal/contract` compiles, and the enum test passes against the document.
- `make conform BASE_URL=... ` runs the suite and reports per-assertion results
  with a non-zero exit on any failure.
- **CI runs the suite against Hoplock Proxy's mock server and it passes** —
  or, if it does not, the PR documents exactly which assertion the mock fails and
  why the suite is right.
- The suite's expectation file is documented well enough that phase 0017 can
  point it at the real server with no code changes.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0002-contract-vendoring-and-conformance-learnings.md`. Summary
block MUST give: the vendored contract's upstream commit, whether types are
generated or hand-written and how to regenerate, the `internal/contract` type
names per endpoint, how to run the suite locally, the suite's expectation-file
format, and any contract ambiguity you found. Every subsequent phase reads this
summary to know what "correct" means.
