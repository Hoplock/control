# Hoplock Control — Console Design System

> This document is **binding on the console phases, 0049–0060,** and on every
> change to `ui/` afterwards. PLAN **M20** is the decision that makes it so. It exists because
> "make it look good" is not a requirement anybody can meet or fail, and a
> console built to acceptance criteria that are all functional will be correct
> and ugly — which, for the one screen an operator opens during an incident, is
> its own kind of incorrect.
>
> Read with `docs/PLAN.md` §2 (M20, M2, M18, M19, and **M21** — the console is
> localisable from its first screen, and English is the only locale that ships)
> and the console phases' prompts (PLAN §10: 0049–0059 build the console,
> 0052 choosing its look with the operator, and 0060 audits every screen for
> one look and feel).

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
any of them: the dark, dense consoles security teams already live in (an
operator's brief named CrowdStrike's, and 0052 chose the look with them),
Linear, Grafana, Tailscale's admin, Vercel's dashboard. So the console is
**dark first**: near-black grounds, flat panels a step above them, hairline
borders and one vivid accent. Light is a designed theme too, not an inversion.
**The anti-goal**, stated so it can be pointed at in review: a Bootstrap admin
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
  keyboard-navigable (0051), there is a global command palette (`⌘K`/`Ctrl-K`),
  and focus is always visible.

---

## 3. Tokens

All values live in **one** file, `ui/src/styles/tokens.css`, as CSS custom
properties. Each colour is declared once for both themes as
`light-dark(<light>, <dark>)`, so a theme is the page's colour scheme — the
system's, or the operator's override as `data-theme` on `<html>` — and never a
second copy of the palette. Nothing else in `ui/` may contain a raw colour, a
raw font size, a raw radius, an off-scale space or a raw duration — §10 makes
that a check rather than a hope.

### 3.1 Neutrals

The structural ramp. Cool blue-grey, near black in dark (Carbon, 0052), so
that the cyan accent and the status hues are the only colour on the page. **Every value below is contrast-verified against the worst-case
background it is used on** (§10.2) — the steps are not evenly spaced in lightness
because the ones that carry text and control borders are pinned to their WCAG
threshold, and a ramp that looks tidier but fails is worth nothing.

| Token | Light | Dark |
| --- | --- | --- |
| `--n-0` | `#ffffff` | `#07090c` |
| `--n-50` | `#f4f6f8` | `#0d1015` |
| `--n-100` | `#eaedf1` | `#13181f` |
| `--n-200` | `#dde2e8` | `#1b212a` |
| `--n-300` | `#c5ccd5` | `#29313d` |
| `--n-400` | `#7e8897` | `#667183` |
| `--n-500` | `#5f6a79` | `#8590a1` |
| `--n-600` | `#454f5d` | `#a2acba` |
| `--n-700` | `#313a46` | `#c3cad4` |
| `--n-800` | `#1f262f` | `#d9dee5` |
| `--n-900` | `#11161c` | `#edf0f4` |
| `--n-950` | `#080b0f` | `#f7f8fa` |

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
| `--bg-selected` | `--a-50` | `--a-100` | selected row |
| `--border-subtle` | `--n-200` | `--n-300` | table rules, dividers — **decorative** |
| `--border-default` | `--n-400` | `--n-400` | inputs and control boundaries (≥ 3:1) |
| `--border-strong` | `--n-500` | `--n-500` | emphasis (≥ 3:1) |
| `--text-primary` | `--n-900` | `--n-900` | body and headings |
| `--text-secondary` | `--n-600` | `--n-600` | labels, metadata |
| `--text-tertiary` | `--n-500` | `--n-500` | placeholders, disabled |
| `--text-on-accent` | `#ffffff` | `--n-0` | text on filled accent |
| `--accent` | `--a-500` | `--a-500` | the primary action, the active navigation mark, selection |
| `--focus-ring` | `--a-500` | `--a-500` | the focus ring (§8) |
| `--bg-inverse` | `--n-900` | `--n-900` | a tooltip's ground |
| `--text-inverse` | `--n-0` | `--n-0` | a tooltip's text |
| `--scrim` | `--n-50` at 70% | `--n-0` at 70% | behind a modal dialog, a drawer, a busy overlay — never a text ground |

