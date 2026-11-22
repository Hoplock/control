# control-PR#161 — one look and feel across every console screen

Queued by [control#161](https://github.com/Hoplock/control/pull/161), Control's
phase 0060, released as `v0.47.0`. It audits every console screen for one look
and feel, fixes what disagreed at its source, and makes the rules checks. It
adds to the component set Enterprise's screens build from three components, a
toast kind and a badge tone; settles one word per concept in DESIGN.md §9;
and adds checks a host's screens are held to. `ext`, `server`, the contract,
the north-bound API version and the cross-repo protocol are untouched. The
obligations are its `## Cross-repo impact` section's, for
`hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/161. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.47.0 and never from main —
(1) its pin, and what its binary now does: no migration; north-bound API
still version 5; Control's console reads the same across its screens — a note
about a page is one Notice shape with its glyph, a destructive action's
success persists as a receipt toast, copying a timestamp copies ISO-8601 UTC,
break-glass is the warn badge everywhere, Fleet's algorithms view is compact;
(2) the component set Enterprise's screens build from gains, at this pin:
Notice (ui/src/components/Notice.tsx: tone info | warn | allow | deny, an
optional title, and a status pill in its glyph's place), Timestamp
(components/Timestamp.tsx: the one way to show an instant), TextButton
(components/Button.tsx: a link-styled button for an action inside the page,
never destructive), useToast().receipt (the success of an action confirmed
by typing a name; persists until dismissed), and Badge's warn tone. An
Enterprise screen uses these for those jobs and never draws its own <time>,
notice, control or overlay; a badge carries a hue only for supervised (info)
or break-glass (warn) (DESIGN.md §7, "One component per job");
(3) DESIGN.md §9's glossary — identity (never "user"; "subject" only as
code), person, principal, operator, target, proxy, node, deployment (never
"this server took / recorded / no longer holds"), server, denied, refused,
blocked — binds a host's catalogue too; §7's "Page anatomy" (the page header
on every screen, one primary action as the header's last, a row with a page
links from its first column and one without opens a drawer) and its toast
rule bind a host's screens; §3.7's density is by area;
(4) DESIGN.md §10.10: a host's screens are held to the same consistency
rules over their own registry — ui/test/consistency.test.ts's rules and the
console-e2e "widths" job (no sideways scroll at 1024 and 900px, 768 for an
incident screen; a wider table scrolls in its own container with its first
column pinned) — and `npm run contact-sheet` is the review aid for a PR that
changes how a screen looks. Prompt 0070 (queued here) moves Control's last
screen-local tables onto DataTable, which will gain a row's data attributes:
not yet released, so cite nothing from it. No citation of Control's numbers
moves; nothing was renumbered.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
