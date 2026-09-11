# Hoplock Control — Console Design System

> This document is **binding on phase 0016** and on every change to `ui/`
> afterwards. PLAN **M20** is the decision that makes it so. It exists because
> "make it look good" is not a requirement anybody can meet or fail, and a
> console built to acceptance criteria that are all functional will be correct
> and ugly — which, for the one screen an operator opens during an incident, is
> its own kind of incorrect.
>
> Read with `docs/PLAN.md` §2 (M20, M2, M18, M19, and **M21** — the console is
> localisable from its first screen, and English is the only locale that ships)
> and `prompts/queued/0016-management-console.md`.

---

## 1. What "modern" means here, precisely

It does not mean decorated. This is an operator console for an infrastructure
access product: the people in it are reading audit trails at 02:00, answering
"why was I denied", and publishing a policy that can lock a fleet out. Every
visual decision serves reading dense, consequential data quickly and being sure
of what you are looking at.

So "modern" is four concrete commitments, each of which a reviewer can check:

1. **Restraint.** One accent colour, a neutral ramp, and status colours that are
   reserved for status. Borders and spacing carry structure; shadows do not.
2. **Density without crowding.** Tables are compact by default because operators
   scan them, and the space that is spent is spent on the vertical rhythm that
   makes a dense table readable rather than on padding around it.
3. **Real dark mode.** Both themes are designed, not one inverted. Operators live
   in terminals; dark is a first-class theme that ships from day one.
4. **Speed you can see.** Skeletons over spinners, optimistic navigation, no
   layout shift after load. A console that flashes empty and then reflows reads
   as broken even when it is fast.

**The calibration** — the class of tool this should sit beside without imitating
any of them: Linear, Grafana, Tailscale's admin, Vercel's dashboard. **The
anti-goal**, stated so it can be pointed at in review: a Bootstrap admin
template. Card-in-a-card panels, a coloured page header, an icon sidebar with no
labels, a modal for anything consequential, and default component-kit blue.

---

## 2. Principles

- **Status is never colour alone.** Every status carries a shape or a glyph and a
  word as well as a hue. This is an accessibility requirement and an operational
  one: a screenshot pasted into an incident channel loses nothing.
- **The dangerous action looks dangerous, and the reversible one does not.**
  Activating a policy bundle, killing a session, and revoking a grant are
  destructive-styled and confirm by typing the object's name. Everything else is
  quiet. If everything is red, nothing is.
- **Identity before data.** Which deployment, which tenant (M18, M19). It is in
  the chrome on every screen, it survives a screenshot, and production is
  visually distinct from staging in a way you cannot miss at a glance.
- **The empty state teaches.** No screen shows a blank rectangle. It says what
  would be here, why it is not, and the one action that changes that.
- **Nothing is fetched from the internet at runtime.** Fonts, icons, styles and
  scripts are bundled into the binary. A console that pulls a webfont from a CDN
  is broken in an air-gapped deployment and, worse, leaks operator activity to a
  third party from inside a security product. This is not a preference.
- **Keyboard is a path, not an affordance.** Audit and Explain are fully
  keyboard-navigable (0016), there is a global command palette (`⌘K`/`Ctrl-K`),
  and focus is always visible.

---

## 3. Tokens

All values live in **one** file (`ui/src/styles/tokens.css`, or the equivalent
for the chosen framework) as CSS custom properties. Nothing else in `ui/` may
contain a raw colour, a raw font size, or a raw radius — §10 makes that a check
rather than a hope.

### 3.1 Neutrals

The structural ramp. Cool grey, so that the accent and the status hues stay warm
against it. **Every value below is contrast-verified against the worst-case
background it is used on** (§10.2) — the steps are not evenly spaced in lightness
because the ones that carry text and control borders are pinned to their WCAG
threshold, and a ramp that looks tidier but fails is worth nothing.

