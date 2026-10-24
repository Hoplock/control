# 0029 — Bounded lists 1: the mechanism, proven on targets and subjects

## Read first
- `docs/PROTOCOL.md`: §1 (the context budget), §3, §4, §5.
- `docs/PLAN.md`:
  - §2: the register, then **M26** whole (this phase builds its mechanism),
    **M18** (a cursor is a value the caller controls, so it never selects a
    tenant), **M11** (a failure is never a short page), **M21** (the error
    grammar a refusal uses), **M5** and **M23** (the release);
  - §3: `internal/httpapi/north`, the paragraph on lists;
  - §10: this phase's row and the bounded-lists-chunks renumbering note.
- `docs/learnings/`: every summary. In
  `0028-bounded-lists-audit-and-plan-learnings.md`, read the Details'
  "Inventory" table and "What 0029 must build". Nothing else of it.
- Code, as the pattern to generalise rather than reinvent:
  - `store.Decisions().ListRange` and `store.Audit().Sessions`, both of which
    read one row past the limit and say when they stopped short;
  - `store.boundLimit`;
  - `north.limitParam` and `(*Server).invalid` (`grants.go`), the refusal
    grammar;
  - `access.ProblemInvalid` and `access.ProblemExceedsMaximum`.
- Code this phase changes:
  - `internal/store/targets.go` (`ListByLabels`) and `internal/store/subjects.go`
    (`List`), with their interfaces in `internal/store/repositories.go`;
  - `internal/httpapi/north/inventory.go` (`handleTargetList`,
    `handleSubjectList`);
  - the north test fixture in `internal/httpapi/north/north_test.go`
    (`newServer`, `token`, `do`, `samplePath`).

## Context estimate
- **Reads:** this prompt, M26 and the §3 paragraph (~150 lines of the plan),
  the summaries, the two learnings sections named above, `targets.go` and
  `subjects.go` (~290 lines), the two handlers and the helpers named above
  (~150 lines), the test fixture's helpers (~120 lines).
- **Changes:**
  - two new files of mechanism (`internal/store/page.go`,
    `internal/httpapi/north/page.go`) and their tests;
  - two readers and two handlers;
  - one new test file (`bounded_test.go`);
  - the plan's §3 paragraph, if the names differ, and `CHANGELOG.md`.
- **Why it fits:** the mechanism is small and specified below, so the session
  writes it rather than designing it. The two lists are the plainest of the
  eight: one table each, ordered by its primary key, with no join. That is
  why they prove the mechanism and nothing else does here.

## Why this phase exists
M26 settles one shape for every north-bound list. This phase builds the one
path every later chunk reuses, and proves it on two lists. The enumerating
test it adds is what keeps the other chunks honest: it names every route still
to do, and the chunk that does it.

## In scope

### 1. The store's page (`internal/store/page.go`, new)
```go
// DefaultPageLimit and MaxPageLimit are M26's: the limit a list takes when
// its caller names none, and the most one page may hold.
const (
	DefaultPageLimit = 500
	MaxPageLimit     = 10000
)

// Page asks a reader for one page of a list (M26).
type Page struct {
	// Limit is how many rows the page may hold, 1 to MaxPageLimit. The
	// handler has refused anything else; a reader handed it answers
	// KindInvalid and never clamps.
	Limit int
	// After is the sort key of the last row the caller was handed, one value
	// per component of the list's order, each typed as its column is
	// (string, int64, time.Time, bool). Nil starts the list.
	After []any
}

// Paged is one page of a list, and whether the list continues past it.
type Paged[T any] struct {
	Rows []T
	// More reports that at least one row follows the last of Rows.
	More bool
}
```
Add unexported helpers, so that no reader writes its own paging:
- one that checks a `Page` against the list's key length (`KindInvalid` on a
  bad limit, or on an `After` whose length is neither zero nor the key's);
- one that renders the keyset predicate as a row comparison in the list's
  direction, `(c1, c2) > ($n, $m)` or `<`, appending its arguments;
- one that scans at most `Limit+1` rows of a statement already ordered and
  limited to `Limit+1`, trims the extra row, and sets `More`.

