# 0069 — a host's console reaches the shell — Learnings

## Summary
- **Shipped (`v0.56.0`, MINOR; no migration; north-bound API unchanged; `ext`/contract untouched; `server` additive):** `server.Options.Console *server.Console{Patterns, Catalogue, Handler}` → `daemon.Host.Console *daemon.HostConsole` → `north.Options.Consoles` with the host's `Provider`. The shell is told the host on `<html>` and loads its catalogue; Reports names a pack's reports and parameters through it.
- **Start-up refusals (before any file is read):** `daemon.Host.checkConsole` runs the real mount on a router nothing serves (`north.CheckConsoles`) against the build's keys (`console.CatalogueKeys()`, which reads only `en.json`). New refusal beside 0038's: **no pattern serving `locales/en.json`** (`north.HostCatalogueDefault`), because the shell always requests it.
- **The page names the host** only when one is mounted: `console.Options.Host{Provider, Catalogue}` adds `data-host="<p>" data-host-catalogue="/host-console/<p>/locales/"` beside `data-stripe`. With none, `/console/*` is `dist/index.html` byte for byte (`TestTheShellNamesAHostOnlyWhenOneIsMounted`).
- **Shell side (`ui/src/i18n/host.ts`, `HostCatalogueProvider.tsx`, `main.tsx`):** `declaredHost()` accepts only `<owner>/<name>` and a directory inside `/host-console/<host>/`; `loadHostCatalogue` fetches `<dir><intlLocale>.json` (10 s timeout), never throws; `hostMessages` keeps only `<host>:<key>` string values, pseudolocalised under a forced `en-XA`/`en-XB`. `HostCatalogue.state` is `loading | ok | failed`; `hostText` reads only `ok`.
- **Reports waits** for a `loading` catalogue as for its data (no code→name flicker), and a `failed` one with a report listed is the gap `reports.partial.hostCatalogue` (`reportsModel.unnamedBy`).
- **Fixture host** (`ui/e2e/fixture/host/console.go`): one pattern, `locales/en.json`, naming `access-review` and its `scope` (`hoplock/e2e-fixture:reports.example.com/e2e/compliance.access-review[.parameters.scope]`); `privileged-sessions` and `owner` stay codes. `serveBuild` (pseudo builds) now copies the server's whole `<html>` tag, not just the stripe.
- **Decisions:** none added or amended; register unchanged. PLAN M15's text, §3 (`server`, the console mount, `ui`) and §10 (0056, 0069 rows; new **0073** row) revised in place; `ui/DESIGN.md` §9 gains how a host's catalogue reaches the shell.
- **Gotchas:** `--update-snapshots` alone keeps an image whose change is under the 1% tolerance — a renamed title was; use `--update-snapshots=all -g <screen>` for a deliberate change. The reports `partial` state is now substituted (catalogue 503) on top of the seeded flaky pack, so it has baseline images of its own.
- **NEXT:** **0073** (queued) loads a host's screen modules into the shell and lists them in the navigation; it reads this file's "What 0073 inherits".

## Details

### Why the shape is a pointer, and why Control's own patterns are reused

`Options.Console` is `*Console`: nil is "no console", and a non-nil one with
nothing in it is refused ("mounts nothing"), so a host that meant to hand a
console over and built it wrong hears about it. A value type would have made
the zero `Console{}` silently mean "none". `daemon.HostConsole` mirrors it
without `Provider`, which is always the host's own (`Host.consoles` fills it),
so neither layer can mount a console under somebody else's name — the mount
would refuse that too (`is not this server's host`).

`Host.consoles(control)` is the one place the list `north.Options.Consoles`
receives is built: `buildNorth` passes the real shell, `checkConsole` passes a
stand-in with `console.Patterns()` and the build's keys. That is what makes
the pre-start check and the mount judge the same input.

`console.Patterns()` became a package function (the method delegates) and
`console.CatalogueKeys()` reads only `dist/locales/en.json`: `console.New`
gzips every asset at best compression, which `Validate` — run by every
subcommand — has no business paying for.

### The catalogue requirement

