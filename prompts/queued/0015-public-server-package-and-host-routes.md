# 0015 — The public server package: one start for both binaries, host config sections, host routes behind Control's middleware

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/11, under `## Upstream request`,
> and answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2). Item 7
> also carries a second Enterprise need, from
> https://github.com/Hoplock/enterprise/pull/8: a stale paragraph in that
> protocol's §1, which only the proxy can fix, rides this phase's request to it.
>
> **Where it sits.** Second, after tagged releases (0014): it is the first phase
> to cut its own release under 0014's rule, so Enterprise can pin the release
> that contains `server/`. It depends only on merged phases: 0004 (`ext/` and
> the registry), 0011 (the north-bound listener, its route table and RBAC) and
> 0013.
> Until it lands, Enterprise's binary cannot start at all, so every Enterprise
> phase ships code that no server runs. Self-service grant requests (0016) and
> the north-bound API (0018) both edit `cmd/hoplock-control` and the route
> table, and this phase moves the first and opens the second, so it runs before
> both and saves each of them a rebase.

## Read first
- `docs/PROTOCOL.md`. Read §3 for `ext/` as a compatibility promise, for
  revising the plan in place, and for the two hand-over obligations: this phase
  owes both.
- `docs/CROSS-REPO-PROTOCOL.md`:
  - **§1**: the shared-surfaces table. This phase adds a public package the
    table does not list, and the paragraph under the table is stale (item 7).
  - **§4.1** and **§4.2**, and **§4.3** for where both are queued.
  - **§5**, at "The PR that answers an upstream request is not a sync".
- `docs/PLAN.md`:
  - **§2**:
    - **M15**: Enterprise imports this module and starts it. This phase builds
      the "server package" M15 has promised since 0004, and revises M15 in
      place to name it.
    - **M2**: two surfaces. Host routes go on the north-bound listener and
      nowhere else.
    - **M18**: the tenant is resolved from the caller. A host route is tenant
      routed and never cross-tenant (see "What this phase answers").
    - **M11**: on this surface a `401` means unauthenticated, and only the
      middleware decides it.
    - **M21**: every error is a stable code, typed parameters, an English
      message and the correlation id.
    - **M13**: closed sets. Permissions, roles, access classes and error codes
      are closed sets.
    - **M7**: break-glass is asserted, never inferred.
    - **M19**: the north-bound surface becomes a compatibility promise (0019).
  - **§3**: the layout, and "Component responsibilities" for
    `internal/httpapi/north`, `internal/identity`, `internal/extdefault`.
    Also the paragraph under the layout that says `ext/` is "the only
    non-internal package this module has". That stops being true here.
  - **§6**: "Users, groups, roles and RBAC (M7, M18)".
  - **§8**: migrations are an explicit command.
  - **§10**: this phase's row.
- `docs/learnings/`. Read the summaries, then open in full:
  - **`0004`**: Details, "Follow-ups, deliberately not done here". The last
    bullet is the entry point this phase builds.
  - **`0011`**: the route table, the principal, and RBAC.
  - **`0001`**: the config loader and the binary's start-up.
- Code. Read it before designing anything:
  - `cmd/hoplock-control/*.go`: all of it. `main.go` holds the subcommand
    dispatch and start-up, `serve.go` and `north.go` the listeners.
  - `internal/config/config.go`, from `Load`/`Parse` to the end of the file.
    `Parse` sets `dec.KnownFields(true)`.
  - `internal/httpapi/north/{router,routes,middleware,server,errors}.go`.
  - `internal/identity/rbac.go` and `principal.go`.
  - `ext/registry.go`.
  - `architecture_test.go`: `TestExtImportsNothingInternal`. It walks `ext/`
    **and every directory beneath it**. That is why this package cannot live
    under `ext/`.
- The request itself, for the requester's own words. It cites Enterprise
  **E1** (Enterprise is a Go program importing Control as a library), **E2** (no
  fork, including a second sign-in in front of its own API) and **E11**
  (cross-tenant reporting for the instance operator). Read them in
  `hoplock/enterprise`'s `docs/PLAN.md` if a choice here turns on one. They are
  cited by id here and never restated.

