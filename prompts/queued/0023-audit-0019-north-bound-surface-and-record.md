# 0023 — Audit of 0019, part 4: the north-bound surface, and the record as a whole

## Read first
- `docs/PROTOCOL.md`: the session workflow, §3, §4, §5 and §6.
- `docs/CROSS-REPO-PROTOCOL.md`: §1 (the shared surfaces), §4.1 and §4.3. This
  phase checks what control#69 told `hoplock/enterprise`.
- `docs/PLAN.md`:
  - §2: **M2**, **M4**, **M8**, **M11**, **M15**, **M16**, **M17**, **M18**, **M21** and **M24**, and the register itself;
  - §3: `internal/httpapi/north`, its route families and the host-routes paragraph;
  - §5.6;
  - §6: RBAC;
  - §7: the query surface;
  - §10: the phase table, the renumbering notes and the mapping table.
- `docs/learnings/`: read every summary. Read
  `0019-northbound-api-and-policy-lifecycle-learnings.md` **whole**: it is the
  claim set, not the truth. Read `0011` (the route table, RBAC and the
  middleware), `0012` (grants on this surface), `0013` (the push and the
  bindings), `0015` (host routes) and `0018` (the cross-tenant class) where an
  item below touches them.
- The three earlier audits' learnings (`0020`, `0021` and `0022`, each named
  `-audit-0019-…`): their corrections, which this phase must find reflected
  in the record.
- `prompts/implemented/0019-northbound-api-and-policy-lifecycle.md`, by heading
  (`grep -n '^##\|^###'`):
  - "The north-bound listener";
  - "Inventory";
  - "Audit query & operator actions";
  - "What is extending this deployment (M15)";
  - "External access context: providers, scope bindings, and why a grant exists";
  - "Tenancy on the surface (M18)";
  - "Errors are machine-readable (M21)";
  - the runbook items of the algorithm section;
  - the acceptance criteria and "Definition of Done & hand-off".

## Why this phase exists
0019 was built in one session whose context was compressed twice, so
everything it states is a claim to verify. Part 1 (`0020`) explains this in
full. This is the last of four:

| Phase | Area |
| --- | --- |
| 0020 | the south-bound surface and the decision path |
| 0021 | the audit store, retention, fleet configuration, the store and the daemon |
| 0022 | the policy lifecycle |
| **0023** | the north-bound surface, and the record as a whole (this phase) |

It runs last for two reasons. The north-bound handlers sit on top of everything
the first three audited. And it reconciles the **record** (`docs/PLAN.md`,
`CHANGELOG.md`, and what control#69 told Hoplock Enterprise) after the earlier
three have corrected what they found.

## Objective
Verify 0019's north-bound surface (routes, access classes, permissions, error
codes, operator audit, and every handler 0019 added) at the architecture,
implementation, test and security levels. Then verify that the record as a
whole is true of the code as it stands after parts 1–3. Fix every defect, and
record a verdict for every item below.

## How to find the work
Base `7e3d002`. Find the merge with `git log --oneline origin/main --grep='#69'`,
then use `git diff 7e3d002 <merge> -- <paths>` path by path. Audit the code as
it is **now**.

## In scope

### Paths
- `internal/httpapi/north/`, as it relates to 0019:
  - `routes.go`, `server.go` and `errors.go`;
  - `policy.go`, `inventory.go`, `audit.go`, `operator.go`, `extensions.go`, `ca.go` and `grants.go`;
  - the tests `lifecycle_test.go`, `extensions_test.go`, `accesscontext_test.go` and `north_test.go`.
- `internal/identity/rbac.go` and its tests: the six new permissions and the role grants.
- `internal/audit/operatorevent.go`: the event names and attributes. Their callers are this phase's.
- `ext/` and `server/`, read only, to confirm what a host can and cannot collide with.
- The record:
  - `docs/PLAN.md`: every passage 0019 added or changed. Read `git diff 7e3d002 <merge> -- docs/PLAN.md`, then the corrections parts 1–3 made;
  - `CHANGELOG.md` `v0.6.0`;
  - control#69's description, especially `## Cross-repo impact`;
  - `prompts/downstream/queued/control-PR#69-northbound-api.md`, or `prompts/downstream/implemented/…` if its sync has already run.

