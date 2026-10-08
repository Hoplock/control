# 0022 — Post-quantum posture: state the wire, plumb the algorithms

## Read first
- `docs/PROTOCOL.md` — session workflow, especially §3 (**never edit
  `contract/`**; `ext/` is a compatibility promise) and §9.
- `docs/PLAN.md` — especially **M2** (two surfaces, two listeners, two
  credential types — and its note that mTLS is the intended production form of
  the south-bound credential), **M1** (the contract is vendored read-only),
  **M7** (identity is federated and short-lived), **M13** (tech choices: closed
  enums, the `exhaustive` linter, and why `x/crypto/ssh` is on both ends),
  **M15** (`ext/` is what Enterprise imports), **M23** (what a release's
  changelog must say), **M5** (the decision path's
  latency budget), **§4** (the south-bound contract, including
  `algorithm_profile` and its absent-value discipline), **§5.2** (the snapshot's
  output vocabulary, where `AlgorithmProfile` lives and where `algorithm_floor`
  now sits beside it), **§6** (the SSH CA and its rotation story), **§7** (at
  "What the leg negotiated is a record"), **§8** (cross-cutting conventions:
  config, CI) and **§9** (test topology).
- `docs/learnings/` — read summaries; open **`0011`** (the CA, `ext.KeyStore`
  custody, the JOSE subset and *why the algorithm comes from the key*), **`0008`**
  (how `algorithm_profile` reaches the snapshot), **`0010`** (the audit record's
  derived columns, including `algorithm_profile`), **`0021`** (the topology
  and scenario suite this phase extends), and **`0018`** (how `algorithm_floor`
  is authored and served, and where the negotiated-algorithm records and each
  target's key-exchange observation are stored).
- `contract/control.yaml` — **`algorithm_profile` and `algorithm_floor` only**
  (their enums, their absent-value rules, the sentence saying anything other
  than `default` is a weakening, and the floor's levels), plus
  `KexObservation`. You do not need the rest of the document.

> **This phase runs after 0021 on purpose.** It touches both listeners, and the
> listeners stop moving only once 0018 has replaced the two temporary operator
> ports and 0020 is serving the console over the north-bound one. Doing TLS
> before then means doing it twice. It also extends 0021's topology and scenario
> suite rather than standing up its own.

## Objective
Make Hoplock Control's cryptographic posture a **stated, asserted property of
this server** rather than an assumption about whatever terminates TLS in front of
it — and plumb the algorithm vocabulary so that migrating to a post-quantum
signature is an enum member and a branch, not a redesign.

Two framings to hold onto, because they decide almost every judgement call below:

- **Confidentiality and authenticity face different post-quantum threats, and
  only one of them is urgent.** Recorded traffic can be decrypted later by an
  adversary who acquires a quantum computer — *harvest now, decrypt later* — so
  key exchange is the thing with a deadline. A signature, by contrast, is only
  worth forging while the thing it signs is still trusted, and this product
  already issues target certificates that live for **five minutes** (0011). The
  work with real urgency is therefore transport, not signing.
- **This phase states and asserts; it does not invent contract vocabulary.**
  `algorithm_profile` can only *weaken* a route's algorithms. The floor this
  phase needed for the proxy→target leg was the proxy's vocabulary to add (M1),
  so it was raised upstream rather than approximated. Upstream has answered:
  `algorithm_floor` exists (`Hoplock/proxy#69`), and 0018 vendors it and makes
  it authorable. So this phase wires it rather than working around it. See
  "The algorithm floor has landed upstream".

## In scope

### 1. TLS on this server's own listeners (`internal/tlsconf`, M2)
Today all four listeners are plain `http.Server` and TLS is terminated by
something else, so nothing in this repository states, tests or would notice a
change in the wire posture. Fix that:

- A new `internal/tlsconf` package whose only job is to **build a `*tls.Config`**
  from configuration. It is a builder, not shared middleware: **M2 forbids the
  two listeners sharing a chain, not sharing a pure constructor**, and each
  listener must construct its own from its own config section. Say so in the
  package doc, because the next reader will reasonably wonder.
- `tls.Config` defaults this repository commits to, each with its reason in a
  comment: `MinVersion: tls.VersionTLS13` (1.2 buys nothing here — both peers are
  ours or a modern browser), and **`CurvePreferences` left nil so the standard
  library's own order applies**, which puts `X25519MLKEM768` first from Go 1.24
  onwards. Pinning a list here would freeze the posture at whatever was current
  when somebody typed it; the point of leaving it nil is that a Go upgrade
  improves it.
- A **`require_hybrid_kex`** knob per listener, default `false`. When true the
  handshake is refused unless the negotiated group is a hybrid post-quantum one,
  via `tls.Config.VerifyConnection` reading `tls.ConnectionState.CurveID`. Default
  false because requiring it breaks any client that cannot do it, and that is an
  operator's decision rather than ours; available because a deployment that has
  decided is entitled to enforce it rather than hope.
- Config: a `tls:` block inside the **existing** `south:` and `north:` sections
  (`certificate_file`, `key_file`, `client_ca_file`, `require_hybrid_kex`),
  strictly decoded like everything else (§8), documented key by key in
  `config.example.yaml`, and **absent by default** so an existing deployment that
  terminates TLS in front of this server keeps working unchanged.
- `cmd/hoplock-control/serve.go` and `north.go` call `ListenAndServeTLS` when a
  section is configured and `ListenAndServe` when it is not. A section that names
  a certificate and no key — or a file that does not exist — **refuses to start**;
  a listener that silently fell back to plaintext because a path was wrong is the
  failure this whole item exists to prevent.
- The startup log states the posture per listener: TLS on or off, and whether a
  hybrid group is required. An operator should be able to answer "is this
  deployment post-quantum on the wire?" from one log line.

**Client authentication is in scope only as far as the config surface.** Wiring
`client_ca_file` into an mTLS *credential* — a proxy identified by its
certificate rather than by a bearer token — is M2's named intended production
form and is **not** this phase: it changes what a south-bound caller *is*, which
is 0007's and M22's territory. Accept the key here, use it for
`tls.ClientCAs`/`ClientAuth`, and leave the credential model alone.

### 2. The algorithm vocabulary (`ext/identity.go`, `internal/credential`)
`ext.KeyAlgorithm` is a closed enum of three classical algorithms, which is the
right shape — adding one is an enum member plus a branch in the software
custodian, and `exhaustive` names every switch that forgot (M13). Two things to
settle:

- **`KeyAlgorithm.String()` currently falls through to `"ed25519"` for a value it
  does not recognise.** Harmless with three classical members; actively
  misleading the day a fourth exists, because a stale or corrupt row would render
  as a real algorithm. Decide this deliberately: `ext/` is a compatibility promise
  (M15, PROTOCOL §3), so the *signature* does not change, but the fallback
  behaviour should. Prefer rendering something that cannot be mistaken for an
  algorithm (`"unknown"`), and say in the learnings that it is a behaviour change
  to a public package and why it is safe — nothing may legitimately produce an
  out-of-range value, and `internal/extdefault`'s `parseAlgorithm` already maps
  unknown *codes* back to a default independently.
- **Do not add a post-quantum member yet, and say why in the prompt's own
  learnings.** There is no standardised post-quantum signature algorithm for
  OpenSSH certificates: OpenSSH defines none and `x/crypto/ssh` implements none,
  so a `KeyAlgorithmMLDSA65` member would be an enum value the CA could generate
  and never sign a certificate with. Adding it would be this repository
  inventing vocabulary for a format it does not own — the same mistake, in a
  different direction, that 0011's `brokered-certificate` seam was built to
  avoid. That seam refused to put a method on the wire until upstream defined
  one. When upstream did (`Hoplock/proxy#68`), the shape was not the one 0011
  had guessed, which is the argument for not guessing. `Hoplock/proxy#69` then
  did the same to the floor this phase asked for (below).
  **Write the check that will tell the next session when this changes** (below).

### 3. Say what is already adequate, once, where a reader will find it
The audit chain's SHA-256, the CA key's AES-256-GCM at rest, the credential
digests and PBKDF2-SHA256 all need **nothing**: at these sizes the best known
quantum speedup does not threaten them, and NIST treats SHA-256 and AES-256 as
adequate. Right now nothing in the repository says so, which means every future
reader re-derives it or — worse — "fixes" it. One short subsection in
`docs/PLAN.md` §8, not a decision of its own.

### 4. A decision in `docs/PLAN.md` §2
This phase settles something the plan does not currently say: **the wire posture
is this server's to state and assert, not the ingress's to be trusted with.**
That is a decision. Add it with the **next free `M` id** (do not assume a number
— check the register, because another queued phase may have taken one first), add
its row to the §2 register in the same PR, and render it in §8 and §10 as the
register's `Rendered in` column requires (PROTOCOL §3).

### 5. Tests and CI
- `internal/tlsconf`: unit tests over the builder — a config naming a certificate
  and no key is refused; `MinVersion` is 1.3; `CurvePreferences` is nil.
- An **end-to-end handshake test** per listener, using `httptest.NewUnstartedServer`
  with the built config: a Go client negotiates `tls.X25519MLKEM768` and
  `ConnectionState.CurveID` says so. This is the assertion that makes the claim
  real, and `tls.ConnectionState.CurveID` is what makes it possible.
- With `require_hybrid_kex: true`, a client restricted to
  `CurvePreferences: []tls.CurveID{tls.X25519}` is **refused**; with it false the
  same client succeeds. Both directions, because a knob that cannot be observed to
  do anything is a knob nobody should trust.
- **A tripwire for the SSH certificate gap**, in `internal/credential`, in the
  shape 0011's brokered-certificate tripwire established. That is a test that
  fails the build on the day an upstream fact changes, and names what to do.
  0018 changes that test to pin the method's shape once `Hoplock/proxy#68` is
  vendored, so copy the pattern and not the name. Assert that `x/crypto/ssh`'s
  certificate algorithms contain no post-quantum signature algorithm, and fail
  with a message naming what to do when one appears. The point is that the next
  session learns by the build going red rather than by reading this prompt.
- Extend 0021's scenario suite and `deploy/` topology so the topology runs with
  TLS configured on both listeners, and the suite asserts the posture rather than
  assuming it. The floor scenarios of item 6 go in the same suite.
- `govulncheck` already gates every PR (0021); nothing new is needed there.

### 6. Assert the proxy→target floor end to end (`Hoplock/proxy#69`)
0018 makes `algorithm_floor` authorable and serves it. This phase proves its
post-quantum level against a real proxy and a real target, on 0021's topology,
because only there do "decides" and "enforces" meet:

- A route whose floor is `pq-hybrid-kex` succeeds against a target whose
  OpenSSH is 9.9 or later. Its `target.algorithms_negotiated` record stores
  `target_kex_algorithm: mlkem768x25519-sha256` beside `algorithm_floor:
  pq-hybrid-kex`. Assert on the stored record, not on the authorize answer,
  because the record is the only per-session proof that the floor held (PLAN
  §7).
- The same floor fails as an **outage**, never a deny, against a target that
  offers no ML-KEM-768 hybrid. The user is given the session id, and the store
  holds a `target.algorithm_policy_unmet` whose `algorithm_axis` is
  `key_exchange` and whose `algorithm_policy_cause` is `floor`. The proxy's own
  topology builds that target cheaply: a second `sshd` on the same host whose
  `KexAlgorithms` name no hybrid (upstream `deploy/target/entrypoint.sh`). Also
  prove the sntrup761 case: a listener whose only hybrid is
  `sntrup761x25519-sha512@openssh.com`, which is how OpenSSH 9.0 to 9.8 behave.
  It fails the floor too, and that is the assertion that nothing here treats
  sntrup761 as meeting the level.
- The target's key-exchange observation reaches the store as a report of its
  own, and the target's rung observation is unchanged by it (0018, item 5 of
  "Algorithm floor and bans reach a proxy").
- Say where the proxy→target posture is read. For this server's own listeners
  the startup log states it (item 1). For the proxy→target leg it is per route
  and per session: the floor in force, as policy states it, and the negotiated
  exchange, as each session's record proves it. Name where an operator reads
  each (0018's query, 0020's views). Add no configuration knob for it here: a
  floor is route policy, not a property of this server's listeners.

## Out of scope
- **Editing `contract/`** (M1). The floor this phase needed exists now
  (`Hoplock/proxy#69`, vendored by 0018), so there is nothing left to invent.
- **Authoring the floor, and anything about bans.** 0018 makes
  `algorithm_floor` and `algorithm_bans` authorable, validates them and serves
  them. This phase asserts the floor end to end (item 6).
- **Configuring the proxy→target SSH leg.** Which key exchange a proxy offers
  and negotiates is `hoplock/proxy`'s code, and the route's floor is the policy
  this server states. This phase *records and asserts* what the proxy reports,
  and configures nothing on the proxy.
- **mTLS as a south-bound credential** (M2, M22, 0007) — see the note in item 1.
- **A post-quantum signature anywhere**, for the reason in item 2.
- Post-quantum for federation (OIDC/SAML): the brokers verify what an IdP signed,
  and no IdP issues ML-DSA tokens. When one does, 0011's JOSE verifier takes an
  algorithm member and a branch, and the rule that makes that safe — *the
  algorithm comes from the key, never from the token* — is already in place.

## The algorithm floor has landed upstream: wire it, do not work around it
**What was missing.** `algorithm_profile` can only weaken. Its enum is
`default | legacy-rsa-sha1 | legacy-device`, every non-default value is a
weakening, and absent means `default`. So a policy could say *this route may
use SHA-1* and could not say *this route must negotiate a hybrid post-quantum
key exchange*. `Hoplock/proxy#66` narrowed where the bottom is, making
`default` the SSH library's secure set, without adding a floor. The PR that
queued this phase raised the floor upstream (`Hoplock/control#36`,
`docs/CROSS-REPO-PROTOCOL.md` §3.2).

**What landed.** `Hoplock/proxy#69` (merged) added `algorithm_floor`, a
sibling of the profile on the authorize response, and moved `policy_version`
to `6`. 0018 re-vendors it and makes it authorable, so check that
`contract/control.yaml` carries it before you start. Three things differ from
what `#36` asked for, and `#36`'s PR body is wrong about all three. Build
against this list, not against that body:

- **`pq-hybrid-kex` is `mlkem768x25519-sha256`, and not
  `sntrup761x25519-sha512`.** `#36` promised either one. The proxy's SSH
  library does not implement sntrup761, so a target whose only hybrid is
  sntrup761 does not meet the level. That is OpenSSH 9.0 to 9.8's default, a
  large installed base. In practice the level needs OpenSSH 9.9 or later on
  the target, or device firmware offering ML-KEM-768 hybrid. No text, log line
  or test here may promise "either". If a later proxy build gains sntrup761,
  that build declares it for the level. This server reads a level's members
  from what each build declares (PLAN M17), never from a list of its own.
- **The floor is an ordered ladder, `modern-kex` < `pq-hybrid-kex`, compared
  by rank**, not the single value `#36` sketched. Nesting is the rule for
  adding a level: every exchange a level accepts is accepted by every level
  below it. A regime that does not nest (FIPS is the example) is not a level.
- **The negotiated exchange is recorded as `target_kex_algorithm`**, on
  `target.algorithms_negotiated`, not as the `kex_algorithm` `#36` asked for
  (PLAN §7).

The proxy refuses `legacy-device` with any floor and accepts `legacy-rsa-sha1`
with one. A floor the target cannot meet fails the session as an outage (PLAN
§5.2). 0018 owns all of that on this side. This phase's part is item 6.

## Acceptance criteria
- Both listeners serve TLS when configured and plaintext when not, and a
  **misconfigured** TLS section refuses to start rather than falling back.
- A handshake against each listener negotiates `X25519MLKEM768`, asserted from
  `tls.ConnectionState.CurveID` rather than from the absence of an error.
- `require_hybrid_kex: true` refuses a classical-only client; `false` accepts it.
  Both asserted.
- An existing configuration with no `tls:` block behaves **exactly** as before —
  no route gains a requirement, no key becomes mandatory.
- The startup log answers "is this deployment post-quantum on the wire?" per
  listener.
- `KeyAlgorithm.String()` cannot render an unrecognised value as a real
  algorithm, and the `ext/` behaviour change is stated in the learnings.
- The SSH-certificate tripwire fails the build if `x/crypto/ssh` gains a
  post-quantum certificate algorithm.
- `docs/PLAN.md` carries the new decision, its register row, and the §8
  subsection on what is already adequate.
- 0021's topology runs with TLS on both listeners and its suite asserts the
  posture.
- A `pq-hybrid-kex` route to a target on OpenSSH 9.9 or later stores
  `target_kex_algorithm: mlkem768x25519-sha256` beside the floor in force. The
  same floor fails as an outage, and stores a `target.algorithm_policy_unmet`
  with cause `floor`, against a target with no hybrid and against one whose
  only hybrid is sntrup761. All three are asserted on stored records in 0021's
  topology.
- **No change to `contract/`**, and `make contract-check` passes.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0022-post-quantum-posture-learnings.md`. The summary block MUST
give: the config keys added, the default posture and the one knob that changes
it, the decision's `M` id, the `ext/` behaviour change and why it is safe, and —
as its own line — **what the floor scenarios proved against the real proxy**:
the target images, their OpenSSH versions, and the exchange each one recorded.
The floor has landed upstream (`Hoplock/proxy#69`, vendored by 0018). So the
next phase to touch routing has a floor to honour, and what it needs from here
is evidence of what holds on the wire.

If this phase changes what `ext.KeyAlgorithm.String()` returns for an
unrecognised value, this PR's `CHANGELOG.md` section owes an entry that names
`ext.KeyAlgorithm.String` and says what it now returns. No API diff sees a
behaviour change, so `make release-check` cannot catch it, and the changelog is
where Enterprise reads what changed before it bumps its pin (M23).