| Token | Light | Dark |
| --- | --- | --- |
| `--n-0` | `#ffffff` | `#0d1117` |
| `--n-50` | `#f7f8fa` | `#12171f` |
| `--n-100` | `#eef0f4` | `#161c26` |
| `--n-200` | `#e2e5ea` | `#1e2632` |
| `--n-300` | `#cbd1da` | `#2b3542` |
| `--n-400` | `#828b9a` | `#66727f` |
| `--n-500` | `#646e7b` | `#828d9c` |
| `--n-600` | `#4c5561` | `#a3aebc` |
| `--n-700` | `#363e49` | `#c2cad5` |
| `--n-800` | `#232a33` | `#d7dde5` |
| `--n-900` | `#161b22` | `#e6eaf0` |
| `--n-950` | `#0d1117` | `#f7f8fa` |

### 3.2 Semantic surface and text

Components reference **these**, never the ramp directly, so a theme is a
remapping rather than a rewrite.

| Token | Light | Dark | Use |
| --- | --- | --- | --- |
| `--bg-canvas` | `--n-50` | `--n-0` | page ground |
| `--bg-surface` | `--n-0` | `--n-100` | cards, tables, panels |
| `--bg-raised` | `--n-0` | `--n-200` | popovers, dropdowns, command palette |
| `--bg-sunken` | `--n-100` | `--n-50` | code, diff gutters, log bodies |
| `--bg-hover` | `--n-100` | `--n-200` | row and control hover |
| `--bg-selected` | `--a-50` | `--a-900` | selected row |
| `--border-subtle` | `--n-200` | `--n-300` | table rules, dividers — **decorative** |
| `--border-default` | `--n-400` | `--n-400` | inputs and control boundaries (≥ 3:1) |
| `--border-strong` | `--n-500` | `--n-500` | emphasis (≥ 3:1) |
| `--text-primary` | `--n-900` | `--n-900` | body and headings |
| `--text-secondary` | `--n-600` | `--n-600` | labels, metadata |
| `--text-tertiary` | `--n-500` | `--n-500` | placeholders, disabled |
| `--text-on-accent` | `#ffffff` | `--n-0` | text on filled accent |

Two of those rows carry an argument rather than a value:

- **`--border-subtle` is decorative and exempt from 3:1.** WCAG 1.4.11 governs
  the boundary you need in order to *identify a control*; a table rule is not
  one, and forcing every divider to 3:1 turns a dense table into a spreadsheet
  grid. `--border-default` is the one an input is recognised by, and it meets
  the threshold. §10.2 tests each against the rule that actually applies to it.
- **`--text-on-accent` is white in light mode and near-black in dark.** The dark
  accent is a light lavender, and white on it is 3.2:1 — it fails, and it also
  simply looks wrong. Flipping the foreground rather than darkening the accent
  keeps the accent recognisably one colour across themes.

### 3.3 Accent

One. It marks the primary action, the active navigation item, and selection —
nothing else. It is deliberately not blue-as-default and deliberately not any of
the status hues.

| Token | Light | Dark |
| --- | --- | --- |
| `--a-50` | `#eef1fe` | `#1b1f3d` |
| `--a-100` | `#dee3fd` | `#232a55` |
| `--a-300` | `#97a4f6` | `#4f5ae3` |
| `--a-500` | `#4f5ae3` | `#7c86ef` |
| `--a-600` | `#3e45c4` | `#97a1f4` |
| `--a-900` | `#262a63` | `#dee3fd` |

### 3.4 Status

Reserved. A status hue never appears as decoration, and these five are the whole
vocabulary — matching the domain rather than a generic palette:

| Token | Meaning in this product | Light | Dark |
| --- | --- | --- | --- |
| `--s-allow` | allowed, healthy, verified, active | `#16753e` | `#3fbd77` |
| `--s-deny` | denied by policy, revoked, expired | `#c4342a` | `#f0736a` |
| `--s-warn` | degraded, stale, expiring, unverified | `#8f5a00` | `#e0a33a` |
| `--s-outage` | unreachable, 5xx, an outage — **never a deny** | `#6d31d9` | `#a78bfa` |
| `--s-info` | informational, pending, in progress | `#175cc4` | `#58a6ff` |

