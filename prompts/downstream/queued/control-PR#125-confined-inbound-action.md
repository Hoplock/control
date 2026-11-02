# control-PR#125 — confined inbound actions

Queued by [control#125](https://github.com/Hoplock/control/pull/125), Control's
phase 0041, which answers enterprise#50's upstream request: `ext.Action.Within`
(`ext.ActionScope`) and `ext.Action.IfPlacedBy`, released as `v0.28.0`. The
obligations are its `## Cross-repo impact` section's, for `hoplock/enterprise`,
the repository that asked.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/125. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.28.0 and never from main —
(1) its 0007's "What Control does not do yet", rewritten from what merged: a
label scope is checked by Control through Within.TargetLabels and a target-id
scope through Within.TargetIDs rather than refused; a kill by SessionID and a
revoke_grant may be offered to a scoped credential; the expiry's seam executes
unlock_subject with IfPlacedBy set to the ActionID that placed the lock; its
two skipped criteria (per-label authorisation, an expiry that never lifts a
lock it did not place) become real ones; (2) what Control decided that it did
not ask for: ActionScope.TargetIDs; a subject scope refuses quarantine_target
(all_subjects) and a target scope refuses lock, unlock and kill by subject
(all_targets); a grant is inside a target scope only when its selector lists
hostnames each naming a held target (case-insensitive) the scope admits, or
for labels its own labels require the label, and a wildcard, a hostname with
no row, zones alone and an empty selector are outside; out_of_scope is checked
before KindNotFound, so a confined requester gets KindNotFound only for an id
its scope names; a repeat is checked against its own scope, and a changed
IfPlacedBy under a used ActionID is KindConflict; the bounds (at most 1 000
SubjectIDs, 1 000 TargetIDs, 64 TargetLabels of at most 256 bytes, a non-nil
empty scope KindInvalid on `within`); and IfPlacedBy matches the handler's
principal (action-handler:<provider>) as well as the action id, so an expiry
does not survive a change of provider name (not_this_lock; an operator lifts
it); (3) its plan: §4's ActionHandler bullet and E7, where they point at the
request; (4) its pin: v0.28.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
