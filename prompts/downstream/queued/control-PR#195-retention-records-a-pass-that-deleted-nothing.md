# control-PR#195 — a retention pass that deleted nothing is recorded too

Queued by [control#195](https://github.com/Hoplock/control/pull/195), Control's
phase 0070, released as `v0.57.0`. It records a retention pass by instant
that deleted nothing, so every registered sink and the archive are handed one
more kind of `operator.audit.retained` record. `ext`, `server`, the contract,
the north-bound API version and the cross-repo protocol are untouched. The
obligations are its `## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/195. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.57.0 and never from main —
(1) its pin, and what its binary now does: a retention pass by instant
(POST …/audit/retention with `before`) that deletes nothing — the instant
reaches no record past the stream's marker, or a registered archive holds it
back entirely — is recorded on the control stream as operator.audit.retained,
in the pass's own transaction, with operator.deleted "0", `after` the
sequence the stream is already retained through ("0" with no marker), the
existing marker's operator.through_hash, operator.key and operator.signature
(all three absent with no marker), and with an archive registered
operator.held_by_archive and operator.archived_through_seq. It writes no
marker and no seal; the answer is unchanged; a refused pass still records
nothing. No migration; north-bound API version unchanged;
(2) every ext.AuditSink and ext.ArchiveStore Enterprise registers is handed
these records like any other. One with operator.deleted "0" explains no gap:
Control counts no sink skip for it. Where Enterprise's plan or prompts treat
an operator.audit.retained record as always meaning a deletion, or a SIEM
mapping or export keys on one as a gap's explanation, they should read
operator.deleted and say so;
(3) the retention trail its binary serves (/console/audit/retention) shows
such passes (Deleted 0, "Held back: archived through N" when held), and a
completed pass's answer says it is recorded whether or not it deleted.
No citation of Control's numbers moves; nothing was renumbered.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
