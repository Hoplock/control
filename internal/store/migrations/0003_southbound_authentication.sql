-- 0003 — what the south-bound API needs to answer for itself (prompt 0007).
--
-- Scope: the rows behind `/v1/auth/{cert,password,mfa/poll}`,
-- `/v1/hostkeys/report` and `/v1/uids/lease`, plus the proxy's own channel
-- credential. `/v1/capabilities/report` adds nothing here: it writes through
-- the capability store 0006 already defined, and a second home for that data
-- is exactly the drift M17 warns about.
--
-- Two rules from 0001 still hold for every table: the tenant is part of the
-- primary key (M12, M18), and every uniqueness constraint is scoped to it.
--
-- One rule is specific to this migration. The tables below hold CREDENTIAL
-- MATERIAL, and none of them holds a secret in a form that is usable if the
-- database is dumped: a password is a PBKDF2 digest with a per-subject salt, a
-- proxy's channel token is a SHA-256 of its secret half, and a public key is
-- public. The one place a plaintext secret exists is in transit, and it is
-- never written to a log, an error, or a row (PLAN §7).

-- ---------------------------------------------------------------------------
-- subject_keys — the public keys and certificates a subject may offer
-- ---------------------------------------------------------------------------
--
-- Keyed by FINGERPRINT rather than by subject, because that is the lookup:
-- `/v1/auth/cert` arrives with a key and a login and has to answer "whose is
-- this". The fingerprint is OpenSSH's — `SHA256:` + unpadded base64 of the
-- SHA-256 of the key blob — so it is the same string the proxy relays and the
-- same string an operator reads out of `ssh-keygen -lf`.
--
-- `valid_from`/`valid_to` are the certificate validity window, NULL for a bare
-- key, which has none. `revoked_at` is the revocation list: certificate
-- validation is where revocation bites, and authentication is never cached
-- (proxy §6.4), so this column is read on every call rather than distributed.
CREATE TABLE subject_keys (
    tenant         text        NOT NULL,
    fingerprint    text        NOT NULL,
    subject_id     text        NOT NULL,
    key_type       text        NOT NULL DEFAULT '',
    is_certificate boolean     NOT NULL DEFAULT false,
    valid_from     timestamptz,
    valid_to       timestamptz,
    revoked_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, fingerprint),
    CONSTRAINT subject_keys_window_ordered
        CHECK (valid_from IS NULL OR valid_to IS NULL OR valid_to > valid_from)
);

-- AUTH PATH: "whose key is this" is served by the primary key. The second
-- index answers the operator question — "what may this subject offer" — and an
-- operator revoking every key a leaver holds.
CREATE INDEX subject_keys_subject_idx ON subject_keys (tenant, subject_id);

-- ---------------------------------------------------------------------------
-- subject_passwords — the local password credential (0011 federates it away)
-- ---------------------------------------------------------------------------
--
-- A separate table from `subjects` rather than columns on it, for one reason:
-- when 0011 lands an IdP broker, the subjects stay and these rows go. A
-- credential that lived on the identity row would have to be migrated out of
-- it instead.
--
-- The digest parameters are columns rather than constants in Go because they
-- have to be able to move without invalidating every stored row: a hash is
-- verified with the parameters it was written with, and raising the iteration
-- count then applies to the next write. `algorithm` names the KDF so a second
-- one can exist beside the first for exactly as long as it takes to re-hash.
CREATE TABLE subject_passwords (
    tenant     text        NOT NULL,
    subject_id text        NOT NULL,
    algorithm  text        NOT NULL,
    iterations integer     NOT NULL,
    salt       bytea       NOT NULL,
    digest     bytea       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, subject_id),
    CONSTRAINT subject_passwords_iterations_positive CHECK (iterations > 0)
);

-- ---------------------------------------------------------------------------
-- subject_mfa — which second-factor provider a subject is enrolled with
-- ---------------------------------------------------------------------------
--
-- `config` is opaque to the orchestrator and belongs to the named provider.
-- That is what lets the deterministic provider this phase ships (a "pending
-- polls" counter and a scripted outcome, mirroring the proxy mock so the
-- conformance suite's MFA cases are reproducible here) and a real out-of-band
-- provider (0011) share one table: the orchestrator owns challenge lifetime,
-- poll-rate enforcement and single use, and knows nothing about how a factor
-- is actually delivered.
--
-- A subject with no row here has no second factor, which is why absence is a
-- real state and not a missing configuration.
CREATE TABLE subject_mfa (
    tenant     text        NOT NULL,
    subject_id text        NOT NULL,
    provider   text        NOT NULL,
    config     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, subject_id)
);

