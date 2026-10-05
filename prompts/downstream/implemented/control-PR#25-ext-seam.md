# control-PR#25 — the ext extension seam (0004)

Backfilled from [control#25](https://github.com/Hoplock/control/pull/25), which
predates the request queues: the kickoff its `## Cross-repo impact` section
handed over, verbatim. Answered by
[enterprise#8](https://github.com/Hoplock/enterprise/pull/8).

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/25. Do not implement any queued prompt in this
session.

Before anything else, confirm that upstream change is MERGED; if it is not,
stop and say so (§2). Then update this repository's prompts, plan, and
protocol so the next session builds against what is now true.

A sync changes text, not behaviour: it implements, enforces, and vendors
nothing, hand-edits no vendored artifact, and adds, renames, or renumbers no
prompt. Land each obligation in the prompt that will implement it, not only in
the plan. If the work seems to need something the upstream repository does not
have, that is §3.2 — stop and tell me rather than approximating it.

The obligations to land are in the upstream PR's "## Cross-repo impact"
section: ext/ now exists with eleven interfaces whose exact signatures are in
docs/learnings/0004-extension-points-learnings.md and which every E-phase
implementing a point must be written against; registration is
ext.NewRegistry() -> RegisterX(ext.Registration{...}, impl) -> hand the
registry to Control, with no plugin loading, no reflection and no build tags;
Default: true is refused to any provider but "hoplock/control", so Enterprise
registers as an extension and supersedes Control's default where one exists;
registering twice at one point is an error naming both registrants, so two
Enterprise modules registering the same point fail at start-up rather than
silently last-wins; ext.KindDenied is the only error kind that can become a
deny and everything else is an outage (M11); AccessContextProvider returns
evidence, not a verdict, so the packaged Qualys and BMC Helix integrations are
written against ext.AccessEvidence; ClusterCoordinator is the seam E9
implements and extdefault.SingleNode is the shape it must match, including
that a second Lead on a held singleton is a conflict; and ext is versioned
with the module and pinned by Enterprise (E1, E3).

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
and says how you searched for stale references — the actual grep, not "I looked
carefully".
```
