# control-PR#160 — Fleet's health bar without patterns

Queued by [control#160](https://github.com/Hoplock/control/pull/160), Control's
phase 0059, released as `v0.46.0`. It replaces the patterns in Fleet's health
bar with solid segments, each labelled with its status glyph and count, and
adds to what Enterprise's screens build from: one semantic colour token, three
layout tokens, and an `ink` form of `StatusGlyph`. `ext`, `server`, the
contract, the north-bound API version and the cross-repo protocol are
untouched. The obligations are its `## Cross-repo impact` section's, for
`hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/160. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.46.0 and never from main —
(1) its pin, and what its binary now does: no migration; north-bound API
still version 5; Control's console draws Fleet's health bar at /console/fleet
as solid segments in their status hues with a gap between two, each wide
enough one labelled with its status glyph and count, a narrower one drawn at a
minimum width and named by the legend, and no pattern anywhere. An Enterprise
screen links to Fleet and never draws its own fleet health bar;
(2) the component set and tokens Enterprise's screens build from gain, at this
pin: --text-on-status (DESIGN.md §3.2: white in light, near-black in dark,
≥ 4.5:1 on every status hue in both themes), the layout tokens
--chart-segment-gap, --chart-segment-min and --chart-label-min (§3.6), and
StatusGlyph's `ink` prop, which draws a glyph in the colour around it rather
than its hue. An Enterprise screen that sets a status glyph or text on a
status hue uses these, never an ink or a size of its own;
(3) DESIGN.md §8 now says how each of the two charts reads in greyscale (the
health bar by a glyph and a count on every segment wide enough, and its
legend; the audit view's counts by one neutral ink), and §11 describes the
health bar as built; a chart on an Enterprise screen is held to the same §8
promise. No citation of Control's numbers moves; nothing was renumbered.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
