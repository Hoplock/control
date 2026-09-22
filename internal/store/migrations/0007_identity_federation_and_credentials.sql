-- 0007 — federation, groups, roles, north-bound principals, and the SSH CA
-- (prompt 0011).
--
-- Scope: everything identity is made of once an IdP is in the picture, plus
-- the per-tenant certificate authority proxy D6a left a table for in 0001.
--
-- ---------------------------------------------------------------------------
-- NOTHING HERE STORES A SECRET A BREACH COULD USE
-- ---------------------------------------------------------------------------
--
-- Three kinds of secret pass through this phase and each one is handled
-- differently, on purpose:
--
--   * an IdP client secret is NEVER a row. `idp_connectors` names the
--     environment variable it is read from and stores nothing else about it,
--     so a database dump is not a set of federation credentials. The prompt's
--     acceptance criterion is "no IdP client secret appears in any log, error
--     or stored row", and the cheapest way to keep that true forever is for
--     there to be no column it could be written to.
--   * a north-bound token is stored as a SHA-256 digest, the same shape 0007
--     used for `proxy_api_tokens`: the presented string is hashed and compared,
--     and the row cannot reconstruct the credential.
--   * a CA private key HAS to come back — it signs — so it is stored as
--     AES-256-GCM ciphertext under a key-encryption key the process is
--     configured with and the database never sees. `tenant_ca_keys.private_ref`
--     names the custodian ("software", "pkcs11:...") and the ciphertext column
--     is populated only by the software custodian.
--
-- ---------------------------------------------------------------------------
-- EVERY TABLE CARRIES THE TENANT, AND TWO OF THEM CARRY IT TWICE OVER
-- ---------------------------------------------------------------------------
--
-- M12 put the column on every table and M18 made it a dimension a caller
-- selects. Two tables here are where that stops being bookkeeping:
--
--   * `federated_identities` is keyed `(tenant, connector, external_subject)`.
--     A subject id is unique WITHIN a tenant, never globally, so two tenants
--     federating with different IdPs that both call someone `00u1` resolve to
--     two different subjects and never to each other.
--   * `tenant_ca_keys` (created in 0001, filled here) is keyed `(tenant,
--     key_id)` and one tenant's targets must not trust another tenant's CA.
--     Key material is per tenant from the first issuance.

-- ---------------------------------------------------------------------------
-- groups — first-class records, from either source (M7)
-- ---------------------------------------------------------------------------
--
-- `subjects.groups` already carries the names policy matches on, and it stays
-- the decision path's read: one row, no join, on the hot path (M5). This table
-- is the AUTHORITY those names are computed from, so that a group has an
-- identity an operator can rename, describe, and grant a role to — and so that
-- a mapped IdP group and a local one are the same kind of thing. A rule must
-- not be able to tell where a group came from, which is why `source` is
-- recorded for the audit trail and matched on by nothing.
CREATE TABLE identity_groups (
    tenant      text        NOT NULL,
    group_id    text        NOT NULL,
    display_name text       NOT NULL DEFAULT '',
    description text        NOT NULL DEFAULT '',
    -- 'local' or the connector name that asserted it.
    source      text        NOT NULL DEFAULT 'local',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, group_id)
);

-- Local membership. IdP-asserted membership is NOT stored here: it is computed
-- at login by the claim mapping and written onto the subject row, because a
-- membership this server did not decide is not one it may keep after the
-- assertion that carried it has gone.
CREATE TABLE identity_group_members (
    tenant     text        NOT NULL,
    group_id   text        NOT NULL,
    subject_id text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, group_id, subject_id),
    FOREIGN KEY (tenant, group_id) REFERENCES identity_groups (tenant, group_id) ON DELETE CASCADE
);

CREATE INDEX identity_group_members_by_subject_idx
    ON identity_group_members (tenant, subject_id);

