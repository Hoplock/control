# 0026 — Bounded lists: find every one, settle the shape, and queue the work in chunks

## Read first
- `docs/PROTOCOL.md`, in full: §1 (the context budget: **under 60% per
  session**, which this phase plans for others as well as keeping itself),
  §3, §4, §5, §6 (adding prompts, renumbering, the mapping table) and §7 (what
  a self-contained prompt must carry).
- `docs/CROSS-REPO-PROTOCOL.md`: §1 (the shared surfaces), §4.1 and §4.3.
  Renumbering moves phases `hoplock/enterprise` cites, and a list shape that
  host routes are asked to follow would be a convention on `server/`.
- `docs/PLAN.md`:
  - §2: the register, then **M19** (the north-bound API becomes a
    compatibility promise in 0027, the reason this phase runs first),
    **M21** (machine-readable errors), **M5** (nothing on or beside the
    decision path does unbounded work), **M11**, **M18** (a cursor is a value
    the caller controls, so it never selects a tenant), **M15** (host routes)
    and **M23** (what each release promises);
  - §3: `internal/httpapi/north`, `internal/store` and `cmd/policyctl`;
  - §7: the query surface and its limits;
  - §10: the phase table, the renumbering notes and the mapping table.
- `docs/learnings/`: read every summary. Read
  `0023-audit-0019-north-bound-surface-and-record-learnings.md` **whole**:
  its Details, "Proposal: bounded inventory lists", are the starting point.
  Read `0021`'s item 14 (where the carry began), `0022`'s item 16 (a read that
  takes `limit+1` and says it was cut) and `0019`'s route table.
- Code, as the pattern to generalise rather than reinvent:
  `store.Decisions().ListRange` and `store.Audit().Sessions` (both read one
  past the limit and say when they stopped short), `store.boundLimit`
  (default 500, at most 10 000), `north.limitParam`, and replay's `truncated`
  (`internal/decision/replay.go`).

## Why this phase exists
0023 found eight north-bound lists that answer a whole tenant, sized by the
estate rather than by the caller. A tenant with a hundred thousand targets gets
one body of a hundred thousand targets, bounded only by the request timeout.
Other lists are bounded but say nothing when they stop short, and some of those
are capped silently. A caller can't tell a complete answer from a cut one.

The user asked for these to be fixed, and for every other list that would
benefit to be found too. Doing all of it in one session would go well past the
60% budget. Just reading the plan sections, the store readers and the handlers
across six packages, then changing them with their tests and record, is more
than a session should hold. So this phase does the finding and the deciding,
and it queues the implementation in chunks that each fit.

It runs before 0027. That phase makes the north-bound API a compatibility
promise (M19), and changing eight response shapes after the promise is made
would cost a version. It also runs before the console (0028), which renders
these lists and should be built on their final shape.

## Objective
1. **Inventory.** Classify every list this server answers, and every store
   reader behind one.
2. **Decide.** Settle one shape for a bounded list and record it in the plan
   as a decision.
3. **Queue.** Write the implementing prompts, chunked so no session goes above
   60%, and place them right after this phase.

This phase changes no behaviour: no handler, store method or response shape.

## In scope

### The eight lists 0023 found (each must land in a chunk)

| Route | Store or service reader | Today |
| --- | --- | --- |
| `GET …/targets` | `store.Targets().ListByLabels` | whole tenant (filtered by label) |
| `GET …/subjects` | `store.Subjects().List` | whole tenant |
| `GET …/role-bindings` | `store.RoleBindings().List` | whole tenant |
| `GET …/policy/bundles` | `authoring.Service.Versions` → `store.PolicyBundles().List` | every version, newest first |
| `GET …/proxies` | `fleet.Registry.Health` | whole fleet |
| `GET …/fleet/config/rollouts` | `fleet.Registry.ConfigRollouts` / `ConfigDrift` | whole fleet |
| `GET …/fleet/algorithms` | `fleet.Registry.AlgorithmDeclarations` | every proxy's declaration |
| `GET …/fleet/target-observations` | `store.TargetCapabilities().List` + `fleet.Registry.TargetKex` | two whole-tenant lists in one answer |

