# 0031 — Identity sync: Control's sync sink, the sync started, and de-provisioning that takes effect

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/26, under `## Upstream request`,
> item 3, from its request file
> `prompts/upstream/queued/enterprise-PR#26-identity-sync-sink.md`, and
> answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2).
>
> **Where it sits.** Right after the action executor (0030), and after
> everything 0030 runs after: the shared event plane (0027), bounded lists
> (0028) with every chunk it queues, and host namespaces (0029). Before
> instance identity (0033), the console (0034) and the E2E topology (0035).
> The identity-sync renumbering note in `docs/PLAN.md` §10 gives one line of
> reasons for each. It needs 0030's subject read and standing-denial decision,
> and 0027 for a deactivation's kill to reach a proxy whichever node holds its
> stream. If 0028's chunks have renumbered it, the order still holds, because
> it runs after all of them.

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
    - **M15**, in full. `IdentitySync` is **disabled** when absent, and its
      doc comment calls what it adds "an identity that exists before anyone
      uses it" and "de-provisioning that takes effect when someone leaves".
      Neither happens today, with or without a registration.
    - **M7**: identity is federated, and IdP claims and groups reach policy
      only through the explicit, versioned claim mapping. A directory is a
      second door, and this decision governs it as well.
    - **The decision 0030 added** for standing denials applied above the
      engine (find it in the register). A deactivation is a second such
      denial, and this phase extends that decision rather than writing a
      parallel one.
    - **M9** (`session_kill`, whose reason the holder is shown, and
      `cache_invalidate`) and **M10** (revocation is the one stored grant
      state, and revoking a grant ends the sessions it backed).
    - **M16**, property 2 (an id is the idempotency key), and **M5** (the
      decision path is stateless, bounded and cheap; a deactivation is read on
      it).
    - **M3** and **M17**, which are why a deactivation is not a policy input.
    - **M4** (every decision explainable), **M11** (a deny on purpose versus
      an outage), **M8** (records on the control stream), **M12** and **M18**
      (tenancy), and **M23** (the release).
  - **§3**, "Component responsibilities": `ext`, `internal/identity`,
    `internal/access`, `internal/revoke`, `internal/decision`,
    `internal/httpapi/north`, `internal/audit`, `internal/store`,
    `internal/daemon` and `internal/extdefault`.
  - **§5.3** (what the layers above the engine decide) and **§5.4** (cache
    hints, which a deactivation must invalidate).
  - **§6**, in full: federation and its join key, the claim mapping as an
    allow-list, users, groups and RBAC, and break-glass.
  - **§7**: the control stream and its records.
  - **§10**: this phase's row and the identity-sync renumbering note. **§11**:
    SCIM itself stays Enterprise's.
- `docs/learnings/` — read every summary. Open these in full:
  - **`0004`**: `IdentitySync` and `SyncSink` as designed, and the
    catalogue's three forms.
  - **`0011`**: federation, the subject id `<connector>:<subject>` and the
    link behind it, the claim mapping, groups and the north-bound
    credentials.
  - **`0009`** and **`0012`**: the stream, the kill reason, revocation's
    order and why the first revocation's actor and reason stand.
  - **`0016`**: a requester's pending requests, and how one is withdrawn.
  - **`0017`**: an end kept before its open. A deactivation that arrives
    before the identity it ends is the same problem.
  - **`0019`** and **`0023`**: the subject routes, the operator records and
    the route table.
  - **`0027`**, **`0028`** with the chunks it queued, **`0029`** and
    **`0030`**: the stream on several nodes, the list shapes, the namespaces,
    and the lock, the executor and how a started handler is shown.