**`--s-deny` and `--s-outage` are different colours on purpose**, and this is the
one place the design system is enforcing an architectural decision rather than a
taste: PLAN **M11** says a deny is a decision this server made and everything
else is an outage, and a console that paints both red re-creates in the UI the
exact confusion M11 exists to prevent — an operator debugging permissions during
an incident. Each pairs with its own glyph and label (§6).

### 3.5 Type

Two families, both **bundled as WOFF2 and self-hosted** (§2):

- **UI**: Inter (variable), fallback `system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`.
- **Mono**: JetBrains Mono, fallback `ui-monospace, SFMono-Regular, "SF Mono", Menlo, monospace`.

Mono is not decorative: session ids, decision ids, fingerprints, labels,
hostnames, policy source, and log bodies are monospace, because they are read
character by character and compared by eye — and they stay LTR, unsubstituted
Latin digits in every locale (§9).

Inter covers Latin, Greek and Cyrillic. A locale needing another script needs
another bundled face, which §9 treats as a deliberate size decision rather than
a download.

| Token | Size / line-height | Weight | Use |
| --- | --- | --- | --- |
| `--t-display` | 32 / 40 | 600 | one per page, at most |
| `--t-h1` | 24 / 32 | 600 | page title |
| `--t-h2` | 18 / 26 | 600 | section |
| `--t-h3` | 15 / 22 | 600 | card header |
| `--t-body` | 14 / 21 | 400 | default |
| `--t-dense` | 13 / 18 | 400 | table cells, lists |
| `--t-label` | 12 / 16 | 500 | field labels, column headers |
| `--t-caption` | 11 / 15 | 500 | metadata, timestamps; letter-spacing `0.02em` |
| `--t-mono` | 12.5 / 19 | 400 | ids, code, logs |

Tabular figures (`font-variant-numeric: tabular-nums`) on every number that sits
in a column. Uppercase is for `--t-caption` only, never for headings.

### 3.6 Space, radius, elevation, motion

- **Space** — 4px base: `2, 4, 6, 8, 12, 16, 20, 24, 32, 40, 48, 64`. No value
  off the scale.
- **Radius** — `--r-sm 4px` (inputs, chips), `--r-md 6px` (buttons, cards),
  `--r-lg 10px` (dialogs, palette), `--r-full 999px` (status pills, avatars).
- **Elevation** — borders carry structure; shadows are for things that float
  above the page and nothing else. Three, and only on overlays:
  `--e-1` dropdowns, `--e-2` popovers/palette, `--e-3` dialogs. In dark mode
  elevation is a lighter surface plus a subtle border, not a heavier shadow —
  shadows do not read on a dark ground.
- **Motion** — `--m-fast 120ms` (hover, focus, checkbox), `--m-base 180ms`
  (dropdown, popover, tab), `--m-slow 240ms` (dialog, drawer, page transition),
  all `cubic-bezier(0.2, 0, 0, 1)`. Nothing animates longer than 240ms.
  `@media (prefers-reduced-motion: reduce)` collapses every duration to `0ms`
  and removes transforms — this is a requirement, not a nicety.

### 3.7 Density

Two modes, remembered per operator, because Fleet and Audit are scanned and
Policy is read:

| | Compact (default on Fleet, Audit) | Comfortable (default elsewhere) |
| --- | --- | --- |
| row height | 32px | 40px |
| cell padding | `6px 12px` | `10px 12px` |
| type | `--t-dense` | `--t-body` |

---

## 4. Layout

- **App shell**: a fixed left navigation (200px, labelled — never icon-only), a
  top bar carrying deployment identity and tenant (§5), and a content column
  with `max-width: 1440px` for reading views and full-bleed for tables.
