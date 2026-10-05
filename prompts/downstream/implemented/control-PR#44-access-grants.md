# control-PR#44 — access grants and the workflow seam (0012)

Backfilled from [control#44](https://github.com/Hoplock/control/pull/44), which
predates the request queues: the kickoff its `## Cross-repo impact` section
handed over, verbatim. Answered by
[enterprise#10](https://github.com/Hoplock/enterprise/pull/10).

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/44. Do not implement any queued prompt in this
session.

Before anything else, confirm that upstream change is MERGED; if it is not,
stop and say so (§2). Then update this repository's prompts, plan, and
protocol so the next session builds against what is now true.

A sync changes text, not behaviour: it implements, enforces, and vendors
nothing, hand-edits no vendored artifact, and adds, renames, or renumbers no
prompt. Land each obligation in the prompt that will implement it, not only in
the plan. If the work seems to need something the upstream repository does not
have, that is §3.2 — never approximate it, and do not stop at telling me: name
the exact shape and hand me the "Upstream request" kickoff from
docs/KICKOFF.md already filled in.

The obligations to land are in the upstream PR's "## Cross-repo impact"
section: (1) ext.GrantRequest.Scope (ext.GrantScope{Name, Hostnames, Labels,
Zones}) is the authoritative reach of a request; Targets now holds only the
inventory records of hostnames the scope names exactly, and Privileges is
[Scope.Name]. (2) Control's handling of each GrantWorkflow answer, stated on
the interface: Submit resubmitted under the same RequestID after an outage;
Status polled every grants.workflow_poll_interval (15s) and on read; Cancel on
operator cancel and when the requested window closes while pending; zero
Window ends mean as-requested and wider ones are clamped; only Approved: true
answers become approvers; KindDenied from Submit is a denial and KindNotFound
from Status closes the request as failed. (3) A denial creates no grant and a
registered workflow is never bypassed; any break-glass override is E8's.
(4) Control's webhook notifier is live beside any Enterprise notifier.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
and says how you searched for stale references — the actual grep, not "I looked
carefully".
```
