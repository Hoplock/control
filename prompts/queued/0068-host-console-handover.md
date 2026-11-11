# 0068 — A host's console reaches the shell

## Read first
- `docs/PROTOCOL.md` — session workflow; §3 on `ext/` and `server/` as a
  compatibility promise; §8's "A console phase shows what it built".
- `docs/PLAN.md` — §2: **M27** (the north-bound listener is four namespaces;
  a host's screens, assets and catalogue live under
  `/host-console/<provider>/`, a host's catalogue keys are `<provider>:<key>`
  and one equal to Control's is refused when it is loaded), **M15** (a host
  starts Control through `server/`, and what it hands over is
  `server.Options`), **M20**, **M21** (the server never localises; a host's
  strings are a catalogue like Control's), **M23** (a field added to
  `server.Options` is a public change, listed in the changelog); §3 at the
  `server/` package and the console's mount; §10's rows for 0049 and 0056.
- **`ui/DESIGN.md`** §4 (every URL a console writes stays under its own
  prefix), §9 (catalogues, keys, pseudolocales), §10 (the checks a host's
  strings must pass too), §11 (the CSP and no runtime fetch from elsewhere).
- `docs/learnings/` — read summaries; open **in full** `0049-console-shell`
  (its Details: "What a host's console field on server.Options must carry")
  and `0056-console-extensions-and-reports` (how a report's name is looked
  up in a host's catalogue, `ui/src/i18n/host.ts`). Read the summaries of
  `0015` (the `server/` package) and `0038` (the namespace and its tests).

## Objective
A host can hand Control its console — its patterns, its catalogue and one
handler — through `server.Options`, Control mounts it under the host's
`/host-console/<provider>/`, and Control's shell loads the host's catalogue so
that what the host registered is named in the host's own words. Today nothing
can: `north.Options.Consoles` takes a host's console, but `server.Options`
and `daemon.Host` have no field for one, so every report pack's report is
named by its code (0056).

## In scope
- **`server.Options.Console`** (and `daemon.Host.Console`), shaped as 0049's
  learnings record: `Patterns []string` (absolute, under
  `/host-console/<provider>/`, method-less), `Catalogue []string` (every key,
  each `<provider>:<key>`) and one `Handler http.Handler`, mounted through
  `north.Options.Consoles` with the host's `Provider`, so the mount's existing
  checks refuse a pattern outside the prefix and a key equal to Control's at
  start-up. Additive to a public package: listed in the changelog, and
  `make release-check` must agree it is not a break.
- **The shell learns the host.** The shell's page names the host's provider
  and the catalogue's URL before the first paint, as it names the stripe
  (`data-stripe`), only when a host console is mounted; Control's console
  never guesses a host from anything else.
- **The shell loads the host's catalogue** for the active locale from
  `/host-console/<provider>/locales/<locale>.json`, under the shell's CSP
  (same origin), refusing every key that is not `<provider>:<key>`, and
  provides it through `HostCatalogueContext` (`ui/src/i18n/host.ts`). A
  catalogue that fails to load is a named gap where a host's name would have
  been, never an error page: every name falls back to its code (M21). The
  `en-XA`/`en-XB` builds pseudolocalise the host's catalogue as they do
  Control's.
- **The fixture host mounts a console** with a catalogue naming
  `example.com/e2e/compliance`'s reports and a parameter
  (`<host>:reports.<provider>.<report>[.parameters.<name>]`), so the Reports
  screen's names are proven through the real mount.

## Out of scope
- Loading a host's screen modules into Control's shell, and a navigation
  entry for them: a later phase, which this one's learnings must name.
- Any change to `ext`, the contract, or the north-bound API's routes.

## Acceptance criteria
- A host passing `server.Options.Console` is mounted under its own
  `/host-console/<provider>/` and nowhere else;
  `TestEveryPathIsInsideItsOwnNamespaceAndNoOther` walks it; a pattern outside
  the prefix, and a catalogue key equal to Control's or not prefixed by the
  provider, refuse start-up, each asserted through `server.Main`.
- With no host console, Control's shell is byte-for-byte what it was, and
  makes no request under `/host-console/`.
- With the fixture host's console, the Reports screen names
  `access-review` and its `scope` parameter in the host's words, and a
  report the catalogue does not name by its code; with the catalogue's
  request failing (substituted in the browser), every report is named by
  its code and the gap is named.
- A host key that is not `<provider>:<key>` in the loaded catalogue is
  ignored and never rendered.
- 0049's static checks and 0050's browser checks stay green, the `en-XA`
  run accenting the host's names.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`, with screenshots of the Reports screen named through
the host's catalogue, light and dark. Move this prompt to `implemented/`; add
`docs/learnings/0068-host-console-handover-learnings.md`. A field added to
`server.Options` changes what Enterprise can call: queue the downstream sync
(`docs/CROSS-REPO-PROTOCOL.md` §4.1).

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
