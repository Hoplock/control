# control-PR#56 — the public server package

Queued by [control#56](https://github.com/Hoplock/control/pull/56), Control's
phase 0015, which answers enterprise#11's `## Upstream request`: Enterprise's
binary can now start Control, keep its own configuration section, and mount its
own north-bound routes behind Control's middleware. It cuts `v0.2.0`. The
obligations are its `## Cross-repo impact` section's, for `hoplock/enterprise`;
the exported shapes are verbatim in Control's
`docs/learnings/0015-public-server-package-and-host-routes-learnings.md`
summary.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/56. Do not implement any queued prompt in this
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
(1) PLAN §3's gap paragraph ("Two attachments that wiring needs do not exist
in Control yet") is rewritten from what merged: Control's public `server`
package (`server.Main`, `server.Run`, `server.Options`, `server.Route`,
`server.Caller`, `server.CallerFrom`, `server.WriteError`; the signatures are
verbatim in Control's 0015 learnings summary). 0001 calls `server.Main(ctx,
os.Args[1:], os.Stdout, os.Stderr, server.Options{...})` and drops the
exit-at-the-missing-call seam; it owns its signals and exit code, and
dispatches any command of its own before calling `server.Main`, which gives
Enterprise's binary every Control subcommand (migrate, seed, audit-verify,
identity, ca).
(2) Its route table becomes `server.Options.Routes`: each route's Pattern is
relative to `/api/v1/tenants/{tenant}/` (no leading "/", no "{tenant}", no
"..", no method prefix; methods GET POST PUT PATCH DELETE), mounted as a
tenant route, and names a permission from Control's table — approval inbox
list `grant:read`, approve/reject `grant:approve`, archive search and report
reads `audit:read`, report schedules and campaigns `report:write`, licence and
entitlement state `license:read`, in-tenant governance `identity:read` /
`identity:write`, a policy change proposed and approved before activation
(0009) `policy:write` / `policy:publish`. 0002, 0003, 0005, 0009 and 0013 drop
"unreachable until Control can mount it"; 0002's two deferred E4 proofs
become runnable. A handler reads its caller only through `server.CallerFrom`
(tenant resolved by Control, Subject ID+Groups, zero for a token, Username and
ExternalID never filled, principal id, break-glass flag, correlation id).
(3) Its config section: `enterprise:` is declared in `Options.HostSections`
and its PLAN §5 layering is decoded in `Options.HostConfig`, which Control
calls with the section re-encoded as YAML before the registry is sealed (so
an extension built from the section can still be registered) and only from
the daemon; every subcommand accepts the section.
(4) Its errors: every Enterprise route answers a failure through
`server.WriteError` with codes declared in `Options.ErrorCodes` (snake_case,
none of Control's), and never a 401; an undeclared code, a 401 or the code
`unauthenticated`, `internal` with a non-5xx status, a status outside
400–599, an empty message, or a call outside a host route renders
`500 internal` and is logged.
(5) What it did not get: no cross-tenant host route, so 0013's E11 reporting
is still blocked and is Enterprise's next upstream request (a cross-tenant
access class); no host-declared permissions or roles; no host subcommands; no
write into Control's audit chain; the self-service "list mine" view is
Control's 0016, not a host route.
(6) Its pin: every obligation above holds from Control `v0.2.0`, the release
control#56 cuts (M23), never from `main`; Enterprise's pin moves to
`v0.2.0`. Start-up refuses a host that can never start, before any file is
read or port bound — the full list is in Control's 0015 learnings summary.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
