# 0066 — the console's visual policy editor — Learnings

## Summary
- **Shipped (`v0.53.0`, MINOR; no migration; north-bound API unchanged at `8`; `ext`/`server`/contract untouched):** `/console/policy/candidate?view=visual` (`&rule=<id>` opens a rule's editor; "Edit visually" from a version) — 0053's candidate shown as cards in evaluation order, default deny last; drag and keyboard reordering; a rule editor of closed choices; a vocabulary panel; "Where would this land?" by dry-run.
- **YAML library: `yaml` 2.9.1** (pinned exact, ISC, no deps, ~30 KB gzip; first load 236 of 350 KB). Chosen because its concrete syntax tree stringifies back every byte and its nodes carry their text ranges. **Edits are text splices, never a re-serialisation** (`ui/src/screens/policyDocument.ts`): a value replaced where it stands, a key inserted after its last sibling, a rule moved as its own lines with its header comments. An edit that would lose a comment is refused (`comment_lost`), one the text's form cannot take is refused (`structure`).
- **How the vocabulary reaches the console:** `model.Vocabularies()` + `model.MatchAxes()` → `ui/src/screens/policyVocabulary.json`, generated and held by `TestTheConsolesPolicyVocabularyIsTheServers` (`HOPLOCK_UPDATE_POLICY_VOCABULARY=1`). No route; no API version move. `ui/test/policyVisual.test.ts` refuses a hand-written option list in the editor's source.
- **Not modelled (read-only, "edit in source" at its line):** top-level `algorithm_bans`, scope details; `match.device`, `subject.claims`, `context.time_of_day`; `route.forwards`, `global_requests`, `requests.subsystems`, `filter.rules`/`restricted_exec`, `algorithm_bans`, `enforcement.platform_role`/`permitted_destinations`/`attestation`, `max_session_duration`, `concurrency`, `port`; ladder entries' keys other than `method`/`username`; obligations' `approver_groups`/`message`. The list is `RULE_MODELLED`/`MODELLED_TERMS`/`ROUTE_MODELLED` in `policyDocument.ts`.
- **Components added:** `SortableList` (+`SortableListSkeleton`), `ChipEditor`; `ViewLink.query`. Gallery page `editor`. **`ui/DESIGN.md` §7, §8, §11 changed** (Details).
- **Decisions:** none added or amended; register unchanged. PLAN §10 (0066's row) revised in place.
- **Gotchas:** the platform's drag and drop never starts from a `<button>` (no `dragstart` at all), so the handle drags by pointer events; the e2e server embeds `dist` when built — rebuild, then restart it; `main` scrolls, so bring both ends of a drag into view.
- **NEXT (0067):** reuse `parsePolicy`/`applyEdit`'s text-splice core, `SortableList`, `ChipEditor`; the policy-specific parts are `visualOf`/`ruleOf` and the rule ops.

## Details

### The document model, and why text splices

A version is its source and that source's digest (PLAN §5.6), and a reviewer
reads the diff, so the editor may change only the bytes an edit names. Every
"comment-preserving" serialiser normalises something it did not touch —
spacing after a colon, line folding, the quoting of an untouched scalar,
indentation width — so `Document.toString()` was not acceptable even with
comments kept. Instead:

- **`parsePolicy`** keeps the parser's CST tokens and a `Document` parsed with
  `keepSourceTokens`, so every node has `range` and `srcToken`.
- **`serialise`** is the CST stringified. The round trip is therefore
  byte-identical by construction; the test holds it over every
  `ui/e2e/fixture/policy*.yaml` and `ui/test/policy-corpus.yaml`, plus CRLF,
  odd spacing, a missing final newline.
- **`applyEdit`** returns a new source string; the screen re-parses it. The
  ops: `set` (scalar, list, map of lists; `undefined` removes the key and any
  map it empties, never the rule), `obligations`, `username`,
  `addCredential`/`removeCredential`, `move`, `addRule`, `removeRule`.
- **Rules are moved by layout**: the `rules` list is cut into each item's span
  (from the `-` line, backed up over comment lines at or left of the dash's
  column — the CST puts those in the item's `start`, except the first rule's,
  which sit on the `rules:` key) and the gaps between spans. A move permutes
  spans and keeps gaps where they were, so blank lines stay put and each
  rule's comments travel with it; the test moves every rule to every place
  and back, byte-identical. A flow-style `rules: [...]` disables structure
  edits (the view says so).
- **New text** is written in the fixtures' style: block maps, flow lists
  (`[a, b]`), flow maps of lists for labels; a scalar plain only where YAML
  reads it back as the same string (`true`, `0755`, `*.x` are quoted).

`removeRule` is the one edit allowed to drop comments — the ones written with
that rule — and its announcement counts them.

### Free text, checked where the server checks it

`checkRuleId` (`rule.id_missing`, `rule.id_duplicate` with `other_line`),
`checkReason` (`rule.deny_needs_reason`), `checkHostPattern`
(`match.invalid_hostname_pattern`, a port of `model.ValidHostPattern`),
`checkCidr` (`match.invalid_cidr`, a port of `compile.parseNetwork`: Go's
`netip.ParsePrefix` then `ParseAddr` — note `fe80::1%eth0/64` is valid,
because the zone swallows `/64`). The CIDR table in
`ui/test/policyDocument.test.ts` was taken by running Go's `netip` on each
input. Each refusal renders through `rejectionText`, so it is the server's
code's sentence. The server's own rejections after the debounced re-check
(`POST …/validate`, 400 ms after an edit) are also set on the field a code is
about (`codeError` in `PolicyVisual.tsx`).

### The screen

- The candidate's two views are view links (`?view=visual`), one candidate:
  the source state is shared, so switching never loses an edit. The visual
  view has its own actions panel (`data-visual-actions`); the results tabs
  below are 0053's, unchanged. Upload is still `policy:write`'s only.
- Notes go on cards by `notesByRule` (`policyVisualModel.ts`): by rule id, by
  line within the rule's span, and a duplicated id by its line; the rest
  head the list. Card notes reuse 0053's `RejectionNote` and `FindingCard`.
- A card is `<article>` in the sortable list: id (a text button opening the
  drawer, `?rule=<id>`, or `#<n>` for a rule with no id), effect as
  `Allowed`/`Denied` (`Not reported` when absent), match chips as
  `term=value` per axis, the route in one mono line, obligations, cache hint,
  unmodelled keys with "Edit in source, line n".
- **A trace is not a status.** A traced card is marked by an accent edge and
  a sentence; a matched chip by the accent (selection, §3.3). An earlier cut
  put `In progress` beside it, which says something false — removed while
  applying 0060's checklist (below).
- Chip keys match the engine's matched-term names (`ENGINE_TERM`):
  `subject.ids`→`id`, `context.days`→`day` (capitalised, Go's
  `Weekday.String()`), `context.source_cidrs`→`source_network`, etc.
- The trace form asks the subject id, its groups (the bundle's), hostname,
  each declared label (one value or none), zone and an instant
  (`datetime-local`, the operator's zone, `fromLocalInput`). A candidate that
  does not compile answers `422 policy_invalid`, shown as 0053's sentence; a
  refused case as `simulationRefusalText`.
- The MFA term is a switch: on writes `mfa: true`, off removes it. `mfa:
  false` in a source shows on the card and is edited in source.
- A new label key is declared with its first value (a key with no values is
  refused by the compiler, and an empty list is how the model removes one).

### 0060's consistency checklist, applied here

1 one component per job — the set's controls only; two components joined the
set rather than living in the screen. 2 status vocabulary — effect as
Allowed/Denied, a bundle Valid/Invalid; **found and fixed:** `In progress` used
as a trace marker. 3 page anatomy — `PageHeader`; the views are view links; a
card's id opens its drawer with the id in the URL. 4 four states — loading
(`VisualSkeleton`), empty (no rules: the default deny alone and the one
action), error (the start's outage), partial (the server's check not read:
the rules without notes, the gap named); all registered. 5 lists — the rule
list is not a data table, because its order is its content and a column sort
would hide it; **DESIGN.md §7 now says so.** 6 mutations — nothing here is
stored until Upload, which is unchanged; removing a rule from a candidate is
reversible and announced, not typed-confirmed. 7 identifiers — ids, terms,
routes, descriptions and reasons mono/LTR/`translate="no"`; positions through
`Intl`. 8 words — "Identity", never "subject"/"user"; a refusal is refused,
the default deny denies. 9 keyboard — every action keyboard-operable and
announced (tested), focus restored by the drawer. 10 links — version ↔
visual, card → its source line. 11 absent — an effect not written is Not
reported. 12 width — one column under 1280px; the widths job runs over the
four new entries. 13 by eye — screenshots in the PR. 14 DESIGN.md and `ui/`
agree — §7/§8/§11 updated.

### Changes to `ui/DESIGN.md`, and why

1. **§7 inventory:** chip editor (primitive), sortable list (composite), and
   a paragraph on each — their keyboard path, announcements and states.
2. **§7 "One component per job":** a list is the data table *except* a list
   whose order is what it says (a policy's rules), which is the sortable
   list.
3. **§7 page anatomy:** a view link may be a query for two views of one object.
4. **§8:** a drag always has a keyboard path that does the same and both
   announce.
5. **§11:** the one YAML document library, why, and its cost.

### Tests

- Go: `TestTheConsolesPolicyVocabularyIsTheServers`;
  `TestTheConsolesPolicyCorpusCompiles` — compiles `ui/test/policy-corpus.yaml`
  and walks every yaml key of every type a bundle decodes into (by
  reflection), so a schema key added later fails until the corpus uses it
  (mutation-checked by deleting `asserted_at`); `TestEveryVocabularyHasMembers`.
- Unit: `policyDocument.test.ts` (55: round trips, every edit's exact line
  diff, every move and back, checks), `policyVisual.test.ts` (14: no option
  list by hand — mutation-checked with `options={["allow", "deny"]}` — notes
  placement, engine term names, trace marks).
- Browser: `e2e/policy-visual.spec.ts` (9, `checks`), including the
  acceptance criterion that one visual edit makes `POST …/policy/diff` name
  exactly that rule (or `document: ["groups"]`) with every comment kept, and
  `rule.unreachable` on its card; `e2e/policy-visual-mutations.spec.ts`
  (`mutations`): built, dragged, validated, traced, uploaded, activated
  through 0053's dialog, then v2 restored. Registry entries
  `policy-candidate-visual` (4 states + upload RBAC), `-editor`, `-trace`,
  `-off`, and gallery page `editor`. CI: a `visual policy editor` entry.

### Noticed, not changed

- The visual view's debounced validate and the Validate button both call
  `POST …/validate`; the button still fills 0053's results tab.
- A route's own `port` is read-only: the compiler does not check it, so
  there is no server check for the field to mirror yet.
- `rule.id_duplicate`'s `other_line` from the console's check is the other
  rule's `- id:` line, as the compiler reports it.

### Cross-repo

No shared surface changed (`ext`, `server`, the contract and the cross-repo
protocol are untouched; no API version move). The component set
Enterprise's screens build from gained `SortableList`, `ChipEditor` and
`ViewLink.query`, and DESIGN.md §7/§8/§11 changed, so a downstream sync for
`hoplock/enterprise` is queued:
`prompts/downstream/queued/control-PR#176-console-visual-policy-editor.md`.
Proxy: none.
