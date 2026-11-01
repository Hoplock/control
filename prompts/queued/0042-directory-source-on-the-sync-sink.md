# 0042 — Directory source on the sync sink: every directory identity keyed by the directory it comes from

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/51, under `## Upstream request`,
> item 1, from its request file
> `prompts/upstream/queued/enterprise-PR#51-directory-source.md`, and
> answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2). The same PR's
> item 2, claim-mapping group entries scoped to directories
> (`enterprise-PR#51-directory-group-scope.md`), depends on this one and is a
> request of its own, answered by a session of its own. It is **not** this
> phase (Out of scope).
>
> **Where it sits.** Right after the confined inbound action (0041), and
> before reports (0043) and everything after them. It extends identity sync
> (0040), which is merged, and depends on nothing queued. The
> directory-source renumbering note in `docs/PLAN.md` §10 gives one line of
> reasons for each neighbour.

## Read first
- `docs/PROTOCOL.md` — session workflow. Read §3 for `ext/` and `server/` as
  a compatibility promise, for forward-only migrations, for revising the plan
  in place, and for the downstream hand-over this phase owes. Then §4 and §5.
- `docs/CROSS-REPO-PROTOCOL.md` — **§4.1**, **§4.3**, and **§5** at "The PR
  that answers an upstream request is not a sync". This phase answers a
  request and changes what `ext/` does and says, so it owes a downstream sync
  (below).
- `docs/PLAN.md`:
  - **§2**, the register first, then:
    - **M7**, in full: the directory is a second door the tenant's one
      claim mapping governs. This phase changes how a directory identity is
      keyed and leaves the mapping exactly as it is.
    - **M28**, in full, above all its paragraph on the directory's
      deactivation and its cost paragraph. The deactivation lives on the
      link row, and the decision path reaches it by the unique
      `(tenant, subject_id)` key. This phase keeps that key and that cost.
    - **M15** (the identity point is *disabled* when absent; `ext/` and
      `server/` are a compatibility promise), **M23** (a changed method
      signature on an exported interface is a `### Breaking` MINOR),
      **M4** (the decision record names a standing denial whole), **M5**
      (the decision path's budget), **M12** and **M18** (every table carries
      the tenant, and a sink call acts in one), and **M11**.
  - **§3**, "Component responsibilities": `internal/provision`, and the
    entries for `internal/identity`, `internal/decision`, `internal/store`
    and `internal/httpapi/north` where they mention the directory.
  - **§6**, "Federation" and "The directory door", whole.
  - **§7**, at "Identity provisioning is audited on this server's own chain".
  - **§8**, on migrations.
  - **§10**: this phase's row and the directory-source renumbering note.
- `docs/learnings/` — read every summary. Open
  **`0040-identity-sync-and-deprovisioning-learnings.md`** in full: its
  Details say how the locks are ordered, why delivery is owed durably on the
  link row, and why a group change re-derives its members. Every one of those
  is re-keyed here, and none of them is redesigned.
- Code. Read it before designing anything:
  - `ext/identity.go`: `SyncedSubject`, `SyncedGroup` and `SyncSink`, with
    every doc comment;
  - `internal/provision/`: `sink.go`, `subject.go` (`getLink`,
    `upsertSubject`, `deactivate`, `recordDeactivated`), `group.go`
    (`getGroup`, `upsertGroup`, `deleteGroup`, `rederive`), `validate.go`
    (`validateSubject`, `validateGroup`, `validateRef`, `checkID`) and
    `deliver.go`;
  - `internal/store/directory.go`: `DirectoryLink`, `DirectoryGroup`, both
    `Lock`s, `Members` and the groups' name read;
  - `internal/store/standing.go`: the subject's standing read, which joins
    `directory_links`;
  - `internal/decision/inputs.go`: `standingDenial` and `deactivatedDenial`;
  - `internal/identity/federation.go`: `linked`, `ConnectorSummary`,
    `DirectoryClaimProblem`; `internal/identity/mapping.go`:
    `ApplyDirectory`;
  - `internal/daemon/identityctl.go`: `connector-put` and its
    `--directory-claim`; `internal/store/identity.go` and
    `identity_types.go`: the connector's columns;
  - `internal/audit/syncevent.go`: `SyncAct` and the `AttrSync*` keys;
  - `internal/httpapi/north/inventory.go`: `subjectView`, `directoryView`,
    `withDirectory`;
  - `internal/store/migrations/0021_directory_provisioning.sql`;
  - `internal/provision/harness_test.go` and `sink_test.go`, and
    `server/provision_test.go` and `server/sink_test.go`, which the new tests
    extend.
