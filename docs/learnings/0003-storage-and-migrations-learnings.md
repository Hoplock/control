# 0003 — storage layer & migrations — Learnings

## Summary
- **What shipped:** the first migration, eight repositories over `pgx`, typed
  errors, a transaction helper, and a Postgres-backed test harness. No
  behaviour — later phases own the semantics of every table here.
- **Tables** (all `PRIMARY KEY (tenant, …)`): `subjects` (who is asking),
  `targets` (hostname/zone/labels), `proxies` (fleet, 0006), `policy_bundles`
  (immutable versions, one active), `decisions` (M4 records), `audit_records`
  (M8 ingest, `record_id` unique), `grants` (M10), `tenant_ca_keys` (empty;
  0011 fills it), `uid_allocation_cursors` (PLAN §4), plus `schema_migrations`
  — **the only table with no tenant column**, and a test names it as the one
  exception.
- **Migration command:** `hoplock-control migrate [--dry-run]`, or
  `make migrate [DRY_RUN=1]`. Never on boot. Applied under an advisory lock,
  one transaction per file, checksum recorded (an edited merged migration is an
  error).
- **The SQL moved**: it is in `internal/store/migrations/`, not a top-level
  `migrations/` — `go:embed` cannot reach outside its package and `ext/` is the
  module's only public package (M15). PLAN §3 revised.
- **Repositories:** `Subject|Target|Proxy|PolicyBundle|Decision|Audit|Grant|UIDCursor`
  from `store.Store` accessors; every method is `(ctx, tenant Tenant, …)` and a
  reflection test fails the build if one is not (M18).
- **Errors:** `store.Error{Op,Kind,Err}` + `IsNotFound/IsConflict/IsExhausted/
  IsInvalid/IsUnavailable`. `KindInternal` is the zero value: anything
  unclassified is an outage, never a deny (M11).
- **Decision-path indexes:** subject by PK; `targets_hostname_key`;
  `grants_live_by_subject_idx` (partial on `revoked_at IS NULL`); proxy by PK.
- **Local tests:** `export HOPLOCK_TEST_DSN=…` then `go test ./internal/store/...`;
  unset it and they skip, except in CI where they fail. See
  `internal/store/README.md`.
- **Decisions:** none added/amended/withdrawn — the §2 register is unchanged.
  M12/M18, M11, M8, M13 rendered. **Cross-repo:** none owed; `ext/` untouched.
- **NEXT session:** use `storetest.New(t)` and `storetest.Seed`; do not write a
  fake. `Advance` reuses a caller's transaction, so it is safe inside `InTx`.

## Details

### The uid cursor is the one thing here that is a correctness guarantee

Everything else in this phase is tables and access. The cursor is the
mechanism behind `POST /v1/uids/lease`'s invariant — a uid inside a granted
block is never inside any other grant, ever again — and it is enforced in three
places, deliberately:

1. **`SELECT … FOR UPDATE`** in `Advance`, inside a transaction. Two concurrent
   leases for one target can then never read the same value. A test runs 16
   independent `Store`s (16 pools, 16 connections) against one row released
   together and asserts the blocks are disjoint, strictly increasing, and cover
   the range exactly. A serial test would pass on the lost-update bug.
2. **A `BEFORE UPDATE` trigger** that raises on `NEW.next_uid < OLD.next_uid`.
   The acceptance criterion was "delete the Go-side guard and the write must
   still fail", so the test writes a lower cursor through raw SQL with no
   repository method in the path at all.
3. **A `CHECK (next_uid <= range_end)`**, so a cursor pushed past its range is
   refused rather than silently handing out uids outside it.

`RaiseFloor` (the storage half of `observed_floor`) uses
`LEAST(GREATEST(next_uid, $floor), range_end)`. `GREATEST` is why a stale
observation is ignored rather than obeyed; `LEAST` is why a target reporting a
floor past its range **exhausts** that target instead of failing the CHECK.
Burning a target's range is the stated exposure of trusting a relayed floor
(PLAN §4); it must not also be an outage. There is no `Release`, `Reclaim` or
`Free`, and a test asserts the interface declares none — if one is ever added,
that test is where the argument has to be made.

