# 0049 — the console's shell, build and design machinery — Learnings

## Summary
- **Shipped (`v0.36.0`, MINOR; no migration; `ext`/`server`/`contract` untouched):** the console at `/console/` — sign-in, the deployment/tenant/supervisor chrome, a Deployment view, both themes and densities, English only — and DESIGN.md §10's static checks 1, 2, 9 plus catalogue keys and budget as CI jobs. Config `console.stripe`.
- **Build/embed:** source `ui/` (React 19, TypeScript, Vite 8, `intl-messageformat`; Node per `ui/.nvmrc`); `npm run build` writes **`internal/console/dist`, committed**; `internal/console` embeds it (`//go:embed all:dist`) and serves it. `make build` needs no Node; CI's `console-build` job rebuilds and fails on any diff. Not `ui/dist`: see Details.
- **Mount (M27):** daemon → `north.Options.Consoles` → one `north.Console{Patterns: c.Patterns(), Catalogue: c.Catalogue(), Handler: c}`; patterns `/console/{$}`, `assets/`, `locales/en.json`, `licenses/`, `{path...}` (the shell). `internal/console` never imports `north` (north's tests mount it); `console.Prefix` is held equal to `north.ConsolePrefix`.
- **Auth:** no login of its own. `GET /api/v1/session/methods` → OIDC `POST …/federated/{c}/start` → IdP → connector `redirect_uri` = `/console/sign-in/callback/{c}` → console calls `GET …/federated/{c}/callback?<IdP params>[&tenant=]` → API sets the `hoplock_session` cookie. Break-glass = `POST /api/v1/session/local`. SAML is listed but cannot complete from the console (Details). Several tenants: the page asks which (`tenant_ambiguous`), only then.
- **Routes the shell calls** (`ui/src/api/routes.ts`): the six session routes above plus `GET /session`, `DELETE /session`, `GET …/tenants/{tenant}/instance`, `…/instance/health` (every 30 s), `…/instance/supervisors`.
- **A later phase adds a screen:** one `SCREENS` entry in `ui/src/screens/registry.tsx` (path under `/console/`, `nav` catalogue key, density, permission) — the nav lists exactly these; its strings as flat keys in `ui/src/locales/en.json` (no `:`); its routes in `ROUTES` with the Go table's exact pattern and permission, called only through `ApiClient.call(name, …)`; links through `Link`/`consoleHref`, never a hand-written path.
- **Enterprise's screens:** served by Control's mount under `/host-console/hoplock/enterprise/`, keys `hoplock/enterprise:<key>` (refused if equal to one of Control's, judged against the real `en.json`); they inherit the look by using `tokens.css`'s custom properties, which are on `:root` of the one page — never a token file of their own. **No `server.Options` field hands them over yet**: whoever needs it raises an upstream request for a field shaped as `north.Console` (Details).
- **Decisions:** none added or amended; PLAN §3 revised in place; **`ui/DESIGN.md` corrected** (§3.2 dark `--bg-selected`, §3.5/§9 Latin-only faces, §5 stripe tokens, §10.1/§10.9 scope, §11 framework and `dist` location).
- **NEXT (0050):** read Details' "What 0050 inherits": `Status` is the one status component to grow, `ErrorText` is a stand-in for its error component, `errorMessage()` is the helper, `ROUTES` is what its RBAC/route test reads, and the pseudolocale builds are CI artefacts (`console-pseudolocales`). Every console PR shows its screens and states as screenshots (`docs/PROTOCOL.md` §8).

## Details

### Why the build lives in `internal/console/dist`, not `ui/dist`

DESIGN.md §11 said `ui/dist/`. `go:embed` cannot reach outside its package's
directory, so embedding `ui/dist` needs a Go package in `ui/` — and that package
would be a **public** package of the module (it is neither `internal/` nor
`main`), so `make release-check` would diff it against every release and
Enterprise could import it (M15, M23). PLAN §3 already makes this argument for
`internal/store/migrations`. So Vite writes to `../internal/console/dist`, the
package beside it embeds it, and DESIGN.md §11 says so now. The pseudolocale
builds go to `ui/build/<mode>` (gitignored): `en.json` must be the only
catalogue in the tree.

**Committed vs built in CI:** committed, with a drift check. The prompt allows
either; committing is what makes `make build` and `go install` work with no
Node at all, including for a host binary that depends on the module (the
module proxy serves the committed `dist`; a CI-only build would ship an empty
console to every `go get`). The cost is that a PR touching `ui/src` must commit
the rebuilt `dist`, which the `console-build` job enforces with `git status`
over `internal/console/dist` after `npm run build`.

### The framework, inside DESIGN.md §11

**React 19.** The decisive reason is §11's own list: Radix, cmdk and TanStack
Table — the headless primitives 0050's component set will want — are written
for React first. Svelte would be smaller, but 0049's whole first-load JS is
86 KB gzipped of the 350 KB budget, so size did not decide it.

- **ICU runtime:** `intl-messageformat` directly, with a 20-line binding
  (`ui/src/i18n/messages.ts`, `I18n.tsx`), not `react-intl`. `react-intl`
  brings its own date and number formatters, which would be a second place
  formatting happens, and the string lint would have to police them.
- **Router:** hand-written (`ui/src/router/`), ~80 lines over the History API.
  It is what makes "every URL is under `/console/`" structural: `consoleHref`
  is the only way to make one, and it refuses `.`/`..`.
- **Styles:** CSS modules plus two global files (`tokens.css`, `base.css`);
  no CSS-in-JS. Inline `style` is refused by the string lint, and by the CSP.
- **Dependencies (exact versions, `ui/package-lock.json`):** `react`,
  `react-dom`, `intl-messageformat`, `@formatjs/icu-messageformat-parser`
  (already intl-messageformat's own; used directly by the pseudolocaliser);
  dev: `typescript` 6.0 (the last with the JS compiler API the string lint
  uses — 7.x is the native port), `vite`, `@vitejs/plugin-react`, `vitest`,
  `stylelint` (built-in rules only, no plugins), `@types/*`. The fonts were
  copied out of `@fontsource-variable/*` into `ui/src/fonts/` with their OFL
  licences in `ui/public/licenses/` (served at `/console/licenses/`), and the
  packages removed.

### Where things live

| What | Where |
| --- | --- |
| token file | `ui/src/styles/tokens.css` — every colour as `light-dark(light, dark)`, so one declaration per token covers both themes; `data-theme` on `<html>` sets `color-scheme` for an override; `data-density="compact"` on the shell; `data-stripe` on `<html>` from the server |
| locale helpers | `ui/src/i18n/format.ts` (`dateTime`, `utc`, `number`, `list`; `isoUTC`), resolution and direction in `locale.ts` |
| catalogue | `ui/src/locales/en.json` (flat keys, ICU); `MessageKey = keyof typeof en`, so a mistyped key is a type error |
| API-error helper | `ui/src/i18n/errors.ts`: `errorMessage(t, envelope)` → `{text, known, correlationId}`; `ERROR_MESSAGES` maps code → key; an unknown code renders `error.unknown` naming the code |
| pseudolocales | `ui/src/i18n/pseudo.ts`; a build with `--mode en-XA`/`en-XB` forces the locale and rewrites the catalogue at load; production carries none of it (`import.meta.env.MODE` folds) |
| prefs | `ui/src/prefs/prefs.ts`: theme, density, tenant, per principal in `localStorage`; the last operator's theme before sign-in |

### The static checks, and how CI runs them

Each is its own job, so a failure names its rule (`.github/workflows/ci.yml`,
`console-checks` matrix and `console-build`); `make ui-check` runs them all
locally and is part of `make check`. The release job now also `needs` both.

| Check | Command | What it is |
| --- | --- | --- |
| token lint (§10.1) | `npm run lint:tokens` | stylelint built-ins: no hex/named/functional colour, no font size, radius, `px` margin/padding/gap or duration outside `tokens.css`; no physical property, `text-align: left/right`, four-value margin/padding |
| contrast (§10.2) | `npm run test:contrast` | `test/contrast.test.ts` resolves the token file's `var()`/`light-dark()` per theme and asserts 123 pairs: text 4.5 on six grounds, placeholder, text-on-accent; borders, focus ring, accent mark and every status glyph ≥ 3 (status hues are held to 4.5, as a label may be set in them); each stripe bar ≥ 3 and its chip text ≥ 4.5. `--border-subtle` is listed as exempt |
| string lint (§10.9) | `npm run lint:strings` | `scripts/lint-strings.mjs` over the TS syntax tree, plus `test/lint-strings.test.ts`, which shows it failing on each case |
| catalogue keys | `npm run test:catalogue` | `en.json` the only catalogue and byte-equal to `dist/locales/en.json`; every key referenced as a literal in `src/`; every key-shaped literal in a known namespace exists; no `:`; each message parses; and `test/pseudo.test.ts`: every message comes out of `en-XA` accented, bracketed, padded ≥ 40%, with the same arguments and still formatting, and out of `en-XB` inside RLO…PDF |
| budget (§11) | `npm run budget` | first-load JS (index's scripts and their static imports), gzip -9: **86 KB** of 350; fonts **87 KB** of 120 |
| build | `console-build` | typecheck, all unit tests, build, drift, budget, both pseudolocale builds uploaded as `console-pseudolocales` |

Go side: `internal/console/console_test.go` (prefix equals north's; catalogue
equals the source's `en.json`, so a stale `dist` fails `go test`; the shell's
CSP; only `/console/assets/` references; both fonts served from the binary;
immutable caching, gzip, ETag; each stripe's tokens in the built CSS),
`internal/config/console_test.go`, and 0038's namespace tests now mounting the
real console (`realConsole()` in `namespace_internal_test.go`), with two more
probes: a callback deep link is the shell, and a missing asset is a 404, not
the shell.

**Mutations run:** dark `--bg-selected` back to `--a-900` → contrast fails
(1.05:1, 1.77:1); an unreferenced key in `en.json` → catalogue keys fails;
`#fff`, `margin-left: 4px`, `font-size: 13px` in a stray CSS file → four
token-lint findings; each string-lint case is in its test.

### Verified in a browser, not only in tests

The server was run against a local Postgres 16 (migrate, `seed` a local
subject, `identity role-bind --role admin`, `console.stripe: orange`) and
driven with Playwright at 1280 and 800 px in both colour schemes: `/console/deployment`
→ sign-in with the return path → break-glass login → the Deployment view;
zero requests to any other origin; no horizontal page scroll at 800 px (the
hover panel had caused one; now capped). Two defects found that way and fixed:
a stale `refused` session state bounced a fresh sign-in back to sign-in (sign-in
now resets the session to loading), and a long build version pushed the page
sideways. Taking the PR's screenshots of every state found two more: the
navigation's surface stopped at one viewport's height on a long page, and at
1280 px the top bar wrapped to two rows, taller than the offset the sticky
navigation assumed. The shell is now an **app frame** — the top bar at
whatever height it wraps to, then the navigation and the content each
scrolling in their own row (`Shell.module.css`, `100dvh`) — and the theme and
density preferences moved from the top bar to the navigation's foot
(`shell/Preferences.tsx`), leaving the bar to identity. The `en-XA` and `en-XB` builds were rendered too: everything accented
and padded, `dir="rtl"` mirrors the shell (navigation on the right), and
identifiers stay LTR in mono. That is a smoke run, not 0050's checks.

### The chrome's rules, and where each is asserted (`ui/test/chrome.test.tsx`)

- Tenants in scope are the session's `scopes` keys (`GET /api/v1/session`);
  the console never widens them. One → no tenant control at all; several → a
  labelled `<select>` showing the current one (the operator's last, while in
  scope). The tenant is console state, **not in the URL**: switching is an
  explicit control (DESIGN.md §5). 0051 must decide whether a shared deep link
  carries it.
- Identity, health and supervisors are read only where the session holds
  `instance:read` in that tenant; otherwise the chrome says the identity
  could not be read, and the Deployment view says the role lacks it.
- Supervised = at least one **live** `sup-` credential in
  `…/instance/supervisors`; one `--s-info` badge each, naming its display name
  (or principal id). The badge has no link yet: 0051 adds the audit view it
  links to (filter by that principal id).
- `break_glass` on the session → a `--s-warn` badge.
- Health: `healthy`→allow, `degraded`/`stale`→warn, `in_progress`→info
  ("In progress", never a fault, M19), `unknown`→Not reported (M17), `left`→
  neutral, and a health that could not be read → outage "Unreachable" (M11).
  The chip shows name, short id (`inst-` + 8 hex, full id in the title) and
  status; hover/focus shows software version, contract version and node
  count with versions; click opens the Deployment view.

### What 0050 inherits, and what is a stand-in

- **The page does not scroll; `main` does.** A whole-screen screenshot or a
  scroll assertion acts on `main#content`, and Playwright's `fullPage` alone
  captures one viewport: grow the viewport to `main`'s scroll height first.
  Below 1024 px the navigation is a drawer over the content's grid row.

- `ui/src/components/Status.tsx` is **the** status component (§6 glyphs and
  tones, glyph in the hue, label in text colour). Grow it into the pill; do
  not render a status beside it.
- `ErrorText` is a minimal stand-in for 0050's error component: it shows
  `errorMessage()`'s sentence and the correlation id, deny-bordered for a
  refusal and outage-bordered for an outage.
- `ApiClient.call` returns `ok | refused | outage`, never conflating the last
  two; 401 on anything but a sign-in call signs the shell out. It sends no
  `Hoplock-Api-Version` header: the console ships in the same binary as the
  API, so "undeclared = current" is always right. A host's screens are not in
  lockstep and should declare theirs.
- `ROUTES` entries carry `permission`, exactly the Go table's, so 0050's
  "every call names a route and is offered only to a role holding it" test
  reads one list.
- The Deployment view's node table is plain markup on DESIGN.md's density
  tokens, not the data table; 0050 may swap it.
- Error codes with a sentence: the ten the shell meets plus `network`. Every
  other code renders the named fallback until a phase that surfaces it adds
  its key to `ERROR_MESSAGES`. Health reasons: all seventeen of
  `instance.AllReasons`, with `reason.unknown` as fallback.

### What a host's console field on `server.Options` must carry

Out of scope here, and recorded for whoever adds it (additive to `server`): a
field shaped as `north.Console` minus `Provider` (which is the host's own,
`server.Options.Provider`): **`Patterns []string`** (absolute, under
`/host-console/<provider>/`, method-less — the mount refuses anything else),
**`Catalogue []string`** (every key, each `<provider>:<key>`; one equal to a
key of Control's is refused at start-up, judged against the real `en.json`),
and **one `Handler http.Handler`**. Control's shell loading a host's screen
modules from there, and a navigation entry for them, is the further work that
phase owns; nothing in 0049's shell does it yet. The host should serve its
assets with the same CSP (`'self'` only) or the shell's page will refuse
them.

### SAML and the console

The SAML ACS is a POST from the IdP to `…/federated/{c}/acs`, which answers
JSON; the browser would display it. So the console lists a SAML connector
with a sentence saying it cannot complete from the console yet, rather than a
button that ends on a JSON page. Making it work needs the ACS to hand the
browser back to the console (an API change in this repository, not upstream).
Not queued as a prompt; worth one when a deployment needs SAML through the
console.

### Changes to `ui/DESIGN.md` in this PR, and why

1. **§3.2 `--bg-selected` dark `--a-900` → `--a-100`.** The dark accent ramp
   is inverted, so its `--a-900` is `#dee3fd`; body text on it was 1.05:1 and
   the contrast test failed on arrival. Everything else in the palette passed
   as DESIGN.md claimed, including `--text-tertiary`.
2. **§3.2 adds `--accent` and `--focus-ring`; §3.4 adds `--s-unknown`.**
   Aliases the components needed so they reference semantics, not the ramp.
3. **§3 intro:** both themes are one declaration per token, `light-dark()`.
4. **§3.5 and §9:** the bundled faces are their Latin subsets. Greek and
   Cyrillic subsets would add ~38 KB and break the 120 KB budget. The
   pseudolocaliser only uses Latin-1 accents, so `en-XA` renders in Inter.
5. **§3.6:** layout dimensions are tokens too.
6. **§5:** the stripe — `console.stripe`, five named colours, a table with
   both themes, none a status hue, and on every page including sign-in.
7. **§10.1, §10.9:** what the token lint and the string lint actually refuse.
8. **§11:** React is chosen; the build is committed to
   `internal/console/dist` (above).

### Gotchas

- `tsc` must stay on TypeScript 6.x while the string lint uses the compiler
  API (7.x has none in JS).
- `vitest` serves from `/`, so `paths.ts`'s base assertion skips under
  `MODE === "test"`.
- The token lint allows 1–3-value `margin`/`padding` (symmetric inline) and
  refuses 4-value, which is physical; a `calc()` with spaces inside a
  shorthand reads as several values — use the longhand.
- `localStorage`/`sessionStorage` failures are swallowed: preferences are a
  courtesy, and a sign-in whose pending flow cannot be stored reports
  "expired", which is true of that tab.

### Cross-repo

No shared surface changes (`ext`, the contract, the cross-repo protocol and
decision ids are untouched), but every release changes what Enterprise ships
(M23), and Enterprise's screens are built on this shell, so a downstream sync
for `hoplock/enterprise` is queued: see the PR's `## Cross-repo impact`.
Proxy: none.
