# 0016 — Self-service grant requests: a requester's own door, break-glass on the request, every live tenant polled

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/10, under `## Upstream request`,
> and answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2).
>
> **Where it sits.** Third, after tagged releases (0014) and the public server
> package (0015). It depends on nothing queued: only on 0011 (RBAC and the
> north-bound credential model), 0012 (grants and the workflow seam) and 0013
> (whose `integration` role and push-receiver refusals it follows), all merged.
> It runs before the north-bound API (0018) because Enterprise's approval phase
> (its 0003) builds around these three gaps until they exist.

## Read first
- `docs/PROTOCOL.md` — session workflow. Read §3 for `ext/` as a compatibility
  promise and for revising the plan in place.
- `docs/CROSS-REPO-PROTOCOL.md` — **§4.1**, **§4.3** (where the sync is
  queued), and **§5** at "The PR that answers an upstream request is not a
  sync". This phase changes `ext/`, a shared surface, and it answers a request,
  so it owes a downstream sync (below).
- `docs/PLAN.md`:
  - **§2**:
    - **M10**: grants, and governance as a seam. This phase extends its
      governance paragraph.
    - **M15**: the line between this repository and Enterprise, and the three
      forms an absent extension takes.
    - **M18**: tenancy is resolved from the caller and never asserted by it.
      This phase applies the same rule to the subject, and its third need is
      M18's "the store cannot enumerate tenants".
    - **M7**: break-glass is asserted, never inferred.
    - **M11**: a refusal made on purpose is a decision, and a 5xx is an
      outage.
    - **M2**: the north-bound surface and its one middleware chain.
    - **M9** and **M22**: the event subscription every proxy holds, and the
      tenant its credential carries.
  - **§6**, "Users, groups, roles and RBAC (M7, M18)" and "Break-glass is
    asserted, never inferred (M7)".
  - **§10**: this phase's row, and 0012's.
- `docs/learnings/` — read the summaries.
  - Open **`0012`** in full: the workflow contract, and under Details "The
    poller, and the one gap M18 forces". Need 3 below is that gap.
  - Open **`0011`** for RBAC, the principal and the route table, **`0004`** for
    the `ext` registry and `ext.Points()`, and **`0009`** for the event
    subscription.
  - In **`0013`**'s summary, read the first bullet. Its `integration` role is
    the first role without `readOnly`, and its push receiver is the precedent
    for refusing a capability this deployment lacks: a 4xx with its own code.
- Code. Read it before designing anything:
  - `ext/access.go`: `GrantRequest`, and the doc comment on `GrantWorkflow`.
    Its "What Control does with each answer" list is the contract Enterprise
    builds against.
  - `ext/point.go`: `PointGrantWorkflow`.
  - `internal/identity/rbac.go` and `principal.go`.
  - `internal/httpapi/north/{router,routes,middleware,grants,errors}.go`, and
    `refusePush` in `accesscontext.go`.
  - `internal/access/{service,workflow}.go`.
  - `cmd/hoplock-control/{serve,grants}.go`.
