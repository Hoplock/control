-- 0001 — the initial schema.
--
-- Scope: exactly what phases 0003..0010 need (prompt 0003). A schema written
-- for features nobody has designed yet is a guess that costs a migration to
-- correct, so the semantics of most of these tables belong to a later phase —
-- the columns land here, the behaviour does not.
--
-- Two rules hold for every table below (PLAN M12, M18):
--
--   * it carries the `tenant` column, and the tenant is part of the primary
--     key, so a row cannot exist outside a tenant;
--   * every uniqueness constraint is scoped to the tenant, so two tenants may
--     each hold a target called `db-1` without colliding.
--
-- Indexes are deliberate. The decision path (M5) is the only latency-critical
-- reader in the system, and it asks four questions: "identity by subject",
-- "target + labels by hostname", "live grants by subject", and "proxy by id".
-- Each one names the index that serves it below. Everything else may be slower,
-- and an index nobody can name a query for is a write cost with no reader.

-- ---------------------------------------------------------------------------
-- subjects — who is asking (0007 authenticates them, 0011 federates them)
-- ---------------------------------------------------------------------------
--
-- Enough to answer an auth call: the principals a subject may present, the
-- groups policy matches on, and the claims an IdP asserted. Claim *mapping* is
-- explicit and versioned above this layer (M7); this table stores what was
-- asserted, not what it was mapped to.
CREATE TABLE subjects (
    tenant       text        NOT NULL,
    subject_id   text        NOT NULL,
    source       text        NOT NULL,
    display_name text        NOT NULL DEFAULT '',
    principals   text[]      NOT NULL DEFAULT '{}',
    groups       text[]      NOT NULL DEFAULT '{}',
    claims       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, subject_id)
);

-- DECISION PATH: "identity by subject" is served by the primary key. There is
-- no second index here on purpose — a lookup by display name or by claim is an
-- operator query, not a decision-path one.

-- ---------------------------------------------------------------------------
-- targets — what is being reached (0006 registers them, 0008 routes to them)
-- ---------------------------------------------------------------------------
CREATE TABLE targets (
    tenant            text        NOT NULL,
    target_id         text        NOT NULL,
    hostname          text        NOT NULL,
    zone              text        NOT NULL,
    -- Policy matches on labels (M3), so they are queried, not just stored.
    labels            jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- A hint only. The authoritative credential method is the one policy
    -- emits on the authorize response; this records what the target is known
    -- to accept so an operator can see a route that cannot work.
    credential_method text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, target_id)
);

-- DECISION PATH: "target + labels by hostname". The whole row is fetched by
-- hostname, labels included, so this unique index is the entire lookup — no
-- join, no second round trip.
CREATE UNIQUE INDEX targets_hostname_key ON targets (tenant, hostname);

-- Label matching by an operator or by policy simulation ("which targets carry
-- env=prod?"). GIN over jsonb because the keys are open: policy authors invent
-- them, so a btree per key is not available to us.
CREATE INDEX targets_labels_idx ON targets USING gin (labels jsonb_path_ops);

-- ---------------------------------------------------------------------------
-- proxies — the fleet (0006 owns enrollment and liveness semantics)
-- ---------------------------------------------------------------------------
--
-- The enrollment state is a closed set here rather than free text: a state the
-- server does not recognise is not a state it can act on, and 0006 will read
-- these values rather than invent parallel ones.
CREATE TABLE proxies (
    tenant            text        NOT NULL,
    proxy_id          text        NOT NULL,
    zone              text        NOT NULL,
    public_key        bytea       NOT NULL,
    enrollment_state  text        NOT NULL,
    last_heartbeat_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, proxy_id),
    CONSTRAINT proxies_enrollment_state_check
        CHECK (enrollment_state IN ('pending', 'enrolled', 'revoked'))
);

-- DECISION PATH: "proxy by id" is served by the primary key.
--
-- Pathfinding (M6) walks the fleet a zone at a time, and the fleet is small
-- relative to the estate, so a zone index is worth its write cost.
CREATE INDEX proxies_zone_idx ON proxies (tenant, zone);

