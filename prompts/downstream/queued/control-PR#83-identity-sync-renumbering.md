# control-PR#83 — the identity-sync renumbering

Queued by [control#83](https://github.com/Hoplock/control/pull/83), which
answered enterprise#26's request for Control's sync sink by queueing Control's
phase 0031, identity sync. It renumbered Control's queued phases after it. The
obligations are its `## Cross-repo impact` section's, for
`hoplock/enterprise`. The sync owed for 0031's own change is not this one:
0031 queues that when it merges.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/83. Do not implement any queued prompt in this
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
(1) Control's queued phases moved (revision identity-sync): instance identity
and supervisory registration is now 0032, the management console 0033, E2E
topology 0034, CA custody through the key store 0035, post-quantum posture
0036 and one contract version 0037. Where this repository cites them —
instance identity in its 0011 and 0012; the console in its 0008 and 0016; E2E
in its 0017; CA custody in docs/PLAN.md §4 and in its 0010 and 0017; and any
others a sync since added — cite each by name beside the number Control's
docs/PLAN.md §10 mapping table gives when you run. That table also supersedes
the numbers the syncs of control#78, control#80, control#81 and control#82
carry for the same phases, whichever of the syncs runs first. Citations of
merged Control phases do not move.
(2) The request enterprise-PR#26-identity-sync-sink.md is answered by a
Control phase that is queued, not merged: 0031, identity sync. Where
prompts/queued/0004 and docs/PLAN.md §4 point at
prompts/upstream/queued/enterprise-PR#26-identity-sync-sink.md, point at the
file where it is when you run (it moves to prompts/upstream/implemented/ once
control#83 has merged), and name Control's 0031 as queued. Say in 0004's
"What Control does not do yet" what 0031 will build, including what the
request left to Control: Control implements ext.SyncSink and starts the
registered ext.IdentitySync, with no ext signature change; a connector may
name a directory_claim, through which a login reaches the subject the
directory provisioned or is refused with federation.not_provisioned; the
directory is the only writer of what it provisions, so PUT and DELETE on such
a subject answer 409 subject_directory_managed; synced attributes and groups
reach policy only through the tenant's claim mapping, a group matched by its
directory Name, so Enterprise's claim rules feed the mapping rather than
replace it; a deactivation is a standing denial of its own, beside Control's
subject lock, lifted only by the directory, kept even for an ExternalID not yet
linked, and ordered against a re-activation by Control's clock; a deactivation
revokes grants, pending grant requests, north-bound sessions and tokens,
outstanding certificates and live sessions, and a retry finishes it; a
username another subject presents is KindConflict, and no existing subject is
adopted; and ExternalID must be unique per tenant across everything the sync
feeds in, which is where this repository's multi-IdP collision rule applies.
Say too that a Control decision record will name only Control's mapping
version, so 0004's criterion that explain names a claim rule needs a shape the
request did not ask for. Note it as such, and raise it as a request only if
0004 still needs it. Do not describe any of it as existing (§2): 0004 keeps
building against a fake sink and keeps its end-to-end criteria skipped.
Rewriting 0004 from what merges is the sync 0031 owes when it merges.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
