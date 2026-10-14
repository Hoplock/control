# 0019 — north-bound API, inventory & policy lifecycle — Learnings

## Summary
- **What shipped:** the north-bound surface is complete (policy lifecycle, simulation, explain, inventory, audit query and retention, operator actions, extension and access-context listings, all under `/api/v1/tenants/{tenant}/`, one permission each — table below); `internal/authoring` (the lifecycle and its publish-time report); `cmd/policyctl`; contract re-vendored at `4.7.0` (`hoplock/proxy@976faaa`), `policy_version` `6`; brokered certificates (#68), algorithm floor and bans (#69, #72), the records of #66 and #71, and fleet configuration delivery (D18) reach a proxy. Cuts **`v0.6.0`**, with `### Breaking`. No new M-decision; M2, M4, M8, M16, M17, M21 amended in place, register row M17 now renders in §3 too, new plan §5.6.
- **Bundle states:** uploaded (immutable, numbered, `sha256:` digest, identical source = the existing version) → active (one per tenant, moved in one transaction) → superseded (kept, diffable, simulable, activatable). Rollback = activating an older version, reported `rollback: true`. `apply` = upload + activate.
- **Severities** (ext's vocabulary, Control's checks and a validator's alike): `advice` shown, never gates; `warning` gates activation until accepted by finding id (`409 findings_not_accepted` lists them), recorded on `operator.policy.activated`; `blocking` (`422 policy_blocked`) only from a registered validator. Compiler refusals are `422 policy_invalid`, verbatim. **This changes `ext.FindingWarning`'s meaning** (it "blocked nothing") — doc-only, no API diff, listed under Breaking.
- **Simulation:** `POST …/policy/simulate {source|version|neither=active, cases[], replay{from,to,limit≤10000}, labels}` → `{digest, version, dry_run[], replay{replayed, unchanged, truncated, allow_to_deny[], deny_to_allow[], unreplayable[]}}`, flips grouped by direction and was/now rule. Replay is pure and total: inputs only from the record (incl. external context — never re-probes), no clock, no fleet/subject/target re-read; a relabel is an explicit label override.
- **Explain:** `GET …/decisions/{id}`, `GET …/sessions/{id}/decisions` → `{decision_id, session_id, proxy_id, effect, decided_at, rule, rule_line, mapping{version, active}, obligations, grant, inputs, explanation, snapshot, unserved}`; an unexplainable record is `500 decision_unexplainable`, logged at error; `""` session refused (`400`).
- **policyctl:** `validate | diff [-against N] | apply [-accept ID]… | activate N [-accept ID]… | versions | simulate [-from -to -cases -fail-on-flip] | explain ID | -session S`; one TSV record per line or `-json`; exit `0` ok, `1` refused, `2` usage/transport. Env `HOPLOCK_SERVER`, `HOPLOCK_TOKEN`, `HOPLOCK_TENANT`.
- **D18:** versions `v<N>`, hashes `sha256:<hex>`, ETag = quoted hash; fleet-owned keys only in `fleet.fleetOwned` (`internal/fleet/configwire.go`), tied to the contract by `TestTheFleetOwnedKeysAreTheContracts` (extracts 17 keys from the vendored description); report stored on `proxy_config_state` (`report_*`), drift derived from it; `announced_hash` retries a lost `config_changed`; `Heartbeat.RunningConfigVersion` **removed**; fetch is `204` when nothing is published.
- **#66/#71:** `""` accepted on every kind, refused only as a session lookup (`Reader.Session`, `SessionLoss`, `Algorithms`, `SessionCertificates`, explain); `credential_method`/`credential_rung` the only names (column rename, rung from 1, degraded = rung > 1); drift feed filters `device`, `account`, `object_kind`, `op` on derived columns; weakening = profile present and not `default`; unmet warning reads stored `target.algorithm_policy_unmet` over 30 days; gaps on `gap_*` columns, loss summed per cause; log body limit `south.max_log_body_bytes` 4 MiB.
- **#68:** tiers in `internal/contract/enums.go`; `credential.LadderEntry` the one renderer, tripwire pins policy-only params; issuance `200/400/401 (refuseToMint)/503 (noAuthority)`, bound to the decision's proxy; lifetime = min(CA default, CA ceiling, `lifetime_seconds`); serial joins as canonical decimal text.
- **#69/#72:** `requiredVersion` → 6 floor/ban, 5 certificate, 4 else; `Declares()` = rungs only (floor-only/profiles-only → false); emptied axis judged per proxy in `checkAlgorithms` and per build at publish time; kex stored by host+port beside rungs; declarations written only on digest change (M5); runbook = `policy/apply` → `revocations {cache_invalidate, all}` → `audit/negotiated` → `revocations {session_kill}`.
- **Deviations:** binding route is `…/access-context/providers/{provider}/binding` (mux conflict); stale/absent capability evidence is advice, not a warning; `seed` stays the database-direct bootstrap. Sync for `hoplock/enterprise` queued (see PR).

## Details

### The route table and who may call it

Every route below is `AccessTenant` under `/api/v1/tenants/{tenant}`. Reading
permissions (`policy:read`, `grant:read`, `fleet:read`, `identity:read`,
`ca:read`, `audit:read`, `decision:read`, `access-context:read`,
`extension:read`, `license:read`) are held by every reading role: `auditor`,
`policy-author`, `grant-admin`, `fleet-admin`, `admin`. `admin` holds every
permission. `integration` holds only `access-context:push`, `requester` only
`grant:request`.

| Method | Path | Permission | Roles that hold it |
| --- | --- | --- | --- |
| POST | `/policy/validate` | `policy:read` | readers |
| GET | `/policy/bundles` | `policy:read` | readers |
| POST | `/policy/bundles` (upload) | `policy:write` | policy-author, admin |
| GET | `/policy/bundles/{version}` | `policy:read` | readers |
| GET | `/policy/bundles/{version}/diff?against=` | `policy:read` | readers |
| POST | `/policy/diff` (two sources) | `policy:read` | readers |
| POST | `/policy/bundles/{version}/activate` | `policy:publish` | policy-author, admin |
| GET | `/policy/active` | `policy:read` | readers |
| POST | `/policy/apply` | `policy:publish` | policy-author, admin |
| POST | `/policy/simulate` | `policy:read` | readers |
| GET | `/decisions/{decision}` | `decision:read` | readers |
| GET | `/sessions/{session}/decisions` | `decision:read` | readers |
| GET | `/targets`, `/targets/{target}` | `fleet:read` | readers |
| PUT, DELETE | `/targets/{target}` | `target:write` | policy-author, fleet-admin, admin |
| POST | `/targets/relabel` | `target:write` | policy-author, fleet-admin, admin |
| GET | `/subjects`, `/subjects/{subject}` | `identity:read` | readers |
| PUT, DELETE | `/subjects/{subject}` | `identity:write` | admin |
| GET | `/role-bindings` | `identity:read` | readers |
| POST | `/role-bindings`, `/role-bindings/remove` | `identity:write` | admin |
| GET, PUT | `/identity/claim-mapping` | `identity:read` / `identity:write` | readers / admin |
| GET | `/proxies` | `fleet:read` | readers |
| POST | `/proxies/enrollments`, `/proxies/{proxy}/withdraw` | `fleet:write` | fleet-admin, admin |
| PUT | `/fleet/config/{scope}/{id}` | `fleet:write` | fleet-admin, admin |
| POST | `/fleet/config/{scope}/{id}/rollback` | `fleet:write` | fleet-admin, admin |
| GET | `/fleet/config/rollouts`, `/fleet/algorithms`, `/fleet/target-observations` | `fleet:read` | readers |
| POST | `/revocations` | `session:revoke` | grant-admin, admin |
| GET | `/extensions` | `extension:read` | readers |
| GET | `/access-context/providers`, `/access-context/bindings`, `/access-context/providers/{provider}/binding` | `access-context:read` | readers |
| PUT, DELETE | `/access-context/providers/{provider}/binding` | `access-context:write` | admin |
| GET | `/audit/records`, `/audit/records/{record}` | `audit:read` | readers |
| GET | `/audit/blocked-commands`, `/audit/drift`, `/audit/algorithm-policy-unmet`, `/audit/negotiated`, `/audit/gaps` | `audit:read` | readers |
| GET | `/audit/sessions/{session}/loss`, `…/algorithms`, `…/certificates`, `/audit/certificates/{serial}/records` | `audit:read` | readers |
| POST | `/audit/retention`, `/audit/captures/retention` | `audit:retain` | admin |

The routes 0011, 0012, 0013 and 0016 bound (federation, sessions, the CA,
grants, grant requests, the push) are unchanged. Every write this phase adds records
`operator.<noun>.<verb>` on the control stream (`audit.OperatorAct`: actor,
before, after, accepted warnings), inside the write's transaction wherever
there is one; a binding write is `access_context.binding_put` or
`…binding_deleted`, as 0013 records it. The approver of the prompt's minimum
role set is `grant-admin` (`grant:approve`).

### The publish-time report

`authoring.Report{valid, digest, rejections[], findings[]}`. A finding is
`{id, severity, code, source, rule, line, parameters, message}`; Control's
codes are `authoring.ControlCodes` (13, in three families: `satisfiability.*`,
`authoring.*`, `algorithm.*`), held by `TestControlCodesIsEveryFindingCode`.
The id is `code/part/part…` — the rule and what the finding is about (proxy,
target, axis, cause) — never the message, so it is stable for one
bundle against one fleet and a CI job can carry `-accept` lines forward. A
validator's finding id includes its provider and `Path`. The report is
recomputed at activation against the fleet as it is then; accepting an id
that no longer arises accepts nothing and is not an error.

What is a warning and what is advice:

- **Warning:** no declaring proxy provides a rung/method/platform/device field
  (`satisfiability.no_proxy_provides`); a fresh target record says it cannot
  take an applied rung (`…target_cannot_take`); an interpreter in a restricted
  allow-list under an `account-restricted`/`account-confined` rung; a cache
  hint on a rule gated by a condition the key does not carry, on a route no
  grant and no duration bounds; a floor level a build did not declare; an axis
  a ban empties on a build (one finding per declaring proxy); a ban no recorded
  build offers; a target observed below the floor; a stored
  `target.algorithm_policy_unmet` under the route's profile.
- **Advice:** no proxy has declared anything (`satisfiability.proxies_unreported`
  — proxies that declared nothing are left out of every per-build judgement);
  a stale, undated or absent target record (`…target_unproven`); a declaration
  that omits the route's profile or `algorithm_profiles`
  (`algorithm.declaration_unknown`); a target with no fresh key-exchange
  observation.

The interpreter list ships in `authoring.DefaultInterpreters` and is replaced
whole by `policy.interpreters`. The unmet window is 30 days
(`authoring.Options.UnmetWindow`).

### Simulation and explain

Dry-run cases are `{name, now, subject{id, source, groups, claims,
auth_method, mfa}, target{hostname, port, zone, labels}, context{proxy_id,
source_addr}, device{posture}, grants[]}`; each answers `{name, effect, rule,
rule_line, terms, deny_reason, obligations}`. Replay reads at most
`authoring.MaxReplayRecords` (10000) decisions in `[from, to)` through
`store.Decisions().ListRange` and says `truncated` when it stopped short. A
record whose inputs do not decode is listed under `unreplayable` with its
reason, never skipped. What replay compares is the engine's effect; a record
that allowed and went unserved compares as the allow it was.
`POST …/targets/relabel {changes[], replay, apply}` diffs the label change,
replays it under the active policy, and writes it only with `apply: true`.

Explain is total by construction: everything comes from the record, the grant
from `inputs.grants[]` by the id `explanation.grant` names, so it never joins
the grant table. `mapping.version` is the claim mapping the decision was taken
under and `mapping.active` the one active now.

### Fleet configuration delivery (proxy D18)

- **Wire rendering:** `fleet.WireConfigVersion(int64)` → `v<N>`,
  `fleet.WireConfigHash(hex)` → `sha256:<hex>`, both parsed back by their
  `Parse…` pair; the fetch's ETag is the wire hash quoted, and
  `If-None-Match` holding it is `304`. Neither is compared with a literal by
  the conformance suite.
- **Fleet-owned keys:** `fleet.fleetOwned` (17 keys with their kinds) is the
  one list; `fleet.FleetOwnedKeys()` exposes it. `TestTheFleetOwnedKeysAreTheContracts`
  extracts the backticked keys from the vendored `ProxySettings` description
  and asserts the count and the set, and `TestTheFleetOwnedKeyTestCatchesDrift`
  proves the extraction fails on a lost or an added key. A publish naming any
  other key is `422 config_key_not_fleet_owned`; a value of the wrong shape
  `422 config_value_invalid`.
- **Report storage and drift:** `POST /v1/proxies/{id}/config/report` writes
  the `report_*` columns of `proxy_config_state` under `FOR UPDATE`; an
  unchanged report touches `last_reported_at` only, an older one overwrites
  nothing. Drift (`Registry.ConfigDrift`, `GET …/fleet/config/rollouts`) is
  every proxy whose running hash is not the desired one, with its state:
  `pending_restart` waits for a restart, `rejected` needs an operator and
  carries `last_error` (keys, never values), `fetch_failed` is retrying.
- **Delivery without reconnect:** a publish commits first and announces after;
  `announced_hash` lags the desired hash until a `config_changed` is taken or
  a report confirms it, and the announcer re-emits on start and every 15 s.
- **`Heartbeat.RunningConfigVersion` is removed.** The contract never carried
  it; the report is the only statement of what a proxy runs.
- **Fetch:** `200` with the document, `304` on the held ETag, `204` when
  neither the proxy's zone nor the proxy has a published scope (the proxy runs
  on its bootstrap file), `404 not_enrolled` for an id this server has not
  enrolled — never `401`.

### `Hoplock/proxy#66`

- **Which records may lack a session:** every kind (since #71). `Parse`
  accepts `""`; the by-session reads and explain refuse it.
- **Credential columns:** `credential_method`, `credential_rung` (renamed from
  `target_auth_*`; no rewrite, nobody ever sent anything else). Degraded is
  `credential_rung > 1` (`Reader.DegradedCredentials`); a record with no rung
  is not degraded.
- **Drift feed:** `Reader.DeviceConfigChanges(DriftQuery{Device, Account,
  ObjectKind, Op})` over the derived columns `device_change_op`,
  `target_account`, `device_object_kind` and `target`, served at
  `GET …/audit/drift?device=&account=&object_kind=&op=`.
  `object_kind=administrator` matches the empty kind.
- **Weakening:** `Reader.Weakened` — `algorithm_profile` present and not
  `default`; an absent profile is a record not about a target leg.
- **Algorithm-policy warning:** reads `Reader.AlgorithmPolicyUnmet` (records of
  kind `target.algorithm_policy_unmet`, 30 days, all targets) in
  `authoring.Service.gather`, matched to a rule's reachable targets under the
  profile the record names.

### `Hoplock/proxy#68`

- **Tier:** `contract.VocabularyBrokeredCertificate` (5) in
  `internal/contract/enums.go`; a route naming `brokered-certificate` at any
  rung is refused to a proxy declaring below it, `5xx` naming both numbers.
- **Renderer:** `credential.LadderEntry(model.CredentialEntry)` is the one
  renderer (`decision/snapshot.go`'s ladder calls it). The tripwire,
  `TestABrokeredCertificateEntryCarriesPolicyOnly`, now pins the entry to
  `username`, `key_type`, `lifetime_seconds` and names `certificate`,
  `certificate_serial`, `ca_public_keys` one by one.
- **Issuance:** `200`; `400` for no `session_id`, an unparseable key or a key
  that is a certificate; `401` through `south.refuseToMint`
  (`contract.RefusedToMint`: `unknown_decision`, `decision_mismatch`) — a
  third denier beside the user's and the proxy token's; `503` through
  `noAuthority` (no CA, or no KEK); anything else a `5xx`.
- **Proxy binding:** yes. A bound credential's proxy must equal the
  decision's `proxy_id`; an unbound token is held to the tenant. The session
  id is deliberately not matched, because a cached decision serves many.
- **Lifetime:** `min(credential.certificate_validity, credential.max_certificate_validity,
  lifetime_seconds)`, valid from one minute before issuance; `lifetime_seconds`
  is an upper bound (`IssueRequest.UpperBound`), never a request.
- **Serial join:** `audit_records.credential_certificate_serial` holds the
  canonical decimal (`contract.FormatSerial`, parsed as uint64, never through
  a float) and joins `ssh_certificates.serial::text`
  (`Reader.SessionCertificates`, `CertificateSessions`).

### `Hoplock/proxy#69` and `Hoplock/proxy#72`

- **Tiers:** `requiredVersion` (`internal/decision/vocabulary.go`) answers
  `VocabularyAlgorithmFloor` (6) for a floor or any ban, else
  `VocabularyBrokeredCertificate` (5) for a certificate rung, else
  `VocabularyBaseline` (4); `contract.PolicyVersion` is 6. The version check
  runs before the capability checks.
- **Per-level declaration:** `checkAlgorithms` (`internal/decision/capability.go`)
  — a floor level the request did not declare is never sent.
  `ProxyCapabilities.Declares()` answers **false** for a floor-only and for a
  profiles-only declaration: it is a rung declaration.
- **Authoring:** a route's `algorithm_floor` and `algorithm_bans` (refusals
  `route.algorithm_floor_profile_conflict`, `algorithm_ban_empty`,
  `algorithm_ban_repeated`, matching the proxy's own and no more); a
  fleet-wide ban is the bundle's top-level `algorithm_bans`, merged into every
  allow route's at compile.
- **Emptied axis:** decision path — `checkAlgorithms` refuses the route to
  the asking proxy from its own declared lists (`fleet.JudgeAlgorithms`);
  publish time — `algorithm.axis_emptied`, one warning per rule, declaring
  proxy and axis, so each build's answer is its own;
  unknown (advice, answered as authored) — a declaration omitting the route's
  profile or `algorithm_profiles`, or none at all.
- **Observations:** rungs stay in `target_capabilities`; the key exchange is
  `target_kex_observations`, keyed `(tenant, hostname, target_port)`, never by
  platform. A report replaces only the observation it carries.
- **Declarations within M5:** `proxy_algorithm_declarations` holds the three
  lists as one JSON document with its sha256; the decision path keeps the
  last digest per proxy in memory and writes only when it changes — one write
  per build change, none per call.
- **Negotiated queries:** `Reader.SessionsBy(NegotiatedQuery)` over the GIN
  indexed `negotiated` (`axis:value`) and `algorithm_bans` (`axis:name`)
  columns and `algorithm_floor`, at `GET …/audit/negotiated`;
  `Reader.Algorithms(session)` at `…/sessions/{session}/algorithms`.
- **Runbook:** ban — `POST …/policy/apply`; invalidate — `POST …/revocations
  {"type":"cache_invalidate","all":true}`; find — `GET …/audit/negotiated?axis=…&value=…`
  (or `floor=`, or `ban_axis=&ban_name=`, exactly one);
  kill — `POST …/revocations {"type":"session_kill","session_ids":[…],"reason":…}`.
  `TestTheEmergencyRunbookIsCallableAndAudited` drives all four with the roles
  that hold them. The impact preview is `GET …/fleet/target-observations` plus
  the publish-time findings.

### `Hoplock/proxy#71`

- **`""`:** accepted on every kind; refused as a session lookup by the
  by-session reads and explain (`audit.ErrNoSession`, `decision.ErrNoSession`).
- **`logging.gap`:** projected onto `gap_cause`, `gap_records`, `gap_bytes`,
  `gap_first_at`/`gap_last_at` (the missing records' span, never the gap
  record's own time), `gap_kinds`, `gap_critical` (a count), `gap_record_ids`;
  indexed by span and by record id. `Reader.SessionLoss` sums every report of a
  cause, because the proxy never rewrites one.
- **Every `400` the log paths answer**, by class: *never stored* — a body
  that is not one JSON document, an empty batch, a record `Parse` refuses
  (unknown kind, missing `record_id`, bad timestamp…), a record that cannot be
  encoded, a record naming a tenant the proxy is not enrolled in; *halving
  cures* — a body over `south.max_log_body_bytes`, more than
  `audit.max_batch_records`. Store failures, timeouts and overload are `5xx`.
- **Body limit:** `south.max_log_body_bytes`, 4 MiB by default, separate from
  `south.max_body_bytes` (1 MiB): a full default batch of a busy session's
  capture is about 2.7 MiB of JSON.

### Retention

`POST …/audit/retention {stream, through_seq | before}` deletes the
contiguous prefix of one stream through a sequence, or received before an
instant, never the head, under the stream's append lock,
and moves `audit_retention(through_seq, through_hash)` forward;
`POST …/audit/captures/retention {before}` empties capture bytes and sets
`retained_at`. Each pass records `operator.audit.retained`. The verifier starts
from the marker, counts retained captures (`StreamResult.RetainedThrough`,
`CapturesRetained`), and `hoplock-control audit-verify` prints both;
`internal/audit/retention_test.go` runs the verifier after a pass.

### Conformance keys added

`config.proxy_id`, `config.unknown_proxy_id`, `config.publish_url`,
`config.publish_method`, `config.publish_body`, `config.publish_token`;
`certificate.route`, `certificate.username`; `algorithms.floor`,
`algorithms.floor_level`, `algorithms.ban`, `algorithms.kex_target`. The
existing `logs.read_url`/`read_token` and `events.publish_url`/`publish_token`
now point at the north-bound routes and scoped tokens `control-seed.yaml`
seeds. CI generates a throwaway CA key for the mock and sets `HOPLOCK_KEK`.

### Where this differs from the prompt, and why

- **The binding route is `…/access-context/providers/{provider}/binding`**,
  not `…/access-context/bindings/{provider}`: `net/http`'s mux refuses the
  latter beside 0013's `…/access-context/{provider}/push` as an ambiguous
  pattern pair. The list stays at `…/access-context/bindings`.
- **"Warn, do not refuse" is two severities.** A definite shortfall is a
  warning that gates activation until accepted (the override the prompt asks
  for on interpreters, applied to every warning so there is one rule); the
  absence of proof is advice that gates nothing, so a stale record never adds
  friction to authoring an unprobeable appliance.
- **`hoplock-control seed` stays the database-direct bootstrap.** The API cannot
  mint its own first credential, and keys, MFA, channel tokens and host keys
  have no north route; it now also seeds scoped API tokens (`api_tokens`).
- **Replay is capped** at 10000 records per request and says so, rather than
  being unbounded on a north-bound request.

### NEXT session

0020 versions this surface (M19): `north.AllCodes` and `authoring.ControlCodes`
are the code registries it promises. 0021's console reads these routes and
nothing else; the runbook and the impact preview are already callable. 0022
drives `policyctl` and the D18 delivery end to end.
