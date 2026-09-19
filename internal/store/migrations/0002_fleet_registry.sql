-- 0002 — the fleet registry (prompt 0006).
--
-- Scope: what the fleet graph, its liveness, its declared capabilities and its
-- configuration rollout need (PLAN M6, M17, M18). `proxies` already existed
-- (0001); this migration widens it and adds the tables around it. Migrations
-- are forward-only, so 0001 is untouched.
--
-- The same two rules as 0001 hold for every table below (M12, M18): it carries
-- the `tenant` column, and the tenant is the first column of the primary key,
-- so a row cannot exist outside a tenant and no uniqueness constraint can
-- straddle two.
--
-- There is **one deliberate exception**, and it is the enrollment token's hash
-- (below). It is called out where it happens rather than here.

-- ---------------------------------------------------------------------------
-- proxies — widened for health, capabilities and config state
-- ---------------------------------------------------------------------------
--
-- `contract_version` is the vocabulary the proxy declared at enrollment. It is
-- a FLEET-READINESS signal — "can this zone be routed through yet" — and never
-- the authority on what a given connection may be answered with: the proxy
-- declares that per call in `policy_version` on the authorize request, and 0008
-- answers within that. A column cannot be stale; a request field cannot.
ALTER TABLE proxies ADD COLUMN contract_version integer NOT NULL DEFAULT 0;

-- The rungs, credential methods, platforms and per-platform device-field names
-- this proxy's BUILD declares it can provide (M17). Stored as the proxy sent
-- it: the device-field namespace is open, the contract enumerates no names, and
-- a registry that validated against a list of its own would reject exactly the
-- customer-written driver proxy D13 makes first-class.
ALTER TABLE proxies ADD COLUMN declared_capabilities jsonb NOT NULL DEFAULT '{}'::jsonb;

-- Health, as the console's fleet screen (0016) and an incident need it.
ALTER TABLE proxies ADD COLUMN session_count integer     NOT NULL DEFAULT 0;
ALTER TABLE proxies ADD COLUMN last_error    text        NOT NULL DEFAULT '';
ALTER TABLE proxies ADD COLUMN last_error_at timestamptz;

-- ---------------------------------------------------------------------------
-- proxy_enrollments — the grant a proxy enrolls against (0006)
-- ---------------------------------------------------------------------------
--
-- Enrollment is an administrative act. A proxy cannot enroll itself into a zone
-- it was not granted, because an auto-enrolling fleet lets anyone who can reach
-- this server insert a hop into other people's routes — which is not an
-- information leak, it is one party's session traversing another's kit.
--
-- So an operator pre-registers the proxy id, the zones it may claim, and a
-- one-time enrollment token; this row is what an enrollment is checked against.
-- `consumed_at` makes the token one-time: a second enrollment with the same
-- token is refused rather than re-issued.
CREATE TABLE proxy_enrollments (
    tenant        text        NOT NULL,
    proxy_id      text        NOT NULL,
    -- The zones this proxy may claim. Empty grants nothing, which is the
    -- fail-safe reading of a row somebody half-filled in.
    granted_zones text[]      NOT NULL DEFAULT '{}',
    -- SHA-256 of the token's secret half. The secret itself is never stored,
    -- so a dump of this table cannot enroll anything.
    token_hash    bytea       NOT NULL,
    expires_at    timestamptz,
    consumed_at   timestamptz,
    created_by    text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, proxy_id)
);

-- THE ONE GLOBAL UNIQUENESS CONSTRAINT IN THIS SCHEMA, and the reason is M18.
--
-- South-bound tenancy is resolved from the proxy's enrolment credential, so a
-- token hash that existed under two tenants would be a credential resolving to
-- two authorities — the vulnerability class M18 exists to close. Every other
-- constraint here is tenant-scoped precisely so two tenants may reuse a name;
-- a secret is the one thing they may not reuse.
--
-- Note what this does NOT license: no query looks a token up across tenants.
-- The token names its own tenant (see fleet.EnrollmentToken), the server
-- verifies the secret within that tenant, and the repository method still takes
-- a tenant like every other. This index makes a collision impossible rather
-- than improbable; it is not a lookup path.
CREATE UNIQUE INDEX proxy_enrollments_token_key ON proxy_enrollments (token_hash);

