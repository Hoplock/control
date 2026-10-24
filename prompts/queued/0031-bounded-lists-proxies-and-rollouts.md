# 0031 — Bounded lists 3: proxies and configuration rollouts

## Read first
- `docs/PROTOCOL.md`: §1, §3, §4, §5.
- `docs/PLAN.md`:
  - §2: the register, then **M26** (the shape), **M6** and **M17** (what a
    proxy's health carries), **M25** (the subscription copy health reads,
    from memory), **M5**, **M11**, **M18** and **M23**;
  - §3: `internal/fleet` and `internal/httpapi/north`, the paragraph on
    lists;
  - §4: "Configuration distribution (proxy D18)", for what a rollout row
    says and what drift means;
  - §10: this phase's row.
- `docs/learnings/`: every summary. Read
  `0029-bounded-lists-mechanism-targets-and-subjects-learnings.md` whole. In
  `0028-bounded-lists-audit-and-plan-learnings.md`, read the inventory rows
  for these two routes.
- Code:
  - `internal/httpapi/north/inventory.go`: `handleProxyList`,
    `handleConfigRollouts`;
  - `internal/fleet/registry.go`: `Health`, `isLive`;
  - `internal/fleet/config.go`: `ConfigRollouts`, `ConfigDrift`,
    `rolloutOf`;
  - `internal/store/proxies.go`: `List`;
  - `internal/store/fleet.go`: `RelayRegistrations().List`,
    `ProxyConfigs().ListStates`, `proxyConfigStateColumns`;
  - `store.ProxyConfigState.Drifted` (`fleet_types.go`);
  - 0029's `page.go`, store and north.

## Context estimate
- **Reads:** this prompt, M26 and the named decisions (~250 lines), the
  summaries, 0029's learnings, the functions above (~400 lines), and 0029's
  helpers (~200 lines).
- **Changes:** two handlers; `Health` and the two rollout readers in
  `internal/fleet`; three store readers (a page of proxies, and two
  for-these-ids reads); their tests; a pending-list edit; `CHANGELOG.md`.
- **Why it fits:** the files are four, but the changes are paged variants
  0029's helpers make mechanical. The one real design is health's per-page
  reads, and it is specified below.

## In scope

### 1. `GET …/proxies`
- List `proxies`, key `[keyString]` (proxy id). There are no filters.
- `fleet.Registry` gains `HealthPage(ctx, tenant, store.Page)
  (store.Paged[ProxyHealth], error)`. It reads three things, each indexed:
  - a page of proxies (`Proxies().Page`, ordered by `proxy_id`, primary
    key);
  - **for that page's proxies only**, their relay registrations
    (`RelayRegistrations().ForUpstreams(ctx, tenant, ids)`, primary key
    `(tenant, upstream_proxy_id, …)`);
  - their configuration states (`ProxyConfigs().StatesFor(ctx, tenant,
    ids)`, primary key).

  Liveness keeps reading the subscription copy from memory (M25). A read that
  fails fails the page (M26.6).
- The answer is `{proxies, liveness, truncated, next}`. `liveness` is the same
  on every page.
- Keep `Health` only if something other than the handler calls it. Today
  nothing does: delete it, and keep its tests' assertions on `HealthPage`.

### 2. `GET …/fleet/config/rollouts`
- List `rollouts`, key `[keyString]` (proxy id). Its filter is `drifted=true`
  when `?drifted=true`, and nothing otherwise, so a cursor from one view is
  refused on the other.
- The store gains `ProxyConfigs().PageStates(ctx, tenant, drifted bool,
  store.Page)`:
  - it is ordered by `proxy_id` (primary key) and selects every column but
    `desired_document`, which a rollout row never shows;
  - **drift is a predicate in the statement**, not a filter after it, so a
    page of drift is a full page. It must equal `ProxyConfigState.Drifted`
    exactly. Hold that with a table test over every state the two can
    disagree on.
- `fleet.ConfigRollouts` and `ConfigDrift` become one paged reader. Today
  only the handler calls them.
- The answer is `{rollouts, fleet_owned_keys, truncated, next}`.
- `ListStates` stays for the announcer (`config.go`), an internal
  whole-tenant read that 0028's inventory rules outside M26.

### 3. The enumerating test
Move both routes from `pendingBounded` to `pagedLists`, each with a seed of
at least two items. The rollouts route needs two seeds, one per view.

## Out of scope
- `…/fleet/algorithms` and `…/fleet/target-observations` (0032), and every
  other list.
- The fleet graph (`Registry.Graph`) and the announcer: internal, and M5's.

## Acceptance criteria
- Both lists answer in M26's shape, and pass `TestEveryListIsBoundedOrSaysWhyNot`.
- A page of proxies reads relay registrations and states for its own proxies
  and no others. Assert it with a tenant holding more proxies than the page.
- A drifted page never holds a caught-up row, and a walk of the drifted view
  visits exactly the rows `Drifted` selects.
- Two tenants (M18): neither view leaks across, from any cursor.

## Tests
- `internal/store`:
  - `Page`, `ForUpstreams`, `StatesFor` and `PageStates`, each with two
    tenants;
  - the drift-predicate table.
- `internal/fleet`: `HealthPage`, including a proxy with a live relay, one
  with no state row, and one subscribed on another node (the copy).
- `internal/httpapi/north`: the handlers' tests updated, and the bounded test.

## Release (M23)
The next MINOR after the latest tag. Under `### Breaking`:
- both answers are at most 500 by default, and gain `truncated` and `next`;
- `?drifted=true` is now a cursor filter.

## Cross-repo impact
- `hoplock/enterprise`: none. 0028's sync announced the shape, and no `ext/`
  or `server/` identifier changes.
- `hoplock/proxy`: none.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `prompts/implemented/`, and add
`docs/learnings/0031-bounded-lists-proxies-and-rollouts-learnings.md`.
