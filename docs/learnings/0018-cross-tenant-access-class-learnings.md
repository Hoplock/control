# 0018 — cross-tenant access class — Learnings

## Summary
- **What shipped:** one north-bound route class spanning tenants, for a host's reads. **M24** added; M18's register row now says **`amended by M24`**. M24 in one line: *a cross-tenant read is a route class the middleware resolves, and only a host serves one.* Cuts **`v0.5.0`**. Answers `Hoplock/enterprise#19`. `ext` unchanged; `server` additive (release-check: 5 compatible, no break).
- **`server`, verbatim:**
  ```go
  type Access int
  const ( AccessTenant Access = iota /* zero value: /api/v1/tenants/{tenant}/<Pattern>, Caller.Tenant */; AccessCrossTenant /* /api/v1/cross-tenant/<Pattern>, Caller.Tenants */ )
  type Route struct { Method, Pattern, Permission string; Access Access; Summary string; Handler http.Handler }
  // Caller gains, after Tenant (which is zero on a cross-tenant route):
  Tenants []ext.Tenant // sorted, never empty, on AccessCrossTenant; nil on AccessTenant
  ```
- **Prefix and selector:** `north.CrossTenantPrefix` = `/api/v1/cross-tenant/`, this class only, both ways; no `{tenant}` in a pattern. Selector = every non-empty `?tenant=` value + every `X-Hoplock-Tenant` value, trimmed, de-duplicated, sorted.
- **Answers:** no selector → every tenant in scope holding the permission (`identity.Principal.TenantsWith`), or `403 forbidden {permission}` if none. Selector → each named tenant checked in sorted order, first failure answered: out of scope `403 tenant_out_of_scope {tenant}`, in scope without it `403 forbidden {tenant, permission}`; never dropped. No credential `401 unauthenticated`. No new code. Access log carries `tenants`, no `tenant`.
- **Permission rule:** one `identity.RoleAuditor.Can` — the auditor's reads, not a list of its own (a second definition of "read" would drift). Every read, not only `audit:read`: the class grants nothing N requests would not.
- **Control serves none of its own:** `Router.Register` refuses the class with an empty `Provider`; `TestControlServesNoCrossTenantRouteOfItsOwn`; a source test (`architecture_test.go`) allows `TenantsWith` one reference outside identity's tests, in `internal/httpapi/north/middleware.go`.
- **Who spans tenants today:** only a token minted on the box (`identity token-issue --scope 'a=auditor;b=auditor'`). A session — federated or break-glass — is minted for the one tenant it signed in through, so it covers `[that tenant]`.
- **NEXT session:** 0019's routes are Control's and stay per tenant (its "Tenancy on the surface" says so). 0021's console must not promise a signed-in person an aggregate. Sync for `hoplock/enterprise` queued (see PR).

## Details

### Where this differs from the prompt, and why

- **The federated-session criterion is proven in `internal/httpapi/north`,
  not through `server.Run`.** A federated login needs an IdP, and
  `server.Options` has no way to hand one in (nor should it, for a test).
  `TestAFederatedSessionCoversTheOneTenantItSignedInThrough` drives the real
  `Federation.Begin`/`Complete` with a stub `BrokerFactory`, so the session is
  minted by the real `mintSession`, with auditor bindings in two tenants, and
  shows the session covers only the one it signed in through. Everything else
  in "The class, end to end" is in `server/server_test.go`, through
  `server.Main`.
- **The header selector reads every value** (`Header.Values`), not the first
  (`Header.Get`, which tenant routes use). A second header line skipped would be
  a named tenant dropped rather than checked.
- **`north.HostRoute.Access`'s zero value is `AccessAnonymous`, and it is
  refused.** Only `server.Access` has the tenant class as its zero value. Every
  internal `HostRoute` literal names its class; `server.Options.host()` maps
  each `server.Access` explicitly, and an unknown one maps to `north.Access(-1)`,
  which `hostRoute` refuses ("has no access class Control defines"), so every
  host check still lives below `server` (§3).
- **Registration also refuses** a cross-tenant pattern that is the bare prefix
  (a ServeMux subtree catch-all), `{tenant...}` as well as `{tenant}`, and any
  other class at the unslashed root `/api/v1/cross-tenant`. `namesTenantParam`
  is shared by `Router.Register` and `checkHostPattern`.
- **`TenantsFrom` returns a copy**, and `HostCallerFrom`/`CallerFrom` clone
  `Tenants`, so a handler cannot change what a request covers.
- **M10's "without the cross-tenant read M18 rules out"** (the poller) now says
  "a store query spanning tenants": it meant a store read, which M24 does not
  change, and as written it contradicted M24.

### Plan revised in place

§2: register (M24 row; M18 `amended by M24`), M15 (a host's routes are tenant
routes or cross-tenant reads), M18 (first line, north-bound bullet, last
paragraph), M24 itself. §3: `internal/identity`, `internal/httpapi/north` and
its host-routes paragraph, `server`. §6 RBAC paragraph, §10's 0018 row, §11's
governance bullet. 0019's "Tenancy on the surface" and its Read first (M18, M24).

### Tests worth knowing about

- North fixtures carry three host routes; the third is the cross-tenant read
  (`GET reports/privileged-access`, `audit:read`), which answers its caller.
  `TestEveryRegisteredRouteRefusesATenantOutsideTheCallersScope` names the tenant
  in the selector for that class and requires one of each class.
- `TestEveryCrossTenantRouteResolvesOnlyTheTenantsWhereTheCallerHoldsItsPermission`
  is the class's isolation table: the read in `tenant-a`, a role lacking it in
  `tenant-b` (picked per route by `lacking`), `tenant-c` out of scope.
- `TestTheTenantSetGuardActuallyCatchesOne` proves the source test on a
  throwaway tree, as the import guards beside it do.
- `TestAHostRouteThatIsNeitherClassOrACrossTenantWriteIsRefusedWithBothPortsFree`
  calls `server.Run` with real free ports and checks both still bind.

### Local tooling gotcha

The container's preinstalled `golangci-lint` was built with go1.25 and refuses a
go1.27 module. `go install …/golangci-lint@v2.14.0` outside the module also
builds with the base toolchain (go1.26); prefix it with
`GOTOOLCHAIN=go1.27.0`. Postgres 16 is installed but stopped:
`service postgresql start`, set the `postgres` password, and export
`HOPLOCK_TEST_DSN`, or every store-backed test skips.
