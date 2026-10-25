# 0032 — bounded lists 4: algorithm declarations and target observations — Learnings

## Summary
- **What shipped:** M26's shape on `GET …/fleet/algorithms` (`{declarations, truncated, next}`, by proxy id), and M26.5's split: `GET …/fleet/target-observations/rungs` (`{rungs, ttl_seconds, truncated, next}`, by hostname, port, platform) and `…/key-exchanges` (`{key_exchanges, ttl_seconds, floor_levels, truncated, next}`, by hostname, port). The combined route is withdrawn (`404 not_found`). Release `v0.19.0`, behaviour-only `### Breaking`. No migration: every order is a primary key.
- **Store:** `ProxyAlgorithmDeclarations().Page`, `TargetCapabilities().Page`, `TargetKexObservations().Page`. The three `List`s stay for `fleet.CheckPolicy` and `authoring`'s publish-time facts.
- **Fleet:** `Registry.AlgorithmDeclarationPage` and `Registry.TargetKexPage`, decoding as `AlgorithmDeclarations`/`TargetKex` do (shared `declaredAlgorithms`, `kexObservation`). The rung list reads the store directly, as the old handler did.
- **North:** `declarationList`, `rungList`, `kexList` (`inventory.go`); handlers `handleAlgorithmDeclarations`, `handleTargetRungs`, `handleTargetKeyExchanges`. **New key kind `keyInt32`** (`page.go`): the port. It is written and read as `keyInt`, but out of 32-bit range neither decodes (`400 after invalid`) nor mints.
- **The test:** three `pagedLists` rows; 0032's two `pendingBounded` rows gone. `TestTheListCheckFailsOnARouteNobodyClassified`'s chunkless case now uses `audit/negotiated` (0037), so 0033–0036 need not touch it.
- **Decisions:** none added or amended; register unchanged. M26.5 and §4's impact-preview paragraph revised in place to name the two lists. **Cross-repo:** none owed. 0028's sync (control-PR#97) already told `hoplock/enterprise` about the split; `ext/`, `server/`, `contract/` are untouched.
- **Gotcha:** `target_port` is `integer`. A cursor's int64 past that range made pgx fail to encode the parameter, which is a `500` from a forged cursor. Hence `keyInt32`.
- **NEXT (0033–0037):** a composite key's int component typed by a 32-bit column takes `keyInt32`. The store's `pagedReader` takes a `key` func for a key of more than one component (`page_test.go`).

## Details

### What the prompt left open, and what was chosen
- **`keyInt32`, not `keyInt`, for the port.** The prompt named the keys
  `[keyString, keyInt, keyString]` and `[keyString, keyInt]`. With `keyInt`, a
  hand-built cursor naming port `4294967318` decoded and reached Postgres as an
  int64 for an `integer` parameter. pgx refused to encode it
  (`unable to encode … into binary format for int4`), which is a
  `KindInternal` store error, so a `500` logged as an outage. M26.2 refuses "a
  component that does not parse as its kind", and M11 keeps a client's bad
  input out of the outage path. So the port's kind is the column's range. It is
  still a decimal string in `k`, and still an `int64` in `store.Page.After`, so
  nothing else in the codec or the store changed. `math.MaxInt32` is a valid
  position (it reads past every row of the host), and one past it is refused.
- **Two fleet wrappers, not three.** "Three paged store readers and their
  service wrappers": declarations and key exchanges each had a fleet reader
  (decode, and map to `KexObservation`), and each gets a paged one. The rung
  observations never went through the fleet on this route: the old handler read
  `TargetCapabilities().List` from the store and built its view itself. A
  pass-through `Registry` method would add nothing, so the new handler reads
  `TargetCapabilities().Page` the same way.
- **List names** are the members: `declarations`, `rungs`, `key_exchanges`. None
  has a `:` (`TestNoControlListNameHasAColon`).
- **Freshness is computed per item at read time**, against `now` taken before
  the page is read, as before. `ttl_seconds` and `floor_levels` go in
  `answerPage`'s `extra`.
- **0043 needed no edit.** The prompt asked to name the two lists wherever the
  console's prompt names the old path. It names no path: its impact preview
  cites §4 "One endpoint carries two observations", and that paragraph now
  names `…/key-exchanges` and `…/rungs`.
- **§3's inventory sentence** ("the algorithm declarations and the target
  observations") is still true and was left as it is.

### Tests added
- `internal/store/page_test.go`: the three readers join `pagedReaders`, so they
  inherit limit and `More`, out-of-bounds pages (now also a position one
  component short, for a composite key), a position from one tenant read in
  another, the walk with writes placed between pages, and the walk racing a
  writer. `pagedReader.key` maps an item's id to its full position; nil keeps
  the one-component lists as they were. `TestObservationPagesFollowTheirWholeKey`
  walks rows that share a hostname: ports in numeric order (22, 80, 2222, not
  as text), the platform as the tiebreak, every column kept, two tenants.
  `TestADeclarationPageKeepsItsDocument` checks the stored bytes and digest
  come back unchanged. `tenancy_test.go` covers the three methods. Test-only
  deletes are in `export_test.go`.
- `internal/fleet/observations_test.go`: the paged wrappers decode as the
  whole-tenant ones do. An undecodable declaration fails the page and leaves it
  empty, and a page that ends before that row still answers.
- `internal/httpapi/north/observations_test.go`: each list's exact member set
  on every page, with `ttl_seconds` and `floor_levels` constant (empty tenant
  included); freshness per item (fresh, stale, undated); the old path
  `404 not_found` with a correlation id, and absent from the router; two
  tenants with the same keys, one tenant's cursor continuing the other's list
  with its rows alone and refused to a one-tenant credential; port cursors at
  and past the 32-bit edge; an undecodable declaration is a `5xx`, never a
  short page.
- `internal/httpapi/north/page_test.go`: `TestAnInt32ComponentKeepsToItsRange`.
- `bounded_test.go`: the three lists walk, refuse, and refuse each other's
  cursors. The rung and key-exchange seeds share a hostname, so the walk
  continues from every component.

### Mutations the tests caught
- Rung order `(hostname, platform, target_port)`:
  `TestObservationPagesFollowTheirWholeKey` fails, because the position's
  components then bind to the wrong columns.
- `keyInt32` decoded like `keyInt` (no range check):
  `TestAnInt32ComponentKeepsToItsRange` and
  `TestAPortCursorOutsideItsRangeIsRefused` fail; the second gets a `500`.

### Known and accepted
- **A host may now serve `GET …/fleet/target-observations`.** The withdrawn path
  is no longer one of Control's, so `CheckHost` does not refuse it. A host
  that served it would answer its own shape there. This is not refused, for the
  same reason any unused path is open to a host.
