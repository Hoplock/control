# 0059 — Fleet's health bar without patterns — Learnings

## Summary
- **Shipped (`v0.46.0`, MINOR; no migration; north-bound API unchanged at `5`; `ext`/`server`/`contract` untouched; no Go change):** Fleet's health bar as the operator chose it in 0052 — solid segments in their status hues, a gap of surface between two, each wide enough one labelled with its §6 glyph and its count; no `<pattern>`, and the legend's patterned swatch gone.
- **Where the labels sit, and why: on the segment**, from its inline start, in a new ink `--text-on-status` (`--n-0`: white in light, near-black in dark). One ink per theme reads on all five hues — ≥ 5.47:1 in light (`--s-deny`), ≥ 7.48:1 in dark (`--s-info`) — so the label cannot be misread as another segment's, which a label beside the bar could be when two narrow segments meet. Held as **text** (4.5:1) in `ui/test/contrast.test.ts`, which also covers the glyph's 3:1.
- **The narrow-segment rule** (`layoutBar` in `ui/src/screens/fleetModel.ts`, `ui/test/fleet.test.ts`): a segment narrower than `--chart-segment-min` (6px) is drawn at it, the width coming out of the wider segments (repeated until none is squeezed under it), so the bar always ends at its track; one at least `--chart-label-min` (72px) carries its label, a narrower one is named by the legend alone; gaps are `--chart-segment-gap` (`--sp-2`). A track too narrow for every minimum shares the room equally, and shrinks gaps it cannot hold.
- **How it is drawn:** the bar is measured (`ResizeObserver`, before first paint) and its three tokens read with `getComputedStyle`, so the SVG has no `viewBox` (one unit = one CSS pixel) and nothing stretches; each label is HTML in a `<foreignObject>`, so it inherits `direction` and starts at the inline start in `en-XB` by itself. `StatusGlyph` gained `ink` (draws in `currentcolor`, not its hue).
- **Checks added** (`ui/e2e/fleet.spec.ts`): no `pattern`/`url(`; each segment's fill equals its legend glyph's colour and carries its tone's glyph and count; a 1-in-501 segment at the minimum, unlabelled, named by the legend; greyscale glyph **shapes** compared by pixels (`shapeOf`/`unlike`); the bar mirrored in `en-XB`. `openScreen` gained `scale`.
- **Decisions:** none added or amended; register unchanged. PLAN §10 (0059's row) revised in place; **`ui/DESIGN.md` §3.2, §3.6, §8, §11 changed** (Details).
- **Gotchas:** the generic en-XA clip and en-XB mirror checks skip everything inside an `<svg>` — the bar's mirroring is held only by its own test in `fleet.spec.ts`. A `<style>` cannot be injected under the console's CSP; set `document.documentElement.style` (CSSOM) instead.
- **NEXT (0060):** the consistency audit sees this bar; nothing else on any screen changed.

## Details

### What changed, where

| What | Where |
| --- | --- |
| the bar | `ui/src/screens/FleetChart.tsx` (`useBarGeometry`, the SVG), `Fleet.module.css` (`.bar`, `.segment[data-tone]`, `.segmentLabel`; `.swatch` and the pattern classes gone) |
| the layout rule | `ui/src/screens/fleetModel.ts`: `BarGeometry`, `BarSegment`, `layoutBar` |
| the ink glyph | `ui/src/components/Status.tsx` (`StatusGlyph`'s `ink`), `Status.module.css` (`.glyph[data-tone][data-ink]`) |
| tokens | `ui/src/styles/tokens.css`: `--text-on-status`; `--chart-segment-gap`, `--chart-segment-min`, `--chart-label-min` |
| unit tests | `ui/test/fleet.test.ts` (new), `ui/test/contrast.test.ts` (five pairs) |
| browser checks | `ui/e2e/fleet.spec.ts` (four tests, one per theme for the first), `ui/e2e/open.ts` (`scale`) |
| baseline | `ui/e2e/baseline/fleet-{light,dark}-{comfortable,compact}.png`, `fleet-{loading,empty,error}-{light,dark}.png` — every Fleet image, because every Fleet state shows the summary; nothing else changed |

### Why on the segment and not beside it

The prompt allowed either. Beside the bar, each label would be aligned to its
segment's start, and two adjacent segments only a little wider than their
labels would put two labels side by side with nothing to say which segment
each belongs to except position — exactly the reading the greyscale promise
is meant to make unnecessary. On the segment, a label is inside the shape it
names. The only cost is contrast against five hues in two themes, and the
token file already had the answer in `--text-on-accent`'s argument: the light
hues are all dark (white on them is 5.47–6.79:1) and the dark hues all bright
(near-black on them is 7.48–10.43:1). So the ink is `--n-0` in both themes,
a semantic token of its own so a component never reaches for the ramp.

### Why the bar is measured rather than drawn in a stretched `viewBox`

0050's bar was `viewBox="0 0 100 6"` with `preserveAspectRatio="none"`: fine
for rectangles, wrong for a glyph or a number, which it would stretch. The
alternatives each ran into a rule: an HTML flex row of segments needs a width
per segment, and an inline `style` is refused by the string lint (§10.9);
percentage `x`/`width` attributes cannot express "the minimum width" or "wide
enough for a label", which are pixels. So the bar measures itself and lays
out in pixels: `useBarGeometry` reads the width with `getBoundingClientRect`
and the three tokens with `getComputedStyle(...).getPropertyValue`, which
returns a custom property with its `var()` already substituted (`--sp-2` →
`2px`). It runs in `useLayoutEffect`, so the first paint has the bar, and a
`ResizeObserver` relays it out on resize; a geometry equal to the last one
does not re-render.

The 72px threshold is a glyph (10px), a gap (4px), five tabular digits at
`--t-label`, and 8px of padding each side. A count with more digits on a
segment barely over the threshold would be cut by the `<foreignObject>` — a
fleet that large is far past anything the fixture or any test draws, and the
legend still carries every count.

### The greyscale check, and how it was calibrated

"Each labelled segment still carries a distinct glyph shape" is checked on
pixels, not on markup: the page is set to `filter: grayscale(1)` (CSSOM — the
console's CSP refuses `addStyleTag`), opened at `scale: 4`, every one of the
five states substituted in (`live 3, stale 2, unreachable 2, pending 2,
revoked 2`), and each segment's glyph screenshotted with a 2px margin.
`shapeOf` decodes the PNG in the page (`createImageBitmap` over a `Blob`, so
no URL is fetched) and marks every pixel that differs from the corner's
ground by more than half the box's greatest difference — a shape, whatever
its colours. `unlike` is the share of pixels two shapes differ in at the
best of 81 small alignments, so a sub-pixel offset is not a difference.

Measured on this container's Chromium: a segment's glyph against its own
legend glyph (hue on surface, a different colour pair, the same shape)
differs in 0.00–0.06% of pixels; the closest two different glyphs (filled
circle and filled square) in 5.74%; every other pair in 11.8–27.7%. The test
asserts the control (< 1%) as well as the distinction (> 3%), so the measure
is seen to say "alike" before it is trusted to say "different". It also
asserts each glyph's greyscale contrast against its segment (> 0.3; measured
0.55–0.71).

**Mutations run** (each check seen failing, then restored): the label glyph
drawn without `ink` (in its own hue) fails the greyscale test on contrast;
the segments placed from the left whatever the direction fails the `en-XB`
test.

### Changes to `ui/DESIGN.md`, and why
1. **§3.2** adds `--text-on-status` to the table and its argument beside
   `--text-on-accent`'s, with the ratios.
2. **§3.6** names the three layout tokens and what each is for.
3. **§8** says how each chart reads in greyscale: the audit counts are one
   ink; the health bar's segments carry glyph and count, and the legend names
   every state.
4. **§11** describes the health bar as built: order, fill, gap, label, the
   narrow segment, the legend, and that the layout is a pure function over
   the measured bar.

### Baseline
Fleet's ten images were rewritten with `--update-snapshots=all` on a fresh
database, compressed losslessly with oxipng at level 4 with safe stripping
(here through its Python binding, `pyoxipng`; pixels checked identical
before and after: 964 KB → 739 KB), and the whole suite then run on a second
fresh database with no update.

### Cross-repo
No shared surface changed. The release changes what Enterprise's binary
serves (Fleet's bar), and Enterprise's screens take `:root`'s tokens and
Control's components, so they gain `--text-on-status`, the three chart
tokens and `StatusGlyph`'s `ink`. A downstream sync is queued for
`hoplock/enterprise` (`prompts/downstream/queued/`). Proxy: none.
