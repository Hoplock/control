# 0008 — South-bound authorize & route

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§5 (the whole section)**, §2 (M4, M5, M11), §4.
- `docs/learnings/` — read summaries; open `0005` (the engine's signature and
  output vocabulary), `0006` (`Path` and the no-path outcome), `0007` (the
  listener and the `Deny`-vs-`error` mechanism), `0003` (decision table).
- `contract/control.yaml` — `/v1/authorize` and every schema it references,
  including `ConnMeta.hop_trail`, `EnforcementPolicy`, `Attestation`,
  `GrantContext` and `ConcurrencyLimits`. In the **Hoplock Proxy repository**,
  `api/README.md` "The policy vocabulary" is the same material as prose,
  including the table of which rung is applied and which attested. Read
  **"Versioning: one live vocabulary, and a proxy that fails closed"** there too,
  for the **tightening** rule: `params.username` is required on every credential
  method, with no version gate behind it, and enforcing it is this phase's job
  (below). Note that `api/README.md` carries **no revision history** — upstream
  `Hoplock/proxy#53` (merged) removed it, so everything reads in the present
  tense and there is no "v4.1→v4.2" section to look for.
- In the **Hoplock Proxy repository**, `docs/PLAN.md` §6.1 ("Hop trail, loops,
  and the cap") — what the proxy does with the trail on its side, and why every
  entry in it can only cause a refusal.

## Objective
Serve the endpoint the whole system turns on. One call assembles the inputs,
evaluates policy, computes a route, writes an explanation, and returns a
**whole-connection snapshot** — while a user's SSH handshake is held open,
on every connection, per hop.

## In scope

### The composition root (`internal/decision`)
Assemble inputs in one place: identity and claims (0007), target and its labels
(0003), live grants (0012 owns them; the approval *workflow* is a Hoplock
Enterprise extension), connection metadata from
the request — **including `conn.hop_trail`, see below** — the current time, and
the fleet path (0006). Evaluate (0005). Build the snapshot. Persist the decision
record. Return.

### `conn.hop_trail` — the server's only view of a chain

`ConnMeta.hop_trail` has been in the contract since 0002; the proxy started
sending it with its own phase 0008 (`Hoplock/proxy#6`, merged). **Read it.** It
is the proxy ids the session has already travelled through, oldest first, and it
is empty on the user's first hop.

It is the *only* thing that tells this server it is being asked about the second
leg of a chain rather than a fresh connection. Every hop of a chain calls this
endpoint for itself, with the same identity and the same final target (proxy
D2), so without the trail two calls that mean entirely different things are
byte-identical apart from `conn.proxy_id`. Three consequences, and each is a
thing to build rather than a thing to note:

- **Route from where the caller actually is.** The path (0006) starts at
  `conn.proxy_id` — the proxy *asking* — and not at the user's entry proxy,
  which on a chained call is the first id in the trail and is somewhere else
  entirely. Take it from the request; never assume the two are the same. The
  same login and target legitimately answer `nexthop` at the edge and `direct`
  at the proxy behind it; the proxy's mock models exactly this by matching its
  routes on `proxy_id` too.
- **Refuse a loop, and refuse it as an outage.** If `conn.proxy_id` or the
  `next_proxy_id` you are about to answer with already appears in the trail, the
  fleet graph has produced a cycle. That is a fault in the estate's routing, not
  a decision about the user: `5xx` (M11), never `401`. The proxy enforces this
  too, as a safety net — M6's "this server should not give a bad answer" applies
  here, and 0006 already asks you to enforce the maximum path length on this
  side for the same reason.
- **Length is the hop count so far.** `len(hop_trail)` against the cap in force
  is how this server keeps its own answers inside `max_hops` instead of relying
  on the proxy to notice.

**The trail carries no authority, and must never be given any.** Every entry in
it can only cause a refusal, which is what makes it safe to accept from a
caller: a forged trail restricts the forger. So it may narrow a decision and it
may never widen one — never grant on the strength of a hop id in the trail, and
never let a shorter trail unlock something a longer one would not. The authority
on a chain leg is the previous hop's key, which 0007 authenticates.

Put the trail in the decision record (M4) as an input. "Which hop asked, and
what had it already been through" is the first question anyone debugging a
chained session asks, and it is unrecoverable afterwards if it was not stored.

### Snapshot assembly
Translate the engine's output into the contract's response, complete: route type
and target, permitted channels, in-channel requests, forwarding destinations,
global requests, filter policy (rule list **or** restricted exec, never both),
target credential method and parameters, hop metadata **including connection
direction**, `decision_id`, and — only when policy says so — a cache hint.

Anything the engine can express and the contract cannot carry is a **contract
gap**: stop and report it (PROTOCOL §3). Do not approximate it, and do not edit
`contract/`.

### Vocabulary negotiation (`policy_version`, PLAN §4)
The request carries `policy_version`: the highest policy vocabulary the calling
proxy implements. **It is REQUIRED and has no absent-value default** (upstream
`Hoplock/proxy#53`, merged — it previously defaulted to `1`), so there are two
distinct cases and they get two distinct answers:

- **Declared.** Answer within it, per the rest of this section.
- **Absent.** Refuse the request with `400 invalid_request`. Do **not** fall back
  to `1`, or to any other number. A proxy that cannot say what it is able to read
  is not one this contract knows how to answer safely, and guessing a version for
  it is guessing which restrictions it would silently drop — which is exactly the
  failure the rest of this section exists to prevent. Never `401`: a deny is a
  decision about a user (M11) and this is a malformed request.

For a declared version: **answer within it.** The proxy decodes this response
strictly and fails the session closed on a field it does not understand, so a
field sent outside the declared version is not ignored — it takes the session
down, as an outage rather than a deny.

That cuts both ways and both halves need building:
- **Never emit a field the declared version does not include.** Assembly is
  version-aware, in one place, not sprinkled through the mapping.
- **When policy needs a field the proxy cannot read, that is an outage, not a
  quiet downgrade.** Return `5xx` (M11) naming the version mismatch. Silently
  dropping the restriction and allowing the session is the one behaviour this
  rule exists to prevent: it turns a mid-upgrade fleet into a fleet enforcing
  less than its policy says, invisibly. Dropping a *permission* is merely wrong;
  dropping a *restriction* is a breach.

Hoplock Proxy's `cmd/mock-control` implements this and is the reference: read
its authorize handler if the intended behaviour is unclear.

The current vocabulary is **`4`**, exported upstream as `control.PolicyVersion`,
and it is the **only** one the contract now describes: upstream
`Hoplock/proxy#53` (merged) removed the superseded vocabularies and the whole
revision history from `api/control.yaml` and `api/README.md`. Read the vocabulary
out of the vendored document, in the present tense; there is no "since version N"
annotation on any field to look up, and no older generation to assemble for.

**That removed the older versions, not the versioning.** The mechanism above is
intact upstream and is intact here: `policy_version` is on the wire, it is read
on every authorize request, and this server still never answers with policy
fields introduced after the version the caller declared. Build it. A future
session reading "one vocabulary" as "nothing to negotiate" would delete the only
thing standing between a fleet mid-upgrade and a silently widened session.

**The `device_field.<name>` namespace does not fit this mechanism, and forcing
it in is the bug.** It is an open namespace, deliberately unenumerated: a name
inside it is not a new policy field, so it demands no bump and there is no
version to gate a device field on. An older proxy parses a route bearing device
fields exactly as it parses one without them. Version-aware assembly must leave
the namespace alone — do not invent a sub-version to compare against, and do not
withhold a device field from a proxy declaring the current version. Whether a
driver accepts a given name is a **capability** question (M17, 0006), not a
version one.

What replaces the version check is the **capability** check (M17, 0006). A driver
declares the field names it accepts, and a rung naming one it does not declare is
a **skipped rung** on the proxy, not a dropped field — which is exactly why the
addition is safe, and also why it is not free: the ladder gets shorter with no
error anywhere, and a one-rung ladder becomes a denial the operator never
authored. Emitting a device field the enforcing proxy cannot honour is therefore
the same class of mistake as naming a platform it has no driver for, and takes
the same answer: it is a decision that cannot be served, so catch it on the issue
path against the declared capabilities rather than shipping a rung that will be
skipped. Note the asymmetry against the rule above — an unreadable *field* is an
outage (`5xx`), an unhonourable *rung* is a shorter ladder — and keep the two
paths distinct in the code, because they are answered from different data.

### Cache hints (PLAN §5.4)
- Authored per rule, never global, never invented.
- The key selects the sharing scope and **must never be shared across
  identities**.
- **Never issue a hint to a proxy whose event stream is unhealthy** (M9): a
  cached allow that cannot be withdrawn is a grant with no revocation. This
  needs a liveness read from 0006/0009 on the issue path.
- **These rules are not authorize-only.** The same `CacheHint` also rides on
  `HostKeyReportResponse`, which 0007 serves — so if this phase lands the
  issue-path machinery (the liveness read, the key derivation, the TTL clamp),
  build it where 0007 can call it rather than inside the authorize handler.
  Two copies of "may I hint this proxy right now" is two places to get M9
  wrong.
- **Never put a monotonic floor on a cacheable response** (PLAN §4, §5.4). This
  is a rule about *what may ride on this response*, and the uid lease is the
  worked example: the non-reuse floor under
  an `ephemeral-user` account's uid is served by its own endpoint,
  `POST /v1/uids/lease` (0007), and is **deliberately not a field here**. The
  reason is this section's whole subject — a cached decision is replayed from
  whenever it was taken, and this server is unreachable exactly when it is most
  needed, so a floor carried on an authorize response is served **stale**, and a
  **stale floor is a lowered floor**. That is the uid reuse the mechanism
  exists to prevent, reintroduced by the cache.

  A lease is exempt only because it is **exclusive**: replaying it grants the
  same block to the same proxy, so replay is harmless rather than merely
  unlikely. Keep the general form of the test when the next such value appears,
  because it will look like an obvious field to add to the snapshot: **if a
  value is only correct when it is fresh, it cannot ride on anything this
  section governs** — not the authorize response, and not the host-key one
  either. Add an endpoint instead: never invent one here, and raise it as an
  **upstream request** rather than only saying so — name the endpoint and its
  method under `## Upstream request` in your PR, build the rest behind a seam
  named for what is missing, and hand the user the filled-in kickoff from
  `docs/KICKOFF.md` (`docs/CROSS-REPO-PROTOCOL.md` §3.2, §4.2).

### Decision records (M4)
Every evaluation — allow and deny alike — writes a record: inputs, matched rule,
obligations, snapshot, timestamp, keyed by `decision_id`. This is the other half
of the proxy's deliberately vague "access denied": the user gets a session id,
and an operator resolves it here into the whole story. A deny with no stored
explanation makes that promise false, so **the deny path writes a record too** —
it is the path that matters most and the easiest one to forget.

### The latency budget (M5)
- A hard server-side deadline: answer, never hang. A timeout the proxy
  classifies as an outage beats a slow answer that looks like one.
- No unbounded work: compiled policy from memory, indexed reads only, one
  transaction.
- Record the decision **without** putting a synchronous write on the critical
  path if you can avoid it — but if you make it asynchronous, say what happens
  when the writer is behind or fails, because "we allowed it but cannot say why"
  is a worse failure than a slightly slower allow. State the trade-off you chose
  in your learnings.
- A benchmark, and a documented p99 target under a realistic bundle and fleet.

### Snapshot fields added by the privileged-access and enforcement vocabulary

`docs/PLAN.md` §5.2 lists the full snapshot; these are the ones that did not
exist when this prompt was first written, and each has a rule attached:

- **An ordered `target_auth_ladder`** (proxy D14), not a single method. Authoring
  a ladder is a policy decision with real consequences — it states both a
  preference and what the deployment will accept instead — so a one-entry ladder
  must be as easy to express as a multi-entry one, and the engine must never
  synthesise a fallback the policy did not write.

  **`target_auth_ladder` is the only way to name a credential method.** The
  singular `target_auth` field is **gone** from `AuthorizeResponse` (upstream
  `Hoplock/proxy#53`, merged): there is no one-object shape beside the ladder any
  more, and no normalisation between the two. A response this server emits
  carrying `target_auth` is now an **unknown field**, which the proxy fails
  closed on — an outage in front of a user, not a deny. So emit a one-entry
  ladder where policy names exactly one method; never the bare object.

  `TargetAuth` — the object itself — is unchanged and is still the ladder's
  **entry type**, and it is still extensible (proxy D6a). Only the singular
  *field* went away.
- **Device platform and expiry posture** on `ephemeral-account` routes (proxy
  D13), constrained by the target's own attributes and by the proxy's declared
  capabilities (M17, 0006). Naming a platform the enforcing proxy has no driver
  for is a decision that cannot be served.
- **Additional device fields** on those same routes — the open
  `device_field.<name>` namespace (proxy phase 0016), carried through from the
  engine's snapshot as data this server does not interpret.
  `device_field.vdom` on a FortiGate scopes the provisioned administrator to one
  virtual domain; **absent, the administrator is global**, which is the strongest
  account the device has and is therefore never something to emit by accident or
  to drop on the way through. Emit the shape the contract states — name of
  lowercase letters, digits, hyphens and underscores and ≤64 characters, value
  non-empty and ≤256 characters, ≤16 per entry — and emit no name of your own
  invention: a field this server made up is one no driver declares.
- **A per-route algorithm profile** (`algorithm_profile`) where the target
  speaks something the proxy's SSH stack does not enable by default. It is a
  **named preset**, not an algorithm list, and the enum is exactly
  `default`, `legacy-rsa-sha1`, `legacy-device`:

  - `default` — nothing beyond the library defaults, and **absent ⇒ `default`**.
    It is the only value that is not a weakening, so emit the field only where
    policy genuinely names one of the other two.
  - `legacy-rsa-sha1` — additionally offers RSA with SHA-1 signatures
    (`ssh-rsa`) for host keys and public-key auth.
  - `legacy-device` — `legacy-rsa-sha1` plus the SHA-1 key exchanges, CBC
    ciphers and SHA-1 MACs that appliance firmware of that era offers.

  Two properties make this safe and it needs both, so neither is this server's
  to relax. It is **named by the server, per route** — never a proxy-wide knob,
  which would weaken every leg in the fleet to serve the oldest device on it.
  And a **preset cannot be widened one algorithm at a time** by someone who does
  not know what they are enabling. Emit no value outside the enum: the proxy
  refuses an unknown profile rather than coercing it, because coercing down to
  `default` would deny every route on the estate this exists for and coercing up
  would weaken a leg nobody asked to weaken.

  Anything other than `default` is a weakening and **emits its own audit event**
  (0010), on D14's sibling rule for credential methods: an operator learns that a
  route runs on SHA-1 from the record, not by reading policy.
- **A session deadline** (`session_deadline`), as an **absolute instant** —
  RFC 3339, not a duration. A duration re-anchors at each hop of a chained route
  and silently multiplies the window. The proxy enforces it locally, so it holds
  when the revocation stream is down, which is exactly when an immortal root
  session is least acceptable. Nothing else expresses it: a cache TTL bounds
  decision *reuse* and `ephemeral-user`'s lifetime bounds the *credential*, while
  an already-open session outlives both.
- **Concurrency caps** (`concurrency`) per subject and/or target
  (`max_sessions_per_subject`, `max_sessions_per_target`). This server cannot
  count live sessions — only the proxy holds the session registry — so it states
  the ceiling and the proxy enforces it. Absent or `0` is uncapped, and exceeding
  a cap is a **policy denial**, never an outage: the estate is healthy and the
  answer is "no".
- **Grant context** (`grant_context`): the system, the reference, and the window
  that justified this access, copied from the grant that supplied it (M10, M16),
  plus `additional_context` — which admits a JSON **string or a JSON object**,
  and nothing else. A number, a list or a boolean there is a contract violation,
  so emit one of the two shapes rather than coercing. The proxy carries all of it
  opaquely into its records and never parses it; this is what makes an audit trail
  able to answer "why was this allowed" without a human joining two systems by
  hand. `window_start`/`window_end` are **recorded, not enforced** — they look
  like a deadline and are not one, and the bound that is enforced is
  `session_deadline`, which this server sets having already weighed the window.
- **Required session capture** (`require_session_capture`) as an obligation the
  proxy refuses to serve without, on unbounded-privilege routes (proxy D16). The
  proxy checks it **before the target leg is dialled**, and buffering to local
  disk counts as recording, so the refusal is outage-class and fires only when the
  proxy has no recording path at all.

### `username` on every ladder entry

`TargetAuth.params.username` is **required on every method the contract
defines** — `ephemeral-user`, `ephemeral-account`, `static-key` and
`brokered-key` alike. The proxy refuses a route that omits it as a contract
violation at the **first authorize call**, so a snapshot this server assembles
without one is an outage in front of a user rather than a decision about them.

This is the contract's one **tightening**, and the contract announces it as a
break rather than gating it on a version — see the rule below.

Both halves are written here for the first time. v3's requirement reached the
contract and the proxy and was never mirrored into these prompts, so this is the
whole rule rather than an extension of one; treat it that way when you build it.

- **`brokered-key` is included, and the reasoning that once excluded it is
  dead.** The argument was that it logs into a **standing** account an operator
  already chose rather than one the proxy provisions, so the route need not name
  one. That left the proxy falling back to the identity's `login`: a deployment
  that configured no account locally logged in as whatever string the connecting
  user typed at their SSH client. The contract closed the difference. What the
  proxy will accept is the route's `username` or the operator's own local
  configuration, and a route offering neither is refused as an outage rather
  than served on a guess.
- **Never derive it from the identity's `login`.** It is a client-typed string,
  and the rule that this server must not base an authorization decision on one
  does not weaken when the string is used to *name* an OS or device account
  instead of to match against. The account name is what the target's own audit
  trail, its file ownership and — on a password credential — half the credential
  pair are made of: this server names it or there is no route.
- **There is no version to gate this on, and there cannot be.** A tightening
  adds no field and changes no field's meaning, so it is not expressible through
  `policy_version` at all: a proxy that was never told parses such a route
  exactly as it always did, and refuses a `username`-less one exactly as a
  current proxy does. The contract's "Versioning" section says so and announces
  the tightening as a **break** instead. Do not build a "proxies declaring some
  version may omit it" path. (0018 removes the multi-version question outright;
  this assembly must not grow one before it lands.)
