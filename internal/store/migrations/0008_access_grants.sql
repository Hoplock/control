-- 0008 — access grants (prompt 0012, PLAN M10).
--
-- Scope: the grant object 0012 owns, the workflow requests that may precede
-- one, and the column that lets a decision record say which grant supplied its
-- access. Additive and forward-only: every new column on an existing table
-- arrives with a default, so the rows 0001 could already hold stay readable.
--
-- ---------------------------------------------------------------------------
-- A GRANT IS A POLICY INPUT, AND EXPIRY IS A PREDICATE, NOT A JOB
-- ---------------------------------------------------------------------------
--
-- Nothing in this schema has to run for a grant to stop working. A grant is
-- live exactly when the evaluation's time input falls inside
-- [not_before, expires_at) and it has not been revoked — `ListLive` asks that
-- question with the instant as a parameter, and `grants_live_by_subject_idx`
-- (0001) answers it. There is deliberately no `state` column that says
-- "expired": a column somebody has to update is a column a stuck job leaves
-- saying "active", and a stuck job would then be standing production access.
-- The only state that IS stored is revocation, because that is the one change
-- a clock cannot make.

-- ---------------------------------------------------------------------------
-- grants — the object, widened to what M10 says it carries
-- ---------------------------------------------------------------------------
ALTER TABLE grants
    -- THE SCOPE. `scope` (0001) is the scope's NAME — what a policy rule
    -- matches with `grant.scopes`, and so what the grant permits. These three
    -- are its SELECTOR — which targets it covers — and all three are ANDed.
    -- Empty means "every target the matching rule already covers": a grant
    -- never reaches a target no rule reaches, because it is an input to the
    -- engine and never a way around it.
    ADD COLUMN scope_targets text[] NOT NULL DEFAULT '{}',
    ADD COLUMN scope_labels  jsonb  NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN scope_zones   text[] NOT NULL DEFAULT '{}',

    -- WHY, AND WHO. `reason` is the creator's own words and `reason_code` an
    -- optional stable code for it. The creator is stored as the three facts a
    -- reader needs and nothing a reader would have to infer: the person (empty
    -- for a machine token), the credential that acted, and whether that
    -- credential was a break-glass one (M7).
    ADD COLUMN reason_code             text    NOT NULL DEFAULT '',
    ADD COLUMN reason                  text    NOT NULL DEFAULT '',
    ADD COLUMN created_by              text    NOT NULL DEFAULT '',
    ADD COLUMN created_by_principal    text    NOT NULL DEFAULT '',
    ADD COLUMN created_by_break_glass  boolean NOT NULL DEFAULT false,

    -- THE WORKFLOW PATH (Hoplock Enterprise E8). `request_id` is Control's own
    -- request (below); `approval_ref` (0001) keeps the workflow's reference for
    -- it; `approvers` are the subjects whose approval the workflow reported.
    ADD COLUMN request_id text   NOT NULL DEFAULT '',
    ADD COLUMN approvers  text[] NOT NULL DEFAULT '{}',

    -- THE EXTERNAL PATH (M16, populated by 0013). `external_ref` (0001) is the
    -- ticket, scan or incident; these are the system that asserted it, the
    -- window it asserted — recorded, never enforced: the deadline the proxy
    -- enforces already weighed it — and `additional_context`, a JSON string
    -- or a JSON object and nothing else, kept as the text that arrived.
    ADD COLUMN external_system          text NOT NULL DEFAULT '',
    ADD COLUMN external_window_start    timestamptz,
    ADD COLUMN external_window_end      timestamptz,
    ADD COLUMN external_additional_kind text NOT NULL DEFAULT '',
    ADD COLUMN external_additional      text NOT NULL DEFAULT '',

    -- REVOCATION. Who withdrew it and the reason the holder was SHOWN — the
    -- same text the session_kill carried, because "why did my session end"
    -- has one answer and the auditor should read the one the user read.
    ADD COLUMN revoked_by           text NOT NULL DEFAULT '',
    ADD COLUMN revoked_by_principal text NOT NULL DEFAULT '',
    ADD COLUMN revoke_reason        text NOT NULL DEFAULT '';

ALTER TABLE grants
    ADD CONSTRAINT grants_external_additional_kind_check
        CHECK (external_additional_kind IN ('', 'string', 'object')),
    -- Revocation facts exist only on a revoked grant. A reason with no
    -- revocation time would be a grant that reads as withdrawn and is live.
    ADD CONSTRAINT grants_revocation_facts_check
        CHECK (revoked_at IS NOT NULL
               OR (revoked_by = '' AND revoked_by_principal = '' AND revoke_reason = ''));

