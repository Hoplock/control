# Migrations

Versioned SQL for the Postgres schema, applied by an explicit command — never
automatically on boot, where two nodes starting at once must not race
(PLAN §8):

```sh
hoplock-control migrate --config config.yaml            # apply
hoplock-control migrate --config config.yaml --dry-run  # print, change nothing
make migrate                                            # same, via the Makefile
```

Three rules hold for every file here:

- **Forward-only.** Never edit a migration that has been merged; add a new one.
  The applied checksum is recorded, so an edited file is a startup error rather
  than a silent divergence between two deployments.
- **Every table carries the tenant column** and every query filters on it
  (PLAN M12), and the tenant is part of the primary key so a row cannot exist
  outside a tenant. Tenancy is a dimension the caller selects, not a process
  constant (M18) — see `internal/store` for the repository half of that.
- **Name files `NNNN_short_description.sql`**, four digits, applied in
  ascending numeric order. `schema_migrations` records which have run.

## Why the SQL is here and not in a top-level `migrations/`

`go:embed` cannot reach outside its own package directory, so SQL in a
top-level directory would need a top-level Go package to embed it — and `ext/`
is the only non-internal package this module has (M15). The alternative, a
server that reads migration files off disk at runtime, gives up the
one-binary deployment for nothing. See `embed.go` and PLAN §3.