Three of those rows carry an argument rather than a value:

- **`--border-subtle` is decorative and exempt from 3:1.** WCAG 1.4.11 governs
  the boundary you need in order to *identify a control*; a table rule is not
  one, and forcing every divider to 3:1 turns a dense table into a spreadsheet
  grid. `--border-default` is the one an input is recognised by, and it meets
  the threshold. §10.2 tests each against the rule that actually applies to it.
- **`--text-on-accent` is white in light mode and near-black in dark.** The dark
  accent is a bright cyan, and white on it is 1.8:1. It fails, and it also
  simply looks wrong. Flipping the foreground rather than darkening the accent
  keeps the accent recognisably one colour across themes: near-black on it
  is 11:1.
- **`--bg-selected` is `--a-100` in dark.** The dark accent ramp runs the
  other way, so its `--a-900` is the pale cyan `#cffafe`, on which body
  text would be 1.02:1. `--a-100` is the dark ramp's tint, as `--a-50` is the
  light one's.

### 3.3 Accent

One. It marks the primary action, the active navigation item, and selection —
nothing else. It is an electric **cyan**, which the operator chose in 0052: vivid
on the near-black ground, and in light a deep teal-cyan that carries white text.
It is deliberately not blue-as-default and deliberately not any of the status
hues. It sits between allow's green and info's blue, and info is tuned bluer
(§3.4) so the two are not read as one. Red is never the accent, because red
means denied (§3.4, M11), and every primary button would read as a denial.

| Token | Light | Dark |
| --- | --- | --- |
| `--a-50` | `#ebfcfe` | `#061b20` |
| `--a-100` | `#cdf5fb` | `#0a2930` |
| `--a-300` | `#5fcfe3` | `#0e7490` |
| `--a-500` | `#0b7189` | `#22d3ee` |
| `--a-600` | `#0b5a6e` | `#6fe6f8` |
| `--a-900` | `#08313d` | `#cffafe` |

### 3.4 Status

Reserved. A status hue never appears as decoration, and these five are the whole
vocabulary — matching the domain rather than a generic palette:

| Token | Meaning in this product | Light | Dark |
| --- | --- | --- | --- |
| `--s-allow` | allowed, healthy, verified, active | `#13753c` | `#3ccf80` |
| `--s-deny` | denied by policy, revoked, expired; a bundle refused (invalid, blocking) | `#c4322a` | `#ff7a70` |
| `--s-warn` | degraded, stale, expiring, unverified | `#8f5a00` | `#f0b03f` |
| `--s-outage` | unreachable, 5xx, an outage — **never a deny** | `#6d31d9` | `#ad93ff` |
| `--s-info` | informational, pending, in progress | `#1d55c9` | `#6a9dff` |

The dark values are a step brighter than a mid-grey ground would need, so they
hold on the near-black one. Info is a step bluer than a default blue, away from
the cyan accent.

`--s-unknown` (`--n-400`) is the glyph of *not reported* (§6): a neutral, so
that it is never read as any of the five.

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

Each face is bundled as its **Latin subset** (Inter 48 KB, JetBrains Mono
40 KB, under `unicode-range`), every accent the `en-XA` pseudolocale uses
included. The faces themselves cover Greek and Cyrillic too, but those subsets
would add about 38 KB and take the fonts past §11's 120 KB, so text in them
falls back to the system stack until a locale needs it — and then bundling them
is that locale's size decision (§9), never a download.

