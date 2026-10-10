# 0017 — Ending an external window: a push that ends what it opened, and the sessions it backed

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/12, under `## Upstream request`,
> and answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2).
>
> **Where it sits.** Fourth, after tagged releases (0014), the public server
> package (0015) and self-service grant requests (0016), and before the
> north-bound API (0019). It depends only on merged phases: 0009 (the revocation
> stream), 0012 (grants, and the revocation that ends the sessions a grant
> backed) and 0013 (the push receiver, scope bindings and the declarative
> provider). It runs this early because Enterprise's access-context phase (its
> 0008) builds its vendors' "ended" detection unwired, and skips its
> live-session criterion, until this lands. The public server package (0015)
> runs first, so the wiring it moved out of `cmd/hoplock-control` lives in
> `internal/daemon`; nothing here depends on where.

## Read first
- `docs/PROTOCOL.md` — session workflow. Read §3 for `ext/` as a compatibility
  promise, for revising the plan in place, and for the downstream hand-over
  this phase owes.
- `docs/CROSS-REPO-PROTOCOL.md` — **§4.1**, **§4.3** (where the sync is
  queued), and **§5** at "The PR that answers an upstream request is not a
  sync". This phase changes `ext/`, a shared surface, and it answers a request,
  so it owes a downstream sync (below).
- `docs/PLAN.md`:
  - **§2**:
    - **M16**, in full: the two directions, the three properties the framework
      owns (the binding; replay, idempotency and skew; the ceiling), and "a
      confirmed window **is** a grant". This phase revises its push direction
      and its replay property in place.
    - **M10**: revocation is the one state stored, it ends the sessions a grant
      backed, and every act on a grant is audited with its actor in the
      transaction that does it. This phase adds a second actor who may revoke:
      the system whose push opened the window, for that window only.
    - **M9**: the `session_kill` and `cache_invalidate` a revocation publishes.
      Nothing new is published.
    - **M5**: why the authorize path gains nothing here, and why a probe's
      answer revokes nothing (see "What this phase answers").
    - **M4**: vague to the user, total to the auditor. It decides what the
      holder is shown.
    - **M11**: a refusal made on purpose is a 4xx with a code; everything else
      is an outage the caller may retry.
    - **M15**: `ext/` is a compatibility promise, and every seam has a real
      answer here — for this one, Control's declarative provider.
    - **M12** and **M18**: a new table carries the tenant column, and the
      tenant comes from the credential, never the body.
  - **§5.5**: "The three shapes" — which windows are stored grants and which
    are built per decision.
  - **§3**: "Component responsibilities", for `internal/access` and
    `internal/accessctx`.
  - **§10**: this phase's row, and 0012's and 0013's.
- `docs/learnings/` — read the summaries.
  - Open **`0013`** in full. "The push receiver's order" is the order this
    phase extends. "The three shapes, and why a probe-only grant is not stored"
    is the precedent for the answer to the requester's side question. Its
    findings say what disabling a binding does and does not end.
  - Open **`0012`** for revocation: the order, the settle pass, and why the
    first revocation's time, actor and reason are the ones that stand.
  - **`0009`**'s summary for the stream, and **`0003`**'s for forward-only
    migrations and the tenancy guards.
- Code. Read it before designing anything:
  - `ext/accesscontext.go`: `WindowAssertion`, and the `AccessContextProvider`
    doc comment — "THE TWO DIRECTIONS", and how `Interpret` answers.
  - `internal/accessctx/{push,events,errors,service}.go`.
  - `internal/access/{external,revoke,events,errors}.go`.
  - `internal/audit/{grantevent,contextevent}.go`.
  - `internal/store/{grants,repositories,types}.go`; `audit.go`, for the
    advisory lock the hash chain takes; and
    `migrations/0009_external_access_context.sql`, for
    `grants_external_assertion_key`.
  - `internal/httpapi/north/accesscontext.go`, and `handleGrantRevoke` in
    `grants.go` for how an undelivered revocation is answered.
  - `internal/accessctx/declarative/provider.go` (`Interpret`,
    `assertion.holds`) and `internal/config/accesscontext.go`
    (`DeclarativePushConfig`, `DeclarativeAssertion`, and their validation).
- The request itself, for the requester's own words. The Enterprise decisions
  it turns on are **E7** (inbound actions are privileged, as amended by M16)
  and **E13** (packaged integrations are packaging, not capability). Read them
  in `hoplock/enterprise`'s `docs/PLAN.md` if a choice here turns on one. They
  are cited by id here and never restated.

