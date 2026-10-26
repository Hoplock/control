# 0037 — bounded lists 9: the audit joins, negotiated sessions and per-session reads — Learnings

## Summary
- **M26 is complete on Control's routes.** Every `GET` route is paged, signal-only or exempt with its reason; `pendingBounded` and its plumbing are gone. The exemptions that remain: one-object routes, a sign-in step, and the lists bounded by configuration: sign-in methods, the trust bundle, the extension registry, the access-context providers and bindings, a session's loss, plus two documents bounded by their upload body (the claim mapping, a bundle diff). Release `v0.24.0`, behaviour-only `### Breaking`. No migration.
- **`…/audit/blocked-commands`:** `{blocked, truncated, next}`, keyed `[keyTime, keyString, keyInt]` on the record's place, newest first unless `?order=asc`. Cursor binds `label`, `value`, order and window (`blockedFilters`).
- **`…/audit/negotiated`:** `{sessions, truncated, next}`, keyed `[keyTime, keyString]`: latest matching record newest first, then session id. Keyset in `HAVING`, no row comparison. **Items are now `{session_id, latest_recorded_at}`, not bare ids** (M26.2; see Details). Cursor binds `auditFilters(q.Selection())`.
- **Signals:** `…/sessions/{session}/certificates` gains `truncated`; `SessionAlgorithms.Truncated` (`truncated`). Both read `audit.SessionRecordBound` (500) records, plus one more.
- **Store:** `Audit().BlockedCommands(ctx, tenant, BlockedCommandQuery, Page) (Paged[BlockedCommand], error)`, with `BlockedCommandQuery.Ascending` and no `Limit`; `Audit().Sessions(ctx, tenant, AuditQuery, Page) (Paged[AuditSession], error)`, `AuditSession{SessionID, LatestAt}`.
- **Audit:** `Reader.BlockedCommands(ctx, tenant, store.BlockedCommandQuery, store.Page)`; `SessionsBy(…, store.Page) (store.Paged[NegotiatedSession], error)`; `NegotiatedQuery.Selection() (Query, bool)`, no `Limit`; `SessionCertificates` returns `(…, bool, error)`.
- **Deleted:** `windowParams`, `windowLimit` (`audit.go`), `limitParam` (`policy.go`).
- **Measured:** one page of the aggregate over 100 000 matching records (25 000 sessions) is **~43 ms** at any limit, warm; the plan is the GIN index, then a hash aggregate, then a top-N sort.
- **Decisions:** none added or amended; register unchanged. M26 (intro, .4, its test paragraph), §3, §5.2 and §7 revised in place. **Cross-repo:** none owed; `ext/`, `server/`, `contract/` untouched.
- **NEXT:** a `GET` route must be paged, signal-only or exempt on the day it is added; there is no fourth class. Follow-up for the user: `blocked` items and `…/loss` rows render store structs with Go field names (Details).

## Details

### Choices the prompt left open
- **A negotiated session is an object.** M26.2 says a cursor holds nothing
  its caller was not handed, and the key is the session's latest matching
  instant, which a bare id does not carry. 0036 settled the same question the
  same way for drift, unmet and gaps (each gained `stream` and `chain_seq`).
  So each item is `{session_id, latest_recorded_at}`. That is an answer
  shape the prompt's release list did not name, so it is under `### Breaking`
  in `v0.24.0`. Nothing in Control or the conformance suite read the old
  shape except the tests here. The runbook hands `session_kill` each item's
  `session_id`.
- **`?order=asc` on the showcase join.** PLAN §7 already listed it among the
  audit lists that page "newest first unless `?order=asc`", and 0036 read
  that sentence the same way for its five. The join reads
  `audit_records_order_idx` backwards for it (the plan test asserts both
  directions). The negotiated sessions have one order: §7 does not list them
  there, and an oldest-first aggregate would not be what the runbook reads.
- **What the join's cursor binds** is `label`, `value` (even when empty: the
  join matches `env=`), `order`, `from` and `to`, rendered by
  `blockedFilters`. `TestEveryShowcaseFieldBindsItsCursor` reflects over
  `BlockedCommandQuery` as 0036's test does over `AuditQuery`. The
  negotiated cursor binds `auditFilters(sel)`, which always renders
  `order=desc` for it. That is harmless, and means `?order=asc` on
  `negotiated` is ignored and does not refuse a cursor.
- **The question is validated before the page.** A negotiated request asking
  no question, or half of one, is refused for that before its `limit` or
  `after` is read, as before.
- **Store methods keep their names** (`Sessions`, `BlockedCommands`) and
  gain a `Page`. Nothing else called either.
- **The runbook sentence is in §5.2, not §5.4.** The prompt pointed at §5.4,
  which only refers to the runbook's second step. The sentence naming
  `audit/negotiated` is §5.2's, and it and §7's were revised.
- **0043's prompt gained one clause.** Its runbook workflow ends sessions
  from `audit/negotiated`, and now says to read every page first. Its
  "Read first" already names M26 and §5.2.