- The request itself, for the requester's own words. It cites Enterprise
  **E8** (approvals are policy inputs, never a second path into the decision)
  and **E4** (unlicensed or unconfigured means exactly Control's behaviour).
  Read them in `hoplock/enterprise`'s `docs/PLAN.md` if a choice here turns on
  one. They are cited by id here and never restated.

## Objective
Make M10's own example true from its first clause, on a deployment with an
approval workflow registered: **a developer asks for access for themselves.**

Today only `grant:write` reaches `ext.GrantWorkflow.Submit`. The same
permission revokes any grant and cancels any request in the tenant. So the
people a workflow exists to govern can reach it only by being given the power
the workflow exists to govern.

Alongside that door, deliver the two things the workflow needs to decide well
and to have its decisions applied everywhere:

- the break-glass assertion on the request it decides (M7);
- a poller that does not forget a tenant across a restart (M18).

Control alone is unchanged. With no workflow registered, an administrator
creates a grant directly (M10), exactly as today. A requester's own request is
refused, and the refusal says why: nothing would decide it, and a request
nobody decides is a self-grant.

## The request, in this repository's vocabulary
Three needs. For each: the shape asked for, and what Enterprise cannot do until
something meets it. Enterprise's approval phase (its 0003, "What Control does
not have yet") builds around all three and says what it leaves unwired.

1. **A requester's own door (M10, M18, §6).**
   - *Asked for:*
     - a permission, `grant:request`;
     - `POST /api/v1/tenants/{tenant}/grant-requests` under it, taking the
       administrator's create body without `subject`;
     - the caller as the holder, and a principal with no subject refused;
     - the create answering as a create does with a workflow registered:
       `202`, `201`, `403 grant_request_denied`;
     - a refusal with a machine-readable code while no `ext.GrantWorkflow`
       is registered;
     - the caller's own requests listed, and cancellable under
       `grant:request`, while cancel stays `grant:write` for everybody else's.
   - *Until it exists:* a developer cannot ask. A grant administrator raises
     every request on the holder's behalf, so E8's "who may ask" chooses only
     among grant administrators.
2. **The break-glass assertion on the request (M7, M15).**
   - *Asked for:* `ext.GrantRequest.BreakGlass bool`, true when the credential
     that raised the request was minted on the break-glass path.
   - *What exists:* Control already records the flag on the stored request
     (`grant_requests.requested_by_break_glass`) and on the grant
     (`CreatedBy.BreakGlass`), and writes it into the audit record. But
     `workflowRequest` drops it.
   - *Until it exists:* E8 puts any override inside the workflow, never in a
     route around it, and the workflow has nothing to key it on. A break-glass
     administrator's request waits for approvers like any other. That includes
     an IdP outage, which is when break-glass is used and when approvers may not
     be able to sign in.
3. **A decided request applied without anybody reading it (M10, M18).**
   - *Asked for:* either Control's poller covering every tenant with a pending
     request from start-up, without the cross-tenant read M18 rules out; or a
     sink handed to the workflow, `Decided(ctx, tenant, workflowRef) error`,
     after which Control asks `Status` at once.
   - *Until it exists:* the poller walks only the tenants this process has
     seen. That is the configured tenant, plus any other once a request is
     raised or read in it. So in a deployment with more than one tenant, after
     a restart, an approval in a non-configured tenant creates no grant, and
     the proxy keeps denying. That lasts until somebody reads that request
     through Control or raises another request in that tenant.

## What this phase answers, and the two places it differs from the ask
The requester could not read this plan, so their proposed shapes were never
checked against it. Need 2 is met exactly as asked. Needs 1 and 3 do not fit as
written; each alternative below meets the same need.

- **Need 1: "own" is a route family, not a branch inside a route.**
  - *Why the ask does not fit.* On the existing cancel route, the ask makes the
    permission depend on whose request it is. Only the handler can know that,
    after reading the row. §6 rules that out: every route has one access class
    and one permission, checked before the handler runs, because "a handler is
    never asked".
  - *What to build.* "Own" becomes part of the route's definition. The routes
    sit in a self-scoped family whose subject the middleware resolves from the
    credential, under one permission, `grant:request`. That is M18's rule for
    the tenant, applied to the subject. The tenant-wide routes keep
    `grant:read` and `grant:write`, untouched.
  - The create moves into the same family. That way a future tenant-wide
    `GET …/grant-requests` (the approvers' list a console will want) never
    shares a path with a self-scoped create.
- **Need 3: the poller learns tenants by seeing them, not by listing them.**
  - *Not "from start-up".* That cannot be done literally. There is no tenant
    registry, and every repository method names its tenant. That is what makes
    a cross-tenant read unwritable (0003's `TestRepositoryMethodsTakeATenant`).
  - *Not the sink.* `Status` stays the only answer, however Control is told to
    ask. A sink is a second trigger, and when it fails to fire (a workflow
    that never calls it, or a call lost in a crash) the same gap remains. The
    poller has to close that gap anyway. The sink would also add an interface
    to `ext/` that Enterprise must implement.
  - *What closes it.* The poller learns **every tenant a grant could be used
    in**. A grant is only ever used through a proxy, and every proxy holds one
    long-lived event subscription (M9) whose credential names its tenant (M22).
    So each proxy's tenant is reported to the poller when it opens its
    subscription.
  - *The result.* After a restart, each tenant with a live proxy is polled
    soon after its first proxy reconnects, and a decision there is applied
    within one `grants.workflow_poll_interval`. Tenants are recorded as they
    are seen, never listed, and no store read crosses tenants.
  - If E8 later measures that it needs the push, that is a request of its own.

## In scope

### 1. RBAC: a permission to ask, and a role that holds only that (`internal/identity/rbac.go`, §6)
- `PermGrantRequest Permission = "grant:request"`: asking the registered
  workflow for a grant for yourself. Add it to `AllPermissions`.
- `RoleRequester Role = "requester"`, holding **exactly** `grant:request`.
  - It goes in `AllRoles` after `integration` (0013) and before `auditor`:
    the list is ordered least privilege first.
  - `admin` holds it through `AllPermissions`. No other role gains it.
  - Test it the way `TestTheIntegrationRoleCanOnlyPush` tests `integration`:
    `requester` holds exactly `grant:request`, no role but it and `admin`
    holds that permission, and `ParseRole("requester")` resolves.
- `requester` deliberately does **not** hold `readOnly`. That set includes
  `audit:read`, `decision:read` and `identity:read` over the whole tenant, and
  nobody needs those to ask for access.
  - It is the second role without `readOnly`, and the first that a person
    holds. Since 0013, `readOnly`'s comment says it is "what every role a
    PERSON holds can do", with `integration` the one exception. That stops
    being true, so revise the comment to name both exceptions and their
    different reasons:
    - `integration` is somebody else's software, and it reads nothing on
      purpose.
    - `requester` changes only its own requests, and it reads exactly those,
      through the self routes below and nowhere else. So it never changes
      anything blind.
  - `TestEveryRoleCanReadWhatItCanChange` gains no pair for `grant:request`,
    because no tenant-wide read matches it. Its comment says so.
- `TestTheAuditorChangesNothing` lists `grant:request` among the writes.

### 2. A self-scoped access class (`internal/httpapi/north/router.go`, `middleware.go`)
- Add `AccessSelf` to the closed `Access` enum. A route in this class needs:
  - a live principal **with a person behind it**;
  - exactly one tenant resolved and one permission checked, exactly as
    `AccessTenant` does;
  - then the **subject resolved from the principal** and put on the context.
- `SubjectFrom(ctx) (string, bool)` is the only way a handler reads that
  subject, mirroring `TenantFrom`. A handler on a self route never reads a
  subject from the path, the query or the body, because none of them carries
  one.
- A principal with no subject (a machine token, `Principal.Subject == ""`) is
  refused on every `AccessSelf` route, after the permission check, with `403`
  and a new code, `subject_required`. A token has no "own" requests, and a
  request it raised for "itself" would name nobody.
- `Route.Permission` is required for `AccessSelf`, as it is for
  `AccessTenant`. Registration refuses a self route without one, and the two
  existing tests that hold the table to that are extended to the new class:
  `TestEveryRouteDeclaresAnAccessClassAndATenantRouteDeclaresAPermission` and
  `TestARouteThatIsNotFullyClassifiedIsRefusedAtRegistration`.
- `errors.go` gains `subject_required` and `grant_workflow_not_registered`
  (item 3 below), both in `AllCodes`.

### 3. The requester's routes (`internal/httpapi/north/routes.go`, `grants.go`)
Append to the route table, under `// --- a requester's own (M10, 0016) ---`:

```
POST /api/v1/tenants/{tenant}/me/grant-requests                   AccessSelf  grant:request  ask the registered workflow for a grant for yourself
GET  /api/v1/tenants/{tenant}/me/grant-requests                   AccessSelf  grant:request  your own requests, newest first
GET  /api/v1/tenants/{tenant}/me/grant-requests/{request}         AccessSelf  grant:request  one of your own, asked about again while it is pending
POST /api/v1/tenants/{tenant}/me/grant-requests/{request}/cancel  AccessSelf  grant:request  withdraw one of your own pending requests
```

- **The create's body** is the administrator's create without `subject`:

  ```
  {"scope": {"name", "targets", "labels", "zones"},
   "not_before", "expires_at" | "duration",
   "reason", "reason_code", "external_ref"}
  ```

  It is validated exactly as 0012 validates a create (`normalize`,
  `grants.max_duration`). A body that names `subject` is `400
  invalid_request`: the body type has no such field, and strict decoding
  refuses unknown fields. The holder is the caller, never named in the body.
- **The create's answers** match the administrator's create with a workflow
  registered:
  - `202` with the request while it is pending;
  - `201` with the grant and the request when approved at once;
  - `403 grant_request_denied`;
  - `502 grant_workflow_failed`.
- It has one answer of its own: **with no workflow registered, `403` and
  `grant_workflow_not_registered`**.
  - Nothing is stored: no request row, no grant, no audit record. The message
    says a grant administrator can grant access directly.
  - It is a refusal this server makes on purpose, so it is a 4xx with its own
    code (M11). That is how 0013's push receiver answers a capability this
    deployment lacks: `push_not_supported` is a `403`.
  - It is not a 5xx. On this surface a 5xx is an outage, and a caller may
    retry an outage. Retrying here gets the same answer until a workflow is
    registered, and a deployment with none is working as designed (M15).
  - That is also why it is not a `503` like `ca_not_configured`, which reports
    a deployment missing a setting it needs.
  - It is not `grant_request_denied`, because no workflow said no. A client
    tells the two apart by code (M21).
  - The refusal lives in the service, not only in the handler (item 4 below),
    so no later caller can turn a self-request into a self-grant.
- **"Own" means the caller is the request's holder** (its `subject`), whoever
  raised it. So a request a grant administrator raised on the caller's behalf
  is the caller's to read and withdraw too. Withdrawing a request for your own
  access cannot widen anything.
- **List** takes `?state=` (one of
  `pending|approved|denied|expired|cancelled|failed`) and `?limit=`, with the
  grant list's default and ceiling. It is newest `requested_at` first, and
  answers `200 {"tenant", "requests": [...]}` in 0012's request view.
- **Read** behaves as `GET …/grant-requests/{request}` does: a pending request
  is advanced first, and the answer is `200` whatever its state, with the grant
  it produced if any.
- **Cancel** behaves as `POST …/grant-requests/{request}/cancel` does,
  including `409 grant_request_not_pending`. It is audited with the requester
  as the actor.
- **Somebody else's request is `404 not_found`** on read and on cancel, the same
  answer as a request that does not exist. Never `403`, which would confirm that
  it exists.
- The tenant-wide routes do not change. `POST …/grants` and
  `POST …/grant-requests/{request}/cancel` stay `grant:write`, and a requester
  calling either gets `403 forbidden`.
- If 0019 has merged when you start, these routes enter M19's north-bound
  version like any other addition. If it has not, 0019 versions them with the
  rest of the surface.

### 4. The service and the store (`internal/access`, `internal/store`)
- `access.Service` gains:
  - `RequestOwn(ctx, tenant, actor Actor, spec Spec) (Created, error)`.
    - The holder is `actor.Subject` and nothing else. A non-empty
      `spec.Subject`, or an empty `actor.Subject`, is a wiring fault and so an
      outage, the way `Actor.check` treats a missing principal.
    - With no workflow registered it returns a new `ErrNoWorkflow`, **before
      anything is stored**.
    - Otherwise it is 0012's `request` path, unchanged: recorded, audited as
      `grant.requested`, announced, tracked, submitted.
    - It never reaches `createDirect`.
  - `OwnRequests(ctx, tenant, subject, q)`, `OwnRequest(ctx, tenant, subject,
    requestID)` and `CancelOwn(ctx, tenant, actor, requestID, reason)`. Each
    reads through the subject-scoped store methods below, then behaves exactly
    as 0012's read and cancel.
- `store.GrantRequestRepository` gains `GetForSubject(ctx, tenant, subjectID,
  requestID)` and `ListForSubject(ctx, tenant, subjectID, q
  GrantRequestQuery)`.
  - `GrantRequestQuery{State GrantRequestState; Limit int}`: a zero limit takes
    a default, and there is no unbounded list, as with `GrantQuery`.
  - **The subject is a `WHERE` predicate, never a filter applied in Go**, for
    the same reason M18 puts the tenant in every query. Both methods take the
    tenant first, so `TestRepositoryMethodsTakeATenant` keeps passing.
  - `TestEmptyTenantIsRefusedBeforeAnyQuery` lists its calls by hand. Add both
    methods beside the existing `GrantRequests.*` entries.
- A new forward-only migration indexes
  `grant_requests (tenant, subject_id, requested_at DESC)`. Use the next free
  number: `0010` as this is written (0013 took `0009`), or whatever is next
  when you start.

### 5. Break-glass on the request (`ext/access.go`, `internal/access/workflow.go`)
- Add to `ext.GrantRequest`, beside `RequestedBy`:

  ```go
  // BreakGlass reports that the credential RequestedBy presented was minted
  // on the break-glass path (M7). It is copied from the request Control
  // stored — asserted when that credential was minted, never inferred from a
  // login source or a name — and it describes who raised the request, never
  // Subject. A workflow may treat such a request differently; it learns the
  // fact here or not at all.
  BreakGlass bool
  ```
- `workflowRequest` sets it from `req.RequestedBy.BreakGlass`, which comes from
  the stored request. So a request the poller resubmits after an outage still
  carries it, even though the poller's actor is empty.
- The change is additive, so Enterprise builds unchanged. It is still an `ext`
  change, so say so in your learnings (PROTOCOL §3).

### 6. Every live tenant is polled (`cmd/hoplock-control`, `internal/access`)
- When a proxy opens its event subscription (`GET /v1/proxies/{proxy_id}/events`,
  0009), report its tenant to `access.Service.Track`. That is the tenant its
  credential carries (M22), never one it asserts.
- Wire it in `cmd/hoplock-control`: wrap the `EventStream` handed to the
  south-bound server so that `Subscribe` reports the tenant, then delegates.
  - `internal/httpapi/south` and `internal/revoke` do **not** import
    `internal/access`.
  - The authorize path gains nothing (M5).
- `serve.go` builds the grant service (`buildGrants`) after `south.New` today.
  Build it first: it depends on the bus, the notifier and the emitter, none of
  which depend on the south-bound server.
- `Track` stays as it is: idempotent, cheap, and only ever adding. Nothing lists
  tenants from the store, and no repository gains a method without a tenant.

### 7. The documents
- `ext/access.go`, the `GrantWorkflow` comment. A request reaches `Submit`
  through one of two doors:
  - an administrator's create, which names the holder, so `RequestedBy` is the
    administrator;
  - a requester's own request, where `Subject` and `RequestedBy` are one person.

  Both arrive as the same `GrantRequest`. A workflow tells them apart by
  comparing the two, never by another field.
- `ext/point.go`: `PointGrantWorkflow`'s `Absent` text adds that a requester's
  own request is refused (`grant_workflow_not_registered`), because nothing
  would decide it.
- `docs/PLAN.md`, **revised in place** (PROTOCOL §3):
  - M10's governance paragraph: the requester's door exists only with a
    workflow registered, break-glass reaches the workflow, and decisions are
    applied in every tenant a proxy is live in.
  - §6's RBAC paragraph: the `requester` role, why it holds no `readOnly`, and
    `AccessSelf`, where the subject is resolved from the caller and never
    asserted. Its role list still names five roles; 0013 recorded
    `integration` in M16 only, so name the whole set.
  - §3, wherever it describes the route table or the poller.
  - This phase's §10 row, to match what was delivered.
- No new decision is expected: this phase applies M10, M15 and M18. If you find
  yourself settling something none of them settles, add a new decision with its
  register row in the same PR, and say so in its first line.

## Out of scope
- **A sink the workflow pushes to** (`GrantDecisionSink`, a
  `GrantWorkflow.Start`). The reason is above: `Status` stays the only answer.
- **Approving inside Control.** With no workflow, a requester's own request is
  refused, not queued for a grant administrator. An approval step here would be
  the approval workflow that §11 and M15 put in Enterprise. `grant:approve`
  stays as it is.
- **The approver's side**: an inbox, approve and reject. That is the
  workflow's own (E8). Also out: any route Enterprise would serve behind this
  surface's authentication. `ext` does not offer one, and that gap is separate
  from this request.
- **The caller's own grants** (`GET …/me/grants`), and **withdrawing your own
  grant early**. Neither was asked for, and a request read already returns the
  grant it produced.
- **Console screens** for the self routes. 0020's console is a client of this
  API, and whether a requester's view belongs in Control's console or
  Enterprise's is for those phases to decide. For a UI that hides what a caller
  cannot do, `GET /api/v1/session` already lists `grant:request` per tenant.
- **Polling across nodes** beyond what 0012 already guarantees
  (`Resolve … WHERE state = 'pending'`). Each node polls the tenants it has
  seen, which includes every tenant with a proxy subscribed to that node.
- **`contract/` (M1).** Nothing here touches the wire. If it seems to, that is
  an upstream request to `hoplock/proxy`, not part of this phase.

## Acceptance criteria
Each criterion is a test. The table-driven ones enumerate the route table, as
`TestEveryRegisteredRouteRefusesATenantOutsideTheCallersScope` does, so a self
route added later is covered on the day it is added.

**The door**
- With a fake workflow registered, a principal whose only role is `requester`
  raises a request for themselves and gets `202`. The fake approves.
  - The grant then exists with the requester as holder and creator, and origin
    `workflow`.
  - The next authorize for that subject allows what it denied before.
  - Once the window closes it denies again, with no sweeper having run. This is
    0012's lifecycle, entered through the new door.
- The fake sees `Subject.ID == RequestedBy.ID` from the self door. From the
  administrator's create, it sees the administrator's id as `RequestedBy`.
- With **no** workflow registered:
  - the self create answers `403 grant_workflow_not_registered`;
  - the tenant has no new request row, no new grant and no new audit record;
  - the administrator's direct create still works exactly as before.
- A body naming `subject` gets `400 invalid_request`. A denied self-request gets
  `403 grant_request_denied`, and no grant exists.
- The requester lists and reads its own requests, and cancels one that is
  pending. The cancel is audited and the workflow is told. Cancelling a decided
  request gets `409 grant_request_not_pending`.
- **Subject isolation**, table-driven over every `AccessSelf` route: with two
  requesters A and B in one tenant, B's request is `404` to A on every route
  that names a request, and never appears in A's list.
- **A machine token** holding `requester` gets `403 subject_required` on every
  `AccessSelf` route.
- **A requester reaches nothing tenant-wide**, table-driven over every
  `AccessTenant` route: `403 forbidden` on each, including `POST …/grants` and
  `POST …/grant-requests/{request}/cancel`.
- Tenant isolation:
  `TestEveryRegisteredRouteRefusesATenantOutsideTheCallersScope` and
  `TestEveryRegisteredRouteRefusesAnUnauthenticatedCaller` cover the
  `AccessSelf` routes.
- RBAC: `requester` holds exactly `grant:request`, every permission is still
  held by some role, and the auditor still changes nothing.

**Break-glass**
- A request raised by a break-glass principal reaches the fake workflow with
  `BreakGlass: true` in three cases:
  - through the administrator's create;
  - through the self door;
  - on a resubmission after `Submit` failed with `KindUnavailable`.

  A federated principal's request arrives with `false`.
- The flag is never inferred: a principal with `Source: "local"` and
  `BreakGlass: false` arrives with `false`.

**Every live tenant**
- Set up two tenants, a fake workflow, and a request pending in the tenant that
  is not the configured one.
  - Start a fresh `access.Service`, which simulates a restart: only the
    configured tenant is tracked.
  - A proxy of the other tenant opens its event subscription, and the fake
    approves.
  - Within one poll, the grant exists, though nobody read the request and no
    new request was raised. That subject's next authorize allows.
- Neither `internal/httpapi/south` nor `internal/revoke` imports
  `internal/access`. Assert this beside the import guards in
  `architecture_test.go`.

## Cross-repo impact
This phase answers an upstream request **and** changes `ext/`, so it owes a
downstream sync. The PR that implements it MUST carry a `## Cross-repo impact`
section (`docs/CROSS-REPO-PROTOCOL.md` §4.1) naming **every** consuming
repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5, "The PR that answers an upstream request is
not a sync"). It knows what it asked for. It does not know what was built, and
two of the three shapes differ from the ask. The session there that rewrites its
prompts will be a fresh one that knows nothing.

State at least these obligations:

1. **The requester's door.** Rewrite its 0003's "What Control does not have
   yet" from what merged.
   - The door exists at `…/me/grant-requests`, under `grant:request` (role
     `requester`).
   - It is refused with `403 grant_workflow_not_registered` while no workflow
     is registered, which is what its E4 already requires of an unlicensed
     binary.
   - Enterprise builds no requester's door of its own.
   - Reconcile 0003's "list mine from its own record" with
     `GET …/me/grant-requests`, so the same requests do not have two lists.
2. **Break-glass.** `ext.GrantRequest.BreakGlass` exists. Any override E8
   designs keys on it, and 0003's acceptance criteria test it.
3. **No sink.** A decision is applied within one poll interval in any tenant
   with a live proxy, across restarts.
   - 0003 drops "put the restart gap in your learnings".
   - It proves "approval grants access automatically" in a tenant other than
     the configured one.
   - It builds no push path.
   - Its E2E (its 0017) waits at most one poll interval for Control to apply an
     approval.
4. **Its plan.** Its PLAN names none of the three gaps; they live in 0003.
   But its E8 says Control submits and polls "every create", and its §4
   describes the `GrantWorkflow` seam. Revise both wherever they say how a
   request reaches the workflow, so they name the second door and the
   break-glass flag.

Queue the **"Downstream sync" kickoff** from `docs/KICKOFF.md`, verbatim except
for its blanks — this PR's URL, and the obligations above — as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`, committed
once the PR is open, and end the section by naming that file
(`docs/CROSS-REPO-PROTOCOL.md` §4.3).

### `hoplock/proxy`
Write **"None"** rather than leaving the repository out. The contract (M1) is
untouched, and the proxy consumes neither `ext/` nor the north-bound surface.
"None" is a finding (`docs/CROSS-REPO-PROTOCOL.md` §4.1).

### Who runs the sync
Not you. This PR merges first, and the sync runs afterwards in its own session
(`docs/CROSS-REPO-PROTOCOL.md` §2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0016-self-service-grant-requests-learnings.md`. Enterprise's
sync is written from its summary block, which MUST give:

- the four routes, with their access class and permission;
- the `requester` role, and why it holds no `readOnly`;
- every answer the self create can give, including `403
  grant_workflow_not_registered`;
- `ext.GrantRequest.BreakGlass`, verbatim;
- how the poller now learns a tenant.
