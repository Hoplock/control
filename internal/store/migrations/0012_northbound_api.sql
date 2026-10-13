-- 0012 — the north-bound API and policy lifecycle (prompt 0019; PLAN M17, M8,
-- M4, M9, M10, M12, proxy D18).
--
-- One migration for the phase, in sections. Every table carries the tenant
-- column (M12) and nothing here rewrites a row a merged migration wrote: where
-- a column is renamed, the rename is the whole change, because no producer has
-- ever written anything the new name does not mean.

-- ---------------------------------------------------------------------------
-- proxy_algorithm_declarations — what each proxy BUILD offers (Hoplock/proxy#69,
-- #72)
-- ---------------------------------------------------------------------------
--
-- The three declarations a proxy makes on every authorize request and nowhere
-- else in the contract: the floor levels it enforces with each level's key
-- exchanges, what each profile offers per axis, and every identifier it can
-- offer. The copy on each request is the AUTHORITY, and the decision path
-- judges against it; this row is the latest copy, a readiness signal the
-- publish-time report and the fleet view read.
--
-- They change together and only when a proxy's build does, so the decision
-- path writes this row only when the digest of what it was sent differs from
-- the one it last wrote (M5: one write per build change, never one per call).
CREATE TABLE proxy_algorithm_declarations (
    tenant      text        NOT NULL,
    proxy_id    text        NOT NULL,
    -- The three lists as one JSON document: algorithm_floors,
    -- algorithm_profiles and algorithms, under the contract's names.
    declaration text        NOT NULL,
    -- sha256 of declaration; the write is a no-op when it is unchanged.
    digest      text        NOT NULL,
    declared_at timestamptz NOT NULL,

    PRIMARY KEY (tenant, proxy_id)
);

-- ---------------------------------------------------------------------------
-- target_kex_observations — a target's key exchange, as a proxy saw it
-- (Hoplock/proxy#69)
-- ---------------------------------------------------------------------------
--
-- The second observation a capability report carries, merged BESIDE the rung
-- observation in target_capabilities and never over it: a report replaces only
-- the observation it carries. It has its own date.
--
-- Keyed by host and port, never by platform: the proxy's reports carry only
-- `target` and `target_port`, so an observation keyed by platform too would be
-- filed where a lookup for a device route's platform never finds it.
--
-- It grants nothing. The authorize response is the authority for a floor and
-- the handshake re-checks it on every connection; this is read at publish time
-- to show which targets raising a floor would break.
CREATE TABLE target_kex_observations (
    tenant      text        NOT NULL,
    hostname    text        NOT NULL,
    target_port integer     NOT NULL DEFAULT 0,
    -- none | modern-kex | pq-hybrid-kex: the highest level the target was
    -- observed to meet.
    floor_met   text        NOT NULL,
    -- The exchange a successful handshake used; empty after a failed one.
    negotiated  text        NOT NULL DEFAULT '',
    -- The target's whole list, when a failed negotiation revealed it.
    offered     text[]      NOT NULL DEFAULT '{}',
    observed_at timestamptz NOT NULL,
    reported_by text        NOT NULL DEFAULT '',
    received_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, hostname, target_port)
);

-- ---------------------------------------------------------------------------
-- audit_records — the records the proxy emits since its phases 0043–0046
-- (Hoplock/proxy#66, #68, #69, #71)
-- ---------------------------------------------------------------------------
--
-- ONE NAME PER FIELD. The credential method and its rung are recorded under the
-- only names the proxy emits, `credential_method` and `credential_rung`, and
-- the rung counts FROM 1 exactly as it arrived. The `target_auth_*` pair came
-- from the proxy's own contract text, which published it by mistake (#66
-- corrects it), and no producer has ever sent anything but `credential_rung`
-- counting from 1 — so this is a rename and no row is rewritten.
ALTER TABLE audit_records RENAME COLUMN target_auth_method TO credential_method;
ALTER TABLE audit_records RENAME COLUMN target_auth_rung TO credential_rung;

-- Every column below is a projection of the hashed body (migration 0006): it is
-- derived from the record's attributes when the record is ingested, holds
-- nothing the body does not, and is recomputable from it. A key that is absent
-- or does not parse projects nothing — the record is stored as it arrived.
--
-- Rows ingested before this migration project nothing on these columns. The
-- product ships its proxy and its server together and has no installed base to
-- backfill (PLAN §10, on 0024), so no UPDATE runs here.
ALTER TABLE audit_records
    -- The brokered certificate a session presented (#68), as the canonical
    -- decimal of the record's `credential_certificate_serial`. Text, joined to
    -- ssh_certificates.serial::text: the serial is a uint64 on the wire and
    -- is parsed as one, never through a float, and its canonical decimal is
    -- the one rendering both sides agree on.
    ADD COLUMN credential_certificate_serial text NOT NULL DEFAULT '',
    -- The policy in force on a target leg (#69): the floor (empty means NONE,
    -- never a floor), the bans as `axis:name` entries so each axis is a filter
    -- of its own and never a substring search, and the banned names no build
    -- implemented, as the proxy recorded them.
    ADD COLUMN algorithm_floor          text   NOT NULL DEFAULT '',
    ADD COLUMN algorithm_bans           text[] NOT NULL DEFAULT '{}',
    ADD COLUMN algorithm_bans_unmatched text[] NOT NULL DEFAULT '{}',
    -- What the target leg negotiated (#69), as `axis:value` entries: the key
    -- exchange (`target_kex_algorithm`), the host key, and the ciphers and
    -- MACs in both directions. The runbook's third step finds running
    -- sessions by these.
    ADD COLUMN negotiated text[] NOT NULL DEFAULT '{}',
    -- The drift feed (#66): one `device.config.change` per change the proxy
    -- made on a device, filtered by the object and the operation.
    ADD COLUMN device_change_op   text NOT NULL DEFAULT '',
    ADD COLUMN target_account     text NOT NULL DEFAULT '',
    ADD COLUMN device_object_kind text NOT NULL DEFAULT '',
    -- What a proxy could not deliver (#71): `logging.gap`, by cause, by span
    -- (the MISSING records' own timestamps, never the gap record's), by the
    -- record ids a refusal named, by kind and by how many were critical.
    ADD COLUMN gap_cause      text        NOT NULL DEFAULT '',
    ADD COLUMN gap_records    bigint,
    ADD COLUMN gap_bytes      bigint,
    ADD COLUMN gap_first_at   timestamptz,
    ADD COLUMN gap_last_at    timestamptz,
    ADD COLUMN gap_kinds      text[]      NOT NULL DEFAULT '{}',
    ADD COLUMN gap_critical   bigint,
    ADD COLUMN gap_record_ids text[]      NOT NULL DEFAULT '{}';

CREATE INDEX audit_records_certificate_idx
    ON audit_records (tenant, credential_certificate_serial)
    WHERE credential_certificate_serial <> '';
CREATE INDEX audit_records_negotiated_idx
    ON audit_records USING gin (negotiated);
CREATE INDEX audit_records_bans_idx
    ON audit_records USING gin (algorithm_bans);
CREATE INDEX audit_records_gap_span_idx
    ON audit_records (tenant, gap_first_at, gap_last_at)
    WHERE gap_cause <> '';
CREATE INDEX audit_records_gap_ids_idx
    ON audit_records USING gin (gap_record_ids);

-- ---------------------------------------------------------------------------
-- proxy_config_state — the report, and what was announced (proxy D18,
-- Hoplock/proxy#65)
-- ---------------------------------------------------------------------------
--
-- 0006 kept only the running version and hash, from a heartbeat field the
-- contract never carried. `POST /v1/proxies/{proxy_id}/config/report` is now the
-- only call that says what a proxy runs, and drift is derived from it: a proxy
-- reporting `pending_restart` holds the new document and has not finished; one
-- reporting `rejected` needs an operator; one reporting `fetch_failed` is
-- retrying. The report's strings are kept as they arrived, beside the parsed
-- running version 0006's columns hold (0 when the proxy runs on its bootstrap
-- file, or reports a version that is not one of this server's — which is drift,
-- not a 400).
--
-- Reports repeat — the proxy reports after every sync, every reconnect and
-- every 304 — so an unchanged one moves `last_reported_at` and nothing else,
-- and one older than the stored `report_reported_at` overwrites nothing.
ALTER TABLE proxy_config_state
    ADD COLUMN report_running_version text        NOT NULL DEFAULT '',
    ADD COLUMN report_running_hash    text        NOT NULL DEFAULT '',
    ADD COLUMN report_desired_version text        NOT NULL DEFAULT '',
    ADD COLUMN report_desired_hash    text        NOT NULL DEFAULT '',
    ADD COLUMN report_state           text        NOT NULL DEFAULT '',
    ADD COLUMN restart_required       text[]      NOT NULL DEFAULT '{}',
    -- Keys, never values: the proxy names settings and never quotes one.
    ADD COLUMN last_error             text        NOT NULL DEFAULT '',
    -- The proxy's own reported_at: when it reached this state.
    ADD COLUMN report_reported_at     timestamptz,
    -- When this server last heard any report, changed or not.
    ADD COLUMN last_reported_at       timestamptz,
    -- DELIVERY WITHOUT RECONNECT. The hash of the composed document this
    -- server last announced with a `config_changed` that the stream took. A
    -- publish commits the desired document first and announces after; a
    -- failed announcement — or a crash between the two — leaves this behind
    -- the desired hash, and the announcer re-emits every such proxy, at start
    -- and on a timer, until it is announced or a report confirms it. The
    -- proxy has no periodic fetch, so without this a notification lost while
    -- the stream stays healthy leaves it on the old document until something
    -- makes it reconnect.
    ADD COLUMN announced_hash         text        NOT NULL DEFAULT '',
    ADD COLUMN announced_at           timestamptz;