A reader runs **one statement per page**, so a page is consistent with
itself. A failure is an error, never a short page (M26.6, M11).

### 2. The handler's page (`internal/httpapi/north/page.go`, new)
```go
// keyKind is the kind of one component of a list's order key.
type keyKind int

const (
	keyString keyKind = iota
	keyInt
	keyTime
	keyBool
)

// list names one north-bound list and its order key (M26).
type list struct {
	name string    // the cursor's "l": unique among Control's lists
	key  []keyKind // the order key's components, in order
}

// pageRequest is one page asked of a list: the store's Page, and what the
// cursor continuing it must bind.
type pageRequest struct {
	store.Page
	list    list
	filters string // the digest of the canonical filters ("f")
}

// readPage reads ?limit= and ?after= for one list under its canonical
// filters ("name=value", sorted here). On a refusal it has written the 400
// and answers false.
func (s *Server) readPage(w http.ResponseWriter, r *http.Request, l list, filters ...string) (pageRequest, bool)

// answerPage writes one page: member → items (never null), "truncated" →
// more, "next" → the cursor after last when more, and every extra member
// unchanged.
func (s *Server) answerPage(w http.ResponseWriter, r *http.Request, p pageRequest, member string, items any, more bool, last []any, extra map[string]any)
```
- **Limit.** Absent: `store.DefaultPageLimit`. Zero, a negative or a
  non-integer: `s.invalid(w, r, "limit", access.ProblemInvalid, "")`. Above
  `store.MaxPageLimit`: `s.invalid(w, r, "limit",
  access.ProblemExceedsMaximum, "10000")`. Never clamped.
- **Cursor.** Unpadded base64url of `{"v":1,"l":<name>,"f":<digest>,"k":[...]}`.
  - The digest is the hex of the first 16 bytes of SHA-256 over the sorted
    filters joined by `\x1f`.
  - Each key component is a string: a string as it is, an int in decimal, a
    time in RFC 3339 with nanoseconds in UTC, a bool as `true` or `false`.
  - Decoding refuses, as `s.invalid(w, r, "after", access.ProblemInvalid, "")`:
    - a cursor that does not decode;
    - another `v`, another `l`, another `f`;
    - a key of another length, or a component that does not parse as its
      kind.
  - **The tenant is never in it**, and neither is a self route's subject
    (M18).
- No error code is added to `north.AllCodes`.
- `limitParam` stays for the audit reads until 0036 replaces it.

