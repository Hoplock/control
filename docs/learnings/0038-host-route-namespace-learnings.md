# 0038 — host routes and the console in namespaces of their own — Learnings

## Summary
- **M27** (new; amends M15, M20, M24): four prefixes, no path in two. Control's API `/api/v1/` outside `/api/v1/hosts/`; a host's API `/api/v1/hosts/<provider>/`, `tenants/{tenant}/<pattern>` and `cross-tenant/<pattern>` (`/api/v1/cross-tenant/` withdrawn, folded in); Control's console `/console/`; a host's `/host-console/<provider>/`. Catalogue keys: Control's carry no `:`, a host's are `<provider>:<key>`, one equal to Control's refused at mount.
- **Shapes at `v0.24.0`, each × 69 tenant routes, request level:** refused — exact, method beside, literal under a wildcard, mux conflict, `.`, absolute, climb. Accepted, harmless — case variant, 59 of 69 wildcards over literals. Accepted, answering Control's *unserved* paths instead of its `404` — `{x...}` (tree, family, below), subtree (family, below), `{$}`, `rel/{$}`, 10 wildcards over literals, 5 `//` (→ `307`). **None took a served path or a `405`**: no `v0.11.0` defect. After the move: every in-namespace shape accepted and harmless; climb, absolute, `.`, `//` refused.
- **A host binary must:** call `/api/v1/hosts/<provider>/…`; name itself `<owner>/<name>` (`hoplock/enterprise`, never a module path); drop `.` and empty mid-segments from patterns; put screens under `/host-console/<provider>/` with `<provider>:` keys. It may drop any "stay off Control's families" rule.
- **Files:** `internal/httpapi/north/{namespace,console,host,router}.go`; tests `namespace_test.go` (request level, `export_test.go`'s `ServeTable`), `namespace_internal_test.go`. **Types:** `north.Console`, `Options.Consoles`, `Server.Consoles()`, `HostAPIRoot`/`HostTenantPrefix`/`HostCrossTenantPrefix`/`HostConsoleRoot`; `HostPrefix`, `CrossTenantPrefix` gone; `server` docs only. No migration.
- **Release `v0.25.0`**, MINOR, behaviour-only `### Breaking`. Sync for `hoplock/enterprise` queued in `prompts/downstream/queued/` (see the PR); proxy: none.
- **NEXT (0043, 0044):** a Control route that can match `/api/v1/hosts/…` is refused at registration. 0044 mounts through `Options.Consoles`; its prompt names the tests to keep green. No `server.Options` field hands over a host's screens yet.

## Details

### What was verified, and how

`TestEveryWayAHostCanOverlapControlIsRefusedOrAnswersNothingOfControls`
(`namespace_test.go`) builds every overlap shape from Control's real table
(`north.Table`, never a hand list) and, for each shape `CheckHost` accepts,
mounts it on `north.ServeTable` — the table `Table` lists, served with no
services behind it: Control's routes answer who they are, a host's route its
own handler, and a path nobody serves the real not-found handler. It then sends
the requests "beside" Control's route — an instance of its path under its own
method and under one it does not serve, the instance with a trailing slash and
with an unserved segment, the tenant root and an unserved path under it, and an
unserved path under `/api/v1/` — and requires each answer to equal the answer
with no host mounted. Comparing with the bare table, rather than expecting
"404", is what lets a probe that one of Control's wildcards does serve stay a
probe.

Run against `main` at `v0.24.0` (`host.go`, `router.go` and `server/route.go`
unchanged since `v0.10.0`, as the prompt's probe found), counts per shape over
the 69 tenant routes (77 for the two escapes):

| Shape | Refused | Accepted, no effect | Accepted, answers Control's unserved paths |
| --- | --- | --- | --- |
| Control's exact pattern | 69 | | |
| a method beside it | 69 | | |
| a literal under its wildcards | 29 | | |
| a mux conflict | 29 | | |
| a pattern the mux would clean (`.`) | 69 | | |
| an absolute pattern / a climb out | 77 / 77 | | |
| a case variant | | 69 | |
| a wildcard over its literals | | 59 | 10 |
| `{x...}` over the whole tree | | | 69 |
| `{x...}` inside its family | 16 | | 53 |
| `{x...}` below it | 14 | | 55 |
| a trailing-slash subtree over its family | | | 69 |
| a trailing-slash subtree below it | 5 | | 64 |
| the tenant root `{$}` | | | 69 |
| its path with a trailing slash only (`rel/{$}`) | | | 69 |
| a pattern the mux would clean (`//`) | 62 | 2 | 5 |

Every one of the 876 changed answers was a `404` Control would have given: a
host catch-all, subtree or `{$}` answered it instead (`418` from the test
host), and five `//` patterns (`ca//`, `role-bindings//`) turned it into a
`307` to a trailing-slash path. **None was a path Control serves or a `405` it
answers.** So there was no `v0.11.0` defect in the prompt's sense to fix under
a failing test; the namespaces close the gap the table shows, and the same test
— unchanged — passes after the move with every in-namespace shape accepted.

Two refusals at `v0.24.0` were accidents of `servedBy`: a `.` pattern was
refused because `mux.Handler` reports the pattern the *cleaned* path would
reach, and most `//` patterns for the same reason. Both are refused on purpose
now (`unclean`, in `checkHostPattern`), since the mux redirects a request for
such a path and the route could never be served.

### The claims, ruled

- **`CHANGELOG.md` `## v0.10.0`**, "a host route on a path Control serves
  through a wildcard is refused at start-up" — **true**. Every served path and
  every `405` stayed Control's. **Incomplete**: it implied a host was kept apart
  from Control's paths, and a catch-all, subtree, `{$}` or wildcard answered
  the ones Control had not served yet. `v0.25.0`'s `### Breaking` says what it
  left out.
- **`server.Route`'s doc comment** (start-up refusals) — **true** item by item
  at `v0.24.0`; silent on future paths and on `.`. Rewritten: a host now names
  no path of Control's at all, and the list is what a host can still get wrong.
- **PLAN §3**, "takes no path Control serves (its pattern, a method beside it,
  or a literal under one of Control's wildcards …)" — **true**, same
  incompleteness. Rewritten in place.

### Mutations

Each applied, `go test ./internal/httpapi/north/ ./server/` run, reverted.

**At `v0.24.0`** (with the verify test added; it fails there on the unserved
paths alone, so "served-path probes" counts the extra damage):

- `servedBy` returns `""` → `TestAHostCanTakeNoPathAndNoCodeControlServes`,
  `TestAHostRouteControlWillNotMountIsRefusedAtStartUp`,
  `TestAHostThatCannotStartIsRefusedAtOnce`, and the verify test on 99 served
  paths or `405`s.
- the mux-conflict check swallowed (`handle` recovers and returns nil) →
  `TestAHostRouteControlWillNotMountIsRefusedAtStartUp`, and the verify test
  on 1.
- the method rule dropped (`served == pattern` let through) →
  `TestAHostCanTakeNoPath…`, `TestAHostRouteControlWillNotMount…`,
  `TestAHostThatCannotStart…`, and the verify test on 70 (`405` → host).

No guard went unnoticed.

**After the move:**

Every in-namespace shape is now accepted and answers nothing of Control's (69
of 69, or 29 of 29 for the shapes that need a wildcard); the absolute pattern
and the climb (77 of 77), `.` and `//` (69 of 69) are refused.

- `servedBy` returns `""` → `TestServedByStillRefusesAHostPathControlServes`
  (it builds Control's paths inside the host namespace by hand, since the
  router no longer lets one there; without it this guard would be unseen).
- the mux-conflict check swallowed → `TestAHostRouteControlWillNotMountIsRefusedAtStartUp`,
  `TestAHostThatCannotStartIsRefusedAtOnce` (both "a mux conflict between
  two").
- the method rule dropped → `TestServedByStillRefusesAHostPathControlServes`.
- the router letting Control's routes into `/api/v1/hosts/` →
  `TestARouteThatIsNotFullyClassifiedIsRefusedAtRegistration`.
- a route of Control's added under `/api/v1/hosts/…` → every test that builds
  a server (registration refuses it); with the router's guard also off →
  `TestEveryPathIsInsideItsOwnNamespaceAndNoOther`.
- host routes mounted at the old `/api/v1/tenants/{tenant}/` prefix, router
  guard off → `TestEveryPathIsInsideItsOwnNamespaceAndNoOther`,
  `TestEveryWayAHostCanOverlap…`, `TestTheRouteListingNamesWhoseEveryRouteIs`,
  and every host request test.
- the router letting a host out of its namespace →
  `TestARouteThatIsNotFullyClassifiedIsRefusedAtRegistration`.
- the provider shape unchecked → `TestAHostRouteControlWillNotMount…`,
  `TestAHostThatCannotStart…`, `TestARouteThatIsNotFullyClassified…`.
- `.` and empty segments accepted → `TestAHostRouteControlWillNotMount…`,
  `TestAHostThatCannotStart…`.
- the console mount's prefix check, the catalogue's equality check and its
  `<provider>:` check, each removed → `TestAConsoleOutsideItsPrefixIsRefused`
  (the equality check is seen because the test asserts the refusal names the
  key as Control's; the prefix rule alone would refuse it too).
- `/` redirecting with no console, or never → `TestEachNamespaceAnswersOnlyInsideItsOwnPrefix`.

No guard went unnoticed.

### The decision's choices, and what was weighed

- **Tree vs reserved segment.** `/api/v1/tenants/{tenant}/x/<provider>/…` kept
  tenant resolution where it is, but an ingress or WAF rule matching "a host's
  routes" would need a wildcard tenant segment, and the listing would read
  tenant first. `/api/v1/hosts/<provider>/tenants/{tenant}/…` keeps `{tenant}`
  a path wildcard the middleware reads exactly as before (`r.PathValue`), so
  resolution, the scope check, RBAC and the access log did not change a line.
- **The provider in the path.** One binary carries one host today, but a
  provider-less prefix would make "two hosts never collide" a promise about
  how many hosts there are. With it, the listing, a log line and a WAF rule all
  name the host. Its shape is fixed at two segments because a provider of any
  depth could sit inside another's root (`a` and `a/tenants/x`).
- **Cross-tenant folded in**, not kept: M24.5 makes it host-only, and a
  top-level prefix would be a host path outside the host namespace. The class
  is still visible in the path, which is what M24.2 wanted.
- **Consoles as siblings.** Nesting a host's screens under `/console/hosts/`
  would put them under Control's history fallback unless something reserved the
  hole; siblings make each fallback a subtree of its own prefix with nothing to
  reserve. The API nests under `/api/v1/` because nothing of Control's has a
  catch-all there and the API version (M19) is one segment for the surface.
- **`/` redirects only once Control's console is mounted.** Before 0044 it
  would redirect to a `404`; now it stays Control's `404`, and 0044 gets the
  redirect by mounting.

### How the namespaces are held

- `Router.Register` → `checkNamespace`: Control's route must be `inside`
  `/api/v1/` and `overlaps` nothing under `/api/v1/hosts/` (so `/api/v1/{x}/…`
  and `/api/v1/` are refused, not only literal `hosts/…`); a host's must be
  under its provider's class prefix. `inside` and `overlaps` read ServeMux
  pattern segments: a wildcard, a subtree (trailing `/`) or a `{x...}` matches
  outside a literal prefix; `{$}` does not.
- `mountConsoles`: Control's first (so a host's catalogue is judged against
  it), each pattern clean, method-less and `inside` its prefix; one Control
  console; a host console only for the server's own host provider.
- `TestEveryPathIsInsideItsOwnNamespaceAndNoOther` walks each side's own table
  (`Routes()`, `Consoles()`) with stub consoles; its walk is
  `namespaceProblems`, and `TestTheNamespaceWalkFailsOnEveryPathThatStrays`
  feeds it fifteen strays, one per way, so the walk is shown failing without a
  mutation.

### Tests changed and why

- `TestAHostCanTakeNoPathAndNoCodeControlServes` now asserts the opposite of
  what it did for patterns: a host writing Control's pattern or a literal
  under its wildcard is accepted and mounted at its own path, never at
  Control's. Codes are refused as before.
- `TestAHostRouteControlWillNotMountIsRefusedAtStartUp` lost "a duplicate
  Control route", "a method on Control's path" and "a mux conflict with
  Control" (now the host's own paths) and gained the namespace escapes, `.`,
  `//`, and nine bad provider shapes.
- `TestAHostThatCannotStartIsRefusedAtOnce` (server) likewise, and asserts a
  pattern refusal names the route.
- Router unit cases that registered `"/x"` now use `/api/v1/x`, so each is
  refused for the reason it names and not for being outside the API.

### Follow-ups

- **A host's screens have no `server.Options` field.** `north.Options.Consoles`
  takes them, but nothing public hands them over. The phase that serves
  Enterprise's screens (0044 says so in its learnings) adds a field shaped like
  `north.Console` — patterns, catalogue keys, handler — which is an additive
  `server` change.
- The verify test's request-level harness (`ServeTable`) needs no database;
  a later phase that adds a route family can rerun it to see what a host
  shape would do.
