-- 0009 — external access context (prompt 0013, PLAN M16).
--
-- Scope: the two facts the external path still needed on a grant, and the scope
-- bindings that say what each integration may ever assert. Additive and
-- forward-only: every new column arrives with a default, so every grant 0008
-- could already hold stays readable and means what it meant.
--
-- ---------------------------------------------------------------------------
-- A CONFIRMED WINDOW IS A GRANT, NOT A PARALLEL PATH (M10, M16)
-- ---------------------------------------------------------------------------
--
-- A push that the integration's binding admits becomes a row in `grants`, with
-- origin 'external', created through internal/access like every other grant and
-- audited in the same transaction. The engine reads it the way it reads an
-- administrator's. Nothing here is a second table of "external access" that a
-- decision would have to know to consult — the one new table is what an
-- integration MAY assert, never what it has asserted.

-- ---------------------------------------------------------------------------
-- grants — the assertion's id, and how the window arrived
-- ---------------------------------------------------------------------------
ALTER TABLE grants
    -- THE ASSERTION'S OWN ID in the external system. It is what makes a push
    -- idempotent: the same id from the same system is the same window, not
    -- two — and a revoked window stays revoked however often it is pushed
    -- again, because the second push finds the first row. Empty for every
    -- grant no push produced.
    ADD COLUMN external_assertion_id text NOT NULL DEFAULT '',
    -- HOW THE WINDOW ARRIVED. 'push' is a push the integration's binding
    -- trusts on its own; 'push-probe' is the default composition M16 names —
    -- the push opened the window, and it counts only while the provider's
    -- probe confirms it at decision time. Empty for every grant no push
    -- produced. ('probe' alone never appears here: a window only a probe
    -- asserted is confirmed per decision and stored only in the decision
    -- record that relied on it.)
    ADD COLUMN external_mode text NOT NULL DEFAULT '';

ALTER TABLE grants
    ADD CONSTRAINT grants_external_mode_check
        CHECK (external_mode IN ('', 'push', 'push-probe')),
    -- An assertion id belongs to a system, and a push-produced grant names
    -- both. A row with one and not the other is a grant whose idempotency key
    -- means nothing.
    ADD CONSTRAINT grants_external_assertion_check
        CHECK ((external_assertion_id = '') = (external_mode = '')
               AND (external_assertion_id = '' OR external_system <> ''));

-- The idempotency key. Unique per tenant and system, so a second push of one
-- assertion cannot insert a second grant however two requests race: the loser
-- reads the winner's row and answers with it.
CREATE UNIQUE INDEX grants_external_assertion_key
    ON grants (tenant, external_system, external_assertion_id)
    WHERE external_assertion_id <> '';

-- ---------------------------------------------------------------------------
-- access_context_bindings — what an integration may EVER assert, per tenant
-- ---------------------------------------------------------------------------
--
-- Without this table the push receiver IS an access-granting API with somebody
-- else's software on the other end (proxy D15's warning). A binding is the
-- pre-registered scope M16 requires: which subjects the integration may grant
-- to, which targets it may name, the longest window it may open, and whether it
-- may open privileged access at all. A push outside it is refused and audited
-- as an attempted privilege escalation; a binding that names nobody, or every
-- target, cannot be written.
--
-- One binding per provider per tenant, and one grant scope per binding: "what
-- this integration may grant" is then one name a reviewer can read, and an
-- integration that needs two scopes is configured as two providers.
CREATE TABLE access_context_bindings (
    tenant   text NOT NULL,
    -- The provider's name (ext.AccessContextInfo.Name): the external system.
    provider text NOT NULL,

    -- 'push', 'probe', or 'push-probe' (M16's default composition).
    mode  text NOT NULL,
    -- The grant scope every window from this integration carries — what a
    -- policy rule matches with `grant.scopes`.
    scope text NOT NULL,

    -- WHO it may grant to: these subject ids, or members of these groups.
    subjects       text[] NOT NULL DEFAULT '{}',
    subject_groups text[] NOT NULL DEFAULT '{}',
    -- WHAT it may name: a selector of the grant scope's own shape — hostname
    -- patterns, required labels, zones — all ANDed. The labels and zones ride
    -- on every grant the integration produces, so a target that stops
    -- carrying them stops being covered at decision time too.
    targets       text[] NOT NULL DEFAULT '{}',
    target_labels jsonb  NOT NULL DEFAULT '{}'::jsonb,
    target_zones  text[] NOT NULL DEFAULT '{}',
    -- HOW LONG: the longest window it may open, before the server's own
    -- ceiling, which applies on top.
    max_window_seconds integer NOT NULL,
    -- WHETHER IT MAY OPEN PRIVILEGED ACCESS AT ALL. A scope the active policy
    -- marks privileged can be pushed only through a binding that says so:
    -- two people — the policy's author and the integration's — have to agree.
    privileged boolean NOT NULL DEFAULT false,
    -- WHICH CREDENTIALS may push for it: north-bound principal ids. Empty
    -- means none may, so a binding never accepts a push from a credential
    -- nobody named — least of all another integration's.
    push_principals text[] NOT NULL DEFAULT '{}',

    enabled     boolean NOT NULL DEFAULT true,
    description text    NOT NULL DEFAULT '',

    -- Who last wrote it, as a grant records its creator.
    updated_by             text        NOT NULL DEFAULT '',
    updated_by_principal   text        NOT NULL DEFAULT '',
    updated_by_break_glass boolean     NOT NULL DEFAULT false,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, provider),
    CONSTRAINT access_context_bindings_mode_check
        CHECK (mode IN ('push', 'probe', 'push-probe')),
    CONSTRAINT access_context_bindings_window_check
        CHECK (max_window_seconds > 0),
    -- A binding that may grant to nobody is a mistake; one that may grant to
    -- anybody is not expressible.
    CONSTRAINT access_context_bindings_subjects_check
        CHECK (cardinality(subjects) + cardinality(subject_groups) > 0),
    -- Likewise a binding that may name every target.
    CONSTRAINT access_context_bindings_targets_check
        CHECK (cardinality(targets) > 0 OR target_labels <> '{}'::jsonb
               OR cardinality(target_zones) > 0)
);