-- ---------------------------------------------------------------------------
-- policy_bundles — versioned, immutable policy source (0005, 0014)
-- ---------------------------------------------------------------------------
--
-- Rows are never updated except to move `active`: the source and its hash are
-- what a decision record's explanation points back at (M4), and a bundle that
-- can be edited in place makes every historical explanation a guess.
CREATE TABLE policy_bundles (
    tenant      text        NOT NULL,
    version     bigint      NOT NULL,
    source      bytea       NOT NULL,
    hash        text        NOT NULL,
    uploaded_by text        NOT NULL,
    uploaded_at timestamptz NOT NULL DEFAULT now(),
    active      boolean     NOT NULL DEFAULT false,

    PRIMARY KEY (tenant, version),
    CONSTRAINT policy_bundles_version_positive CHECK (version > 0)
);

-- At most one active bundle per tenant, enforced by the database rather than
-- by whoever remembers to clear the old one. The compiled program is per
-- tenant (M18), and "which program is served" must have exactly one answer.
CREATE UNIQUE INDEX policy_bundles_one_active_key
    ON policy_bundles (tenant) WHERE active;

-- ---------------------------------------------------------------------------
-- decisions — the decision record (M4), written by 0008
-- ---------------------------------------------------------------------------
--
-- The proxy tells a user "access denied" and a session id, deliberately vague;
-- the operator resolves the decision id here into the whole story. That pair
-- only works if this side is total, so the inputs digest, the rule that
-- matched, the obligations emitted and the snapshot returned are all columns
-- rather than a log line.
CREATE TABLE decisions (
    tenant        text        NOT NULL,
    decision_id   text        NOT NULL,
    subject_id    text        NOT NULL,
    target_id     text        NOT NULL,
    inputs_digest text        NOT NULL,
    matched_rule  text        NOT NULL DEFAULT '',
    obligations   text[]      NOT NULL DEFAULT '{}',
    snapshot      jsonb       NOT NULL,
    decided_at    timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, decision_id)
);

-- "What did we decide for this subject last week?" — policy simulation (M4)
-- and incident review both read by subject over a time window.
CREATE INDEX decisions_subject_decided_at_idx
    ON decisions (tenant, subject_id, decided_at DESC);

-- ---------------------------------------------------------------------------
-- audit_records — the log ingest destination (M8), filled by 0010
-- ---------------------------------------------------------------------------
--
-- `record_id` is assigned by the client and is the idempotency key: a proxy
-- draining its disk buffer after an outage resends, and the SECOND insert must
-- be refused by Postgres. It is deduplicated by a unique constraint rather
-- than by a read-then-write in Go, because concurrent writers on different
-- nodes make the Go version a race with a plausible-looking test.
--
-- The chain columns are keyed PER TENANT per stream (M8, M18): a tenant's
-- history must verify on its own and be exportable without the rest, so two
-- tenants' records interleaved by arrival time still produce two chains that
-- each verify alone. 0010 fills `prev_hash`/`hash` in; the columns and the
-- sequence they are ordered by exist here.
--
-- `severity` is deliberately NOT a CHECK constraint. It carries the contract's
-- closed enum, and the contract is owned upstream (M1) — a value added there
-- would turn an ingest into a constraint violation, i.e. a 5xx, on a path
-- whose whole job is to accept what the proxy already recorded.
CREATE TABLE audit_records (
    tenant      text        NOT NULL,
    record_id   text        NOT NULL,
    stream      text        NOT NULL,
    chain_seq   bigint      NOT NULL,
    prev_hash   text        NOT NULL DEFAULT '',
    hash        text        NOT NULL DEFAULT '',
    session_id  text        NOT NULL DEFAULT '',
    kind        text        NOT NULL,
    severity    text        NOT NULL,
    payload     jsonb       NOT NULL,
    recorded_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, record_id),
    CONSTRAINT audit_records_chain_seq_positive CHECK (chain_seq > 0)
);

-- One chain per tenant per stream: the sequence is unique within it, which is
-- what makes a removed record detectable as a gap as well as a hash break.
CREATE UNIQUE INDEX audit_records_chain_key
    ON audit_records (tenant, stream, chain_seq);

-- Session reconstruction, and the query behind "show me this session".
CREATE INDEX audit_records_session_idx
    ON audit_records (tenant, session_id, recorded_at);

