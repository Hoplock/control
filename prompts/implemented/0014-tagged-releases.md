# 0014 — Tagged releases: the first tag, a release from every phase after it, and what a version promises

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/8, under `## One thing for you, not
> fixable here` (its first paragraph), and answered here as one phase
> (`docs/CROSS-REPO-PROTOCOL.md` §3.2). That PR predates the request queues. Its
> request file is `enterprise-PR#8-control-release-tag.md` (§4.3).
>
> **Where it sits.** First in the queue. It depends only on merged phases: 0001
> (the Makefile, CI and `--version`) and 0004 (`ext/`, the public package a
> release versions). It goes first because Enterprise's first phase (its 0001)
> cannot meet its E3 until a release exists, so every Enterprise phase waits on
> this one, and because every phase after it then releases under the rule it
> adds. It is also small. The public server package (0015) comes next and cuts
> its own release under that rule. Should 0015 have merged first anyway,
> `cmd/hoplock-control/version.go` lives in `internal/daemon`, and nothing here
> depends on where.

## Read first
- `docs/PROTOCOL.md`. Read §3 for `ext/` as a compatibility promise, for
  revising the plan in place, and for a new decision's register row. Read §4,
  because this phase adds a line to every phase's Definition of Done, and §7,
  because it edits queued prompts.
- `docs/CROSS-REPO-PROTOCOL.md`:
  - **§1**: the `ext/` row says it reaches Enterprise as "a pinned, released
    module version". There has never been one. This phase makes the row true.
  - **§4.1** and **§4.3**: the sync this phase queues.
  - **§5**, at "The PR that answers an upstream request is not a sync".
- `docs/PLAN.md`:
  - **§2**:
    - **M15**: Enterprise imports this module and ships it inside its own
      binary. That is why every phase is a release, not only the phases that
      touch `ext/`.
    - **M14**: the open-source plane. The module is public, so its versions are
      served by the public module proxy and recorded in the checksum database.
      That is what makes a published version immutable.
    - **M19**: a deployment reports "the software version". This phase defines
      what that is. It is not the north-bound API version 0019 adds.
    - **M1**: the contract has a version of its own, pinned by commit in
      `contract/UPSTREAM`. Not this one either.
    - **M13**: Go.
  - **§8**: the module path, the Go floor, and the CI bullet. This phase adds a
    releases bullet and two CI steps.
  - **§10**: this phase's row. Read 0018's and 0023's rows only far enough to
    see that the contract re-vendor and `policy_version` are other version
    axes.
- `docs/learnings/`. Read the summaries, then open:
  - **`0001`** at "Versioning": how the binary stamps itself today, and why a
    build with no provenance says `dev`.
  - **`0004`**: what `ext/` promises, and how its catalogue is machine-readable.
- Code. Read it before designing anything:
  - `Makefile`: `VERSION`, `LDFLAGS`, `build` and `exhaustive-guard`.
  - `cmd/hoplock-control/version.go`, all of it.
  - `.github/workflows/ci.yml`, all of it. The release job depends on every job
    in it.
  - `scripts/exhaustive-guard.sh`: the house style for proving that a check
    catches a violation, rather than asserting that it does.
  - `ext/README.md`, "The compatibility promise".
- The request itself, for the requester's own words. It cites Enterprise
  **E3**. Read E3, and E1 beside it, in `hoplock/enterprise`'s `docs/PLAN.md`:
  their wording is what this phase must make true, and both are cited here by
  id, never restated. Then read Enterprise's
  `prompts/queued/0001-scaffold-and-control-integration.md` at "Dependency on
  Control": it is the stop this phase lets Enterprise remove.

## Objective
Make Enterprise's E3 satisfiable, now and at every phase after this one.
Control publishes **released versions** of `github.com/hoplock/control` that a
consumer can pin, can read the changes of before it bumps, and can trust never
to move.

Deliver five things:

1. **The release decision**: what a release is, when one is cut, and what its
   number promises.
2. **`CHANGELOG.md`**.
3. **A release check** that holds every PR to that decision.
4. **A release job** that tags a merged release. Nothing else tags.
5. **The first release, `v0.1.0`**, cut by this phase's own merge.

Plus one correction the first release makes necessary: a build reports
Control's own version, in one spelling.

