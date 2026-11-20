# control-PR#153 — the console's Policy screens

Queued by [control#153](https://github.com/Hoplock/control/pull/153), Control's
phase 0053, released as `v0.40.0`. It adds Policy to Control's console —
versions, a version's source, changes, publish-time report and replay, a
candidate editor, and activation and rollback through one typed-name dialog —
and grows the component set Enterprise's screens are built from. No shared
surface changed: `ext`, `server`, the contract and the cross-repo protocol are
untouched. The obligations are its `## Cross-repo impact` section's, for
`hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/153. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.40.0 and never from main —
(1) its pin, and what its binary now does: no migration, north-bound API
version unchanged (3); Control's console has Policy at /console/policy (the
stored versions), /console/policy/versions/<n>[?tab=changes|report|simulate]
[&against=<n>] (one version) and /console/policy/candidate[?from=<n>] (a
source not stored yet), reading and validating, comparing and replaying under
policy:read, uploading under policy:write, activating and rolling back under
policy:publish, each offered only to a role holding it; an Enterprise screen
that links to Control's Policy names these paths under /console/ and never
builds its own; (2) a registered ext.PolicyValidator's finding renders in
Control's console by its code, its provider, its parameters (shown as JSON)
and its id — never its English message — and is set beneath the line it names
when it carries Rule and Line; a warning is offered for acceptance by its id in
the activation dialog, a blocking finding reads "Blocking" (deny-class, never
an outage). So every prompt that adds or changes an Enterprise validator says:
a finding about a rule carries its Rule and Line, its parameters carry
everything a reader needs (names and counts, not prose), its id is stable
(code and what it is about), and its severity is one of the three; (3) the
component set Enterprise's screens build from (Control's 0050) gains, at this
pin: DiffView, DiffSkeleton and parseUnified (ui/src/components/Diff.tsx, a
unified diff the server wrote, never diffed in the browser), SourceView
(Code.tsx, numbered lines with notes beneath them), ButtonLink (Button.tsx), a
mono Textarea, ConfirmDialog's hint, a Breadcrumb crumb that may be a node,
the statuses valid, invalid, blocking, warning, advice and superseded
(DESIGN.md §6), and the tokens --diff-add-bg and --diff-remove-bg (DESIGN.md
§3.8: a diff's sides are never status hues); ApiClient's ok answer carries the
correlation id; every prompt that builds an Enterprise screen showing a diff,
a source or a finding uses these and adds no look of its own; (4) a host's
browser checks may force a child scrolling region with data-force-child (a
button before), as Control's harness now does; (5) Control queued two new
phases at the end of its queue, renumbering nothing: 0065 (policy refusals as
codes) and 0066 (a visual policy editor, a second view of the candidate that
changes no schema, route or storage). No citation of Control's numbers moves.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
