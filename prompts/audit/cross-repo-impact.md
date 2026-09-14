# Audit — Cross-repo impact: what the proxy expects of Control

> **This is an audit, not a phase**, which is why it lives in `prompts/audit/`
> and carries no number (`docs/PROTOCOL.md` §6). A number says where work sits
> in the build order and this has no such place: numbered last it would run
> *after* the phases it exists to protect, and numbered first it would renumber
> every live phase to buy an ordering a kickoff states in one line. Three things
> follow from where it lives:
>
> - **Nothing in `prompts/audit/` is queued.** "The lowest-numbered queued
>   prompt" (`docs/PROTOCOL.md` §0.2) never reaches this folder; a session runs
>   this because the user pasted the "Audit" kickoff from `docs/KICKOFF.md`.
> - **Run it before building on text the proxy may have moved under us** —
>   typically before starting the next phase after a batch of upstream merges,
>   and after any stretch where syncs have lagged. An audit that runs after the
>   phases have been built is a list of things that were already built wrong.
> - **The file never moves.** It is re-run rather than completed, so it stays
>   here, and each run adds to one learnings file (below) instead of writing a
>   new one.

## Read first
- `docs/PROTOCOL.md` — session workflow; especially §3 (scope discipline, and
  that `contract/` is never edited), §6 (prompt-numbering invariants — this
  phase renumbers nothing), and §7 (what a prompt you *write* owes a future
  session).
- `docs/CROSS-REPO-PROTOCOL.md` — **in full.** It is short and it is the subject
  of this phase: §1 (the shared surfaces and who owns each), §2 (the direction
  rule), §3.1 (the sync flow), §3.2 (upstream-blocked — the answer when this
  repository needs something the proxy does not have), §4 (the upstream author's
  obligation to look downstream, which is what this phase audits), §5 (what a
  sync PR owes, including "how you searched"), §6 (the guardrails).
- `docs/PLAN.md` — the decision register at the head of **§2**, then **§4** (the
  endpoint table and the obligations graded by the conformance suite), **§5**
  (§5.2 in particular — the snapshot this server answers with), **§8**
  (conventions), and **§10** (the phase table and its renumbering notes).
  Decisions this phase leans on: **M1** (the contract is owned upstream and
  vendored read-only), **M11** (`401` means deny, everything else means outage),
  **M15** (Enterprise extends this repository), **M17** (the fleet graph carries
  capabilities).
- `docs/learnings/` — every summary block; open a full file only where a summary
  names a cross-repo obligation.
- In the **Hoplock Proxy repository**: `api/control.yaml` and `api/README.md`,
  `docs/PLAN.md` §2's decision register (`D*` ids this repository cites),
  `prompts/implemented/` and `docs/learnings/` (what the proxy's phases assumed
  about this server).

  **Neither contract document carries a revision history any more.** Upstream
  `Hoplock/proxy#53` (merged) deleted it: both read in the present tense, with no
  revision sections and no "since version N" annotation on any field. An earlier
  version of this prompt called those sections "the spine of this audit" — they
  were, and they are gone, so the spine is now **§2's merged-PR walk**: the
  history lives in the PR bodies and in `git log`, and the documents tell you
  only what is true now. That is a change in method, not in scope: comparing this
  repository's text against a present-tense document is if anything sharper,
  because a stale claim here no longer has a matching section upstream to hide
  behind.

## Objective
Find every obligation the proxy has placed on Hoplock Control, and every place
this repository's prompts have drifted from what the proxy now says — then land
the text, so the next implementation session builds against what is true rather
than against what was true when its prompt was written.

This is an **audit that fixes text**. It ships no behaviour.

### Why this phase exists

`docs/CROSS-REPO-PROTOCOL.md` §4 puts the downstream look on the upstream
author, before merge, in a section named `## Cross-repo impact`. That is the
right place for it and it is the only place it happens — which means when it
does not happen, or happens from memory, nothing catches it. Two cases, both
real and both found by accident:

- The sync for `Hoplock/proxy#41` was handed an impact section
  saying this repository's *South-bound authorize & route* prompt "already
  carries the v3 `username` requirement" and that 4.2 merely extends it. It
  carried nothing: `grep -rni "username" . --exclude-dir=.git` returned zero
  hits across the whole repository. The requirement had reached the contract and
  the proxy and was never mirrored downstream at all — a gap roughly a year of
  phases wide, invisible because nothing fails to compile. The same impact
  section pointed at a conformance case in *Contract vendoring & conformance
  harness* that did not exist either.
- `#41` then went unsynced long enough that the **`#51`** sync arrived first,
  noticed it had never landed, and named it in `docs/PLAN.md` §4 as a gap it was
  deliberately not closing (§5: one upstream change, one sync PR). That worked —
  but it worked because a session happened to look.

