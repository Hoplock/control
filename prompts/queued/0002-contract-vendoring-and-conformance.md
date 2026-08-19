# 0002 — Contract vendoring & conformance harness

## Read first
- `docs/PROTOCOL.md` — session workflow, especially §3 ("never edit
  `contract/`") and §9.
- `docs/PLAN.md` — especially **§2 (M1)**, §4 (the endpoint table and the two
  obligations the suite must grade).
- `docs/learnings/` — read summaries; open `0001` (Makefile targets, CI shape).
- In the **bastion repository**: `api/management.yaml` and `api/README.md`.
  Read the ground rules and the endpoint table; read individual schemas as you
  need them. Do not read the bastion's Go code.

## Objective
Bring the contract into this repo as a **vendored, verifiable artifact**, and
build the **black-box conformance suite** that decides whether an implementation
of it is correct. Everything after this phase is graded by what you build here,
so the suite's honesty matters more than its size.

## In scope

### Vendoring (M1)
- `contract/management.yaml` — a byte-for-byte copy of the bastion repo's
  `api/management.yaml`, plus `contract/UPSTREAM` recording the source
  repository, the commit SHA, and the date it was taken.
- `make contract-sync` — fetches a given ref from the bastion repo, replaces the
  copy, and rewrites `contract/UPSTREAM`. Takes the ref as a variable so a
  session can pin a specific commit.
- `make contract-check` — recomputes the copy's checksum and fails if it does not
  match what `contract/UPSTREAM` records. This is what catches a local edit. Wire
  it into CI as its own job so the failure names itself.
- `contract/README.md` — one screen: this directory is generated, here is how to
  change the contract (in the bastion repo, then sync), here is why editing it
  locally is the specific failure this rule prevents.

### Generated types (`internal/contract`)
- Go types for every payload in the document, and the server-side handler
  interfaces they imply. Generate them if you can do so reproducibly (`make
  generate`, checked in, CI verifies regeneration is a no-op); hand-write them
  only if generation costs more than it saves, and say which you chose and why in
  your learnings.
- Named constants for every enum, and a test asserting they match the document —
  the bastion repo does exactly this and it is what catches an enum drifting.
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
- **Host keys**: first sighting and a known key.
- **Logs**: batch ingest returns `202` and counts accepted records; **the same
  batch replayed does not double-count** (idempotency on `record_id` — a bastion
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
- **Heartbeats arrive within the interval the server advertises**, so a bastion's
  staleness detection is not tripped by a healthy server.

### Proving the suite (this is the point of the phase)
Run `cmd/pdpconform` in CI against the **bastion repository's
`cmd/mock-management`**, which already implements the contract. A suite that has
only ever been run against the implementation it was written beside tests
agreement with itself. If the mock fails an assertion, decide honestly which of
the two is wrong: a genuine mock bug is a finding to report to the user (it is a
change in the *other* repo), and a suite bug is yours to fix.

## Out of scope
- Implementing any endpoint here (0006 onwards). The suite is written before the
  server exists, on purpose.
- Changing the contract (M1). If it is ambiguous, record the ambiguity in your
  learnings and tell the user — an ambiguity in a contract between two
  components is a finding, not something to resolve unilaterally.

## Acceptance criteria
- `make contract-check` passes on a clean tree and **fails** if a byte of
  `contract/management.yaml` is changed (test this, don't assume it).
- `internal/contract` compiles, and the enum test passes against the document.
- `make conform BASE_URL=... ` runs the suite and reports per-assertion results
  with a non-zero exit on any failure.
- **CI runs the suite against the bastion repo's mock server and it passes** —
  or, if it does not, the PR documents exactly which assertion the mock fails and
  why the suite is right.
- The suite's expectation file is documented well enough that phase 0014 can
  point it at the real server with no code changes.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0002-contract-vendoring-and-conformance-learnings.md`. Summary
block MUST give: the vendored contract's upstream commit, whether types are
generated or hand-written and how to regenerate, the `internal/contract` type
names per endpoint, how to run the suite locally, the suite's expectation-file
format, and any contract ambiguity you found. Every subsequent phase reads this
summary to know what "correct" means.
