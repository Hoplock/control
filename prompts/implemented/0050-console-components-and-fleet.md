# 0050 — Management console 2: the component set, the browser checks and Fleet

## Read first
- `docs/PROTOCOL.md` — session workflow, and §3 on debug endpoints (a fixture
  path is never a route the server binds).
- `docs/PLAN.md` — §2: **M2**, **M11** (a deny and an outage are different
  things, and the console must not paint both red), **M17** (a capability that
  is stale, undated or absent is `Not reported`), **M19** (a rolling upgrade is
  in progress, never a fault), **M20**, **M21**, **M26** (every list is bounded
  by its caller and says where it stopped) and **M23**; §3 (`ui/`); §4 at
  "Configuration distribution (proxy D18)", for the configuration state each
  proxy reports.
- **`ui/DESIGN.md` — in full.** This phase builds §6 (the status vocabulary),
  §7 (the component inventory and the four states), §8 (accessibility) and §10
  checks 3–8.
- `docs/learnings/` — read summaries; open `0049-console-shell` **in full**: it
  is the shell, the toolchain, the token file, the locale helpers and the way
  a phase adds a screen, and this phase builds on all of it. Open `0028` and
  `0029` (the bounded-list shape and its mechanism, which the data table pages
  through), `0031` (`GET …/proxies` and `…/fleet/config/rollouts`, paged by
  proxy id), `0006` (proxy health, zones and relay registrations), `0045` (the
  fleet view's names: a zero instant absent, `liveness` in seconds) and
  `0019` (the route table: every fleet route and its required role).

## The series
The console is ten phases (PLAN §10, the console-split note), each a release
and a PR of its own:

| Phase | Builds |
| --- | --- |
| 0049 | the shell: build and embed, the mount under `/console/`, sign-in, tokens and themes, the catalogue machinery, the deployment and tenant chrome, and the static checks |
| **0050** | the component set, the browser checks, the seeded fixture and the RBAC harness, and Fleet (this phase) |
| 0051 | Explain and the audit view |
| 0052 | Policy |
| 0053 | Inventory |
| 0054 | locks, quarantines and the directory's subjects |
| 0055 | Extensions and Reports |
| 0056 | stream gaps, the archive's lag and retention, in the audit view |
| 0057 | algorithm posture and the emergency runbook |
| 0058 | the consistency audit of every screen |

Build only this phase's row. Every later screen is built from what this phase
ships, and is checked by the harnesses this phase builds, so its learnings are
read in full by every phase after it.

## Objective
Ship the one component set every screen is built from, and the browser checks
that hold every screen to `ui/DESIGN.md`, so that a later phase adds a screen
by registering it rather than by re-deciding what a table, a status or an
error looks like. Prove both on the first screen: **Fleet**.

## In scope

### The component set (DESIGN.md §7)
- Every primitive and composite DESIGN.md §7 lists, each with **every** state
  designed — default, hover, focus-visible, active, disabled, loading, error,
  empty, and both themes — except three that ship with the screen that first
  needs them, into this same set: the timeline and the time range picker
  (0051) and the diff view (0052).
- **The status vocabulary is the one in DESIGN.md §6**, rendered by a single
  component, never colour alone, and it encodes decisions this plan already
  made: a **deny** and an **outage** are different colours and different words
  (M11 — a console that paints both red re-creates the confusion M11 exists to
  prevent); a rolling upgrade reads as *in progress*, never as a fault (M19);
  *not reported* is its own state and never renders as healthy (M17, PLAN §4).
  A state a later phase needs is added to §6 and to this component, never
  rendered beside it.
- **Every screen defines four states** — loading (a skeleton matching the
  final layout, so nothing shifts), empty (what would be here, why it is not,
  the one action), error (what failed, the correlation id, retry — and it says
  *outage*, never *denied*, per M11) and partial (some data, a named gap).
  Build them as one frame every screen uses.
- **The data table pages through the bounded-list shape (M26)**: it follows
  `next`, says when an answer was cut (`truncated`), and never assumes one
  answer holds the whole tenant. Sticky header, pinned first column, sort,
  column visibility, row selection, per-row actions, and a filter bar whose
  state serialises into the URL under `/console/` (DESIGN.md §4).
- **Destructive actions look destructive** — activating a bundle, killing a
  session, revoking a grant confirm by typing the object's name. One confirm
  component, which every later destructive action uses. A failed mutation
  reports inline, never by a toast alone.
- **Errors from a code plus parameters.** The component that renders an API
  or compiler error from its code and parameters through 0049's helper, and a
  useful fallback for a code the catalogue does not know, naming the code and
  the correlation id rather than an empty string or a crash.
- **Accessibility is part of done, not a later pass** — WCAG 2.2 AA in both
  themes, visible focus on everything, full keyboard operation with focus
  trapping in dialogs, accessible names on icon-only controls, real table
  markup, and `prefers-reduced-motion` honoured.

### The browser checks (DESIGN.md §10)
Each is a CI job this phase adds, and each runs over **a registry of screens**,
so a later phase's screen is covered by registering it, with its states and
fixtures, and by nothing else:

1. axe assertions on every screen in the E2E run, both themes, zero violations
   at `serious` or above (§10.3);
2. a committed screenshot baseline — every screen, both themes, both
   densities, plus loading, empty and error states — diffed in CI, updated
   deliberately in the PR that changes the design (§10.4);
3. a test that loads the console with all external origins blocked and
   asserts it renders identically (§10.5);
4. a test asserting no transition exceeds 0ms under reduced motion (§10.6);
5. the `en-XA` pseudolocale run over the screenshot set: no string renders
   unaccented, and nothing clips or overflows at +40% (§10.7);
6. the `en-XB` mirrored run: nothing leaks a physical CSS property (§10.8).

### The fixture and the RBAC harness
- **A seeded database** every screen's E2E run works against:
  `hoplock-control seed` (0019) and the north-bound API, extended by each
  later phase for its own screen. Never a route the server binds
  (`docs/PROTOCOL.md` §3's debug-endpoint rule).
- **RBAC is asserted at the API, not at the button.** A harness that signs in
  as each role, and for each registered screen asserts that a role without a
  permission sees the read-only view and that **the API refuses** each
  mutating call the console hides. Each later phase adds its screen's mutating
  calls to it.
- **No API route is reachable from the console that RBAC would deny.** A test
  over the console's API client (0049): every call it makes names a route in
  the north-bound route table, and is offered only to a role that holds that
  route's permission.

### Fleet
- Proxies, zones, health, last heartbeat, contract version, which have live
  relay registrations (0006), and each proxy's configuration state as the
  proxy **reported** it (0019, proxy D18): desired and running version, and
  `applied`, `pending_restart` (with the settings that are waiting on a
  restart), `rejected` or `fetch_failed` (with `last_error`). A proxy that has
  never reported is shown as its own state, never as `applied`.
- A fleet with one unreachable node is *partial*, not an error page.
- The fleet health chart is hand-written SVG (DESIGN.md §11: no chart library
  until there is a third chart), readable in greyscale.
- The algorithm-floor coverage and each build's profile offers are 0057's,
  added to this screen there.

## Out of scope
- Every other screen: the series table's.
- Enterprise screens. They inherit these tokens and this component set; a
  second visual identity inside one console is the thing M20 exists to
  prevent.
- Dashboards and charts beyond fleet health and simple audit counts (0051).
- Re-opening the design. Correct DESIGN.md where it is wrong — in this PR, with
  the reason — but do not redesign it.
- Any locale other than English.

## Acceptance criteria

**Function**
- Fleet works against the seeded database, through 0029's paging, and shows
  each configuration state above, a never-reported proxy as its own state, and
  one unreachable node as partial. Assert each.
- The RBAC harness runs against Fleet: an auditor session sees the read-only
  view, and the API refuses each mutating call the console hides — assert the
  API refusal, not just the hidden button.
- The route test passes: no API route is reachable from the console that RBAC
  would deny.

**Design** — each of these fails the build, not the reviewer's patience:
- Axe reports zero `serious`+ violations on every screen, in both themes.
- The screenshot baseline is committed and CI diffs clean: every screen × both
  themes × both densities, plus the loading, empty and error state of each.
- Every screen has all four states implemented and captured — a screen with no
  empty state is an incomplete screen, not a minor gap.
- The offline test renders the console identically with all external origins
  blocked; no request leaves the page at runtime.
- Reduced motion collapses every transition to 0ms.
- The deployment identity (name, id, health, accent stripe) is legible in a
  screenshot of any screen, and two deployments are distinguishable from that
  screenshot alone — this is asserted in the baseline, not eyeballed.
- A deny and an outage are rendered with different colours **and** different
  words, asserted in a test (M11). A rolling upgrade renders as in-progress,
  not as a fault (M19). A capability that is stale, undated or absent renders
  as `Not reported`, not as healthy (M17).

**Localisation**
- The pseudolocale run is clean: no screen renders an unaccented string, and no
  screen clips or overflows at +40% expansion, in both themes.
- The mirrored pseudolocale run reports no physical-property leak.
- Every API and compiler error the console can surface is rendered from its
  code and parameters — asserted with a fixture carrying a code the catalogue
  does not know, which must render a useful fallback naming the code and the
  correlation id rather than an empty string or a crash.
- 0049's static checks stay green.

## Definition of Done & hand-off
**Screenshots in the PR** (`docs/PROTOCOL.md` §8, "A console phase shows what
it built"): the description shows every screen and every state this phase
builds or changes, light and dark side by side. Every component of the set is
shown in each state it defines, as well as Fleet. The baseline this phase
commits (DESIGN.md §10.4) is in the module's tree, so every `go get` of the
module carries it (M23): keep it to one scale and compressed, and give its size
in the learnings. From this phase on, a screen the baseline covers is shown by
the baseline's own files.

Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0050-console-components-and-fleet-learnings.md`. Its summary
block MUST give the routes Fleet consumes, where the component set lives, and
**how a later phase registers a screen with every harness**: its states and
fixtures in the browser checks, its seed data, its mutating calls in the RBAC
harness, and the command that updates its baseline. It also says how a host's
screens use this component set rather than bringing their own (M20).

Details MUST record how each browser check is wired into CI, and **any change
made to `ui/DESIGN.md` in this PR**, with the reason.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
