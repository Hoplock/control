# 0022 — Audit of 0019, part 3: the policy lifecycle

## Read first
- `docs/PROTOCOL.md`: the session workflow, §3, §4 and §5.
- `docs/PLAN.md`:
  - §2: **M3**, **M4**, **M10**, **M15**, **M16**, **M17**, **M21** and **M23**;
  - §3: `internal/authoring`, `internal/decision` and `cmd/policyctl`;
  - §5.3: compilation;
  - §5.4: cache hints;
  - §5.5: external context in the record;
  - §5.6: the policy lifecycle. All of §5.6 was written by 0019, so all of it is a claim.
- `docs/learnings/`: read every summary. Read
  `0019-northbound-api-and-policy-lifecycle-learnings.md` **whole**: it is the
  claim set, not the truth. Read `0004` (`ext.PolicyValidator`), `0005` (the
  engine, its rejections, and time as an input), `0008` (decision records),
  `0012` (grants in the record) and `0013` (external context in the record)
  where an item below touches them.
- The two earlier audits' learnings, `0020-audit-0019-…` and `0021-audit-0019-…`:
  what they changed. Do not re-audit their files.
- `prompts/implemented/0019-northbound-api-and-policy-lifecycle.md`, by heading
  (`grep -n '^##\|^###'`):
  - "Policy lifecycle";
  - "Can this policy actually be satisfied? (M17)";
  - "A cache hint that outlives what gated it (0005)";
  - "Explain a decision (M4)";
  - the algorithm section's publish-time items (the per-build finding, the impact preview, `target.algorithm_policy_unmet`);
  - "`cmd/policyctl`" and "Errors are machine-readable (M21)";
  - the matching acceptance criteria.
- `ext/policy.go`: `PolicyValidator`, `PolicyDocument`, `FindingSeverity` and `Finding`.

## Why this phase exists
0019 was built in one session whose context was compressed twice, so
everything it states is a claim to verify. Part 1 (`0020`) explains this in
full. This is part 3 of four:

| Phase | Area |
| --- | --- |
| 0020 | the south-bound surface and the decision path |
| 0021 | the audit store, retention, fleet configuration, the store and the daemon |
| **0022** | the policy lifecycle (this phase) |
| 0023 | the north-bound surface, and the record as a whole |

This area carries the session's one confirmed late defect: a validator was
never handed the version it was activating. It was found and fixed only after
the second compression. It also carries the change 0019 made to a public
package's **meaning** with no API diff: `ext.FindingWarning` used to block
nothing, and now gates activation until it is accepted. Hoplock Enterprise
implements that interface, so a wrong call here reaches another repository.

## Objective
Verify 0019's policy lifecycle at four levels: architecture, implementation,
tests and documentation. That covers validation and the publish-time report,
upload, diff, activation and rollback, apply, simulation (dry-run and replay),
explain, and `policyctl`. Fix every defect, and record a verdict for every item
below.

## How to find the work
Base `7e3d002`. Find the merge with `git log --oneline origin/main --grep='#69'`,
then use `git diff 7e3d002 <merge> -- <paths>` path by path. Audit the code as
it is **now**.

## In scope

### Paths
- `internal/authoring/`, all of it: `report.go`, `checks.go`, `service.go`, `diff.go`, `simulate.go` and the tests.
- `internal/decision/`: `replay.go`, `explain.go` and their tests. `StoredActiveMapping` is in scope; the authorize path was part 1's.
- `cmd/policyctl/`: `main.go`, `main_test.go` and `README.md`.
- `ext/policy.go`: the doc comments 0019 changed.
- `internal/daemon/north.go`: how the authoring service is built, its validators and its interpreters.

### What to check
Give each item a verdict (**confirmed**, **wrong → fixed** or **open**) with
evidence: a file and line, a test name, or command output.

1. **The severity model, as architecture.** 0019 made a definite shortfall a **warning** that gates activation until it is accepted by id, and the absence of proof **advice**, which never gates. The prompt says "warn, do not refuse".
   - Decide whether gating on an unaccepted warning is the override the prompt asks for, or a refusal by another name.
   - Check it against M17's fail-safe paragraph: a stale record must never make an unprobeable appliance unauthorable.
   - Check the claim that Control's own checks never produce `blocking`.
   - Record the verdict and its reasoning. If you would change it, write the proposal in the PR for the user to decide. It is a cross-repo change (item 9).
2. **Finding ids.**
   - An id is `code/part/part…` for Control and `code/provider/path` for a validator, and it must be stable for one bundle against one fleet.
   - Check that no part is derived from a message, a timestamp, map iteration order, or anything that varies between two runs on the same state.
   - Run validation twice on the same state and compare the ids.
   - Accepting an id that no longer arises must accept nothing, and must not be an error.
3. **Each of Control's checks** (`authoring.ControlCodes`). For each code, find the check, the severity it produces, and a test that exercises its positive and negative case. Check satisfiability against **both** capability sources (proxy and target, M17). Proxies that declared nothing must be left out of every per-build judgement.
4. **The interpreter check.** The shipped list, its configuration (`policy.interpreters` replaces it whole), and the rungs it applies under (`account-restricted`, `account-confined`). The message must say **why** the named executable weakens the claim, as the prompt requires.
5. **The cache-hint check.** Compare it with the prompt's section, item by item: a hint on a rule gated by a condition the cache key does not carry, on a route that no grant and no duration bounds. Look for false negatives first, because a missed case is the costly one here.
6. **The algorithm findings at publish time.**
   - A floor level a build did not declare is a warning.
   - An axis a ban empties on a build is a warning, one per rule, proxy and axis.
   - A declaration that cannot judge a ban is advice.
   - A ban that nothing offers is a warning.
   - A target observed below the floor is a warning.
   - A target with no fresh observation is advice, and is never shown as met.
   - A stored `target.algorithm_policy_unmet` is a warning, under the profile the record names, over `authoring.Options.UnmetWindow` (30 days by default).
   - Check each against the prompt's algorithm section.
