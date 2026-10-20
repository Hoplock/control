# 0030 — Inbound actions: Control's action executor, the handler started, and the subject lock and target quarantine it pulls

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/26, under `## Upstream request`,
> item 4, from its request file
> `prompts/upstream/queued/enterprise-PR#26-action-executor.md`, and answered
> here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2).
>
> **Where it sits.** After the shared event plane (0027), bounded lists (0028)
> with every chunk it queues, and host namespaces (0029). Before identity sync
> (0031), which runs next and builds its deactivation beside this phase's lock,
> and before instance identity (0032), the console (0033) and the E2E topology
> (0034). The action-executor and identity-sync renumbering notes in
> `docs/PLAN.md` §10 give one line of reasons for each. On one node it depends only on merged phases: 0009 (the
> event stream), 0012 (grant revocation) and 0019 (the north-bound surface and
> its operator records). It needs 0027 for a kill to reach a proxy whichever
> node holds its stream. If 0028's chunks have renumbered it, the order still
> holds, because it runs after all of them.

## Read first
- `docs/PROTOCOL.md` — session workflow. Read §3 for `ext/` as a
  compatibility promise, for revising the plan in place, for the register, and
  for the downstream hand-over this phase owes. Then §4 and §5.
- `docs/CROSS-REPO-PROTOCOL.md` — **§4.1**, **§4.3**, and **§5** at "The PR
  that answers an upstream request is not a sync". This phase answers a
  request, and it changes what `ext/` does and says. So it owes a downstream
  sync (below).
- `docs/PLAN.md`:
  - **§2**, the register first, then:
    - **M15**, in full, for the three forms a point's answer takes.
      `ActionHandler` is **disabled** when absent, and the justification is
      "somebody else's automation pulling a lever an operator can already
      pull". Today three of the five levers have no operator handle at all
      (below). This phase makes that sentence true.
    - **M9** covers `session_kill`, whose reason is shown to the user and is
      never empty, and `cache_invalidate`. Until 0027 a publish reaches only
      its own node.
    - **M10**: revocation is the one grant state stored, and revoking a grant
      ends the sessions it backed, audited with its actor.
    - **M16**, property 2 (an id is the idempotency key, and a different thing
      under it is a conflict) and its door. This is the precedent for an
      inbound, privileged, third-party path.
    - **M5**: the decision path is stateless, bounded and cheap. A lock and a
      quarantine are read on it.
    - **M3** (the engine's vocabulary is closed) and **M17** (constraints the
      decision service applies on top of what policy answered). Together they
      are why a lock is not a policy input.
    - **M4** (every decision explainable: vague to the user, total to the
      auditor), **M11** (a deny on purpose versus an outage), **M7**
      (break-glass), **M12** and **M18** (tenancy), and **M23** (the
      release).
  - **§3**, "Component responsibilities": `ext`, `internal/revoke`,
    `internal/access`, `internal/decision`, `internal/httpapi/north`,
    `internal/audit`, `internal/store`, `internal/daemon` and
    `internal/extdefault`.
  - **§5.3** (evaluation, and what the layers above the engine decide) and
    **§5.4** (cache hints, which are what a lock and a quarantine must
    invalidate).
  - **§7**: the control stream and its `operator.<noun>.<verb>` records.
  - **§10**: this phase's row and the action-executor renumbering note.
    **§11**: SOAR actions themselves stay Enterprise's.
- `docs/learnings/` — read every summary. Open these in full:
  - **`0004`**: `ActionHandler` and `ActionExecutor` as designed, and the
    catalogue's three forms.
  - **`0009`**: the stream, the kill reason, and what a dropped subscriber
    costs.
  - **`0012`**: revocation's order, the settle pass, and why the first
    revocation's time, actor and reason are the ones that stand.
  - **`0013`** and **`0017`**: the inbound push. They cover idempotency on an
    id, an end kept before its open, how one id's open and end are
    serialised, and why the holder is shown Control's reason rather than a
    vendor's (0017's "What this phase answers", item 2).
  - **`0019`**: the route table, the operator records, `POST …/revocations`
    and the emergency runbook. Read **`0023`** for its verdicts on that
    surface.
  - **`0027`**, **`0028`** with the chunks it queued, and **`0029`**: what
    each changed under the stream, the lists and the namespaces.
