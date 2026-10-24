# 0030 — bounded lists for host routes — Learnings

## Summary
- **What shipped:** M26.7. A host's list reads `limit`/`after` and answers `{<member>, truncated, next}` through `server.ReadList` and `(server.List).Write`, over 0029's one codec. Release `v0.17.0`, MINOR, no `### Breaking` (release-check: 4 compatible additions).
- **`server`, verbatim:** `DefaultListLimit = 500`, `MaxListLimit = 10000`; `type List struct { Limit int; After []string; page north.HostPage }`; `func ReadList(w, r, name string, keyLen int, filters ...string) (List, bool)`; `func (l List) Write(w, r, member string, items any, more bool, last []string, extra map[string]any)`.
- **North (`page.go`):** `HostPage{Limit, After}`, `ReadHostPage`, `WriteHostPage`; `hostList(provider, name, keyLen)` = `list{"<provider>:<name>", keyString × keyLen}`. `readPage` is now a package function and `pageBody` builds the answer for both `answerPage` and the host's. `invalid` is the package form of `(*Server).invalid`.
- **Host errors share one refusal:** `(*hostRequest).refuse` (500 + log) serves `WriteHostError` (unchanged event `northbound_host_error_refused`) and the list pair (`northbound_host_list_refused`, with `problem`, `provider`, `list`, …).
- **Fails closed (500, logged):** outside a host route; empty name; `keyLen < 1`; a `List` not from `ReadList`; member empty/`truncated`/`next`; an extra key equal to member/`truncated`/`next`; items not a slice/array (or `[]byte`); `more` with nil `last`, or `last` of the wrong length.
- **Decisions:** none added or amended; register unchanged. PLAN §3 `server` entry revised. **Migrations:** none.
- **Cross-repo:** a downstream sync for `hoplock/enterprise` is queued (see the PR). `hoplock/proxy`: none.
- **NEXT (0031–0037):** nothing here changes how a Control list is built (`readPage(w, r, list, …)` is now a function, not a method). Name no list of Control's with a `:`; `TestNoControlListNameHasAColon` holds it.

## Details

### Why the façade is shaped as it is
- **All components are strings.** A host's key types are its own; Control
  cannot type-check them, so the cursor carries strings and the host parses
  `List.After`. A component that does not parse is the host's to refuse — with
  `WriteError` and `400 invalid_request` naming `after`, ideally, so a client
  does what it does for Control's lists (drop the cursor).
- **The provider is in `l`.** One server has at most one host provider today,
  but a cursor names `<provider>:<name>` anyway, so the same list name on
  Control's side (`targets`) or a future second host can never continue it.
  Control's names have no `:`; a source test reads every `list{name: …}`
  literal in north (except `hostList`) and fails on one.
- **`List` holds the unexported `north.HostPage`**, so `Write` cannot be
  called with a page whose list or filter digest the host made up. A zero
  `List{}` is refused ("not read by ReadList"). Mutating `Limit`/`After` on the
  returned value changes nothing `Write` does.
- **No set in the cursor.** On a cross-tenant route the selector
  (`?tenant=`, `X-Hoplock-Tenant`) is the middleware's and is not a filter.
  `list_test.go` proves the first page's cursor is byte-identical with and
  without a selector, and continues under any set.

### Tests
- `internal/httpapi/north/page_test.go`: the pair without a database (request
  carrying a `hostRequest` in its context): every refusal byte-identical to
  `readPage`'s, cross-list/provider/filter refusals both ways, the answer
  shape, and every fail-closed case with its log line.
- `server/list_test.go`: a real host via `startHostWith` (new, in
  `server_test.go`; `startHost` calls it). Refusals compared with Control's
  `GET …/subjects` (status, code, parameters); a walk of the example's own
  `listArchive`; cross-list, cross-host-list, Control↔host and cross-filter
  refusals; no tenant or set in a decoded cursor on both classes; each misuse
  answered `500 internal` and logged at ERROR with its problem.
- `server/example_test.go` gains `listArchive` and an `archive` route; the
  README block was re-copied (the guard test requires it verbatim). The
  `Example` itself has no `// Output:` and so compiles but does not run (it
  needs a database); the handler runs in `TestAHostListIsBoundedAndWalkedAsControlsAre`.

### Running the database tests locally
Postgres 16 binaries are in the container image:
`initdb -D /tmp/pg/data -U postgres --auth=trust` as a non-root user, start it
on 5432, then `HOPLOCK_TEST_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'`.
golangci-lint must be built with Go ≥ 1.27 (`GOTOOLCHAIN=go1.27.0 go install …@v2.13.2`).
