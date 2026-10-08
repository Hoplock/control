# control-PR#52 — the queue-order renumbering

Queued by [control#52](https://github.com/Hoplock/control/pull/52), which renumbered
every queued Control prompt into the order it runs in. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/52. Do not implement any queued prompt in this
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
section: (1) Three citations of Control's queued phases change number:
docs/PLAN.md's note on 0008 ("What 0008 now waits on instead is Control's
0014") becomes Control's 0018; prompts/queued/0011 ("Control's phase 0015
already acquires it") and prompts/queued/0012 ("the outbound registrations
Control's phase 0015 makes") become Control's 0019. Citations of merged Control
phases and of PRs do not move. (2) The Control phases this repository waits on
now run first, in this order: tagged releases 0014 (0001's pin), the public
server package 0015 (0001's start, and every route), self-service grant
requests 0016 (0003), ending an external window 0017 (0008); 0008 also waits on
the north-bound API, 0018. Wherever the text names the Control phase it waits
on, use the new number. (3) ext/point.go: ReportProvider's ControlShips text
now reads "(phases 0010, 0018)"; no signature changes, and nothing here reads
that text, so no action. Any other old Control number resolves through
Control's docs/PLAN.md §10 table.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
