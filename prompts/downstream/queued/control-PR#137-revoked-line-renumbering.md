# control-PR#137 — the revoked-line renumbering

Queued by [control#137](https://github.com/Hoplock/control/pull/137), which
answered enterprise#64's first request, a revoked registration says so, by
queueing Control's phase 0047, the revoked registration line. It renumbered
Control's queued phases after it. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`. The sync owed for
0047's own change is not this one: 0047 queues that when it merges.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/137. Do not implement any queued prompt in this
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
(1) Control's queued phases moved (revision revoked-line): the management
console is now 0048, E2E topology 0049, CA custody through the key store
0050, post-quantum posture 0051 and one contract version 0052. On main when
this was queued, this repository cited them as control#120 numbered them:
the console as 0047 in docs/PLAN.md §3 and its 0007, 0008 and 0016; E2E as
0048 in its 0017; and CA custody as 0049 in docs/PLAN.md §4 and its 0010 and
0017 — and any others a sync since added. Cite each by name beside the
number Control's docs/PLAN.md §10 mapping table gives when you run. Hits that
only recount a number's history ("control#118 0047", "0048 as control#119
did") are history and stay. Citations of merged Control phases do not move.
(2) The request enterprise-PR#64-registration-revoked-line.md is answered by
a Control phase that is queued, not merged: 0047, the revoked registration
line. Where prompts/queued/0012's "What v0.33.0 does not give this phase" and
its criterion that skips "local revocation wins"'s second half, docs/PLAN.md
§5's "Supervising one of Control's deployments", and its 0017 where it
asserts the revocation point at
prompts/upstream/queued/enterprise-PR#64-registration-revoked-line.md, point
at the file where it is when you run (it moves to
prompts/upstream/implemented/ once control#137 has merged), and name
Control's 0047 as queued. Say in 0012 what 0047 will build, including what
Control decided that the request did not ask for: the revoked line as asked
(kind revoked, the heartbeat's id form, at the instant the deployment record
holds, no by), sent only when a node ends the registration because of a
local revocation, and never for a shutdown, a lost connection, a lost
singleton or a silent supervisor; the registration protocol negotiated — the
supervisor's resume line gains protocol, the newest it reads, absent meaning
1; hello's protocol the newest the deployment speaks, 2; the session the
lower of the two — so a supervisor is sent revoked only when its resume
declares "protocol":2, and one that declares nothing is sent exactly what
v0.33.0 sends; the promise written in Control's docs/API-VERSIONING.md beside
the north-bound one, whose version does not move; the line followed by a
clean end of the request body, on which the supervisor ends its response, all
within one heartbeat interval, so an instance revoked while the plane is not
reading ends with no line. Do not describe any of it as existing (§2): 0012
keeps its revoked state unwired, and a revoked instance shows as
unreachable, until its pin carries 0047's release. Rewriting 0012 from what
merges is the sync 0047 owes when it merges.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