## The request, in this repository's vocabulary
- *Asked for*, in the requester's words: "Control has published no release tag
  — `git ls-remote --tags` on `hoplock/control` returns nothing. E3 requires a
  *pinned released version*, and phase 0001 cannot satisfy that against an
  untagged module." It was still true when this prompt was written.
- *In this repository's words:* an annotated tag `vMAJOR.MINOR.PATCH` on a
  commit on `main`, of the one module (§8), that the public module proxy
  resolves. Enterprise's `go.mod` can then
  `require github.com/hoplock/control v0.1.0`. That is a released version, not a
  pseudo-version of `main`, and it needs no `replace`.
- *Until it exists:*
  - Enterprise's 0001 builds with its Control pin visibly unresolved, and
    cannot prove what E3 requires.
  - No Enterprise phase has a Control version to build against, to bump
    deliberately, or to print in `--version`.
  - Its 0008 runs its end-to-end criteria only once Control can be started from
    there and Control's north-bound API (0018) is pinned. With no releases,
    that pin cannot exist.

## What this phase answers, and where it differs from the ask
The shape is met as asked: `v0.1.0` is a tag on `main`. The requester could not
read this plan, and three things in it make one tag, on its own, the wrong
deliverable.

- **A release is needed at every phase, not once.**
  - *Why.* Enterprise's binary is Control plus its extensions (M15). So every
    phase here changes what Enterprise ships, not only the phases that touch
    `ext/`. And Enterprise takes a change only from a release (E3). One tag
    would unblock its 0001 and leave the same gap at the next phase it needs:
    its 0008 already waits on 0018, which changes no public package.
  - *What to build.* Every phase releases, in its own PR. CI cuts the tag from
    the merged PR, so a release never depends on somebody remembering to.
- **A version must say what it promises.**
  - *Why.* E3 has Enterprise read Control's changelog before every bump, and
    there is no changelog. `ext/` is a compatibility promise (PROTOCOL §3),
    but today only intention enforces it.
  - *What to build.* A changelog, a bump rule, and a check. The check compares
    the public packages with the previous release and refuses a break the
    release does not declare. Enterprise *implements* `ext`'s interfaces, so
    the check counts an added interface method as a break. A caller-side
    reading of compatibility misses exactly that case.
- **A published version can never change.**
  - *Why.* The module is public (M14). A version's content is fixed the first
    time anyone fetches it: the module proxy caches it, and the checksum
    database records its hash. A moved tag is never seen by proxy users, and it
    is a checksum failure for everyone else.
  - *What to build.* No session and no person pushes a tag. A tag is cut only
    after every check has passed on the merged commit. A bad release is
    retracted and superseded, never moved.

One more thing is half there already. The binary reports a version (0001), but
two builds of one commit spell it differently: `git describe` through the
Makefile, Go's own stamp through `go build`. And once 0015 lets a host start
Control, it would report the host's version under Control's name.

## In scope

### 1. The release decision (`docs/PLAN.md` §2)
Add it with the **next free `M` id**: M23 as this is written. Do not assume the
number, because 0022 also adds a decision with the next free id. Use the id you
take wherever this prompt says "the release decision", and add its register row
in the same PR (PROTOCOL §3). Its first sentence is the row's "Settles":
"Control is released as immutable tags of one module, and every phase is a
release." Then it states:

1. **What a release is.** An annotated tag `vMAJOR.MINOR.PATCH` on a commit on
   `main`, of the one module (§8). `ext/`, and `server/` once 0015 lands, are
   versioned with it, never separately.
2. **How one is cut.** By merging a PR whose `CHANGELOG.md` names a version with
   no tag yet. CI tags that merge commit once every other job has passed on it
   (item 4). No session and no person pushes a tag. The tag records what review
   approved, at the commit that was checked.
3. **When.** Every numbered phase releases, in its own PR. A host embeds the
   whole server (M15), so every phase changes what Enterprise ships. A sync, an
   audit or a request answer changes nothing a consumer runs, and releases
   nothing. A fix between phases may release on its own.
