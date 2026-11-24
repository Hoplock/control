# 0071 — Every list through the data table

## Read first
- `docs/PROTOCOL.md`: §3 (scope discipline), §4 (the Definition of Done),
  §5 (the learnings file) and §8 ("A console phase shows what it built").
- `docs/PLAN.md` — §2: **M20** (one design, one component set), **M21**,
  **M23**; §3 (`ui/`); §10's rows for 0050 and 0060.
- `ui/DESIGN.md` — §4 (a wide table scrolls in its own container with its
  first column pinned), §7 (the data table, "One component per job"), §10.4
  and §10.10.
- `docs/learnings/` — every summary; in full,
  `0060-console-consistency-audit` (item 1's verdict and why this was left
  open), `0050-console-components-and-fleet` (the data table) and
  `0058-console-algorithm-posture-and-runbook` (most of these tables).

## Why this phase exists
0060 found seven tables a screen draws itself instead of through
`DataTable` (`ui/src/components/DataTable.tsx`), in three shapes of their own
(`Screen.module.css` `.table`, `Algorithms.module.css` `.table`,
`Policy.module.css` `.flipTable`), two of which ignore the operator's density.
It pinned their first column so they meet DESIGN.md §4, and left moving them
open because their checks select rows by data attributes the data table does
not carry. `ui/test/consistency.test.ts` holds their count, which may only
fall.

## Objective
Every list in the console is the data table, and the only other `<table>` is
the audit view's counts (a chart, DESIGN.md §11).

## In scope
1. `DataTable` gains what these tables need, deliberately and in the gallery:
   a row's own data attributes (`rowData?(row): Record<\`data-${string}\`, string>`,
   or equivalent), and a form with no toolbar for a table set inside a card
   or panel. Both are part of the component's contract (M20): name them in
   DESIGN.md §7.
2. Move onto it, keeping every selector their checks and specs read:
   - `ui/src/screens/Deployment.tsx` — the nodes;
   - `ui/src/screens/FleetAlgorithms.tsx` — the levels and the profiles
     (`[data-levels] tbody tr[data-level]`, …);
   - `ui/src/screens/AlgorithmFindings.tsx` — a floor's targets and a ban's
     proxies, inside their finding cards;
   - `ui/src/screens/PolicyParts.tsx` — a replay's flips;
   - `ui/src/screens/Runbook.tsx` — the sessions step 3 ends.
3. Delete the three screen-local table styles, and set `TABLES` in
   `ui/test/consistency.test.ts` to `{ "screens/AuditCounts.tsx": 1 }`.
4. Update the baseline of every screen whose look changes, deliberately, and
   name each in the PR.

## Out of scope
New columns, sorting or filters these lists did not have; any API change.

## Acceptance criteria
- `TABLES` holds only AuditCounts, and the check fails if a screen draws a
  table of its own (record the mutation).
- Every DESIGN.md §10 check, 0060's "widths" job, the RBAC harness and every
  console spec pass; `go test ./...`, `go vet ./...` and `golangci-lint` at
  CI's pin pass.
- The PR shows each moved table before and after, light and dark
  (`docs/PROTOCOL.md` §8), and attaches `npm run contact-sheet`'s sheets for
  the areas it touched.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`: move this prompt to `implemented/`, add
`docs/learnings/0071-every-list-through-the-data-table-learnings.md`, and
cut the next MINOR (`make release-check`). **Cross-repo impact:** the data
table's contract gains members Enterprise's screens may use; queue the
downstream sync for `hoplock/enterprise` (`docs/CROSS-REPO-PROTOCOL.md` §4.1).
