# 0073 — A host's screens in Control's shell

## Read first
- `docs/PROTOCOL.md` — session workflow; §3 on `ext/` and `server/` as a
  compatibility promise; §8's "A console phase shows what it built".
- `docs/PLAN.md` — §2: **M27** (a host's screens, assets and catalogue live
  under `/host-console/<provider>/`, loaded by Control's shell; every URL a
  console writes stays under its own prefix), **M15** (a host starts Control
  through `server/`, and hands over its console as `server.Options.Console`),
  **M2** (a console is a north-bound API client with no privilege of its
  own), **M20** (one design, one component set; no runtime fetch from another
  origin), **M21** (a host's strings are a catalogue like Control's, keys
  `<provider>:<key>`), **M23** (a change to `server.Options` is a public
  change, in the changelog); §3 at `server`, the console mount and `ui`;
  §10's rows for 0049, 0050, 0056 and 0069.
- **`ui/DESIGN.md`** §4 (URLs under each console's own prefix; a link from
  one console to the other names the other's prefix), §7 (the component set a
  host's screens build from), §9 (a host's catalogue, and how it reaches the
  shell), §10 (the checks a host's screens are held to, over their own
  registry), §11 (the CSP, the bundle budget, no runtime fetch from elsewhere).
- `docs/learnings/` — read summaries; open **in full**
  `0069-host-console-handover` (how the host's console is mounted and how
  the shell learns the host and loads its catalogue: `data-host`,
  `data-host-catalogue`, `ui/src/i18n/host.ts`, `HostCatalogueProvider`),
  `0049-console-shell` ("A later phase adds a screen", the router and
  `consoleHref`), and `0050-console-components-and-fleet` ("A host's screens
  use this set, not their own", the screen registry and the RBAC harness).

## Objective
A host's screens are reached from Control's console: Control's shell learns
which screens a host's console offers, lists them in its navigation for the
operators whose permissions they need, and loads each screen's module from the
host's own `/host-console/<provider>/` — under the shell's CSP, from this
origin — rendering it inside the shell's chrome with the shell's tokens,
components, catalogue machinery and API client. Today 0069 hands the host's
catalogue over and nothing else: a host's screens can be served, but nothing
in Control's console leads to them.

## In scope
- **How a host's console declares its screens**: a manifest the host's
  console serves under its own prefix (for example
  `/host-console/<provider>/screens.json`), naming each screen's path (under
  `/host-console/<provider>/`), its navigation label key (`<provider>:<key>`,
  in the catalogue 0069 loads), the permission it needs (one of Control's,
  as a host route names one: M15), and its module's URL under the host's
  prefix. Refused entries are ignored and named as a gap, as 0069's foreign
  keys are; a manifest that cannot be loaded is a gap, never an error page.
  Whether Control also checks the manifest at start-up — through a field
  added to `server.Console` — is this phase's decision, recorded in its
  learnings; a field added there is listed in the changelog and must pass
  `make release-check` as additive.
- **The navigation entry**: each declared screen listed in Control's
  navigation, after Control's own, offered only to an operator holding its
  permission (as `ui/src/api/permit.ts` offers Control's), and linking to the
  host's prefix — never to a path under `/console/` (DESIGN.md §4).
- **Loading a screen's module** from the host's prefix (a dynamic `import()`
  of a same-origin URL the CSP's `script-src 'self'` allows) and rendering it
  inside Control's shell, with what a host screen needs handed to it:
  `HostCatalogueContext`, the shell's I18n, the API client, the tenant, and
  the component set it is built from (0050's learnings). A module that fails
  to load is the screen's error state, named as the host's, never Control's
  outage (M11).
- **The history fallback**: a deep link under `/host-console/<provider>/`
  opens Control's shell on that screen. The host's console serves its own
  fallback (its patterns); this phase decides how that fallback hands over
  to Control's shell — for example the host serving Control's shell page,
  which `server` would then have to offer — and states it in `server.Console`'s
  doc comment.
- **The fixture host** declares one screen and serves its module, so the
  navigation entry, the RBAC hiding, a deep link, a module that fails, and a
  manifest that cannot be loaded are each proven in a browser against the
  real mount; the screen is registered in `ui/e2e/screens.ts` and held to
  DESIGN.md §10's checks, `en-XA` and `en-XB` included.

## Out of scope
- Any change to `ext`, the contract, or the north-bound API's routes.
- Enterprise's own screens: this phase builds the seam and proves it with the
  fixture host; Enterprise builds its screens against it in its repository.
- A second locale for a host's catalogue (M21: English is the only one).

## Acceptance criteria
- With no host console, and with a host console that declares no screens,
  Control's shell and navigation are what they were, and nothing beyond 0069's
  catalogue request is made under `/host-console/`.
- With the fixture host's screen declared, an operator holding its permission
  sees its navigation entry, named in the host's catalogue, and one without
  it does not; following it loads the module from the host's prefix and
  renders the screen inside the shell; every URL it writes stays under
  `/host-console/<provider>/`.
- A deep link to the host's screen opens it; a module that fails to load is
  the screen's error state naming the host; a manifest that fails to load, or
  an entry outside the host's prefix or naming a permission Control lacks, is
  a named gap and never an error page.
- `TestEveryPathIsInsideItsOwnNamespaceAndNoOther` still walks every
  console's mounts; 0049's static checks and 0050's browser checks stay
  green, over the host's screen too.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`, with screenshots of the navigation and the host's
screen, light and dark, and the `en-XA` and `en-XB` builds. Move this prompt
to `implemented/`; add `docs/learnings/0073-host-screens-in-the-shell-learnings.md`.
A change to what a host's console must serve, or a field added to
`server.Console`, changes what Enterprise can build: queue the downstream sync
(`docs/CROSS-REPO-PROTOCOL.md` §4.1).

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