## Objective
Let an external system end a window it opened, and let that end the sessions
the window backed.

Today a push can only open a window:

- a second push under the same assertion id that says something else is a
  `409`;
- the `integration` role holds `access-context:push` and nothing else, so it
  cannot revoke a grant;
- a probe answering `WindowNotConfirmed` stops a push-probe window counting for
  *later* decisions, after any cached TTL, and revokes nothing;
- and only a revoked grant ends the sessions it backed (0012).

So a scan that finished, a change that was cancelled, or an approval withdrawn
mid-window reaches new connections at best — at the next uncached probe of a
push-probe window — and a live session not at all. That session runs to the
window's asserted end or the ceiling, which is at most
`access_context.max_window`, 12 hours by default. M16 exists to gate the access
least safe to leave standing, and this is that access, left standing.

After this phase the integration pushes an **end**. Control revokes the grant
the window became through 0012's revocation, the holder's sessions end with a
reason Control wrote, and the act is audited with the credential that pushed
it. Control alone gets this too: its declarative provider reads an end (M15).

## The request, in this repository's vocabulary
- *Asked for*, verbatim from the request:

  ```go
  type WindowAssertion struct {
      // ...existing fields...

      // Ended reports that the external system says the window identified by ID
      // has ended: the scan finished, the change was cancelled, the approval was
      // withdrawn. Control revokes the grant ID became, which ends the sessions
      // it backed, audited with the pushing principal as the actor and the
      // Reference as the reason. Only ID is required with Ended. It is
      // idempotent: ending an already-revoked or expired window is a no-op
      // answered as such, and an Ended for an ID that never became a grant
      // is recorded and creates nothing. The binding's credential check and
      // the rate limit apply as to any push; scope is not re-checked, because
      // ending access cannot widen it.
      Ended bool
  }
  ```

  In this repository's words: a push may **end** the window its assertion id
  opened. It arrives at the same receiver and passes the same binding,
  credential check and rate as an open (M16). Control revokes the grant that
  window became (M10), which publishes the `session_kill` and
  `cache_invalidate` every revocation already publishes (M9).
- *Asked as a question for upstream:* should a push-probe window whose probe
  now answers `WindowNotConfirmed` also revoke its stored grant, so that
  windows close for vendors that cannot push?
- *Noted, and not raised:* `ext.AccessContextPush` carries no request headers,
  so a provider cannot verify a vendor's webhook signature. Out of scope
  (below).
- *Until it exists:* Enterprise's Qualys and BMC Helix integrations cannot close
  a window early. A finished scan or a cancelled change stops new connections
  only where a probe runs, and never ends a live session — a person's least of
  all, which is BMC Helix's whole case. A customer on Control alone has the same
  gap with the declarative provider.

## What this phase answers, and where it differs from the ask
The requester could not read this plan. The need fits it: M16 already says "a
revoked window stays revoked however often it is pushed again", and M10 already
ends a grant's sessions when it is revoked. So the shape is **met as asked** —
`Ended bool` on `WindowAssertion`, read by id alone, through the binding's
credential check and rate, with no scope check, and a no-op answered as such for
a window already over. Four things around it change, each for a reason the
requester could not see, and the side question is answered no.

**Why a bool is right, and not an enum.** The set is closed at two. Every other
change a vendor can make to a window — extended, shortened, rescheduled,
reopened — is a new assertion under a new id plus an end of the old one: a
grant's window is immutable, revocation is the one state stored (M10), and the
id is the idempotency key (M16). And the zero value is an open, which is what
every push meant before the field existed, so Enterprise builds unchanged.

1. **An end that arrives before its open is kept, and the open is refused.**
   - *Why the ask does not fit.* "Recorded and creates nothing" leaves nothing
     to stop the open. The idempotency key lives on the grant row
     (`grants_external_assertion_key`), and the receiver never consults an
     audit record. Webhooks are not delivered in order, so an end delivered
     before its open — a short scan, a change created and cancelled at once, a
     retry — would be followed by an open creating exactly the grant the end
     was meant to prevent, standing until its asserted end or the ceiling.
   - *What to build.* An end naming no window keeps its id as ended, and an
     open under that id afterwards is refused (`window_closed`, `field: id`).
     That is M16's "a revoked window stays revoked", read as "an ended window
     stays ended". An open and an end of one id are serialized, so that no
     interleaving leaves a live grant behind an end that was answered.
