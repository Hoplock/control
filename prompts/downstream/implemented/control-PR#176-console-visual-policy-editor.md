# control-PR#176 — the console's visual policy editor

Queued by [control#176](https://github.com/Hoplock/control/pull/176), Control's
phase 0066, released as `v0.53.0`. It adds a visual view of the policy
candidate to Control's console, and adds to what Enterprise's screens build
from: two components, a view link's query, and the one YAML document library.
`ext`, `server`, the contract, the north-bound API version and the cross-repo
protocol are untouched. The obligations are its `## Cross-repo impact`
section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/176. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.53.0 and never from main —
(1) its pin, and what its binary now serves: Control's console shows a policy
candidate visually at /console/policy/candidate?view=visual ("Edit visually"
from a stored version; &rule=<id> opens a rule's editor) — the rules as cards
in evaluation order with the default deny last, reordered by drag and by
keyboard, every enumerated field a choice from the server's vocabulary, and a
dry-run trace of one request. No migration; north-bound API unchanged at 8;
(2) the component set Enterprise's screens build from gains, at this pin,
SortableList (with SortableListSkeleton) for an ordered list whose order is
its content, ChipEditor for a list of values bound to a closed set or to
checked typed text, and ViewLink.query for two views of one object. An
Enterprise screen with such a list or such values uses these, never a table,
a drag or a chip input of its own;
(3) ui/DESIGN.md §7 now names the one exception to "a list is the data table"
(the sortable list) and a view link's query, and §8 requires every drag to
have a keyboard path that does the same and announces it; a host's screens
are held to both;
(4) §11 names the console's one YAML document library, `yaml` 2.9.1, pinned:
an Enterprise screen that edits YAML uses it, never a second, and edits a
source as the smallest change to its text rather than re-serialising it.
No citation of Control's numbers moves; nothing was renumbered.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