-- ---------------------------------------------------------------------------
-- grants — JIT access (M10), filled by 0012 and extended by Enterprise
-- ---------------------------------------------------------------------------
--
-- A grant's origin varies and is recorded: an administrator made it by hand,
-- an approval workflow produced it, or an external system asserted a window
-- and a provider confirmed it (M16). All three are the same object to the
-- engine — that is the point — but "explain why" that cannot name the ticket
-- is not an explanation, so the external reference is a column.
CREATE TABLE grants (
    tenant       text        NOT NULL,
    grant_id     text        NOT NULL,
    subject_id   text        NOT NULL,
    scope        text        NOT NULL,
    not_before   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    origin       text        NOT NULL,
    approval_ref text        NOT NULL DEFAULT '',
    external_ref text        NOT NULL DEFAULT '',
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, grant_id),
    CONSTRAINT grants_origin_check
        CHECK (origin IN ('manual', 'workflow', 'external')),
    CONSTRAINT grants_window_check CHECK (expires_at > not_before)
);

-- DECISION PATH: "live grants by subject". The predicate the decision path
-- applies on top of this is a time window, which the index carries as its
-- trailing columns so the scan is bounded by the subject rather than by how
-- many grants the tenant has ever issued. Revoked grants are excluded from the
-- index entirely: they are never a decision input again, and leaving them in
-- makes the hot index grow with history rather than with live access.
CREATE INDEX grants_live_by_subject_idx
    ON grants (tenant, subject_id, expires_at, not_before)
    WHERE revoked_at IS NULL;

-- ---------------------------------------------------------------------------
-- tenant_ca_keys — the SSH CA, per tenant (M7, M18, proxy D6a); 0011 fills it
-- ---------------------------------------------------------------------------
--
-- Empty at this phase, and here rather than later for M12's reason: one
-- tenant's targets must never trust another tenant's CA, so the table carries
-- the tenant from its first row rather than acquiring it in a migration over
-- live key material.
CREATE TABLE tenant_ca_keys (
    tenant      text        NOT NULL,
    key_id      text        NOT NULL,
    algorithm   text        NOT NULL,
    public_key  bytea       NOT NULL,
    private_ref text        NOT NULL,
    active      boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    retired_at  timestamptz,

    PRIMARY KEY (tenant, key_id)
);

-- At most one active CA per tenant, for the same reason as the active bundle.
CREATE UNIQUE INDEX tenant_ca_keys_one_active_key
    ON tenant_ca_keys (tenant) WHERE active;

-- ---------------------------------------------------------------------------
-- uid_allocation_cursors — the non-reuse floor (PLAN §4); 0007 serves it
-- ---------------------------------------------------------------------------
--
-- One integer per target, and the whole endpoint is its invariant:
--
--   a uid inside a granted block is never inside any other grant — for this
--   proxy or any other, ever again — whether the block was used, abandoned,
--   or allowed to expire.
--
-- Which is why there is no lease table, no expiry column, and no release path.
-- A granted block is gone. A schema that modelled leases as reclaimable rows
-- would have encoded the bug: a "recycled" block is a fresh ephemeral account
-- inheriting ownership of a torn-down one's files, invisible until it is an
-- incident. `lease_id` lives on whatever 0007 records for audit, not as a row
-- this layer can hand back.
--
-- The trigger below is the point of this table. The cursor is advanced by a
-- read-modify-write, and Go is the wrong place to enforce that it only ever
-- rises: a caller that skips the check, a future phase that writes the column
-- directly, or a repair script run at 3am all bypass a Go guard and none of
-- them bypasses Postgres.
CREATE TABLE uid_allocation_cursors (
    tenant     text        NOT NULL,
    target_id  text        NOT NULL,
    next_uid   bigint      NOT NULL,
    range_end  bigint      NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, target_id),
    -- The cursor may sit exactly at range_end: that is an exhausted range,
    -- which answers 409, not a corrupt row.
    CONSTRAINT uid_cursor_within_range CHECK (next_uid <= range_end),
    CONSTRAINT uid_cursor_range_ordered CHECK (range_end >= 0 AND next_uid >= 0)
);

CREATE FUNCTION uid_cursor_forward_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.next_uid < OLD.next_uid THEN
        RAISE EXCEPTION
            'uid allocation cursor may only advance: % -> % for tenant %, target %',
            OLD.next_uid, NEW.next_uid, OLD.tenant, OLD.target_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER uid_cursor_forward_only
    BEFORE UPDATE ON uid_allocation_cursors
    FOR EACH ROW EXECUTE FUNCTION uid_cursor_forward_only();