Neither is a criticism of the sessions involved; both are the predicted failure
of a check that runs once, from one side, under a deadline. This phase is the
compensating pass: done once over the whole history, it leaves behind an
as-of marker so every later run is incremental.

## In scope

### 1. Get access, and say so if you cannot

The PR **bodies** are half the evidence and a clone does not carry them: a
`git log` gives you titles and merge commits, not the `## Cross-repo impact`
section. So this phase needs the proxy repository reachable **through the GitHub
API**, not only as files.

- Attach `Hoplock/proxy` to the session (`add_repo`) with the access level that
  makes the GitHub tools work against it, and clone it for the file half.
- If the API half is not available to you, **do the file half, and say plainly
  in the PR and to the user which half is missing.** A partial audit reported as
  a whole one is worse than no audit, because it is what the *next* session will
  trust instead of looking. Do not reconstruct impact sections from commit
  titles and present them as the record.

### 2. Bound the set of upstream changes

Do not read every proxy PR. The shared surfaces (§1 of the cross-repo protocol)
are `api/control.yaml`, `api/README.md`, and `docs/CROSS-REPO-PROTOCOL.md`, so
the set that can possibly owe this repository something is the set that touched
one of them:

```
git -C <proxy clone> log --oneline --merges -- api/ docs/CROSS-REPO-PROTOCOL.md
```

Each merge commit names its PR number; those numbers are your list. Fetch each
one's body through the GitHub API. Record the command you actually used and the
number of PRs it produced — a reviewer cannot re-derive "I looked at all of
them", and `docs/CROSS-REPO-PROTOCOL.md` §5 asks for the search, not the
adjective.

Two edges worth handling deliberately rather than discovering late: a PR that
changed a shared surface and was **reverted** later, and a change that reached
`api/` through a merge with no PR. Both are rare and both are findings.

### 3. Classify every PR in that set

Three buckets, and the third is the interesting one:

1. **Obligations stated** for `hoplock/control`.
2. **"None" stated** — a finding in its own right, per §4, and cheap to verify
   against the diff.
3. **No `## Cross-repo impact` section at all.** §4 says an omitted section is
   indistinguishable from never having looked. Treat these as unaudited and
   derive the obligation from the diff yourself.

### 4. Trace every stated obligation into this repository

For each obligation: is it in this repository's text today, and **where**? Cite
file and line. Landed, partly landed, or missing — and missing means landing it
here, **in the prompt that will implement it** (§5 of the cross-repo protocol),
not only in `docs/PLAN.md`. A session reads its prompt closely and skims the
plan; that asymmetry is the whole reason the rule exists.

### 5. Check the contract itself, not the claims about it

This is the half that catches what §4 cannot, and it is not optional. A PR body
is a claim, written before merge, about two repositories at once; the contract
document is what is true now. Check this repository's prompts against the
**document**:

- every method's required parameters, every enum value, every endpoint in
  `docs/PLAN.md` §4's table, against `api/control.yaml`;
- **every citation of a contract revision by number** — "since contract vN",
  "since 4.N", a named "vN→vM revision" section. The documents no longer have
  any, so **every such citation left in this repository is stale by
  construction**: the rule it points at may well still be true, but the section
  it cites is not there to check it against. Restate the rule in the present
  tense beside what it governs, and delete the number. Grep for them, do not
  read for them:
  `grep -rniE "contract v[0-9]|contract [0-9]\.[0-9]|vocabulary v[0-9]|since v[0-9]" prompts/ docs/ README.md`.
- the two version numbers: the document's `info.version` and `policy_version`.
  They move independently and this repository states both in several places
  (§4, 0002, 0018). Are they current, and is each stated where the phase that
  owns it can see it? Note that `info.version` does **not** only ever rise —
  `#53` moved it `4.3.0` → `4.0.0` — so a check that assumes monotonicity is
  itself a finding.
- **`policy_version` is required and has no absent-value default.** Does this
  repository's text say so everywhere it describes the field, and does it keep
  the mechanism? Removing superseded *vocabularies* is not removing
  *versioning*, and a prompt that has drifted into the second reading is the
  most damaging drift this audit can find: it would delete the negotiation the
  fleet's mid-upgrade safety rests on.
- every `D*` id this repository cites — does it still exist in the proxy's
  register, and does it still settle what we say it settles? Ids are cited and
  never restated (§1), so a `D*` that moved is a silent wrong reference.
- every identifier, path, filename and enum value this repository names from
  upstream: `grep` for each across `prompts/`, `docs/` and `README.md`.

### 6. Read what the proxy's phases assumed about this server

The proxy's `prompts/implemented/` and `docs/learnings/` are where "Control will
do X" gets written down without ever becoming an impact section — an assumption
inside an upstream phase is not a contract change, so §4 never fires on it.

