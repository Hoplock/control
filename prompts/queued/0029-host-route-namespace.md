# 0029 — Host routes and the console in namespaces of their own: verify every refusal, then make a collision impossible

## Read first
- `docs/PROTOCOL.md`: §1, §3, §4, §5, and §6 for the register.
- `docs/CROSS-REPO-PROTOCOL.md`: §1, §4.1 and §4.3. Every path a host serves
  moves, and `hoplock/enterprise` builds its whole route table on those paths,
  so this phase owes it a sync.
- `docs/PLAN.md`:
  - §2: the register, then **M15** (a host extends Control and never forks
    it; its routes enter Control's one table), **M24** (a cross-tenant read is
    a route class only a host serves, at `/api/v1/cross-tenant/`), **M18**
    (the tenant is resolved from the caller, never chosen by a path a host
    controls), **M2** (two listeners), **M11** (a `401` is the middleware's
    alone), **M19** (0032 makes the north-bound API a compatibility promise,
    the reason this phase runs first), **M20** and **M21** (the console and its
    catalogue, which a host's screens join), and **M23** (what this phase's
    release promises);
  - §3: `internal/httpapi/north`, at "A host binary's routes (M15) enter
    **the same table**", and `server`;
  - §10: the host-namespace renumbering note, which says why this phase exists
    and why it sits here.
- `docs/learnings/`: read every summary. Read
  `0015-public-server-package-and-host-routes-learnings.md` whole, for every
  start-up refusal a host meets. Read
  `0023-audit-0019-north-bound-surface-and-record-learnings.md` for item 8 of
  its verdict table and its mutations under "Item 8". It is the last time
  these refusals were wrong. Read the `0018` summary for the cross-tenant
  class.
- Code, which is the authority over every document here:
  - `internal/httpapi/north/host.go`: `HostPrefix`, `checkHostPattern`,
    `controlPaths.servedBy` and `hostRoute`;
  - `internal/httpapi/north/router.go`: `CrossTenantPrefix`, the method-less
    mount, and the pattern-first dispatch that answers `404` and `405`;
  - `server/route.go` (`Route`'s doc comment names the paths a host may not
    take), `server/example_test.go`, and `ext/README.md`, "Starting Control
    from a host";
  - the tests that hold today's rules:
    - `TestAHostCanTakeNoPathAndNoCodeControlServes` and
      `TestAHostRouteControlWillNotMountIsRefusedAtStartUp`
      (`internal/httpapi/north/host_test.go`);
    - `TestControlServesNoCrossTenantRouteOfItsOwn` (`crosstenant_test.go`);
    - `TestAHostThatCannotStartIsRefusedAtOnce` (`server/server_test.go`).
- `ui/DESIGN.md`, for the URLs the console writes (its deep links and
  URL-serialisable filters), and `prompts/queued/0033-management-console.md`,
  which this phase rewrites to build into the console's namespace.
- In **`hoplock/enterprise`**, read only, `docs/PLAN.md` §3 at "One route
  table, mounted by Control" and "Start-up's refusal is the backstop, not the
  rule". They are what the sync you owe will change.

## Why this phase exists
A host's tenant routes are mounted at `/api/v1/tenants/{tenant}/<pattern>`,
in the same namespace as every route of Control's. The only thing keeping them
apart is a check at start-up. That check has already been wrong once:
`v0.6.0`–`v0.9.0` accepted a literal under one of Control's wildcards, such as
`targets/special` beside `targets/{target}`, and the mux then sent that path to
the host. 0023 fixed it in `v0.10.0`.

The user asked for this to be verified, and for a stronger property: **no
route a host declares may ever take over one of Control's API or console
routes, and the two should live in namespaces that cannot collide**, so that
the property holds by construction rather than by a check.

What the check does today was probed when this phase was queued. A scratch
test in `server/` called `server.Run` with one host route at a time, at
`v0.10.0` and on Control's `main` after `v0.11.0`. `host.go`, `router.go` and
`server/route.go` are the same at both.

**Refused at start-up:**
- a literal under one of Control's wildcards (`targets/special`,
  `audit/records/x`, `me/grant-requests/mine`, and ten more, all accepted at
  `v0.9.0`);