- Code. Read it before designing anything:
  - `ext/notify.go`: `ActionKind`, `Action`, `ActionResult`,
    `ActionExecutor`, `ActionHandler` and their doc comments.
  - `ext/point.go` (`PointActionHandler`'s catalogue entry), `ext/errors.go`
    (the kinds) and `ext/registry.go` (`Extensions.ActionHandler`).
  - `internal/daemon/main.go` (`start`, `prepareExtensions`) and
    `internal/daemon/serve.go` (`serve`): where the registry is sealed and
    where the listeners open. Read them for how the other points are wired.
  - `internal/revoke/operator.go` and `internal/revoke/event.go`.
  - `internal/access/revoke.go`, `events.go` and `spec.go`: `Service.Revoke`,
    `Actor`, `DefaultRevokeReason`, `ExternalEndReason`,
    `normalizeRevokeReason` and `MaxRevokeReasonLen`.
  - `internal/decision/inputs.go`, `service.go`, `record.go`, `explain.go` and
    `replay.go`: how the subject and target rows are read, and what the
    record carries.
  - `internal/httpapi/north/routes.go` and `operator.go`;
    `internal/audit/operatorevent.go`; `internal/identity/rbac.go`.
  - `internal/store/types.go`, `subjects.go`, `targets.go` and the latest
    migration.
- The request itself, for the requester's own words. The Enterprise decision it
  turns on is **E7** (integrations are one-way by default, and inbound actions
  are privileged, as amended by M16). The Enterprise phase that consumes this
  one is its 0007. Read either in `hoplock/enterprise` only if a choice here
  turns on it. Both are cited by id and never restated.

## Objective
Let a registered action handler act on this deployment, and let an operator
pull every lever the handler can.

Today `ext` declares the inbound action channel, and nothing in Control runs it:

- nothing calls `ActionHandler.Start`, so a registered handler is listed by
  `GET …/extensions` and never started;
- nothing implements `ActionExecutor`, so a started handler would have nothing
  to act through;
- of the five `ActionKind`s, Control has a mechanism for two. A session is
  ended through the event stream (M9), and a grant is revoked through 0012.
  **Nothing in Control can lock a subject out or quarantine a target.** An
  operator's only lever for either is a policy publish, the emergency
  runbook's ban. A policy publish is not a lever this plan will hand to an
  automated channel.

So `ActionHandler`'s own doc comment says "an operator can do all of it through
the north-bound API and the console", and that is false for three of the five
kinds. M15's justification for the point being disabled when absent rests on
that sentence.

After this phase, Control implements `ext.ActionExecutor` over its own
mechanisms and starts the registered handler. It also has a **subject lock**
and a **target quarantine**: standing denials that an operator places and lifts
on the north-bound API, and that the executor places through the same code.

## The request, in this repository's vocabulary
- *Asked for*, verbatim from enterprise#26, item 4:

  > Control implements `ext.ActionExecutor`. `Execute(ctx, Action)` performs
  > every `ActionKind` `ext` declares — `kill_session`, `lock_subject`,
  > `unlock_subject`, `revoke_grant` and `quarantine_target` — idempotent on
  > `ActionID` and scoped to `Action.Tenant`, through Control's own mechanisms
  > (revocation fan-out M9, grants M10). It refuses `ActionUnspecified`. Each
  > execution is audited on the control stream with the action's `ExternalRef`
  > and the handler's provider. Where Control has no mechanism for a kind yet
  > (a subject lockout, a target quarantine), adding it or refusing that kind
  > with a classified error is Control's call. A silent no-op is never the
  > answer. Control calls the registered `ActionHandler.Start(ctx, exec)` once,
  > after the registry is sealed and before the listeners open, and `Stop` on
  > shutdown, as `ext/notify.go`'s doc comment already says. No `ext` signature
  > changes. With nothing registered, nothing changes.

  In this repository's words: an `ext.ActionExecutor` in a new
  `internal/action` package, which `internal/daemon` hands to the one
  registered `ext.ActionHandler`. Each kind is executed through the same
  service an operator's route calls. The executor is idempotent per tenant on
  `ActionID`, as M16 keys a push on its assertion id. Every execution is
  recorded on the control stream (§7) with the handler as the actor.
- *Until it exists:* a SOC detection cannot end a session, revoke a grant, lock
  a person out or quarantine a host through Enterprise. Enterprise's 0007 builds
  its inbound channel, credentials, scopes, blast-radius cap and lockout
  lifecycle against a fake executor, and skips every end-to-end criterion. A
  customer on Control alone loses nothing today, because the point is
  disabled. But that customer cannot lock a compromised account out, or fence
  off a compromised host, except by publishing policy.

## What this phase answers, and where it differs from the ask
The shape is **met as asked**:

- every kind is executed, and no `ext` signature changes;
- the executor is idempotent on `ActionID` and scoped to the tenant;
- `ActionUnspecified` is refused;
- every execution is audited with the external reference and the provider;
- `Start` and `Stop` are called where the doc comment says.

The request left two calls to Control, and reading this plan changes four
things around the shape. Each item below says why, then what to build.

1. **Lock and quarantine are built, not refused.**
   - *Why.* M15 puts a capability that could sit on either side here. It
     calls this point's absence harmless only because an operator can already
     pull every lever the handler pulls. Refusing `lock_subject` and
     `quarantine_target` would keep that sentence false, and would leave
     Enterprise's lockout lifecycle (its 0007) with nothing to drive. Locking
     a compromised account out is not governance and not scale. It is access
     control.
   - *What to build.* A subject lock and a target quarantine (§3 and §4
     below), each with operator routes, and the executor calling the same
     service. `ActionHandler`'s doc comment then becomes true as written.
2. **A lock is a standing denial applied above the engine, not a policy
   input.**
   - *Why.* A policy input can be matched or ignored by a rule, and a lock a
     policy author can write around is not a lock. M3 keeps the engine's
     vocabulary closed. M17 is the precedent for a constraint the decision
     service applies on top of what policy answered.
   - *What to build.* While a lock stands, every authorize for that subject is
     a deny (`EffectDeny`, never an outage — M11), whatever policy, grants or
     windows say. The record names the lock (M4), `explain` says so, and
     replay reproduces it from the record. The same holds for a quarantine and
     its target. Record this as a new decision in the register, numbered when
     it is written.
3. **`kill_session` takes a session or a subject.**
   - *Why.* `Action` names both `SessionID` and `SubjectID`, and says that
     which one is required depends on the kind. Enterprise's 0007 kills "a
     named session" and "every session for a subject". `revoke.Kill` already
     takes either selector.
   - *What to build.* Exactly one of `SessionID` or `SubjectID`. Both, or
     neither, is `KindInvalid`. The doc comment on `ActionKillSession` says
     so.
4. **An inbound action cannot lock a break-glass subject.**
   - *Why.* M7 keeps a break-glass path so that access survives the failure
     of everything else. A third party able to lock that path out holds a
     denial of service for whoever controls the integration, and Enterprise's
     0007 names the same hazard from its side. An operator, acting
     deliberately, still can.
   - *What to build.* `lock_subject` on a subject whose row is `BreakGlass` is
     refused with `KindDenied` and the `ReasonCode` `break_glass_subject`, and
     the refusal is recorded. `KindDenied`'s doc comment says it is the answer
     rather than the symptom. Add that Control returns it from `Execute` when
     it refuses on purpose.
5. **The holder is shown `ReasonText`, as the seam already promises. It is
   validated first, and Control writes its own text when it is empty.**
   - *Why the 0017 precedent does not apply.* 0017 refused to show a vendor's
     text for two reasons. That text arrives over HTTP from software Control
     knows only by a credential, and `ext.WindowAssertion` never promised it
     would be shown. Here the text crosses code the host compiled into its own
     binary, and `Action.ReasonText`'s doc comment already says it is "what
     the user is told". The host owns which integration may say what (its
     E7).
   - *What to build.* `ReasonText` must pass the kill reason's rules: one
     line, no control characters, at most `access.MaxRevokeReasonLen`. Text
     that fails them is `KindInvalid` on `reason_text`, and is never truncated
     or replaced. When it is empty, the holder is shown a fixed reason Control
     writes. There is one for a killed session, one for a lock and one for a
     revoked grant. None of them is `DefaultRevokeReason`, which says "by an
     administrator" and would be false.