- **Catch it before the response is written**, next to the applied-rung check
  below. A ladder entry with no `username` is a route that can only fail at
  connect time, and a policy that can only fail in front of a user has already
  failed. The compiler rejects it at authoring time (0005); this is the second
  net, for a snapshot assembled from anywhere else.

### The enforcement rung (`enforcement`)

The largest single thing this phase has to assemble. It says **where** a policy claim is enforced, on **two axes** — what
the session may execute, and what it may reach — which are separate questions
with separate mechanisms, so a route may stand on a different rung of each. The
vocabulary and its per-rung guarantees are in `contract/control.yaml`
(`EnforcementPolicy`) and PLAN §5.2; do not restate them from memory.

Five things to build, not to note:

- **Absent means proxy-side enforcement only** on both axes — `proxy-inspected`
  and `proxy-channel-policy`. That is the documented absent-value default, not a
  fallback this server picks. Emit the object
  only where the route genuinely stands somewhere else. An emitted default is
  noise in a record whose entire purpose is to say which rung was in force.
- **An applied rung must never be chosen for a brokered-key route.** An *applied*
  rung is one the proxy configures per session and tears down, which needs it to
  administer the account — only `ephemeral-user` and `ephemeral-account` do that.
  So a response naming an applied rung where **no** entry in the route's
  `target_auth_ladder` provisions the target is a contract violation the proxy
  refuses outright, and refusing it here is the point: a policy that can only fail
  at connect time fails in front of a user. An **attested** rung
  (`platform-attested`, either axis) on that same route is fine and is the whole
  reason the kind exists — it is how the appliance estate carries a real
  enforcement claim instead of "none available". An attested rung requires
  `attestation.asserted_by` and `attestation.reference`, both non-empty: the
  system verifies none of it, so what the contract asks for instead is
  attributability, and "trust us" and an empty string are the same answer.
