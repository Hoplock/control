# control-PR#114 — inbound actions: the executor, a subject lock and a target quarantine

Queued by [control#114](https://github.com/Hoplock/control/pull/114), Control's
phase 0039 (decision M28, release `v0.26.0`). The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`. It answers
`Hoplock/enterprise#26`, item 4.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/114. Do not implement any queued prompt in this
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
section: for hoplock/enterprise — every obligation holds from Control
v0.26.0 (M23), never from main.
(1) Its 0007, "What Control does not do yet": rewrite it from what merged and
stop building around the gap. kill_session takes exactly one of SessionID or
SubjectID (Affected 1 = the kill Control published, never sessions seen to
close); lock_subject and unlock_subject take SubjectID; revoke_grant takes
GrantID; quarantine_target takes TargetID; a selector a kind does not take is
KindInvalid naming the field. lock_subject on a break-glass subject is
KindDenied with ReasonCode break_glass_subject (an operator's lock still
works). Error kinds: KindInvalid, KindNotFound (including an id that exists
only in another tenant), KindDenied, KindConflict (an ActionID reused for a
different action), KindUnavailable (the act was made and stands; safe to
retry under the same ActionID, which delivers it), KindInternal (a store
failure). ReasonText is shown to the holder verbatim: one line, no control
characters, at most 512 bytes, else KindInvalid on reason_text, never
truncated; when empty Control writes "Your session was ended as part of a
security response.", "Your access was suspended as part of a security
response." or "Your temporary access grant was revoked as part of a security
response." Zero Affected with a nil error is nothing to act on, with
ReasonCode already_locked, not_locked, already_quarantined, already_revoked or
expired. The lock and the quarantine are standing denials above the engine
(subject_locked, target_quarantined); an operator places and lifts each on
Control's routes and console (POST .../subjects/{subject}/lock and unlock,
POST .../targets/{target}/quarantine and release, under session:revoke). The
handler is the actor on every record (action-handler:<provider>, no subject),
and each record carries action_id, action_kind, external_ref, reason_code and
provider; a no-op is recorded as action.unchanged and a refusal as
action.refused. A Start error is logged at ERROR, Control serves, and GET
.../extensions shows the handler registered and not started (started,
start_error); an unreachable platform is not a Start error: return nil and
reconnect on your own goroutines. Turn its skipped end-to-end criteria into
real ones.
(2) Its lockout lifecycle: a lock stands until unlock_subject or an operator
lifts it on Control's routes and console; expiry is Enterprise's to drive by
executing unlock_subject, and a lock that expires inside Control would be a
new request.
(3) Its plan: §4's ActionHandler row and its misreadings, wherever E7 says
what an inbound action may do, and the permission an operator's lock and
quarantine sit under (session:revoke, held by grant-admin and admin) for its
0013's estate actions.
(4) Its pin: the pin is v0.26.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
