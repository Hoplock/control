# 0007 — South-bound authentication & host keys

## Read first
- `docs/PROTOCOL.md` — session workflow, especially §3 (M11: `401` is a
  decision).
- `docs/PLAN.md` — especially §4 (endpoint obligations), §6 (MFA orchestration),
  §2 (M2, M5, M11).
- `docs/learnings/` — read summaries; open `0002` (contract types + the
  conformance assertions you must now pass), `0003` (identity tables).
- `contract/control.yaml` — `/v1/auth/cert`, `/v1/auth/password`,
  `/v1/auth/mfa/poll`, `/v1/hostkeys/report`, `/v1/capabilities/report`.
- In the **Hoplock Proxy repository**, `docs/PLAN.md` §6.1 ("Chain trust model")
  and `api/README.md` ("What a chained hop sends this API") — why a chained hop
  authenticates against this endpoint with the previous hop's key, and what it
  expects back.
- In the **Hoplock Proxy repository**, `api/README.md` §"Reusing a host-key
  decision (`cache` on `HostKeyReportResponse`)" and §"The v4→v4.1 revision" —
  what the proxy does with a hint on this endpoint's response, which is the only
  thing that makes the rules below rules rather than preferences.

## Objective
Serve the south-bound authentication endpoints for real: resolve a certificate
or a password to an identity with claims, own the MFA conversation end to end,
and record what the proxy reports back about a target — its host key, and (since
contract v4) the enforcement rungs it can take. Decide, per host key and since
contract 4.1, whether the proxy may stop re-reporting it. This is the first phase
where the conformance suite from 0002 grades a real implementation.

## In scope

### The south-bound listener (`internal/httpapi/south`)
- Its own listener, its own middleware chain, its own authentication: a bearer
  token identifying a **proxy**, with mTLS as a documented seam (M2). Nothing
  north-bound is routable from it — assert this in a test, because the day
  someone mounts an admin route on the wrong mux is not the day anyone notices.
- Request logging with correlation ids, timeouts, and body size limits.
- **M11 discipline in the error mapper**: exactly one path produces `401`, and it
  is a deliberate deny. Database failures, timeouts, and panics produce `5xx`
  with a correlation id. Make this structurally hard to get wrong (a typed
  `Deny` result versus an `error`), not a convention.

### Certificate authentication
Resolve the offered key/certificate to a subject and its claims and groups.
Validate what the contract says to validate — including certificate validity
windows and revocation, since proxy §6.4 notes that **certificate validation is
where revocation bites** and authentication is never cached.

#### A key belonging to one of the fleet's own proxies is a chain leg

Not every key offered here belongs to a user. When a session is chained, the
hop in front presents **its own** key — never the user's — with the user's
`login` in the same request. The proxy shipped this in its own phase 0008
(`Hoplock/proxy#6`, merged); its `docs/PLAN.md` D11 and §6.1 are the trust
model. So this endpoint has two callers, and telling them apart is this
server's job:

- If the offered key belongs to a **user**, resolve it as above.
- If it belongs to one of **the fleet's own proxies**, this is a **chain leg**.
  Answer `authenticated` with the identity of the `login` in the request —
  **established here, by this server**, exactly as it would be for that user's
  own client. Record which proxy's key authenticated the leg (the proxy's mock
  puts it on the identity as a `chain_hop_proxy_id` claim); it is audit data,
  and it is the one thing the leg adds.

Two rules make this safe, and neither is optional:

- **Never take an identity from the calling proxy.** The proxy relays a key and
  a login and asserts nothing — the same contract it has for a user's own client
  (`AuthenticateCertRequest` has no field it could assert one in, and that is
  deliberate). A compromised proxy must be able to offer only its own key and
  let this server decide what that key may reach.
- **A proxy key is not a wildcard.** Recognising the key says "this caller is a
  legitimate hop", nothing more. The `login` still has to resolve to a real
  identity here, and `/v1/authorize` still decides for that hop separately.

