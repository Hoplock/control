# 0021 — audit of 0019, part 2: audit store, retention, fleet configuration — Learnings

## Summary
- **Findings:** 15 items, no high-severity defect. Fixed: 6 medium (retention's record outside the pass's transaction; four indexes 0012 built that no query could use; the runbook's session list limited by records; a proxy with nothing published announced and shown as drift forever; a publish's tenant never retried; a `config_changed` counted delivered when only the publishing node's bus took it), 1 low (per-session readers read the first 500 records only), 16 test gaps a mutation passed, 4 doc errors. Full table and every mutation in Details.
- **Fixed, by commit:** `fe394d9` retention pass and record in one transaction, one stream-lock function; `61decac` containment filters for the GIN indexes, the serial's partial index, `Audit().Sessions`, per-session reads by what they need, readers held to their tenant; `1666294` nothing published is version 0, publishes are announcer tenants, delivery gaps tested; `41a7745` the debug-route, fleet-owned-key, example-config, activation, recompute and seed guards, seed's comment; `c88ce66` PLAN M2/M8/§4/§7, pdpconform README, the 0022/0023 prompts; `656fed3` an announcement settled only by a live stream, so the node holding it delivers; `7c54586` the two phases queued.
- **Confirmed as built:** items 2, 5, 6, 9, 11, 12, 13. **Confirmed after the fixes:** 1, 3, 4, 7, 8, 10, 14, 15. Migration 0012 on a populated 0011 database rewrote no row and no table (same relfilenodes, xmins and values).
- **Queued at the user's direction (PLAN §10, audit-findings revision):** **0024** signed retention markers, because the verifier trusts `audit_retention`, so one forged row hides a deleted prefix; that session first decides whether signing is the right fix. **0025** shared event plane, because a revocation reaches only the publishing node's subscribers, so Control runs as one node. The five queued phases after the audits moved two places. Carried into prompts: unbounded inventory lists, `audit/negotiated`'s missing truncation signal and an internal name in a refusal (0023); bundle-list cost and the unmet-warning limit (0022).
- **0019 claims this corrects:** gaps "indexed by record id" and negotiated/bans "GIN indexed" (no query could use them); drift "every proxy whose running hash is not the desired one" (it included proxies with nothing published); "each pass records `operator.audit.retained`" (not in the pass's transaction); `TrackConfigTenant` "each tenant a publish is made in" (it was not); the debug-route test "fails on a reintroduction" (three passed); the drift meta-test "proves the extraction fails" (true, but not the contract test). The "17 keys" claim holds.
- **Decisions:** none added, amended or withdrawn; register unchanged. **Migrations:** none. Release `v0.8.0`, no `### Breaking`. **Cross-repo:** none owed: neither the audit columns nor the retention marker promised in control#69 changed.
- **NEXT (0022, 0023):** the items carried into your prompts ("Carried from 0021"). Then 0024 and 0025, queued here. Old numbers 0024–0028 resolve through PLAN §10's table.

## Details

### How the audit was run

Every item was read in the code as it is on `main` after `3303825` (part 1
merged), using `git diff 7e3d002 1b9bf55 -- <path>` only to find what 0019
touched. "Mutation" means: change the rule, run the package's tests, record
what fails, revert. A mutation nothing caught is a test gap, and each one found
got a test that fails under it, or a reason it is not a gap.

Environment notes for the next audit: the container's Postgres 16 is started
with `service postgresql start`, then `ALTER USER postgres PASSWORD
'postgres'`. golangci-lint v2.14.0 must be built with
`GOTOOLCHAIN=go1.27.0 go install …@v2.14.0`: a plain `go install` picks Go
1.26, and that build refuses the module. pgx binds parameters, so `EXPLAIN
(GENERIC_PLAN)` of a statement with `$n` goes through
`tx.Conn().PgConn().Exec`, the simple protocol. The pre-0019 binary for the
migration run was built from a worktree at `7e3d002`.

### Verdicts

| # | Item | Verdict | Evidence |
| --- | --- | --- | --- |
| 1 | Migration `0012` | wrong → fixed `61decac` (indexes) | Forward-only, with the checksum guard (`TestPendingMigrationsRefusesAnEditedMigration`). `tenant` leads the primary key of all three new tables. A database migrated to 0011 by the pre-0019 binary, holding 2000 audit rows, 50 captures and a config state, was migrated to 0012 by this one. `pg_class.relfilenode` was the same for all three tables, and an md5 over every old column plus `xmin` was the same before and after. So nothing was rewritten or updated, and the renamed `audit_records_auth_rung_idx` now names `credential_*`. Derived columns: see item 2. **Indexes:** the GIN indexes on `negotiated`, `algorithm_bans` and `gap_record_ids` were unusable by `$n = ANY(col)`. The certificate partial index was unusable by `= $n` under a generic plan. On 200 000 rows a rare negotiated value took 69 ms as a scan, and 0.29 ms through GIN once written as `@>`. `TestEveryIndexedAuditFilterCanUseItsIndex` failed on all four, and passed on the span index, which was right. |
| 2 | Derived columns | confirmed; strengthened `41a7745` | Every column is projected in `Record.Row` (`internal/audit/record.go:474-560`) from the redacted attributes `Body` hashes: the serial via `ParseSerial`/`FormatSerial` (`0042` → `42`, the same number), the floor and profile verbatim, the bans as `axis:name`, the unmatched bans split, `negotiated` as `axis:value` for six attributes, the device change and gap facts only on their event. An absent or unparseable serial, rung, count or time projects nothing (`TestTheCertificateSerialIsParsedAsAnUnsignedInteger`, `TestAGapRecordIsReadLeniently`). The gap span is `gap_first_at`/`gap_last_at` from the attributes, never `RecordedAt` (`TestWhatAProxyCouldNotDeliverIsFindable`'s "own timestamps" case). `TestEveryDerivedColumnIsRecomputableFromTheBody` rebuilds the row from the stored body alone and compares every field. A body dropping a projected key fails it. `proxy_id` is the stream's identity, not a projection. |
| 3 | The readers | wrong → fixed `61decac` | Tenant: every reader goes through `auditWhere`'s `tenant = $1`, `GapLoss`'s own, or `SSHCertificates().Get(tenant)`. Removing the filter passed every audit test; `TestEveryReaderStaysInsideItsTenant` fails now. Ordering: newest first, session reads oldest first, `Sessions` by the newest record. Bounds: `boundLimit` (500, at most 10 000). **`SessionsBy`** limited records, so one session's records crowded out others, and a session-less record could take the only place (`TestSessionsByLimitsSessionsNotRecords`: `[sess-new]` and `[]`). Now `Audit().Sessions` returns distinct sessions with a session limit. **`SessionCertificates`/`Algorithms`** read the session's first 500 records (`TestASessionsCertificateAndAlgorithmsAreFoundPastItsFirstPage`: empty on a 601-record session); they now filter on `HasCertificate`/`HasAlgorithms`. Degraded `> 1`, weakened `<> '' AND <> 'default'`, administrator as the empty kind, loss summed: each caught by a mutation. `""` refused by every by-session read, and `Algorithms` and `SessionCertificates` are now tested for it. |
| 4 | Retention | wrong → fixed `fe394d9` | The prefix is contiguous through the marker and never the head (`store/retention.go`). It is taken under `lockStream`, the lock `AppendChain` takes, and `TestARetentionPassWaitsForTheStreamsAppendLock` fails without it. The marker moves forward only, with `through_seq` and `through_hash`. **`operator.audit.retained` was written by `OperatorEvent(ctx, nil, …)` after `RetainPrefix` committed**, so the marker and the record carried different `xmin`s (15611 and 15612). Now `Emitter.RetainPrefix`/`RetainCaptures` run in one transaction, and the xmins match over the wire and in the store; a trigger refusing the record leaves all six records and no marker. On the running server, `audit-verify` after passes on `control` (through 3) and `unattributed` (through 65 of 66) printed `OK, 10 records, … retained through 3` and `OK, 1 records, … retained through 65`. A capture pass printed `4 capture(s) removed by retention`. A pass through the head answered `400`. Capture retention empties bytes, keeps the digest, sets `retained_at`, and `ErrCaptureRetained` counts as retained (`TestRetainedCapturesAreGoneNotChanged`). Mutations below. |
| 5 | Wire (D18) | confirmed; strengthened `1666294` | `v<N>`/`sha256:<hex>` round trip, and `v0`, `v04`, `v-1` and foreign strings refused (`TestTheWireRenderingRoundTrips`). The ETag is the quoted wire hash; `If-None-Match` is read as an entity tag (quoted, bare, weak, a list, `*`). The fetch answers `200`, `304`, `204` or `404 not_enrolled`, the last through `notEnrolled`, never `rejectCredential` (`TestTheConfigFetchOverTheWire`). The report answers `{"accepted": true}` for every valid report from an enrolled proxy, the repeat included, and `400` only for a state outside the enum or a `reported_at` that is not RFC 3339, both of which the contract's `BadRequest` covers. Checked against `contract/control.yaml:763-905` and `:2840-2975`. The 304's ETag was untested; RFC 9110 §15.4.5 requires it, and it is now asserted. |
| 6 | Fleet-owned keys | confirmed; strengthened `41a7745` | `fleetOwned` (`configwire.go:138`) is the only list, and `grep` finds the keys nowhere else outside `contract/`. The vendored description names 17: 10 listed singly and 7 `logging.*`. Mutations: a key lost, a key added and a looser extraction each failed the contract test. **The drift meta-test checked its own copy of the comparison**, so rewriting the contract test to compare the list with itself passed both. The two now share `fleetOwnedDrift`, and lost, gained and renamed keys each fail it. Dropping the count changes nothing, because an empty extraction already disagrees with the list. `422 config_key_not_fleet_owned`/`config_value_invalid`: `TestAPublishNamingAnythingButAFleetOwnedKeyIsRefused`, `TestAPublishOfANonFleetOwnedKeyStoresNothing`. |
| 7 | Delivery without reconnect | wrong → fixed `1666294`, `656fed3` | Commit first, announce after (`PublishConfig` returns from `InTx`, then `announce`). The marker lags until the stream takes the event or a report names the desired hash. `MarkAnnounced` never moves it onto a hash no longer desired. The announcer runs once at start, then every `DefaultAnnounceInterval` (15 s). **`TrackConfigTenant`'s comment said publishes were tracked; they were not**, so a lost announcement in a tenant that was neither configured nor subscribed here was never retried. **A proxy with nothing published was announced v1**, a document the fetch denied (see item 8). Killed between commit and emit (`fail: 1`), the announcement reaches the proxy at the announcer's start, and the proxy converges to no drift. A double announcement is harmless: the proxy drops a notification naming the hash it holds, and the fetch answers `304`. **Lost across nodes: fixed `656fed3`.** The bus is per node, so a publish on node A marked a proxy announced although its stream was on node B. An announcement now settles only when a live local subscription takes it (`Receipt.Delivered`) or a report confirms it, and every node's announcer delivers to the proxies whose streams it holds. `TestAnAnnouncementReachesAProxySubscribedOnAnotherNode` failed first. A proxy subscribed nowhere is not re-announced; it fetches on reconnect. |
| 8 | Drift | wrong → fixed `1666294` | Drift and the rollout view read only the report columns: `Drifted` needs `applied`, the running hash and the running version. **With nothing published, enrollment stored the empty composition as desired v1**, so a proxy reporting its bootstrap file was drift forever, and its first document was v2. That state is now version 0. `pending_restart` naming the desired document as running was untested, and is now tested. `rejected` and `fetch_failed` carry `last_error`, stored as reported. `Heartbeat.RunningConfigVersion` and `ReportRunningConfig` are gone; `running_*` is written only by `PutReport`. |
| 9 | Store changes | confirmed; strengthened `41a7745` | `Activate` locks the wanted row (`FOR UPDATE`), clears the others and then sets it, in one transaction. A version that does not exist clears nothing. Racing activations leave exactly one active, and the loser is a conflict (`TestRacingActivationsLeaveExactlyOneActive`, 10 rounds of 8). The single-statement form passed that race test, because its failure was an allowed conflict. An unraced rollback now catches it, as authoring's lifecycle test already did. `TestEmptyTenantIsRefusedBeforeAnyQuery` and `TestRepositoryMethodsTakeATenant` ran and passed, `Audit.Sessions` included. |
| 10 | Debug paths gone | wrong → fixed `41a7745` (test), `c88ce66` (docs) | All four keys are refused as unknown (`TestTheSupersededDebugKeysNoLongerParse`). **`TestNoGoFileBindsADebugRoute` missed three reintroductions:** a route bound through a named constant, a handler switching on `r.URL.Path`, and any file under `server/`. It now walks the module and refuses any `/debug/` literal in non-test code; all three, and a test server bound to one, fail it. `cmd/pdpconform/README.md` still described the publish listener and `internal/daemon/publish.go`, and is corrected. `docs/`, `deploy/` and `README.md` were clean. |
| 11 | `seed` | confirmed; comment wrong → fixed `41a7745` | A seeded token stores `Credential.Digest()`, the SHA-256 the middleware looks up, exactly as `IssueToken` does. It authenticates, holds only the named roles in the seeded tenant, and refuses an empty or unknown role (`TestASeededAPITokenIsAnIssuedOneHoldingOnlyItsRoles`). Mutations caught: a clear secret, an extra role, a second tenant. The comment said seed would become a client of the API once 0019 landed. It now says what 0019 decided, and why seed is no back door: it holds the DSN. A seeded token, unlike an issued one, writes no `token_issued` record. |
| 12 | Configuration | confirmed; strengthened `41a7745` | `policy.interpreters`: empty takes `DefaultInterpreters`, and a list replaces it (`authoring/service.go:85-88`). `south.max_log_body_bytes`: 4 MiB default, floored at `MinLogBodyBytes`, log paths only (0020's tests). The example names every key and only keys config knows: strict loading proves one direction, and `TestTheExampleDocumentsEveryKey` proves the other (a field added undocumented passed the old test). Every value in the example equals the default it claims. |
| 13 | Daemon wiring | confirmed | `buildCA` is called once in `serve.go:169`, and the result goes to `south.New` and `northDeps`. `identityctl.go:610` is a separate command. `buildNorth` builds the federation, the lifecycle and the handler once. `TestNoContractRouteIsReachableOnThisListener` and `TestNoNorthBoundRouteIsReachable` pass. |
| 14 | Security | confirmed; lists **open → 0023, 0022** | Every query in scope is parameterised. The array filters bind their value as `ARRAY[$n::text]`, and `auditWhere` builds fixed clauses with `$n`. The audit readers are bounded. The inventory lists (`Subjects().List`, `PolicyBundles().List`, declarations, observations, `ListStates`) are whole-tenant reads, sized by the estate rather than the caller, and are carried into 0023's prompt. Retention holds only its own stream's lock and the control stream's. The announcer holds no mutex across a publish (`sync.Map.Range`). `go test -race` passes on every package in scope. |
| 15 | The record | wrong → fixed | Corrections are below. PLAN M2 (the debug-route test), M8 and §7 (one transaction) and §4 D18 (version 0, the tracked tenants, what "taken" means with several nodes) are revised in place. `CHANGELOG.md` `v0.6.0`'s bullets for this area are true. |

### Mutations, by item

Each was applied, its package's tests run, and reverted; "→" names what
failed.

- **Item 1:** the four filters in their 0019 form → `TestEveryIndexedAuditFilterCanUseItsIndex`, one subtest each.
- **Item 2:** the body dropping `algorithm_floor` → `TestEveryDerivedColumnIsRecomputableFromTheBody`.
- **Item 3:** administrator not mapped to the empty kind → `TestTheDriftFeedFiltersByDeviceObjectAndOperation`. Degraded from rung 1 → `TestTheStoreReturnsTheRungInForceNotTheRungRequested`. `default` read as a weakening → `TestTheWeakeningQueryReadsPresenceAndNeverAbsence`. `Algorithms("")` accepted → `TestSessionLessRecordsAreStoredOnBothPaths` (passed before 0021). `SessionLoss("")` accepted → same. The latest report for the sum → `TestWhatAProxyCouldNotDeliverIsFindable`. The span on `recorded_at` → same. The query unscoped, and `GapLoss` unscoped → `TestEveryReaderStaysInsideItsTenant` (both passed before 0021).
- **Item 4:** delete `< through` → `TestARetentionPassKeepsTheChainVerifying`, `…ByInstant…`. The head allowed → `TestRetentionNeverMovesBackAndNeverTakesTheHead`. The marker allowed back → same. The marker not moved → `…ByInstant…`. The verifier ignoring the marker, or not chaining onto `through_hash` → `…KeepsTheChainVerifying`, `…ByInstant…`. The record outside the transaction → `…ByInstant…` (xmin), `TestRetentionOverTheWireKeepsTheChainVerifying` and `TestCaptureRetentionOverTheWireIsRecordedInItsOwnTransaction`. Outside the transaction, a pass over the control stream also waited on its own append lock until the request timed out. No `retained_at` → `TestRetainedCapturesAreGoneNotChanged`. A retained capture read as empty bytes → same. The plan by instant taking the first record at the instant → `…ByInstant…`. No lock → `TestARetentionPassWaitsForTheStreamsAppendLock`.
- **Item 5:** an unquoted ETag; `If-None-Match` never holding; `200 {}` for nothing published; `401` for not enrolled → `TestTheConfigFetchOverTheWire`. A repeated report refused → `TestTheConfigReportOverTheWire`. No ETag on the 304 → `TestTheConfigFetchOverTheWire` (passed before 0021).
- **Item 6:** a key lost from, or added to, `fleetOwned`; an extraction of anything in backticks → `TestTheFleetOwnedKeysAreTheContracts`. The extraction returning the server's list → `TestTheFleetOwnedKeyTestCatchesDrift`. The check comparing the list with itself, or dropping the set comparison → `TestTheFleetOwnedKeyTestCatchesDrift` (both passed before 0021). A key not fleet-owned, or any shape, accepted → `TestAPublishNamingAnythingButAFleetOwnedKeyIsRefused`.
- **Item 7:** marked announced when the bus took the event but no live subscription did; the announcer ignoring which node holds the stream → `TestAnAnnouncementReachesAProxySubscribedOnAnotherNode` (both passed before `656fed3`). Marked announced before the emit; nothing owed → `TestAFailedAnnouncementIsRetriedAcrossARestart` and both announcer tests. No start pass → `TestAnAnnouncementLostInAnyTenantIsRetriedUntilTheProxyConverges`. A publish not tracked → same, and `TestTheAnnouncerRetriesOnItsTimer`. A confirming report not settling → `TestAReportNamingTheDesiredDocumentSettlesItsAnnouncement`. The marker on a stale hash → `TestTheAnnouncedMarkerNeverNamesAStaleDocument`. All three last passed before 0021. An older report overwriting, or an unchanged one counted → `TestReportsAreIdempotentAndOrdered`.
- **Item 8:** `pending_restart` counted as caught up → `TestDriftIsDerivedFromTheReport` (passed before 0021). The empty composition stored as a version → `TestNothingPublishedIsNeitherAnnouncedNorDrift`, `TestTheFetchServesTheDocumentDesiredNow`, `TestAPublishOfANonFleetOwnedKeyStoresNothing`.
- **Item 9:** one statement setting before clearing → `TestRacingActivationsLeaveExactlyOneActive`'s unraced rollback, and authoring's `TestTheBundleLifecycleEndToEnd`. No existence lock → `TestPolicyBundlesAreImmutableAndOneIsActive`.
- **Item 10:** a constant pattern, a path switch, a route in `server/` (all three passed before 0021), and a test server bound to one → `TestNoGoFileBindsADebugRoute`.
- **Item 11:** the secret stored in clear, an extra role, a second tenant → `TestASeededAPITokenIsAnIssuedOneHoldingOnlyItsRoles`.
- **Item 12:** a config key the example does not document → `TestTheExampleDocumentsEveryKey`; `TestExampleConfigLoads` alone passed it.

### Corrections to 0019's claims, in full

- **"`logging.gap` … indexed by span and by record id"** (#71) and **"`Reader.SessionsBy(NegotiatedQuery)` over the GIN indexed `negotiated` … and `algorithm_bans` columns"** (#69/#72): the indexes existed and no query could use them. Migration 0012's index comments stand; the queries changed.
- **"Drift … is every proxy whose running hash is not the desired one"** ("Fleet configuration delivery"): it also listed every proxy with nothing published, for good. And "the fetch is `204` when nothing is published" disagreed with the announcer, which named a version 1.
- **"Each pass records `operator.audit.retained`"** ("Retention"): it did, but in a transaction of its own. PLAN §3's "in the transaction that makes the change" was not met.
- **`TrackConfigTenant`: "the configured tenant, each tenant a proxy subscribes in, and each one a publish is made in"** (code comment): publishes were not tracked.
- **"`TestNoGoFileBindsADebugRoute` reads every Go file under `cmd/` and `internal/` … so that none comes back"** (PLAN M2): it read them, and three ways of bringing one back passed.
- **"`TestTheFleetOwnedKeyTestCatchesDrift` proves the extraction fails on a lost or an added key"**: it proved the extraction did, not the contract test.
- **seed.go, "When 0019 lands, this becomes a thin client of that API or it goes away"**: 0019 kept it, as its learnings say; the comment now does too.
- **Confirmed as stated:** 17 fleet-owned keys; `credential_*` renamed with no rewrite; degraded is rung `> 1`; `object_kind=administrator` matches the empty kind; `SessionLoss` sums per cause; the announcer's 15 s; `Heartbeat.RunningConfigVersion` removed; the conformance keys 0019 lists exist in `cmd/pdpconform/expectations.go`.

### What became of the two proposals

- **Several nodes and `config_changed`.** `announceOne` marks a proxy announced
  when this node's bus accepted the event. The bus retains it in its ring and
  queues it to local subscribers only (`revoke.Bus.publish`). A proxy whose
  stream is on another node is marked announced and is never told. The fix:
  mark only when `SubscriptionState.LiveSubscriptions` shows the proxy on this
  node, and emit from the announcer only to local subscribers, so the node
  holding the stream delivers it within one interval. It costs nothing on one
  node, and the reconnect fetch covers a disconnected proxy. Revocations have
  the same gap, which is why it is M9's and not a local fix. **Built at the
  user's direction (`656fed3`)** for configuration, whose durable markers make
  it a local fix. Revocations have no marker, and are queued as 0025, the
  shared event plane.
- **A forged marker.** The verifier starts from `audit_retention` and trusts
  it. Deleting a prefix used to break at sequence 1; now a `DELETE` plus one
  `INSERT` of the marker, with the last deleted row's hash read beforehand,
  verifies cleanly. The control chain's `operator.audit.retained` names the
  stream, `through_seq` and `through_hash`. Requiring a matching record would
  make a forgery also need a correctly chained control record. But the chain's
  hash takes no secret, so the same writer appends one at the head, and still
  rewrites no later record: the cross-check does not restore §7's guarantee.
  Signing the marker with a per-tenant key behind `ext.KeyStore`, checked
  against the private half, does. **Queued as 0024 at the user's direction**,
  with that session asked first to compare it with the alternatives (a
  cross-check, a checkpoint record in the stream, keyed links, an external
  anchor) and to build it only if it comes out best.

### Observations, not defects

- Retention's `ThroughSeqBefore` reads `received_at`, which is `now()` at the
  append's transaction start, so it is not monotonic in `chain_seq` under
  concurrent appends. The plan can only take fewer records than asked, never
  one at or after the instant.
- After a prefix is retained, a resent record id from it is fresh again
  (`unseen` finds no row) and is appended as new. A proxy's buffer window is
  far shorter than any sane retention age.
- A large retention pass holds the stream's append lock for its `DELETE`, and
  `RetainPrefix` sets no store timeout of its own. The proxy's ingest waits and
  retries, losing nothing.
- `negotiated` includes the account-mapping event's `target_kex_algorithm`
  (the driver's own connection), so the runbook also finds sessions whose
  driver connection used a banned exchange. That is wanted.
- A proxy enrolled before this release with nothing published keeps the empty
  composition as v1 until its scope is first published. Its fetch now answers
  `200` with no settings, which the contract treats like the bootstrap file
  (`CHANGELOG.md` `v0.8.0`). No migration resets it: there is no installed
  base (0012's reasoning).

### Cross-repo impact

None for `hoplock/proxy`: the contract is unchanged, and nothing here needs a
shape it lacks. None for `hoplock/enterprise`: `ext/` and `server/` are
unchanged (`make release-check`: no public API change). The audit column names
and the retention marker control#69 promised are untouched. What changes
for a host is behaviour inside the server it embeds, listed in `v0.8.0`.