6. **A failed `Start` does not stop Control.**
   - *Why.* An action channel is somebody else's automation. Making access
     depend on it is the outage M11 exists to prevent. A handler is told to
     return promptly, so a `Start` error is a broken registration, not a slow
     broker.
   - *What to build.* The error is logged at `ERROR` with the provider and its
     kind, and the point reports it (§2 below). Control serves, and nothing is
     executed. The doc comment says that a handler whose platform is merely
     unreachable returns nil from `Start` and reconnects on its own
     goroutines.

## In scope

### 1. The executor (`internal/action`, new)
- `action.Executor` implements `ext.ActionExecutor`. `internal/daemon` builds
  one for the registered handler, and the executor knows that handler's
  provider. Nothing else constructs one.
- **Validation, before anything is read or written.**
  - `Tenant` is non-empty, and `ActionID` is non-empty, one line and bounded.
  - `Kind` is one of the five.
  - The action carries exactly the fields its kind needs, and no other
    selector:
    - `kill_session`: one of `SessionID` or `SubjectID`;
    - `lock_subject` and `unlock_subject`: `SubjectID`;
    - `revoke_grant`: `GrantID`;
    - `quarantine_target`: `TargetID`.
  - `ReasonCode` and `ExternalRef` are bounded single lines.

  A failure is an `*ext.Error` of `KindInvalid` naming the field, and it is
  recorded (below).
