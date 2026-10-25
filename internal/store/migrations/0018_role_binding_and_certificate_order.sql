-- 0018 — the role bindings' and outstanding certificates' order, served whole
-- (prompt 0034; PLAN M26, M7, M12, M18).
--
-- Scope: two indexes added. No column, no table, no row rewritten, and no
-- index dropped.
--
-- ---------------------------------------------------------------------------
-- WHY NO INDEX SERVED EITHER PAGE
-- ---------------------------------------------------------------------------
--
-- Both lists are read one page at a time after a keyset position (M26), with
-- a row comparison over exactly the expressions each index below holds, so
-- the planner reads the page in order from the index and sorts nothing.
--
-- - Role bindings are listed per role, subject bindings by subject and then
--   group bindings by group: `(role, (subject_id IS NULL),
--   coalesce(subject_id, group_id)) > ($n, $m, $o)`. The two partial unique
--   indexes 0007 built are `(tenant, subject_id, role)` and
--   `(tenant, group_id, role)`: each holds one kind of holder, and by holder
--   before role. They stay, because they are what a bind's ON CONFLICT names.
--   The new index is unique as they are, because a holder holds a role once.
-- - Outstanding certificates are listed newest first, by issuance and then
--   serial: `(issued_at, serial) < ($n, $m)`. `ssh_certificates_outstanding_idx`
--   (0007) is `(tenant, valid_before) WHERE revoked_at IS NULL`: it serves
--   expiry, not issuance. It stays, because a compromise rotation
--   (`RevokeByCAKey`) still reads it, revoking every unrevoked certificate a
--   key signed whose `valid_before` is still ahead. The new index is partial
--   on the same predicate, because the list never reads a revoked row.
--
-- ---------------------------------------------------------------------------
-- THE LOCK
-- ---------------------------------------------------------------------------
--
-- This file runs in one transaction, so CONCURRENTLY is not available. A plain
-- CREATE INDEX blocks writes to its table while it builds: no role is bound or
-- unbound until `identity_role_bindings`'s build commits, and no certificate
-- is issued or revoked until `ssh_certificates`'s does. Reads continue while
-- the indexes build, the decision path's included, and nothing here drops an
-- index. Migrations are applied by an explicit command, never on boot (PLAN
-- §8), so the operator chooses when this runs; the changelog says so.

-- ---------------------------------------------------------------------------
-- identity_role_bindings — per role, holder kind and holder
-- ---------------------------------------------------------------------------
CREATE UNIQUE INDEX identity_role_bindings_order_key
    ON identity_role_bindings (tenant, role, (subject_id IS NULL), (coalesce(subject_id, group_id)));

-- ---------------------------------------------------------------------------
-- ssh_certificates — the outstanding ones, newest first
-- ---------------------------------------------------------------------------
CREATE INDEX ssh_certificates_issued_order_idx
    ON ssh_certificates (tenant, issued_at DESC, serial DESC) WHERE revoked_at IS NULL;
