# 0070 — a retention pass that deleted nothing is recorded too — Learnings

## Summary
- **Shipped (`v0.57.0`, MINOR; no migration; north-bound API unchanged; `ext`/`server`/`contract` untouched):** `audit.Emitter.RetainPrefix` records a pass by instant that deletes nothing — the instant reaches no record past the marker, or the archive holds it back entirely — in the pass's own transaction. The capture pass already did.
- **Its `operator.audit.retained` record:** `operator.stream`; `operator.deleted` `0`; `after` = the existing marker's `through_seq`, or `0` with none; `operator.through_hash`, `operator.key`, `operator.signature` = the existing marker's, **absent** with none; with an archive, `operator.held_by_archive` and `operator.archived_through_seq`. Message: `stream S: nothing retained[, still retained through N][, held back by the archive]`.
- **No marker is written, moved or re-signed** for such a pass, so `audit-verify` sees no change. The answer is unchanged in shape and values. A refused pass (`409 retention_not_archived`, store-refused, `503 retention_key_not_configured`, `409 retention_unverified`) still records nothing.
- **Key files:** `internal/audit/retention.go` (`recordPrefixPass`, one writer for both paths, built from the answer so the two agree); tests in `internal/audit/retention{,_archive}_test.go`, `internal/httpapi/north/archive_test.go`, `internal/archive/sink_test.go`; console `ui/src/screens/Retention.tsx`; checks `ui/e2e/screens.ts` (`HELD`, `HELD_TRAIL`), `audit-gaps.spec.ts`, `mutations.spec.ts`.
- **Decisions:** M8 revised in place (register row unchanged, `live`); PLAN §7 and §10 revised in place; `ui/DESIGN.md` §7's retention paragraph revised. No decision added.
- **Gotchas:** a real pass by instant now writes a record, so the `checks` project must never run one — the held answer and the trail after it are substituted there. A no-op pass does not run the start check, so it can name an unsigned marker's (empty) seal; it signs nothing, so it launders nothing (Details).
- **NEXT must know:** owes `hoplock/enterprise` a downstream sync (its sinks and archive see `operator.deleted` `0` records); `hoplock/proxy` none.

## Details

### The change

Both early returns of the pass by instant collapsed into one: the store's
`Retention` answers the zero marker with `ErrNotFound`, so `n <= prior.ThroughSeq`
covers "no marker and the instant reaches nothing" (`0 <= 0`) as well as "the
instant stops at or behind the marker". That branch now copies the marker's
facts into the answer, as it did, and calls `recordPrefixPass`, which the
deleting path calls too. One function writes the record from the answer, so
"the record and the answer agree on every fact both carry" holds by
construction; `agrees` in `retention_test.go` asserts it for every shape (no
marker, marker, held, deleting).

`put` drops empty values, so with no marker `through_hash`, `key` and
`signature` are absent rather than empty strings, while `after` is the string
`"0"` and present.

### Why no north-bound version move

`docs/API-VERSIONING.md`: the answer is unchanged, the event is an existing
member of the closed set (`operator.audit.retained`), and `attributes` is an
open map whose keys are not members. One record more, in more cases, with the
meaning it always had, moves nothing.

### Why not `### Breaking`

M23's "incompatible" is Go's API compatibility, read from the implementer's
side; no public package changed (`make release-check`: "no change to the public
API"). 0026 set the precedent: sinks newly called, no `### Breaking`. The
behaviour change is listed in the release's body, as M23 asks of a change no
API diff can see.

### Tests

- `TestAPassByInstantThatTakesNothingIsRecorded`: no marker → record with
  `after` `0` and no seal, no marker row, verifies from 1; then a marker → a
  second no-op pass names it, and the marker row (`row_to_json`) is byte for
  byte unchanged.
- `TestAPassByInstantTheArchiveHoldsBackEntirelyIsRecorded`: archive at 0 →
  one record, `deleted` `0`, held, archived through `0`; no `audit_retention`
  row at all; the stream verifies with no marker.
- `TestARefusedPassRecordsNothing`: store refusals (head, at and behind the
  marker), no key store (including a pass that would delete nothing — the key
  store check comes first), and `retention_unverified` by sequence and by
  instant, each leaving the control stream's head (count and last hash) where
  it was. `retention_not_archived` is covered by the existing
  `unchanged` helper, which counts the control stream.
- `TestAPassThatDeletedNothingReachesASinkAndSkipsNothing`
  (`internal/archive`): the sink holds the record, and no position, lag or log
  line counts a skip.
- Over the wire: `TestAPassTheArchiveHoldsBackEntirelyIsInTheTrailOverTheWire`
  reads the record back through `GET …/audit/records?event=`.
- `TestARetentionPassByInstantTakesWhatWasReceivedBeforeIt` asserted "the no-op
  is not an act" (2 records); it now expects 3.

### The console and the browser checks

- `PassOutcome` says the pass is recorded whether or not it deleted: the
  existing "sealed by" sentence when it deleted, a new one
  (`retention.result.recordedNothing`) when it did not — it signed no new
  marker, so no key is shown even when the answer carries the old marker's.
  `data-retention-recorded` (`sealed` / `nothing`) marks which.
- The trail's empty state has new keys (`retention.trail.none.*`); the old
  `retention.trail.empty.*` keys are gone, never reused (DESIGN.md §11).
- `audit-retention-held`: the refused pass by sequence is still the server's
  own; then `HELD` (the answer) and `HELD_TRAIL` (the trail it re-reads) are
  substituted before the pass by instant, so the screenshot shows the trail
  with the held pass, as the real one would. `audit-retention-trail`'s
  substituted trail gains a third row, a pass that deleted nothing.
- The real pass runs in `mutations.spec.ts`, which reads `audit-verify` through
  the fixture's own binary before and after (`execFileSync`, as
  `fixture/standing.ts` runs the CLI) and asserts the line is identical and
  names no "retained through".
- Playwright's `fill` on a `datetime-local` refuses seconds; fill
  `YYYY-MM-DDTHH:mm`.
- Baselines retaken deliberately: `audit-retention` (light, dark,
  partial ×2), `-refused`, `-bare`, `-held`, `-trail` (×2 each) — every one
  shows the trail, so every one's empty sentence or rows changed.
  `audit-retention-loading-light` was rewritten by `--update-snapshots=all`
  below tolerance and restored; `-error` and `-loading` do not show the trail's
  empty state.

### Noticed, not changed

- A pass that deletes nothing reads the marker without the stream's append
  lock and runs no start check (`checkPrefixStart`), as before 0070. So over a
  marker that does not verify it succeeds and its record echoes that marker's
  facts. It signs nothing, so it cannot launder the marker (the reason
  `retention_unverified` exists), and `audit-verify` still reports the marker
  as a break. Under a concurrent pass, its `after` can name the marker as it
  was when read. Both are worth a ruling only if a reader starts treating a
  no-op record as vouching for the marker.
- `retention.confirm.chain` ("from the signed marker the pass leaves") is
  shown before the operator knows whether the pass will delete; a pass that
  deletes nothing leaves no new marker. Left as is: the prompt changed the
  trail's and the answer's sentences only.

### Cross-repo

No shared surface changed, but every registered `ext.AuditSink` and
`ext.ArchiveStore` is now handed `operator.audit.retained` records with
`operator.deleted` `0`, which explain no gap. A downstream sync for
`hoplock/enterprise` is queued
(`prompts/downstream/queued/control-PR#<n>-retention-records-a-pass-that-deleted-nothing.md`).
Proxy: none.
