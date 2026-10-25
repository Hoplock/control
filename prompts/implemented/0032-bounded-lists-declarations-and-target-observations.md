# 0032 — Bounded lists 4: algorithm declarations and target observations

## Read first
- `docs/PROTOCOL.md`: §1, §3, §4, §5.
- `docs/PLAN.md`:
  - §2: the register, then **M26** (point 5, a list of two lists, is this
    phase's), **M17** (what an observation is and why it grants nothing),
    **M18**, **M11** and **M23**;
  - §3: `internal/fleet` and `internal/httpapi/north`, where it lists
    inventory routes ("the algorithm declarations and the target
    observations");
  - §4: the paragraph that says `GET …/fleet/target-observations` serves the
    impact preview (search for the route);
  - §10: this phase's row.
- `docs/learnings/`: every summary. Read `0029`'s learnings whole. In
  `0028`'s, read the inventory rows for these routes.
- Code:
  - `internal/httpapi/north/inventory.go`: `handleAlgorithmDeclarations`,
    `handleTargetObservations` and their views;
  - `internal/fleet/observations.go`: `AlgorithmDeclarations`, `TargetKex`,
    `TargetObservationTTL`;
  - `internal/store/algorithms.go`: `ProxyAlgorithmDeclarations().List`,
    `TargetKexObservations().List`;
  - `internal/store/fleet.go`: `TargetCapabilities().List`;
  - 0029's `page.go`.

## Context estimate
- **Reads:** this prompt, M26 and M17 (~200 lines), the summaries, 0029's
  learnings, the functions above (~350 lines), and 0029's helpers.
- **Changes:**
  - two handlers become three;
  - three paged store readers and their service wrappers;
  - one route withdrawn;
  - the plan's mention of the old route;
  - the pending list, and `CHANGELOG.md`.
- **Why it fits:** three plain lists, each ordered by its primary key. There
  is no migration and no join.

## In scope

### 1. `GET …/fleet/algorithms`
- List `declarations`, key `[keyString]` (proxy id), no filters.
- Its reader is `ProxyAlgorithmDeclarations().Page`, on the primary key
  `(tenant, proxy_id)`.
- The answer is `{declarations, truncated, next}`.
- A stored declaration that does not decode stays an outage, failing the
  page (M26.6).

### 2. `GET …/fleet/target-observations`, split (M26.5)
- **`GET …/fleet/target-observations/rungs`**:
  - list `rungs`, key `[keyString, keyInt, keyString]` (hostname, port,
    platform);
  - reader `TargetCapabilities().Page`, primary key;
  - answer `{rungs, ttl_seconds, truncated, next}`, each item with its
    `fresh`, as today.
- **`GET …/fleet/target-observations/key-exchanges`**:
  - list `key_exchanges`, key `[keyString, keyInt]` (hostname, port);
  - reader `TargetKexObservations().Page`, primary key;
  - answer `{key_exchanges, ttl_seconds, floor_levels, truncated, next}`.
- Both take `fleet:read`, as the old route did.
- **The combined route is withdrawn.** It answers Control's `404`, and the
  enumerating test no longer finds it. Remove it from `pendingBounded`; the
  test fails on a pending route that does not exist.
- Is the literal `rungs` or `key-exchanges` already taken by a host? 0023's
  host refusal (`TestAHostCanTakeNoPathAndNoCodeControlServes`) walks
  Control's routes, so a host's route on either is refused at start-up. Say
  so under `### Breaking`.
- **The whole-tenant readers stay, for internal callers outside M26.**
  - `TargetCapabilities().List` serves `fleet.CheckPolicy`.
  - `fleet.AlgorithmDeclarations` and `fleet.TargetKex`, over their store
    `List`s, serve `authoring`'s publish-time facts
    (`internal/authoring/service.go`).

  Each reads the estate once per validate, which 0028's inventory names and
  rules outside this shape. The handlers stop calling them.

### 3. The documents
- **§4's impact-preview paragraph** is the one place the plan names the old
  route by path. Revise it in place to name the two lists.
- **§3's inventory sentence** ("the algorithm declarations and the target
  observations") stays true. Check it rather than assume.
- Grep `prompts/queued/` for the path too. The console's prompt (0043) reads
  the preview, so name the two lists wherever it names the old path.

### 4. The enumerating test
- `fleet/algorithms` and the two new routes go into `pagedLists`, each
  seeded with two items.
- `fleet/target-observations` leaves `pendingBounded`.

## Out of scope
- The publish-time report's use of these observations (0035 bounds what its
  findings name).
- The console's impact preview (0043), which reads the two lists.

## Acceptance criteria
- The three lists answer in M26's shape, and pass the bounded test.
- The old route answers `404 not_found` in Control's envelope.
- Freshness is judged per item as before, and `ttl_seconds` and
  `floor_levels` are the same on every page.
- Two tenants (M18): no list reads across.

## Tests
`Page` for the three readers (with two tenants), the handlers, the bounded
test, and a test that the old path is `404`.

## Release (M23)
The next MINOR after the latest tag. Under `### Breaking`:
- `GET …/fleet/target-observations` is withdrawn for `…/rungs` and
  `…/key-exchanges`, and a host route on either new literal is refused at
  start-up;
- every list here is at most 500 by default, with `truncated` and `next`.

## Cross-repo impact
- `hoplock/enterprise`: none, unless its prompts or plan name
  `GET …/fleet/target-observations`. 0028's sync asks it to look; if it named
  the route, that sync carries the split.
- `hoplock/proxy`: none.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `prompts/implemented/`, and add
`docs/learnings/0032-bounded-lists-declarations-and-target-observations-learnings.md`.
