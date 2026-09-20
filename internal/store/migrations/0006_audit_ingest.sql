-- 0006 — log ingest, the tamper-evident chain, and the columns a query needs
-- (prompt 0010).
--
-- Scope: `audit_records`, plus one new table for session capture. 0003 created
-- `audit_records` as the destination and said outright that 0010 owns its
-- semantics; this is that phase, so the placeholder shape it left behind is
-- replaced rather than extended around.
--
-- ---------------------------------------------------------------------------
-- `body` IS THE RECORD, AND IT IS text RATHER THAN jsonb
-- ---------------------------------------------------------------------------
--
-- The chain hash covers this column's bytes exactly as they were hashed, so a
-- verifier re-reads the text and re-hashes it with nothing in between. That
-- forces the type: jsonb is a decoded document, not bytes — it drops key
-- order, drops duplicate keys, and rewrites number literals (`1e2` comes back
-- `100`) — so a hash taken over what arrived would stop matching what comes
-- back. Phase 0006 hit the same wall storing a configuration document beside
-- its hash and reached the same answer.
--
-- It replaces the `payload` column 0003 created, which nothing has ever
-- written: no shipped code path reaches this table before this phase, so the
-- column is dropped rather than migrated. Forward-only still holds — 0001 is
-- untouched and this file is the correction (PLAN §8).
--
-- ---------------------------------------------------------------------------
-- EVERY OTHER COLUMN IS A DERIVED INDEX
-- ---------------------------------------------------------------------------
--
-- The projections below are all recomputable from `body`. They exist because
-- the queries in PLAN §7 are the product — "every blocked command on env=prod
-- last week, who ran it, over which route, under which decision" is a join,
-- not a scan — and because a value buried in a JSON document is a value nobody
-- can index. The duplication is deliberate and one-directional: the body is
-- authoritative, the columns are how it is found, and the verifier reads only
-- the body.
ALTER TABLE audit_records DROP COLUMN payload;

