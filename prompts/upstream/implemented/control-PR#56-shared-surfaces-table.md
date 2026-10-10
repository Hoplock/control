# control-PR#56 — server/ in the shared-surfaces table, and its stale paragraph

Queued by [control#56](https://github.com/Hoplock/control/pull/56), Control's
phase 0015, which adds a second public package Enterprise consumes. Two changes
to `docs/CROSS-REPO-PROTOCOL.md` §1, which only `hoplock/proxy` may make: list
`server/` beside `ext/`, and rewrite the paragraph that still says `contract/`
and `ext/` "do not exist yet" — the second is enterprise#8's need, folded into
this request so §1 is edited once. The shapes are under the PR's
`## Upstream request`.

```
Read docs/PROTOCOL.md and follow it. You are turning an upstream request from
hoplock/control into a queued prompt. The request is in
https://github.com/Hoplock/control/pull/56, under "## Upstream request". Do not
implement any queued prompt in this session, and do not implement this one.

Read that section, then this repository's docs/PLAN.md — its section headings and
its decision register — far enough to place the work: which sections and which D
decisions the phase touches, and whether an existing decision already settles
part of it. The requester could not do this, which is the whole reason the
request stops at a need.

Write ONE self-contained prompt into prompts/queued/ per docs/PROTOCOL.md §7 —
a "Read first" block naming plan sections by § and decisions by D id, in-scope
and out-of-scope items, the exact files and shapes, acceptance criteria and
required tests. Cite the requesting repository's decision ids by id (M* for
control), never restated (docs/CROSS-REPO-PROTOCOL.md §1).

Queue it where it should run, never simply last: the default kickoff takes the
lowest-numbered prompt, so its number is when it runs. Read the queued prompts
and put it in the best order to run them in — after everything it depends on,
before everything that depends on it, and early when nothing queued has a
better claim, since the requesting repository is waiting on it. Renumber the
queued prompts after it as docs/PROTOCOL.md §6 says, in this PR. The other
repositories cite these numbers: grep them, and name every citation the
renumbering leaves stale under "## Cross-repo impact", with its sync queued
(docs/CROSS-REPO-PROTOCOL.md §4.1).

Two things the prompt MUST carry, because they are what the request is for:
- the exact shape asked for, in this repository's own vocabulary, and what
  downstream is unable to do until it exists;
- that the phase implementing it owes a downstream sync to EVERY consuming
  repository once merged — including the one that raised the request, which is
  the one most easily forgotten because it is already waiting
  (docs/CROSS-REPO-PROTOCOL.md §5, "The PR that answers an upstream request is
  not a sync").

If the request cannot be met as asked — it contradicts a D decision, or the
shape is wrong for reasons the requester could not see — say so and propose the
alternative rather than queueing a prompt you expect to be wrong. A request is a
need, not an instruction, and the answer "not like that, like this" is a real
outcome.

Work on the branch this session was given, whatever it is named — if the name is
yours to choose, claude/NNNN-short-description matching the prompt you add. Open
one PR whose body names the PR the request came from and the request file this
answers, quotes the shape requested, and says where in the queue you put it and
why.
```