-- ---------------------------------------------------------------------------
-- role bindings — RBAC, per tenant and only per tenant
-- ---------------------------------------------------------------------------
--
-- There is no `roles` table. The role set is FIXED and documented in Go
-- (`internal/identity/rbac.go`): a role whose permissions are rows is a role
-- whose permissions can be widened by an UPDATE, and "admin means these
-- fourteen permissions" is then a fact about the database rather than about
-- the product. A deployment that needs a different set needs a different
-- release, which is the point.
--
-- The binding is keyed by tenant, so a role granted in tenant A confers
-- nothing in tenant B — including to an administrator. That is not a filter
-- somebody remembers to apply; it is the primary key.
CREATE TABLE identity_role_bindings (
    tenant text NOT NULL,
    -- Exactly one of subject_id / group_id is set; the CHECK below enforces
    -- it. Binding to a group is what makes "the SREs are fleet admins" a
    -- statement about the group rather than a list that drifts.
    subject_id text,
    group_id   text,
    role       text        NOT NULL,
    granted_by text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT identity_role_bindings_one_holder
        CHECK ((subject_id IS NULL) <> (group_id IS NULL))
);

CREATE UNIQUE INDEX identity_role_bindings_subject_key
    ON identity_role_bindings (tenant, subject_id, role) WHERE subject_id IS NOT NULL;
CREATE UNIQUE INDEX identity_role_bindings_group_key
    ON identity_role_bindings (tenant, group_id, role) WHERE group_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- idp_connectors — per tenant, because two tenants federate differently (M18)
-- ---------------------------------------------------------------------------
CREATE TABLE idp_connectors (
    tenant     text        NOT NULL,
    connector  text        NOT NULL,
    -- 'oidc' or 'saml'. The Go side is a closed enum; this column records it.
    kind       text        NOT NULL,
    display_name text      NOT NULL DEFAULT '',
    enabled    boolean     NOT NULL DEFAULT true,
    -- The connector's non-secret configuration, as the broker parses it. The
    -- client secret is NOT in here: `client_secret_env` inside the document
    -- names the environment variable it is read from.
    config     jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, connector)
);

-- ---------------------------------------------------------------------------
-- claim_mappings — the versioned document a decision record names (M7)
-- ---------------------------------------------------------------------------
--
-- Versioned the way policy bundles are, and for the same reason: "why did
-- Alice match the `sre` rule" is answered by the mapping as often as by the
-- rule, so the decision record names the version it used and the version has
-- to still be readable years later. Immutable rows, one active per tenant.
CREATE TABLE claim_mappings (
    tenant     text        NOT NULL,
    version    integer     NOT NULL,
    -- The document exactly as it was submitted. text rather than jsonb for the
    -- reason 0006 gives: the digest covers these bytes.
    document   text        NOT NULL,
    digest     text        NOT NULL,
    active     boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by text        NOT NULL DEFAULT '',

    PRIMARY KEY (tenant, version),
    CONSTRAINT claim_mappings_version_positive CHECK (version > 0)
);

CREATE UNIQUE INDEX claim_mappings_one_active
    ON claim_mappings (tenant) WHERE active;

-- ---------------------------------------------------------------------------
-- federated_identities — the join between an IdP's subject and ours
-- ---------------------------------------------------------------------------
CREATE TABLE federated_identities (
    tenant          text        NOT NULL,
    connector       text        NOT NULL,
    -- The IdP's stable identifier (`sub`, or the SAML NameID). It is the join
    -- key and it must not change when a person's name or address does.
    external_subject text       NOT NULL,
    subject_id      text        NOT NULL,
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, connector, external_subject)
);

CREATE INDEX federated_identities_by_subject_idx
    ON federated_identities (tenant, subject_id);

-- ---------------------------------------------------------------------------
-- federation_flow_states — one row per outstanding login
-- ---------------------------------------------------------------------------
--
-- Rows rather than process memory, for the reason PLAN §6 gives about MFA
-- challenges: nothing makes a browser's callback land on the node that started
-- the flow, so an in-memory state answers "unknown state" to a user who did
-- nothing wrong on a deployment that has merely been scaled out (M5).
--
-- Single use, like an MFA challenge: `consumed_at` is set rather than the row
-- deleted, so a replayed callback is told "spent" rather than "never issued".
CREATE TABLE federation_flow_states (
    tenant       text        NOT NULL,
    state        text        NOT NULL,
    connector    text        NOT NULL,
    kind         text        NOT NULL,
    -- OIDC: the nonce the ID token must echo, and the PKCE verifier.
    nonce        text        NOT NULL DEFAULT '',
    pkce_verifier text       NOT NULL DEFAULT '',
    -- SAML: the AuthnRequest id the Response must name in InResponseTo.
    request_id   text        NOT NULL DEFAULT '',
    redirect_uri text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,
    consumed_at  timestamptz,

    PRIMARY KEY (tenant, state)
);

