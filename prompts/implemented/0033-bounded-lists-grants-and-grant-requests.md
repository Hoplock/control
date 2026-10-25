# 0033 — Bounded lists 5: grants and grant requests

## Read first
- `docs/PROTOCOL.md`: §1, §3 (migrations are forward-only), §4, §5.
- `docs/PLAN.md`:
  - §2: the register, then **M26** (points 2, 3 and 4 above all), **M10**
    (grants and their states), **M18** (neither the tenant nor a self
    route's subject is ever in a cursor), **M12** (every table carries the
    tenant), **M11** and **M23**;
  - §3: `internal/access` and `internal/httpapi/north` (the self route
    class, and the paragraph on lists);
  - §6: grants and self-service requests;
  - §10: this phase's row.
- `docs/learnings/`: every summary. Read `0029`'s learnings whole. In
  `0028`'s, read the inventory rows for these routes. Read the `0016` summary
  for the self route.
- Code:
  - `internal/httpapi/north/grants.go`: `handleGrantList`,
    `handleOwnRequestList`, the grant view (`handleGrantGet` and its
    `decisions`);
  - `internal/access/service.go`: `List`, `Inspect`, `inspectDecisions`;
  - `internal/access/own.go`: `OwnRequests`;
  - `internal/store/grants.go`: `Grants().List`,
    `GrantRequests().ListForSubject`, `defaultGrantLimit`, `maxGrantLimit`;
  - `internal/store/decisions.go`: `ListByGrant`;
  - migrations `0001` and `0008` for `grants_by_created_idx`,
    `grants_by_subject_idx` and `grant_requests_by_subject_idx`;
  - 0029's `page.go`.

## Context estimate
- **Reads:** this prompt, M26 and the named decisions (~250 lines), the
  summaries, 0029's learnings, the functions above (~450 lines), and 0029's
  helpers.
- **Changes:**
  - three handlers;
  - two paged store readers, and `ListByGrant`'s signal;
  - the service passthroughs;
  - one new migration;
  - tests, the pending list, and `CHANGELOG.md`.
- **Why it fits:** one handler file, two store files and one service, with
  the indexes specified below.

## In scope

### 1. `GET …/grants`
- List `grants`, key `[keyTime, keyString]` (created-at, grant id), newest
  first.
- Filters: `subject=<id>` when given, and `state=<state>` when given. A state
  is judged at the server's instant on every page (`access.Service.List`), so
  a grant can change state between pages. That is fine, because the key is
  creation, which never moves.
- `?limit=` follows M26. The silent clamp at 1 000 (`maxGrantLimit`) and
  the default of 100 go.
- The answer is `{tenant, grants, truncated, next}`.
- **The index (a finding).** The query orders by `created_at DESC,
  grant_id DESC`, but `grants_by_created_idx` is `(tenant, created_at DESC,
  grant_id)`. A row comparison needs one direction, so add
  `(tenant, created_at DESC, grant_id DESC)` and drop the old index. Under a
  subject filter, add `(tenant, subject_id, created_at DESC, grant_id DESC)`
  and drop `grants_by_subject_idx`, which has no tiebreak. Check with
  `EXPLAIN` in a test that both orders read the new indexes, as 0021 did for
  the GIN indexes.

### 2. `GET …/me/grant-requests`
- List `own_requests`, key `[keyTime, keyString]` (requested-at, request id),
  newest first. Its filter is `state=<state>` when given.
- **The subject is the middleware's (`SubjectFrom`), never the cursor's.**
  The reader filters by it on every page. Test that a cursor minted by one
  requester and presented by another reads only the second's requests.
- `?limit=` follows M26, so the clamp at 1 000 and the default of 100 go.
- The answer is `{tenant, requests, truncated, next}`.
- **The index (a finding).** `grant_requests_by_subject_idx` is
  `(tenant, subject_id, requested_at DESC)`, with no tiebreak. Add
  `(tenant, subject_id, requested_at DESC, request_id DESC)` and drop the
  old one.

### 3. `GET …/grants/{grant}`: a signal, not a page
It answers one grant with a sample of the decisions it supplied: the newest
50 (`inspectDecisions`). That is a list inside one object, bounded by the
server. It **needs only the signal**: `ListByGrant` reads one past its bound,
and the view gains `decisions_truncated`. There is no cursor. Every record
the grant backed is in the audit query, which is paged (0036).

### 4. The migration
One new migration holding the index changes of items 1 and 2. Drop an index
only after its replacement exists, in the same file. Note in the changelog
that the build takes a write lock on `grants` and `grant_requests` for its
duration.

### 5. The enumerating test
- `grants` and `me/grant-requests` move to `pagedLists`. The self route is
  called as a requester, so its spec carries its own token.
- `grants/{grant}` moves to `signalLists` with member `decisions_truncated`.

## Out of scope
- `GET …/grant-requests/{request}` and `…/me/grant-requests/{request}`
  (one object each, exempt).
- The workflow poller's `ListPending` and revocation's `SessionsByGrant`:
  internal, already bounded, and the second already says when it is cut.

## Acceptance criteria
- Both lists answer in M26's shape and pass the bounded test, and the grant
  view says when its sample is cut.
- A walk under a state filter visits each matching grant at most once, even
  when grants expire mid-walk.
- `EXPLAIN` shows the new indexes serving both orders, and neither dropped
  index is read by any query left in the tree (grep the store).
- Two tenants, and two requesters: no cursor crosses either.

## Tests
- `internal/store`: the two `Page` readers, `ListByGrant`'s signal, and the
  plans.
- `internal/access`: the passthroughs.
- `internal/httpapi/north`: the handlers, the bounded test, and the
  requester-crossing test.

## Release (M23)
The next MINOR after the latest tag. Under `### Breaking`:
- both lists default to 500 rather than 100, and refuse above 10 000 rather
  than clamping at 1 000;
- both gain `truncated` and `next`;
- the grant view gains `decisions_truncated`;
- the migration's lock.

## Cross-repo impact
- `hoplock/enterprise`: none in `ext/` or `server/`. If its approval inbox
  or console reads `GET …/grants` or `…/me/grant-requests`, 0028's sync has
  told it the shape.
- `hoplock/proxy`: none.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `prompts/implemented/`, and add
`docs/learnings/0033-bounded-lists-grants-and-grant-requests-learnings.md`.