-- ---------------------------------------------------------------------------
-- mfa_challenges — the outstanding second factors this server is waiting on
-- ---------------------------------------------------------------------------
--
-- In the database rather than in a process, and the reason is M5: nothing on
-- the decision path may be node-local state. A proxy polls
-- `/v1/auth/mfa/poll` several times per challenge and there is nothing to make
-- those polls land on the node that issued it, so an in-memory map answers
-- "unknown token" — a 401, a DENY — to a user who did nothing wrong, on a
-- deployment that has simply been scaled out.
--
-- `state` is a closed set. `polls` and `last_polled_at` are what poll-rate
-- enforcement reads. `resolved_at` is what makes the token single-use: a
-- challenge that has answered once is never replayable, whichever way it
-- answered, so the row is kept and refused rather than deleted and forgotten
-- — a deleted row is indistinguishable from a token this server never issued,
-- and the two deserve different audit records even though they share a status
-- code.
CREATE TABLE mfa_challenges (
    tenant         text        NOT NULL,
    token          text        NOT NULL,
    subject_id     text        NOT NULL,
    login          text        NOT NULL,
    provider       text        NOT NULL,
    provider_ref   text        NOT NULL DEFAULT '',
    prompt         text        NOT NULL DEFAULT '',
    poll_after_ms  integer     NOT NULL,
    state          text        NOT NULL,
    polls          integer     NOT NULL DEFAULT 0,
    issued_at      timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,
    last_polled_at timestamptz,
    resolved_at    timestamptz,

    PRIMARY KEY (tenant, token),
    CONSTRAINT mfa_challenges_state_check
        CHECK (state IN ('pending', 'approved', 'denied', 'expired')),
    CONSTRAINT mfa_challenges_poll_after_non_negative CHECK (poll_after_ms >= 0)
);

-- Retiring resolved and expired challenges is an operator's housekeeping over
-- a time window, so the index is on the window rather than on the subject.
CREATE INDEX mfa_challenges_expiry_idx ON mfa_challenges (tenant, expires_at);

-- ---------------------------------------------------------------------------
-- target_host_keys — trust on first use, with a record (proxy D7)
-- ---------------------------------------------------------------------------
--
-- Keyed by (hostname, port, fingerprint), which is EXACTLY the shape the proxy
-- keys its own reuse on. A target presenting a different key is a different
-- row, so a man-in-the-middle, a rotated key and a rebuilt host all arrive
-- here as a first sighting for a target that already has one — which is the
-- changed-key detection, and it is a property of the key rather than of any
-- bookkeeping this server does on top.
--
-- `decision` is stored rather than recomputed because a later, stricter
-- per-target policy must be able to pin an answer without the proxy changing:
-- the response says what was decided, not what the current rule would decide.
--
-- There is deliberately no `cache_key` column. This phase issues no cache hint
-- (M9: never issue a hint the revocation stream cannot withdraw, and the
-- stream is 0009's), and a column for a value nothing writes is a promise the
-- schema makes on behalf of a phase that has not run. 0009 adds it in the same
-- migration that starts issuing hints, which is also where withdrawing one by
-- its own key becomes possible.
CREATE TABLE target_host_keys (
    tenant            text        NOT NULL,
    hostname          text        NOT NULL,
    port              integer     NOT NULL DEFAULT 0,
    fingerprint       text        NOT NULL,
    key_type          text        NOT NULL DEFAULT '',
    decision          text        NOT NULL,
    first_seen_at     timestamptz NOT NULL DEFAULT now(),
    last_seen_at      timestamptz NOT NULL DEFAULT now(),
    first_reported_by text        NOT NULL DEFAULT '',
    last_reported_by  text        NOT NULL DEFAULT '',

    PRIMARY KEY (tenant, hostname, port, fingerprint),
    CONSTRAINT target_host_keys_decision_check
        CHECK (decision IN ('accept', 'reject'))
);

-- "What else has this target ever presented?" — the query behind detecting a
-- changed key, and the one an operator runs when they are told it changed.
CREATE INDEX target_host_keys_target_idx ON target_host_keys (tenant, hostname, port);

-- ---------------------------------------------------------------------------
-- uid_leases — the audit record of a granted block (PLAN §4)
-- ---------------------------------------------------------------------------
--
-- 0001 said this would exist and said what it must not be: "`lease_id` lives
-- on whatever 0007 records for audit, not as a row this layer can hand back".
-- So it is an APPEND-ONLY LOG of grants and nothing reads it in order to
-- allocate. There is no release, no expiry sweep and no free list — each is a
-- plausible-looking optimisation that reintroduces the uid reuse the cursor
-- exists to prevent, and none of them fails visibly.
--
-- `term_seconds` is recorded because an incident asks when a proxy stopped
-- allocating from a block, not because anything here acts on it: an expired
-- block is one that proxy stops using, never one this server hands to somebody
-- else.
--
-- The cursor's own `target_id` is the key this row joins on, so the two agree
-- about what a target is by construction.
CREATE TABLE uid_leases (
    tenant       text        NOT NULL,
    lease_id     text        NOT NULL,
    target_id    text        NOT NULL,
    proxy_id     text        NOT NULL,
    uid_from     bigint      NOT NULL,
    uid_to       bigint      NOT NULL,
    term_seconds integer     NOT NULL DEFAULT 0,
    granted_at   timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant, lease_id),
    -- A block is non-empty by construction. The answer at the top of a range
    -- is 409, never a 200 carrying an empty or inverted block.
    CONSTRAINT uid_leases_block_non_empty CHECK (uid_to > uid_from)
);

