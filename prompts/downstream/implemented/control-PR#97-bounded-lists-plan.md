# control-PR#97 — bounded lists: the plan, and nine phases renumbered

Queued by [control#97](https://github.com/Hoplock/control/pull/97), Control's
phase 0028. It adds decision M26: every north-bound list is bounded by its
caller and says where it stopped, Control's lists and a host's alike. It
queues the implementation as Control phases 0029–0037, and moves the phases
after them by nine. It cuts `v0.15.0`, a MINOR that changes no behaviour. The
obligations are its `## Cross-repo impact` section's, for
`hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/97. Do not implement any queued prompt in this
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
(1) Every Control phase number it cites from 0029 on moved by nine; resolve
each through Control's docs/PLAN.md §10, the "bounded-lists-chunks" rows:
host namespaces 0029→0038, action executor 0030→0039, identity sync
0031→0040, reports 0032→0041, instance identity 0033→0042 (its E14 builds on
it), console 0034→0043, E2E 0035→0044, CA custody 0036→0045, post-quantum
0037→0046, one contract version 0038→0047. Update every citation in its
prompts and plan. This is true from v0.15.0.
(2) M26 (Control's PLAN §2): over Control phases 0029–0037, each in its own
release with its own ### Breaking, every Control north-bound list answers
{<list>, truncated, next}. ?limit= defaults to 500, and above 10 000 it is
400 invalid_request {field: limit, problem: exceeds_maximum, limit: "10000"}
(refused, never clamped). ?after= takes an opaque cursor; a bad one is
400 invalid_request {field: after, problem: invalid}. GET
…/fleet/target-observations splits into …/rungs and …/key-exchanges (0032).
Wherever its prompts or plan read one of Control's lists (the supervisory
plane, its console, anything reading grants, audit or the fleet), they must
follow next until truncated is false, and name the Control release they
build against.
(3) M26.7: Control phase 0030 adds server.ReadList, (server.List).Write,
server.DefaultListLimit (500) and server.MaxListLimit (10 000), so a host's
list reads limit and after and answers truncated and next as Control's do,
its cursor named for its provider. Its prompts that build list routes
(approval inbox, archive search, report schedules, and any other) should be
written against them, from the release 0030 cuts. Its own route tests should
assert the shape, because Control cannot see which host route is a list.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
