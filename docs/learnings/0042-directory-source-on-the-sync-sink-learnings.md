# 0042 — directory source on the sync sink — Learnings

## Summary
- **Release `v0.29.0`, MINOR with `### Breaking`.** Migration `0023`. M7 and M28 revised in place; no decision added, register unchanged. Answers `Hoplock/enterprise#51`, item 1.
- **`ext`:** `SyncedSubject.Source` and `SyncedGroup.Source`, required, `^[a-z0-9][a-z0-9-]{0,62}$` (else `KindInvalid` naming `source`, nothing applied, recorded `identity_sync.refused`). `ext.DirectoryRef{Tenant, Source, ExternalID}`. `SyncSink.DeactivateSubject(ctx, ref DirectoryRef, reasonCode string)` and `SyncSink.DeleteGroup(ctx, ref DirectoryRef)`.
- **Key:** everything is keyed `(tenant, source, ExternalID)`: links, groups, `UpdatedAt` ordering, the kept deactivation, tombstones, owed delivery, the per-identity lock. A subject's `Groups` resolve among **its own source's** groups only; a group change re-derives only that source's members. The tenant's groups lock stays per tenant. A login name is still unique per tenant **across** sources (`KindConflict`); never adopted, so a person who moves directories is two subjects.
- **Tie:** `identity connector-put --directory-claim <c> --directory-source <s>`, **both or neither** (DB check too); neither unties. Refused with no sync registered, for a malformed source, and **for a source nothing in the tenant has written** (error names up to 20 it has): provision first, then tie. A login looks up `(tenant, directory_source, claim value)`. `GET /api/v1/session/methods` shows `directory_source` beside `directory_claim`, both `omitempty`.
- **Migration:** every existing link, group, tombstone and tied connector is keyed under source **`default`**. A sync rebuilt against `v0.29.0` that sends `Source: "default"` carries on unchanged; another name provisions new subjects whose usernames collide (`KindConflict`).
- **Shown and recorded:** subject read `directory {provider, source, external_id}` (top-level `source` unchanged: what asserted the subject, not the directory); the deactivation's standing entry `source` beside `external_ref` (absent on locks, quarantines, older records); `source` (`audit.AttrSyncSource`) on every `identity_sync.*` record, `refused` included when the call named one.
- **Breaking lines:** the two signatures; `Source` required; rows keyed under `default`; `--directory-claim` needs `--directory-source`.
- **Decision path:** `directory_links`' unique `(tenant, subject_id)` key kept; `d.source` read in the existing join. `TestStandingDenialsCostTheDecisionPathNoRoundTrip` unchanged.
- **Key files:** `ext/identity.go`, `internal/provision/{validate,sink,subject,group,deliver}.go`, `internal/store/{directory,standing,identity}.go`, `internal/store/migrations/0023_directory_source.sql`, `internal/identity/federation.go` (`DirectorySourceProblem`), `internal/daemon/identityctl.go`.
- **NEXT:** 0043 (directory-scoped group entries) builds on the group row's `source`. Prompts 0046, 0047, 0048 carry this phase's fields. Enterprise's sync is queued (see the PR).

## Details

