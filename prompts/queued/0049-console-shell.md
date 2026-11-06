# 0049 — Management console 1: the shell, the build and the design machinery

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — §2: **M2** (the console is a north-bound client and
  authenticates like one), **M18** (tenancy is invisible to a deployment that
  does not use it), **M19** (a deployment has an identity and can be
  supervised), **M20** (the design is specified, not improvised), **M21** (the
  console is localisable from its first screen and English is the only locale
  that ships), **M27** (the four namespaces on the north-bound listener, and
  where this console and a host's screens are served) and **M23** (every phase
  is a release); §3 (`ui/`, and `internal/httpapi/north` at "A host binary's
  routes (M15) enter **the same table**"); §10, the console-split renumbering
  note, which says why the console is ten phases.
- **`ui/DESIGN.md` — in full, before writing any markup.** It is the design
  system this series is built to: tokens, type scale, layout, status
  vocabulary, component inventory, state matrix, internationalisation, and the
  checks that enforce all of it. It is binding under M20, and it is not a
  style suggestion you may improve on mid-implementation — if it is wrong,
  change it in this PR and say so.
- `docs/learnings/` — read summaries; open `0038` in full: it reserved where
  this console is served, where a host's screens and assets go and how a
  host's catalogue keys are namespaced (M27). Build into those namespaces, and
  never serve a console path inside the one reserved for a host. The code is
  `internal/httpapi/north/console.go` (`Console`, the mount) and
  `namespace.go` (the prefixes); the tests are `namespace_internal_test.go`.
  Open `0011` (RBAC, OIDC and the north-bound session the console signs in
  with), `0019` (**the API the console consumes** — every route and its
  required role, and the M21 error envelope), and `0046` (the deployment's
  identity, version set and health, which the chrome shows).

## The series
The console was one phase until the console-split revision (PLAN §10). One
session could not build it without its context being compacted several times,
and a compaction keeps "show the lag" while it drops the field names and the
"never render X as Y" rules this console is made of. So it is ten phases, each
a release and a PR of its own:

| Phase | Builds |
| --- | --- |
| **0049** | the shell: build and embed, the mount under `/console/`, sign-in, tokens and themes, the catalogue machinery, the deployment and tenant chrome, and the static checks (this phase) |
| 0050 | the component set, the browser checks, the seeded fixture and the RBAC harness, and Fleet |
| 0051 | Explain and the audit view |
| 0052 | Policy |
| 0053 | Inventory |
| 0054 | locks, quarantines and the directory's subjects |
| 0055 | Extensions and Reports |
| 0056 | stream gaps, the archive's lag and retention, in the audit view |
| 0057 | algorithm posture and the emergency runbook |
| 0058 | the consistency audit of every screen |

Build only this phase's row. Every later phase reads this phase's learnings in
full, so they are the contract the rest of the series builds on.

## Objective
Give Hoplock Control the foundation of an operator console. A self-hosted
deployment should end the series usable without `curl`: the proxy fleet's
health, targets and identities, policy authored and simulated, the audit trail,
and — the one screen that sells the product — a session id pasted in and the
full explanation of why access was allowed or denied.

**It must also be good to look at and to use.** This is the surface a
self-hoster judges the whole product by, and the one an operator reads at 02:00
during an incident. "Works" is half the bar here; `ui/DESIGN.md` is the other
half, and it is written so that half can fail a review rather than merely
disappoint one. This phase lays everything every screen will stand on, so that
no later phase invents its own.

## In scope
- `ui/`: a single-page app served by `cmd/hoplock-control` from embedded assets
  (`embed.FS`), so a deployment is still one binary. No separate web server, no
  Node runtime in production.
- **Build.** Assets built and checked in, or built in CI with a reproducible
  step; `make build` must work on a machine with no Node installed. Say which
  you chose and why in learnings. `ui/DESIGN.md` §11 constrains the toolchain
  (TypeScript + Vite, headless primitives, no visual kit, no CSS-in-JS
  runtime, an ICU MessageFormat runtime, a bundle budget) — the framework
  choice inside those constraints is yours, and every later console phase
  inherits it.
- **Served inside its own namespace, and nowhere else (M27).** 0038 reserved
  four prefixes on the north-bound listener, each a constant in
  `internal/httpapi/north`, and built the tests that hold them apart:

  | Namespace | Constant | Prefix |
  | --- | --- | --- |
  | Control's API | `ControlAPIPrefix` (less `HostAPIPrefix`) | `/api/v1/` |
  | a host's API | `HostAPIRoot(provider)` | `/api/v1/hosts/<provider>/` |
  | **this console** | `ConsolePrefix` | `/console/` |
  | a host's console | `HostConsoleRoot(provider)` | `/host-console/<provider>/` |

  - Mount the console through `north.Options.Consoles`, as one `north.Console`
    with no `Provider`: its `Patterns` (the shell, `assets/`, the catalogue,
    and the history fallback as a `{…...}` subtree), its `Catalogue` keys and
    one `Handler`. The mount refuses at start-up any pattern outside
    `/console/`, and `/` redirects to `/console/` only once it is mounted.
    Wire it in `internal/daemon` where the north server is built.
  - Every path this console serves is under `/console/`: the shell, every
    asset, and every deep link and URL-serialisable filter it writes
    (`/console/audit/records?subject=…`, never `/audit?…`). The app's router
    base is `/console/`, and the build's asset base is `/console/assets/`.
  - Nothing is served at `/` but that redirect. The history fallback answers
    only inside `/console/`. An unknown path outside every prefix is
    Control's `404` envelope, and a path under `/api/v1/` is never answered
    with the console's index.
  - A host's screens and assets load from `/host-console/<provider>/`, never
    from inside `/console/`, and none of this console's paths sits under the
    host's. This console's shell may load a host's screen modules from there;
    a link from one console to the other names the other's prefix.
  - Catalogue keys (M21): this console's carry no `:`; a host's are
    `<provider>:<key>` (`hoplock/enterprise:approvals.title`), and one equal to
    a key of this console's is refused when the host's catalogue is mounted.
    Pass the console's real `en.json` keys as `Catalogue`, so the refusal is
    judged against them.
  - If a screen needs a path outside these rules, the namespace is wrong.
    Change M27 in this PR and say why, rather than serving around it.
- **It is an API client and nothing more.** The console calls the north-bound
  API (0019) with the operator's own session; it never reaches into the
  database and never has a privileged path of its own. Any capability the
  console has, the API has, and RBAC (0011) applies identically — a console
  that can do what the API forbids is a second, unaudited authorisation
  system. Build the console's API client so every call names its route, and
  later phases add theirs through it; 0050 builds the test that holds this.
- **Auth**: OIDC for humans, the same session the API issues. No separate
  login. The sign-in page lists the session methods the API offers
  (`GET /api/v1/session/methods`); 0054 adds a connector's directory fields to
  it.
- **The app shell** (DESIGN.md §4): the fixed, labelled left navigation, the
  top bar, the content column, the 1024px responsive floor and its collapse,
  no horizontal page scroll, a skip-to-content link, and the router under
  `/console/`. The navigation lists only screens that exist; each later phase
  adds its own entry.

### Deployment identity and tenant context (M18, M19)
- The console always says **which deployment** this is: name, instance id,
  version set, and health (0046), with the operator-assigned accent stripe
  (DESIGN.md §5). An operator with a staging and a production instance open in
  two tabs must never have to guess which is which, and the distinction has to
  survive a screenshot.
- Where more than one tenant is in scope, the current tenant is visible in the
  chrome and switching is explicit. With a single tenant, none of this appears
  — tenancy is invisible to a deployment that does not use it (M18).
- If the deployment is supervised, say so and name the supervisor, as an
  `--s-info` badge (DESIGN.md §5). The badge's link to the audit view filtered
  to that supervisor's actions is 0051's, because the audit view is; leave the
  badge ready to carry it.

### Design machinery (M20)
`ui/DESIGN.md` is the specification; this is what implementing it obliges in
this phase, and every line of it is checkable.

- **One token file.** Colour, type, space, radius, elevation and motion live
  in `tokens.css` (or the framework equivalent) and nowhere else. No component
  carries a raw hex, a raw font size, or a raw radius.
- **Both themes ship together.** Light and dark are both designed, follow the
  system by default, and the operator's override persists. Dark is not an
  inversion filter and not a follow-up PR.
- **Both densities** (DESIGN.md §3.7), remembered per operator.
- **Nothing is fetched from the internet at runtime.** Fonts and icons are
  bundled into the binary. An air-gapped deployment must render identically,
  and a security product must not report its operators' activity to a CDN.
  0050 adds the test that proves it in a browser.
- **The static checks**, each a CI job this phase adds (DESIGN.md §10):
  1. a token lint that fails on a colour, font size or radius outside the
     token file (§10.1);
  2. a contrast test over the token pairs themselves, both themes, asserting
     each against the threshold that actually applies to it — the palette in
     DESIGN.md is already verified this way, so the test should pass on
     arrival and exists to keep it true (§10.2);
  3. the string and format lint (§10.9);
  4. the catalogue key test (below);
  5. the bundle budget: ≤ 350 KB gzipped JS on first load, fonts ≤ 120 KB
     (§11).

### Multilingual support, English only (M21)
Build the machinery; write one catalogue. `ui/DESIGN.md` §9 is the
specification — this is the shape of what lands.

- **Every user-visible string comes from `ui/src/locales/en.json`**, ICU
  MessageFormat, keyed by a stable id. `en.json` is the **only** catalogue
  committed in this series. Do not add a second locale, do not
  machine-translate one to "prove" it works, and do not ship a language picker
  with one entry — the picker appears when there is something to pick.
- **No sentence built by concatenation**, and no user-visible text baked into
  an image or sprite. Plurals and interpolation belong to the message.
- **Logical CSS properties throughout** (`margin-inline-start`, never
  `margin-left`) and `dir` taken from the locale. No RTL locale ships; this is
  what stops adding one from being a restyle of every component.
- **Layout absorbs +40% expansion** — no control sized to its English label,
  no single-line assumption for catalogue text.
- **`Intl` for every date, time, number and list**, through locale helpers
  that every later phase uses. Timestamps render in the operator's zone with
  UTC on hover; the copyable form is ISO-8601 UTC.
- **Identifiers stay LTR, mono and unsubstituted** in every locale: session
  and decision ids, fingerprints, hostnames, labels, rule names, policy source.
- **Operator-authored content is never translated** — a rule name is data, and
  "explain why" must cite the rule that is actually in the bundle.
- **Errors come from the API as a code plus parameters** (0019) and from the
  compiler the same way (0005); the console builds the sentence. Build the
  helper that turns a code and its parameters into a message here; 0050 builds
  the component that renders it and its fallback. If either phase shipped
  prose only, say so and raise it rather than parsing English.
- **The server is not asked to localise.** The locale is resolved in the
  browser from the operator's stored preference and `navigator.languages`; the
  server negotiates no language and returns no translated content.
- **The pseudolocale builds.** An `en-XA` build accents and pads every string
  to +40%, and a mirrored `en-XB` build sets the direction (DESIGN.md §10.7,
  §10.8). This phase makes both builds; 0050 renders the screenshot set under
  each and fails on what they find.

## Out of scope
- Every screen. Fleet is 0050's, and the rest are the series table's.
- The component inventory (DESIGN.md §7) beyond what the shell itself needs,
  and the browser checks (axe, the screenshot baseline, the offline test,
  reduced motion, the pseudolocale runs). They are 0050's.
- Enterprise screens (approval inboxes, report schedules and history,
  recertification campaigns). Enterprise adds its own, served through the same
  shell from `/host-console/<provider>/` (M27). This phase does not add the
  `server.Options` field a host hands its screens through, and says in its
  learnings what that field must carry (its patterns, its catalogue keys and
  one handler, as `north.Console` does) so the phase that adds it can. They
  inherit these tokens and this component set; a second visual identity inside
  one console is the thing M20 exists to prevent.
- Re-opening the design. DESIGN.md's palette, scale and constraints are
  settled input to this series. Correct it where it is wrong — in this PR,
  with the reason — but do not spend the session redesigning it.
- **Any locale other than English.** Translations are a later phase and a
  different skill; this series ends when a second catalogue would be the only
  thing left to add. Also out of scope: server-side localisation, a
  translation management integration, and locale-aware content in the audit
  store (M21 keeps records in codes and data, permanently).

## Acceptance criteria

**Function**
- `make build` produces one binary serving the console with no Node present.
- 0038's namespace tests stay green, and pass with the real console mounted in
  place of its stub (`internal/httpapi/north/namespace_internal_test.go`):
  - `TestEveryPathIsInsideItsOwnNamespaceAndNoOther`: every path the console
    serves is under `/console/`, and none is under `/api/v1/` or a host's
    prefix;
  - `TestEachNamespaceAnswersOnlyInsideItsOwnPrefix`: an unknown API path
    answers the API's `404` envelope, never `index.html`; a deep link outside
    `/console/` does not load the console; `/` redirects to `/console/` and
    nothing else does;
  - `TestAConsoleOutsideItsPrefixIsRefused`: a host screen's assets cannot
    shadow one of the console's, and a host catalogue key equal to one of the
    console's is refused;
  - `TestEveryWayAHostCanOverlapControlIsRefusedOrAnswersNothingOfControls`
    (`namespace_test.go`) and the route-listing tests, unchanged.
- An operator signs in through OIDC with the API's own session and no other.
- With one tenant in scope no tenant control appears; with two, the current
  one is shown and switching is explicit. A supervised deployment shows the
  supervisor's badge. Assert each.

**Design** — each of these fails the build, not the reviewer's patience:
- The token lint passes: no colour, font size or radius outside the token file.
- The contrast test passes for every semantic pair in both themes.
- Bundle budget holds: ≤ 350 KB gzipped JS on first load, fonts ≤ 120 KB.
- Fonts and icons are bundled into the binary; 0050's offline test proves
  nothing else is fetched.

**Localisation**
- The string lint passes: no user-visible literal in a component, and no date
  or number formatted outside the locale helpers.
- `ui/src/locales/en.json` is the only catalogue in the tree, and a test
  asserts every key it holds is referenced and every referenced key exists.
- The `en-XA` and `en-XB` builds are produced by a reproducible step, and every
  catalogue string comes out accented and padded in `en-XA`.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0049-console-shell-learnings.md`. Its summary block MUST give
the asset build/embed mechanism, the auth flow, the routes the shell consumes,
how a later console phase adds a screen (its route, its navigation entry, its
catalogue keys, its API calls), and **how Hoplock Enterprise adds screens to
this shell** without forking it — which, per M20, includes how it inherits the
tokens rather than bringing its own look — and, per M21, **how it adds its own
catalogue keys without colliding with this one**, since Enterprise ships
screens this repository never sees.

Details MUST also record: the framework chosen and why, within DESIGN.md §11's
constraints; where the token file, the locale helpers and the catalogue live;
how the static checks are wired into CI; what the `server.Options` field for a
host's screens must carry; and **any change made to `ui/DESIGN.md` in this
PR**, with the reason — a design document that drifts silently from the
console is worse than none, and the next session reads it expecting it to be
true.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names. Its `CHANGELOG.md` section says the
console is served at `/console/` and what it shows so far.