`Advance` needs a transaction for the row lock, but 0007 will call it from
inside `InTx` (recording the lease alongside the block). Opening a second
transaction on a second connection there would deadlock against the first, so
`uidRepo.inTx` runs in place when the `Store` is already transaction-bound and
only begins one when it holds the pool.

### Why the error type is a closed Kind rather than sentinels alone

`errors.Is(err, ErrNotFound)` is the API; `Kind` is what makes it decidable.
The zero value is `KindInternal` on purpose — a caller that forgets to classify
says "outage", and `wrap` maps only what it can positively identify (no rows,
unique violation, check violation, connect error, deadline). An unrecognised
SQLSTATE is `KindInternal`, because an unknown Postgres error read as "not
found" becomes a `401` one layer up, which is exactly M11's failure.

Two places where this shapes the SQL rather than the Go:

- `Audit.Append` has **no** `ON CONFLICT DO NOTHING`. `LogBatchResponse.accepted`
  is how a proxy observes its replay being deduplicated, so a resend must come
  back as `ErrConflict` for 0010 to count. Swallowing it would make every batch
  look fully accepted.
- `PolicyBundles.Activate` is one statement with a `wanted` CTE. Without it,
  "cleared the old active row and set nothing" and "there was nothing to clear"
  are the same row count, and a caller could deactivate the live bundle by
  asking for a version that does not exist.

### Test isolation is a schema, not a transaction

`storetest.New(t)` creates `hoplock_test_<random>`, opens the store with
`search_path` pointing at it, migrates, and drops the schema on cleanup. A
rolled-back transaction would have been cheaper and cannot hold the concurrency
test: N goroutines advancing one cursor need N connections, and connections
inside one transaction are one connection. Nothing in the SQL under test
qualifies a table name, which is what makes the `search_path` trick work.

`storetest.Seed` takes a `Fixture` per tenant, which is why the two-tenant
isolation test is two calls rather than two copies of a setup. Both tenants are
seeded with **identical ids** — that is the point: a query that forgot its
tenant filter finds a row and passes every other test in the package.

### Deviations from the plan

One, and it is recorded in PLAN §3 in place: the migration SQL is in
`internal/store/migrations/` and the top-level `migrations/` directory is gone.
`go:embed` cannot reach outside its own package directory, so keeping the SQL
at the top level would have required a second non-internal package, which M15
forbids in one line. The alternative — reading migration files off disk at
runtime — gives up the one-binary deployment for nothing. PLAN §3 already
assigned "migrations" to `internal/store` in the same tree, so the two halves
now agree.

### Things a later phase will want and does not have yet

- **No `tenants` table.** The tenant is a `text` column with no referential
  integrity behind it, because M18 resolves the tenant from the caller and
  nothing here needs to enumerate them. 0011 or 0014 may want one; adding it is
  a migration, and the columns it would key are already named.
- **`severity` has no CHECK constraint**, on purpose: it carries the contract's
  closed enum, and the contract is owned upstream (M1). A value added there
  would otherwise turn an ingest into a constraint violation — a `5xx` on the
  path whose whole job is to accept what the proxy already recorded.
- **`decisions.snapshot` is `jsonb`, not a typed column set.** 0008 writes the
  response verbatim; a schema for it would be a second copy of the contract.
- **No retention job.** M8 says retention is a policy applied by a documented
  job that records what it deleted. 0010 owns it.
- **`tenant_ca_keys.private_ref` is a reference, not key material.** The column
  exists so 0011 does not have to migrate a populated table, and what it points
  at is 0011's decision.

### CI

The `test` job gained a `postgres:16` service and `HOPLOCK_TEST_DSN`. The
harness **fails** rather than skips when `CI` is set and the DSN is not: a
skipped test reads as a passing one in the log, and this is the leg that is
supposed to run them.
