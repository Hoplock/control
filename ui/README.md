# UI

The management console: fleet health, "explain this decision", audit query,
policy authoring, and inventory. It is served from the server binary rather
than deployed separately, so a self-hosted install is one process plus Postgres.

It is a client of the north-bound API and holds no privilege the API does not
grant it (PLAN M2).

Built in **phase 0015**.
