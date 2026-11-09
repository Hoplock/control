# 0059 — Management console 11: one look and feel across every screen

## Read first
- `docs/PROTOCOL.md`: the session workflow, §3 (doing the work, and scope
  discipline), §4 (the Definition of Done) and §5 (the learnings file).
- `docs/PLAN.md` — §2: **M20** (the console is a product surface with a
  specified design, and one identity), **M21** (every string from the
  catalogue, every identifier untranslated), **M11** (a deny and an outage are
  different colours and different words), **M17** (`Not reported`), **M19**
  (in progress, never a fault; the deployment's identity in the chrome),
  **M18**, **M27** (every URL under `/console/`), **M2** and **M23**; §3
  (`ui/`).
- **`ui/DESIGN.md` — in full.** It is the standard every screen is audited
  against, and a claim to verify against the console as much as a source.
- `docs/learnings/` — read every summary. Open the learnings of the ten
  console phases **in full** — `0049-console-shell`,
  `0050-console-components-and-fleet`, `0051-console-explain-and-audit`,
  `0052-console-design-direction` (the look the operator chose, and the
  rounds that chose it: audit against that look, never re-open it),
  `0053-console-policy`, `0054-console-inventory`,
  `0055-console-standing-denials-and-directory`,
  `0056-console-extensions-and-reports`,
  `0057-console-audit-gaps-and-retention` and
  `0058-console-algorithm-posture-and-runbook` — and in each, the record of
  every change it made to `ui/DESIGN.md`. They are the claim set this phase
  checks, not a source of truth.

## Why this phase exists
The console was built in ten phases (PLAN §10, the console-split and
console-look notes), each in a session of its own that saw only its own screens. Each built to
`ui/DESIGN.md` and to 0050's component set, and each passed the checks DESIGN.md
§10 makes CI jobs.

Those checks look at one screen at a time. They catch a raw colour, a contrast
failure, an axe violation, a hardcoded string, a clipped label and a changed
pixel. None of them catches two screens that each pass and disagree: two
empty states phrased in two shapes; one screen's primary action at the start of
its header and another's at the end; a status the status component renders on
one screen and a screen-local pill renders on the next; "subject" on one
screen and "identity" on another for the same thing; a confirmation that asks
for the object's name in one place and for a click in another. A screenshot
baseline proves a screen did not change. It does not prove it matches its
siblings.

An operator does not use ten phases. They use one console, and M20 exists so
that it has one identity. This phase looks across the whole of it, once, after
the last screen lands and before anything is built on top of it — the E2E
topology (0060) signs a person in to it, and Enterprise's screens inherit it.

## Objective
1. **Audit** every screen against `ui/DESIGN.md` and against every other
   screen, and give each item below a verdict.
2. **Fix** every inconsistency at its source — the component, the token, the
   catalogue, the shell — never by adjusting one screen to look like another.
3. **Hold it.** Make every consistency rule that can be held mechanically a
   check that fails on drift, so the console stays consistent after this
   phase, and a host's screens are held to the same rules.

## How to find the work
- 0050's screen registry lists every screen and its states; the component set,
  `tokens.css` and `ui/src/locales/en.json` are where 0049 and 0050 put them.
- Find each console phase's changes with
  `git log --oneline -- ui/` and each phase's merge commit; audit the console
  **as it is now**, and use the history only to find who introduced what.
- **The contact sheet.** Add a step that renders every registered screen side
  by side — both themes, both densities, and the loading, empty, error and
  partial states — from the screenshot set, so the console can be reviewed
  whole. Commit the step, and attach its output to the PR. It is the evidence
  for every item judged by eye.

## What to check
Give each numbered item a verdict: **confirmed** (with evidence), **wrong →
fixed** (with the commit), or **open** (with the reason). Evidence is a file and
line, a test name, a contact-sheet image, or command output. A sentence in a
doc is never evidence.

1. **One component per job.** Inventory every component under `ui/src`. A
   screen-local element that does the job of one in the set — a table, a
   status pill, a badge, a button, a confirmation, an empty or error panel, a
   copyable id, a timestamp, a lag readout — is a finding: replace it with the
   set's. So is a screen-local style that restyles a component of the set.
2. **The status vocabulary.** Build the table: every state every screen
   renders, the component that renders it, its colour, its glyph and its word.
   Each must be a row of DESIGN.md §6, rendered by the one status component
   (M11, M17, M19). The same state carries the same word on every screen. The
   states later phases added — a lock, a quarantine and a deactivation (0055),
   a refused gap (0057), a failing sink and an extension that did not start
   (0056), a build that is not judged (0058) — are in §6, and nowhere else.
3. **Page anatomy.** Every screen's page header (title, id, status, actions;
   DESIGN.md §7) carries the same parts in the same order and place. One
   primary action per view, in the same position. A list opens its detail the
   same way everywhere (drawer or page, by one rule), and tabs, breadcrumbs
   and section spacing follow one pattern.
4. **The four states.** For each screen: the loading skeleton matches the
   final layout and nothing shifts when data arrives (measure it); the empty
   state says what would be here, why it is not, and the one action, in the
   same shape on every screen; the error state says *outage*, gives the
   correlation id and a retry, and is the same component everywhere; the
   partial state names its gap. Compare their copy across screens.
5. **Lists and filters.** Every list goes through the data table and M26's
   paging in the same way: the same words for a cut answer, the same control
   for the next page, the same sort indicators and column controls, the same
   filter bar, its state serialised under `/console/` with one naming scheme
   for parameters, and the first column pinned. Densities default as DESIGN.md
   §3.7 says: compact on Fleet and Audit, comfortable elsewhere.
