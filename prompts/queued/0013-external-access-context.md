# 0013 — External access context: push, probe, and the provider seam

> New phase (privileged-access revision, PLAN §10). It comes after grants (0012)
> because a confirmed window **is** a grant and this phase must not build a
> second path into the decision (M10, M16). It comes before the north-bound API
> (0014) so that surface can expose provider registration, scopes, and the
> reason a grant exists, rather than being retrofitted for it.

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — **M16** (this phase implements it), M10 (grants are policy
  inputs and a confirmed window is one), M5 (the decision-path budget a probe
  spends from), M15 (why the default here must be a real product, not a stub),
  M11 (deny versus outage — a probe failure is not a `401`).
- `contract/` — the vendored proxy contract. The **grant context** fields the
  proxy carries are defined there, not here; this phase populates them.
- `docs/learnings/` — read summaries; open `0004` (the `ext` registry, its
  registration rules, and the default-implementation requirement), `0005` (how
  an input reaches the engine and what a decision record must be able to
  explain) and `0012` (the grant object, its scope, and its expiry).

## Objective
Let an external system open a time-boxed, target-scoped window into Hoplock —
by push, by probe, or by both — through a seam a customer can implement without
writing Go, and without a scanner vendor's software becoming an unaudited
access-granting API.

## Why this phase exists

Proxy D15 says the data plane must never learn that Qualys exists, and it is
right: a push that grants access and a probe that validates one are policy
inputs, and policy lives here. That leaves this repository owning the harder
half — the part where an integration is, by construction, software written by
someone else that asks Hoplock to open root access to a production host.

Enterprise E7 already calls an inbound SOAR action "an administrative surface
reachable by another vendor's software". Granting access is a **larger**
privilege than ending a session, and it therefore gets the same treatment and
more.

## In scope

### 1. The seam (`ext/`, extending 0004)

`AccessContextProvider`, with the two directions M16 names and an explicit
answer for each failure. At minimum it must express: *is there a live window for
this subject, this target, at this instant*, and *what is its reference and
expiry*. Design notes that are requirements:

- **A provider returns evidence, not a verdict.** It says "this scan is running,
  here is its id and window"; the engine decides what that permits. A provider
  that returns allow/deny has quietly become the policy engine, and simulation
  and explain (0014) lose the ability to say why.
- **Three outcomes, not two**: confirmed, not confirmed, and *could not
  determine* — the last is what M11 exists for, and collapsing it into "not
  confirmed" turns an outage into a denial that an operator will debug as a
  permissions problem.
- Registration follows 0004's rules unchanged: before start, immutable
  afterwards, double registration an error, and **visible in the north-bound
  API**. Multiple providers coexist (a customer may run a scanner integration
  and a ticketing one); the registry keys them by name and the scope binding
  below is per provider.

### 2. The push receiver

An authenticated endpoint on the north-bound surface (M2) that accepts a window
assertion. This is the security-critical component of the phase:

- **Bound to a pre-registered scope** per integration: which subjects it may
  grant to, which targets it may name, the maximum window it may open, and
  whether it may grant privileged access at all. A push outside its scope is
  rejected and **audited as an attempted privilege escalation**, not dropped
  quietly — the interesting security event in this whole design is a scanner
  integration asking for a host it has never scanned.
