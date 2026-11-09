# control-PR#150 — the console's Explain and audit view

Queued by [control#150](https://github.com/Hoplock/control/pull/150), Control's
phase 0051, released as `v0.38.0`: Explain, the audit view and session replay,
the timeline and the time range picker in the component set, and north-bound
API version `3` to serve them. No shared surface changed (`ext`, `server`, the
contract and the cross-repo protocol are untouched); the obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/150. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.38.0 and never from main —
(1) its pin, and what its binary now does: migration 0025 (a partial index on
audit_records; writes wait while it builds), north-bound API version 3
(north_api_min still 1), Control's console serving Explain at
/console/explain[/sessions/<id>|/decisions/<id>] and the audit view at
/console/audit/records, /console/audit/blocked-commands and
/console/audit/sessions/<id>/replay, and the supervised badge linking to
/console/audit/records?principal=<sup-…>; (2) for the supervisory plane (its
E14) and any prompt that reads a supervised deployment's trail: a deployment
at north_api >= 3 answers GET …/audit/records?principal=<principal> — the
records naming that principal as actor (attributes.principal_id), so the
plane can read back what its own sup- credential did there — and
GET …/audit/records/{record}/capture (audit:read; record_id, session_id,
capture_bytes, capture_sha256, retained, payload in base64 unless retained;
404 for a record with no capture), and one below 3 does neither, so a client
declares its version and checks versions.north_api before relying on either;
acting_operator is still never a filter; (3) the prompts that build its
screens build them from Control's component set at the pinned version,
which now includes Timeline/TimelineSkeleton (an ordered, keyboard-choosable
event list), TimeRangePicker with readRange/rangeQuery/resolveRange (all
time, relative as range=, absolute as from=/to= in ISO-8601 UTC), typed text
fields on FilterBar, Tree's expandAll and STATUS.unserved — a report
schedule's or history's time filter is TimeRangePicker, never one of
Enterprise's own; and their browser checks may substitute the server's own
answer changed (the harness's `patch` substitute) for a state the seeded data
cannot reach; (4) an Enterprise screen that links to an explanation or an
audit view names Control's prefix and these deep links
(/console/explain/sessions/<id>, /console/audit/records?<filters>), never a
path of its own (M27); (5) Control's queued prompts were renumbered by the
console-look revision: a new 0052 chooses the console's look with the
operator, and 0052→0053, 0053→0054, 0054→0055, 0055→0056, 0056→0057,
0057→0058, 0058→0059, 0059→0060, 0060→0061, 0061→0062, 0062→0063 — the console
is now 0049–0059, its consistency audit 0059, the E2E topology 0060 and one
contract version 0063. Update every citation of those numbers in this
repository's prompts and plan, resolving each through Control's PLAN §10
table, and note that the console's look will change at the release 0052 cuts,
which this repository's screens inherit through the tokens.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
