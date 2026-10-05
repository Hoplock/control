# control-PR#36 — an algorithm floor

Backfilled from [control#36](https://github.com/Hoplock/control/pull/36), which
predates the request queues: the kickoff its `## Upstream request` section
handed over, verbatim. Answered by
[proxy#64](https://github.com/Hoplock/proxy/pull/64) (queued 0045) and
[proxy#69](https://github.com/Hoplock/proxy/pull/69) (delivered 0045).

```
Read docs/PROTOCOL.md and follow it. You are turning an upstream request from
hoplock/control into a queued prompt. The request is in
https://github.com/Hoplock/control/pull/36, under "## Upstream request".
Do not implement any queued prompt in this session, and do not implement this
one.

Read that section, then this repository's docs/PLAN.md — its section headings and
its decision register — far enough to place the work: which sections and which D
decisions the phase touches, and whether an existing decision already settles
part of it. The requester could not do this, which is the whole reason the
request stops at a need.

Write ONE self-contained prompt into prompts/queued/ per docs/PROTOCOL.md §7 —
lowest unused number, contiguous above the implemented block, a "Read first"
block naming plan sections by § and decisions by D id, in-scope and out-of-scope
items, the exact files and shapes, acceptance criteria and required tests. Cite
the requesting repository's decision ids by id (M* for control), never restated
(docs/CROSS-REPO-PROTOCOL.md §1).

Two things the prompt MUST carry, because they are what the request is for:
- the exact shape asked for, in this repository's own vocabulary, and what
  downstream is unable to do until it exists;
- that the phase implementing it owes a downstream sync to EVERY consuming
  repository once merged — including the one that raised the request, which is
  the one most easily forgotten because it is already waiting
  (docs/CROSS-REPO-PROTOCOL.md §5, "The PR that answers an upstream request is
  not a sync").

If the request cannot be met as asked — it contradicts a D decision, or the shape
is wrong for reasons the requester could not see — say so and propose the
alternative rather than queueing a prompt you expect to be wrong. A request is a
need, not an instruction, and the answer "not like that, like this" is a real
outcome.

Work on the branch this session was given, whatever it is named — if the name is
yours to choose, claude/NNNN-short-description matching the prompt you add. Open
one PR whose body names the PR the request came from, quotes the shape requested,
and says where in the queue you put it and why.
```
