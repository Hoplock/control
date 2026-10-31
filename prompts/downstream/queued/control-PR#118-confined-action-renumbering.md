# control-PR#118 — the confined-action renumbering

Queued by [control#118](https://github.com/Hoplock/control/pull/118), which
answered enterprise#50's request for a confined inbound action by queueing
Control's phase 0041, confined inbound actions. It renumbered Control's queued
phases after it. The obligations are its `## Cross-repo impact` section's, for
`hoplock/enterprise`. The sync owed for 0041's own change is not this one:
0041 queues that when it merges.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/118. Do not implement any queued prompt in this
session.

Before anything else, confirm that upstream change is MERGED; if it is not,
stop and say so (§2). Then update this repository's prompts, plan, and
protocol so the next session builds against what is now true.

A sync changes text, not behaviour: it implements, enforces, and vendors
nothing, hand-edits no vendored artifact, and adds, renames, or renumbers no
prompt. Land each obligation in the prompt that will implement it, not only in
the plan. If the work seems to need something the upstream repository does not
have, that is §3.2 — never approximate it, and do not stop at telling me: name
the exact shape and queue the "Upstream request" kickoff from docs/KICKOFF.md,
filled in, in this repository's prompts/upstream/queued/ (§4.3).

The obligations to land are in the upstream PR's "## Cross-repo impact"
section: for hoplock/enterprise —
(1) Control's queued phases moved (revision confined-action): registered
reports served is now 0042, north-bound answer names 0043, instance identity
and supervisory registration 0044, the management console 0045, E2E topology
0046, CA custody through the key store 0047, post-quantum posture 0048 and one
contract version 0049. Where this repository cites them — reports in
docs/PLAN.md §4 and its 0009; answer names in docs/PLAN.md, docs/PROTOCOL.md
and its 0009, 0011, 0012, 0013, 0015 and 0016, seven of those by the path
prompts/queued/0042-north-bound-answer-names.md, which is now
prompts/queued/0043-north-bound-answer-names.md; instance identity in
docs/PLAN.md, docs/PROTOCOL.md and its 0011, 0012, 0013 and 0016; the console
in docs/PLAN.md and its 0007, 0008 and 0016; E2E in its 0017; CA custody in
docs/PLAN.md and its 0010 and 0017; and any others a sync since added — cite
each by name beside the number Control's docs/PLAN.md §10 mapping table gives
when you run. Citations of merged Control phases do not move.
(2) The request enterprise-PR#50-confined-inbound-action.md is answered by a
Control phase that is queued, not merged: 0041, confined inbound actions.
Where prompts/queued/0007's "What Control does not do yet", docs/PLAN.md §4's
ActionHandler bullet and E7 point at
prompts/upstream/queued/enterprise-PR#50-confined-inbound-action.md, point at
the file where it is when you run (it moves to prompts/upstream/implemented/
once control#118 has merged), and name Control's 0041 as queued. Say in 0007
what 0041 will build, including what the request left to Control or did not
ask for: ext.Action gains Within *ActionScope and IfPlacedBy; ActionScope has
SubjectIDs, TargetIDs (added by Control, so a credential scoped by a list of
target ids can be offered a kill by session and a grant revocation) and
TargetLabels; Control resolves what each kind reaches from its own data inside
the action's transaction (a session's subjects and targets from its decision
records, every hop included; a grant's subject and selector); a dimension a
kind reaches whole fails a scope that confines it, so a target scope refuses
lock_subject, unlock_subject and a kill by subject, and a subject scope
refuses quarantine_target; a grant's selector is inside a target scope only
when it cannot reach outside it (ScopeLabels[k] == v, or every listed target
carries k=v or is in TargetIDs), and a selector of zones alone or an empty one
is outside every target scope; outside is KindDenied with ReasonCode
out_of_scope, recorded as action.refused, decided before KindNotFound, with an
error that names the scope field and never an id; a repeat of an ActionID is
checked against its own scope before the first result is returned or
anything is sent again; a non-nil scope that confines nothing is KindInvalid
on within, with bounds of 1 000 SubjectIDs, 1 000 TargetIDs and 64
TargetLabels; IfPlacedBy is for unlock_subject only (else KindInvalid on
if_placed_by), lifts a lock only if both its action_id and its placing
principal (action-handler:<provider>) match, and otherwise leaves it standing
with Affected 0, not_this_lock and a nil error, recorded as action.unchanged;
IfPlacedBy is part of an action's identity, so a changed one under the same
ActionID is KindConflict; no method is added to ext.ActionExecutor. Do not
describe any of it as existing (§2): 0007 keeps refusing a label scope,
keeps a kill by SessionID and revoke_grant to a whole-tenant credential, keeps
its expiry behind the seam executing the plain unlock_subject, and keeps both
criteria skipped. Rewriting 0007 from what merges is the sync 0041 owes when
it merges.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
