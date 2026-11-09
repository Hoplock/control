# 0054 — the console's Inventory — Learnings

## Summary
- **Shipped (`v0.41.0`, MINOR; migration `0026`; north-bound API `4`; `ext`/`server`/`contract` untouched):** Inventory under `/console/inventory/` — targets, identities, groups, roles, grants — and, to serve it, the group reads and holder filters (Details: why, and who decided).
- **Routes each screen consumes** (`ui/src/api/routes.ts`): targets `GET …/targets[?label=]`, `…/targets/{target}`, `PUT`/`DELETE …/targets/{target}` (`fleet:read`/`target:write`), and the target's newest records `GET …/audit/records?target=<hostname>&limit=10` (`audit:read`); identities `GET …/subjects[?group=]`, `…/subjects/{subject}`, `PUT`/`DELETE` (`identity:read`/`identity:write`), a subject's `…/role-bindings?subject=` and `…/grants?subject=&limit=10`; groups `GET …/groups`, `…/groups/{group}`, members `…/subjects?group=`, roles `…/role-bindings?group=`; roles `…/role-bindings?limit=1` (its `roles`) and `…/role-bindings?role=`; `POST …/role-bindings` and `…/role-bindings/remove` (`identity:write`); grants `GET …/grants[?subject=&state=]`, `…/grants/{grant}` (`grant:read`), `POST …/grants`, `…/grants/{grant}/revoke` (`grant:write`).
- **Deep links:** `/console/inventory/targets[?label=k=v]`, `…/targets/<id>[?edit=1]`, `…/subjects[?group=]`, `…/subjects/<id>[?edit=1]`, `…/groups`, `…/groups/<name>`, `…/roles`, `…/roles/<role>`, `…/grants[?subject=&state=]`, `…/grants/<id>`; creates at `/console/inventory/new/{targets,subjects,grants}[?subject=]` (a segment no list uses, so any id has a detail path). Out: `/console/audit/records?subject=|target=<hostname>`, `/console/explain/decisions/<d_…>`, `/console/explain/sessions/<id>`.
- **Where 0055 adds the lock, the quarantine and the directory's facts:** the target list's columns and `TargetScreen`'s facts panel and header actions (`ui/src/screens/Targets.tsx`); the identity list's columns and `SubjectScreen`'s facts panel and header actions (`Identities.tsx`). `Target.quarantined` and `Subject.locked`/`directory_managed`/`deactivated` are already in `src/api/types.ts`; the fuller members are 0055's to add. A directory-managed identity's Edit and Remove are still offered and refused inline (`409`, named fallback until 0055 adds its sentence).
- **Components added to the set:** `ViewLinks` (a screen's views as links; Audit's views now use it), `Ident`'s `title` size, `STATUS.scheduled`; tokens `--form-max`, `--form-column-min`; `DataTable`'s cut-page note carries `data-partial-note`.
- **Decisions:** none added or amended; register unchanged. PLAN M26.4, §3, §6, §10 (0054's row) and the version paragraph revised in place; `docs/API-VERSIONING.md` row `4`; **`ui/DESIGN.md` §3.6, §6, §7 changed** (Details).
- **Gotchas:** `seedInventory` keys off its own `prod-db` grant — the audit fixture already makes one grant; a grant's id is minted by the server, so `inventory-grant`'s `path` is a getter reading `ui/build/e2e/inventory.json` inside the test; rebuild `dist` before an e2e run, or the server embeds the last build.
- **NEXT (0055):** add the standing denials and the directory's facts to the screens named above, and their sentences to `ERROR_MESSAGES` (`target_quarantined`, `subject_directory_managed`, `standing_denial_undelivered`).

## Details

### Why this phase moved the north-bound API (and who decided)

The prompt asks for "groups … a list and a detail for each, through the
bounded-list shape (M26)", and the console is an API client with no private
path (M2). The API had no group route at all, `GET …/role-bindings` took no
filter, and `GET …/subjects` could not be narrowed to a group. Building a
groups list, a group's members or an identity's roles from the reads that
existed would have meant reading one page as the tenant, which M26 forbids.
The session stopped and asked the user, who chose to add the reads here
(additive, as 0051 did for replay) over a console-only phase or deferring
groups to a new one. Version `4`:

- **`GET …/groups`** (`identity:read`, paged by name): the distinct names on
  `subjects.groups`, `identity_role_bindings.group_id` and `identity_groups`,
  each with `member_count` (subjects whose array holds it), `roles` (bound to
  it, in code order) and `record` only where an `identity_groups` row exists
  (`display_name`, `description`, `source`, times). A group needs no record:
  the claim mapping produces names nobody wrote down, and a rule cannot tell.
  `store.Groups().Page` / `.Summary`, `store.GroupSummary`.
- **`GET …/groups/{group}`**: one of those; `404 not_found` for a name the
  tenant does not hold. Exempt from M26 as one object.
- **`?group=` on `GET …/subjects`**: `groups @> ARRAY[$n]`, served by
  `subjects_groups_idx` (GIN, migration `0026`) — containment, because the GIN
  serves `@>` and not `= ANY`. `store.SubjectQuery{Group}`.
- **`?role=`, `?subject=`, `?group=` on `GET …/role-bindings`**: a role this
  build does not define is `400 invalid_request {field: role, problem:
  invalid}`; both holders at once `{field: group, problem:
  at_most_one_of_subject_or_group}`. `?subject=` is what the subject holds
  **directly**; a role it holds through a group is the group's, and the
  identity page says so rather than resolving groups itself.
  `store.RoleBindingQuery{Role, SubjectID, GroupID}`.
- Each cursor binds its filters, **only those that are set**
  (`setFilters`): an unfiltered list still mints cursors under the empty
  digest it used before these filters existed, so a client's earlier cursor
  continues it (M26.2; `server`'s host-list test hand-mints one, and
  `TestAnUnfilteredListStillBindsNoFilter` holds it). A filter added to an
  existing list later must do the same. And
  `TestEveryListIsBoundedOrSaysWhyNot` walks every filtered view
  (`bounded_test.go`: subjects `?group=`, groups, role-bindings `?role=`,
  `?subject=`, `?group=`). Answers are held in `groups_test.go`.

**The group list has no index, on purpose, and M26.4 says so.** Its items are
distinct names from three sources, one an array on every subject row, so a
page re-reads the tenant's names and keeps those after the position. That is
bounded by the tenant's subjects, not by the caller; the alternative was a
groups table maintained on every login, a write on the decision path's row
for an operator's list.

### What each screen reads, and its four states

| Screen | Main read | Partial (a named gap) |
| --- | --- | --- |
| targets, identities, groups, grants, a role's holders | one page of the list | the server's page marked cut: the table's note says it is one page of more (DESIGN.md §7 now says this is a list's partial) |
| roles | `role-bindings?limit=1` → `roles` | a role the console has no description for (a newer server): listed by its code, named as a gap |
| a target | `…/targets/{id}` | its audit records could not be read |
| an identity | `…/subjects/{id}` | its direct roles or its grants could not be read |
| a group | `…/groups/{name}` | its members or its roles could not be read |
| a grant | `…/grants/{id}` | `decisions_truncated`: the decision sample is cut (M26.3) |