### What to check
Give each item a verdict (**confirmed**, **wrong → fixed** or **open**) with
evidence: a file and line, a test name, or command output.

1. **The route table.**
   - Compare every route 0019 added (method, pattern, access class and permission) with the table in the 0019 learnings, row by row. Every difference is a finding in one or the other.
   - Every route must sit under `/api/v1/tenants/{tenant}/` as `AccessTenant`. Control must serve no cross-tenant route of its own (M24).
   - The isolation tests must enumerate the router rather than a list. Prove they fail on a route registered without isolation.
2. **M2.** `TestNoContractRouteIsReachableOnThisListener` and the south-bound `TestNoNorthBoundRouteIsReachable` must both still hold, and must cover every route 0019 added.
3. **RBAC, as least privilege.** For each new permission (`target:write`, `audit:retain`, `session:revoke`, `access-context:read`, `access-context:write` and `extension:read`), and for each role that gained one, decide whether the grant is right:
   - `policy-author` and `fleet-admin` gaining `target:write`;
   - `grant-admin` gaining `session:revoke`;
   - `audit:retain` and `access-context:write` held by `admin` alone;
   - every reading role gaining `access-context:read` and `extension:read`.
   Record the reasoning, and confirm the role table test holds the result.
4. **Every write is audited.**
   - For each mutating route 0019 added, find the `operator.<noun>.<verb>` record it writes (or `access_context.binding_*` for bindings). Confirm it is written **in the same transaction as the change** wherever one exists, and names the actor, the before and the after.
   - List any write that records nothing, or records outside its transaction, and fix it.
   - The routes to check:
     - policy: upload, activate and apply;
     - targets: put, delete and relabel with `apply`;
     - subjects: put and delete;
     - role bindings: add and remove;
     - the claim mapping put;
     - proxies: enrollment and withdrawal;
     - fleet configuration: publish and rollback;
     - revocations;
     - both retention routes;
     - binding put and delete.
