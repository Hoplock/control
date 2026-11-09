# 0052 — the console's look, chosen with the operator — Learnings

## Summary
- **Shipped (`v0.39.0`, MINOR; no migration; `ext`/`server`/`contract` untouched):** the console's look is **Carbon**, dark first, approved by the operator in their words ("Approve A2L as is") after **3 rounds**. It is applied through `ui/src/styles/tokens.css` alone, with `DEFAULT_PREFS.theme` `dark` in `ui/src/prefs/prefs.ts`.
- **Accent** `--a-500`: light `#0b7189`, dark `#22d3ee` (electric cyan). **Canvas / surface:** light `#f4f6f8` / `#ffffff`; dark `#07090c` / `#13181f`. Type is a step denser (body 13/20, cells 12/18, labels 11/16 at 600). Radii are unchanged at 4/6/10.
- **Tooling:** `ui/e2e/design-directions.shoot.ts` with `ui/playwright.design.config.ts` photographs any candidate token file in the browser, and is never a CI job. `CONTRAST_TOKENS=<file>` points the contrast test at a candidate.
- **Queued from the operator:** **0059**, Fleet's health bar without patterns (their words: "I don't like the patterns in thr bar showing the health"; their choice: "Solid + glyph labels"). Queued prompts 0059–0063 became 0060–0064 (PLAN §10, fleet-bar note).
- **Decisions:** none added or amended; the register is unchanged. `ui/DESIGN.md` §1, §3.1–§3.5, §5 and §8 are revised in place (Details). PLAN §10 has 0052's row, the new 0059 row, the note and the mapping.
- **Gotchas:** with dark the default, a browser check that sets only `colorScheme` sees dark in a light run. `useDensity` (called by every `openScreen`) now also sets the theme to follow the system. A candidate's `@font-face` is stripped before injection: a second rule for the same family, with a URL relative to the page, breaks the face.
- **NEXT (0053):** build Policy in this look. Reference tokens only. Re-read DESIGN.md §3, because the sizes are smaller than when the earlier screens were written.

## Details

### Round log

