# 0041 — Confined inbound actions: an action held to its requester's scope, and an unlock that lifts only its own lock

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/50, under `## Upstream request`,
> from its request file
> `prompts/upstream/queued/enterprise-PR#50-confined-inbound-action.md`, and
> answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2). The request
> joins two shapes into one need, at the user's direction, and leaves Control
> free to split them. This prompt keeps them in one phase. Both change one
> struct, one validator and one ledger row, and Enterprise's 0007 waits on both.
>
> **Where it sits.** Right after the action executor (0039) and identity sync
> (0040). It extends 0039's executor, and every phase it depends on is merged.
> It runs before reports (0042) and everything after it. The confined-action
> renumbering note in `docs/PLAN.md` §10 gives one line of reasons for each.

## Read first
- `docs/PROTOCOL.md` — session workflow. Read §3 for `ext/` as a compatibility
  promise, for revising the plan in place, and for the downstream hand-over
  this phase owes. Then §4 and §5.
- `docs/CROSS-REPO-PROTOCOL.md` — **§4.1**, **§4.3**, and **§5** at "The PR
  that answers an upstream request is not a sync". This phase answers a
  request and changes what `ext/` does and says, so it owes a downstream sync
  (below).
- `docs/PLAN.md`:
  - **§2**, the register first, then:
    - **M28**, in full. It is the decision this phase extends. Its sentence
      "a lock's expiry is the requester's to drive by executing
      `unlock_subject`" is the one this phase makes safe to follow.
    - **M15** (the action point is *disabled* when absent, and Enterprise
      implements the handler), **M23** (the release: a field added to a struct
      is compatible, and a method added to `ext.ActionExecutor` would not be),
      **M11** (a refusal is a deny on purpose, never an outage), **M10** (a
      grant's selector: targets, labels and zones, ANDed), **M7** (the
      break-glass refusal, which a scope does not change), **M18** (a scope is
      read inside the action's one tenant) and **M5** (nothing here touches the
      decision path).
  - **§3**, "Component responsibilities": `internal/standing` and
    `internal/action`.
  - **§7**, at "Standing denials and inbound actions are audited on this
    server's own chain".
  - **§10**: this phase's row and the confined-action renumbering note.
- `docs/learnings/` — read every summary. Open these in full:
  - **`0039`**: the executor, its ledger, how a no-op and a refusal are
    recorded, and why a repeat sends again only what still stands;
  - **`0012`**, where it describes a grant's scope selector.
- Code. Read it before designing anything:
  - `ext/notify.go`, from `ActionKind` to `ActionHandler`, with every doc
    comment;
  - `internal/action/executor.go`: `Execute`, `validate`, `sameAction`,
    `apply`, `unlock`, `refuse`, `recordRefusal` and `recordUnchanged`;
  - `internal/standing/service.go`: `LiftLock` and `LiftRequest`;
  - `internal/store/standing.go`: `SubjectLock`, `StandingActor` and
    `subjectLockRepo.Lift`;
  - `internal/store/types.go`: `Grant` (`ScopeTargets`, `ScopeLabels`,
    `ScopeZones`) and `Target` (`Labels`); `internal/store/targets.go` (`Get`);
    `internal/store/decisions.go` (`SessionExists`, `ListBySession`);
  - `internal/store/migrations/0020_standing_denials_and_actions.sql`
    (`subject_locks`, `action_executions`);
  - `internal/action/harness_test.go` and `executor_test.go`, which the new
    tests extend.
- The request itself, for the requester's own words. The Enterprise decisions
  it turns on are **E2** (only Control's public packages are a surface an
  extension may use), **E7** (inbound actions are privileged) and **E14**
  (only the supervisory plane is a north-bound client). The phase that consumes
  this one is its 0007. Read any of them in `hoplock/enterprise` only if a
  choice here turns on it. Each is cited by id and never restated.

## Context estimate
- **Reads:** this prompt, M28 (~120 lines), the summaries, 0039's learnings,
  and the code above (~1 500 lines, the executor half of it).