| Token | Size / line-height | Weight | Use |
| --- | --- | --- | --- |
| `--t-display` | 28 / 36 | 600 | one per page, at most |
| `--t-h1` | 20 / 28 | 600 | page title |
| `--t-h2` | 16 / 24 | 600 | section |
| `--t-h3` | 14 / 20 | 600 | card header |
| `--t-body` | 13 / 20 | 400 | default |
| `--t-dense` | 12 / 18 | 400 | table cells, lists |
| `--t-label` | 11 / 16 | 600 | field labels, column headers |
| `--t-caption` | 11 / 15 | 500 | metadata, timestamps; letter-spacing `0.04em` |
| `--t-mono` | 12 / 18 | 400 | ids, code, logs |

The scale is a step denser than a document's, because tables are scanned (§1):
body text is 13px, a table cell 12px, and a label is set semibold so that a
small size still reads as a heading for its column.

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
- **Layout dimensions** — the navigation's width, the top bar's height, a
  control's height, a status glyph's size, a border's width — are tokens as
  well (`--shell-nav-width`, `--shell-topbar-height`, `--control-height`,
  `--glyph-size`, `--border-width`, `--icon-size`, and the widths of a
  dialog, a drawer, a menu, a search field and the rail a timeline stands in
  beside what it explains (`--rail-width`), a form's greatest width and the
  narrowest a field shares a row at (`--form-max`, `--form-column-min`), a
  table's and a list's greatest height), so no component carries a magic
  number.
- **Motion** — `--m-fast 120ms` (hover, focus, checkbox), `--m-base 180ms`
  (dropdown, popover, tab), `--m-slow 240ms` (dialog, drawer, page transition),
  all `cubic-bezier(0.2, 0, 0, 1)`. Nothing animates longer than 240ms. A
  busy indicator is a loop, not a transition: one turn is `--m-spin` (900ms),
  and reduced motion stops it as it stops everything else.
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

### 3.8 Diff

The two sides of a change, which the diff view (§7) sets lines on. They are
**not statuses**: an added line is not *allowed* and a removed one is not
*denied*, and a policy diff is exactly where a removed allow rule in deny red
would be read as a denial. So neither takes a status hue (§3.4). Each is a
pale ground of its own, and its side is said by its `+`/`−` marker and by
words a screen reader reads, never by the tint alone (§2). Body and secondary
text are held to 4.5:1 on both, in both themes (§10.2).

| Token | Light | Dark | Use |
| --- | --- | --- | --- |
| `--diff-add-bg` | `#e3f4e8` | `#0e2618` | an added line |
| `--diff-remove-bg` | `#fbe8e6` | `#2c1214` | a removed line |

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
- **Every URL the console writes is under `/console/`** (PLAN **M27**): the
  shell, every asset, every deep link, and every URL-serialisable filter —
  `/console/audit/records?subject=alice`, never `/audit?subject=alice`. The
  history fallback answers only there; `/` redirects to it and nothing else
  does. A host's screens live under `/host-console/<provider>/`, every URL one
  of them writes stays there, and Control's shell loads them from there and
  never from inside `/console/`. A link from one console to the other names
  the other's prefix. A deep link is part of the console's surface: it is
  stable, and renaming one is a change someone notices.

---

## 5. Deployment and tenant chrome (M18, M19)

0049 requires that an operator with staging and production open in two tabs can
never confuse them, and that the distinction survives a screenshot. Design side:

- The top bar carries the **deployment display name**, its short instance id in
  mono, and its health dot. Hovering gives version, contract version and node
  membership; clicking opens the instance view.
- Each deployment carries an **operator-assigned accent stripe** — a 3px bar
  along the top edge of the viewport, on every page including sign-in, plus a
  matching chip behind the deployment name. It is configuration, not
  inference: the console must never guess "production" from a hostname. It is
  `console.stripe` in the server's configuration, one of the five below, and
  the server writes it onto `<html>` as `data-stripe` before the first paint.
  Default is the accent colour; a deployment that sets nothing looks normal.
  None is a status hue, because a deployment's colour is not a status:

  | Stripe | Bar, light / dark | Chip tint, light / dark |
  | --- | --- | --- |
  | `accent` (default) | `#0b7189` / `#22d3ee` | `#ebfcfe` / `#061b20` |
  | `magenta` | `#b5179e` / `#e879d0` | `#fbeaf7` / `#3a1534` |
  | `teal` | `#0f766e` / `#2dd4bf` | `#e3f6f4` / `#0f2e2b` |
  | `orange` | `#c2410c` / `#fb923c` | `#fdeee6` / `#3a1d0c` |
  | `slate` | `#475569` / `#94a3b8` | `#eef1f5` / `#1c2430` |

  Each bar is ≥ 3:1 on the canvas and the name and id on each tint ≥ 4.5:1, in
  both themes (§10.2). `teal` is the stripe nearest the cyan accent, and so the
  nearest to the default bar. A deployment that must never be taken for one
  with no stripe set chooses another.