## Objective
Make M15's sentence true: Enterprise's `main` imports Control's server package,
registers its implementations, and starts it.

Today `cmd/hoplock-control`'s `package main` holds the only way to start
Control. Nothing outside this module can call it. The config decoder refuses
any key Control does not define. The north-bound route table can only be
written from inside `internal/httpapi/north`.

Deliver one public package, `server/`, that gives a host binary four things:

1. Control's whole command line: the daemon and every subcommand.
2. Its own top-level sections in Control's configuration file.
3. Its own north-bound routes, behind Control's authentication, tenant
   resolution and RBAC.
4. Control's error envelope.

`cmd/hoplock-control` becomes a caller of that package like any other host, so
there is one start-up path, not two that drift.

Control alone is unchanged. Same flags, same subcommands, same output, same
config, same routes.

## The request, in this repository's vocabulary
Three needs. For each: the shape asked for, and what Enterprise cannot do until
something meets it. Enterprise's `docs/PLAN.md` §3 states the gap. Its 0001,
0002, 0003, 0005, 0009, 0013 and 0016 each build around it and leave a route
unmounted.

1. **A public start (M15).**
   - *Asked for:* `server.Run(ctx, Options) error`, with
     `Options.Registry *ext.Registry` populated by the host. Control adds its
     own defaults, seals, serves both listeners, and returns when `ctx` ends or
     a listener fails.
   - *Until it exists:* Enterprise's binary builds and cannot start. Its 0001
     exits non-zero at the missing call, on purpose. No Enterprise feature
     reaches a user.
2. **Host sections in the configuration (§8, M13).**
   - *Asked for:* `Options.HostSections []string`: top-level keys the strict
     decoder accepts without defining them. Plus
     `Options.HostConfig func(section string, raw []byte) error`, called with
     each section's undecoded bytes before anything serves. An error there
     fails start-up. Every other unknown key stays an error.
   - *Until it exists:* Control refuses an Enterprise config file because of
     its `enterprise:` section. Enterprise's config layering (its PLAN §5)
     cannot work even once Control can be started.
3. **Host routes behind Control's middleware (M2, M18, M21).**
   - *Asked for:* `Options.Routes []Route`, where
     `Route{Method, Pattern, Permission string; Handler http.Handler}` and the
     pattern sits under `/api/v1/tenants/{tenant}/`. Control mounts each route
     behind the same middleware as its own. A collision with Control's routes,
     or a permission Control does not define, fails start-up.
     `server.CallerFrom(ctx) (Caller, bool)`, with
     `Caller{Tenant ext.Tenant; Subject ext.Subject; Principal string; BreakGlass bool; CorrelationID string}`.
   - *Asked as questions:* who defines the permissions; how a cross-tenant
     route would be scoped; how a host answers an error.
   - *Until it exists:* none of Enterprise's API can be reached: licence state
     (its 0002), the approval inbox (0003), archive search (0005), report
     schedules and campaigns (0009), tenancy governance (0013), and the data
     behind every console screen (0016). The only workaround is a second
     listener with a second sign-in, which E2 and M2 both rule out.

## What this phase answers, and where it differs from the ask
The requester could not read this plan, so their proposed shapes were never
checked against it. Need 2 is met as asked. Needs 1 and 3 are met with the
changes below; each meets the same need.

- **Need 1: the public entry is the whole command line, not only the daemon.**
  - *Why `Run` alone does not fit.* Migrations are an explicit command (§8),
    never a side effect of start-up, so that two nodes do not race to build the
    schema. An Enterprise binary with only `Run` could not `migrate` Control's
    schema. Nor could it `seed`, `audit-verify`, or run the `identity` and `ca`
    commands an operator uses. An Enterprise deployment would then need
    Control's binary beside it for operations, and the two would have to be the
    same version.
  - *What to build.* `server.Main(ctx, args, stdout, stderr, Options) error`
    is Control's command line, with every subcommand. `cmd/hoplock-control`'s
    `main` becomes one call to it. `server.Run(ctx, Options)` is what `Main`
    calls for the daemon, and it is exported for tests and for a host that
    builds its own command line. Each host section is accepted by every
    subcommand that loads the config. `HostConfig` is called only by `Run`.