- **Changes:** `ext/notify.go`'s doc comments and two fields; one new type;
  `internal/action`'s validator, a reach resolver and the confined unlock;
  `internal/standing`, and one conditional statement in `internal/store`; one
  migration; their tests; PLAN M28, §3, §7 and §10's row; `CHANGELOG.md`.
- **Why it fits:** no route, no screen and no decision-path change. The reach
  of each kind is settled below, so the session implements a table rather than
  designing one.

## Objective
Let an `ext.ActionHandler` hand Control the scope its requester may act
within, and have Control refuse an action that reaches outside it. Let the
handler ask for an unlock that lifts a lock only if its own action placed it.

Today an inbound action is confined to nothing but its tenant:

- `ext.Action` names what it acts on by id alone. A session, a grant and a
  target carry a subject, a selector and labels, and nothing at the seam
  resolves them. So a handler confining a credential to some subjects or to
  some labels can check only the ids an action names itself;
- reading them through the north-bound API is not a surface an extension may
  use (E2, E14), and a read followed by an act would race the act anyway;
- `unlock_subject` lifts whatever lock stands on the subject
  (`subjectLockRepo.Lift`, keyed by subject alone). Yet `subject_locks`
  already records the `action_id` and the principal that placed each lock.

After this phase Control checks an action's reach against the scope inside the
act's own transaction, from its own data. It lifts a lock conditionally when
asked. An action that sets neither behaves exactly as it does today.

## The request, in this repository's vocabulary
- *Asked for*, verbatim from enterprise#50:

  > ```go
  > // on ext.Action
  >
  > // Within confines the action to what the requester may touch. When it is
  > // non-nil, Control checks what the action reaches against it, inside the
  > // action's own transaction and from Control's own data, and refuses an
  > // action that reaches outside it: KindDenied, ReasonCode `out_of_scope`,
  > // nothing executed, recorded as action.refused. Nil confines nothing, as
  > // today.
  > Within *ActionScope
  >
  > // IfPlacedBy is for ActionUnlockSubject only. When it is set, the lock is
  > // lifted only if the ActionID that placed it equals this one. A lock placed
  > // by another action or by an operator is left standing: Affected 0,
  > // ReasonCode `not_this_lock`, nil error. With any other kind it is
  > // KindInvalid on `if_placed_by`.
  > IfPlacedBy string
  >
  > // ActionScope is what an integration may touch. Each field that is set
  > // must hold for everything the action reaches; an empty field confines
  > // nothing.
  > type ActionScope struct {
  >     // SubjectIDs: every subject the action reaches is one of these.
  >     SubjectIDs []string
  >     // TargetLabels: every target the action reaches carries each of these
  >     // labels with this value (ext.Target.Labels).
  >     TargetLabels map[string]string
  > }
  > ```
  >
  > Not asked: a lock that expires inside Control. M28 leaves expiry with this
  > repository. `IfPlacedBy` is the smallest change that makes driving it from
  > here safe.

  The request also gives its reading of what each kind reaches, and leaves the
  hard cases to Control: whether a target scope admits a subject-wide kind,
  and how a grant scoped by wildcard, label or zone meets `TargetLabels`.

  In this repository's words: two fields on `ext.Action` and one new type in
  `ext`. `internal/action` resolves what each kind reaches from the store,
  inside the transaction `Execute` already opens, and checks it before
  `apply`. The unlock becomes a conditional delete in `internal/store`, reached
  through `internal/standing`.
- *Until it exists*, Enterprise's 0007:
  - refuses a label scope when an integration is configured, rather than
    accepting it unchecked;
  - offers a kill by `SessionID` and a `revoke_grant` only to a credential
    scoped to the whole tenant, because nothing says whose they are;
  - drives a lockout's expiry through the plain `unlock_subject`, so an expiry
    can lift a lock an operator placed after this phase's lock was lifted;
  - skips two criteria, per-label authorisation and an expiry that never lifts
    a lock it did not place.

  A customer on Control alone loses nothing. The action point is *disabled*
  when absent (M15), and an operator's levers are unconfined by design: an
  operator's scope is the RBAC of a person.

