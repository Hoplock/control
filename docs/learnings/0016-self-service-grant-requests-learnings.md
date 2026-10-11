# 0016 — self-service grant requests — Learnings

## Summary
- **What shipped:** a requester's own door into a registered approval workflow, `ext.GrantRequest.BreakGlass`, and a poller that learns every tenant a proxy subscribes in. Migration **`0010`**. Cuts **`v0.3.0`**. Answers `Hoplock/enterprise#10`.
- **Routes** (all `AccessSelf`, all `grant:request`): `POST /api/v1/tenants/{tenant}/me/grant-requests` (ask), `GET …/me/grant-requests` (yours, newest first; `?state=`, `?limit=`), `GET …/me/grant-requests/{request}` (one of yours, advanced while pending), `POST …/me/grant-requests/{request}/cancel` (withdraw). Somebody else's request is `404 not_found`. A credential with no subject is `403 subject_required` on all four. Tenant-wide routes unchanged.
- **The self create answers:** `202` + request (pending) · `201` + grant + request (approved at once) · `403 grant_request_denied` · `502 grant_workflow_failed` · **`403 grant_workflow_not_registered` with no workflow registered — nothing stored, audited or announced** · `400 invalid_request` (a body naming `subject`, or one 0012 would refuse).
- **`requester` role** = exactly `grant:request` (`admin` also holds it). **No `readOnly`**: it reads only its own requests, through the self routes, so it changes nothing blind; the tenant's audit, decisions and identities are not needed to ask. So it does **not** hold `license:read` either.
- **`ext` change (additive), verbatim:**
  ```go
  // BreakGlass reports that the credential RequestedBy presented was minted
  // on the break-glass path (M7). It is copied from the request Control
  // stored — asserted when that credential was minted, never inferred from a
  // login source or a name — and it describes who raised the request, never
  // Subject. A workflow may treat such a request differently; it learns the
  // fact here or not at all.
  BreakGlass bool
  ```
  A workflow tells the doors apart by `Subject.ID == RequestedBy.ID` (self) and never by another field.
- **How the poller learns a tenant:** `internal/daemon` wraps the `south.EventStream` (`trackSubscriptions`) so each `Subscribe` calls `access.Service.Track(tenant)` — the tenant the proxy's credential carries (M22) — then delegates. Plus the configured tenant, and any tenant a request is raised or read in. No store read lists tenants; no sink.
- **Key files:** `internal/access/own.go`, `internal/httpapi/north/{router,middleware,routes,grants,errors}.go`, `internal/store/grants.go`, `internal/daemon/{grants,serve}.go`, `ext/{access,point}.go`.
- **Decisions:** none added; M10 revised in place (register row now renders in §6). §3, §6, §10 revised.
- **NEXT session:** a route about the caller's own records is `AccessSelf` with a `/me/` path, reads `SubjectFrom`, and its store query names the subject. Enterprise's licence route is unreachable to a requester-only principal.

## Details

### What differs from the ask, and why

The request (`Hoplock/enterprise#10`) proposed three shapes; need 2 is met exactly,
needs 1 and 3 in the alternative shapes the prompt specified:

- **Need 1** asked for a create at `…/grant-requests` and for cancel to take
  `grant:request` for one's own request and `grant:write` for anybody else's.
  The second half makes a route's permission depend on whose row it is, which
  only the handler can know — §6 rules that out. So "own" became an access
  class (`AccessSelf`) and a route family (`…/me/…`), and the tenant-wide
  routes were left alone. The create moved into the family too, so a future
  approvers' `GET …/grant-requests` never shares a path with a self create.
- **Need 3** offered a poller covering every tenant "from start-up", or a
  `Decided` sink. The first cannot be done literally (no tenant registry, and
  every repository method names its tenant), the second is a second trigger
  that leaves the same gap when it fails to fire. A grant is only ever used
  through a proxy, and every proxy holds an event subscription naming its
  tenant, so that subscription is what reports the tenant.

### `AccessSelf`, precisely

`enforce` runs the `AccessTenant` steps unchanged — credential, tenant selector
inside scope, permission — and only then, for `AccessSelf`, refuses a principal
with `Subject == ""` (`403 subject_required`, logged through `logRefusal`) and
puts the subject on the context. The order matters: a tenant outside scope still
answers `tenant_out_of_scope`, and a missing permission `forbidden`, so the
existing isolation tests apply to self routes unchanged (they now enumerate
both classes). `Register` requires a defined permission for `AccessSelf` as for
`AccessTenant`. The route-table test also asserts a self route's path carries
`/me/` and no tenant route's does, so "own" is visible in a log line.