- **Need 3a: a host names a permission; it never declares one.**
  - *Why declaring does not fit.* §6 and `internal/identity/rbac.go` say "the
    role set is fixed and lives in code". A role's meaning is a fact about the
    product, not about a database or a plugin. If a host could declare codes,
    it would also have to say which of Control's fixed roles grant them. Then
    "admin means these permissions" would depend on which binary is running.
  - *What to build.* Control defines the codes Enterprise's known routes need,
    in its own closed set, as it already did for `grant:approve`. A host
    route's `Permission` must be one of `identity.AllPermissions`, or start-up
    fails. See item 4 for the mapping. A later Enterprise surface that fits
    none of these is a new upstream request. It is not a reason to open the
    set.
- **Need 3b: host routes are tenant routes, and no host route is
  cross-tenant.**
  - *Why.* M18 and §3 make a cross-tenant aggregate route hard to write on
    purpose: `Principal`'s scope map is unexported. E11's operator reporting
    across tenants needs an access class this surface does not have. Creating
    one is a decision about who the instance operator is and how such a
    principal is scoped. That decision must be made and recorded on its own
    terms (a new `M`), not on the way to a plugin seam.
  - *What to build.* Every host route is `AccessTenant`, its pattern is
    relative, and Control prefixes it. Raise E11's cross-tenant route as a
    separate upstream request, after this phase.
- **Need 3c: a host answers errors in Control's envelope, with codes it
  declared.** M21 makes a code the stable thing a client reads, so a host's
  codes join the closed set at start-up. A `401` stays the middleware's alone
  (M11).
- **Need 3d: `Caller` is a public projection.** It is never
  `*identity.Principal`. That type is internal, and its scope map stays
  unexported (M18).

## In scope

### 1. The package split (`server/`, `internal/daemon/`, `cmd/hoplock-control/`)
- **Move** everything in `cmd/hoplock-control/*.go` except `main()` itself
  into a new **`internal/daemon`**. That is the subcommands, `serve`, the
  listener builders, `versionString`, and their tests.
  - Keep each file's name and history (`git mv`).
  - Keep every behaviour.
  - `run(args, stdout, stderr)` becomes
    `daemon.Main(ctx, args, stdout, stderr, daemon.Host) error`. Here
    `daemon.Host` is the internal form of `server.Options` below.
- **`server/`** is a new **public** package at the module root, imported as
  `github.com/hoplock/control/server`. It is a thin façade: the public types,
  their validation, and forwarding into `internal/daemon`. No logic lives here
  that `internal/daemon` could hold. Every exported identifier is a
  compatibility promise, exactly as `ext/` is (PROTOCOL §3).
- **`cmd/hoplock-control/main.go`** becomes:
  `server.Main(ctx, os.Args[1:], os.Stdout, os.Stderr, server.Options{})`,
  plus signal handling and the exit code. Signal handling moves out of `run`
  into `main`, so that a host owns its own signals.
- The `ext` registry still starts empty in the host. `Run` registers Control's
  defaults (`extdefault.Register`, `registerDeclarative`) **into the host's
  registry** before sealing. 0004's rule that an extension beats a Control
  default at a single point, in either registration order, is what makes that
  safe. A nil `Options.Registry` means `ext.NewRegistry()`, which is Control
  alone.
- Every queued prompt that names a file this item moves has its paths updated
  **in this PR**. As this is written that means:
  - 0016: `serve.go`, `grants.go`;
  - 0018: `publish.go`, `auditread.go`, `serve.go`.

  Grep `prompts/queued/` for `cmd/hoplock-control/`. A deletion obligation
  pointing at a path that no longer exists is how a debug endpoint outlives
  its phase (PROTOCOL §3).

