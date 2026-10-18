# 0030 — CA custody through the key store: a registered ext.KeyStore holds the CA's keys

## Read first
- `docs/PROTOCOL.md`: the session workflow, §3, §4, §5 and §9.
- `docs/CROSS-REPO-PROTOCOL.md`: §1 (the shared surfaces), §4.1 and §4.3.
  `hoplock/enterprise` implements `ext.KeyStore` for HSM and KMS custody
  (M15), and this phase changes what Control does with one, so it owes a
  downstream sync.
- `docs/PLAN.md`:
  - §2: **M15** (every extension point has a real answer here; `ext/` and
    `server/` are a compatibility promise), **M7** and **M18** point 4 (the
    SSH CA is per tenant), **M11** (a missing or unreachable key is never a
    `401`), **M2** and **M23** (what this release promises);
  - §3: `internal/credential`, `internal/extdefault`, `internal/daemon`,
    `ext/` and `server/`;
  - §6: "Credential brokerage", above all "The CA is built" and "Rotation
    answers the outstanding-certificate question";
  - §7: "Retention holds to the same guarantee because it is signed". The
    retention keys already live in the store this phase gives the CA;
  - §8: migrations, which are forward-only and carry a tenant column.
- `docs/learnings/`: read every summary. Read
  `0024-signed-retention-markers-learnings.md` **whole**: "Found, not fixed"
  states the gap, and its Details explain `daemon.buildKeyStore` and
  `daemon.prepareExtensions` and why the subcommands call `HostConfig`. Read
  `0011` (the CA, the key-encryption key, rotation) and `0004` (the `ext`
  package, and why `KeyStore` is `WhenAbsentCore`). Read `0015` where an item
  below touches `server.Main` or `HostConfig`.
- Code to read:
  - `ext/identity.go`: `KeyStore`, `KeyRef`, `KeyInfo.Custodian`;
  - `internal/daemon/north.go`: `buildCA`, `keyEncryptionKey`,
    `buildKeyStore`;
  - `internal/daemon/identityctl.go`: `openCA`;
  - `internal/credential/ca.go`: `Ensure`, `Rotate`, `generate`, `signer`,
    `custodianOf`;
  - `internal/extdefault/keystore.go`;
  - migration `0007`: `tenant_ca_keys.private_ref` and its comment.

## Why this phase exists
`ext.KeyStore` promises a host that "When no implementation is registered,
Control holds its own software keys". So when one is registered, Control is
meant to use it. The CA never does. `buildCA` builds Control's software
custodian over `credential.key_encryption_key_env` and nothing else. A host
that registers an HSM-backed key store still has its tenants' CA private keys
in Control's `software_keys`, and a host with a key store but no key-encryption
key has no CA at all (`ca_not_configured`).

0024 found this. Retention already resolves its key store correctly
(`buildKeyStore`: a registered `ext.KeyStore`, else the software custodian,
else none). The CA was left as it was, because moving it strands every CA key
a host already holds in `software_keys`.

## Objective
The CA's keys live where the user asked:
- in the registered `ext.KeyStore`, when one is registered;
- otherwise in Control's software custodian;
- with neither, there is no CA, as today.

Retention and the CA use one resolution, so the two never disagree about where
this deployment's keys live. No key that already signs is stranded, and no
trust bundle changes behind an operator's back.

## In scope

### One resolution for every key
- `buildCA` takes its key store from `buildKeyStore`: the registered
  `ext.KeyStore` if any, else the software custodian over the key-encryption
  key. With neither, the CA is nil and the CA routes answer
  `503 ca_not_configured`. Its message names both ways to configure one: the
  key-encryption key or a registered key store.
- New CA keys are generated in that store: `Ensure` on first use, and `Rotate`.
- `hoplock-control ca show | rotate | issue | certificates` build the same key
  store as the daemon. That means the host's sections handed to `HostConfig`,
  the registry sealed, then `buildKeyStore`: `prepareExtensions`, as
  `audit-verify` does since 0024.
  - Revise `server.Main`'s and `server.Options.HostConfig`'s doc comments, which
    name the subcommands that call `HostConfig`.
- A test holds the one resolution. For example, a source or behaviour test
  that fails if the CA and retention can be handed different key stores.

### Keys that predate the registration
A deployment that ran `v0.1.0`–`v0.11.0` with a host's key store registered has
CA keys in `software_keys`: the active key, and any retired key still inside its
overlap. The registered store does not hold them. Decide what happens to them,
under these constraints:
- **Never strand a key that signs.** An active key the resolved store does not
  hold must not turn every brokered-certificate session into an outage.
  Whatever answers instead is never a `401` (M11).
- **Never rotate behind the operator's back.** A rotation changes the trust
  bundle, and targets must trust the new key before it signs. That is why `ca
  rotate` prints the bundle to publish. Moving the active key into the
  registered store is therefore the operator's act, and nothing does it on
  boot.