Handlers read the subject through `SubjectFrom` only (`selfActor` overrides
the actor's subject with it). The service reads through
`GrantRequests().GetForSubject` / `ListForSubject`, whose `WHERE` clause names
the subject; a mutation that dropped the predicate fails
`TestEverySelfRouteKeepsOneRequestersRequestsFromAnother` (checked by hand
during the phase).

### The refusal with no workflow

`access.ErrNoWorkflow` is returned by `RequestOwn` before `normalize` and before
anything is stored, so the refusal holds for any future caller of the service,
not only the handler — `RequestOwn` never reaches `createDirect`. It is a 403
with its own code, on 0013's `push_not_supported` precedent: a refusal made on
purpose (M11), not a 5xx a client would retry, not a 503 (nothing is
misconfigured — M15 says a deployment with no workflow is complete), and not
`grant_request_denied` (no workflow said no). Reading and cancelling one's own
requests still work with no workflow: requests raised while one was registered
remain the holder's to see and withdraw.

### Wiring faults vs refusals

`RequestOwn` with a non-empty `spec.Subject`, or an actor with no subject, is a
plain error (an outage), as `Actor.check` treats a missing principal: the
transport never sends either. Same for `OwnRequests`/`OwnRequest` with an empty
subject. The body type for the self create has no `subject` field, so a client
that sends one gets `400 invalid_request` (`field: body`) from strict decoding.

### Shape changes inside the module

- `north.grantRequestBody` now embeds `grantSpecBody` (the holder-less part),
  which is the self create's body; `(grantSpecBody).spec()` builds the
  `access.Spec` for both. A bad `duration` still answers `field: duration`.
- `access.Service.Request`/`Cancel` delegate to unexported `read`/`cancel`,
  which the `Own*` methods share after their subject-scoped read.
- `serve.go` builds the notifier and the grant service before `south.New`.

### Things a reader will trip over

- **`grant_self_granted`.** A grant from the self door has creator == holder,
  so its `grant.created` audit record carries `grant_self_granted: "true"`,
  exactly as 0012 defined the attribute. It also carries origin `workflow` and
  the approvers, which is how an auditor tells it from an administrator's
  self-grant. Not changed here.
- **`license:read`.** 0015 gave it to "every role a person holds". `requester`
  is a person's role that holds only `grant:request`, so the rule is now
  "every role but `integration` and `requester`" (test renamed
  `TestLicenseReadIsHeldByEveryRoleThatReadsTheTenant`). A host's licence route
  is `403 forbidden` to a requester-only principal; a console hiding licence
  state from such a user is correct.
- **Multi-node.** Each node tracks the tenants of proxies subscribed to it. A
  tenant with no proxy on any node is polled only once a request there is read
  or raised — but then no grant there can be used either.

### Test notes

- `internal/access/own_test.go`: the lifecycle through the new door (approve,
  allow, expire with nothing run), the two doors as the workflow sees them, the
  no-workflow refusal storing nothing, wiring faults, own-means-holder, and
  break-glass on the admin create, the self door and a resubmission.
- `internal/httpapi/north/own_test.go`: the same over the wire, plus the three
  table-driven tests over the router (subject isolation over every `AccessSelf`
  route, machine token over every `AccessSelf` route, requester over every
  `AccessTenant` route) and break-glass from the credential (a `local` session
  without the flag arrives unflagged). Sessions are minted with a
  `store.NorthPrincipal` row (`serverFixture.session`).
- `internal/daemon/tenants_test.go`: two tenants, a request pending in the
  non-configured one, a fresh service from `buildGrants`, the real south-bound
  listener with `trackSubscriptions`; nothing is applied until a proxy of that
  tenant subscribes over HTTP, then the grant exists and authorize allows. A
  mutation that skipped `track` fails it.
- `architecture_test.go`: `TestTheSouthBoundSideDoesNotImportTheGrantService`
  and its proof against a planted tree.

### Cross-repo

Owes `hoplock/enterprise` a downstream sync (it asked, and `ext/` changed); the
PR's `## Cross-repo impact` lists the obligations and names the queued file.
`hoplock/proxy`: none — the contract is untouched.