- **Tenant** appears in the chrome only where more than one is in scope (M18).
  Switching is an explicit control, never a URL the operator edits by hand.
- **Supervised** deployments show a badge naming the supervisor, linking to the
  audit view filtered to its actions. It is `--s-info`, never `--s-warn`: being
  supervised is a fact, not a fault.
- **The operator** ends the bar: their name, which opens the account menu —
  the display preferences, theme and density, each a group of choices, and
  signing out. The preferences are the operator's, so they hang off the
  operator's name rather than sitting in the navigation, and they are kept
  for the person (their subject), never for one sign-in. The command palette
  sets them too.

---

## 6. Status vocabulary

One component renders all of it, so a status looks identical everywhere:

| State | Colour | Glyph | Label |
| --- | --- | --- | --- |
| allowed / healthy / active / applied / valid | `--s-allow` | filled circle | `Allowed`, `Healthy`, `Active`, `Applied`, `Valid` |
| denied / revoked / expired / invalid / blocking / locked / quarantined / deactivated | `--s-deny` | filled square | `Denied`, `Revoked`, `Expired`, `Invalid`, `Blocking`, `Locked`, `Quarantined`, `Deactivated` |
| degraded / stale / expiring / rejected / warning | `--s-warn` | filled triangle | `Degraded`, `Stale`, `Expiring`, `Rejected`, `Warning` |
| unreachable / outage / fetch failed / unserved | `--s-outage` | hollow circle, dashed | `Unreachable`, `Fetch failed`, `Unserved` |
| pending / scheduled / in progress / pending restart / advice | `--s-info` | half-filled circle | `Pending`, `Scheduled`, `In progress`, `Pending restart`, `Advice` |
| unknown / not reported / left / superseded | `--n-400` | hollow circle, dotted | `Not reported`, `Left`, `Superseded` |

The component is `Status` in `ui/src/components/Status.tsx`, and every row
above is a named entry of its `STATUS` map: a screen names a status, it never
picks a colour. As a pill it sits on a bordered ground (dashed for an outage,
dotted for not reported), for a status that heads a page or a detail.

A proxy's configuration is read as the proxy **reported** it (proxy D18):
`Rejected` is the proxy refusing a document and keeping the one it runs —
degraded, not a deny; `Fetch failed` is the proxy not reaching Control for it
— an outage; an older document applied while a newer one is desired is a
rollout `In progress`. A proxy that has never reported is `Not reported`,
never `Applied`.

A decision is read as its effect: `Allowed`, `Denied`, or **`Unserved`** — an
allow the server could not answer (PLAN §5.6), which is an outage and never a
deny (M11). A session reads as its worst hop: a deny anywhere denies it.

A policy version reads as **`Active`**, the one decisions are taken under,
or **`Superseded`**, every other (PLAN §5.6): kept and activatable, neither
healthy nor failed. What is said about a bundle reads on one ladder of what it
stops: a compiler's rejection is **`Invalid`** and a validator's blocking
finding **`Blocking`** — both refuse it outright, as a decision the server
made, so both are deny-class and never an outage (M11); a **`Warning`**
stops activation until it is accepted by its id; **`Advice`** stops nothing.
A severity outside the three, which the server answers as an outage, never
reads as fine: it is `Not reported`. A bundle that compiles is **`Valid`**.

