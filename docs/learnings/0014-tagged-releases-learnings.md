# 0014 — tagged releases — Learnings

## Summary
- **What shipped:** **M23**, `CHANGELOG.md`, `make release-check` and its guard,
  the CI `release` job, and a `--version` that reports Control's own module
  version. **This PR's merge cuts `v0.1.0`.** To confirm it, run
  `go list -m github.com/hoplock/control@v0.1.0` outside the module, or read
  the `release` job's last step on the merge commit.
- **Key files:** `scripts/release-check.sh` (`check`, `notes`, `plan`),
  `scripts/release-check-guard.sh`, `.github/workflows/ci.yml` (`release-check`
  and `release`), `release_workflow_test.go`, `cmd/hoplock-control/version.go`,
  `Makefile`, `CHANGELOG.md`.
- **Bump rule (M23), verbatim:** "While MAJOR is `0`: **MINOR** for a release
  that contains a phase, or an incompatible change to a public package;
  **PATCH** for anything else." MAJOR stays `0` until a decision of its own.
  A release is the next MINOR or the next PATCH after the latest, and every
  incompatible change is listed under `### Breaking`.
- **How a release is cut:** the phase's PR adds `## vX.Y.Z` to `CHANGELOG.md`
  (PROTOCOL §4). On merge, the `release` job tags the pushed commit, with that
  section as its annotation. It runs on a push to `main` only, `needs:` every
  other job, is the only job with `contents: write`, and is serialized. It then
  publishes a GitHub Release and resolves the version through
  `proxy.golang.org`. **If it fails on `main`**, fix the cause (a repository
  setting that refuses tags included) and re-run it **on the same commit**. A
  commit that is already tagged resumes. Never push a tag by hand, and never at
  another commit.
- **Public packages:** derived by `go list ./...`: every package with non-test
  Go files that is not `package main` and has no `internal` path element.
  Today that is `ext`. Each is diffed against the latest tag by
  `golang.org/x/exp/cmd/apidiff`, pinned by `APIDIFF_VERSION`
  (`v0.0.0-20261007192929-f45ad48fbe92`) in the Makefile.
- **What a build reports:** Control as the main module reports Go's stamp: the
  tag, a pseudo-version after it, or `+dirty`. Control as a dependency reports
  its `Deps` version, never the host's version or VCS stamps. A replaced
  Control reports `dev (… replaced by <path>)`. No build info reports `dev`.
  `VERSION=` is the only override.
- **Migrations:** none. **Decisions:** M23 added (register row; rendered in §8
  and §10). M19 is not amended. `ext/` and `contract/` are untouched.
- **Cross-repo:** owes `hoplock/enterprise` a sync, queued as
  `prompts/downstream/queued/control-PR#53-tagged-releases.md`. Proxy: none.
- **NEXT session:** your PR adds its own `## vX.Y.Z` (a phase is a MINOR). The
  last line of `make release-check` says what your merge releases. 0015 moves
  `version` into `internal/daemon`, so it must move `-X main.version` too.

## Details

### Why every phase is a release, and what the check holds

The request asked for one tag. M23 makes every phase a release instead. Its
rule 3 gives the reason: Enterprise's binary embeds the whole server (M15), so
every phase changes what Enterprise ships, and Enterprise takes a change only
from a release (its E3). One tag would have unblocked Enterprise's 0001 and left
the same gap at the next Control phase it needs.

`make release-check` refuses every case the prompt lists, and the guard has a
case for each. It also refuses a few that follow from the rules, and those are
interpretations a reviewer should see:

- **The change fixes the number.** A MINOR release that contains no phase, no
  incompatible change and no `### Breaking` is refused. So is a skipped version
  (`v0.1.0` → `v0.3.0`). Rule 4 says PATCH is "for anything else", and a
  number its author could choose is a number a consumer cannot rely on. M23
  rule 4 now says so: "the next MINOR or the next PATCH after the latest".
- **A declared break is a MINOR** even when apidiff cannot see it, so a PATCH
  with `### Breaking` is refused. This is the 0022 case: a behaviour change to a
  public package.
- **The format refuses three more things:** a pending section that is empty, a
  `### Breaking` with nothing listed under it, and a changelog that does not
  open with `## Unreleased`.
- **A release left behind on the base branch.** On a pull request (CI sets
  `GITHUB_BASE_REF`), a pending version that is already on the base branch with
  no tag is refused. That PR does not release it, and its final line would
  otherwise say it does. Locally there is no base to compare, so this is
  skipped.

Two things are deliberately **not** checked, and review is what covers them:

- **That `### Breaking` names each identifier the diff found.** apidiff's
  object strings (`Sink.Flush`, `(*T).M`, `T.M, method set of *U`) are not
  stable text to search a changelog for, and a false refusal would teach
  authors to paste tool output instead of writing an entry. The refusal prints
  every finding, ready to list.