-- ---------------------------------------------------------------------------
-- proxy_edges — declared reachability, one row per zone a proxy can reach
-- ---------------------------------------------------------------------------
--
-- The fleet is a graph (M6) and this table is its edge set. An edge is declared
-- BY a proxy TOWARDS a zone, because that is what a proxy actually knows about
-- itself, and the concrete next proxy is resolved at path time from the zone's
-- live members (deterministically — lowest proxy id) unless the edge names one.
--
-- `direction` is proxy D11, and it is a routing decision rather than a proxy
-- config flag because only this server knows which downstream proxies currently
-- hold a live outbound relay registration:
--
--   * `dial`  — this proxy opens a connection to `address`. Needs an inbound
--               rule at the far end.
--   * `relay` — the far proxy has already registered an outbound relay
--               connection with this one (see relay_registrations). The
--               protected zone needs no inbound rule at all.
--
-- A `relay` edge with no live registration is NEVER downgraded to a dial: that
-- would punch through the boundary the mode exists to preserve. It drops out of
-- routing, and if it was the only path the answer is an outage (M11).
--
-- There is no tenant column on the edge's far side and there cannot be: an edge
-- exists within one tenant's subgraph, so a cross-tenant edge is not pruned
-- late, it is unrepresentable.
--
-- The primary key carries the DIRECTION and the pinned next proxy as well as the
-- zone, so a proxy may declare more than one way into one zone. That is not
-- generality for its own sake: in a segmented estate a proxy can legitimately
-- reach a zone by dialling one member and over a relay registration another
-- member holds, and a schema that allowed only one row per zone would force an
-- operator to pick — or force a later phase to pay a migration to un-pick it.
CREATE TABLE proxy_edges (
    tenant        text        NOT NULL,
    proxy_id      text        NOT NULL,
    to_zone       text        NOT NULL,
    direction     text        NOT NULL,
    -- Where to dial. Required on a `dial` edge, meaningless on a `relay` one.
    address       text        NOT NULL DEFAULT '',
    -- The proxy this edge reaches, when the operator pinned one. Empty means
    -- "whichever live proxy in to_zone", resolved deterministically.
    next_proxy_id text        NOT NULL DEFAULT '',
    -- Cost orders equal-viability paths. Lower wins; ties break stably.
    cost          integer     NOT NULL DEFAULT 1,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, proxy_id, to_zone, direction, next_proxy_id),
    CONSTRAINT proxy_edges_direction_check
        CHECK (direction IN ('dial', 'relay')),
    CONSTRAINT proxy_edges_cost_check
        CHECK (cost > 0),
    CONSTRAINT proxy_edges_dial_needs_address_check
        CHECK (direction <> 'dial' OR address <> '' OR next_proxy_id <> '')
);

-- ---------------------------------------------------------------------------
-- relay_registrations — which downstream proxies are registered right now
-- ---------------------------------------------------------------------------
--
-- The registration itself is proxy-to-proxy plumbing and not part of the
-- contract (proxy §6.1): the downstream proxy keeps one outbound connection
-- open to its upstream's registration listener. What this server needs is the
-- FACT of it, because that fact is what makes a `relay` edge viable, and the
-- upstream proxy is the only party that can report it.
--
-- `last_seen_at` is what goes stale. A registration nobody has confirmed
-- recently is not a routing option, for the same reason a silent proxy is not:
-- routing through one is an outage the user experiences as a hang.
CREATE TABLE relay_registrations (
    tenant              text        NOT NULL,
    upstream_proxy_id   text        NOT NULL,
    downstream_proxy_id text        NOT NULL,
    registered_at       timestamptz NOT NULL DEFAULT now(),
    last_seen_at        timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, upstream_proxy_id, downstream_proxy_id)
);

-- Pathfinding asks "what is registered with this upstream", which the primary
-- key serves. The reverse question — "where is this proxy registered" — is the
-- fleet view's, and it is worth an index because an operator asks it during an
-- incident.
CREATE INDEX relay_registrations_downstream_idx
    ON relay_registrations (tenant, downstream_proxy_id);

-- ---------------------------------------------------------------------------
-- proxy_configs — immutable configuration document versions (0006)
-- ---------------------------------------------------------------------------
--
-- A proxy's bootstrap config is local — it must be, to start at all — but
-- everything above it belongs here, so an operator configures a fleet rather
-- than N files.
--
-- Documents are scoped: `zone` for everything a zone's members share, `proxy`
-- for the overrides one member needs. Rows are immutable, because rolling out a
-- bad config must be survivable and a version that can be edited in place is a
-- rollback target that lies.
--
-- `document` is TEXT rather than JSONB, deliberately. `hash` is a digest of the
-- exact bytes, and jsonb is a parsed representation: it re-renders whitespace and
-- key order on the way out, so a document stored as jsonb would come back
-- semantically equal and byte-different, and the hash beside it would no longer
-- be the hash of it. Nothing queries inside a configuration document — the keys
-- are the PROXY's vocabulary, opaque to this server — so jsonb buys nothing here
-- and costs the one property the column has to keep.
CREATE TABLE proxy_configs (
    tenant     text        NOT NULL,
    scope_kind text        NOT NULL,
    scope_id   text        NOT NULL,
    version    bigint      NOT NULL,
    document   text        NOT NULL,
    hash       text        NOT NULL,
    created_by text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, scope_kind, scope_id, version),
    CONSTRAINT proxy_configs_scope_kind_check
        CHECK (scope_kind IN ('zone', 'proxy')),
    CONSTRAINT proxy_configs_version_check
        CHECK (version > 0)
);

