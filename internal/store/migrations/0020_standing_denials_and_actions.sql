-- 0020 — standing denials and the inbound action ledger (prompt 0039, PLAN M28,
-- M15).
--
-- Scope: three tables. A subject lock and a target quarantine are standing
-- denials an operator places and lifts, and that an inbound action handler
-- places through the same code (M28). The ledger is what makes an inbound
-- action idempotent on the id its requester gave it (M16's precedent).
--
-- ---------------------------------------------------------------------------
-- A STANDING DENIAL IS A TABLE OF ITS OWN, AND HAS NO FOREIGN KEY
-- ---------------------------------------------------------------------------
--
-- Both denials are keyed by the id they deny, and neither references the table
-- that names that id. Three things follow, and each is deliberate:
--
--   * a login's upsert, or `PUT …/subjects/{subject}`, never clears a lock:
--     those statements write `subjects`, and a lock is not a column of it;
--   * a lock outlives a deleted subject row, so deleting and re-creating a
--     subject cannot be the way out of one;
--   * the decision path reads a lock in the statement that reads the subject,
--     with a LEFT JOIN on the primary key it already uses (M5), and a lock for
--     an id that has no subject row still comes back.
--
-- A quarantine is the one place that guards the row it names, and it does so in
-- the repository rather than in the schema: deleting a quarantined target is
-- refused (`target_quarantined`), because the delete would otherwise lift the
-- quarantine silently.
--
-- Rows are deleted when the denial is lifted. The act is recorded in the audit
-- chain (`operator.subject.unlocked`, `operator.target.released`), which is
-- where history lives; a table that kept lifted rows would be one more thing
-- the decision path had to skip.

-- ---------------------------------------------------------------------------
-- subject_locks — who may not get access, whatever policy says
-- ---------------------------------------------------------------------------
CREATE TABLE subject_locks (
    tenant       text        NOT NULL,
    subject_id   text        NOT NULL,
    locked_at    timestamptz NOT NULL,
    -- Who placed it, as a grant records its creator: the person (empty for a
    -- machine token, or for an inbound action handler), the credential, and
    -- whether the credential was a break-glass one (M7).
    locked_by              text    NOT NULL DEFAULT '',
    locked_by_principal    text    NOT NULL,
    locked_by_break_glass  boolean NOT NULL DEFAULT false,
    -- Why, as a stable code a report can group by, and the text the holder was
    -- shown when their sessions were ended.
    reason_code  text        NOT NULL DEFAULT '',
    reason       text        NOT NULL DEFAULT '',
    -- What tied it to the incident that caused it, when an action placed it.
    external_ref text        NOT NULL DEFAULT '',
    action_id    text        NOT NULL DEFAULT '',

    PRIMARY KEY (tenant, subject_id)
);

-- DECISION PATH: the lock is read by its primary key, joined onto the subject
-- read. There is no second index, because nothing else asks the question.

-- ---------------------------------------------------------------------------
-- target_quarantines — what may not be reached by anyone, whatever policy says
-- ---------------------------------------------------------------------------
CREATE TABLE target_quarantines (
    tenant       text        NOT NULL,
    target_id    text        NOT NULL,
    quarantined_at timestamptz NOT NULL,
    quarantined_by             text    NOT NULL DEFAULT '',
    quarantined_by_principal   text    NOT NULL,
    quarantined_by_break_glass boolean NOT NULL DEFAULT false,
    reason_code  text        NOT NULL DEFAULT '',
    reason       text        NOT NULL DEFAULT '',
    external_ref text        NOT NULL DEFAULT '',
    action_id    text        NOT NULL DEFAULT '',

    PRIMARY KEY (tenant, target_id)
);

-- DECISION PATH: read by primary key, joined onto the target-by-hostname read.

-- ---------------------------------------------------------------------------
-- action_executions — the ledger of inbound actions
-- ---------------------------------------------------------------------------
--
-- One row per tenant and action id: what was asked, what Control answered, and
-- when. The same id with the same action returns the first answer and changes
-- nothing; the same id with a different action is a conflict (`KindConflict`).
-- Two calls under one id serialise on an advisory lock on the pair, so a pair
-- has one effect.
--
-- The row is written in the transaction that does the act, so there is no act
-- without its row and no row for an act that was not done.
--
-- It is never swept. Bounding its growth is a decision the first deployment
-- that has to make it should take, with its retention in front of it: a row
-- deleted early is an action id that can be executed twice.
CREATE TABLE action_executions (
    tenant       text        NOT NULL,
    action_id    text        NOT NULL,
    -- What was asked: the kind's code and the one selector it carries. A
    -- selector the kind does not take is empty.
    kind         text        NOT NULL,
    subject_id   text        NOT NULL DEFAULT '',
    session_id   text        NOT NULL DEFAULT '',
    target_id    text        NOT NULL DEFAULT '',
    grant_id     text        NOT NULL DEFAULT '',
    -- The first answer, returned again on every repeat: what the action
    -- changed, a code for a nothing-to-do, and when it was done.
    affected     integer     NOT NULL,
    reason_code  text        NOT NULL DEFAULT '',
    applied_at   timestamptz NOT NULL,
    -- The registered provider that asked.
    provider     text        NOT NULL,
    -- What a kill owes the stream, kept so a repeat can send the same words:
    -- the reason the holder is shown. Empty for a kind that kills nothing.
    holder_reason text       NOT NULL DEFAULT '',
    -- When the record of the act was written. A kill is recorded after it is
    -- published, so a repeat that finds this empty publishes again and writes
    -- the record the first execution owed; every other kind records in the
    -- transaction that does it.
    recorded_at  timestamptz,

    PRIMARY KEY (tenant, action_id)
);
