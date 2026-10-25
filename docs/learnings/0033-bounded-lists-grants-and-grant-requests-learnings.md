# 0033 — bounded lists 5: grants and grant requests — Learnings

## Summary
- **What shipped:** M26's shape on `GET …/grants` (`{tenant, grants, truncated, next}`, by created-at and grant id) and `GET …/me/grant-requests` (`{tenant, requests, truncated, next}`, by requested-at and request id), both newest first; `decisions_truncated` on `GET …/grants/{grant}`. Release `v0.20.0`, behaviour-only `### Breaking`.
- **Store:** `Grants().Page(ctx, tenant, GrantQuery, Page)` and `GrantRequests().PageForSubject(ctx, tenant, subject, GrantRequestQuery, Page)` replace `List` and `ListForSubject`; `GrantQuery.Limit`, `GrantRequestQuery.Limit`, `defaultGrantLimit` and `maxGrantLimit` are gone. `Decisions().ListByGrant` returns `([]Decision, bool, error)`, reading one past its bound. `storetest.Grants(t, st, tenant)` reads a tenant's grants for tests.
- **Access:** `Service.GrantPage` and `Service.OwnRequestPage` replace `List` and `OwnRequests`; `Inspect` returns `(Grant, []Decision, bool, error)`.
- **North:** `grantList` (`grants`) and `ownRequestList` (`own_requests`), both `[keyTime, keyString]`. The self list's filter is `state`; the subject is `SubjectFrom`'s on every page and never in the cursor.
- **Migration `0017`:** `grants_created_order_idx`, `grants_subject_order_idx`, `grant_requests_subject_order_idx` (each ending `…, <id> DESC`) replace `grants_by_created_idx`, `grants_by_subject_idx`, `grant_requests_by_subject_idx`. All three builds run first, then all three drops (Details: the lock).
- **The test:** `grants` (three views: whole, `subject`, `state`) and `me/grant-requests` are in `pagedLists`; `grants/{grant}` is in `signalLists`, now `map[route]signalList{member, seed}`. A `pagedList` takes an optional `token`, the requester's on the self route.
- **Decisions:** none added or amended; register and PLAN unchanged. **Cross-repo:** none owed; `ext/`, `server/`, `contract/` untouched.
- **Gotcha:** `DROP INDEX` takes an ACCESS EXCLUSIVE lock and holds it to the commit, so in a one-transaction migration a drop placed before another build blocks the decision path's reads for that build.
- **NEXT (0034–0037):** a newest-first list in `store/page_test.go`'s harness keys its items with `newestFirst(id)`; a put that sets a column after its insert does both in one transaction, or the concurrent-walk test sees the row at the database's clock.

## Details

### What the prompt left open, and what was chosen
- **New index names, not the old ones reused.** An index cannot be built under
  a name that exists, and renaming the replacement after the drop would leave
  "grep the store for the dropped index" answering with a live index. The new
  names say what they serve (`…_order_idx`), as 0036's
  `audit_records_order_idx` will.
