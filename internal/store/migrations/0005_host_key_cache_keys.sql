-- 0005 — the key a host-key decision is withdrawn by (prompt 0009).
--
-- Scope: the `target_host_keys` table only. 0003 left the column out
-- deliberately and said why: this server issued no cache hint, because M9
-- forbids issuing one the revocation stream cannot withdraw and the stream was
-- 0009's. 0009 serves the stream, so the hint is turned on — and this is the
-- column that makes turning it on safe rather than merely possible.
--
-- WHY A COLUMN AND NOT A DERIVATION. The key is derived (tenant, target, port,
-- fingerprint), so it could be recomputed on demand — but what an operator
-- withdrawing a decision needs is the key THIS SERVER ISSUED, and a derivation
-- recomputed by a later revision of the scope would produce a key no proxy
-- holds. The withdrawal would then succeed, report success, and drop nothing.
--
-- A SUBJECT-SCOPED `cache_invalidate` CANNOT REACH ONE OF THESE (PLAN §5.4).
-- The proxy keys host-key reuse on target, port and key fingerprint — not on a
-- person — so withdrawing a host-key decision means publishing this key, or
-- `resync`. That is the whole reason the column exists, and it is why the
-- operator surface reports what an invalidation covered rather than leaving it
-- to be assumed.
--
-- Additive and forward-only: the column arrives with a default, so every row
-- 0003 wrote stays readable. An empty value means no key was recorded for that
-- sighting — a row written before this migration — and the operator surface
-- says so rather than publishing an empty key.

ALTER TABLE target_host_keys
    ADD COLUMN cache_key text NOT NULL DEFAULT '';
