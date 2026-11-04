# 0045 — north-bound answer names — Learnings

## Summary
- **Release `v0.32.0`, MINOR, behaviour-only `### Breaking`; no migration; `ext`/`server`/`contract` untouched.** No decision added or amended; §3, §7, §8 (one new convention) revised in place.
- **The guard:** `writeJSON` (`errors.go`) calls `answerProbe(body)` when non-nil (nil in production; set only by `export_test.go`'s `SetAnswerProbe`; `TestNoSourceFileSetsTheAnswerProbe` holds that). `TestMain` in `answer_names_test.go` walks every body the package's tests produce, by value, and **exits 1 naming type, field and answer path for any exported field with no snake_case `json` tag. A type rendered without its names now fails the build.**
- **Views (wire, not internal types):** `blockedView` (`record` = `recordView`; `decided_at`/`matched_rule`/`route_type` omitted when not found; `target_labels` `{}`), `lossView`, `proxyView` (zero instants absent, `relay_registrations` `[]`), `livenessView` (`…_seconds`, rounded up, `ceilSeconds`), and `mappingRejectionView`. Shared `instant(t) *time.Time` (`audit.go`): zero → nil → absent.
- **Deviation:** the guard found a **fifth** answer the prompt's inventory missed: `PUT …/identity/claim-mapping`'s `parameters.rejections` rendered `identity.MappingRejection` (`Code`/`Field`/`Message`). Given a view, under `### Breaking`. Policy rejections were already named (`authoring.Rejection`).
- **Unchanged:** `fleet.ProxyHealth`, `store.BlockedCommand`, `store.GapLoss`, `identity.MappingRejection`; positions and cursors (computed from store rows, so a `v0.24.0` cursor continues).
- **NEXT:** a new answer must be a tagged view on the day it is written. Host answers are not walked (written by the host, not `writeJSON`). `ttl_seconds` beside the observation lists still truncates a sub-second TTL (noticed, not changed).

## Details

### Shown failing, twice
1. **On `main` before this phase** (`e374ae6`, with only the probe, the hook
   and `answer_names_test.go` applied), the guard named 88 members across
   ten types: `store.BlockedCommand` (9), `store.AuditRecord` (34),
   `store.GrantContextFacts` (6), `store.EnforcementFacts` (4),
   `store.DeviceChangeFacts` (3), `store.GapFacts` (8), `store.GapLoss` (4),
   `fleet.ProxyHealth` (13), `fleet.Liveness` (4) — the prompt's nine — and
   `identity.MappingRejection` (3), at `parameters.rejections[].Code` etc.
   Paths seen: `blocked[].Record.Grant.WindowStart`, `loss[].Cause`,
   `proxies[].ProxyID`, `liveness.HeartbeatTTL`.
2. **With `recordView.Stream`'s tag removed** (then restored), it named
   exactly `north.recordView.Stream at Stream`, `at blocked[].record.Stream`
   and `at records[].Stream`, and failed the package.

### Why the inventory missed the mapping rejections
The prompt's walk recorded every value the tests *answer*; the rejections sit
inside an error envelope's `parameters` map, which the prompt put out of scope
as "already named". The envelope is; a map value inside it is not. The probe
walks map values (keys are data), so it found them. The acceptance criterion
"the guard passes on the whole package" and "do not exempt" left a view as
the only option; it renders `{code, field, message}`, the policy rejections'
names, with `field` absent at document scope.

### How the walk works (`answer_names_test.go`)
Interfaces and pointers are followed; a nil pointer, and an empty slice or
array, walks a zero value of its element when that leads to a struct (once
per type on the path, so a self-referential type terminates). The first
element of a non-empty slice is walked. Map values are walked, map keys never.
A type that implements `json.Marshaler` or `encoding.TextMarshaler`, or whose
pointer does, is skipped (`time.Time`, `json.RawMessage`). `[]byte` is
skipped. Embedded fields are walked but not themselves checked. `json:"-"` is
allowed. Records are collected under a mutex (tests run in parallel), printed
sorted after `m.Run()`.

### What the guard does not see
A route no test answers through `writeJSON` with a body is not seen. The
probe sees bodies, not routes, so it cannot list unseen routes itself; the
bounded test (`TestEveryListIsBoundedOrSaysWhyNot`) seeds and walks every
paged and signal-only list, so every list's items are produced. One-object
routes are seen where a handler test reads them, which is most; nobody
enumerated which are not. A follow-up could record the route pattern beside
each body (the probe would need the request) and fail on a `200` route never
seen — worth doing when 0046 makes the surface a promise. Host answers
(written by the host's own handlers, M26.7) are not walked; the test host
fixtures needed no tags.

### Tests
`answer_views_test.go`: the join with a found decision and an aged-out one;
a session's loss with two causes (two reports of one summed) and none (`[]`);
a proxy that reported beside one that never did (no `0001-01-01` anywhere);
default liveness `90/60/86400/21600`. `views_internal_test.go`: sub-second
liveness rounded up; a labelless target `{}` and a not-found decision with no
rule, route or instant. Decoders moved: `decodeBlocked` (`audit_list_test.go`,
now `blockedItem`), the fleet `whose` (`bounded_test.go`), the mapping
rejections (`mapping_scope_test.go`).

### Cross-repo
Downstream sync owed to `hoplock/enterprise` (its supervisory plane and
console read the fleet view): queued under `prompts/downstream/queued/` once
the PR is open (see the PR's `## Cross-repo impact`). `hoplock/proxy`: none.
