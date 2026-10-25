-- 0017 — the grant lists' order, served whole (prompt 0033; PLAN M26, M10, M12, M18).
--
-- Scope: three indexes replace three. No column, no table, no row rewritten.
--
-- ---------------------------------------------------------------------------
-- WHY THE OLD INDEXES DO NOT SERVE A PAGE
-- ---------------------------------------------------------------------------
--
-- Both lists are read newest first, one page at a time after a keyset position
-- (M26): `(created_at, grant_id) < ($n, $m)` for the operator's grants, and
-- `(requested_at, request_id) < ($n, $m)` for a requester's own requests. A
-- row comparison continues a list only when every component of its key sorts
-- the same way, and an index serves the page only when it holds that order
-- whole.
--
-- - `grants_by_created_idx` (0008) is `(tenant, created_at DESC, grant_id)`:
--   its tiebreak runs the other way.
-- - `grants_by_subject_idx` (0008) is `(tenant, subject_id, created_at DESC)`
--   and `grant_requests_by_subject_idx` (0010) is
--   `(tenant, subject_id, requested_at DESC)`: neither has a tiebreak at all.
--
-- So each is replaced by the same columns with the unique id last, descending
-- with the time before it. Every new index is built before any old one is
-- dropped, in this one transaction, so no committed state of the schema has
-- neither.
--
-- ---------------------------------------------------------------------------
-- THE LOCK
-- ---------------------------------------------------------------------------
--
-- This file runs in one transaction, so CONCURRENTLY is not available. A plain
-- CREATE INDEX blocks writes to its table while it builds: `grants` and
-- `grant_requests` take no inserts, revocations or decided requests until this
-- commits. Reads continue while the indexes build. A DROP INDEX blocks reads
-- too, the decision path's grant read included, and holds that lock to the
-- commit, so the three drops come last, after every build. Migrations are
-- applied by an explicit command, never on boot (PLAN §8), so the operator
-- chooses when this runs; the changelog says so.

-- ---------------------------------------------------------------------------
-- grants — the operator's list, for a tenant and for one holder
-- ---------------------------------------------------------------------------
CREATE INDEX grants_created_order_idx
    ON grants (tenant, created_at DESC, grant_id DESC);
CREATE INDEX grants_subject_order_idx
    ON grants (tenant, subject_id, created_at DESC, grant_id DESC);

-- ---------------------------------------------------------------------------
-- grant_requests — a requester's own list (0016)
-- ---------------------------------------------------------------------------
--
-- The holder stays the second column, because it is a predicate on every page
-- and never part of the position (M18).
CREATE INDEX grant_requests_subject_order_idx
    ON grant_requests (tenant, subject_id, requested_at DESC, request_id DESC);

-- ---------------------------------------------------------------------------
-- The replaced indexes, once every replacement exists
-- ---------------------------------------------------------------------------
DROP INDEX grants_by_created_idx;
DROP INDEX grants_by_subject_idx;
DROP INDEX grant_requests_by_subject_idx;