## What this phase answers, and where it differs from the ask
It is **met as asked**, with one field added and the cases the requester left
to Control settled. Each item below is binding.

1. **`ActionScope` gains `TargetIDs []string`**: every target the action
   reaches is one of these. The requester's own 0007 already scopes a
   quarantine by a list of target ids, and checks it from the id the action
   names. With only `SubjectIDs` and `TargetLabels`, a credential scoped that
   way still could not be offered a kill by session or a grant revocation. That
   is half of what the request is for, and it is the same reach check against
   a set of ids. This is the one difference from the ask, and the PR says so.
2. **What each kind reaches.** It is resolved inside the transaction, from the
   store:

   | Kind | Subjects it reaches | Targets it reaches |
   | --- | --- | --- |
   | `kill_session` by `SessionID` | every `subject_id` on the session's decision records | every `target_id` on them, every hop of a chain included |
   | `kill_session` by `SubjectID` | that subject | **all** of them (Control cannot see which sessions are open) |
   | `lock_subject`, `unlock_subject` | that subject | **all** of them (a lock denies every target) |
   | `revoke_grant` | the grant's `SubjectID` | the grant's selector (item 4) |
   | `quarantine_target` | **all** of them (a quarantine denies the target to every subject) | that target |

3. **A dimension a kind reaches whole fails a scope that confines it.** A
   scope that sets `TargetLabels` or `TargetIDs` refuses `lock_subject`,
   `unlock_subject` and a kill by subject. That is the requester's own reading,
   confirmed. A scope that sets `SubjectIDs` refuses `quarantine_target`. That
   is the same rule the other way round, and it differs from the request's
   reading, which listed a quarantine as reaching its target alone. A
   credential confined to some people must not be able to deny a target to
   everyone else.
4. **A grant's selector is inside a target scope only when it cannot reach
   outside it.** The selector's three parts are ANDed (M10), and an empty
   selector means every target the matching rule covers. So:
   - `TargetIDs`: inside only if `ScopeTargets` is non-empty and every id in it
     is in `TargetIDs`;
   - `TargetLabels`: inside only if, for each required label `k=v`, either
     `ScopeLabels[k] == v`, or `ScopeTargets` is non-empty and every target it
     lists exists and carries `k=v`;
   - a selector of zones alone, or an empty one, is outside every target
     scope. A zone is not a label, and an empty selector reaches whatever a
     rule does.
5. **A confined requester learns nothing outside its scope.** With `Within`
   set, the scope is checked before Control says whether a selector is known.
   An unknown selector is outside the scope, unless the scope admits it by id
   alone: a `SubjectID` in `SubjectIDs`, or a `TargetID` in `TargetIDs`, on a
   kind whose other dimension the scope leaves unconfined. So `KindNotFound`
   reaches a confined requester only for an id its own scope names. The error
   text names the scope field that failed, never the subject or target that
   lay outside it. Control's own record names those (item 9).
6. **A repeat is checked too.** The scope is checked on every `Execute`,
   before the ledger answers with the first result and before anything is sent
   again. Otherwise a requester outside the scope could repeat another
   credential's `ActionID` and have a kill published again. `Within` is who may
   ask, not what is asked, so it is not part of an action's identity and is
   not stored. `IfPlacedBy` changes what the action does, so it **is** part of
   it. Under one `ActionID`, an unlock with a different `IfPlacedBy`, or with
   one where the first had none, is `KindConflict`, held by a new ledger column
   (In scope §4).
7. **A scope must confine something.** A non-nil `Within` with every field
   empty is `KindInvalid` on `within`. It is far more likely a credential whose
   scope failed to load than a request for the whole tenant, which is `nil`.
   The bounds are this phase's own: at most 1 000 `SubjectIDs` and 1 000
   `TargetIDs`, each held to `checkToken`, and at most 64 `TargetLabels`, each
   key and value non-blank, one line and at most 256 bytes. Anything else is
   `KindInvalid` naming `within.subject_ids`, `within.target_ids` or
   `within.target_labels`.
