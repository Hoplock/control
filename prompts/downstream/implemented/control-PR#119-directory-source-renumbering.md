# control-PR#119 — the directory-source renumbering

Queued by [control#119](https://github.com/Hoplock/control/pull/119), which
answered enterprise#51's first request, a directory source on the identity
sync sink, by queueing Control's phase 0042, directory source on the sync
sink. It renumbered Control's queued phases after it. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`. The sync owed for
0042's own change is not this one: 0042 queues that when it merges. Nor is
control#118's sync (`control-PR#118-confined-action-renumbering.md`), which
moves the same citations one revision earlier and is answered by its own PR.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/119. Do not implement any queued prompt in this
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
(1) Control's queued phases moved again (revision directory-source):
registered reports served is now 0043, north-bound answer names 0044,
instance identity and supervisory registration 0045, the management console
0046, E2E topology 0047, CA custody through the key store 0048, post-quantum
posture 0049 and one contract version 0050. On main when this was queued,
this repository still cited them as control#110 numbered them, because
control#118's sync had not run: reports as 0041 in docs/PLAN.md §4 and its
0009; answer names as 0042 in docs/PLAN.md, docs/PROTOCOL.md and its 0009,
0011, 0012, 0013, 0015 and 0016, seven of those by the path
prompts/queued/0042-north-bound-answer-names.md, which is now
prompts/queued/0044-north-bound-answer-names.md; instance identity as 0043 in
docs/PLAN.md, docs/PROTOCOL.md and its 0011, 0012, 0013 and 0016; the console
as 0044 in docs/PLAN.md and its 0007, 0008 and 0016; E2E as 0045 in its 0017;
and CA custody as 0046 in docs/PLAN.md and its 0010 and 0017 — and any others
a sync since added. Cite each by name beside the number Control's
docs/PLAN.md §10 mapping table gives when you run, whether or not
control#118's sync has run by then. Citations of merged Control phases, and of
0041, confined inbound actions, do not move.
(2) The request enterprise-PR#51-directory-source.md is answered by a Control
phase that is queued, not merged: 0042, directory source on the sync sink.
Where prompts/queued/0004's "What Control does not do yet" and docs/PLAN.md
§4's IdentitySync bullet point at
prompts/upstream/queued/enterprise-PR#51-directory-source.md, point at the
file where it is when you run (it moves to prompts/upstream/implemented/ once
control#119 has merged), and name Control's 0042 as queued. Say in 0004 what
0042 will build, including what the request left to Control or did not ask
for: ext.SyncedSubject.Source and ext.SyncedGroup.Source, required, matching
^[a-z0-9][a-z0-9-]{0,62}$, KindInvalid on source otherwise;
ext.DirectoryRef{Tenant, Source, ExternalID}, taken by
SyncSink.DeactivateSubject(ctx, ref, reasonCode) and SyncSink.DeleteGroup(ctx,
ref), a ### Breaking MINOR that also lists Source becoming required; every
key 0040 set on an ExternalID (link and group rows, ordering, tombstones,
owed delivery, the per-identity lock, a group's members) keyed on (tenant,
source, ExternalID), with a subject's Groups resolving within its own source
only; a login name still unique per tenant across sources (KindConflict), so
a person who moves between directories is two subjects and the second is
refused while the first presents the name; a tied connector naming
--directory-source beside --directory-claim, both or neither, refused for a
source nothing in the tenant has written yet (the error names the sources it
has), so a sync provisions before a connector is tied, and directory_source
shown on GET /api/v1/session/methods beside directory_claim; a migration
keying every row written before the release, and every tied connector, under
the source "default", so a sync rebuilt with Source "default" carries on and
one sending another name collides loudly; source on the subject read's
directory {provider, source, external_id} (the read's top-level source is
unchanged and is not the directory's), on the deactivation's standing entry
in the decision record beside external_ref, and on every identity_sync.*
record. Do not describe any of it as existing (§2): 0004 keeps feeding a
tenant from one directory, keeps refusing a second one naming the request,
and keeps the criteria for two directories in one tenant skipped. The
group-scope request (enterprise-PR#51-directory-group-scope.md) is not
answered by this and stays as it is. Rewriting 0004 from what merges is the
sync 0042 owes when it merges.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
