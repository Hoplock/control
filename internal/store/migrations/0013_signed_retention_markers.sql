-- 0013 — signed retention markers (prompt 0024; PLAN M8, §7, M18, M15).
--
-- Retention (0012) left the verifier trusting one row per stream: a writer of
-- the database alone could delete a stream's oldest records and insert the
-- marker a lawful pass would have written, and the chain verified. It did the
-- same for a capture whose bytes were emptied and marked retained. This
-- migration gives both the place for a signature made by a per-tenant key the
-- database does not hold in usable form (`ext.KeyStore`), which the verifier
-- checks against that key's private half and never against anything stored
-- here.
--
-- Nothing here rewrites a row. A marker written before this migration keeps
-- its values and gains an empty signature, which the verifier reports as a
-- break: an unsigned marker is indistinguishable from a forged one, and is
-- never silently trusted (CHANGELOG v0.11.0 says what an operator does).

-- ---------------------------------------------------------------------------
-- audit_retention — the signature over each marker
-- ---------------------------------------------------------------------------
--
-- `key_name` is the key store's name for the key that signed, so a marker
-- signed before a rotation still names the key it was signed with. The tenant
-- is the row's own: a marker is never checked against another tenant's key
-- (M18). `signature` is base64 of an Ed25519 signature over the versioned,
-- length-prefixed tenant, stream, through_seq and through_hash
-- (`audit.RetentionMessage`). Both are empty on a marker no pass signed.
ALTER TABLE audit_retention
    ADD COLUMN key_name  text NOT NULL DEFAULT '',
    ADD COLUMN signature text NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- audit_capture_retention — how far capture retention has lawfully reached
-- ---------------------------------------------------------------------------
--
-- A capture pass empties the bytes of every capture whose record was recorded
-- before an instant, and keeps the record and its digest. The verifier counts
-- such a capture as retained only when its record's own timestamp — inside the
-- hashed body, never the derived column — is before the instant this row
-- signs. One row per tenant, because a capture pass spans the tenant's
-- streams, moved forward by each pass and never back.
CREATE TABLE audit_capture_retention (
    tenant          text        NOT NULL,
    retained_before timestamptz NOT NULL,
    key_name        text        NOT NULL,
    signature       text        NOT NULL,
    retained_at     timestamptz NOT NULL DEFAULT now(),
    retained_by     text        NOT NULL DEFAULT '',

    PRIMARY KEY (tenant)
);