- Code. Read it before designing anything:
  - `ext/identity.go`: `SyncedSubject`, `SyncedGroup`, `SyncSink`,
    `IdentitySync` and their doc comments. Also `ext/point.go`
    (`PointIdentitySync`'s entry), `ext/registry.go`
    (`Extensions.IdentitySync`), `ext/errors.go` and `ext/seam_test.go`.
  - `internal/daemon/main.go` and `internal/daemon/serve.go`: where the
    registry is sealed and the listeners open, and how 0030 starts the action
    handler.
  - `internal/identity/federation.go` (`Complete`, `provision`,
    `principalsFor`, `LocalLogin`, `Authenticate`) and
    `internal/identity/mapping.go` (`Assertion`, `Apply`).
  - `internal/store/subjects.go`, `internal/store/identity.go` (groups,
    `federated_identities`, north-bound principals, SSH certificates), and
    migrations `0001`, `0007` and the latest.
  - `internal/access/revoke.go` and `internal/access/workflow.go`
    (`Service.Revoke`, `Service.Cancel`), and `internal/revoke/operator.go`.
  - `internal/decision/inputs.go`, and the subject-and-lock read 0030 added.
  - `internal/httpapi/north/inventory.go` (the subject routes) and
    `internal/audit/operatorevent.go`.
- The request itself, for the requester's own words. The Enterprise decisions
  it turns on are **E4** (every feature degrades honestly) and **E11** (the
  tenancy mechanism is Control's, the governance Enterprise's). The phase that
  consumes this one is its 0004. Read either in `hoplock/enterprise` only if a
  choice here turns on it. Both are cited by id and never restated.

## Objective
Let a registered identity sync provision and de-provision this deployment's
subjects, and make a de-provisioned person lose their access when the
directory says so.

Today `ext` declares the provisioning seam, and nothing in Control runs it:

- nothing calls `IdentitySync.Start`, so a registered sync is listed by
  `GET …/extensions` and never started;
- nothing implements `SyncSink`, so a started sync would have nothing to apply
  what it learned through;
- `subjects` has no state for "no longer live", so nothing could make a
  subject's next authorization a deny, whatever policy says;
- a federated login creates its own subject, keyed `<connector>:<subject>`,
  and nothing joins it to a directory's record. So a sink built on its own
  would provision one subject while the person logged in as another, and
  de-provisioning the first would leave the second's access standing.

After this phase, Control implements `ext.SyncSink` over its identity store,
starts the registered sync, and treats a deactivation as a standing denial
that only the directory lifts. A login through a connector tied to the
directory reaches the subject the directory provisioned.

## The request, in this repository's vocabulary
- *Asked for*, verbatim from enterprise#26, item 3:

  > Control implements `ext.SyncSink` over its identity store, per tenant,
  > with every method idempotent on `ExternalID` and an out-of-order write
  > resolved by `UpdatedAt`:
  > - `UpsertSubject` creates or updates a subject, joined on `ExternalID`
  >   and never on the username;
  > - `DeactivateSubject` marks it inactive and revokes what it holds — its
  >   grants and its live sessions, through Control's own revocation (M9,
  >   M10) — so its next authorization is denied and its audit history stays
  >   attributable;
  > - `UpsertGroup` and `DeleteGroup` maintain groups, and a deleted group's
  >   derived membership stops applying.
  >
  > Each call is audited on the tenant's control stream with the provider
  > that made it. Control calls the registered `IdentitySync.Start(ctx, sink)`
  > once, after the registry is sealed and before the listeners open, with
  > the server's lifetime as the context, and `Stop` on shutdown. That is what
  > `ext/identity.go`'s doc comment already says. No `ext` signature changes.
  > With nothing registered, nothing changes.

  In this repository's words: an `ext.SyncSink` in a new `internal/provision`
  package, which `internal/daemon` hands to the one registered
  `ext.IdentitySync`. A provisioned subject is an ordinary row in `subjects`,
  joined to the directory's `ExternalID` by a link row the way
  `federated_identities` joins an IdP's subject. A deactivation is a standing
  denial read on the decision path beside 0030's lock. Every applied call is
  recorded on the control stream (§7) with the sync as the actor.
- *Until it exists:* SCIM provisions nobody. Enterprise's 0004 builds its SCIM
  server, multi-IdP trust model, claim rules and reconciliation against a fake
  sink, and skips every end-to-end criterion. A person disabled in the
  customer's IdP keeps their access, and their live sessions, until their next
  failed login. A customer on Control alone loses nothing today, because the
  point is disabled: identities arrive at login, as M15 says.

## What this phase answers, and where it differs from the ask
The shape is **met as asked**:

- `SyncSink` is implemented per tenant, every method idempotent on
  `ExternalID`, and an out-of-order write resolved by `UpdatedAt`;
- a subject is joined on `ExternalID` and never on the username;
- a deactivation revokes what the subject holds, denies its next
  authorization, and deletes nothing;
