# control-PR#84 — the report-provider renumbering

Queued by [control#84](https://github.com/Hoplock/control/pull/84), which
answered enterprise#26's request for registered reports served by queueing
Control's phase 0032, registered reports served. It renumbered Control's
queued phases after it. The obligations are its `## Cross-repo impact`
section's, for `hoplock/enterprise`. The sync owed for 0032's own change is not
this one: 0032 queues that when it merges.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/84. Do not implement any queued prompt in this
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
(1) Control's queued phases moved (revision report-provider): instance
identity and supervisory registration is now 0033, the management console
0034, E2E topology 0035, CA custody through the key store 0036, post-quantum
posture 0037 and one contract version 0038. Where this repository cites them —
instance identity in its 0011 and 0012; the console in its 0008 and 0016; E2E
in its 0017; CA custody in docs/PLAN.md §4 and in its 0010 and 0017; and any
others a sync since added — cite each by name beside the number Control's
docs/PLAN.md §10 mapping table gives when you run. That table also supersedes
the numbers the syncs of control#78, control#80, control#81, control#82 and
control#83 carry for the same phases, whichever of the syncs runs first.
Citations of merged Control phases do not move.
(2) The request enterprise-PR#26-report-provider-routes.md is answered by a
Control phase that is queued, not merged: 0032, registered reports served.
Where prompts/queued/0009 and docs/PLAN.md §4 point at
prompts/upstream/queued/enterprise-PR#26-report-provider-routes.md, point at
the file where it is when you run (it moves to prompts/upstream/implemented/
once control#84 has merged), and name Control's 0032 as queued. Say in 0009's
"What Control does not do yet" what 0032 will build, including what the
request left to Control: Control serves every registered ext.ReportProvider
under audit:read, at GET …/reports and POST …/reports/generate, with the
provider and the report named in the body and never in the path, because a
provider name is a module path; it generates only a report the provider lists
for the tenant, and checks the format, the parameters and the period (from
inclusive, to exclusive) against the descriptor before Generate, so a pack
need not repeat those checks; it answers with the format's own media type
(application/json, text/csv; charset=utf-8, application/pdf), nosniff and
attachment, never the provider's ContentType, and refuses a Report whose
Format or ContentType disagrees; a generation has its own bound
(north.report_timeout) and a per-node ceiling (north.max_concurrent_reports),
and a body that fails partway aborts the response; ErrNotFound is 404,
ErrInvalid 400, ErrUnavailable 503 report_provider_unavailable, everything
else 502 report_provider_failed, and the ceiling 503 report_generation_busy;
server.CallerFrom answers inside Reports and Generate, for attribution and
never authorisation, and Control records nothing of a generation on its
control stream, so a pack that keeps generated reports as evidence records
them itself (E6); a provider whose Reports fails is listed with its code
rather than dropped. Say which of 0009's planned report routes that leaves
its own — the history of generated reports (audit:read), schedules and
campaigns (report:write) — and that listing and on-demand generation become
Control's, in docs/PLAN.md §3's route table as well. Do not describe any of it
as existing (§2): 0009 keeps serving its packs through its own routes and
keeps the Control end-to-end criterion skipped. Rewriting 0009 from what
merges is the sync 0032 owes when it merges.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
