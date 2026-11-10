# 0055 — the console's locks, quarantines and the directory's subjects — Learnings

## Summary
- **Shipped (`v0.42.0`, MINOR; no migration; north-bound API `5`; `ext`/`server`/`contract` untouched):** standing denials and the directory's facts on the inventory, the four levers, a claim-mapping screen at `/console/inventory/claim-mapping`, the tie on the sign-in page, the directory in Explain; and, to serve them, two reads (Details: why, and who decided).
- **Routes consumed** (`ui/src/api/routes.ts`): `POST …/subjects/{subject}/lock` · `/unlock`, `POST …/targets/{target}/quarantine` · `/release` (all `session:revoke`); `GET …/subjects` and `/{subject}` for `locked`/`lock`, `directory_managed`/`directory`, `deactivated`/`deactivation`, `record_missing`, and `GET …/subjects?record=missing` (new); `GET …/targets[/{target}]` for `quarantined`/`quarantine`; `GET` (`identity:read`) and `PUT` (`identity:write`) `…/identity/claim-mapping`, reading `directories`, `require_directories`, `stale_directory_subjects` and `group_map` (new); `GET /api/v1/session/methods` for `directory_claim` and `directory_source`; Explain's `mapping.directory_source`.
- **Vocabulary added (DESIGN.md §6, deny row):** `Locked`, `Quarantined`, `Deactivated` (`STATUS.locked`/`quarantined`/`deactivated`); an undelivered denial reads `Pending` beside its own status.
- **Error sentences added:** `target_quarantined` ("release it first"), `subject_directory_managed` ("managed by your directory"), `standing_denial_undelivered` ("in force, not yet delivered"); every `mapping.*` rejection (`claimMappingCodes.ts`, held to `ui/test/mapping-codes.json`, which `TestTheConsolesMappingCodesAreTheServers` keeps equal to the server's constants).
- **Key files:** `ui/src/screens/Standing.tsx` + `standingModel.ts` (panels, dialogs, cells), `ClaimMapping.tsx`, `Targets.tsx`, `Identities.tsx`; `internal/httpapi/north/{ca,inventory}.go`, `internal/store/standing.go` (`Subjects().RecordMissing`); `ui/e2e/fixture/host/main.go` (the fixture host), `fixture/standing.ts`, `e2e/standing.spec.ts`.
- **Decisions:** none added or amended; register unchanged. PLAN §3, §6, M26.4, the version paragraph and §10 (0055's row) revised in place; `docs/API-VERSIONING.md` row `5`; **`ui/DESIGN.md` §6, §7, §10 changed** (Details).
- **Gotchas:** the e2e server is now the fixture host, not `cmd/hoplock-control` — Control plus one `ext.IdentitySync` that provisions `control.yaml`'s `e2e_directory` at start; a directory subject's id is random per run, so `steady()` masks `dir-…`, `inventory-subjects` is captured sorted by name, and a screen opening one reads `ui/build/e2e/standing.json` (`fixtureSubject`). The RBAC harness has a fourth role, `grant-admin` (`gia`).
- **NEXT (0056):** the identity sync's row on the extensions screen is yours; the fixture host already registers one (`e2e-directory`, started).

## Details

### Why this phase moved the north-bound API (and who decided)

Two of the prompt's criteria had no API to stand on, and the console has no
private path (M2). The session stopped and asked the user each time.

1. **Each group entry's `directories`.** The claim-mapping read answered the
   tenant-wide `directories` and the raw YAML `document`; nothing said which
   entry serves which directory short of parsing YAML in the browser, which
   would make the console a second reader of a document the server owns. No
   claim-mapping screen existed either, though the prompt said "as it renders
   the older ones". The user chose: build the screen and its editor here, and
   add `group_map` to the read. It is the **first `since` member** on this
   surface (`since:"5"`), so a client declaring an older version that reads a
   mapping with group entries is answered `500 api_version_unexpressible`;
   asked about that consequence, the user said no clients are built or
   deployed yet, so the member stood. `TestMain`'s projection check
   (`answer_names_test.go`) was written for "no `since` member yet" and said
   the first one revisits it: it now holds an older version to encoding/json
   less exactly the members it predates (`walkSince`, `withoutMember`), and to
   a refusal when one of them holds a value — mutation-tested by making
   `project` thin silently.
2. **"Listed" record-missing subjects.** `GET …/subjects` pages subject rows;
   a lock that outlived its row, and a deactivation ahead of its create, were
   readable only by id — and the second's id is Control's random `dir-…`.
   The user chose `?record=missing` over making the list itself the union
   (a changed meaning) or showing them on the detail only. It pages the union
   of `subject_locks` and deactivated `directory_links` with no `subjects` row
   (`store.Subjects().RecordMissing`), by id; its cursor binds the filter;
   `record=` with another value, or `group` beside it, is refused. M26.4 now
   names it as a third unindexed order (bounded by the tenant's standing
   denials). Held in `directory_test.go`, `bounded_test.go`, `page_test.go`,
   `tenancy_test.go`.

### What each screen shows, and where

| Screen | Added |
| --- | --- |
| identities list | `Access` (each denial's status, the lock first; `No record`), `Directory` (`directory.source` chip beside the directory's id; the provider is the `Source` column, which for a directory subject is the sync's provider); Edit/Remove not offered for a directory subject; "Denied, with no record" beneath, from `?record=missing`, absent while empty |
| an identity | header statuses and `Managed by directory`; Standing denials panel (lock facts + Unlock, deactivation facts + "only the directory lifts it", or Lock); Directory panel (provider, directory, the directory's id, the "change it there" note); no Edit/Remove for a directory subject or an id with no row |
| targets list / a target | `Access` (`Quarantined`); Standing denials panel (facts + Release, or Quarantine); the remove dialog says release it first when quarantined |
| claim mapping (new, an inventory view) | facts, stale notice (`In progress`, while `stale_directory_subjects` > 0), group entries with their directories, the document in the source view, the connectors and their ties; `?edit=1` is the editor |
| sign-in | under a tied connector, its claim and directory |
| Explain | the directory beside the mapping version (`data-mapping-directory`) |

**The levers.** All four confirm by typing the id (`ConfirmDialog`). Lock asks
for the holder's text (one line, ≤ 512 bytes, counted in UTF-8 as the server
counts it, `standingModel.lineProblem`) and a reason code, and shows what the
holder will see — the server's own `LockedByAdministrator` when empty, a
constant the unit test holds to `internal/standing/service.go`. An
over-long text holds the confirm (`ConfirmDialog`'s new `ready`). A `503
standing_denial_undelivered` closes the dialog and leaves an in-force notice
with "Send again", which repeats the same call (M28: only asking repairs a
failed publication). Unlock of a locked-and-deactivated subject says the
deactivation still denies. Quarantine says it ends no session, drops every
cached decision in the tenant, and names `POST …/revocations` — there is no
revocation screen to link to.

**Local group membership** for a directory subject: the prompt says it stays
offered, but no north-bound route writes it (PLAN §6 says it is an operator's;
the only writer is `PUT …/subjects/{subject}`, which refuses a directory
subject). The console offers what exists — the lock and role bindings — and
nothing more. Worth a route if an operator asks.

### The fixture host

A directory-managed subject exists only where a sync provisioned it, and the
prompt asks for the API's own `409 subject_directory_managed`, so a browser
substitute would not do and a test route on the server is forbidden
(PROTOCOL §3). `ui/e2e/fixture/host/main.go` is `daemon.Main` — what
`server.Main` forwards to; only `cmd/hoplock-control` may import `server`
(`TestServerIsImportedOnlyByTheCommand`) — with one registered
`ext.IdentitySync` (`e2e-directory`, host provider
`hoplock/e2e-fixture`, section `e2e_directory`) that applies the section
through Control's own sink at `Start`: two directories (`corp`, `lab`), both
calling someone `00u1`; `eli` deactivated; `sam` deactivated (and locked by
the fixture); `00u4` deactivated before its create. It is `package main`
under `ui/`, so release-check never diffs it. `serve.sh` builds it instead of
`cmd/hoplock-control`; `identity connector-put --directory-claim` works
through it (the CLI seals the host's registry), which is how
`fixture/standing.ts` ties `corp-sso` — after the server has started, since
the tie is refused until the directory has written something.

`seedStanding` (after `seedInventory`): locks `noor` (INC-4821), `sam`
(SEC-77) and `ex-contractor` then removes it; quarantines `cache-01`; writes
claim mapping version 1 (`Payments Engineering` scoped to `corp` and to `lab`,
`sre` unscoped) and waits for `stale_directory_subjects` to reach 0; ties two
connectors; asks `/v1/authorize` about dana as `sess-e2e-directory`.

### Changes to `ui/DESIGN.md` in this PR, and why

1. **§6** adds `Locked`, `Quarantined`, `Deactivated` to the deny row, and a
   paragraph on how a standing denial reads: a deny of its own (never the
   plain `Denied`, because each is lifted by a different party), both shown
   when both stand, and an undelivered one as `Pending` delivery — the
   prompt's "in force, not yet delivered", never a failure.
2. **§7** says a lock's confirm hint is that lifting restores nothing it
   ended (the default "cannot be undone" is false for a lock), and names
   `ConfirmDialog`'s `ready`: a field refused where it stands holds the
   confirm.
3. **§10** says the server is the fixture host.

### Harness notes

- **Registry:** 15 entries in `STANDING` (`e2e/screens.ts`), plus mutations
  on `inventory-target` (quarantine) and `inventory-subject` (lock). A state
  that needs a click (a dialog open, a refusal, the undelivered notice) is a
  `prepare`, written to do nothing for a role offered nothing. The undelivered
  answer is substituted from `prepare` (`UNDELIVERED`), since a prepare can
  route the page after it loads.
- **`DataTable`'s `<th>` carries `data-column`**, so a check can sort by a
  column whatever the locale's word for it.
- **Mutations project:** a grant-admin locks `svc-deploy` and lifts it; an
  admin quarantines `build-01` and releases it.
- **CI:** `standing.spec.ts` is its own `console-e2e` entry ("standing
  denials"); `TestEveryConsoleSpecRunsInCI` refuses a spec no entry names.
- Baselines changed for every screen whose data this fixture touches: the
  identities and targets lists and details, groups (two new groups), the
  audit records (the fixture's records), the sign-in page (two connectors),
  the gallery's status page (three statuses), and every new entry.

### Noticed, not changed

- A claim-mapping write activates at once and is not confirmed by typing,
  unlike a policy activation (DESIGN.md §2 names activation as dangerous).
  It has no diff or dry run on the server to show first; a later phase that
  adds one should put the write behind the same typed confirm.
- The two fact lists in the standing-denials panel size their label columns
  independently, so a lock's and a deactivation's values do not line up.
- `golangci-lint` here was built with Go 1.25 and refuses this module
  (1.27); CI's pinned v2.14.0 was installed and run locally instead.

### Cross-repo

No shared surface changes (`ext`, `server`, the contract, the cross-repo
protocol). The release changes what Enterprise ships, its screens build from
the component set (three statuses, `ConfirmDialog`'s `ready`), and the
north-bound API is a compatibility promise Enterprise's console reads (M19) —
version `5`, whose `group_map` refuses an older declared version — so a
downstream sync for `hoplock/enterprise` is queued (the PR's
`## Cross-repo impact`). Proxy: none.
