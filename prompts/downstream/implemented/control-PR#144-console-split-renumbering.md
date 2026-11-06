# control-PR#144 — the console-split renumbering

Queued after [control#144](https://github.com/Hoplock/control/pull/144)
merged. It split Control's management console, phase 0049, into nine phases
(0049–0057), added a tenth (0058) that audits every screen for one look and
feel, and renumbered Control's queued phases after them. control#144 merged
without a `## Cross-repo impact` section and without this file, so the
obligations below are the whole of them, for `hoplock/enterprise`. The
citations they name were read from enterprise `main` at `be657f4`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/144. Do not implement any queued prompt in this
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
(1) Control's queued phases moved (revision console-split): the management
console is now ten phases, 0049–0058; E2E topology is 0059, CA custody
through the key store 0060, post-quantum posture 0061 and one contract
version 0062. On main when this was queued, this repository cited them as
control#138 numbered them: the console as 0049 in docs/PLAN.md §3 (the bullet
on where a host's screens live, at "No server.Options field hands screens to
Control yet") and in its 0007 (the reversibility bullet), 0008 ("Managing
bindings") and 0016 (its Read first, at the M26, v0.32.0 and v0.31.0
bullets; in-scope item 3, Compliance; and "How the screens reach Control is
not built yet"); E2E as 0050 in its 0017's Read first; and CA custody as
0051 in docs/PLAN.md §4 (the certificate authority bullet) and in its 0010
(Read first, and "The CA is not on it yet") and 0017 (Read first) — and any
others a sync since added. Cite each by name beside the number Control's
docs/PLAN.md §10 mapping table gives when you run. Hits that only recount a
number's history ("this file said 0047, 0044 before that") are history and
stay. Citations of merged Control phases do not move.
(2) A citation of Control's console now names the series 0049–0058 and, where
it relies on one item, the phase that builds that item:
- the shell, the mount under /console/, the catalogue machinery, and the
  learnings that say how a host adds screens and catalogue keys and what the
  server.Options field for a host's screens must carry (its patterns, its
  catalogue keys and one handler): 0049, the console shell;
- the tokens and the component set a host's screens inherit, and the browser
  checks every screen is held to: 0049 and 0050;
- the Reports screen that lists and generates every registered pack from
  GET …/reports and POST …/reports/generate, naming a report through its
  host's <provider>: keys and by its code where no catalogue names it: 0055,
  whose learnings say how a host's report names reach the catalogue (0016's
  item 3 cites "the key 0049's learnings say it reads": it is 0055's);
- releasing a lockout, a lock and a quarantine shown and lifted: 0054;
- the deployment's identity and health in the chrome (from 0046): 0049;
- a console surface for access-context bindings: no console phase builds
  one, so 0008's "if any" stays true and it cites the series;
- the consistency audit, 0058: if it changes what a host's screens must
  follow (a component's contract, the catalogue's glossary, a new check), it
  owes a sync of its own when it merges.
Nothing the console's prompt told this repository changed: the series still
adds no server.Options field for a host's screens, every Control list it
renders is paged (M26, after 0029–0037), and it runs after 0045 and 0046.
Do not describe any of it as existing (§2): no console phase is merged, and
nothing is served at /console/ until a release carrying 0049 is the pin.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