4. **What the number says.** MAJOR stays 0 until a decision of its own declares
   the public packages stable. Every major after 1 changes the module path
   (Go's semantic import versioning), so it is not taken in passing. While
   MAJOR is 0:
   - **MINOR** for a release that contains a phase, or an incompatible change
     to a public package;
   - **PATCH** for anything else.

   Every incompatible change is listed under `### Breaking` in the release's
   changelog section.
5. **What "incompatible" means.** Go's API-compatibility rules, read from the
   implementer's side as well as the caller's. Adding a method to an exported
   interface is incompatible, because Enterprise implements `ext`'s interfaces.
   The public packages are every package in the module that is neither under
   `internal/` nor `package main`.
6. **Immutability.** A published version is never moved, re-pushed or deleted.
   A bad one is superseded by a new release and listed in a `retract` directive
   in `go.mod`.
7. **What it is not.** The module version is one of four version axes, and it
   is none of the other three: the vendored contract's (M1,
   `contract/UPSTREAM`), `policy_version` (§4, 0023), and the north-bound API
   version (M19, 0019). M19's "software version" **is** this one.

Its register row's `Rendered in` names §8 and §10.

### 2. The changelog (`CHANGELOG.md`)
- At the module root. A short header says what it records and how to add an
  entry, and points at the release decision rather than restating it.
- `## Unreleased` first, then one `## vX.Y.Z` per release, newest first. Under a
  version:
  - `### Breaking`, required when the check finds an incompatible change;
  - the phase or phases it contains, by number;
  - changes to public packages, by identifier.
- Not listed: anything nobody consuming the module or running the binary can
  observe. That is prompts, plans, learnings and CI.
- A behaviour change to a public package that no API diff can see is listed by
  the phase that makes it, because nothing else will catch it. The change 0022
  proposes to `ext.KeyAlgorithm.String()` would be one.
- `## v0.1.0` is the first release. Say what it contains by reference, without
  restating PLAN §10: the phases merged when this one runs (0001–0013 and this
  one, plus any queued phase a session ran out of order before it), and `ext`
  as `ext/README.md` describes it.

### 3. The release check (`scripts/release-check.sh`, `make release-check`, a CI job)
It runs on every PR and every push. It reads `CHANGELOG.md`, the repository's
`v*` tags and the tree, and it:

- refuses a changelog it cannot parse: a version heading that is not
  `vMAJOR.MINOR.PATCH`, headings duplicated or out of order, a tag with no
  section, or more than one version without a tag;
- finds the **pending release**: the newest version heading, if it has no tag;
- refuses a pending release that is not greater than the latest tag, or whose
  MAJOR is above 0. With no tag at all, the pending release must be `v0.1.0`;
- refuses a PATCH release when `prompts/implemented/` gained a prompt since the
  latest tag, or when the public packages changed incompatibly since it;
- refuses a pending release with no `### Breaking` when the public packages
  changed incompatibly, and prints what the diff found;
- refuses a tree where `prompts/implemented/` gained a prompt since the latest
  tag but there is no pending release, because a phase releases in its own PR;
- ends with one line a reviewer reads: `on merge this releases v0.1.0 (MINOR:
  0014 implemented; no previous release to diff)`, or `on merge this releases
  nothing`.

The API diff compares the latest tag with the tree, for every public package.
The set is **derived**, not listed, so `server/` is covered the day 0015 adds
it and nobody has to remember. The expected tool is
`golang.org/x/exp/cmd/apidiff`, pinned the way `golangci-lint` is. Another tool
is fine if it reports an added interface method as incompatible. With no
previous release there is nothing to diff, and the check says so rather than
passing silently.

### 4. The release job (`.github/workflows/ci.yml`)
- A `release` job. It runs only on a push to `main`, `needs:` every other job in
  the workflow, and does nothing unless the check finds a pending release.
- It tags the **pushed commit** (`github.sha`), never a branch head it re-reads.
  The tag is annotated, with the release's changelog section as its message.
  The job pushes the tag and creates a GitHub Release with the same notes.
- It ends by resolving the new version through `https://proxy.golang.org`, with
  a bounded retry, and fails if it cannot. That proves what E3 needs, that the
  version is fetchable by path, at the moment the version was made.
- It is the only job with `permissions: contents: write`; the workflow stays
  `contents: read`. A `concurrency` group serializes it, so two merges in quick
  succession cannot race for one version.
- It tags only the merge that introduced the pending version. If the pushed
  commit is a later one, a failed run left a release behind: the job fails and
  names the commit to re-run. Tagging the later commit would publish content
  the release's section does not describe.
- On a PR, the release check prints the tag and the notes a merge would
  publish. The real run happens only on the merge commit, so the PR says
  plainly that it cannot be proven before merge.