5. **Error codes and statuses (M21, M11).**
   - Every new code must be in `north.AllCodes`, and the test that holds it must pass.
   - Each code must answer the status the 0019 learnings and `CHANGELOG.md` claim: `policy_invalid` `422`, `findings_not_accepted` `409`, `policy_blocked` `422`, `decision_unexplainable` `500`, `config_key_not_fleet_owned` and `config_value_invalid` `422`, `host_key_unresolved` `409`, `unavailable` `503`.
   - No handler may answer `401` (the middleware's alone).
   - Every error must carry its typed parameters and the correlation id.
6. **Handlers, one by one.** For each handler 0019 added:
   - the tenant is read from the request context only, never the path, the body or a query parameter;
   - the input is validated, and every refusal names its field;
   - every list is bounded (`limitParam`'s cap), and time parameters are parsed strictly;
   - the response shape matches the learnings and §5.6.
   Pay particular attention to:
   - `targets/relabel`: the diff, the replay and `apply`;
   - `revocations`: through `revoke.Operator`, never beside it, with `covers_host_key_decisions` on every receipt, and the host-key withdrawal resolved from the record;
   - the fleet configuration publish: fleet-owned keys only;
   - the audit query routes, whose filters must map exactly to the readers part 2 audited;
   - the binding routes.
7. **The binding route's path.** 0019 moved it to `…/access-context/providers/{provider}/binding`, saying `…/access-context/bindings/{provider}` was an ambiguous pattern beside `…/access-context/{provider}/push`. Reproduce the conflict, or show it does not exist. Either way record the verdict, and do not move the route without the user, because Enterprise was told this path.
8. **What a host can no longer take.** Confirm a host route on any path family 0019 added, and a host code equal to any new Control code, is refused at start-up (`server` tests). The control#69 description says both.
9. **The emergency runbook.** `TestTheEmergencyRunbookIsCallableAndAudited` must drive the ban, `cache_invalidate all`, the negotiated query and `session_kill`, using the roles that hold each permission, and must assert the audit record of each step.
10. **Security review of the whole north-bound diff.** Look for:
    - authorisation bypass, and cross-tenant reads by id (a decision, a session, a certificate serial or a record id from another tenant);
    - injection through query parameters;
    - unbounded reads;
    - body size on every new route;
    - information disclosure in error messages.
    Run `go test -race` on every package in scope.
11. **The register and the plan.**
    - Every decision 0019 amended (M2, M4, M8, M16, M17 and M21) must say so in its **first line** where the protocol requires it.
    - Each register row's status and "Rendered in" must be true.
    - §3's entries must describe the code as it is.
    - §5.6 must survive parts 1–3's corrections.
    - §10's row for 0019 and the rows for 0020–0028, the renumbering notes, and the composed mapping table must be consistent (`protocol_test.go`).
    - There must be no dated layers and no "now"/"previously" narration (`docs/PROTOCOL.md` §3).
12. **`CHANGELOG.md` `v0.6.0`.**
    - Check every bullet against the code after parts 1–3. A released section is never edited (M23), so a wrong bullet is corrected in **this** phase's release section, saying what `v0.6.0` stated wrongly.
    - Confirm `### Breaking` named everything incompatible.
13. **What control#69 told Hoplock Enterprise.**
    - Check each of its eight obligations against the code. The eighth is the renumbering that queued these audits.
    - Check the queued sync file carries them faithfully.
    - Never edit a merged request file. A wrong or missing obligation is this phase's own `## Cross-repo impact` plus a new downstream sync naming the correction. Check whether Enterprise's sync of control#69 has already run (`docs/CROSS-REPO-PROTOCOL.md` §4.3), and say so in the obligation.
14. **The 0019 learnings as a whole.** List every claim parts 1–3 did not already rule on, and rule on it. The result is a complete account of which 0019 statements are true. Record corrections here; the 0019 file stays as it is.
15. **Carried from 0021** (found there, on this phase's surface, and left to it).
    - **Lists without a bound or a cursor.** `GET …/subjects`, `…/policy/bundles`, `…/fleet/algorithms`, `…/fleet/target-observations` and `…/fleet/config/rollouts` each answer a whole tenant. The size depends on the estate, not on the caller, but nothing caps it. Propose the shape `store.Decisions().ListRange` already uses (a limit plus `truncated`), or say why these lists are exempt.
    - **`GET …/audit/negotiated` gives no truncation signal.** Since 0021 its limit counts sessions, but the runbook's next step ends exactly the sessions it lists. A caller cannot tell a complete answer from one cut at the limit. Propose the signal.
    - **A refusal quotes an internal name.** A retention pass refused at the head answers `400` with the message `this query cannot be answered: store.Audit.RetainPrefix: invalid: …`. The error is right, but the message names a Go function. Check every `auditFailure` refusal for the same leak.

## Out of scope
- New behaviour. A design question that needs more than a local fix is proposed in the PR for the user to decide.
- Re-auditing parts 1–3's files, beyond confirming that their corrections reached the record.
- Editing merged learnings, released `CHANGELOG.md` sections, or merged request files.

## Acceptance criteria
- Every item has a verdict with evidence in this phase's learnings, and item 14's list is complete.
- Every behaviour fix comes with a test that failed before it.
- For items 1 and 4, the learnings record the mutation that proved the guarding tests.
- `go build`, `go vet`, `go test ./...` and `-race` on changed packages pass, and `golangci-lint` at CI's pin reports 0 issues.
- `make contract-check`, `license-check` and `release-check` pass, and `make conform` passes on a fresh database.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0023-audit-0019-north-bound-surface-and-record-learnings.md`.

Its summary block MUST give:
- the findings, by verdict and severity;
- each fix, by commit;
- the RBAC verdicts;
- the list of writes and their audit records;
- the corrections to `v0.6.0`'s changelog;
- every correction owed to `hoplock/enterprise`;
- across all four audits, the 0019 claims that were wrong, so that the next session reads one place.

Its Details carry the verdict table.

**Release.** The next MINOR after the latest tag (M23), with a `CHANGELOG.md`
section, including any correction to what `v0.6.0` stated.

**Cross-repo impact.** State it per repository (`docs/CROSS-REPO-PROTOCOL.md`
§4.1). Corrections to control#69's obligations, and any change to a shared
surface, owe a queued downstream sync (§4.3). Otherwise write **"None"**.