- a deleted group's membership stops applying;
- every applied call is audited with the provider;
- `Start` and `Stop` are called where the doc comment says, and no `ext`
  signature changes.

The request left several calls to Control, and reading this plan adds three
things the requester could not see. Each item below says why, then what to
build.

1. **A login reaches the subject the directory provisioned.**
   - *Why.* Federation keys a subject by `<connector>:<subject>` and creates
     one at first login (§6, `Federation.provision`). Nothing in the request
     joins that subject to the directory's. Without a join, the person has two
     subjects. The directory de-provisions one, and the person keeps signing
     in, and keeps their grants, as the other. That is the failure the request
     exists to prevent, and the requester could not see it because the join
     is Control's internals.
   - *What to build.* A connector may name a **`directory_claim`**: the claim
     in its assertions that carries the directory's `ExternalID` for the same
     person (`sub` for some IdPs, an object-id claim for others). It is
     validated when the connector is written. A login through such a
     connector resolves to the subject provisioned under that `ExternalID`,
     and links the federated identity to it. A login with no provisioned
     match is refused with `federation.not_provisioned`, and nothing is
     created, because a just-in-time subject beside the directory's would be
     the second identity that de-provisioning never reaches. A connector
     without the claim works as it does today.
2. **The directory is the only writer of what it provisions.**
   - *Why.* A provisioned subject's groups, attributes and logins come from
     the directory. If a login or an operator's `PUT` also rewrote them,
     whichever wrote last would decide access, and a group the directory
     removed would come back with the next token that still carried it.
   - *What to build.* A login through a linked connector authenticates the
     provisioned subject and rewrites none of its groups, attributes or
     principals. `PUT …/subjects/{subject}` and `DELETE …/subjects/{subject}`
     on a directory-managed subject answer `409 subject_directory_managed`.
     An operator's levers are still there: the lock (0030), role bindings and
     local group membership. Local membership merges into `subjects.groups`
     as it does for a federated subject.
3. **The claim mapping governs the directory door too (M7).**
   - *Why.* Whoever can set a field or a group in the customer's directory
     must not grant themselves a policy attribute or group without anyone here
     deciding to trust it. That is what the mapping is for, and §6 makes it
     the one place an IdP's vocabulary becomes this server's. Enterprise's
     claim rules (its 0004) transform what the directory holds, and the
     tenant's mapping then decides what of it counts.
   - *What to build.* A synced subject's `Attributes`, with `Email` offered
     as the claim `email`, are offered to the tenant's active mapping as
     claims. Its groups are resolved from `ExternalID` to the directory's
     group `Name` and offered under the claim the mapping's `groups.claim`
     names. So one `groups.map` serves login and directory alike. Only what
     the mapping names becomes an attribute or a group, and the subject row
     records the mapping version (M4). A renamed directory group maps nothing
     until the mapping names the new name, so a rename narrows access and
     never widens it. A tenant whose tokens carry group ids maps both
     spellings onto one group. A new mapping version reaches a provisioned
     subject at its next applied write, as it reaches a federated subject at
     its next login.
4. **A deactivation is a standing denial of its own, not 0030's lock.**
   - *Why.* The directory places and lifts a deactivation. An operator or an
     action handler places and lifts a lock. If they were one state, an
     operator's unlock would re-admit a person the directory had
     de-provisioned, and the directory's re-activation would lift a SOC's
     lock. Both can stand at once.
   - *What to build.* Store the deactivation on the subject's link row, never
     on the `subjects` row, so a login's upsert never clears it. It records
     when it was applied and its reason code. The decision path reads it in
     the round trip 0030 built for the subject and its lock, so M5's budget
     holds. A deactivated subject's authorize is a deny with the reason code
     `subject_deactivated` (`EffectDeny`, never an outage, M11), whatever
     policy, grants or windows say. The record names it (M4), `explain` says
     so, and replay reproduces it. A subject both locked and deactivated is
     recorded with both. Extend 0030's decision to cover it, and update its
     register row.
