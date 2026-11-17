# 0060 — one look and feel across every console screen — Learnings

## Summary
- **Shipped (`v0.47.0`, MINOR; no migration; north-bound API `5`; `ext`/`server`/`contract` untouched; no Go change):** the audit of all 31 screens and the gallery against `ui/DESIGN.md` and each other — 14 items: **2 confirmed, 7 wrong → fixed, 5 confirmed or fixed with a part left open** (table below, with severities).
- **Fixed at the source** (`c8609e9`; checks `37717ec`): `Timestamp` (11 screen-local `<time>`s; copies ISO-8601 UTC), `Notice` (14 notes in 2 screen-local shapes, no glyph; a refusal now `deny`), `TextButton` (Fleet's/Audit's row openers, a source line), sign-in/App/TopBar on `Button`/`Field`/`Select`, break-glass `warn` everywhere, Fleet's relay count neutral, `HEALTH_STATUS` → `STATUS`'s entries, Fleet's algorithms view compact, a destructive success a persisting `receipt` toast, catalogue words (Identity/person/deployment), the algorithms tables' first column pinned.
- **Checks added, each failed by a mutation** (Details): `ui/test/consistency.test.ts` (controls/overlays/instants only from the set; `<table>` ratchet; status hues only in named places; badge hue only for break-glass; every screen has `PageHeader`; density by area; §6 table = `STATUS`; no copied tone/word; glossary) and the `widths` browser job (1024/900px, Explain and Audit 768). `npm run contact-sheet` (not CI).
- **DESIGN.md changed:** §3.3 (a link's underline is accent), §3.7 (Fleet/Audit = every view), §7 (text button, timestamp, notice; "One component per job"; "Page anatomy"; receipts; one partial heading), §9 (the glossary), §10 (check 10).
- **Open:** seven screen tables not yet `DataTable` → **0070** (queued); no layout-shift measure on load; the palette reaches screens, not actions; Fleet ↔ a proxy's stream has no link either way; the claim-mapping write is not typed-confirmed (**0067** owns it).
- **A host's screens must now:** use `Notice`, `Timestamp`, `TextButton` and `toast.receipt` for those jobs; give a badge a hue only for supervised/break-glass; use §9's words; pass check 10's rules and the widths job over their registry.
- **Gotchas:** main's CI for `e7833f1` (0059) failed its baseline job, so `v0.46.0` is untagged; `release-check` here used a local-only tag. Check 10 reads JSX by the syntax tree: a `<button` split over lines is still found.
- **NEXT:** 0061 signs a person in to this console; 0070 moves the last tables; new screens register with `consistency.test.ts` by existing in `registry.tsx`.

## Details

### The verdicts

Severity: **high** — an operator could misread state or lose a record;
**medium** — two screens disagree in a way an operator sees; **low** — a
duplicate that has not drifted yet.

| # | Item | Verdict | Evidence |
| --- | --- | --- | --- |
| 1 | One component per job | **wrong → fixed** (medium), tables **open** → 0070 | 11 `<time>` copies (`Audit.When`, `Explain.When`, `Fleet.Instant`, `InventoryParts.Instant`, `Policy.Instant`, `AlgorithmFindings.Instant`, inline in Deployment ×2, FleetAlgorithms, PolicyParts ×3, Runbook, FeedLag, Timeline) → `components/Timestamp.tsx`. Notes in two shapes (`Audit.module.css .notice`: bordered card; `Inventory/Algorithms/Policy .notice/.note/.warnNote/.receipt`: sunken edge), colour alone → `components/Notice.tsx`. Row openers `Fleet .open`, `Audit .open`, `Policy .lineLink` → `TextButton`. `SignIn` (5 buttons, 3 inputs, own `.primary/.warning/.quiet/.input`), `App .retry`, TopBar nav toggle and tenant `<select>` → the set. `chrome.ts HEALTH_STATUS` copied seven tone/word pairs → `STATUS.*`. `Reports.module.css .fault` was dead. Seven tables (Deployment 1, FleetAlgorithms 2, AlgorithmFindings 2, PolicyParts 1, Runbook 1) in three local shapes: open, because their specs select rows by attributes `DataTable` rows lack; their first column now pins (item 12); `TABLES` ratchet in check 10. |
| 2 | Status vocabulary | **confirmed**, two drifts **fixed** (medium) | 43 `STATUS` entries = §6's 43 words (now `has the same states, by tone and word, as the STATUS map`). Every later phase's state is in §6's rows (lock, quarantine, deactivation deny; refused at ingest, failing, not started warn; not judged unknown). Fixed: break-glass was `info` in the inventory (Identities ×2, Standing ×2, Grants) and `warn` in the chrome; the error toast drew a warn glyph on an outage edge. Table below. |
| 3 | Page anatomy | **confirmed** | Every one of the 25 screen functions `registry.tsx` imports renders `PageHeader` (test per screen). Header order is the component's (`PageHeader.tsx`: breadcrumb, title, id, status, actions at the end, lede). Views that are deep links are `ViewLinks` (Fleet, Audit, Inventory); a page's own sections are `Tabs` (`?tab=`). A row with a page links from its first column (inventory, policy versions); one without opens a drawer from its first column (Fleet `?proxy=`, Audit `?record=`) — now one `TextButton`. Written as §7 "Page anatomy". Sign-in's connectors are each primary: the stated exception. |
| 4 | The four states | **confirmed**; skeleton shift **open** (low) | `ScreenFrame` frames every list and detail (counts per file: Audit 2, Identities 6, Policy 3, …); `ErrorState` is the one error panel and says outage (`state.error.*`); `EmptyState` everywhere (title, body, one action); `PartialNotice` everywhere. 0057 asked whether a hole needs its own heading: ruled **no** — one heading on every screen, the gap line names the cause (§7). Loading is in the baseline for every registered screen, but no check measures shift after load: not built here. |
| 5 | Lists and filters | **wrong → fixed** (medium) | `fleet/algorithms` defaulted to comfortable while Fleet is compact (`registry.tsx`); now compact, and check 10 holds "Fleet/Audit compact, else comfortable". Paging: `DataTable` + `Pagination` on every bounded list; a cut answer is `DataTable`'s `partialNote`, and a cut replay a `Notice` (warn). Filter parameters are single lowercase words equal to the API's (`subject`, `target`, `principal`, `proxy`, `cause`, `label`, `group`, `state`, `range`/`from`/`to`), under `/console/` by `consoleHref`. |
| 6 | Mutations | **wrong → fixed** (high) | 15 danger buttons, every one opening `ConfirmDialog` (grep `variant="danger"` + `danger: true`). But 13 typed-confirm successes reported only in a 5 s toast — a revoked grant's ended sessions vanished: now `toast.receipt` (persists). Runbook and retention keep receipts on the page. Failures are inline (`ConfirmDialog`'s `ErrorMessage`, `useFailure`). Text shown to someone else is previewed the same way: a lock's holder text and the runbook's kill reason ("Each person will read:"). The claim-mapping write is not typed-confirmed: 0067. |
| 7 | Identifiers, time, numbers | **wrong → fixed** (medium) | Copying a timestamp gave the local form: `Timestamp`'s `onCopy` writes ISO-8601 UTC when the selection is inside it. Ids: `CopyableId`/`Ident`/`code dir="ltr" translate="no"`. Numbers: `DataTable` `numeric`, `Badge` tabular. Relative time: none anywhere — the rule is "none" (§9). |
| 8 | Words | **wrong → fixed** (medium) | "Subject" labelled an identity in Explain, Audit, simulation (inventory says "Identity"); "user" in Explain and the runbook; "this server" for what the deployment holds (Explain, blocked commands). 17 messages reworded, keys unchanged; glossary in §9 and below; lint in check 10. |
| 9 | Navigation and keyboard | **confirmed**; palette actions **open** (low) | Nav order is `SCREENS`'s; the palette lists every nav screen (`go:`), theme and density (`Shell.tsx:151`), not screens' primary actions — that needs each screen to register its action, a feature. Skip link `Shell.tsx:126`; focus restored by `useModal` (`Dialog.tsx:45`) for every dialog and drawer; keyboard hints through `KeyboardHint`. |
| 10 | Links between screens | **confirmed**; Fleet ↔ audit **open** (low) | Audit ↔ Explain both ways; inventory → audit filtered (`Targets`, `Identities`, `Grants`); supervised badge → `audit/records?principal=`; Policy ↔ runbook both ways. No link from a Fleet proxy to its stream (`audit/gaps?proxy=`) or back: Fleet opens a proxy only from the page it holds, so the way back needs Fleet to read one proxy by id. |
| 11 | Absent and unavailable | **confirmed** | Every `Not reported` is `StatusOf name="notReported"` (11 files); an absent extension reads Control's own answer (`extensionsModel` `POINTS`); a pack that cannot list is `Unavailable`, never "none". |
| 12 | Width | **wrong → fixed** (medium) | The new check found Fleet's algorithms (1024, 900) and a floor finding (1024) scrolling sideways with the first column unpinned; `Algorithms.module.css` now pins it. 151 → all pass. |
| 13 | By eye | **wrong → fixed** (low) | From the contact sheet: notes with a hue and no glyph; the break-glass submit edged in warn; a relay count in the info hue; the accent underlining every link, which §3.3 forbade in words while every screen did it — §3.3 corrected. The standing-denials panel's two fact lists still size their columns apart (0055's note): open, low. |
| 14 | DESIGN.md and `ui/` agree | **wrong → fixed** (low) | §7's inventory lacked text button, timestamp, notice; §3.7 did not say Fleet's other view; §3.3 (above). §6 = `STATUS` is now a test. 0049–0059's DESIGN.md changes still hold, but 0050's "Status… do not render a status beside it" was broken by `HEALTH_STATUS`'s copy (fixed). |

