# control-PR#82 — the sink-delivery renumbering

Queued by [control#82](https://github.com/Hoplock/control/pull/82), which
answered enterprise#26's request for audit sink delivery by queueing Control's
phase 0026, audit sink delivery. It renumbered Control's queued phases after
it. The obligations are its `## Cross-repo impact` section's, for
`hoplock/enterprise`. The sync owed for 0026's own change is not this one:
0026 queues that when it merges.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/82. Do not implement any queued prompt in this
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
(1) Control's queued phases moved (revision sink-delivery): the shared event
plane is now 0027, bounded lists 0028, host namespaces 0029, the action
executor 0030, instance identity and supervisory registration 0031, the
management console 0032, E2E topology 0033, CA custody through the key store
0034, post-quantum posture 0035 and one contract version 0036. Where this
repository cites them — bounded lists in its 0004, 0008, 0009, 0011, 0012,
0013 and 0016; instance identity in its 0011 and 0012; the console in its
0008 and 0016; E2E in its 0017; CA custody in docs/PLAN.md §4 and in its 0010
and 0017; and any others a sync since added — cite each by name beside the
number Control's docs/PLAN.md §10 mapping table gives when you run. That
table also supersedes the numbers the syncs of control#78, control#80 and
control#81 carry for the same phases, whichever of the syncs runs first.
Citations of merged Control phases do not move.
(2) The request enterprise-PR#26-audit-sink-delivery.md is answered by a
Control phase that is queued, not merged: 0026, audit sink delivery. With
sinks registered, it will deliver every committed record to every registered
ext.AuditSink, per tenant and stream, in ascending sequence, at least once,
off ingest and the decision path, as one more consumer of the feed Control's
0025 builds. Where prompts/queued/0006 and docs/PLAN.md §4 point at
prompts/upstream/queued/enterprise-PR#26-audit-sink-delivery.md, point at the
file where it is when you run (it moves to prompts/upstream/implemented/ once
control#82 has merged), and name Control's 0026 as queued. Say in 0006's
"What Control does not do yet" what 0026 will build, including the four
things the request left to Control: the position is Control's, kept in its
store per sink, tenant and stream, so a sink keeps no cursor of its own, and
a sink is named by its Registration.Provider, with a new or renamed one
starting at each stream's chain start; Export is called concurrently across
streams and tenants, never twice for one stream, so a sink must be safe for
concurrent use; retention never waits on a sink, and a sink behind a
retained prefix resumes after the marker, with the gap counted on its lag and
explained by the operator.audit.retained record it receives; and the record
a sink receives is the one the archive does, with the Stream, PrevHash and
Body that 0025 adds. Say too that delivery writes no audit record of its own,
and that each sink's lag will show on GET …/extensions. docs/PLAN.md §6's
0006 row says the sinks keep "cursors"; say that the position will be
Control's. Do not describe any of it as existing (§2): 0006 keeps building
against a fake feed and keeps its end-to-end criteria skipped. Rewriting
0006 from what merges is the sync 0026 owes when it merges.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