- **Grid**: 8px. Page gutters 24px, section spacing 32px, card padding 16px.
- **Responsive floor is 1024px** for full fidelity: this is a desktop operator
  tool and pretending otherwise produces a bad phone app and a worse console.
  Below 1024px the shell must still be *usable and not broken* — navigation
  collapses, tables scroll horizontally within their own container, and the page
  body never scrolls sideways. Explain and Audit read acceptably at 768px,
  because those are the two someone opens on a phone during an incident.
- **No horizontal page scroll at any width.** A wide table scrolls inside its
  own `overflow-x` container with the first column pinned.

---

## 5. Deployment and tenant chrome (M18, M19)

0016 requires that an operator with staging and production open in two tabs can
never confuse them, and that the distinction survives a screenshot. Design side:

- The top bar carries the **deployment display name**, its short instance id in
  mono, and its health dot. Hovering gives version, contract version and node
  membership; clicking opens the instance view.
- Each deployment carries an **operator-assigned accent stripe** — a 3px bar
  along the top edge of the viewport plus a matching chip behind the deployment
  name. It is configuration, not inference: the console must never guess
  "production" from a hostname. Default is the accent colour; a deployment that
  sets nothing looks normal.
- **Tenant** appears in the chrome only where more than one is in scope (M18).
  Switching is an explicit control, never a URL the operator edits by hand.
- **Supervised** deployments show a badge naming the supervisor, linking to the
  audit view filtered to its actions. It is `--s-info`, never `--s-warn`: being
  supervised is a fact, not a fault.

---

## 6. Status vocabulary

One component renders all of it, so a status looks identical everywhere:

| State | Colour | Glyph | Label |
| --- | --- | --- | --- |
| allowed / healthy / active | `--s-allow` | filled circle | `Allowed`, `Healthy` |
| denied / revoked / expired | `--s-deny` | filled square | `Denied`, `Revoked` |
| degraded / stale / expiring | `--s-warn` | filled triangle | `Degraded`, `Stale` |
| unreachable / outage | `--s-outage` | hollow circle, dashed | `Unreachable` |
| pending / in progress | `--s-info` | half-filled circle | `Pending` |
| unknown / not reported | `--n-400` | hollow circle, dotted | `Not reported` |

A rolling upgrade reads as **`Pending` / in progress**, never as a fault (M19).
`Not reported` is its own state and never renders as healthy — PLAN §4 is
explicit that stale, undated and absent are one case, and the console says so
rather than guessing.

---

## 7. Components

The inventory 0016 must ship, each with **every** state designed — default,
hover, focus-visible, active, disabled, loading, error, empty, and both themes:

**Primitives** — button (primary, secondary, ghost, danger), icon button (always
with an accessible name), input, textarea, select, combobox, checkbox, radio,
switch, tabs, tooltip, dropdown menu, dialog, drawer, toast, badge, status pill,
chip/label, avatar, breadcrumb, pagination, skeleton, spinner (overlay only),
code block, diff view, empty state, error state, keyboard hint.

**Composites** — data table (sticky header, pinned first column, column sort,
column visibility, row selection, per-row actions, saved views), filter bar
(chip-based, URL-serialisable), time range picker (absolute and relative),
detail drawer, timeline (Explain), tree (fleet graph, hop path), JSON viewer
(collapsible, copyable), command palette, page header (title, id, status,
actions), copyable id (mono, click-to-copy, truncated with full value in the
title).

**Every screen defines four states, and they are reviewed**: loading (skeleton
matching the final layout so nothing shifts), empty (what would be here, why it
is not, the one action), error (what failed, the correlation id, retry — and it
says *outage*, never *denied*, per M11), and partial (some data, a named gap —
a fleet where one node is unreachable is not an error page).

**Form and mutation rules.** Destructive actions confirm by typing the object
name. A failed mutation reports inline at the field or the form, never by a toast
alone — a toast is not a record. Toasts are for successful, reversible things and
auto-dismiss; errors persist until dismissed.

---

## 8. Accessibility

Not a separate pass; part of "done".

