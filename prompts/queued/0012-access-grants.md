# 0012 — Access grants: time-boxed access, without the workflow

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially §2 (M10, M15), §5.1 (grants are an evaluation
  input).
- `docs/learnings/` — read summaries; open `0004` (`ext.GrantWorkflow`), `0005`
  (the grant input the engine already reads), `0008` (grants are assembled into
  the decision), `0003` (the grants table).

## Objective
Ship the open-source half of just-in-time access: **a grant is a first-class,
time-boxed policy input that an administrator can create, inspect, and revoke.**
The approval *workflow* around it — requests, notifications, N-of-M approvers,
on-call routing — is a Hoplock Enterprise extension (`ext.GrantWorkflow`).

The split is deliberate and it is not a crippled stub. A self-hosted operator
who can say "Alice gets prod for the next 30 minutes, starting now" through the
API or the console has real, useful JIT access. What Enterprise adds is the
*governance* around who may ask and who must agree.

The engineering point is that a grant is a **policy input**, never a bypass. A
special case that skipped the engine would be invisible to simulation and to
"explain why" (0013) — the two features that make the rest of the policy story
credible.

## In scope
- `internal/access`:
  - **Grant**: subject, scope (target or label selector, and what may be done
    with it), validity window, who created it, why, and its state.
  - Create, list, inspect, revoke — through the north-bound API (0013) with RBAC
    (0011): creating a grant is an administrative act and is itself audited.
  - **Expiry is enforced at evaluation time, not by a sweeper.** A grant is live
    only if its window contains the evaluation's time input. A sweeper may tidy
    rows, but if expiry depends on the sweeper having run, a stuck sweeper is
    standing production access. Test the clock, not the job.
  - Revoking a grant that backs a running session publishes `session_kill`
    through 0009 with a disclosable reason, and the user is told — access ending
    must never look like a crash.
- **The Enterprise seam**: `ext.GrantWorkflow` (0004). When no implementation is
  registered, grants are created directly by an authorised administrator. When
  one is, grant creation is routed through it and the grant records which path
  produced it. A decision record (0008) names the grant either way, so
  `explain` (0013) tells the same story for both.
- Integration: 0008 already reads live grants; make them real, and make sure a
  grant's contribution appears in the decision record.

## Out of scope
- Requests, approvals, notifiers, approver policy — Hoplock Enterprise.
- A UI beyond the API (0014 builds the console).

## Acceptance criteria
- Full lifecycle: create a grant → the **next authorize allows what it
  previously denied** → after the window closes it denies again, with **no
  sweeper having run**. That last clause is the point of the phase.
- Revocation of a live grant ends the session it backed, with a disclosable
  reason reaching the user.
- A decision made under a grant names it in the record and in `explain`.
- Creating and revoking a grant are audited with the actor.
- RBAC: a role without grant-creation permission is refused.
- With a fake `ext.GrantWorkflow` registered, creation routes through it and the
  resulting grant is indistinguishable to the engine — assert that the same
  authorize outcome and the same decision-record shape result from both paths.
  Enterprise depends on that equivalence.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0012-access-grants-learnings.md`. Summary block MUST give the
grant type and states, the evaluation-time expiry rule, how a grant appears in a
decision record, and the exact `ext.GrantWorkflow` contract Enterprise
implements — including what Control does with a grant the workflow rejects.
