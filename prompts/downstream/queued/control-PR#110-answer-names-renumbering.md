# control-PR#110 — the answer-names renumbering

Queued by [control#110](https://github.com/Hoplock/control/pull/110). It
queued Control's phase 0042, north-bound answer names, before instance
identity, and renumbered Control's queued phases after it. The obligations
are its `## Cross-repo impact` section's, for `hoplock/enterprise`. This is
not the sync 0042's own change owes; 0042 queues that one when it merges.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/110. Do not implement any queued prompt in this
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
(1) Control's queued phases from 0042 on moved by one (revision
answer-names): instance identity and supervisory registration is now 0043
(its E14 builds on it), the management console 0044, E2E topology 0045, CA
custody through the key store 0046, post-quantum posture 0047 and one
contract version 0048. Wherever this repository cites them, in its prompts
and its plan, cite each by name beside the number Control's docs/PLAN.md
§10 mapping table gives when you run. That table also supersedes the
numbers earlier queued syncs carry for the same phases, whichever runs
first. Citations of merged Control phases do not move.
(2) Control's new phase 0042, north-bound answer names, is queued, not
merged. Before 0043 makes the north-bound API a compatibility promise
(Control M19), it renames four answers whose members are Go field names
today. The fleet view's items (GET …/proxies) become snake_case, with a
zero instant absent rather than year one. The fleet view's liveness becomes
heartbeat_ttl_seconds, relay_registration_ttl_seconds,
target_capability_ttl_seconds and report_after_seconds, integer seconds,
where it was four nanosecond counts. The showcase join's items
(GET …/audit/blocked-commands) become {record, command, action,
target_zone, target_labels, decision_found, matched_rule, route_type,
decided_at}, with record an audit record's item. A session's loss rows
(GET …/audit/sessions/{session}/loss) become {cause, reports, records,
bytes}. Control's prompts/queued/0042-north-bound-answer-names.md specifies
each shape. Wherever this repository's prompts or plan read one of these
answers (its supervisory plane and its console read the fleet view), they
must build against 0042's names, and name the Control release that renames
them as not yet cut. Do not describe 0042 as merged (§2). Rewriting from
what merges is the sync 0042 owes when it merges.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