5. **Order is decided without trusting either clock to widen access.**
   - *Why.* `DeactivateSubject` carries no `UpdatedAt`, so Control's own
     clock is the only one that can order it. A late re-delivery of an update
     written before the deactivation must not re-activate the person. And a
     deactivation can arrive before the create it ends, which is 0017's
     problem of an end kept before its open.
   - *What to build.* Per link, Control keeps the last applied `UpdatedAt`.
     - An `UpsertSubject` whose `UpdatedAt` is not later than it changes
       nothing and writes no record. That is a replay or a stale write.
     - `Active: false` on an upsert is a deactivation, with exactly
       `DeactivateSubject`'s effect.
     - `Active: true` lifts a standing deactivation only when its `UpdatedAt`
       is later than both the last applied one and the moment Control applied
       the deactivation. A directory clock behind Control's delays a
       re-activation, and never shortens a deactivation.
     - `DeactivateSubject` for an `ExternalID` with no link keeps the
       deactivation on a link of its own and returns nil. The create that
       arrives later provisions the subject already deactivated.
     - `DeleteGroup` keeps a tombstone in the same way. An `UpsertGroup`
       older than the tombstone changes nothing.
6. **De-provisioning revokes everything the subject holds, through the
   services an operator's levers call, and a retry finishes it.**
   - *Why.* "Revokes what it currently holds" has to mean every way back in.
     A deactivation that ended the SSH sessions but left a console session, an
     API token or a pending grant request standing would leave a path the
     person could still use.
   - *What to build.* In this order:
     1. Commit the deactivation and its record. From here the next authorize
        is a deny.
     2. Revoke every live grant the subject holds through
        `access.Service.Revoke`, which ends the sessions each grant backed.
     3. Withdraw its pending grant requests through 0016's cancel path, so a
        workflow's later approval creates nothing.
     4. Revoke its north-bound sessions and API tokens, and mark its
        outstanding SSH certificates revoked.
     5. Publish `session_kill` for the subject and `cache_invalidate` for the
        subject, through `revoke.Operator`.

     The holder is shown a fixed text Control writes, saying the account was
     deactivated by the organisation's directory. It is never the directory's
     `reasonCode`, which is a code and is recorded, and never
     `DefaultRevokeReason`, which says "by an administrator" and would be
     false. A failure after step 1 returns `KindUnavailable` or
     `KindInternal`, and the deactivation stands. A `DeactivateSubject` for a
     subject already deactivated writes no second deactivation record, and it
     still revokes whatever the subject still holds and re-publishes the kill.
     So a retry under the same `ExternalID` completes it, as 0030's executor
     re-sends a kill. A re-activation restores nothing that was revoked
     (M10).
7. **Logins are unique to one subject.**
   - *Why.* The SSH path resolves a subject by the login name it presents
     (`Subjects.GetByPrincipal`). If a provisioned subject and another subject
     both present `alice`, either can answer, and a directory rename that
     hands `alice` to a new hire hands them the old subject's access.
   - *What to build.* A provisioned subject's principals are exactly the
     directory's `Username`. They are replaced on each applied upsert, never
     accumulated. An upsert whose username another subject in the tenant
     already presents is `KindConflict`, recorded and applied to nothing. An
     existing subject is never adopted or merged silently. A federated login
     through an unlinked connector that would add a login a directory-managed
     subject presents is refused with `federation.login_refused`. Uniqueness
     between subjects the directory does not manage is earlier behaviour this
     phase does not change. Note it in the learnings.
8. **A failed `Start` does not stop Control.** 0030's item 6, for the same
   reason: provisioning is somebody else's software, and making access depend
   on it is the outage M11 exists to prevent.

## In scope

### 1. The sink (`internal/provision`, new)
- `provision.Sink` implements `ext.SyncSink`. `internal/daemon` builds one for
  the registered sync, and the sink knows that sync's provider. Nothing else
  constructs one.
- **Validation, before anything is read or written.** `Tenant` and
  `ExternalID` are non-empty, single-line and bounded. A `Username`, a group
  `Name`, a `reasonCode` and an attribute code are bounded single lines. A
  failure is an `*ext.Error` of `KindInvalid` naming the field, and it is
  recorded (below).
- **Tenancy (M12, M18).** Every read and write is scoped to the call's tenant.
  An `ExternalID` linked only in another tenant is not linked. The isolation
  test class covers the sink. Control keeps no registry of tenants, so the
  sink accepts any tenant the store accepts, as every other door does. Which
  tenants a directory may provision is the host's (its E11).
