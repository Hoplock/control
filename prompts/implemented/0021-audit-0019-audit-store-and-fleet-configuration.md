# 0021 — Audit of 0019, part 2: the audit store, retention, fleet configuration, the store and the daemon

## Read first
- `docs/PROTOCOL.md`: the session workflow, §3, §4 and §5.
- `docs/PLAN.md`:
  - §2: **M5**, **M6**, **M8**, **M12**, **M18**, **M2** and **M23**.
  - §3: `internal/audit`, `internal/store` and `internal/daemon`.
  - §4: "Configuration distribution (proxy D18)".
  - §7: ingest, the derived columns, gaps, the query surface and retention.
  - §8: migrations, which are forward-only and carry a tenant column.
- `docs/learnings/`: read every summary. Read
  `0019-northbound-api-and-policy-lifecycle-learnings.md` **whole**: it is the
  claim set, not the truth. Read `0003` (the store and migrations), `0006`
  (fleet configuration and rollout), `0009` (the event stream) and `0010`
  (audit and the chain) where an item below touches them.
- `docs/learnings/0020-audit-0019-south-bound-and-decision-path-learnings.md`:
  what part 1 found and changed. Do not re-audit its files.
- `prompts/implemented/0019-northbound-api-and-policy-lifecycle.md`, by heading
  (`grep -n '^##\|^###'`):
  - "Fleet configuration becomes deliverable";
  - "The records the proxy emits since proxy phase 0043" (the storage and query half);
  - "What a proxy could not deliver" (gaps);
  - "Audit query & operator actions" (retention only);
  - "Delete the two debug paths this phase supersedes";
  - the matching acceptance criteria.
- `contract/control.yaml` (vendored, read-only): `ProxyConfigDocument`,
  `ProxyConfigReport`, `ProxySettings`, `config_changed`, and the record kinds
  and attributes behind the derived columns.

## Why this phase exists
0019 was built in one session whose context was compressed twice, so
everything it states is a claim to verify. Part 1 (`0020`) explains this in
full. This is part 2 of four:

| Phase | Area |
| --- | --- |
| 0020 | the south-bound surface and the decision path |
| **0021** | the audit store, retention, fleet configuration delivery, the store and the daemon (this phase) |
| 0022 | the policy lifecycle |
| 0023 | the north-bound surface, and the record as a whole |

It runs second because what it audits is durable. A defect in the chain, in a
migration or in retention is one that later data inherits.

## Objective
Verify 0019's work on the audit store, retention, fleet configuration delivery,
the store, the configuration and the daemon's wiring, at four levels:
architecture, implementation, tests and documentation. Fix every defect, and
record a verdict for every item below.

## How to find the work
Find the merge as part 1 does: `git log --oneline origin/main --grep='#69'`,
based on `7e3d002`. Then use `git diff 7e3d002 <merge> -- <paths>` to see what
0019 changed. Audit the code as it is **now**, after part 1. Read the diff path
by path, never whole.

## In scope

