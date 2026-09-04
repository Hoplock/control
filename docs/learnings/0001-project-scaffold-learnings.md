# 0001 — project scaffold — Learnings

## Summary
- **What shipped:** the empty-but-real repository — Go module, package skeleton,
  Apache-2.0 per-file headers and their checker, Makefile, golangci-lint v2 config,
  two-leg CI, a strict YAML config loader, and a daemon that loads config, says
  who it is, and starts nothing. No product behaviour.
- **Key packages/files:** `go.mod`, `Makefile`, `.golangci.yml`,
  `.github/workflows/ci.yml`, `scripts/license-check.sh`, `config.example.yaml`,
  `internal/config/`, `cmd/hoplock-control/`, `ext/doc.go`, a `doc.go` in every
  `internal/` package.
- **Module path:** `github.com/hoplock/control`. **Go floor:** `go 1.24.0`
  (the `go` directive). **CI legs:** `1.24.0` and `stable`, both with
  `GOTOOLCHAIN: local`.
- **Key interfaces/types added:** `config.Config{Tenant, Listeners{South,North},
  Database{DSN}, Log{Level}}`, `config.FieldError{Field,Msg}`, `config.Load`,
  `config.Parse`, `(*Config).Validate`, `LogConfig.SlogLevel`,
  `config.DefaultTenant` = `"default"`, `config.DefaultLogLevel` = `"info"`.
- **Config keys (all of them):** `tenant`, `listeners.south`, `listeners.north`,
  `database.dsn`, `log.level`. Required: the two listeners and the DSN.
- **Makefile targets:** `build test vet lint fmt license-check tidy clean run
  check help`, plus `contract-check contract-sync conform`, which exit 1 with
  "implemented in phase 0002".
- **Linters:** `errcheck govet ineffassign staticcheck unused`; formatters
  `gofmt` + `goimports` with `github.com/hoplock/control` as the local prefix.
- **Database tables/migrations added:** none. `migrations/` exists with a README;
  phase 0003 puts the first SQL in it.
- **Decisions made/affected:** M2 (two listener fields from day one), M11, M12
  (`tenant` present but unexposed), M13, M14 (Apache-2.0 chosen and applied),
  M15 (`ext/` created as the only public package).
- **Gotchas:** `contract/` is deliberately **not** created here — 0002 vendors it
  and `contract-check` compares it against upstream, so a placeholder file in it
  would be drift. The licence checker and `make fmt` both exclude it already.
- **What the NEXT session must know:** add your phase's config under a new
  top-level key, document it in `config.example.yaml`, and extend the loader's
  tests — strict decoding means an undocumented key is a startup error. Replace
  the `contract-check`/`contract-sync`/`conform` placeholders rather than adding
  new targets beside them.
- **Cross-repo dependencies:** none. Nothing here touches a shared surface
  (`docs/CROSS-REPO-PROTOCOL.md` §1), so this PR owes no downstream sync.

## Details

### Go floor and the two-leg matrix

`go.mod` says `go 1.24.0` and CI runs the tests twice: once pinned to exactly
`1.24.0`, once on `stable`. The floor leg is only meaningful because the
workflow sets `GOTOOLCHAIN: local` at the top level — without it a runner whose
toolchain is older than the `go` directive silently downloads a newer one, both
legs end up testing the same toolchain, and the floor drifts upward without
anyone deciding it should. Each job prints `go version` so a reader of the log
can confirm the floor leg really ran on the floor.

The floor moves **only when a dependency forces it** (PLAN §8). When it does,
change the `go` directive and the `"1.24.0"` entry in the matrix together; the
workflow comment says so.

### The package skeleton

Every directory in PLAN §3 exists. The `internal/` packages each carry a
`doc.go` whose comment states what belongs there and cites the decision it
serves — that is the whole content of the package today, and it is deliberate:
an empty directory does not survive git, and a package with no stated purpose
gets a different purpose invented for it by the next session.

Two directories are intentionally missing their placeholder:

- `contract/` — vendored and drift-checked (M1). Anything committed there now
  would fail `make contract-check` in 0002. `.golangci.yml`, `make fmt`, and
  `scripts/license-check.sh` already exclude the path, so 0002 only has to
  populate it.
- The repository root has no `config.yaml`; it is gitignored, and a test in
  `cmd/hoplock-control` fails if one is ever committed (it would carry a DSN).

`ui/`, `deploy/`, `migrations/`, `cmd/pdpconform`, and `cmd/policyctl` hold a
short README naming the phase that fills them in.

### `ext/` is public on purpose, and it is the only thing that is

`ext` is the sole non-`internal` package in the module (M15). Its doc comment
carries the two invariants — Control never imports Enterprise, and every seam
ships a real default here — because the package is where someone will read them
at the moment they matter. Phase 0004 adds the interfaces and the import-graph
guard; until then the package is a doc comment and nothing else, which is the
right amount of surface to promise.

### Config: strict, minimal, and validated by naming the field

`config.Parse` decodes with `yaml.Decoder.KnownFields(true)`, so an unknown key
— top-level or nested — is an error that names the key. Validation returns a
`*config.FieldError` carrying the dotted YAML path, so a caller can
`errors.As` it and a human reads `config: listeners.south: is required`.

Three choices worth keeping:

- **`listeners.south` and `listeners.north` are two fields from the first
  release** (M2), and validation rejects them being equal. One field that later
  becomes two is a breaking config change, and the two surfaces sharing a port
  is the escalation M2 exists to prevent.
- **`tenant` is present, defaulted, and unexposed** (M12). Nothing reads it yet.
- **`DatabaseConfig` implements `fmt.Stringer` and prints
  `database{dsn:[redacted]}`.** The DSN carries a password in any real
  deployment, and `%v` on a `Config` is exactly how one ends up in a log line.
  A test asserts a password does not survive formatting. If a later phase adds
  another secret-bearing struct, give it the same treatment.

`TestExampleConfigLoads` loads `config.example.yaml` through the real loader, so
the documentation cannot rot into something that does not parse. Keep that test
passing when you add keys.

### Versioning

`make build` stamps `main.version`, `main.commit`, and `main.date` from
`git describe --tags --always --dirty`, `git rev-parse --short HEAD`, and the
build time. A plain `go build ./...` leaves them empty and `versionString()`
falls back to the VCS stamps the toolchain embeds in `debug.BuildInfo`, so the
binary reports something true either way; a build with neither calls itself
`dev` rather than inventing a number. CI checks out with `fetch-depth: 0` so
`git describe` has tags to find.

`main.run` takes its args and writers as parameters and returns an error instead
of calling `os.Exit`, which is what makes the startup path testable without a
process. Keep that shape when you add listeners: `run` should take a context and
return, and `main` stays four lines.

### Licence headers

`docs/LICENSE-HEADER.md` is the specification; `scripts/license-check.sh` is the
enforcement. It checks the copyright line by regex (any year), the SPDX line
verbatim, and that line 3 is blank so the header is not absorbed into a package
doc comment. It walks every `.go` file except `contract/` and `.git/`, and fails
if it finds no files at all — a checker that passes vacuously is worse than none.

### Follow-ups deliberately not done here

- No `govulncheck` job yet; PLAN §8 lists it under CI and phase 0016 owns
  hardening. Adding it now would fail on a module with one dependency for
  reasons no one here can fix.
- No `go mod tidy` drift check in CI. Worth adding when the dependency list is
  large enough for it to catch something.
- No Dockerfile. `deploy/` (phase 0016) needs one; it is that phase's to shape,
  since the topology decides what the image must contain.