- **The link.** A table of its own, added by the next migration, keyed per
  tenant on `ExternalID`. It holds the subject id, the provider, the last
  applied `UpdatedAt`, the directory's group ids for the subject, and the
  deactivation: when it was applied and its reason code. An index serves the
  decision path's read by subject id. `ExternalID` is unique per tenant across
  everything the one registered sync provisions. A host that feeds several
  directories into one sink namespaces their ids itself (its collision rule).
- **The subject.** Control allocates the subject id when it first links an
  `ExternalID`. The link row, not the id's spelling, is the join. The id can
  never collide with a federated `<connector>:<subject>` id or a local one, and
  a test registers a connector whose name is the id's prefix to prove it.
  `Source` names the provider. `DisplayName` is the directory's. Principals,
  groups and claims follow items 3 and 7 above. A provisioned subject is
  never break-glass.
- **Groups.** The directory's groups are a table of their own: per tenant and
  `ExternalID`, the `Name`, the last applied `UpdatedAt` and any tombstone.
  They are the directory's vocabulary and never policy's. Policy sees only
  what the mapping makes of them (item 3). An applied `UpsertGroup` that
  renames a group, and a `DeleteGroup`, re-derive `subjects.groups` for every
  subject the directory says is a member. They do this in the same
  transaction, or in bounded batches that leave no member half-applied.
- **What an applied write publishes.** An applied upsert that changes a
  subject's groups, claims or principals publishes `cache_invalidate` for the
  subject, so a narrowing takes effect now rather than at a cached allow's
  expiry. It kills no session. A deactivation kills them (item 6).
- **Concurrency.** The sink is safe for concurrent use, and its doc comment
  says so. Two calls on one `ExternalID` are serialised the way 0017
  serialises one assertion id. Calls on different ids run concurrently.
- **Errors.**

  | Case | Kind |
  | --- | --- |
  | Malformed | `KindInvalid` |
  | A username another subject presents | `KindConflict` |
  | A revocation or publish failed after the state change committed | `KindUnavailable`. The state stands, and the same call is safe to retry |
  | A store failure | `KindInternal` |
  | A call after `Stop` began | `KindUnavailable` |

  Unknown ids are not errors, by item 5: deactivating or deleting what has no
  link keeps the deactivation or the tombstone. Never return a nil
  error over something that was not done.
- **The actor.** The sync is the actor on every record it writes and on every
  grant, request and credential it revokes: principal
  `identity-sync:<provider>`, with no subject, in the way 0030 names
  `action-handler:<provider>`.
- **Records**, on the tenant's control stream, each in the transaction that
  makes the change. They carry the `ExternalID`, the subject id and the
  provider:
  - `identity_sync.subject_provisioned` and `identity_sync.subject_updated`,
    with the before and after of groups, claims and principals and the
    mapping version;
  - `identity_sync.subject_deactivated` with the reason code, and
    `identity_sync.subject_reactivated`;
  - `identity_sync.group_upserted` and `identity_sync.group_deleted`, with the
    number of members re-derived;
  - `identity_sync.refused`, with the field or the conflict.

  The revocations a deactivation makes write the records their own services
  write (`grant.revoked`, `grant.request_closed`,
  `operator.revocation.published`), with the sync as the actor. A replay or a
  stale write writes nothing.

### 2. The sync started (`internal/daemon`, `ext/`)
- With a sync registered, `internal/daemon` calls `Start(ctx, sink)` once. It
  does so after the registry is sealed and before either listener opens, with
  the server's lifetime as the context. On shutdown it calls `Stop` before the
  listeners drain, bounded by the shutdown deadline.
- A `Start` error is logged at `ERROR` with the provider and its kind, and
  Control serves. The point's row in `GET …/extensions`, and the start-up log,
  say the sync is registered and **not started**, in the shape 0030 settled
  for the action handler.
- With nothing registered, nothing changes. No sink is built, no connector may
  name a `directory_claim` it could never match (writing one is refused with
  `400` saying no identity sync is registered), and the tests prove it.
