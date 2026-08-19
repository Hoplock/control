# 0012 — JIT access requests & approvals

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§2 (M10)**, §5.1 (grants are an evaluation
  input), §2 (M4 — a grant must be visible to simulation and explanation).
- `docs/learnings/` — read summaries; open `0004` (the grant input type the
  engine already reads), `0007` (grants are assembled into the decision),
  `0011` (the north-bound surface and roles), `0008` (publishing an event when
  access ends), `0003` (the grants table).

## Objective
Ship the feature a security buyer understands immediately: *a developer who
normally cannot reach production requests 30 minutes, an approver is notified,
approval grants access automatically, and the access disappears on its own.*

The engineering point is that a grant is a **policy input**, not a bypass. A
special case that skipped the engine would be invisible to simulation and to
"explain why" — the two features that make the rest of the policy story
credible.

## In scope

### The objects (`internal/access`)
- **Request**: who, what scope (target or label selector, and what they want to
  do with it), why, for how long, requested when.
- **Grant**: the approved result — subject, scope, validity window, the request
  and approvals that produced it, and its current state.
- **Approval**: who approved or rejected, when, and on what basis. Support
  **multi-approver** rules (N-of-M, or specific roles) since one approver is a
  policy choice, not an architecture.
- Who may approve what is **authored in policy** (0004's bundle), not hard-coded:
  the same document that says who may reach production says who may approve
  reaching it.

### Lifecycle
- Request → notify → approve/reject → active → expired (or revoked).
- **Expiry is enforced at evaluation time, not by a sweeper.** A grant is live
  only if its window contains the evaluation's time input. A sweeper may tidy
  rows, but if expiry depends on the sweeper having run, a stuck sweeper is
  standing production access. Test the clock, not the job.
- Revoking a grant while a session is running publishes `session_kill` through
  0008 with a disclosable reason — and the user is told, per the bastion's
  disclosure rule. Access ending must never look like a crash.
- Self-approval is refused unless policy explicitly allows it, and the refusal is
  audited.

### Notifiers
A `Notifier` interface with one job, and implementations for Slack, Teams, a
generic webhook, and email. Delivery is best-effort and **must never block or
fail an approval**: an approval that succeeded but could not be announced is
fine, an approval lost because Slack was down is not. Retries, and an audit
record of every notification attempt.

### Integration
- 0007 already reads live grants; make them real and make sure a grant's
  contribution is named in the decision record.
- North-bound routes on 0011's surface: create a request, list mine, list
  awaiting my approval, approve, reject, revoke.

## Out of scope
- A UI. Notifications carry a link; the API is the surface.
- Approval via a Slack button (the callback is a webhook consumer — note it as
  future work, and design the approve endpoint so it is possible).

## Acceptance criteria
- Full lifecycle test: request → notify → approve → the **next authorize now
  allows what it previously denied** → after expiry, it denies again, with **no
  sweeper having run**. That last clause is the point of the phase.
- N-of-M approval: the grant activates on the Nth approval and not before.
- Rejection, and revocation of a live grant, both work; revocation of a grant
  backing a running session publishes a `session_kill` with a disclosable
  reason.
- Self-approval is refused unless policy permits it, and is audited either way.
- A decision made under a grant **names that grant** in its record and in
  0011's `explain` output.
- Simulation (0011) over a period containing grants reproduces the grant-aware
  outcomes — grants are inputs, so replay must account for them.
- A notifier failure does not fail the approval, and the attempt is audited.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0012-jit-access-and-approvals-learnings.md`. Summary block MUST
give the request/grant/approval types and states, how approver rules are
authored in policy, the evaluation-time expiry rule, the `Notifier` interface and
its implementations, and how a grant appears in a decision record.