- Control's own pattern under another wildcard name (`targets/{t}`);
- a method beside one of Control's routes (`PATCH extensions`);
- a pattern the mux finds ambiguous (`{a}/special`, against
  `grants/{grant}`).

**Accepted:**
- a host catch-all over the whole tenant tree (`{x...}`), and a one-segment
  wildcard (`{x}`);
- a catch-all inside a family Control serves (`audit/{rest...}`,
  `targets/{t}/{rest...}`);
- a trailing-slash subtree (`targets/`, `targets/x/`), and the tenant root
  (`{$}`);
- `ca/{x}`, `identity/{x}` and `me/{x}`;
- any new literal beside Control's routes inside a family (`audit/archive`,
  `policy/changes`, `grants/{grant}/approve`);
- `{x...}` under the cross-tenant prefix.

None of those takes a path Control serves **today**, because the mux prefers
Control's more specific pattern. Each one claims paths in Control's own
namespace that Control has not served **yet**. When a later release serves
one, either of two things happens, and neither is acceptable:
- a host literal on that path refuses the host binary at start-up on its next
  pin bump;
- a host wildcard or subtree over it silently loses the path to Control. No
  start-up error is raised, and requests that reached the host now reach
  Control.

A catch-all may also answer what Control would have answered `404` or `405`
in its own envelope (M21). Verify that rather than assume it.

The console (0033) will add paths of its own to the north-bound listener, and
a host's screens will sit beside them. Today nothing says where either goes.
A single-page app's usual shape, served at `/` with a fallback that answers
any unknown path with its index, would be a catch-all over the whole
listener. The user asked for the console's paths to have a namespace of their
own as well.

This phase runs before instance identity (0032), which makes the north-bound
API a compatibility promise (M19). Moving every host path after the promise
would cost a version. It also runs before the console, so that both the
console and a host's screens are laid out in their own namespaces from the
first screen.

## Objective
1. **Verify.** Prove, against Control's real route table and at the request
   level, every way a host pattern can overlap a path Control serves. Prove
   each guard with a mutation.
2. **Decide.** Settle four namespaces on the north-bound listener, each under
   a prefix of its own: Control's API, a host's API (both classes), Control's
   console and a host's console. No path is in two of them. Record it as a
   decision.
3. **Move and reserve.** Mount every host route in its namespace, and reserve
   both console namespaces before the console exists. Hold every pair apart
   with tests in both directions, and keep start-up's refusal as defence in
   depth.

## In scope

### 1. Verify the refusals that exist
- **Enumerate, never hand-list.** Build the cases from Control's route table:
  `f.server.Routes()`, or what `TestAHostCanTakeNoPathAndNoCodeControlServes`
  walks. That covers the tenant routes, `/api/v1/session…`, `ca…` and
  `identity/claim-mapping` beside them, and the cross-tenant prefix. Then try
  each overlap shape against every route:
  - Control's exact pattern;
  - a method beside it;
  - a literal under its wildcards;
  - a host wildcard over its literals;
  - a multi-segment `{x...}`;
  - a trailing-slash subtree;
  - `{$}`;
  - a mux conflict;
  - a case variant;
  - a pattern the mux would clean (`//`, `.`).
- **At the request level as well as at start-up.** For every Control route,
  mount a host route of each accepted shape. Send a request to an instance of
  Control's path, and assert that Control's handler answered it. Then send a
  request to a path under Control's prefix that nobody serves, and record who
  answers it, with what status and envelope.
- **Check every claim.** Give each claim in `server.Route`'s doc comment,
  `CHANGELOG.md` `## v0.10.0` and PLAN §3 a verdict with evidence. Mutate each
  guard (`servedBy`, the mux-conflict check, the method rule) and record which
  test fails. A guard no test notices is a finding.
- **Fix under a test that failed first.** Anything that lets a host answer a
  request one of Control's routes serves today is a defect of `v0.11.0`. It is
  fixed in this phase, under a test that failed first, and the changelog says
  what `v0.10.0` stated wrongly.

### 2. Decide the namespace
These requirements bind the decision. It may choose the shape, never relax
them:
- **Disjoint by construction, in both directions.** No path Control serves,
  or will serve, is inside the host namespace, and no host route is outside
  it. One test walks Control's whole table, and the console's paths once they
  exist, and fails on any Control pattern inside the namespace. Another fails
  on any mounted host route outside it. A route Control adds later is covered
  the day it is added.
