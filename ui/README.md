# UI

The management console: fleet health, "explain this decision", audit query,
policy authoring, and inventory. It is served from the server binary rather
than deployed separately, so a self-hosted install is one process plus Postgres.

It is a client of the north-bound API and holds no privilege the API does not
grant it (PLAN M2).

Its **visual design is specified, not improvised**: `ui/DESIGN.md` is the design
system this console is built to — tokens, type scale, component inventory,
states and motion — and PLAN M20 is the decision that makes it binding.

Built in **phases 0049–0059**, its look chosen with the operator in **0052**,
and audited for one look and feel across every screen in **0060**. Older documents number it differently; PLAN §10's
table says what each old number is now.

## Working on it

TypeScript, React and Vite; Node as `ui/.nvmrc` names it. `make build` does not
need any of it: the build is committed to `internal/console/dist`, which the
server embeds and serves under `/console/`.

```sh
npm ci              # the locked toolchain
npm run build       # rebuild internal/console/dist — commit the result
npm run check       # the static checks, unit tests, build, budget and pseudolocale builds
npm run dev         # a dev server; point it at a running north-bound listener

# The browser checks (DESIGN.md §10.3–§10.8): Go, Chromium
# (npx playwright install chromium) and an empty Postgres database the run
# empties, seeds and serves from.
HOPLOCK_E2E_DSN=postgres://… npm run e2e
HOPLOCK_E2E_DSN=postgres://… npm run e2e:update   # after a deliberate design change
```

| Where | What |
| --- | --- |
| `src/styles/tokens.css` | the one token file: every colour (both themes), type size, radius, space, motion |
| `src/locales/en.json` | the one catalogue: flat keys, ICU MessageFormat, no `:` in a key |
| `src/i18n/` | the locale helpers (`format.ts`), messages, the API-error helper, the pseudolocales |
| `src/api/routes.ts` | every north-bound route the console calls, as the route table writes it |
| `src/screens/registry.tsx` | every screen: path, navigation entry, density, permission |
| `src/components/` | the component set every screen is built from (DESIGN.md §6, §7) |
| `src/api/permit.ts` | whether a route is offered to the session: its permission, held in the tenant |
| `scripts/` | the string and format lint, and the bundle budget |
| `test/` | contrast, catalogue keys, pseudolocales, chrome rules, status rules, the route test |
| `e2e/screens.ts` | the registry of screens the browser checks run over: a screen is checked by registering it |
| `e2e/fixture/` | the seeded server they run against (`serve.sh`, `seed.yaml`, each screen's fixture) |
| `e2e/baseline/` | the committed screenshot baseline |
| `gallery/` | a test page with every component in every state, never in the product build |

The checks are `ui/DESIGN.md` §10's; CI runs each as its own job.
