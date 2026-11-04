# control-PR#131 — registered reports served

Queued by [control#131](https://github.com/Hoplock/control/pull/131), Control's
phase 0044. It answers enterprise#26's upstream request, item 5, as asked:
Control lists and generates every registered `ext.ReportProvider` on its
north-bound API, under `audit:read`, for the tenant the middleware resolved.
It is released as `v0.31.0`. The obligations are in its `## Cross-repo impact`
section, for `hoplock/enterprise`, the repository that asked.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/131. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.31.0 and never from main
(no ext or server signature changed; doc comments only) — (1) its 0009's
"What Control does not do yet", rewritten from what merged, and no longer
built around the gap: GET /api/v1/tenants/{tenant}/reports and POST
/api/v1/tenants/{tenant}/reports/generate, both audit:read; the body is
{provider, report, from, to, parameters, format}, the provider (the pack's
ext.Registration.Provider) and the report named in the body and never in the
path; a listing entry is {provider, version, reports:[{report, formats,
parameters:[{name, required, values}]}], error?} in registration order, not
paged (exempt from M26); every check Control makes before Generate, which its
packs need not repeat (the report is listed by Reports for that tenant, else
404; format present and listed; no parameter the descriptor does not name;
every Required one present; values inside a non-empty Values; from and to
RFC 3339, from before to, from inclusive and to exclusive); what makes a
descriptor servable (a non-empty ReportID unique in the provider, at least one
format each one of the three constants, parameter names non-empty and unique —
else the whole provider is listed report_provider_failed); the three media
types (application/json, text/csv; charset=utf-8 with any other CSV charset
refused, application/pdf), with Report.Format equal to the format asked for and
ContentType naming its media type, else 502; north.report_timeout (10m, at
least north.request_timeout, at most 1h) and north.max_concurrent_reports (2,
per node), a body failing after its first byte aborting the response, and
Body.Close possibly called while a Read is in progress; the error kinds
(Generate's ErrNotFound 404 not_found, its ErrInvalid 400 invalid_request with
its message, ErrUnavailable from either call 503 report_provider_unavailable,
every other kind including ErrDenied, a non-*ext.Error or a panic 502
report_provider_failed, over the ceiling 503 report_generation_busy with
Retry-After); server.CallerFrom answering inside Reports and Generate (tenant,
subject, principal, break-glass, correlation id) so a pack can record who
generated it, and Control recording nothing of a generation (its E6); its
skipped end-to-end criterion — a pack listed and generated through Control —
turned into a real one; (2) its own report routes: listing and on-demand
generation are Control's, and what stays its own is the history of generated
reports (audit:read), schedules and campaigns (report:write), in its host
namespace (M27) — its 0009's route list and its plan's §3 route table row for
"report reads" say which is which; (3) its plan's §4 ReportProvider row and the
bullet saying neither method is called, and its console phase if it planned a
screen listing packs (Control's console, 0047, lists them now); (4) its pin:
all of it holds from v0.31.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