8. **`IfPlacedBy` matches the action and the handler.** The lock is lifted
   only if its `action_id` equals `IfPlacedBy` **and** its
   `locked_by_principal` is this executor's principal,
   `action-handler:<provider>`. `ActionID` is the requester's own name, and a
   lock is not this handler's because some other placer reused a string. A
   lock placed under a provider name since changed is therefore
   `not_this_lock`, and an operator lifts it. `IfPlacedBy` is held to
   `checkToken`. With any kind but `unlock_subject` it is `KindInvalid` on
   `if_placed_by`.
9. **The check and the act share one transaction, and nothing moves between
   them.** The reads the check makes run in the transaction `Execute` opens,
   after the ledger's advisory lock. They take `FOR SHARE` on the rows they
   read (the target rows, the grant row), so a relabel or a re-scope committed
   meanwhile either waits for the act or is seen by the check. The decision
   path takes no such lock, and it is not slowed. The confined unlock is one
   statement: `DELETE … WHERE tenant, subject_id, action_id,
   locked_by_principal … RETURNING`. No read-then-delete can lift a lock
   placed in between.
10. **Records.** An `out_of_scope` refusal is `action.refused`, with `code`
    `out_of_scope`, `scope_field` (`subject_ids`, `target_ids` or
    `target_labels`) and `outside`: the first subject or target id found
    outside, or `all_subjects` / `all_targets` for a dimension reached whole.
    It is Control's data on Control's own chain. A `not_this_lock` is
    `action.unchanged` with `outcome_reason` `not_this_lock`, as 0039 records
    every nothing-to-act-on, and the lock and its record are left as they
    stand.
11. **No method is added.** Adding fields to `ext.Action` is compatible for
    every caller and every implementer: the struct stays comparable, since
    `*ActionScope` is a pointer. A read such as `ActionExecutor.Reach` was the
    requester's alternative. It is not built, because a method added to an
    interface Enterprise implements in its fakes breaks it (M23), and because
    a read followed by an act races. If `make release-check` reports a break
    anyway, declare it under `### Breaking` and say why in the learnings.

## In scope

### 1. `ext/notify.go`: two fields, one type, and every doc comment
- `Action.Within *ActionScope` and `Action.IfPlacedBy string`, with the
  request's doc comments, revised to say what items 2–8 settle.
- `ActionScope{SubjectIDs []string; TargetIDs []string; TargetLabels
  map[string]string}`. Its doc comment carries item 2's table and items 3–5 in
  prose a handler's author can follow without this prompt.
- `ActionResult.ReasonCode`'s list gains `not_this_lock`, and `out_of_scope`
  with `KindDenied`. `ActionExecutor.Execute`'s error list says `KindDenied`
  is `break_glass_subject` or `out_of_scope`, and `KindNotFound` says what
  item 5 changes for a confined requester.
- `ActionLockSubject`'s comment ("it stands until `ActionUnlockSubject` or an
  operator lifts it") and `ActionUnlockSubject`'s say how an expiry uses
  `IfPlacedBy`.

### 2. `internal/action`: validation, reach, the check
- `validate` checks `Within` (item 7) and `IfPlacedBy` (item 8), and `request`
  carries both. `sameAction` compares `IfPlacedBy` and never `Within`.
