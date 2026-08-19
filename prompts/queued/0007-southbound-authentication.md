# 0007 — South-bound authentication & host keys

## Read first
- `docs/PROTOCOL.md` — session workflow, especially §3 (M11: `401` is a
  decision).
- `docs/PLAN.md` — especially §4 (endpoint obligations), §6 (MFA orchestration),
  §2 (M2, M5, M11).
- `docs/learnings/` — read summaries; open `0002` (contract types + the
  conformance assertions you must now pass), `0003` (identity tables).
- `contract/control.yaml` — `/v1/auth/cert`, `/v1/auth/password`,
  `/v1/auth/mfa/poll`, `/v1/hostkeys/report`.

## Objective
Serve the south-bound authentication endpoints for real: resolve a certificate
or a password to an identity with claims, own the MFA conversation end to end,
and record reported target host keys. This is the first phase where the
conformance suite from 0002 grades a real implementation.

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

## Out of scope
- `/v1/authorize` (0008), the event stream (0009), log ingest (0010).
- Real IdP federation (0011): identities come from the store (0003) for now, and
  the interface must be shaped so an IdP broker slots in behind it.

## Acceptance criteria
- The conformance suite's auth and host-key assertions pass against this server
  (`make conform`), and CI runs it.
- A test proves no north-bound route is reachable on the south-bound listener.
- A test proves a database failure on the auth path returns `5xx`, not `401`
  (inject the failure; this is M11's regression test and it is easy to lose).
- MFA: pending → authenticated, deny, expiry, unknown token, replay of a
  resolved challenge, and poll-rate enforcement each have a test.
- The password appears in no log line, error, or stored row — asserted against
  captured output.
- A changed host key for a known target is detected and surfaced.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0007-southbound-authentication-learnings.md`. Summary block MUST
give the listener/middleware layout, the `Deny`-vs-`error` mechanism that keeps
M11 honest, the identity-resolution interface 0011 will implement, the MFA
provider interface and its deterministic test implementation, and the host-key
storage shape.
