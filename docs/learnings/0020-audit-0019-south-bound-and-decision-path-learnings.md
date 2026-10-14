# 0020 — audit of 0019, part 1: south-bound surface & decision path — Learnings

## Summary
- **Findings:** 14 items, no high-severity defect. Fixed: 2 medium (M5 declaration memory, CI never ran pdpconform's `error` group), 3 low behaviour (blank-named ban refused, issuance on a decision of any age, a log body limit below one record), 9 test gaps a mutation passed, 3 doc errors. Nothing left open. Full table and every mutation in Details.
- **Fixed, by commit:** `afbedde` the per-node declaration memory could keep another node's row forever (M5); `bf42e07` the `error` group in CI, five 0019 conformance cases missing or vacuous; `3d66a2d` compiler refused a ban of blanks the proxy accepts; `22075da` a 401 named in an allow-listed function; `e6db314` issuance proxy binding, cert-as-key, cache by session/key; `0d0a4ca` the "did not allow" check; `15323e0` certificate tier at any rung; `2e62ae8` the log body limit; `d362c74` enum completeness, fixture round trip; `5fb0267` PLAN §7 severity, two doc comments; `ec38909` issuance only inside the cited decision's reuse window; `e92b979` `south.max_log_body_bytes` refused below one storable record.
- **Confirmed as built:** items 3, 5, 7, 10, 11, 13. **Confirmed after the fixes:** 1, 2, 4, 6, 8, 9, 12, 14.
- **Raised as open, then fixed at the user's direction:** issuance cites a decision for its hint's TTL plus `credential.DecisionSetupAllowance` (2 min) and no longer; `config.MinLogBodyBytes` is the floor under the log body limit.
- **0019 claims this corrects** (this file supersedes 0019's learnings for them): "declarations written only on digest change"; "serial joins `ssh_certificates.serial::text`"; the "#71 every `400`" list; PLAN §7 "severity is not [closed]"; CI's "EVERY group"; "`make conform` passes with the #68/#69/#71 cases", which was true while five specified cases did not exist or graded nothing.
- **Decisions:** none added or withdrawn; **M5 revised in place** (which memories the decision path keeps, and each one's bound). Register unchanged. **Migrations:** none. Release `v0.7.0`; `### Breaking`: one new pdpconform expectation key, and a log body limit below the floor is refused at start-up.
- **NEXT (0021):** `$1 = ANY(negotiated)` (and `gap_*`, `algorithm_bans`) cannot use the GIN indexes migration 0012 built for them — the store's readers are 0021's. **Cross-repo:** none owed, to either repository.

## Details

### How the audit was run

Every item below was read in the code as it is on `main` after `1b9bf55`,
using `git diff 7e3d002 1b9bf55 -- <path>` only to find what 0019 touched.
"Mutation" means: change the rule in the code, run the package's tests, record
what fails, revert. A mutation nothing caught is a test gap, and each one found
here got a test that fails under it. The proxy was checked out at the vendored
commit (`976faaa`) for two purposes only: to build `cmd/mock-control`, as CI
does, and to regenerate the capability fixture with the proxy's own encoder.
Its `api/README.md` "Capability advertisement" and `internal/control/validate.go`
were read to settle items 2 and 6, because 0019's code cites them.

Environment notes for the next audit: a local Postgres 16 is enough for
`HOPLOCK_TEST_DSN`; `ssh-keygen` may be missing, and an ed25519 OpenSSH key for
the mock can be written with `ssh.MarshalPrivateKey`; golangci-lint v2.14.0
must be built with Go 1.27 or it refuses the module. The two conformance runs
both bind `127.0.0.1:8080`, because the expectation files name that port.

### Verdicts

| # | Item | Verdict | Evidence |
| --- | --- | --- | --- |
| 1 | M11: `401` only by decision | wrong → fixed `22075da` (test strength) | The 401 paths, all through the three deniers: `middleware.go:255,264` and `events.go:82`, `config.go:136` (`rejectCredential`); `handlers.go:202,270,296,519` (`deny`); `credentials.go:73` (`refuseToMint`). The source test caught a call to `contract.Denied`/`RefusedToMint` or a use of `ErrDenied` elsewhere, but not `http.StatusUnauthorized` named inside a function the status test allows (0019 added `serveConfig` to that list) nor a literal `401`: both mutations passed. Both fail now. Mismatch: `VersionMismatchError` names both numbers, `5xx` (`TestAuthorizeEndToEnd/a_version_below…`, `requireMismatch`). Absent `policy_version`: `400` (`…/an_absent_policy_version_is_a_400…`). |
| 2 | Never a thinned answer | wrong → fixed `15323e0` (test strength) | `requiredVersion` (`vocabulary.go:65`) 6/5/4; gate before capabilities (`service.go:329` then `:346`). Mutations, each caught: floor tier → 4 (`TestTheAlgorithmFloorTier`); certificate tier → 4 (`TestTheBrokeredCertificateTier`); capability check moved before the gate (`TestTheAlgorithmFloorTier`: a 5-declaring proxy gets a capability error instead of the mismatch); undeclared floor sent (`TestAFloorIsSentOnlyToAProxyThatDeclaredIt`); curve25519 spellings not folded (`TestABanIsJudgedPerProxyFromItsOwnDeclaration`, `TestAnEmptiedAxisIsJudgedFromTheDeclaration`); emptied axis not refused, and unknown refused (`TestABanIsJudgedPerProxy…`). **Not caught:** checking only the first rung for `brokered-certificate`; now caught. The judgement is the proxy's rule: profile list, intersect with the level, subtract with curve25519 as one (`fleet/algorithms.go`, upstream README "Capability advertisement", `validate.go` `validateAlgorithmPolicy`). |
| 3 | `Declares()` | confirmed | `resolve.go:166` reads `execution`/`reach` only; `TestAnAlgorithmOnlyDeclarationDeclaresNoRung`. It has **no production caller** (`grep '\.Declares()'`: the test alone), and never had one; the algorithm declarations are read by `fleet.JudgeAlgorithms` and `DeclaresFloor` only. |
| 4 | Certificate issuance | wrong → fixed `e6db314`, `0d0a4ca` (test strength), `ec38909` (decision age) | `credential/session.go` `IssueForSession`/`boundDecision`. Mutations caught: lifetime passed as `ValidFor` (`TestARouteBoundAboveTheDefaultDoesNotLengthenIt`); lifetime ignored (`TestAnIssuanceSignsExactlyTheSubmittedKey`); a store read failure turned into a refusal (`TestAStoreFailureOnIssuanceIsA5xxAndNeverA401`); target not cross-checked (`TestARefusalToMintIs401ThroughItsOwnCode`). **Not caught, now caught:** the bound proxy check removed; the effect check removed (a deny or unserved record carries no snapshot, so the snapshot check hid it); a cache keyed by session and key. Cert-as-key `400` now tested at the handler. A decision is now cited only inside its reuse window (below, "first left open"). Lifetime `min(certificate_validity, max_certificate_validity, lifetime_seconds)` at `ca.go:523-530`; `valid_before` and `serial` from the parsed certificate; no CA `503` (`credentials.go:56`, `:77`). |
| 5 | The tripwire | confirmed | `TestABrokeredCertificateEntryCarriesPolicyOnly` pins the three names and names the three artifacts; adding `certificate_serial` for the method fails it and `TestTheBrokeredCertificateTier`. The only non-zero `contract.TargetAuth` is built at `ladder.go:78`, called only from `snapshot.go:191`. The compiler refuses every other parameter on the method (`compile.go` `checkParamScope`, `checkDeviceFields`), so a compiled entry cannot carry one. |
| 6 | Authoring floor and bans | wrong → fixed `3d66a2d` | The floor/profile refusal is the proxy's (`ProfileWidensKeyExchange` ≙ `WidensKeyExchange`). The proxy refuses `name == ""` and a literal repeat; the compiler refused `TrimSpace(name) == ""` too, which is more than the proxy. Bundle-wide bans merge into routes only (`compile.go` `rule`), and a deny cannot carry a route (`rule.deny_carries_route`); the merge test now holds a deny rule too. Every refusal carries code, rule, line and parameters; bundle-level ones carry `""`/`0`, as every `policy.*` code does. |
| 7 | The capability report | confirmed | Merge by presence: `handlers.go:394-474`; `TestTheTwoObservationsAreMergedNeverClobbered`, `TestAKeyExchangeReportLeavesAnAppliedRungServable`. Kex keyed `(tenant, hostname, target_port)` in migration 0012 and the handler; no platform column exists to key on. An unparseable `observed_at` is a `400`: `TestAMalformedCapabilityReportIs400` ("an unparseable observed_at"). That is this endpoint's own rule, not #71's: `observed_at` is `format: date-time` and the endpoint's `400` is `BadRequest`, "malformed or failed validation". |
| 8 | Declarations within M5 | wrong → fixed `afbedde` | The `sync.Map` was a memory with no bound: proxy on node A, upgraded on B, rolled back on A, and the row kept B's build while the proxy reached A. `TestTwoNodesConvergeOnAProxysLatestDeclaration` failed on that. Decision: a node may hold a copy of shared state for a stated bound and nothing longer; the write is saved for `fleet.DeclarationRecheck` (1 min), then a no-op upsert re-asserts it. PLAN M5 says so in place. |
| 9 | Log ingest | wrong → fixed `2e62ae8` (test strength), `e92b979` (body limit floor) | `""` on every kind, both paths: `TestSessionLessRecordsAreStoredOnBothPaths`, `TestARecordOfEveryKindMayHaveNoSession` (mutation: refuse `""` on non-`error` kinds, caught). Every `400` is listed below; store failure `5xx` (mutation: every failure `400`, caught by `TestAStoreFailureOnEitherLogPathIsNeverA400`). All or nothing (mutation: skip the refused record, caught by `TestAPartiallyValidBatchStoresNothing`). Priority durable: `Ingest` returns after the commit (`TestAPriorityRecordIsReadableTheInstantItIsAcked`). Body limit: `middleware.go:224-228`. Applying the log limit everywhere was caught; **removing it on the log paths was not**, and is now. |
| 10 | Serials | confirmed | Every rendering and parse in the server goes through `contract.FormatSerial`/`ParseSerial` (`credentials.go:83`, `audit/record.go:529-530`, `audit/query.go:255,274`, `north/audit.go:157,162,413,427`). `pdpconform` uses `strconv` on purpose (comment reworded in `5fb0267`). SQL compares text (`credential_certificate_serial = $N`), and the row is read by `int64` after a `≤ 2^63-1` guard; nothing passes through a float. |
| 11 | Vendored contract and types | confirmed; strengthened `d362c74` | `make contract-check` passes; `contract/UPSTREAM` names `976faaa865cc…`. 22 enums in the document, 22 cases in `TestEnumsMatchContract`, `ProxyConfigReport.state` among them; the test now fails on an enum it has no case for. The fixture equals the proxy's `AlgorithmFloorCapabilities`, `AlgorithmProfileCapabilities` and `OfferableAlgorithms` at `976faaa`, regenerated and compared key by key; the round trip holds and is now tested. |
| 12 | Conformance | wrong → fixed `bf42e07` | CI's `-only` omitted the `error` group (no listed substring matches "error discipline (M11)"), so it never ran against this server; it passes. Missing or vacuous against 0019's spec: ban "as authored" passed for any ban; the #72 `algorithm_profiles` case did not exist; `accepted: true` unchecked on the kex report and the session-less priority record; the certificate's `lifetime_seconds` bound ungraded; the `logging.gap` case did not exist. Runs: mock at `976faaa` 58/58; this server after `migrate` and `seed` on a fresh database 58/58 with CI's list. The config group's first case is the 204, so a run on a database something published to fails, as it should. |
| 13 | Security | confirmed | Every route is behind `withProxyAuth`, which wraps the whole mux (`TestEveryPathRefusesABadCredentialIdentically` walks `Routes()`; the conformance case now names the 0019 routes). The tenant comes from `caller.Tenant` in every 0019 handler. Bodies are bounded by `MaxBytesReader`, 1 MiB, 4 MiB on the log paths, a public key included. All SQL in scope is parameterised; the array filters are `= ANY($N)` and `$N = ANY(col)` with bound values. `go test -race` passes on all nine packages in scope. |
| 14 | The record | wrong → fixed `5fb0267`, `afbedde` | Corrections listed in the summary and below. PLAN §4, §5.2 and §6 agree with the code; §7's severity sentence did not, and M5 was silent on the decision path's memories. `CHANGELOG.md` `v0.6.0`'s south-bound bullet is true. |

### Every `400` on `/v1/logs/batch` and `/v1/logs/priority`

0019's list ended in an ellipsis. This is the whole of it.

- **Never stored (a record this server refuses):** a body that is not one JSON
  object of the endpoint's shape, or carries a second document (`decode`); an
  empty batch (`ErrEmptyBatch`); in `Parse`: a blank `record_id`, a missing or
  unknown `kind`, an unknown `severity`, more than 256 attributes
  (`audit.Limits.MaxAttributes`), a timestamp that is not RFC 3339, a
  `tenant` attribute naming another tenant (`TenantMismatchError`), a payload
  on a kind other than `stream`, a payload that is not base64, a capture over
  `audit.max_capture_bytes`, a body that cannot be encoded or is over
  `audit.max_record_bytes`; and `Row` failing to encode the body. A priority
  request with no `record` arrives as a record with no `record_id`.
- **Halving cures:** more than `audit.max_batch_records`; a body over
  `south.max_log_body_bytes`, which `config.MinLogBodyBytes` keeps above one
  storable record (`e92b979`).
- **Cannot reach a live proxy:** a body read that fails mid-stream is answered
  by `decode` as malformed, but the connection it failed on is the one the
  answer would go back on. The listener sets `ReadHeaderTimeout` and no
  `ReadTimeout`, so no deadline produces this on a live connection.
- Everything else — a store failure, a timeout, a tenant missing from the
  submission — is a `5xx`.

### The two items first left open, and how they were fixed

Both were raised in the PR as low-severity decisions for the user, who asked
for them fixed in this phase.

- **An issuance could cite a decision of any age** (`ec38909`).
  `boundDecision` checked the tenant, the effect, the method, the target, the
  account and the bound proxy, but not when the decision was made. A
  conforming proxy cites a decision only while it may serve a session under
  it, so an older id is a replay, and a certificate minted on it is a fresh
  credential that the `cache_invalidate` and `session_kill` ending the
  decision's reuse never reach. A decision may now be cited for its cache
  hint's TTL, or none, plus `credential.DecisionSetupAllowance` (two minutes,
  for the seconds between the answer and the call); past that it is `401
  unknown_decision` through `refuseToMint`, with its own message. The clock is
  the CA's against the record's `decided_at`, both this server's.
  `TestAnIssuanceCitesADecisionOnlyInsideItsWindow` covers both sides of both
  windows. The south harness's issuance tests now decide on the real clock,
  because the CA signs on it.
- **`south.max_log_body_bytes` was not held above one storable record**
  (`e92b979`). Halving cures a body limit only while one record fits it,
  because a batch of one cannot be halved. `Validate` now refuses a limit below
  `config.MinLogBodyBytes`: `audit.max_capture_bytes` as base64, plus
  `audit.max_record_bytes`, plus `config.LogRecordEnvelopeBytes` (4 KiB). The
  shipped bounds need about 1.4 MiB under a 4 MiB default. `config` restates the
  two audit defaults, held to `audit.DefaultLimits` by
  `TestTheConfigurationKnowsTheShippedBounds`, and
  `TestTheLargestStorableRecordFitsTheLogBodyFloor` marshals the largest record
  `Parse` accepts and finds it under the floor on both paths. The one record the
  floor does not bound is one whose redaction shortened a long value, which is
  larger on the wire than its stored body; a capture-sized record is not that
  record.

### Corrections to 0019's claims, in full

- **"declarations written only on digest change — one write per build change,
  none per call"** (#69/#72): per node, and since `afbedde` also once a minute
  per proxy per node while unchanged, as a no-op upsert. Migration 0012's
  comment says "only when the digest … differs"; migrations are frozen, so this
  file is the correction.
- **"serial … joins `ssh_certificates.serial::text`"** (#68): there is no SQL
  join. `Reader.SessionCertificates` parses each record's serial with
  `ParseSerial`, bounds it to `int64`, and reads the row with
  `SSHCertificates().Get`; `CertificateSessions` filters the text column by
  `FormatSerial`. Migration 0012's comment repeats the SQL claim.
- **"Every `400` the log paths answer, by class"** (#71): incomplete; see the
  list above.
- **PLAN §7, "the kind enum is closed … severity is not"**: `Parse` refuses an
  unknown severity, and has since 0010. Revised in place.
- **CI, "that is now EVERY group the suite has"**: the `error` group was not
  named. Fixed, and the comment says what to check the list against.
- **"`make conform` passes with the certificate / floor, ban and report /
  session-less cases against this server and the mock"**: it did, but five
  cases 0019's prompt specified were missing or graded nothing (item 12).

### Observations, not defects

- Two refusals of a bound token's scope, in `Authorize` (`handlers.go:270`) and
  the uid lease (`:519`), go through `deny`, whose comment says it refuses the
  user's credential; the event stream and fleet configuration use
  `rejectCredential` for the same fact. Both are 401 decisions, so M11 holds;
  only the log line's wording differs. Predates 0019.
- A key-exchange observation is replaced by any later report, even one dated
  earlier than the stored one. The contract states no ordering, and the
  observation grants nothing; worth a look if two proxies report one target.
- `IssueForSession` signs any key type the proxy submits. The route's
  `key_type` tells the proxy what to generate; the contract does not ask the
  server to check it.

### Mutations that changed nothing, and why

- Rendering `valid_before` from the issuance's own clock instead of the
  certificate: `Format(time.RFC3339)` truncates to the second, as the
  certificate does, so the two are equal. Not a gap.

### Cross-repo impact

None for `hoplock/proxy`: the contract is unchanged and nothing here needs a
shape it lacks. None for `hoplock/enterprise`: `ext/` and `server/` are
unchanged (`make release-check`: no public API change), and nothing control#69
told it in its `## Cross-repo impact` is corrected. The one expectation-file
key added to `cmd/pdpconform` is graded by this repository's CI, not
Enterprise's.
