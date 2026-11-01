# 0043 — Directory-scoped group entries: a claim-mapping group entry lists the directories it applies to

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/51, under `## Upstream request`,
> item 2, from its request file
> `prompts/upstream/queued/enterprise-PR#51-directory-group-scope.md`, and
> answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2). The same
> PR's item 1, a directory named on the sync sink, is 0042, the phase this one
> builds on.
>
> **Where it sits.** Right after the directory source (0042), and before
> reports (0044) and everything after them. It needs a directory group's
> source, which 0042 adds, and that is its only dependency. The group-scope
> renumbering note in `docs/PLAN.md` §10 gives one line of reasons for each
> neighbour.

## Read first
- `docs/PROTOCOL.md` — session workflow. Read §3 for revising the plan in
  place and for the downstream hand-over this phase owes, then §4 and §5.
- `docs/CROSS-REPO-PROTOCOL.md` — **§4.1**, **§4.3**, and **§5** at "The PR
  that answers an upstream request is not a sync". This phase answers a
  request and changes what the claim mapping accepts and does, so it owes a
  downstream sync (below).
- `docs/PLAN.md`:
  - **§2**, the register first, then:
    - **M7**, in full: the mapping is an allow-list, versioned and named in
      the decision record, and the directory is a second door the same
      mapping governs. This phase changes how a group entry matches. It
      keeps every one of those properties, and M7 is revised in place rather
      than amended.
    - **M4** (the decision record names what decided, whole), **M5** (the
      decision path's budget), **M28**'s cost paragraph (the decision path
      reaches the directory's link row by a join in the subject's statement,
      which this phase reads one more column from, and adds no join to),
      **M11** (an unreadable mapping is an outage, never an empty one),
      **M21** (rejection codes are stable and rendered by the console),
      **M23** (every phase is a release), and **M12** and **M18** (a mapping
      is per tenant).
  - **§3**, "Component responsibilities": `internal/identity`,
    `internal/provision` and `internal/decision`.
  - **§5.6**, at "Explain": the claim-mapping version a decision was taken
    under and the active one.
  - **§6**, "Federation" and "The directory door", whole.
  - **§10**: this phase's row and the group-scope renumbering note.
- `docs/learnings/` — read every summary. Open
  **`0042-directory-source-on-the-sync-sink-learnings.md`** in full: where a
  group's source is stored, how a subject's groups resolve within its own
  source, and the source pattern and its validator, which this phase reuses
  and never copies. Open the claim-mapping part of
  **`0011-identity-federation-and-credentials-learnings.md`**, and the Details
  of **`0040-identity-sync-and-deprovisioning-learnings.md`** on how a
  provisioned subject is derived through the mapping.
- Code. Read it before designing anything:
  - `internal/identity/mapping.go` whole: `mappingDocument`,
    `mappingGroupsEntry`, `mappingGroupEntry`, `ParseMapping`, the
    `RejectMapping*` codes, `MaxMappingEntries`, `Apply`, `ApplyDirectory`,
    `MappedGroupNames`; and `mapping_test.go`;
  - `internal/provision/subject.go`: `derive`, the one caller of
    `ApplyDirectory`;
  - `internal/identity/federation.go`: `ActiveMapping` and `PutMapping`;
  - `internal/daemon/identityctl.go`: `runMappingPut`, `runMappingShow`,
    `printMapping`;
  - `internal/httpapi/north/ca.go`: `claimMappingResponse` and
    `handleClaimMapping`; `inventory.go`: `handleClaimMappingPut`;
  - `internal/store/standing.go`: the subject's standing read, its
    `directory_links` join and the `source` 0042 selects there;
    `internal/decision/inputs.go` and `record.go`: `recordedSubject` and
    `newRecordedInputs`; `explain.go`: `MappingFacts`.
- The request itself, for the requester's own words. The Enterprise decisions
  it turns on are **E3** (Enterprise builds against a pinned release, never
  `main`), **E4** (a feature degrades honestly when it cannot work) and
  **E11** (Control owns tenancy's mechanism, and Enterprise owns which
  directory may write which tenant). The phase that consumes this one is its
  0004. Read any of them in `hoplock/enterprise` only if a choice here turns
  on it. Each is cited by id and never restated.

## Context estimate
- **Reads:** this prompt, M7, M28's cost paragraph, §6's two identity
  sections, the summaries, 0042's learnings, and the code above (~1 200
  lines, `mapping.go` half of it).
