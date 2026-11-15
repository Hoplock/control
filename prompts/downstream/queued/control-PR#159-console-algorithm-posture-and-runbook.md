# control-PR#159 — the console's algorithm posture and the emergency runbook

Queued by [control#159](https://github.com/Hoplock/control/pull/159), Control's
phase 0058, released as `v0.45.0`. It adds Fleet's algorithms view, the floor's
impact preview and a ban's per-build warnings inside the publish-time report,
and the advisory runbook to Control's console; four statuses to the set
Enterprise's screens are built from; and, in DESIGN.md §7, the shape of a
workflow and of a finding's evidence. `ext`, `server`, the contract, the
north-bound API version and the cross-repo protocol are untouched. The
obligations are its `## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/159. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.45.0 and never from main —
(1) its pin, and what its binary now does: no migration; north-bound API
still version 5; Control's console serves Fleet's algorithms view at
/console/fleet/algorithms (fleet:read) — each algorithm_floor level with the
key exchanges it accepts per build, what each profile offers per axis, the
proxies that declared nothing, disagreeing builds In progress and a build that
said nothing Not reported — and the advisory runbook at
/console/policy/runbook[?axis=&name=&limit=&range=|from=&to=] (the ban applied
with policy:publish, cache_invalidate with all and session_kill with
session:revoke, the negotiated sessions read with audit:read), and sets out the
floor's impact preview and a ban's per-build warnings inside the publish-time
report's finding cards, each warning and never blocking. An Enterprise screen
links to those paths under /console/ and never builds its own view of
algorithm posture or its own runbook;
(2) the component set Enterprise's screens build from gains, at this pin, the
statuses Declared and Done (allow) and Not declared and Not judged (not
reported) in DESIGN.md §6; an Enterprise screen showing a build's declaration
or a workflow's step renders through these and never invents its own;
list_unterminated is a sentence for the console's own outage (a list read to
its end that named no new position), not a server code;
(3) DESIGN.md §7 now states the shape of a workflow — ordered steps, each
offered only once the one before it answered and only to a role holding its
permission, each destructive call through ConfirmDialog, each answer kept as a
receipt — and that a finding's evidence lives inside its card; an Enterprise
workflow or finding follows them, and a registered validator's finding still
renders by the fallback, unchanged. No citation of Control's numbers moves;
nothing was renumbered.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
