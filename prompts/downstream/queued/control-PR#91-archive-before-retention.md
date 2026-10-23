# control-PR#91 — an archive fed before retention deletes

Queued by [control#91](https://github.com/Hoplock/control/pull/91), Control's
phase 0025, which answers `Hoplock/enterprise#26` item 1: Control feeds a
registered `ext.ArchiveStore` every committed record and capture, and
retention never deletes what the archive has not confirmed. It cuts
`v0.12.0`, with a `### Breaking` section (`ArchiveStore.ArchiveCaptures`). The
obligations are its `## Cross-repo impact` section's, for `hoplock/enterprise`,
the repository that asked.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/91. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, all holding from v0.12.0, never from main —
(1) Its 0005, "What Control does not do yet", rewritten from what merged, and
no longer built around the gap: the feed (Archive gets every committed record,
one tenant's stream per batch, ascending Sequence, contiguous from the last
confirmed record, at least once; nil confirms and Control moves a durable
position; an error confirms nothing and is offered again after a backoff, for
that stream alone; duplicates happen, so stay idempotent on RecordID);
ArchiveCaptures(ctx, []ext.Capture) error and ext.Capture{Tenant, RecordID,
SessionID, Digest (capture_sha256, "sha256:<hex>"), Size, Body}, a capture
offered only after its record is confirmed; ext.AuditRecord.Stream, PrevHash
and Body (the hashed canonical JSON) and ext.ChainLink(tenant, stream,
sequence, prevHash, body) with ext.ChainVersion "hoplock.chain/1", the code
Control's own chain uses, so its verifier recomputes links with it rather than
reimplementing Control's; a stream retained before registration starting
after its marker, explained by operator.audit.retained on the control stream;
the retention gate (409 retention_not_archived {stream, archived_through_seq}
for a pass by through_seq beyond what the archive holds, records and captures
both; a pass by before held back; a capture pass run to the earliest
unconfirmed capture's recorded_at, signed there, emptying only confirmed
captures; held_by_archive on both answers, archived_through_seq on the prefix
answer, operator.held_by_archive and operator.archived_through_seq on the
record); and the lag on the archive row of GET …/extensions
{streams_behind, oldest_unconfirmed_at, captures_waiting} for the caller's
tenant. Turn its skipped end-to-end criteria into real ones.
(2) Its 0006 (SIEM export): every sink sees the new AuditRecord fields; sink
delivery itself is Control's 0026, which owes its own sync.
(3) Its plan: §4's ArchiveStore row and its misreadings, and wherever E6 says
what reaches the archive — every record and every capture, with Control's
retention waiting on its confirmation.
(4) Its pin and the break: its archive implements ArchiveCaptures or it does
not compile against v0.12.0; Control still calls Search nowhere.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