- The request itself, for the requester's own words. The Enterprise decisions
  it turns on are **E3** (Enterprise builds against a pinned release, never
  `main`), **E4** (a feature degrades honestly when it cannot work) and
  **E11** (Control owns tenancy's mechanism, and Enterprise owns which
  directory may write which tenant). The phase that consumes this one is its
  0004. Read any of them in `hoplock/enterprise` only if a choice here turns
  on it. Each is cited by id and never restated.

## Context estimate
- **Reads:** this prompt, M7, M28 (~120 lines), §6's two identity sections,
  the summaries, 0040's learnings, and the code above (~2 000 lines, the
  sink and its store half of it).
- **Changes:** `ext/identity.go` (two fields, one type, two signatures, doc
  comments); `internal/provision` (validation, keys, locks, records);
  `internal/store` (the directory repositories, the standing read, the
  connector columns) and one migration; `internal/identity` (the tied login,
  the connector summary); `internal/daemon` (one flag); `internal/decision`
  (one field on the deactivation's standing entry); `internal/httpapi/north`
  (one member on the subject read); their tests; PLAN M7, M28, §3, §6, §7 and
  §10's row; three queued prompts; `CHANGELOG.md`.
- **Why it fits:** no route, no screen, no new decision, and one column read
  on the decision path in a statement it already sends. Every rule 0040 set
  holds; it is re-keyed rather than redesigned, and this prompt settles every
  case the request left to Control.

## Objective
Let the registered identity sync say which directory each identity and group
comes from, and key everything Control keeps about a directory identity by
`(tenant, source, ExternalID)` instead of `(tenant, ExternalID)`. Two
directories feeding one tenant then keep their own native ids, and a connector
tied to the directory says which directory its logins resolve in.

Today, at `v0.27.0` and on `main`:

- the sink joins a subject and a group on `(tenant, ExternalID)`:
  `directory_links` and `directory_groups` are keyed that way, and
  `ext.SyncedSubject.ExternalID`'s doc comment tells a sync that feeds
  several directories to **namespace their ids itself**;
- a tied connector's `directory_claim` must carry that `ExternalID`
  verbatim (`Federation.linked` reads `DirectoryLinks().Get(tenant,
  claim value)`), so a namespaced id has to be emitted by each customer's IdP
  at login, or every login through it is `federation.not_provisioned`;
- nothing Control records or shows says which directory an identity came
  from: the subject read's `directory` is `{provider, external_id}`, and the
  `identity_sync.*` records carry `provider` and `external_id`.

So an organisation with two directories cannot provision both into one
tenant without rewriting every id into a string each IdP must then be
configured to emit — after an acquisition, or with staff and contractors in
separate IdPs. Enterprise's 0004 refuses a second directory for a tenant until
this lands (E4).

After this phase a directory is a field, every key and every lock carries it,
and a sync that names one directory behaves exactly as 0040 built it.

## The request, in this repository's vocabulary
- *Asked for*, verbatim from enterprise#51:

  > ```go
  > // on ext.SyncedSubject and ext.SyncedGroup
  >
  > // Source names the directory this identity (or group) comes from, as the
  > // sync names it: ^[a-z0-9][a-z0-9-]{0,62}$. Required. ExternalID is unique
  > // per (Tenant, Source), not per Tenant, so two directories that both call
  > // someone `00u1` are two subjects and neither id is rewritten. A subject's
  > // Groups name groups of its own Source.
  > Source string
  >
  > // DirectoryRef names one identity or group in one directory.
  > type DirectoryRef struct {
  >     Tenant     Tenant
  >     Source     string
  >     ExternalID string
  > }
  >
  > type SyncSink interface {
  >     UpsertSubject(ctx context.Context, s SyncedSubject) error
  >     DeactivateSubject(ctx context.Context, ref DirectoryRef, reasonCode string) error
  >     UpsertGroup(ctx context.Context, g SyncedGroup) error
  >     DeleteGroup(ctx context.Context, ref DirectoryRef) error
  > }
  > ```
  >
  > - **Connector:** a connector names its source beside its claim, e.g.
  >   `hoplock-control identity connector-put --directory-claim sub
  >   --directory-source okta-a`. A tied connector names both. A login through
  >   it looks up `(tenant, source, claim value)`, and
  >   `GET /api/v1/session/methods` shows both.
  > - **Keys:** `directory_links` and `directory_groups` are keyed
  >   `(tenant, source, external_id)`. Rows written at `v0.27.0` carry no
  >   source, and how a migration keys them is Control's call.
  > - **Reads and records:** subject reads carry
  >   `directory {provider, source, external_id}`, and the `identity_sync.*`
  >   records carry `source`.
  > - **Unchanged:** a login name stays unique per tenant across sources
  >   (`KindConflict`). The ordering, revocation and error rules apply per
  >   `(tenant, source, ExternalID)`.

- *Why a field and not namespaced ids*, in the request's words: each IdP sends
  its native id at login, with no claim expression to configure on the
  customer's side; there is no separator to choose and nothing to escape or
  parse; provenance is data on reads and records; and a wrong tie is caught
  when the connector is configured, not as `federation.not_provisioned` at a
  user's sign-in.
- *Compatibility*, in the request's words: the struct fields only add.
  `DeactivateSubject` and `DeleteGroup` change signature, a `### Breaking`
  release under M23. Positional `source, externalID` strings invite swapping
  the two, and new methods break every `SyncSink` implementation as surely.
- *What stays broken until it lands*: an organisation with two directories
  cannot provision both into one tenant.

## What this phase answers, and where it differs from the ask
It is **met as asked**. Nothing in it contradicts an M decision: M7's mapping,
M28's denial and its cost, and M23's release rule all hold. These are the
cases the request left to Control, settled:

1. **The field is `Source`, on both structs, and required.** It matches
   `^[a-z0-9][a-z0-9-]{0,62}$`. Absent or malformed, the call is
   `KindInvalid` naming `source`, nothing is applied, and it is recorded as
   `identity_sync.refused`, as every other field's refusal is.

   The subject read already has a top-level `source`: the connector that
   asserted the subject, `local`, or for a directory subject the sync's
   provider (`internal/store/types.go`, `Subject.Source`). It is unchanged.
   The directory's source is `directory.source`, inside the object that
   already says the subject is the directory's. The two words are the same
   and the things are not, so the doc comment on `subjectView.Source`, the
   `directory` view and §6 each say so once. Renaming the top-level member
   would be a north-bound break nobody asked for, and calling the new field
   anything but the request's name would make the two repositories describe
   one field in two words.
2. **`DirectoryRef {Tenant, Source, ExternalID}`**, and the two signatures
   exactly as asked. Both are listed under `### Breaking` (M23), and so is
   `Source` becoming required: a caller compiled against `v0.27.0`'s structs
   still builds, and every one of its calls is then `KindInvalid` on
   `source`. That is a break in what the module promises, whatever the
   compiler says. The structs keep `Tenant`, `Source` and `ExternalID` as
   fields of their own rather than embedding a `DirectoryRef`, as asked, so a
   keyed literal written today needs one field added and nothing renamed.
3. **Everything 0040 keyed on an `ExternalID` is keyed on
   `(tenant, source, ExternalID)`:** the link row and the group row, the
   ordering by `UpdatedAt`, the deactivation kept for an id not seen yet, the
   group tombstone, the owed-delivery flags, the per-identity advisory lock,
   and the members a group change re-derives. A subject's `Groups` resolve
   among the groups of **its own source** only, so another directory's group
   with the same id is never its group. The tenant's groups lock stays per
   tenant: a group change is still exclusive of the subject writes in flight
   in its tenant, and narrowing it to a source was not asked.
4. **A subject is still one identity in one directory.** `directory_links`
   keeps its unique `(tenant, subject_id)` key, so M28's cost paragraph holds
   word for word: the decision path reads the deactivation by the same join,
   and reads the link's `source` in the same statement.
   `TestStandingDenialsCostTheDecisionPathNoRoundTrip` holds unchanged.
5. **A login name stays unique per tenant across sources** (`KindConflict`),
   and a subject is still never adopted or merged. So a person who moves from
   one directory to another is two subjects here, and the second is
   `KindConflict` while the first still presents the name. That is the
   existing rule, now with a case worth one sentence in `ext`'s doc comment.
6. **A tied connector names its source.** `idp_connectors` gains
   `directory_source`. A connector has both or neither: `connector-put
   --directory-source` without `--directory-claim`, or the reverse, is
   refused naming the flag that is missing, and writing a connector without
   either unties it, as at `v0.27.0`. The source is checked against the
   pattern in item 1, and the tie is refused while no sync is registered, as
   the claim is today. It is also refused **when nothing in the tenant comes
   from that source** — no link and no group — and the error names the
   sources the tenant does have, up to twenty. That refusal is what makes the
   request's fourth reason true: a misspelt source is caught when the
   connector is written rather than at somebody's sign-in. It costs a
   bring-up nothing, because a tie to a source nothing has written refuses
   every login through the connector anyway (`federation.not_provisioned`):
   provision first, then tie.

   A login through a tied connector looks the link up by
   `(tenant, connector's source, claim value)`. Everything else about it is
   0040's: it reaches the provisioned subject, rewrites none of it, and
   otherwise is `federation.not_provisioned` and creates nothing. A login
   through an untied connector is unchanged.

   `GET /api/v1/session/methods` shows `directory_source` beside
   `directory_claim`, both omitted when empty. That route answers anonymous
   callers, and 0040 decided that a claim name is configuration rather than a
   secret. A source is the same kind of thing, a label an operator chooses. So
   `connector-put`'s help says it is shown on the sign-in methods, and to
   name the directory with it, not describe the organisation.
7. **The migration keys every existing row under the source `default`**: each
   link, each group, and each connector with a non-empty `directory_claim`. A
   sync built against `v0.27.0` and rebuilt against this release with
   `Source: "default"` then carries on exactly as it was. One that sends
   another name provisions new subjects, whose login names collide loudly
   with the old ones (`KindConflict`) rather than doubling silently. The
   changelog's `### Breaking` says both. Rejected:
   - refusing to migrate a database that holds rows: an upgrade that cannot
     run;
   - leaving old rows with an empty source: a required field with a
     permanent exception;
   - adopting old rows into whichever source writes first: adoption, which
     0040 refused, and the wrong answer the moment two directories write.

   No Enterprise build has written a row, since its 0004 is unbuilt.
   Control is open source, though, and another host may have, so the rows are
   kept rather than assumed absent.
8. **The decision record names the directory identity whole (M4).** The
   deactivation's standing entry gains `source` beside `external_ref`, which
   stays the directory's `ExternalID`. It is omitted on a lock and a
   quarantine, and absent on records written before this release. `explain`
   renders it when present. One `00u1` is two people once there are two
   directories, so `external_ref` alone no longer identifies anyone.
9. **Records and reads carry it.** Every `identity_sync.*` record carries
   `source` (a new `AttrSyncSource`) beside `provider` and `external_id`,
   `identity_sync.refused` included whenever the refused call named one. So
   "everything the directory did to this person" is still one filter, now on
   three keys. The subject read's `directory` becomes
   `{provider, source, external_id}`, which only adds a member.

## In scope

### 1. `ext/identity.go`: two fields, one type, two signatures, every doc comment
- `SyncedSubject.Source` and `SyncedGroup.Source`, as item 1 says.
- `DirectoryRef`, and `SyncSink.DeactivateSubject(ctx, ref DirectoryRef,
  reasonCode string)` and `SyncSink.DeleteGroup(ctx, ref DirectoryRef)`.
- Rewrite `ExternalID`'s doc comment on both structs: unique per
  `(Tenant, Source)`. Delete the instruction to namespace ids, which is now
  wrong. Say that a subject's `Groups` resolve within its own source.
- `SyncSink`'s doc comment: idempotent on `(Tenant, Source, ExternalID)`;
  ORDER and ERRORS per that key; the tied connector names a source; the
  `KindConflict` sentence carries item 5's case.

### 2. `internal/provision`: validation, keys, locks, records
- `validateSubject`, `validateGroup` and `validateRef` check `source` (item
  1). `validateRef` takes a `DirectoryRef`.
- Every read, write and lock in `subject.go`, `group.go` and `deliver.go`
  carries the source (item 3).
- `rederive` reads the members of a group among the links of the group's
  own source.
- Every `audit.SyncAct` carries the source (item 9).

### 3. `internal/store` and one migration
- The next forward-only migration (`internal/store/migrations/`, numbered
  after whatever is newest when you run; 0041 adds one too):
  - `directory_links.source` and `directory_groups.source`, `text NOT NULL`,
    backfilled to `default` (item 7), then with no default;
  - both primary keys become `(tenant, source, external_id)`;
  - `directory_links_subject_key` on `(tenant, subject_id)` is kept, unique
    (item 4);
  - the members read stays served by an index: a group's members are the
    links of one tenant and one source whose `group_ids` contain the id.
    Show that it is with `EXPLAIN` in a test or in your learnings;
  - `idp_connectors.directory_source`, `text NOT NULL DEFAULT ''`,
    backfilled to `default` where `directory_claim <> ''`, with a check
    that the two are both empty or both set.
  - The migration's header comment says why each change was made, in 0021's
    style. 0021 is never edited.
- `DirectoryLink`, `DirectoryGroup` and `SubjectDirectory` gain `Source`.
  `Get`, `Lock`, `Members` and the groups' name read take it.
- The subject's standing read in `standing.go` selects `d.source` in its
  existing join. It adds no statement and no join.
- A read for item 6's refusal: whether any link or group in the tenant has a
  given source, and the tenant's sources, bounded at twenty.
- `store.Connector` gains `DirectorySource`, read and written beside
  `DirectoryClaim`.

### 4. `internal/identity` and `internal/daemon`: the tie and the login
- `Federation.linked` looks the link up by the connector's source (item 6).
  Its refusals stay `federation.not_provisioned`, and the internal reason
  names the source as well as the claim.
- `ConnectorSummary` gains `DirectorySource` (`directory_source`,
  `omitempty`).
- `identity connector-put --directory-source`, validated and refused as item
  6 says, and printed beside `directory_claim` in the command's output.

### 5. `internal/decision`: the deactivation's standing entry
`deactivatedDenial` carries the source, and the record's standing entry
renders it as `source` (item 8). `explain` shows it, and its refusal of a
record whose explanation names a denial its inputs do not hold is unchanged.

### 6. `internal/httpapi/north`: the subject read
`directoryView` gains `Source` (`source`). `subjectView.Source`'s doc comment
says what the top-level member is and that it is not the directory's source
(item 1).

### 7. The plan, revised in place
- **M7:** its sentence on the directory door names the directory's source as
  part of what links a person, if it reads otherwise.
- **M28:** the paragraph on the deactivation says the record names the
  directory's source as well as its id. The cost paragraph is unchanged,
  and that is checked rather than assumed. No decision is added or amended,
  so the register's rows stay as they are.
- **§3:** `internal/provision`'s entry says the sink joins on the directory's
  source and `ExternalID`.
- **§6, "The directory door":** the key, the tie's source, item 5's case,
  and item 1's two meanings of "source".
- **§7, "Identity provisioning is audited":** `source` on every record.
- **§10:** this phase's row, revised to say what was built.

### 8. The queued prompts this phase changes
Each obligation goes in the prompt that will implement it, not only in the
plan:
- **instance identity (0045):** its north-bound compatibility promise covers
  `directory.source` on the subject reads and `directory_source` on the
  sign-in methods. Add `0042` beside `0040` where its "Read first" lists the
  learnings whose fields the promise covers.
- **the console (0046):** it shows a directory subject's source where it
  shows the directory's id, and a tied connector's source beside its claim.
- **the E2E topology (0047):** the de-provisioning scenario names a source,
  and a second scenario provisions two directories into one tenant with one
  id in common and shows that they are two subjects, and that each tied
  connector's login reaches its own.

### 9. `CHANGELOG.md`
The release's section (M23), with `### Breaking` naming both signatures,
`Source` becoming required, and the migration's `default` source with what a
rebuilt sync must send to carry on. It names the two fields, the type, the
connector's flag and member, the read's member and the record attribute.

## Out of scope
- **Group entries scoped to directories:** enterprise#51's item 2, a request
  of its own, still queued in `hoplock/enterprise` as
  `prompts/upstream/queued/enterprise-PR#51-directory-group-scope.md`. This
  phase changes nothing in the claim mapping. A directory group still reaches
  policy by its `Name` through the tenant's one `groups.map` (M7), and
  `ApplyDirectory` is unchanged. Do not add `directories` to a mapping entry
  here, and do not build toward it. A group's source is on its row, which is
  all that request needs from this phase.
- **Which directory may write which tenant.** That is Enterprise's
  governance (E11), built on M18. Control applies what its one registered
  sync sends.
- **Two untied connectors whose login group claims share a name.** Control's
  own multi-connector login, and not asked.
- **Adopting or merging a subject across sources**, or moving a link from one
  source to another. Never, as 0040 decided.
- **A north-bound route listing sources or connectors.** The sign-in methods
  stay the only connector read, and the CLI's refusal names the tenant's
  sources.
- **The decision path beyond the one column** (M5).

## Acceptance criteria
- A sync that names one source behaves exactly as 0040 built it. Every
  existing test in `internal/provision`, `internal/identity`,
  `internal/decision`, `internal/httpapi/north` and `server/` passes with
  `Source` added to its fixtures and nothing else changed.
- **Two directories, one id.** Upserts from sources `a` and `b`, both with
  `ExternalID` `00u1` and different usernames, provision two subjects with
  two `dir-…` ids. Each read shows its own `directory.source`. Deactivating
  `(a, 00u1)` denies only the first, and the second's authorize is
  unchanged.
- **Groups stay in their source.** A subject of `a` whose `Groups` name
  `g1` takes the name of `(a, g1)`, never that of `(b, g1)`. Renaming or
  deleting `(b, g1)` re-derives no subject of `a`.
- **Ordering and tombstones per key.** A stale upsert for `(a, x)` changes
  nothing even when `(b, x)` was written later. A deactivation for an unseen
  `(a, x)` is kept and provisions `(a, x)` deactivated, and leaves a later
  create of `(b, x)` active. A tombstone for `(a, g)` does not stop an
  upsert of `(b, g)`.
- **Username across sources:** `(b, y)` presenting a name `(a, x)` presents
  is `KindConflict`, applies nothing, and is recorded `identity_sync.refused`
  with `source` `b`.
- **Validation:** `Source` absent, upper-case, 64 characters, or starting
  with `-` is `KindInvalid` naming `source`, on each of the four methods.
- **The tie:** a connector tied to `(sub, a)` logs in the subject provisioned
  under `(a, <sub>)` and is `federation.not_provisioned` for an id only `b`
  provisioned. `connector-put` refuses a source with no claim, a claim with
  no source, a malformed source, and a source nothing in the tenant has
  written, the last naming the sources that exist. Writing the connector
  without either flag unties it. `GET /api/v1/session/methods` shows both
  members for a tied connector, and neither for an untied one.
- **The record:** a deactivated subject's decision record carries `source`
  and `external_ref` in its standing entry, and `explain` shows both. A
  lock's and a quarantine's entries carry no `source`.
- **Records:** every `identity_sync.*` kind carries `source`.
- **The decision path:** `TestStandingDenialsCostTheDecisionPathNoRoundTrip`
  passes unchanged in its counts.
- **The migration** applies forward over a database holding 0040's rows: a
  link, a deactivated link kept ahead of its create, a group, a tombstone,
  and a tied connector. Afterwards each has source `default`, the tied
  connector's logins still resolve, and an upsert with `Source: "default"`
  for an existing id updates it and creates nothing.
- `make release-check` passes, and the release is a MINOR with
  `### Breaking`.

## Tests
Postgres-backed, on 0040's harness. Name them for what they prove, for
example `TestTwoDirectoriesKeepTheirOwnIDsInOneTenant`,
`TestASubjectsGroupsResolveWithinItsOwnSource`,
`TestOrderingAndTombstonesAreKeptPerSource`,
`TestALoginNameIsUniqueAcrossSources`,
`TestATiedConnectorResolvesInItsOwnSource`,
`TestATieToASourceNothingHasWrittenIsRefused` and
`TestRowsFromBeforeTheSourceAreKeyedUnderDefault`. Use one table per item
rather than a fixture per case. `internal/store` gains a test for the source
read and for the members read under a source. `server/provision_test.go`
gains one case that drives two sources through a registered sync.

## Release (M23)
The next MINOR after the latest tag, with `### Breaking`: two `SyncSink`
methods change signature, and `Source` is required. It adds
`ext.DirectoryRef` and the two `Source` fields.

## Cross-repo impact
This phase answers an upstream request and changes what `ext/` does and
says, so it owes a downstream sync. Its PR MUST carry `## Cross-repo impact`
(`docs/CROSS-REPO-PROTOCOL.md` §4.1), naming **every** consuming repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5). It knows what it asked for, but not what
was built, and the session there that rewrites its prompts will be a fresh one.
State at least these obligations:

1. **Its 0004, "What Control does not do yet".** Rewrite the directory-source
   half from what merged, and stop building around it:
   - its sync names each directory as `Source`, sends each IdP's ids as the
     IdP holds them, and calls `DeactivateSubject` and `DeleteGroup` with a
     `DirectoryRef`;
   - a tenant may be fed by several directories, so the refusal of a second
     one goes, and so does the skip on the criteria for two directories in
     one tenant, **except** where they also need the group-scope request,
     which is still its own;
   - a tied connector names its source (`--directory-source`), and a tie is
     made after the directory has provisioned.
2. **What Control decided that it did not ask for**: item 6's refusal of a
   tie to a source nothing has written, and its both-or-neither rule; item
   7's `default` source for rows from before the release; item 8's `source`
   on the deactivation's standing entry; item 1's note on the two meanings of
   `source` on a subject read; and item 5's case of a person who moves
   between directories.
3. **Its plan.** §4's `IdentitySync` bullet, where it says that nothing on
   the sink names a directory and points at the request.
4. **Its pin.** Every obligation holds from the release this PR cuts (M23),
   never from `main`, and that release breaks two signatures 0004 calls. Name
   the release by version.

Queue the **"Downstream sync" kickoff** from `docs/KICKOFF.md`, verbatim except
for its blanks, as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`. Commit it
once the PR is open, and end the section by naming that file
(`docs/CROSS-REPO-PROTOCOL.md` §4.3).

### `hoplock/proxy`
Write **"None"**. The contract is untouched (M1), and the proxy consumes
neither `ext/` nor the north-bound surface. "None" is a finding.

### Who runs the sync
Not you. This PR merges first, and the sync runs afterwards in its own session
(`docs/CROSS-REPO-PROTOCOL.md` §2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0042-directory-source-on-the-sync-sink-learnings.md`.
Enterprise's sync is written from its summary block, which MUST give:

- the two fields, the pattern, `DirectoryRef` and the two signatures;
- the key, `(tenant, source, ExternalID)`, and that a subject's groups
  resolve within its own source;
- the tie: `--directory-source`, both or neither, and the refusal of a source
  nothing has written;
- the migration's `default` source, and what a rebuilt sync must send;
- `source` on the subject read's `directory`, on the deactivation's standing
  entry, and on every `identity_sync.*` record;
- the release, by version, and its `### Breaking` lines.