- **The rung is a property of the route, not of a ladder entry.** An entry that
  cannot carry it is a *skipped rung* on the proxy (D14) and the proxy walks on;
  it never runs the session without the rung its record would claim. Do not emit
  a per-entry rung and do not synthesise a weaker one as a fallback — a silent
  downgrade is the failure this vocabulary exists to prevent.
- **The claim must agree with the rest of the snapshot**, and the proxy refuses a
  response that disagrees with itself, so check it here:
  `no-interactive-shell` needs `permitted_requests` present and denying both
  `shell` and `pty-req`; `account-restricted` and `account-confined` need
  `filter_policy.exec_mode: restricted`; `platform-authorized` needs
  `platform_role`; `account-egress-restricted` needs a non-empty
  `permitted_destinations`; `attestation` rides an attested rung and no other.
  Note that `permitted_destinations` reuses `ForwardDestination`'s *shape* and
  shares none of its meaning — `permitted_forwards` is a rule about SSH channels
  the proxy sees, this is a rule about sockets the target's kernel sees — so one
  must never be assembled from the other, and neither ever widens the other.
- **Constrain the choice by capability, on the issue path** (M17, 0006), from
  both sources: the proxy build's `AuthorizeRequest.capabilities` and the
  target's own reported capabilities. A record that is stale, undated or absent is
  **one case** and provides nothing that has to be *applied*, while leaving the
  two proxy-side defaults and an attested rung available — which is how an
  unprobeable appliance still gets a real claim. This is the same issue-path check
  the device-field paragraph above describes, against the same data.

