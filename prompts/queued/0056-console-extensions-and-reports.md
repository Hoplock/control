# 0056 — Management console 8: Extensions and Reports

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — §2: **M15** (Enterprise extends this repository, and every
  extension point has a real default, so an absent one is not a broken one),
  **M8** (retention never outruns a registered archive and never waits on a
  sink), **M11**, **M21** (a report is named by its code where no catalogue
  names it), **M27** (a host's catalogue keys are `<provider>:<key>`),
  **M20** and **M23**; §7 at "Export" and at "Reports".
- **`ui/DESIGN.md` — in full.**
- `docs/learnings/` — read summaries; open `0049-console-shell` and
  `0050-console-components-and-fleet` **in full**: the shell, the component
  set, and how a screen is registered with every harness. Open `0004` (the
  `ext` points and their registration), `0026-audit-sink-delivery` (each
  sink's lag on its registration's row of `GET …/extensions`: its summary,
  and `docs/PLAN.md` §7 at "Export"), and `0044` **in full** (the report
  routes this screen consumes). Read the summaries of `0039` and `0040` for
  `started` and `start_error` on the action handler's and the identity sync's
  rows of `GET …/extensions`, and of `0025` for the archive's lag, which
  shares the sinks' names.

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
| **0056** | Extensions and Reports (this phase) |
| 0057 | stream gaps, the archive's lag and retention, in the audit view |
| 0058 | algorithm posture and the emergency runbook |
| 0059 | the consistency audit of every screen |

Build only this phase's row. Build every screen from 0050's component set and
register it with 0050's harnesses; a component this phase needs that the set
lacks is added to the set, never built inside the screen.

## Objective
Let an operator debugging behaviour see what is in play — which extension
points have an implementation, whether each started, and how far behind each
downstream consumer is — and generate any report a registered pack offers.
The console degrades honestly when an extension point is absent: an
Enterprise feature that is not installed is not shown as broken.

## In scope
- **Extensions** — which `ext` points have an implementation registered
  (0004). An operator debugging behaviour must be able to see that Enterprise
  is in play.
  - **Each registered sink shows its delivery lag** for the tenant (0026),
    from its registration's `lag` on `GET …/extensions`: `streams_behind`,
    `oldest_unconfirmed_at`, `failing` and `skipped_by_retention` — the same
    names as the archive's lag, so one component renders both. Ship that
    component into the set here; 0057 renders the archive's lag with it. A
    sink that is failing says so, and a sink that a retention pass overtook
    shows how many records it missed and that the pass's
    `operator.audit.retained` record explains the gap. Neither is an outage or
    tampering: a sink is a downstream consumer, and the local store is still
    the record (M8, M11). A sink caught up says so plainly. With no sink
    registered, the sink point shows what Control does instead and no lag.
  - **The action handler and the identity sync** (0039, 0040): a registered
    one whose `Start` succeeded shows as running; one whose `Start` failed
    shows as registered and not started, with the error's kind
    (`start_error`, beside `started`), and neither as an outage. With none
    registered, the point shows what Control does instead (M15).
- **Reports** — every registered report pack this tenant can generate, from
  `GET …/reports` (0044), and a form that generates one through
  `POST …/reports/generate` and saves the file. The form is built from the
  descriptor: the period, each parameter (a closed set as a choice, required
  ones marked), and the formats it lists. A report is named through the
  catalogue of the host that registered it, under its `<provider>:` keys
  (M27), and by its code where no catalogue names it (M21). The listing's
  `provider` is the pack's `ext.Registration.Provider` (by convention a module
  path), not the host's `<owner>/<name>`: a binary has one host, so the
  catalogue is that host's. Post `provider` and `report` back exactly as
  listed; neither is ever a path segment. A provider listed with an error is
  shown as unavailable, never as having no packs. With no provider
  registered, the screen says that the audit view is the reporting available,
  which is the point's own answer (M15), and is not shown as broken. A
  generation that ends broken is shown as failed, never offered as a saved
  report.

## Out of scope
- The archive's lag and retention's answers in the audit view (0057).
- Enterprise screens: report schedules and history are Enterprise's own,
  served through the shell from `/host-console/<provider>/` (M27). Listing and
  generating a registered pack is this screen, and Control's.
- Every other screen: the series table's.
- Re-opening the design, and any locale other than English.

## Acceptance criteria

**Function**
- The console degrades honestly when an extension point is absent: an
  Enterprise feature that is not installed is not shown as broken.
- With a fake action handler registered, the extensions screen shows it as
  running; with one whose `Start` failed it shows it as registered and not
  started, with the error's kind (`start_error`), and neither as an outage.
  With none registered, the point shows what Control does instead (M15).
  Assert each, and the same for the identity sync.
- With two fake sinks registered, one behind and failing and one overtaken by a
  retention pass, the extensions screen shows each one's lag on its own row:
  the failing one as failing, the overtaken one with its skipped count. With
  none registered, no sink lag is shown. Assert each.
- With a fake report provider registered, the Reports screen lists its pack
  and generates it with a parameter and a format chosen. A second provider
  listed with an error is shown as unavailable, and its absence of packs is
  never shown as "none". A generation that ends broken is shown as failed.
- The RBAC harness and the route test stay green with this phase's calls in
  them.

**Design and localisation**
- Both screens are registered with every browser check, and each passes: axe,
  the screenshot baseline (both themes, both densities, loading, empty and
  error), offline, reduced motion, and both pseudolocale runs.
- Each screen has all four states implemented and captured. A failing sink and
  an extension that did not start never render as an outage (M11).
- The lag component is in the set with every state designed, in both themes.
- 0049's static checks stay green, the catalogue key test among them.

## Definition of Done & hand-off
**Screenshots in the PR** (`docs/PROTOCOL.md` §8, "A console phase shows what
it built"): the description shows every screen and every state this phase
builds or changes, light and dark side by side.

Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0056-console-extensions-and-reports-learnings.md`. Its summary
block MUST give the routes each screen consumes, the lag component's place in
the set (0057 uses it), and how a host's report names reach the catalogue.
Details MUST record **any change made to `ui/DESIGN.md` in this PR**, with the
reason.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
