# 0019 — Post-quantum posture: state the wire, plumb the algorithms

## Read first
- `docs/PROTOCOL.md` — session workflow, especially §3 (**never edit
  `contract/`**; `ext/` is a compatibility promise) and §9.
- `docs/PLAN.md` — especially **M2** (two surfaces, two listeners, two
  credential types — and its note that mTLS is the intended production form of
  the south-bound credential), **M1** (the contract is vendored read-only),
  **M7** (identity is federated and short-lived), **M13** (tech choices: closed
  enums, the `exhaustive` linter, and why `x/crypto/ssh` is on both ends),
  **M15** (`ext/` is what Enterprise imports), **M5** (the decision path's
  latency budget), **§4** (the south-bound contract, including
  `algorithm_profile` and its absent-value discipline), **§5.2** (the snapshot's
  output vocabulary, where `AlgorithmProfile` lives), **§6** (the SSH CA and its
  rotation story), **§8** (cross-cutting conventions: config, CI) and **§9**
  (test topology).
- `docs/learnings/` — read summaries; open **`0011`** (the CA, `ext.KeyStore`
  custody, the JOSE subset and *why the algorithm comes from the key*), **`0008`**
  (how `algorithm_profile` reaches the snapshot), **`0010`** (the audit record's
  derived columns, including `algorithm_profile`), and **`0017`** (the topology
  and scenario suite this phase extends).
- `contract/control.yaml` — **`algorithm_profile` only** (its enum, its
  absent-value rule, and the sentence saying anything other than `default` is a
  weakening). You do not need the rest of the document.

> **This phase runs after 0017 on purpose.** It touches both listeners, and the
> listeners stop moving only once 0014 has replaced the two temporary operator
> ports and 0016 is serving the console over the north-bound one. Doing TLS
> before then means doing it twice. It also extends 0017's topology and scenario
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
- **This phase states and asserts; it does not invent contract vocabulary.** The
  one thing Control cannot fix from here is that `algorithm_profile` can only
  *weaken* a route's algorithms and has no way to express a *floor*. That is the
  proxy's vocabulary (M1) and it is raised as an upstream request, not
  approximated. See "Cross-repo dependency".

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
  different direction, that `internal/credential/seam.go` exists to avoid.
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
  shape `TestTheBrokeredCertificateMethodIsNotYetInTheContract` already
  established (0011): assert that `x/crypto/ssh`'s certificate algorithms contain
  no post-quantum signature algorithm, and fail with a message naming what to do
  when one appears. The point is that the next session learns by the build going
  red rather than by reading this prompt.
- Extend 0017's scenario suite and `deploy/` topology so the topology runs with
  TLS configured on both listeners, and the suite asserts the posture rather than
  assuming it.
- `govulncheck` already gates every PR (0017); nothing new is needed there.

## Out of scope
- **Editing `contract/`** (M1) — and in particular inventing an
  `algorithm_profile` value or a route field for an algorithm floor. That is the
  cross-repo dependency below.
- **The proxy→target SSH leg.** Which key exchange a proxy negotiates with a
  target is `hoplock/proxy`'s code. This phase may *record and assert* what the
  proxy reports; it may not configure it.
- **mTLS as a south-bound credential** (M2, M22, 0007) — see the note in item 1.
- **A post-quantum signature anywhere**, for the reason in item 2.
- Post-quantum for federation (OIDC/SAML): the brokers verify what an IdP signed,
  and no IdP issues ML-DSA tokens. When one does, 0011's JOSE verifier takes an
  algorithm member and a branch, and the rule that makes that safe — *the
  algorithm comes from the key, never from the token* — is already in place.

## Cross-repo dependency (upstream, `hoplock/proxy`)
**`algorithm_profile` can only weaken, and there is no way to state a floor.**
The contract's enum is `default | legacy-rsa-sha1 | legacy-device`, every
non-default value is documented as a weakening, and absent means `default`. So a
policy can say *this route may use SHA-1* and cannot say *this route must
negotiate a hybrid post-quantum key exchange*. The asymmetry is the finding.
`Hoplock/proxy#66` (merged) narrowed where the bottom is without changing
that. `default` is now the SSH library's secure set, so no SHA-1 key exchange
and no `ssh-rsa` or `ssh-dss` host key without a legacy profile. It is still
a preset that can only be weakened, and it names no key exchange a route must
negotiate. A target the route's profile cannot reach is reported as
`target.algorithm_policy_unmet` (PLAN §7). Read it as the existing record of
an unmet algorithm policy, not as a floor.

This phase **must not** close it locally: a new enum value or a new route field
is contract vocabulary, it bumps `policy_version`, and the proxy decodes the
authorize response strictly and fails a session closed on anything it does not
recognise — so an invented value reaches a user as an outage. Build the seam,
name the gap, and leave the wire alone. The request is raised by the PR that adds
this prompt (`docs/CROSS-REPO-PROTOCOL.md` §3.2, §4.2); if it has landed
upstream and been synced by the time this phase runs, the vendored contract will
carry it and this section becomes the thing to wire up instead of the thing to
work around — check `contract/control.yaml` before assuming either.

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
- 0017's topology runs with TLS on both listeners and its suite asserts the
  posture.
- **No change to `contract/`**, and `make contract-check` passes.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0019-post-quantum-posture-learnings.md`. The summary block MUST
give: the config keys added, the default posture and the one knob that changes
it, the decision's `M` id, the `ext/` behaviour change and why it is safe, and —
as its own line — **whether the algorithm-floor vocabulary has landed upstream**,
because that decides whether the next phase to touch routing has a floor to
honour or a gap to keep working around.
