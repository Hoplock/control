# 0001 — Project scaffold & conventions

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially §3 (layout), §8 (conventions), §2 (M11, M12,
  M13, M14).
- `docs/learnings/` — empty; you are the first session.

## Objective
Stand the repository up so every later phase has somewhere to put its code and a
CI job that tells it the truth. No product behaviour ships here.

## In scope
- **Go module**: `github.com/mauroasilva/commandproxymanagemente`. Set the
  `go` directive to the current stable minor as the floor and document that it
  moves only when a dependency forces it (PLAN §8).
- **Package layout**: create the directory skeleton from PLAN §3 with a
  `doc.go` in each `internal/` package stating what belongs there in two or
  three sentences. Empty directories do not survive git; a `doc.go` that says
  what a package is for also stops the next session inventing a different
  answer.
- **`LICENSE` + per-file headers**: proprietary license (already in the repo
  root) and the two-line SPDX header on every `.go` file
  (`docs/LICENSE-HEADER.md`).
- **`Makefile`** with, at minimum: `build`, `test` (race), `vet`, `lint`, `fmt`,
  `license-check`, `tidy`, `clean`, `run`. Add placeholder targets for
  `contract-check`, `contract-sync` and `conform` that exit non-zero with
  "implemented in phase 0002" — a missing target and a failing target read very
  differently in a Definition-of-Done checklist.
- **`.golangci.yml`** (schema v2), matching the bastion repo's linter set:
  `errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`, with `gofmt` and
  `goimports` formatters and this module as the local prefix.
- **CI** (`.github/workflows/ci.yml`): build/vet/test on **both** the go.mod
  floor and the latest stable, with `GOTOOLCHAIN: local` so the floor is
  enforced rather than silently satisfied by a toolchain download; plus lint and
  license jobs. Comment *why* the matrix has two legs — the bastion repo learned
  this the hard way and the comment is what keeps it.
- **`internal/config`**: YAML loader with **strict decoding** (unknown keys are
  an error), plus `config.example.yaml`. Cover only what the scaffold needs:
  the two listener addresses (M2 — they are separate fields from the start,
  because one field that later becomes two is a breaking config change), the
  Postgres DSN, log level, and a `tenant` placeholder (M12). Every field
  documented in the example file.
- **`cmd/management`**: a `main` that loads config, logs its version, starts
  nothing, and exits cleanly on SIGINT/SIGTERM. `--version` works.

## Out of scope
- The contract, the conformance suite (0002), the database (0003), and every
  endpoint. Nothing listens on a port yet beyond what a health check needs.

## Acceptance criteria
- `make build`, `make test`, `make vet`, `make lint`, `make license-check` all
  pass on a clean checkout.
- `make contract-check` and `make conform` fail with the "phase 0002" message.
- `cmd/management --version` prints a version derived from git.
- Config loading is unit-tested: a valid file loads; an unknown key is an error;
  a missing required field is an error naming the field.
- CI is green, and the floor leg genuinely runs on the floor (check the job log
  says the version you expect, not a downloaded newer one).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0001-project-scaffold-learnings.md`. Summary block MUST give the
module path, the Go floor and the CI versions, the config struct's shape and
every key in it, the Makefile targets, and the linter set — every later phase
starts by adding to one of those.