Recognise a fleet key from the enrolled proxy identities in the fleet registry
(0006, over the `proxies` table from 0003) — that registry is exactly the "which
keys are ours" question, and a second list of proxy keys maintained beside it
would drift. The proxy's `cmd/mock-control` models the same thing with its
`proxies[]` fixtures (`id` + `key_fingerprints`); read its
`handleAuthenticateCert` if the intended behaviour is unclear, because the
conformance suite grades against that behaviour.

**Without this, a chained hop cannot authenticate at all**: the second proxy
offers a key no user owns, gets a `401`, and multi-hop — which the fleet graph
in 0006 and the route computation in 0008 both assume works — is dead on
arrival.

### Password + MFA orchestration (PLAN §6)
The proxy only relays and polls; the whole conversation is yours.
- `/v1/auth/password` → `authenticated`, or `mfa_required` with a challenge
  carrying the poll interval and expiry.
- `/v1/auth/mfa/poll` → still pending, authenticated, or `401` for denied,
  expired, or unknown.
- Own: challenge lifetime, poll-rate enforcement, single-use semantics (a
  resolved challenge cannot be replayed), and expiry as a **deny**.
- Provide a deterministic provider for tests and CI — the proxy's mock models
  MFA with a "pending polls" counter and the conformance suite depends on that
  determinism being reproducible here. A real out-of-band provider is 0011.
- **Never log, store, or echo the password.** Assert it in a test against your
  actual log output, not by inspection.

### Host key reporting
Record a reported target host key and return the trust decision. Prototype
policy is trust-on-first-use with a record (proxy D7); return an explicit
decision every time so a stricter per-target policy later needs no proxy
change. Store first-seen keys and detect a **changed** key for a known target —
that is a security event worth an audit record (its ingest lands in 0010; emit
through whatever logging exists now with a stable shape).

#### The response may carry a `cache` hint (contract 4.1)

Added by `Hoplock/proxy#35` (merged). `HostKeyReportResponse` may now carry the
same `CacheHint` object `/v1/authorize` already answers with, and a proxy that
receives one stops reporting that key on every connection: upstream measured
this endpoint at **46% of the Control calls that survive an authorize cache
hit**, because a proxy reconnecting to a target it has seen ten thousand times
reported the same key ten thousand times. `policy_version` stays `4` — the field
is on this endpoint, not on authorize, and it grants rather than restricts.

**Absent means what every server does today**, so issuing no hint at all is a
correct implementation of this phase and a fine place to start. What is not fine
is issuing one without the four rules below, each of which is a property of the
proxy's behaviour rather than a preference of ours:

- **Issue it under 0008's discipline, not a looser one** (PLAN §5.4). The
  lifetime is this server's to set, and **never issue a hint to a proxy whose
  event stream is unhealthy** (M9) — a decision that cannot be withdrawn is a
  grant with no revocation, and that rule was never authorize-only. This
  endpoint is the second place it has to be enforced, which means the liveness
  read from 0006/0009 is on this path too.
- **What the proxy reuses is narrower than a hint looks.** It keys the reuse on
  `target`, `target_port` **and `host_key.fingerprint`**, and on nothing wider.
  So what is reused is the answer to "may this target, presenting *this* key, be
  reached": a target presenting a different key is a different lookup, misses,
  and arrives here on the first connection that sees it. That is what keeps the
  changed-key detection above honest under reuse — the man-in-the-middle, the
  rotated key and the rebuilt host are all still reported (proxy D7) — and it is
  not a shape this server can widen by hinting more broadly.
- **Never hint a `reject` or a `known: false`.** The proxy refuses to reuse
  either one however it is hinted: a rejected host key is a security event it
  must keep reporting, and reusing a first sighting would replay "trusted on
  first use" into the audit log for every later connection. A hint on either is
  therefore not a widening but dead weight, and issuing one says this server has
  not read the rule. Hint only a key already ruled on and accepted.
- **Store the key you issue, because subject-scoped invalidation cannot reach
  this decision.** `cache_invalidate` with a `subject` does not match a host-key
  entry — a host-key decision is not made for a person — so withdrawing one
  means publishing that decision's own `key`, or `resync`. Keep the key on the
  host-key record rather than generating and forgetting it; 0009 publishes it,
  but a key nobody stored is a decision nobody can withdraw short of resyncing
  the entire fleet's cache.