2. **The holder is shown Control's reason, never the vendor's reference.**
   - *Why the ask does not fit.* A revocation's reason is the `session_kill`
     reason, which the contract shows to the user verbatim as the session
     closes and requires to be safe to disclose. A reference is text a vendor's
     software chose. Printing it on a person's terminal is a channel nobody
     reviewed: a compromised integration could message everybody it holds a
     window for. And `DefaultRevokeReason` says "by an administrator", which
     would be false here.
   - *What to build.* A fixed reason Control writes, `ExternalEndReason`
     (item 3 below). The references — the grant's, and the end's own if it
     states one — go in the audit record, where an operator resolves them (M4).
3. **An end is never refused for its age, its scope or its window, and an
   expired window is not rewritten.**
   - The ask exempts an end from the scope check because ending cannot widen
     access. The same argument covers every check that exists to stop a push
     widening access: staleness, clock skew, the closed window and the ceiling.
     Refusing a late end leaves access standing, which is the failure M16
     exists to prevent.
   - An end naming a window that already expired is a no-op. The store will
     revoke an expired grant if asked, and `Grant.State` would then answer
     `revoked` — a second, false answer to when access stopped.
4. **Control's declarative provider reads an end too.** Not in the ask, because
   the requester builds packaged integrations. M15 requires a real answer here,
   and by Enterprise's own E13 an end only its packaged integrations could
   express would not be packaging. Without it, a customer's scanner on Control
   alone could open windows it can never close.

**The side question: no. A probe that stops confirming still revokes nothing.**

- Revocation is a transaction, a hash-chain append and a fan-out with a settle
  pass; the authorize path makes none of those (M5). 0013 declined even to
  *store* a probe-only window, for that reason.
- A probe answers about one access — one subject on one host — and a pushed
  window can name several hosts. "Not confirmed for this host" is not "the
  window ended".
- A `WindowNotConfirmed` is cached for its TTL and may be a vendor's momentary
  view; a revocation is permanent. Permanence would rest on one cacheable read.
- And it would not meet the need: a probe runs only when somebody connects, and
  a live session whose window ended is never probed.

So a vendor that can push ends its windows through this phase. A vendor that can
only be asked keeps M16's behaviour: a push-probe window stops counting at the
next uncached probe, and a probe-only window's sessions run to the end the probe
stated, or the ceiling. Ending a live session on a probe's answer would take
Control re-probing the windows that back live sessions, off the authorize path,
on a cadence and a budget of its own. That is a phase of its own, and — if
Enterprise needs it, because a vendor turns out to be probe-only — a request of
its own.

## In scope

### 1. The seam (`ext/accesscontext.go`)
Add to `WindowAssertion`, after `AdditionalContext`:

```go
	// Ended reports that the external system says the window ID names has
	// ended: the scan finished, the change was cancelled, the approval was
	// withdrawn. Control revokes the grant that window became, which ends the
	// sessions it backed (M10), audited with the credential that pushed.
	//
	// With Ended, Control reads ID, and records Reference, IssuedAt and
	// AdditionalContext when they are set; it ignores every other field and
	// requires none of them. An end is never refused for its age, its scope
	// or its window, because ending access cannot widen it.
	//
	// An ended window stays ended. An end that arrives before its window was
	// opened is kept, and the open is refused when it comes, because pushes
	// are not delivered in order; a ticket reopened or a scan re-run is a new
	// window under a new ID. Ending a window already ended or already expired
	// changes nothing, and is answered as such.
	//
	// The zero value is an open, which is what every push meant before this
	// field existed.
	Ended bool
```

Revise in place:

- `WindowAssertion`'s own comment: what one push asserts is that a window has
  opened for one subject on some targets, or, with `Ended`, that a window an
  earlier push opened has ended.
- `Subject`, `Targets` and `Window`: "Required" becomes "Required to open a
  window".
- `ID`: beside "a revoked window stays revoked", "and an ended one stays ended".
- `AccessContextProvider`'s "THE TWO DIRECTIONS", the push bullet: a push may
  also end a window the same system opened, and Control revokes the grant it
  became.

Nothing else in the interface changes. `Interpret` answers in the same
vocabulary, and an end is a `WindowAssertion` with a nil error. The change is
additive: a provider that never sets `Ended` behaves exactly as before. It is
still an `ext` change; say so in your learnings (PROTOCOL §3).