Read the implemented prompts' titles and each learnings **summary block**; open
a full file only where the summary shows it touches this server. What you are
looking for:

- behaviour the proxy now depends on this server providing — refusal classes and
  status codes (M11), endpoint semantics, determinism the proxy's mock models
  (MFA polling, for instance), fixture shapes its E2E expects;
- anything the proxy built *because* this server does not do it yet, which is a
  queued phase here or a gap in one;
- `cmd/mock-control`: it is the reference implementation of this repository's own
  API and 0002 grades the conformance suite against it. Treat its **documented
  behaviour** as evidence; do not copy Go code into this repository's prompts,
  and keep 0002's black-box discipline intact. A disagreement between the mock
  and the document is a contract ambiguity, which is a finding for the user, not
  something to resolve here (M1).

A gap in the proxy's prompt numbering is not a missing phase — some of its
prompts are questions that were answered rather than implemented. Do not infer
one.

### 7. Land the fixes

Text only, with a sync's discipline (§3.1, §6):

- implement nothing, enforce nothing, vendor nothing;
- **never hand-edit a vendored artifact.** If `contract/` exists by the time this
  runs (it lands with 0002), a stale copy is fixed by `make contract-sync`, and
  a contract that has moved is a **downstream sync** (§3.1) with its own PR — not
  this phase's to fold in. Hand the user the kickoff and say so;
- **renumber no prompt** (`docs/PROTOCOL.md` §6). Appending a new queued prompt
  at the end is allowed where the audit finds work that is genuinely a phase, but
  prefer a sentence in the prompt that already owns the area: a new prompt is a
  PR someone has to run, and most findings here are a paragraph;
- anything this repository needs that the proxy does not have is **§3.2**: stop,
  name the exact field, signature or endpoint, tell the user, and record it as a
  named cross-repo dependency. Do not approximate it, and do not open a PR
  upstream from this session.

### 8. Leave an as-of marker

The next run of this audit must be able to start where this one stopped. Record,
in the learnings summary block:

- the proxy `main` commit SHA the audit was taken against;
- the **highest proxy PR number** examined, and the bounded-set command;
- what the next run can therefore skip.

Without those markers the next audit repeats the whole history, which is how a
periodic check quietly becomes a one-off.

## Out of scope
- Implementing any phase, here or upstream.
- **Changing anything in `hoplock/proxy`** — the direction rule (§2). A needed
  upstream change is §3.2 and is the user's to schedule as its own work.
- `hoplock/enterprise`. Control→Enterprise is a different direction with a
  different owner (M15), and `ext/` does not exist yet.
- Re-vendoring or bumping the contract (that is a sync, §3.1).
- The north-bound API version and its negotiation (M19, phase 0015) — a separate
  number with a separate lifecycle.

## Acceptance criteria
- **One row per upstream PR in the bounded set**, in the PR body and in the
  learnings file: PR number, which shared surface it touched, whether it stated
  obligations / "None" / had no impact section, the obligations, and for each
  either where it is landed in this repository (file + line) or why it is not
  owed.
- **Every stated obligation is traced**, and every missing one is either landed
  by this PR in the prompt that will implement it, or listed as a §3.2 dependency
  with the exact upstream shape named.
- **The independent contract check of §5 is done**, and the greps are written
  down verbatim. "I checked carefully" is not a finding a reviewer can re-derive.
- **Every `D*` id cited in this repository resolves** in the proxy's register
  today; each that does not is fixed here or reported.
- No prompt renumbered, no prompt renamed, no vendored artifact hand-edited,
  nothing pushed upstream.
- `make check` passes.
- The reply to the user lists, separately: obligations landed, §3.2 dependencies
  found, and anything the audit could not reach (see §1).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`, with two deliberate departures that follow from this
prompt being a repeating audit rather than a phase:

- **Leave this prompt in `prompts/audit/`.** Do not move it to
  `prompts/implemented/`, do not copy it into `prompts/queued/`, and do not
  rename it. The next audit needs it where it is; a prompt filed under
  "implemented" is one nobody runs again.
- **One learnings file, updated in place:**
  `docs/learnings/audit-cross-repo-impact-learnings.md`. Add a section for this
  run at the **top**, dated, and leave the previous runs beneath it — the
  history is what shows whether the same obligation keeps being missed. If the
  file does not exist yet, create it with the same summary-block shape every
  other learnings file uses.

That summary block MUST give: the **as-of markers** (§8), the bounded-set
command and how many PRs it produced, the findings table, every obligation
landed and where, every §3.2 dependency as a named shape, and what the next run
can skip because this one covered it.

Write the reasoning into the prompts and the plan as you go, not only into the
PR body: a rationale that lives only in a merged PR description has been
archived, not communicated (§5).