- **Say it loudly.**
  - `ca show` names each key's custodian truthfully: from `ext.KeyInfo.Custodian`
    as the key was generated, never derived from a name as `custodianOf` does
    today.
  - Start-up logs, per tenant, an active key the resolved store does not hold,
    and names the command that moves it.
- **One code path above the seam.** Whatever reads a key's signer goes through
  `ext.KeyStore`. If a key's custodian must be recorded, record it on its row:
  in a forward migration with the next free number, or in `private_ref`. Do not
  edit `0007`. Its comment and the code disagree about what `private_ref`
  holds, so settle that and say what you found.

**The recommended answer**, to confirm or refute before building:
- Each CA key row records the custodian that holds it.
- A key keeps signing from the custodian that holds it until it is rotated, so
  the software custodian still answers for the keys it holds while a
  key-encryption key is configured.
- `ca rotate` generates the new key in the resolved store, and the routine
  overlap keeps the old key trusted.
- `CHANGELOG.md` gives the migration: `ca rotate`, publish the bundle, wait out
  the overlap.

If something else is better, say why in the learnings and the PR. Ask the user
before building anything that changes this phase's objective.

Also decide, and say:
- what happens when a key store that holds a tenant's active CA key is
  unregistered or unreachable;
- what a compromise rotation does when the retired key's custodian is gone.

## Out of scope
- Binding the software custodian's ciphertext to its row with associated data.
  0024's learnings explain why it buys nothing for retention. Record it again if
  it matters for the CA. Do not build it.
- Any change to `ext.KeyStore`'s interface. A method added to it breaks every
  implementer (M23). The phase must work with the interface as it is.
- Post-quantum algorithms and `ext.KeyAlgorithm`'s vocabulary. They are 0031's,
  which runs after this phase on purpose: its algorithm plumbing should land on
  the custody this phase settles.
- Retention, which 0024 already wired to `buildKeyStore`.

## Acceptance criteria
- **A registered key store and no key-encryption key:** the CA is configured. Its
  keys are generated in the registered store, and `software_keys` gains no
  row. A certificate issues and verifies. `ca show` names the store's
  custodian.
- **A registered key store and a key-encryption key:** new keys go to the
  registered store.
- **A key-encryption key alone:** behaviour is unchanged.
- **Neither:** the CA routes answer `503 ca_not_configured`, and the answer
  names both settings.
- **A tenant whose CA keys predate the registration:**
  - issuance continues and is never a `401`, per the decision above;
  - the state is logged and shown by `ca show`;
  - `ca rotate` moves the active key into the registered store, and the old key
    stays trusted for the overlap;
  - a compromise rotation does what the decision says.
- The `ca` subcommands resolve the same key store as the daemon, host registry
  included. A test runs them through `Main` with a registered store.
- One resolution serves the CA and retention, held by a test.
- Each guarding test has been mutated and failed, and the learnings list each
  mutation and the test it failed. At least these four mutations:
  - the registered store ignored;
  - the software custodian preferred over it;
  - a predating key turned into an outage or a `401`;
  - the subcommands built without the host's registry.
- `go build`, `go vet`, `go test ./...` and `-race` on the changed packages
  pass, and `golangci-lint` at CI's pin reports 0 issues.
- `make contract-check`, `license-check` and `release-check` pass, and
  `make conform` passes on a fresh database. The `brokered` group exercises the
  CA.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0030-ca-custody-through-the-key-store-learnings.md`. Its
summary block MUST give:
- the resolution order;
- what a predating key does, and the operator's migration;
- where a key's custodian is recorded, and the migration if one was added;
- what an unregistered or unreachable key store does to issuance.

Revise PLAN §6 in place. "The CA is built" carries 0024's sentence saying a
registered key store is not yet consulted for the CA. Revise §3's
`internal/credential` and `internal/daemon` entries to state what is now true,
and M15 only if its text claims otherwise. No decision is expected. If the
phase adds one, update the §2 register.

**Release.** The next MINOR after the latest tag (M23). Under `### Breaking`, list
that a host with an `ext.KeyStore` now has its CA keys generated there, and
that the `ca` subcommands call `HostConfig`.

**Cross-repo impact.** For `hoplock/enterprise`, state in the PR's
`## Cross-repo impact` section:
- what its key store must now support for the CA: generate, sign and describe
  Ed25519 keys named `ssh-ca:<generation>`, per tenant, kept for the rotation
  overlap;
- the migration a host with existing tenants owes;
- the `server` doc change.

Queue the "Downstream sync" kickoff from `docs/KICKOFF.md` as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`, committed
once the PR is open, and name the file (§4.3). For `hoplock/proxy`, write
**None**: the CA reaches a proxy through `POST /v1/credentials/certificate`,
whose shape does not change.