- **Changes:** `internal/identity/mapping.go` (two keys, matching, three
  rejection codes, one accessor) and its tests; `internal/provision` (one
  argument); `internal/decision` (one member on the record's subject, and
  `explain`); `internal/httpapi/north` (two members on the mapping read);
  `internal/daemon` (the mapping commands' output); PLAN M7, §3, §6 and §10's
  row; three queued prompts; `CHANGELOG.md`.
- **Why it fits:** no route, no migration, no `ext` change, no new decision,
  and nothing added to the decision path but one column of a row it already
  reads. The matching rule is a lookup in a map keyed one way more than
  today's.

## Objective
Let a `groups.map` entry in a tenant's claim mapping list the directories it
applies to, so that a directory's groups reach only the entries that list
that directory, and so that one entry can map a group several directories
share. Let a tenant require that every directory group match only such an
entry.

Today, at `v0.27.0` and on `main` (and after 0042, which changes nothing in
the mapping):

- a directory group reaches policy by its `Name` alone, through the tenant's
  one `groups.map`, the same entries a login's group claim goes through
  (`Mapping.ApplyDirectory` offers the names under `groups.claim` and calls
  `Apply`);
- so two directories feeding one tenant whose groups share a name get the
  same mapping: an IdP that names a group `admins` gets whatever `admins` maps
  to, whichever directory the mapping author meant;
- and a group several directories are meant to share can be mapped once only
  by an entry every directory reaches, including any connected later.

So once two directories feed one tenant, either can reach what the mapping
gives the other's group of the same name, and Enterprise could avoid it only
by renaming groups into namespaced strings for the mapping author to match.
Enterprise's 0004 feeds a tenant from one directory until this and 0042 have
both landed (E4).

After this phase the mapping document says which directories each group
entry applies to, a tenant can make that mandatory for directory groups, and
a tenant that uses neither key maps exactly as it did.

## The request, in this repository's vocabulary
- *Asked for*, verbatim from enterprise#51:

  > ```yaml
  > schema_version: 1
  > groups:
  >   claim: groups
  >   require_directories: true      # optional: see the last point below
  >   map:
  >     - external: BigCorporationAdmins
  >       directories: [de, fr, jp]  # that group from any of these, and no other
  >       group: corp-admins
  >     - external: admins
  >       directories: [de]
  >       group: de-admins
  >     - external: admins
  >       directories: [fr]
  >       group: fr-admins
  >     - external: engineering      # no directories: matches as at v0.27.0
  >       group: engineering
  > ```
  >
  > - **`directories`** on an entry lists one or more sources (request 1). The
  >   entry matches a directory group of that `Name` from any directory it
  >   lists, and never a login's group claim. So one entry maps a group every
  >   listed IdP carries to one local group, and a directory it does not list
  >   gets nothing from it.
  > - **No wildcard.** A directory added later reaches an entry only once
  >   someone lists it, so a new IdP never inherits the right to assert an
  >   admin group by being connected. Control may add one if it judges
  >   otherwise; this request does not ask for it.
  > - **An entry without `directories`** matches as at `v0.27.0`: a login's
  >   group claim and every directory's groups. A tenant with one directory
  >   changes nothing.
  > - **Precedence:** for a group named N from source S, an entry listing S
  >   wins over one listing none. A source listed twice for the same
  >   `external`, in one entry or across two, is a rejection. So are two
  >   entries with the same `external` and no `directories`.
  > - **Requiring scoping:** `groups.require_directories: true` makes every
  >   directory group match only an entry listing its own source, so unscoped
  >   entries serve logins only. Without it, a new directory still reaches
  >   every unscoped entry. Control may prefer another way for a tenant to
  >   require scoping, for example unscoped entries ceasing to match directory
  >   groups once more than one source has written to the tenant. The request
  >   accepts any, provided a tenant can make each directory's groups reach
  >   only the entries that list it.

- *Not asked*, in the request's words: attributes, which Enterprise's 0004
  keeps apart per directory with its own claim rules; and two untied
  connectors whose login group claims share a name, which is Control's own
  multi-connector login, "though a shape that also lets an entry list
  connectors would cover it".
- *What stays broken until it lands*: once two directories feed one tenant,
  either can reach what the mapping gives the other's group of the same
  name, and a group meant for several directories maps once only by
  matching every directory.

## What this phase answers, and where it differs from the ask
It is **met as asked**. Nothing in it contradicts an M decision: the mapping
is still an allow-list of exact names with no pattern and no transformation
(M7, §6), still validated when it is written, and still versioned and named
in the decision record. A list of directory names on an entry is one more
exact key to match, not a rule language. These are the cases the request
left to Control, settled:

1. **The keys are the request's.** `directories` on a `groups.map` entry, a
   list of directory sources, and `require_directories` under `groups`, a
   boolean defaulting to false. Each listed name is a directory source as
   0042 defines it, checked by 0042's validator. `schema_version` stays `1`:
   both keys only add, and a document that uses neither is read exactly as
   before.

   A build without this phase reads a document that uses either key as
   unparseable, because the mapping is decoded strictly. During a rolling
   upgrade, an older node that reads such a mapping as the tenant's active
   one therefore refuses that tenant's logins and directory writes as an
   outage (§6, M11), and never applies the mapping unscoped. That is the
   safe direction, and the changelog says it: finish the upgrade before
   writing a mapping that scopes.
2. **How a group matches.** For a group named N that a directory of source S
   holds for a subject, the entry that lists S under `external: N`, if one
   does. Otherwise, unless `require_directories` is set, the entry with
   `external: N` and no `directories`. Otherwise nothing. A login's group
   claim matches entries with no `directories` only, and a scoped entry
   never matches a login. S is the source of the group's row (0042), which
   is the subject's own, since a subject's groups resolve within its source.
3. **"Wins over" means instead of, never as well as.** Where an entry lists S
   for N, the unscoped entry for N does not also apply to S's group. So
   `admins` from `de` maps to `de-admins` alone, and not also to whatever
   unscoped `admins` maps to. If both applied, scoping could add a group but
   never take one away, and the clash the request exists to close would
   stay open.
4. **Rejections**, each found when the mapping is written, and all of them
   at once, as today. Three codes are added, and none reuses an existing one
   (the rule at the head of the codes in `mapping.go`):
   - `mapping.directory_name`: a listed name that is not a valid source;
   - `mapping.directories_empty`: `directories: []`. An empty list reads as
     "no directory" to one person and "every directory" to another, so it is
     refused rather than given either meaning. An unscoped entry is written
     without the key;
   - `mapping.directory_duplicate`: a source listed twice for the same
     `external`, in one entry or across two. The message names the entry
     that listed it first.

   `require_directories` anywhere but under `groups` is an unknown key, which
   the strict decoder already refuses (`mapping.unparseable`), so it needs no
   code of its own.

   Two entries with the same `external` and no `directories` stay
   `mapping.group_duplicate`. A scoped entry and an unscoped one with the
   same `external` are not a duplicate: that is item 3's precedence.
5. **The bound counts what is matched.** `MaxMappingEntries` counts a scoped
   entry once for each directory it lists, so the bound holds over the
   `(external, source)` pairs a document matches as well as over its lines.
   An unscoped entry counts once, as now, so every document valid today
   stays valid.
6. **No wildcard**, as asked. A directory connected later reaches no scoped
   entry until somebody lists it. Under `require_directories` it reaches
   nothing at all until then, which is the safe direction for a new IdP.
7. **Requiring scoping is the explicit key, and the alternative is
   rejected.** The request offered Control a rule that unscoped entries stop
   matching directory groups once a second source has written to the
   tenant. That would make what one mapping version maps depend on state
   outside the document. The version a decision record names (M4, M7) would
   no longer say what it mapped, and a second directory's first write would
   change what the first directory's groups map to, with no mapping written
   and no `operator.claim_mapping.activated` record. The key is in the
   document, so it is versioned, reviewed, validated and recorded like the
   rest. `require_directories` with no scoped entries is accepted: every
   directory group then maps nothing, which is a legitimate step while
   entries are written, and the safe direction.
8. **A mapping still reaches a provisioned subject at its next applied
   write**, as §6 says, scoped or not. So a mapping that adds scoping, or sets
   `require_directories`, takes nothing back from a directory subject until
   the directory next writes that subject, exactly as removing an entry does
   today. This phase does not re-derive a tenant's directory subjects when a
   mapping is written. That would make every mapping write, scoped or not, a
   write to every directory subject in the tenant under the mapping's lock,
   and nobody asked for it. `identity mapping-put` says so in its output when
   the document names any directory, the changelog says so, and Enterprise's
   sync is told (Cross-repo impact): the directory's next full push is what
   applies a tightened mapping to everyone.
9. **The decision record names the directory a subject's groups came from
   (M4).** With scoped entries, "why is Alice in `corp-admins`" depends on
   two things: the mapping version, which the record names, and the
   directory Alice's groups came from, which it does not. So the record's
   subject gains `directory_source`, the source of the subject's link row. It
   is omitted for a subject no directory provisioned, and absent on records
   written before this release. It is not the subject's existing `source`,
   which names the connector or the sync's provider (0042's item 1).

   It costs the decision path nothing (M5). The subject's statement already
   joins `directory_links`, and 0042 has it select `d.source`. This phase
   records that value: no statement, no join and no column read beyond it.
   `TestStandingDenialsCostTheDecisionPathNoRoundTrip` holds unchanged.
   `explain` shows it beside the mapping version.
