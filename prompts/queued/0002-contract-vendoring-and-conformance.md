# 0002 — Contract vendoring & conformance harness

## Read first
- `docs/PROTOCOL.md` — session workflow, especially §3 ("never edit
  `contract/`") and §9.
- `docs/PLAN.md` — especially **§2 (M1)**, §4 (the endpoint table and the two
  obligations the suite must grade).
- `docs/learnings/` — read summaries; open `0001` (Makefile targets, CI shape).
- In the **Hoplock Proxy repository**: `api/control.yaml` and `api/README.md`.
  Read the ground rules, the endpoint table, and — before writing any
  assertion — **"Versioning: additive fields, and a proxy that fails closed"**
  and **"Policy vocabulary v2"**. The second carries the absent-value default of
  every policy field in one table, which is what tells you whether a missing
  field in a response is a pass or a failure. Read individual schemas as you
  need them. Do not read the proxy's Go code.

  Read **"The v3→v3.1 revision"** too, and the **"Additional device fields
  (`device_field.<name>`)"** subsection under "Ephemeral accounts on devices".
  That revision is the one that breaks a naive reading of the version rules: it
  adds vocabulary and leaves `policy_version` at `3`, so the document's version
  and the negotiated version are two different numbers, and nothing here may
  assume they move together.

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
- **Vocabulary negotiation**: the same authorize request sent with
  `policy_version` set to the current version and to `1`. Assert the low-version
  answer carries **no field introduced after version 1** — or is a `5xx` naming
  the mismatch, which is the correct answer when the policy needs one. Both are
  passes; a thinned snapshot that drops a restriction and returns `200` is not,
  and neither is a `401`. This is the assertion that stops a server from
  breaking every older proxy in a fleet mid-upgrade, and no other assertion here
  catches it: a response tested only at the current version looks perfect.
- **Additional device fields** (`device_field.<name>`, contract v3.1): an
  `ephemeral-account` rung carrying device fields round-trips with the names and
  values intact and **unenumerated** — the suite asserts the shape the contract
  states (name lowercase letters, digits, hyphens and underscores and ≤64
  characters; value non-empty and ≤256 characters; ≤16 fields per entry) and
  asserts nothing about which names are meaningful, because the contract does not
  say and a suite that hard-codes `vdom` will fail the first customer driver.
  Assert too that `policy_version` is still `3` on such a response: a suite that
  expects a bump here is asserting a rule the contract does not have.
- **Host keys**: first sighting and a known key.
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
not a snapshot. Device provisioning and the credential ladder have landed
upstream (v3), the `device_field.` namespace after them (v3.1, upstream
`Hoplock/proxy#21`, merged), and enforcement points and session bounds are
queued behind those (v4) — so the drift check and the conformance suite must
treat a version bump as routine. If `make contract-sync` is painful to run twice
in a week, it is wrong. Nothing here waits for the next revision; they are named
so the design is not accidentally shaped around whichever one you happen to
vendor.

Two of those numbers are not the same number, and v3.1 is what proves it. The
**document** version (`info.version`, `3.1.0` as vendored) and the **negotiated
policy vocabulary** (`policy_version`, `3`) move independently: v3.1 added
vocabulary and left `policy_version` alone, because that field numbers what a
proxy can *read* and reading did not change. So do not derive one from the
other, do not assert a relationship between them, and do not let the drift check
key off `policy_version` — the checksum in `contract/UPSTREAM` is what catches a
changed document.

## Out of scope
- Implementing any endpoint here (0007 onwards). The suite is written before the
  server exists, on purpose.
- Changing the contract (M1). If it is ambiguous, record the ambiguity in your
  learnings and tell the user — an ambiguity in a contract between two
  components is a finding, not something to resolve unilaterally.

## Acceptance criteria
- `make contract-check` passes on a clean tree and **fails** if a byte of
  `contract/control.yaml` is changed (test this, don't assume it).
- `internal/contract` compiles, and the enum test passes against the document.
- `make conform BASE_URL=... ` runs the suite and reports per-assertion results
  with a non-zero exit on any failure.
- **CI runs the suite against Hoplock Proxy's mock server and it passes** —
  or, if it does not, the PR documents exactly which assertion the mock fails and
  why the suite is right.
- The suite's expectation file is documented well enough that phase 0016 can
  point it at the real server with no code changes.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0002-contract-vendoring-and-conformance-learnings.md`. Summary
block MUST give: the vendored contract's upstream commit, whether types are
generated or hand-written and how to regenerate, the `internal/contract` type
names per endpoint, how to run the suite locally, the suite's expectation-file
format, and any contract ambiguity you found. Every subsequent phase reads this
summary to know what "correct" means.
