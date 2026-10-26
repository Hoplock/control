# 0037 — Bounded lists 9: the audit joins, negotiated sessions and per-session reads

## Read first
- `docs/PROTOCOL.md`: §1, §3, §4, §5.
- `docs/PLAN.md`:
  - §2: the register, then **M26** (point 4's one order without an index is
    this phase's, and point 3's signal), **M8**, **M18**, **M11** and **M23**;
  - §5.4 and §7: the emergency runbook (`audit/negotiated`, then
    `revocations`), and §7's query paragraph on M26;
  - §10: this phase's row.
- `docs/learnings/`: every summary. Read `0029`'s and `0036`'s learnings
  whole. In `0028`'s, read the inventory rows for these routes. Read the
  `0023` summary for why `negotiated` says `truncated`.
- Code:
  - `internal/httpapi/north/audit.go`: `handleBlockedCommands`,
    `handleNegotiated`, `handleSessionCertificates`,
    `handleSessionAlgorithms`;
  - `internal/audit/query.go`: `BlockedCommands`, `SessionsBy`,
    `SessionCertificates`, `Algorithms`, `SessionAlgorithms`;
  - `internal/store/audit.go`: `BlockedCommands`, `Sessions`, and 0036's
    `Page`;
  - `internal/httpapi/north/bounded_test.go`.

## Context estimate
- **Reads:** this prompt, M26 and the runbook (~200 lines), the summaries,
  0029's and 0036's learnings, the functions above (~400 lines), and the
  bounded test.
- **Changes:** four handlers; two paged store readers (the join and the
  aggregate); two signals; the bounded test's pending list deleted; tests;
  `CHANGELOG.md`.
- **Why it fits:** 0036 built the audit key. This phase adds one join on that
  key, one aggregate with its own keyset, and two signals.

## In scope

### 1. `GET …/audit/blocked-commands`
- List `blocked_commands`, key `[keyTime, keyString, keyInt]`, on the audit
  record: `a.recorded_at`, `a.stream`, `a.chain_seq`, newest first.
- Filters: `label`, `value`, `from`, `to`.
- `store.Audit().BlockedCommands` becomes paged, with the keyset on the
  record's columns. Its join to targets and decisions is unchanged.
- The answer is `{blocked, truncated, next}`.

### 2. `GET …/audit/negotiated`
- List `negotiated_sessions`, key `[keyTime, keyString]`: a session's latest
  matching record, newest first, then the session id, ascending. That is
  today's order.
- Its direction is mixed, so the keyset is
  `max(recorded_at) < $t OR (max(recorded_at) = $t AND session_id > $s)`, in
  `HAVING`. Do not use a row comparison.
- **No index serves an aggregate, and M26.4 keeps it so.** Each page
  re-reads the selection, which a floor, a ban or a negotiated value over a
  window keeps narrow. Measure one page over a selection of 100 000 records,
  and put the number in the learnings.
- The limit still counts sessions, and the answer is
  `{sessions, truncated, next}`.
- **The runbook's next step ends exactly the sessions listed.** Its doc in
  §7 and §5.4 now says to follow `next` until `truncated` is false before
  ending them. Revise both sentences in place.

### 3. Two per-session signals (M26.3)
A session's certificates and its algorithm records are bounded by what one
session is, so each **needs only the signal**. Neither takes `limit` or
`after`.
- **`GET …/audit/sessions/{session}/certificates`.** `SessionCertificates`
  reads its records through a bound of 500 and one more. The answer gains
  `truncated`.
- **`GET …/audit/sessions/{session}/algorithms`.** `Algorithms` summarises at
  most 500 records. The summary gains `truncated` (`SessionAlgorithms.Truncated`,
  JSON `truncated`) when there were more. The summary of the first 500 is
  still served, because what a session negotiated rarely changes after its
  first legs.

### 4. The enumerating test, finished
- `blocked-commands` and `negotiated` move to `pagedLists`, and the two
  per-session routes to `signalLists`.
- **0036 left one clamped-limit reader for these two handlers:**
  `windowParams` and `windowLimit` (`audit.go`) and `limitParam`
  (`policy.go`). Once both read `readPage`, nothing else reads them: delete
  all three, and read the window through `window` as the paged audit lists
  do. A filter either list reads binds its cursor, as `auditFilters` renders
  the five paged lists' selections.
- `pendingBounded` is now empty. **Delete it and its plumbing**, so no route
  can be "pending" again. From here on, every `GET` route is paged,
  signal-only, or exempt with its reason, and a route added later must be
  one of the three on the day it is added.
- Revise M26's paragraph on the test, and §3's, if either mentions a pending
  list.

## Out of scope
- Anything 0036 paged, and every exemption.

## Acceptance criteria
- The two lists answer in M26's shape, and the two signals say when they were
  cut. All pass the bounded test, which has no pending list.
- **Negotiated sessions:**
  - a walk visits each matching session exactly once;
  - two sessions whose latest record shares an instant are ordered by id,
    and neither is skipped at a page boundary;
  - a session that gains a newer matching record mid-walk may appear again
    in a later walk, never twice in one. Assert it and explain why in the
    learnings.
- **Blocked commands:** a walk is exact under ties.
- Two tenants (M18): no cursor crosses.

## Tests
- `internal/store`: the join and the aggregate pages, with ties, two
  tenants, and the `HAVING` boundary case.
- `internal/audit`: both signals at 500 and 501 records.
- `internal/httpapi/north`: the handlers, the bounded test, and a test that
  the runbook's query walked to the end lists every session a ban matches.

## Release (M23)
The next MINOR after the latest tag. Under `### Breaking`:
- `blocked-commands` gains `truncated` and `next`;
- `negotiated` gains `next`;
- the two per-session answers gain `truncated`;
- `?limit=` above 10 000 is refused on both lists.

## Cross-repo impact
- `hoplock/enterprise`: none in `ext/` or `server/`. 0028's sync announced
  the shape.
- `hoplock/proxy`: none.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `prompts/implemented/`, and add
`docs/learnings/0037-bounded-lists-audit-joins-and-sessions-learnings.md`.
Its summary says M26 is complete on Control's routes, and names the
exemptions that remain.
