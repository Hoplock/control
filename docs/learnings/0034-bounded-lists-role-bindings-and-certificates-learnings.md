# 0034 — bounded lists 6: role bindings and outstanding certificates — Learnings

## Summary
- **What shipped:** M26's shape on `GET …/role-bindings` (`{bindings, roles, truncated, next}`, by role, holder kind, holder id) and `GET …/ca/certificates` (`{tenant, certificates, truncated, next}`, by issued-at and serial, newest first, outstanding judged per page). `hoplock-control ca certificates` reads every page and prints the same lines. Release `v0.21.0`, behaviour-only `### Breaking`.
- **Store:** `RoleBindings().Page(ctx, tenant, Page)` replaces `List`; `SSHCertificates().OutstandingPage(ctx, tenant, now, Page)` replaces `Outstanding`. `RolesFor` is unchanged.
- **Credential/daemon:** `CA.OutstandingPage(ctx, tenant, store.Page)` replaces `CA.Outstanding`; the CLI's loop is `writeOutstanding` (`internal/daemon/identityctl.go`), at `store.DefaultPageLimit` a page.
- **North:** `roleBindingList` (`role_bindings`, `[keyString, keyBool, keyString]`) and `certificateList` (`certificates`, `[keyTime, keyInt]`). `certificatesResponse` is gone. The test fixture carries its CA (`serverFixture.ca`).
- **Migration `0018`:** adds `identity_role_bindings_order_key`, unique on `(tenant, role, (subject_id IS NULL), (coalesce(subject_id, group_id)))`, and `ssh_certificates_issued_order_idx` on `(tenant, issued_at DESC, serial DESC) WHERE revoked_at IS NULL`. Nothing dropped: the partial unique keys are `Bind`'s `ON CONFLICT` targets, and `RevokeByCAKey` reads `ssh_certificates_outstanding_idx`.
- **The test:** both routes moved from `pendingBounded` to `pagedLists`.
- **Decisions:** none added or amended; register and PLAN unchanged. **Cross-repo:** none owed; `ext/`, `server/`, `contract/` untouched.
- **Gotcha:** an order over expressions works through `order` as is: its `columns` are SQL, so `"(subject_id IS NULL)"` renders into both the row comparison and `ORDER BY`. The index has to hold the same expressions, or the planner sorts.
- **NEXT (0035–0037):** a store reader that `checkPage`s through a `…PageSQL(op, …)` helper can be planned by `planWithOnly` in a test (`export_test.go` has one per list).

## Details

### What the prompt left open, and what was chosen
- **Reader names.** `RoleBindings().Page`, as every plain list is named, and
  `SSHCertificates().OutstandingPage`, keeping the name of the read it pages
  rather than calling a filtered list `Page`. Both build their statement in a
  `…PageSQL` function, as 0033's do, so the plan test asks Postgres about the
  statement the reader runs, not a copy.
- **The cursor names.** `role_bindings` and `certificates`. The CA list's member
  is `certificates`, so the cursor's `l` matches it as the other lists' do.
- **The handler's position for a binding** is written out per holder kind rather
  than as `SubjectID + GroupID`: the table's CHECK makes exactly one of them set,
  and the explicit form says which component is the kind.
- **The CLI buffers its lines, not its rows.** It formats each page's lines into
  one buffer and writes them once the last page is read. So a failure on page
  three prints nothing (as the one statement before did), and what is held is
  the output, not every certificate's bytes. It reads at the default page size:
  a CLI has no limit to give, and 500 keeps each statement small.
- **The CA list still reads the certificate column.** The answer leaves it out,
  as before; dropping it from the select (as 0031's `StatesFor` does for the
  desired document) would save bytes per page and was not asked. A later phase
  may, if the list is found heavy.

### The plan test
`TestTheBindingAndCertificateListsReadTheirOrderFromAnIndex`
(`internal/store/binding_certificate_page_test.go`) uses 0033's `planWithOnly`:
the generic plan with every other index on the table dropped and sequential
scans off. With the new index alone, each page is an `Index Scan using …` with
no `Sort`, and a page after a position has the row comparison in its
`Index Cond` (`ROW(role, (subject_id IS NULL), COALESCE(subject_id, group_id)) >
ROW($2, $3, $4)`, and `ROW(issued_at, serial) < ROW($3, $4)` with
`valid_before > $2` as a filter). With the index the list had before 0018
(`identity_role_bindings_subject_key`, `ssh_certificates_outstanding_idx`), the
plan sorts, so the assertion tells the two apart. The test also asserts the
kept `ssh_certificates_outstanding_idx` is still in the schema.

### Tests added
- `internal/store/page_test.go`: `RoleBindings.Page` and
  `SSHCertificates.OutstandingPage` join `pagedReaders`, inheriting the limit,
  bounds, two-tenant, insert/delete and concurrent-walk tests. A certificate is
  put at `newestFirst(id)` with serial `certificateSerial(id)`, beside its
  tenant's test CA key (the foreign key needs one); `DeleteSSHCertificate`
  (`export_test.go`) removes one, which nothing in the product does.
- `internal/store/binding_certificate_page_test.go`: the bindings' whole key
  (two roles, subjects before groups, a subject and a group of one name), the
  certificates judged at each page's instant (one revoked, one expiring at the
  instant, one expiring between pages, two issued at one instant ordered by
  serial), and the plans.
- `internal/credential/ca_test.go`: `OutstandingPage` at the CA's clock, across
  an expiry and across two tenants.
- `internal/daemon/identityctl_test.go`: `ca certificates` through `Main`, and
  `writeOutstanding` at one, two and four a page over five certificates, all
  printing the same lines; an empty tenant; a failure on the second page prints
  nothing.
- `internal/httpapi/north/binding_certificate_page_test.go`: the binding order
  and `roles` on every page, a binding cursor from tenant-a continuing
  tenant-b's from its place (and refused to a tenant-a-only credential), and
  the certificate list newest first with `tenant` on every page, no certificate
  bytes, and a tenant-a position continuing tenant-b's from that instant.

### Mutations the tests caught
- `(group_id IS NULL)` for `(subject_id IS NULL)` in `roleBindingOrder`:
  `TestRoleBindingPagesFollowTheirWholeKey` (wrong order) and the plan test (the
  index no longer matches, so the plan sorts).
- The handler's position naming a group binding's kind as `false`:
  `TestEveryListIsBoundedOrSaysWhyNot` fails with "page 1 repeats" the group's
  binding.
- The CLI printing each page as it reads it: the CLI test fails.

### Checks run
`go build ./...`, `go vet ./...`, `go test ./...` against Postgres 16, and
golangci-lint v2.14.0 (the CI pin, built with Go 1.27.0): clean.
`make contract-check`, `make license-check`, `make release-check` ("on merge this
releases v0.21.0"). The conformance suite against this server, as CI's
`conform-self` job runs it: 58 of 58.

### Cross-repo impact
None. `server/` and `ext/` are unchanged, so nothing Enterprise builds against
moved, and 0028's sync already announced the shape. `contract/` is untouched.
