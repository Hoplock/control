# 0036 — Bounded lists 8: the audit query

## Read first
- `docs/PROTOCOL.md`: §1, §3 (migrations are forward-only), §4, §5.
- `docs/PLAN.md`:
  - §2: the register, then **M26** (points 1, 2 and 4: the audit reads
    clamp today, and their order needs an index), **M8** (audit is
    append-only and per stream), **M18**, **M21**, **M11** and **M23**;
  - §3: `internal/audit` and `internal/httpapi/north`, the paragraph on lists;
  - §7: "Query", with its paragraph on M26;
  - §10: this phase's row.
- `docs/learnings/`: every summary. Read `0029`'s learnings whole. In
  `0028`'s, read the inventory rows for these routes. Read the `0021` summary:
  it audited these readers and their indexes.
- Code:
  - `internal/httpapi/north/audit.go`: `windowParams`, `handleAuditRecords`,
    `handleDrift`, `handleAlgorithmPolicyUnmet`, `handleGaps`,
    `handleCertificateSessions`;
  - `internal/audit/query.go`: `Find`, `DeviceConfigChanges`,
    `AlgorithmPolicyUnmet`, `Gaps`, `CertificateSessions`;
  - `internal/store/audit.go`: `Query`, `auditQuerySQL`, `auditWhere`,
    `auditOrder`, `boundLimit`;
  - every caller of `AlgorithmPolicyUnmet` outside north (the publish-time
    unmet warning in `internal/authoring`);
  - migrations `0006` and `0012` for `audit_records`' indexes;
  - 0029's `page.go`.

## Context estimate
- **Reads:** this prompt, M26 and §7's query (~250 lines), the summaries,
  0029's learnings, the functions above (~550 lines), and 0029's helpers.
- **Changes:**
  - five handlers on one shared reading of filters;
  - one paged store reader, with five service readers passing through;
  - one migration;
  - tests, the pending list, and `CHANGELOG.md`.
- **Why it fits:** five routes share one store statement, so the paging is
  written once. Each handler change is the same few lines. The joins and the
  aggregate are 0037's.

## In scope

### 1. One paged read
- `store.Audit()` gains `Page(ctx, tenant, q AuditQuery, store.Page)
  (store.Paged[AuditRecord], error)`.
  - It uses `auditWhere`'s predicates and `auditOrder`'s order, plus the
    keyset `(recorded_at, stream, chain_seq)` compared in the query's
    direction: `<` newest first, `>` with `Ascending`.
  - That key is total, because `(tenant, stream, chain_seq)` is unique.
  - Keep one function rendering the statement, as `auditQuerySQL` does, so
    the plan test reads what runs.
- `Query` stays for internal readers that read one session or one grant
  (`Session`, `UnderGrant`, `ByDeviceField`, and the per-session reads 0037
  takes).

### 2. Five routes
Each is a list keyed `[keyTime, keyString, keyInt]`.

| Route | List | Member | Service reader |
| --- | --- | --- | --- |
| `GET …/audit/records` | `audit_records` | `records` | `Reader.Find` → page |
| `GET …/audit/drift` | `audit_drift` | `changes` | `DeviceConfigChanges` → page |
| `GET …/audit/algorithm-policy-unmet` | `audit_unmet` | `unmet` | `AlgorithmPolicyUnmet` → page |
| `GET …/audit/gaps` | `audit_gaps` | `gaps` | `Gaps` → page |
| `GET …/audit/certificates/{serial}/records` | `certificate_records` | `records` | `CertificateSessions` → page |

- **Filters.** Every query parameter the route reads binds the cursor,
  canonical and sorted: repeated `kind` and `severity` values sorted, the
  window's `from` and `to` (RFC 3339), and `order`. The serial is in the
  path, and binds it too.
- **A window narrows a page but is never the cursor** (§7): many records
  share an instant.
- **The limit follows M26.** It defaults to 500, and above 10 000 it is
  refused, not clamped. `limitParam` and `windowParams` give way to
  `readPage`. Delete `limitParam` if nothing else reads it.
- **`certificate_records`** answers `{serial, records, truncated, next}`.
  The others answer `{<member>, truncated, next}`.
- **The publish-time unmet warning** reads `AlgorithmPolicyUnmet` with its
  own bound and reports a cut read (0022). Keep that working through the
  paged reader: one page of `UnmetLimit`, and `More` is the cut.

### 3. The index (a finding)
`audit_records_recorded_idx` is `(tenant, recorded_at DESC)`. It serves the
first key only, so every page boundary sorts the records of one instant.
Add `audit_records_order_idx` on `(tenant, recorded_at DESC, stream DESC,
chain_seq DESC)`, read backwards for `Ascending`. Drop
`audit_records_recorded_idx` in the same migration: the new index has the
same leading columns, so every query that read it can read the new one.
Prove both directions with `EXPLAIN`.

The filtered indexes (subject, target, kind, event, grant) end in
`recorded_at DESC`. A page read through one of them sorts only the ties at
its boundary instant. Correctness never depends on an index, so leave them;
the learnings say so.

**The build takes a write lock on `audit_records` for its duration.**
Migrations run in a transaction, so it cannot be `CONCURRENTLY`. Nothing is
deployed (0024), and the changelog says so for whoever is first.

### 4. The enumerating test
Move the five routes from `pendingBounded` to `pagedLists`. Seed records
with **two at the same instant in different streams**, so the walk proves
the tiebreak.

## Out of scope
- `…/audit/blocked-commands`, `…/audit/negotiated` and the per-session reads
  (0037).
- `GET …/audit/records/{record}`, which is one object.
- The retention passes, verification and the feeds, which are internal and
  read by chain position.

## Acceptance criteria
- The five lists answer in M26's shape and pass the bounded test.
- A walk visits every matching record exactly once in both directions, with
  ties at one instant, under concurrent ingest.
- `?limit=10001` is refused where it used to be clamped.
- `EXPLAIN` shows `audit_records_order_idx` serving the unfiltered order in
  both directions.
- The publish-time unmet warning still says when its read was cut.
- Two tenants (M18): no cursor crosses.

## Tests
- `internal/store`: `Page` with ties, both directions, two tenants, a
  concurrent ingest, and the plans.
- `internal/audit`: the five readers.
- `internal/authoring`: the unmet warning's cut.
- `internal/httpapi/north`: the handlers and the bounded test.

## Release (M23)
The next MINOR after the latest tag. Under `### Breaking`:
- `?limit=` above 10 000 is refused rather than clamped;
- the five answers gain `truncated` and `next`;
- the migration's lock on `audit_records`.

## Cross-repo impact
- `hoplock/enterprise`: none in `ext/` or `server/`. If its archive search or
  reports read Control's audit query, 0028's sync told it the shape.
- `hoplock/proxy`: none. The conformance suite reads one record by id, which
  is unchanged.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `prompts/implemented/`, and add
`docs/learnings/0036-bounded-lists-audit-query-learnings.md`.