### 2. The end path (`internal/accessctx/push.go`, `events.go`)
Steps 1–3 of the receiver — the provider, the binding and whether it is
enabled, the credential it names (`push_not_permitted`, an escalation), its
mode, the rate, `Interpret` — run for an end exactly as for an open. The rate
runs before `Interpret`, so opens and ends share one bucket by construction. A
disabled or deleted binding refuses an end as it refuses an open; whoever
disabled it ends what is still open by revoking it (0012).

After `Interpret`, an assertion with `Ended` leaves the open path, before
`normalizeAssertion`:

- **Shape.** `normalizeEnd` checks only what an end reads:
  - `ID`, as `normalizeAssertion` checks it;
  - `Reference`, when set, likewise. An empty one stays empty: on an end it
    means "not stated", not "the ID";
  - `AdditionalContext`, against `access.AdditionalContextValid`.

  A malformed end is `assertion_malformed` with its field, as an open is. Every
  other field is cleared, not checked.
- **No when, whether or how long.** Steps 5–7 — staleness and skew, the closed
  window, scope and privilege, the ceiling — do not run.
- **The window, under its lock.** In one transaction, take the assertion's lock
  (item 4), then read the grant the assertion became
  (`Grants().GetByAssertion(tenant, provider, id)`):
  - **A grant.** Commit, and end it through `access.Service.EndExternal`
    (item 3). The outcome is `revoked`, or `already_ended` when it was revoked
    or had expired before.
  - **No grant, and the id already ended.** `already_ended`. Nothing is
    written.
  - **No grant.** Keep the id as ended (`AccessContextWindowEnds().Insert`) and
    record `access_context.window_ended`, both in that transaction.
    `recorded`.
- A failure anywhere is an outage the integration may retry (M11), and a retry
  is safe by construction: every branch above is idempotent.

Add, beside `PushResult`:

```go
// EndOutcome is what an end did. Closed set.
type EndOutcome string

const (
	// EndRevoked is an end that revoked the grant its window became.
	EndRevoked EndOutcome = "revoked"
	// EndAlreadyEnded is an end of a window that was revoked, expired or
	// ended before. Nothing new is stored; a revoked grant's kill is sent
	// again (0012).
	EndAlreadyEnded EndOutcome = "already_ended"
	// EndRecorded is an end naming an id no grant was made under. It is kept,
	// and an open under that id is refused.
	EndRecorded EndOutcome = "recorded"
)
```

`PushResult` gains `Ended EndOutcome`, empty for an open. Its `Grant` is the
grant the window became, for `revoked` and for `already_ended` when there is
one, and the zero grant otherwise.

`events.go` gains `EventWindowEnded = "access_context.window_ended"`: an end
naming an id no grant was made under, recorded because it changes what a later
open may do. It carries the assertion (`Event.Assertion`) and files at the
default `warn`. `contextMessage` reads "end from <provider> recorded for an id
with no window". An end that revoked a grant is recorded by `internal/access`
as `grant.revoked` (item 3), not here, the way an admitted open is recorded as
`grant.created`.

**The open path learns the ended ids.** At step 4 ("Again?"), when no grant
exists under the id, read `AccessContextWindowEnds().Get`. An ended id is
refused before steps 5–8:

- `window_closed`, `field: "id"`, with the detail "the external system ended
  this window at <ended_at>, before it was opened";
- audited `access_context.push_refused`, like any refusal after the push was
  read.

The read at step 4 is the fast answer. The one that holds under a race is inside
`CreateExternal` (item 3), which returns `access.ErrAssertionEnded`; map it to
the same refusal.

Revise the order comment at the head of `push.go` to name both paths.

### 3. Revocation by the window's own system (`internal/access`, `internal/audit`)
- `ExternalEnd` is what an end stated, for the record:

  ```go
  // ExternalEnd is what an external system said when it ended a window
  // (M16). It is recorded with the revocation and never shown to the holder.
  type ExternalEnd struct {
  	// Reference is the end's own reference, when it stated one.
  	Reference string
  	// IssuedAt is when the external system says it ended the window. It is
  	// recorded, never judged.
  	IssuedAt time.Time
  	// AdditionalContext is a JSON string or a JSON object, verbatim, or
  	// empty.
  	AdditionalContext json.RawMessage
  }
  ```