- Revise these `ext` doc comments, and change no signature:
  - `SyncSink`: safe for concurrent use; ordering by `UpdatedAt` and by
    Control's clock for a deactivation (item 5); a deactivation or a
    tombstone kept for an unknown id; the username conflict; the error kinds
    and which are safe to retry; and that what the directory holds reaches
    policy only through the tenant's claim mapping;
  - `SyncSink.DeactivateSubject`: what "revokes what it currently holds"
    covers (item 6);
  - `SyncedSubject.ExternalID`: unique per tenant across everything the sync
    provisions;
  - `SyncedSubject.Groups`: group `ExternalID`s, resolved to names by
    `UpsertGroup`;
  - `IdentitySync`: what a `Start` error does, and that a directory that is
    merely unreachable is not one;
  - `PointIdentitySync`'s catalogue entry and `ext/README.md`'s row, if their
    wording no longer holds.

  `make release-check` reports no break.

### 3. Federation (`internal/identity`)
- A connector's `directory_claim`, stored and validated as item 1 says.
- `Federation.Complete` through a linked connector resolves the provisioned
  subject by the claim's value, refuses `federation.not_provisioned` when
  there is none, and links the federated identity to the provisioned subject.
  It rewrites none of its groups, attributes or principals. A link to a
  different subject is refused, as today.
- A login that resolves to a deactivated subject, through any connector or
  `LocalLogin`, is refused with `federation.subject_deactivated` and recorded.
  `Federation.Authenticate` refuses a north-bound credential whose subject is
  deactivated, so a credential minted the instant before the deactivation
  committed is no way back in.

### 4. The decision path and the north-bound surface
- `internal/decision` reads the deactivation with the subject and its lock, in
  one round trip, and denies as item 4 says. Prove the budget with
  `internal/decision/bench_test.go`.
- `GET …/subjects` and `GET …/subjects/{subject}` show whether the directory
  manages the subject (its provider and `ExternalID`) and any standing
  deactivation (when, and the reason code). They do so under their existing
  permission, in the shape 0028's chunks settled.
- `PUT` and `DELETE` on a directory-managed subject answer
  `409 subject_directory_managed`. Declare the code in Control's table.
- The connector routes accept and show `directory_claim`.
- This phase adds no route, so 0029's namespaces move nothing of it.

### 5. The documents
- `docs/PLAN.md`, revised in place:
  - the extended decision and its register row, and M7's row if the new text
    amends it;
  - §3's components, `internal/provision` among them;
  - §5.3, for the second standing denial;
  - §6, for the directory door, the linked connector, and the directory as
    the only writer of what it provisions;
  - §7, for the new record kinds;
  - M15's wording on this point, which is now true;
  - §10's row, revised to what was built.

  §11 is unchanged: SCIM itself stays Enterprise's.
- The console prompt (0034). It shows a directory-managed subject and a
  deactivation wherever it shows a subject. It offers no edit or delete of a
  directory-managed subject, and it shows a connector's `directory_claim`.
- The E2E prompt (0035). Add one scenario in which a fake sync deactivates a
  subject with a live session: the session is killed, the next authorize is
  denied, and the console login is refused.
- `CHANGELOG.md`: the release this phase cuts, a MINOR with no
  `### Breaking` (M23). Its entry names the new concurrency contract on
  `SyncSink`, the connector field and the new codes.

## Out of scope
- **Enterprise's half** (its 0004): the SCIM server, its authentication and
  rate limits, the multi-IdP trust model and its collision rule, the claim
  rules, and reconciliation.
- **Naming an Enterprise claim rule in Control's records.** A decision record
  names Control's mapping version. `SyncedSubject` carries nothing of
  Enterprise's rules, and adding a field was not asked. If Enterprise's
  0004 needs its rule in `explain`, that is a new request.
- **Adopting a subject that existed before the directory provisioned it.** It
  is refused as a conflict (item 7), and an operator resolves it. Merging two
  identities silently is what both repositories rule out.
- **Principal uniqueness between subjects the directory does not manage.**
- **An operator lever that lifts a deactivation.** Only the directory does.
- **Any new `ext` method or signature.**
- **Delivery on several nodes**, beyond what 0027 built.
- **The console screens** (0034).
- **`contract/` (M1).** Nothing on the wire changes: `session_kill`,
  `cache_invalidate` and a deny all exist.

## Acceptance criteria
Each criterion is a test against Postgres with a real revocation bus, in the
packages' own tests and over the wire in `internal/httpapi/north`.

**The sink**
- `UpsertSubject` provisions a subject joined on `ExternalID`. Renaming the
  username in a later upsert keeps the same subject, replaces its principal,
  and the old name resolves to nobody.
