# 0059 — Management console 11: Fleet's health bar without patterns

## Read first
- `docs/PROTOCOL.md` — the session workflow, and §8 in full: "A console phase
  shows what it built" (the screenshots this PR owes) and "Waiting for review
  is waiting, not polling".
- `docs/PLAN.md` — §2: **M20** (the console's design is specified, and
  `ui/DESIGN.md` is changed in the same PR rather than diverged from),
  **M11** (a deny and an outage are different colours, glyphs and words),
  **M17** (`Not reported` is neither healthy nor failed), **M19** (in
  progress is never a fault), **M21** (every string from the catalogue; both
  pseudolocales pass) and **M23** (every phase is a release); §3 (`ui/`, the
  build committed to `internal/console/dist`); §10, the **fleet-bar
  renumbering note**, which says why this phase exists and why it runs here.
- `ui/DESIGN.md` — §2 ("status is never colour alone"), §3.4 and §6 (the
  status hues and glyphs the segments use), §7 (components, and a chart's
  four states), §8 ("charts … are readable in greyscale"), §9 (numbers through
  `Intl`, logical properties), §10 (the checks, the baseline) and §11 (no chart
  library; the two charts).
- `docs/learnings/` — read every summary; open
  `0050-console-components-and-fleet` (Fleet's rules, the chart, the baseline
  and how it is rewritten) and `0052-console-design-direction` (the round log
  in which the operator asked for this, and the look it must sit in) **in
  full**.

## The series
The console is twelve phases (PLAN §10, the console-split, console-look and
fleet-bar notes), each a release and a PR of its own:

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
| 0057 | stream gaps, the archive's lag and retention, in the audit view |
| 0058 | algorithm posture and the emergency runbook |
| **0059** | Fleet's health bar: solid segments, each labelled with its status glyph and count, in place of patterns (this phase) |
| 0060 | the consistency audit of every screen |

Build only this phase's row. It changes one component of one screen.

## Objective
In 0052's interview the operator said of Fleet, in their words: **"I don't
like the patterns in thr bar showing the health."** Asked what should replace
them, they chose **"Solid + glyph labels: solid-colour segments separated by
gaps, each labelled with its status glyph and count, so colour isn't needed."**

Today every segment of Fleet's health bar but the healthy one carries an SVG
pattern (hatching, dots, bars, crosses) in the surface colour over its status
hue. The patterns are there on purpose: DESIGN.md §2 says status is never
colour alone, and §8 says the chart is readable in greyscale. Replace them
with what the operator chose, and keep both promises by other means.

## In scope
- **`ui/src/screens/FleetChart.tsx`**: no `<pattern>` and no `url(#…)` fill.
  Each segment is its status hue, solid (`--s-allow`, `--s-warn`,
  `--s-outage`, `--s-info`, `--s-deny`, as today's `SEGMENT_STATUS` maps
  them), and consecutive segments are separated by a visible gap of the
  canvas or surface between them (a space token, never a magic number).
- **Each segment is labelled with its status glyph and its count**: the
  glyph from `StatusGlyph` (the §6 shape for its tone: filled circle, filled
  triangle, dashed hollow circle, half-filled circle, filled square), and the
  count through `f.number` (DESIGN.md §9). This is what makes the bar
  readable without colour: two segments of different status never look the
  same in greyscale, because their glyphs differ.
  - A label must be legible on its segment in both themes, or sit beside the
    bar (above or below, aligned to its segment) where it cannot. Decide
    which, say why in the learnings, and hold it in the contrast test or a
    browser check. A glyph drawn in its own hue on a ground of the same hue is
    invisible: if the label sits on the segment, it is drawn in a colour that
    contrasts with every status hue (≥ 3:1, WCAG 1.4.11, both themes), and
    that pair goes in `ui/test/contrast.test.ts`.
  - **A segment too narrow for its label** (one proxy of hundreds) is still
    drawn, at least a minimum width so it is seen, and its label is left to
    the legend, which already names every state with its glyph, word and
    count. Define the threshold and the minimum width as tokens (§3.6:
    layout dimensions are tokens), and unit-test the layout rule (which
    segments carry a label, each segment's start and width, the minimum
    width never pushing the total past the track) in a pure function beside
    `segmentCounts` in `ui/src/screens/fleetModel.ts`.
- **Unchanged:** the segments' order (`CHART_SEGMENTS`), the fill from the
  inline start (a right-to-left page reads it from the right, so `en-XB`
  still mirrors it), the legend (glyph, word, count for every state, zeros
  included), the screen reader's sentence (`fleet.chart.summary`), the data
  source (the server's fleet summary), and the chart's place on the screen.
  Remove the legend's patterned swatch, which now describes a pattern that
  no longer exists; the legend keeps its glyph.
- **`ui/DESIGN.md`**: §8's sentence on charts and §11's description of
  Fleet's health bar say how it now reads in greyscale (a glyph and a count
  on, or beside, every segment wide enough, and the legend for every state);
  §3.6 names the new layout tokens. Revise in place.
- **Strings:** none new should be needed; if the label needs an accessible
  form beyond the existing sentence, it comes from the catalogue (M21).
- **Checks:** Fleet's baseline images (`fleet-*` in `ui/e2e/baseline/`, both
  themes and densities, and the states it defines) rewritten with
  `--update-snapshots=all` on a fresh database, compressed losslessly as 0050's
  were, and reproduced on a second fresh database with no update. Add to
  `ui/e2e/fleet.spec.ts`: the bar has no `pattern` element and no `url(`
  fill; every non-zero segment wide enough carries its state's glyph; and,
  rendered with `filter: grayscale(1)`, each labelled segment still carries a
  distinct glyph shape. The gallery is untouched unless the chart joins it.

## Out of scope
- Any other chart, including the audit view's counts (0051), which are one
  neutral ink and carry no pattern.
- Fleet's table, filters, tree, states or any other screen.
- A charting library (DESIGN.md §11), or a third chart.
- New status colours or glyphs: the five hues and §6's glyphs are the
  vocabulary (0052 settled their values).

## Acceptance criteria
- Fleet's health bar has no pattern: solid segments in their status hues,
  separated by gaps, each wide enough segment labelled with its §6 glyph and
  its count; a narrower one still visible, and named by the legend.
- The bar reads in greyscale: no two labelled segments share a glyph, and
  the legend pairs every state's glyph with its word and count.
- The label colour (if on the segment) passes ≥ 3:1 against every status hue
  in both themes, asserted in `ui/test/contrast.test.ts`; the layout rule is
  unit-tested; the new tokens pass the token lint.
- Every browser check passes: axe in both themes, the baseline (Fleet
  rewritten, nothing else changed), no network, reduced motion, `en-XA`
  (nothing clipped at +40%) and `en-XB` (the bar mirrored), Fleet and RBAC.
  The baseline reproduces on a fresh database.
- `ui/DESIGN.md` §3.6, §8 and §11 say what was built.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. The PR shows Fleet's bar before and after, light and
dark side by side at 2× (§8), with a greyscale rendering of the after.
Move this prompt to `implemented/`, and add
`docs/learnings/0059-console-fleet-health-bar-learnings.md`, whose summary
says where the labels sit and why, the narrow-segment rule and its tokens.

**Release.** The next MINOR after the latest tag (`make release-check` names
it). The changelog says, in a deployment operator's words, that Fleet's
health bar is now solid segments labelled with their status and count.

**Cross-repo.** No shared surface changes, but the release changes what
Enterprise's binary serves; queue the downstream sync
(`docs/CROSS-REPO-PROTOCOL.md` §4.1).
