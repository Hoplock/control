# 0018 — A cross-tenant access class: one route class that spans tenants, for a host's reads

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/19, under `## Upstream request`,
> and answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2). The
> request file is Enterprise's
> `prompts/upstream/queued/enterprise-PR#19-cross-tenant-access-class.md`.
>
> **Where it sits.** Fifth, after tagged releases (0014), the public server
> package (0015), self-service grant requests (0016) and ending an external
> window (0017), and before the north-bound API (0019). It depends only on
> merged phases: 0011 (the credential model, the route table and its one
> middleware) and 0015 (host routes, `server.Caller`). It runs after 0016 because
> 0016 adds `AccessSelf` to the same closed `Access` enum and the same
> middleware, and after 0017 because Enterprise needs both of those sooner (its
> 0003 and 0008 run before its 0013). It runs before 0019 because 0019 needs
> nothing from it, and this phase revises 0019's rule that cross-tenant reads do
> not exist on this surface. Enterprise's cross-tenant reporting (its 0013)
> builds around the gap until this lands. PLAN §10's cross-tenant renumbering
> note says the same.

## Read first
- `docs/PROTOCOL.md` — session workflow. Read §3 for `server/` and `ext/` as a
  compatibility promise, for revising the plan in place, and for adding a
  decision with its register row.
- `docs/CROSS-REPO-PROTOCOL.md` — **§4.1**, **§4.3** (where the sync is
  queued), and **§5** at "The PR that answers an upstream request is not a
  sync". This phase changes `server/`, a public package Enterprise imports, and
  it answers a request, so it owes a downstream sync (below).
- `docs/PLAN.md`:
  - **§2**:
    - **M18**, in full. This phase amends it: today every north-bound request
      resolves exactly one tenant. Read its last paragraph twice: "The mechanism
      is here because the queries are here, and a seam that hands another
      module the job of filtering correctly is a seam that will eventually be
      used incorrectly." That sentence is why the set is resolved by the
      middleware here, never by a host's handler.
    - **M15**: what a host gets, and the line between this repository and
      Enterprise ("governance and scale, not capability").
    - **M2**: one north-bound listener, one middleware chain, one credential
      model.
    - **M11**: a refusal made on purpose is a 4xx with a code, and a `401` is
      the middleware's alone.
    - **M8**: the audit chain is per tenant. It is why this class reads and
      never writes.
    - **M21**: every error carries a stable code. This phase adds none.
    - **M23**: `server/` is a compatibility promise, and what "incompatible"
      means.
    - **M13**: closed sets. `Access` is one.
  - **§3**, "Component responsibilities": the `internal/httpapi/north`,
    `internal/identity` and `server` bullets, and the host-routes paragraph.
  - **§6**, "Users, groups, roles and RBAC (M7, M18)": the fixed role set, "RBAC
    is enforced in one place", and "A host names a permission; Control defines
    it (M15)".
  - **§10**: this phase's row, and 0015's and 0016's.
  - **§11**: the "Multi-tenant **governance**" bullet.
- `docs/learnings/` — read the summaries.
  - Open **`0015`** in full, especially "What a host does not get
    (deliberately)" and its summary's last bullet, which named this request
    before it was raised.
  - Open **`0011`** for the principal, its unexported scope map and the route
    table, and **`0016`** (if it has merged) for `AccessSelf`, the most recent
    class added to the same enum.
- Code. Read it before designing anything:
  - `server/{route,caller,server,errors}.go` and `server/server_test.go`;
  - `internal/httpapi/north/{router,routes,middleware,host,server}.go`, and in
    `north_test.go` the isolation tests named under "Acceptance criteria";
  - `internal/identity/{principal,rbac}.go`;
  - `internal/daemon/identityctl.go`, `parseScopes`: today the only way to
    mint a principal scoped to several tenants;
  - `internal/identity/federation.go`, `mintSession`: a federated session is
    scoped to the one tenant its connector belongs to.
