-- 0016 — the shared event plane (prompt 0027; PLAN M9, M5, M12, M18, M15).
--
-- The event stream (`GET /v1/proxies/{proxy_id}/events`) was a broker in one
-- process: its ids a counter in memory, its replay a ring in memory, its live
-- subscriptions a map. On several nodes a revocation published on one reached
-- only the proxies whose streams that node held. What makes the stream the same
-- whichever node a proxy reaches is here: the log every node replays and fans
-- out from, in one order, and which proxies hold a stream on which node. The
-- coordinator's bus (ext.ClusterCoordinator) only tells a node to read; the
-- authority is these rows.
--
-- Nothing here rewrites a row: three new tables and a sequence.

-- ---------------------------------------------------------------------------
-- event_log_seq — the deployment's one order
-- ---------------------------------------------------------------------------
--
-- Every logged event takes its position from this sequence, so an event id
-- minted on one node names the same point on every other. A sequence, and not
-- the largest position in the log, because the log is pruned: a position
-- computed from what is left would hand out an id a proxy may still hold.
-- Positions are not contiguous — a rolled-back publish leaves a hole, and other
-- tenants' events are interleaved — and nothing reads them as if they were.
CREATE SEQUENCE event_log_seq AS bigint MINVALUE 1 NO CYCLE;

-- ---------------------------------------------------------------------------
-- event_streams — one tenant's stream: who minted its ids, how far it reaches
-- ---------------------------------------------------------------------------
--
-- `epoch` is in every id this tenant's stream hands out, so an id this log did
-- not mint — one from the in-process broker every release before this one ran,
-- or from another deployment's database — is unknown and answered `resync`,
-- never read as a position. `head_seq` is the tenant's last logged position.
-- `floor_seq` is the highest position pruned from the tenant's log: a proxy
-- resuming from before it is owed an event that is gone, so it is answered
-- `resync`; from at or after it, everything it missed is still here.
--
-- THE ROW IS THE TENANT'S PUBLISH LOCK. A publish takes it FOR UPDATE before it
-- takes a position and holds it to its commit, so within a tenant positions are
-- committed in order: a reader that sees position N sees every earlier one of
-- the tenant's that will ever exist. That is what lets any node fan out by
-- reading "after the last one I delivered", and never skip one that commits
-- later.
CREATE TABLE event_streams (
    tenant    text   NOT NULL,
    epoch     text   NOT NULL,
    head_seq  bigint NOT NULL DEFAULT 0,
    floor_seq bigint NOT NULL DEFAULT 0,

    PRIMARY KEY (tenant),
    CONSTRAINT event_streams_epoch_shape CHECK (epoch ~ '^[0-9a-f]{8,32}$'),
    CONSTRAINT event_streams_floor_within_head CHECK (floor_seq >= 0 AND floor_seq <= head_seq)
);

-- ---------------------------------------------------------------------------
-- event_log — every event a proxy may be replayed
-- ---------------------------------------------------------------------------
--
-- One row per logged event: `session_kill`, `cache_invalidate`, `resync` and
-- `config_changed`. A heartbeat is never logged, and neither is the `resync`
-- line answering one reconnect; both are lines to one subscriber that name a
-- position rather than take one. `body` is the RevocationEvent as JSON without
-- its id or its heartbeat interval: the id is rendered from (epoch, seq), and
-- the interval is the one the node writing the line keeps. The audience is
-- stored beside it because it decides replay exactly as it decided delivery.
--
-- The newest `events.replay_buffer` rows of each tenant are kept; a publish
-- prunes past them and moves the tenant's floor in the same transaction.
CREATE TABLE event_log (
    tenant           text        NOT NULL,
    seq              bigint      NOT NULL,
    audience_all     boolean     NOT NULL,
    audience_proxies text[]      NOT NULL DEFAULT '{}',
    body             text        NOT NULL,
    published_at     timestamptz NOT NULL,
    origin           text        NOT NULL DEFAULT '',

    PRIMARY KEY (tenant, seq),
    CONSTRAINT event_log_seq_positive CHECK (seq > 0),
    CONSTRAINT event_log_addressed CHECK (audience_all OR cardinality(audience_proxies) > 0)
);

-- ---------------------------------------------------------------------------
-- event_subscriptions — which proxy holds a live stream on which node
-- ---------------------------------------------------------------------------
--
-- The cache-hint gate (PLAN §5.4) and the fleet's liveness read whether a proxy
-- holds a live subscription ANYWHERE. Each node writes its own rows and reads
-- the others' into a copy in memory; the decision path reads only that copy
-- (M5). `seen_at` is the subscription's last successful write as its node last
-- recorded it, rewritten as it ages, so a row of a node that died stops being
-- believed where every reader already stops believing a stale subscription.
CREATE TABLE event_subscriptions (
    tenant   text        NOT NULL,
    proxy_id text        NOT NULL,
    node_id  text        NOT NULL,
    seen_at  timestamptz NOT NULL,

    PRIMARY KEY (tenant, proxy_id, node_id)
);