- If tag creation is refused on `main` (a repository setting), the job fails
  red. The remedy is to fix the setting and re-run the job **on the same
  commit**. Never push a tag by hand, and never at another commit. Say this in
  the learnings.

### 5. What a build reports (`cmd/hoplock-control/version.go`, or `internal/daemon/version.go` if 0015 has merged; `Makefile`)
- `versionString` reports **Control's** module version from `debug.BuildInfo`:
  - `info.Main`, when its path is `github.com/hoplock/control`;
  - otherwise the `info.Deps` entry with that path, following `Replace` and
    saying so. A replaced build is a working copy, and it must not read as the
    release (E3).

  A host started through 0015's `server.Main` then prints the Control release
  it was built against, not its own version under Control's name.
- **One spelling per build.** Go stamps the main module's version from the
  repository's tags: the tag itself at a tagged commit, a pseudo-version after
  it, and `+dirty` for a modified tree. That is a valid module version. The
  Makefile's `git describe` override spells the same build differently. Stop
  overriding by default, and keep `VERSION=` as an explicit override for a
  build outside a git checkout, where Go has nothing to stamp.
- `dev` stays what a build with no provenance calls itself (0001).

### 6. The documents, and the queued prompts that owe Enterprise a sync
- `docs/PLAN.md`, **revised in place** (PROTOCOL §3):
  - §2: the release decision and its register row (item 1). M19 is not
    amended: its "software version" is the release decision's, and 0019 says
    so (below).
  - §8: a **Releases** bullet citing the decision, and the CI bullet gains the
    release check and the release job.
  - §10: this phase's row, to match what was delivered.
- `docs/PROTOCOL.md`:
  - §4 gains one Definition-of-Done line: `CHANGELOG.md` names the release this
    phase cuts, with its entry and any `### Breaking` the check lists, and
    `make release-check` passes.
  - §3's `ext/` bullet says the check now holds the compatibility promise,
    against the previous release.
- `ext/README.md`, "The compatibility promise": the bump rule, cited by the
  decision's id, and the check.
- `README.md`: how to depend on Control (a released version, never a
  `replace`) and where the changelog is.
- The queued prompts whose hand-off owes `hoplock/enterprise` a sync. As this is
  written that is 0015, 0016 and 0017 (each one's `hoplock/enterprise`
  section), and 0019 (its closing **Cross-repo impact** paragraph). Read each
  queued prompt's hand-off for any added since.
  - Each one's Enterprise obligations gain one line: Enterprise's pin moves to
    the release that PR cuts, named by version.
  - 0019's "the software version (already stamped by 0001)" names the release
    decision.
- 0022 decides whether `ext.KeyAlgorithm.String()` changes what it returns. No
  API diff sees a behaviour change, so its Definition of Done names the
  changelog entry it owes if it does.
- No other queued prompt needs an edit. Every phase's release comes from
  PROTOCOL §4, not from its prompt.

## Out of scope
- **`v1.0.0`**, and the decision that the public packages are stable. Say in
  the learnings what it would take.
- **Signed tags and build provenance.** For an access-control product they
  deserve a decision of their own. Name them in the learnings as a follow-up.
- **Release artifacts**: binaries, container images, SBOMs. A module version is
  what was asked for, and Enterprise ships its own binary.
- **Maintenance branches and back-ports.** Every release is cut from `main`.
- **A separate `ext` module.** M15 and Enterprise's E3 both rest on `ext` being
  versioned with the module.
- **The other version axes** (item 1, rule 7). Nothing here moves them.
- **Enterprise's `go.mod`.** Its 0001 pins Control, and this phase's sync tells
  it how.
- **`contract/` (M1).** Nothing here touches the wire.

## Acceptance criteria
Each criterion is a test, or a CI step whose log shows it.

**The check**
- On this phase's tree, `make release-check` passes. It prints that the merge
  releases `v0.1.0`, and that there is no previous release to diff.
- Each refusal in item 3 has a case, run in CI against a throwaway repository,
  in the style of `make exhaustive-guard`:
  - a pending release that is not greater than the latest tag, and one whose
    MAJOR is above 0;
  - an unparseable or out-of-order heading, and a tag with no section;
  - a prompt moved into `prompts/implemented/` with no pending release;
  - a PATCH release that contains a phase;
  - a method added to an `ext` interface, released as PATCH; and the same
    method released as MINOR with no `### Breaking`, where the refusal names
    the method.