-- The operator's list, newest first, for a tenant and for one subject. Neither
-- is on the decision path — that is `grants_live_by_subject_idx` — so they are
-- plain indexes over history rather than partial ones over live access.
CREATE INDEX grants_by_created_idx
    ON grants (tenant, created_at DESC, grant_id);
CREATE INDEX grants_by_subject_idx
    ON grants (tenant, subject_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- decisions.grant_id — which grant supplied the access (M4)
-- ---------------------------------------------------------------------------
--
-- The explanation already names the grant inside its document. It is a column
-- as well because two questions are asked FROM the grant rather than from the
-- decision: "which sessions did this grant back", which revocation must answer
-- to end them, and "what did this grant let anybody do", which is the first
-- thing an auditor asks of a grant. Neither should be a scan of every
-- explanation the tenant has ever produced.
ALTER TABLE decisions
    ADD COLUMN grant_id text NOT NULL DEFAULT '';

CREATE INDEX decisions_by_grant_idx
    ON decisions (tenant, grant_id, decided_at DESC)
    WHERE grant_id <> '';

-- ---------------------------------------------------------------------------
-- grant_requests — what a registered workflow is deciding (ext.GrantWorkflow)
-- ---------------------------------------------------------------------------
--
-- A SEPARATE TABLE FROM `grants`, AND THAT IS THE SAFETY PROPERTY. A request
-- the workflow has not approved is not access, and the way to make sure it can
-- never be read as access is for it to live somewhere the decision path does
-- not read. A `state` column on `grants` would make "pending is not live" a
-- predicate every query has to remember; here it is a table no query of the
-- decision path names.
--
-- A request exists only when a workflow is registered. Without one, an
-- administrator's grant is created directly and nothing is written here.
CREATE TABLE grant_requests (
    tenant        text        NOT NULL,
    request_id    text        NOT NULL,

    -- What was asked for: the same scope and window a grant carries.
    subject_id    text        NOT NULL,
    scope         text        NOT NULL,
    scope_targets text[]      NOT NULL DEFAULT '{}',
    scope_labels  jsonb       NOT NULL DEFAULT '{}'::jsonb,
    scope_zones   text[]      NOT NULL DEFAULT '{}',
    not_before    timestamptz NOT NULL,
    expires_at    timestamptz NOT NULL,
    reason_code   text        NOT NULL DEFAULT '',
    reason        text        NOT NULL DEFAULT '',
    external_ref  text        NOT NULL DEFAULT '',

    -- Who asked, as the grant records its creator.
    requested_by             text        NOT NULL DEFAULT '',
    requested_by_principal   text        NOT NULL DEFAULT '',
    requested_by_break_glass boolean     NOT NULL DEFAULT false,
    requested_at             timestamptz NOT NULL DEFAULT now(),

    -- The workflow that is deciding, and its own reference for the request.
    -- An empty reference on a pending request means the submission was never
    -- confirmed — the workflow could not be reached — and Control resubmits
    -- under the same request id, which the workflow must treat as one.
    workflow_provider text NOT NULL DEFAULT '',
    workflow_ref      text NOT NULL DEFAULT '',

    -- Where it stands.
    state          text        NOT NULL DEFAULT 'pending',
    outcome_code   text        NOT NULL DEFAULT '',
    outcome_text   text        NOT NULL DEFAULT '',
    approvals      jsonb       NOT NULL DEFAULT '[]'::jsonb,
    -- The workflow approved a window wider than the one asked for, and
    -- Control narrowed it (ext.GrantDecision.Window says it records that).
    window_clamped boolean     NOT NULL DEFAULT false,
    grant_id       text        NOT NULL DEFAULT '',
    decided_at     timestamptz,
    polled_at      timestamptz,

    PRIMARY KEY (tenant, request_id),
    CONSTRAINT grant_requests_state_check
        CHECK (state IN ('pending', 'approved', 'denied', 'expired', 'cancelled', 'failed')),
    CONSTRAINT grant_requests_window_check CHECK (expires_at > not_before),
    -- A request is decided exactly when it has left `pending`.
    CONSTRAINT grant_requests_decided_check
        CHECK ((state = 'pending') = (decided_at IS NULL)),
    -- An approved request names the grant it produced, and nothing else does.
    CONSTRAINT grant_requests_grant_check
        CHECK ((state = 'approved') = (grant_id <> ''))
);

-- What the poller walks: a tenant's pending requests, least recently asked
-- about first, so one request the workflow keeps failing on cannot starve the
-- rest.
CREATE INDEX grant_requests_pending_idx
    ON grant_requests (tenant, polled_at NULLS FIRST, requested_at)
    WHERE state = 'pending';
