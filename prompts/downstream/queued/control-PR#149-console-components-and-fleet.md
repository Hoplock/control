# control-PR#149 — the console's component set and Fleet

Queued by [control#149](https://github.com/Hoplock/control/pull/149), Control's
phase 0050, released as `v0.37.0`: the component set every console screen is
built from, the browser checks that hold each screen to `ui/DESIGN.md`, and
Fleet. No shared surface changed; the obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/149. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.37.0 and never from main —
(1) its pin, and what its binary now does: Control's console serves Fleet at
/console/fleet and withdraws a proxy through POST …/proxies/{proxy}/withdraw
for a role holding fleet:write, and every console control that calls the API
is offered only when the session holds that route's permission in the
tenant; (2) the prompts that build its screens (approval inboxes, report
schedules and history, recertification) build them from Control's component
set at the pinned version — ui/src/components/, as Control's
docs/learnings/0050-console-components-and-fleet-learnings.md describes —
never from a visual kit or a copy: every status from STATUS (a status Control
lacks is an upstream request to add it to Control's ui/DESIGN.md §6 and
STATUS, never one of Enterprise's own), the four states through ScreenFrame,
lists through DataTable and Pagination over M26 answers, destructive actions
through ConfirmDialog, errors through ErrorState and ErrorMessage with
Enterprise's own codes under its own hoplock/enterprise:<key> catalogue keys;
and each of those prompts holds its screens to the same six browser checks
(Control's ui/DESIGN.md §10.3–§10.8: axe, a committed screenshot baseline,
no network at runtime, reduced motion, en-XA and en-XB) over a registry of
Enterprise's screens, against a seeded server, with RBAC asserted at the API.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