- **Tenancy (M12, M18).** Every read and write is scoped to `Action.Tenant`.
  An id that exists only in another tenant is not found. The isolation test
  class covers the executor.
- **Idempotency.** It is keyed per tenant on `ActionID`, in a table of its own
  added by the next migration. The table holds the action's kind and selector,
  the first result, when it ran, and the provider.
  - The same `ActionID` with the same action returns the first result. It
    changes nothing and writes no second record.
  - It does re-publish the `session_kill` or `cache_invalidate` the first
    execution owed. A kill is idempotent at the proxy, and this is how a retry
    after `KindUnavailable` gets its kill delivered. 0017's re-push is the
    precedent.
  - The same `ActionID` with a different action is `KindConflict`.
  - Two concurrent calls under one id have one effect. Serialise them the way
    0017 serialises one assertion id.
- **Each kind goes through the service an operator's route calls, never
  beside it:**
  - `kill_session`: `revoke.Operator.Kill` to every subscriber in the tenant,
    by the session or by the subject, with the holder's reason (above).
  - `revoke_grant`: `access.Service.Revoke`, which ends the sessions the grant
    backed. A grant already revoked or expired is nothing to act on.
  - `lock_subject` and `unlock_subject`: the lock (§3).
  - `quarantine_target`: the quarantine (§4). Lifting a quarantine is an
    operator's act only. `ext` declares no kind for it, and adding one was not
    asked.
  - Anything else, `ActionUnspecified` included, is `KindInvalid` on `kind`.
- **`ActionResult`.** `Affected` counts what Control can count, and its doc
  comment says what that is for each kind. A killed session is one Control
  published a kill for, never one it saw close. Zero with a nil error means
  "nothing to act on" (already locked, already unlocked, a grant already
  revoked), with a `ReasonCode` naming which.
- **Errors.**

  | Case | Kind |
  | --- | --- |
  | Malformed | `KindInvalid` |
  | An unknown subject, grant or target | `KindNotFound` |
  | Refused on purpose | `KindDenied`, with a `ReasonCode` |
  | The stream refused a publish | `KindUnavailable`. The state change stands, and the call is safe to retry under the same `ActionID` |
  | A store failure | `KindInternal` |

  Never return a nil error over something that was not done.
- **The actor.** The handler is the actor on every record it writes and every
  grant row it touches: principal `action-handler:<provider>`, with no
  subject. This is the way `localActor` names a command run from the box.
  Every record carries `action_id`, `action_kind`, `external_ref`,
  `reason_code` and the provider.
