-- 0021 — directory provisioning: the sync sink's links and groups, a
-- connector's directory claim, and the read that ends a person's credentials
-- (prompt 0040; PLAN M15, M7, M28, M12, M18).
--
-- Scope: two tables, one column, two indexes. No row is rewritten.
--
-- ---------------------------------------------------------------------------
-- THE LINK IS THE JOIN, NOT THE SUBJECT ID'S SPELLING
-- ---------------------------------------------------------------------------
--
-- A directory names a person by its own stable identifier (`ExternalID`).
-- Control allocates the subject id the first time it links one, and this row
-- joins the two, the way `federated_identities` joins an IdP's subject. A
-- username is never the join: a directory administrator can change one, and a
-- join on it turns a rename into an account takeover when somebody else takes
-- the old name.
--
-- The row also holds what the directory last said about the person, in the
-- directory's own vocabulary — the attributes, the email, the group ids — so
-- that a group renamed or deleted later, or a new claim mapping, can re-derive
-- the subject without asking the directory again. None of it is a policy input:
-- only what the tenant's claim mapping makes of it reaches `subjects` (M7).
--
-- ---------------------------------------------------------------------------
-- THE DEACTIVATION LIVES HERE, NEVER ON `subjects`
-- ---------------------------------------------------------------------------
--
-- A deactivation is a standing denial the directory places and lifts (M28). It
-- is on the link so that a login's upsert or an operator's write of the subject
-- row can never clear it, and so that it can be kept for an id the directory
-- has not created yet: a deactivation that arrives before its create is kept
-- here, and the create provisions the subject already deactivated (0017's
-- problem of an end kept before its open).
--
-- `deactivated_at` is CONTROL'S clock, because `DeactivateSubject` carries no
-- time of its own. A re-activation lifts it only when the directory's time is
-- later than both the last applied one and this, so a directory clock behind
-- Control's delays a re-activation and never shortens a deactivation.
--
-- The decision path reads it in the statement that reads the subject and its
-- lock, by the unique (tenant, subject_id) index below (M5).
CREATE TABLE directory_links (
    tenant       text        NOT NULL,
    external_id  text        NOT NULL,
    -- Allocated by Control when the external id is first linked. Unique per
    -- tenant: one subject is one directory identity.
    subject_id   text        NOT NULL,
    -- The registered sync's provider.
    provider     text        NOT NULL,
    linked_at    timestamptz NOT NULL,
    -- Whether an upsert has been applied, and so whether a subject row was
    -- written. False for a link a deactivation made ahead of its create.
    provisioned  boolean     NOT NULL DEFAULT false,
    -- The directory's own UpdatedAt of the last applied upsert. NULL until
    -- one is applied.
    directory_updated_at timestamptz,
    -- What the directory last said, in its own vocabulary.
    username     text        NOT NULL DEFAULT '',
    display_name text        NOT NULL DEFAULT '',
    email        text        NOT NULL DEFAULT '',
    attributes   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    group_ids    text[]      NOT NULL DEFAULT '{}',
    -- The standing deactivation: when Control applied it, and the
    -- directory's reason code. NULL is active.
    deactivated_at      timestamptz,
    deactivation_reason text NOT NULL DEFAULT '',
    -- What a committed change still owes the revocation stream, so that a
    -- retry of the same call delivers it: a cache_invalidate for the
    -- subject, and the rest of a deactivation (its revocations and its kill).
    invalidate_owed     boolean NOT NULL DEFAULT false,
    deactivation_owed   boolean NOT NULL DEFAULT false,

    PRIMARY KEY (tenant, external_id)
);

-- DECISION PATH: the deactivation is joined onto the subject read by this.
CREATE UNIQUE INDEX directory_links_subject_key ON directory_links (tenant, subject_id);

-- A group renamed or deleted re-derives every member. Containment (`@>`), not
-- `= ANY`, is what a GIN index serves, so the members read is written that way.
CREATE INDEX directory_links_group_ids_idx ON directory_links USING gin (group_ids);

-- ---------------------------------------------------------------------------
-- directory_groups — the directory's groups, in its own vocabulary
-- ---------------------------------------------------------------------------
--
-- A group's name is what the tenant's claim mapping matches (`groups.map`), so
-- one mapping serves a login and the directory alike. A renamed group maps
-- nothing until the mapping names the new name: a rename narrows access and
-- never widens it.
--
-- A deleted group keeps its row as a tombstone (`deleted_at`, Control's clock),
-- so an upsert written before the delete and delivered after it changes
-- nothing.
CREATE TABLE directory_groups (
    tenant       text        NOT NULL,
    external_id  text        NOT NULL,
    provider     text        NOT NULL,
    name         text        NOT NULL DEFAULT '',
    directory_updated_at timestamptz,
    deleted_at   timestamptz,

    PRIMARY KEY (tenant, external_id)
);

-- ---------------------------------------------------------------------------
-- idp_connectors.directory_claim — a connector tied to the directory
-- ---------------------------------------------------------------------------
--
-- The claim in this connector's assertions that carries the directory's
-- ExternalID for the same person. A login through a connector that names one
-- reaches the subject the directory provisioned, or is refused; it never
-- creates a subject of its own beside the directory's, which would be the
-- second identity de-provisioning never reaches. Empty is a connector as it was.
ALTER TABLE idp_connectors ADD COLUMN directory_claim text NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- north_principals by subject — what a deactivation revokes
-- ---------------------------------------------------------------------------
--
-- A deactivation revokes every live north-bound credential of the person. The
-- partial index holds only those, so the revocation reads what it revokes.
CREATE INDEX north_principals_live_by_subject_idx
    ON north_principals (tenant, subject_id)
    WHERE subject_id <> '' AND revoked_at IS NULL;
