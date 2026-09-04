# Migrations

Versioned SQL for the Postgres schema, applied by an explicit command — never
automatically on boot, where two nodes starting at once must not race.

Two rules hold for every file here:

- **Forward-only.** Never edit a migration that has been merged; add a new one.
- **Every table carries the tenant column** and every query filters on it
  (PLAN M12). The prototype is single-tenant and nothing in the API exposes
  tenancy, but retrofitting it into a populated audit store later is a migration
  nobody wants to run.

First migrations land in **phase 0003**.