- `Event` gains `Ended *ExternalEnd`.
- `ExternalEndReason`, beside `DefaultRevokeReason`:

  ```go
  // ExternalEndReason is what the holder is shown when the external system
  // that opened their window ended it. It is Control's text, never the
  // system's: it is displayed verbatim as the session ends (the contract's
  // disclosure rule), and the references it does not name are in the record.
  const ExternalEndReason = "The access window this session depended on has ended."
  ```
- `(*Service).EndExternal(ctx, tenant, actor Actor, grantID string, end ExternalEnd) (Revocation, error)`:
  - It is `Revoke` with `ExternalEndReason` as the reason and `Event.Ended`
    set: the same transaction, audit record and announcement, the same two
    passes, the same `ErrUndelivered`. Reach that by factoring `Revoke`'s body
    into one unexported function both call. Do not copy it: two revocation
    paths are two places for the settle pass to drift apart.
  - It refuses a grant no push produced (`External.AssertionID == ""`) with an
    outage, not a refusal: the receiver only ever passes a grant it found by
    its assertion, so anything else is a wiring fault.
  - A grant that is **expired and not revoked** is returned untouched:
    `Revoked` false, nothing written, published or announced. Judge that at
    the instant the revocation would be stamped with — one `now` for both —
    so that no revocation is ever recorded after the grant's own expiry.
  - A grant already revoked is handled exactly as `Revoke` handles one: the
    first revocation's time, actor and reason stand, no second record is
    written, and the kill and the invalidation are sent again. That is what
    makes an end whose delivery failed safe to push again.
- **`CreateExternal` honours an ended id.** Inside its transaction, before its
  insert, take the assertion's lock and read `AccessContextWindowEnds().Get`.
  An ended id returns the new `ErrAssertionEnded` (`errors.go`), with nothing
  inserted or recorded.
- **Lock order.** In both paths the assertion's lock is the first thing the
  transaction takes, before the audit append takes the chain's. One order
  everywhere is what keeps the two paths from deadlocking.
- The actor is the integration's credential: `Principal` set, `Subject` empty.
  `grantActor` already stores that, so `revoked_by` names the credential that
  pushed the end.
- **The record** (`internal/audit/grantevent.go`). A `grant.revoked` whose event
  carries `Ended`:
  - adds `grant_external_ended` = `"true"`, and
    `grant_external_ended_reference`, `grant_external_ended_issued_at` and
    `grant_external_ended_additional_context` when set. Keep them apart from
    the existing `grant_external_window_end`, which is the end of the window
    the open asserted;
  - reads "grant revoked for <holder>: <system> ended the window" in
    `grantMessage`;
  - keeps `grantSeverity`: `warn`, as any revocation.

  The grant's own attributes — system, assertion id, mode, reference, the
  window asserted — are already on every `grant.revoked`, so the record names
  the window without help.

### 4. The ended ids (`internal/store`, a migration)
A new forward-only migration, numbered next when you start — `0010` as this is
written, unless 0016 or another phase has taken it. The content, not the house
style; match `0009`'s column conventions where they differ:

```sql
-- An end that named no window (M16, 0017). The id is kept so that an open
-- pushed under it afterwards is refused. A grant made under an id and an end
-- kept for it never both exist: both are written under the assertion's lock.
CREATE TABLE access_context_window_ends (
    tenant             text        NOT NULL,
    external_system    text        NOT NULL,
    assertion_id       text        NOT NULL,
    external_ref       text        NOT NULL DEFAULT '',
    issued_at          timestamptz,
    ended_at           timestamptz NOT NULL,
    ended_by           text        NOT NULL DEFAULT '',
    ended_by_principal text        NOT NULL,
    PRIMARY KEY (tenant, external_system, assertion_id)
);
```

Rows are never deleted. There is one per end that arrived with no window — rare,
and bounded by the integration's rate — and no sweeper: a missing row is exactly
what would let an ended window reopen.

`store.AccessContextWindowEndRepository`, reached as `AccessContextWindowEnds()`
on the store and on a transaction:

- `Lock(ctx, tenant, system, assertionID) error` takes
  `pg_advisory_xact_lock` on one `bigint` key, `hashtextextended` of the three
  joined. It is valid only inside a transaction, and refuses outside one.
  - Postgres keeps the one-`bigint` key space apart from the two-`int4` space
    the hash chain locks in (`audit.go`), so the two never contend.
  - A collision — of the hash, or of the joined text — only serializes two
    assertions.
- `Get(ctx, tenant, system, assertionID) (AccessContextWindowEnd, error)`. Not
  found is `IsNotFound`.