### 2. The public shape (`server/server.go`, `server/route.go`, `server/caller.go`, `server/errors.go`)

```go
package server

// Main is Hoplock Control's command line: the daemon and every subcommand
// (migrate, seed, audit-verify, identity, ca). hoplock-control's main is one
// call to it; a host binary's main is one call to it with its Options.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer, o Options) error

// Run is the daemon alone: decode the configuration, register Control's
// defaults into o.Registry, seal it, mount o.Routes, serve both listeners,
// and return when ctx is done (nil) or a listener fails (the error).
func Run(ctx context.Context, config io.Reader, o Options) error

type Options struct {
	// Provider names the host in the route listing and the start-up log
	// (e.g. "hoplock/enterprise"). Required when Routes or HostSections is
	// non-empty; ext.ProviderControl is refused.
	Provider string

	// Registry is populated by the host and NOT sealed. Nil means Control
	// alone.
	Registry *ext.Registry

	// HostSections are top-level configuration keys the strict decoder
	// accepts without defining them. A name Control itself defines is
	// refused at start-up. Every other unknown key is still an error.
	HostSections []string
	// HostConfig is called by Run, once per section present in the file and
	// before anything serves, with that section re-encoded as YAML. An error
	// fails start-up. Absent sections are not called.
	HostConfig func(section string, raw []byte) error

	// Routes are the host's north-bound routes. See Route.
	Routes []Route
	// ErrorCodes are the codes the host's handlers may answer with (M21).
	ErrorCodes []string
}

// Route is a host's north-bound route. Control mounts it on the north-bound
// listener only (M2), at /api/v1/tenants/{tenant}/<Pattern>, as AccessTenant:
// authenticate the caller, resolve exactly one tenant from its scope (M18),
// check Permission in that tenant — before Handler runs.
type Route struct {
	Method     string
	Pattern    string // relative; no leading "/", no "{tenant}", no ".."
	Permission string // one of Control's permission codes; never declared here
	Summary    string // one line for the route listing
	Handler    http.Handler
}

// CallerFrom returns who Control authenticated for the request a host route
// is serving. ok is false outside a host route.
func CallerFrom(ctx context.Context) (Caller, bool)

type Caller struct {
	Tenant        ext.Tenant  // resolved by Control, never read from the request
	Subject       ext.Subject // zero for a machine token
	Principal     string      // the credential that acted; safe to log
	BreakGlass    bool        // asserted at minting, never inferred (M7)
	CorrelationID string
}

// WriteError answers in Control's error envelope (M21): code, typed params,
// English message, correlation id.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code string, params map[string]any, message string)
```

- Names are yours to refine, as long as each need is met as stated. Record
  every divergence from the block above in the learnings summary, because
  Enterprise's sync is written from that summary.
- `Caller.Subject` gives `ID` and `Groups` from the principal. Fill
  `Username` and `ExternalID` where the principal carries them, and say which
  in the doc comment. Never fill them by guessing.

### 3. The configuration (`internal/config`)
- `Parse` takes the set of host sections. It decodes the document once into a
  `yaml.Node` and removes the host sections' top-level keys. It then decodes
  the rest with `KnownFields(true)` exactly as today, and returns the removed
  sections' nodes beside the `*Config`.
  - An unknown key that is **not** declared is still refused, with today's
    message.
  - A declared name that Control's schema defines is refused when the options
    are validated, before the file is read. A host may not shadow `listeners`.
- `Load` and every subcommand go through this path. `migrate` with an
  Enterprise config file must work.
- `config.example.yaml` is unchanged. Control declares no host sections.

### 4. The permissions host routes name (`internal/identity/rbac.go`, §6)
The closed set gains only what Enterprise's routes cannot express with codes
Control already defines:

| Enterprise surface (its phase) | Permission |
| --- | --- |
| approval inbox: list (0003) | `grant:read` |
| approval inbox: approve, reject (0003) | `grant:approve` (exists, unused today) |
| archive search (0005) | `audit:read` |
| reports: read (0009) | `audit:read` |
| report schedules and campaigns: change (0009) | **new** `report:write` |
| licence and entitlement state: read (0002) | **new** `license:read` |
| in-tenant governance (0013) | `identity:read`, `identity:write` |
| cross-tenant reporting (0013, E11) | **none**: not served (out of scope) |