- The request itself, for the requester's own words. It cites Enterprise
  **E11** (cross-tenant reporting for an instance's operator is Enterprise's)
  and **E2** (Enterprise serves its API from Control's listener, behind
  Control's sign-in, never a second one). Read them in `hoplock/enterprise`'s
  `docs/PLAN.md` if a choice here turns on one. They are cited by id here and
  never restated.

## Objective
Give a host binary **one** north-bound route class that spans tenants, so that
the aggregate E11 assigns to Enterprise — one report of privileged access
across every division a Control instance hosts — has somewhere to be served.

Keep the property M18 exists for. The set of tenants a request covers is
resolved by Control's middleware from the caller's own scope and handed to the
handler, which never chooses it, never widens it and never filters it. That is
the difference between this class and the approximation Enterprise's 0013
forbids: a tenant route whose handler reads tenants other than the one Control
resolved.

Control alone is unchanged. It serves no cross-tenant route of its own: the
aggregation is Enterprise's (M18, §11, its E11). Every route Control serves
today, and every host route written before this phase, behaves exactly as it
did.

## The request, in this repository's vocabulary

**The need.** E11 makes cross-tenant reporting for an instance's operator
Enterprise's work (its 0013). But every host route is `AccessTenant` and
resolves exactly one tenant. Control serves no cross-tenant route of its own,
by design: 0019 says "Aggregating them is a governance feature and it is
Enterprise's (its E11)". So the aggregate has nowhere to be served.

**The shape asked for**, quoted from the request. The requester marked it a
proposal, with the names and mechanics Control's to decide under M18:

```go
// server/route.go
type Access int

const (
	// AccessTenant is today's only class, and stays the zero value: the route
	// is at /api/v1/tenants/{tenant}/<Pattern> and resolves exactly one tenant.
	AccessTenant Access = iota
	// AccessTenants mounts the route outside any one tenant. The middleware
	// authenticates the caller and resolves every tenant in which the caller
	// holds Permission, and never more, before Handler runs; a caller holding
	// it in none is refused 403. A selector on the request may narrow that
	// set, never widen it.
	AccessTenants
)

type Route struct {
	Method, Pattern, Permission, Summary string
	Access                               Access // new; the zero value is today's behaviour
	Handler                              http.Handler
}

// server/caller.go
type Caller struct {
	Tenant        ext.Tenant   // zero on an AccessTenants route
	Tenants       []ext.Tenant // new: the resolved set on an AccessTenants route, sorted; nil otherwise
	Subject       ext.Subject
	Principal     string
	BreakGlass    bool
	CorrelationID string
}
```

It left three questions for Control:

1. where such a route is mounted, and how it stays off Control's paths;
2. which permissions may be used across tenants ("reports read under
   `audit:read`; perhaps reads only");
3. whether `Caller` grows a field or `server` adds an accessor, whichever keeps
   the M23 promise cleanest.

And it argued the fit with M18: no role spans tenants, so an instance operator
is a principal holding the permission in each tenant, granted tenant by tenant;
the filter stays in the one enforcement point; and the isolation test class
extends to the new class.

**Until it exists.** An instance operator cannot get a cross-tenant report
from a running Enterprise server, and Enterprise's console cannot show one.
They can fetch each tenant's report and combine them by hand. That is the
aggregation E11 assigns to Enterprise, done by the customer instead.
Enterprise's 0013 builds the report behind a seam beside its route table, never
in `Options.Routes`, tests its handler directly, and says that no instance
operator can reach it.

## What this phase answers, and where it differs from the ask
The request fits the plan, and its mechanism is met as asked: the middleware
resolves the set and the handler is handed it. 0015 decided that a class like
this needs a decision of its own, "about who the instance operator is and how
such a principal is scoped", made on its own terms and not on the way to a
plugin seam. So this phase records **M24**, and answers the three questions
there. Four things differ from the sketch, each for a reason the requester
could not see.

- **The class is `AccessCrossTenant`, not `AccessTenants`.**
  - *Why.* `AccessTenant` and `AccessTenants` differ by one letter, are the same
    type, and both compile on any route. The line where one is typed for the
    other is the line the isolation property lives on, and a reviewer reading
    a diff will not see the `s`. `Caller.Tenant` and `Caller.Tenants` keep their
    names, because there the types differ and the compiler catches a slip.
  - Its `String()` is `"cross-tenant"`, which is what the route listing and the
    tests print.
- **Question 1: it is mounted at `/api/v1/cross-tenant/<Pattern>`, and that
  prefix holds this class and nothing else.**
  - It sits beside `/api/v1/tenants/` and `/api/v1/session`. An operator
    reading a log line can tell from the path alone which class answered, the
    same reason this surface is `/api/` and the contract's is `/v1/`.
  - A pattern there may not carry `{tenant}`: the set is not a path segment.
  - Registration refuses an `AccessCrossTenant` route outside the prefix, and
    any other class inside it. That is how it stays off Control's paths in
    both directions, structurally rather than by convention.
- **Question 2: reads only, and "a read" is a permission the auditor role
  holds.**
  - *Why reads.* A write across tenants is one request making N changes in N
    tenants, with N audit records in N independent chains (M8, M18's third
    consequence). Nothing makes them atomic, and a partial outcome is neither
    a success nor a refusal: M11 has no answer for it. E11 asked for
    reporting, and reporting reads.
  - *Why the auditor's set, and not a list of its own.* `auditor` is defined as
    "can read everything and change nothing", and
    `TestTheAuditorChangesNothing` already holds it to that. A second list
    would be a second definition of "read" that could drift from the first.
  - *Why every read, and not only `audit:read`.* The class grants nothing a
    caller could not already get with N requests, because the caller must hold
    the permission in every tenant the set contains. What it adds is the
    aggregate, and the danger it brings is a missing filter, which the
    middleware removes whichever read it is. Narrowing to `audit:read` would
    send Enterprise back upstream for every new report (entitlement state
    across tenants is `license:read`), for no safety it buys.
  - A route naming any other permission is refused at start-up.
- **Question 3: a field on `Caller`, as asked.**
  - Adding a field to a struct is compatible under Go's rules, which M23
    applies. `Caller` is already not comparable, because `ext.Subject` holds a
    slice, so a slice field takes nothing away. `make release-check` confirms
    this; if it reports a break, stop and say so.
  - An accessor would be a second call for one projection, and a handler would
    have two places to look for who is calling.
  - `Tenant` is zero on a cross-tenant route, and `Tenants` is nil on a tenant
    route, so a handler mounted on the wrong class gets an empty value rather
    than a plausible one.
- **A named tenant that fails is refused, never dropped.** The ask says a
  selector "may narrow that set, never widen it", but not what happens when it
  names a tenant the caller cannot read. Dropping it silently would answer a
  report that covers fewer tenants than were asked for, and look complete.
  So a named tenant outside the caller's scope is `403 tenant_out_of_scope`,
  and one inside it without the permission is `403 forbidden`, exactly as on a
  tenant route. With no selector, the set is every tenant where the permission
  is held. Tenants in scope without it are not in the set, and the set the
  handler is given is the one it reports.
- **Who the instance operator is today: a token minted on the box, not a
  person signed in.** The requester could not see this.
  - A federated session is scoped to the one tenant whose connector it signed
    in through (`mintSession`). The only principal scoped to several tenants is
    an API token an operator mints on the box,
    `hoplock-control identity token-issue --scope 'a=auditor;b=auditor'`
    (`parseScopes`), which is delegated administration and deliberately takes
    an operator rather than an API call.
  - So a cross-tenant route reached by a signed-in person covers that person's
    one tenant, and covers several only for such a token.
  - The class is still the right shape, and this phase does not change how a
    session is scoped. A session spanning tenants needs one identity linked
    across tenants that may federate from different IdPs. That is delegated
    administration, which is E11's governance on M18's mechanism, and its own
    request if Enterprise needs it. Say this plainly in the learnings, in the
    PR and in the downstream obligations, so Enterprise's console does not
    promise a person an aggregate that a session cannot reach.

## In scope

### 1. The decision (`docs/PLAN.md` §2)
Add **M24**, with its register row, and say in its first line that it amends
M18. A sketch; write it in the plan's own voice:

> **M24 — A cross-tenant read is a route class the middleware resolves, and only
> a host serves one (amends M18, new).**
> - The instance operator is not a role or a principal kind, and no role spans
>   tenants: it is a principal holding a read in each tenant a report covers,
>   granted tenant by tenant. Today that is a token minted on the box; a
>   federated session is scoped to one tenant.
> - One class, `AccessCrossTenant`, at `/api/v1/cross-tenant/`. The middleware
>   resolves the set — every tenant in the caller's scope where it holds the
>   route's permission, narrowed by a selector and never widened — and hands it
>   to the handler, which never chooses it. A named tenant that fails is
>   refused, never dropped.
> - Reads only: a permission the auditor role holds. A write across tenants is
>   N records in N chains (M8) with no atomicity and an outcome M11 cannot
>   name.
> - Only a host serves one (M15, E11). Control registers none of its own, and
>   registration refuses one.
> - The isolation test class extends to it.

- Register: add the M24 row (rendered in §3, §6, §10, §11), and set M18's
  status to `amended by M24`.
- M18: say in its first line that M24 amends it. Revise its north-bound
  bullet and its last paragraph in place: a request resolves one tenant, or, on
  a cross-tenant read, a set resolved by the same rule. The mechanism for
  cross-tenant reporting is now here (the class); the reporting itself stays
  Enterprise's (E11).
- M15: a host's routes are tenant routes or cross-tenant reads (M24).
- If you find yourself settling something M24 does not, widen M24 rather than
  adding a second decision, and say so in the PR.

### 2. The class (`internal/httpapi/north/router.go`, `middleware.go`)
- Add `AccessCrossTenant` to the closed `Access` enum, after every existing
  class (after `AccessSelf` if 0016 has merged). `String()` is
  `"cross-tenant"`.
- `const CrossTenantPrefix = APIPrefix + "/cross-tenant/"`.
- `Router.Register` refuses, naming the route:
  - an `AccessCrossTenant` route with no permission, or with one the auditor
    role does not hold (`identity.RoleAuditor.Can`);
  - an `AccessCrossTenant` route with an empty `Provider`: Control serves no
    cross-tenant route of its own (M24);
  - an `AccessCrossTenant` route whose pattern is not under
    `CrossTenantPrefix`, or names `{tenant}`;
  - a route of any other class whose pattern is under `CrossTenantPrefix`.
- `enforce`, for `AccessCrossTenant`, after authentication exactly as today:
  1. The **selector** is every non-empty `tenant` query value plus the
     `X-Hoplock-Tenant` header's value, trimmed and de-duplicated. A path never
     carries one.
  2. **With a selector**, check each named tenant in sorted order, and answer
     the first failure. Outside the principal's scope: `403
     tenant_out_of_scope` with `{"tenant"}`. In scope without the permission:
     `403 forbidden` with `{"tenant", "permission"}`. Log each with
     `logRefusal`, as today. The set is the selector, sorted.
  3. **With no selector**, the set is `principal.TenantsWith(route.Permission)`.
     Empty is `403 forbidden` with `{"permission"}`, and the handler never
     runs.
  4. Put the set on the context. `TenantsFrom(ctx) ([]store.Tenant, bool)` is
     the only way to read it, and returns a copy. `TenantFrom` stays false on
     this class, as it is on every class that resolves no single tenant.
  5. The access log line carries `tenants`, the sorted set, and no `tenant`.
     `requestFacts` gains the field.
- No new error code. The three refusals are this surface's existing ones,
  because they are the same three operator problems.

### 3. The principal (`internal/identity/principal.go`)
- Add:

  ```go
  // TenantsWith returns the tenants in this principal's scope in which it holds
  // perm, sorted. It is the one question about the scope map whose answer is a
  // set, and the north-bound middleware is its only caller (M24): a handler is
  // handed the set and never asks.
  func (p *Principal) TenantsWith(perm Permission) []store.Tenant
  ```

  A nil principal answers nil.
- Revise the comment on `scopes`. It says a handler cannot iterate the map,
  "which is what stops a cross-tenant aggregate route from being easy to
  write". That stays true of a handler, and the one route class that may
  aggregate now has its set resolved here and checked by a source test (below).
- `ScopedTenants` keeps its comment: nothing decides access with it.

### 4. Host routes (`internal/httpapi/north/host.go`)
- `HostRoute` gains `Access Access`. A host route is `AccessTenant` or
  `AccessCrossTenant`. Any other class is refused at start-up, naming the
  route: a host gets no anonymous, authenticated-only or self-scoped route
  (0015).
- `hostRoute` prefixes the pattern for its class: `HostPrefix` or
  `CrossTenantPrefix`. `checkHostPattern` applies to both. The collision check
  against Control's patterns applies to both.
- `HostCaller` gains `Tenants []ext.Tenant`. `withHostRequest` fills it from
  `TenantsFrom` on a cross-tenant route and leaves `Tenant` empty there.
  `HostCallerFrom` clones it, as it clones `Subject.Groups`.
- Revise the block comment "Three things a host does NOT get": the first is now
  a cross-tenant **write**, and a cross-tenant read is the class M24 defines.

### 5. The public surface (`server/`)
Exactly this, additively. Every identifier written before this phase keeps its
meaning:

```go
// server/route.go

// Access is a host route's class: what Control resolves before Handler runs.
// Closed set; the zero value is AccessTenant, so a Route written before Access
// existed is unchanged.
type Access int

const (
	// AccessTenant mounts the route at /api/v1/tenants/{tenant}/<Pattern>.
	// Control resolves exactly one tenant from the caller's scope and checks
	// Permission in it. Caller.Tenant is that tenant.
	AccessTenant Access = iota
	// AccessCrossTenant mounts the route at /api/v1/cross-tenant/<Pattern>.
	// Control resolves every tenant in the caller's scope where it holds
	// Permission, narrowed by the request's tenant selector and never widened,
	// and refuses a named tenant that fails rather than dropping it.
	// Caller.Tenants is that set. Permission must be a read: one the auditor
	// role holds. Control serves no route of this class of its own (M24).
	AccessCrossTenant
)
```

- `Route` gains `Access Access`, after `Permission`, documented as above, and
  its doc comment stops saying there is no cross-tenant host route.
- `Caller` gains, after `Tenant`:

  ```go
  // Tenants is the set Control resolved on an AccessCrossTenant route:
  // sorted, never empty, each one in the caller's scope with the route's
  // permission. It is nil on an AccessTenant route, where Tenant is the one.
  // Read the set from here and nowhere else, and say in the response which
  // tenants it covers: that is how a report says "these" rather than "some".
  Tenants []ext.Tenant
  ```

  and `Tenant`'s comment says it is zero on an `AccessCrossTenant` route.
  `CallerFrom` copies the slice.
- `Options.host()` maps each `Access` explicitly. An unknown value is refused at
  start-up, naming the route, before a file is read or a port bound, like every
  other refusal 0015 made.
- The package comment's item 3 says a host's routes are tenant routes or
  cross-tenant reads.
- `server/example_test.go`, and `ext/README.md`'s "Starting Control from a host"
  (its `Routes` row and the paragraph under the table), show one route of each
  class. `TestTheReadmeExampleIsTheCompiledOne` keeps them in step.

### 6. The documents
- `docs/PLAN.md`, **revised in place** (PROTOCOL §3), beyond §2 above:
  - §3: the `internal/httpapi/north` bullet and the host-routes paragraph
    ("each is `AccessTenant` under `/api/v1/tenants/{tenant}/`"); the
    `internal/identity` bullet ("the only questions a caller can ask"); and the
    `server` bullet, whose "does not get a cross-tenant route" becomes "a
    cross-tenant write".
  - §6's RBAC paragraph: the middleware resolves one tenant, or on a
    cross-tenant read the set (M24).
  - §11's "Multi-tenant **governance**" bullet: the mechanism now includes the
    cross-tenant read class; the reporting stays Enterprise's.
  - This phase's §10 row, to match what was delivered.
- `prompts/queued/0019-northbound-api-and-policy-lifecycle.md`, "Tenancy on the
  surface (M18)". Revise its opening sentence to say every route **of Control's
  own** resolves exactly one tenant. Revise its third bullet to: Control serves
  no cross-tenant route of its own; a caller with scope in three tenants makes
  three requests to Control's routes; aggregating them is Enterprise's (its
  E11), served as a host's cross-tenant read (M24), whose set the middleware
  resolves. Keep its reason ("the one place where a missing filter leaks
  everything at once"), which is why only the middleware resolves a set.
- Code comments that say every request or every host route resolves exactly
  one tenant: `middleware.go` (`TenantFrom`), `router.go`, `host.go`,
  `server/route.go`, `server/caller.go` (its "a handler that could ask would be
  a handler that could write a cross-tenant route"), `server/server.go`.
- `CHANGELOG.md`: this phase's release, the next MINOR (M23), naming phase 0018
  and `server.Access`, `server.AccessTenant`, `server.AccessCrossTenant`,
  `server.Route.Access` and `server.Caller.Tenants`. There is no
  `### Breaking`: every change is additive, and `make release-check` says so.

## Out of scope
- **A cross-tenant write**, including `report:write` (a cross-tenant report
  schedule). The reason is above. If Enterprise needs one, it is a request of
  its own, and it starts with what a partial outcome answers.
- **A route of Control's own in this class.** The console (0021), `policyctl`
  and every route 0019 adds stay per tenant. Aggregating is E11's.
- **A session spanning tenants, a role spanning tenants, or an "instance"
  role.** See "Who the instance operator is". `RoleSet` stays per tenant, and
  `token-issue --scope` stays the only way to mint a principal across tenants.
- **Granting a principal in many tenants at once.** That is delegated
  administration, and it is Enterprise's (its E11).
- **A self-scoped or authenticated-only host route.** Only `AccessTenant` and
  `AccessCrossTenant` are offered to a host.
- **Paging, fan-out or partial results across tenants.** The handler is handed
  the set and reads its own data per tenant. How it pages is the host's.
- **Across deployments.** One Control instance's tenants only. Aggregating many
  deployments is the supervisory plane (M19, 0020, its E14).
- **North-bound versioning.** 0020 versions this class with the rest of the
  surface (M19).
- **`contract/` (M1).** Nothing here touches the wire. If it seems to, that is
  an upstream request to `hoplock/proxy`, not part of this phase.

## Acceptance criteria
Each criterion is a test. The table-driven ones enumerate the router, so a
cross-tenant route added later is covered on the day it is mounted. The north
tests build every server with host routes (`testHostRoutes`): add one
`AccessCrossTenant` route naming `audit:read` there, and have each table test
assert it enumerated it, as 0015's do.

**The class, end to end through `server.Run`.**
- In `server/server_test.go`, a host mounts a cross-tenant route naming
  `audit:read` beside its tenant routes. Three tenants, `a`, `b`, `c`.
- A token scoped `a=auditor;b=auditor;c=integration` calls it with no selector.
  The handler sees `Tenants == [a b]` and `Tenant == ""`, and answers `200`.
- With `?tenant=b`, the handler sees `[b]`. With `?tenant=a&tenant=b`, or the
  header naming `a` plus `?tenant=b`, it sees `[a b]`.
- With `?tenant=c`, the answer is `403 forbidden` naming `c` and `audit:read`.
  With `?tenant=d`, which is out of scope, it is `403 tenant_out_of_scope`
  naming `d`. With `?tenant=a&tenant=c`, it is `403 forbidden` naming `c`. The
  handler runs in none of them.
- A token holding `audit:read` in no tenant gets `403 forbidden`, and the
  handler does not run.
- A federated session in `a` sees `[a]`. A single-tenant deployment reaches the
  route with no selector, and its handler sees its one tenant.
- The same host's tenant route still sees `Tenant` and `Tenants == nil`.

**Isolation, enumerated from the router.**
- New, table-driven over every `AccessCrossTenant` route: a principal holding
  the route's permission in `tenant-a` only, scoped to `tenant-b` with a role
  lacking it, and not scoped to `tenant-c`. With no selector, the handler sees
  exactly `[tenant-a]`. Naming `tenant-b` is `403 forbidden`, and naming
  `tenant-c` is `403 tenant_out_of_scope`, on every such route.
- `TestEveryRegisteredRouteRefusesATenantOutsideTheCallersScope` and
  `TestEveryRegisteredRouteRefusesAnUnauthenticatedCaller` cover the
  cross-tenant routes. A cross-tenant route names its tenant in the selector,
  since it has no path segment.
- `TestEveryRouteDeclaresAnAccessClassAndATenantRouteDeclaresAPermission` and
  `TestARouteThatIsNotFullyClassifiedIsRefusedAtRegistration` cover the new
  class, and every refusal in item 2 fires.

**What Control does not do.**
- No route in Control's own table (`Provider == ""`) is `AccessCrossTenant`,
  and registering one is refused.
- A host cross-tenant route naming `grant:write`, `report:write` or
  `access-context:push` is refused at start-up, naming the route, with both
  ports still free. So is an unknown `server.Access` value, a cross-tenant
  pattern naming `{tenant}`, and a host route of any class but these two.
- A source test: outside its own package's tests, `TenantsWith` has exactly
  one caller, in `internal/httpapi/north/middleware.go`. It is the same kind of
  check as the south-bound one that fails if a third function can build a
  `401`.

**Everything else.**
- `TestTheAccessLogNamesThePrincipalAndTheTenantItResolved` gains a
  cross-tenant case. The line names `tenants` as the sorted set, and no
  `tenant`.
- `TenantsWith`: sorted; omits a tenant in scope without the permission;
  omits one out of scope; nil principal answers nil.
- `make release-check` passes with the release this PR names, and lists no
  break.

## Cross-repo impact
This phase answers an upstream request **and** changes `server/`, which
Enterprise imports, so it owes a downstream sync. The PR that implements it
MUST carry a `## Cross-repo impact` section (`docs/CROSS-REPO-PROTOCOL.md` §4.1)
naming **every** consuming repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5, "The PR that answers an upstream request is
not a sync"). It knows what it asked for. It does not know what was built, and
four things differ from the ask. The session there that rewrites its prompts
will be a fresh one that knows nothing.

State at least these obligations:

1. **Its 0013 can mount the report.** Rewrite its "Cross-tenant reporting
   cannot be mounted" paragraph from what merged:
   - the report is a `server.Route` with `Access: server.AccessCrossTenant`
     under `audit:read`, served at `/api/v1/cross-tenant/<Pattern>`;
   - its handler reads `server.Caller.Tenants`, only, and its response names
     the tenants it covers;
   - a selector narrows the set and never widens it, and a named tenant the
     caller cannot read is refused, never dropped;
   - its seam beside the route table goes, and "no instance operator can reach
     it" stops being true;
   - the prohibition on approximating with a tenant route stays.
2. **Who can reach it.** A token minted on the box with a scope naming several
   tenants. A federated session is scoped to one tenant, so a signed-in person
   gets that tenant only. If E11 needs a person to hold scope across tenants
   through a sign-in, that is a request of its own to Control. Its 0016 console
   labels the cross-tenant report with what a session can reach, rather than
   promising an aggregate one cannot.
3. **Reads only.** A cross-tenant route names a permission Control's auditor
   role holds. A cross-tenant write, such as a report schedule spanning tenants
   under `report:write`, is not offered. Its PLAN §3's "What a host does not
   get" says "a cross-tenant write" where it says "a cross-tenant route".
4. **The class names.** `server.AccessCrossTenant`, not `AccessTenants`, and
   why: a one-letter difference on the line the isolation property lives on.
5. **Its pin.** Every obligation above holds from the release this PR cuts
   (M23), never from `main`. Name that release by version, as this PR's
   `CHANGELOG.md` section does, and say that Enterprise's pin moves to it.

Queue the **"Downstream sync" kickoff** from `docs/KICKOFF.md`, verbatim except
for its blanks — this PR's URL, and the obligations above — as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`, committed
once the PR is open, and end the section by naming that file
(`docs/CROSS-REPO-PROTOCOL.md` §4.3).

### `hoplock/proxy`
Write **"None"** rather than leaving the repository out. The contract (M1) is
untouched, and the proxy consumes neither `server/` nor the north-bound
surface. "None" is a finding (`docs/CROSS-REPO-PROTOCOL.md` §4.1).

### Who runs the sync
Not you. This PR merges first, and the sync runs afterwards in its own session
(`docs/CROSS-REPO-PROTOCOL.md` §2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0018-cross-tenant-access-class-learnings.md`. Enterprise's sync
is written from its summary block, which MUST give:

- `server.Access`, its two constants, `Route.Access` and `Caller.Tenants`,
  verbatim;
- the prefix, the selector, and every answer the middleware gives on this
  class, with its code and parameters;
- the permission rule: the auditor's reads, and why not a list of its own;
- that Control serves none of its own, and how that is enforced;
- who holds a scope across tenants today, and that a federated session does
  not;
- M24 in one line, and that M18's register row now says `amended by M24`.
