# control-PR#188 — a registered validator's finding at its rule's line

Queued by [control#188](https://github.com/Hoplock/control/pull/188), Control's
phase 0067, which answers enterprise#76's `## Upstream request`, item 1
(`prompts/upstream/queued/enterprise-PR#76-registered-finding-line.md` in
`hoplock/enterprise`). This is the sync that phase owes the repository that
asked; control#183's renumbering sync is a separate file and says so. The
obligations are control#188's `## Cross-repo impact` section's, for
`hoplock/enterprise`. Nothing is owed to `hoplock/proxy`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/188. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, from Control's release v0.54.0 — the release
control#188 cuts — and never from Control's main:
(1) The pin, and what the binary now does. From v0.54.0, a registered
validator's finding whose Path is the id of a rule in the bundle is set
beneath that rule's line — the rule's `- id:` line, where Control's own
findings about the rule and the compiler's rejections of it are — in the
publish-time report (every answer that carries it: validate, upload, apply,
and activation's 409 findings_not_accepted and 422 policy_blocked), in a
version's source and in the candidate editor. Control resolves the line from
the program it compiled; the validator sets the rule's id in Path and nothing
else. A finding whose Path names no rule of the bundle (empty, a document
pointer, an id the bundle does not hold) is listed and placed nowhere. The
finding's id (<Code>/<provider>/<Path>), its severity, its acceptance and
ext/ are unchanged: ext changes by Finding.Path's doc comment alone, and
gains no Line field. The north-bound API stays at version 8.
(2) docs/PLAN.md §4 (the PolicyValidator bullet, "No validator's finding is
set beneath a line yet") and prompts/queued/0009 ("A guardrail's finding is
not set beneath a line at …") are rewritten to say that from v0.54.0 a
guardrail's finding is placed by its Path: one about a rule sets Path to that
rule's id and is set beneath that rule's line. The prohibition on computing a
line in this repository, in Path or in Detail, stands — now because Control
already does it, and because Path is part of the finding's id. 0009 gains an
acceptance criterion holding a guardrail's finding about a rule to being
placed at that rule's line against the pinned release. Where either text says
a finding is not placed until Control's 0067 lands, or names 0067 as queued,
say it is built and released in v0.54.0.
(3) Where this repository points at
prompts/upstream/queued/enterprise-PR#76-registered-finding-line.md, point at
prompts/upstream/implemented/enterprise-PR#76-registered-finding-line.md,
where it is by then.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
