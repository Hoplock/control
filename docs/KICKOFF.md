# Kickoff — starting an implementation session

Copy one of the prompts below into a **fresh** Claude Code session (the repo is
cloned fresh per session). The prompts in `prompts/queued/` are self-contained;
`docs/PROTOCOL.md` tells the session how to pick up and deliver the work.

The last three are not phases, and none of them is ever "next" in the queue:
they run because somebody pastes them. An **audit** (`prompts/audit/`) is re-run
against the whole repository rather than built once. A **downstream sync** and
an **upstream request** have no prompt file at all, and
`docs/CROSS-REPO-PROTOCOL.md` — not `docs/PROTOCOL.md` — is what governs both;
they are the two directions the same cross-repo obligation runs in, and this
repository is the only one of the three that owes both.

## Default: implement the next queued prompt

```
Read docs/PROTOCOL.md and follow it. Implement the lowest-numbered prompt
in prompts/queued/. Do not start any other prompt in this session.
```

## Specific prompt (run out of order)

```
Read docs/PROTOCOL.md and follow it. Implement prompts/queued/<NNNN-name>.md.
Do not start any other prompt in this session.
```

## Audit (not queued, and re-run)

An **audit** lives in `prompts/audit/`, carries no number, and is run against
whatever has been built so far — repeatedly (`docs/PROTOCOL.md` §6). Nothing
selects it automatically: `prompts/queued/` is the build order and the default
kickoff above takes the lowest-numbered prompt from it, so an audit runs when
**you** paste this and not otherwise. That is the trade for keeping it out of
the sequence, and it is why this section exists.

There is one audit today: `prompts/audit/cross-repo-impact.md`, which checks
what `hoplock/proxy` has made true that this repository has not caught up with.
Run it **before** starting a phase that will build on text the proxy may have
moved underneath us — after a batch of upstream merges, or after any stretch
where syncs have lagged. Findings that arrive after the phase is built are a
list of things already built wrong.

```
Read docs/PROTOCOL.md and follow it, then run the audit in
prompts/audit/<name>.md. Do not implement any queued prompt in this session.

This audit needs the Hoplock Proxy repository reachable through the GitHub
API, not only as a clone: the PR descriptions are half the evidence and a
clone does not carry them. Attach it read-only if it is not already in scope.
If you cannot reach the API half, do the half you can and say plainly which
half is missing — do not reconstruct PR descriptions from commit titles.

An audit changes text, not behaviour: it implements and enforces nothing,
hand-edits no vendored artifact, and renames or renumbers no prompt. Land each
finding in the prompt that will implement it, not only in the plan. A finding
that is a whole phase rather than a paragraph may be appended to the queue as
a new numbered prompt — say so plainly — but never built here. If the work
seems to need something the upstream repository does not have, that is
docs/CROSS-REPO-PROTOCOL.md §3.2 — never approximate it, and do not stop at
telling me: name the exact field, endpoint, enum value or signature, record it
as a named cross-repo dependency, and hand me the "Upstream request" kickoff
from docs/KICKOFF.md already filled in, so the proxy phase that answers it can
actually be started.

Leave the audit prompt where it is — it is re-run, not completed — and record
this run at the top of its one learnings file, with the as-of markers.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/audit-<short-description>. Open one PR whose body
carries the findings table and says how you searched — the actual commands,
not "I looked carefully".
```

Fill in `<name>` and leave the rest alone: each paragraph is a Definition-of-Done
item from the audit prompt itself, and the two most droppable — the API access
and "leave the prompt where it is" — are the two that quietly turn an audit into
a worse version of itself. Dropped, you get an audit that read only what a clone
carries, and one that files itself away so the next run never happens.

## Downstream sync (no prompt, no number)

A **sync** is the follow-up a merged change to a shared surface owes another
repository: it updates that repository's prompts, plan, and protocol so the next
session there builds against what is now true
(`docs/CROSS-REPO-PROTOCOL.md` §3.1). It is not a phase, so it has no prompt
file and no `NNNN` — which is exactly why it needs a kickoff of its own rather
than one of the two above.

