# 0031 — bounded lists: proxies and rollouts — Learnings

## Summary
- **What shipped:** M26's shape on `GET …/proxies` (`{proxies, liveness, truncated, next}`) and `GET …/fleet/config/rollouts` (`{rollouts, fleet_owned_keys, truncated, next}`), both keyed by proxy id. Release `v0.18.0`, behaviour-only `### Breaking`. No migration: both orders are primary keys.
- **Store:** `Proxies().Page`; `RelayRegistrations().ForUpstreams(ctx, tenant, ids)`; `ProxyConfigs().StatesFor(ctx, tenant, ids)` and `PageStates(ctx, tenant, drifted, page)`. The two for-ids reads take at most `MaxPageLimit` ids (`checkPageIDs`, `page.go`) and read nothing for none. `StatesFor`/`PageStates` select `NULL` for the desired document (`proxyRolloutStateColumns`), so `DesiredDocument` is nil.
- **Drift in SQL:** `driftPredicate` (`store/fleet.go`) is `ProxyConfigState.Drifted` exactly; `TestTheDriftedPageIsExactlyWhatDriftedSelects` holds the two equal over 20 states.
- **Fleet:** `Registry.HealthPage(ctx, tenant, store.Page) (store.Paged[ProxyHealth], error)` replaces `Health`; `Registry.ConfigRolloutPage(ctx, tenant, drifted, store.Page)` replaces `ConfigRollouts` and `ConfigDrift`. Liveness reads `SubscriptionState.LiveSubscription` once per proxy of the page, from memory (M25).
- **North:** `proxyList` and `rolloutList` (`inventory.go`). The rollouts' one filter is `drifted=true`, bound only when the value is `true`.
- **The test:** `pagedLists` is now `map[route][]pagedList`, a route naming one view per filter (`query`), and every view is walked and cross-refused. 0031's rows left `pendingBounded`.
- **Decisions:** none added or amended; register and PLAN unchanged. **Cross-repo:** none owed; `ext/`, `server/`, `contract/` untouched.
- **Gotcha:** `restart_required = '{NULL}'` and a `timestamptz` of `'infinity'` are rows pgx cannot scan, which makes a "this read never touched that row" assertion out of a real database: poison the rows outside the page.
- **NEXT (0032–0037):** a list with two views takes two `pagedList` entries under one route. A view's members that describe the list go in `answerPage`'s `extra`.

## Details

### What the prompt left open, and what was chosen
- **Liveness per proxy, not per tenant.** `HealthPage` asks
  `LiveSubscription` for each proxy of the page rather than taking
  `LiveSubscriptions`' whole-tenant map, so a page's cost is what the page
  holds however many streams the deployment has. Both read the same memory
  (M25); the answer is the same. `SubscriptionState`'s doc now says
  `LiveSubscriptions` is the graph load's and the fleet view asks per proxy.
- **The for-ids reads skip the document too.** The prompt said
  `PageStates` selects no `desired_document`; `StatesFor` feeds the same
  `rolloutOf`, which never shows it, so it skips it as well. Both interfaces
  say `DesiredDocument` is nil. `GetState`, `ListStates` and `LockState` still
  read it (the fetch and the announcer need it). `scanProxyConfigState` now
  scans the document through a `*string`; it is `NOT NULL`, so a whole-row read
  always sets it.
- **The for-ids reads are bounded.** More than `MaxPageLimit` ids is
  `KindInvalid`: such a read is a whole-tenant read in another form. Nothing
  passes more than a page today.
- **Drift is `Drifted` exactly**, `desired_version <> 0`. The old Go filter
  (`ConfigDrift`) also required the version to be `> 0`. The two part only on
  a negative desired version, which nothing writes (`NextVersion` counts from
  1). The table holds that case so a `> 0` in either half fails it.
- **`?drifted=` other than `true` is the whole view**, as before, and binds no
  filter: 0029's rule that a cursor binds the filter the reader applied.
- **`ProxyHealth` still has no JSON tags**, so a proxy row's keys are Go's
  (`ProxyID`, `Zone`, …) while a rollout row's are snake case. Not this
  phase's to change: it would be a second breaking change to the same answer.
  Worth settling before 0042 freezes the surface.

### The drifted page and its index
The rollout view is ordered by `proxy_id`, the primary key, and the drift
predicate filters as the key is scanned. Migration `0002`'s comment ("the
rollout view reads one tenant's states whole … and filters in Go") is now
stale; it is a merged migration and stays as it is. A drifted page in a fleet
that has caught up scans past every caught-up row to fill `limit+1`; that is
the fleet's size, not the estate's, and no partial index was added for it.

### Tests
- `internal/store/page_test.go`: `Proxies.Page`, `ProxyConfigs.PageStates` and
  its drifted view join `pagedReaders` and inherit 0029's promises. A state
  needs deleting there, which the product never does:
  `store.DeleteProxyConfigState` (`export_test.go`).
- `internal/store/fleet_test.go`: the for-ids reads with two tenants holding
  identical ids, an id with no row, none and too many ids; the drift table,
  walked at limits 1, 2, 3 and the maximum with caught-up rows interleaved;
  `TestFleetRepositoriesDoNotReachAnotherTenant` gains the four readers.
- `internal/fleet/registry_test.go`:
  `TestAPageOfTheFleetViewReadsItsOwnProxiesAlone`. Two real `revoke.Bus`
  nodes over one store; p-3's stream is on node B and its heartbeat has aged
  out, so it is live only through node A's copy, and not live once the stream
  ends. A proxy with a live relay, one with no state row, and two poisoned rows
  past the page. `healthAll` and `rolloutsOf` read the old whole-tenant
  answers as one page for the existing tests.
- `internal/httpapi/north/bounded_test.go`: both routes in `pagedLists` (the
  rollouts with both views), the outage test for each, every page describing
  its list, a full drifted page holding only drift, and both views continued
  across tenants from a tenant-a cursor.

### Mutations the tests caught
- `desired_version > 0` for `<> 0`; dropping `running_version <> desired_version`;
  no drift predicate at all (the store's table).
- `ForUpstreams` → `List`, `StatesFor` → `ListStates` (the poisoned rows), and
  no subscription read (p-3 not live) in `HealthPage`.
- The handler binding no filter (a drifted cursor accepted on the whole view),
  and passing `drifted` as `false` (the walk ran past its items; a caught-up
  row on the drifted page).

### Running the database tests locally
`service postgresql start`, set the `postgres` password, then
`HOPLOCK_TEST_DSN='postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable'`.
The image's golangci-lint is too old for Go 1.27:
`GOTOOLCHAIN=go1.27.0 GOBIN=<dir> go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`
(the CI pin).
