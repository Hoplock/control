# 0057 — Management console 9: stream gaps, the archive's lag and retention, in the audit view

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — §2: **M8** (audit is append-only and tamper-evident;
  retention never outruns a registered archive, and never waits on a sink),
  **M11** (an outage is not a deny, and a refusal is neither), **M20**,
  **M21** and **M23**; §7 at "A gap in a proxy's stream is a record, and it is
  not a break in the chain", at "Retention has to remove a chain's TAIL" and
  at "Export", for the archive's lag on `GET …/extensions` and the retention
  answers the audit view shows.
- In the **Hoplock Proxy repository**, `api/README.md` "When records do not
  arrive" specifies the `logging.gap` record that `Hoplock/proxy#71` added and
  0019 serves. It is cited here and never restated.
- **`ui/DESIGN.md` — in full.** §6 has no state for a refused gap yet, and
  this phase decides one; §7 defines the partial state.
- `docs/learnings/` — read summaries; open `0049-console-shell`,
  `0050-console-components-and-fleet` and `0051-console-explain-and-audit`
  **in full**: the shell, the component set, how a screen is registered with
  every harness, and the audit view and replay this phase adds to. Read the
  summary of `0056-console-extensions-and-reports` for the lag component.
  Open `0025-archive-before-retention-learnings.md` and
  `0026-audit-sink-delivery-learnings.md` (their summary blocks), `0019` (the
  `logging.gap` records stored and indexed by session, cause and span, and
  the routes that serve them), `0036` (`…/audit/gaps` paged) and `0037`
  (`…/audit/sessions/{session}/loss` and the per-session reads that say
  `truncated`).

## The series
The console is eleven phases (PLAN §10, the console-split and console-look
notes), each a release
and a PR of its own:

| Phase | Builds |
| --- | --- |
| 0049 | the shell: build and embed, the mount under `/console/`, sign-in, tokens and themes, the catalogue machinery, the deployment and tenant chrome, and the static checks |
| 0050 | the component set, the browser checks, the seeded fixture and the RBAC harness, and Fleet |
| 0051 | Explain and the audit view |
| 0052 | the console's look: three directions in screenshots, then an interview with the operator, round by round, until one is chosen and applied through the tokens |
| 0053 | Policy |
| 0054 | Inventory |
| 0055 | locks, quarantines and the directory's subjects |
| 0056 | Extensions and Reports |
| **0057** | stream gaps, the archive's lag and retention, in the audit view (this phase) |
| 0058 | algorithm posture and the emergency runbook |
| 0059 | the consistency audit of every screen |

Build only this phase's row. It adds to the audit view and replay 0051 built,
through 0050's component set and harnesses, and renders the archive's lag
with the component 0056 shipped.

## Objective
Make the audit view honest about what it does not hold: where a proxy's stream
has a hole and why, what the archive holds, and what retention waited for —
each as the fact it is, and none of them as a deny, an outage or tampering it
is not.

## In scope
- **Where a proxy's stream has a hole, and why** (`Hoplock/proxy#71`, served
  by 0019). A `logging.gap` is shown at its **span**, from `gap_first_at` to
  `gap_last_at`, and never at its own timestamp, which is often after the
  session ended. Show how many records are missing, their kinds, and how many
  were critical. A gap that hid something critical is marked as critical.
  Show the two causes as different facts:
  - **`evicted`**: this server never received the records. The proxy was cut
    off from it for longer than its disk window could hold. That is
    outage-class, and it reads as one (M11).
  - **`refused`**: this server received the records and refused them itself.
    Show its own answer (`refusal_code`, `refusal_message`) and the refused
    record ids. It is neither a deny nor an outage, and it must not read as
    either. DESIGN.md §6 has no state for it yet, so decide one in DESIGN.md in
    this PR (M20), and render it through 0050's status component.

  A session with a hole is the **partial** state (DESIGN.md §7): some data and
  a named gap, never an error page, and never shown as complete. Where a
  session has two reports of one cause, show their sum. Replay over a span
  that a gap covers shows the hole, and never splices the capture on either
  side together. A gap is never rendered as tampering, and a stream whose
  chain verifies is never rendered as "nothing missing" (PLAN §7). Gaps that
  belong to no session appear in a per-proxy view of the stream, placed by
  span.
- **What the archive holds, and what retention waited for** (0025). With an
  archive registered, the audit view shows its lag for the tenant from the
  archive row of `GET …/extensions` (`lag`: `streams_behind`,
  `oldest_unconfirmed_at`, `failing`, `captures_waiting`), with 0056's lag
  component, and says plainly when the archive is caught up. It is a delivery
  fact, never an outage and never tampering (M11, PLAN §7). A retention pass
  the archive held back says so: the answer's `held_by_archive` and
  `archived_through_seq` after a pass the operator ran here, and the
  `operator.audit.retained` record's `operator.held_by_archive` in the trail.
  A pass by sequence refused with `409 retention_not_archived` is shown as a
  refusal that deleted nothing, naming the stream and `archived_through_seq`,
  and offers the pass by instant, which deletes as far as the archive holds.
  With no archive registered, none of this appears.
- A retention pass deletes records, so it is a destructive action and confirms
  as one (0050's confirm).

## Out of scope
- Each sink's lag on the extensions screen (0056).
- Every other screen: the series table's.
- Re-opening the design beyond the state this phase must decide, and any
  locale other than English.

## Acceptance criteria

**Function**
- Against a seeded fixture, the audit view shows an `evicted` gap and a
  `refused` gap in one session's timeline at their spans, as different states,
  and neither of them as denied. It shows two reports of one cause summed. It
  shows the session as partial rather than complete. And replay across the gap
  shows the hole. A session-less gap appears in the per-proxy view. Assert
  each.
- With a fake archive registered and behind, the audit view shows its lag; a
  retention pass by sequence beyond it renders `retention_not_archived` as a
  refusal that deleted nothing; and a pass by instant renders as held back by
  the archive. With none registered, no lag is shown. Assert each.
- The RBAC harness covers the retention pass: a role without its permission
  sees no control for it, and the API refuses the call — assert the API
  refusal, not just the hidden button. The route test stays green.

**Design and localisation**
- `ui/DESIGN.md` §6 carries the state decided for a refused gap, and the status
  component renders it; a test asserts it is neither the deny nor the outage
  state.
- Every screen this phase changed passes every browser check again, with its
  baseline updated deliberately and each changed screen named in the PR; the
  partial state of a session with a hole is in the baseline.
- 0049's static checks stay green, the catalogue key test among them.

## Definition of Done & hand-off
**Screenshots in the PR** (`docs/PROTOCOL.md` §8, "A console phase shows what
it built"): the description shows every screen and every state this phase
builds or changes, light and dark side by side.

Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0057-console-audit-gaps-and-retention-learnings.md`. Its
summary block MUST give the routes and fields the audit view now consumes, and
the state decided for a refused gap. Details MUST record **every change made
to `ui/DESIGN.md` in this PR**, with the reason.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