- **Records.**
  - An execution that applied, or found nothing to act on, writes the record
    the operator's lever writes, with the handler as the actor and the
    attributes above. Those records are `operator.revocation.published`,
    `grant.revoked`, and the lock's and the quarantine's own (below).
  - A refusal writes `action.refused`, with the field or the reason code.
  - A replay writes nothing.

### 2. The handler started (`internal/daemon`, `ext/`)
- With a handler registered, `internal/daemon` calls `Start(ctx, exec)` once.
  It does so after the registry is sealed and before either listener opens,
  with the server's lifetime as the context.
- On shutdown it calls `Stop` before the listeners drain, bounded by the
  shutdown deadline. An `Execute` that arrives once `Stop` has begun is
  `KindUnavailable`.
- A `Start` error is handled as item 6 above says. The point's row in
  `GET …/extensions`, and the start-up log, say the handler is registered and
  **not started**, with the error's kind. Add that to the listing in whatever
  shape 0028 settled for it, and say so in the release's changelog.
- With nothing registered, nothing changes. No executor is built, and the
  tests prove it.
- Revise these `ext` doc comments, and change no signature:
  - `ActionHandler`: what a `Start` error does, and that an unreachable
    platform is not one;
  - `ActionKillSession`: the two selectors;
  - `ActionResult.Affected`: what it counts for each kind;
  - `Action.ReasonText`: validated, never truncated, and Control's text when
    it is empty;
  - `KindDenied`: Control returns it from `Execute` when it refuses on
    purpose;
  - `PointActionHandler`'s catalogue entry, if its `Absent` line no longer
    holds.

  `make release-check` reports no break.

### 3. The subject lock (`internal/store`, a service package, `internal/decision`, `internal/httpapi/north`)
- **The row.** There is one row per tenant and subject id while a lock stands.
  It holds when the lock was placed, by whom (principal, subject and
  break-glass), the reason code, and the external reference and action id if
  an action placed it.
  - The lock is a table of its own, keyed by the subject id. So a login's
    upsert, or a `PUT …/subjects/{subject}`, never clears it, and a lock
    outlives a deleted subject row.
  - Placing one needs a subject Control has a row for. Without one it is
    `KindNotFound`, or `404` on the route.
- **Placing a lock** stores it, then publishes `session_kill` for the subject
  with the holder's reason and `cache_invalidate` for the subject. It records
  `operator.subject.locked`. Grants are not revoked: unlocking restores
  whatever policy allows, and meanwhile a grant expires on its own clock
  (M10).
- **Lifting a lock** deletes the row and records `operator.subject.unlocked`.
  Nothing is published, because a deny is never cached as an allow.
- **On the decision path**, the lock is read in the round trip that reads the
  subject: one query that returns the subject's row, if any, and its lock, if
  any. So a lock holds for a subject id with no row, and M5's budget holds.
  Prove the budget with `internal/decision/bench_test.go`. A locked subject is
  a deny with the reason code `subject_locked`, recorded with the lock (M4),
  whatever policy matched.
- **Routes.** Both sit in Control's namespace, under `session:revoke`:
  - `POST …/subjects/{subject}/lock`, with an optional `reason_code` and
    `reason`;
  - `POST …/subjects/{subject}/unlock`.

  `session:revoke` is the permission whose holders can already end every
  session in the tenant, which is a larger lever than a lock. A lock already
  standing, or no lock to lift, is a `200` that says so. `GET …/subjects` and
  `GET …/subjects/{subject}` show `locked` and the lock's facts, under their
  existing permission. If a different permission is right, choose it and say
  why in the plan. Never declare a new permission silently.

### 4. The target quarantine (same packages)
- **The row** has the same shape, keyed by target id: when, by whom, the
  reason code, the external reference and the action id. Placing one needs a
  target row.
- **Placing a quarantine** stores it and records
  `operator.target.quarantined`. It publishes the `cache_invalidate` that
  reaches every decision cached for that target: by key, if this server can
  name the keys from its records, and otherwise `all` for the tenant.

  Live sessions are not killed, because `ActionQuarantineTarget` "stops new
  access". An operator who wants them ended has `POST …/revocations`.