- **Both classes.** Tenant routes, and cross-tenant reads (M24).
  `/api/v1/cross-tenant/` is host-only today, held by
  `TestControlServesNoCrossTenantRouteOfItsOwn`. Keep it, fold it into the
  namespace, or move it, and say which and why.
- **Tenancy and access unchanged.** M18's resolution, the scope check, RBAC,
  the access log and the isolation tests apply exactly as they do now.
  `server.CallerFrom` gives a handler what it gives today. No path segment a
  host's caller writes ever selects a tenant.
- **One host does not collide with another.** Today one binary carries one
  host. Decide whether the namespace carries `Options.Provider`, so that two
  hosts' routes, and their listing entries, are told apart by path. Say why
  either way.
- **The console has namespaces of its own too.** See "The console's
  namespaces" below. It is part of this decision, not a later one.
- **Shapes to weigh.** Choose one; do not build several:
  - a reserved segment under the tenant prefix, such as
    `/api/v1/tenants/{tenant}/x/<provider>/<pattern>`. The middleware already
    resolves `{tenant}` there;
  - a tree of its own, such as `/api/v1/hosts/<provider>/tenants/{tenant}/<pattern>`
    beside `/api/v1/hosts/<provider>/cross-tenant/<pattern>`.

  Weigh tenant resolution, what an ingress rule or a WAF can match by prefix,
  what reads well in the route listing and in Enterprise's console, and what
  a caller of both surfaces has to know.
- **Record it.** Make it a new decision with its register row, using the next
  free `M` id (0028 is expected to take `M25`), rendered in §3. Amend M15's,
  M20's and M24's rows wherever their text changes.

### The console's namespaces
The console (0033) is not built yet, so this is the one moment its paths can
be placed without moving anything. The user asked that they have a namespace
of their own, like a host's routes. So the north-bound listener ends this
phase with **four namespaces, each under a prefix of its own, and no path in
two of them**:

| Namespace | Holds | Served by |
| --- | --- | --- |
| Control's API | every route of Control's, under `/api/v1/` outside the host's API namespace | Control |
| a host's API | every route a host declares, of both classes | the host |
| Control's console | the console's shell, its assets, its deep links and its catalogue | Control |
| a host's console | a host's screens, their assets, deep links and catalogue | the host, through Control's shell |

These requirements bind it:
- **Disjoint by construction, every pair, in both directions.** Name each
  prefix once, as a constant in `internal/httpapi/north`. Then test every pair:
  for every path each side mounts, from its own table and never a hand-kept
  list, assert that it is inside its own prefix and inside no other. A console
  path inside `/api/v1/` fails, and so does an API route inside a console
  prefix. Show each test failing once.
- **No catch-all outside its own prefix.** The console is never served at `/`
  with a fallback, so a single-page app's history fallback (its index for a
  deep link it does not know) answers only inside the console prefix. Outside
  every prefix, an unknown path is Control's `404` envelope. A path under
  `/api/v1/` is never answered with the console's index, and an API typo never
  reaches the console. The bare `/` may redirect to the console prefix, and
  nothing else may.
- **A host's screens are not inside Control's console, nor the reverse.**
  Control's shell may load a host's screens, but from the host's prefix. A
  host's assets cannot overwrite or shadow one of Control's, and a host's deep
  link cannot be a path Control's console routes.
- **Deep links stay in their namespace.** Every URL the console writes,
  including the URL-serialisable filters `ui/DESIGN.md` requires, is under its
  own prefix, and so is every URL a host's screen writes. A link one console
  hands to the other names the other's prefix.
- **Catalogue keys are namespaced the same way (M21).** A host's keys cannot
  collide with Control's catalogue, and a test proves it: a host key equal to
  one of Control's is refused when the host's catalogue is loaded.
- **Reserved before the console exists.** This phase builds the prefixes, a
  mount point that refuses anything outside its prefix, and the pairwise
  tests. A stub handler stands in for each console in the tests. 0033 then
  builds into a namespace the tests already hold, and fails them if it strays.
- **Bind 0033.** Rewrite `prompts/queued/0033-management-console.md` in this
  PR, so that it states the prefixes, where a host's screens load from, and the
  tests it must keep green. Change `ui/DESIGN.md` too wherever it says where a
  URL goes.