### The latency budget is not a formality

M5 now carries a magnitude: an estate where machine-to-machine health checking
runs through the proxy can put this endpoint into five figures of requests per
second. Two things follow for this phase. Cache hints stop being an optimisation
and become load-shedding, so 5.4's "issued deliberately" needs a deliberate
answer for the machine-identity pattern — one subject against very many targets,
where a hint keyed per (subject, target) has a hit rate near zero. And the
measured latency numbers this phase reports should be measured under fan-out,
not only under a single hot subject.

## Out of scope
- The revocation stream (0009) — read its liveness, do not implement it.
- Ephemeral uid leases (`POST /v1/uids/lease`, 0007). The floor is not this
  response's to carry and the endpoint is not this phase's to serve; the rule
  above is here only so nothing puts it on the snapshot.
- The grant *approval workflow* (a Hoplock Enterprise extension via `ext`).
  Control ships manual, time-boxed grants (0012); read live grants as an input
  and do not care which of the two created them.
- Simulation and explain-a-decision APIs (0014) — write the records they read.

## Acceptance criteria
- The conformance suite's authorize assertions pass; `make conform` is green.
- End-to-end tests through the real listener and a real database: `direct`,
  `nexthop` (both connection directions), and `401`.
- **`conn.hop_trail` is read, not ignored:** one login and one target, asked
  twice — an empty trail from the edge proxy answers `nexthop`, and a trail
  naming that edge proxy, asked by the next proxy, answers `direct` for the same
  user and target. A test that only exercises the empty-trail call cannot tell a
  server that reads the trail from one that discards it.
