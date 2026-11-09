# 0053 — the console's Policy screens — Learnings

## Summary
- **Shipped (`v0.40.0`, MINOR; no migration; north-bound API version unchanged at `3`; `ext`/`server`/`contract` untouched):** Policy — versions, a version's source/changes/report/replay, the candidate editor, activation and rollback through one typed-name dialog with accept-by-id — and the diff view, source view and button link in the component set.
- **Routes consumed** (`ui/src/api/routes.ts`): `GET …/policy/bundles`, `…/bundles/{version}`, `…/bundles/{version}/diff?against=`, `…/policy/active`; `POST …/policy/validate`, `…/policy/diff`, `…/policy/simulate` (all `policy:read`); `POST …/policy/bundles` (`policy:write`); `POST …/bundles/{version}/activate` (`policy:publish`, activation and rollback alike).
- **Deep links:** `/console/policy`; `/console/policy/versions/<n>[?tab=changes|report|simulate][&against=<n>|0][&range=|from=&to=]`; `/console/policy/candidate[?from=<n>][&tab=changes|simulate]`.
- **Where 0058 builds:** the publish-time report is `Report`/`FindingCard` in `ui/src/screens/PolicyParts.tsx`, rendered on the version's `report` tab, under the candidate's source, and in `ActivateDialog`'s gate. The floor's impact preview and a ban's per-build finding JOIN that list: a new code goes in `FINDING` (`policyCodes.ts`) and `en.json` and renders in the same card, in the report's order; never a panel beside it.
- **Codes, not English:** every compiler rejection and Control finding code has a sentence (`policyCodes.ts`), held to the server's codes and each code's parameters by `ui/test/policy.test.ts` over `ui/test/policy-codes.json`, which `TestTheConsolesPolicyCodesAreTheServers` (`internal/authoring`) keeps current from `model.Codes()`/`model.Parameters()` (new).
- **Decisions:** none added or amended. **`ui/DESIGN.md` §3.4, §3.8 (new), §6, §7, §10 changed** (Details); PLAN §10 has 0053's row and a new **0065**.
- **Gotchas:** the console's catalogue namespace is `policies.`, not `policy.` — compiler codes begin `policy.` and the catalogue test reads every key-shaped literal in a known namespace as a key. The compiler reports a rule's rejection at the rule's `- id:` line, not the offending key's. Isolates (U+2068/9) wrap identifiers in sentences, so a test matching text allows them.
- **NEXT (0054):** a screen's four states and mutations register in `ui/e2e/screens.ts`; a step after load is `prepare` (it runs for every role in the RBAC harness, so it must tolerate a role offered nothing).

## Details

### What each view does

- **Versions** read one page (`limit` 100 unless the URL names one) and the
  active version beside it. The active one's 404 is a fact ("none is
  active"), not an error; its outage is the list's partial state, the gap
  named. Row actions: open, compare with the active version, and activate or
  roll back (danger; offered to `policy:publish`). A version older than the
  active one is a rollback (`isRollback`), which only changes the words.
- **A version** reads its bundle, then validates its source (`POST
  …/validate`, `policy:read`) for the report against the fleet as it is now —
  there is no GET for a stored version's report. A 404 is the empty state; a
  report that cannot be read is the partial state (the source shown without
  notes). The `changes` tab compares against the active version by default
  and, for the active version itself, the one before it (`against` = n−1).
- **The candidate** starts from the active source, `?from=<n>`'s, or this
  tab's kept draft (`sessionStorage`, per tenant — a courtesy). Validate,
  Compare and Simulate are every reader's; Upload is `policy:write`'s. A 422
  on upload sets each rejection at its line and an `ErrorMessage` at the form,
  never a toast. A successful upload navigates to the new version (or the one
  that already held that source — `existing`).
- **Replay** asks `POST …/simulate {version|source, replay:{from,to}}` over
  the window the time range picker writes to the URL (default the last seven
  days; "all time" asks from 1970). Flips are grouped as the server groups
  them; each decision links to Explain. Dry-run cases are not built: replay is
  what "against recorded traffic" asks, and `policyctl simulate -cases`
  remains the way to dry-run.
- **ActivateDialog** reads the version and validates it on opening, lists
  blocking findings and each warning with an accept box (`data-accept`), and
  sends `{accept: [...ids]}`. A `409 findings_not_accepted` merges the
  findings the server named at activation into the list (`mergeFindings`) and
  reports in the dialog; the dialog stays. The confirm hint says decisions
  taken under it are not taken back (a rollback reverses the pointer, not the
  decisions), via `ConfirmDialog`'s new `hint`.

### Statuses (`STATUS`, DESIGN.md §6)

`valid` (allow), `invalid` and `blocking` (deny: the server refused on
purpose — never an outage), `warning` (warn), `advice` (info), `superseded`
(unknown: neither healthy nor failed). A severity outside the three renders
`notReported`, never fine. A compiler rejection is `invalid`, not
`rejected`: §6's `Rejected` is a proxy refusing a document, and a compile
error refuses as outright as a validator's block, so both sit on deny.

### Changes to `ui/DESIGN.md` in this PR, and why

1. **§3.8 (new): `--diff-add-bg`, `--diff-remove-bg`.** A diff's sides are not
   statuses; a removed allow rule tinted deny-red would read as a denial, so
   neither takes a status hue. Pale grounds of their own, the side said by
   the `+`/`−` marker and words; both held to 4.5:1 for body and secondary
   text in `test/contrast.test.ts` (143 pairs now).
