# 0020 — Audit of 0019, part 1: the south-bound surface and the decision path

## Read first
- `docs/PROTOCOL.md`: the session workflow, §3 (doing the work), §4 (the
  Definition of Done) and §5 (the learnings file).
- `docs/PLAN.md`:
  - §2: **M1**, **M5**, **M11**, **M17**, **M22** and **M23**.
  - §4: the contract, the vocabulary tiers, the capability report and the
    key-exchange observation.
  - §5.2: the credential ladder, `brokered-certificate`, the algorithm floor and
    bans, and the emergency runbook's first step.
  - §6: the per-tenant CA and brokered certificates.
  - §7: ingest, the all-or-nothing batch, the two sanctioned `400` classes, and
    session-less records.
- `docs/learnings/`: read every summary. Read
  `0019-northbound-api-and-policy-lifecycle-learnings.md` **whole**: it is the
  claim set this phase checks, not a source of truth. Read `0007` (south-bound
  authentication), `0008` (authorize and the vocabulary gate), `0010` (audit
  ingest) and `0011` (the CA) in full where an item below touches them.
- `prompts/implemented/0019-northbound-api-and-policy-lifecycle.md`. It is
  1900 lines, so list its headings (`grep -n '^##\|^###'`) and read only these:
  - "The records the proxy emits since proxy phase 0043" (the ingest half);
  - "Brokered certificates reach a proxy";
  - "Algorithm floor and bans reach a proxy";
  - "What a proxy could not deliver";
  - the acceptance criteria that name those sections.
- `contract/control.yaml` (vendored, read-only, M1) for every wire shape below.
  The shapes are checked against the vendored document, never against
  memory.

## Why this phase exists
Phase 0019 (`Hoplock/control#69`) was implemented in one session whose context
was **compressed twice**. A compression replaces the earlier work with a
summary, so whatever the session did after each one was built on that summary
rather than on the code.

That session found one defect of exactly this kind before opening its PR: a
registered policy validator was never told the version it was judging (commit
`fix(authoring): tell a validator the version it is activating`). It also
corrected several of its own learnings claims, among them the drift feed's
filters, the retention body and the finding id, before committing them. There
is no reason to think those were the only ones.

So every statement 0019 makes is a **claim to verify**, not a fact. That covers
its code comments, `docs/PLAN.md`, its learnings, `CHANGELOG.md` `v0.6.0`, the
PR description, and the Enterprise sync it queued. Four phases audit it, split
by area so that each fits one session:

| Phase | Area |
| --- | --- |
| **0020** | the south-bound surface and the decision path (this phase) |
| 0021 | the audit store, retention, fleet configuration delivery, the store and the daemon |
| 0022 | the policy lifecycle |
| 0023 | the north-bound surface, and the record as a whole |

This one runs first because a wire defect here reaches a proxy, and through it
a user's session.

## Objective
Verify 0019's south-bound and decision-path work against the code, at four
levels:
- **architecture:** does it keep M1, M5, M11, M17 and M22?
- **implementation:** does each function do what its comment says?
- **tests:** would each test fail if the rule it guards were removed?
- **documentation:** is every claim about this area true?

Fix every defect you find, and record a verdict for every item below.

## How to find the work
0019 merged onto `main` after `7e3d002`. Find its merge with
`git log --oneline origin/main --grep='#69'`, or with the commit that added
`prompts/implemented/0019-…`:
`git log --diff-filter=A --oneline -- 'prompts/implemented/0019-*'`.

Then `git diff 7e3d002 <that commit> -- <paths>` shows what it changed, path
by path. Audit the code **as it is now**, and use the diff only to find what
0019 touched. Never read the whole diff at once: it is 22,000 lines, and this
phase's share is about a quarter of it.

## In scope