### Why a session is never read twice in one walk, and can be missed
The order is each session's **latest** matching record, newest first. A
session that gains a newer matching record moves toward the head of the
list. Any position the walk holds is past the head, so the session's new
place is behind that position. A page reads only what lies past its
position, so:
- a session the walk has already listed is not listed again by it, and a
  later walk lists it at its new place
  (`TestASessionThatMovesMidWalkIsNeverReadTwiceInOneWalk`, s-1);
- a session the walk has **not** reached yet, and that moves, also lands
  behind the position, so this walk misses it and the next one finds it
  (the same test, s-4).

That second case is the price of a keyset over an aggregate whose key a
write can change. An offset would not fix it: it would repeat and skip
sessions under inserts. For the runbook it is narrow. After the ban and
`cache_invalidate`, no new leg can negotiate the banned value, so the
records that move a session are late-arriving ones, and the runbook can be
re-run to catch them. PLAN §7 states the behaviour, and so does
`v0.24.0`'s `### Breaking`.

### The measurement
`BenchmarkANegotiatedSessionPageOver100000Records`
(`internal/store/audit_joins_page_test.go`). 200 000 records in 200 streams,
one a second. Half of them, 100 000, negotiated the selection's key
exchange, across 25 000 sessions of four matching records each. Postgres 16
on a 4-vCPU container, warm cache, `-benchtime 20x`:

| Page | ns/op |
| --- | --- |
| first page of 500 | 42 874 015 |
| page of 500 mid-walk (after the 5 000th session) | 45 122 090 |
| first page of 10 000 | 40 376 902 |

The cost is the selection, not the page. `EXPLAIN (ANALYZE)` of a mid-walk
page:

- a Bitmap Index Scan on `audit_records_negotiated_idx`: 100 000 rows,
  4 ms;
- a Bitmap Heap Scan: 24 ms;
- a HashAggregate into 25 000 groups, with `HAVING` as its filter;
- a top-N heapsort: 41 ms in all.

A full walk of this selection at the default limit is 50 pages, about 2 s.
A narrower selection (a window, a ban rather than a common value) costs
proportionally less. Nothing here suggests the kept order needs an index:
one would have to be over an aggregate, which Postgres cannot maintain.

### Tests added
- `internal/store`: `pagedReaders` rows `Audit.BlockedCommands`,
  `Audit.BlockedCommands ascending at one instant`, `Audit.Sessions` and
  `Audit.Sessions at one instant`. The last runs every harness walk, the
  racing writer included, through HAVING's tie. Four tests in
  `audit_joins_page_test.go`:
  - `TestNegotiatedSessionsAreOrderedByTheirLatestMatchingRecord`: a newer
    non-matching record does not move a session, a session-less one is not
    a session, and ties go by id at every limit. It also covers positions
    on, between and around the tie, and a cursor from tenant A read in B.
  - `TestASessionThatMovesMidWalkIsNeverReadTwiceInOneWalk`.
  - `TestBlockedCommandPagesAreExactUnderTies`: two streams at one
    instant, a window, both directions, and rows the join leaves out.
  - `TestTheShowcaseJoinReadsItsOrderFromAnIndex`.
- `internal/audit`: `TestTheSessionReadsSayWhenTheirBoundCutThem` covers 500
  and 501 records for both signals, and that the summary of the first 500
  is served (the 501st negotiates a value it must not name).
- `internal/httpapi/north`:
  - the bounded test: `blocked-commands` (two views) and `negotiated` in
    `pagedLists`, the two per-session routes in `signalLists`. The seed's
    cmd records are now blocked commands on an `env=prod` target, and each
    seed adds two negotiated sessions at one instant.
  - `TestTheJoinAndTheNegotiatedSessionsBindTheirSelectionAndCrossNoTenant`.
  - `TestEveryShowcaseFieldBindsItsCursor`.
  - `TestTheRunbooksQueryWalkedToTheEndListsEverySessionABanMatches`
    (limits 1, 2, 3 and none).
  - The runbook and negotiated refusal tests, now walking pages.

### Mutations the tests caught
- `session_id > $s` → `>=` in HAVING, `max < $t` → `<=`, and HAVING's tie
  clause dropped: each fails the harness's walks and both negotiated tests.
- `session_id DESC` in the aggregate's ORDER BY: the harness and the
  ordering test fail.
- `a.stream` dropped from the join's order: the harness and the tie test
  fail.

### Conformance
`make conform` against this server, migrated through `0019`: 58 of 58.

### Follow-up, not done here
`GET …/audit/blocked-commands` renders `store.BlockedCommand`, and
`…/audit/sessions/{session}/loss` renders `store.GapLoss`. Neither has JSON
tags, so their members are Go field names (`Record.RecordID`, `Command`,
`TargetZone`, `Cause`, `Reports`) where every other north-bound answer is
snake_case under the proxy's names. This predates 0037, and paging did not
need it changed. Since 0019 the north-bound names are a compatibility
promise (M19), and 0042 makes the surface one, so renaming them is a
breaking change that is cheapest before 0042. Raised with the user in the
PR. A phase that takes it renders a view type, as `recordView` does.