- **Lifting it** records `operator.target.released`.
- **On the decision path**, the quarantine is read with the target row. A
  quarantined target is a deny with `target_quarantined`, recorded with the
  quarantine.
- **Routes.** Both sit under `session:revoke`:
  - `POST …/targets/{target}/quarantine`;
  - `POST …/targets/{target}/release`.

  `GET …/targets` and `GET …/targets/{target}` show the quarantine.
  `DELETE …/targets/{target}` on a quarantined target is `409
  target_quarantined` until it is released, because deleting the row would
  otherwise lift the quarantine silently.

### 5. The documents
- `docs/PLAN.md`, revised in place:
  - the new decision and its register row;
  - §3's components;
  - §5.3, for what the layers above the engine decide;
  - §7, for the new record kinds and `action.refused`;
  - M15's wording on this point, which is now true;
  - §10's row, revised to what was built.

  §11 is unchanged: SOAR itself stays Enterprise's.
- The console prompt (0033). It shows a lock and a quarantine wherever it
  shows a subject or a target. It offers lock, unlock, quarantine and release
  under the same permission, because Enterprise's 0007 relies on an operator
  releasing a lockout there.
- The E2E prompt (0034). Add one scenario in which an action handler kills a
  session and locks its subject.
- `CHANGELOG.md`: the release this phase cuts, a MINOR (M23).

## Out of scope
- **Enterprise's half** (its 0007, E7):
  - the inbound transport;
  - per-integration credentials and scopes;
  - the blast-radius cap and rate limits;
  - its own records;
  - the lockout lifecycle, including expiry, which Enterprise drives by
    executing `unlock_subject`. `ext.Action` has no expiry, and adding one was
    not asked.
- **A kind to lift a quarantine**, or any other new `ActionKind` or `ext`
  signature.
- **Identity sync's deactivation.** Enterprise queued it separately, as
  `enterprise-PR#26-identity-sync-sink.md`, and it is answered by 0031, which
  runs next. A deactivated subject is not a locked one: the directory places
  and lifts a deactivation, and an operator or a handler places and lifts a
  lock, so neither may lift the other. 0031 reads both on the round trip this
  phase builds and extends this phase's decision to cover both. So build that
  read and the decision's text so a second standing denial on a subject joins
  them without a second query. Do not build the deactivation here.
- **Delivery on several nodes**, beyond what 0027 built.
- **The console screens** (0033).
- **`contract/` (M1).** Nothing on the wire changes: `session_kill`,
  `cache_invalidate` and a deny all exist.
- **Bounding the idempotency table's growth.** Note it in the learnings.

## Acceptance criteria
Each criterion is a test against Postgres with a real revocation bus, in the
packages' own tests and over the wire in `internal/httpapi/north`.

**The executor**
- `kill_session`, by session and by subject, publishes one `session_kill`
  with the holder's reason: `ReasonText` verbatim, or Control's text when it
  is empty. `operator.revocation.published` names the handler, the action id
  and the external reference. `audit-verify` passes.
- `revoke_grant` revokes the grant through 0012's path. Its sessions are
  killed, and `grant.revoked` names the handler. A grant already revoked or
  expired is `Affected: 0` with a reason code, and the grant row is unchanged.
- `lock_subject` then `unlock_subject` round-trips. Access is denied while the
  subject is locked and allowed afterwards, where policy permits. Each act is
  recorded once.
- `quarantine_target` denies new access to the target and leaves a live
  session to it running. An operator's release restores access.
- Each of these is `KindInvalid` naming the field, writes an `action.refused`
  record, and has no effect:
  - `ActionUnspecified` or an unknown kind;
  - a missing or extra selector, or both selectors on a kill;
  - an empty `ActionID` or tenant;
  - a `ReasonText` with a control character, or over the limit.
- An unknown subject, grant or target is `KindNotFound`. So is one in another
  tenant, and nothing in either tenant changes.
- `lock_subject` on a break-glass subject is `KindDenied`,
  `break_glass_subject`. An operator's lock of the same subject succeeds.
