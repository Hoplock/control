# internal/store

Postgres repositories, the forward-only SQL that backs them, and the test
harness later phases build fixtures with (PLAN M13, §3).

- `migrations/` — the schema, embedded into the binary. Read its README first.
- `repositories.go` — the whole repository surface in one place.
- `south.go` / `south_types.go` — the rows the south-bound API answers out of
  (0007): subject keys and passwords, MFA enrollments and challenges, target
  host keys, uid leases, and the proxy's channel credential.
- `errors.go` — the not-found/failure distinction M11 depends on.
- `storetest/` — the Postgres-backed harness (`storetest.New(t)`).

## Applying migrations

```sh
make migrate                  # apply to the database in config.yaml
make migrate DRY_RUN=1        # print what would be applied, change nothing
```

Never on boot: two nodes starting together must not race to build the schema
(PLAN §8), so the server has no code path that migrates.

## Running the Postgres-backed tests locally

The repository layer *is* the SQL, so its tests run against a real database
rather than a fake. Point `HOPLOCK_TEST_DSN` at any Postgres you can create
schemas in — each test gets a private one and drops it afterwards, so an
existing database is not disturbed:

```sh
docker run -d --name hoplock-pg -p 5432:5432 \
    -e POSTGRES_PASSWORD=postgres postgres:16

export HOPLOCK_TEST_DSN='postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable'
go test ./internal/store/...
```

With the variable unset the Postgres-backed tests **skip**, so `go test ./...`
still works without a database. In CI they **fail** instead — a skipped test
reads as a passing one in the log, and CI is where these tests are supposed to
run.

## Two rules for anything added here

- **The tenant is an argument, never a field** (M18). Every repository method
  takes `(ctx, tenant, …)`; `TestRepositoryMethodsTakeATenant` fails the build
  if one does not, because a signature that cannot express a cross-tenant read
  is worth more than a convention a reviewer has to notice.
- **Absence and failure are different errors** (M11). Return `ErrNotFound`
  only when the row is genuinely not there. Everything a query cannot
  positively identify is an outage, because one layer up a not-found becomes a
  deny, and a database timeout read as a deny sends an operator to debug
  permissions during an outage.
