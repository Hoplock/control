# 0050 — the console's component set, browser checks and Fleet — Learnings

## Summary
- **Shipped (`v0.37.0`, MINOR; no migration; `ext`/`server`/`contract` untouched):** the component set, DESIGN.md §10's browser checks 3–8 as CI jobs (`console-e2e`, one per rule), the seeded fixture, the RBAC harness, the route test, and Fleet at `/console/fleet`.
- **Routes Fleet consumes:** `GET …/proxies` (`fleet:read`; one page, `?limit=` 100 unless the URL names one, `?after=` the answer's `next`), `POST …/proxies/{proxy}/withdraw` (`fleet:write`, typed-id confirm), and the shell's `GET …/instance/health` (`instance:read`), whose `fleet[].summary` draws the chart.
- **Component set:** `ui/src/components/`, one family per file. `Status`/`STATUS` is the one status vocabulary; `States.tsx` (`ScreenFrame`, `ErrorState`, `EmptyState`, `PartialNotice`, `Skeleton`) is the four-state frame; `DataTable` + `Pagination` show one M26 page; `ConfirmDialog` is the one destructive confirm; `src/api/permit.ts` `offered()` gates every control that calls the API. `ui/gallery/` shows every component in every state (test page, never in `dist`).
- **A later phase registers a screen with every harness:** (1) its `SCREENS` entry in `src/screens/registry.tsx` and its routes in `src/api/routes.ts` (the route test reads them); (2) ONE entry in `ui/e2e/screens.ts` — `path`, `ready`, `states` (an API answer substituted in the browser, or `seeded`), `densities`, and `mutations` (route, path, permission, `reveal`, `control`), which the RBAC harness asserts hidden and API-refused per role; (3) its data in `ui/e2e/fixture/seed.yaml`, or a function in `SEEDS` that drives the real APIs; (4) its baseline: `HOPLOCK_E2E_DSN=… npm run e2e:update` in `ui/` (or `make ui-e2e UPDATE=1`), images committed in `ui/e2e/baseline/`.
- **A host's screens use this set, not their own (M20):** they build from Control's `ui/src/components/` at the pinned module version (aliased into their build), take every status from `STATUS` (one Control lacks is an upstream request to add it to DESIGN.md §6 and `STATUS`), and are held to the same six checks over their own registry.
- **Decisions:** none added or amended. `ui/DESIGN.md` §3.2, §3.6, §6, §7, §10 and PLAN §3, §10 (0050's row) revised in place (Details).
- **Gotchas:** `main` scrolls, not the page (grow the viewport, `fitContent`); a table skeleton also has `tbody tr`, so `ready` names data rows; a run needs a FRESH server (the `mutations` project withdraws `px-spare-09`; `serve.sh` empties the database); the fixture pins the instance id and the software version, or screenshots reflow.
- **NEXT (0051):** add the timeline and time range picker to the set (and the gallery), and register Explain and Audit in `ui/e2e/screens.ts`.

## Details

### What shipped, where

| What | Where |
| --- | --- |
| primitives and composites (§7) | `ui/src/components/`: `Button`, `Field` (input, textarea, select, checkbox, radio, switch, `Choices`), `Combobox` (+ `CommandPalette`, ⌘K/Ctrl-K, mounted in the nav's foot), `Tabs`, `Menu` (+ `Tooltip`), `Dialog` (+ `Drawer`, `ConfirmDialog`), `Toast`, `Badge` (+ `Chip`, `Avatar`, `KeyboardHint`, `Breadcrumb`), `Pagination`, `States` (skeleton, spinner overlay, empty, error, partial, the frame), `Code` (code block, JSON viewer), `CopyableId`, `DataTable` (+ `TableSkeleton`), `FilterBar` (chips, saved views), `Tree`, `PageHeader`, `Status` |
| Fleet | `ui/src/screens/Fleet.tsx`, `FleetChart.tsx`, `fleetModel.ts` (the status rules, unit-tested), `Fleet.module.css` |
| the browser checks | `ui/playwright.config.ts`, `ui/e2e/*.spec.ts`, `harness.ts`, `open.ts`, `screens.ts`, `global-setup.ts` |
| the fixture | `ui/e2e/fixture/serve.sh`, `control.yaml`, `seed.yaml`, `fleet.ts` |
| the gallery | `ui/gallery.html`, `ui/gallery/`, `ui/vite.gallery.config.ts` → `ui/build/gallery[-en-XA|-en-XB]` |
| the route test | `ui/test/routes.test.ts` over `ui/test/north-routes.json`, kept current by `internal/httpapi/north/console_routes_test.go` |
| status rules | `ui/test/status.test.tsx` (M11, M17, M19, the unknown-code fallback) |

Dependencies added (dev only, exact): `@playwright/test` and `playwright-core`
`1.63.0` (one version, so `@axe-core/playwright`'s `Page` type is
Playwright's), `@axe-core/playwright` `4.14.0`. No runtime dependency: every
primitive is hand-written over the platform — `<dialog>` opened modal (the
browser traps and restores focus), the popover API for menus (top layer, so a
table's scroll container never clips it; anchored by the invoker's implicit
anchor and `position-area`), native form controls. First-load JS 101 KB of
350; fonts unchanged.

### How each browser check is wired into CI

`console-e2e` in `.github/workflows/ci.yml` is a matrix, one job per rule, on
`ubuntu-24.04` with a `postgres:16` service; each job runs `npm ci`,
`npx playwright install --with-deps chromium`, then `npm run e2e -- <args>`,
which builds both pseudolocales and the gallery and runs Playwright, whose
`webServer` runs `e2e/fixture/serve.sh`. A failing job uploads
`ui/build/e2e/results/` (actual/expected/diff) and the HTML report. The
release job `needs` it.

| Job | Runs | Asserts |
| --- | --- | --- |
| axe | `design.spec.ts -g axe` | WCAG 2.2 A/AA, zero `serious`+, every screen and state, both themes |
| screenshot baseline | `design.spec.ts identity.spec.ts -g "baseline\|deployments"` | every screen × theme × density (where it has a table) + loading/empty/error; two deployments' chromes differ by > 5% of pixels |
| no network at runtime | `design.spec.ts -g "external origin"` | every non-origin request aborted and none attempted; page equals the baseline |
| reduced motion | `design.spec.ts -g "reduced motion"` | every element's (and pseudo-element's) transition/animation duration is 0 |
| en-XA pseudolocale | `pseudo.spec.ts -g en-XA` | no text or `aria-label`/`title`/`placeholder`/`alt` without an accented letter, outside `translate="no"` and `<time>`; no box clipping its text; no sideways page scroll |
| en-XB mirrored | `pseudo.spec.ts -g en-XB` | `dir="rtl"`; no CSS rule or inline style with an unequal physical pair; every block box at its `en-XA` mirror (±2px, kerning off) |
| fleet and RBAC | `fleet.spec.ts rbac.spec.ts mutations.spec.ts` | Fleet's acceptance criteria; RBAC at the API; the withdraw flow |

The pseudolocale builds and the gallery are served **in the browser** by
`serveBuild` (`harness.ts`): requests under `/console/` are answered from
`ui/build/<mode>/` (or the gallery's build) with the server's CSP, while
`/api/` still reaches the seeded server. The server binds nothing for tests.

**Mutations run** (each check seen failing): a hardcoded `"Refresh now"` in
Fleet → en-XA fails naming it; `.panel { margin-left: 24px }` → en-XB fails
naming the rule. While building, the checks found real defects, all fixed: the
deployment chip's `aria-label` did not contain its visible text (axe
`label-content-name-mismatch`, WCAG 2.5.3 — the chip's name is now its text and
`chrome.deployment.open` is gone); a loading button hid its label with
`visibility` and lost its name; an empty combobox listbox had no options
(`aria-required-children`); the deployment's display name was not marked as
operator data; the chart's patterned segments rendered solid (a CSS `fill`
beat the pattern attribute).

### The baseline

`ui/e2e/baseline/`: **56 PNGs, 2.54 MB**, 1×, 1280 px wide, full content
height. Compressed losslessly with `oxipng -o4 --strip safe` (about 3.3 MB as
Playwright writes them); recompressing after an update is optional, and
Playwright reads either. Masked: `<time>` and `[data-volatile]` (correlation
ids). Not masked, because the fixture pins them: the instance id
(`inst-0050e2e0…`, inserted before first start — the server keeps an identity
it finds) and the software version (`-X …/internal/daemon.version=v0.0.0-e2e`).
Pseudo-classes in the gallery are forced through CDP
(`CSS.forcePseudoState` on `[data-force]`), so one image shows hover, focus and
active. Tolerance: `maxDiffPixelRatio 0.01`, `threshold 0.2`. Generated in
this repository's cloud container (Ubuntu 24.04, Playwright 1.63.0's
Chromium); if CI's rendering differs, the job's artefacts carry the actual
images.

### The fixture

`serve.sh` empties the database `HOPLOCK_E2E_DSN` names (`DROP SCHEMA public
CASCADE`), migrates, pins the identity, seeds, binds `ada`→admin,
`avery`→auditor, `fenn`→fleet-admin, and runs the server (north
`127.0.0.1:18081`, south `:18080`, `insecure_cookies`, heartbeat and relay
TTLs 24h so the seed stays live). `global-setup.ts` then runs each `SEEDS`
function — Fleet's publishes per-proxy documents north-bound and reports
running state south-bound with each proxy's own credential — waits for the
deployment's health to settle (every node reported, fleet counted), and saves
each role's signed-in state. Fleet's proxies, one per state: `px-core-01`
applied, `-02` pending restart, `px-dmz-03` rejected, `-04` fetch failed,
`px-edge-05` in progress (v1 running, v2 desired), `-06` never reported,
`-07` never heard from (the partial gap), `px-old-08` revoked, `px-spare-09`
withdrawn by the mutation test.

### Fleet's rules

- **Health** follows the server's own `fleet.FleetSummary`, so the table and
  the chart cannot disagree: live → Healthy; heard from, then silent → Stale;
  enrolled, never heard → Unreachable (outage tone); pending → Pending;
  revoked → Revoked. A stale or unreachable proxy on the page is the
  partial gap.
- **Configuration** is the report, never the publish: none → Not reported;
  `applied` and caught up → Applied; `applied` behind a newer desired →
  In progress (M19); `pending_restart` → Pending restart (info);
  `rejected` → Rejected (warn: the proxy keeps its document; not a deny);
  `fetch_failed` → Fetch failed (outage, M11).
- Sort, filters and the by-zone tree act on the page held, and the table says
  so when the answer was cut. Filters, the view and the open proxy are in the
  URL; saved views are named URLs per operator in `localStorage`.

### Changes to `ui/DESIGN.md` in this PR, and why

1. **§3.2** adds `--bg-inverse`/`--text-inverse` (a tooltip) and `--scrim`
   (behind a modal; translucent, so outside the contrast pairs and named
   outside `--bg-*`, which the contrast test resolves as opaque).
2. **§3.6** adds the layout tokens the components needed (icon size, dialog,
   drawer, menu, search widths, table and list heights) and `--m-spin`: a busy
   indicator is a loop, and "nothing animates longer than 240ms" was written
   for transitions.
3. **§6** adds the configuration states to the vocabulary's rows, and says
   where `STATUS` lives and how each config state reads (with the reasons
   above).
4. **§7** says where the set lives, that a table is one M26 page, what a saved
   view is, which overlays are the platform's, and that the gallery exists.
5. **§10** says how checks 3–8 run, over one registry, and what each asserts
   exactly — in particular that §10.8 is asserted by geometry and by
   unequal physical pairs, because the CSSOM writes `margin: 0` as equal
   physical longhands.

### Changes to 0049's code

`ErrorText` is gone (its stand-in role is `States.ErrorMessage`, and screens
use `ErrorState`); the Deployment view renders through `ScreenFrame` (health
unreadable beside a readable identity is partial; no node reported is the
nodes' empty state) — `deployment.forbidden` is gone because a refusal is
`ErrorState`'s; `error.correlation` became `error.correlation.label` (the id is
now a `CopyableId`); `Ident` is unchanged.

### Noticed, not changed

- 0049's preferences are keyed by the **session's** principal id (`sess-…`), so
  a theme or density does not survive a new sign-in except as "the last
  operator's". DESIGN.md §3.7 says "remembered per operator". Worth a fix in a
  console phase; the browser checks set density through `last`.
- Seeded proxies report contract version 0 (nothing authorizes in the
  fixture), so Fleet shows "None"; a fixture that drives `/v1/authorize`
  would show real numbers.
- PLAN §10's row for 0049 is not marked built.

### Cross-repo

No shared surface changes (`ext`, the contract, the cross-repo protocol and
decision ids are untouched). The release changes what Enterprise's binary
serves, and Enterprise's screens are to be built from this component set, so
a downstream sync for `hoplock/enterprise` is queued (the PR's
`## Cross-repo impact`). Proxy: none.
