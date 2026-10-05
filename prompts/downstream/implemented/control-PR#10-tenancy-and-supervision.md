# control-PR#10 — tenancy (M18) and the supervision API (M19)

Backfilled from [control#10](https://github.com/Hoplock/control/pull/10), which
predates the request queues: the kickoff its `## Cross-repo impact` section
handed over, verbatim. Answered by
[enterprise#4](https://github.com/Hoplock/enterprise/pull/4).

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/10. Do not implement
any queued prompt in this session.

Before anything else, confirm that upstream change is MERGED; if it is not,
stop and say so (§2). Then update this repository's prompts, plan, and
protocol so the next session builds against what is now true.

A sync changes text, not behaviour: it implements, enforces, and vendors
nothing, hand-edits no vendored artifact, and adds, renames, or renumbers no
prompt. Land each obligation in the prompt that will implement it, not only in
the plan. If the work seems to need something the upstream repository does not
have, that is §3.2 — stop and tell me rather than approximating it.

The obligations to land are in the upstream PR's "## Cross-repo impact"
section: E11 rewritten (mechanism is Control's M18, governance stays ours),
E2 amended (a supervisory plane is not an ext implementation), E9 amended
(node vs instance; the registration singleton is ClusterCoordinator's), and
Control M19's output — instance identity, north-bound version negotiation,
the registration protocol and event kinds, health at deployment/node/fleet
level, and the supervisor credential's scope grammar.

Branch claude/sync-mssp-multi-instance, commit with the `sync` scope, and open
one PR whose body names the upstream PR, confirms it is merged, and says how
you searched for stale references — the actual grep, not "I looked carefully".
```
