# 0052 — Management console 4: the console's look, chosen with the operator

## Read first
- `docs/PROTOCOL.md` — the session workflow, and §8 in full: "A console phase
  shows what it built" (images committed in one commit and deleted in the
  next, linked by commit SHA) and "Waiting for review is waiting, not
  polling", which governs the waits between rounds here.
- `docs/PLAN.md` — §2: **M20** (the console's design is specified, and
  `ui/DESIGN.md` is changed in the same PR rather than diverged from),
  **M21** (both pseudolocales still pass), **M11** (a deny and an outage are
  different colours, glyphs and words — this is what fences the accent),
  **M17** (`Not reported` is neither healthy nor failed), **M19** (in progress
  is never a fault; the supervised badge is information, never a warning) and
  **M23** (every phase is a release); §3 (`ui/`, the build committed to
  `internal/console/dist`); §10, the console-look renumbering note, which says
  why this phase exists and why it runs here.
- **`ui/DESIGN.md` — in full.** §1 (what "modern" means here, the calibration
  and the anti-goal), §2, §3 (every token this phase may change), §5 (the
  deployment stripes: none a status hue, the default is the accent), §6 (the
  status vocabulary), §8, §10 (the token lint, the contrast test, the
  baseline) and §11 (the font budget, no visual kits).
- `ui/src/styles/tokens.css` — whole. It is the only file whose values this
  phase changes.
- `docs/learnings/` — read summaries; open `0049-console-shell`,
  `0050-console-components-and-fleet` and `0051-console-explain-and-audit`
  **in full**: the token file and `light-dark()`, the contrast test, the
  browser harness and the seeded fixture, the screenshot baseline and how it
  is rewritten, and the screens this phase photographs.

## The series
The console is eleven phases (PLAN §10, the console-split and console-look
notes), each a release and a PR of its own:

| Phase | Builds |
| --- | --- |
| 0049 | the shell: build and embed, the mount under `/console/`, sign-in, tokens and themes, the catalogue machinery, the deployment and tenant chrome, and the static checks |
| 0050 | the component set, the browser checks, the seeded fixture and the RBAC harness, and Fleet |
| 0051 | Explain and the audit view |
| **0052** | the console's look: three directions in screenshots, then an interview with the operator, round by round, until one is chosen and applied through the tokens (this phase) |
| 0053 | Policy |
| 0054 | Inventory |
| 0055 | locks, quarantines and the directory's subjects |
| 0056 | Extensions and Reports |
| 0057 | stream gaps, the archive's lag and retention, in the audit view |
| 0058 | algorithm posture and the emergency runbook |
| 0059 | the consistency audit of every screen |

Build only this phase's row: it changes how the console looks, and adds no
screen.

## Objective
After 0051 the operator said they did not like the console's look and feel.
In their words, they want **"something more sleek like CrowdStrike's console,
with a different color."** Choose the console's visual identity **with the
operator**, not for them: show three distinct directions as screenshots of
screens already built, then interview them, round by round, each round with
its own screenshots, until they say plainly that one is right. Then apply it
through the token file, so every screen built so far and every screen
0053–0058 will build takes it.

## The brief, and what it does not license
- **Read the brief as a class, not a copy.** "Sleek, like CrowdStrike's
  console" reads as: dark-first, dense, flat, high-contrast, with one vivid
  accent and little decoration. `ui/DESIGN.md` §1 names consoles to sit beside
  **without imitating any of them**. No direction takes a vendor's logo,
  wordmark, layout or palette whole, and none is named after a vendor.
- **The accent is never a status hue** (§3.3, §3.4, M11). Red means denied in
  this product, so a red accent would make every primary button read as a
  denial. "A different color" is the operator's to choose, among hues that are
  not one of the five statuses, nor so close to one that the contrast test or
  a reader could confuse them. If the operator asks for red, explain why in
  one sentence and offer the nearest colours that are not a status.
- **Status keeps its meanings.** A direction may retune the five status hues
  so they sit well on its ground, but deny and outage stay different colours
  (§3.4), each keeps its own glyph (§6), and every one passes §10.2.
