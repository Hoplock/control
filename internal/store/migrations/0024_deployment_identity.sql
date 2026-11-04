-- 0024 — the deployment's own identity, its nodes' reports, its tenant set and
-- the supervisory feed (prompt 0046; PLAN M19, M12, M18, M25).
--
-- Scope: four new tables. Nothing written before this migration changes.
--
-- ---------------------------------------------------------------------------
-- WHY THREE OF THESE TABLES HAVE NO TENANT COLUMN
-- ---------------------------------------------------------------------------
--
-- M12 puts the tenant column on every table, and a table without one is the
-- thing it was written to prevent. `deployment`, `deployment_nodes` and
-- `supervision_log` are the exception, named in the schema test beside
-- `schema_migrations`, for the reason that one is: they describe the
-- DEPLOYMENT, which no tenant owns. The instance id is one per logical
-- deployment (a node never carries one, M19), a node serves every tenant, and
-- the supervisory feed is the deployment reporting on itself to a supervisor
-- its operator configured. Keying any of them by tenant would give a
-- deployment one identity per tenant — the exact "a supervisor counts nodes as
-- customers" error the prompt forbids, one level over.
--
-- `deployment_tenants` DOES carry the tenant column, and it is its key: it is
-- the deployment's tenant set (M18), one row per tenant this deployment has
-- served.
--
-- ---------------------------------------------------------------------------
-- WHY THE SUPERVISORY LOG IS NOT `event_log`
-- ---------------------------------------------------------------------------
--
-- 0027's log is per tenant by M18 — its stream row is the tenant's publish
-- lock and its subscribers are a tenant's proxies. The supervisory feed belongs
-- to no tenant, so it keeps the same shape (a position, an epoch, a floor below
-- which a resume is owed `resync`, the newest N kept) in a table of its own.
-- `deployment` is its publish lock, exactly as `event_streams` is a tenant's.

CREATE TABLE deployment (
    -- One row, ever: the CHECK makes a second impossible rather than unlikely.
    singleton              boolean     PRIMARY KEY DEFAULT true CHECK (singleton),
    instance_id            text        NOT NULL CHECK (instance_id <> ''),
    created_at             timestamptz NOT NULL,
    -- The supervisory feed's epoch, head and floor (0027's shape).
    log_epoch              text        NOT NULL CHECK (log_epoch <> ''),
    log_head               bigint      NOT NULL DEFAULT 0,
    log_floor              bigint      NOT NULL DEFAULT 0,
    -- Local revocation of the supervisory registration (M19). Set, the
    -- registration ends on every node without a restart; cleared, it may
    -- register again.
    supervision_revoked_at timestamptz,
    supervision_revoked_by text        NOT NULL DEFAULT ''
);

CREATE TABLE deployment_nodes (
    node_id     text        PRIMARY KEY CHECK (node_id <> ''),
    version     text        NOT NULL,
    started_at  timestamptz NOT NULL,
    -- When the node last wrote this row: the observation time of everything
    -- in `report`.
    reported_at timestamptz NOT NULL,
    -- A node that stopped on purpose says so; one that did not is a node
    -- that stopped reporting, which is a different health state.
    left_at     timestamptz,
    -- The node's own report, JSON, encoded by internal/instance.
    report      text        NOT NULL DEFAULT '{}'
);

CREATE TABLE deployment_tenants (
    tenant      text        PRIMARY KEY CHECK (tenant <> ''),
    first_seen  timestamptz NOT NULL,
    last_active timestamptz NOT NULL
);

CREATE TABLE supervision_log (
    seq       bigint      PRIMARY KEY,
    kind      text        NOT NULL CHECK (kind <> ''),
    body      text        NOT NULL,
    logged_at timestamptz NOT NULL,
    origin    text        NOT NULL DEFAULT ''
);