- `Insert(ctx, tenant, AccessContextWindowEnd) error`. A present id is
  `IsConflict`.
- `store.AccessContextWindowEnd{System, AssertionID, Reference string;
  IssuedAt, EndedAt time.Time; EndedBy GrantActor}`.

All three take the tenant first and refuse an empty one before any SQL — `Lock`
included, and before its transaction check. The tenancy guards in
`internal/store/tenancy_test.go` are lists kept by hand, and a repository
missing from them is one they never check, so add it to all three:
`repositoryInterfaces` (which `TestRepositoryMethodsTakeATenant` walks), the map
in `TestStoreExposesEveryRepository`, and the calls in
`TestEmptyTenantIsRefusedBeforeAnyQuery`, beside the `AccessContextBindings`
entries 0013 added.

### 5. The answer (`internal/httpapi/north/accesscontext.go`)
The route, its access class and its permission (`access-context:push`) do not
change. There is no new route, and no role gains anything. An end is answered
`200`, after the settle pass when it revokes, as the revoke route is — the
`200` is the integration's evidence that the sessions were told:

```go
// endView is what an end is answered with: which window it named, and what
// became of it. Like pushView, it answers the integration's own act and
// nothing more.
type endView struct {
	AssertionID string `json:"assertion_id"`
	Outcome     string `json:"outcome"`         // revoked | already_ended | recorded
	Grant       string `json:"grant,omitempty"` // the grant the window became, if it became one
	State       string `json:"state,omitempty"` // that grant's state now
}
```

- `200` for every outcome. `recorded` is an answer, not an error: a vendor whose
  webhook is refused retries it, or disables the webhook, and an error status
  here would cost the next end.
- An undelivered revocation is `503 grant_revocation_undelivered`, with
  `grant_id` and `revoked: true`, exactly as `handleGrantRevoke` answers it. The
  message says to push the end again to re-send. Check `access.IsUndelivered`
  before the generic failure.
- Every refusal is answered by `refusePush`, as today. An open refused for an
  ended id is `400 window_closed`, `field: "id"` — an existing code, so
  `AllCodes` gains nothing.

### 6. Control's declarative provider (`internal/config/accesscontext.go`, `internal/accessctx/declarative`)
- `DeclarativePushConfig` gains `Ended []DeclarativeAssertion` (`yaml:"ended"`):
  assertions over the push body, **all** of which must hold for the push to
  read as an end. It is the vocabulary `probe.confirm` already uses.
  - Each names a path and exactly one predicate, as `probe.confirm`'s do.
  - A placeholder (`{{`) in any value is refused at load: a push names no
    access to expand one against.
  - Absent or empty, every push is an open, as today.
- `Interpret`, when `ended` is configured and holds:
  - reads `id`, required as for an open;
  - reads `reference`, `issued_at` and `additional_context` when they are
    mapped;
  - sets `Ended`, and returns.

  It does **not** read `subject`, `targets`, `scope` or the window: a vendor's
  "cancelled" body need not carry them.
- `config.example.yaml`: document `push.ended` in the commented provider, with a
  change-ticket example (`status` one of `Cancelled` or `Closed`).

### 7. The documents
- `docs/PLAN.md`, **revised in place** (PROTOCOL §3):
  - **M16**:
    - the push direction: a push opens a window or ends one;
    - the replay property: "a revoked window stays revoked" becomes "a revoked
      or ended window stays ended", including an end that arrives first;
    - "A confirmed window **is** a grant": an end revokes the grant the window
      became, through `internal/access`, audited with the credential that
      pushed it, and the holder is shown Control's reason;
    - one sentence on the side question: a probe that stops confirming stops
      the window counting and revokes nothing, because the authorize path does
      not revoke (M5).
  - **M10**, the revocation paragraph: an administrator revokes a grant, and so
    does the external system that asserted a window, by ending it (M16).
  - **§5.5**, "The three shapes": a pushed window its system ended is a revoked
    grant and never counts again.
  - **§3**, `internal/accessctx`: the receiver ends a window as well as
    admitting one, and keeps an end that named no window.
  - **§10**: this phase's row, to match what was delivered.
  - The register: M10 and M16 stay `live`. Update their "rendered in" lists if
    they change.
- No new decision is expected: this phase applies M16, M10, M9 and M5. If you
  find yourself settling something none of them settles, add a decision with
  its register row in the same PR, and say so in its first line.

