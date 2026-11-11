# 0067 — Management console 14: a visual claim-mapping editor

## Read first
- `docs/PROTOCOL.md` — session workflow, and §8's "A console phase shows what
  it built".
- `docs/PLAN.md` — §2: **M7** (identity is federated, and claims and groups
  reach policy only through an explicit, versioned mapping — the document this
  phase edits), **M4** (a decision record names the mapping version it was
  taken under), **M2**, **M11**, **M19** (a new route or member moves the
  north-bound version), **M20**, **M21** (claim names, attributes, group
  names and directory sources are data, never translated), **M23** and
  **M26** (a new list is bounded by its caller); §6 at "Federation (M7)"
  (what a mapping may express — exact keys, no pattern, no wildcard, no
  transformation language — and why a stored mapping that does not parse is an
  outage) and at "The directory door" (scoped entries, `require_directories`,
  and how a new version reaches every directory identity by itself).
- **`ui/DESIGN.md` — in full.** §2 (the dangerous action looks dangerous), §7
  on composites, keyboard paths and the confirm dialog, §8 (a drag has a
  keyboard equivalent), §9 on identifiers and operator data.
- `docs/learnings/` — read summaries; open **in full**
  `0055-console-standing-denials-and-directory` (the claim-mapping screen this
  phase changes, `group_map`, the rejection sentences in
  `claimMappingCodes.ts` held to `ui/test/mapping-codes.json`, the fixture
  host and its two directories, and its "Noticed, not changed": a write
  activates at once and is not confirmed) and
  `0066-console-visual-policy-editor` (the comment-preserving YAML document
  model it chose and pinned, and the sortable list and closed-set chip editor
  it added to the component set). Read the summaries of `0011` (the mapping
  document and its rejections), `0043` (directory-scoped entries, the three
  directory rejections, the recalculation) and `0060` (the consistency
  checklist, which ran before this phase); `0049` and `0050` for registering a
  screen with every harness.

## Why after 0066
The operator asked for this after 0055 shipped the claim-mapping screen with
its document as YAML: "turn that into a more visual configuration". It is
queued after 0066 because 0066 chooses and pins the comment-preserving YAML
document model and adds the sortable list and the chip editor bound to a
closed set; this phase uses those and adds no second YAML library. Like 0066,
it runs after **0060's consistency audit**, so it holds its screen to 0060's
checklist and tests and says so in its learnings.

## Objective
On `/console/inventory/claim-mapping`, show and edit the mapping as what it
is — claims that become attributes, and group names that become local groups,
some scoped to the directories they come from — so an operator configures it
without writing YAML, and sees before activating what it changes and what it
would produce. The result is the document the API stores, byte for byte where
it was not edited. **Nothing about storage, the schema or the mapping's
semantics changes: a version is still its document and that document's
digest.**

## In scope
- **The Document section becomes a visual configuration**, with the YAML kept
  as the second view of the same document (`?view=source`; the visual view is
  the default). An edit in either is the document. A document that does not
  parse disables the visual view and shows its rejections, as the source does
  today. 0055's facts, group-entries and connectors panels stay; the phase may
  fold the read-only entries table into the visual view and says so.
- **Claims → attributes.** One row per `claims` entry: the claim it reads,
  the attribute it produces, and its permitted `values` as chips (none means
  any value). Rows are added, removed and reordered by drag **and** by keyboard,
  each announced; order means nothing to matching but is kept, because the
  document is what is stored. A reserved attribute (`chain_hop_proxy_id`,
  anything under `hoplock.`) is refused where it is typed.
- **Groups → local groups.** The group claim; `require_directories` as a
  switch with 0055's sentence for each position beside it; and the entries
  **grouped by the external name they read**, so the entry that serves
  sign-ins and every entry scoped to a directory are seen together for one
  name, each row saying what it serves (0055's "Sign-ins, and every directory
  not listed for this name" or "Sign-ins only"). Directories are chips chosen
  from the sources the tenant holds; a local group is offered from the
  tenant's groups (`GET …/groups`) and may be new.
- **The tenant's directory sources.** No route lists them today (the CLI reads
  `store.DirectoryLinks().Sources`). Add `GET …/identity/directories`
  (`identity:read`): each source a directory has written in the tenant, with
  its counts of linked and of deactivated identities, paged by source (M26),
  its cursor binding nothing; held in `TestEveryListIsBoundedOrSaysWhyNot`.
  The alternative — a typed source checked against the pattern — is allowed
  only with the reason in the learnings.