10. **What the mapping read shows.** `GET …/identity/claim-mapping` gains
    `directories`, the sorted sources the active mapping names, empty when it
    names none, and `require_directories`. `groups`, the local groups the
    mapping can produce, includes those of scoped entries. The `PUT`'s answer
    is unchanged. `identity mapping-put`, `--dry-run` included, and
    `identity mapping-show` print both.

    `mapping-put` without `--dry-run` also names every listed source that
    nothing in the tenant has written yet, using 0042's read of the tenant's
    sources. That is a line of output, never a refusal. A mapping is written
    before the directory it scopes, so that the directory's first write is
    already scoped. Refusing an unwritten source would force the reverse
    order, in which a new directory's first writes go through the unscoped
    entries. A tie to an unwritten source is refused (0042's item 6), and
    this is different: a tie to an unwritten source refuses every login, while
    a scoped entry for one maps nothing until the directory writes. Validation
    itself stays pure, so `--dry-run` needs no database.
11. **Nothing else changes.** No `ext` signature: `ApplyDirectory` is
    internal, and the sync never sees the mapping. No route, no migration
    (the document is stored as it was written), no record kind, and no new
    decision: M7 is revised in place, so the register is unchanged.

## In scope

### 1. `internal/identity/mapping.go`: two keys, the match, three codes
- `mappingGroupEntry.Directories []string` (`directories`) and
  `mappingGroupsEntry.RequireDirectories bool` (`require_directories`).
- `ParseMapping` validates them (items 1, 4 and 5). Each rejection names its
  field as `groups.map[i].directories[j]`, in the existing style.
- `Mapping` keeps the unscoped entries keyed by `external`, as now, and the
  scoped ones keyed by `(source, external)`, and records whether scoping is
  required.
- `Apply`, the login path, matches unscoped entries only (item 2).
- `ApplyDirectory` takes the source as an argument and matches item 2's rule
  for every group name it is offered. Attributes and email are offered as
  claims exactly as now. Scoping attributes was not asked.
- `MappedGroupNames` includes scoped entries' groups. A new
  `MappedDirectories` returns the sorted sources the mapping names.
- The file's head comment says what `directories` is and why it is matching
  and not transformation. `ApplyDirectory`'s doc comment states item 2's rule
  and item 3's "instead of".

### 2. `internal/provision`
`derive` passes the link's source to `ApplyDirectory`. Nothing else in the
sink changes. In particular, a mapping write still re-derives nobody (item 8).

### 3. `internal/decision`: the record's subject
`recordedSubject` gains `DirectorySource` (`directory_source`, `omitempty`),
filled from the link row the standing read already returns (item 9).
`explain` renders it beside the mapping version, and its refusals are
unchanged.

### 4. `internal/httpapi/north` and `internal/daemon`: the mapping read
- `claimMappingResponse` gains `Directories` (`directories`) and
  `RequireDirectories` (`require_directories`) (item 10).
- `printMapping` prints both. `runMappingPut` names the listed sources the
  tenant has not seen yet, and says that a new mapping reaches a directory
  subject at the directory's next write (items 8 and 10).

### 5. The plan, revised in place
- **M7:** where it says one mapping governs the directory door, say that a
  group entry may list the directories it applies to, and that a tenant may
  require it.
- **§6, "Federation":** the allow-list paragraph names `directories` as an
  exact key, and keeps "no prefix rule and no pattern" true.
- **§6, "The directory door":** "one `groups.map` serves login and directory
  alike" becomes item 2's rule. Add items 3, 7 and 8 in a sentence each.
- **§5.6, at "Explain":** the directory source, beside the mapping version.
- **§3:** `internal/identity`'s entry, if it describes the mapping's shape.
- **§10:** this phase's row, revised to say what was built.
- No decision is added or amended, so the register's rows stay as they are.

### 6. The queued prompts this phase changes
Each obligation goes in the prompt that will implement it, not only in the
plan:
- **instance identity (0046):** its north-bound compatibility promise covers
  `directories` and `require_directories` on the claim-mapping read, and
  `directory_source` on a decision record's subject. Add `0043` beside `0042`
  where its "Read first" lists the learnings whose fields the promise covers.
- **the console (0047):** where it shows the claim mapping, it shows each
  group entry's directories and the tenant's `require_directories`, and
  renders the three new rejection codes. Its explain view shows the subject's
  directory source beside the mapping version.
- **the E2E topology (0048):** the two-directory scenario 0042 adds gains a
  group name both directories hold, mapped differently per directory, and an
  entry listing both. It shows that each subject gets its own directory's
  mapping, and the shared one.

### 7. `CHANGELOG.md`
The release's section (M23): the two keys, the three codes, the two members
on the mapping read, `directory_source` on the decision record, and two
operator notes. A mapping that scopes is unreadable to an older build, so
finish a rolling upgrade first (item 1). A new mapping reaches a directory
subject at the directory's next write (item 8).

## Out of scope
- **Scoping attributes** (`claims` entries). Not asked: Enterprise's 0004
  decides per directory which attributes it sends.
- **Scoping by connector**, for two untied connectors whose login group
  claims share a name. Not asked, and Control's own multi-connector login. A
  `connectors` key would sit beside `directories` without disturbing it. Do
  not add one here, and do not build toward it.
- **A wildcard** in `directories` (item 6).
- **Re-deriving directory subjects when a mapping is written** (item 8). If
  that is wanted, it is a phase of its own, and it changes every mapping
  write, scoped or not.
- **Which directory may write which tenant.** That is Enterprise's
  governance (E11), built on M18.
- **The decision path beyond the one column** (M5).

## Acceptance criteria
- A mapping with neither key maps exactly as before. Every existing test in
  `internal/identity`, `internal/provision`, `internal/decision`,
  `internal/httpapi/north` and `server/` passes unchanged.
- **The request's example, end to end.** With the document above, and
  directories `de`, `fr`, `jp` and `us` all holding a group named
  `BigCorporationAdmins` and one named `admins`:
  - `de`, `fr` and `jp` subjects get `corp-admins`, and a `us` subject does
    not;
  - a `de` subject's `admins` is `de-admins` alone, and a `fr` subject's is
    `fr-admins` alone;
  - a `us` subject's `admins` maps nothing;
  - `engineering` maps to `engineering` for all four.
- **Instead of, not as well as.** With an unscoped `admins → all-admins` added
  and `require_directories` unset, a `de` subject's `admins` is `de-admins`
  only, and a `us` subject's is `all-admins`.
- **Requiring scoping.** With `require_directories: true`, the `us` subject
  gets neither `all-admins` nor `engineering`, and a login whose group claim
  carries `admins` and `engineering` still gets `all-admins` and
  `engineering`.
- **Logins never match a scoped entry.** A login whose claim carries
  `BigCorporationAdmins` gets nothing from the entry that lists directories.
- **Rejections.** Each of the three codes is produced, with its field, by a
  document that has that fault and no other. One document holding several
  faults returns all of them. A scoped and an unscoped entry with one
  `external` are accepted.
- **The bound.** A document of 300 entries that each list two directories is
  `mapping.too_large`. One of 512 unscoped entries is accepted, as now.
- **No re-derivation.** Writing a mapping that scopes leaves a directory
  subject's groups as they were. The directory's next applied write for that
  subject applies it, and the subject row names the new version.
- **The record.** A directory subject's decision record carries
  `directory_source`, and `explain` shows it. A federated or local subject's
  record has none. `TestStandingDenialsCostTheDecisionPathNoRoundTrip`
  passes unchanged in its counts.
- **The read.** `GET …/identity/claim-mapping` answers `directories` sorted,
  and `require_directories`. `mapping-put --dry-run` prints both without a
  database. `mapping-put` names a listed source nothing has written, and
  stores the mapping.
- `make release-check` passes, and the release is a MINOR with no
  `### Breaking`.

## Tests
`internal/identity/mapping_test.go`: one table for the match (item 2, item 3
and `require_directories`, each through `Apply` and `ApplyDirectory`), and
one for the rejections and the bound. Postgres-backed on 0042's harness:
`TestADirectoryGroupReachesOnlyEntriesThatListItsSource`,
`TestOneEntryMapsAGroupSeveralDirectoriesShare`,
`TestRequiringScopingLeavesUnscopedEntriesToLogins` and
`TestAMappingReachesADirectorySubjectAtItsNextWrite`, or names that say the
same. `internal/decision` gains a record test for `directory_source`, and
`internal/httpapi/north` one for the read's two members.
`server/provision_test.go` gains one case that drives two sources and a
scoped mapping through a registered sync.

## Release (M23)
The next MINOR after the latest tag. Nothing breaks: the mapping keys, the
read's members and the record's member all only add.

## Cross-repo impact
This phase answers an upstream request and changes what Control's claim
mapping accepts and does, so it owes a downstream sync. Its PR MUST carry
`## Cross-repo impact` (`docs/CROSS-REPO-PROTOCOL.md` §4.1), naming
**every** consuming repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5). It knows what it asked for, but not what
was built, and the session there that rewrites its prompts will be a fresh one.
State at least these obligations:

