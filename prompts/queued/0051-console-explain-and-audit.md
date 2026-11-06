# 0051 — Management console 3: Explain and the audit view

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — §2: **M4** (every decision is explainable, durable and
  addressable), **M8**, **M11**, **M19** (a supervised deployment, and the
  shapes a supervisor's word takes), **M20**, **M21**, **M26** and **M23**;
  §5.6 (the policy lifecycle: explain and replay); §7 at "Query" and at "A
  record may belong to no session".
- **`ui/DESIGN.md` — in full.** §4 says Explain and Audit read acceptably at
  768px, and §2 that both are fully keyboard-navigable.
- `docs/learnings/` — read summaries; open `0049-console-shell` and
  `0050-console-components-and-fleet` **in full**: the shell, the component
  set, and how a screen is registered with every harness. Open `0019` (**the
  API this consumes** — explain, the audit routes and each one's required
  role), `0010` (the audit query), `0036` (the audit query paged by
  recorded-at, stream and sequence, a limit above the maximum refused), `0037`
  (`…/audit/blocked-commands` paged; the per-session reads that say
  `truncated`), `0035` (`…/sessions/{session}/decisions` says `truncated`) and
  `0048` (`acting_operator` on a supervisor's records).
- `docs/API-VERSIONING.md`, the open `attributes` rule 0048 wrote: render a key
  you know, and skip one you do not.

## The series
The console is ten phases (PLAN §10, the console-split note), each a release
and a PR of its own:

| Phase | Builds |
| --- | --- |
| 0049 | the shell: build and embed, the mount under `/console/`, sign-in, tokens and themes, the catalogue machinery, the deployment and tenant chrome, and the static checks |
| 0050 | the component set, the browser checks, the seeded fixture and the RBAC harness, and Fleet |
| **0051** | Explain and the audit view (this phase) |
| 0052 | Policy |
| 0053 | Inventory |
| 0054 | locks, quarantines and the directory's subjects |
| 0055 | Extensions and Reports |
| 0056 | stream gaps, the archive's lag and retention, in the audit view |
| 0057 | algorithm posture and the emergency runbook |
| 0058 | the consistency audit of every screen |

Build only this phase's row. Build every screen from 0050's component set and
register it with 0050's harnesses; a component this phase needs that the set
lacks is added to the set, never built inside the screen.

## Objective
The one screen that sells the product: **paste a session id and get the full
explanation of why access was allowed or denied** — and the audit trail it
came from, one deep link away.

## In scope
- **Explain** — a session or decision id in, the whole story out: identity,
  matched rule, mapping version, grant, obligations, route and hops. Deep
  links from the audit view. Ship the timeline (DESIGN.md §7) into the
  component set here, and render the hop path with 0050's tree. 0054 adds,
  beside the mapping version, the directory the subject's groups came from.
- **Audit** — query by subject, target, time, kind, severity; the blocked-
  command view; session replay for recorded sessions. Every list pages
  through the bounded-list shape (M26) and never assumes one answer holds the
  whole tenant, and every filter serialises into the URL under `/console/`
  (`/console/audit/records?subject=…`, never `/audit?…`). Ship the time range
  picker (absolute and relative, DESIGN.md §7) into the component set here.
  0056 adds where a proxy's stream has a hole, the archive's lag and
  retention's answers to this view.
- **Simple audit counts**, as hand-written SVG (DESIGN.md §11: no chart
  library until there is a third chart), readable in greyscale.
- **What a supervisor did (M19).** The supervised badge 0049 put in the chrome
  links to the audit view filtered to that supervisor's actions. An operator
  must be able to see what was done to their deployment from above, in their
  own console.
- **The supervisor's word.** Where a record of a supervisor's act carries
  `acting_operator` (0048), the audit view shows that name beside the `sup-`
  principal. It labels the name as the supervisor's word, and never as a
  person this deployment knows: it is not a subject, it links to no subject,
  and nothing filters or authorises on it (M19's shapes). A record without the
  key shows the principal alone, as before. This is the open `attributes` rule
  `docs/API-VERSIONING.md` states: render a key you know, and skip one you do
  not.
- **Keyboard and width.** Explain and Audit are fully keyboard-navigable, and
  read acceptably at 768px (DESIGN.md §2, §4).

## Out of scope
- Gaps, the archive's lag and retention (0056); the directory's facts on
  Explain and on a record's subject (0054); the emergency runbook's negotiated
  sessions (0057).
- Every other screen: the series table's.
- Re-opening the design, and any locale other than English.

## Acceptance criteria

**Function**
- Both screens work against the seeded database, extended with the sessions,
  decisions and records they show.
- End-to-end, headless: an id pasted into Explain shows identity, matched rule,
  mapping version, grant, obligations, route and hops, for an allow and for a
  deny; a deep link from an audit record opens its explanation.
- A paged audit query follows `next` to its last page, and a cut answer says
  so.
- A record carrying `acting_operator` shows it beside its `sup-` principal as
  the supervisor's word, linked to no subject; a record without it shows the
  principal alone. The supervised badge opens the audit view filtered to the
  supervisor's actions. Assert each.
- The RBAC harness and the route test stay green with this phase's calls in
  them.

**Design and localisation**
- Both screens are registered with every browser check, and each passes: axe,
  the screenshot baseline (both themes, both densities, loading, empty and
  error), offline, reduced motion, and both pseudolocale runs.
- Each screen has all four states implemented and captured, and a deny and an
  outage are never rendered alike (M11).
- The timeline and the time range picker are in the component set with every
  state designed, in both themes.
- 0049's static checks stay green, the catalogue key test among them.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0051-console-explain-and-audit-learnings.md`. Its summary block
MUST give the routes each screen consumes, its deep links (they are part of the
console's surface, DESIGN.md §4), and the components it added to the set.
Details MUST record **any change made to `ui/DESIGN.md` in this PR**, with the
reason.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