- **WCAG 2.2 AA.** Text ≥ 4.5:1, large text and UI boundaries ≥ 3:1, in **both**
  themes. §10 makes this a test rather than a claim.
- Focus visible on every interactive element: 2px `--a-500` ring, 2px offset,
  never `outline: none` without a replacement.
- Full keyboard operation. Dialogs trap and restore focus. A skip-to-content
  link. Logical tab order that matches the visual one.
- Every control has an accessible name; icon-only buttons carry `aria-label`.
- Tables are real `<table>` markup with `<th scope>`; live regions announce
  async results.
- Status is never colour alone (§6). Charts — the two that exist — are readable
  in greyscale.
- `prefers-reduced-motion` honoured (§3.6). Theme follows the system by default
  and the override persists per operator.

---

## 9. Internationalisation

**English is the only locale that ships** (PLAN **M21**). What ships multilingual
is the machinery, and it is built from the first component — because retrofitting
it means revisiting every string, every layout width and every date in the
console at once, which is the same bargain M12 and M18 struck for tenancy and for
the same reason.

- **Every user-visible string comes from a catalogue**, keyed by a stable id. No
  literal in a component, and nothing user-visible baked into an image or a
  sprite — text in an SVG is text.
- **No sentence is assembled by concatenation.** Messages are ICU
  MessageFormat, so interpolation, plurals and ordinals belong to the message:
  word order differs, and so does the number of plural forms — English has two,
  Russian three, Arabic six. `count + " proxies"` is a string that cannot be
  translated, not a shortcut.
- **Layout absorbs +40% text expansion.** German and Finnish run long. No
  fixed-width control sized to its English label, no single-line assumption for
  catalogue text, no ellipsis as the primary defence on a label. Table columns
  are content-driven with a minimum rather than pinned.
- **Logical CSS properties from the first component** — `margin-inline-start`,
  `padding-block`, `inset-inline`, never `left`/`right`. No RTL locale ships and
  none is implemented; the point is that adding one must not be a restyle of
  every component, and a stylesheet written in physical properties is exactly
  that. `dir` comes from the locale; nothing hardcodes `ltr`.
- **Dates, times, numbers and lists go through `Intl`**, never hand-formatted.
  Timestamps render in the operator's zone with UTC on hover; the **copyable**
  form is always ISO-8601 UTC, because an incident is discussed across time
  zones and a pasted timestamp has to be unambiguous. Numbers in columns keep
  tabular figures (§3.5) and take the locale's grouping and decimal separators.
- **Identifiers are never localised.** Session and decision ids, fingerprints,
  hostnames, labels, rule names, policy source and log bodies render LTR in mono
  with an explicit `dir="ltr"` even inside an RTL page, and never take locale
  digit substitution. An operator who reads a fingerprint in Eastern Arabic
  numerals cannot compare it to the one in their terminal.
- **Operator-authored content is never translated** (M21): a rule name, a zone
  name, a target label, a grant reason. The console does not guess at the
  language of data it did not author, and a translated rule name would make
  "explain why" cite a rule that is not in the bundle.
- **Locale resolution happens in the browser**: the operator's stored preference
  → `navigator.languages` → the deployment default. It persists alongside the
  theme choice and is reachable from the chrome, not buried. Note that this is
  the client reading its *own* preference — the server never negotiates a
  language, which is the same statement as the last bullet from the other side.
- **Catalogues load on demand**, one locale at a time, with English inlined so
  the first paint never waits on a fetch. The bundle budget in §11 is per active
  locale.
- **Script coverage is a bundle decision, not a download.** §2 forbids runtime
  network fetches, so a locale whose script the bundled faces do not cover — CJK,
  Arabic, Devanagari — means bundling another face, which is a real size decision
  someone makes on purpose. Inter covers Latin, Greek and Cyrillic; the stack
  falls back to system fonts beyond that. Pretending a new script is a free
  `<link>` is how an air-gapped deployment ends up rendering tofu.
