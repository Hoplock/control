# control-PR#169 — one contract version

Queued by [control#169](https://github.com/Hoplock/control/pull/169), Control's
phase 0064, released as `v0.51.0`. Control now serves exactly one policy
vocabulary south-bound, the vendored contract's, and refuses every other with a
`5xx`; a proxy of the wrong build is named in the fleet view, the fleet's
health and the publish-time report, for which the north-bound API moves to
version 7. `ext`'s interfaces, `server` and the contract are untouched, and
there is no migration. The obligations are its `## Cross-repo impact`
section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/169. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.51.0 and never from main —
(1) the north-bound API is at version 7 (north_api_min still 1): a client
declaring 1 to 6 that reads GET …/proxies or GET …/instance/health while any
proxy declares a policy vocabulary Control does not serve is answered
500 api_version_unexpressible, never a body without the fact, so any
Enterprise supervisor or host code, prompt or plan text that pins 6 moves to 7
or handles that answer; (2) new closed-set values a supervisor or host may
meet, each needing a rendering wherever Enterprise renders Control's reasons,
members or findings: the health reason proxies_contract_unsupported (tenant,
count; the tenant's fleet degraded), contract_version_unsupported on a proxy in
GET …/proxies and in a fleet summary, and Control's publish-time finding
satisfiability.contract_version_unsupported (a warning naming each proxy, so it
gates activation until accepted by its id); (3) south-bound, Control serves
exactly one policy_version and refuses any other, lower or higher, with
500 policy_version_unsupported naming the declared number, the served one and
the proxy_id — any Enterprise text saying Control answers an older proxy route
by route is stale, and proxy and Control builds deploy together; (4) the pin:
no migration, ext and server untouched, the contract unchanged at 4.7.0, and a
proxy's contract_version on the fleet view is now what it last declared on
/v1/authorize.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
