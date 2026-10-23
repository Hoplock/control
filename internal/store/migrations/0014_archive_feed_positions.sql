-- 0014 — the archive feed and its positions (prompt 0025; PLAN M8, M5, M12,
-- M15, §7).
--
-- With an `ext.ArchiveStore` registered, Control hands it every committed
-- record, per tenant and stream in ascending sequence, and retention never
-- removes what the archive has not confirmed. How far it has confirmed is a
-- fact that changes an answer — whether a retention pass may delete — so it
-- lives here and on no node only (M5). Nothing here rewrites a row, and with no
-- archive registered nothing writes to either addition.

-- ---------------------------------------------------------------------------
-- audit_feed_positions — how far a consumer has confirmed each stream
-- ---------------------------------------------------------------------------
--
-- One row per tenant, consumer and stream. `consumer` is `archive` for the
-- registered archive; it is a key rather than a constant so a later consumer
-- of the same feed keeps positions of its own beside it. `confirmed_seq` and
-- `confirmed_hash` are the last record the consumer confirmed, and only ever
-- move forward: a retention pass reads them under the stream's append lock and
-- deletes no further, and a position that moved backwards could only make it
-- delete less.
--
-- The lease is what keeps one node feeding a stream at a time without the
-- shared event plane: a node claims it with `lease_holder` and `lease_until`
-- (the database's clock, so nodes need not agree on theirs), and another node
-- takes it only once it has expired. Two nodes whose turns overlap deliver one
-- batch twice, which the archive tolerates; neither can skip a record, because
-- a position moves only to the end of a batch read from at or behind it.
CREATE TABLE audit_feed_positions (
    tenant         text        NOT NULL,
    consumer       text        NOT NULL,
    stream         text        NOT NULL,
    confirmed_seq  bigint      NOT NULL DEFAULT 0,
    confirmed_hash text        NOT NULL DEFAULT '',
    confirmed_at   timestamptz,
    lease_holder   text        NOT NULL DEFAULT '',
    lease_until    timestamptz NOT NULL DEFAULT 'epoch',

    PRIMARY KEY (tenant, consumer, stream),
    CONSTRAINT audit_feed_positions_confirmed_seq_nonnegative CHECK (confirmed_seq >= 0)
);

-- ---------------------------------------------------------------------------
-- audit_captures.archived_at — the archive confirmed this capture's bytes
-- ---------------------------------------------------------------------------
--
-- A capture crosses to the archive after its record has been confirmed, and is
-- confirmed on its own. A capture pass empties only captures confirmed here,
-- and a prefix pass deletes no record whose capture is not, while an archive is
-- registered.
ALTER TABLE audit_captures
    ADD COLUMN archived_at timestamptz;

-- The captures still waiting for the archive, which is what both the feed and
-- the retention gate ask about. In steady state it is the few the archive has
-- not reached yet, so the index stays small however large the table grows.
CREATE INDEX audit_captures_unarchived_idx
    ON audit_captures (tenant, record_id)
    WHERE archived_at IS NULL AND retained_at IS NULL;
