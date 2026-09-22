# 0011 — Identity, federation, RBAC and credentials — Learnings

## Summary
- **What shipped:** groups + the fixed role set + RBAC's one enforcement point;
  OIDC and SAML behind `identity.Broker`; the versioned claim mapping; a real
  push MFA provider; the per-tenant SSH CA; and the **north-bound listener with
  M18's credential model** — a caller never asserts its own tenant. Migration
  `0007`.
- **Key packages:** `internal/identity` (brokers, mapping, RBAC, principals,
  federation), `internal/credential` (the CA + the contract seam),
  `internal/httpapi/north` (the route table that enforces tenancy),
  `internal/extdefault/keystore.go`, `internal/audit/authevent.go`,
  `cmd/hoplock-control/{north,identityctl}.go`.
- **Broker seam:** `Broker{Name,Kind,Begin→Begun,Complete→Assertion,Metadata}`.
  `Begun` hands the per-flow secrets back to the CALLER to store: a flow is a
  row, single use, like an MFA challenge.
- **Mapping:** YAML `schema_version: 1`, `claims[{claim,attribute,values}]`,
  `groups{claim,map[{external,group}]}`. Strict, **exact match only**, version
  allocated by the store. Unmapped ⇒ dropped; `chain_hop_proxy_id` / `hoplock.*`
  cannot be mapped onto. No mapping ⇒ maps nothing; an unparseable stored one is
  an outage.
- **Break-glass is a field, not a convention:** `break_glass` on the principal,
  `north_principals`, `subjects`, the decision record and the audit record
  (`audit.AttrBreakGlass`, `critical`, stream `control`) — written *before* the
  credential exists, so a sink failure refuses the login.
- **CA:** `credential.CA.{Ensure,Describe,Issue,Verify,Rotate,Revoke,Outstanding}`
  over `ext.KeyStore`. `Issue` takes the **proxy's** public key; no private key
  crosses. Routine rotation keeps the retired key trusted for `rotation_overlap`
  (> max validity) and revokes nothing; `Compromise: true` drops it from the
  bundle at once and revokes every outstanding certificate.
- **CROSS-REPO (upstream, blocking):** `hoplock/proxy` must add
  `TargetAuth.method = "brokered-certificate"` (params `username` required,
  `certificate`, `certificate_serial`, `ca_public_keys`) **and** a south-bound
  signing endpoint over a proxy-generated public key. Seam + tripwire:
  `internal/credential/seam.go`.
- **Decisions:** none added/withdrawn; register unchanged. **M2 revised in
  place** — the north-bound listener is bound by 0011, not 0014; §3 and §6 too.
- **NEXT SESSION:** north-bound routes go in `north.routes()` with an access
  class and a permission or the route tests fail; `north.Options.CA` may be nil
  (no `credential.key_encryption_key_env` ⇒ no CA, `503 ca_not_configured`).

## Details

### The security property, and where it is proved

The phase's acceptance criterion — *a claim the mapping does not cover does not
become a policy attribute* — is a statement about **one function called from one
place**: `Mapping.Apply`, from `Federation.Complete`. That is why the two brokers
return the same `Assertion` type and provision nothing themselves. If OIDC and
SAML each wrote their own subject rows, the property would be a claim about two
code paths that happen to agree today.

It is asserted three times, deliberately:

- `TestAnUnmappedClaimNeverBecomesAnAttribute` (pure, `mapping_test.go`) — the
  mapping drops it.
- `TestAnUnmappedClaimCannotInfluenceADecision` (`federation_test.go`, Postgres)
  — end to end: the test IdP asserts `role: admin`, and the **subject row the
  decision path reads** (0008: groups and claims come from this server's store,
  never from a request) carries exactly two attributes and neither is it.
- `TestBothProtocolsProduceTheSameShapeOfAssertion` — the same mapping applied to
  an OIDC and a SAML assertion produces the same groups and drops the same claim.

The reserved-name rule is the other half and is easy to miss: `ClaimChainHop`
(`chain_hop_proxy_id`) is a claim **this server sets** to say which proxy's key
authenticated a chain leg (0007). A mapping able to produce it would let an IdP
administrator assert the identity of the infrastructure in front of a user. The
`hoplock.` prefix is closed for the next such claim.

### Why the mapping has no transformation language

Prefix rules, regular expressions and templates were all considered and left out.
A mapping language is a policy language, and M3 already decided that this product
has exactly one, with a closed vocabulary and compile-time validation. A second,
weaker one sitting *upstream* of the decision program would be the place
unreachable and contradictory rules came back. Complex claim transformation is
named out of scope by the prompt and belongs behind `ext.IdentitySync`.

