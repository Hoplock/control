# control-PR#95 — the shared event plane

Queued by [control#95](https://github.com/Hoplock/control/pull/95), Control's
phase 0027: the event stream is the same whichever node a proxy reaches, its
log, floor and subscription set in Control's store (M25), and every node woken
through `ext.ClusterCoordinator`'s bus when another logs an event. It cuts
`v0.14.0`, a MINOR whose `### Breaking` is behaviour only: no signature
changed, and `ext.ClusterCoordinator` is now called, with the delivery its doc
comment states. The obligations are its `## Cross-repo impact` section's, for
`hoplock/enterprise`, whose E9 implements that seam.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/95. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, all holding from v0.14.0, never from main —
(1) Its E9 prompt (the clustered ext.ClusterCoordinator) and the plan's E9:
Control now calls Publish and Subscribe — every node publishes a wake-up on
topic hoplock.events.logged (Kind logged, Tenant set, Payload {"seq":N}) each
time it logs an event, and holds a subscription to that topic for its life.
(2) The same prompt is written against ext/cluster.go's "What the bus must
deliver", stated there rather than copied: at least once to every
subscription open when Publish was called, on every node including the
publisher's; any order, duplicates allowed; a subscription the bus cannot keep
up with CLOSED after what it already holds, never left open with events
skipped; closed when its context ends or it is cancelled; Tenant, Topic,
Kind, Origin and Payload delivered as published; Publish failing promptly,
never hanging; Node returning an id unique among live members and stable for
the process (Control keys the event streams a node holds by it in its store).
Its acceptance criteria test each.
(3) Its plan, wherever E9 says what clustering gives Control: the event
stream's state, order and replay are Control's (M25 — event_log,
event_streams, event_subscriptions in Control's store), so a clustered
coordinator adds delivery inside a heartbeat rather than inside
events.sync_interval (default 1s), and real elections for singletons (M19);
it carries no event and none of the order.
(4) Its pin: nothing fails to compile against v0.14.0, but the coordinator's
bus is now called, and an implementation that silently drops events to a slow
subscriber delays revocations on that node by up to events.sync_interval.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
