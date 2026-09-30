# 0012 — access grants — Learnings

## Summary
- **What shipped:** grants as policy inputs, end to end — `internal/access`
  (create, list, inspect, revoke; the workflow seam), six north-bound routes,
  revocation that ends the sessions a grant backed, every act audited in its own
  transaction, and Control's outbound webhook notifier (`internal/notify`).
  Migration `0008`. Conformance 36/36 against this server.
- **Grant (`store.Grant`):** subject; scope = `Scope` (the NAME a rule matches
  with `grant.scopes`) + `ScopeTargets`/`ScopeLabels`/`ScopeZones` (selector,
  ANDed, empty = whatever the rule covers); `NotBefore`/`ExpiresAt`; `Origin`
  (`manual`|`workflow`|`external`, rendered `administrator`|… in policy's words);
  `Reason`/`ReasonCode`; `CreatedBy{Subject,Principal,BreakGlass}`;
  `RequestID`/`ApprovalRef`/`Approvers`; `ExternalRef` + `External{System,
  Window*, Additional*}` (0013 fills); `RevokedAt/By/RevokeReason`.
- **States:** `scheduled` · `active` · `expired` · `revoked`. Only `revoked` is
  stored; the other three are `Grant.State(t)`, a function of the instant.
- **Expiry rule:** live ⇔ `not_before ≤ t < expires_at` ∧ not revoked, where `t`
  is the decision's own time input (`ListLive`'s WHERE clause). Nothing runs for
  a grant to expire; there is no sweeper and no config key for one.
- **In a decision record:** `explanation.grant` = the id; `decisions.grant_id`
  (indexed) = the same; `inputs.grants[]` = every live grant WHOLE, one shape for
  every origin. The wire `grant_context` carries `external_ref` (and the system
  only if one asserted it).
- **`ext.GrantWorkflow` contract** (stated on the interface): with one
  registered, every administrator's create is a `grant_requests` row + `Submit`.
  `Approved` → grant (origin `workflow`, window clamped to the request, zero end
  = as asked). `Denied`/`Expired`/`KindDenied` error → **no grant, ever**:
  request closed, audited, API answers 403 `grant_request_denied`; never a fall
  back to direct creation. `Pending` → polled per tenant; expired + `Cancel`ed if
  the window closes first. `KindUnavailable`/unclassified → stays pending,
  resubmitted under the same `RequestID`; other kinds → closed `failed`.
- **`ext` changed (additive):** `GrantRequest.Scope` (`ext.GrantScope`). Owes
  `hoplock/enterprise` a downstream sync — see the PR's `## Cross-repo impact`.
- **Decisions:** none added, amended or withdrawn; M10 and M2 revised in place,
  §3 and §10 too. **Deviation:** the prompt put notifiers out of scope, the `ext`
  catalogue promised the webhook here; the user chose to ship it (Details).
- **NEXT session:** 0013 creates external grants through `internal/access` (its
  prompt now says how); 0014's explain reads the grant out of the record, not
  the grant table.

## Details

The summary runs past PROTOCOL §5's aim because the prompt names four things it
MUST give — the grant and its states, the expiry rule, the record, and the
workflow contract — and Hoplock Enterprise's E8 is written from the last of them.

### Why two tables, and why expiry is not a column

A pending workflow request is not access, and the cheapest way to make sure it is
never read as access is for it to live in a table the decision path does not
name (`grant_requests`). A `state` column on `grants` would have made "pending is
not live" a predicate every future query has to remember. The same reasoning
kept `expired` out of the schema: a column somebody updates is a column a stuck
job leaves saying `active`, and the prompt's point is that a stuck sweeper must
never be standing production access. `TestAGrantAllowsUntilItsWindowClosesWithNoSweeper`
asserts the grant row is unchanged between the last allow and the first deny.

`grant_requests` carries two CHECKs worth knowing: decided ⇔ not pending, and
approved ⇔ names a grant.

### Revocation is two passes, and the second one is not decoration

Order: `UPDATE grants` + the audit record commit together → `cache_invalidate`
for the holder to every proxy → `session_kill` per proxy for the `(proxy_id,
session_id)` pairs of every ALLOWED decision under the grant → wait one decision
budget (+500 ms) → the same again, skipping what was already ended.

The second pass closes a real race: an authorize that read the grant as live a
moment before the revocation committed records its session after the first pass
looked, and would otherwise run until the grant's original expiry.
`TestASessionThatRacesTheRevocationIsEndedByTheSecondPass` injects exactly that.
It runs only when the grant was live at the instant it was revoked. The wait
happens inside the request, so a revoke takes ~2.5 s by default and its answer
lists every session it ended; the second pass is detached from the caller's
context, so a client that hangs up does not leave it half-delivered.

The invalidation goes first so a user who is killed and reconnects at once
reaches this server rather than a decision cached under the grant (M9). Past
`DefaultKillLimit` (5000) sessions, revocation also kills by subject and says
`widened: true`. A second revoke of the same grant keeps the first revocation's
time, revoker and reason, writes no second audit record, and re-publishes — which
is the answer to `503 grant_revocation_undelivered`.