-- ---------------------------------------------------------------------------
-- proxy_config_desired — which version of each scope is published
-- ---------------------------------------------------------------------------
--
-- `previous_version` is what makes rollback a first-class operation rather than
-- an operator retyping yesterday's document under pressure: it is the version
-- this scope was on before the current one, and rolling back re-publishes it.
CREATE TABLE proxy_config_desired (
    tenant           text        NOT NULL,
    scope_kind       text        NOT NULL,
    scope_id         text        NOT NULL,
    version          bigint      NOT NULL,
    previous_version bigint      NOT NULL DEFAULT 0,
    published_by     text        NOT NULL DEFAULT '',
    published_at     timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, scope_kind, scope_id),
    CONSTRAINT proxy_config_desired_scope_kind_check
        CHECK (scope_kind IN ('zone', 'proxy'))
);

-- ---------------------------------------------------------------------------
-- proxy_config_state — desired vs running, per proxy
-- ---------------------------------------------------------------------------
--
-- The two scopes above compose into ONE document per proxy, materialised here
-- with its own monotonic version, because a proxy reports a single number and
-- the delivery path needs one row read rather than a merge per request.
--
-- Drift between desired and running is the whole reason this table is not just
-- a view: silent drift across a fleet is indistinguishable from a broken
-- rollout, so it has to be a value an operator and the API can both read.
CREATE TABLE proxy_config_state (
    tenant           text        NOT NULL,
    proxy_id         text        NOT NULL,
    desired_version  bigint      NOT NULL DEFAULT 0,
    desired_hash     text        NOT NULL DEFAULT '',
    -- TEXT for the same reason as proxy_configs.document above: desired_hash is
    -- the digest of these exact bytes.
    desired_document text        NOT NULL DEFAULT '{}',
    desired_at       timestamptz,
    -- What the proxy says it is actually running. 0 means it has never said.
    running_version  bigint      NOT NULL DEFAULT 0,
    running_hash     text        NOT NULL DEFAULT '',
    reported_at      timestamptz,

    PRIMARY KEY (tenant, proxy_id)
);

-- No second index here on purpose. The rollout view reads one tenant's states
-- whole (the fleet is small) and filters in Go, so an index on the drift
-- predicate would be a write cost with no reader — the rule 0001 set for this
-- schema.

-- ---------------------------------------------------------------------------
-- target_capabilities — what one TARGET can take (M17)
-- ---------------------------------------------------------------------------
--
-- The second source of capability facts, and the one a policy database cannot
-- hold: whether a target runs systemd, whether cgroup v2 is mounted, whether it
-- is a Linux host at all. `/v1/authorize` happens before the proxy has ever
-- touched the target, so a first-ever connection has nothing to put on the
-- request — `POST /v1/capabilities/report` (served by 0007) is the only path by
-- which this arrives at all.
--
-- Keyed by target AND by platform, because an `ephemeral-account` device is
-- observed through a driver and two drivers can see the same host differently.
--
-- `observed_at` is NULLABLE on purpose. A record with no observation time is
-- stale by definition — a capability with no date has no shelf life — and
-- making the column refuse NULL would force this server to invent a date, which
-- is precisely the fail-open the rule exists to prevent.
CREATE TABLE target_capabilities (
    tenant      text        NOT NULL,
    hostname    text        NOT NULL,
    target_port integer     NOT NULL DEFAULT 0,
    platform    text        NOT NULL DEFAULT '',
    execution   text[]      NOT NULL DEFAULT '{}',
    reach       text[]      NOT NULL DEFAULT '{}',
    observed_at timestamptz,
    detail      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- Which proxy reported it. An observation, never an authority: the report
    -- grants nothing, and the proxy re-checks the rung against the live target
    -- when it provisions.
    reported_by text        NOT NULL DEFAULT '',
    received_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, hostname, target_port, platform)
);

-- The pre-publish query (0014) asks "which targets can take this rung", which
-- is a scan of one tenant's records rather than a point lookup.
CREATE INDEX target_capabilities_hostname_idx
    ON target_capabilities (tenant, hostname);
