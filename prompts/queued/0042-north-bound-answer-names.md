# 0042 — North-bound answer names: every member named, before the API is a promise

## Read first
- `docs/PROTOCOL.md`: §1, §3, §4, §5.
- `docs/PLAN.md`:
  - §2: the register, then **M19** (point 2: 0043 makes the north-bound API
    a compatibility promise, which is why this runs first), **M26** (M26.2:
    a cursor holds nothing its caller was not handed; M26.3: a list is never
    `null`), **M17** (the capabilities a proxy's item carries), **M11** and
    **M23**;
  - §3: `internal/httpapi/north` and its paragraph on lists;
  - §7: the showcase join and a session's loss;
  - §8: the conventions, which this phase adds one to;
  - §10: this phase's row and the answer-names renumbering note.
- `docs/learnings/`: every summary. In `0037`'s, read Details, "Follow-up,
  not done here", which found the first two answers. Read `0031`'s summary
  for the fleet view and its `liveness` member.
- Code:
  - `internal/httpapi/north/errors.go`: `writeJSON`, the package's only
    writer;
  - `internal/httpapi/north/audit.go`: `recordView`, `viewRecord`,
    `handleBlockedCommands`, `handleSessionLoss`;
  - `internal/httpapi/north/inventory.go`: `handleProxyList`;
  - `internal/store/audit.go`: `BlockedCommand`, `GapLoss`;
  - `internal/fleet/registry.go`: `ProxyHealth`;
  - `internal/fleet/liveness.go`: `Liveness`;
  - `internal/httpapi/north/bounded_test.go` and `audit_list_test.go`, which
    decode the old shapes (`decodeBlocked`, the fleet view's `ProxyID`).

## Context estimate
- **Reads:** this prompt, M19's point 2 and M26 (~150 lines), the summaries,
  0037's follow-up, and the types and handlers above (~300 lines).
- **Changes:** four view types and the handlers that render them; one probe
  and the test that installs it; the tests that decode the old shapes;
  PLAN §8 and §7; `CHANGELOG.md`.
- **Why it fits:** the four answers are found, named and shaped below. The
  guard is the instrument that found them, made permanent.

## Why
Every north-bound answer names its members in snake_case, under the proxy's
names where the proxy has one (M19), except four. Those render store and
fleet structs that carry no `json` tags, so their members are Go field names.
`GET …/audit/blocked-commands` answers `Record.RecordID`, `TargetZone` and
`MatchedRule`. A session's loss answers `Cause` and `Reports`. The fleet view
answers `ProxyID`, `LastHeartbeatAt` (`0001-01-01T00:00:00Z` for a proxy
that never reported) and a `liveness` of `HeartbeatTTL` in **nanoseconds**.
So renaming a field inside Control changes the wire, and nothing fails. 0043
makes the north-bound API a compatibility promise (M19). A rename after it
costs a version of the API; a rename before it costs one MINOR with its
`### Breaking`.

The inventory was not read off the code. Every value the north package's
tests answer was walked, and every exported struct field with no `json` tag
was recorded, through every writer, across `internal/httpapi/north` and
`server`. It found these four answers and nine types: `store.BlockedCommand`,
`store.AuditRecord` and the four facts structs inside it
(`GrantContextFacts`, `EnforcementFacts`, `DeviceChangeFacts`, `GapFacts`),
`store.GapLoss`, `fleet.ProxyHealth` and `fleet.Liveness`. That walk becomes
the guard (5).

## In scope

### 1. The showcase join's items (`GET …/audit/blocked-commands`)
Each item of `blocked` is a `blockedView` (`audit.go`):

```json
{
  "record": { "record_id": "…", "stream": "proxy:p-1", "chain_seq": 7, "…": "…" },
  "command": "rm -rf /",
  "action": "block_command",
  "target_zone": "core",
  "target_labels": {"env": "prod"},
  "decision_found": true,
  "matched_rule": "sre-prod",
  "route_type": "nexthop",
  "decided_at": "2026-10-01T12:00:00Z"
}
```

- `record` is `viewRecord(row.Record)`: the record exactly as an item of
  `GET …/audit/records` renders it, so a client reads one record shape. The
  facts the store struct also carried (grant context, enforcement, the
  device change, the gap) are not members of `recordView`; they are in its
  `body`, as on every other audit list. That is a removal, and goes under
  `### Breaking`.
- `matched_rule`, `route_type` and `decided_at` are omitted when empty. A
  decision this store does not have is `decision_found: false` and nothing
  more, never an empty rule or a year-one instant (the store's comment on
  `DecisionFound` says why).
- `target_labels` is always an object, `{}` when the target has none.
- The list, its order and its key are unchanged (`audit_blocked_commands`,
  M26.4). The cursor's position is the record's recorded-at, stream and
  chain sequence, which the item still carries, inside `record` (M26.2). A
  cursor minted by `v0.24.0` still continues.

### 2. A session's loss (`GET …/audit/sessions/{session}/loss`)
Each row of `loss` is a `lossView`:
`{"cause": "evicted", "reports": 2, "records": 10, "bytes": 2048}`.
`loss` stays `[]` when there is none.

### 3. The fleet view's items (`GET …/proxies`)
Each item of `proxies` is a `proxyView` (`inventory.go`), with these members
in this order:

| Member | From | When absent |
| --- | --- | --- |
| `proxy_id` | `ProxyID` | never |
| `zone` | `Zone` | never |
| `state` | `State` | never |
| `live` | `Live` | never |
| `last_heartbeat_at` | `LastHeartbeatAt` | the proxy has never reported |
| `subscription_seen_at` | `SubscriptionSeenAt` | it holds no subscription, or the signal is unavailable |
| `contract_version` | `ContractVersion` | never |
| `capabilities` | `Capabilities`, already named (M17) | never |
| `relay_registrations` | `RelayRegistrations`, `[]` when none | never |
| `config` | `Config`, already named (proxy D18) | never |
| `session_count` | `SessionCount` | never |
| `last_error` | `LastError` | it is empty |
| `last_error_at` | `LastErrorAt` | it is zero |

An instant is RFC 3339 in UTC. A zero instant is **absent, never year one**:
absent means "never", as `ProxyHealth`'s comments already say it does.
`fleet.ProxyHealth` itself is unchanged: the view is the wire, and the
struct stays the fleet's.

### 4. The fleet view's `liveness`
`liveness` is a `livenessView`:

```json
{"heartbeat_ttl_seconds": 90, "relay_registration_ttl_seconds": 60,
 "target_capability_ttl_seconds": 86400, "report_after_seconds": 21600}
```

Each is integer seconds, **rounded up**, so a bound configured below a
second never reads as 0, which a client would read as no bound.
`report_after_seconds` is the name the capability report already answers the
same interval under (`internal/config`, `capability_report_after`).
`liveness` stays a member that describes the list, the same on every page
(M26.3).

### 5. The guard: an answer names every member
- **The probe.** `writeJSON` (`errors.go`) calls `answerProbe(body)` first,
  when it is non-nil. `answerProbe` is an unexported `func(any)` package
  variable, nil in production. Only `export_test.go` (package `north`) sets
  it, through `SetAnswerProbe`. A source test fails if any non-test file in
  the package assigns it, as `discipline_test.go` does for its rules.
- **The check.** `TestMain`, in a new `answer_names_test.go` (package
  `north_test`), installs a probe that walks every body **by value**. It goes
  through interfaces and pointers, through slices and arrays (their first
  element, or a zero element of an empty slice of structs), and through
  map values. It skips a type that implements `json.Marshaler` or
  `encoding.TextMarshaler`, or whose pointer does. It records every exported,
  non-embedded struct field with no `json` tag, or whose tag names a key that
  is not snake_case (`^[a-z][a-z0-9_]*$`; `-` is allowed). Each record names
  the type, the field and the answer path it was found on
  (`proxies[].ProxyID`). After `m.Run()`, it prints every record and exits 1
  if there are any; otherwise it exits with `m.Run()`'s code.
- **Map keys are data** (labels, attributes, claims, device fields) and are
  never checked.
- **What it sees** is every answer the package's tests produce. The bounded
  test seeds every list and signal route, so every list's items are
  produced. A route no test answers with a `200` is not seen. List any that
  remain unseen in the learnings, and say whether a test should answer it.
- **A host's answers** are the host's (M26.7). If a host fixture in the tests
  renders an untagged type, tag the fixture. Do not exempt it.
- **Shown failing, twice.** On `main` before this phase, the guard names the
  nine types above. With one tag removed from `recordView` (then restored),
  it names that field. Both go in the learnings.

### 6. The plan, in place
- §8 gains one convention: **north-bound answers name every member.** Every
  answer is rendered through a view whose every field has a snake_case
  `json` tag. A store, fleet or service struct is never rendered directly,
  because its field names are Go's, and a rename inside Control would then
  break the wire without failing anything. The guard (5) holds it.
- §7: revise any sentence that names the join's items or a session's loss by
  their old members. §3: the `internal/httpapi/north` entry names the guard
  beside the list test.

## Out of scope
- Every answer whose members are already named, and every member already in
  snake_case. This phase renames nothing that is not in (1)–(4).
- `ttl_seconds` beside the observation lists, which truncates a sub-second
  TTL to whole seconds rather than rounding up. Noticed here, not changed.
- The north-bound API's version and its negotiation (0043, M19).
- The error envelope (`Error`), which is already named.
- Moving `fleet.ProxyHealth`, `store.BlockedCommand` or `store.GapLoss` out
  of their packages, or tagging them. They stay internal types, and the
  views are the wire.

## Acceptance criteria
- The four answers render exactly as (1)–(4) specify, and every member of
  each is snake_case.
- The guard passes on the whole package, and fails in both ways (5) names.
- A join walk continues across the change: a cursor minted before this phase
  continues the list.
- Every zero instant in the fleet view is absent, and `liveness` reads
  `90/60/86400/21600` with nothing configured.
- No status code, list order, key or cursor changes. No `401` appears on any
  path this touches (M11).

## Tests
- `internal/httpapi/north`: each view's members, by name, from a handler
  test. Cover the join with a found decision and with an aged-out one (no
  rule, no instant); a proxy that never reported (no instant) beside one
  that did; `liveness` with defaults and with a sub-second configuration
  (rounded up); and a session's loss with two causes. Also the guard, the
  source test on `answerProbe`, and the existing tests' decoders moved to the
  new names.
- No store or fleet test changes. Those types do not move.

## Release (M23)
The next MINOR after the latest tag. Under `### Breaking`, each answer's old
and new members:
- the join's items: `Record` becomes `record`, rendered as an audit
  record's item, with the record's grant, enforcement, device and gap facts
  now in its `body` alone; the rest become snake_case, with `decided_at`
  absent when the decision is not found;
- a session's loss rows become snake_case;
- the fleet view's items become snake_case, with every zero instant absent;
- `liveness` becomes four `…_seconds` integers, where it was four
  nanosecond counts under Go names.

## Cross-repo impact
- `hoplock/enterprise`: none in `ext/` or `server/`. But its prompts read
  Control's north-bound lists (control-PR#97's sync, its point 2): its
  supervisory plane and console read the fleet view. So this phase owes
  `hoplock/enterprise` a downstream sync when it merges. Queue it in
  `prompts/downstream/queued/` from `docs/KICKOFF.md`'s "Downstream sync"
  block (`docs/CROSS-REPO-PROTOCOL.md` §4.1, §4.3). It names the four
  answers, their new members and the release that renames them.
- `hoplock/proxy`: none.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `prompts/implemented/`, and add
`docs/learnings/0042-north-bound-answer-names-learnings.md`. Its summary
names the guard, and says that a type rendered without its names now fails
the build.