- `report:write` is held by `admin` only. `auditor` changes nothing, and no
  other role's documented meaning covers it. `TestEveryRoleCanReadWhatItCanChange`
  pairs it with `audit:read`.
- `license:read` joins `readOnly`. It is not data about anyone, so every role
  a person holds may read it. `integration` still reads nothing.
- If reading Enterprise's prompts shows that a row is wrong, fix the row in
  this PR and say why in the learnings. The rule stays: a host names, Control
  defines.

### 5. Host routes (`internal/httpapi/north`)
- `north.Options` gains the host's routes and provider. `Server.New` registers
  Control's table first and the host's after it. Both go through
  `Router.Register`, the one enforcement point. There is no second router and
  no second middleware chain.
- Host-route registration refuses each of the following, as a start-up error
  naming the route:
  - an empty or unknown method;
  - a pattern with a leading `/`, `{tenant}`, `..`, or a method prefix;
  - a permission outside `identity.AllPermissions`;
  - a nil handler;
  - a duplicate of any route, Control's or the host's;
  - a pattern `net/http`'s mux reports as conflicting. `ServeMux.Handle`
    panics on conflict, so recover it into an error. A host must never be able
    to crash start-up.
- `RouteInfo` gains `Provider` (empty for Control's own). The start-up log and
  the route listing show it. An operator must be able to see which routes are
  not Control's.
- The middleware puts the `Caller` projection on the context for host routes.
  `server.CallerFrom` reads it. Panic recovery, the access log, the body limit
  and the deadline apply to a host handler exactly as to Control's.
- **Errors** (`errors.go`):
  - The host's `ErrorCodes` join `AllCodes` for this process at start-up. A
    code that is not snake_case, or that duplicates a Control code, fails
    start-up.
  - `WriteError` validates when called. Any of the following is rendered as
    `500 internal` with the correlation id and logged as a host programming
    error:
    - an undeclared code;
    - a status outside 4xx and 5xx;
    - **`401`**, which on this surface is only the middleware's to give (M11).

    Fail closed, and loudly.
- The south-bound server gains nothing. `server.Options` has no field that
  reaches it.

### 6. Architecture guards (`architecture_test.go`)
- `server` is imported by nothing in this module except `cmd/hoplock-control`
  and its own tests. The dependency runs host → `server` → `internal`, never
  back.
- `ext` imports nothing from `server`, so `ext` stays interface-only and cheap
  to import (M15).
- `cmd/hoplock-control`'s non-test files import `server` and the standard
  library only. That is the test that there is one start-up path.
- `TestNoPackageImportsHoplockEnterprise` is unchanged and still passes.

### 7. The documents
- `docs/PLAN.md`, **revised in place** (PROTOCOL §3):
  - **M15**: its first paragraph names `server/` as how Enterprise starts
    Control, mounts routes and keeps its config section. Its register row's
    `Rendered in` gains §6 if the RBAC paragraph now cites it.
  - **§3**:
    - The layout gains `server/` and `internal/daemon/`. The sentence that
      calls `ext/` the only non-internal package is revised.
    - Component responsibilities gains `server` and revises
      `internal/httpapi/north` to say a host's routes enter the same table.
  - **§6**: `report:write`, `license:read`, and "a host names a permission,
    Control defines it".
  - **§10**: this phase's row, to match what was delivered.
- `docs/PROTOCOL.md` §3: "`ext/` is a compatibility promise. It is the only
  non-`internal` package" becomes a statement about `ext/` **and** `server/`.
- `ext/README.md`: a section on starting Control from a host, with a compiling
  example (`server/example_test.go`) that:
  - registers one extension;
  - declares one host section and one route;
  - calls `server.Run`.
- `docs/learnings/0004-extension-points-learnings.md`, under "Follow-ups,
  deliberately not done here": the bullet saying `ext` names no server entry
  point gets one clause pointing at this phase. It is history, so do not
  rewrite it.
- **The shared-surfaces table is not this repository's to edit.**
  `docs/CROSS-REPO-PROTOCOL.md` §1 lists `ext/` as the only surface Control
  owns and Enterprise consumes. The file is the proxy's and is mirrored
  verbatim. So `server/` reaches that table by **an upstream request to
  `hoplock/proxy`** (§3.2, §4.2): the `ext/` row should become Control's public
  Go API, `ext/` and `server/`. The same request asks for a second change to
  §1, which Enterprise raised in enterprise#8 and which was folded into this
  phase so that the proxy edits §1 once: the paragraph under the table still
  says `contract/` and `ext/` "do not exist yet", and they have existed since
  0002 and 0004. Name enterprise#8 as where that part came from. Put both in
  your PR under `## Upstream request`, with the `docs/KICKOFF.md` "Upstream
  request" block filled in. Do **not** edit the local copy.
- No new decision is expected. This phase applies M15, M2, M18, M11 and M21.
  If you find yourself settling something none of them settles, add a decision
  with its register row in the same PR, and say so in its first line.

## Out of scope
- **A cross-tenant access class** for E11's operator reporting. The reason is
  above. Name it in the learnings as the next request Enterprise will raise.
- **A self-scoped host route.** 0016's `AccessSelf` is for Control's own
  requester routes. If Enterprise needs one, that is its own request.
- **Host-declared permissions or roles.** See item 4.
- **Host subcommands.** A host that wants commands of its own dispatches them
  before calling `server.Main`. Control does not route to them.
- **A host writing into Control's audit chain.** Nothing in `ext/` or `server/`
  lets a host append an audit record. Enterprise did not ask, and no route
  above needs it: approvals reach Control through `ext.GrantWorkflow`, which
  0012 already audits. If a governance act needs Control's chain, that is a
  request of its own.
- **Console screens.** 0020 owns how Enterprise adds screens. This phase gives
  those screens an API to call, and nothing else.
- **Host routes in M19's north-bound version.** If 0019 has merged, the version
  describes Control's routes, and the listing's `Provider` tells a client which
  routes are not Control's. Versioning a host's routes is the host's own
  compatibility promise.
- **`contract/` (M1).** Nothing here touches the wire.

## Acceptance criteria
Each criterion is a test.

**One start-up path**
- `hoplock-control` behaves exactly as before. The existing `main_test.go`
  cases pass from `internal/daemon` unchanged, apart from the moved imports.
  `--version`, an unknown subcommand, and a strict-decoder refusal produce the
  same output as before.
- A test host in `server/` starts with a registered fake extension, one host
  section and two host routes. Then, without a database for anything the
  routes do not touch:
  - the fake is in the sealed set;
  - Control's own defaults are present too;
  - `HostConfig` received the section's bytes;
  - both listeners serve.
- `server.Main(…, []string{"migrate", …}, …)` with a config carrying a host
  section succeeds against a test database. An undeclared extra key is still
  refused.
- `HostConfig` returning an error fails `Run` before any listener binds.
  A host section named `listeners` is refused before the file is read.

**Host routes**
- A host route answers only after Control has done three things:
  authenticated the caller, resolved the tenant from the principal's scope,
  and checked the permission.
  - `CallerFrom` gives the resolved tenant, the subject, the principal id, the
    break-glass flag and the correlation id.
  - A break-glass principal arrives with `BreakGlass: true`. A federated one
    arrives with `false`.
- **The isolation tests cover host routes on the day they are mounted.** Run
  `TestEveryRegisteredRouteRefusesATenantOutsideTheCallersScope` and
  `TestEveryRegisteredRouteRefusesAnUnauthenticatedCaller` over a server built
  with two fake host routes. Both enumerate the host routes, and both pass.
- A caller without the route's permission gets `403 forbidden`. One without a
  credential gets `401 unauthenticated`. Neither reaches the handler.
- Each registration refusal in item 5 has a case, including a mux conflict,
  which must come back as an error and not a panic.
- No host route is reachable on the south-bound listener. Add a south-side
  test that enumerates the north router's routes, host routes included, and
  asserts that the south-bound server answers none of them. It is the mirror of
  `TestNoContractRouteIsReachableOnThisListener`, which runs on the north
  side.
- `WriteError` has these cases:
  - a declared code renders Control's envelope with the correlation id;
  - an undeclared code renders `500 internal`;
  - `401` renders `500 internal`;
  - a host code that collides with a Control code fails start-up.
- A panicking host handler gets `500 internal`, and the server keeps serving.

**RBAC**
- `report:write` is held by `admin` only.
- `license:read` is held by every role except `integration`.
- Every permission is held by some role. The auditor still changes nothing.

**Architecture**
- The three guards in item 6 pass. Each has a negative case in the style of
  `TestTheImportGuardActuallyCatchesOne`.

## Cross-repo impact
This phase answers an upstream request **and** adds a public package that
`hoplock/enterprise` builds against. So it owes a downstream sync. The PR that
implements it MUST carry a `## Cross-repo impact` section
(`docs/CROSS-REPO-PROTOCOL.md` §4.1) naming **every** consuming repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5, "The PR that answers an upstream request is
not a sync"). It knows what it asked for. It does not know what was built, and
four shapes differ from the ask. The session there that rewrites its prompts
will be a fresh one that knows nothing.

State at least these obligations:

1. **Its PLAN §3 gap paragraph** ("Two attachments that wiring needs do not
   exist in Control yet") is rewritten from what merged. Its 0001 calls
   `server.Main` and drops the exit-at-the-missing-call seam.
2. **Its route table becomes `server.Options.Routes`.** Each route's pattern is
   relative, and each names a permission from item 4's table.
   - 0002, 0003, 0005, 0009 and 0013 drop "unreachable until Control can mount
     it".
   - 0002's two deferred E4 proofs become runnable.
3. **Its config section.** `enterprise:` is declared through `HostSections`,
   and its PLAN §5 layering is decoded in `HostConfig`.
4. **Its errors.** Every Enterprise route answers through `server.WriteError`
   with codes it declares, and never with a `401`.
5. **What it did not get.** There is no cross-tenant route, so its 0013's E11
   reporting is still blocked, and that is its next upstream request. There
   are no host-declared permissions.

Queue the **"Downstream sync" kickoff** from `docs/KICKOFF.md`, verbatim except
for its blanks — this PR's URL, and the obligations above — as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`, committed
once the PR is open, and end the section by naming that file
(`docs/CROSS-REPO-PROTOCOL.md` §4.3).

### `hoplock/proxy`
- **Downstream: "None".** The contract (M1) is untouched, and the proxy
  consumes neither `ext/` nor `server/`.
- **Upstream: one request, for two changes to `docs/CROSS-REPO-PROTOCOL.md`
  §1 (item 7).** List `server/`, and rewrite the paragraph that says
  `contract/` and `ext/` "do not exist yet". The second is enterprise#8's need.
  Its request file, `enterprise-PR#8-protocol-surfaces-exist.md`, is in
  Enterprise's `prompts/upstream/implemented/`, because folding it in here was
  the answer. Put both under `## Upstream request`, with one kickoff queued as
  `prompts/upstream/queued/control-PR#<n>-<short-description>.md`. It is the
  only one of the two hand-overs this phase owes the proxy.

### Who runs them
Not you. This PR merges first, and the sync and the request run afterwards in
their own sessions (`docs/CROSS-REPO-PROTOCOL.md` §2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0015-public-server-package-and-host-routes-learnings.md`.
Enterprise's sync is written from its summary block, which MUST give:

- the exported signatures of `server/`, verbatim;
- the permission table from item 4, as merged;
- every start-up refusal a host can trigger;
- what `WriteError` refuses;
- which files moved from `cmd/hoplock-control` to `internal/daemon`.