- A reach resolver, one function per row of item 2's table, reading through
  `tx`. It needs:
  - a session's subjects and targets: a new `Decisions().SessionReach(ctx,
    tenant, sessionID)` returning the distinct `subject_id`s and `target_id`s,
    over `decisions_by_session_idx`;
  - targets read `FOR SHARE`: a new `Targets().GetForShare`, or a flag on an
    existing read. Choose whichever matches the store's style;
  - the grant read `FOR SHARE`.
- The check runs inside `Execute`'s transaction, after the ledger lock and
  **before** the ledger is consulted (item 6). An action outside its scope
  returns a `*refusal{kind: KindDenied, code: "out_of_scope"}`, so it rolls
  back and is recorded by `refuse`. `recordRefusal` gains `scope_field` and
  `outside` (item 10), and the error text names only the field.
- `unlock` takes the conditional path when `IfPlacedBy` is set (§3), and maps
  a lock left standing to `applied{reasonCode: ReasonNotThisLock}` after
  `recordUnchanged`. With no lock at all it is `not_locked`, or `KindNotFound`,
  exactly as today.

### 3. `internal/standing` and `internal/store`: the conditional lift
- `standing.LiftRequest` gains `IfPlacedBy` and `PlacedByPrincipal`.
  `LiftLock` passes them to a new `SubjectLocks().LiftIfPlacedBy(ctx, tenant,
  subjectID, actionID, principal)`, the single statement of item 9. It returns
  `ErrNotFound` when nothing matched, and the caller tells "no lock" from "not
  this lock" with one `Get` afterwards. The operator's routes never set either
  field: an operator's lift is unconditional.
- The record on a lift is `operator.subject.unlocked` as today, written in the
  same transaction.

### 4. Migration: `action_executions.if_placed_by`
The next forward-only migration (`0022` on `main` when this was queued) adds
`if_placed_by text NOT NULL DEFAULT ''` to `action_executions`, with a comment
in 0020's style saying why it is part of an action's identity and `Within` is
not. `store.ActionExecution` gains the field. Rows written before the
migration read `''`, which is what they asked for.

### 5. The plan, revised in place
- **M28**: its first line names this phase ("0041 confines an inbound
  action"). The paragraph "An inbound action is an operator's lever and nothing
  else" says what a scope is and where it is checked, and the expiry sentence
  says how `IfPlacedBy` makes it safe. The register row's "Settles" says the
  same in a clause. The status stays `live`.
- **§3**, `internal/action`: the reach resolver and the check.
- **§7**, the inbound-action bullet: the `out_of_scope` refusal's fields and
  `not_this_lock`.
- **§10**, this phase's row, rewritten from what shipped.

### 6. `CHANGELOG.md`
The release's section (M23), naming both fields, the type, both reason codes,
the migration, and that an action with neither field set is unchanged.

## Out of scope
- **A lock that expires inside Control.** Not asked, and M28 leaves expiry with
  the requester.
- **Any north-bound route, field or permission.** An operator's levers stay
  unconfined. The lock's `GET` view already shows the `action_id` that placed
  it.
- **A read for the handler** (`ActionExecutor.Reach` or similar). See item 11.
- **Confining by anything but subjects, target ids and labels**: zones,
  groups, a subject's claims, an action's kind. Enterprise checks which kinds
  a credential may use itself, from the `Kind` the action names.
- **The decision path.** Nothing here reads or writes on it (M5).
- **Enterprise's half**: credentials, rate limits, the blast-radius cap and
  the expiry schedule are its 0007's (E7).

## Acceptance criteria
- An action with `Within == nil` and `IfPlacedBy == ""` behaves exactly as it
  does on `main`. Every existing test in `internal/action`, `internal/standing`
  and `server/` passes unchanged.
- For every row of item 2's table, and for each of `SubjectIDs`, `TargetIDs`
  and `TargetLabels`, an action inside the scope executes and one outside is
  `KindDenied` / `out_of_scope`. The outside one changes nothing (no row, no
  ledger entry, no publication), and is recorded as `action.refused` with
  `scope_field` and `outside`.
- Item 3: a target scope refuses a lock, an unlock and a kill by subject, and
  a subject scope refuses a quarantine, each with `outside` `all_targets` or
  `all_subjects`.
- Item 4's grant table holds for every case: labels matched by `ScopeLabels`;
  targets that all carry the label; one that does not; a listed target with no
  row; zones alone; an empty selector; `TargetIDs` a superset, a subset, and
  `ScopeTargets` empty.
- Item 5: an unknown subject outside `SubjectIDs` is `out_of_scope`, never
  `KindNotFound`; an unknown subject inside it is `KindNotFound`. No refusal's
  error text contains a subject or target id the scope does not name.
- Item 6: a repeat of an `ActionID` under a scope that does not admit the first
  execution's reach is refused, and nothing is sent again (the fake revoker
  counts no second kill). A repeat inside the scope returns the first result,
  as today. An unlock repeated with a different `IfPlacedBy` is `KindConflict`.
- Item 7: a non-nil empty scope, and each bound exceeded, is `KindInvalid`
  naming its field.
- Item 8: an unlock with `IfPlacedBy` lifts a lock that action placed. It
  leaves standing, `Affected 0`, `not_this_lock`, nil error, `action.unchanged`
  recorded, each of: a lock an operator placed; a lock another action id
  placed; a lock under the same action id placed by another provider's
  principal. With no lock it is `not_locked`. `IfPlacedBy` on any other kind is
  `KindInvalid` on `if_placed_by`.
- The reported scenario: a handler locks under `A`, an operator lifts it and
  locks the subject again, and the handler's expiry, `unlock_subject` with
  `IfPlacedBy: A`, leaves the operator's lock standing.
- The migration applies forward over a database holding 0039's ledger rows,
  and they read `if_placed_by` `''`.
- `make release-check` passes, and the release is a MINOR.

## Tests
Postgres-backed, in `internal/action`, on 0039's harness. Name them for what
they prove, for example `TestAConfinedActionOutsideItsScopeChangesNothing`,
`TestADimensionReachedWholeFailsAScopeThatConfinesIt`,
`TestAGrantSelectorIsInsideATargetScopeOnlyWhenItCannotReachOutside`,
`TestAConfinedRequesterLearnsNothingOutsideItsScope`,
`TestARepeatIsCheckedAgainstItsOwnScope`,
`TestAConfinedUnlockLiftsOnlyTheLockItsActionPlaced` and
`TestIfPlacedByIsPartOfTheAction`. Use one table per item rather than a fixture
per case. The store gains a test for `LiftIfPlacedBy` in
`internal/store/standing_test.go`, and one for `SessionReach` beside the
decision reads' tests (`tenancy_test.go` holds them today, and another tenant's
session reaches nothing). `server/action_test.go` gains one case that drives a
confined action through a registered handler.

## Release (M23)
The next MINOR after the latest tag. It adds `Action.Within`,
`Action.IfPlacedBy` and `ext.ActionScope`, with no `### Breaking` unless the
check reports one (item 11).