ALTER TABLE audit_records
    -- The canonical JSON this server hashed. See above.
    ADD COLUMN body text NOT NULL DEFAULT '',

    -- The record's own fields, lifted out of the body so they can be filtered
    -- and joined. `subject`, `target` and `login` are the three an
    -- investigation arrives with; `message` is rendered prose and is here for
    -- display rather than for matching.
    ADD COLUMN subject text NOT NULL DEFAULT '',
    ADD COLUMN login   text NOT NULL DEFAULT '',
    ADD COLUMN target  text NOT NULL DEFAULT '',
    ADD COLUMN message text NOT NULL DEFAULT '',

    -- `event` is the producer's own event name (the proxy's `event`
    -- attribute), and it is a column rather than a attributes lookup because
    -- two of the events this phase exists to serve are identified by it and
    -- nothing else: the ephemeral-account MAPPING event and the device
    -- CONFIGURATION-CHANGE event both arrive under a `kind` they share with
    -- ordinary provisioning traffic. Burying either in a JSON document would
    -- make "who did this on that router" a substring search.
    ADD COLUMN event text NOT NULL DEFAULT '',

    -- `decision_id` is the join to `decisions` (M4, 0008 stores it on both
    -- sides); `proxy_id` is the enrolled proxy that ingested the record, which
    -- is where its tenant came from (M18) and is never read out of the body.
    ADD COLUMN decision_id text NOT NULL DEFAULT '',
    ADD COLUMN proxy_id    text NOT NULL DEFAULT '',

    -- The whole attribute map, for the filters no column anticipates.
    ADD COLUMN attributes jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Device fields (proxy phase 0016) are their own map, not a prefix search
    -- over `attributes`. They are OPAQUE and OPEN: the contract enumerates
    -- none of them and customer drivers add their own (proxy D13), so a column
    -- per name would be wrong the first time somebody shipped a driver. They
    -- are stored because on a device that is one unit partitioned into many,
    -- `device_field.vdom` is the difference between an administrator confined
    -- to one virtual domain and a GLOBAL one on the same host — and both
    -- records name the same host. None of them is ever credential material.
    ADD COLUMN device_fields jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- THE ENFORCEMENT RUNG IN FORCE — never the rung policy requested (PLAN
    -- §7, proxy D14). Four columns rather than one string because the two axes
    -- are separate questions asked by separate audiences ("what could this
    -- session run" and "what could it reach"), and because
    -- `enforcement_verified` must not be lost: it is false on an ATTESTED
    -- rung, where the target enforces something already and this system
    -- verified none of it. An attested rung that reads identically to an
    -- applied one turns an unverified claim into an apparent guarantee.
    --
    -- `enforcement_verified` is nullable on purpose. NULL means the record
    -- stated no rung at all, which is a different fact from "a rung that was
    -- not verified", and collapsing the two would invent a claim.
    ADD COLUMN enforcement_execution   text    NOT NULL DEFAULT '',
    ADD COLUMN enforcement_reach       text    NOT NULL DEFAULT '',
    ADD COLUMN enforcement_verified    boolean,
    ADD COLUMN enforcement_attested_by text    NOT NULL DEFAULT '',

    -- THE CREDENTIAL METHOD IN FORCE, and its position in the ladder (proxy
    -- D14). The rung index is what makes degradation queryable: 0 is the
    -- method policy preferred, and anything above it is the deployment
    -- accepting its second choice — a thing to count across an estate rather
    -- than reconstruct per session. Nullable for the same reason as above.
    ADD COLUMN target_auth_method text NOT NULL DEFAULT '',
    ADD COLUMN target_auth_rung   integer,

    -- Anything but `default` is a deliberate weakening of the proxy→target leg
    -- (PLAN §5.2, §7). Recording it is how an operator learns a route runs on
    -- SHA-1 from the record rather than by reading policy.
    ADD COLUMN algorithm_profile text NOT NULL DEFAULT '',

    -- GRANT CONTEXT (M16). The proxy copies it verbatim onto every record of
    -- the session and never parses it; this store does the same. `system` and
    -- `reference` are indexed because "show me every session that ran under
    -- this scan" is the query the field exists for.
    --
    -- `additional_context` is a JSON STRING OR A JSON OBJECT and the column
    -- admits both rather than coercing one into the other: it is stored as the
    -- text that arrived, with its type recorded beside it in
    -- `grant_additional_kind` (`string`, `object`, or empty for absent). An
    -- integration's bag of fields flattened into a sentence, or a sentence
    -- wrapped into an object, would both be this server putting words in that
    -- integration's mouth.
    ADD COLUMN grant_system          text NOT NULL DEFAULT '',
    ADD COLUMN grant_reference       text NOT NULL DEFAULT '',
    ADD COLUMN grant_window_start    timestamptz,
    ADD COLUMN grant_window_end      timestamptz,
    ADD COLUMN grant_additional_kind text NOT NULL DEFAULT '',
    ADD COLUMN grant_additional      text NOT NULL DEFAULT '',

    -- Session capture lives in its own table (below). These two say what is
    -- there without reading it: the byte count for size accounting, and the
    -- digest so the chain covers the captured bytes without carrying them.
    ADD COLUMN capture_bytes  integer NOT NULL DEFAULT 0,
    ADD COLUMN capture_sha256 text    NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- audit_captures — the bytes a replay needs, kept away from every query
-- ---------------------------------------------------------------------------
--
-- SIZE IS THE WHOLE REASON THIS IS A SEPARATE TABLE. A pty capture is a stream
-- of raw terminal writes and a busy session produces megabytes of them; left
-- on `audit_records` every `SELECT *` over a time range would drag them
-- through the network for nothing, and the table's own indexes would be
-- scattered across pages that are almost entirely payload.
--
-- The chain still covers them. `audit_records.capture_sha256` is inside the
-- hashed body, so altering a capture is detectable even though the bytes are
-- not in the chain's input — the verifier re-digests them when asked to.
--
-- No foreign key, matching the rest of this schema: a capture whose record was
-- removed is itself evidence, and a cascade would delete it in the one case
-- somebody wants it most.
CREATE TABLE audit_captures (
    tenant     text  NOT NULL,
    record_id  text  NOT NULL,
    bytes      bytea NOT NULL,

    PRIMARY KEY (tenant, record_id)
);

-- ---------------------------------------------------------------------------
-- The query surface (PLAN §7)
-- ---------------------------------------------------------------------------
--
-- `audit_records_session_idx` (tenant, session_id, recorded_at) already exists
-- from 0001 and is the "show me this session" lookup. The rest are here.

-- A time range on its own: every one of the queries below is bounded by one,
-- and a security team's first question is about a week rather than a session.
CREATE INDEX audit_records_recorded_idx
    ON audit_records (tenant, recorded_at DESC);

-- "What did this person do", and "what happened on this host".
CREATE INDEX audit_records_subject_idx
    ON audit_records (tenant, subject, recorded_at DESC)
    WHERE subject <> '';
CREATE INDEX audit_records_target_idx
    ON audit_records (tenant, target, recorded_at DESC)
    WHERE target <> '';

-- THE JOIN TO decisions (M4). 0008 stores `decision_id` on the decision record
-- and the proxy stamps it onto every log record of the session, and this index
-- is what makes resolving one into the other a lookup rather than a scan.
CREATE INDEX audit_records_decision_idx
    ON audit_records (tenant, decision_id)
    WHERE decision_id <> '';

-- The showcase query's leading filter: blocked commands, by severity, in a
-- window.
CREATE INDEX audit_records_kind_idx
    ON audit_records (tenant, kind, severity, recorded_at DESC);

-- The ephemeral-account mapping event and the device configuration-change
-- event are queried BY NAME and in their own right. On a device whose account
-- name had to drop its readable login segment, the mapping event is the only
-- place attribution exists — nothing on the target says who the account
-- belonged to — so it is a first-class record with its own index rather than
-- session metadata.
CREATE INDEX audit_records_event_idx
    ON audit_records (tenant, event, recorded_at DESC)
    WHERE event <> '';

-- "Show me every session that ran under this scan" (M16).
CREATE INDEX audit_records_grant_idx
    ON audit_records (tenant, grant_system, grant_reference, recorded_at DESC)
    WHERE grant_system <> '';

-- Device fields are an open namespace, so they are searched rather than
-- indexed by name: a GIN index answers `device_fields @> '{"vdom":"root"}'`
-- for any field a driver invents, which a b-tree per name could not.
CREATE INDEX audit_records_device_fields_idx
    ON audit_records USING gin (device_fields jsonb_path_ops);

-- The degradation query: "which routes accepted their second-choice
-- credential", across the estate.
CREATE INDEX audit_records_auth_rung_idx
    ON audit_records (tenant, target_auth_method, target_auth_rung)
    WHERE target_auth_method <> '';