### 3. Move
- `internal/httpapi/north`: mount every host route in the namespace. `Route.Pattern`
  stays relative, now to the namespace, and `checkHostPattern`'s rules stand.
  Keep `servedBy` and the mux-conflict check: they cost nothing and are the
  defence in depth.
- `internal/httpapi/north`, again: the four prefixes as named constants, and
  the mount point each console will use. That mount point refuses, at
  start-up, a path outside its own prefix. The bare `/` redirects to Control's
  console prefix.
- `server/route.go`'s doc comment, `server/example_test.go` and `ext/README.md`,
  "Starting Control from a host", say where a host's routes are now. The
  example still compiles and is still held by its test.
- The route listing and the start-up log name each host route at its full
  path.
- `CHANGELOG.md`: a `### Breaking` that every host route moved, and where to.
  No identifier changes, so `make release-check` cannot see it. Say it under
  the section's behaviour that no API diff shows as well. Name the two console
  prefixes there too, as reserved. The phase is a MINOR
  release (M23).
- `docs/PLAN.md` §3 (the host paragraph and the `server` bullet) and the
  register.

## Out of scope
- Error codes and permissions. A host still declares none of Control's codes
  and names only Control's permissions, and start-up still refuses the rest
  (0015, 0023).
- Building the console (0033). This phase reserves its paths and rewrites its
  prompt, nothing more.
- The north-bound API's version and its negotiation (0032, M19).
- Enterprise's own routes. They move in the sync this phase queues.

## Cross-repo
- **`hoplock/enterprise` is owed a sync** once this merges (CROSS-REPO §4.1).
  Every path its routes are served at moves, and so does whatever its plan and
  prompts say about them:
  - its PLAN §3 route table and its rule that no pattern sits under one of
    Control's families, which this phase may make unnecessary — say so, or say
    why it stays;
  - its 0001's route table;
  - every prompt that writes `/api/v1/tenants/{tenant}/<Pattern>` or
    `/api/v1/cross-tenant/<Pattern>`;
  - its console's screens.

  Name it under `## Cross-repo impact` and queue the filled-in sync in
  `prompts/downstream/queued/`.
- **`hoplock/proxy`: none.** The contract does not change.

## Acceptance criteria
- The enumerating test runs against Control's real table and asserts every
  overlap shape above, refused or answered by Control. Each guard's mutation
  is recorded in the learnings with the test it fails.
- At the request level, with host routes of every shape in the namespace,
  every Control route answers its own requests. A path under Control's prefix
  that nobody serves gets Control's `404` envelope.
- Both disjointness tests exist, and each fails when a route of Control's is
  added inside the namespace or a host route is mounted outside it. Show each
  failing once.
- The four namespaces are held pairwise, with stub console handlers standing
  in for both consoles:
  - a path mounted outside its own prefix, or inside another's, fails;
  - an unknown path outside every prefix is Control's `404` envelope;
  - an unknown path under `/api/v1/` is never the console's index;
  - the console's history fallback answers only inside its own prefix;
  - a host catalogue key equal to one of Control's is refused.
- A host pattern that would leave the namespace is refused at start-up, naming
  the route, before any file is read or a port is bound.
- The host-route tests, the isolation tests and the cross-tenant tests pass at
  the new paths.
- `go build ./...`, `go vet ./...` and `go test ./...` pass. So do
  golangci-lint at CI's pin, `make contract-check`, `make license-check` and
  `make conform`. `make release-check` reports a MINOR with `### Breaking`.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move the prompt to `implemented/` and add
`docs/learnings/0029-host-route-namespace-learnings.md`. Its summary block MUST
give:
- the four prefixes — Control's API, a host's API (each class), Control's
  console and a host's console — and how the catalogue keys are split;
- the decision id;
- every overlap shape with its verdict;
- what a host binary must change, one line each;
- the sync queued for `hoplock/enterprise`.

The context budget: this phase reads `host.go`, `router.go`, the middleware's
tenant resolution, `server/`, their tests, `ui/DESIGN.md`'s navigation and
the console prompt. It changes about ten files. That fits under 60% if the
verify step (1) uses the existing harness rather than building a new one, and
if the consoles are reserved with stub handlers rather than built.
