# control-PR#78 — the host-namespace renumbering

Queued by [control#78](https://github.com/Hoplock/control/pull/78), which
queued Control's phase 0027, host routes in a namespace of their own, at the
user's direction, and renumbered Control's queued phases after it. The
obligations are its `## Cross-repo impact` section's, for `hoplock/enterprise`.
The sync owed for 0027's own change is not this one: 0027 queues that when it
merges.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/78. Do not implement any queued prompt in this
session.

Before anything else, confirm that upstream change is MERGED; if it is not,
stop and say so (§2). Then update this repository's prompts, plan, and
protocol so the next session builds against what is now true.

A sync changes text, not behaviour: it implements, enforces, and vendors
nothing, hand-edits no vendored artifact, and adds, renames, or renumbers no
prompt. Land each obligation in the prompt that will implement it, not only in
the plan. If the work seems to need something the upstream repository does not
have, that is §3.2 — never approximate it, and do not stop at telling me: name
the exact shape and queue the "Upstream request" kickoff from docs/KICKOFF.md,
filled in, in this repository's prompts/upstream/queued/ (§4.3).

The obligations to land are in the upstream PR's "## Cross-repo impact"
section: for hoplock/enterprise —
(1) Control's queued phases moved (revision host-namespace): instance
identity and supervisory registration is now 0028, the management console
0029, E2E topology 0030, CA custody through the key store 0031, post-quantum
posture 0032 and one contract version 0033. Where this repository cites them
— instance identity in its 0011 and 0012, the console in its 0008 and 0016,
E2E in its 0017, and any others the sync of control#73 or control#74 added —
cite each by name beside its new number. Every citation of a Control phase
number resolves through Control's docs/PLAN.md §10 mapping table. Citations
of merged Control phases do not move.
(2) A Control phase that will move every host route is queued, not merged:
0027, host routes in a namespace of their own. It will mount a host's routes
of both classes, and its console screens, in a namespace no route of
Control's enters, and it owes this repository a sync of its own when it
merges. Until then this repository builds against the paths it has. Where
docs/PLAN.md §3 and prompts/queued/0001 describe where a host's routes are
mounted, name 0027 as queued and say the paths will move. Do not describe a
namespace that does not exist yet (§2). Where this repository keeps its own
rule that no pattern sits under one of Control's families, keep it: 0027
decides whether that rule is still needed.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
