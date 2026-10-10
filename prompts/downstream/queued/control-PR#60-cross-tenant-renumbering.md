# control-PR#60 — the cross-tenant renumbering

Queued by [control#60](https://github.com/Hoplock/control/pull/60), which queued
Control's phase 0018, the cross-tenant access class answering enterprise#19, and
renumbered Control's queued phases after it. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/60. Do not implement any queued prompt in this
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
section: (1) Citations of Control's queued phases change number, 0018→0019,
0019→0020, 0020→0021. docs/PLAN.md: "managed through its 0018 routes" and
"What 0008 now waits on instead is Control's 0018" become 0019.
prompts/queued/0003: "Control's `explain` (its phase 0018)" becomes 0019.
prompts/queued/0008: "Control's `prompts/queued/0018-…`" becomes `0019-…` (the
section it quotes is now in 0019-northbound-api-and-policy-lifecycle.md);
"Control's queued 0018", "the release that 0018 cuts", "the release 0018 cut",
"its 0018's routes" and "the release its 0018 cuts" become 0019; "Control's
management console (its 0020)" becomes 0021. prompts/queued/0011: "Control's
phase 0019 already acquires it" becomes 0020. prompts/queued/0012: "the
learnings for its phase 0019", "phase 0019 reports health" and "the outbound
registrations Control's phase 0019 makes" become 0020. prompts/queued/0013:
"Control's north-bound API deliberately has no cross-tenant route (its 0018)"
becomes 0019. Citations of merged Control phases, of Control's 0016 and 0017,
and of PRs do not move. (2) The Control phase that answers
enterprise-PR#19-cross-tenant-access-class.md is now queued: 0018, the
cross-tenant access class. Where 0013 says "Look for the Control phase that
answers it before you start", name 0018 and say it is queued, not merged.
Rewriting that paragraph from what merged is the sync 0018 itself queues once
it merges, not this one. (3) The Control phases this repository waits on, in
the order they now run: 0016 self-service grant requests (its 0003), 0017
ending an external window (its 0008), 0018 the cross-tenant access class (its
0013), 0019 the north-bound API (its 0008's binding routes), 0020 instance
identity (its 0011 and 0012). Wherever the text names the Control phase it
waits on, use that number. (4) ext/point.go: ReportProvider's ControlShips text
now reads "(phases 0010, 0019)"; no signature changes, and nothing here reads
that text, so no action. Any other old Control number resolves through
Control's docs/PLAN.md §10 table.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
