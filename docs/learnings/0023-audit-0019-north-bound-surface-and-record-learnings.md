# 0023 — audit of 0019, part 4: the north-bound surface and the record — Learnings

## Summary
- **Findings:** 15 items, no high-severity defect. Fixed: 5 medium (three writes recorded outside their transaction; a relabel overwrote what was written while it simulated, and concurrent writes recorded stale befores; a host route under one of Control's wildcards took that path from Control; `PUT …/subjects` stored reserved claims such as `chain_hop_proxy_id`; `audit/negotiated` gave no truncation signal), 6 low (an axis without its value was a 500; refusals quoting Go operations, package and SQL text; revocation refusals naming `body`, a blank host key a 409; an oversized body a 400; relabel changes unvalidated; a negative `through_seq` ignored), 7 test gaps a mutation passed, 7 doc errors. **Open:** none. Bounded inventory lists (item 15) were proposed, and the user had them queued as **0026**, an audit-and-plan phase that finds every list and queues the work in chunks. Table and mutations in Details.
- **Fixed, by commit:** `55a410e` host routes under Control's wildcards refused, enumerating test; `f2918e6` role table, code registry, north `401` and M2 guards; `6b55794` records in their transactions, tenant locks, the relabel, subject claims, `truncated`, every refusal (with `TestEveryWriteCommitsWithItsRecordOrNotAtAll`, which fails on `main` on exactly the four writes).
- **RBAC (item 3): every grant confirmed.** `policy-author`/`fleet-admin` `target:write` (the author already changes every decision by publishing; the fleet owns the estate it labels, and a relabel is diffed, simulable, recorded); `grant-admin` `session:revoke` (every revocation act narrows access); `audit:retain`, `access-context:write` admin-only; readers `access-context:read`, `extension:read`. Now held whole by `TestTheRoleTableIsHeldWhole` and rendered in PLAN §6.
- **Writes → records:** policy upload/activate/apply `operator.policy.uploaded`/`activated`; target put, relabel, delete `operator.target.put` (+`operator.targets.relabelled`)/`deleted`; subject put/delete; role bind/remove `operator.role.bound`/`unbound`; claim mapping `operator.claim_mapping.activated`; enrollment/withdrawal `operator.proxy.approved`/`withdrawn`; config publish/rollback `operator.fleet_config.published`/`rolled_back`; both retentions `operator.audit.retained`; bindings `access_context.binding_put`/`deleted`. All in the act's transaction (claim mapping, enrollment, config since `6b55794`) but revocations: a bus event with no transaction, recorded after it is published.
- **`v0.6.0` corrections (in `v0.10.0`):** route list omitted `PUT …/identity/claim-mapping`; `### Breaking` omitted that a host route or code on what it began serving fails start-up; three records were outside their transaction.
- **Owed to `hoplock/enterprise`** (sync queued, see PR): control#69's (3) was not true until `v0.10.0` (a literal under a Control wildcard was accepted); (5) the fleet configuration record drops `operator.unannounced` and gains `before`; (8) is stale since 0021 renumbered, and this PR moved them once more: instance identity is 0027, the console 0028, E2E 0029, post-quantum 0030, one contract version 0031. Its sync of control#69 has **not** run (still queued, no draft move).
- **0019 claims that were wrong, all four audits** (each audit's file has its detail): declarations written once per change, the serial "SQL join", the log `400` list, CI running every group, five conformance cases (0020); GIN/partial indexes usable, drift and nothing-published, retention in its transaction, publish tenants tracked, two meta-tests (0021); explain total, replay listing every unreadable record, the unmet and ban-typo warnings' reach, 13 codes, finding ids per proxy/target, `ext.PolicyValidator`'s doc (0022); every write in its transaction, relabel's apply, the host-route promise, M2's register row, §3's route sentence, `v0.6.0`'s list and Breaking, control#69 (3) and (8) (0023).
- **Decisions:** none added, amended or withdrawn; register rows M2 (now §3, §4, §10) and M16 (adds §6) corrected. **Migrations:** none. Release `v0.10.0`, `### Breaking`. **Proxy:** none owed.
- **NEXT:** 0024 builds on a surface whose every write is held by an enumerating test; a new write route fails it until listed. 0026 (bounded lists) starts from this file's proposal.

## Details

### How the audit was run

Every item was read in the code as it is on `main` after `31f5a7a` (part 3
merged), with `git diff 7e3d002 1b9bf55 -- <path>` only to find what 0019
touched. "Mutation" means: change the rule, run the package's tests, record
what fails, revert (a helper applied one replacement and restored the file).
A behaviour fix's test was also run against `main` in a worktree at
`31f5a7a`, where it failed for the reason it names.

Environment notes: as 0021's (`service postgresql start`, the password,
golangci-lint v2.14.0 built with `GOTOOLCHAIN=go1.27.0`). Export
`HOPLOCK_TEST_DSN` in the same shell, or the DB tests skip and a package
"passes" in milliseconds. `make release-check` refuses in a shallow clone:
`git fetch --unshallow --tags origin` first.

### Verdicts

| # | Item | Verdict | Evidence |
| --- | --- | --- | --- |
| 1 | The route table | confirmed; strengthened | 55 routes 0019 added (`routes.go` `policyRoutes`, `inventoryRoutes`, `auditRoutes`, plus `PUT …/identity/claim-mapping`) equal the learnings' table row by row: method, path, permission. Every one is `tenantRoute`, so `AccessTenant` under `/api/v1/tenants/{tenant}/`; `TestControlServesNoCrossTenantRouteOfItsOwn` holds M24. The isolation tests walk `f.server.Routes()`. Mutations below. |
| 2 | M2 | wrong → fixed `f2918e6` (test strength) | Both tests hold and the south one enumerates `north.Table`, so every 0019 route. The north one probed six hand-listed contract paths, missing seven of the thirteen (`/v1/credentials/certificate`, the config fetch and report, `/v1/logs/priority`, capabilities, MFA poll, events), and a contract path mounted on the router's mux beside the table passed. It now probes every path in `contract/control.yaml`, three methods each. |
| 3 | RBAC | confirmed; held `f2918e6` | Reasoning in the summary and PLAN §6. `fleet-admin` changing decisions through labels is the one grant worth a second look; it is right because target creation without labels is useless, and a split `target:label` would make adding a host a two-role act. Not held: three of five grant mutations passed every test; `TestTheRoleTableIsHeldWhole` now fails on each. |
| 4 | Every write audited | wrong → fixed `6b55794` | Claim mapping (`inventory.go`, `OperatorEvent(…, nil, …)` after `PutMapping`, itself two statements), enrollment (after `IssueGrant`) and config publish/rollback (after `PublishConfig`) recorded outside their transactions. Each now takes a recorder run inside it. The enumerating test (trigger refuses the record; xmin of act and record) failed on `main` on exactly those four and passes now; bindings by `TestABindingWriteCommitsWithItsRecordOrNotAtAll`. Revocations have no transaction (an event on the bus) and record after publishing; that is stated in PLAN §3. Every record names the actor; before/after where the act has them (asserted per write). |
| 5 | Codes and statuses | confirmed; strengthened `f2918e6` | All eight in `AllCodes`; statuses as claimed (`policyFailureFor`, `configFailure`, `revocationFailure`, `explainFailure`; `TestThePhasesErrorPathsCarryTheEnvelope`). `401` only in `enforce` and `refused` (a 0011 login refusal). Every error carries parameters where it has any and the correlation id. Gaps: nothing tied an answered code to `AllCodes`, and nothing held the `401`; both are source tests now. |
| 6 | Handlers | wrong → fixed `6b55794` | Tenant from `requireTenant` in every handler (four delegate). Shapes match the learnings (replay also has `from`/`to`). Defects: relabel's apply wrote what it read before simulating (lost update, stale before), accepted a blank or repeated target, named no missing one; `negotiated` had no truncation signal and an axis without value was a 500; `PUT …/subjects` stored reserved claims; revocation refusals said `body` and quoted `revoke:`; a blank host key was `409 no_record`; a negative `through_seq` was ignored; a body over the limit was "invalid"; store and fleet messages leaked. Fleet publish: fleet-owned keys only (`TestAPublishNamingAnythingButAFleetOwnedKeyIsRefused`). Audit filters map one to one onto the readers 0021 audited. Lists: item 15. |
| 7 | The binding path | confirmed | Registering `…/access-context/bindings/{provider}` beside `…/access-context/{provider}/push` on this `Router` fails: "both match `…/bindings/push`, neither is more specific". It is a conflict because the router mounts method-less patterns (pattern-first dispatch for M21's 404/405); method-qualified `GET`/`POST` patterns would not conflict. Not moved. |
| 8 | What a host cannot take | wrong → fixed `55a410e` | Exact patterns and mux conflicts were refused; a literal under a Control wildcard (`targets/special`, `policy/bundles/latest`, `decisions/latest`, `audit/records/x`, `access-context/bindings/push`) was accepted and took the path from Control. Refused now; `TestAHostCanTakeNoPathAndNoCodeControlServes` walks all of Control's tenant routes (literal and pattern, two methods) and `AllCodes`; `server` refuses `targets/special`, `audit/retention` and `findings_not_accepted` before reading the file. |
| 9 | The runbook | confirmed | `TestTheEmergencyRunbookIsCallableAndAudited`: policy-author applies the ban, grant-admin invalidates `all`, auditor queries, grant-admin kills; records of the activation and both revocations asserted (the query is a read). |
| 10 | Security | wrong → fixed `6b55794` | Authorisation: every route through `enforce`. Cross-tenant ids: every store read names the tenant; `TestAnIDFromAnotherTenantIsNotFound` covers decision, session, record, serial, target, subject, version. Injection: no SQL built here. Unbounded: audit reads clamped by `boundLimit`; inventory lists are item 15. Body: `withLimits` on every route (push its own). Disclosure: `this cannot be stored: ` + `err.Error()` could carry a constraint's or table's name — `store.Reason` now. Subject claims bypassed the reserved-name rule. `go test -race` on north, south, identity, fleet, store, audit, revoke, server: pass. |
| 11 | Register and plan | wrong → fixed | M2's row lacked §4 (0019 added the logs/events rendering there); M16's lacked §6. 0019 revised its six decisions in place (not "amended" in the register's sense: rows stay `live`). §3's sentence put federation under `/tenants/{tenant}/` with a permission; corrected. §5.6 untouched since 0022; §10 consistent (`protocol_test.go`). No dated layers or narration added by 0019 (blame of every "now" in the plan). Added: §6's role grants, §3's lock and revocation sentences, §7's `truncated`. |
| 12 | `CHANGELOG.md` `v0.6.0` | wrong → corrected in `v0.10.0` | Every bullet checked: true except the route list's omission and `### Breaking`'s silence on host start-up refusals. 0020–0022 found the rest of its bullets true. |
| 13 | control#69's obligations | wrong → sync queued | (1) corrected by 0022's sync; (2) true; (3) not true until `55a410e`; (4) true; (5) true, and the config record's shape changes now; (6), (7) true; (8) stale since 0021's renumber, which queued no sync. The sync file carries the eight faithfully; Enterprise has not run it. |
| 14 | 0019's learnings | ruled | Below. |
| 15 | Carried from 0021 | fixed (2), queued (1) as 0026 | `negotiated` gives `truncated`; every `auditFailure` refusal (and inventory's, revocation's, config's) is free of internal names. Lists: proposal below. |

### Mutations, by item

Each was applied, its package's tests run, and reverted; "→" names what
failed. † passed every test before 0023.

- **Item 1:** `GET /targets` registered `AccessAuthenticated` with no permission → `TestEveryRouteDeclaresAnAccessClassAndATenantRouteDeclaresAPermission`. The middleware skipping the scope check for `/targets`, `/captures/retention`, `/revocations`, `/binding` → `TestEveryRegisteredRouteRefusesATenantOutsideTheCallersScope`, each.
- **Item 2:** `/v1/credentials/certificate` mounted on the north mux beside the table † → `TestNoContractRouteIsReachableOnThisListener`.
- **Item 3:** policy-author + `session:revoke` †, grant-admin + `target:write` †, fleet-admin − `target:write` † → `TestTheRoleTableIsHeldWhole`.
- **Item 4:** every operator record on its own connection → 15 subtests of `TestEveryWriteCommitsWithItsRecordOrNotAtAll` (xmin); inventory's records beside their transaction → 9 subtests; a publish recording nothing → its subtest; the relabel without its lock → `TestARelabelKeepsWhatWasWrittenWhileItSimulated`. A delete's record on its own connection is caught only by the trigger direction (no row is left to compare).
- **Item 5:** a code declared and not registered †, a string code in a handler † → `TestEveryCodeAnErrorCarriesIsDeclaredAndRegistered`; `findings_not_accepted` as `401` † → `TestOnlyTheMiddlewareAndALoginRefusalNameA401`.
- **Item 8:** exact-pattern refusal only → `TestAHostCanTakeNoPathAndNoCodeControlServes`, `TestAHostThatCannotStartIsRefusedAtOnce`.
- **Handlers:** subject claims unchecked → `TestASubjectCannotBeGivenAClaimThisServerSets`; sessions read to the limit only → `TestTheNegotiatedQuerySaysWhenItStoppedShort`, `TestSessionsByLimitsSessionsNotRecords`.

### The 0019 learnings, claim by claim (what parts 1–3 did not rule)

- "all under `/api/v1/tenants/{tenant}/`, one permission each" — **true** (item 1).
- "M2, M4, M8, M16, M17, M21 amended in place, register row M17 now renders in §3 too" — revised in place; M17 true; **M2's row lacked §4** (fixed).
- "Cuts `v0.6.0`, with `### Breaking`" — **incomplete** (item 12).
- "binding route … (mux conflict)" — **true** on this router (item 7).
- "Reading permissions … held by every reading role … `integration` holds only `access-context:push`, `requester` only `grant:request`" — **true**, now held whole.
- The route table — **true** row by row; it lists `GET …/claim-mapping` (0011's) beside the new `PUT`.
- "The routes 0011, 0012, 0013 and 0016 bound … are unchanged" — **not quite**: the grant view gained `mode` and `assertion_id` (additive), and `ca.go` was refactored.
- "Every write this phase adds records … inside the write's transaction wherever there is one" — **false for three** (fixed).
- "a binding write is `access_context.binding_put` or `…binding_deleted`" — **true**, and atomic.
- "The approver … is `grant-admin` (`grant:approve`)" — **true**.
- "`POST …/targets/relabel` … writes it only with `apply: true`" — true; **the write was not what was simulated** (fixed).
- "`TestTheEmergencyRunbookIsCallableAndAudited` drives all four with the roles that hold them" — **true**.
- "The impact preview is `GET …/fleet/target-observations` plus the publish-time findings" — **true**.
- "CI generates a throwaway CA key for the mock and sets `HOPLOCK_KEK`" — **true** (`ci.yml` `ssh-keygen`, `HOPLOCK_KEK`).
- Simulation shape — true; the replay also answers `from` and `to`.
- NEXT's "0024 … 0025 … 0026" — resolve through §10's `audit-findings` rows to 0027, 0028, 0029.
- control#69's "a host route on a path Control serves … is refused" — **false until `55a410e`**; its (8) — **stale**.

### Proposal: bounded inventory lists (item 15, queued as 0026)

`GET …/subjects`, `…/policy/bundles`, `…/fleet/algorithms`,
`…/fleet/target-observations` and `…/fleet/config/rollouts` answer a whole
tenant — and so do `…/targets`, `…/role-bindings` and `…/proxies`, which the
carry did not name. Proposed shape, the one `Decisions().ListRange` and now
`Audit().Sessions` use: `?limit=` (default 500, at most 10 000, the audit
reads' `boundLimit`), `?after=<cursor>` in the list's own order (id for
subjects, targets and role bindings; version for bundles; proxy id for
declarations, rollouts and proxies; host and port for observations), and
`{<list>: […], truncated, next}`; the store reads `limit+1`. Exempt:
`…/extensions` (bounded by the sealed registry) and the access-context lists
(one binding per configured provider). Not built here: it changes eight
response shapes the console (0028) will read and cuts today's answers for an
estate over 500. At the user's direction it is queued as **0026**, which
finds every other list that needs the shape too, records the shape as a
decision, and queues the implementation in chunks that each fit one session
(PLAN §10, bounded-lists revision). Its numbers moved instance identity to
0027 and the console to 0028.

### Observations, not defects

- A target id `relabel` cannot be read, written or deleted: Control's own
  `targets/relabel` literal sits beside `targets/{target}`. Rename the route
  only with a reason; nobody names a host that.
- `POST …/proxies/{proxy}/withdraw` reads the state it replaces without a
  lock; two at once record `enrolled → revoked` twice. Idempotent, low value.
- `POST …/policy/diff` answers a parse failure with the parser's text under
  `reason`, prefixed `authoring:`; the line it names is what the author needs.
- A revocation is recorded after it is published, and a failed record answers
  500 after the event went out; a retry publishes again. A revocation is safe
  to repeat.

### Cross-repo impact

`hoplock/enterprise`: corrections to control#69's obligations (3), (5) and
(8), and `v0.10.0`'s behaviour — queued as a downstream sync (PR).
`hoplock/proxy`: none; the contract is unchanged and nothing here needs a
shape it lacks.