2. **§3.4:** deny's meaning includes a bundle refused (invalid, blocking).
3. **§6:** the six statuses above, and a paragraph on how a version and what
   is said about a bundle read.
4. **§7:** the button link, the source view and the diff view (their states
   and direction), the mono textarea, `ConfirmDialog`'s hint, a breadcrumb
   crumb as data.
5. **§10:** `data-force-child` forces a child scrolling region as well as a
   button (`e2e/harness.ts`), for the diff view's focus-visible cell.

### Component set changes

`Diff.tsx` (`DiffView`, `DiffSkeleton`, `parseUnified`, `diffCounts`);
`Code.tsx` `SourceView` + `sourceLineId`; `Button.tsx` `ButtonLink`;
`Field.tsx` `Textarea mono`; `Dialog.tsx` `ConfirmDialog hint`; `Badge.tsx`
`Breadcrumb` label is a node; `router.tsx` exports `LinkProps`;
`client.ts` an `ok` answer carries `correlationId` (the `X-Correlation-Id`
header), so a code in a 200 report the console does not know can name it.
Gallery page `diff`; the buttons page shows the button link.

**Direction.** The source and diff tables follow the page's direction (the
gutter at the line's start) and only each line's text is `dir="ltr"
translate="no"`. A table set `dir="ltr"` whole failed `en-XB`'s mirror check,
and setting it `translate="no"` would have hidden the notes' catalogue text
from `en-XA`.

### What arrived as prose only (raised, not parsed)

None of these is shown as a sentence of the console's; each is raised as
phase **0065**, queued:

1. **Compiler `detail` parameters** — `policy.document_malformed` (the YAML
   parser's message; "unknown day"), `route.filter_rule_invalid`,
   `route.restricted_command_invalid`, `obligation.contradicts_route`. The
   sentence interpolates the English.
2. **`decision.Unreplayable.Reason`** is `err.Error()`. Shown under "Why, in
   the server's words", mono, untranslated.
3. **A refused simulation**: `400 invalid_request {field: body, problem:
   invalid, reason: <English>}`. The console says "the request could not be
   read".
4. **`POST …/policy/diff` on a source that does not parse**: `422
   policy_invalid {reason: <English>}`, with no rejections. The console says
   it does not parse and points at Validate.

A registered validator's finding is not on the list: its code is its
provider's and it renders by the fallback (code, provider, parameters as JSON,
id) by design; Enterprise is told so in the sync.

### The fixture (`ui/e2e/fixture/`)

`policy-v2.yaml` (v1 + `office-db-read`, a cache hint on a `source_cidrs`
rule → `authoring.cache_hint_spans_sources` warning) and `policy-v3.yaml`
(the office path moved to `10.40.0.0/16`, `ops-db-shell` needing a
`db-maintenance` grant, the build-host deny replaced by `sre-build-shell`).
`policy.ts`'s `seedPolicy` runs after `seedAudit`, so every recorded decision
was taken under v1: it uploads v2 and activates it accepting its warning,
then uploads v3. Replaying v3 flips `sess-e2e-allow` allow→deny and
`sess-e2e-rule-deny` deny→allow. `candidates()` gives the refused candidate
(an undeclared group and a duplicated rule id) and a valid one (v3 reworded,
so it uploads as v4). Recorded decisions carry the server's own instant, not
the request's `conn.timestamp`, so a relative window finds them.

### Tests

- Unit: `test/policy.test.ts` (91 cases: every code's sentence and its
  parameters, the fallbacks, the model's rules, the diff parser — a removed
  line beginning `-- ` is a line, since the server writes no file header).
- Browser: `e2e/policy.spec.ts` (checks: rejection at its line from its code,
  the unknown-code fallback with the correlation id, a refused upload inline
  storing nothing, an auditor refused at the API for activate/rollback/upload,
  the deep links) — its own `console-e2e` entry, `policy`; and the author's
  whole flow in `mutations.spec.ts`. Nine registry entries; RBAC covers
  activation and rollback on the list, activation on the version, upload on
  the candidate, per role.
- Go: `TestTheConsolesPolicyCodesAreTheServers`.

### The baseline

46 new images and 74 rewritten (every console screen: the navigation gained
Policy, and the audit view shows the versions' records). **Take a baseline on
a server the fixture set up once**: stop any running e2e server and let
Playwright start its own, as CI does. Global setup signs every operator in on
each run and its seeds skip what exists, so a reused server holds a second set
of sign-in records, and the audit records page shows them. That is how this
PR's first CI run failed on `audit-records` alone. The navigation
change is under the 1% tolerance, so `--update-snapshots` alone left the old
images; they were rewritten with `--update-snapshots=all`, and the 31 whose
pixels did not change restored from `main`. Not recompressed with `oxipng`
(not available in this container; 0050 calls it optional): the directory is
13 MB, against 5.8 MB before. A later session with `oxipng -o4 --strip safe`
can shrink it with no pixel changed.

### Noticed, not changed

- The candidate's hint says where its text started even after it is edited.
- There is no GET for a stored version's publish-time report, so the version
  page validates its source. One call, and the same report activation
  recomputes, so it is right; a route would save a source round trip.

### Cross-repo

No shared surface changed (`ext`, `server`, the contract and the cross-repo
protocol are untouched), but the release changes what Enterprise's binary
serves and the component set its screens build from, and it fixes how a
registered validator's finding reads in Control's console. A downstream sync
for `hoplock/enterprise` is queued:
`prompts/downstream/queued/control-PR#153-console-policy.md`. Proxy: none.