A grant reads as its window (M10): **`Active`** while it is live,
**`Scheduled`** before its window opens — information, never a fault — and
**`Expired`** or **`Revoked`** once it backs no decision, both deny-class. A
state the server names that the console does not know is `Not reported`.

A **standing denial** reads as its own deny (PLAN M28): **`Locked`** for a
subject lock, **`Quarantined`** for a target quarantine, **`Deactivated`** for
the directory's deactivation — each a deny made on purpose, never an outage
(M11), and never the plain `Denied` of a decision, because each is lifted by a
different party: an operator unlocks and releases, and only the directory
re-activates. A subject both locked and deactivated shows both, the lock
first, as its record holds them. A lock or quarantine the server placed and
could not deliver (`standing_denial_undelivered`) is in force: it reads as
`Pending` delivery beside its own status, with a way to ask again, never as a
failure to place it.

A rolling upgrade reads as **`Pending` / in progress**, never as a fault (M19).
`Not reported` is its own state and never renders as healthy — PLAN §4 is
explicit that stale, undated and absent are one case, and the console says so
rather than guessing.

---

## 7. Components

The inventory the component set ships (0050; the timeline and the time range
picker with 0051, the diff view with 0053), each with **every** state designed
— default, hover, focus-visible, active, disabled, loading, error, empty, and
both themes:

**Primitives** — button (primary, secondary, ghost, danger), button link (a
console link set as a primary or secondary button, for going somewhere rather
than doing something, never danger), icon button (always
with an accessible name), input, textarea, select, combobox, checkbox, radio,
switch, tabs, tooltip, dropdown menu, dialog, drawer, toast, badge, status pill,
chip/label, avatar, breadcrumb, view links (a screen's views, each its own
deep link, the current one `aria-current="page"`), identifier (mono, LTR,
never translated; at the page title's size for a page about one object named
by its id), pagination, skeleton, spinner (overlay only), code block, source
view, diff view, empty state, error state, keyboard hint.

**Composites** — data table (sticky header, pinned first column, column sort,
column visibility, row selection, per-row actions, saved views), filter bar
(chip-based, URL-serialisable under `/console/`, §4), time range picker (absolute and relative),
detail drawer, timeline (Explain), tree (fleet graph, hop path), JSON viewer
(collapsible, copyable), command palette, page header (title, id, status,
actions), copyable id (mono, click-to-copy, truncated with full value in the
title).

**Every screen defines four states, and they are reviewed**: loading (skeleton
matching the final layout so nothing shifts), empty (what would be here, why it
is not, the one action), error (what failed, the correlation id, retry — and it
says *outage*, never *denied*, per M11), and partial (some data, a named gap —
a fleet where one node is unreachable is not an error page; for a list that
is nothing else, the rest of an answer the server cut, said beside its rows).

**The timeline** (`Timeline.tsx`) is an ordered list, oldest first, along one
rule: each event an instant (`<time>`, UTC on hover), a marker — the status
vocabulary's glyph beside its word, or a plain dot — a title and facts. When
events can be chosen it is one tab stop: the arrows move between events, Home
and End go to the ends, and the chosen one is `aria-current="step"`. Its
loading state is `TimelineSkeleton`; an event that cannot be read is
disabled. **The time range picker** (`TimeRange.tsx`) is all time, relative
(the last 15 minutes, hour, day, week or 30 days, `range=` in the URL,
resolved when the screen reads so a pasted link still means "the last day"),
or absolute (`from=`/`to=`, ISO-8601 UTC, edited in the operator's zone,
applied explicitly, and a start not before its end refused at the field). A
filter bar's field is a choice or typed text, which applies on Enter or on
leaving the field so a half-typed value reads nothing. A tree may open every
branch at first (`expandAll`), which a hop path, read whole, does.