The kill `reason` is the operator's text, verbatim, as the contract's disclosure
rule requires; the API requires it (≤ 512 chars, one line).

### Audit, in the act's transaction

`audit.Emitter.GrantEvent(ctx, tx, …)` appends to the `control` chain inside the
caller's transaction (`Ingester.within(tx)`; `AppendChain` reuses a
transaction-bound store). Kind `policy_decision` — the contract's enum is closed
— with `event` ∈ `grant.created|grant.revoked|grant.requested|grant.request_closed`.
Severity: break-glass actor → `critical`; created/revoked → `warn`; else `info`.
The record's `subject` is the holder; the actor is `principal_id`,
`actor_subject`, `break_glass`, `correlation_id`. Attribute names deliberately
avoid `grant_system`/`grant_reference`/`grant_window_*`: those index a SESSION's
grant context (M16), and an administrative record filed there would answer
"which sessions did this ticket authorise" with something that is not a session.

### The poller, and the one gap M18 forces

A pending request is advanced by `Service.Watch` (every
`grants.workflow_poll_interval`, only when a workflow is registered) and on every
`GET …/grant-requests/{id}`. The store cannot enumerate tenants — every
repository method names its tenant, which is what makes a cross-tenant read
unwritable — so the poller walks the tenants this PROCESS has seen: the
configured `tenant` from start-up, any other from its first request or read.
After a restart, a pending request in a non-default tenant waits for somebody to
read it or for the next request in that tenant. Transitions are guarded by
`Resolve … WHERE state = 'pending'`, so two nodes polling the same workflow
cannot both apply a decision.

### `ext.GrantRequest.Scope` — why `ext` changed

`GrantRequest` had `Targets []ext.Target` and `Privileges []string`, which cannot
express a label or zone selector or a wildcard. An approver shown three hostnames
for a grant that covers every `env=prod` target has approved something else. The
new `Scope` carries the grant's scope exactly as it will be stored; `Targets` is
now documented as the inventory records for the hostnames the scope names
exactly, and `Privileges` as `[Scope.Name]`. Additive, so Enterprise builds
unchanged, but its E8 must read `Scope` to be right — hence the sync.

### The notifier, and the deviation behind it

The prompt lists "notifiers" as Enterprise's. `ext/point.go` (0004) says
Control's core answer at the Notifier seam is "the outbound webhook notifier
(phase 0012)", and 0013's prompt assumes it exists. The user, asked, chose to
ship it here, reading the prompt's "notifiers" as the approval workflow's
Slack/Teams channels.

`internal/notify` is a package of its own rather than part of `access` (PLAN §3
listed notifiers under `access/`, now revised), because the Notifier seam
carries policy and fleet events too. One queue and worker per destination
(webhook + each registered `ext.Notifier`); `Notify` never blocks; retries with
backoff, a 4xx is not retried; redirects are refused; `https`, or `http` to
loopback only; no credentials in the URL. Signature header:
`X-Hoplock-Signature: v1=<hex HMAC-SHA256(key, X-Hoplock-Timestamp + "." + body)>`,
key read from the env var `notify.webhook_secret_env` names. Events are
announced only after their transaction commits.

### Other things that changed shape

- `store.GrantRepository.Revoke` is now `(ctx, tenant, id, GrantRevocation)
  (revoked bool, err)`; `List` and the `GrantRequestRepository` are new;
  `DecisionRepository` gained `ListByGrant` and `SessionsByGrant`.
- `decision.liveGrants`/`grantOrigin` moved to `access.PolicyGrants`/
  `PolicyOrigin`: the package that owns grants owns their translation.
- `inputs.grants[].scope` in a decision record is now an object; a record
  written before 0012 had it as a string (the name).
- `north.Options.Grants` is required.

### Config added

`grants.max_duration` (168h; a longer window is refused naming the limit),
`grants.workflow_poll_interval` (15s), `notify.webhook_url`,
`notify.webhook_secret_env`, `notify.webhook_timeout` (5s). All documented in
`config.example.yaml`.

### Findings, not fixed here

- 0011's identity repositories (`Group`, `RoleBinding`, `Connector`,
  `ClaimMapping`, `FederatedIdentity`, `FlowState`, `NorthPrincipal`, `CAKey`,
  `SSHCertificate`, `SoftwareKey`) are not in `internal/store`'s
  `repositoryInterfaces`, so the empty-tenant and tenant-argument tests do not
  cover them. The next phase that touches the store should add them.
- Creating a grant does not flush caches: a cached allow made before it (≤
  `decision.max_cache_ttl`) grants less, never more, so the gap is a delay, not
  an exposure.
- The console (0016) will want a grant list filtered by scope and a count per
  state; neither was needed here.

### Verification

`go build`, `go vet`, `go test -race ./...` against Postgres 16, golangci-lint
v2.13.2 (0 issues) and `exhaustive-guard`, `license-check`, `contract-check`,
and the conformance suite against this server (36/36). The binary was also run:
a grant created, refused for an `auditor` (403 `forbidden`, `grant:write`),
revoked in 2.5 s, both acts on the `control` chain, `audit-verify` OK.