- One case passes: a new exported function in `ext`, released as PATCH, with no
  phase in it.
- The public-package set is derived. A case proves that a package added outside
  `internal/` is diffed without editing the script.

**The job**
- Reading the workflow shows that it runs only on a push to `main`, after every
  other job; that it alone holds `contents: write`; that it is serialized; that
  it tags `github.sha` only for a pending release that commit introduced; and
  that it ends by resolving the version through the module proxy.
- The PR's own check log shows the tag and the notes the merge will publish.

**The version**
- Table tests over a constructed `debug.BuildInfo`:
  - Control as the main module at a tag reports the tag;
  - after the tag, it reports a pseudo-version that sorts after it;
  - Control as a dependency of another main module reports the dependency's
    version, never the host's;
  - a replaced dependency says it is replaced;
  - no build info reports `dev`.
- `make build` and `go build` of one clean tree report the same version.

**Unchanged**
- `TestDecisionRegisterCoversEveryDecision` passes with the new decision.
- `go build`, `go vet`, `go test`, `golangci-lint run` and `make contract-check`
  are green.
- No signature in `ext/` changes. Nothing in `contract/` changes.

## Cross-repo impact
This phase answers an upstream request, and it changes how `ext/` reaches its
consumer. So it owes a downstream sync. The PR that implements it MUST carry a
`## Cross-repo impact` section (`docs/CROSS-REPO-PROTOCOL.md` §4.1) naming
**every** consuming repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5, "The PR that answers an upstream request is
not a sync"). It knows it asked for a tag. It does not know that every phase is
now a release, what a version promises, or where the changelog is. The session
there will be a fresh one that knows nothing.

State at least these obligations:

1. **Confirm the release first.** `v0.1.0` must resolve through the module
   proxy: `go list -m github.com/hoplock/control@v0.1.0`. If it does not, the
   release job failed on this PR's merge commit. Stop and say so (§2), and do
   not write prompts against a version that does not exist.
2. **Its 0001, "Dependency on Control".** Rewrite the paragraph that begins "If
   Control has published no release tag" from what merged:
   - pin Control's newest release, never a pseudo-version, and drop the stop
     and the visibly unresolved pin;
   - `--version` reads Control's version from `debug.BuildInfo`, in the
     dependency entry for `github.com/hoplock/control`, which is the pin.
     Control's own `--version` reports the same once started through
     `server.Main`;
   - the learnings item "the pinned Control version and how to bump it" names
     Control's `CHANGELOG.md` and the release decision.
3. **Its plan.** E3 and §5 cite Control's release decision by id for what a
   version promises, instead of describing Control's versioning themselves.
   E3's changelog is Control's `CHANGELOG.md`.
4. **Waiting on a Control phase.** Wherever an Enterprise prompt waits for a
   Control phase to be in "the pinned Control version", it now waits for **the
   release that phase cut**, which Control's changelog names. Its 0008 does
   this for Control's north-bound API (0018).

Queue the **"Downstream sync" kickoff** from `docs/KICKOFF.md`, verbatim except
for its blanks — this PR's URL, and the obligations above — as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`, committed
once the PR is open, and end the section by naming that file
(`docs/CROSS-REPO-PROTOCOL.md` §4.3).

### `hoplock/proxy`
- **Downstream: "None".** The proxy consumes nothing of this module, and the
  contract (M1) has its own version, pinned by commit.
- **Upstream: none.** `docs/CROSS-REPO-PROTOCOL.md` §1 already says `ext/`
  reaches its consumer as "a pinned, released module version". This phase makes
  the row true; it does not change it. "None" is a finding (§4.1).

### Who runs the sync
Not you. This PR merges first, and the sync runs afterwards in its own session
(`docs/CROSS-REPO-PROTOCOL.md` §2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`, including the line this phase adds to §4. This PR is
the first one that line binds, and it releases `v0.1.0`. Move this prompt to
`implemented/`, and add `docs/learnings/0014-tagged-releases-learnings.md`.
Enterprise's sync is written from its summary block, which MUST give:

- the release decision's id, and its bump rule verbatim;
- how a release is cut: the PR, the job, its permission, and what to do when the
  job fails on `main`;
- how the public packages are derived, and the diff tool and its pin;
- what a build reports in each case of item 5;
- that this PR's merge cuts `v0.1.0`, and how to confirm it.