- **The server does not localise.** North-bound errors arrive as a stable code
  and typed parameters and the console owns the sentence (M21, 0014); compiler
  rejections arrive the same way (0005). Server logs stay English — they are read
  by operators and by support, and a log the vendor cannot read is worse than one
  in a second language.

---

## 10. How this is enforced

A design system nobody checks is a mood board. Each of these is a CI job or a
test, and 0016's acceptance criteria name them:

1. **Token lint.** No raw hex, `rgb()`, `px` font size, or `px` radius outside
   `tokens.css`. A stylelint rule; it fails the build.
2. **Contrast test.** Every semantic foreground/background pair asserted against
   its AA ratio, in both themes, as a unit test over the token file. A token
   change that breaks contrast fails before review.
3. **Axe.** Automated accessibility assertions on every screen in the E2E run,
   both themes. Zero violations at `serious` or above.
4. **Visual regression.** A committed screenshot set — every screen, both themes,
   compact and comfortable, plus loading, empty and error states — diffed in CI.
   It is the artefact that makes "it looks fine" reviewable, and a deliberate
   change updates the baseline in the same PR.
5. **No network at runtime.** A test loads the console with all external origins
   blocked and asserts it renders identically — the air-gap requirement of §2.
6. **Reduced motion.** A test asserts no transition exceeds 0ms under
   `prefers-reduced-motion: reduce`.
7. **Pseudolocale.** A build under `en-XA` accents every catalogue string and pads
   it to +40%, then renders the screenshot set. Two things fail it: a string that
   comes out unaccented is hardcoded English, and a layout that clips or overflows
   is too tight for a real translation. This is the check that makes "ready for a
   second locale" testable while only English exists — an assertion about locales
   nobody has written yet is otherwise unfalsifiable, which is how consoles ship
   "i18n-ready" and are not.
8. **RTL smoke.** A mirrored pseudolocale (`en-XB`) renders the same set and
   asserts nothing leaks a physical CSS property. No RTL locale ships; this is
   what keeps adding one cheap.
9. **String and format lint.** No literal user-visible string in a component, and
   no `Date`/`Intl`/number formatting outside the locale helpers.

---

## 11. Constraints on the implementation

- **Typed components with a real build.** TypeScript, Vite. The framework choice
  (React, Svelte, Vue) stays with the implementing session; the tokens, the
  component inventory and §10 do not.
- **Headless primitives are encouraged; visual kits are forbidden.** Radix, Ark,
  Headless UI, TanStack Table, cmdk — accessible behaviour with no imposed
  identity — are exactly right. Material, Bootstrap, Ant, Chakra and anything
  else that ships its own look are not: the point of this document is that the
  console has one identity, and it is this one.
- **No CSS-in-JS runtime.** CSS modules, or a utility framework configured from
  these tokens. Styles are static so the first paint is not waiting on script.
- **An ICU MessageFormat runtime** (`intl-messageformat` and the FormatJS
  binding for the chosen framework, or equivalent). Catalogues are ICU JSON, one
  file per locale under `ui/src/locales/`, keys stable and never reused for a
  different sentence — a renamed key is a new key. `en.json` is the only one
  committed in 0016.
- **No chart library until there is a third chart.** The two §7 allows are
  hand-written SVG; a charting dependency for two charts is a bundle cost and a
  second visual identity at once.
- **Bundle budget**: ≤ 350 KB gzipped JS for the active locale on first load, fonts ≤ 120 KB. The
  binary is shipped to self-hosters who may serve it over a VPN from a small
  instance.
- **Assets are built in CI and committed** under `ui/dist/`, so `make build`
  works on a machine with no Node (0016). The build is reproducible and a drift
  check proves `dist/` matches `src/`.

---

## 12. Changing this document

It is durable truth in the sense of `docs/PROTOCOL.md` §9: a session that finds
it wrong updates it in the same PR, says so in the PR description and in its
learnings, and does not quietly diverge from it. A token added here is added for
the whole console; a one-off colour in one screen is the thing this document
exists to prevent.