This repository sits in the middle of the chain
(`docs/CROSS-REPO-PROTOCOL.md` §2: proxy → control → enterprise), so the prompt
below runs **both ways**. A merged `hoplock/proxy` change to the contract or to
the cross-repo protocol hands you this to run **here**, in a fresh session with
this repository checked out. A change *this* repository merges to `ext/` (M15)
or to `docs/CROSS-REPO-PROTOCOL.md` owes the same kickoff to
`hoplock/enterprise`, emitted by the PR that made it — so this section is both
what you paste and what you copy from when you are the upstream author. An
upstream PR whose `## Cross-repo impact` section names obligations is required
to emit it already filled in (that protocol's §4.1), so normally you paste what
the PR gave you rather than composing this by hand.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following <upstream PR URL>. Do not implement any queued prompt in this
session.

Before anything else, confirm that upstream change is MERGED; if it is not,
stop and say so (§2). Then update this repository's prompts, plan, and
protocol so the next session builds against what is now true.

A sync changes text, not behaviour: it implements, enforces, and vendors
nothing, hand-edits no vendored artifact, and adds, renames, or renumbers no
prompt. Land each obligation in the prompt that will implement it, not only in
the plan. If the work seems to need something the upstream repository does not
have, that is §3.2 — never approximate it, and do not stop at telling me: name
the exact shape and hand me the "Upstream request" kickoff from
docs/KICKOFF.md already filled in.

The obligations to land are in the upstream PR's "## Cross-repo impact"
section: <the obligations, or "see the PR">.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
and says how you searched for stale references — the actual grep, not "I looked
carefully".
```

Fill in `<upstream PR URL>` and the obligations. Leave everything else alone:
each remaining line is a Definition-of-Done item from
`docs/CROSS-REPO-PROTOCOL.md` §5, and dropping one is how a sync quietly turns
into an unreviewable feature PR.

The branch line no longer has a blank to fill: a sync session is normally
started with a branch already assigned, and renaming it is neither possible nor
worth a paragraph in the PR (`docs/CROSS-REPO-PROTOCOL.md` §5,
`docs/PROTOCOL.md` §2). What identifies the sync is the PR body naming the
upstream change.

## Upstream request (no prompt, no number)

The mirror of the block above. A sync carries a **merged** change downstream; a
**request** carries an **unmet need** upstream — what a repository owes the one
above it when it needs a shape that does not exist yet
(`docs/CROSS-REPO-PROTOCOL.md` §3.2, §4.2). The session that hits the gap builds
everything the gap does not block behind a seam named for what is missing, states
the shape in its PR under a `## Upstream request` heading, and hands over this
kickoff already filled in. It does **not** do the upstream work itself (§6).

This repository sits in the middle of the chain
(`docs/CROSS-REPO-PROTOCOL.md` §2: proxy → control → enterprise), so — like the
sync block above, and for exactly the same reason — this one runs **both ways**.
It is the only repository of the three where that is true, which is why both
halves are spelled out:

- **Sending, up to `hoplock/proxy`.** A phase here that needs a contract field,
  endpoint or enum value the proxy has not defined emits this kickoff filled in,
  in its PR and in its reply, pointed at `hoplock/proxy`. Never edit `contract/`
  to close the gap instead (M1) — that is the failure the flow exists to
  prevent. You do not run the kickoff here; the user runs it in a fresh session
  with the proxy checked out.
- **Receiving, from `hoplock/enterprise`.** An Enterprise phase that needs an
  `ext/` seam this repository has not exposed (M15) emits the same kickoff
  pointed **here**, and the prompt below is then what you run, in a fresh
  session with this repository checked out.

**What it produces is a queued prompt, not the change.** What arrives is a
*need*, described by somebody who navigated this repository's plan only far
enough to be blocked by it. Turning that into a specified phase is this
repository's own work, and it is why the session below stops at the prompt: a
requester who wrote the prompt too would be specifying a phase against an
architecture they have not read.

```
Read docs/PROTOCOL.md and follow it. You are turning an upstream request from
<requesting repo> into a queued prompt. The request is in <requesting PR URL>,
under "## Upstream request". Do not implement any queued prompt in this session,
and do not implement this one.

Read that section, then this repository's docs/PLAN.md — its section headings and
its decision register — far enough to place the work: which sections and which M
decisions the phase touches, and whether an existing decision already settles
part of it. The requester could not do this, which is the whole reason the
request stops at a need.

Write ONE self-contained prompt into prompts/queued/ per docs/PROTOCOL.md §7 —
lowest unused number, contiguous above the implemented block, a "Read first"
block naming plan sections by § and decisions by M id, in-scope and out-of-scope
items, the exact files and shapes, acceptance criteria and required tests. Cite
the requesting repository's decision ids by id (E* for enterprise), never
restated (docs/CROSS-REPO-PROTOCOL.md §1).

Two things the prompt MUST carry, because they are what the request is for:
- the exact shape asked for, in this repository's own vocabulary, and what
  downstream is unable to do until it exists;
- that the phase implementing it owes a downstream sync to EVERY consuming
  repository once merged — including the one that raised the request, which is
  the one most easily forgotten because it is already waiting
  (docs/CROSS-REPO-PROTOCOL.md §5, "The PR that answers an upstream request is
  not a sync").

If the request cannot be met as asked — it contradicts an M decision, or the
shape is wrong for reasons the requester could not see — say so and propose the
alternative rather than queueing a prompt you expect to be wrong. A request is a
need, not an instruction, and the answer "not like that, like this" is a real
outcome.

Work on the branch this session was given, whatever it is named — if the name is
yours to choose, claude/NNNN-short-description matching the prompt you add. Open
one PR whose body names the PR the request came from, quotes the shape
requested, and says where in the queue you put it and why.
```

Fill in `<requesting repo>` and `<requesting PR URL>` and leave the rest alone. The
two most droppable paragraphs are the two that matter: reading the plan before
writing the prompt, and the reminder that the phase owes a sync **back**. Dropped,
you get a prompt specified from outside this repository's architecture, and a
change that lands here and is never picked up by the repository that asked for it.

When you are **sending** rather than receiving, the same block is what you paste
into your PR's `## Upstream request` section and into your reply — with
`hoplock/control` as the requesting repository, your own PR as the URL, and `D`
in place of `M` for the proxy's decision ids. Your phase does not wait for the
answer: it merges with the gap named and the seam unwired, because a seam that
fails visibly is what makes not-waiting safe (§4.2).

## Rules of thumb

- **One session = one prompt = one PR.** Start a fresh session for each queued
  prompt. The session ends when its PR is merged (see `docs/PROTOCOL.md`).
- **Respect dependencies / ordering.** Prompts are numbered in implementation
  order and later ones assume earlier ones are merged. A fresh session branches
  off `main`, so it only sees **merged** work — kick off the next prompt after
  the previous PR merges. Only run prompts in parallel when they genuinely don't
  depend on each other.
- **An audit is not a phase either, and is not scheduled.** It has no number and
  no place in the order, so it runs when you ask for it. Ask after upstream has
  moved, not after the phase that assumed it hadn't.
- **A sync is not a phase.** One upstream change means one sync PR per affected
  repository, each in its own fresh session against that repository. Never sync
  from a session that is implementing a prompt — the two are separately
  reviewable and separately revertible. A phase here that changes `ext/` or
  `docs/CROSS-REPO-PROTOCOL.md` **emits** Enterprise's kickoff in its PR and in
  its reply (`docs/CROSS-REPO-PROTOCOL.md` §4.1); it does not do that sync itself.
- **A request is not a phase either, and it produces one rather than being one.**
  An upstream request arrives as a need and leaves as a queued prompt; the phase
  that implements it is a later, separate session. Never let the two collapse —
  a session that writes the prompt and then implements it has reviewed its own
  specification. The same rule bounds the sending half: the session that found
  the gap emits the kickoff and never opens the upstream PR itself
  (`docs/CROSS-REPO-PROTOCOL.md` §3.2, §6).
- **Don't paste prompt bodies.** Point the session at the file in the repo so it
  reads the canonical version (numbers can change under the invariants in
  `docs/PROTOCOL.md` §6; the file is always current).
- **Hoplock Enterprise is downstream.** A phase here may define an extension
  point that Enterprise implements; it may never import Enterprise or assume it
  is installed (PLAN M15).
- **The Hoplock Proxy repository is a sibling, not a dependency to edit.** A session
  here may need to *read* the proxy's plan or its mock server; it may never
  change them, and it may never edit `contract/` (see `docs/PLAN.md` M1). If a
  phase turns out to need a contract change, that is a separate piece of work in
  the Hoplock Proxy repository — raise it as an **upstream request**: build the
  seam unwired, name the shape under `## Upstream request` in your PR, and hand
  the user the filled-in kickoff above (`docs/CROSS-REPO-PROTOCOL.md` §3.2,
  §4.2). Telling the user and stopping there is what left two such needs
  unowned.
- **Phase 0002 is worth doing early and well.** After it lands, every later phase
  has a conformance suite it did not write itself telling it whether the server
  is correct. Before it lands, "correct" is an opinion.