-- "Which proxy's block did this uid come from?" — the incident query, answered
-- without polling the fleet.
CREATE INDEX uid_leases_target_idx ON uid_leases (tenant, target_id, uid_from);

-- ---------------------------------------------------------------------------
-- proxy_api_tokens — the proxy→server channel credential (M2)
-- ---------------------------------------------------------------------------
--
-- South-bound is a bearer token in the prototype with mTLS as the intended
-- production form. The token is `<tenant>.<secret>`, the same shape as an
-- enrollment token and for the same reason (M18): the credential CARRIES the
-- tenant, so the tenant is resolved from something this server minted rather
-- than from something the caller asserted. Only the SHA-256 of the secret half
-- is stored.
--
-- `proxy_id` EMPTY is a real state and means the token is not bound to one
-- proxy: a bootstrap or harness credential that authenticates "a proxy of this
-- tenant" and nothing narrower. Enrollment mints a bound one, and a bound
-- token is refused for any other proxy's traffic.
CREATE TABLE proxy_api_tokens (
    tenant     text        NOT NULL,
    token_id   text        NOT NULL,
    proxy_id   text        NOT NULL DEFAULT '',
    token_hash bytea       NOT NULL,
    label      text        NOT NULL DEFAULT '',
    issued_at  timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    revoked_at timestamptz,

    PRIMARY KEY (tenant, token_id)
);

-- AUTH PATH: every south-bound request presents a token, so this is the
-- hottest lookup in the middleware chain. It is unique because two rows with
-- the same secret would make "which proxy is this" ambiguous.
CREATE UNIQUE INDEX proxy_api_tokens_hash_key ON proxy_api_tokens (tenant, token_hash);

-- ---------------------------------------------------------------------------
-- proxies.key_fingerprint — "is this key one of ours" (the chain leg)
-- ---------------------------------------------------------------------------
--
-- A GENERATED column, not a second table, and that is the whole point. A chain
-- leg offers the previous hop's key (proxy D11), so `/v1/auth/cert` has to ask
-- whether a fingerprint belongs to one of the fleet's own proxies — and the
-- fleet registry already answers "which keys are ours". A list of proxy key
-- fingerprints maintained beside `proxies.public_key` would be a second answer
-- to the same question, and it would drift the first time somebody re-enrolled
-- a proxy with a new key.
--
-- Derived here rather than in Go so that the index is over the same expression
-- the lookup uses. The expression is OpenSSH's fingerprint: `SHA256:` followed
-- by the unpadded standard base64 of the SHA-256 of the key blob, which is
-- what `ssh-keygen -lf` prints and what the proxy relays in
-- `public_key.fingerprint`.
--
-- A proxy with no key material yields NULL rather than the fingerprint of the
-- empty string — otherwise every keyless proxy would share one fingerprint and
-- an offered key that hashed to it would authenticate a chain leg for all of
-- them.
ALTER TABLE proxies ADD COLUMN key_fingerprint text
    GENERATED ALWAYS AS (
        CASE
            WHEN public_key IS NULL OR octet_length(public_key) = 0 THEN NULL
            ELSE 'SHA256:' || rtrim(encode(sha256(public_key), 'base64'), '=')
        END
    ) STORED;

-- AUTH PATH: "is this fingerprint one of the fleet's own proxies".
CREATE INDEX proxies_key_fingerprint_idx ON proxies (tenant, key_fingerprint)
    WHERE key_fingerprint IS NOT NULL;