**This leaves a real gap for Enterprise, and it is worth stating rather than
discovering.** `ext.SyncSink.UpsertSubject` carries `Groups []string` of *external*
group identifiers, and the claim mapping is a **login-time** artifact: it never
sees a provisioned subject. So a SCIM implementation today must write local group
names, or write `ext.SyncedGroup` records and membership and let Control's own
group table be the authority. That is workable and it is not obviously the right
answer; the question — *does provisioning need its own mapping, or does it write
through the same one?* — is 0014's to settle when it exposes the mapping API, and
it should not be settled by whichever Enterprise phase hits it first.

### JOSE is hand-written, and SAML's signature is not

`internal/identity/jose.go` is ~300 lines of "verify a compact JWS against a
published JWKS", and nothing else. No JWE, no `none`, no symmetric algorithms at
all — an `oct` entry in a JWKS is either a misconfiguration or an attack. The
rule that matters is stated as code rather than configured: **the algorithm comes
from the resolved key, never from the token's header**, so the classic
`alg: HS256`-signed-with-the-RSA-public-key forgery reaches no branch.
`TestTheAlgorithmComesFromTheKeyAndNeverFromTheToken` drives six header values
through it. RSA below 2048 bits and an EC point not on its curve are refused at
key-parse time.

**XML signatures are not hand-written**, and that asymmetry is deliberate.
Exclusive canonicalisation is where every home-grown SAML implementation gets it
wrong: the bug is silent, it only shows up against one IdP's namespace handling,
and its consequence is accepting a forged assertion. `goxmldsig` does the
canonicalisation and reference processing (two new dependencies,
`github.com/russellhaering/goxmldsig` and `github.com/beevik/etree`; the test IdP
signs with the same library, so a test cannot pass by sharing a bug with the
verifier). What *is* this repository's job is the second-most-common SAML
mistake, and it is `SAMLBroker.validate`: **a valid signature somewhere in the
document is not a signed assertion.** Either the `Response` is signed — and the
assertion is then taken out of the *validated* copy — or the single `Assertion` is
signed in its own right. A document with two assertions is refused, because
"which one did the signature cover" then has a wrong answer available.

`x/crypto` also arrives as a dependency, for the SSH CA. M13 anticipated it
("`x/crypto/ssh` is already on the far end"), and matching the proxy byte for byte
is the reason.

### The SAML profile, stated rather than configured

No IdP-initiated login (`InResponseTo` is checked on the `Response` *and* on a
`SubjectConfirmationData`, and an unsolicited assertion carries neither); no
unsigned assertion under any configuration — the broker refuses to *build* without
a signing certificate; no artifact binding; **no encrypted assertions**. The last
one is a missing feature rather than a deliberate narrowing, and it is the first
thing a customer with an ADFS deployment will ask for.

### North-bound tenancy: the route table is the enforcement point

M18's failure mode is not a wrong decision in the middleware. It is **one handler
that reads the tenant out of the path itself.** So:

- a route is registered with an `Access` class (`AccessAnonymous`,
  `AccessAuthenticated`, `AccessTenant`) and, for `AccessTenant`, exactly one
  `identity.Permission`. `Router.Register` **refuses** a tenant route with no
  permission, an anonymous route carrying one, an unknown permission, and a
  duplicate;
- `north.enforce` is the only implementation of any of it: authenticate → resolve
  one tenant → check one permission → run the handler;
