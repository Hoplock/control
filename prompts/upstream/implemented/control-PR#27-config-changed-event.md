# control-PR#27 — a config_changed event

Backfilled from [control#27](https://github.com/Hoplock/control/pull/27), which
predates the request queues: the kickoff its `## Upstream request` section
handed over, verbatim. Answered by
[proxy#61](https://github.com/Hoplock/proxy/pull/61) (queued 0042) and
[proxy#65](https://github.com/Hoplock/proxy/pull/65) (delivered 0042).

```
Read docs/PROTOCOL.md and follow it. You are turning an upstream request from
hoplock/control into a queued prompt. The request is in
https://github.com/Hoplock/control/pull/27, under "## Upstream request". Do not
implement any queued prompt in this session, and do not implement this one.

Read that section, then this repository's docs/PLAN.md — its section headings and
its decision register — far enough to place the work: which sections and which D
decisions the phase touches, and whether an existing decision already settles
part of it. The requester could not do this, which is the whole reason the
request stops at a need.

Write ONE self-contained prompt into prompts/queued/ per docs/PROTOCOL.md §7 —
lowest unused number, contiguous above the implemented block, a "Read first"
block naming plan sections by § and decisions by D id, in-scope and out-of-scope
items, the exact files and shapes, acceptance criteria and required tests. Cite
the requesting repository's decision ids by id (M* for control), never restated.

Two things the prompt MUST carry: the exact shape asked for, in this
repository's own vocabulary, and what downstream cannot do until it exists; and
that the phase implementing it owes a downstream sync to EVERY consuming
repository once merged — including the one that raised the request, which is the
one most easily forgotten because it is already waiting.
```
