# 0068 — the console's visual claim-mapping editor — Learnings

## Summary
- **Shipped (`v0.55.0`, MINOR; no migration; north-bound API `8`→`9`, `north_api_min` `1`; `ext`/`server`/contract untouched):** `/console/inventory/claim-mapping` edits a draft of the next version visually (default) or as source (`?view=source`, one text); the dry run checks it as typed, rejections land on their field; "What would this produce?"; a change summary; the write typed-confirmed with the tenant's id.
- **Document model (0066's, shared):** the text-splice core moved out of `policyDocument.ts` into `ui/src/screens/yamlDocument.ts` (one `yaml` 2.9.1, unchanged output; 0066's 55 tests unchanged and green). `claimMappingDocument.ts` = `mappingOf` (rows), `applyMappingEdit` (splices), `checkMappingField`, `rejectionTarget`, `mappingChanges`.
- **Routes:** `POST …/identity/claim-mapping/validate` (identity:read) `{document, samples?}` → the PUT's own refusal, or `{digest, attributes, groups, directories, require_directories, group_map, samples}`; a sample `{claims, groups, directory_source?}` or `{subject}`. `GET …/identity/directories` → `{directories: [{source, linked, deactivated}], truncated, next}` by source (M26).
- **Chosen here, flagged in the PR for review** (asked; the user did not pick): a `{subject}` sample (mapped from what its directory last wrote, via `provision.Mapped`, the recalculation's own path) is answered to **identity:write only** (`403` otherwise): raw directory data is otherwise unreadable here, and a crafted draft would read it back. Typed samples are every reader's.
- **Server rules → console:** `identity.Rules()` + `DirectorySourcePattern` → `ui/src/screens/claimMappingRules.json` (`HOPLOCK_UPDATE_MAPPING_RULES=1`), each field's code proven against `ParseMapping` by `TestTheConsolesMappingRulesAreTheServers`; `test/claimMappingVisual.test.ts` refuses a hand-written rule or option list.
- **Not modelled (shown read-only, "edit in source, line n"):** any top-level key but `schema_version`/`description`/`claims`/`groups`; claim keys but `claim`/`attribute`/`values`; `groups` keys but `claim`/`require_directories`/`map`; entry keys but `external`/`group`/`directories`. A flow-style `claims`/`map` list is read and edited per field, never added to, removed from or reordered.
- **Components:** `TextField` (0066's apply-on-leave field, moved into the set) and `Suggestions` (a datalist). Gallery `editor` gains "text field, applied". **`ui/DESIGN.md` §7 and §11 changed** (Details).
- **Decisions:** none added or amended; register unchanged. PLAN M19's version paragraph, §6 "The directory door" and §10 (0068's row) revised in place.
- **NEXT:** a third document editor builds on `yamlDocument.ts`. A key added to the mapping schema fails `TestTheConsolesMappingCorpusParses` until `ui/test/mapping-corpus.yaml` uses it. Candidate: a list of past mapping versions and a rollback (the API lists none).

## Details

### The routes, and why each is shaped so

- **The dry run refuses as the PUT refuses.** Both call `refuseMapping`
  (`internal/httpapi/north/claimmapping.go`), so `TestTheDryRunRefusesAsTheWriteRefuses`
  compares status, code, message and parameters over seven documents. An
  empty document is the PUT's `{field: document, problem: required}`.
- **Its own refusals come before the document's**: more than
  `north.MaxMappingSamples` (16) is `{field: samples, problem: too_many,
  limit: "16"}`; a `directory_source` outside the source pattern is
  `samples[i].directory_source invalid`; a `subject` beside anything else is
  `samples[i].subject invalid`. Then the subject sample's permission check
  (`403 forbidden`, `permission: identity:write`, before the store is read,
  so a non-writer learns nothing about which ids exist), then the parse, then
  the subject's link (`invalid` when no directory provisioned it).
- **A sign-in sample's groups** are offered under `groups.claim` as a
  broker's `MultiClaims`; `identity.Mapping` gained `GroupClaim()` for it. A
  directory sample's `email` claim is passed as the directory's email, the
  rest as attributes — exactly the sink's call.
- **`provision.Mapped`** is new and `derive` now calls it, so the preview and
  the recalculation cannot disagree.
- **`group_map` is always present on the dry run** (`[]` when none): it is
  a new route, so no older client reads it and nothing needs the read's
  `since:"5"` absence.
- **Directory sources** union `directory_links` and `directory_groups`
  (a source with only a group, even a tombstone, is listed with zero counts),
  as `Sources`/`HasSource` already did. `linked` counts provisioned links
  (deactivated included), `deactivated` those deactivated; a deactivation
  ahead of its create counts in neither. The order is `source`, the leading
  key column of both primary keys after the tenant. Held in
  `TestEveryListIsBoundedOrSaysWhyNot`, the store's paged-reader harness
  (`store.DeleteDirectorySource` is a test-only delete for it), the tenancy
  and isolation tables.
- **Version `9`** is one row in `docs/API-VERSIONING.md` naming both routes
  and every shape above.

### The screen

- **One draft, two views.** `Mapping` (in `ClaimMapping.tsx`) holds the draft
  text, keyed by the active digest so a write starts a fresh draft. The view
  links are a query (`?view=source`), as 0066's are. `?edit=1` is gone: the
  editor is always there, and a draft that equals the active document is
  just the active version shown editable.
- **0055's read-only entries table is folded into the visual view**, as the
  prompt allowed: the facts panel keeps a count (`data-group-map` carries the
  entries for tests), and the entries themselves are the visual view's.
- **The check** is the dry run, 400 ms after the last edit
  (`data-mapping-check`: `pending`, `valid`, `invalid`, `failed`). The write
  is offered only while the draft differs from the active version and the
  check said `valid` for exactly this text. A check that could not be read is
  the partial notice, never a pass.
- **Rejections** go through `rejectionTarget(field)`: a claim's or an entry's
  field gets the server's sentence as its `error` (so `aria-invalid`); a
  `values[j]` or `directories[j]` the chip editor's error; anything else
  heads the view as 0055's list. A refused write keeps its rejections for
  that text (`writeRefused`) and says nothing was written.
- **Groups by name.** `namesOf` groups entries in the order each name first
  appears; within a name, the document's order. An entry offers only the
  directories no other entry of that name lists (one listed twice is
  `mapping.directory_duplicate`). "Add an entry for a directory" is a
  disclosure, and says when every source is already listed.
- **The preview** asks the dry run twice, the active document and the draft,
  with the same sample, and marks each difference by sign and word on the
  diff's grounds (§3.8), never by tint alone. The identity list is the first
  page (100) of identities, directory-managed, loaded for writers only.
- **The change summary** keys a claim by its attribute (one attribute has
  one source) and an entry by its name and its set of directories, so a
  change of scope reads as one entry removed and another added. A changed
  document whose rows are equal (spacing, a comment) says so rather than
  "nothing".

### The document model

`yamlDocument.ts` is 0066's `policyDocument.ts` core moved, not rewritten:
`parseYaml(source, keep?)` (the policy's "never empty a rule" rule is now
`keep`, passed by `parsePolicy`), `serialise`, `attempt` (the comment check
`applyEdit` did), `setAt`, `appendItem`, `removeItem`, `itemSpan`, and the
list ops generalised from the rules list: `layoutOf`, `moveItem`,
`removeSpan`, `spanComments`. Two behaviours changed, both additive: a map
appended to an empty flow list (`claims: []`) becomes a block list, as one
appended under an empty key does (it was refused); and the last item's
removal writes `key: []` for any list (it was the rules list's alone).

### Changes to `ui/DESIGN.md`, and why

1. **§7 inventory:** text field (it existed inside 0066's screen; 0068 needed
   it too, and §7 says none is built inside a screen) and suggestions.
2. **§7 "One component per job":** a mapping's claims are the sortable list
   — their order maps nothing but is the document's.
3. **§11:** one document core over the one YAML library, shared by both
   editors, replacing "a second editor uses the library too".

### 0060's consistency checklist, applied here

1 one component per job — every control from the set; `TextField` and
`Suggestions` joined it rather than living in the screen; **found and
fixed:** a row error painted `--s-deny` in screen CSS (consistency check 10
caught it) is a `Notice` now. 2 status vocabulary — the check as
`Valid`/`Invalid`, stale as `In progress`; preview marks are words, not
statuses. 3 page anatomy — the header has no primary action (the write is
the document's, danger-styled); the views are view links. 4 four states —
loading (the visual skeleton), error, partial (stale, connectors and
directories unreadable, the check unreadable); empty does not exist (a
tenant always has a mapping). 5 lists — connectors stay a data table; claims
are the sortable list (§7, changed). 6 mutations — the write confirms typed
and leaves a receipt; removing a row is reversible (it is a draft) and
announced. 7 identifiers — every claim, attribute, group, source mono, LTR,
`translate="no"`. 8 words — "identity", "refused" for the server's refusals.
9 keyboard — tested (`claim-mapping.spec.ts`). 10 links — "Edit in source,
line n". 11 absent — a value list not written reads "any value". 12 width —
the widths job runs over the six entries. 13 by eye — the PR's screenshots.
14 `DESIGN.md` and `ui/` agree — §7, §11 updated. **Found and fixed by eye:**
panels nested three deep (the anti-goal in §1) — the draft is now a heading
over page-level panels, and a name's entries are rule-separated, not boxed.

### Tests

- Go: `TestTheDryRunStoresAndRecordsNothing`, `…RefusesAsTheWriteRefuses`,
  `…MapsEachSampleAsTheServerWould` (Apply and ApplyDirectory, compared to
  the mapping's own answer and to literals), `…RefusesSamplesOutsideItsBounds`,
  `TestADryRunSampleMayNameADirectoryIdentityForAWriter`,
  `TestTheEditorsReadsAreIdentityReadAndTheWriteIsNot`,
  `TestTheDirectorySourcesAreListedWithTheirIdentities` (north);
  `TestTheSourcePageCountsEachDirectorysIdentities` (store);
  `TestTheConsolesMappingRulesAreTheServers` (mutation-checked: a wrong code,
  a wrong `trimmed`), `TestTheConsolesMappingCorpusParses` (mutation-checked:
  the corpus without `description`).
- Unit: `claimMappingDocument.test.ts` (29: round trips, each edit's exact
  line diff, every move and back, comments, checks, targets, changes);
  `claimMappingVisual.test.ts` (10: no hand-written rule, option or
  `<option>`; mutation-checked).
- Browser: registry entries `inventory-claim-mapping` (states and two
  RBAC-checked calls: the write, and the identity-naming preview),
  `-source`, `-refused`, `-off`, `-preview`, `-confirm`;
  `claim-mapping.spec.ts` (checks) and `claim-mapping-mutations.spec.ts`
  (the acceptance flow, which writes the seeded mapping back at its end).
  CI: a "visual claim-mapping editor" entry.
- **The fixture** gained one directory group for dana (`Platform Guild`,
  `corp`), which the seeded mapping does not map, so the write's scoped entry
  has someone to give a group to.

### Noticed, not changed

- **A list of past mapping versions and a rollback** would be the operator's
  next ask: the API lists none, so the preview can compare the draft with the
  active version only.
- The identity a writer previews is chosen from the first page of
  identities; a tenant with more than 100 needs a search, which the
  subjects list has no filter for (directory-managed).
- The preview's identity sample maps the directory's own data; an identity's
  local group membership merges in beside it (§6) and the panel says it is
  not shown.

### Cross-repo

No shared surface changed (`ext`, `server`, the contract, the cross-repo
protocol). The north-bound API moved to `9` (two routes), the component set
Enterprise's screens build from gained `TextField` and `Suggestions`, and
`ui/DESIGN.md` §7/§11 changed, so a downstream sync for `hoplock/enterprise`
is queued (see the PR's `## Cross-repo impact`). Proxy: none.