CREATE INDEX federation_flow_states_expiry_idx
    ON federation_flow_states (tenant, expires_at);

-- ---------------------------------------------------------------------------
-- north_principals — the north-bound credential model (M2, M18)
-- ---------------------------------------------------------------------------
--
-- One table for both principal kinds, because the thing the middleware needs
-- is the same for both: who is this, which tenants may it act in, and with
-- which roles in each. A session is a principal with an expiry and a subject
-- behind it; a token is a principal with a digest and no browser.
--
-- `scopes` is a JSON object mapping tenant -> array of roles. It is the set
-- M18 calls "the tenants it may act in": a tenant named in a path is a
-- SELECTOR within this set and never a widening of it. The row lives in the
-- tenant that issued the credential; a scope naming another tenant is
-- delegated administration, which is Enterprise's to govern (its E11) and is
-- written here only by an operator command.
CREATE TABLE north_principals (
    -- The issuing tenant. Every row has one, which is what keeps the
    -- repository's tenant argument meaningful (M18).
    tenant       text        NOT NULL,
    principal_id text        NOT NULL,
    -- 'token' or 'session'.
    kind         text        NOT NULL,
    -- The subject behind a session; empty for a machine token.
    subject_id   text        NOT NULL DEFAULT '',
    display_name text        NOT NULL DEFAULT '',
    -- 'local' for the break-glass path, or the connector that asserted it.
    source       text        NOT NULL DEFAULT 'local',
    -- BREAK-GLASS IS ASSERTED, NEVER INFERRED. A local login that looks like a
    -- normal one is an audit failure (M7), so the flag is written at the
    -- moment the credential is minted and travels with the principal from
    -- there into every decision and audit record it touches.
    break_glass  boolean     NOT NULL DEFAULT false,
    -- The claim-mapping version that produced this principal's attributes, 0
    -- when no mapping was involved (a local login, a machine token).
    mapping_version integer  NOT NULL DEFAULT 0,
    -- SHA-256 of the presented secret, for a token; empty for a session,
    -- whose id IS the secret and is stored hashed in `session_digest`.
    token_digest text        NOT NULL DEFAULT '',
    session_digest text      NOT NULL DEFAULT '',
    scopes       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at   timestamptz,
    revoked_at   timestamptz,

    PRIMARY KEY (tenant, principal_id),
    CONSTRAINT north_principals_one_secret
        CHECK ((token_digest <> '') <> (session_digest <> ''))
);