- **Tokens only.** This phase changes values in `tokens.css` and the tables in
  `ui/DESIGN.md` that state them. It changes no component, screen, layout or
  string. What tokens cannot express — an icon-only navigation (which §4
  forbids), a different top-bar layout, a new component shape — is out of
  scope here. If the operator asks for one, record the ask in their words and
  queue it as a numbered prompt after this one (`docs/PROTOCOL.md` §6,
  renumbering the queue).

## In scope

### A direction
- **A direction is a complete copy of `tokens.css` with its values changed:**
  the same custom properties, the same selectors, nothing added or removed.
  It changes any of these: the neutral ramp (§3.1), the semantic surfaces,
  text and borders (§3.2), the accent ramp (§3.3), the status hues (§3.4),
  the stripe colours (§5, the `accent` stripe following the accent), the type
  scale's sizes, weights and tracking (§3.5), the radii and the elevation
  (§3.6). Both themes are designed in every direction, whichever one it is
  shown in first.
- **Faces:** round one uses the bundled Inter and JetBrains Mono. Both are
  variable, so weight is a token. A different face is a later round's
  question, and only one whose Latin subset fits §11's 120 KB with the mono
  face, under a licence the console can bundle (§3.5, §11). It is never
  fetched at runtime.
- **Every direction passes the contrast test before the operator sees it.**
  Make `ui/test/contrast.test.ts` accept a token file's path in
  `CONTRAST_TOKENS` (default `src/styles/tokens.css`) and run it against each
  candidate. A candidate that fails is fixed or dropped, never shown.

### The screenshots
- **The same five for every direction, in every round,** so rounds and
  directions compare like for like. All at 2×, 1280px wide, full content
  height, against the seeded fixture (`ui/e2e/fixture/`, a fresh database):
  1. Explain, the two-hop session (`/console/explain/sessions/sess-e2e-hops`),
     in the direction's first theme;
  2. the same page in its other theme;
  3. Audit, records, compact (`/console/audit/records`), first theme;
  4. Fleet, compact (`/console/fleet`), first theme;
  5. the component gallery's `status` page (`/console/gallery?section=status`)
     in the first theme: the status vocabulary, where M11 is visible.

  Add any screen or state the operator asks to see, in that round and every
  later one.
- **How they are taken:** `ui/e2e/design-directions.shoot.ts`, run by
  `ui/playwright.design.config.ts` (`testMatch: /\.shoot\.ts$/`, the same
  `webServer` and launch flags as `playwright.config.ts`). It opens each page
  in a context with `bypassCSP: true` and the theme as `colorScheme`, waits
  for the page to be ready as `ui/e2e/screens.ts` defines, applies the
  candidate with `page.addStyleTag({ path })` over the console's own tokens,
  waits for fonts and finite animations, and writes the image. It is a tool,
  not a check: it asserts nothing, it is never a CI job, and its `.shoot.ts`
  name is outside the `*.spec.ts` that `TestEveryConsoleSpecRunsInCI`
  requires CI to run. The candidate is applied in the browser, so the server
  binds nothing new (`docs/PROTOCOL.md` §3), and `tokens.css` is untouched
  until a direction is approved.
- **Where they go:** each round's candidate files and images are committed
  together in one commit under `ui/design/round-<n>/` (`<id>.css`, and
  `<id>-<k>-<screen>-<theme>.png` for each of the five), and deleted in the
  next commit, so the module's tree never carries them (M23). They are linked
  by that commit's SHA. Show them to the operator directly as well, wherever
  the session can attach an image to the conversation.

### Round one: three directions
- **Three genuinely different directions**, not one palette with three
  accents. At least one answers the brief directly: dark-first, near-black
  grounds, dense, flat, a vivid accent. The other two are real alternatives
  that might suit the product better. Name each with a short descriptive
  name, never a vendor's, and give each three lines: what it is, which part
  of the brief it answers, and what it gives up.
- **Present all three together:** the fifteen images, grouped by direction,
  and for each direction its three lines and its accent and grounds in both
  themes as values.

### The interview
- **After each round, ask; never assume.** Ask at most four questions at a
  time. Each is concrete, with options and room for the operator's own words.
  Use the session's question tool where it has one, and plain chat otherwise.
  Round one's questions are about direction: which is closest, what to take
  from the others, the accent's colour and intensity, dark or light first.
  Later rounds narrow: how dense, how much contrast between surfaces, corners,
  borders against shadow, type weight, a specific screen that looks wrong.