- **That a section names each phase by number.** `v0.1.0` names its phases as
  a range, `0001–0013`.

### The diff

- Each public package is written as export data with `apidiff -w` in both
  trees, and the two are compared. apidiff exits 0 whatever it finds, so the
  script parses its "Incompatible changes:" and "Compatible changes:" sections.
  A package that leaves the public set is incompatible (`package removed`). A
  new one is compatible.
- The old tree is `git archive <tag>`, unpacked into a temporary directory.
  `go list` there resolves that tree's own dependencies, from the network or
  the module cache.
- **`shopt -s inherit_errexit` is load-bearing.** Without it, bash ignores a
  failure inside `$(...)`, and an apidiff that crashed would read as "no
  change".
- **The pin.** `golang.org/x/exp` has no tags, so the pin is a
  pseudo-version. `cmd/apidiff` is not a module of its own: it is in the main
  x/exp module. apidiff reads the compiler's export data through the x/tools it
  was built with, which couples the pin to the Go release, as golangci-lint's
  pin is. Bump it in the commit that moves the toolchain. The guard proves that
  the new pin still reports an added method.

### The release job

`plan REV BEFORE` decides what to do. A push introduced a version when the
version's heading is absent at `BEFORE` (`github.event.before`). The job asks
about the push and not about the commit's parent because of rebase merges: they
push several commits, and only the head gets a run. A first-parent walk is used
only to name the commit in a refusal.

How it fails, and what to do:

1. **Tag creation is refused** by a ruleset or tag protection. The job goes
   red at "Tag the merge commit". Fix the setting, then re-run the job on the
   same commit. Never push the tag by hand, and never at another commit.
2. **The proxy is slow.** The bounded retry waits about 16 minutes. If it runs
   out, re-run the job on the same commit: the tag exists, so the job resumes
   and does not tag again. Do not ask the proxy for a version before its tag
   exists, because a not-found answer can be cached for a while. This session
   asked for `@v0.1.0` once, while it built this phase.
3. **GitHub cancels a waiting run.** A concurrency group keeps one run waiting.
   If a third merge arrives while one run is in progress and one is waiting,
   the waiting one is cancelled. The next run then refuses ("introduced by an
   earlier push, at …") and names the commit. Re-run the job there.
4. **Another job is red on `main`.** The release job is skipped. Re-run the
   failed jobs on that commit, and GitHub re-runs the jobs that depend on them.
5. **A release left behind blocks the next phase's PR.** Two versions without a
   tag are refused, until the job is re-run on the commit that added the first.

Two command flags are load-bearing:

- `git tag --cleanup=verbatim`: by default git strips every line that begins
  with `#` from a tag message, which is every heading in it.
- `gh release create --verify-tag`: without it, the API would create a missing
  tag at the branch head, not at this commit.

`release_workflow_test.go` makes those, and the job's shape, fail the build if
they change.

The repository is public, which was checked: the proxy can fetch it as
`github.com/hoplock/control`. A tag pushed with `GITHUB_TOKEN` starts no
workflow, and nothing here listens for tags.

### What a build reports

Go 1.24 and later stamp the main module's version from VCS: the tag at a tagged
commit, `vX.Y.(Z+1)-0.<time>-<rev>` after it, and `+dirty` for a modified tree.
**Untracked files count as modified.** A shallow clone stamps a `v0.0.0-`
pseudo-version, which is why CI checks out with `fetch-depth: 0`. The `commit`
and `date` ldflags variables are gone, and the date was the build time, so two
builds of one commit spelled their versions differently. VCS stamps are read
only when Control is the main module: in a host's binary they are the host's.

The test job proves two things: `make build` and `go build` print the same
line, and `make build VERSION=…` lands. The linker silently ignores an `-X` that
names no variable, which is why 0015's prompt now warns about it.

### Out of scope, and what follows

- **`v1.0.0`** needs a decision of its own that declares `ext` (and `server`)
  stable. After that, every incompatible change is a new major and a new module
  path (`/v2`), which Enterprise must import afresh. It is worth taking only
  once Enterprise's phases have implemented every point against real
  integrations, and with a deprecation policy (how long a deprecated identifier
  stays). The check's MAJOR rule changes with it.
- **Signed tags and build provenance** deserve a decision of their own:
  gitsign or sigstore, GitHub artifact attestations, SLSA. The release job is
  the one place to add them.
- **Not done, as the prompt says:** release artifacts, maintenance branches,
  a separate `ext` module, and the other version axes.

### Test notes

- The guard has 34 cases. Blinding the check to incompatible changes turned 5
  of them red. Dropping a job from `needs:`, or giving `lint` write access,
  turned the workflow test red.
- Run locally: golangci-lint v2.13.2 (0 issues), actionlint v1.7.7 with
  shellcheck 0.11.0 (clean), `go test -race ./...`.
- Not provable before merge: the release job itself. The PR's release-check log
  shows the tag and notes it will publish.