## Cross-repo impact
This phase answers an upstream request and changes what `ext/` does and
says, so it owes a downstream sync. Its PR MUST carry `## Cross-repo impact`
(`docs/CROSS-REPO-PROTOCOL.md` §4.1), naming **every** consuming repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5). It knows what it asked for, but not what
was built, and the session there that rewrites its prompts will be a fresh one.
State at least these obligations:

1. **Its 0007, "What Control does not do yet".** Rewrite it from what merged,
   and stop building around the gap:
   - a label scope is checked by Control through `Within.TargetLabels`, and a
     target-id scope through `Within.TargetIDs`, rather than refused;
   - a kill by `SessionID` and a `revoke_grant` may be offered to a scoped
     credential, because Control checks their reach;
   - the expiry's seam executes `unlock_subject` with `IfPlacedBy` set to the
     `ActionID` that placed the lock;
   - its two skipped criteria become real ones.
2. **What Control decided that it did not ask for**: item 1's `TargetIDs`;
   item 3's refusal of a quarantine under a subject scope; item 4's grant rule;
   item 5's ordering of `out_of_scope` before `KindNotFound`; item 6's check
   on a repeat and `KindConflict` on a changed `IfPlacedBy`; item 7's bounds
   and the refusal of an empty scope; and item 8's principal match, so an
   expiry survives no change of provider name.
3. **Its plan.** §4's `ActionHandler` bullet and E7, where they point at the
   request.
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
`docs/learnings/0041-confined-inbound-action-learnings.md`. Enterprise's sync
is written from its summary block, which MUST give:

- the two fields, the type and its three fields, with the bounds;
- item 2's reach table and item 3's whole-dimension rule;
- the grant rule (item 4);
- the error and reason-code table: `out_of_scope` with `KindDenied`,
  `not_this_lock` with a nil error, `KindInvalid` on `within…` and
  `if_placed_by`, and `KindConflict` on a changed `IfPlacedBy`;
- what a confined requester can and cannot learn (item 5), and that a repeat is
  checked;
- the release, by version.