- a handler reads the tenant from `north.TenantFrom(ctx)` or not at all;
- `Principal.scopes` is **unexported**. The only questions available are
  `MayActIn` and `RolesIn`, which is what stops a cross-tenant aggregate route
  from being easy to write (M18 forbids one; Enterprise's E11 owns aggregation).

`TestEveryRegisteredRouteRefusesATenantOutsideTheCallersScope` enumerates
`Server.Routes()` and drives every tenant route with a **tenant-A administrator**
token naming tenant B. It is total over routes that do not exist yet, which is the
only version of this test worth having: 0014 adds a dozen routes and inherits it.
`TestEveryRegisteredRouteRefusesAnUnauthenticatedCaller` is the same shape for the
credential.

Three ways to select a tenant — `{tenant}` in the path, `X-Hoplock-Tenant`, a
`tenant` query parameter — and one place that checks all three. A principal scoped
to exactly one tenant needs none of them, which is M18's "single-tenant
deployments look untouched"; a principal scoped to several and naming none gets
`tenant_ambiguous`, never a default.

**`tenant_out_of_scope` and `forbidden` are different codes** because they are
different operator problems: a missing role, versus a credential being used
against the wrong estate.

### The north-bound listener came up here, which the plan did not say

PLAN M2 said 0014 owned the north-bound listener's bring-up, on the reasoning
that 0014 owned its credential model. **M2 is revised in place** (PROTOCOL §3: no
dated layers) because 0011 *is* that credential model, and a listener that
authenticates nobody cannot serve a login. 0014 now adds routes to a listener that
already authenticates, which is strictly less work than standing one up.

Two consequences worth knowing:

- the path prefix is **`/api/v1`**, not `/v1`: `/v1` is the contract's namespace
  on the other listener, and an operator reading a log line should be able to tell
  which listener answered from the path alone. `TestNoContractRouteIsReachableOnThisListener`
  asserts both directions.
- the router owns its **404 and 405**. `http.ServeMux`'s own answers are
  plain-text sentences, and M21 requires every error to carry a code, typed
  parameters, an English message and the correlation id — so routes are registered
  by pattern and dispatched by method inside this package.

`north.AllCodes` is the code registry this phase starts. 0014 extends it; nothing
may reuse a code for a different condition.

### The CA: what SSH enforces, and what issuance enforces

Said plainly because the comfortable documentation is the wrong one: **a
certificate has no hostname field.** It is scoped by its principals, its validity
window and its critical options. So:

- **target scoping is enforced at issuance** — this server mints only for a target
  the decision path authorised, for one session — and *recorded* in
  `ssh_certificates.target`, which is what makes the scope auditable;
- the certificate carries the tenant, subject, target and session in its `key_id`,
  so the **target's own** auth log names the person rather than a shared account;
- `source-address` is set when the caller knows the proxy's address;
- exactly **one extension**, `permit-pty`. Agent forwarding, port forwarding, X11
  and user-rc are each a way for a session to become something other than a
  session. `force-command` is deliberately absent: what a session may run is the
  filter policy's job (§5.2), and encoding it here would put one of the two
  answers somewhere policy cannot change it.

**Custody.** `ext.PointKeyStore` is `WhenAbsentCore`, so `extdefault.SoftwareKeyStore`
is *not* registered in the registry — it is what the CA uses when nothing is. It
keeps material in `software_keys` (its own table, so an HSM-backed store writes no
row at all) as AES-256-GCM ciphertext under a key-encryption key from
`credential.key_encryption_key_env`. **It refuses to exist without one**, and
there is no option to disable encryption, because that is an option somebody finds
in a hurry during an incident. The wrong key is an outage whose message names the
likeliest cause.

**A deployment with no key-encryption key still boots.** `north.Options.CA` may be
nil and the CA routes answer `503 ca_not_configured` naming the config key. The
alternative — refusing to bind the listener — makes the console unreachable to the
person who needs it to fix the configuration.

**Rotation, both halves.** `RotateRequest.Compromise` is the field the whole story
hangs on. Routine: the retired key stays in the trust bundle for
`rotation_overlap`, which defaults to **twice** `max_certificate_validity`, so
nothing it signed can outlive the trust in it; nothing is revoked and no session
drops. Compromise: it leaves the bundle at the rotation instant and every
outstanding certificate it signed is revoked. Both paths render the **whole**
`TrustedUserCAKeys` file, because a client assembling it itself is a client that
will one day publish only the active key and break every session signed a minute
earlier.

`Verify` exists because the CA's promises are worth what something checks: an
expired certificate, a revoked one, a wrong principal and **one signed by another
tenant's CA** are all answered by the same function, so they are one kind of answer
rather than four beliefs. Note that `ssh.CertChecker` defaults to the real clock
and no authority — both are overridden, or a test could only assert expiry by
sleeping.

### CROSS-REPO DEPENDENCY: the exact contract change `hoplock/proxy` needs

**Nothing brokered can reach a proxy until this lands.** `internal/credential/seam.go`
carries the shape, refuses to put it on the wire
(`ErrMethodNotInContract`, outage-class — an allowed decision that cannot be served
is a `5xx` and an `unserved` record, never a 401), and
`TestTheBrokeredCertificateMethodIsNotYetInTheContract` **fails the build on the
day the method appears in the vendored document**. That is the signal for the
syncing phase to come here and delete the refusal.

Two parts, and the second is the one that is easy to miss:

1. **A new `TargetAuth.method` value: `brokered-certificate`.** Params:
   `username` (**required**, as on every method the document defines — never
   defaulted to the identity's `login`), `certificate` (the `authorized_keys`-form
   certificate), `certificate_serial` (so the proxy names it in its own records and
   an operator matches a session to a row), `ca_public_keys` (optional, the trust
   bundle for a proxy that administers the target). It is **vocabulary**, so it
   bumps `policy_version` upstream — that is the mechanism that lets a fleet
   upgrade without an outage, and it is upstream's to operate.
2. **A south-bound issuance endpoint.** A certificate must be signed over a key
   **the proxy generated per session**, or a private key would have to travel — and
   `brokered-key`'s own wording ("no credential material travels on this API") says
   it must not. The authorize request has nowhere to carry a public key, so the flow
   has to be: proxy generates a key pair → asks Control to sign the public half →
   presents the certificate. Suggested shape:
   `POST /v1/credentials/certificate` with `{session_id, decision_id, public_key}`
   answering `{certificate, serial, valid_before, ca_public_keys}`; the ladder entry
   then carries `certificate_serial` and the proxy correlates. Control already has
   everything else: `credential.CA.Issue` takes exactly that public key today.

Until both land, a route that wants a brokered certificate must name
`ephemeral-user` or `brokered-key` instead. The CA is exercised by tests and by
`hoplock-control ca issue`, so it is proven rather than merely written.

### The MFA provider is a push provider, and not TOTP

TOTP was the obvious choice and it is wrong here. A code the user types has to
arrive on the credential channel, and on this contract that channel is the
password field — so a TOTP factor would be a password with a second meaning,
which is how "enter your password, then your password again" happens. The contract
is poll-shaped (the proxy relays and polls), so the factor must resolve
asynchronously: `PushMFA` calls an operator-configured MFA service, signing every
request HMAC-SHA256 over the exact body bytes. An unsigned "approve challenge X"
is a request anybody on the network can forge, so the provider **refuses to build**
without a shared secret. `identity.ScriptedMFA` is untouched and is still what CI
uses; an answer the build cannot read is an **outage**, because coercing it to
pending holds the session open forever and coercing it to refused denies somebody
who approved.

### Where the secrets are not

Three kinds pass through this phase and each is handled differently, which is the
only reason "no IdP client secret, private key or token in any log, error or
stored row" is true by construction rather than by review:

- an **IdP client secret** is never a row. `idp_connectors.config` names the
  environment variable (`client_secret_env`); there is no column it could be
  written to. Errors name the **variable**, never its value.
- a **north-bound credential** is stored as a SHA-256 digest, the shape 0007 used
  for `proxy_api_tokens`. It is printed once by `identity token-issue` and reaches
  no log and no audit record — an audit record of a credential is a credential in
  the audit store, which is the one place designed never to forget anything.
- a **CA private key** has to come back, so it is ciphertext (above).

### Operator commands, and why they are not `policyctl`

`cmd/policyctl` is 0014's and talks to the north-bound API. These stay on the
daemon because **three of them are the bootstrap**: a deployment with no
principals has nobody who can call an authenticated route, so `identity
token-issue`, `identity role-bind` and `identity mapping-put` cannot come from the
API without a chicken-and-egg problem. `ca issue` is a command rather than an
endpoint for a different reason: a certificate is minted on the decision path for
one session, and an API that minted one on request would be a way to get a
credential without a decision.

### Deviations from the plan

1. **The north-bound listener is bound by this phase, not 0014** (above). M2
   revised in place; §3 gained an `internal/httpapi/north` entry; §6 rewritten to
   state what is now true rather than what was intended.
2. **`cmd/policyctl` was not used** for the CA's CLI surface, which the prompt
   named. It does not exist yet (0014 builds it) and a bootstrap command that
   talks to the API cannot bootstrap. The commands live on `hoplock-control`
   beside `seed`, `publish` and `audit-verify`.
3. **Three new direct dependencies:** `golang.org/x/crypto` (anticipated by M13),
   `github.com/russellhaering/goxmldsig` and `github.com/beevik/etree` (the XML
   signature reasoning above). No dependency is on the decision path.

### Test notes

- Postgres-backed: `internal/credential`, `internal/httpapi/north`, the
  `federation_test.go` half of `internal/identity`, `internal/audit`. Export
  `HOPLOCK_TEST_DSN` (see `internal/store/README.md`); they skip without it,
  except in CI.
- Pure: `mapping_test.go`, `rbac_test.go`, `oidc_test.go`, `saml_test.go`,
  `push_test.go`. The test IdP (`testidp_test.go`) is a real one — it signs,
  publishes a JWKS, redeems a code, and can be told to sign the wrong thing, so
  the refusals are graded against the same machinery as the successes.
- Conformance: **36/36 against this server**, unchanged. This phase serves no
  contract endpoint.
- `golangci-lint` could not be run locally: the installed binary is built with Go
  1.25 and refuses a module whose `go` directive is 1.27 (the failure M13's floor
  policy and 0001's gotcha both describe). `go vet ./...` is clean; CI pins a
  linter that can read the floor.

### Follow-ups this phase did not do

- **The provisioning-mapping question** above — 0014's, when it exposes the
  mapping API.
- **Encrypted SAML assertions** — a real feature, not a narrowing.
- **A `/session/methods` enumeration surface.** The pre-authentication routes
  confirm that a tenant exists to anybody who asks, which a login page needs. A
  deployment that cares can put the console behind something; it is noted rather
  than solved.
- **Cross-tenant scope authoring** is CLI-only on purpose. The *mechanism* (a
  scope map) is here because the queries are here; governing who may grant it is
  Enterprise's E11.
