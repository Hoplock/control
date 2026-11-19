# control-PR#162 — a report of the relay registrations a proxy holds

Raised by [control#162](https://github.com/Hoplock/control/pull/162) (phase
0061, the cross-repository end-to-end topology), whose `## Upstream request`
section states the shape: the contract has no way for a proxy to tell Hoplock
Control which relay registrations it holds, so a `relay` route works only until
a seeded registration ages out. The kickoff, filled in:

```
Read docs/PROTOCOL.md and follow it. You are turning an upstream request from
hoplock/control into a queued prompt. The request is in
https://github.com/Hoplock/control/pull/162, under "## Upstream request". Do not
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