### The state table (item 2)

Screens naming each state, by a literal search of `src/screens`, `src/shell`
and `FeedLag`/`States` (a model file is where a screen's state is chosen).

| Word | `STATUS` | Hue | Glyph | Where |
| --- | --- | --- | --- | --- |
| Allowed | `allowed` | --s-allow | filled circle | Grants, PolicyParts, explainModel |
| Healthy | `healthy` | --s-allow | filled circle | Fleet, fleetModel |
| Active | `active` | --s-allow | filled circle | Grants, Policy, Runbook, extensionsModel, inventoryModel, policyModel, runbookModel |
| Applied | `applied` | --s-allow | filled circle | Fleet, Runbook, fleetModel, runbookModel |
| Valid | `valid` | --s-allow | filled circle | PolicyParts, Runbook |
| Running | `running` | --s-allow | filled circle | Extensions, PolicyParts, extensionsModel |
| Caught up | `caughtUp` | --s-allow | filled circle | FeedLag |
| Declared | `declared` | --s-allow | filled circle | FleetAlgorithms, algorithmModel |
| Done | `done` | --s-allow | filled circle | PolicyParts, Retention, Runbook, runbookModel |
| Denied | `denied` | --s-deny | filled square | Grants, PolicyParts, explainModel, States |
| Revoked | `revoked` | --s-deny | filled square | Fleet, fleetModel, inventoryModel |
| Expired | `expired` | --s-deny | filled square | inventoryModel |
| Invalid | `invalid` | --s-deny | filled square | PolicyParts, Runbook, reportsModel |
| Blocking | `blocking` | --s-deny | filled square | PolicyParts, Runbook, policyModel |
| Locked | `locked` | --s-deny | filled square | Standing, standingModel |
| Quarantined | `quarantined` | --s-deny | filled square | Standing, standingModel |
| Deactivated | `deactivated` | --s-deny | filled square | Standing, standingModel |
| Degraded | `degraded` | --s-warn | filled triangle | Fleet |
| Stale | `stale` | --s-warn | filled triangle | Fleet, fleetModel |
| Expiring | `expiring` | --s-warn | filled triangle | none yet: reserved by §6 for a grant near its end |
| Rejected | `rejected` | --s-warn | filled triangle | Fleet, fleetModel |
| Warning | `warning` | --s-warn | filled triangle | PolicyParts, Runbook, policyModel |
| Not started | `notStarted` | --s-warn | filled triangle | Extensions, extensionsModel |
| Failing | `failing` | --s-warn | filled triangle | FeedLag |
| Refused at ingest | `refusedAtIngest` | --s-warn | filled triangle | Gaps, gapModel |
| Unreachable | `unreachable` | --s-outage | hollow circle, dashed | Fleet, fleetModel, chrome |
| Fetch failed | `fetchFailed` | --s-outage | hollow circle, dashed | Fleet, fleetModel |
| Unserved | `unserved` | --s-outage | hollow circle, dashed | Explain, explainModel |
| Unavailable | `unavailable` | --s-outage | hollow circle, dashed | Reports |
| Failed | `failed` | --s-outage | hollow circle, dashed | Policy, PolicyParts, Reports |
| Evicted | `evicted` | --s-outage | hollow circle, dashed | Gaps, Replay, gapModel |
| Pending | `pending` | --s-info | half-filled circle | Fleet, Runbook, Standing, fleetModel |
| Scheduled | `scheduled` | --s-info | half-filled circle | Grants, inventoryModel |
| In progress | `inProgress` | --s-info | half-filled circle | AlgorithmFindings, ClaimMapping, Fleet, FleetAlgorithms, algorithmModel, fleetModel |
| Pending restart | `pendingRestart` | --s-info | half-filled circle | Fleet, fleetModel |
| Advice | `advice` | --s-info | half-filled circle | PolicyParts, policyModel |
| Behind | `behind` | --s-info | half-filled circle | Retention, FeedLag |
| Not reported | `notReported` | --n-400 | hollow circle, dotted | AlgorithmFindings, Fleet, FleetAlgorithms, PolicyParts, algorithmModel, explainModel, fleetModel, gapModel, inventoryModel, policyModel, FeedLag |
| Left | `left` | --n-400 | hollow circle, dotted | chrome, Deployment (through `HEALTH_STATUS`, now `STATUS`'s own entries) |
| Superseded | `superseded` | --n-400 | hollow circle, dotted | policyModel |
| Not registered | `notRegistered` | --n-400 | hollow circle, dotted | Extensions, extensionsModel |
| Not declared | `notDeclared` | --n-400 | hollow circle, dotted | FleetAlgorithms, algorithmModel |
| Not judged | `notJudged` | --n-400 | hollow circle, dotted | AlgorithmFindings |

Rendered by `Status`/`StatusOf` (pill or inline) everywhere, by
`StatusGlyph` in Fleet's bar and legend and in a `Notice`.

### The glossary (item 8, DESIGN.md §9)

identity (the record; never "user"; "subject" only as code) · person (the
human denied or whose session ends) · principal (the credential that made a
call: `sess-`, a token, `sup-`) · operator (signed in here) · target (what
policy names; "host" only for its hostname and keys) · proxy (a Hoplock
Proxy) · node (a Control server in this cluster) · deployment (this Control:
what it decided, holds, recorded) · server (only the API answering) · denied
(policy's decision) · refused (a request or record not accepted) · blocked (a
command a proxy's filter stopped).

### The checks and the mutations that proved them

`npx vitest run test/consistency.test.ts` after each drift, then restored:

| Drift | Failed |
| --- | --- |
| a `<time>` on Extensions | `screens/Extensions.tsx renders no control, overlay or instant of its own` |
| a `<table>` on Extensions | `renders a list through the data table, but for the tables 0070 moves` |
| `color: var(--s-deny)` in `Reports.module.css` | `draws a status hue only in the status component, the set, and three named places` |
| a break-glass badge back to `info` | `gives a badge a hue only for break-glass` |
| Reports' `PageHeader` removed | `ReportsScreen heads its page with the page header` |
| `fleet/algorithms` back to comfortable | `defaults the scanned screens to compact …` |
| `notJudged` moved to the warn row | `has the same states, by tone and word, as the STATUS map` |
| `left` copied as a tone and word in `chrome.ts` | `is the one map: no other copies a state's tone and word` |
| "Subject" back as a label | `no message uses /^Subject$/` |

The widths job failed on arrival on three real defects (item 12), which is
its mutation. **Not mechanical, written instead:** the receipt rule (which
success follows a typed confirm is the call graph's, not a line's: §7), one
primary per view, and the by-eye rules of §3.3–§3.6.

### The contact sheet

`ui/scripts/contact-sheet.mjs` reads only `ui/e2e/baseline/`, so it shows
exactly what CI diffs and needs no server: one PNG per area and theme in
`ui/build/contact-sheet/`. The PR links the before (main's baseline) and
after sheets.

### Noticed, not changed

- Main's CI for 0059's merge (`e7833f1`) failed the screenshot baseline job,
  so the release job was skipped and `v0.46.0` is untagged; `release-check`
  refuses every later release until the job is re-run there. This phase was
  checked with a local-only `v0.46.0` tag on that commit, as 0054 did.
- `PartialNotice` uses a fixed element id (`partial-title`); two on one page
  would share it. None does today.
- `Expiring` is in §6 and `STATUS` but no screen renders it yet.

### Cross-repo

No shared surface changed (`ext`, `server`, the contract, the cross-repo
protocol), but Enterprise's screens build from this component set and
catalogue conventions (M20, M27), and this phase adds three components, a
toast kind, a badge tone, a glossary and checks a host's screens are held
to. A downstream sync for `hoplock/enterprise` is queued (the PR's
`## Cross-repo impact`). Proxy: none.