A 404 on a detail is its empty state ("no such …"), never an error; a role's
detail treats the API's `400` on `role` as "this release defines no such
role", its empty state. The create forms read nothing, so they have no
loading, empty or error state; their states are the form, a refusal at the
field (`inventory-grant-refused`) and, for a role without the permission, the
`forbidden` message in place of the form.

### Writes, and how each is offered

`offered()` gates every control. Removing a target or an identity, withdrawing
a role and revoking a grant are destructive: `ConfirmDialog`, typing the
target's, identity's, holder's or grant's id. Revoking asks for the reason the
holder is shown (the API requires one). A refused write is reported where the
API points: `useFailure` (`InventoryParts.tsx`) maps `invalid_request`'s
`field` onto the form's field (`hostname`, `scope.name`, `expires_at`, …) with
a sentence per `problem` (`inventoryModel.PROBLEMS`, every problem
`internal/access` names, plus a fallback); anything else is `ErrorMessage` at
the form. A grant create answered `202` (a registered workflow) shows the
request as pending and navigates nowhere. Withdrawing your own role is allowed
— the dialog says what it costs.

### The harnesses

- **Registry (`ui/e2e/screens.ts`, `INVENTORY`):** 17 entries — the five lists,
  their details, `inventory-targets-filtered` (the filter's own empty state),
  `inventory-target-edit`, the three create forms and `inventory-grant-refused`.
  `listStates`/`detailStates` build the four states; `formMutation` registers
  a create form as its own mutation (shown to a holder, the `forbidden`
  message to anyone else).
- **RBAC:** every inventory write is a `mutation` — targetPut/Delete,
  subjectPut/Delete, roleBind/Unbind on an identity, a group and a role,
  grantCreate, grantRevoke — so each is asserted hidden from, and refused by
  the API to, every role without its permission. A bindings table always has
  a row menu (open the holder or the role), so `reveal` works for a role
  offered nothing destructive.
- **Fixture:** `seed.yaml` gains targets `api-01`, `cache-01`, `scratch-01`
  and identities `lin`, `noor`, `svc-deploy`, `temp-intern`;
  `fixture/inventory.ts` (`seedInventory`, after `seedPolicy`) binds `sre` →
  fleet-admin and auditor, `payments` → grant-admin, `lin` → policy-author,
  and grants `vault-read` (revoked), `payments-deploy` (scheduled, two days
  out), `build-cache` and `prod-db` (active), recording each id by scope in
  `ui/build/e2e/inventory.json`. These API calls write audit records, so
  `audit-records`' baseline changed.
- **Mutations project:** an administrator grants access through the form
  (refused first at `reason`), finds it in the list filtered by holder and in
  its detail, and revokes it by typing its id (a near miss refused); a
  fleet-admin removes `scratch-01` by typing its id.
- Masked as volatile: grant ids (list, detail title and breadcrumb, an
  identity's grants) and a binding's `granted_by` (a sign-in's session id).

### Changes to `ui/DESIGN.md` in this PR, and why

1. **§3.6** names `--form-max` and `--form-column-min`: the inventory's forms
   needed a width and a column floor, and a layout dimension is a token.
2. **§6** adds `Scheduled` to the info row, and says how a grant reads:
   Active while live, Scheduled before its window — information, not a fault
   (M10: expiry is a predicate, so a future window is normal) — and Expired or
   Revoked, deny-class, once it backs nothing; an unknown state is Not
   reported.
3. **§7** adds two primitives the set lacked: view links (Audit had built
   them screen-locally in 0051; the inventory's five lists needed the same,
   and the prompt forbids building a component inside a screen) and the
   identifier, with a title size for a page about one object named by its id
   (Policy had a screen-local one for version names).
4. **§7** says a list's partial state is the rest of an answer the server
   cut, said beside its rows: the inventory lists have no other gap, and 0050
   had already named "a page marked cut" as a way to make one.

### Noticed, not changed

- Relabelling (`POST …/targets/relabel`: a diff and a replay before applying)
  is not in the console; a label is edited through `PUT`. Worth a later phase
  beside 0066's visual editor, since it is the simulable path M3 prefers.
- `ERROR_MESSAGES` has no sentence for `target_quarantined`,
  `subject_directory_managed` or the grant workflow's codes; they render the
  named fallback with the correlation id. 0055 owns the first two.
- The grants list is wide (eight columns) and scrolls in its own container at
  1280 px, as DESIGN.md §4 allows.
- Main's CI run for 0053's merge (`43e03c5`) failed before any job started,
  so `v0.40.0` was never tagged and `make release-check` refuses every later
  release until the release job is re-run there. This phase's section was
  checked against a local-only tag on that commit.

### Cross-repo

No shared surface changes (`ext`, `server`, the contract, the cross-repo
protocol), but the release changes what Enterprise ships, its screens build
from the component set, and the north-bound API is a compatibility promise
Enterprise's console reads (M19), so a downstream sync for
`hoplock/enterprise` is queued at
`prompts/downstream/queued/control-PR#154-console-inventory.md` (the PR's
`## Cross-repo impact`). Proxy: none.
