# 0034 — Bounded lists 6: role bindings and outstanding certificates

## Read first
- `docs/PROTOCOL.md`: §1, §3 (migrations are forward-only), §4, §5.
- `docs/PLAN.md`:
  - §2: the register, then **M26** (point 4, every order has an index), **M7**
    (the per-tenant CA and what it issues), **M18**, **M12**, **M11** and
    **M23**;
  - §3: `internal/credential` and `internal/httpapi/north`, the paragraph on
    lists;
  - §6: RBAC (the fixed role set, bindings to a subject or a group) and the
    CA;
  - §10: this phase's row.
- `docs/learnings/`: every summary. Read `0029`'s learnings whole. In
  `0028`'s, read the inventory rows for these routes. Read the `0011` summary
  for the CA and RBAC.
- Code:
  - `internal/httpapi/north/inventory.go`: `handleRoleBindingList`;
  - `internal/httpapi/north/ca.go`: `handleCACertificates`,
    `certificatesResponse`;
  - `internal/store/identity.go`: `RoleBindings().List`,
    `SSHCertificates().Outstanding`;
  - `internal/credential/ca.go`: `Outstanding`;
  - `internal/daemon/identityctl.go`: `runCACertificates`;
  - migration `0007` (`identity_role_bindings`, its two partial unique
    indexes; `ssh_certificates` and `ssh_certificates_outstanding_idx`);
  - 0029's `page.go`.

## Context estimate
- **Reads:** this prompt, M26 and the named decisions (~250 lines), the
  summaries, 0029's learnings, the functions above (~300 lines), and 0029's
  helpers.
- **Changes:** two handlers in two files; two paged store readers in one
  file; `CA.Outstanding` and the CLI; one migration; tests; the pending list;
  `CHANGELOG.md`.
- **Why it fits:** two lists, one store file. The migration is two index
  statements, specified below.

## In scope

### 1. `GET …/role-bindings`
- List `role_bindings`, key `[keyString, keyBool, keyString]`:
  - the role;
  - whether the holder is a group (`subject_id IS NULL`);
  - the holder's id (`coalesce(subject_id, group_id)`).

  This is today's order: per role, subject bindings by subject, then group
  bindings by group. It is unique per tenant, because a holder holds a role
  once.
- **The index (a finding).** No index serves today's order. Add a unique
  expression index on `(tenant, role, (subject_id IS NULL),
  coalesce(subject_id, group_id))`, and read with a row comparison over
  exactly those expressions, so the planner can use it. Prove it with
  `EXPLAIN` in a test.
- The answer is `{bindings, roles, truncated, next}`. `roles`
  (`identity.AllRoles`) is the same on every page.
- `RoleBindings().List` has no other caller; delete it. `RolesFor` is the
  per-subject read RBAC uses, and is unchanged.

### 2. `GET …/ca/certificates`
- List `certificates`, key `[keyTime, keyInt]` (issued-at, serial), newest
  first. Outstanding means not revoked and `valid_before` after now. The
  instant is taken per page, so a certificate that expires mid-walk drops out
  of later pages, and the key, issuance, never moves.
- **The index (a finding).** `ssh_certificates_outstanding_idx` is
  `(tenant, valid_before) WHERE revoked_at IS NULL`, which serves expiry, not
  this order. Add `(tenant, issued_at DESC, serial DESC) WHERE revoked_at IS
  NULL`. Keep the old index if a query still reads it: grep the store, and
  say which.
- `credential.CA` gains `OutstandingPage(ctx, tenant, store.Page)`. The
  answer is `{tenant, certificates, truncated, next}`, without certificate
  bytes, as today.
- **`hoplock-control ca certificates`** reads every page, in order, and
  prints exactly what it prints today. It is a CLI on the box and has no
  limit to give. If nothing else calls the whole-tenant `Outstanding`,
  delete it.

### 3. The migration
One new migration with both index changes. Note in the changelog that the
build takes a write lock on each table for its duration.

### 4. The enumerating test
Move `role-bindings` and `ca/certificates` from `pendingBounded` to
`pagedLists`. The CA route needs the fixture with a CA configured, and its
seed issues two certificates.

## Out of scope
- `GET …/ca` (the trust bundle), which is exempt and answered whole.
- Compromise rotation, which revokes by statement rather than by reading the
  list.

## Acceptance criteria
- Both lists answer in M26's shape and pass the bounded test.
- `EXPLAIN` shows each order read from its new index.
- The CLI prints the same lines as before for a tenant with more
  certificates than one page holds.
- Two tenants (M18): no cursor crosses.

## Tests
- `internal/store`: both `Page` readers with two tenants, plus the plans.
- `internal/credential`: `OutstandingPage`.
- `internal/daemon`: the CLI over two pages.
- `internal/httpapi/north`: the handlers and the bounded test.

## Release (M23)
The next MINOR after the latest tag. Under `### Breaking`:
- both lists are at most 500 by default, with `truncated` and `next`;
- the migration's locks.

## Cross-repo impact
- `hoplock/enterprise`: none. No `ext/` or `server/` identifier changes, and
  0028's sync announced the shape.
- `hoplock/proxy`: none.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `prompts/implemented/`, and add
`docs/learnings/0034-bounded-lists-role-bindings-and-certificates-learnings.md`.
