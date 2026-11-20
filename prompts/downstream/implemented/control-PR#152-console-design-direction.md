# control-PR#152 — the console's look, chosen with the operator

Queued by [control#152](https://github.com/Hoplock/control/pull/152), Control's
phase 0052, released as `v0.39.0`. It gives the console a new look, "Carbon":
near-black and dark first, with a cyan accent and denser type, changed through
the token file alone and with dark as the default theme. It also renumbers
Control's queued prompts. No shared surface changed: `ext`, `server`, the
contract and the cross-repo protocol are untouched. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/152. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.39.0 and never from main —
(1) its pin, and what its binary now does: no migration, north-bound API
version unchanged (3); Control's console has a new look, Carbon, and opens
dark for an operator who has never chosen a theme (light, or following the
system, is a choice in the account menu, and a choice already made is kept);
(2) Enterprise's screens take the console's tokens from :root of the one page
(Control's 0049), so they change look at this pin with no code of theirs —
which holds only while a host's screen references tokens alone and never a
colour, size or radius of its own: every prompt that builds or changes an
Enterprise screen says so, and the values a reviewer will now see are
--a-500 (the accent) #0b7189 light / #22d3ee dark, --bg-canvas #f4f6f8 /
#07090c, --bg-surface #ffffff / #13181f, type a step denser (--t-body 13/20,
--t-dense 12/18, --t-label 11/16 at 600), radii unchanged (4/6/10), and the
five status hues retuned within their meanings (deny and outage still
different colours, each with its own glyph) — Control's ui/DESIGN.md §3 at
v0.39.0 holds every value; (3) a host's browser checks choose their theme
explicitly: with dark the default, a check that sets only the browser's
colour scheme sees dark in a "light" run unless it sets the console's theme
preference to follow the system first, as Control's harness now does
(e2e/harness.ts useDensity, which every openScreen calls), so Enterprise's
screenshot baselines are retaken at this pin in both themes; (4) the default
deployment stripe follows the accent (cyan), and `teal` is the configurable
stripe nearest it, so any Enterprise text recommending a stripe for
production names magenta, orange or slate; (5) Control's queued prompts were
renumbered by the fleet-bar revision: a new 0059 replaces Fleet's health
bar's patterns with solid segments labelled with their status glyph and
count (the operator's ask in 0052), and 0059→0060, 0060→0061, 0061→0062,
0062→0063, 0063→0064 — the console is now 0049–0060, its consistency audit
0060, the E2E topology 0061, CA custody 0062, post-quantum posture 0063 and
one contract version 0064. Update every citation of those numbers in this
repository's prompts and plan, resolving each through Control's PLAN §10
table (an older request, control-PR#150, cited the numbers before this
revision; resolve those through the same table).

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