### Capability reporting (`POST /v1/capabilities/report`, contract v4)

Added by `Hoplock/proxy#25` (merged) and served here because it is the sibling of
host-key reporting: the same shape, the same south-bound listener, the same
"the proxy tells this server what it found" pattern. It arrives after the proxy
has logged into a target and probed it, and it carries the **enforcement rungs
that target can take** — which `/v1/authorize` cannot ask for, because authorize
happens before the proxy has ever touched the target, so a first-ever connection
has nothing to put on the request.

- Persist through the capability store **0006 defines**; this phase serves the
  endpoint and does not invent a second home for the data. Key by target, and by
  the reported `platform` where one is given.
- Answer `accepted` truthfully. A report the server did not record is one the
  proxy must not believe it made, so `accepted: true` on a write that did not land
  is a contract violation — the same "the ack means it is stored" discipline as
  the priority log path (PLAN §4).
- Answer `report_after_seconds` from the freshness rule 0006 sets. The server owns
  the interval; a proxy may re-observe sooner, never later.
- **Record it as an observation, never as a grant.** Nothing here may widen a
  decision. The authority for a rung is the authorize response, and the proxy
  re-checks it against the live target at provisioning time — which is what makes
  a stale record cost at worst a refused session rather than a session running
  below the rung its audit record claims.

## Out of scope
- `/v1/authorize` (0008), the event stream (0009), log ingest (0010).
- Real IdP federation (0011): identities come from the store (0003) for now, and
  the interface must be shaped so an IdP broker slots in behind it.

## Acceptance criteria
- The conformance suite's auth, host-key and capability-report assertions pass
  against this server (`make conform`), and CI runs it.
- A test proves no north-bound route is reachable on the south-bound listener.
- A test proves a database failure on the auth path returns `5xx`, not `401`
  (inject the failure; this is M11's regression test and it is easy to lose).
- MFA: pending → authenticated, deny, expiry, unknown token, replay of a
  resolved challenge, and poll-rate enforcement each have a test.
- The password appears in no log line, error, or stored row — asserted against
  captured output.
- A changed host key for a known target is detected and surfaced — including
  when the previous key's decision was hinted as cacheable, since the proxy
  keys reuse on the fingerprint and a new key misses.
- **Host-key cache hint** (if this phase issues one at all): a hint is issued
  only for an already-known, accepted key, and never on a `reject` or a
  `known: false` response; no hint is issued to a proxy whose event stream is
  unhealthy; and the issued key is stored on the host-key record, proven by
  reading it back and withdrawing the decision by that key. If the phase issues
  no hint, say so in the learnings and assert the response carries none —
  "we did not get to it" and "we decided not to" read identically in a diff.
- **Capability report**: a report is stored and readable through 0006's store,
  and `accepted` is `true` only when the write landed — inject a store failure and
  assert the response is not a cheerful `accepted: true`. A second report for the
  same target replaces the first rather than accumulating duplicates, and a
  report carrying no `observed_at` is stored in a way that reads back as stale.
- **Chain leg**: a cert-auth call offering an enrolled proxy's key with a valid
  `login` returns that **user's** identity, carrying the authenticating proxy's
  id; the same call with a `login` that resolves to no identity is still a
  `401`; and an unknown key that belongs to neither a user nor a fleet proxy is
  a `401`. Assert the returned subject is the user's, not the proxy's — a test
  that only checks for `200` passes on precisely the bug this exists to
  prevent.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0007-southbound-authentication-learnings.md`. Summary block MUST
give the listener/middleware layout, the `Deny`-vs-`error` mechanism that keeps
M11 honest, the identity-resolution interface 0011 will implement, the MFA
provider interface and its deterministic test implementation, the host-key
storage shape **including whether a `cache` hint is issued and where its key is
stored** (0009 needs that key to withdraw a host-key decision, and a
subject-scoped invalidation will not do it), and **how a chain leg is
recognised** — which registry answers "is this key one of ours" and how the
authenticating proxy's id is carried on the identity, since 0008 pairs it with
`conn.hop_trail` and 0017 proves the pair end to end.