### Paths
- `contract/` and `contract/UPSTREAM`: check the vendoring (`make contract-check`). Never edit them (M1).
- `internal/contract/`: the vocabulary tiers (`enums.go`), the serial helpers (`serial.go`), `Declares()` (`resolve.go`), the types and enum tests, and the `testdata/upstream-capabilities.json` fixture.
- `internal/httpapi/south/`: `credentials.go`, `errors.go`, `logs.go`, the capability report handler, and the discipline tests. `config.go` is 0021's.
- `internal/credential/`: `session.go`, the ladder renderer, and the `UpperBound` path in `ca.go`.
- `internal/policy/model` and `internal/policy/compile`: `algorithm_floor`, `algorithm_bans` (per route and top-level), and the `brokered-certificate` entry and its refusals.
- `internal/decision/`: `vocabulary.go`, `capability.go` and `snapshot.go`, and where the authorize path calls them. `replay.go` and `explain.go` are 0022's.
- `internal/fleet/`: `algorithms.go` and `observations.go`, with the store repositories they use (`proxy_algorithm_declarations`, `target_kex_observations`, `target_capabilities`).
- `cmd/pdpconform/`: every check file, `expectations.go`, `testdata/`, `README.md`, and the two conformance jobs in `.github/workflows/ci.yml`.

### What to check
Give each numbered item a verdict: **confirmed** (with evidence), **wrong →
fixed** (with the commit), or **open** (with the reason). Evidence is a file and
line, a test name, or command output. A sentence in a doc is never evidence.

1. **M11: `401` only by decision.**
   - List every path on the south-bound listener that can answer `401`. The
     learnings name three deniers: the user's, the proxy token's, and
     `refuseToMint` for a certificate issuance.
   - Confirm the source test that forbids a fourth (`TestOnlyOneFunctionCanProduceA401` or its successor) knows about all three, and fails if a handler builds a `401` itself.
   - A vocabulary mismatch must be a `5xx` naming both numbers, and an absent `policy_version` must be a `400`.
2. **Never a thinned answer.**
   - `requiredVersion` must answer `6` for a floor or any ban, `5` for a `brokered-certificate` rung, and `4` otherwise, and the version check must run **before** the capability checks.
   - A floor level the asking proxy did not declare must never be sent.
   - A ban that empties an axis of the asking proxy's declared lists must be refused to that proxy only. A declaration omitting the route's profile, or `algorithm_profiles`, must be answered as authored.
   - For each of these, prove the guarding test fails when the rule is removed (mutate the code, run the test, revert).
3. **`Declares()`.** Floor-only and profiles-only declarations must answer `false`, and nothing reads the algorithm declarations through it.
4. **Certificate issuance.**
   - The cited decision must be this tenant's and must have named a `brokered-certificate` rung. The target and username must match it, and a bound credential's proxy must equal the decision's `proxy_id`. Each mismatch must be a `401` through `refuseToMint`.
   - Exactly the submitted key must be certified. A key that is itself a certificate must be a `400`.
   - Every call must get a new serial and its own row. Nothing may be cached, by decision, by session or by key.
   - Lifetime must be `min(certificate_validity, max_certificate_validity, lifetime_seconds)`, with `lifetime_seconds` only ever an upper bound.
   - `valid_before` and `serial` must be rendered from the certificate itself.
   - No CA, or no KEK, must be a `503`. A store failure must be a `5xx`, never a `401`.
5. **The tripwire.** `TestABrokeredCertificateEntryCarriesPolicyOnly` must pin the entry to `username`, `key_type` and `lifetime_seconds`, and `credential.LadderEntry` must be the **only** renderer. Grep for any other place that builds a `TargetAuth` naming the method.
6. **Authoring the floor and bans.**
   - The compiler's refusals (`route.algorithm_floor_profile_conflict`, `algorithm_ban_empty`, `algorithm_ban_repeated`) must match the proxy's own refusals in the vendored contract, **and no more**.
   - A bundle's top-level `algorithm_bans` must be merged into every allow route's, and into no deny route.
   - Every new refusal must carry a code, a rule, a line and parameters (0005, M21).
7. **The capability report.**
   - The rung and key-exchange observations must be merged by presence, so a report replaces only what it carries.
   - The key exchange must be stored by host and port, never by platform.
   - Check the claim that an unparseable `observed_at` is a `400`, and that this is the contract's rule for this endpoint. #71's rule binds the log endpoints only.
8. **Recording declarations within M5.**
   - The decision path must write `proxy_algorithm_declarations` only when the digest changes.
   - The suppression is a per-node `sync.Map`. M5 says nothing on the decision path may be **node-local state**, so decide whether a write-suppression cache is state in that sense. Correctness must never depend on it: two nodes must both be right, at the cost of one extra write each.
   - Record the decision, and correct the plan in place if it is silent.
