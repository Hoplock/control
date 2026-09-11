# UI

The management console: fleet health, "explain this decision", audit query,
policy authoring, and inventory. It is served from the server binary rather
than deployed separately, so a self-hosted install is one process plus Postgres.

It is a client of the north-bound API and holds no privilege the API does not
grant it (PLAN M2).

Its **visual design is specified, not improvised**: `ui/DESIGN.md` is the design
system this console is built to — tokens, type scale, component inventory,
states and motion — and PLAN M20 is the decision that makes it binding.

Built in **phase 0016** (renumbered from 0015 by the multi-instance revision,
PLAN §10).
