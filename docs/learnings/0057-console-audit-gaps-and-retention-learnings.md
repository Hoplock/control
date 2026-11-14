# 0057 — the console's stream gaps, the archive's lag and retention — Learnings

## Summary
- **Shipped (`v0.44.0`, MINOR; no migration; north-bound API unchanged at `5`; `ext`/`server`/`contract` untouched):** a session's holes in its replay, the per-proxy gaps view `/console/audit/gaps[?proxy=&cause=&scope=sessionless]`, the retention view `/console/audit/retention`, and an `event` filter on the records view.
- **Routes and fields consumed** (`ui/src/api/routes.ts`): `GET …/audit/gaps` (`audit:read`; `session`, `proxy`, `cause`, `no_session`, `order`, `limit`, `after`; reads `gap_cause`, `gap_records`, `gap_bytes`, `gap_first_at`, `gap_last_at`, `gap_kinds`, `gap_critical`, `gap_record_ids[_truncated]`, `refusal_code`, `refusal_message`, `session_id`, `stream`, `proxy_id`, `reported_at`); `GET …/audit/sessions/{session}/loss` (`audit:read`; `loss[].cause|reports|records|bytes`); `GET …/extensions` (`extension:read`; the `ArchiveStore` row's `registered`, `registrations`, `lag`); `POST …/audit/retention` (`audit:retain`; `stream` + `through_seq` or `before`; answer `deleted`, `through_seq`, `key`, `held_by_archive`, `archived_through_seq`; `409 retention_not_archived {stream, archived_through_seq}`); `GET …/audit/records?event=operator.audit.retained` (`operator.stream`, `operator.deleted`, `after`, `operator.held_by_archive`, `operator.archived_through_seq`).
- **The state decided for a refused gap:** `Refused at ingest` — the warn row (filled triangle, `--s-warn`), `STATUS.refusedAtIngest`; neither the deny nor the outage state, asserted in `ui/test/status.test.tsx`. `evicted` is `Evicted`, the outage row (`STATUS.evicted`). An unknown cause is `Not reported`.
- **Key files:** `ui/src/screens/gapModel.ts` (pure: `placeGaps`, `gapStatus`, `gapOfRecord`, `lossByCause`, `gapsByStream`, `retainedOf`; `ui/test/gaps.test.ts`), `Gaps.tsx` (`GapStatus`, `GapFacts`, `gapEvent`, the per-proxy view), `Retention.tsx`, `Replay.tsx`; fixture `ui/e2e/fixture/host/retention.go`, `fixture/audit.ts` (`seedGaps`); spec `ui/e2e/audit-gaps.spec.ts`.
- **Decisions:** none added or amended; register unchanged. PLAN §7 (one sentence) and §10 (0057's row) revised in place; **`ui/DESIGN.md` §6 and §7 changed** (Details).
- **Gotchas:** a pass by `before` measures *received* time (the server's clock), so an instant before the run deletes nothing and is not held; a pass the archive holds back entirely deletes nothing and writes **no** `operator.audit.retained` record. The fixture's archive holds every batch until `audit.archive.attempt_timeout` (6h), so it is behind, never failing. Streams are named `proxy:<id>` and `control`.
- **NEXT (0058):** the retention view and the gaps view are under the audit section; a new audit view adds its id to `AuditViews`. The extensions screen's archive row is now registered in the fixture (`e2e-archive`), and a key store (`e2e-keys`) is registered.

## Details

### What each view does with what it reads

- **Replay** (`/console/audit/sessions/<id>/replay`) reads, beside the records
  page it already read, the session's gaps (`…/audit/gaps?session=&order=asc`,
  one page of 500) and its loss (`…/audit/sessions/{session}/loss`).
  `placeGaps` merges them: each record at its `recorded_at`, each gap at its
  `gap_first_at`, records first at one instant, and the gap report's own
  record (`event: logging.gap`) dropped from the steps — its place is its
  span. A report without a span is listed beneath the timeline
  (`Unplaced`), never placed. A gap is placed only on the records page its
  span starts in (the page's first record bounds it below when there is a
  previous page, its last above when there is a next), so a paged session
  does not pile every gap onto one page. Steps are `?step=<record id>` for
  records and gaps alike; a gap step shows `GapFacts` above the terminal.
  The terminal ends the run at each gap and shows it where it falls
  (`data-stream-gap`), exactly as 0051 ended it at a missing capture — every
  gap breaks the run, whatever its `gap_kinds`, because the span is a time the
  store has nothing for. The partial notice sums per cause from the loss
  route (two `evicted` reports read as one line of 52 records in 2 reports),
  adds the critical count from the gaps page when it holds every report, and
  names an unread loss or unread gaps as gaps of their own. With both read
  and empty, the rail says the proxy reported nothing missing *and* that a
  verifying chain is not proof of that — never "nothing missing".
- **Gaps** (`/console/audit/gaps`) is one page of `…/audit/gaps`, grouped by
  `stream` and placed by span within the page (`gapsByStream`), each group a
  timeline whose events name their session (a replay link) or "belongs to no
  session". `scope=sessionless` maps to `no_session=true`. The facts of every
  report on a stream are behind one disclosure, drawn with an SVG chevron:
  the platform's triangle is a font glyph outside the bundled subsets, which
  0051 found renders differently from host to host.
- **Retention** (`/console/audit/retention`) has three parts. The archive
  panel reads `…/extensions` only with `extension:read` and renders nothing
  unless the `ArchiveStore` row is registered; a failed read is an inline
  error and the screen's partial state. The pass form is offered only with
  `audit:retain`; it confirms through `ConfirmDialog` by the stream's name.
  `409 retention_not_archived` closes the dialog and is shown beside the form
  (`ErrorMessage`, refused, with the sentence naming the stream and
  `archived_through_seq`) with "Delete as far as the archive holds", which
  switches the form to the instant mode, prefilled with now. Every other
  failure stays inline in the dialog. A held pass reads `Behind` beside
  "Held back by the archive". The trail is one page of
  `…/audit/records?event=operator.audit.retained`; its Archive column appears
  only when some row carries `operator.held_by_archive`.
- **Records**: `event` joins `RECORD_FILTERS` (and so the cursor's
  selection, which the server already bound). A gap report's row shows its
  cause beside its event; the drawer shows `GapFacts` for a gap report and
  `RetainedFactsOf` for a retention pass.
- **FeedLag's link** to "the retention records" pointed at
  `audit/records?kind=operator.audit.retained`. That is an event, not a kind;
  `readRecordFilters` drops an unknown kind, so the link opened every record.
  It now opens `audit/retention`, and the constant is `RETAINED_EVENT`
  (re-exported by `gapModel`). `ui/test/extensions.test.tsx` and the
  extensions spec expect the new link.

### Changes outside the new screens

- **`ConfirmDialog` clears what was typed as it closes, not as it opens.**
  Cleared on open, a dialog opened a second time showed the last name typed —
  the confirmation already met, its destructive button enabled — for the
  frames before the effect ran, and a name typed in those frames was then
  wiped. The retention form opens the dialog twice in one flow (the refused
  pass, then the pass by instant), and the browser checks typed into that
  window; an operator pressing Enter there would have confirmed without
  typing. Every screen's typed-name confirm shares the fix.
- **The record drawer marks an attribute's key as an identifier** (`Id`,
  mono, `translate="no"`). Its label was the raw key, which the `en-XA` check
  found unaccented the first time a registered screen opened the drawer
  (`audit-record-gap`).
- `e2e/explain-audit.spec.ts` counts grace's command records as three: the
  gap session adds one.
- The baseline of every audit screen was retaken with `--update-snapshots=all`:
  the two new view links and the `Event` filter changed `audit-blocked` and
  `audit-supervisor` by less than the 1% tolerance, so a `changed` run kept
  their old images.

### Changes to `ui/DESIGN.md` in this PR, and why

1. **§6, table:** the warn row gains `refused at ingest` / `Refused at
   ingest`; the outage row gains `evicted` / `Evicted`. The prompt required a
   state for a refused gap that is neither a deny nor an outage.
2. **§6, new paragraph** on a stream gap: why `Refused at ingest` is the warn
   row (the store lacks records it was handed — degraded) with its own word
   (nothing was decided about anyone's access, so not a deny; the records
   arrived, so not an outage; and not `Rejected`, which is the proxy refusing a
   document on the other side of the same wire); that `Evicted` is
   outage-class (M11); an unknown cause is `Not reported`; critical is marked;
   a gap is never tampering and a verifying chain is never "nothing missing".
   Alternatives considered: the info row (rejected — info is "pending, in
   progress", and a refused record is lost for good: upstream does not replay
   it) and a sixth hue (rejected — re-opening the palette is out of scope, and
   §3.4 says the five are the whole vocabulary).
3. **§7, new paragraph** on stream gaps and retention: a gap by its span, a
   report without one listed apart, gaps as replay steps, the hole never
   joined across, the partial state with summed loss, the per-proxy view, the
   archive lag on the retention view, `retention_not_archived` beside the form
   offering the instant pass, held passes said so, nothing archive-related
   without an archive, and the typed-name confirm.
4. **§7, the feed lag paragraph:** a skip's explanation is linked to the
   retention trail (was "linked in the audit view", which the broken link
   above did not honour).

### The fixture

- `host/retention.go` registers `e2e-archive`, whose `Archive` and
  `ArchiveCaptures` block until the attempt's context ends and then fail, and
  `e2e-keys`, an in-memory Ed25519 `ext.KeyStore` (retention is refused
  without one, 0024; Control's CA ignores a registered key store until 0062,
  so nothing else changes). `control.yaml` sets
  `audit.archive.attempt_timeout: 6h`, so no attempt ends in a run: three
  streams behind, `failing: false`, seven captures waiting. Shutdown cancels
  the attempt's context, so the server still stops.
- `audit.ts`'s `seedGaps` delivers through `/v1/logs/batch`, as px-core-01:
  `sess-e2e-gaps` (grace → `app-01.core.internal`, a target no inventory screen
  opens, so no other baseline moved for its records) with seven records, two
  `evicted` reports (40 and 12 records, sent after the session) and one
  `refused` report (2 records, one critical, `invalid_record`, ids
  `rec-px-0411`, `rec-px-0412`, sent when refused); and as px-dmz-03 one
  `evicted` report of no session (120 records, 07:00–07:30).
- Both retention flows run in the `checks` project, even as `prepare` steps:
  the server refuses the pass by sequence (nothing deleted or recorded) and
  holds the pass by instant back to sequence 0 (nothing deleted, and nothing
  recorded, see the gotcha). A refused north-bound request is logged, never
  recorded, so the RBAC harness's `403`s do not move the records baseline.
  The trail with a held pass is a substituted answer
  (`audit-retention-trail`), because the seed runs no pass that deletes.

### A capture race CI found, fixed here

The first CI run failed the screenshot baseline on `audit-retention`'s loading
state (1020 px tall against a 1134 px baseline). That state holds only the
trail's read open, and a loading screenshot does not wait for the network to
settle, so whether the archive's panel had arrived or was still its skeleton
depended on which answer came first. Reproduced locally by delaying the
extensions answer by 1.5 s (1014 px); the state's `ready` now also waits for
the archive's panel, and the same delayed run passes. A state that holds one
read open must wait for every other read on the screen.

### Registered with the harnesses

`ui/e2e/screens.ts` (`GAPS`): `audit-replay-gaps` (partial by its data),
`audit-replay-gap-refused` (`?step=` on the refused hole), `audit-gaps` (four
states; partial is the server's page marked cut), `audit-gaps-proxy`,
`audit-record-gap`, `audit-retention` (loading, error, partial = the archive's
lag unreadable; the `auditRetention` mutation for the RBAC harness),
`audit-retention-refused`, `audit-retention-held`, `audit-retention-bare`
(extensions answer substituted with `BARE`), `audit-retention-trail`. CI:
`console-e2e` gains "stream gaps and retention" (`e2e/audit-gaps.spec.ts`).

### Noticed, not changed

- A pass by instant that the archive holds back *entirely* answers
  `held_by_archive: true` and writes no `operator.audit.retained` record, so
  the trail cannot show that a pass was attempted and held. PLAN §7 says a
  pass by `before` "is sealed and recorded as any pass is, and deletes nothing
  when the archive is at or behind the marker": whether "recorded" covers the
  pass that deleted nothing is ambiguous. The console says what the answer
  says; worth a ruling in a server phase.
- The partial notice's heading is the frame's generic "Some of this could not
  be read", which is not quite what a hole is (the records were never stored).
  A per-screen heading would be a change to `ScreenFrame` for every screen;
  0060's consistency audit is the place to weigh it.
- The capture retention pass (`POST …/audit/captures/retention`) is not in the
  console; the prompt asked for the prefix pass. Its answer carries
  `held_by_archive` the same way, so it would sit in the same form.

### Cross-repo

No shared surface changed (`ext`, `server`, the contract and the cross-repo
protocol are untouched), but the release changes what Enterprise's binary
serves and the status vocabulary its screens are built from, so a downstream
sync for `hoplock/enterprise` is queued (the PR's `## Cross-repo impact`).
Proxy: none; `Hoplock/proxy#71`'s record is consumed as specified.