- **Each later round answers the last one's answers.** Show one to three
  variants and the same five screenshots of each, plus anything the operator
  asked to see. Give each variant a short list of the token values that
  changed from the variant it came from (`--a-500` dark `#7c86ef` →
  `#22d3ee`; surfaces one step darker), so the operator sees what their words
  did.
- **Narrow, don't wander.** By round three, aim to show one direction in at
  most two variants, unless the operator asks to start again, which is
  theirs to ask.
- **Approval is the operator's sentence, never an inference.** Continue until
  they say plainly that one variant is the one ("approved", "this one", "go
  with B2"). Silence, "looks good so far", a question, or praise for a single
  screen is not approval. Between rounds the session waits for the operator,
  and does not poll or re-post (`docs/PROTOCOL.md` §8).
- **Keep the record as you go.** The PR description holds a round log: for
  each round, the SHA-linked images, the operator's answers summarised in
  their words, and what changed next. The learnings file keeps the same log
  for every later session.

### Applying the approved direction
- `ui/src/styles/tokens.css` becomes the approved file.
- **`ui/DESIGN.md` states it:** the §3.1–§3.4 tables at the new values in both
  themes, §3.5 and §3.6 where the type, radius or elevation changed, the §5
  stripe table if the stripes changed, and §1's calibration paragraph if the
  operator's brief changes what the console sits beside. Revise in place, and
  say which sections changed and why in the learnings.
- **If the operator wants dark first,** `DEFAULT_PREFS.theme` in
  `ui/src/prefs/prefs.ts` becomes `dark`, and DESIGN.md §8's "theme follows
  the system by default" says so. This is the one code change outside the
  token file this phase may make.
- **The console is rebuilt and re-photographed:** `npm run build` (the
  committed `dist`), the whole baseline rewritten with
  `--update-snapshots=all` on a fresh database, compressed losslessly as
  0050's was, and reproduced with no update on a second fresh database.

## Out of scope
- Any new screen, component, layout change or string, and the account menu's
  placement.
- A component's shape, or the navigation and top bar's arrangement. If the
  operator asks for one, queue it as its own numbered prompt.
- A vendor's logo, wordmark, palette taken whole, or name.
- A locale other than English.

## Acceptance criteria
- **Round one** showed three directions, each a complete token file passing
  the contrast test in both themes, each with the five screenshots, presented
  together with their names, three lines and key values.
- **Every round** is in the PR description and the learnings: its images
  linked by commit SHA, the operator's answers in their words, and what
  changed in the next round.
- **The operator approved one direction in their own words,** quoted in the
  PR description and the learnings.
- `tokens.css` is the approved direction, and `ui/DESIGN.md` states its
  values. Nothing outside the token file changed, except the default theme if
  the operator chose dark first.
- The contrast test (unchanged pairs), the token lint, the string lint, the
  catalogue keys, the unit tests and the budget pass. Fonts are unchanged
  unless a face was approved, and then within 120 KB.
- **Every browser check passes** on the rewritten baseline: axe in both
  themes, the baseline, no network, reduced motion, `en-XA` and `en-XB`,
  Fleet, Explain and Audit, and RBAC. The baseline reproduces on a fresh
  database.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Open the PR as a **draft after round one**, so every
round is visible in one place. Mark it ready once the direction is applied and
every check passes. Its screenshots are the rounds' images and, for the
result, the rewritten baseline beside the base's, light and dark side by side
(§8). Move this prompt to `implemented/`, and add
`docs/learnings/0052-console-design-direction-learnings.md`. Its summary block
MUST give the approved direction's name, its accent and its canvas and
surface grounds in both themes, the number of rounds, and every ask of the
operator's queued as a later prompt. Its Details MUST record every change to
`ui/DESIGN.md`, with the reason, and the round log.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names. The changelog entry describes the new
look in a deployment operator's words.

**Cross-repo.** Enterprise's screens take the console's tokens from `:root`
(0049), so its screens change look at its next pin with no code of theirs.
Queue the downstream sync (`docs/CROSS-REPO-PROTOCOL.md` §4.1): the release,
the new values, and that a host's screens reference tokens only, never a
colour of their own.