9. **Log ingest (#66, #71).**
   - `session_id: ""` must be accepted on every kind, on both paths.
   - Enumerate **every** code path that answers `400` on `/v1/logs/batch` and `/v1/logs/priority`. Each must be either a record this server will never store, or a bound that halving the batch cures (`audit.max_batch_records`, `south.max_log_body_bytes`). A store failure, a timeout or overload must be a `5xx`.
   - A batch must be all or nothing, and the priority ack must be durable before it is sent.
   - The body limit must apply to the two log paths and nothing else.
10. **Serials.** `contract.FormatSerial` and `ParseSerial` must be the one place a serial is rendered or parsed (grep). A uint64 must never pass through a float, in Go or in SQL.
11. **The vendored contract and its types.**
    - `make contract-check` must pass, and `contract/UPSTREAM` must name `976faaa`.
    - The enum tests must cover every enum 0019 added, `ProxyConfigReport.state` among them.
    - The `upstream-capabilities.json` fixture is said to come from the proxy's own encoder. Check its provenance comment, and that a decode/encode round trip holds.
12. **Conformance.**
    - Each new `pdpconform` case (fleet configuration, brokered certificates, algorithm floor and bans, session-less records) must grade what its name says. Read each assertion and run with `-v`. A case that passes vacuously, for example on an empty list, is a finding.
    - Run the suite against this server, after `migrate` and `seed` on a **fresh** database, and against the proxy's mock at the vendored commit (CI's two jobs show how). The config group needs a server with nothing published, and CI's `-only` list must name every group.
13. **Security.**
    - Every new south route must sit behind the proxy credential, and must read the tenant from it (M22), never from the body.
    - Request sizes must be bounded, a public key among them.
    - No SQL may be built by concatenating input. Check the array filters in particular.
    - Run `go test -race` on every package in scope.
14. **The record, for this area.** Check every claim about this area in:
    - `docs/learnings/0019-…-learnings.md`: its "#68", "#69/#72" and "#71" sections, and the summary lines that name them;
    - `docs/PLAN.md` §4, §5.2, §6 and §7;
    - the south-bound bullets of `CHANGELOG.md` `v0.6.0`.

## Out of scope
- Anything 0021, 0022 or 0023 owns: the files they name.
- New behaviour. A defect is fixed. A design question that needs more than a local fix is written up with a proposal in this phase's PR, and the user decides. Never widen silently.
- Editing `contract/`, re-vendoring, or editing the 0019 learnings or any other merged learnings file. Corrections to 0019's claims are recorded in this phase's learnings, which supersede it for the items they name. `docs/PLAN.md` is corrected in place.

## Acceptance criteria
- Every item above has a verdict with evidence in this phase's learnings.
- Every behaviour fix comes with a test that **failed before it**. Say so in the commit body.
- For items 1, 2, 4 and 9, the learnings record the mutation that proved the guarding test is not vacuous.
- `go build ./...`, `go vet ./...`, `go test ./...` and `go test -race` on every changed package all pass, and `golangci-lint` at CI's pin reports 0 issues.
- `make contract-check`, `make license-check` and `make release-check` pass, and both conformance runs pass.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0020-audit-0019-south-bound-and-decision-path-learnings.md`.

Its summary block MUST give:
- the findings, by verdict and severity;
- each defect fixed, by commit;
- what was confirmed, by item number;
- what is left open, and why;
- each correction to a 0019 claim, naming the claim.

Its Details carry the full verdict table.

**Release.** Every phase is a release (M23). This one is the next MINOR after
the latest tag (`make release-check` names it). Its `CHANGELOG.md` section lists
what a consumer can observe: fixes by identifier, behaviour changes, and
`### Breaking` where one applies.

**Cross-repo impact.** If a fix changes a shared surface
(`docs/CROSS-REPO-PROTOCOL.md` §1), state the obligations under
`## Cross-repo impact` and queue the downstream sync (§4.1, §4.3). Shared surfaces
include `ext/`, `server/`, and anything Enterprise was told in control#69's
`## Cross-repo impact`. Otherwise write **"None"** for each repository.

If you find the proxy's contract wrong or ambiguous, that is an upstream request
(§3.2). Raise it, and do not work around it.