### Paths
- `internal/audit/`: record parsing for session-less records, the derived
  columns, `query.go` (every reader 0019 added), `retention*`, `verify.go`,
  `operatorevent.go` (the emitter's transaction use only; the north-bound
  callers are 0023's), and their tests.
- `internal/store/`:
  - `migrations/0012_northbound_api.sql`;
  - `audit.go`, `retention.go`, `bundles.go` (`Activate`, `List`),
    `decisions.go` (`ListRange`) and `subjects.go`;
  - the repositories behind fleet configuration (`LockState`, `PutReport`,
    `TouchReport`, `MarkAnnounced`);
  - `repositories.go` and `tenancy_test.go`.
- `internal/fleet/`: `config.go`, `configwire.go`, and the announcer
  (`Announce`, `WatchAnnouncements`, `TrackConfigTenant`). `algorithms.go` and
  `observations.go` were part 1's.
- `internal/httpapi/south/config.go`: the fetch and the report.
- `internal/revoke/`: what 0019 added for `config_changed`.
- `internal/daemon/`:
  - `serve.go` (the wiring, including `trackSubscriptions`) and `north.go`;
  - `seed.go` (`api_tokens`);
  - `auditverify.go`;
  - `nodebug_test.go`, and the deletion of `publish.go` and `auditread.go`.
- `internal/config/` and `config.example.yaml`.

### What to check
Give each item a verdict (**confirmed**, **wrong → fixed** or **open**) with
evidence: a file and line, a test name, or command output.

1. **Migration `0012`.**
   - It must be forward-only.
   - Every new table must carry `tenant` (M12): `proxy_algorithm_declarations`, `target_kex_observations` and `audit_retention`.
   - The rename `target_auth_* → credential_*` must rewrite no row.
   - Every derived column must be recomputable from the hashed body and hold nothing the body does not.
   - Each index must match a query that uses it.
   - Run it against a database migrated to `0011` that holds rows.
2. **Derived columns.**
   - For each one (`credential_certificate_serial`, `algorithm_floor`, `algorithm_bans`, `algorithm_bans_unmatched`, `negotiated`, `device_change_op`, `target_account`, `device_object_kind`, every `gap_*`), find where ingest projects it.
   - Confirm an absent or unparseable attribute projects nothing.
   - Confirm `gap_first_at`/`gap_last_at` are the **missing records'** span, never the gap record's own timestamp.
3. **The readers.** For each reader 0019 added, check four things: its filters, its tenant scoping, its ordering, and its bounds. The readers are:
   - `DeviceConfigChanges`, `Weakened` and `DegradedCredentials` (rung > 1);
   - `SessionCertificates` and `CertificateSessions` (the serial join as text);
   - `AlgorithmPolicyUnmet`, `SessionsBy` and `Algorithms`;
   - `Gaps`, `GapOf` and `SessionLoss` (summed per cause).
   Every by-session read must refuse `""`.
4. **Retention** (the one rule M8 must not break).
   - A pass must delete only a **contiguous prefix** of one stream, never its head, under that stream's append lock, so a concurrent ingest cannot interleave.
   - It must move the `audit_retention` marker forward only, with `through_seq` and `through_hash`.
   - It must record `operator.audit.retained` on the control stream, in the same transaction as the deletion.
   - The verifier must resume from the marker. A chain with a retained prefix must verify, and one with a record removed anywhere else must not.
   - `hoplock-control audit-verify` must pass after a pass and print what was retained.
   - Capture retention must empty bytes, keep digests and set `retained_at`, and `ErrCaptureRetained` must reach the verifier as "retained", never as tampering.
   - Prove each property with a test that fails when it is broken.
5. **Fleet configuration on the wire (D18).**
   - Versions render as `v<N>` and hashes as `sha256:<hex>`, and both parse back. The ETag is the quoted wire hash, and `If-None-Match` holding it answers `304`.
   - The fetch is `200`, `304`, `204` (nothing published for the proxy's zone or the proxy) or `404 not_enrolled`, never `401`.
   - The report answers `200 accepted` every time. An unchanged report touches `last_reported_at` only, and an older `reported_at` overwrites nothing.
   - Check each of these against the vendored contract text, not the learnings.
6. **The fleet-owned keys.**
   - `fleet.fleetOwned` must be the **only** list, and `TestTheFleetOwnedKeysAreTheContracts` must extract it from the vendored description.
   - Check the "17" claim against the document.
   - Prove `TestTheFleetOwnedKeyTestCatchesDrift` fails on a lost key and on an added key.
   - A publish naming another key, or a value of the wrong shape, must be refused (`422`).
7. **Delivery without reconnect.**
   - A publish must commit first and announce after.
   - `announced_hash` must lag until the stream takes a `config_changed`, or until a report confirms the document.
   - The announcer must re-emit on start and on a timer (`fleet.DefaultAnnounceInterval`).
   - `TrackConfigTenant` must cover the configured tenant and every subscribed tenant.
   - Kill the announcement between commit and emit in a test, and confirm the proxy still converges.
   - Check that the announcer is safe with several Control nodes: a double announce must be harmless, and none may be lost.
8. **Drift.** `ConfigDrift` and the rollout view must be derived from the report alone. Each state's meaning must match the contract: `pending_restart`, `rejected` (which carries `last_error`, keys never values) and `fetch_failed`. `Heartbeat.RunningConfigVersion` is said to be removed: confirm nothing still reads a running version from a heartbeat.
9. **Store changes.**
   - `PolicyBundles().Activate` was rewritten because a rollback hit the `policy_bundles_one_active_key` unique index. Confirm it locks, clears and sets in one transaction, and that concurrent activations cannot leave zero or two active.
   - `tenancy_test.go` must have a case for every repository method 0019 added; the reflection test fails otherwise, so confirm it ran.
10. **The debug paths are gone.**
    - `events.publish_listener`, `events.publish_token`, `audit.read_listener` and `audit.read_token` must be refused as unknown keys.
    - `TestNoGoFileBindsADebugRoute` must read every Go file under `cmd/` and `internal/` and fail on a reintroduction. Prove it.
    - No document may still tell anyone to use them: grep `docs/`, `deploy/`, `README.md` and `cmd/pdpconform/`.
11. **`seed`.**
    - It is still the database-direct bootstrap, and now seeds scoped API tokens. Confirm a seeded token is hashed at rest exactly like an issued one, and carries only the roles named.
    - Check its comment's claim that it is not a back door (it needs the database credential).
12. **Configuration.**
    - `policy.interpreters`: empty takes the shipped list, and a list replaces it.
    - `south.max_log_body_bytes`: its default, its validation, and the fact that it applies to the log paths only.
    - `config.example.yaml` must match `internal/config` key for key. A test that loads it would catch drift.
13. **The daemon's wiring.**
    - `serve.go` must build the CA once and hand it to both listeners.
    - The north-bound dependencies must be wired exactly once.
    - No north-bound route may be reachable from the south-bound listener, or the reverse (M2). Run both isolation tests.
14. **Security and robustness.**
    - Every new query must be parameterised. Check the array filters in particular.
    - Every list must be bounded.
    - Retention and the announcer must hold no lock across I/O they do not own.
    - Run `go test -race` on every package in scope.
15. **The record, for this area.** Check every claim about this area in:
    - `docs/learnings/0019-…-learnings.md`: "Fleet configuration delivery", "#66", "#71", "Retention" and "Conformance keys added";
    - `docs/PLAN.md` M8, the D18 subsection, §7 and §3;
    - the matching bullets of `CHANGELOG.md` `v0.6.0`.

## Out of scope
- Parts 1, 3 and 4's files.
- New behaviour. A design question that needs more than a local fix is proposed in the PR for the user to decide.
- Editing any merged learnings file, or any merged migration. A migration fix is a new forward migration.

## Acceptance criteria
- Every item has a verdict with evidence in this phase's learnings.
- Every behaviour fix comes with a test that failed before it.
- For items 4, 6 and 7, the learnings record the mutation that proved the guarding tests are not vacuous.
- `go build`, `go vet`, `go test ./...` and `-race` on changed packages pass, and `golangci-lint` at CI's pin reports 0 issues.
- `make contract-check`, `license-check` and `release-check` pass.
- `make conform` against this server passes on a fresh database. Run it twice: the second run's config cases fail by design, which the suite documents. Confirm that is the only difference.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0021-audit-0019-audit-store-and-fleet-configuration-learnings.md`.

Its summary block MUST give:
- the findings, by verdict and severity;
- each fix, by commit;
- what was confirmed, by item;
- what is left open, and why;
- each correction to a 0019 claim.

Its Details carry the verdict table.

**Release.** The next MINOR after the latest tag (M23), with a `CHANGELOG.md`
section listing what a consumer can observe. A new migration or a changed
column is observable.

**Cross-repo impact.** A fix to a shared surface owes the obligations under
`## Cross-repo impact` and a queued downstream sync (`docs/CROSS-REPO-PROTOCOL.md`
§4.1, §4.3). The audit column names and the retention marker were both promised
to `hoplock/enterprise` in control#69. Otherwise write **"None"** for each
repository.