### The audit for every other list
Find the rest by enumerating, not by remembering: walk `north.Table("", nil,
nil)` for every `GET` route, and grep `internal/store` for every reader that
returns a slice without a limit. For each, record:

- the route and permission, or "internal";
- the reader behind it;
- the bound today: none, a silent default or cap (and its value), or a limit
  with a signal;
- whether a caller can tell a complete answer from a cut one;
- whether it can grow with the estate or with time;
- the verdict: **needs the shape**, **needs only the signal**, or **exempt**,
  with the reason;
- the chunk that takes it.

Candidates already visible, each to be ruled on rather than assumed:

- **The audit reads**: `GET …/audit/records`, `…/audit/blocked-commands`,
  `…/audit/drift`, `…/audit/algorithm-policy-unmet`, `…/audit/gaps` and
  `…/audit/certificates/{serial}/records`. Each takes `?limit=`, clamps it
  silently at 10 000 (`boundLimit`), and gives no `truncated` and no cursor.
  A time window is a crude cursor at best: many records share an instant.
- **Per-session reads capped silently**: `…/audit/sessions/{session}/certificates`
  (the reader's 500 default), `…/sessions/{session}/decisions`
  (`ListBySession`'s default), `…/audit/sessions/{session}/algorithms` and
  `…/loss`.
- **Earlier phases' lists**:
  - `GET …/grants` (0012);
  - `GET …/me/grant-requests` (0016), which has `?limit=` and no signal;
  - `GET …/ca/certificates` (0011);
  - the access-context lists (likely exempt: one binding per configured
    provider);
  - `GET …/extensions` (likely exempt: the registry is sealed).
- **Clients of these lists**: `policyctl versions`, and anything in
  `cmd/pdpconform` or `cmd/` that reads a list.
- **Host routes (M15)**: whether `server/` should offer the shape so a host's
  lists answer the same way. If it should, a chunk adds it as a deliberate
  `server/` change, which owes `release-check` and a downstream sync. If not,
  say why.
- **Internal readers**: whole-tenant reads no caller sees, such as the fleet
  graph or the announcer. Classify them. They are bounded by M5 and by what
  the estate is, not by this shape. Name any on a request path whose size a
  caller drives.

### The decision
Settle and record, in `docs/PLAN.md` §2, the shape every north-bound list
answers in. Make it a new decision (`M25`) with its register row, unless
reading the register shows an existing decision already settles part of it, in
which case amend that one and say so in its first line. Render it in §3 and §7.

Start from 0023's proposal and decide each of these points:

- **The limit:** `?limit=`, its default and its maximum. Today's audit reads
  clamp silently. Decide whether a limit over the maximum is refused (`400`,
  `field: limit`, with the maximum as `limit`) or clamped with the signal.
- **The cursor:**
  - **Keyset, never offset**, so a page is stable under concurrent inserts.
  - **Opaque or transparent**, and its encoding.
  - **What it binds:** the list, its filters and its order, so a cursor from
    another list or another filter is refused rather than misread.
  - **Never the tenant (M18).** A cursor is a value the caller controls.
  - **An invalid one:** `400`, `field: after`, and whether that needs its own
    code in `north.AllCodes`.
- **The answer:** `{<list>: [...], truncated, next}`. Decide whether
  `truncated` and `next` both stay, or `next` alone says it; what an empty
  page looks like; and whether an object that is a list today keeps its other
  members (`roles`, `liveness`, `fleet_owned_keys`, `ttl_seconds`).
- **Order:** each list's stable sort key, and the index that serves it. A
  list whose order has no index is a finding.
- **A list of two lists** (`…/fleet/target-observations`): split it, page
  each, or page neither and say why.
- **Errors and statuses:** within M21 and M11. A store failure while paging is
  a `5xx`, never a short page that looks complete.

### The chunks
Queue the implementing prompts in `prompts/queued/`, numbered right after this
phase. Under §6, renumber every queued prompt after them, including 0027 and
the console, with a renumbering note in `docs/PLAN.md` §10 and the composed
mapping table regenerated. Each chunk:

- **Stays under 60% context.** Size it by what the session must read and
  change, not by route count. As a guide: one chunk touches the handler file
  for its routes, no more than two or three store or service files, their
  tests, the plan rendering and the changelog. Write the estimate into the
  prompt: the files it reads, the files it changes, and why that fits. A chunk
  you cannot argue fits is two chunks.
- **Builds on the first chunk.** The first chunk builds the shared mechanism
  (the cursor codec, the store helper that reads `limit+1`, the handler
  helpers for `limit` and `after`, any new code) and proves it on two or three
  of the eight. Later chunks reuse it and add no mechanism of their own.
- **Is held by one enumerating test.** The first chunk adds a test that walks
  every `GET` route on the router and fails on a list that does not answer in
  the shape. It carries an explicit pending list naming each route a later
  chunk will fix, and that chunk's number. Each chunk removes its routes from
  the pending list. The last chunk leaves only the exempt routes, each with
  its reason. A route added later fails the test until it answers in the shape
  or is listed as exempt with a reason.
- **Is self-contained (§7).** It needs a "Read first" naming plan sections and
  decision ids. It needs the exact routes, readers, answer shapes, index
  changes (as new migrations: migrations are forward-only), clients to update
  (`policyctl`), tests, and its `CHANGELOG.md` `### Breaking` (a default limit
  that cuts today's answer is a behaviour break). It also needs its
  cross-repo impact.
- **Releases (M23):** each chunk is a phase, so each is a MINOR.

Put the eight named lists in the chunks, in an order that gets the mechanism
proven first. Put everything the audit rules as needing the shape or the
signal in a chunk too. Leave nothing found and unqueued.

## Out of scope
- Implementing any list's change, the mechanism included. This phase writes
  the inventory, the decision and the prompts.
- The south-bound contract. It is owned upstream (M1), and nothing on `/v1`
  answers a list to a proxy that this phase could change.
- Enterprise's own routes, beyond deciding whether `server/` offers them the
  shape.
- Re-auditing anything 0020–0023 ruled on, beyond the bounds of these lists.

## Acceptance criteria
- **The inventory table is in this phase's learnings.** It covers every `GET`
  route enumerated from the router, and every store reader returning a slice
  with no limit. Each row has its verdict, its reason and its chunk.
- **The decision is in `docs/PLAN.md` §2** with its register row, rendered in
  §3 and §7. Every point under "The decision" is answered.
- **The chunk prompts are in `prompts/queued/`**, each self-contained, each
  with its context estimate and its slice of the enumerating test's pending
  list. Between them they cover the eight named lists and everything else the
  inventory rules on.
- **The renumbering is complete.** The prompts after the chunks are
  renumbered, the §10 note says why, the mapping table is composed, and
  references outside merged learnings, migrations and request files are
  updated. `go test .` passes (`protocol_test.go`).
- **Cross-repo impact is stated per repository.** The renumbering moves
  phases Enterprise cites (its E14 builds on instance identity), so it owes a
  downstream sync queued under §4.3.
- **Release:** the next MINOR after the latest tag (M23), with a
  `CHANGELOG.md` section that says this release changes no behaviour and what
  it plans. `make release-check` passes.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `prompts/implemented/`, and add
`docs/learnings/0026-bounded-lists-audit-and-plan-learnings.md`.

Its summary block MUST give:
- the decision's one line and its id;
- the count of lists by verdict;
- the chunks, by number, with the routes each takes;
- the enumerating test's name, and where its pending list lives;
- what the NEXT session, the first chunk, must know first.

Its Details carry the inventory table.
