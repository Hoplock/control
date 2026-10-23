-- 0015 — audit sink delivery (prompt 0026; PLAN M8, M5, M12, M15, §7).
--
-- Each registered `ext.AuditSink` is one more consumer of the feed 0014 built
-- for the archive, with positions of its own in `audit_feed_positions` under
-- the key `sink:<provider>`. That table was keyed by consumer from the start,
-- so a sink adds rows and no key changes. What a sink needs that the archive
-- does not is said here.
--
-- Nothing here rewrites a row. The two counts start at zero and the instant
-- empty, which is what every archive row already means: the archive gates
-- retention, so a pass never removes what it has not confirmed.

-- ---------------------------------------------------------------------------
-- audit_feed_positions — records a retention pass removed before a consumer
-- confirmed them, and whether its delivery is failing
-- ---------------------------------------------------------------------------
--
-- Retention does not wait on a sink (M8). When a pass removes records a sink
-- has not confirmed, delivery moves the sink's position to the stream's marker
-- and resumes after it, and the records it moved past are counted here:
-- `skipped_records` over the row's life, and `skipped_behind` since the sink
-- last confirmed through the stream's head, which a confirmation that reaches
-- the head sets back to zero. The control stream's `operator.audit.retained`
-- record for the pass is what explains the gap to the sink; these say how
-- large it was to an operator.
--
-- `failing_since` is the first failed attempt of the current run of them, and
-- empty while delivery succeeds. It is here rather than on the node that tried,
-- because the extension listing may be answered by any node (M5).
ALTER TABLE audit_feed_positions
    ADD COLUMN skipped_records bigint NOT NULL DEFAULT 0,
    ADD COLUMN skipped_behind  bigint NOT NULL DEFAULT 0,
    ADD COLUMN failing_since   timestamptz,
    ADD CONSTRAINT audit_feed_positions_skipped_counts CHECK (
        skipped_records >= 0 AND skipped_behind >= 0 AND skipped_behind <= skipped_records);