- Idempotency and order:
  - the same upsert twice applies once and writes one record;
  - an upsert older than the last applied one changes nothing;
  - at least a hundred concurrent upserts across fresh ids each apply once.
- An attribute or group the tenant's mapping does not name reaches neither
  the subject row nor a decision. One the mapping names does. A synced
  attribute named `chain_hop_proxy_id`, or anything under `hoplock.`, is
  never stored.
- `DeactivateSubject` on a subject with a live grant, a live session, a
  pending grant request, a north-bound session, an API token and an
  outstanding certificate leaves all of them revoked. The session is killed
  with Control's text. The next authorize is a deny with
  `subject_deactivated`, whatever policy allows. The audit history is intact,
  and `audit-verify` passes.
- With the stream refusing to publish, `DeactivateSubject` is
  `KindUnavailable` and the deny already holds. Once the stream recovers, the
  same call delivers the kill, writes no second deactivation record, and
  returns nil.
- A deactivation for an unknown `ExternalID`, followed by its create,
  provisions the subject deactivated. A re-delivered upsert written before a
  deactivation does not re-activate. A later `Active: true` does, and restores
  no revoked grant.
- A locked and deactivated subject stays denied after either is lifted alone.
- `DeleteGroup` removes the derived group from every member's
  `subjects.groups` and invalidates their cached decisions. An older
  `UpsertGroup` after it changes nothing.
- A username another subject presents is `KindConflict`, recorded, and
  applied to nothing.
- Each malformed case is `KindInvalid` naming the field, is recorded, and has
  no effect.
- Two tenants: a sync call in one reaches nothing in the other.

**Federation**
- A login through a linked connector reaches the provisioned subject, links
  it, and rewrites none of its groups, attributes or principals. With no
  provisioned match, it is refused with `federation.not_provisioned` and
  nothing is created. An unlinked connector behaves as before.
- A deactivated subject's login is refused through every door, and an
  existing north-bound credential of theirs stops authenticating.

**The sync and the surface**
- A fake sync registered through `server.Options.Registry` is started once,
  after the registry is sealed and before a listener accepts a connection. It
  receives a working sink and is stopped on shutdown. A call after `Stop`
  began is `KindUnavailable`.
- A sync whose `Start` fails leaves Control serving, and `GET …/extensions`
  shows it registered and not started.
- With no sync registered, no sink exists, `directory_claim` is refused, and
  the existing identity tests pass unchanged.
- `PUT` and `DELETE` on a directory-managed subject are
  `409 subject_directory_managed`. The subject reads show the provider, the
  `ExternalID` and the deactivation.
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

1. **Its 0004, "What Control does not do yet".** Rewrite it from what merged,
   and stop building around the gap. It needs:
   - each method and its ordering rules, including the deactivation kept for
     an unknown id and Control's clock on a re-activation;
   - what a deactivation revokes, the holder's text, and that a retry
     finishes it;
   - the error kinds and which are safe to retry;
   - the username conflict, and that an existing subject is never adopted;
   - that `ExternalID` is unique per tenant across every directory it feeds
     in, so its multi-IdP collision rule namespaces them;
   - that the tenant's claim mapping decides what of its claim rules' output
     reaches policy, and that a group is matched by its directory `Name`;
   - `directory_claim` on a connector, and `federation.not_provisioned`;
   - `409 subject_directory_managed`, and that the sync is the actor
     (`identity-sync:<provider>`);
   - what a `Start` error does.

   Turn its skipped end-to-end criteria into real ones.
2. **Its criterion that `explain` names a claim rule.** Control's record names
   Control's mapping version only. If Enterprise still needs its rule there,
   that is a new request.
3. **Its plan.** §4's `IdentitySync` row and its misreadings, and wherever
   E4 and E11 say what provisioning does.
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
`docs/learnings/0031-identity-sync-and-deprovisioning-learnings.md`.
Enterprise's sync is written from its summary block, which MUST give:

- the methods, their ordering rules, and the error kinds, with which are safe
  to retry;
- what a deactivation revokes, in order, and the holder's text verbatim;
- how a login reaches a provisioned subject (`directory_claim`), and the two
  federation codes;
- how the claim mapping applies to what the directory holds;
- the records, the actor, `409 subject_directory_managed`, and what a `Start`
  error does;
- the extended decision's id, in one line.