6. **Mutations and destructive actions.** Enumerate every destructive action
   in the console — activating a bundle and rolling back, revoking a grant,
   lock, unlock, quarantine and release, a retention pass, each runbook step —
   and confirm each uses the one typed-name confirmation. Every failed
   mutation reports inline, never by a toast alone; toasts are only for
   successful, reversible things. Every text the console sends to be shown to
   someone else (a lock's holder text, a kill's `reason`) is shown before
   sending in the same pattern.
7. **Identifiers, time and numbers.** Every id renders through the copyable-id
   component, LTR and in mono; every timestamp through 0049's locale helpers,
   in the operator's zone with UTC on hover and ISO-8601 UTC when copied; every
   number in a column in tabular figures; relative time by one rule.
8. **Words.** One term per concept across `en.json` — subject, identity or
   user; target or host; proxy or node; denied, refused or blocked — each used
   for exactly one thing. Sentence case throughout, one key-naming scheme, no
   two keys for one sentence and no key reused for two (DESIGN.md §11), and
   the unknown-code fallback (0050) the same wherever an error renders. Record
   the settled terms as a short glossary in DESIGN.md §9, so a host's screens
   use the same words.
9. **Navigation and keyboard.** Navigation labels and order; the command
   palette reaches every screen and every primary action in the same way;
   shortcuts are consistent and shown with the keyboard-hint component; tab
   order matches the visual order; focus is restored after every dialog and
   drawer; the skip link works on every screen.
10. **Links between screens.** Where two screens are related, the link exists
    in both directions where the relation does, uses the same affordance, and
    is under `/console/`: audit and Explain, inventory and the audit view
    filtered by subject or target, the supervised badge and the supervisor's
    actions, Policy and the runbook, Fleet and a proxy's stream in the audit
    view. A deep link a later phase renamed is a finding (DESIGN.md §4).
11. **Absent and unavailable.** Every place an extension point can be absent
    reads the same way — what Control does instead, never broken (M15) — and
    every `Not reported` reads the same.
12. **Width.** Every screen at 1024px and in the collapse below it, Explain
    and Audit at 768px, no horizontal page scroll anywhere, and every wide
    table scrolling in its own container with the first column pinned
    (DESIGN.md §4). Add these widths to the screenshot set if they are not in
    it.
13. **By eye, from the contact sheet.** What no check catches: spacing rhythm
    and alignment, the type scale used as §3.5 says (at most one display per
    page, the page title at `--t-h1`), elevation only on overlays (§3.6), the
    accent only on the primary action, the active navigation item and
    selection (§3.3), status hues never used as decoration (§3.4). Record each
    finding with its image.
14. **DESIGN.md and `ui/` agree.** Every rule in DESIGN.md is true of the
    console; §7's inventory is what the set ships; §3's tables are what
    `tokens.css` holds; every change 0049–0058 made to DESIGN.md is recorded
    in their learnings and still holds; and every claim those learnings make
    about the look and feel is true.

### Hold it
For every rule above that can be held mechanically, add a check that fails on
the drift, and prove it is not vacuous: introduce the drift, watch the check
fail, revert, and record the mutation in the learnings. Candidates, each to be
ruled on rather than assumed: a lint that refuses a status rendered outside the
status component; a test that every registered screen renders the page header
and has all four states captured; a test that every destructive call goes
through the confirmation; a catalogue lint for the glossary's terms; the
widths of item 12 in the baseline. A rule that cannot be held mechanically is
written into DESIGN.md where it belongs, so a reviewer can point at it.

## Out of scope
- New screens, new features and new API calls. A functional defect in the
  console found here is fixed under a test that failed first; a defect in the
  API, or a gap that needs more than a local fix, is written up with a proposal
  in this phase's PR and queued as a prompt (`docs/PROTOCOL.md` §3, §6). Never
  widen silently.
- Re-opening the design. DESIGN.md's palette, scale and constraints are
  settled. Correct it where it is wrong or silent — in this PR, with the
  reason — but do not redesign it.
- Enterprise screens, and any locale other than English.

## Acceptance criteria
- Every item above has a verdict with evidence in this phase's learnings, and
  item 2's state table and item 8's glossary are in them in full.
- Every inconsistency found is fixed at its source, or open with its reason.
  Every screen whose look changed has its baseline updated deliberately, and
  the PR names each screen and why.
- Every check this phase adds fails on a recorded mutation.
- Every DESIGN.md §10 check, 0049's static checks, 0050's RBAC harness and
  route test, and 0038's namespace tests pass.
- `go build ./...`, `go vet ./...` and `go test ./...` pass, and
  `golangci-lint` at CI's pin reports 0 issues.
- `ui/DESIGN.md` says what is true of the console, and the contact sheet is
  attached to the PR.

## Definition of Done & hand-off
**Screenshots in the PR** (`docs/PROTOCOL.md` §8, "A console phase shows what
it built"): the description shows every screen and every state this phase
builds or changes, light and dark side by side. For each inconsistency fixed,
the screen before and after.

Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0059-console-consistency-audit-learnings.md`.

Its summary block MUST give:
- the findings, by verdict and severity;
- each inconsistency fixed, by commit;
- each check added, and the mutation that proved it;
- each change to `ui/DESIGN.md`, with the reason;
- what is left open, and why;
- what a host's screens must now follow that they did not before.

Its Details carry the full verdict table, the state table and the glossary.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names. Its `CHANGELOG.md` section lists what an
operator can observe: what changed on which screen.

**Cross-repo impact.** Enterprise's screens inherit these tokens, this
component set and this catalogue's conventions (M20, M27). If this phase
changes what a host's screens must follow — a component's contract, the
glossary, a new check a host's screens are held to — state it under
`## Cross-repo impact` and queue the downstream sync
(`docs/CROSS-REPO-PROTOCOL.md` §4.1, §4.3). Otherwise write **"None"** for each
repository.
