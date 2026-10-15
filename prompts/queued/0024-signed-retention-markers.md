# 0024 — Signed retention markers

## Read first
- `docs/PROTOCOL.md`: the session workflow, §3, §4, §5 and §9.
- `docs/CROSS-REPO-PROTOCOL.md`: §1 (the shared surfaces), §4.1 and §4.3. The
  retention marker is one of the things control#69 promised
  `hoplock/enterprise`, so changing it owes a downstream sync.
- `docs/PLAN.md`:
  - §2: **M8** (audit is append-only and tamper-evident; retention), **M7**
    and **M18** point 4 (keys are per tenant), **M15** (`ext.KeyStore` is the
    seam a host's HSM or KMS fills), **M11** (a missing key is never a `401`)
    and **M23** (what this release promises);
  - §3: `internal/audit`, `internal/credential`, `internal/extdefault` and
    `ext/`;
  - §6: the per-tenant SSH CA and its key custody, which this phase reuses;
  - §7: "What the chain does and does not defend against" and "Retention has to
    remove a chain's TAIL";
  - §8: migrations, which are forward-only and carry a tenant column.
- `docs/learnings/`: read every summary. Read
  `0021-audit-0019-audit-store-and-fleet-configuration-learnings.md` **whole**:
  its Details, "The two open proposals", state the gap and the first proposal.
  Read `0010` (the chain and the verifier), `0011` (the CA, the key-encryption
  key and `ext.KeyStore`) and `0004` (the `ext` package) where an item below
  touches them.
- `ext/identity.go`: `KeyStore`, `KeyRef` (whose doc comment already names an
  `audit-chain` role), `KeyAlgorithm`, `KeyInfo`.

## Why this phase exists
Retention (0019, `POST …/audit/retention`) deletes the oldest records of one
stream and writes `audit_retention(through_seq, through_hash)`. The verifier
then starts from that row instead of from sequence 1. The row is trusted as it
stands.

Before retention existed, deleting a stream's oldest records broke verification
at sequence 1. Hiding the deletion meant renumbering and rehashing every record
left. Now a writer of the database alone needs two statements: delete the
prefix, and insert one marker naming the last deleted sequence and its hash,
both readable beforehand. Verification passes. PLAN §7 says the chain detects
"anybody who can reach the database but cannot rewrite every later record".
This attack rewrites no later record, so retention made the chain's stated
guarantee false for prefixes.

0021 found this and proposed one fix. The user asked that this session check
whether that fix is the right one before building it.

## Objective
Restore §7's guarantee for retained prefixes: a writer of the database alone
cannot make a deleted prefix verify as lawful retention. Claim no more than
that.

## First: decide whether signed markers are the right fix
Before writing code, compare these approaches against the criteria below, and
record the comparison in the learnings and the PR:

1. **Signed markers** (0021's proposal). Retention signs the marker with a
   per-tenant key that the database does not hold in usable form. The verifier
   refuses a marker whose signature does not check.
2. **A cross-check against the control chain.** Require a matching
   `operator.audit.retained` record for every marker. 0021 judged this
   insufficient: the chain's hash takes no secret, so the same writer appends a
   correctly chained forged control record at the head, and still rewrites no
   later record. Confirm or refute that.
3. **A checkpoint record in the retained stream itself.** The marker becomes a
   record chained into the stream. It has the same weakness as 2, and it puts
   server-written records into a proxy's stream (§7: "a stream is the
   submitting proxy"), which muddles what a stream is.
4. **Keyed links.** Every record's hash becomes an HMAC under a per-tenant key,
   so no record can be forged without the key. It is stronger, and it changes
   the chain for every record, the body version and every verifier.
5. **An external anchor.** Stream heads are published outside this system on a
   schedule. PLAN §7 names this as future work; it is the only one that also
   defends against a writer who holds the key or controls the server.

The criteria:
- It closes the gap against a writer of the database alone.
- It overstates nothing (§7: "an audit store that overstates its guarantee is
  worse than one that states a smaller one").
- It works with the software custodian and with an `ext.KeyStore` extension
  alike, with no second code path (M15).
- `hoplock-control audit-verify` can check it from the server's own
  configuration.
- Its cost in code, migrations and operator burden is in proportion to the gap.

**If signed markers come out best, build them** (below). If another approach is
better, do not build it. Put the comparison in the PR and ask the user, as
`docs/PROTOCOL.md` §9 says to when a prompt seems wrong. Building something the
prompt did not describe is how a session quietly decides the user's question for
them.

## In scope (if signed markers are confirmed)

### The signature
- One key per tenant, `ext.KeyRef{Tenant, Name: "audit-retention"}` (or a name
  you justify), Ed25519, created through the configured `ext.KeyStore` on first
  use. M18 point 4 is the reason it is per tenant: a departing tenant's chain
  must verify on its own.
- The signed bytes are a versioned, length-prefixed encoding of the tenant, the
  stream, `through_seq` and `through_hash`, in the style of the chain's `Link`
  (`internal/audit/chain.go`). The version prefix is inside the signed bytes,
  for the reason `bodyVersion` is.
- **Verify against the private half, never a stored public half.** Check with
  `Signer(ref).Public()`, or an equivalent that cannot be satisfied by
  rewriting a database row. A public key the database holds can be replaced
  together with the marker. Read `internal/extdefault/keystore.go` and confirm
  the software custodian's ciphertext cannot be swapped for another key row
  (another tenant's included) to the same effect. Say what you found.
- **A forward migration** (the next free number) adds the signature and the key
  reference to `audit_retention`. Never edit `0012`.
- `operator.audit.retained` also carries the signature and the key reference, so
  the control chain records them.

### The verifier
- A marker whose signature is missing, does not check, or names a key the key
  store does not hold is a **break**, and `audit-verify` exits 1 naming it. Use a
  break kind of its own, so an operator can tell it from a removed record.
- `audit-verify` builds the key store from the configuration, exactly as the
  server does.
- **An unsigned marker written before this phase** (`v0.6.0`–`v0.8.0`) is
  indistinguishable from a forged one, and is never silently trusted. Decide
  what an operator with such a marker does, and say it in the changelog. Any
  path that signs an existing marker must not be able to sign a forged one
  without the operator verifying the stream first.

### Retention without a key
- With no key store (no key-encryption key and no `ext.KeyStore` extension),
  both retention routes are refused before anything is deleted. The code names
  the setting to configure, as `ca_not_configured` does, and is never a `401`
  (M11). Add the code to `north.AllCodes`.
- Capture retention leaves the record and its digest, so it needs no marker.
  Decide whether its pass must be signed too, and say why.

### Rotation and destruction
- The key reference on each marker keeps older markers verifiable after a
  rotation. Say what destroying a key does to the markers it signed: they
  become unverifiable, and that must be loud rather than silent.

## Out of scope
- External anchoring of stream heads (PLAN §7's future work), and keyed links
  for every record. Either may be the right next step; record it, do not build
  it.
- Any change to the chain's link hash or the record body version.
- `hoplock/proxy`: nothing here is on the contract.

## Acceptance criteria
- The comparison above is in the learnings and the PR, with a verdict and its
  reasons.
- If signed markers were built:
  - **The attack fails.** Delete a prefix with SQL and insert a marker with the
    correct `through_seq` and `through_hash` but no valid signature: the
    verifier breaks, naming the stream. Do the same after replacing whatever
    public half the database holds: still a break.
  - A lawful pass verifies, and `audit-verify` prints the retained prefix as it
    does today.
  - A marker signed before a key rotation still verifies after it.
  - With no key store, retention is refused, nothing is deleted, and the marker
    does not move.
  - Tenant A's key neither signs nor verifies tenant B's markers.
  - Each guarding test has been mutated (the signature check removed, the
    public half read from the database, the signed bytes missing a field) and
    failed. The learnings list each mutation and the test it failed.
- `go build`, `go vet`, `go test ./...` and `-race` on the changed packages
  pass, and `golangci-lint` at CI's pin reports 0 issues.
- `make contract-check`, `license-check` and `release-check` pass, and `make
  conform` passes on a fresh database.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0024-signed-retention-markers-learnings.md`. Its summary block
MUST give:
- the approach chosen, and the one-line reason each other was not;
- the key's name, algorithm and custody;
- the signed bytes;
- the migration and its columns;
- the break kind and the refusal code;
- what an unsigned pre-existing marker does.

Revise PLAN M8 and §7 in place to state the guarantee as it now stands. If the
change is a new decision rather than a revision of M8, add it and update the
§2 register.

**Release.** The next MINOR after the latest tag (M23). Refusing retention
without a key is behaviour a deployment can observe; list it under
`### Breaking`.

**Cross-repo impact.** `audit_retention` and the retention routes were promised
to `hoplock/enterprise` in control#69. A host with an `ext.KeyStore` extension
now signs markers with it. State the obligations in the PR's
`## Cross-repo impact` section (`docs/CROSS-REPO-PROTOCOL.md` §4.1). Queue the
"Downstream sync" kickoff from `docs/KICKOFF.md` as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`, committed
once the PR is open, and name the file (§4.3). For `hoplock/proxy`, write
**None**.
