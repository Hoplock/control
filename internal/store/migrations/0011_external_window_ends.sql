-- 0011 — ending an external window (prompt 0017, PLAN M16, M10).
--
-- Scope: one table. An external system may now END a window it opened, and an
-- end that names a window which became a grant revokes that grant through
-- internal/access (M10) — a column `grants` already has. What has nowhere to
-- live is an end that names NO window: one that arrived before its open.
--
-- ---------------------------------------------------------------------------
-- AN ENDED WINDOW STAYS ENDED, EVEN ONE THAT ENDED FIRST (M16)
-- ---------------------------------------------------------------------------
--
-- Webhooks are not delivered in order. A scan that finished in seconds, a
-- change created and cancelled at once, a retry — each can deliver the end
-- before the open. If the end were only recorded, nothing would stop the open
-- that follows from creating exactly the grant the end was meant to prevent,
-- standing until its asserted end or the ceiling. So the id is KEPT here, and
-- an open under it is refused (`window_closed`, field `id`).
--
-- A grant made under an id and an end kept for it never both exist: the push
-- receiver writes either one only while it holds the assertion's advisory lock
-- (AccessContextWindowEnds().Lock), and it reads the other under the same
-- lock first.
--
-- Rows are never deleted. There is one per end that arrived with no window —
-- rare, and bounded by the integration's push rate — and no sweeper: a missing
-- row is exactly what would let an ended window reopen.
CREATE TABLE access_context_window_ends (
    tenant          text NOT NULL,
    -- The provider's name (ext.AccessContextInfo.Name): the external system
    -- whose id this is. Ids are scoped to their system, as on `grants`.
    external_system text NOT NULL,
    -- The assertion's own id in that system: the one an open must not reuse.
    assertion_id    text NOT NULL,
    -- What the end stated, recorded and never judged: its own reference, when
    -- it gave one, and when it says it ended the window.
    external_ref    text NOT NULL DEFAULT '',
    issued_at       timestamptz,
    -- When this server kept it.
    ended_at        timestamptz NOT NULL,
    -- Who pushed it, as a grant records its revoker: the person (empty for a
    -- machine token, which an integration's always is) and the credential.
    ended_by           text NOT NULL DEFAULT '',
    ended_by_principal text NOT NULL,

    PRIMARY KEY (tenant, external_system, assertion_id)
);
