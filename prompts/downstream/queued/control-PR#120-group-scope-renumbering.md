# control-PR#120 — the group-scope renumbering

Queued by [control#120](https://github.com/Hoplock/control/pull/120), which
answered enterprise#51's second request, claim-mapping group entries scoped to
directories, by queueing Control's phase 0043, directory-scoped group entries.
It renumbered Control's queued phases after it. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`. The sync owed for
0043's own change is not this one: 0043 queues that when it merges. Nor are
control#118's and control#119's syncs (`control-PR#118-confined-action-renumbering.md`,
`control-PR#119-directory-source-renumbering.md`), which move the same
citations one and two revisions earlier and are answered by PRs of their own.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/120. Do not implement any queued prompt in this
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
(1) Control's queued phases moved again (revision group-scope): registered
reports served is now 0044, north-bound answer names 0045, instance identity
and supervisory registration 0046, the management console 0047, E2E topology
0048, CA custody through the key store 0049, post-quantum posture 0050 and one
contract version 0051. On main when this was queued, this repository still
cited them as control#110 numbered them, because neither control#118's sync
nor control#119's had run: reports as 0041 in docs/PLAN.md §4 and its 0009;
answer names as 0042 in docs/PLAN.md, docs/PROTOCOL.md and its 0009, 0011,
0012, 0013, 0015 and 0016, seven of those by the path
prompts/queued/0042-north-bound-answer-names.md, which is now
prompts/queued/0045-north-bound-answer-names.md; instance identity as 0043 in
docs/PLAN.md, docs/PROTOCOL.md and its 0011, 0012, 0013 and 0016; the console
as 0044 in docs/PLAN.md and its 0007, 0008 and 0016; E2E as 0045 in its 0017;
and CA custody as 0046 in docs/PLAN.md and its 0010 and 0017 — and any others
a sync since added. Cite each by name beside the number Control's
docs/PLAN.md §10 mapping table gives when you run, whether or not the earlier
two syncs have run by then. Citations of merged Control phases, of 0041,
confined inbound actions, and of 0042, directory source on the sync sink, do
not move.
(2) The request enterprise-PR#51-directory-group-scope.md is answered by a
Control phase that is queued, not merged: 0043, directory-scoped group
entries. Where prompts/queued/0004's "Read first" and "What Control does not
do yet" and docs/PLAN.md §4's IdentitySync bullet point at
prompts/upstream/queued/enterprise-PR#51-directory-group-scope.md, point at
the file where it is when you run (it moves to prompts/upstream/implemented/
once control#120 has merged), and name Control's 0043 as queued. Say in 0004
what 0043 will build, including what the request left to Control or did not
ask for: a groups.map entry's directories, a list of directory sources
(0042's pattern), matching a directory group of its external name from a
listed source and never a login's group claim; an entry listing the group's
source applies instead of the unscoped entry for that name, never as well as
it; groups.require_directories: true makes every directory group match only
an entry listing its own source, so unscoped entries serve logins only —
chosen over a rule that unscoped entries stop matching once a second source
has written, which would make a mapping version's effect depend on store
state; no wildcard; three rejection codes (mapping.directory_name,
mapping.directories_empty for directories: [], mapping.directory_duplicate
for a source listed twice for one external), two unscoped entries for one
external still mapping.group_duplicate; a scoped entry counted once per
directory it lists against the mapping's 512-entry bound; schema_version
still 1, and a build without 0043 refusing a scoped mapping as an outage,
never applying it unscoped, so a rolling upgrade finishes before one is
written; every mapping write, scoped or not, reaching every directory
subject by itself — subjects whose row names another mapping version
recalculated by the server after the write commits, in batches under the
sink's own lock order, at once on the node that took an API write and by a
once-a-minute sweep otherwise, whether or not a sync is registered or
started, their cached decisions dropped as a group change drops them (live
sessions are not ended), each run recorded as
operator.claim_mapping.applied — so 0004 needs neither a re-push nor its
reconciliation to apply a mapping (a re-push of unchanged data is a replay
Control writes nothing for), and its "When the sync is not running" gains
that a mapping change still reaches directory subjects while no sync
applies anything; directories, require_directories and
stale_directory_subjects on GET …/identity/claim-mapping; and
directory_source on a decision record's subject, which bears on 0004's
explain criterion. Do not describe any of it
as existing (§2): 0004 keeps feeding a tenant from one directory, keeps
refusing a second one naming both requests, and keeps the criteria for two
directories in one tenant skipped, until its pin carries both 0042 and 0043.
Rewriting 0004 from what merges is the sync 0043 owes when it merges.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
