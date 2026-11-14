# control-PR#157 — the console's stream gaps, the archive's lag and retention

Queued by [control#157](https://github.com/Hoplock/control/pull/157), Control's
phase 0057, released as `v0.44.0`. It adds a session's holes, the stream-gaps
view and the retention view to Control's audit view, two statuses to the set
Enterprise's screens are built from, and a fix to the typed-name confirm they
share. `ext`, `server`, the contract, the north-bound API version and the
cross-repo protocol are untouched. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/157. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.44.0 and never from main —
(1) its pin, and what its binary now does: no migration; north-bound API
still version 5; Control's console shows a session's logging.gap reports in
its replay (/console/audit/sessions/<id>/replay), each at its span and never
at the report's timestamp, the session partial with its loss summed per cause;
the stream-gaps view at /console/audit/gaps[?proxy=&cause=&scope=sessionless]
(audit:read), where a gap of no session is shown; and the retention view at
/console/audit/retention — the archive's lag, a retention pass
(audit:retain, typed-name confirm), retention_not_archived shown as a refusal
that deleted nothing, a held-back pass said so, and the passes in the trail.
An Enterprise screen links to those paths under /console/ and never builds its
own view of gaps, the archive's lag or a retention pass;
(2) the component set Enterprise's screens build from gains, at this pin, the
statuses Evicted (outage: records the server never received) and Refused at
ingest (warn: records the server received and refused itself — neither a deny
nor an outage) in DESIGN.md §6; an Enterprise screen showing a stream gap
renders through these and never invents its own; retention_not_archived,
retention_unverified and retention_key_not_configured have sentences;
(3) ConfirmDialog now clears what was typed as it closes, not as it opens, so
an Enterprise screen's typed-name confirm opened a second time starts empty —
no Enterprise change is needed, and no Enterprise screen may rely on the old
behaviour;
(4) an archive Enterprise registers (ext.ArchiveStore) has its lag shown on
Control's retention view through FeedLag, and a pass by sequence beyond what it
confirmed is refused there: Enterprise's archive need do nothing new, and an
Enterprise e2e fixture that registers an archive or a key store can follow
Control's ui/e2e/fixture/host/retention.go. No citation of Control's numbers
moves; nothing was renumbered.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
