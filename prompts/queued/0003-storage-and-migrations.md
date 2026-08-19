# 0003 — Storage layer & migrations

## Read first
- `docs/PROTOCOL.md` — session workflow, especially §3 (forward-only
  migrations, tenancy column).
- `docs/PLAN.md` — especially §2 (**M5, M8, M12, M13**), §3 (`internal/store`),
  §8 (migrations are applied by an explicit command).
- `docs/learnings/` — read summaries; open `0001` (config keys, CI shape).

## Objective
Put a real database under the server: schema, migrations, repositories, and a
test harness later phases can use without each inventing their own. Model only
what the phases through 0010 need — a schema written for features nobody has
designed yet is a guess that costs a migration to correct.

## In scope

### Migrations (`migrations/`)
Versioned, **forward-only**, plain SQL, applied by an explicit command
(`cmd/hoplock-control migrate` or a `policyctl`-style subcommand — pick one and
document it). Never on boot in production: two nodes starting together must not
race. Include a `--dry-run` that prints what would be applied.

Initial schema, all tables carrying the tenant column (M12) and every query
filtering on it:
- **subjects / identities** — subject id, source, display name, principals,
  groups, claims. Enough to answer an auth call; the IdP integration (0011)
  extends it.
- **targets** — hostname, zone, labels (key/value, indexed — policy matches on
  them), credential method hint.
- **proxies** — id, zone, public key, enrollment state, last heartbeat.
  0006 owns the semantics; the table lands here.
- **policy bundles** — versioned, immutable rows: the source, its hash, who
  uploaded it, when, and which version is active.
- **decisions** — the decision record (M4): decision id, subject, target, inputs
  digest, matched rule, obligations, the snapshot returned, timestamp.
- **audit records** — the log ingest destination (M8): `record_id` (client
  assigned, **unique** — this is the idempotency key), session id, kind,
  severity, payload, chain fields (0010 fills them; the columns exist here).
- **grants** — JIT access grants (M10): subject, scope, expiry, approval
  reference. 0012 owns manual grants and Hoplock Enterprise extends them with
  approval workflows; the table lands here and serves both.

Index deliberately and say why in a comment: the decision path (M5) is the only
latency-critical reader, and its queries are "identity by subject",
"target + labels by hostname", "live grants by subject", and "proxy by id".
Everything else may be slower.

### Repositories (`internal/store`)
- One interface per aggregate, hand-written SQL through `pgx`, no ORM (M13).
- Context-aware, with timeouts, and typed errors that distinguish **not found**
  from **failed** — collapsing those two is how a database outage becomes a
  `401` and violates M11 three phases later.
- A transaction helper, because authorize (0008) writes a decision record on the
  same path it reads policy inputs.

### Test harness
- Postgres-backed tests against a **real** database (container in CI, documented
  local setup). Do not fake SQL: the whole point of the repository layer is the
  SQL, and a fake tests the fake.
- Per-test isolation (transaction rollback or a fresh schema) so tests are
  order-independent and can run in parallel.
- A seeding helper later phases use to build fixtures without hand-writing
  inserts.

## Out of scope
- Policy semantics (0005), fleet semantics (0006), audit chaining (0010), grant
  workflow (0012, and its Enterprise extension). This phase provides tables and
  access, not behaviour.

## Acceptance criteria
- `make migrate` applies cleanly to an empty database, is idempotent on a second
  run, and `--dry-run` changes nothing.
- Every table has the tenant column and every repository method filters on it —
  add a test that a query without a tenant filter cannot reach another tenant's
  row (seed two tenants and assert isolation).
- Repository tests pass against a real Postgres in CI.
- Not-found and failure are distinguishable in the error API, with a test.
- The audit table's `record_id` uniqueness is enforced **by the database**, and a
  test proves a duplicate insert is rejected rather than deduplicated in Go —
  0010 depends on this being a database guarantee across concurrent writers.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0003-storage-and-migrations-learnings.md`. Summary block MUST
list every table and its purpose, the repository interfaces and their error
semantics, how to run a Postgres-backed test locally, the migration command, and
the indexes the decision path depends on.
