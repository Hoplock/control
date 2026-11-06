# 0052 — Management console 4: Policy

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — §2: **M3** (policy is data compiled into a decision
  program), **M4**, **M11**, **M20**, **M21** (errors arrive as a code plus
  parameters, and the console builds the sentence), **M26** and **M23**; §5.6
  (the policy lifecycle: versions, validation, simulation, activation,
  rollback, the publish-time report and its severity).
- **`ui/DESIGN.md` — in full.** §2 and §7 on destructive actions: activating a
  bundle confirms by typing the object's name.
- `docs/learnings/` — read summaries; open `0049-console-shell` and
  `0050-console-components-and-fleet` **in full**: the shell, the component
  set, and how a screen is registered with every harness. Open `0005` (the
  compiler's rejections: a code, a rule, a line and parameters), `0019` (the
  authoring routes and each one's required role), `0022` (the publish-time
  report, its severity model, finding ids and accept-by-id, upload,
  activation, rollback, diff and dry-run, as audited) and `0035`
  (`GET …/policy/bundles` paged newest first with no source; a finding names
  at most 100 targets or proxies beside `<name>_count` and `<name>_truncated`).

## The series
The console is ten phases (PLAN §10, the console-split note), each a release
and a PR of its own:

| Phase | Builds |
| --- | --- |
| 0049 | the shell: build and embed, the mount under `/console/`, sign-in, tokens and themes, the catalogue machinery, the deployment and tenant chrome, and the static checks |
| 0050 | the component set, the browser checks, the seeded fixture and the RBAC harness, and Fleet |
| 0051 | Explain and the audit view |
| **0052** | Policy (this phase) |
| 0053 | Inventory |
| 0054 | locks, quarantines and the directory's subjects |
| 0055 | Extensions and Reports |
| 0056 | stream gaps, the archive's lag and retention, in the audit view |
| 0057 | algorithm posture and the emergency runbook |
| 0058 | the consistency audit of every screen |

Build only this phase's row. Build the screen from 0050's component set and
register it with 0050's harnesses; a component this phase needs that the set
lacks is added to the set, never built inside the screen.

## Objective
Author and simulate policy from the console: see what is active and what came
before it, see what a candidate would change and would break, and activate or
roll back deliberately.

## In scope
- **Policy** — bundle versions, diff, validation errors inline against the
  source, **simulate** a candidate against recorded traffic, activate, roll
  back.
- **Versions** page through the bounded-list shape (M26), newest first.
- **Validation errors inline against the source**, each rendered from the
  compiler's code and parameters at its rule and line (0005, M21) — never by
  parsing English. Policy source is an identifier in DESIGN.md §9's sense:
  LTR, mono, never translated.
- **The diff** between two versions, with the diff view (DESIGN.md §7) shipped
  into the component set here.
- **The publish-time report.** Each finding by its code and severity, a finding
  that names at most 100 targets or proxies saying how many there are (0035),
  and the override 0019 serves for a warning the author has read: accepting a
  finding by its id (0022), which the record names. The floor's impact
  preview and a ban's per-build finding are 0057's, added to this view there.
- **Activating a bundle** is a destructive action and confirms as one, by
  typing the object's name (0050's confirm). A rollback activates a version,
  so it confirms the same way. A failed activation reports inline, never by a
  toast alone.

## Out of scope
- The algorithm floor's impact preview, a ban's per-build warning and the
  emergency runbook (0057).
- Every other screen: the series table's.
- Re-opening the design, and any locale other than English.

## Acceptance criteria

**Function**
- The screen works against the seeded database, extended with the versions and
  recorded traffic it shows.
- End-to-end, headless: a candidate is uploaded, its validation errors render
  inline at their lines, it is simulated against recorded traffic, its diff is
  shown, and it is activated through the typed confirmation; then the previous
  version is rolled back to.
- A compiler rejection renders from its code and parameters, and one the
  catalogue does not know renders 0050's fallback naming the code and the
  correlation id.
- The RBAC harness covers activation and rollback: an auditor session sees the
  read-only view and the API refuses both calls — assert the API refusal, not
  just the hidden button. The route test stays green.

**Design and localisation**
- The screen is registered with every browser check, and each passes: axe, the
  screenshot baseline (both themes, both densities, loading, empty and error),
  offline, reduced motion, and both pseudolocale runs.
- The screen has all four states implemented and captured.
- The diff view is in the component set with every state designed, in both
  themes.
- 0049's static checks stay green, the catalogue key test among them.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0052-console-policy-learnings.md`. Its summary block MUST give
the routes the screen consumes, its deep links, and what 0057 builds on (where
the publish-time report renders, so the floor preview and the per-build ban
finding join it rather than sit beside it). Details MUST record **any change
made to `ui/DESIGN.md` in this PR**, with the reason, and any compiler or API
error that arrived as prose only, raised rather than parsed.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
