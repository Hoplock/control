# control-PR#111 — host routes and the console in namespaces of their own

Queued by [control#111](https://github.com/Hoplock/control/pull/111), Control's
phase 0038 (decision M27, release `v0.25.0`). The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/111. Do not implement any queued prompt in this
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
(1) Every host route moved (Control v0.25.0, M27). A tenant route is served
at /api/v1/hosts/<Provider>/tenants/{tenant}/<Pattern>, where it was
/api/v1/tenants/{tenant}/<Pattern>; a cross-tenant read at
/api/v1/hosts/<Provider>/cross-tenant/<Pattern>, where it was
/api/v1/cross-tenant/<Pattern>. Pattern is unchanged and relative. Rewrite
this repository's docs/PLAN.md §3 "One route table, mounted by Control" from
what merged (its "These paths will move" paragraph goes), its 0001's route
table, and every prompt that writes either old path (at main 5455bf4: 0001,
0002, 0003, 0008, 0013, 0015, 0016), naming v0.25.0 as the release that
moves them and saying the old paths hold while the pin is older.
(2) Options.Provider is <owner>/<name>, each lower-case letters, digits and
hyphens: hoplock/enterprise is; a module path such as
github.com/hoplock/enterprise is refused at start-up. docs/PLAN.md §3 says
Provider "names this module": say which string.
(3) "Start-up's refusal is the backstop, not the rule" can drop its rule once
the pin is v0.25.0: no route of Control's can match a path under
/api/v1/hosts/, and Control's router refuses one that could, so a pattern
under one of Control's families (audit/archive, policy/changes) can no
longer be refused on a later pin bump or lose its path. Say so, or say why
it stays. Start-up also newly refuses a pattern with a "." segment or an
empty segment before its end, and no longer refuses one equal to Control's
(it is the host's own path now).
(4) This repository's console screens (its 0016) are served from
/host-console/hoplock/enterprise/, loaded by Control's shell from there;
every URL they write stays under that prefix, and their catalogue keys are
hoplock/enterprise:<key> (one equal to Control's is refused). No
server.Options field hands screens to Control yet — Control's queued 0044 or
a later phase adds one — so do not describe it as available (§2).

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
