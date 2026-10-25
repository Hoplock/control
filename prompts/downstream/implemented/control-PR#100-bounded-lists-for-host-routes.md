# control-PR#100 — bounded lists for a host's routes

Queued by [control#100](https://github.com/Hoplock/control/pull/100), Control's
phase 0030. It adds `server.ReadList`, `(server.List).Write`,
`server.DefaultListLimit` and `server.MaxListLimit`, so a host's list answers
in decision M26's shape through the code Control's own lists use. It cuts
`v0.17.0`, a MINOR with no `### Breaking`. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/100. Do not implement any queued prompt in this
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
(1) From Control v0.17.0, every list among its host routes (approval inbox,
archive search, report schedules, and any other) answers through
server.ReadList(w, r, name, keyLen, filters...) and
(server.List).Write(w, r, member, items, more, last, extra): ?limit= 1 to
server.MaxListLimit (10 000), default server.DefaultListLimit (500); ?after=
a cursor Control mints; the answer {<member>: [...], "truncated": bool,
"next": "<cursor>"}, next present exactly when truncated. Refusals are
Control's 400 invalid_request {field: limit|after, problem: ...}. Each prompt
that builds or reshapes such a list names these identifiers and the release.
(2) The cursor binds the list name (Control prefixes the provider), the
filters the host passes as "name=value", and the order key, one string per
component, which the host parses from List.After. It never carries the
tenant, nor on a cross-tenant route the set: read those from
server.CallerFrom on every page. Every query parameter that changes what a
list holds or its order belongs in filters. The host's plan should state
M26's shape for its lists.
(3) Control cannot see which host route is a list, so nothing in Control
enforces the shape. Enterprise's own route tests should assert it for every
list route (an enumerating test over its own routes is Enterprise's to
write). Misusing the façade (outside a host route, a member named truncated
or next, a colliding extra key, more with no last or a last of the wrong
length, items that are not a JSON array) answers 500 internal and logs
northbound_host_list_refused.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
