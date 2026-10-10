# Changelog

What each release of `github.com/hoplock/control` contains, newest first, for
anyone who depends on the module or runs the binary. Hoplock Enterprise reads it
before every bump of its pin (its E3).

What a release is, when one is cut, and what its number promises is decision
**M23** in `docs/PLAN.md` §2. It is not restated here.

**Adding an entry.**

- List what someone consuming the module or running the binary can observe. Do
  not list prompts, plans, learnings or CI.
- A phase releases in its own PR. Add a `## vX.Y.Z` section above the newest
  one, and move anything under `## Unreleased` into it. In the section:
  - `### Breaking`, when the release changes a public package incompatibly.
    `make release-check` finds those changes and prints them;
  - the phase or phases it contains, by number;
  - the changes to public packages, by identifier;
  - a behaviour change that no API diff can see, which nothing else catches.
- A change that does not release goes under `## Unreleased` until one does.
- A released section is what its tag says. Never edit one; correct it in the
  next release's section.
- `make release-check` says what merging the tree releases, and refuses a
  version number that does not match the change.

## Unreleased

## v0.2.0

A host binary can start Control.

- **Phase:** 0015, the public server package and host routes.
- **Public packages:** `server` is added, beside `ext`, and is the same
  compatibility promise. `server.Main` is Control's whole command line and
  `server.Run` the daemon alone; `server.Options` carries what a host adds:
  `Provider`, `Registry`, `HostSections`, `HostConfig`, `Routes` (`server.Route`)
  and `ErrorCodes`. A host route reads its caller with `server.CallerFrom`
  (`server.Caller`) and answers a failure with `server.WriteError`. `ext` is
  unchanged.
- **Permissions:** two codes are added to the closed set. `report:write` is
  held by `admin` alone; `license:read` by every role except `integration`.
  Control serves no route that needs either: a host's routes name them.
- **`hoplock-control`** is one call to `server.Main` and otherwise unchanged:
  the same flags, subcommands, output, configuration and routes. Two things a
  build or an operator can see:
  - an interrupt or `SIGTERM` now cancels a running subcommand, which stops
    with an error, where it used to end the process outright;
  - the `VERSION=` override lands in `internal/daemon`, so an `-X
    main.version=…` passed by hand no longer sets anything. Use
    `make build VERSION=…`.

## v0.1.0

The first release.

- **Phases:** 0001–0013, and 0014, which cut this release. `docs/PLAN.md` §10
  says what each one delivered.
- **Public packages:** `ext`, as `ext/README.md` describes it. `ext.Points()` is
  the authoritative list of its extension points. It is the module's only
  public package.
- **`hoplock-control --version`** reports Control's module version from the
  build's own information: the tag at a release, a pseudo-version after one, a
  `+dirty` suffix for a modified tree, and `dev` when the build has no version.
  Built into another program, it reports the version of Control that program
  depends on, never the program's own. A Control swapped in by a `replace`
  directive reports `dev` and names its replacement.