- **Builds first, drops last.** The prompt asked that each old index be dropped
  only after its replacement exists. Interleaving create/drop pairs meets that,
  but `DROP INDEX` takes ACCESS EXCLUSIVE on its table and a transaction holds
  every lock to its commit, so the first drop would block every read of
  `grants` (the decision path's `ListLive` among them) while the later indexes
  build. With every build first, writes wait for the whole file and reads only
  from the drops to the commit. The changelog states both.
- **Renamed readers, not new signatures under old names**, as 0029 (`Page`) and
  0031 (`HealthPage`) did: `Grants().Page`, `GrantRequests().PageForSubject`,
  `Service.GrantPage`, `Service.OwnRequestPage`. Every test that read
  `Grants().List` to ask "was a grant created?" now calls `storetest.Grants`,
  which fails on more than one page rather than reading a cut list as whole.
- **The filters a grant cursor binds** are `subject=<trimmed>` and
  `state=<state>`, both always present (empty when not given), so a cursor from
  the whole list is refused under `?subject=` and the other way round. The self
  list binds `state=` only.
- **`tenant` stays** on both answers, through `answerPage`'s `extra`, the same
  on every page (M26.3).
- **`ListByGrant` keeps its default** (`defaultDecisionLimit`) for a
  non-positive limit, rather than refusing one as `SessionsByGrant` does: the
  existing tests pass `0`, and the only product caller passes 50.
- **A signal list's sample path names an object**, so `signalList.seed` stores
  it (a grant named `sample`). The signal check also asserts the route answers
  the same `200` with `?limit=1` and with a junk `?after=`: a signal list takes
  neither.

### Tests added
- `internal/store/grant_page_test.go`: a state-filtered walk with the instant
  moving two minutes a page, grants expiring and starting mid-walk (each
  listed at most once, each live one once, the exact sequence asserted); the
  tiebreak across one instant for all three orders; a requester's position
  presented for another subject; `ListByGrant`'s signal at, below and above its
  bound; and the plan test.
- `store/page_test.go`'s harness gains `Grants.Page`, `Grants.Page subject` and
  `GrantRequests.PageForSubject`, so they inherit the limit, bounds, tenant,
  insert/delete and concurrent-write walks. `newestFirst(id)` maps an id to a
  creation instant that falls as the id rises (base 64 over `-`, digits and
  lower case, eight bytes), so a newest-first list walks its ids ascending as
  the harness expects. `SetGrantCreatedAt`, `DeleteGrant`, `DeleteGrantRequest`,
  `GrantPageSQL`, `GrantRequestPageSQL` are in `export_test.go`.
- `internal/httpapi/north/grant_page_test.go`: alice's cursor (decoded: exactly
  `v l f k`, `l` = `own_requests`, no holder, no tenant) presented by bob reads
  only bob's requests; a tenant-a grant cursor continues tenant-b's with its
  rows alone; a handler walk under `?state=active` with the service clock moved
  between pages; the grant view at 50 and 51 decisions.
- `internal/access`: `GrantPage` judged at the service clock on every page, and
  a page above the maximum refused by the store rather than clamped.

### The plan test
`TestTheGrantListsReadTheirOrderFromAnIndex` asks for the generic plan of each
page statement (first page and after a position, under every filter) with only
the new index left on the table and sequential scans off, as 0021's audit plan
test does, and asserts an index scan on it with no `Sort` above it. It then
builds the index 0017 replaced in its place and asserts that plan sorts
(`Incremental Sort`, `Presorted Key: created_at`), so the assertion is shown to
tell the two shapes apart. It also asserts the three old names are absent from
the schema. `grep -rn 'grants_by_created_idx\|grants_by_subject_idx\|grant_requests_by_subject_idx' internal/store`
finds only migrations 0008, 0010 and 0017 and that test.

### Mutations the tests caught
- `PageForSubject` with `subject_id = $2` replaced by `$2::text IS NOT NULL`:
  the requester-crossing tests in both `store` and `north` fail. The enumerating
  test does not, because its seed counts through the same reader; that is why
  the crossing test exists.
- `ListByGrant` reading `limit` rather than `limit+1`: the store signal test and
  the grant view test fail.
- The first version of the concurrent-walk harness entry inserted a grant and
  then set its `created_at` in a second statement. In the full suite, a walk
  read one row between the two, at the database's clock, after every other
  item, and failed "went backwards". `putGrant` now does both in `InTx`.

### Checks run
`go build`, `go vet`, `go test ./...` against Postgres 16, golangci-lint
(v2.13.2 built with Go 1.27.0; CI pins v2.14.0) with 0 issues,
`make contract-check`, `make license-check`, `make release-check` (releases
`v0.20.0`, MINOR), and the `conform-self` leg reproduced locally: migrate
(applying 0017), seed, start, `make conform` 58/58.

### Cross-repo impact
None owed. The north-bound API is not a shared surface
(`docs/CROSS-REPO-PROTOCOL.md` §1), and 0028's sync told `hoplock/enterprise`
the shape every list takes; the changelog carries the defaults, the new members
and the migration's lock to a host that reads it before bumping (E3).
`hoplock/proxy`: none.
