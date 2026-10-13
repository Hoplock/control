# control-PR#67 — the cross-tenant access class

Queued by [control#67](https://github.com/Hoplock/control/pull/67), Control's
phase 0018, which answers enterprise#19's `## Upstream request`: one
north-bound route class that spans tenants, for a host's reads. The mechanism
is met as asked: the middleware resolves the set and the handler is handed it.
Four things differ from the sketch. The class is `AccessCrossTenant`, it is
mounted at `/api/v1/cross-tenant/`, it is reads only, and a named tenant that
fails is refused rather than dropped. Decision M24 records it. The PR cuts
`v0.5.0`. The obligations are its `## Cross-repo impact` section's, for
`hoplock/enterprise`. The identifiers, the answers and the permission rule are
verbatim in Control's
`docs/learnings/0018-cross-tenant-access-class-learnings.md` summary.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/67. Do not implement any queued prompt in this
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
(1) Its 0013 can mount the report. Rewrite its "Cross-tenant reporting cannot
be mounted" paragraph from what merged:
- The report is a server.Route with Access: server.AccessCrossTenant, under
  audit:read, served at /api/v1/cross-tenant/<Pattern>.
- Its handler reads server.Caller.Tenants and nothing else. Caller.Tenant is
  empty on this class. Its response names the tenants it covers.
- A selector (?tenant=, repeatable, or X-Hoplock-Tenant) narrows the set and
  never widens it. A named tenant the caller cannot read is refused, never
  dropped: out of scope is 403 tenant_out_of_scope {tenant}, and in scope
  without the permission is 403 forbidden {tenant, permission}.
- With no selector, the set is every tenant where the caller holds the
  permission. If there is none, the answer is 403 forbidden {permission}, and
  the handler never runs.
- Its seam beside the route table goes, and "no instance operator can reach
  it" stops being true.
- The prohibition on approximating the report with a tenant route stays.
(2) Who can reach it. Only a token minted on the box with a scope naming
several tenants (hoplock-control identity token-issue --scope
'a=auditor;b=auditor'). A session, federated or break-glass, is scoped to the
one tenant it signed in through, so a signed-in person gets that tenant only.
If E11 needs a person to hold scope across tenants through a sign-in, that is
a request of its own to Control. Its 0016 console labels the cross-tenant
report with what a session can reach, and does not promise an aggregate that
a session cannot reach.
(3) Reads only. A cross-tenant route names a permission Control's auditor
role holds. A cross-tenant write, such as a report schedule spanning tenants
under report:write, is not offered, and it is refused at start-up. Where its
PLAN §3 "What a host does not get" says "a cross-tenant route", it now says
"a cross-tenant write".
(4) The class names. The class is server.AccessCrossTenant, not
AccessTenants: the two would differ by one letter on the line the isolation
property lives on. server.AccessTenant is the zero value, so its existing
routes are unchanged.
(5) Its pin. Every obligation above holds from v0.5.0, the release control#67
cuts, never from main. Enterprise's pin moves to v0.5.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