### Where this phase read the prompt
- **The source validator lives in `internal/identity`** (`DirectorySourceProblem`, `MaxDirectorySourceLen`), beside `DirectoryClaimProblem`, because the sink and `connector-put` both need it and `provision` already imports `identity`. `ext` documents the pattern and exports no validator: that would be new public surface nobody asked for.
- **Validation order** on every sink call: `tenant`, `source`, `external_id`, then the rest. A refusal records the source the call named when it is fit for a record (one line, ≤ 256 bytes), malformed or not, so a sync sending `Okta` sees `source: Okta` on its refusals.
- **The tie's refusals are checked in this order:** both-or-neither (names the missing flag), claim token, source pattern, broker config, sync registered (`openWithSync`), then the source written (`DirectoryLinks().HasSource`, then `Sources` for the message). The last needs the tenant, so it runs after the store is open; nothing is written on any refusal.
- **`HasSource` counts groups too, tombstones included.** A directory that has written only groups is a directory the tenant has. `Sources` is a `UNION` of both tables, ordered, `LIMIT 20`; it scans the tenant's prefix of each primary key, which is fine for a CLI path.
- **`DirectoryLinks().Lock` keys on `source \x1f externalID`** under the existing `directory-subject` kind; an empty source or id is refused by `lockKey`'s existing "an id is required".
- **The migration drops the column defaults immediately** after backfilling, so a write that names no source fails instead of landing in `default`. The connector's `directory_source` keeps `DEFAULT ''` (untied), with `CHECK ((directory_claim = '') = (directory_source = ''))`.
- **No new index.** The members read (`directoryMembersSQL`, exported to tests as `store.DirectoryMembersSQL`) is served by 0021's GIN index on `group_ids`; tenant and source are a filter on the rows it returns. `TestAGroupsMembersAreReadWithinItsSource` asserts it with only that index left:
  ```
  Sort
    -> Bitmap Heap Scan on directory_links
         Recheck Cond: (group_ids @> ARRAY[$3])
         Filter: (provisioned AND (tenant = $1) AND (source = $2))
         -> Bitmap Index Scan on directory_links_group_ids_idx
  ```
  A group id common across many tenants would make the recheck read other tenants' rows; a composite `(tenant, source, group_ids)` GIN needs `btree_gin`, an extension this deployment does not otherwise require. Revisit only if it shows up.
- **The decision record:** `recordedStanding.Source` is `json:"source,omitempty"`, so locks, quarantines and pre-0042 records render as before. `explain` returns the raw standing JSON, so it shows `source` with no change to its own code; replay ignores it.

### Tests
- `internal/provision/source_test.go`: two directories one id (`TestTwoDirectoriesKeepTheirOwnIDsInOneTenant`), groups per source, ordering and tombstones per source (one table), login name across sources, the source pattern on all four methods (one table), and every `identity_sync.*` kind carrying `source`.
- `internal/store/directory_source_test.go`: the source reads, members and names within a source with the plan above, and `TestRowsFromBeforeTheSourceAreKeyedUnderDefault` — `MigrateThrough(22)`, 0021-shaped rows by raw SQL, `Migrate`, then every row under `default`, the tied connector's lookup `(tenant, directory_source, claim)` finding its link, the check and the dropped default refusing bad writes, and a real `provision.Sink` (with three small fakes) upserting `Source: "default"` over the old link: renamed in place, nothing created, one `subject_updated`. It lives in `internal/store` because `MigrateThrough` is a test-only export there; this is the first store test that imports another internal package.
- `internal/identity/directory_test.go`: `TestATiedConnectorResolvesInItsOwnSource` (one id in two sources, each tied connector reaching its own; an id only another source has is `federation.not_provisioned`), and the source pattern.
- `internal/decision/standing_test.go`: the deactivation's entry carries `source` and `external_ref`; lock and quarantine entries carry no `source`.
- `internal/httpapi/north/directory_test.go`: `directory.source` beside the top-level `source`; the sign-in methods show both tie members or neither.
- `server/provision_test.go`: two sources through a registered sync, and `TestATieToASourceNothingHasWrittenIsRefused` through `server.Main`.
- Every 0040 test passes with `Source` added to its fixtures. `TestADirectoryClaimNeedsARegisteredSync` also provisions under the source before the tie that is meant to succeed, because a tie to a source nothing has written is now refused.

### Cross-repo
- **`hoplock/enterprise`** (the repository that asked) owes a sync: its 0004's "What Control does not do yet", its plan's §4 `IdentitySync` bullet, and its pin to `v0.29.0`. Queued in `prompts/downstream/queued/` (named in the PR).
- **`hoplock/proxy`:** none. The contract is untouched (M1).

### Local runs
- `golangci-lint` must be the CI pin (`v2.14.0`) built with Go ≥ 1.27: `GOTOOLCHAIN=go1.27.0 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`. An older build refuses the config.
- `make release-check` needs the tags: `git fetch --unshallow --tags origin` in a fresh clone.
