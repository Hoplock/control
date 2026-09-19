-- 0004 — what a decision record has to be able to answer (prompt 0008).
--
-- Scope: the `decisions` table only. 0001 created it with a digest of the
-- inputs, which fixes WHETHER a simulation is replaying the same question but
-- cannot answer WHAT the question was — and the first thing anybody debugging a
-- chained session asks is "which hop asked, and what had it already been
-- through" (PLAN §5.3). A hop trail is unrecoverable after the fact, so it is
-- stored rather than derived.
--
-- Additive and forward-only: every column arrives with a default, so the rows
-- 0001 could already hold stay readable. The digest stays exactly as it was —
-- it is a cheap identity for a set of inputs and the inputs beside it are not a
-- substitute for one.

ALTER TABLE decisions
    -- The whole input the evaluation saw, as a document: subject, target,
    -- context (including `conn.hop_trail`), and the live grants. It is the
    -- half of M4 the digest cannot serve, and it is what 0014's simulation
    -- replays.
    ADD COLUMN inputs jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- The explanation: effect, basis, the terms that matched, the deny
    -- reason, the bundle digest. `matched_rule` stays a column of its own
    -- because it is what an operator filters on.
    ADD COLUMN explanation jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- `allow` or `deny`. A denial has no snapshot, so without this column a
    -- deny and a decision whose snapshot failed to serialise look alike —
    -- and the deny path is the one that matters most (M4).
    ADD COLUMN effect text NOT NULL DEFAULT '',
    -- The hop that asked and the session it asked for. `session_id` is what
    -- the user is told alongside "access denied", so it is the column an
    -- operator arrives with.
    ADD COLUMN proxy_id text NOT NULL DEFAULT '',
    ADD COLUMN session_id text NOT NULL DEFAULT '';

-- An operator resolving a user's complaint has a session id, not a decision
-- id. The index is partial because a record written outside a session — a
-- simulation (0014) — has none, and those rows would otherwise be the bulk of
-- it.
CREATE INDEX decisions_by_session_idx
    ON decisions (tenant, session_id, decided_at DESC)
    WHERE session_id <> '';
