-- 0023 — the directory source: every directory identity keyed by the directory
-- it comes from (prompt 0042; PLAN M7, M28, M15, M12, M18).
--
-- Scope: one column and a new primary key on each of `directory_links` and
-- `directory_groups`, one column and one check on `idp_connectors`. Every row
-- written before this migration is kept, and keyed under the source `default`.
--
-- ---------------------------------------------------------------------------
-- WHY THE DIRECTORY IS PART OF THE KEY
-- ---------------------------------------------------------------------------
--
-- 0021 keyed a directory identity on (tenant, external_id), and so asked a sync
-- that feeds several directories into one tenant to rewrite every id into a
-- namespaced string. Each customer's IdP would then have to emit that string at
-- login for a tied connector to find the person. Two directories that both
-- call somebody `00u1` are two people, and the directory they come from is
-- data, not a prefix to choose, escape and parse. So the registered sync names
-- the directory (`Source`) and every key carries it: a link and a group are
-- unique per (tenant, source, external_id).
--
-- ---------------------------------------------------------------------------
-- WHY EXISTING ROWS GO UNDER `default`
-- ---------------------------------------------------------------------------
--
-- A sync built against v0.27.0 named no directory, and its rows have to be
-- keyed somehow. They are keyed under `default`, which a sync rebuilt against
-- this release sends to carry on exactly as it was. One that sends another
-- name provisions new subjects, whose login names then collide loudly with the
-- old ones (`KindConflict`) rather than doubling silently. Rejected: refusing
-- to migrate a database that holds rows (an upgrade that cannot run); leaving
-- old rows with an empty source (a required field with a permanent exception);
-- adopting old rows into whichever source writes first (adoption, which 0040
-- refused, and the wrong answer the moment two directories write).
--
-- The column's default exists only to backfill. It is dropped at once, so a
-- write that names no source fails rather than landing in `default`.

ALTER TABLE directory_links ADD COLUMN source text NOT NULL DEFAULT 'default';
ALTER TABLE directory_links ALTER COLUMN source DROP DEFAULT;
ALTER TABLE directory_links DROP CONSTRAINT directory_links_pkey;
ALTER TABLE directory_links ADD PRIMARY KEY (tenant, source, external_id);

-- ---------------------------------------------------------------------------
-- WHAT DOES NOT CHANGE
-- ---------------------------------------------------------------------------
--
-- A subject is still one identity in one directory: 0021's unique
-- `directory_links_subject_key` on (tenant, subject_id) is kept, and it is the
-- key the decision path joins the deactivation by (M28, M5). The subject's
-- statement reads the link's `source` in the same join, and sends nothing more.
--
-- A group's members are the links of one tenant and one source whose
-- `group_ids` contain the group's id. 0021's GIN index on `group_ids` serves
-- that containment, and the tenant and the source are checked on the rows it
-- returns; no new index is built.

ALTER TABLE directory_groups ADD COLUMN source text NOT NULL DEFAULT 'default';
ALTER TABLE directory_groups ALTER COLUMN source DROP DEFAULT;
ALTER TABLE directory_groups DROP CONSTRAINT directory_groups_pkey;
ALTER TABLE directory_groups ADD PRIMARY KEY (tenant, source, external_id);

-- ---------------------------------------------------------------------------
-- idp_connectors.directory_source — which directory a tied login resolves in
-- ---------------------------------------------------------------------------
--
-- A connector tied to the directory names the claim carrying the person's
-- directory id (0021) and now the source that id is unique in. A login looks
-- the link up by (tenant, directory_source, claim value). A connector has both
-- or neither: a claim with no source could match nothing, and a source with no
-- claim ties nothing. Connectors tied before this migration resolve in
-- `default`, where their links now are.

ALTER TABLE idp_connectors ADD COLUMN directory_source text NOT NULL DEFAULT '';
UPDATE idp_connectors SET directory_source = 'default' WHERE directory_claim <> '';
ALTER TABLE idp_connectors ADD CONSTRAINT idp_connectors_directory_tie_check
    CHECK ((directory_claim = '') = (directory_source = ''));
