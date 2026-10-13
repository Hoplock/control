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