**Where it lives.** One family per file in `ui/src/components/`; the four
states are one frame (`States.tsx`: `ScreenFrame`, `ErrorState`, `EmptyState`,
`PartialNotice`, `Skeleton`). A data table shows **one page** of a bounded
list (PLAN M26): it says so beside its rows when the answer was cut, its sort
and filters act on that page, and `Pagination` moves by the answer's own
cursor. Saved views are named URLs, kept per operator in the browser. The
overlays are the platform's own: `<dialog>` opened modal (the browser traps
and restores focus), and a menu is a popover in the top layer anchored to its
button. A test-only gallery (`ui/gallery/`, never built into the console)
renders every component in every state, and the browser checks run over it.

**The diff view** (`Diff.tsx`, 0053) renders a unified diff the server wrote
and never diffs itself: a figure captioned with what is compared and the
count of lines added and removed, then a table of hunks, each line with its
number on both sides, a `+`/`−` marker with its side in words, and its text
on the side's ground (§3.8). The table follows the page's direction, so the
gutter is at the line's start in either, and each line's text is LTR, mono
and untranslated (§9); a long diff scrolls inside its own focusable region.
Its states are the lines, **no difference** (the sources are identical),
**too far apart** (the server would not render a line diff, and the changes
by rule are the whole answer), loading (`DiffSkeleton`), and an error, which
is the caller's `ErrorMessage` where the read failed. **The source view**
(`Code.tsx`'s `SourceView`) numbers a source's lines and sets each note — a
compiler's rejection, a finding — beneath the line it is about, the line's
gutter marked in the worst note's status hue; a note about the whole document
heads it. A line is focusable, so a list beside it can go to one. A textarea
holding source is `mono`: LTR, mono, unchecked for spelling and untranslated.

**Form and mutation rules.** Destructive actions confirm by typing the object
name, through the one `ConfirmDialog`, whose hint says what typing commits to:
by default that the action cannot be undone, and for an activation, which a
rollback reverses, that the decisions taken under it are not taken back; for
a lock, that lifting it later restores none of the sessions it ended. A field
inside the dialog that is refused where it stands (a lock's holder text over
its byte limit) holds the confirm (`ready`) until it is put right. A
breadcrumb's crumb may be data, such as a version's name in mono. A failed mutation reports inline at the field or the form, never by a toast
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
- Status is never colour alone (§6). Charts — the two that exist, Fleet's
  health bar and the audit view's counts — are readable in greyscale.
- `prefers-reduced-motion` honoured (§3.6). The console is **dark by
  default** (the operator's choice in 0052; `DEFAULT_PREFS` in
  `ui/src/prefs/prefs.ts`). An operator may choose light, or to follow the
  system, and the choice persists per operator.

---

## 9. Internationalisation

**English is the only locale that ships** (PLAN **M21**). What ships multilingual
is the machinery, and it is built from the first component — because retrofitting
it means revisiting every string, every layout width and every date in the
console at once, which is the same bargain M12 and M18 struck for tenancy and for
the same reason.

- **Catalogue keys are namespaced** (M27): Control's carry no `:`, and a host's
  screens use `<provider>:<key>` (`hoplock/enterprise:approvals.title`), so a
  host's key can never be one of Control's; one that is is refused when the
  host's catalogue is mounted.
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
  someone makes on purpose. The bundled subsets are Latin (§3.5): Greek
  and Cyrillic, which the faces cover, fall back to system fonts until a
  locale bundles their subsets. Pretending a new script is a free
  `<link>` is how an air-gapped deployment ends up rendering tofu.
- **The server does not localise.** North-bound errors arrive as a stable code
  and typed parameters and the console owns the sentence (M21, 0019); compiler
  rejections arrive the same way (0005). Server logs stay English — they are read
  by operators and by support, and a log the vendor cannot read is worse than one
  in a second language.

---

## 10. How this is enforced

A design system nobody checks is a mood board. Each of these is a CI job or a
test, and the console phases' acceptance criteria name them (0049 the static
checks 1, 2 and 9; 0050 the browser checks 3–8):