1. **Its 0004, "What Control does not do yet".** Rewrite the group-scope half
   from what merged, and stop building around it:
   - a mapping author scopes a directory group with `directories`, and makes
     scoping mandatory with `require_directories`. Its claim rules feed that
     one mapping and never replace it;
   - with 0042 also in the pin, the refusal of a second directory for a
     tenant goes, and so does the skip on the criteria for two directories in
     one tenant;
   - its criteria for two directories in one tenant cover the request's own
     example: one group mapped per directory, one shared group mapped once
     from the directories listed, and a directory it does not list getting
     nothing.
2. **What Control decided that it did not ask for**: item 3's "instead of";
   item 7's rejection of the alternative; item 8's rule that a new mapping
   reaches a directory subject only at its next write, so 0004's
   reconciliation is what applies a tightened mapping to everyone; item 9's
   `directory_source` on the decision record, which bears on 0004's
   `explain` criterion; and item 1's note that an older build refuses a
   scoped mapping.
3. **Its plan.** §4's `IdentitySync` bullet, where it says that group names
   reach the mapping with nothing saying which directory they came from, and
   points at the request.
4. **Its pin.** Every obligation holds from the release this PR cuts (M23),
   never from `main`. Name the release by version.

Queue the **"Downstream sync" kickoff** from `docs/KICKOFF.md`, verbatim except
for its blanks, as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`. Commit it
once the PR is open, and end the section by naming that file
(`docs/CROSS-REPO-PROTOCOL.md` §4.3).

### `hoplock/proxy`
Write **"None"**. The contract is untouched (M1), and the proxy consumes
neither the claim mapping nor the north-bound surface. "None" is a finding.

### Who runs the sync
Not you. This PR merges first, and the sync runs afterwards in its own session
(`docs/CROSS-REPO-PROTOCOL.md` §2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0043-directory-scoped-group-entries-learnings.md`.
Enterprise's sync is written from its summary block, which MUST give:

- the two keys, and the matching rule with its "instead of";
- the three rejection codes and how the bound counts a scoped entry;
- that logins match unscoped entries only;
- that a new mapping reaches a directory subject at its next write;
- `directories` and `require_directories` on the mapping read, and
  `directory_source` on the decision record;
- the release, by version.