- **Checked as typed, decided by the server.** Each free-text field (claim,
  attribute, external name, group, source) is held where it is typed to the
  server's own pattern and reserved set, generated from `internal/identity`
  into a file the console builds from and held to it by a Go test, as
  `ui/test/mapping-codes.json` is; no pattern or option list is written by
  hand. The server stays the authority: every `mapping.*` rejection renders on
  the row and field its `field` path names (`groups.map[2].directories[0]` →
  that entry's chip), from `claimMappingCodes.ts`; one about the whole
  document heads the view.
- **A dry run, which the API does not offer today.** Add
  `POST …/identity/claim-mapping/validate` (`identity:read`), which stores and
  records nothing. Body `{document, samples?}`. A document that does not parse
  is answered as the `PUT` refuses it (`400 invalid_request`,
  `parameters.rejections`); one that parses is answered `{digest, attributes,
  groups, directories, require_directories, group_map, samples}`, each sample
  `{claims: {name: value}, groups: [name], directory_source?}` answered with
  the `attributes` and `groups` this document produces for it — through the
  server's own `Mapping.Apply` for a sign-in and `ApplyDirectory` for a
  directory (M7: one mapping serves both doors). Both new routes move the
  north-bound version once (M19, `docs/API-VERSIONING.md`).
- **What it would produce, and what it changes.** The editor validates as the
  operator works (debounced), and a **"What would this produce?"** panel takes
  a sample — an existing identity to start from, or claims and groups typed in,
  through sign-in or a chosen directory — and shows the attributes and groups
  under the active version and under the edited one, side by side, the
  difference marked. A change summary lists, by row, what the edit adds,
  removes and changes against the active version.
- **Writing is an activation, and confirms as one** (DESIGN.md §2; 0055 noted
  it did not). The write shows the change summary, says that every directory
  identity is brought onto the new version in the background and every
  sign-in from now on is mapped by it, and confirms through `ConfirmDialog` by
  typing the tenant's id, its hint saying the decisions taken under the old
  version are not taken back. Offered to `identity:write` only.
- **Lossless round trip,** by 0066's rule: the editor works on the source's
  document, never a re-serialised copy, so an edit never drops a comment,
  reorders unrelated keys or changes a value it did not touch. A key the visual
  view does not model is shown read-only with "edit in source", never dropped.
- **Components** the editor needs that the set lacks join it with every state
  in the gallery (0050); none is built inside the screen. Reuse 0066's
  sortable list and chip editor.

## Out of scope
- Any change to the mapping's schema (`schema_version` stays `1`), its
  semantics (no wildcard, no pattern, no transformation language — §6), the
  recalculation, or how a version is stored.
- Connectors and their ties, which the CLI writes; 0055's connectors panel is
  unchanged.
- A list of past mapping versions and a rollback (the API lists none): note it
  as a candidate in the learnings if the operator would want it.
- Any locale other than English.

## Acceptance criteria

**Function**
- **A round trip is lossless.** For the fixture's mapping (`MAPPING` in
  `ui/e2e/fixture/standing.ts`) and a corpus this phase adds that uses every
  key at least once — claims with and without `values`, scoped and unscoped
  entries for one name, `require_directories`, a description, comments —
  loading into the visual view and serialising back yields byte-identical
  source, and after one edit the document differs only in the key edited, with
  no comment lost. This runs as a test.
- **End to end, headless:** an operator adds a claim that becomes an attribute
  with two permitted values, scopes an entry to a directory chosen from the
  tenant's sources, sees the preview for a provisioned identity of that
  directory gain the new group, reads the change summary, writes it through
  the typed confirm, and the screen's active version and `group_map` show it.
- Each `mapping.*` code the server answers renders on the row and field it
  names; a refused write writes nothing.
- The validate route stores nothing and records nothing (a test), refuses as
  the `PUT` refuses, and answers each sample as the server's own mapping code
  would map it (a test over a sign-in and a directory sample).
- **Every pattern and reserved name the editor checks is the server's** (a Go
  test over the generated file), and no option list is written by hand (a test
  over the source).
- **RBAC:** every role holding `identity:read` may edit visually, validate and
  preview; only `identity:write` is offered the write; the API's refusal of
  the write is asserted, not only the hidden button.

**Design and localisation**
- The screen is registered with every browser check in each of its states
  (both views, a refused document, the preview, the confirm), and each passes:
  axe, the screenshot baseline (both themes, loading, empty, error, partial),
  offline, reduced motion, and both pseudolocale runs. Claim names,
  attributes, groups and sources stay LTR, mono and untranslated.
- Adding, removing, reordering and editing are each fully keyboard operable,
  and each announces its result.
- 0060's consistency checklist, applied to this screen, passes. The learnings
  record anything it found.

## Definition of Done & hand-off
**Screenshots in the PR** (`docs/PROTOCOL.md` §8): every state, light and dark
side by side, both pseudolocales, the preview panel and the confirm, and the
base's claim-mapping screen beside the head's.

Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0067-console-visual-claim-mapping-editor-learnings.md`. Its
summary block names the YAML document model reused from 0066, the two routes
and their shapes, how the server's patterns reach the console, and what the
visual view does not model, so a later phase can extend it. Record any change
to `ui/DESIGN.md` with its reason. The new routes and any change to the
component set owe a downstream sync for `hoplock/enterprise`
(`docs/CROSS-REPO-PROTOCOL.md` §4.1).

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
