# control-PR#80 — the action-executor renumbering

Queued by [control#80](https://github.com/Hoplock/control/pull/80), which
answered enterprise#26's request for Control's action executor by queueing
Control's phase 0028, inbound actions: the executor, a subject lock and a
target quarantine. It renumbered Control's queued phases after it. The
obligations are its `## Cross-repo impact` section's, for
`hoplock/enterprise`. The sync owed for 0028's own change is not this one:
0028 queues that when it merges.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/80. Do not implement any queued prompt in this
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
(1) Control's queued phases moved (revision action-executor): instance
identity and supervisory registration is now 0029, the management console
0030, E2E topology 0031, CA custody through the key store 0032,
post-quantum posture 0033 and one contract version 0034. Where this
repository cites them — instance identity in its 0011 and 0012, the console
in its 0008 and 0016, E2E in its 0017, CA custody in docs/PLAN.md §4 and in
its 0010 and 0017, and any others a sync since added — cite each by name
beside the number Control's docs/PLAN.md §10 mapping table gives when you
run. That table also supersedes the host-namespace numbers that the sync of
control#78 carries for the same phases, whichever of the two syncs runs
first. Citations of merged Control phases do not move.
(2) The request enterprise-PR#26-action-executor.md is answered by a Control
phase that is queued, not merged: 0028, inbound actions. It will implement
ext.ActionExecutor over Control's own mechanisms, start the registered
ext.ActionHandler, and add a subject lock and a target quarantine (standing
denials applied above Control's policy engine, each with operator routes).
Where prompts/queued/0007 and docs/PLAN.md §4 point at
prompts/upstream/queued/enterprise-PR#26-action-executor.md, point at the
file where it is when you run (it moves to prompts/upstream/implemented/
once control#80 has merged), and name Control's 0028 as queued.
Say in 0007's "What Control does not do yet" what 0028 will build,
including where it differs from the ask: lock and quarantine built rather
than refused; a lock no policy rule can write around; kill_session by
exactly one of SessionID or SubjectID; lock_subject on a break-glass
subject refused with KindDenied; ReasonText shown to the holder once
validated, Control's own text when it is empty; and a failed Start that
never stops Control. Do not describe any of it as existing (§2): 0007 keeps
building against a fake executor and keeps its end-to-end criteria skipped.
Rewriting 0007 from what merges is the sync 0028 owes when it merges.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