- A trail that already contains the asking proxy, or the `next_proxy_id` about
  to be answered, returns a `5xx` outage — not a `401`, and not a route that
  closes the loop.
- A decision record for a chained call names the trail it was decided under.
- A deny writes a decision record naming the deciding rule — test it.
- **No path available** (an enclave relay down) returns a `5xx` outage, not a
  `401`. This is the M11 test that matters most in this phase, because "deny"
  is the tempting shortcut here.
- A route whose policy omits a cache hint returns none; one that sets it returns
  the server's TTL; and no key is ever shared across two subjects (test with two
  identities against one target).
- A proxy with an unhealthy event stream receives **no** cache hint even when
  policy grants one.
- **Vocabulary negotiation, both directions:** a request declaring the current
  version gets the full snapshot; a request declaring `1` (or omitting the
  field) against a policy that only needs the v1 vocabulary gets a valid v1
  response with none of the newer fields invented; and a request declaring `1`
  against a policy that **requires** a newer field gets a `5xx` naming the
  mismatch — never a thinned snapshot, and never a `401`.
- **The enforcement rung, four assertions.** A route that names neither axis
  emits no `enforcement` object at all (not one carrying the defaults). A policy
  naming an **applied** rung on a route whose every ladder entry is `brokered-key`
  is refused before a response is written — assert the refusal, not a thinned
  response. The same route with `platform-attested` is served, and carries
  `attestation.asserted_by` and `attestation.reference`. And a rung whose
  co-requisite is missing — `no-interactive-shell` beside a `permitted_requests`
  that still allows `shell` — is refused rather than emitted for the proxy to
  reject.
- **Capability constraint, both sources and the fail-safe direction.** A rung the
  proxy build does not declare is never emitted; a rung the *target* has not been
  reported able to take is never emitted; and a target whose capability record is
  **stale, undated, or absent** still gets the two proxy-side defaults and an
  attested rung. Test the three record states together — they are one case, and a
  suite that only covers "absent" will not notice an implementation that treats
  undated as fresh.
- **The session bounds are emitted as the contract shapes them:**
  `session_deadline` as an absolute RFC 3339 instant (assert it is not a
  duration, and that a chained call does not re-anchor it), `concurrency`
  omitted rather than `0` when uncapped, and `grant_context.additional_context`
  round-tripping as both a string and an object.
- A benchmark reports p50/p99 against the stated budget, with the bundle and
  fleet size documented.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0008-southbound-authorize-and-route-learnings.md`. Summary block
MUST give the input assembly order (including where `conn.hop_trail` enters it
and how loops and the hop cap are refused), the snapshot mapping (engine field →
contract field), the cache-hint issuance rules, the decision-record shape and
whether its write is synchronous, and the measured latency numbers. Phase 0014's simulation
and explain features read those records; phase 0017 asserts this end to end
against a real proxy.
