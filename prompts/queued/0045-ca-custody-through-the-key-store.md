# 0045 — CA custody through the key store: a registered ext.KeyStore holds the CA's keys

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
else none). The CA was left as it was, out of that phase's scope.

**There is no deployment of Control or the proxy yet** — no installed base
(PLAN §10, "On 0047 running last"). So no CA key anywhere is held by a custodian
other than the one this phase resolves, and nothing here migrates one: a path
for keys stranded by an upgrade would be code nobody runs, and a second
custodian answering for them is the second code path M15 rules out.

## Objective
The CA's keys live where the user asked:
- in the registered `ext.KeyStore`, when one is registered;
- otherwise in Control's software custodian;
- with neither, there is no CA, as today.

Retention and the CA use one resolution, so the two never disagree about where
this deployment's keys live, and the CA reads every key's signer from that one
store and no other.

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

### A key the resolved store does not hold
With no installed base, the only way to meet one is a store registered,
swapped or unregistered after keys were made: a development or test database,
or an operator changing custody by hand. Answer it plainly rather than build a
migration:
- **Issuance is refused, never a `401` (M11) and never a fallback.** A tenant
  whose active CA key the resolved store does not hold gets a `5xx` from
  `POST /v1/credentials/certificate` and the CA routes, naming the key and the
  store asked. The CA never reaches for another custodian to sign: that would
  be the second code path, and it would sign with a key the operator believes
  is somewhere else.
- **Unreachable is not absent.** A store that cannot be asked (`ext`'s
  `KindUnavailable`, a timeout) is an outage too, distinct in the message from
  a key the store answers it does not hold.
- **Start-up says so** per configured tenant, and `ca show` names each key's
  custodian truthfully — from the key store's `KeyInfo.Custodian`, never
  derived from a name as `custodianOf` does today.
- **The way out is `ca rotate`**, which generates the new key in the resolved
  store; nothing rotates on boot, because targets must trust a new key before
  it signs (§6). A compromise rotation whose retired key the store does not
  hold still removes it from the trust bundle and revokes what it signed:
  neither needs the private half.
- `tenant_ca_keys.private_ref`'s comment in `0007` says it names the custodian;
  the code stores the key's name there. Settle which is true — in code, or in
  a forward migration with the next free number if a column is needed — and
  never edit `0007`. Say what you found.

## Out of scope
- Binding the software custodian's ciphertext to its row with associated data.
  0024's learnings explain why it buys nothing for retention. Record it again if
  it matters for the CA. Do not build it.
- Any change to `ext.KeyStore`'s interface. A method added to it breaks every
  implementer (M23). The phase must work with the interface as it is.
- Post-quantum algorithms and `ext.KeyAlgorithm`'s vocabulary. They are 0046's,
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
- **An active key the resolved store does not hold:** issuance answers a `5xx`
  naming the key, never a `401`, and nothing is signed by another custodian;
  an unreachable store is an outage told apart from an absent key; start-up
  logs it and `ca show` names the custodian; `ca rotate` puts the new key in
  the resolved store, with the old one in the trust bundle for the overlap; a
  compromise rotation removes the old one and revokes what it signed.
- The `ca` subcommands resolve the same key store as the daemon, host registry
  included. A test runs them through `Main` with a registered store.
- One resolution serves the CA and retention, held by a test.
- Each guarding test has been mutated and failed, and the learnings list each
  mutation and the test it failed. At least these four mutations:
  - the registered store ignored;
  - the software custodian preferred over it;
  - a key the resolved store lacks signed by the software custodian instead;
  - that refusal answered `401`;
  - the subcommands built without the host's registry.
- `go build`, `go vet`, `go test ./...` and `-race` on the changed packages
  pass, and `golangci-lint` at CI's pin reports 0 issues.
- `make contract-check`, `license-check` and `release-check` pass, and
  `make conform` passes on a fresh database. The `brokered` group exercises the
  CA.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0045-ca-custody-through-the-key-store-learnings.md`. Its
summary block MUST give:
- the resolution order;
- what a key the resolved store does not hold, and a store that cannot be
  asked, do to issuance, and the code each answers;
- where a key's custodian comes from, and the migration if one was added.

Revise PLAN §6 in place. "The CA is built" carries 0024's sentence saying a
registered key store is not yet consulted for the CA. Revise §3's
`internal/credential` and `internal/daemon` entries to state what is now true,
and M15 only if its text claims otherwise. No decision is expected. If the
phase adds one, update the §2 register.

**Release.** The next MINOR after the latest tag (M23). Under `### Breaking`, list
that a host with an `ext.KeyStore` now has its CA keys generated there — and
that a CA key made earlier in Control's software custodian is refused rather
than used, with `ca rotate` the way out — and that the `ca` subcommands call
`HostConfig`. There is no installed base, so the changelog states the
behaviour and owes no migration guide.

**Cross-repo impact.** For `hoplock/enterprise`, state in the PR's
`## Cross-repo impact` section:
- what its key store must now support for the CA: generate, sign and describe
  Ed25519 keys named `ssh-ca:<generation>`, per tenant, kept for the rotation
  overlap;
- that a CA key made in Control's software custodian before its key store was
  registered is refused, not used — a test fixture or development database,
  since nothing is deployed;
- the `server` doc change.

Queue the "Downstream sync" kickoff from `docs/KICKOFF.md` as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`, committed
once the PR is open, and name the file (§4.3). For `hoplock/proxy`, write
**None**: the CA reaches a proxy through `POST /v1/credentials/certificate`,
whose shape does not change.
