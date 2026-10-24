# 0029 — bounded lists 1: the mechanism, targets and subjects — Learnings

## Summary
- **What shipped:** M26's one paging path, proven on `GET …/targets` and `GET …/subjects` (`{targets|subjects, truncated, next}`), and `TestEveryListIsBoundedOrSaysWhyNot`. Release `v0.16.0`, behaviour-only `### Breaking`. No migration: both orders are primary keys.
- **Store (`internal/store/page.go`):** `DefaultPageLimit` 500, `MaxPageLimit` 10000, `Page{Limit int; After []any}`, `Paged[T]{Rows []T; More bool}`. Unexported: `order{columns []string; desc bool}`; `checkPage(op, order, Page) error`; `(order).after(Page, args) (pred, args)` (`""` on page one); `(order).clause(Page, args) (sql, args)` = ` AND <after> ORDER BY … LIMIT $n` with `Limit+1`; `queryPage[T](ctx, db, op, Page, sql, args, scan) (Paged[T], error)`.
- **Readers:** `Targets().Page(ctx, tenant, labels, store.Page) (store.Paged[store.Target], error)`, `Subjects().Page(ctx, tenant, store.Page) (store.Paged[store.Subject], error)`. `Subjects().List` deleted; `Targets().ListByLabels` stays (`fleet.CheckPolicy`).
- **North (`internal/httpapi/north/page.go`):** `keyKind` (`keyString keyInt keyTime keyBool`), `list{name, key}`, `pageRequest{store.Page; list; filters}`, `readPage(w, r, list, filters ...string) (pageRequest, bool)`, `answerPage(w, r, p, member, items, more, last []any, extra)`; codec `filterDigest`, `encodeCursor`, `decodeCursor`. Lists: `targetList`, `subjectList` (`inventory.go`).
- **The test's four maps** (`bounded_test.go`, keyed `"GET /api/v1/…"`): `pagedLists` (route → `{member, path, seed}`): targets, subjects. `signalLists` (route → member): empty. `exemptRoutes` (route → reason): 0028's 20. `pendingBounded` (route → chunk): 20, 0031–0037. A host's routes are skipped (M26.7).
- **Decisions:** none added or amended; M26 revised in place (its test paragraph), plus PLAN §3's paragraph on lists. **Cross-repo:** none owed.
- **Gotcha:** a statement Postgres fails is usually refused before any row arrives (it sorts first), so "never a short page" is proved by a failure in the scan, after rows were read (`TargetIDPageFailingAt`).
- **NEXT (0030–0037):** a reader adds an `order`, calls `checkPage`, `clause`, `queryPage`; a handler adds a `list`, calls `readPage`, `answerPage`. Move your rows out of `pendingBounded` and give each paged one a `seed`.

## Details

### How a later chunk uses it
A plain list is four lines in the reader and four in the handler:

```go
var grantOrder = order{columns: []string{"created_at", "grant_id"}, desc: true}
// …
if err := checkPage(op, grantOrder, p); err != nil { … }
tail, args := grantOrder.clause(p, []any{tenant, …})
return queryPage(ctx, r.s.db, op, p, `SELECT … FROM grants WHERE tenant = $1 AND …`+tail, args, scanGrant)
```

```go
var grantList = list{name: "grants", key: []keyKind{keyTime, keyString}}
p, ok := s.readPage(w, r, grantList, "state="+state, "subject="+subject)
// … read the page …
s.answerPage(w, r, p, "grants", out, page.More, []any{last.CreatedAt, last.ID}, nil)
```

- **One direction per order.** A row comparison continues a list only when every
  component sorts the same way, which is what M26.4's keys all do. `order` has one
  `desc` for that reason.
- **A list whose order is an aggregate** (`audit/negotiated`, 0037) cannot use
  `clause` whole: `after` goes in a `HAVING`, and the reader writes its own
  `ORDER BY` and `LIMIT` with `Limit+1`, still through `queryPage`.
- **The filters a cursor binds are the ones the reader applies**, rendered
  `name=value`, not the raw query. `?label=env=a&label=env=b` filters on `env=b`,
  so it binds `label=env=b`, and the same filter spelt any other way continues it.
  A list offering two orders (`?order=asc`) passes the order as one of the filters
  (M26.2).
