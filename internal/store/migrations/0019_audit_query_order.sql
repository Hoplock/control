-- 0019 — the audit query's order, served whole (prompt 0036; PLAN M26, M8,
-- M12, M18).
--
-- Scope: one index replaces one. No column, no table, no row rewritten.
--
-- ---------------------------------------------------------------------------
-- WHY THE OLD INDEX DOES NOT SERVE A PAGE
-- ---------------------------------------------------------------------------
--
-- Every audit list is read one page at a time after a keyset position (M26),
-- by recorded-at, stream and chain sequence: `(recorded_at, stream,
-- chain_seq) < ($n, $m, $o)` newest first, `>` with `?order=asc`. The key is
-- total because `(tenant, stream, chain_seq)` is unique
-- (`audit_records_chain_key`, 0001).
--
-- `audit_records_recorded_idx` (0006) is `(tenant, recorded_at DESC)`. It
-- holds the first component only, so every page sorts the records of the
-- instants it reads, and many records share an instant: a proxy's batch is
-- stamped to the second. The new index holds the whole key, so a page is read
-- in order, forwards newest first and backwards for `?order=asc`, and nothing
-- is sorted. It has the old index's leading columns, so every query that read
-- the old one can read it, and the old one is dropped.
--
-- The filtered indexes (subject, target, kind, event, grant) end in
-- `recorded_at DESC` and are left as they are: a page read through one sorts
-- only the ties at its boundary instant, and a page's correctness never
-- depends on an index.
--
-- ---------------------------------------------------------------------------
-- THE LOCK
-- ---------------------------------------------------------------------------
--
-- This file runs in one transaction, so CONCURRENTLY is not available. A plain
-- CREATE INDEX blocks writes to `audit_records` while it builds: no proxy's
-- log batch and no operator's record is stored until this commits, and a
-- proxy buffers to disk and resends meanwhile. Reads continue while it builds.
-- The DROP INDEX comes last, after the build, because it blocks reads too and
-- holds that lock to the commit. Migrations are applied by an explicit
-- command, never on boot (PLAN §8), so the operator chooses when this runs;
-- the changelog says so.

CREATE INDEX audit_records_order_idx
    ON audit_records (tenant, recorded_at DESC, stream DESC, chain_seq DESC);

DROP INDEX audit_records_recorded_idx;