Every round's images were committed in one commit and deleted in the next.
They are reachable at these SHAs under `ui/design/round-<n>/`, and the PR
(#152) shows them inline. In every round each candidate showed the same five
screens of the seeded fixture: Explain (`sess-e2e-hops`) in its first theme
and in its other theme, Audit records compact, Fleet compact, and the
gallery's `status` page. All were at 2×, 1280 px, full content height. Every
candidate passed all 135 contrast pairs before it was shown.

**Round 1 (`da271ab`): three directions.**
- **A — Carbon** (dark first). Blue-black near-black grounds, hairlines,
  2/4/6 px corners, denser type and a cyan accent. It answered the brief
  ("something more sleek like CrowdStrike's console, with a different
  color") directly.
- **B — Porcelain** (light first). A warm off-white ground, lifted white
  surfaces, 6/8/14 px corners, today's type and a magenta accent. The
  `magenta` stripe moved to rose so it would not match the accent stripe.
- **C — Terminal** (dark first). True black, 0/2/4 px corners, headings and
  labels in the mono face, and a lime accent. Allow was retuned cooler.
- *Operator's answers:* closest **A — Carbon**. From the others, take
  **"Porcelain's softer corners"**. The accent: **"Cyan, vivid"**. **"Dark
  first"**.

**Round 2 (`5dabafe`): Carbon with softer corners.**
- **A1** used Porcelain's corners: `--r-sm` 2→6, `--r-md` 4→8, `--r-lg`
  6→14.
- **A2** used corners in between: 2→4, 4→6, 6→10, which are today's radii.
- **A3** was A1 with the dark surfaces lifted one step: `--n-100`
  `#0f1318`→`#13181f`, `--n-200` `#161b22`→`#1b212a`, `--n-300`
  `#222933`→`#29313d`, `--n-50` `#0b0e12`→`#0d1015`. `--n-400` and
  `--n-500` moved one step lighter to hold contrast.
- *Operator's answers:* closest **A2**. Density **"Keep Carbon's"**. Accent
  **"Right as is"**. Problems: **"The fleet screen, I don't like the
  patterns in thr bar showing the health"**.

**Round 3 (`6226cfc`): A2 and A2 lifted.**
- **A2**, unchanged.
- **A2L**: A2's corners with A3's lifted dark surfaces (the same six steps as
  A3).
- *Operator's answers:* closest **A2L**. Asked whether it was the one:
  **"Approve A2L as is"**, with no further changes ("Nothing"). For the
  health bar, asked what should replace the patterns: **"Solid + glyph
  labels"**.

A2L is what shipped. Its full diff from the pre-0052 tokens is
`git diff v0.38.0 -- ui/src/styles/tokens.css`.

### Why the bar is a phase of its own, and why it runs at 0059

The patterns are drawn by `FleetChart.tsx` as SVG `<pattern>`s in
`--bg-surface` over each status hue. No token removes them, and 0052 changes
tokens alone. They also exist for a reason: DESIGN.md §2 and §8 need the bar
readable without colour. The operator's chosen replacement, a glyph and a
count on each segment, keeps that promise by other means. The new phase sits
before the consistency audit, so the audit checks the bar operators will
use, and after the last console screen, so nothing is built around it. That
renumbered five prompts rather than eleven.

### Changes to `ui/DESIGN.md`, and why
1. **§1 calibration** names the class the operator's brief pointed at: the
   dark, dense consoles security teams live in, CrowdStrike's being the one
   they named. It states dark first as the commitment. This is a calibration,
   not a model to imitate: no vendor's palette, layout or mark was taken.
2. **§3.1** gives the new neutral ramp, and its intro now reads blue-grey,
   near black in dark. The old "cool grey so the accent stays warm" was false
   with a cyan accent.
3. **§3.2's two argument rows** quote the new figures: white on the dark cyan
   is 1.8:1, so `--text-on-accent` is near-black there at 11:1; dark
   `--a-900` is the pale `#cffafe`, on which body text is 1.02:1. The
   mappings are unchanged.
4. **§3.3** gives the cyan ramp, why it was chosen, where it sits among the
   status hues, and why red is never the accent.
5. **§3.4** gives the retuned status values and why the dark ones are a step
   brighter and info a step bluer.
6. **§3.5** gives the denser scale, and why a label is semibold.
7. **§5** updates the accent stripe's row and adds a note that `teal` is
   nearest the default bar (cyan against teal is 1.03:1 in luminance).
8. **§8**: the console is dark by default, and an operator may choose light
   or the system.

§3.6 (radii unchanged), §3.7, §6 and §10 did not change.

### The one code change outside the token file

`DEFAULT_PREFS.theme` is now `dark`. That alone would have changed nothing:
`valid()` fell back to the literal `"system"` for a missing theme and never
read `DEFAULT_PREFS`. It now falls back to `DEFAULT_PREFS`, and accepts a
stored `"system"` as an explicit choice, so an operator who chose to follow
the system keeps that choice. `ui/test/prefs.test.ts` holds this. The theme
is applied by `main.tsx` before React renders. A visitor whose system is
light may see a light frame before the module runs; nothing changed there,
and the server does not write `data-theme`.

### The tooling, for a later look change
- `DESIGN_ROUND=<n> npx playwright test -c playwright.design.config.ts`
  photographs every `ui/design/round-<n>/<id>.css`. `DESIGN_ONLY` limits it
  to some ids. `DESIGN_EXTRA` adds screens from its `EXTRA` list. A
  candidate's header comment `first-theme: dark` picks its first theme.
- The candidates were generated from small override maps (only values
  changed), which guarantees "the same properties and selectors". The
  generator was a scratch script, not committed.
- It needs a fresh server, as every e2e run does (`HOPLOCK_E2E_DSN`), and
  the gallery build (`vite build -c vite.gallery.config.ts`).

### Baseline
The baseline was rewritten with `--update-snapshots=all` on a fresh database
and reproduced with no update on a second fresh database. Every screen
changed, because every screen takes the tokens.

### Cross-repo
No shared surface changed. The release changes what every Enterprise screen
looks like at its next pin (they take `:root`'s tokens), and dark is the
default, so a host's browser checks must choose a theme explicitly. A
downstream sync is queued:
`prompts/downstream/queued/control-PR#152-console-design-direction.md`.
Proxy: none.
