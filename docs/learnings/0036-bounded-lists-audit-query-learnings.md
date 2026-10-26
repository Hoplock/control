# 0036 — bounded lists 8: the audit query — Learnings

## Summary
- **What shipped:** M26's shape on `GET …/audit/records` (`records`), `…/drift` (`changes`), `…/algorithm-policy-unmet` (`unmet`), `…/gaps` (`gaps`) and `…/certificates/{serial}/records` (`{serial, records, …}`), each keyed `[keyTime, keyString, keyInt]` by recorded-at, stream and chain sequence, newest first unless `?order=asc` (all five now take it). Release `v0.23.0`, behaviour-only `### Breaking`.
- **Store:** `Audit().Page(ctx, tenant, AuditQuery, Page)`. `Query` stays for internal readers and runs the same statement (`auditPageSQL`, one renderer) at `Page{Limit: boundLimit(q.Limit)}`. `auditOrder(asc)` is now an `order`. `defaultAuditLimit`/`maxAuditLimit` are gone: `boundLimit` uses `DefaultPageLimit`/`MaxPageLimit`.
- **Audit:** `Find`, `DeviceConfigChanges`, `AlgorithmPolicyUnmet`, `Gaps`, `CertificateSessions` take a `store.Page` and answer `store.Paged[T]`. New `UnmetQuery`, `CertificateQuery`; `DriftQuery`/`GapQuery` lose `Limit`, gain `Ascending`. Each query type has `Selection() Query`. `DriftChange`, `UnmetAlgorithmPolicy`, `Gap` gain `stream` and `chain_seq` (a cursor holds nothing the caller was not handed).
- **North:** `auditRecordList`, `driftList`, `unmetList`, `gapList`, `certificateRecordList`; `auditFilters(store.AuditQuery)` renders the WHOLE selection a reader applies into the cursor's filters; `window` reads `from`/`to`; `ascending(r)` reads `order`.
- **Migration `0019`:** `audit_records_order_idx (tenant, recorded_at DESC, stream DESC, chain_seq DESC)` replaces `audit_records_recorded_idx`; build first, drop last. Write lock on `audit_records` while it builds.
- **Authoring:** the publish-time unmet read is one page of `UnmetLimit`, and `More` is the cut. `New` refuses `UnmetLimit > store.MaxPageLimit`.
- **Decisions:** none added or amended; register unchanged. M26.1 and §7's query paragraph revised in place. **Cross-repo:** none owed; `ext/`, `server/`, `contract/` untouched.
- **Gotcha:** a time in `auditFilters` must be rendered in UTC, or one instant from two zones binds two cursors.
- **NEXT (0037):** its prompt now carries deleting `windowParams`, `windowLimit` and `limitParam`, which only `blocked-commands` and `negotiated` still read. A new `AuditQuery` field must be rendered by `auditFilters`, or `TestEverySelectionFieldBindsTheAuditCursor` fails.

## Details

### Why the cursor binds the selection, not the parameters
The five handlers read different parameters, and three of them map theirs
through a service query type (`DriftQuery`, `UnmetQuery`, `GapQuery`) into
the store's `AuditQuery`. Rendering the cursor's filters from the raw
parameters would let a handler read a parameter and forget to bind it. So
each query type renders its `Selection()`, which the reader itself pages
through, and the handler binds `auditFilters(q.Selection())`. What a cursor
binds is then exactly what the reader applies, by construction, and a field
added to `AuditQuery` is caught by a reflection test over its fields
(`audit_filters_test.go`), which also proves no two fields render the same
filter.

`auditFilters` renders only set fields (an absent filter and an empty one
read the same), `DeviceObjectKind` when non-nil (empty is a value: an
administrator), `DeviceFieldName=value` only beside a name (the reader ignores
a value alone), kinds and severities sorted and once each, instants in RFC 3339
UTC, and always `order=asc|desc`. A cursor from before this phase does not
exist: the five lists had none.

### Choices the prompt left open
- **`?order=asc` on all five.** Only `audit/records` read it. PLAN §7 already
  said all five page "newest first unless `?order=asc`", and the prompt's
  filters include `order`, so the shared reading gives it to each. An unknown
  `order` value is newest first, as before, and binds `order=desc`: the
  filters a cursor binds are the ones the reader applies (0029).
