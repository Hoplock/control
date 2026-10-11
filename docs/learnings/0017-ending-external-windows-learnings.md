# 0017 — ending an external window — Learnings

## Summary
- **What shipped:** a push may END the window its id opened: Control revokes the grant it became through 0012's revocation (its sessions killed), audited with the pushing credential. Migration **`0011`**. Cuts **`v0.4.0`**. Answers `Hoplock/enterprise#12`; an `ext` change, additive.
- **The seam, verbatim** (`ext/accesscontext.go`, on `WindowAssertion`, after `AdditionalContext`):
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
- **The door is the open's:** the same route and permission (`access-context:push`), binding, named credential (`push_not_permitted`, an escalation), mode and rate (one bucket for opens and ends). Malformed `id` is `400 assertion_malformed`. Nothing else is checked.
- **Answers:** `200 {assertion_id, outcome, grant, state}` — `revoked` (after the settle pass), `already_ended` (revoked, ended or expired before; a revoked grant's kill is re-sent; an expired one is never rewritten), `recorded` (no window under the id). `503 grant_revocation_undelivered` (`grant_id`, `revoked: true`): revoked, sessions not told — push the end again. All safe to retry.
- **End before open:** kept in `access_context_window_ends`, recorded `access_context.window_ended`; the later open is `400 window_closed`, `field: id`. Opens and ends of one id serialize on a per-assertion advisory lock (`AccessContextWindowEnds().Lock`), so an id never has both a grant and a kept end.
- **Holder sees** `access.ExternalEndReason` = `"The access window this session depended on has ended."` — never the vendor's text, which goes on the record (`grant_external_ended`, `grant_external_ended_{reference,issued_at,additional_context}`).
- **Declarative:** `push.ended: [{path, equals|not_equals|one_of|exists|contains}]` — all must hold, no `{{` placeholders; reads `id` (+ `reference`, `issued_at`, `additional_context` if mapped). Absent: every push opens.
- **Side question:** no — a probe that stops confirming stops the window counting and revokes nothing (M5); ending live sessions on a probe's answer needs re-probing off the authorize path, a phase (and request) of its own.
- **Decisions:** none added; M16 and M10 revised in place (register rows unchanged); §3, §5.5, §7, §10 too.
- **NEXT session:** `access.Service.EndExternal` and `Revoke` share one body (`revoke`); change revocation there, once. The ended-id table is never swept.

## Details

The summary runs past PROTOCOL §5's aim for the reason 0013's did: the prompt
names seven things it MUST give, and Hoplock Enterprise's sync is written from
the summary alone.

### Where it differs from the ask, and why

Met as asked: `Ended bool`, read by id, through the binding's credential check
and rate, no scope check, a no-op answered as such. Four things around it, each
for a reason the requester could not see:

1. **An end before its open is kept, and the open refused.** "Recorded and
   creates nothing" leaves nothing to stop the open, because the idempotency key
   lives on the grant row. Webhooks are not ordered.
2. **The holder is shown Control's reason.** A revocation's reason is the
   `session_kill` reason, shown verbatim on a person's terminal; a vendor's
   reference there would be an unreviewed channel to everybody the integration
   holds a window for. `DefaultRevokeReason` says "by an administrator", which
   would be false.
3. **Never refused for age, scope or window; an expired window not rewritten.**
   Every check that stops a push widening access is irrelevant to an end, and a
   refused end is access left standing. The store would revoke an expired grant
   if asked, and `Grant.State` would then answer `revoked` — a second, false
   answer to when access stopped.
4. **The declarative provider reads an end too** (M15, and by Enterprise's own
   E13 an end only packaged integrations could express would not be packaging).

### The lock, and the race it closes

`AccessContextWindowEndRepository.Lock` takes `pg_advisory_xact_lock` on one
`bigint` — `hashtextextended(tenant ␟ system ␟ assertion_id, 0)` (U+001F; an id
with a control character is refused upstream, and a collision only serializes
two assertions). It refuses outside a transaction (where it would be released at
once) and an empty tenant before that.

- The end path: one transaction — lock, read the grant by assertion; no grant
  and no earlier end ⇒ insert the end and its `window_ended` record. A grant ⇒
  commit, then `EndExternal` in its own transaction.
- The open path: `CreateExternal`'s transaction takes the same lock FIRST, reads
  the ended ids, and only then inserts and records. So the end either sees the
  committed grant (and revokes it) or the open sees the kept end (and refuses).
- Lock order: the assertion's lock is always the first a transaction takes, and
  the audit chain's (`AppendChain`, the two-`int4` space Postgres keeps apart)
  always the last. `EndExternal` itself takes no assertion lock: it needs none,
  because by then the grant exists and the revocation's `WHERE revoked_at IS
  NULL` is the only race left. The migration lock (`migrationsLockID`) shares
  the one-`bigint` space; it is session-scoped and taken only by `migrate`, so a
  collision would at most serialize a push behind a migration.
- Step 4's read of the ended ids is the fast answer; `access.EndedError`
  (wrapping `access.ErrAssertionEnded`, carrying the kept end) from
  `CreateExternal` is the one that holds under the race, mapped to the same
  `window_closed` refusal and audited `push_refused`.

`TestAnOpenAndAnEndThatRaceLeaveNothingLive` runs 100 races. Unstaggered, the
end won 99 of 100 (the open does more before its lock), so the end now waits
`i%20 × 250 µs`; runs land around 65–70 opens won. `TestCreateExternalRefusesAnIDAlreadyEnded`
proves the under-lock check deterministically.

### Revocation: one body

`Revoke` and `EndExternal` both call `(*Service).revoke(…, end *ExternalEnd)`;
the settle pass, `ErrUndelivered` and the re-send on an already-revoked grant are
the operator's path unchanged. What `end != nil` adds, inside the transaction:
read the grant; no `External.AssertionID` ⇒ an outage (a wiring fault); not
revoked and `now ≥ ExpiresAt` ⇒ return untouched (one `now` for the judgement
and the stamp). The record carries `Event.Ended`; the notification gains
`window_ended: "true"`.

**"Again after the settle pass", read precisely.** 0012's second pass re-sends
the `cache_invalidate` and kills only sessions it has not already ended (the
ones that raced the revocation). So a live session gets ONE `session_kill` and
the holder TWO invalidations; the receiver test asserts exactly that, and 0012's
`TestASessionThatRacesTheRevocationIsEndedByTheSecondPass` covers the raced
session through the same function.

### Other things that changed shape

- `accessctx.PushResult` gains `Ended EndOutcome` and `AssertionID` (an end's).
  `Push` now returns a populated result WITH an error in one case:
  `access.ErrUndelivered`, so the route can name the grant in its `503`.
- `audit.ContextEvent` now records an assertion's `additional_context` as
  `access_context_additional_context` — on `window_ended`, and also on
  `push_refused` / `escalation_attempt` records, which previously dropped it.
- A declarative end treats a mapped `reference` that is absent from the body as
  not stated (as `issued_at` and `additional_context` already were): a vendor's
  cancellation may carry less than its approval did. An open is unchanged.
- `config.example.yaml` documents `push.ended`, with an ITSM change-ticket
  provider (`status` one of `Cancelled`, `Closed`) and a note that the id must
  differ per window.
- The tenancy guards list `AccessContextWindowEndRepository` in all three places.

### Follow-ups, not done here

- Re-probing the windows that back live sessions (the side question's "no") is a
  phase of its own, and a request of its own if Enterprise needs it.
- "Disable and revoke" on a binding remains 0019's, if wanted. A disabled
  binding refuses an end as it refuses an open; whoever disabled it revokes what
  is still open (0012).
- Vendor webhook signatures: still not verifiable (`ext.AccessContextPush`
  carries no headers). Enterprise noted it and did not raise it.

### Verification

`go build`, `go vet`, `go test -race ./...` against Postgres 16 (24 packages),
golangci-lint v2.13.2 built with Go 1.27 (0 issues), `exhaustive-guard`,
`license-check`, `contract-check`, `release-check` (`ext: WindowAssertion.Ended:
added`, compatible, MINOR for phase 0017 ⇒ `v0.4.0`), and the conformance suite
against this server after a real `migrate` (11 migrations) and `seed`: 36/36.
The push receiver was not driven through the running binary: there is no
operator path to a binding before 0019, and this phase adds no daemon wiring, so
the over-the-wire tests through a real `north.Server` and federation tokens are
the end-to-end evidence.