-- The lookup the middleware does on every request: a digest, inside a tenant
-- the presented credential named itself (M22's shape, reused here).
CREATE UNIQUE INDEX north_principals_token_digest_key
    ON north_principals (tenant, token_digest) WHERE token_digest <> '';
CREATE UNIQUE INDEX north_principals_session_digest_key
    ON north_principals (tenant, session_digest) WHERE session_digest <> '';

-- ---------------------------------------------------------------------------
-- the SSH CA (proxy D6a, M7, M18)
-- ---------------------------------------------------------------------------
--
-- 0001 created `tenant_ca_keys` empty and said 0011 fills it. Two columns are
-- added here, both about custody:
--
--   * `private_key` is AES-256-GCM ciphertext, written only by the software
--     custodian. An `ext.KeyStore` implementation holding the key in an HSM
--     leaves it NULL and puts its own handle in `private_ref`.
--   * `trusted_until` is the rotation story in one column. A retired key stays
--     in the trust bundle until this instant so that certificates it already
--     signed keep working for their (short) remaining life; a compromise
--     rotation sets it to the rotation instant instead, and the key leaves the
--     bundle at once.
ALTER TABLE tenant_ca_keys
    ADD COLUMN private_key   bytea,
    ADD COLUMN trusted_until timestamptz,
    ADD COLUMN comment       text NOT NULL DEFAULT '';

-- Issued certificates. They are short-lived and the row outlives them on
-- purpose: "which certificate was minted for which session, against which
-- target, under which CA key" is the question an incident asks, and a
-- certificate that has expired is exactly the one somebody is asking about.
CREATE TABLE ssh_certificates (
    tenant        text        NOT NULL,
    serial        bigint      NOT NULL,
    key_id        text        NOT NULL,
    ca_key_id     text        NOT NULL,
    subject_id    text        NOT NULL,
    session_id    text        NOT NULL DEFAULT '',
    -- The principals the certificate is valid for: the login on the target,
    -- and nothing else.
    principals    text[]      NOT NULL DEFAULT '{}',
    -- The target this certificate was minted for. SSH itself cannot enforce
    -- it (a certificate is scoped by principal, validity and critical
    -- options, not by hostname), so this column is what makes the scope
    -- auditable: issuance is the enforcement point, and this is its record.
    target        text        NOT NULL DEFAULT '',
    target_port   integer     NOT NULL DEFAULT 0,
    source_address text       NOT NULL DEFAULT '',
    valid_after   timestamptz NOT NULL,
    valid_before  timestamptz NOT NULL,
    issued_at     timestamptz NOT NULL DEFAULT now(),
    revoked_at    timestamptz,
    revoked_reason text       NOT NULL DEFAULT '',
    -- The certificate as `ssh.MarshalAuthorizedKey` renders it. There is no
    -- private key here and there never will be: the proxy generates the key
    -- pair, this server signs the public half.
    certificate   text        NOT NULL,

    PRIMARY KEY (tenant, serial),
    FOREIGN KEY (tenant, ca_key_id) REFERENCES tenant_ca_keys (tenant, key_id)
);

CREATE INDEX ssh_certificates_outstanding_idx
    ON ssh_certificates (tenant, valid_before) WHERE revoked_at IS NULL;
CREATE INDEX ssh_certificates_by_subject_idx
    ON ssh_certificates (tenant, subject_id, issued_at DESC);

-- The serial is a per-tenant monotonic counter, for the reason the uid cursor
-- is: a serial that repeats makes a revocation list ambiguous.
CREATE TABLE ssh_certificate_serials (
    tenant text   NOT NULL,
    next   bigint NOT NULL DEFAULT 1,

    PRIMARY KEY (tenant),
    CONSTRAINT ssh_certificate_serials_forward CHECK (next > 0)
);

-- ---------------------------------------------------------------------------
-- subjects.break_glass — the flag, on the row the decision path already reads
-- ---------------------------------------------------------------------------
--
-- The decision record has to say "this subject's credential is a break-glass
-- one" without a second query on the hot path (M5), and `subjects` is already
-- read there. A subject created for break-glass carries the flag from the
-- moment it exists.
-- `mapping_version` is on the same row and for the same reason: a decision
-- record has to name the claim-mapping version that produced the attributes it
-- matched on (M4, M7), and "why did Alice match the sre rule" is answered by the
-- mapping as often as by the rule. Reading it from a second table on the hot
-- path would spend M5's budget on a number that never changes between logins.
ALTER TABLE subjects
    ADD COLUMN break_glass     boolean NOT NULL DEFAULT false,
    ADD COLUMN mapping_version integer NOT NULL DEFAULT 0;

-- ---------------------------------------------------------------------------
-- software_keys — Control's own key custody, behind `ext.KeyStore`
-- ---------------------------------------------------------------------------
--
-- `ext.KeyStore` exists because what varies between deployments is CUSTODY:
-- whether the private half may exist as bytes in this process at all. An
-- organisation with an HSM is not asking for different cryptography, it is
-- asking for the same cryptography with the key somewhere this process cannot
-- read it.
--
-- This table is the DEFAULT custodian — Control holding its own software keys —
-- and it is deliberately separate from `tenant_ca_keys`. That table is the CA's
-- LIFECYCLE (which key is active, which retired keys are still trusted, until
-- when); this one is one custodian's material. An HSM-backed key store writes
-- nothing here and `tenant_ca_keys.private_ref` names it instead, which is only
-- possible because the two concerns are not in one row.
--
-- `private_key` is AES-256-GCM ciphertext under a key-encryption key the
-- process is configured with and the database never sees. There is no code path
-- that writes a plaintext private key here: the software key store refuses to
-- start without a key-encryption key, because a CA private key in plaintext in
-- a database is not a default worth shipping.
CREATE TABLE software_keys (
    tenant      text        NOT NULL,
    -- The key's role plus its generation, e.g. `ssh-ca:2026-09-21-a1b2`. A new
    -- generation is a new name: rotation never replaces a key that
    -- certificates are still chained to.
    name        text        NOT NULL,
    algorithm   text        NOT NULL,
    public_key  bytea       NOT NULL,
    private_key bytea       NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, name)
);
