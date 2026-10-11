# control-PR#63 — self-service grant requests

Queued by [control#63](https://github.com/Hoplock/control/pull/63), Control's
phase 0016, which answers enterprise#10's `## Upstream request`: a requester's
own door into a registered approval workflow, the break-glass flag on the
request, and a poller that applies a decision in every tenant with a live proxy.
Two of the three shapes differ from the ask. It cuts `v0.3.0`. The obligations
are its `## Cross-repo impact` section's, for `hoplock/enterprise`; the routes,
the answers and `ext.GrantRequest.BreakGlass` are verbatim in Control's
`docs/learnings/0016-self-service-grant-requests-learnings.md` summary.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/63. Do not implement any queued prompt in this
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
(1) The requester's door: 0003's "What Control does not have yet" is
rewritten from what merged. The door is POST and GET
/api/v1/tenants/{tenant}/me/grant-requests, GET …/{request} and POST
…/{request}/cancel, all AccessSelf (the subject is the caller's, resolved
from the credential, never sent), all `grant:request`, held by the role
`requester` (and `admin`). The create takes the administrator's body without
`subject` and answers 202 / 201 / 403 grant_request_denied / 502
grant_workflow_failed; somebody else's request is 404; a machine token is 403
subject_required. With no workflow registered it is 403
grant_workflow_not_registered and nothing is stored — what E4 already
requires of an unlicensed binary. Enterprise builds no requester's door of its
own, and 0003's "list mine from its own record" is reconciled with GET
…/me/grant-requests so the same requests do not have two lists.
(2) Break-glass: `ext.GrantRequest.BreakGlass` exists (true when the
credential that raised the request was minted on the break-glass path; copied
from the stored request, so a resubmission keeps it). Any override E8 designs
keys on it, and 0003's acceptance criteria test it. A workflow tells the self
door from the administrator's by comparing Subject.ID with RequestedBy.ID,
never by another field.
(3) No sink: a decision is applied within one grants.workflow_poll_interval
in any tenant with a live proxy, across restarts. 0003 drops "put the restart
gap in your learnings", proves "approval grants access automatically" in a
tenant other than the configured one, and builds no push path; its E2E (its
0017) waits at most one poll interval for Control to apply an approval.
(4) Its plan: E8 where it says Control submits and polls "every create", and
§4 where it describes the GrantWorkflow seam, are revised to name the second
door and the break-glass flag.
(5) `license:read` is no longer "every role a person holds": `requester`
lacks it, so a requester-only principal gets 403 forbidden on Enterprise's
licence route; any Enterprise text saying "every role but integration" now
says "every role but integration and requester".
(6) A host route pattern beginning `me/grant-requests` now collides with
Control's and is refused at start-up.
(7) Its pin: every obligation above holds from Control v0.3.0, which control#63
cuts, never from main; Enterprise's pin moves to v0.3.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