The prompt asked for 0038's existing refusals through `server.Main`. One more
was added: a host console none of whose patterns routes
`/host-console/<p>/locales/en.json` (checked with a throwaway `ServeMux` over
the host's own patterns). Without it a host that forgot the catalogue would
start, every name it registered would be a gap on every screen for good, and
the only person told would be an operator opening Reports. It costs a host
nothing it was not already obliged to serve.

### How the shell learns the host, and why not from anything else

The server writes both attributes; the shell parses them once in `main.tsx`
before the first render. `declaredHost` refuses a provider not shaped as the
server's `providerShape`, and a catalogue URL that is not a clean directory
inside `/host-console/<host>/` — so even a hand-edited page cannot point the
shell at another host's strings, another origin, or `/console/`. The server
refuses the same on its side (`console.New`: absolute, clean, ends in `/`,
not `//`). The URL is a directory because the server does not negotiate a
language (M21): the shell appends its own `<locale>.json`, and a pseudolocale
asks for `en.json` (`intlLocale`).

Why a fetch and not `ApiClient.call`: the catalogue is a file of the host's
console, not a route of the north-bound table, so it is not in `ROUTES` and
not subject to the route test; it carries no credential the console does not
already send, and the CSP's `connect-src 'self'` is the only reach it has.

### Pseudolocales

`hostMessages` pseudolocalises only when `FORCED_LOCALE` is set, the same
constant `catalogue.ts` folds, so production carries none of `pseudo.ts`
(checked: the accent map is absent from `dist/assets/index-*.js`; first load
248.1 KB of 350, +0.7 KB). The unit test for that path mocks
`../src/i18n/catalogue` (`test/hostCataloguePseudo.test.ts`). The browser's
§10.7 run of `reports` now sees the host's names and passes; an explicit
`en-XA` test asserts the name is bracketed, accented and padded.

### Tests

- Go: `server` — 14 new start-up refusals through `server.Main` with an
  absent config path (each names its pattern or key), and the real start-up
  test serves the host's catalogue, its fallback, Control's 404 envelope for
  four near misses, and a shell page naming the host. `internal/console` —
  byte-for-byte without a host, only `<html>` changes with one, six refused
  `Host`s. `internal/daemon` — `consoles`/`shellHost`/`checkConsole` for a
  host and for Control alone. `internal/httpapi/north` — the walk's stub host
  console now has a host's shape (index, assets, catalogue, fallback; count 4),
  four more request-level probes, two more refusals (no catalogue; another
  locale only).
- UI unit: `test/hostCatalogue.test.ts` (31) and
  `test/hostCataloguePseudo.test.ts` (2).
- Browser (`e2e/extensions-reports.spec.ts`, "a host's names for its reports
  (0069)", 4; `e2e/pseudo.spec.ts`, 1): names in the host's words and codes
  where it gives none, with exactly one request under `/host-console/`; the
  catalogue failing (substituted, the `partial` state); foreign keys
  substituted into the catalogue and never rendered; the page with
  `data-host*` stripped making no `/host-console/` request.
- `TestEveryPathIsInsideItsOwnNamespaceAndNoOther` walks the stub, not the
  fixture's console: it is a north-internal test and cannot import `daemon`
  (a cycle). What joins them is `Host.consoles`, tested in `daemon`, and the
  real start-up test in `server`, which probes the mounted console at the
  request level.

### What 0073 inherits

- `HostCatalogueContext` is provided above `App` for every page, signed in or
  not; a host screen reads it with `useHostText` / `hostText`.
- A host console's patterns are the host's to choose; Control requires only
  the catalogue. 0073 decides how a deep link under the host's prefix becomes
  Control's shell (the host's fallback cannot serve Control's page today:
  `server` offers no handler for it).
- The shell loads nothing else from the host. A manifest of screens, the
  module loading and the navigation entry are 0073's; the CSP already allows
  a same-origin `import()`.

### Cross-repo

`server` changed (additively), so a downstream sync for `hoplock/enterprise`
is queued in `prompts/downstream/queued/` (the PR's `## Cross-repo impact`
names it): Enterprise can now hand its console over, and its catalogue's
`hoplock/enterprise:reports.<pack>.<report>` keys name its packs on Control's
Reports screen. Proxy: none.
