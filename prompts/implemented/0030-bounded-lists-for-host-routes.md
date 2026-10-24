# 0030 — Bounded lists 2: the shape for a host's lists (`server/`)

## Read first
- `docs/PROTOCOL.md`: §1, §3 (where it calls `ext/` and `server/` a
  compatibility promise), §4, §5.
- `docs/CROSS-REPO-PROTOCOL.md`: §1, §4.1 and §4.3. This phase adds to
  `server/`, which `hoplock/enterprise` builds on, so it owes a downstream
  sync.
- `docs/PLAN.md`:
  - §2: the register, then **M26** (point 7 is this phase), **M15** (a host's
    routes enter Control's one table, answer in Control's envelope, and never
    get a `401`), **M18** (a cursor never carries the tenant), **M24** (a
    cross-tenant read's set is the middleware's), **M21** and **M23**;
  - §3: `server` and `internal/httpapi/north`, the paragraph on lists;
  - §10: this phase's row.
- `docs/learnings/`: every summary. Read
  `0029-bounded-lists-mechanism-targets-and-subjects-learnings.md` whole. It
  is the mechanism this phase exposes. Read the `0015` summary and `0018`
  summary for what `server` promises today.
- Code:
  - `server/errors.go`: `WriteError`, the pattern to follow, which forwards to
    `north.WriteHostError` and fails closed;
  - `server/route.go`, `server/caller.go` and `server/example_test.go`;
  - `internal/httpapi/north/page.go` (0029) and `internal/httpapi/north/host.go`.

## Context estimate
- **Reads:** this prompt, M26 and M15 (~200 lines of the plan), the
  summaries, 0029's learnings, `server/` (~420 lines without tests), north's
  `page.go` and the host-route parts of `host.go` (~250 lines).
- **Changes:**
  - one new file in `server/` and its tests, and a host-facing pair in north
    `page.go`;
  - `Route`'s doc comment and `example_test.go`;
  - the plan's §3 `server` entry, and `CHANGELOG.md`;
  - the downstream sync file.
- **Why it fits:** the mechanism exists. This phase is a façade over it and a
  public promise, and the promise is most of the work: its doc comments,
  its failure modes and `make release-check`.

## Why this phase exists, and why here
A host's lists (Enterprise's approval inbox, its archive search, its report
schedules) are read by the same console and the same supervisor as
Control's. One shape means one client. It runs straight after 0029 because
Enterprise is building those lists now, and every one built before this lands
is one more to reshape later.

## In scope

### 1. `server/list.go` (new), additive
```go
// DefaultListLimit and MaxListLimit are the bounds of every north-bound list,
// Control's and a host's (M26).
const (
	DefaultListLimit = 500
	MaxListLimit     = 10000
)

// List is one page a caller asked of a host's list: how many items it may
// hold, and the position it continues from.
type List struct {
	// Limit is 1 to MaxListLimit; DefaultListLimit when the caller named none.
	Limit int
	// After is the order key of the last item the caller was handed, one
	// string per component, exactly as the host passed it to Write. Nil
	// starts the list. The host parses it and owns what each component means.
	After []string
	// unexported: the list's name, its filter digest, its key length
}

// ReadList reads ?limit= and ?after= for one of a host's lists.
//
// name names the list among the host's own; Control prefixes it with the
// host's provider, so a cursor from one of Control's lists or another
// host's is refused. filters are the request's own filter values as
// "name=value", which the cursor binds. keyLen is the number of components
// in the list's order key.
//
// On a bad limit or cursor it writes Control's 400 refusal and answers
// false. Outside a host route it answers 500 internal and logs it, as
// WriteError does.
func ReadList(w http.ResponseWriter, r *http.Request, name string, keyLen int, filters ...string) (List, bool)

// Write answers one page in M26's shape:
// {member: items, "truncated": more, "next": <cursor after last>, extra...}.
// last is read only when more is true. items must encode as a JSON array;
// nil is written as [].
func (l List) Write(w http.ResponseWriter, r *http.Request, member string, items any, more bool, last []string, extra map[string]any)
```
- **Forward, never duplicate.** Both forward to a host-facing pair in
  `internal/httpapi/north/page.go` that calls 0029's `readPage` and
  `answerPage` code path. There is one codec and one set of refusals.
- **The cursor's `l`** is `<provider>:<name>`. Control's list names have no
  `:`, so the two can never collide.
- **The tenant, and on a cross-tenant read the set, are never in the
  cursor (M18, M24).** Say so in the doc comment, and test it by decoding
  one.
- **Fail closed, as `WriteError` does.** A `member` that is empty or is
  `truncated` or `next`, an `extra` key that collides with any of the three,
  `more` with a nil `last`, or a `last` of the wrong length is a host
  programming error. Each answers `500 internal` and is logged.
- **`Route`'s doc comment says a list is expected to answer through
  `ReadList` and `List.Write`.** Control cannot see which route is a list,
  so nothing enforces it, and the comment says that too (M26.7).

### 2. The example
Add a host list to `server/example_test.go`: a route that pages a slice by
id, reads `List.After`, and writes `next`. It compiles and runs as an
example, so `ext/README.md`'s pointer to it stays true.

## Out of scope
- Control's own lists beyond 0029's two.
- An enumerating test over a host's routes: Control cannot know which are
  lists. Enterprise can, and the sync says so.
- Anything in `ext/`.

## Acceptance criteria
- A host route built with the façade answers every refusal 0029's test
  asserts for a Control list, with the same envelope and parameters, and
  walks a list with `next` exactly as Control's do.
- A cursor from a Control list, from another host list, or under other
  filters is refused on a host list, and the other way round.
- Each fail-closed case answers `500 internal`, with its log line.
- `make release-check` reports only compatible additions; the release is a
  MINOR anyway (M23).

## Tests
- `server/list_test.go`:
  - every refusal;
  - a walk;
  - the cross-list and cross-filter refusals;
  - the tenant and the set absent from a decoded cursor, on both access
    classes;
  - each fail-closed case.
- North tests for the host-facing pair, beside 0029's.

## Release (M23)
The next MINOR after the latest tag. It has no `### Breaking`: the public
additions are `server.DefaultListLimit`, `server.MaxListLimit`, `server.List`,
`server.ReadList` and `(server.List).Write`. Name each in the changelog.

## Cross-repo impact
- `hoplock/enterprise`: a sync is owed. Every list among its host routes
  should answer through `server.ReadList` and `List.Write`, holding from this
  release. Its plan should state M26's shape for them, and its route tests
  should assert it, because Control cannot. Queue the filled-in "Downstream
  sync" kickoff in `prompts/downstream/queued/` (§4.1, §4.3) once the PR is
  open.
- `hoplock/proxy`: none.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `prompts/implemented/`, add
`docs/learnings/0030-bounded-lists-for-host-routes-learnings.md`, and revise
the plan's §3 `server` entry to name the façade's new identifiers.