7. **The lifecycle.**
   - An upload must dedupe by digest, retry the version-number race, and audit inside its transaction.
   - Activation must recompute the report against the fleet as it is now, refuse blocking findings and unaccepted warnings, and audit with `before`, `after`, `accepted` and `rollback`.
   - Activating the active version must change nothing and record nothing.
   - Apply must store an inactive version when its warnings are unaccepted, and name that version in the refusal.
   - A stored version this release no longer compiles must be refused, never served.
   - Concurrent activations must leave exactly one active version (`PolicyBundles().Activate` is part 2's; here, prove it from the service).
8. **Validators.**
   - Every registered `ext.PolicyValidator` must run on validate, upload and activate.
   - A validator error must be an outage (`5xx`), never a refusal.
   - `PolicyDocument.Ref.Version` must be empty before upload and the stored version at activation. That was fixed late; confirm the test proves it.
   - `ActiveVersion`, `AuthorID` and `ProposedAt` must be set correctly.
   - A validator's `Detail` and `Path` must reach the finding.
9. **`ext/policy.go`'s new meaning.**
   - Confirm the doc comments match the behaviour exactly.
   - Confirm release `v0.6.0`'s `### Breaking` says what changed.
   - Confirm control#69's `## Cross-repo impact` told `hoplock/enterprise` all of it.
   If anything was wrong or missing, the correction travels as this phase's own Cross-repo impact and a new downstream sync.
10. **Diff.**
    - The rule-level answer must cover added, removed, changed, moved and document-level changes.
    - Check that `Moved` is right when rules are inserted, removed and reordered at once.
    - The text diff must be bounded (`maxEdit`, `maxTrace`) and must say when it was omitted.
    - A source that does not parse must be refused. Diffing against version `0` must show everything as added.
11. **Replay: pure and total.**
    - Inputs must come only from the stored record, at the instant it was asked.
    - Nothing may read a clock, or call a provider (a replay never re-probes).
    - Nothing may re-read the fleet, a subject or a target. A relabel is simulated only through an explicit label override.
    - Records must be read through `ListRange`, bounded by `authoring.MaxReplayRecords`, and `truncated` must be reported.
    - A record that cannot be read must be listed as unreplayable with its reason, never skipped.
    - Flips must be grouped by direction and by the old and new rule.
    - Prove purity with a test that fails if a clock read or a store read is added, or show why one cannot exist, and say so.
12. **Dry-run.** Each `DryRunInput` field must map to the engine input it claims, with grants and external context among them. Check the defaults of every field a case may omit.
13. **Explain.**
    - The story must come **only** from the record: the grant is taken from `inputs.grants[]` by the id `explanation.grant` names, and the grant table is never joined.
    - `mapping.version` must be the mapping the decision was taken under, and `mapping.active` the one active now.
    - An unexplainable record must be `500 decision_unexplainable` and logged at error. Find every way a record can be unexplainable, and test each.
    - `""` must never be looked up as a session.
14. **`policyctl`.**
    - Every command and flag in its README must exist, and none may exist undocumented.
    - Exit codes are `0`, `1` for a refusal the server answered, and `2` for usage or transport. Prove each with a test.
    - The TSV output must be stable, one record per line, and `-json` must be valid JSON.
    - It must hold an API token and never a database credential.
    - Check the claim that apply can carry `-accept` lines forward in CI.
15. **The record, for this area.** Check every claim about this area in:
    - `docs/learnings/0019-…-learnings.md`: the summary's lifecycle, severity, simulation, explain and policyctl lines, "The publish-time report" and "Simulation and explain";
    - `docs/PLAN.md` §5.6, M4, M17's added paragraph, §3's `internal/authoring`, `internal/decision` and `cmd/policyctl` entries, and M21's code registries;
    - the matching `CHANGELOG.md` `v0.6.0` bullets.

## Out of scope
- Parts 1, 2 and 4's files: the north-bound handlers that call this package are 0023's.
- New behaviour. Item 1's design question, if you would change the answer, is a proposal for the user.
- Editing merged learnings files.

## Acceptance criteria
- Every item has a verdict with evidence in this phase's learnings, and item 1's verdict states its reasoning.
- Every behaviour fix comes with a test that failed before it.
- For items 2, 7 and 11, the learnings record the mutation or double run that proved the guarding test.
- `go build`, `go vet`, `go test ./...` and `-race` on changed packages pass, and `golangci-lint` at CI's pin reports 0 issues.
- `make release-check` passes. A change to `ext/` is reported by it and listed under `### Breaking` when it is incompatible.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0022-audit-0019-policy-lifecycle-learnings.md`.

Its summary block MUST give:
- the findings, by verdict and severity;
- each fix, by commit;
- the severity-model verdict;
- what was confirmed, by item;
- what is left open, and why;
- each correction to a 0019 claim.

Its Details carry the verdict table.

**Release.** The next MINOR after the latest tag (M23), with a `CHANGELOG.md`
section.

**Cross-repo impact.** `ext.PolicyValidator` is a shared surface
(`docs/CROSS-REPO-PROTOCOL.md` §1). Any change to its behaviour or its doc
comments, or any correction to what control#69 told `hoplock/enterprise`, owes
the obligations under `## Cross-repo impact` and a queued downstream sync
(§4.1, §4.3). Otherwise write **"None"** for each repository.