### 3. Two lists
- **`GET …/targets`**: list `targets`, key `[keyString]` (target id), filters
  one `label=<key>=<value>` per `?label=`, trimmed as today.
  - The reader becomes `Targets().Page(ctx, tenant, labels, store.Page)
    (store.Paged[store.Target], error)`, ordered by `target_id` and served by
    the primary key. The label predicate is the `@>` the GIN index serves.
  - The answer is `{targets, truncated, next}`.
  - `ListByLabels` stays: `fleet.CheckPolicy` reads the whole estate, which
    is internal and outside M26 (0028's inventory says why).
- **`GET …/subjects`**: list `subjects`, key `[keyString]` (subject id), no
  filters.
  - The reader becomes `Subjects().Page(ctx, tenant, store.Page)
    (store.Paged[store.Subject], error)`, ordered by `subject_id`, primary key.
  - The answer is `{subjects, truncated, next}`.
  - `Subjects().List` has no other caller; delete it, and move its tests to
    `Page`.
- Every reader still names its tenant first, and the reflection test that
  holds that (0003) holds the new methods too.

### 4. The enumerating test (`internal/httpapi/north/bounded_test.go`, new)
`TestEveryListIsBoundedOrSaysWhyNot` walks every `GET` route of
`newServer(t).server.Routes()`. Each must be in exactly one of four maps,
keyed by `RouteInfo.String()` (`"GET /api/v1/tenants/{tenant}/targets"`):

- `pagedLists`: route → `{member, path, seed}`. `seed` stores at least two
  items in the tenant and returns how many the list holds. For each one, the
  test asserts:
  1. `?limit=10001` answers `400 invalid_request`
     `{field: limit, problem: exceeds_maximum, limit: "10000"}`.
  2. `?limit=0` and `?limit=x` answer `{field: limit, problem: invalid}`.
  3. `?after=not-a-cursor` answers `{field: after, problem: invalid}`.
  4. Walking with `?limit=1` visits every item exactly once:
     - each page holds one item;
     - every page but the last has `truncated: true` and a `next`;
     - the last has `truncated: false` and no `next`;
     - the count equals `seed`'s.
  5. The first page's `next` from this list, presented to every other paged
     list, answers `{field: after, problem: invalid}`.
- `signalLists`: route → the name of its truncation member (`truncated`, or
  `decisions_truncated` inside one object). The test asserts the member is a
  boolean in a `200` answer.
- `exemptRoutes`: route → its reason, which must be non-empty.
- `pendingBounded`: route → the four-digit chunk that takes it. The test skips
  its assertions and fails if a pending route is not a route.

A route in none of the maps, or in two, fails the test, so a route added
later fails it until it is classified. Fill the maps as 0028's inventory
rules:

- **Paged:** `targets` and `subjects`.
- **Pending:**
  - `proxies` and `fleet/config/rollouts` → `0031`;
  - `fleet/algorithms` and `fleet/target-observations` → `0032`;
  - `grants`, `me/grant-requests` and `grants/{grant}` → `0033`;
  - `role-bindings` and `ca/certificates` → `0034`;
  - `policy/bundles` and `sessions/{session}/decisions` → `0035`;
  - `audit/records`, `audit/drift`, `audit/algorithm-policy-unmet`,
    `audit/gaps` and `audit/certificates/{serial}/records` → `0036`;
  - `audit/blocked-commands`, `audit/negotiated`,
    `audit/sessions/{session}/certificates` and
    `audit/sessions/{session}/algorithms` → `0037`.
- **Exempt:** the twenty routes 0028's inventory rules exempt, each with the
  reason it gives there.

Show the test failing once on a `GET` route added and left unclassified.

## Out of scope
- Every other list, including the pending ones. Leave the inventory's
  verdicts and chunk numbers as they are; a disagreement goes in your
  learnings, not into a silent change.
- `server/`: 0030 offers the shape to a host.
- Any migration: both orders here are their primary keys.

## Acceptance criteria
- The two lists answer in M26's shape, and every refusal is as specified.
- **Inserts and deletes:**
  - A page walk under concurrent inserts and deletes neither repeats nor
    skips an item that existed for the whole walk.
  - An item inserted behind the position is not seen.
  - Deleting the item a cursor names does not break the walk.
- **Two tenants (M18):** a cursor minted in tenant A and presented under
  tenant B's path reads only B's rows, from that position.
- A store failure during a page answers `5xx`, never a short page (M11).
- `TestEveryListIsBoundedOrSaysWhyNot` passes, and fails on an unclassified
  `GET` route.

## Tests
- `internal/store`: `Page` for both readers. Cover limit and more, after,
  an empty tenant, two tenants, `KindInvalid` on a bad limit or key length,
  and the concurrent walk.
- `internal/httpapi/north`: the cursor codec, by table:
  - each kind round-trips;
  - each refusal has its case: another `v`, `l` or `f`, a bad key length, a
    bad component, bad base64 and bad JSON;
  - the test that the tenant is never in a cursor decodes one and asserts its
    fields.
- Then `bounded_test.go` as above, and the handlers' existing tests updated
  for the new members.

## Release (M23)
The next MINOR after the latest tag. Under `### Breaking`, three behaviour
changes:
- `GET …/targets` and `GET …/subjects` answer at most 500 items by default.
- Both answers gain `truncated` and, when cut, `next`.
- `?limit=` above 10 000 is refused.

## Cross-repo impact
- `hoplock/enterprise`: none owed by this phase. The shape was announced by
  0028's downstream sync, and `server/` is unchanged until 0030. Write "None"
  and that reason under `## Cross-repo impact`.
- `hoplock/proxy`: none. The contract is untouched.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `prompts/implemented/`, and add
`docs/learnings/0029-bounded-lists-mechanism-targets-and-subjects-learnings.md`.
Its summary names the helpers as built, with their signatures, and the test's
four maps. Every later chunk reads it first.