- **Authenticated as an administrative caller** (E7's standard), rate-limited,
  and every accepted push audited with the identity that made it.
- **Idempotent, replay-resistant, and skew-tolerant**: an assertion carries its
  own id and window; the same id twice is the same window, not two.
- A **server-side ceiling** on window length, applied after the integration's
  own request. An integration may ask for less; it can never ask for more.
- The result is a **grant** (M10, 0012) carrying origin `external`, the provider
  name, and the external reference — indistinguishable to the engine from an
  administrator's grant, exactly as E8 requires of approval workflows.

### 3. The probe path and its budget

A probe is a network call **on the decision path**, which M5 governs absolutely.
So:

- A **hard timeout inside the authorize budget**, not merely a timeout. Decide
  the budget split and write it down; a probe that can consume the whole budget
  has made every authorize call hostage to a third party's availability.
- **Cacheable by the provider's own TTL**, because the same scan is asked about
  once per connection and a scanner opens many.
- **Unreachable is configurable per policy, and privileged grants fail closed.**
  The default for anything the policy marks privileged is deny; the default
  elsewhere is configurable. Whatever a deployment chooses, the decision record
  says the probe did not answer and which way it fell.
- **Composition with push, per M16**: a push opens a pending window and a probe
  confirms it. A deployment may run either alone; the engine must express all
  three shapes without a special case.

### 4. The declarative HTTP provider (Control's default — M15)

Not a stub. A provider configured rather than coded, sufficient for most
scanners and ITSM systems: a probe URL, its authentication, a request template,
assertions over the response (a JSON path and an expected value or predicate), a
TTL, and a webhook mapping for the push direction. This is what a self-hosting
customer uses to integrate a system nobody has heard of, and it is the reason
`AccessContextProvider` is a seam rather than a hole.

Treat the configuration as **untrusted-ish input**: a template that can be
pointed at an internal address is an SSRF primitive with an administrator
holding the pen, so egress restrictions and a documented allow-list belong here
rather than in each deployment's head.

### 5. Explainability (feeding 0014)

A decision that depended on external context must be able to say so: which
provider, which reference, which window, whether it was pushed or probed, and
whether the probe answered. M4 makes every decision explainable; a decision that
says "a scanner said so" without saying which scan is not explained.

## Out of scope
- **Qualys and BMC Helix themselves.** Packaged vendor integrations are
  Enterprise's (M15, PLAN §11). If you find yourself writing a Qualys API
  client, stop.
- The north-bound HTTP surface for managing providers and scopes — 0014 builds
  it; define the internal API and the data model it will expose.
- A compiled plugin ABI. Providers are Go implementations registered by the host
  binary (0004's model) or the declarative provider above. Nothing loads at
  runtime.
- Notifications about windows opening — that is `Notifier`'s job and already
  exists.
- Any change to `contract/`. The grant-context fields are the proxy's
  (`CROSS-REPO-PROTOCOL.md` §3.2): if one is missing, that is upstream work with
  its own prompt, never a local edit to a vendored artifact.

## Acceptance criteria
- `ext.AccessContextProvider` is defined, documented, registered under 0004's
  rules, and visible in the registry listing.
- The declarative provider works end to end against a fake external system in
  tests: probe confirms, probe denies, probe times out, probe returns malformed
  data — and the last two are distinguishable in the decision record.
- A push creates a grant that the engine reads through the **normal** grant path.
  A test asserts the engine has no branch on grant origin: the same scenario with
  an administrator-created grant produces an identical decision.
- Scope enforcement: a push naming a target outside its registered scope is
  rejected, audited as an escalation attempt, and creates no grant. A push asking
  for a window longer than the ceiling is clamped, not rejected, and the clamp is
  recorded.
- Replay: the same assertion id twice yields one grant.
- Budget: a probe cannot exceed its share of the authorize budget, proven with a
  deliberately slow fake; the authorize call still answers within M5's timeout
  and classifies as an outage rather than a deny (M11).
- Privileged access with an unreachable probe is denied by default, and the
  decision record says why.
- Explain output names the provider, the reference, and the window.

## Cross-repo impact

Per `CROSS-REPO-PROTOCOL.md` §4, state per repository. Expect at least:
`hoplock/enterprise` gains a real `ext` point to implement, and its packaged
Qualys and BMC Helix integrations are written against this interface — so its
prompt for them must not be opened before this merges (§2).

Naming that obligation is not enough on its own. Because `hoplock/enterprise`
has obligations here, this PR's impact section MUST **end with a ready-to-run
sync kickoff for it, already filled in** — the "Downstream sync" block in
`docs/KICKOFF.md`, verbatim except for its blanks: this PR's URL, a
`<short-description>` branch suffix, and the obligations just stated. Repeat
that kickoff in your reply to the user, saying it needs a **fresh session with
`hoplock/enterprise` checked out** (`CROSS-REPO-PROTOCOL.md` §4, "Hand over a
runnable sync kickoff"). The kickoff does not make the sync yours to do: this PR
merges first and the sync runs afterwards, in its own session, downstream (§2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0013-external-access-context-learnings.md`. The summary block
MUST carry the `AccessContextProvider` signature verbatim, the three outcomes and
what each means, the scope-binding model, the probe budget split, the ceiling
rule, and the declarative provider's configuration shape — Enterprise's
integrations are written from that summary and from nothing else.