- **`audit/gaps` gained no `from`/`to`.** It never read them, and its window
  is `span_from`/`span_to` over the missing records, never the report's
  instant. Adding a report-time window beside it would be a new filter, not
  paging.
- **`Query` runs the paged statement.** One renderer means the plan tests read
  what both run; `Query` reads one row more than it returns, which is free.
- **The drift, unmet and gap items gain `stream` and `chain_seq`.** M26.2 says
  a cursor holds nothing the caller was not handed; these items carried only
  their instant. `audit/records` and the certificate join already carried
  both. The changelog lists the members as observable, beside `### Breaking`.
- **`UnmetLimit` above one page is refused at `authoring.New`.** It used to be
  clamped to 10 000 by `boundLimit` with `+1` lost in the clamp, so a limit of
  10 000 or more could never report a cut read. Only tests set it.
- **The certificate join's `serial`** is an extra member on every page
  (`answerPage`'s `extra`), canonical decimal, and binds the cursor through
  `CertificateSerial` in the selection: a cursor from serial 9 is refused on
  serial 10.

### The index
`EXPLAIN (GENERIC_PLAN)` with only `audit_records_order_idx` left and
sequential scans off: newest first is `Index Scan using`, `?order=asc` is
`Index Scan Backward using`, neither sorts, and a position is an
`Index Cond: … ROW(…)`, with and without a window. Under the replaced
`(tenant, recorded_at DESC)` every case is an `Incremental Sort`. Dropping
`chain_seq` from the new index makes every case sort, which the test catches
(`TestTheAuditQueryReadsItsOrderFromAnIndex`).

The filtered indexes (subject, target, kind, event, grant) end in
`recorded_at DESC` and were left: a page read through one sorts only the
ties of its boundary instant (an incremental sort), and a page's correctness
never depends on an index. `audit_records_event_idx` is partial on
`event <> ''`, which a generic plan for `event = $n` cannot prove; that is
0021's territory and unchanged here.

The migration builds before it drops, as 0017 learned: `DROP INDEX` takes an
ACCESS EXCLUSIVE lock and holds it to the commit, so a drop first would block
reads for the whole build.

### Tests added
- `internal/store`: two `pagedReaders` rows — `Audit.Page` (newest first, one
  instant per id) and `Audit.Page ascending at one instant` (every record at
  one instant, so every harness walk, the racing-writer one included, is the
  tiebreak's) — each record the first of a stream named for its id;
  `TestAuditPagesBreakTiesAtOneInstantInBothDirections` (two streams and one
  stream at one instant, both directions, limits 1/2/4, a window, and `Query`);
  `TestTheAuditQueryReadsItsOrderFromAnIndex`; `Audit.Page` in the isolation
  and tenancy tables. Test-only `AuditPageSQL` and `DeleteAuditRecord`.
- `internal/audit`: `TestTheFiveAuditListsPageInTheAuditOrder` — each reader's
  items carry their record's place, the list is in the audit order with a
  cross-stream tie, and walks of 1 and 4 a page match the whole list both ways.
- `internal/authoring`: the cut read (existing) plus `UnmetLimit` refused past
  a page and accepted at one.
- `internal/httpapi/north`: the five routes in `pagedLists` (records three
  views: whole, `order=asc`, two kinds; drift two; the rest one), every seed
  two proxies at one fixed instant; `TestAnAuditCursorBindsItsSelectionAndCrossesNoTenant`;
  `TestEverySelectionFieldBindsTheAuditCursor`.

### Mutations the tests caught
- A drift position without its stream and sequence: the bounded test's walk
  repeats an item (`order=asc`) and ends a page empty (newest first).
- `has_algorithms` dropped from `auditFilters`: the reflection test names it.
- `chain_seq` dropped from the migration's index: the plan test sees the sort.

### Conformance
`make conform` against this server, migrated through `0019`: 58 of 58. The
suite reads one record by id, which is unchanged.

### Known and accepted
- A seed at a fixed instant (`auditInstant`, north tests): a `time.Now()`
  seed in two tenants can straddle a second, and the cross-tenant check
  would then read nothing after the position.
- The prompt's "under concurrent ingest" is the store harness's racing-writer
  walk, which appends and deletes records around every page.
