# 0024 — signed retention markers — Learnings

## Summary
- **Chosen: signed markers**, and signed capture watermarks with them. Rejected: a control-chain cross-check (the chain takes no secret, so the same writer appends a forged `operator.audit.retained`); an in-stream checkpoint (same, and a server record in a proxy's stream); keyed links (a marker beside HMAC'd records is still forgeable, and `ext.KeyStore` has no MAC); an external anchor of heads (cannot tell lawful retention from forged, and is a subsystem). Details has the full comparison.
- **Key:** per tenant, Ed25519, `audit-retention:<YYYYMMDDTHHMMSS.nnnnnnnnnZ>-<8 hex>`, newest name signs, made on first pass. Custody: a registered `ext.KeyStore`, else the software custodian over `credential.key_encryption_key_env` (`daemon.buildKeyStore`). Checked against `Signer(ref).Public()`, never a stored public half.
- **Signed bytes:** `writeParts` (the chain's `<len>:<part>` encoder) over `hoplock.retention/1`, tenant, stream, `through_seq`, `through_hash`. Captures: `hoplock.capture-retention/1`, tenant, instant (UTC, µs).
- **Migration `0013`:** `audit_retention.key_name`, `.signature` (base64, `''` = unsigned); `audit_capture_retention(tenant PK, retained_before, key_name, signature, retained_at, retained_by)`.
- **Break kind** `retention-marker` (`audit.BreakRetentionMarker`). **Codes** `503 retention_key_not_configured` (no key store, nothing deleted) and `409 retention_unverified` (a pass over a start that does not verify).
- **Unsigned pre-existing marker** (`v0.6.0`–`v0.10.0`): a break, and no pass builds on it (`409 retention_unverified`). No path signs one after the fact: nothing is deployed, so the user had the adopt command built here removed (Details).
- **Key files:** `internal/audit/seal.go`, `retention.go`, `verify.go`; `internal/store/retention.go`; `internal/daemon/auditretention.go`, `north.go` (`buildKeyStore`); `internal/httpapi/north/audit.go`.
- **Decisions:** M8 revised in place (register row unchanged, `live`); no decision added. Release `v0.11.0`, `### Breaking`. Owes `hoplock/enterprise` a sync; `hoplock/proxy` none.
- **NEXT must know:** Control's CA ignores a registered `ext.KeyStore` (pre-existing), queued at the user's direction as **0030**, CA custody through the key store; post-quantum and one contract version moved to 0031 and 0032 (PLAN §10, ca-custody revision). `audit-verify` and `audit-retention` now call `HostConfig`.

## Details

### The comparison (the prompt's first question)

Every approach was judged on the prompt's five criteria: closes the gap against
a writer of the database alone; overstates nothing; one code path for the
software custodian and an `ext.KeyStore`; checkable by `audit-verify` from the
server's configuration; cost in proportion.

| Approach | Closes it? | Why / why not |
| --- | --- | --- |
| 1. Signed markers | **yes** | The signature needs a private half the database holds only as AES-256-GCM ciphertext under a KEK it never sees (or not at all, with an HSM). One path: `ext.KeyStore.Signer`. `audit-verify` builds the same key store. Cost: one migration, one signer, one check. |
| 2. Cross-check against the control chain | no — **0021 confirmed** | `Link` hashes public data only. The writer reads the control head, computes `Link(tenant, "control", head+1, headHash, body)` for a forged `operator.audit.retained` naming the stream, sequence and hash, takes `pg_advisory_xact_lock` itself, and appends it at the head. Nothing later is rewritten. |
| 3. Checkpoint record in the stream | no | Same unkeyed chain: a forged checkpoint appended at the head verifies. And it puts server-written records into a proxy's stream (§7: a stream is the submitting proxy). |
| 4. Keyed links (HMAC per record) | **no, on its own** | The prompt calls it stronger, and for rewriting it is — but the marker is still a row. Delete the prefix, write `(N, hash of N)`: the first surviving record's MAC is valid and chains onto it. The marker would need its own MAC, which is option 1 plus a rewrite of every record, the body version and every verifier. `ext.KeyStore` exposes `crypto.Signer`, no MAC (an `ext` break to add one), a verifier holding the HMAC key can forge, and an HSM round trip per record at ~9 400 records/s is out of the question. |
| 5. External anchor of stream heads | **no, as stated** | An anchor saying "head N had hash H" stays consistent after a prefix below N is deleted. Closing the gap needs the *marker* published outside too — option 1's statement, published. It is the only option that would also defend against a key holder, a server-controller and tail truncation, but it is a new extension point, a default for Control alone (M15), a schedule and an outside read in the verifier. Recorded as the next step, not built. |

Verdict: signed markers. The PR asks no question about it.

### What the guarantee is now, exactly

- A writer of the database alone cannot make a deleted prefix, or an emptied
  capture, verify as lawful retention.
- It is **not** a defence against whoever holds the key (KEK + database, or the
  key store) or controls the server. Nor does the chain, sealed or not, see the
  newest records of a stream deleted, or a whole stream deleted: nothing later
  is rewritten. PLAN §7 now says the second sentence too; it was true before
  0024 and unstated.

### The software custodian's ciphertext (asked by the prompt)

**It can be swapped.** `SoftwareKeyStore.seal` uses AES-GCM with no associated
data, so a writer can copy any `software_keys.private_key` (another tenant's,
or the CA's) into the row a marker names, and `Signer` will decrypt it. **It
does not forge anything**, for two reasons, both held by tests:
- every signature a swapped-in key ever made is over bytes naming another tenant
  (`TestATenantsKeyNeitherSignsNorVerifiesAnothers` copies tenant B's key and
  marker into tenant A: "does not check under key …"), or another purpose: an
  SSH certificate's signed data begins with a 4-byte length, a retention
  message with an ASCII digit, and a capture message has its own version;
- the server signs only during a pass an operator ran, over values it read.

Binding the ciphertext to `(tenant, name)` as associated data would be cleaner
and is not done: every CA key already sealed would need a migration path, and
the swap buys nothing above. A follow-up, if anyone wants it.

### Decisions this session made, and why

- **Capture passes are signed too.** `audit_captures.retained_at` was trusted
  exactly as the marker was: one `UPDATE` emptied a capture and the verifier
  counted it "removed by retention". The prompt asked whether to sign; the
  answer is yes, because otherwise §7 would have to say the very sentence this
  phase removes, about the most-used retention rule. One signature per pass:
  a per-tenant watermark, not one per capture (an HSM round trip each). The
  verifier compares the record's **body** timestamp, never `recorded_at`,
  which a writer can move (mutation M8). The instant is truncated to µs before
  signing, because that is what Postgres keeps, and clamped to now, because a
  watermark in the future vouches for captures not yet recorded.
- **A pass refuses to sign over a start that does not verify** (409): a marker
  whose seal fails, a stream not starting where its marker (or sequence 1)
  says, captures retained outside the watermark. A new seal there would launder
  it. This is the O(1) check — one signature, one row. Verifying every record a
  pass deletes was not built: it reads the whole prefix, under the append lock
  or racing it, and `audit-verify` already reports such a break to anyone who
  runs it. Recorded, not proposed.
- **No adopt path, at the user's direction.** The phase first built
  `audit-retention adopt`, which signed a `v0.6.0`–`v0.10.0` marker by its
  exact sequence and hash once the stream verified from it, and a capture
  watermark by a named instant. Told there is no deployment of Control or the
  proxy yet, the user had it removed: with no installed base it was code nobody
  runs. An unsigned marker stays a break, a pass over one is still refused, and
  a development database carrying one is rebuilt. If an installed base ever
  predates a change like this, that phase builds its own path.
- **Rotation is a command** (`audit-retention rotate-key`), not a route: the
  CA's rotation is on both, but a new route is surface Enterprise inherits and
  the prompt did not ask for one. Generation names are fixed-width instants,
  and a new one is dated after the newest even when this clock is behind
  (it caught a same-second rotation sorting below the current key).
- **Destroying a key:** Control never destroys one. If a key store loses one,
  each marker it signed is a break naming the key ("does not hold"). A key that
  will not decrypt (wrong KEK) is an outage — `audit-verify` exits non-zero
  with the error — never a verdict either way.
- **`audit-verify` and `audit-retention` call `HostConfig`** and seal the host's
  registry (`daemon.prepareExtensions`, shared with the daemon). Without it a
  host registering its `ext.KeyStore` from its configuration section would sign
  with one key store and verify with another. `server.Main`'s doc said "none but
  the daemon"; it now names the three.
- **The verifier with no key store** reports a sealed marker as a break it
  cannot check, never a pass. A stream with no marker verifies with no key
  store, as before.

### Found, not fixed

**Control's CA ignores a registered `ext.KeyStore`.** `buildCA` builds the
software custodian from the KEK and nothing else, so a host's HSM is never asked
for CA keys, against `ext.KeyStore`'s own doc ("When no implementation is
registered, Control holds its own software keys"). Retention uses the
registered one (`buildKeyStore`). Wiring the CA to it would strand every CA key
already in `software_keys` for a host that registered one, so it needs its own
phase with a migration story. PLAN §6 now states it. Asked in the PR, the user
had it queued as **0030** (`prompts/queued/0030-ca-custody-through-the-key-store.md`):
the registered key store if any, else the software custodian. Post-quantum
posture moved to 0031 and one contract version to 0032, because post-quantum
plumbs the CA's algorithms and should land on the custody 0030 settles.

### Mutations

Each was applied by a throwaway script, the named packages' tests run, and
reverted; every row is reproducible by hand from its description. All 21
failed (two more held the adopt path, removed since):

| Mutation | Caught by |
| --- | --- |
| signature check removed | `TestReplacingTheStoredPublicHalfForgesNothing`, `TestATenantsKeyNeitherSignsNorVerifiesAnothers`, `TestAForgedCaptureRetentionIsABreak` |
| public half read from the database (`Describe`) | `TestReplacingTheStoredPublicHalfForgesNothing` |
| signed bytes miss tenant | `TestEveryFieldOfAMarkerIsSigned`, `TestATenantsKeyNeitherSignsNorVerifiesAnothers` |
| … miss stream / `through_seq` / `through_hash` / version; capture bytes miss tenant | `TestEveryFieldOfAMarkerIsSigned` (one run each) |
| verifier skips the marker's seal | `TestADeletedPrefixWithAForgedMarkerIsABreak` and six more |
| a destroyed key reads as a pass | `TestAMarkerSignedBeforeARotationStillVerifies`, `TestADeletedPrefixWithAForgedMarkerIsABreak`, `TestATenantsKey…` |
| pass does not check its start | `TestAPassRefusesToSignOverAStartThatDoesNotVerify` (3 subtests), `TestARetentionPassOverAnUnverifiedMarkerIsAConflict` |
| capture pass does not check its start | `TestAPassRefusesToSignOverAStartThatDoesNotVerify/captures…` |
| retained capture judged by `recorded_at` | `TestAForgedCaptureRetentionIsABreak` |
| retained capture needs no watermark | `TestAForgedCaptureRetentionIsABreak`, `TestCapturesRetainedBeforeSealing…` |
| rotation ignores the newest key | `TestAMarkerSignedBeforeARotationStillVerifies` |
| capture pass not clamped to now | `TestAForgedCaptureRetentionIsABreak` |
| emitter's no-key-store refusal removed | `TestRetentionWithoutAKeyStoreIsRefusedAndDeletesNothing`, `…OverTheWire` |
| `buildKeyStore` ignores a host's key store | `TestAuditVerifyChecksSealsWithTheServersOwnKeyStore` |
| `audit-verify` built without the key store | same |
| north maps no key store to a 500 | `TestRetentionWithoutAKeyStoreIsRefusedOverTheWire` |

### Verified by hand

On a fresh database (migrate, seed, `make conform`: 58/58), a lawful pass over
the wire answered `key`, `audit-verify` printed `OK, 23 records, …, retained
through 10`, `audit-retention key` printed the key and its public half. A SQL
`DELETE` of 11–15 plus an `UPDATE` of the marker to 15 and its real hash: `BROKEN
… retention-marker; the retention marker through 15 (hash …) does not verify:
its signature does not check under key …`, exit 1; a further pass `409
retention_unverified`.

### Cross-repo impact

`hoplock/enterprise` (control#69 promised the marker and the routes):
its archive verifier, which "starts from the `audit_retention` marker as
Control's does", must check the seal the way `audit.Verifier` does, or call it;
a host with an `ext.KeyStore` now holds `audit-retention:*` keys and must keep
them (destroying one breaks every marker it signed); retention on a host
deployment with no key store is refused; `HostConfig` is called by two
subcommands; new codes, records and fields. `hoplock/proxy`: none — nothing
here is on the contract.