- **Idempotency.**
  - The same action twice returns the first result, writes no second record,
    and re-sends the kill.
  - A different action under the same id is `KindConflict`.
  - At least a hundred concurrent pairs, each under a fresh id, each have one
    effect.
- With the stream refusing to publish, the answer is `KindUnavailable` and the
  state change stands. Once the stream recovers, the same `ActionID` delivers
  the kill and returns the first result.

**The handler**
- A fake handler registered through `server.Options.Registry` is started
  once, after the registry is sealed and before a listener accepts a
  connection. It receives a working executor, and it is stopped on shutdown.
  An `Execute` after `Stop` began is `KindUnavailable`.
- With a handler whose `Start` fails, Control serves and logs the failure, and
  `GET …/extensions` shows the handler registered and not started.
- With no handler registered, no executor exists, and the existing extension
  tests pass unchanged.

**The lock and the quarantine**
- A locked subject's authorize is a deny with `subject_locked`, whatever a
  policy rule, a grant or an external window says. The record names the lock,
  `explain` says so, and replaying that record reproduces the deny.
- Placing a lock invalidates a cached allow for the subject, and kills a
  session the subject holds, with the reason.
- A login, a `PUT …/subjects/{subject}`, and a `DELETE` followed by
  re-creating the subject each leave the lock standing.
- The quarantine's equivalents hold, including the `cache_invalidate` reaching
  a cached allow for the target, and `409 target_quarantined` on delete.
- The four routes:
  - their permission is enforced, role by role;
  - each write is recorded once, with the operator as actor;
  - a lock already standing, or a release with nothing to lift, is a `200`
    that says so.
- The decision benchmark shows no extra round trip.

## Cross-repo impact
This phase answers an upstream request and changes what `ext/` does, so it
owes a downstream sync. Its PR MUST carry `## Cross-repo impact`
(`docs/CROSS-REPO-PROTOCOL.md` §4.1), naming **every** consuming repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5). It knows what it asked for, but not what
was built, and the session there that rewrites its prompts will be a fresh one.
State at least these obligations:

1. **Its 0007, "What Control does not do yet".** Rewrite it from what merged,
   and stop building around the gap. It needs:
   - each kind, and what it does;
   - the two selectors on a kill;
   - the break-glass refusal;
   - the error kinds, and which are safe to retry under the same `ActionID`;
   - `ReasonText`'s rules, and Control's texts when it is empty;
   - `Affected` for each kind;
   - the lock and the quarantine, their records, and that an operator lifts
     either on Control's routes and console;
   - that the handler is the actor (`action-handler:<provider>`);
   - what a `Start` error does.

   Turn its skipped end-to-end criteria into real ones.
2. **Its lockout lifecycle.** A lock stands until `unlock_subject` or an
   operator lifts it, and expiry is Enterprise's to drive. If Enterprise needs
   a lock that expires inside Control, that is a new request.
3. **Its plan.**
   - §4's `ActionHandler` row, and its misreadings.
   - Wherever E7 says what an inbound action may do.
   - The permission an operator's lock and quarantine sit under, for its
     0013's estate actions.
4. **Its pin.** Every obligation holds from the release this PR cuts (M23),
   never from `main`. Name that release by version.

Queue the **"Downstream sync" kickoff** from `docs/KICKOFF.md`, verbatim except
for its blanks, as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`. Commit it
once the PR is open, and end the section by naming that file
(`docs/CROSS-REPO-PROTOCOL.md` §4.3).

### `hoplock/proxy`
Write **"None"**. The contract is untouched (M1), and the proxy consumes
neither `ext/` nor the north-bound surface. "None" is a finding.

### Who runs the sync
Not you. This PR merges first, and the sync runs afterwards in its own session
(`docs/CROSS-REPO-PROTOCOL.md` §2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0030-action-executor-lock-and-quarantine-learnings.md`.
Enterprise's sync is written from its summary block, which MUST give:

- the kinds, the selectors each takes, and what `Affected` counts for each;
- the error kinds, which of them are safe to retry, and the break-glass
  refusal;
- `ReasonText`'s rules, and Control's texts verbatim;
- the lock's and the quarantine's routes, permission and records, and the
  handler's actor;
- what a `Start` error does, and where it shows;
- the new decision's id, in one line.