## Out of scope
- **Revoking on a probe's `WindowNotConfirmed`.** Answered no, above.
- **Re-probing the windows that back live sessions**, off the authorize path.
  It is the only way a probe's answer could end a live session, and it is a
  phase of its own — and a request of its own if Enterprise needs it.
- **Ending a window in process**: a sink handed to a provider, or a method it
  calls. An end comes through the receiver like an open, because the receiver
  is where the binding, the credential, the rate and the audit are, and a sink
  has no credential to audit. An integration that learns of an end by polling
  its vendor pushes it like any other.
- **Changing a window in place** — extending, shortening, rescheduling. A
  grant's window is immutable (M10): open a new assertion under a new id, and
  end the old one.
- **Request headers on `ext.AccessContextPush`**, for vendor webhook
  signatures. Enterprise noted it and did not raise it; it raises it only if a
  vendor's signature must be checked.
- **Ending a probe-only window.** It is never stored (0013). Disabling the
  binding stops it being asked about.
- **"Disable and revoke" on a binding**, which 0013's learnings leave to 0019's
  surface if it is wanted.
- **The console** (0021). It is a client of the grant views, which already show
  `revoked_by` and the reason.
- **`contract/` (M1).** Nothing on the wire changes: `session_kill` and
  `cache_invalidate` exist, and a revocation already publishes both. If it seems
  otherwise, that is an upstream request to `hoplock/proxy`, not part of this
  phase.
- **The authorize path.** It gains no read and no branch (M5). A revoked grant
  is already never a decision input.

## Acceptance criteria
Each criterion is a test against Postgres: beside the receiver's existing ones
in `internal/accessctx/accessctx_test.go`, whose harness wires a real revocation
bus, and over the wire in `internal/httpapi/north/accesscontext_test.go`.

**An end ends the sessions**
- An integration pushes a window (`201`); the holder is authorized under it,
  and the decision record names the grant; the integration pushes an end naming
  only the id. Then:
  - `200`, `outcome: revoked`;
  - the grant is revoked: `revoked_by` is the integration's credential with no
    subject, and `revoke_reason` is exactly `ExternalEndReason`;
  - a `session_kill` with that reason, addressed to the session's proxy, and a
    `cache_invalidate` for the holder are published, and again after the settle
    pass;
  - the next authorize for that subject and target does not count the grant;
  - there is one `grant.revoked` record, with `grant_external_ended: "true"`,
    the system, the assertion id and the integration's principal, and
    `audit-verify` passes.
- The same for a push-probe window.
- A scheduled window, ended before it opens, is `revoked` and never counts.
- An end stating a reference, an `issued_at` older than `max_assertion_age` and
  additional context still ends the window, and the record carries all three
  under `grant_external_ended_*`. An end that also carries a subject, targets
  and a window matching nothing is still read by its id alone.

**Idempotent, and never rewriting**
- The end pushed again: `200 already_ended`. The first revocation's time, actor
  and reason stand, no second `grant.revoked` is written, and the kill is sent
  again.
- A window an operator revoked, then ended by its system: `already_ended`, and
  the operator's revocation stands.
- A window that expired, then ended: `already_ended`, `state: expired`. The
  grant row is unchanged (`revoked_at` still unset), and there is no record and
  no event.
- With the stream refusing to publish, an end is `503
  grant_revocation_undelivered` and the grant is revoked. Once the stream
  recovers, the same end pushed again sends the kill and answers `200
  already_ended`.

**Ended stays ended**
- An end for an id never opened: `200 recorded`, and one
  `access_context.window_ended` record. Then an open under that id: `400
  window_closed`, `field: id`, one `access_context.push_refused`, and no grant.
  A second end: `already_ended`, with no second record.
- **The race.** At least a hundred times, an open and an end of one fresh id
  run concurrently. After both return, if the end answered `200`, no live grant
  exists under the id; and no id ever has both a grant and a kept end.
- Ends are scoped to their system and tenant. An end naming an id that only
  another system opened keeps an end for its own system and leaves the other's
  grant live. The tenant is the credential's, never the body's.

**The door is the open's door**
- An end from a credential the binding does not name is `403
  push_not_permitted`, audited as an escalation, and ends nothing. A disabled
  binding is `binding_disabled`, and ends nothing.
- Opens and ends share one rate: a burst of ends past `push_burst` is `429`.
- An end with an empty id, or one with a control character, is `400
  assertion_malformed`, `field: id`.
