# 0014 — North-bound API & policy lifecycle

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§2 (M2, M3, M4, M17)**, §5 (the bundle and the
  explanation, including §5.2's enforcement rungs), §7 (audit query).
- `docs/learnings/` — read summaries; open `0005` (bundle, compiler errors,
  explanation type), `0006` (the capability query this surface exposes), `0008`
  (decision records), `0010` (audit query layer), `0009` (publishing an operator
  event), `0007` (listener conventions).

## Objective
Give humans and CI a surface. This is the phase where the product becomes
operable rather than merely correct: authoring with real validation, **policy
simulation**, **"explain why this was denied"**, audit query, and the operator
actions that were internal until now.

## In scope

### The north-bound listener (`internal/httpapi/north`)
- A **separate listener** with its own authentication: OIDC for humans, scoped
  API tokens for automation (M2). Roles at minimum: read-only auditor, policy
  author, approver, admin. A token's scope is enforced per route.
- Every mutating action is itself audited: who, what, when, and the before/after
  version. An audit system whose own administration is unaudited has a hole
  exactly where it matters.
- Never routable from the south-bound listener — the mirror of 0007's test.

### Policy lifecycle
- **Upload → validate → diff → activate**, with bundles immutable and versioned
  (0003). Activation names the version; rollback is activating an older one.
- **Validation returns the compiler's errors verbatim** (0005): the rule, the
  line, and what to do instead. This is a product surface, so test its text.
- **Simulation** — the feature that makes a policy change reviewable instead of
  a leap:
  - *dry-run*: evaluate a candidate bundle against synthetic inputs;
  - *replay*: evaluate it against **recorded decision inputs** from a time range
    (0008's records) and report what would change — every decision that flips
    allow→deny or deny→allow, grouped so a human can actually read it.
  - Replay must be pure and total: the engine takes time as an input precisely
    so this works (0005). If it is not total, say why.
- **GitOps**: a bundle can be applied from CI with an API token, and the
  response is machine-readable enough to gate a pull request.

### Can this policy actually be satisfied? (M17)

Validation that compiles is not validation that can be served. 0006 builds the
query; **this phase is where an operator sees the answer, before publishing
rather than after a user complains**. It spans both capability sources: the
proxies that would enforce the policy, and — since contract v4 — the targets it
would be enforced on.

- **A rung no proxy in the path can provide, or no target can take, is a policy
  that denies at connect time.** Show it at publish time: which rule, which
  route, which proxies or targets fall short, and what they do provide. The
  failure it prevents is specific and silent — a rung the enforcing proxy cannot
  honour is a *skipped rung*, so the ladder just gets shorter with no error
  anywhere, and on a one-rung ladder the session is denied and nobody authored the
  denial.
- **Warn, do not refuse, where the shortfall is capability rather than
  correctness.** A capability record that is stale, undated or absent is not proof
  a target cannot take a rung — it is the absence of proof, and it fails safe by
  providing nothing that must be *applied*. Blocking publication on it would make
  an unprobeable appliance unauthorable, which is the opposite of what attested
  rungs exist for. Internal contradictions are a different matter: those are
  compiler errors (0005) and reach this surface as errors.
- **An allow-list containing an interpreter is not an allow-list, and catching
  that is this server's job.** `find`, `awk`, `less`, `vi`, `tar`, `python` and
  most editors hand back a shell (GTFOBins), so a `restricted_exec` list naming
  one does not deliver the boundary an `account-restricted` or `account-confined`
  rung claims — its real guarantee drops to `no-interactive-shell` at best. The
  contract states this as **a documented rule enforced by Control at authoring
  time**, and deliberately *not* as a proxy-side refusal: a shipped deny-list of
  interpreter names in the data plane would be a blacklist masquerading as a
  boundary, incomplete the day it shipped and liable to refuse a route over a name
  collision.

  So this is a **warning the author must see and may override**, never a silent
  pass and never a hard refusal. Ship a starting list, make it configurable, say
  in the text *why* the named executable weakens the claim, and record that the
  author accepted it — the rung is a claim about a mechanism, bounded by the list
  it renders, and the policy author owns that trade-off. A check that cannot be
  overridden will be worked around; one that is never shown is not a check.

### Explain a decision (M4)
Given a `decision_id` or a session id, return the whole story: the inputs, the
matched rule, the mapping version that produced the attributes (0011), the
obligations, the snapshot, and — if a grant was involved — which one and who
approved it.

This is the other half of the proxy's disclosure rule: the user is told
"access denied" and a session id, deliberately vague so the proxy is not an
oracle for probing the estate, and an operator resolves that id here into
everything. Vague to the user, total to the auditor — and the pair only works if
this endpoint is genuinely total. Make an unexplainable decision impossible to
represent, or make it loud.

### Inventory
CRUD for the things an operator manages, all RBAC-gated and all audited:
targets and their labels (labels are policy inputs, so editing one changes
decisions — the audit record matters), identities and groups, roles, grants
(0012), and proxy enrollment/approval (0006). Label edits are the sharpest
edge here: a bulk relabel is a bulk policy change, so it is diffable and
appears in simulation like any other change.

### Audit query & operator actions
- Query over 0010's store, including the showcase join (blocked commands on
  `env=prod`, with the access that permitted them).
- Operator actions: kill a session, kill everything for a subject, invalidate
  cached decisions (publishing through 0009), and enroll/approve a proxy
  (0006). Each requires the right role and each is audited.

### `cmd/policyctl`
The same operations from a terminal: `validate`, `diff`, `simulate`, `apply`,
`explain`. It talks to the north-bound API — never to the database directly, or
it becomes a second implementation of the rules.

### Tenancy on the surface (M18)
Every north-bound route resolves exactly one tenant from the caller's scope
(0011) before it does anything else. Three rules keep that honest:

- **A tenant is a selector, never a widening.** The middleware refuses a tenant
  outside the principal's set before a handler runs.
- **Single-tenant deployments look untouched.** With one tenant, no route gains
  a required parameter and no response gains a field an operator must care
  about. Tenancy that is visible to someone who does not use it is a tax on the
  common case.
- **Cross-tenant reads do not exist on this surface.** A caller with scope in
  three tenants makes three requests. Aggregating them is a governance feature
  and it is Enterprise's (its E11); an aggregate route here would be the one
  place where a missing filter leaks everything at once.

## Out of scope
- The management console (0016). It is a **client** of this API and lives in
  this repository under `ui/` (PLAN §3) — not a separate project, and not a
  privileged path of its own. Every capability it has, this surface grants it,
  which is why it cannot be built before this phase exists.
- JIT requests and approvals (0012), though `explain` must be ready to name a
  grant.
- SIEM export (0014).

## Acceptance criteria
- Role enforcement is tested per route, including an auditor token being refused
  a policy activation.
- A north-bound route is not reachable on the south-bound listener, and vice
  versa.
- Upload → validate (with a deliberately broken bundle, asserting the error text
  names the rule and the line) → activate → rollback, end to end.
- Simulation replay over seeded decision records reports the exact set of flipped
  decisions for a candidate bundle — assert the set, not just the count.
- **Satisfiability, both directions.** A bundle naming an enforcement rung no
  enrolled proxy provides is reported at publish time, naming the rule and the
  proxies; a bundle naming a rung the target's capability record says it cannot
  take is reported the same way; and a bundle whose targets have **stale, undated
  or absent** records publishes with a warning rather than a refusal. Assert the
  last case explicitly — refusing it is the plausible-looking bug that makes every
  unprobeable appliance unauthorable.
- **The interpreter warning fires and is overridable.** A `restricted_exec`
  allow-list containing an interpreter, under an `account-restricted` or
  `account-confined` rung, produces a warning naming the executable and the rung's
  real guarantee; publication succeeds when the author accepts it, and the
  acceptance is audited like any other mutating action.
- `explain` returns a complete story for an allow, for a deny, and for a
  decision made under a mapping version that has since changed.
- Every mutating action appears in the audit store with the actor.
- `policyctl` covers each operation and its output is stable enough to script.
- Every route is exercised by a table test asserting tenant isolation, and the
  test enumerates routes from the router rather than from a hand-written list —
  a route added later without isolation must fail this test.
- With a single tenant configured, the API's shape and responses are identical
  to those a pre-M18 client expects.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0014-northbound-api-and-policy-lifecycle-learnings.md`. Summary
block MUST give the route table with required roles, the bundle lifecycle states,
the simulation API and its purity requirements, the `explain` response shape, and
the `policyctl` command set. Phase 0012 adds routes to this surface and phase
0017 drives it end to end.