- **`answerPage`'s `extra`** carries the members that describe the list rather
  than the page (`roles`, `liveness`, `tenant`, …). The paging members are written
  after them and cannot be overwritten.
- **A time component** is written `2006-01-02T15:04:05.000000000Z` in UTC, and read
  back as UTC with any RFC 3339 form accepted. Pass the column's own `time.Time`
  as the key: Postgres holds microseconds, so the position is exact.

### Choices the prompt left open
- **`?limit=` empty is absent**, as `limitParam` already treated it; `?after=`
  empty starts the list. A first value wins over a repeated one.
- **A positive integer too large for `int`** (`?limit=99999999999999999999`) is
  `exceeds_maximum`, not `invalid`: it is an integer above the maximum.
- **The codec is strict.** An unknown member, trailing JSON, a padded or standard
  base64 alphabet, a bool spelt other than `true`/`false`, and an int out of
  `int64` are all refused. A cursor never expires; only `v` retires one.
- **The test skips a host's routes** rather than classifying the fixture's
  `test/host` routes. M26.7 says nothing enforces the shape on a host, and a
  fixture's routes in the exemption map would read as Control's.
- **Every map's entries must name a served route**, not only `pendingBounded`'s
  as the prompt asked, so a stale exemption fails as a stale pending row does.
- **The check is a function over a route list** (`listClasses.problems`), so
  `TestTheListCheckFailsOnARouteNobodyClassified` holds its failure modes without
  a database: an unclassified `GET`, one in two maps, an entry naming no route,
  a reasonless exemption, a pending row with no chunk.

### Shown failing once
With `GET /api/v1/tenants/{tenant}/widgets` registered on the real router and left
unclassified (then removed):

```
--- FAIL: TestEveryListIsBoundedOrSaysWhyNot (0.22s)
    bounded_test.go:275: the router and the list classes disagree:
          GET /api/v1/tenants/{tenant}/widgets is a GET route in none of pagedLists, signalLists, exemptRoutes and pendingBounded: page it, signal it, or exempt it with its reason (M26)
```

### Mutations the tests caught
- `queryPage` returning the rows it had on a scan or statement error: both
  `TestAFailureDuringAPageIsNeverAShortPage` (store) and
  `TestAFailureDuringAListPageIsAnOutageNeverAShortPage` (north, which seeds an
  unscannable row after a good one) fail with a `200`.
- `>=` for `>` in the keyset predicate: the walks in both packages fail.

The first version of the store's failure test placed the failure only in the
statement, and the mutation showed Postgres had refused it before sending a row:
the test passed for a reason other than the one it named. It now fails in the scan
as well.

### Tests added
- `internal/store/page_test.go`, over `pagedReaders` (both readers; a later reader
  joins the map and inherits them): limit and `More`, a position between items or
  past the end, an empty tenant, `KindInvalid` on each bad page, a position from
  tenant A read in tenant B, a walk with writes placed between pages (behind,
  ahead, the named item deleted), a walk racing a writer, the failure test, and
  the label filter. `isolation_test.go` and `tenancy_test.go` cover both new
  methods.
- `internal/httpapi/north/page_test.go` (package `north`): the codec by table, the
  limit's rule, `answerPage`'s shape and its `500` on a key it cannot mint.
- `internal/httpapi/north/bounded_test.go`: the enumerating test, its failure
  modes, a cursor decoded to show it holds exactly `v l f k` and no tenant and
  continuing tenant-b's list from tenant-a's position (and refused to a
  tenant-a-only credential), the label filter's binding, and the `5xx`. There
  were no handler tests of either list before this phase.

### Known and accepted
- **A filter value containing `\x1f`** can make two filter sets share a digest,
  because the digest joins sorted filters with `\x1f` as specified. The result is
  continuing one filtered list from a position minted under another, which a
  caller can already do by building a cursor by hand: a cursor carries no MAC by
  design (M26.2), and the binding guards honest mistakes, which do not contain
  `\x1f`. Not worth a refusal on `?label=`.
- `store.defaultAuditLimit` and `maxAuditLimit` still exist beside the new
  constants, with the same values. 0036 replaces `boundLimit` and `limitParam`
  with this path; tie or remove them there.

### Cross-repo impact
None. `server/` and `ext/` are unchanged, so nothing Enterprise builds against
moved, and 0028's sync already announced the shape; 0030 is the phase that owes
one. `contract/` is untouched.