- A provider that never sets `Ended` behaves exactly as before: 0013's receiver
  tests pass unchanged.

**The declarative provider**
- With `push.ended` configured, a change-ticket body whose status is `Cancelled`
  reads as an end carrying only its id and reference, and one whose status is
  `Approved` reads as an open.
- Without `ended`, every push is an open: `TestTheDeclarativePushMapping` passes
  unchanged.
- Configuration refuses an `ended` assertion without exactly one predicate, and
  one whose value holds a placeholder.
- End to end, with the declarative provider as the integration: open, authorize,
  end, and the session is killed.

## Cross-repo impact
This phase answers an upstream request **and** changes `ext/`, so it owes a
downstream sync. The PR that implements it MUST carry a `## Cross-repo impact`
section (`docs/CROSS-REPO-PROTOCOL.md` §4.1) naming **every** consuming
repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5, "The PR that answers an upstream request is
not a sync"). It knows what it asked for. It does not know what was built, and
four things around the shape differ from the ask. The session there that
rewrites its prompts will be a fresh one that knows nothing.

State at least these obligations:

1. **Its 0008, "Ending a window early — raised upstream".** Rewrite it from what
   merged, and stop building around the gap:
   - `ext.WindowAssertion.Ended`, verbatim, and what Control reads on an end:
     the id, plus the reference, issued-at and additional context it records.
     Nothing on an end is refused for its age, scope or window.
   - The answers: `200` with `revoked`, `already_ended` or `recorded`, and `503
     grant_revocation_undelivered`, which means push the end again. Every one
     is safe to retry.
   - Ended stays ended, an end before its open included, so an integration
     need neither order nor buffer what it pushes. A reopened or rescheduled
     Helix change, or a re-run Qualys scan, is a **new assertion id**: derive
     ids that differ per window — say, the change id and its revision — or the
     reopened window is refused.
   - The holder is shown `ExternalEndReason`, never the vendor's text.
   - Drop "behind a seam named for it and visibly unwired", and turn the
     skipped live-session criterion into a real one: a Helix cancellation ends
     a person's live session with `session_kill`. It stays subject to 0008's
     other skips — no binding route before Control's 0019, and the pinned
     version (E3).
2. **Its "Qualys specifically" and "BMC Helix specifically".** "Closing reaches
   new connections only" stays true only for a vendor that cannot push. Record
   the side question's answer: a probe that stops confirming revokes nothing,
   so a probe-only deployment's live session runs to the end its probe stated,
   or the ceiling, and the docs say so. If that is not acceptable for Qualys,
   the re-probe of live windows is a new upstream request, and this prompt's
   reasoning is where it starts.
3. **Its plan.** Wherever E7 says what an inbound push may do: it may now end a
   window its own system opened, never another system's, through the same
   credential and binding. Wherever E13 or §4 describes the seam: the
   declarative provider ends windows too (`push.ended`), so ending is not
   something the packaged integrations add.
4. **Its pin.** Every obligation above holds from the release this PR cuts
   (M23), never from `main`. Name that release by version, as this PR's
   `CHANGELOG.md` section does, and say that Enterprise's pin moves to it.

Queue the **"Downstream sync" kickoff** from `docs/KICKOFF.md`, verbatim except
for its blanks — this PR's URL, and the obligations above — as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`, committed
once the PR is open, and end the section by naming that file
(`docs/CROSS-REPO-PROTOCOL.md` §4.3).

### `hoplock/proxy`
Write **"None"** rather than leaving the repository out. The contract (M1) is
untouched: `session_kill` and `cache_invalidate` exist, and a revocation already
publishes both. The proxy consumes neither `ext/` nor the north-bound surface.
"None" is a finding (`docs/CROSS-REPO-PROTOCOL.md` §4.1).

### Who runs the sync
Not you. This PR merges first, and the sync runs afterwards in its own session
(`docs/CROSS-REPO-PROTOCOL.md` §2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0017-ending-external-windows-learnings.md`. Enterprise's sync is
written from its summary block, which MUST give:

- `ext.WindowAssertion.Ended`, verbatim;
- what an end reads, and that nothing on it is refused for age, scope or window;
- the three outcomes and the `503`, with their statuses;
- an end before its open: kept, and the open refused with `window_closed` on
  `id`;
- `ExternalEndReason`, verbatim;
- `push.ended`'s shape;
- the side question's answer, in one line.
