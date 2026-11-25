# control-PR#183 — the registered-finding-line renumbering

Queued by [control#183](https://github.com/Hoplock/control/pull/183), which
answered enterprise#76's request for a registered validator's finding at its
rule's line by queueing Control's phase 0067, a registered validator's finding
at its rule's line. It renumbered Control's queued phases after it. The
obligations are its `## Cross-repo impact` section's, for `hoplock/enterprise`.
The sync owed for 0067's own change is not this one: 0067 queues that when it
merges.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/183. Do not implement any queued prompt in this
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
(1) Control's queued phases moved (revision registered-finding-line): the
visual claim-mapping editor is now 0068, a host's console reaches the shell
0069, a retention pass that deleted nothing is recorded too 0070, every list
through the data table 0071, and E2E scenarios through a test host's
extensions 0072. Where this repository cites them — the claim-mapping editor
(as 0067) in docs/PLAN.md and in its 0016; a host's console reaching the shell
(as 0068) in docs/PLAN.md and in its 0001, 0009 and 0016; every list through
the data table (as 0070) in docs/PLAN.md and in its 0016; and any others a
sync since added — cite each by name beside the number Control's docs/PLAN.md
§10 mapping table gives when you run. Where a passage says the PR that queued
one of them "renumbered nothing", that stays true of that PR; say that this
revision renumbered it. That table also supersedes any number the syncs of
control#169, control#173 and control#176 carry for the same phases, whichever
of the syncs runs first. Citations of merged Control phases do not move.
(2) The request enterprise-PR#76-registered-finding-line.md is answered by a
Control phase that is queued, not merged: 0067, a registered validator's
finding at its rule's line. Where docs/PLAN.md §4 (the PolicyValidator bullet,
"No validator's finding is set beneath a line yet") and prompts/queued/0009
("A guardrail's finding is not set beneath a line at …") point at
prompts/upstream/queued/enterprise-PR#76-registered-finding-line.md, point at
the file where it is when you run (it moves to prompts/upstream/implemented/
once control#183 has merged), and name Control's 0067 as queued and what it
will build: every finding in the publish-time report whose rule names a rule
of the compiled bundle is placed at that rule's line, resolved by Control from
the compiled program, so a guardrail's finding whose Path is a rule's id is
set beneath that rule in the report, in a version's source and in the
candidate editor; a Path that names no rule is not placed; the finding's id
(<Code>/<provider>/<Path>), its severity and its acceptance do not change;
and ext/ changes by Finding.Path's doc comment alone, with no Line field. Do
not describe any of it as existing (§2): both texts' statements that no
guardrail's finding is set beneath a line stay true at every release this
repository can pin, and 0009's rule against computing a line here, in Path or
in Detail, stands. Rewriting both from what merges is the sync 0067 owes when
it merges.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