1. **Token lint.** No raw hex, named colour, `rgb()`/`hsl()`/`light-dark()`,
   font size, radius, `px` margin, padding or gap, or transition duration
   outside `tokens.css`; and no physical property (`margin-left`, `left`,
   `border-right`, a four-value margin or padding, `text-align: left`) anywhere
   (§9). Stylelint's own rules, in `ui/.stylelintrc.json`; it fails the build.
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
Checks 3–8 run in CI as `console-e2e`, one job per rule, over **one
registry of screens** (`ui/e2e/screens.ts`), against a real server on a seeded
database (`ui/e2e/fixture/`; the server is the fixture host, Control with one
registered identity sync, so a directory's subjects are real): a screen is
checked by registering it — its
path, how it is ready, how its loading, empty, error and partial states are
made (an API answer substituted in the browser — a fixed answer, a request
held open, or the server's own answer changed, such as a page marked cut —
never a route the server binds), and the calls it offers that change
something. The gallery's pages
are in the registry too. In detail:

- §10.3 runs axe at WCAG 2.2 A and AA on every screen and state, both themes.
- §10.4's baseline is `ui/e2e/baseline/`, at one scale (1×, 1280px wide),
  times and build versions masked; `npm run e2e:update` rewrites it, and a
  PR that changes a screen changes its images. Pseudo-classes are forced
  through the browser's developer protocol, so a gallery image shows hover,
  focus and active at once — on a cell's own element, or on the button or
  scrolling region a wrapper names (`data-force-child`).
- §10.5 aborts every request to another origin, asserts there was none, and
  holds the page to the same baseline image.
- §10.6 reads every element's computed transition and animation durations.
- §10.7 fails on text, or a read-aloud attribute, with no accented letter
  outside `translate="no"` (identifiers and operator data, §9) or a `<time>`;
  on a box that clips its text; and on a page that scrolls sideways.
- §10.8 fails on a page that is not `dir="rtl"`; on any rule or inline style
  whose physical pair differs (the CSSOM writes a symmetric shorthand as
  equal longhands, so only an asymmetric pair is a leak); and on any block
  box that is not the mirror of its `en-XA` position.

9. **String and format lint.** No literal user-visible string in a component, and
   no `Date`/`Intl`/number formatting outside the locale helpers
   (`ui/src/i18n/`). It reads the TypeScript syntax tree
   (`ui/scripts/lint-strings.mjs`), and it also refuses an inline `style` and a
   raw colour in a string, the two ways a value escapes the token lint.

---

## 11. Constraints on the implementation

- **Typed components with a real build.** TypeScript, Vite. The framework is
  **React** (chosen in 0049, inherited by every later phase): the headless
  primitives below are written for it first, which is the deciding reason; the
  tokens, the component inventory and §10 bind whatever it renders.
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
  committed in the console phases (0049–0060).
- **No chart library until there is a third chart.** The two §7 allows are
  hand-written SVG — Fleet's health bar (0050) and the audit view's counts of
  the records on a page (0051), a table whose bars are one neutral ink beside
  their figures — and a charting dependency for two charts is a bundle cost
  and a second visual identity at once.
- **Bundle budget**: ≤ 350 KB gzipped JS for the active locale on first load, fonts ≤ 120 KB. The
  binary is shipped to self-hosters who may serve it over a VPN from a small
  instance.
- **Assets are built in CI and committed** under `internal/console/dist/`, so
  `make build` works on a machine with no Node (0049). Not `ui/dist/`:
  `go:embed` cannot reach outside its own package's directory, and a Go package
  in `ui/` would be a public package of the module — a compatibility promise to
  Enterprise (PLAN M15, M23), which a directory of built files has no business
  being. The build is reproducible from `ui/package-lock.json`, and a drift
  check rebuilds it in CI and fails on any difference.

---

## 12. Changing this document

It is durable truth in the sense of `docs/PROTOCOL.md` §9: a session that finds
it wrong updates it in the same PR, says so in the PR description and in its
learnings, and does not quietly diverge from it. A token added here is added for
the whole console; a one-off colour in one screen is the thing this document
exists to prevent.
