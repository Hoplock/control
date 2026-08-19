# 0011 — North-bound API & policy lifecycle

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§2 (M2, M3, M4)**, §5 (the bundle and the
  explanation), §7 (audit query).
- `docs/learnings/` — read summaries; open `0004` (bundle, compiler errors,
  explanation type), `0007` (decision records), `0009` (audit query layer),
  `0008` (publishing an operator event), `0006` (listener conventions).

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
- Never routable from the south-bound listener — the mirror of 0006's test.

### Policy lifecycle
- **Upload → validate → diff → activate**, with bundles immutable and versioned
  (0003). Activation names the version; rollback is activating an older one.
- **Validation returns the compiler's errors verbatim** (0004): the rule, the
  line, and what to do instead. This is a product surface, so test its text.
- **Simulation** — the feature that makes a policy change reviewable instead of
  a leap:
  - *dry-run*: evaluate a candidate bundle against synthetic inputs;
  - *replay*: evaluate it against **recorded decision inputs** from a time range
    (0007's records) and report what would change — every decision that flips
    allow→deny or deny→allow, grouped so a human can actually read it.
  - Replay must be pure and total: the engine takes time as an input precisely
    so this works (0004). If it is not total, say why.
- **GitOps**: a bundle can be applied from CI with an API token, and the
  response is machine-readable enough to gate a pull request.

### Explain a decision (M4)
Given a `decision_id` or a session id, return the whole story: the inputs, the
matched rule, the mapping version that produced the attributes (0010), the
obligations, the snapshot, and — if a grant was involved — which one and who
approved it.

This is the other half of the bastion's disclosure rule: the user is told
"access denied" and a session id, deliberately vague so the bastion is not an
oracle for probing the estate, and an operator resolves that id here into
everything. Vague to the user, total to the auditor — and the pair only works if
this endpoint is genuinely total. Make an unexplainable decision impossible to
represent, or make it loud.

### Audit query & operator actions
- Query over 0009's store, including the showcase join (blocked commands on
  `env=prod`, with the access that permitted them).
- Operator actions: kill a session, kill everything for a subject, invalidate
  cached decisions (publishing through 0008), and enroll/approve a bastion
  (0005). Each requires the right role and each is audited.

### `cmd/policyctl`
The same operations from a terminal: `validate`, `diff`, `simulate`, `apply`,
`explain`. It talks to the north-bound API — never to the database directly, or
it becomes a second implementation of the rules.

## Out of scope
- A web UI (a consumer of this API, and a separate project).
- JIT requests and approvals (0012), though `explain` must be ready to name a
  grant.
- SIEM export (0013).

## Acceptance criteria
- Role enforcement is tested per route, including an auditor token being refused
  a policy activation.
- A north-bound route is not reachable on the south-bound listener, and vice
  versa.
- Upload → validate (with a deliberately broken bundle, asserting the error text
  names the rule and the line) → activate → rollback, end to end.
- Simulation replay over seeded decision records reports the exact set of flipped
  decisions for a candidate bundle — assert the set, not just the count.
- `explain` returns a complete story for an allow, for a deny, and for a
  decision made under a mapping version that has since changed.
- Every mutating action appears in the audit store with the actor.
- `policyctl` covers each operation and its output is stable enough to script.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0011-northbound-api-and-policy-lifecycle-learnings.md`. Summary
block MUST give the route table with required roles, the bundle lifecycle states,
the simulation API and its purity requirements, the `explain` response shape, and
the `policyctl` command set. Phase 0012 adds routes to this surface and phase
0014 drives it end to end.
